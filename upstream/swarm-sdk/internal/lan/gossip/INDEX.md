# `internal/lan/gossip`

## Purpose

Phase 06 (P06.D)'s "genuinely signed LAN gossip v2" primitive per
`.swarmflow/swarm-attach-architecture/p06-transport-discovery-simplification/CONTRACT.md`
item 4. This package defines:

- a new, signed (Ed25519) LAN advertisement wire frame (`frame.go`), distinct
  from `internal/a2a/lan_registry.go`'s existing unsigned v1
  `peerSyncFrame`/`Service`/`Version` wire shape;
- a bounded-memory nonce replay cache (`replay.go`) a `Verifier` consults;
- a `Verifier` (`verifier.go`) that verifies signature, issue/expiry skew
  bounds, and replay-freshness before returning a `VerifiedAdvertisement`.

It is consumed by a NEW, side-by-side, flag-gated-off-by-default ingestion
loop in `internal/a2a/lan_registry_v2.go` (a separate file; `lan_registry.go`
itself, including its v1 frame and loops, is untouched this phase).

## Exported-symbol summary

- `type Frame struct{...}` -- the v2 wire frame: `PeerIdentity`,
  `DaemonInstance`, `Route`, `Signer` (hex Ed25519 public key), `IssuedAt`,
  `ExpiresAt`, `Nonce` (hex), `Signature` (hex Ed25519 signature over the
  canonical signed payload of every other field).
- `const NonceBytes = 16` -- random bytes in a nonce before hex encoding.
- `type Signer struct{...}` -- holds one process's in-memory Ed25519 key
  pair (see "Key-provisioning gap" below).
- `func NewSigner() (*Signer, error)` -- generates a fresh in-memory key
  pair for the calling process's lifetime.
- `func NewSignerFromSeed(seed []byte) (*Signer, error)` -- deterministic
  Signer construction for tests that need reproducible/mismatched key
  material.
- `func (*Signer) PublicKeyHex() string` -- the Signer's public key,
  lowercase-hex-encoded (same encoding as `Frame.Signer`).
- `func Sign(signer *Signer, peerIdentity, daemonInstance, route string,
  issuedAt, expiresAt time.Time) (*Frame, error)` -- builds, nonces, and
  signs a new `Frame`.
- `func (*Frame) Bytes() ([]byte, error)` -- wire (JSON) serialization.
- `func ParseFrame(data []byte) (*Frame, error)` -- structural JSON parse
  only; does NOT evaluate signature/skew/replay. Use `Verifier.Verify` for
  any trust decision.
- `type VerifiedAdvertisement struct{...}` -- the CONTRACT.md-fixed shared
  type-seam struct (see below).
- `type Verifier interface { Verify(frame []byte, now time.Time)
  (VerifiedAdvertisement, error) }` -- the CONTRACT.md-fixed shared
  type-seam interface.
- `func NewVerifier() Verifier` -- default-capacity replay cache.
- `func NewVerifierWithReplayCapacity(capacity int) Verifier` -- explicit
  replay-cache capacity, mainly for tests.
- `const MaxClockSkew = 30 * time.Second` -- see "Skew tolerance" below.

Everything else (`signedFields`, `canonicalSignedBytes`, `replayCache` and
its methods, `newNonce`) is unexported package-internal plumbing.

## Key-provisioning gap (documented, in scope this phase only as an
in-memory primitive)

`NewSigner` generates an Ed25519 key pair entirely in memory, for the
lifetime of the calling process. There is:

- **no durable keystore** -- a process restart gets a brand-new key, so any
  peer that had learned the old public key can no longer verify frames from
  the restarted process until it learns the new one;
- **no rotation mechanism** -- a key never changes within a process
  lifetime, and there is no bounded-overlap rotation window analogous to
  ADR-007's credential rotation;
- **no cross-process trust-distribution mechanism** -- this package does
  not publish, pin, or otherwise distribute a `Signer`'s public key to
  peers. A verifying process today has no way to know in advance which
  public key it should trust for a given `PeerIdentity`/`DaemonInstance`;
  in practice today's `Verifier.Verify` accepts any structurally valid,
  freshly-signed, non-replayed frame regardless of which key signed it
  (self-consistency of `Signer`+`Signature`, not signer-identity trust, is
  what is checked). Binding a specific expected signer identity to a
  specific `PeerIdentity` is deferred along with the broker/trust work
  below.

