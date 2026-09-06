package a2a

// registry_ops.go — field-scoped presence registry operations.
//
// Phase 03 CONTRACT.md requires replacing the old read-whole/mutate-field/
// write-whole call sites with a small set of explicit, atomically-scoped
// operations. Every operation below is built on the same foundation:
// WithPeerHandleTransaction acquires this package's process-local mutex AND
// the stable cross-process OS lock for exactly one handle's presence file,
// then hands the callback a *PeerHandleTransaction whose Get/Publish (and
// the package-private publishRaw) reuse that already-held lock. Because the
// lock fully serializes every call — of any op — against the same handle,
// a single operation's read-modify-write is always atomic relative to every
// other operation on that handle: there is no window in which two calls can
// interleave their reads and writes, so no concurrent combination of these
// operations can lose an update or produce a torn (partially written) file.
// writeFileAtomicLocked additionally guarantees every write itself is
// atomic (temp file + rename), so a reader never observes a partial write
// even absent the lock.
//
// Each operation reads the current record, mutates ONLY the fields it owns,
// and republishes the full (still-atomic) record. "Field-scoped" here means
// ownership discipline — never touching a field outside its stated set —
// not a partial on-disk patch format; the presence file remains one JSON
// document per handle, as every other reader in this package expects.
//
// TouchPeer/UpdateHeartbeat never fabricate a record: they fail with
// "peer not found" for a handle with no existing publication, exactly like
// the pre-refactor UpdateStatus did. This is what makes the Shutdown-races-
// UpdateHeartbeat resolution in registry_ops_test.go deterministic —
// heartbeat can never resurrect a peer Shutdown already removed.

import (
	"fmt"
	"time"
)

// UpdateHeartbeat refreshes ONLY a peer's LastSeenAt liveness timestamp. It
// is the field-scoped replacement for the pre-refactor TouchPeer, which
// called UpdateStatus(swarmName, handle, "", "") — a lost-update bug: that
// call unconditionally set Status and CurrentTask to the empty string on
// every heartbeat, silently discarding whatever status a concurrent
// UpdateStatus caller had just published. UpdateHeartbeat touches no field
// but LastSeenAt, so it can run concurrently with UpdateStatus/
// UpdateEndpoints/UpdateCapabilities without ever clobbering their fields.
//
// Applies to any existing peer type (local, daemon, or remote) — remote
// liveness refresh is exactly what the LAN gossip ingest path
// (lan_registry.go) and pollers rely on, and remote presence's projected
// shape already carries nothing but Handle/Status/LastSeenAt/Type.
func UpdateHeartbeat(swarmName, handle string) error {
	return WithPeerHandleTransaction(swarmName, handle, func(tx *PeerHandleTransaction) error {
		peer, err := tx.Get()
		if err != nil {
			return err
		}
		if peer == nil {
			return fmt.Errorf("peer not found: %s", handle)
		}
		peer.LastSeenAt = time.Now().UTC()
		return tx.publishRaw(*peer)
	})
}

// TouchPeer updates the last_seen_at timestamp for a peer. It is now a thin
// wrapper over UpdateHeartbeat instead of a bare positional call into the
// old broad UpdateStatus — see UpdateHeartbeat's doc comment for why that
// mattered.
func TouchPeer(swarmName, handle string) error {
	return UpdateHeartbeat(swarmName, handle)
}

// UpdateStatus updates ONLY a peer's Status and CurrentTask fields (plus
// refreshing LastSeenAt as a liveness side effect of any successful write —
// matching the pre-refactor UpdateStatus's behavior exactly, so existing
// callers that relied on a status update also counting as a heartbeat see
// no change). CurrentTask is never persisted for a remote peer: remote
// presence's allow-list projection (remotePresenceProjection) never carries
// it, exactly as before.
//
// The exported name/signature is preserved for external callers (see
// CONTRACT.md's backend compatibility note and swarm-sdk/a2a/a2a.go's public
// re-export). It intentionally still operates on remote peers too — the
// pre-refactor UpdateStatus did, and TestPublicSDKBareLegacyRemoteHandleNeverProjectsOrPersists
// plus the "status rewrite" case in TestRemotePresence_AllPersistenceWritersSanitize
// depend on a writer rewriting (and re-sanitizing) an existing remote
// record — so this calls the lower-level publishRaw rather than Publish,
// which would otherwise refuse PeerTypeRemote and reject a caller-supplied
// Handle that no longer matches tx.handle after projection (a bare/legacy
// non-opaque remote handle projects to an empty Handle). UpdateStatus never
// creates a new file — Get returning nil fails closed with "peer not
// found" — so it can never bypass upsertRemotePeer's retention-lock/cap/
// replacement checks, which exist solely to gate the creation of new
// remote entries.
func UpdateStatus(swarmName, handle, status, currentTask string) error {
	return WithPeerHandleTransaction(swarmName, handle, func(tx *PeerHandleTransaction) error {
		peer, err := tx.Get()
		if err != nil {
			return err
		}
		if peer == nil {
			return fmt.Errorf("peer not found: %s", handle)
		}
		peer.Status = status
		if peer.Type != PeerTypeRemote {
			peer.CurrentTask = currentTask
		}
		peer.LastSeenAt = time.Now().UTC()
		return tx.publishRaw(*peer)
	})
}

