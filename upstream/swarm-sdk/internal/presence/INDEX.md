# `internal/presence`

## Responsibility

Own versioned local and remote presence records: define their schema,
validate them, manage leases, and provide atomic registry operations
(`PublishLocal`, `RenewLocal`, `RemoveLocal`, `UpsertRemote`, `Get`,
`List`) plus a versioned JSON codec. Presence reports discoverable facts;
it does **not** decide lifecycle transitions, transport routes, inbox
delivery, or A2A identity conversion.

**Explicit non-responsibilities** (docs/architecture/swarm-attach/
package-boundaries.md's "presence" section, and this phase's CONTRACT.md
section 1, item 1's own warning): inbox message storage/delivery and A2A
identity conversion do **not** live here — they stay in `internal/a2a`
(`internal/inbox`, and `PeerPresenceToIdentity`/`PeerIdentity` in
`internal/a2a/discovery.go` and sibling files). This package does not know
what LAN transport, UDP discovery frame, attach RPC, control socket, or
journal is — those are `internal/lan`, `attachclient`/`daemon`, and
`internal/journal*`'s concerns respectively.

## Owned state

Local leases (`Lease`), validated remote-presence cache entries
(`AuthenticatedRemoteRecord`), expiry metadata, per-handle registry
serialization locks (`FileRegistry.handleLocks`), and canonical presence
values (`LocalRecord`, `Record`). **Local and remote records are different
Go types** — see `TestRegistryLocalVsRemoteAreDistinctTypes` in
`presence_test.go`, which reflectively proves `Registry.PublishLocal`'s
second parameter type and `Registry.UpsertRemote`'s second parameter type
are distinct `reflect.Type`s, not just distinct names.

## Public entry points and normal call sequence

```go
type Registry interface {
    PublishLocal(context.Context, LocalRecord) error
    RenewLocal(context.Context, Lease) error
    RemoveLocal(context.Context, InstanceID) error
    UpsertRemote(context.Context, AuthenticatedRemoteRecord) error
    Get(context.Context, Handle) (Record, error)
    List(context.Context, Filter) ([]Record, error)
}
```

This signature is FIXED byte-for-byte by package-boundaries.md's "presence"
section and this phase's CONTRACT.md section 3 — do not change it without
updating both documents in the same change (package-boundaries.md's own
"INDEX contract" rule).

Normal sequence for a local publisher (a daemon or TUI instance):
1. `reg := presence.NewFileRegistry(root)` (or inject a `FileSystem`/`Clock`
   via `WithFileSystem`/`WithClock` for tests).
2. `reg.PublishLocal(ctx, LocalRecord{Handle: h, ...})` on startup.
3. `reg.RenewLocal(ctx, Lease{Instance: InstanceID{Handle: h, Token: tok}, ExpiresAt: ...})`
   periodically (heartbeat).
4. `reg.RemoveLocal(ctx, InstanceID{Handle: h, Token: tok})` on clean
   shutdown.

Normal sequence for an authenticated remote ingester (for example
`internal/lan` after verifying a signed discovery frame):
1. `reg.UpsertRemote(ctx, AuthenticatedRemoteRecord{Handle: h, Provenance: ..., ExpiresAt: ...})`.
   `UpsertRemote` fails closed with `ErrRemoteRequiresProvenance` /
   `ErrRemoteRequiresExpiry` if either is missing — an authenticated origin
   and a bounded expiry are mandatory, never optional.

Any reader (local or remote): `reg.Get(ctx, handle)` / `reg.List(ctx, filter)`.
`List` never mutates; use `FileRegistry.ReapExpired(ctx)` (a `*FileRegistry`-
specific method, not part of `Registry`) to actively delete stale entries.

Also exported: `EncodeLocal`/`DecodeLocal`, `EncodeRemote`/`DecodeRemote`
(the versioned codec), and `DecodeFirstJSON` (the shared tolerant-decode
primitive `internal/a2a/discovery.go`'s `unmarshalPeerTolerant` now
delegates to — see "Compatibility adapters" below). This package does
**not** expose registry file paths, `os.File`, mutable maps, or LAN
packets, per package-boundaries.md's own constraint.

## Allowed and forbidden dependencies

**Allowed** (package-boundaries.md, verbatim): lifecycle value types,
neutral identity/contract values, standard library, and an injected
clock/filesystem boundary. This package currently imports only the Go
standard library (`bytes`, `context`, `encoding/json`, `errors`, `fmt`,
`os`, `path/filepath`, `strings`, `sync`, `time`) — it has not yet needed a
lifecycle value type.

**Forbidden** (package-boundaries.md, verbatim): `daemon`, `attachclient`,
`presentationcontrol`, `supervisor`, concrete `lan`, CLI/TUI, gateway
proxy, and engine packages. In particular this package does **not** import
`internal/a2a` — the dependency direction runs the other way (`internal/a2a`
imports `internal/presence`, not vice versa), and package-boundaries.md's
extraction-order rule ("No extraction step may introduce a reverse import
into an already extracted package") forbids this package ever importing
back into `internal/a2a`.

## Persistence/protocol ownership

Owns the on-disk presence schema (`wireRecord`'s JSON shape, including the
`schema_version` field), atomic write/rename rules
(`osFileSystem.WriteFileAtomic`: temp file in the same directory + `fsync`
+ `chmod 0600` + rename + best-effort directory `fsync`), permissions
(0600 files via the temp-file `Chmod`; the injected `FileSystem.MkdirAll`
creates the registry root at 0700), lease expiry (`Lease.Expired`,
`FileRegistry.ReapExpired`), and schema migrations
(`SchemaVersion.KnownOrLegacy`, legacy-zero-value decode). It does **not**
own UDP discovery, attach RPC, control sockets, inbox messages, or
journals — those remain `internal/lan`, `attachclient`/`daemon`,
`internal/a2a`/`internal/inbox`, and `internal/journal*` respectively.

`FileRegistry` persists one JSON file per handle directly inside its root
directory (`root/{handle}.json`), matching `internal/a2a/discovery.go`'s
pre-extraction flat per-handle-file layout (`~/.swarm/swarms/{swarm}/peers/{handle}.json`).
This package itself has no opinion on where that root directory lives —
callers (for example a future `internal/a2a` migration, out of scope this
phase) choose the root path.

## Focused tests (`presence_test.go`)

- **Golden v0/v1 decode fixtures**: `TestGoldenDecodeLocalV0Legacy` (raw
  JSON with no `schema_version` key, hand-written, not produced by this
  package's own encoder), `TestGoldenDecodeLocalV1BuildProvenance` (raw
  JSON with `schema_version: 1` and a populated `build_provenance` object),
  `TestGoldenDecodeRemoteV1AuthenticatedProvenance`. Plus
  `TestGoldenEncodeDecodeRoundTrip` proving `Encode*`/`Decode*` are true
  inverses for a fully-populated record of each kind.
- **Local/remote type separation**: `TestRegistryLocalVsRemoteAreDistinctTypes`
  reflectively inspects the `Registry` interface's own method signatures.
- **Real atomic concurrent update test under `-race`**:
  `TestConcurrentUpdatesNoLostUpdateForUnrelatedPeers` (24 goroutines, 12
  local + 12 remote peers, 20 writes each, asserts every peer's FINAL write
  survives — package-boundaries.md invariant "Registry updates cannot lose
  an unrelated peer's update") and `TestConcurrentRenewSamePeerNoCorruption`
  (16 goroutines hammering `RenewLocal` for the SAME handle, asserts the
  record decodes cleanly afterward — proves the per-handle mutex actually
  serializes, not just narrows, the race window).
- **Stale lease cleanup**: `TestStaleLeaseCleanupReapsOnlyExpired` (fake
  `Clock`, asserts `ReapExpired` removes only records past their expiry —
  local `Lease.ExpiresAt` or remote `ExpiresAt` — and leaves a no-lease
  local record and a not-yet-expired record untouched).
- **Malformed/unknown-version rejection**:
  `TestDecodeLocalRejectsUnknownFutureSchemaVersion`,
  `TestDecodeRemoteRejectsUnknownFutureSchemaVersion` (schema_version
  greater than `CurrentSchemaVersion` is rejected, not silently trusted),
  `TestDecodeLocalRejectsMalformedJSON`,
  `TestDecodeFirstJSONTolerantOfTrailingGarbage` (documents exactly why the
  streaming-decoder strategy exists, by also showing plain
  `json.Unmarshal` rejects the same input).
- Additional invariant tests: `TestUpsertRemoteRejectsMissingProvenanceOrExpiry`,
  `TestRenewLocalRejectsInstanceTokenMismatch`,
  `TestRemoveLocalRejectsInstanceTokenMismatch`,
  `TestGetReturnsErrNotFoundForAbsentHandle`,
  `TestListEmptyRegistryReturnsNoError`, `TestListFilterByKind`.

**Verification command:** `go test -race ./internal/presence/...`

## Invariants

(Numbered to match package-boundaries.md's "presence" invariants list,
same wording as the executable tests above.)

1. A record has a schema version and canonical daemon/process identity —
   `SchemaVersion` + `InstanceID`/`Instance` field on every `LocalRecord`;
   enforced by `Decode{Local,Remote}`'s `KnownOrLegacy` check.
2. Remote records require authenticated provenance and expiry —
   `UpsertRemote` fails closed with `ErrRemoteRequiresProvenance` /
   `ErrRemoteRequiresExpiry` (`TestUpsertRemoteRejectsMissingProvenanceOrExpiry`).
3. A local control socket is never copied into a remote record —
   `AuthenticatedRemoteRecord` has no `ControlSocket` field at all; it is
   structurally impossible to populate one via `UpsertRemote`.
4. Registry updates cannot lose an unrelated peer's update — per-handle
   locking in `FileRegistry` (`TestConcurrentUpdatesNoLostUpdateForUnrelatedPeers`).
5. Free-text `current_task` is display data, never execution identity —
   `LocalRecord.CurrentTask` is a plain `string`, never parsed or used as a
   lookup/authorization key anywhere in this package.

## Concurrency model

`FileRegistry` serializes every operation on a given `Handle` through a
per-handle `*sync.Mutex` (constructed lazily under a package-private
top-level `sync.Mutex` guarding the `handleLocks` map itself — mirroring
`internal/a2a/discovery.go`'s pre-extraction `registryLocks sync.Map` /
`registryHandleMutex` pattern). Operations on different handles proceed
fully concurrently with no cross-handle blocking. There are no goroutines
owned by this package — every method is synchronous and returns once its
filesystem operation completes (or `ctx` is canceled, checked via
`ctx.Err()` at the start of every method). No background sweeper/timer
runs automatically; `ReapExpired` is caller-invoked.

## Error categories and security/privacy boundary

- `ErrNotFound` — no record at the requested handle.
- `ErrUnknownSchemaVersion` — a record's `schema_version` is newer than
  this build's `CurrentSchemaVersion`; never silently trusted/truncated.
- `ErrInstanceMismatch` — a `RenewLocal`/`RemoveLocal` caller's instance
  token does not match the stored record's token (fails closed, matching
  `internal/a2a/discovery.go`'s `RemovePeerIfInstance` philosophy: missing
  or mismatched evidence never authorizes destructive action).
- `ErrRemoteRequiresProvenance` / `ErrRemoteRequiresExpiry` — an
  unauthenticated or unbounded-lifetime remote record is refused.

Privacy boundary: `AuthenticatedRemoteRecord` has no field for build/commit
identity, control sockets, or process evidence — those exist only on
`LocalRecord`, matching `internal/a2a/discovery.go`'s
`remotePresenceProjection` allow-list philosophy (a future field must be
explicitly added to the remote-safe type to become remote-visible; it is
never inherited implicitly).

## Compatibility adapters

- **`internal/a2a/discovery.go`'s `unmarshalPeerTolerant`** now delegates
  directly to this package's `DecodeFirstJSON` (owner: this worker, P08.1;
  no removal gate — this is a permanent, byte-for-byte-behavior-preserving
  delegation of a single self-contained helper function, not a temporary
  shim). See `migration-policy.md` for the general compatibility-window
  policy; this specific delegation carries no legacy path to remove since
  it changed no observable behavior.
- **`internal/a2a/discovery.go`'s `PeerPresence` type itself** is NOT yet
  redefined in terms of this package's `LocalRecord`/`AuthenticatedRemoteRecord`
  — see the `TODO`-style comment directly above `PeerPresence`'s
  declaration in `discovery.go` for the full rationale (it is referenced by
  name from ~10 other `internal/a2a` files outside this worker's file
  allowlist). Owner of that follow-up: whichever future phase migrates
  `internal/a2a`'s remaining legacy presence callers onto this package
  directly (see "Deferred" below).

## Deferred

Per this phase's CONTRACT.md section 2 (record verbatim):

- `internal/daemon` composition-root extraction is deferred (structurally
  last in package-boundaries.md's extraction order); `internal/a2a/lan_registry_v2.go`'s
  full migration off legacy unsigned discovery is deferred (gated on
  authenticated parity, Phase 06 scope).

Additionally, specific to this package's own extraction:

- `internal/a2a/discovery.go`'s `PeerPresence` type is not redefined in
  terms of `presence.LocalRecord`/`presence.AuthenticatedRemoteRecord` this
  phase (see "Compatibility adapters" above) — deferred until the ~10 other
  `internal/a2a` files that reference `PeerPresence` by name can be migrated
  in the same coordinated change, which is out of this disjoint worker's
  scope.
- `internal/lan`'s production use of this package's `Registry`/`UpsertRemote`
  for authenticated remote ingestion is not wired this phase —
  package-boundaries.md's extraction order places full `internal/lan`
  extraction (step 4) after `presence` (step 2) but this phase does not
  reach step 4.
- This package's own file-size note (package-boundaries.md's "File-size
  policy"): `presence.go` is intentionally a single file above the 400-500
  line soft threshold. The single retained concern is "everything this
  package's `Registry` contract needs to be reviewable as one cohesive
  unit without cross-file hunting" (value types, errors, codec, and the one
  concrete `FileRegistry` implementation) — this worker's file allowlist
  for this phase (`presence.go`, `presence_test.go`, `INDEX.md` only) does
  not permit splitting it into sibling files without exceeding the
  `# FILES:` grant. Follow-up owner: a future phase, if/when this package
  gains a second concrete `Registry` implementation or the codec grows
  enough to warrant `codec.go`/`registry_file.go` splits.