This is an intentional, documented gap for this phase, matching the task
brief's explicit allowance: "an in-memory or file-backed key for the
process lifetime is acceptable this phase." A durable keystore integration,
key distribution, and per-peer trust binding are future-phase work.

## Skew tolerance

`MaxClockSkew = 30 * time.Second`. `Verifier.Verify` accepts a frame whose
`IssuedAt` is up to `MaxClockSkew` ahead of the verifier's own `now`
(tolerating ordinary unsynchronized-NTP LAN clock drift), but rejects a
frame issued further in the future than that. Expiry (`ExpiresAt`) is
checked as a strict `now < ExpiresAt` boundary with no additional skew
added -- "at or after `ExpiresAt`" is always rejected, matching the task
brief's explicit wording.

## Replay-cache design choice

`replayCache` (`replay.go`) is a size-capped LRU cache keyed by nonce, with
**oldest-eviction** when a new nonce would exceed
`defaultReplayCacheCapacity` (4096 entries). It also opportunistically
sweeps entries whose associated frame `ExpiresAt` has passed relative to
the `now` given to the current call, freeing that nonce (and a capacity
slot) for reuse -- i.e. an expired entry's nonce CAN be reused after sweep;
this is a deliberate design choice, not permanent retention-until-eviction.
Both the capacity cap and the sweep-then-reuse behavior are covered by
`replay_test.go`. This means retention is bounded in two independent ways
(hard capacity AND expiry-based reclamation), and the tradeoff explicitly
accepted is: a nonce far enough in the past to have been LRU-evicted (even
before its own `ExpiresAt`) is no longer remembered as "seen" -- this
package chooses a hard, provable memory bound over an unbounded exact
replay history, per the task brief's explicit requirement that the cache
"must never grow unbounded under a flood of distinct nonces."

## Scope vs ADR-007/task-14 (deferred this phase -- copied verbatim from
CONTRACT.md's "Deferred this phase" list, LAN-discovery-relevant items)

> - A production discovery broker abstraction and multi-interface ranking
>   (no existing code to migrate; `firstLANIPv4()`'s single-interface
>   heuristic is unchanged this phase). Defer to Phase 08.
> - Removing unsigned legacy LAN discovery (`lan_registry.go`'s v1 frame) --
>   task 14 itself gates this on "authenticated parity", which this phase's
>   side-by-side, flag-gated-off-by-default v2 primitive does not yet reach.
> - Any construction of `AuthenticatedEndpoint`/`NewRemoteAuthenticatedEndpoint`
>   from LAN gossip v2 output in a production code path.

This package's `VerifiedAdvertisement` is deliberately field-compatible
with `internal/lan.RemoteAdvertisementProof` (see the type's doc comment)
so a LATER phase can build that proof one field at a time without a rename
-- but neither this package, nor `internal/a2a/lan_registry_v2.go`, ever
constructs a `lan.RemoteAdvertisementProof` or calls
`lan.NewRemoteAuthenticatedEndpoint`. `VerifiedAdvertisement` also
deliberately omits `TLS` (`lan.RemoteTLSIdentity`): this primitive is
UDP-broadcast gossip with no TLS-terminated connection, so there is no TLS
identity to bind yet. A future reader must not treat this package, or the
`internal/a2a` ingestion loop that consumes it, as production-wired
authenticated discovery: `internal/a2a/lan_registry_v2.go`'s ingestion loop
is gated off by default behind `LANRegistryV2Config.EnableSignedGossipV2`
(defaults to `false`), and even when enabled, it only writes an
unauthenticated-provenance `PeerPresence` record (via the existing
`upsertRemotePeer` write path) into the same on-disk registry the unsigned
v1 gossip already writes to -- it does not gate gateway/proxy access, does
not select an `AuthenticatedEndpoint`, and is not consulted by any
ADR-007 policy decision this phase.

## Import boundary

Per CONTRACT.md's hard invariants, `internal/lan/gossip` MUST NOT import
`internal/a2a`, `internal/inbox`, or `internal/presentationcontrol`,
and this package does not. It also does not import its parent package
`internal/lan` -- none of `internal/lan`'s existing crypto/identity helpers
were needed (Ed25519 signing/verification comes directly from the
standard library's `crypto/ed25519`), so there was no reason to take on
even a read-only dependency on that package, keeping any import-cycle risk
moot. The new side-by-side ingestion file in `internal/a2a` imports
`internal/lan/gossip`, never the reverse.
