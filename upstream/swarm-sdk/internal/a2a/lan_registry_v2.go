package a2a

// LAN peer registry v2 — a NEW, side-by-side, signed-gossip ingestion path.
//
// Phase 06 (P06.D) per CONTRACT.md item 4: "This runs as a NEW side-by-side
// ingestion path (its own new file in internal/a2a, not editing
// lan_registry.go's existing unsigned v1 frame/loop) gated by an explicit
// config flag defaulting to off." This file is that new path. It does NOT
// edit lan_registry.go, does NOT reuse its unsigned peerSyncFrame wire
// shape or PeerSyncPort, and reuses ONLY the existing upsertRemotePeer
// write path (defined in lan_registry.go) to persist a verified frame into
// the shared on-disk presence registry -- it does not duplicate that
// registry-write logic.
//
// See swarm-sdk/internal/lan/gossip/INDEX.md's "Scope vs ADR-007/task-14"
// section: this loop is gated off by default, and even when enabled it
// never constructs a lan.AuthenticatedEndpoint or
// lan.RemoteAdvertisementProof, and is not consulted by any ADR-007 policy
// decision this phase.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/lan/gossip"
)

const (
	// PeerSyncV2Port is the fixed UDP port v2 signed-gossip frames are
	// listened for on, intentionally distinct from PeerSyncPort (8789) so
	// the unsigned v1 and signed v2 gossip channels never cross-talk or
	// misparse each other's frames.
	PeerSyncV2Port = 8790
)

// LANRegistryV2Config configures the v2 signed-gossip ingestion loop.
// EnableSignedGossipV2 defaults to false (Go zero value), matching
// CONTRACT.md's "gated by an explicit config flag defaulting to off" — a
// zero-value LANRegistryV2Config passed to StartLANRegistryV2 starts
// nothing and binds no socket.
type LANRegistryV2Config struct {
	// Swarm is the swarm name verified peers are persisted into. Empty
	// falls back to DefaultSwarmName, matching StartLANRegistry's existing
	// convention.
	Swarm string
	// EnableSignedGossipV2 gates the entire loop. False (the default) means
	// StartLANRegistryV2 opens no socket and starts no goroutine.
	EnableSignedGossipV2 bool
	// Port overrides the UDP port listened on. Zero falls back to
	// PeerSyncV2Port. Exists mainly so tests can avoid colliding on a
	// well-known port when running in parallel; production callers should
	// normally leave this zero.
	Port int
	// Verifier overrides the gossip.Verifier used to authenticate inbound
	// frames. Nil falls back to gossip.NewVerifier(). Exists so tests can
	// inject a Verifier pre-seeded with a known Signer's trust (today,
	// Verify itself doesn't pin a specific signer identity — see
	// gossip/INDEX.md's key-provisioning gap — but a distinct Verifier
	// instance still gives each test its own isolated replay cache).
	Verifier gossip.Verifier
}

// lanRegistryV2 holds one running v2 ingestion loop's state.
type lanRegistryV2 struct {
	swarm    string
	verifier gossip.Verifier
	recv     *net.UDPConn
}