// UpdateEndpoints updates ONLY a peer's transport/route fields — EndpointURL,
// ServeURL, ControlSocket, and CardURL — plus refreshing LastSeenAt as a
// liveness side effect. It goes through the transaction's Publish (not the
// lower-level publishRaw UpdateStatus/UpdateHeartbeat use) so it inherits
// Publish's PeerTypeRemote refusal: remote presence's allow-list projection
// never carries endpoint/route fields at all, so accepting an endpoint
// update for a remote handle would silently do nothing useful at best, or
// signal a caller error at worst — Publish's refusal surfaces that as a
// clear error instead.
func UpdateEndpoints(swarmName, handle string, endpointURL, serveURL, controlSocket, cardURL string) error {
	return WithPeerHandleTransaction(swarmName, handle, func(tx *PeerHandleTransaction) error {
		peer, err := tx.Get()
		if err != nil {
			return err
		}
		if peer == nil {
			return fmt.Errorf("peer not found: %s", handle)
		}
		peer.EndpointURL = endpointURL
		peer.ServeURL = serveURL
		peer.ControlSocket = controlSocket
		peer.CardURL = cardURL
		peer.LastSeenAt = time.Now().UTC()
		return tx.Publish(*peer)
	})
}

// UpdateCapabilities updates ONLY a peer's descriptive/capability fields —
// Name, Model, Workspace, and Version — plus refreshing LastSeenAt as a
// liveness side effect. Like UpdateEndpoints, it goes through Publish and so
// refuses PeerTypeRemote: none of these fields survive
// remotePresenceProjection's allow-list either.
func UpdateCapabilities(swarmName, handle string, name, model, workspace, version string) error {
	return WithPeerHandleTransaction(swarmName, handle, func(tx *PeerHandleTransaction) error {
		peer, err := tx.Get()
		if err != nil {
			return err
		}
		if peer == nil {
			return fmt.Errorf("peer not found: %s", handle)
		}
		peer.Name = name
		peer.Model = model
		peer.Workspace = workspace
		peer.Version = version
		peer.LastSeenAt = time.Now().UTC()
		return tx.Publish(*peer)
	})
}

// Shutdown marks handle's presence as shutting down/removed by dispatching
// to the exact removal semantics discovery.go already defines per peer
// type — it introduces no new deletion policy of its own:
//
//   - instanceToken == "": delegates to LeaveSwarm, which removes a
//     local/remote publication outright (local also removes its canonical
//     control socket; remote removes only the JSON publication) and refuses
//     a daemon publication ("daemon cleanup requires matching instance
//     token") exactly as it always has.
//   - instanceToken != "": delegates to RemovePeerIfInstance, which removes
//     canonical artifacts (presence file + canonical control socket) only
//     while the current publication still carries a matching InstanceToken,
//     and unconditionally refuses PeerTypeRemote — again, exactly the
//     pre-existing behavior, just reused rather than reimplemented.
//
// Both delegates already use WithPeerHandleTransaction's underlying lock
// primitive (withRegistryHandle, optionally preceded by
// withRemoteRetentionLock for LeaveSwarm), so Shutdown serializes against
// every other operation on the same handle exactly like the field-scoped
// update operations above. See registry_ops_test.go's
// TestRegistryOpsShutdownRacingHeartbeatResolvesDeterministically for the
// documented shutdown-wins-and-is-irreversible resolution when Shutdown
// races a concurrent UpdateHeartbeat on the same handle.
func Shutdown(swarmName, handle, instanceToken string) error {
	if instanceToken != "" {
		_, err := RemovePeerIfInstance(swarmName, handle, instanceToken)
		return err
	}
	return LeaveSwarm(swarmName, handle)
}
