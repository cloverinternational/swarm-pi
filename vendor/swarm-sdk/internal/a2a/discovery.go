package a2a

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/inbox"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/presence"
)

// DefaultSwarmName is the default swarm name for peer discovery.
const DefaultSwarmName = "default"

// Filesystem boundary contract for the on-disk registry (~/.swarm/swarms/...):
// directories are always created/migrated to dirPerm (0700) and files to
// filePerm (0600). Every handle- or swarm-name-derived path is validated and
// canonicalized beneath the owned registry root (SwarmDir()) before use, and
// every write goes through writeFileAtomic so publication is atomic
// (temp file + same-directory rename) with best-effort directory durability.
const (
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600
)

// PeerType indicates whether a peer is local (same machine) or remote.
type PeerType string

const (
	PeerTypeLocal  PeerType = "local"
	PeerTypeRemote PeerType = "remote"
	PeerTypeDaemon PeerType = "daemon" // headless persistent worker
)

// PeerPresence represents a peer's presence in a swarm.
// This is stored as JSON in ~/.swarm/swarms/{swarm}/peers/{handle}.json
//
// Phase 08 (package decomposition) P08.1 note: package-boundaries.md's
// "presence" section and this phase's CONTRACT.md direct this type's
// definition to be redirected to the new internal/presence package
// (presence.LocalRecord / presence.AuthenticatedRemoteRecord). That full
// redefinition is deliberately NOT done here: PeerPresence is referenced by
// name from many other internal/a2a files outside this worker's file
// allowlist (registry_ops.go, lan_registry.go, lan_registry_v2.go,
// build_provenance.go, presence_version.go, card.go, peers.go,
// peer_mute.go, poller.go, hub.go, runtime.go, and the registryFS adapter
// feeding internal/inbox's Store) — renaming or restructuring it here would
// ripple into every one of those out-of-scope files, violating both
// CONTRACT.md's disjoint per-worker file ownership and this worker's
// explicit invariant that discovery.go's 3 existing inbox/identity callers
// see zero behavior change. Per this worker's own brief ("if you cannot
// achieve this without touching code outside your file/package list, STOP
// that specific sub-change and leave a clear TODO comment citing this brief
// instead of touching an out-of-scope file"), this comment is that TODO:
// the PeerPresence-wide redefinition is deferred to a later phase (see
// internal/presence/INDEX.md's "Deferred" section). What DOES move now,
// safely, is the one presence-schema concern fully self-contained within
// this file: unmarshalPeerTolerant's tolerant/versioned decode logic now
// delegates to presence.DecodeFirstJSON (see below) with byte-for-byte
// identical behavior.
type PeerPresence struct {
	Handle         string    `json:"handle"`
	Name           string    `json:"name,omitempty"`
	PID            int       `json:"pid,omitempty"`
	EndpointURL    string    `json:"endpoint_url,omitempty"`
	CardURL        string    `json:"card_url,omitempty"`
	Workspace      string    `json:"workspace,omitempty"`
	Branch         string    `json:"branch,omitempty"`
	Model          string    `json:"model,omitempty"`
	Status         string    `json:"status"`
	CurrentTask    string    `json:"current_task,omitempty"`
	ConversationID string    `json:"conversation_id,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	Type           PeerType  `json:"type"`
	AddedBy        string    `json:"added_by,omitempty"`       // For remote peers: who added them
	AddedAt        time.Time `json:"added_at"`                 // For remote peers: when added
	ControlSocket  string    `json:"control_socket,omitempty"` // Unix socket path for the automation control plane
	ServeURL       string    `json:"serve_url,omitempty"`      // HTTP base for the SDK client interface (serve): /rpc (JSON-RPC) + /sse (events). A thin TUI/webapp attaches here.

	// Attach capabilities are response-only hints used by desktop clients.
	// They are derived by the serving backend and are never persisted as
	// presence capabilities. Keeping these separate from private transport
	// fields lets a LAN projection say exactly what can be opened without
	// leaking a control socket or an internal URL.
	NativeSession bool `json:"native_session,omitempty"`
	TUIAttach     bool `json:"tui_attach,omitempty"`

	// Version is the peer binary's version string at startup (daemons only).
	// Used by ensureGlobalDaemon to detect a daemon left running an outdated
	// binary after a swarm update.
	Version string `json:"version,omitempty"`

	// BinaryModTime is the mtime of the peer's executable at startup (daemons
	// only). More reliable than Version for staleness detection: local
	// rebuilds change the mtime even when the version string is unchanged.
	BinaryModTime time.Time `json:"binary_mtime,omitempty,omitzero"`

	// SchemaVersion identifies which PeerPresence schema this record was
	// written under (see presence_version.go). A legacy record decoded
	// without a "schema_version" key naturally zero-values to
	// PresenceSchemaVersionLegacy — never an error, never an "invalid"
	// version. JoinSwarm and other local writers stamp
	// CurrentPresenceSchemaVersion on every record this build produces.
	SchemaVersion PresenceSchemaVersion `json:"schema_version,omitempty"`

	// BuildProvenance is this peer's build identity (daemons/local peers
	// only), computed once at process start from
	// runtime/debug.ReadBuildInfo() via CurrentBuildProvenance(). It exists
	// to replace BinaryModTime's wall-clock heuristic with a deterministic
	// same-build/different-build comparison (BuildProvenance.SameBuild) for
	// any freshness/staleness decision introduced from this phase onward;
	// BinaryModTime remains readable for legacy/backward-compatible records
	// but is no longer authoritative once BuildProvenance is populated. Like
	// Version and BinaryModTime, this is local-presence-only: it is never
	// included in remotePresenceProjection's allow-list below.
	BuildProvenance BuildProvenance `json:"build_provenance,omitempty,omitzero"`

	// Additive Phase 01 primitive evidence. Legacy records remain readable, but
	// missing evidence never authorizes signaling or automatic cleanup.
	InstanceToken string `json:"instance_token,omitempty"`
	ProcessStart  string `json:"process_start,omitempty"`
	Executable    string `json:"executable,omitempty"`
}

// MarshalJSON keeps the broad local/daemon compatibility shape while making
// the remote persistence and SDK JSON boundary an explicit opaque-liveness
// allow-list. In particular, time.Time's omitempty behavior cannot otherwise
// suppress zero StartedAt/AddedAt keys.
func (peer PeerPresence) MarshalJSON() ([]byte, error) {
	type localPeerPresence PeerPresence
	if peer.Type != PeerTypeRemote {
		return json.Marshal(localPeerPresence(peer))
	}
	projected := remotePresenceProjection(peer)
	return json.Marshal(struct {
		Handle     string    `json:"handle"`
		Status     string    `json:"status"`
		LastSeenAt time.Time `json:"last_seen_at"`
		Type       PeerType  `json:"type"`
	}{
		Handle:     projected.Handle,
		Status:     projected.Status,
		LastSeenAt: projected.LastSeenAt,
		Type:       projected.Type,
	})
}

// remotePresenceProjection is the single fail-closed representation of an
// unauthenticated remote peer. It deliberately constructs an allow-list of
// display/liveness metadata instead of clearing today's known capabilities:
// any future PeerPresence field is therefore excluded until it is explicitly
// reviewed as safe for unauthenticated remote presence.
//
// SchemaVersion and BuildProvenance (presence_version.go, build_provenance.go)
// are deliberately excluded from this allow-list, exactly like the
// pre-existing Version and BinaryModTime fields they complement: build/commit
// identity is never safe to hand to an unauthenticated remote peer, since it
// would let that peer fingerprint/correlate a specific host's exact binary
// build across records without any authentication proving the peer is
// entitled to that information.
//
// Only a strict process-generated origin~alias handle (opaqueRemoteHandle) is
// ever preserved. There is no bare/legacy-handle fallback: a non-opaque
// handle — however liveness-only the rest of the record otherwise is —
// carries no verifiable origin and projects to an empty handle instead of
// surviving remote persistence, legacy reads, or public SDK JSON.
//
// Non-remote peers are returned unchanged so local and daemon behavior retains
// its transport endpoints and process-instance evidence.
func remotePresenceProjection(peer PeerPresence) PeerPresence {
	if peer.Type != PeerTypeRemote {
		return peer
	}
	return PeerPresence{
		Handle:     opaqueRemoteHandle(peer.Handle),
		Status:     peer.Status,
		LastSeenAt: peer.LastSeenAt,
		Type:       PeerTypeRemote,
	}
}

// SwarmMessage represents a message in a peer's inbox.
//
// Phase 06 inbox extraction: the struct definition and the entire
// send/read/clear/compaction implementation now live in the leaf package
// internal/inbox (see internal/inbox/INDEX.md's "SwarmMessage location
// decision"). A repo-wide grep before the move found no external
// reference to a2a.SwarmMessage as a named type outside this file, so the
// type itself was free to move without rippling into other files; this
// alias keeps a2a.SwarmMessage usable, with true type identity (not just
// a structurally-identical copy), by every existing caller
// (internal/a2a/runtime.go:2089,2282 and
// swarm-tui/internal/chat/app_a2a_debug.go:117) with zero changes to
// those call sites.
type SwarmMessage = inbox.Message

// SwarmDir returns the base directory for swarm discovery data.
// This is ~/.swarm/swarms/ on all platforms.
func SwarmDir() string {
	return paths.In("swarms")
}

// SwarmPath returns the path for a specific swarm's directory. swarmName is
// sanitized first: an empty, malicious (path separators, "..", NUL) or
// otherwise invalid name silently falls back to DefaultSwarmName rather than
// letting a hostile swarm name escape SwarmDir() — this keeps SwarmPath's
// simple string-returning signature (many call sites across the package
// don't check an error) while still closing the traversal hole at the
// lowest-level chokepoint every other path in this file builds on.
func SwarmPath(swarmName string) string {
	return filepath.Join(SwarmDir(), sanitizeSwarmName(swarmName))
}

// ─── filesystem security primitives ────────────────────────────────────────
//
// Every function below is a chokepoint used by the rest of this file (and,
// transitively, by lan_registry.go's writes through writeFileAtomic) to keep
// the on-disk registry inside its owned root, free of symlink substitution,
// and owned by the current user with the tightest permissions the package
// contract requires (0700 dirs / 0600 files).

// unsafeEntryError marks a registry entry that exists but must never be
// trusted (symlink, wrong type, or owned by a different user). Callers use
// isUnsafeEntryError to fail closed (treat as absent / skip) rather than
// surfacing a hard error for state that is merely untrusted, not corrupt.
type unsafeEntryError struct {
	path   string
	reason string
}

func (e *unsafeEntryError) Error() string {
	return fmt.Sprintf("registry entry %s is unsafe: %s", e.path, e.reason)
}

func isUnsafeEntryError(err error) bool {
	var u *unsafeEntryError
	return errors.As(err, &u)
}

// validateRegistryName rejects any handle/swarm-name candidate that could
// not be safely turned into a single path segment: empty, ".", "..", NUL
// bytes, path separators, or an embedded "..". Every function in this file
// that accepts a caller-supplied handle validates it with this BEFORE any
// path is constructed.
func validateRegistryName(name string) error {
	if name == "" {
		return fmt.Errorf("registry name must not be empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("registry name %q is not allowed", name)
	}
	if strings.ContainsRune(name, 0) {
		return fmt.Errorf("registry name %q contains a NUL byte", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("registry name %q must not contain a path separator", name)
	}
	if strings.Contains(name, "..") {
		return fmt.Errorf("registry name %q must not contain '..'", name)
	}
	return nil
}

// sanitizeSwarmName is validateRegistryName with a safe fallback instead of
// an error, for use by the error-less SwarmPath.
func sanitizeSwarmName(name string) string {
	if err := validateRegistryName(name); err != nil {
		return DefaultSwarmName
	}
	return name
}

// validateWithinRegistryRoot fails closed when path does not canonicalize to
// somewhere strictly beneath SwarmDir(). This is the last line of defense
// against traversal that survives name validation upstream (for example a
// handle originating from an untrusted LAN peer-sync frame in
// lan_registry.go, which this package cannot itself validate before the
// path is built, but which still funnels through writeFileAtomic).
func validateWithinRegistryRoot(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve registry path: %w", err)
	}
	root, err := filepath.Abs(SwarmDir())
	if err != nil {
		return fmt.Errorf("resolve registry root: %w", err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("registry path %q escapes owned root %q", path, root)
	}
	return nil
}

// resolveRegistryPath is the single choke point for every handle-derived
// registry file path: it validates name, joins it under dir with ext, then
// re-verifies the joined result both canonicalizes to exactly dir/name+ext
// and still lies beneath the owned registry root. A name that survives
// validateRegistryName but somehow still alters its parent via Join's
// cleaning (defense in depth — should be unreachable given the checks above)
// is rejected here too.
func resolveRegistryPath(dir, name, ext string) (string, error) {
	if err := validateRegistryName(name); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+ext)
	if filepath.Dir(path) != filepath.Clean(dir) {
		return "", fmt.Errorf("registry name %q escapes directory %q", name, dir)
	}
	if err := validateWithinRegistryRoot(path); err != nil {
		return "", err
	}
	return path, nil
}

// fileOwnerUID returns the owning UID of fi when the platform's FileInfo.Sys()
// exposes one, and false otherwise. Implemented via reflection (rather than a
// direct *syscall.Stat_t type assertion) so this file compiles on every
// platform — Windows' syscall package has no Stat_t type, and a build-tagged
// second file isn't available under this worker's file allowlist. Ownership
// enforcement is therefore Linux/Unix-focused and gracefully absent
// elsewhere, matching the "Linux-focused, portable" test requirement.
func secureDir(dir string) error {
	return registrySecureDir(dir)
}

// safeReadFile reads path only when it is a regular file, never a symlink
// (which could substitute an attacker- or another-user-chosen target), and —
// where ownership is determinable — owned by the current user. A legacy file
// that is safely ours but has broader-than-filePerm permissions is tightened
// in place before being read. Unsafe entries return an *unsafeEntryError so
// callers can fail closed instead of trusting them.
func safeReadFile(path string) ([]byte, error) {
	return registryReadFile(path)
}

var registryLocks sync.Map
var registryConditionalCleanupAfterRead = func() {}
var registryAcquireHandleLock = acquireRegistryHandleLock

// registryTransactionEntered is a deterministic test barrier: it fires once
// WithPeerHandleTransaction has acquired the process-local mutex and the
// stable cross-process OS lock for a handle, immediately before invoking the
// caller's callback. Production code never overrides this; subprocess tests
// use it to prove a second process genuinely blocks on the same handle's
// lock (rather than merely racing) and that a different handle stays
// independent, without relying on sleeps/timing ratios as primary proof.
var registryTransactionEntered = func() {}

func registryHandleMutex(path string) *sync.Mutex {
	value, _ := registryLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	return value.(*sync.Mutex)
}

func withRegistryHandle(path string, fn func() error) error {
	mu := registryHandleMutex(path)
	mu.Lock()
	defer mu.Unlock()
	unlock, err := registryAcquireHandleLock(path)
	if err != nil {
		return fmt.Errorf("acquire registry handle lock: %w", err)
	}
	fnErr := fn()
	unlockErr := unlock()
	if fnErr != nil {
		return fnErr
	}
	if unlockErr != nil {
		return fmt.Errorf("release registry handle lock: %w", unlockErr)
	}
	return nil
}

// PeerHandleTransaction scopes Get/Publish access to a single peer handle's
// presence file for the lifetime of one WithPeerHandleTransaction callback.
// It carries no lock of its own: the process-local mutex and the stable
// cross-process OS lock for the handle are already held by the enclosing
// withRegistryHandle call by the time a *PeerHandleTransaction reaches the
// callback, and remain held until the callback returns. Get and Publish
// therefore call straight through to the already-locked read/write
// primitives (registryReadFile, writeFileAtomicLocked) instead of the
// re-locking wrappers (safeReadFile, writeFileAtomic) — going through the
// re-locking wrappers here would attempt to re-acquire the same
// non-reentrant per-path mutex/OS lock and deadlock the calling goroutine
// against itself.
type PeerHandleTransaction struct {
	swarmName string
	handle    string
	path      string
}

// WithPeerHandleTransaction acquires this package's process-local mutex and
// stable cross-process OS lock for handle's registry file exactly once, then
// invokes fn with a *PeerHandleTransaction bound to that already-verified
// handle. The lock is released when fn returns, whether or not fn returned
// an error — mirroring withRegistryHandle's fail-closed release semantics,
// so a callback error never leaves the handle's lock held.
//
// fn MUST NOT call JoinSwarm, LeaveSwarm, UpdateStatus, TouchPeer,
// UpdateHeartbeat, UpdateEndpoints, UpdateCapabilities, Shutdown, or
// RemovePeerIfInstance for this (or any) handle: those helpers acquire the
// very same per-path mutex/OS lock via withRegistryHandle (directly or via
// WithPeerHandleTransaction), and the lock this package uses is not
// reentrant — a nested acquisition attempt from inside fn would deadlock the
// calling goroutine against itself rather than fail fast. Callbacks that
// need to read or mutate presence for the locked handle must do so
// exclusively through the transaction's Get/Publish methods, which reuse the
// lock already held instead of acquiring it again.
//
// A lock-acquisition failure (process mutex contention aside — that merely
// blocks — but a stable-OS-lock open/flock/verify error) is returned
// directly to the caller and fn is never invoked, so no mutation is
// possible: the function fails closed exactly like every other writer in
// this file that goes through withRegistryHandle.
func WithPeerHandleTransaction(swarmName, handle string, fn func(*PeerHandleTransaction) error) error {
	if err := validateRegistryName(handle); err != nil {
		return fmt.Errorf("invalid peer handle: %w", err)
	}
	path, err := resolveRegistryPath(filepath.Join(SwarmPath(swarmName), "peers"), handle, ".json")
	if err != nil {
		return err
	}
	tx := &PeerHandleTransaction{swarmName: swarmName, handle: handle, path: path}
	return withRegistryHandle(path, func() error {
		registryTransactionEntered()
		return fn(tx)
	})
}

// Get returns the current PeerPresence for the transaction's already-verified
// handle, reusing the lock WithPeerHandleTransaction already holds. A missing
// file returns (nil, nil); an unsafe/corrupt entry fails closed the same way
// GetPeer does — never surfaced as a live peer, never returned as a hard
// error a careless caller might otherwise treat as "presence unknown but
// maybe present".
func (tx *PeerHandleTransaction) Get() (*PeerPresence, error) {
	data, err := registryReadFile(tx.path)
	if err != nil {
		if os.IsNotExist(err) || isUnsafeEntryError(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read peer file: %w", err)
	}
	var peer PeerPresence
	if err := unmarshalPeerTolerant(data, &peer); err != nil {
		return nil, fmt.Errorf("unmarshal peer: %w", err)
	}
	peer = remotePresenceProjection(peer)
	return &peer, nil
}

// Publish validates that presence.Handle matches the transaction's locked
// handle — a callback can never be tricked (by a caller-constructed value,
// or by copy/paste from a different handle's presence) into writing under a
// different handle's path than the one whose lock is actually held — and
// then writes it using the same already-locked atomic writer every other
// writer in this file uses (writeFileAtomicLocked: temp file in the same
// directory + rename, so readers never observe a partial write). A marshal
// or write failure is returned directly; the transaction fails closed and
// the caller sees no partial mutation beyond what the atomic rename itself
// guarantees never happens.
//
// Publish refuses PeerTypeRemote outright: WithPeerHandleTransaction only
// ever holds the per-handle lock, never the retention lock upsertRemotePeer
// requires, so a remote publish here could create an uncapped record and
// bypass the opaque-handle and local/daemon replacement checks. Every remote
// write — including a transaction-shaped one — must go through
// upsertRemotePeer (via JoinSwarm) instead.
func (tx *PeerHandleTransaction) Publish(presence PeerPresence) error {
	if presence.Handle != tx.handle {
		return fmt.Errorf("transaction publish handle %q does not match locked handle %q", presence.Handle, tx.handle)
	}
	if presence.Type == PeerTypeRemote {
		return fmt.Errorf("transaction publish refuses PeerTypeRemote: remote writes must go through upsertRemotePeer's retention-lock, cap, and replacement checks")
	}
	return tx.publishRaw(presence)
}

// publishRaw is the shared already-locked marshal+atomic-write primitive
// behind Publish. It intentionally performs neither the handle-match nor
// the PeerTypeRemote checks Publish enforces for ordinary callers: it exists
// so registry_ops.go's field-scoped heartbeat/status operations (which only
// ever mutate a record already read back via this same transaction's Get —
// never fabricate one — and which must remain able to refresh an existing
// remote peer's liveness/status exactly like the pre-refactor UpdateStatus
// did) and upsertRemotePeer (which independently enforces the
// retention-lock, cap, and non-remote-replacement invariants before ever
// calling this) can share one write path instead of duplicating
// marshal+writeFileAtomicLocked. Every other caller must go through Publish.
func (tx *PeerHandleTransaction) publishRaw(presence PeerPresence) error {
	data, err := json.MarshalIndent(presence, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peer: %w", err)
	}
	if err := writeFileAtomicLocked(tx.path, data); err != nil {
		return fmt.Errorf("write peer file: %w", err)
	}
	return nil
}

// ListPeers returns all active peers in the given swarm.
// Dead peers (PID not running) are automatically removed.
func ListPeers(swarmName string) ([]PeerPresence, error) {
	swarmPath := SwarmPath(swarmName)
	peersDir := filepath.Join(swarmPath, "peers")

	// Harden/migrate the peers directory itself before trusting anything
	// inside it (creates it at 0700 if missing, tightens legacy 0755, or
	// quarantines a symlink/wrong-owner substitution).
	if err := secureDir(peersDir); err != nil {
		return nil, fmt.Errorf("secure peers directory: %w", err)
	}

	entries, err := registryReadDir(peersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []PeerPresence{}, nil
		}
		return nil, fmt.Errorf("read peers directory: %w", err)
	}

	var peers []PeerPresence
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		path := filepath.Join(peersDir, entry.Name())
		data, err := safeReadFile(path)
		if err != nil {
			continue // Skip unreadable/unsafe files
		}

		var peer PeerPresence
		if err := unmarshalPeerTolerant(data, &peer); err != nil {
			continue // Skip malformed files
		}

		// Check liveness based on peer type
		if peer.Type == PeerTypeRemote {
			// Remote peers live on another machine, so we can't PID-check them.
			// Instead they are refreshed by the LAN gossip (lan_registry.go);
			// a peer whose LastSeenAt is older than RemotePeerTTL means its
			// machine stopped advertising (dropped off the LAN / shut down),
			// so reap it. This keeps the cross-device list self-healing.
			if time.Since(peer.LastSeenAt) <= RemotePeerTTL {
				peers = append(peers, remotePresenceProjection(peer))
			} else {
				_, _ = removeStaleRemotePeer(swarmName, peer)
			}
		} else if peer.Type == PeerTypeDaemon {
			// Daemons run headless and own a serve endpoint. Liveness is the
			// PID (they register with their real pid) — a dead daemon's
			// presence + sockets must be reaped just like a local TUI's.
			if isProcessAlive(peer.PID) {
				peers = append(peers, peer)
			} else if peer.InstanceToken != "" {
				_, _ = RemovePeerIfInstance(swarmName, peer.Handle, peer.InstanceToken)
			}
		} else {
			// For local peers, check if PID is alive
			if isProcessAlive(peer.PID) {
				peers = append(peers, peer)
			} else if peer.InstanceToken != "" {
				_, _ = RemovePeerIfInstance(swarmName, peer.Handle, peer.InstanceToken)
			}
		}
	}

	// Sweep orphaned control sockets: a *.ctrl with no live owner. Plain TUIs
	// historically removed their *.json on exit but leaked the sibling *.ctrl,
	// so the peers dir accretes hundreds of dead sockets. Any *.ctrl that is
	// not dial-able (nothing listening) is dead — remove it.
	//
	// THROTTLED: the sweep dials every socket (200ms timeout each), and
	// ListPeers sits on hot request paths (the gateway resolves the target
	// peer on every /rpc, /sse and /ws call). Unthrottled, a dir full of stale
	// sockets added seconds of latency per request — the "attach sometimes
	// hangs" symptom. Once a minute is plenty for garbage collection.
	if now := time.Now().UnixNano(); now-lastSocketSweep.Load() > int64(socketSweepInterval) {
		if lastSocketSweep.CompareAndSwap(lastSocketSweep.Load(), now) {
			sweepOrphanControlSockets(peersDir)
		}
	}

	return peers, nil
}

// lastSocketSweep is the UnixNano timestamp of the last orphan-socket sweep;
// socketSweepInterval is how often ListPeers is allowed to run one.
var lastSocketSweep atomic.Int64

const socketSweepInterval = time.Minute

// removePeerArtifacts deletes a dead peer's presence file and its canonical
// sibling control socket (<handle>.ctrl, derived purely from jsonPath's own
// basename). It NEVER trusts a presence-supplied ControlSocket value for
// deletion — that field can come from untrusted state (a corrupted file, or
// a peer ingested from a LAN peer-sync frame) and must never be handed to a
// filesystem removal call, or a hostile/corrupt presence file could make us
// unlink an arbitrary path on the host.
// removeCanonicalControlSocket removes only the sibling <handle>.ctrl next to
// jsonPath, with handle re-derived and re-validated from jsonPath's own file
// name (never from JSON content). unlink(2) never follows symlinks, so even
// if that exact entry had been symlinked elsewhere, only the link itself —
// never its target — is ever removed.
func removeCanonicalControlSocket(jsonPath string) error {
	dir := filepath.Dir(jsonPath)
	handle := strings.TrimSuffix(filepath.Base(jsonPath), ".json")
	ctrlPath, err := resolveRegistryPath(dir, handle, ".ctrl")
	if err != nil {
		return err
	}
	return registryRemove(ctrlPath)
}

// controlSocketAlive reports whether something is actually listening on the
// given unix socket path. A leaked socket file lingers on disk but refuses
// connections, so a failed dial means the owner is gone.
func controlSocketAlive(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// sweepOrphanControlSockets removes every *.ctrl in peersDir whose socket is
// not dial-able. Safe to call on every ListPeers: a live TUI/daemon keeps its
// socket connectable, so only genuinely dead sockets are reaped. A *.ctrl
// entry that is itself a symlink is never dialed (dialing would follow it to
// an arbitrary target) — it is removed outright, since a legitimate control
// socket is always created directly, never as a symlink.
func sweepOrphanControlSockets(peersDir string) {
	entries, err := registryReadDir(peersDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ctrl") {
			continue
		}
		sockPath := filepath.Join(peersDir, entry.Name())
		mode, err := registryEntryMode(sockPath)
		if err != nil {
			continue
		}
		if mode&os.ModeSymlink != 0 {
			continue
		}
		// A dead socket without its presence record has no instance token to
		// prove ownership. Legacy/missing evidence denies automatic deletion;
		// token-conditional peer cleanup removes the socket while serialized
		// with replacement writers.
		_ = controlSocketAlive(sockPath)
	}
}

// GetPeer returns a specific peer's presence information.
func GetPeer(swarmName, handle string) (*PeerPresence, error) {
	path, err := resolveRegistryPath(filepath.Join(SwarmPath(swarmName), "peers"), handle, ".json")
	if err != nil {
		return nil, err
	}
	data, err := safeReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		if isUnsafeEntryError(err) {
			// Fail closed: unsafe legacy/hostile state is never trusted as a
			// live peer — behave exactly as "not found".
			return nil, nil
		}
		return nil, fmt.Errorf("read peer file: %w", err)
	}

	var peer PeerPresence
	if err := unmarshalPeerTolerant(data, &peer); err != nil {
		return nil, fmt.Errorf("unmarshal peer: %w", err)
	}

	peer = remotePresenceProjection(peer)
	return &peer, nil
}

// unmarshalPeerTolerant decodes the FIRST JSON object in data into peer,
// tolerating trailing garbage left by a legacy non-atomic write that was
// partially overwritten by a shorter one. Plain json.Unmarshal rejects such
// files ("invalid character after top-level value"), which used to make a live
// peer disappear from discovery. Using a streaming Decoder reads exactly one
// object and ignores anything after it.
//
// Phase 08 P08.1: this now delegates to internal/presence.DecodeFirstJSON,
// which is the exact same json.NewDecoder(bytes.NewReader(data)).Decode(v)
// strategy this function used before extraction — see that function's own
// doc comment ("byte-for-byte the same decode strategy ... extracted here
// so discovery.go can delegate to it directly with zero behavior change").
// This is the one presence-schema-owning piece of this file that is safely
// self-contained (unmarshalPeerTolerant has no other caller outside this
// file) and therefore genuinely movable within this worker's file
// allowlist; see the TODO on PeerPresence above for why the surrounding
// type itself is not also redefined this phase.
func unmarshalPeerTolerant(data []byte, peer *PeerPresence) error {
	return presence.DecodeFirstJSON(data, peer)
}

// JoinSwarm registers a peer in the swarm by writing its presence file.
// PeerTypeRemote is routed through upsertRemotePeer (after timestamps are
// set) so every public and internal join shares the exact same
// retention-lock-then-handle-lock ordering, shared retained-count cap
// (refreshing an existing opaque entry even at the cap), and refusal to
// replace a local/daemon publication that lan_registry.go's ingest path
// already relies on. A non-opaque remote handle fails closed here — it never
// reaches the local/daemon write path below.
func JoinSwarm(swarmName string, peer PeerPresence) error {
	if err := validateRegistryName(peer.Handle); err != nil {
		return fmt.Errorf("invalid peer handle: %w", err)
	}

	swarmPath := SwarmPath(swarmName)
	peersDir := filepath.Join(swarmPath, "peers")
	inboxesDir := filepath.Join(swarmPath, "inboxes")

	// Ensure directories exist, owned by us, at 0700 (migrating/quarantining
	// any unsafe legacy state).
	if err := secureDir(peersDir); err != nil {
		return fmt.Errorf("secure peers directory: %w", err)
	}
	if err := secureDir(inboxesDir); err != nil {
		return fmt.Errorf("secure inboxes directory: %w", err)
	}

	// Set timestamps if not set
	now := time.Now().UTC()
	if peer.StartedAt.IsZero() {
		peer.StartedAt = now
	}
	peer.LastSeenAt = now

	// Default to local type
	if peer.Type == "" {
		peer.Type = PeerTypeLocal
	}

	// Remote joins never take the local/daemon write path below: they go
	// through upsertRemotePeer, which enforces the opaque-handle contract,
	// the retention-lock-then-handle-lock ordering, the shared retained-count
	// cap, and refuses to replace a local or daemon publication.
	if peer.Type == PeerTypeRemote {
		return upsertRemotePeer(swarmName, peer)
	}

	// Get current PID if not set
	if peer.PID == 0 && peer.Type == PeerTypeLocal {
		peer.PID = os.Getpid()
	}

	path, err := resolveRegistryPath(peersDir, peer.Handle, ".json")
	if err != nil {
		return err
	}

	// Write peer presence file atomically (temp + rename). Concurrent writers
	// (e.g. a heartbeat TouchPeer racing a status update) using a plain
	// truncating write can interleave and leave trailing bytes, corrupting the
	// JSON so GetPeer's Unmarshal fails and the peer is reported "not found"
	// despite being alive. A temp-file + rename makes each write atomic.
	data, err := json.MarshalIndent(peer, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal peer: %w", err)
	}

	if err := writeFileAtomic(path, data, filePerm); err != nil {
		return fmt.Errorf("write peer file: %w", err)
	}

	return nil
}

// writeFileAtomic writes data to path atomically via a temp file in the same
// directory followed by a rename. This guarantees readers see either the old
// or the new complete file — never a partially written / interleaved one.
//
// It is also the package's shared filesystem-security chokepoint: path is
// required to canonicalize beneath the owned registry root (SwarmDir()) —
// closing the traversal path even for callers this package cannot itself
// validate a handle for (lan_registry.go's upsertRemotePeer, which this
// worker does not own, still funnels its writes through this function) — the
// destination directory is hardened/migrated to dirPerm first, the written
// file is always chmod'd to filePerm (0600) regardless of what the caller
// requested (perm is accepted for call-site compatibility but a legacy 0644
// request can never widen the on-disk result), and the directory is
// best-effort fsync'd after the rename for durability.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	_ = perm // perm is intentionally ignored; filePerm (0600) always wins.
	if err := validateWithinRegistryRoot(path); err != nil {
		return err
	}
	return withRegistryHandle(path, func() error {
		return registryWriteFileAtomic(path, data)
	})
}

func writeFileAtomicLocked(path string, data []byte) error {
	return registryWriteFileAtomic(path, data)
}

// LeaveSwarm removes a peer from the swarm.
//
// Locking mirrors upsertRemotePeer's retention-lock-then-handle-lock order
// unconditionally (even for a local/daemon leave) so a leave can never
// invert lock order against a concurrent remote join/refresh. The on-disk
// type is re-read AFTER both locks are held — never trusted from a caller
// snapshot — because a hostile or racing LAN ingest could otherwise flip a
// record's type out from under an in-flight leave.
//
// Remote leave removes ONLY the JSON publication: it never exercises process
// authority and must never remove a control socket. remotePresenceProjection
// already strips ControlSocket from every genuine remote record, so a
// *.ctrl sibling next to a remote-typed entry never belongs to that remote
// record's own advertisement — it can only belong to a different local/
// daemon instance that happens to share a handle in a hostile/corrupt
// scenario, and must be preserved. Local/daemon behavior is unchanged: the
// presence file and its canonical sibling control socket are both removed
// (a daemon still requires token-conditional cleanup via
// RemovePeerIfInstance, so a bare LeaveSwarm on a daemon record fails
// closed exactly as before).
//
// Deliberately does NOT read the presence file's ControlSocket field to
// decide what to remove: that value is untrusted (it can be corrupted or,
// via lan_registry.go's ingest path, ultimately network-influenced) and
// must never be passed to a filesystem removal call. Only the path derived
// purely from the validated handle is ever touched.
func LeaveSwarm(swarmName, handle string) error {
	swarmPath := SwarmPath(swarmName)
	peersDir := filepath.Join(swarmPath, "peers")

	peerPath, err := resolveRegistryPath(peersDir, handle, ".json")
	if err != nil {
		return err
	}

	return withRemoteRetentionLock(peersDir, func() error {
		return withRegistryHandle(peerPath, func() error {
			data, err := registryReadFile(peerPath)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			var current PeerPresence
			if len(data) > 0 {
				if unmarshalPeerTolerant(data, &current) != nil {
					current = PeerPresence{}
				}
			}
			switch current.Type {
			case PeerTypeDaemon:
				return fmt.Errorf("daemon cleanup requires matching instance token")
			case PeerTypeRemote:
				return registryRemove(peerPath)
			default:
				if err := registryRemove(peerPath); err != nil {
					return err
				}
				_ = removeCanonicalControlSocket(peerPath)
				return nil
			}
		})
	})
}

// RemovePeerIfInstance removes canonical artifacts only while the current
// publication still carries expectedToken. Missing legacy evidence denies
// destructive cleanup. PeerTypeRemote is refused unconditionally — even a
// hostile legacy record forged with a matching InstanceToken carries no
// process authority, since remotePresenceProjection never persists an
// InstanceToken for a genuine remote record in the first place.
func RemovePeerIfInstance(swarmName, handle, expectedToken string) (bool, error) {
	if expectedToken == "" {
		return false, nil
	}
	peerPath, err := resolveRegistryPath(filepath.Join(SwarmPath(swarmName), "peers"), handle, ".json")
	if err != nil {
		return false, err
	}
	removed := false
	err = withRegistryHandle(peerPath, func() error {
		data, err := registryReadFile(peerPath)
		if err != nil {
			if os.IsNotExist(err) || isUnsafeEntryError(err) {
				return nil
			}
			return err
		}
		var current PeerPresence
		if err := unmarshalPeerTolerant(data, &current); err != nil {
			return nil
		}
		if current.Type == PeerTypeRemote {
			return nil
		}
		registryConditionalCleanupAfterRead()
		if current.InstanceToken == "" || current.InstanceToken != expectedToken {
			return nil
		}
		if err := registryRemove(peerPath); err != nil {
			return err
		}
		_ = removeCanonicalControlSocket(peerPath)
		removed = true
		return nil
	})
	return removed, err
}

// UpdateStatus, TouchPeer, UpdateHeartbeat, UpdateEndpoints,
// UpdateCapabilities, and Shutdown — the field-scoped registry operations —
// live in registry_ops.go. They are declared there (not here) so each
// operation's narrow field ownership is reviewable as one cohesive unit,
// per Phase 03 CONTRACT.md's registry-ops requirement.

// registryFS adapts this package's registry filesystem primitives
// (secureDir, resolveRegistryPath, withRegistryHandle, safeReadFile,
// registryReadFile, registryRemove, writeFileAtomicLocked, writeFileAtomic,
// isUnsafeEntryError) to internal/inbox's FileSystem interface, satisfying
// CONTRACT.md's "constructor parameters/interfaces rather than
// duplicating them" requirement: these primitives are shared, registry-
// wide security chokepoints (also used by peer presence and LAN registry
// code in this package) and therefore stay here rather than being copied
// into internal/inbox.
type registryFS struct{}

func (registryFS) SecureDir(dir string) error { return secureDir(dir) }

func (registryFS) ResolvePath(dir, name, ext string) (string, error) {
	return resolveRegistryPath(dir, name, ext)
}

func (registryFS) WithHandle(path string, fn func() error) error {
	return withRegistryHandle(path, fn)
}

func (registryFS) SafeReadFile(path string) ([]byte, error) { return safeReadFile(path) }

func (registryFS) ReadFile(path string) ([]byte, error) { return registryReadFile(path) }

func (registryFS) RemoveFile(path string) error { return registryRemove(path) }

func (registryFS) WriteFileLocked(path string, data []byte) error {
	return writeFileAtomicLocked(path, data)
}

func (registryFS) WriteFileAtomic(path string, data []byte) error {
	return writeFileAtomic(path, data, filePerm)
}

func (registryFS) IsUnsafeEntryError(err error) bool { return isUnsafeEntryError(err) }

// inboxStore is the single internal/inbox.Store used by every
// SendMessage/ReadInbox/ClearInbox call in this package, backed by
// registryFS. Constructing it once at package init time (rather than per
// call) is safe because *inbox.Store carries no mutable state of its own —
// all synchronization happens inside registryFS's underlying
// withRegistryHandle/registryLocks.
var inboxStore = inbox.NewStore(registryFS{})

// SendMessage appends a message to a peer's inbox. See
// internal/inbox.Store.SendMessage for the full NDJSON format, truncation,
// and legacy-array back-compat/upgrade-on-write documentation — this
// function is a one-line delegation preserving the exact historical
// signature so internal/a2a/runtime.go:2089,2282 need no changes.
func SendMessage(swarmName, toHandle, fromHandle, text string) error {
	return inboxStore.SendMessage(SwarmPath(swarmName), toHandle, fromHandle, text)
}

// ReadInbox returns all messages in a peer's inbox. See
// internal/inbox.Store.ReadInbox for the full NDJSON/legacy-array format
// documentation — this function is a one-line delegation preserving the
// exact historical signature so
// swarm-tui/internal/chat/app_a2a_debug.go:117 needs no changes.
func ReadInbox(swarmName, handle string) ([]SwarmMessage, error) {
	return inboxStore.ReadInbox(SwarmPath(swarmName), handle)
}

// ClearInbox removes all messages from a peer's inbox. One-line
// delegation preserving the exact historical signature.
func ClearInbox(swarmName, handle string) error {
	return inboxStore.ClearInbox(SwarmPath(swarmName), handle)
}

// isProcessAlive checks if a process with the given PID is still running.
func isProcessAlive(pid int) bool {
	return registryProcessAlive(pid)
}

// PeerPresenceToIdentity converts a PeerPresence to PeerIdentity for A2A transport.
func PeerPresenceToIdentity(p PeerPresence) PeerIdentity {
	p = remotePresenceProjection(p)
	return PeerIdentity{
		Handle:         p.Handle,
		SessionID:      "", // Not stored in presence
		EndpointURL:    p.EndpointURL,
		CardURL:        p.CardURL,
		WorkspacePath:  p.Workspace,
		ConversationID: "",
		RegisteredAt:   p.StartedAt,
		LastSeenAt:     p.LastSeenAt,
		ExpiresAt:      time.Time{}, // Not used with filesystem discovery
		Metadata: map[string]any{
			"model":        p.Model,
			"status":       p.Status,
			"current_task": p.CurrentTask,
			"pid":          p.PID,
		},
	}
}