// StartLANRegistryV2 starts (or, when cfg.EnableSignedGossipV2 is false,
// deliberately does NOT start) the v2 signed-gossip ingestion loop for the
// given config, returning a stop function.
//
// When cfg.EnableSignedGossipV2 is false, this function opens no socket,
// starts no goroutine, and returns a no-op stop function and a nil error —
// callers can always defer the returned stop() unconditionally regardless
// of whether the flag was set. lan_registry_v2_test.go verifies the
// "opens no socket" half of that claim by independently binding the same
// UDP port after calling this with the flag false, proving nothing is
// listening there — not merely by reading the flag value back.
//
// When enabled, it binds cfg.Port (default PeerSyncV2Port) on all
// interfaces, verifies every inbound datagram via cfg.Verifier (default
// gossip.NewVerifier()), and on a successful verification calls the
// EXISTING upsertRemotePeer write path (defined in lan_registry.go) to
// persist the verified peer into the shared on-disk presence registry.
// Frames that fail verification (bad signature, expired, replayed, skew
// violation, malformed) are silently dropped, mirroring lan_registry.go's
// existing ingestFrame's best-effort/drop-on-error posture for its own v1
// frame.
func StartLANRegistryV2(ctx context.Context, cfg LANRegistryV2Config) (stop func(), err error) {
	if !cfg.EnableSignedGossipV2 {
		return func() {}, nil
	}

	swarm := cfg.Swarm
	if swarm == "" {
		swarm = DefaultSwarmName
	}
	port := cfg.Port
	if port == 0 {
		port = PeerSyncV2Port
	}
	verifier := cfg.Verifier
	if verifier == nil {
		verifier = gossip.NewVerifier()
	}

	recv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return nil, fmt.Errorf("peer-sync v2 listen: %w", err)
	}

	reg := &lanRegistryV2{swarm: swarm, verifier: verifier, recv: recv}

	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reg.ingestLoop(loopCtx)
	}()

	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			cancel()
			_ = recv.Close()
			<-done
		})
	}, nil
}

// ingestLoop reads verified v2 frames and upserts remote peers into the
// local registry until ctx is cancelled. Structurally mirrors
// lan_registry.go's ingestLoop (timeout-based re-check of ctx.Done, drop
// and retry on any read error) without editing or importing anything from
// that file's private state.
func (r *lanRegistryV2) ingestLoop(ctx context.Context) {
	buf := make([]byte, 65536)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = r.recv.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := r.recv.ReadFrom(buf)
		if err != nil {
			// Timeout is expected (lets us re-check ctx); anything else we
			// also just retry on the next loop, matching lan_registry.go's
			// existing v1 ingestLoop posture.
			continue
		}
		r.ingestFrame(buf[:n])
	}
}

// ingestFrame verifies one inbound v2 frame and, only on success, persists
// it as a remote peer via the EXISTING upsertRemotePeer write path.
func (r *lanRegistryV2) ingestFrame(data []byte) {
	verified, err := r.verifier.Verify(data, time.Now())
	if err != nil {
		return // reject silently, same drop-on-error posture as v1 ingestFrame
	}
	peer := verifiedAdvertisementToPeerPresence(verified)
	_ = upsertRemotePeer(r.swarm, peer)
}

// verifiedAdvertisementToPeerPresence maps a gossip.VerifiedAdvertisement
// into the SAME opaque-handle PeerPresence shape upsertRemotePeer already
// requires (an "origin~alias" pair of 32-lowercase-hex-char halves, per
// lan_registry.go's opaqueRemoteHandle/validOriginID/validAliasID). The v2
// frame carries no separate origin/alias pair (it wasn't designed to reuse
// v1's wire shape — see this file's package doc comment), so the handle is
// derived deterministically from the verified PeerIdentity/DaemonInstance
// pair by truncated SHA-256 hashing into two 16-byte (32 hex char) halves.
// This is deterministic per identity pair (repeated frames from the same
// peer/daemon collapse to the same handle, so upsertRemotePeer's existing
// no-op-if-unchanged and retention-cap logic behaves sensibly) without
// exposing anything about the identity strings themselves in the on-disk
// handle.
func verifiedAdvertisementToPeerPresence(v gossip.VerifiedAdvertisement) PeerPresence {
	origin := truncatedHexHash(v.PeerIdentity)
	alias := truncatedHexHash(v.DaemonInstance)
	return PeerPresence{
		Handle:     origin + "~" + alias,
		Status:     "active",
		Type:       PeerTypeRemote,
		LastSeenAt: time.Now().UTC(),
	}
}

// truncatedHexHash returns the first 16 bytes of sha256(s), lowercase-hex
// encoded (32 hex chars) — matching lan_registry.go's originIDLen/
// aliasIDLen (originIDBytes/aliasIDBytes = 16) exactly so the derived
// handle passes validOriginID/validAliasID.
func truncatedHexHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:16])
}
