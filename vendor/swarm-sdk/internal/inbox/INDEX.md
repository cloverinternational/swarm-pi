# inbox

`swarm-sdk/internal/inbox` — the peer-inbox (durable offline-safe direct
messaging queue) implementation extracted from
`internal/a2a/discovery.go`, per Phase 06's
`.swarmflow/swarm-attach-architecture/p06-transport-discovery-simplification/CONTRACT.md`
("Inbox extraction", item 3 of the in-scope list) and
`docs/architecture/swarm-attach/package-boundaries.md`.

## Purpose

A dependency-light leaf package holding the on-disk peer-inbox message
format: append-only NDJSON (one JSON object per line), transparent
detection/parsing of the older JSON-array format for back-compat, and
truncation to a bounded message count. `internal/a2a/discovery.go`'s
`SendMessage`/`ReadInbox`/`ClearInbox` package-level functions are now
one-line delegations to this package's `*Store` methods, preserving their
exact historical signatures so every existing call site needed zero
changes (`internal/a2a/runtime.go:2089`, `:2282`, and
`swarm-tui/internal/chat/app_a2a_debug.go:117`).

## SwarmMessage location decision

CONTRACT.md's "Shared type seam" section asked: does `SwarmMessage` move
into `internal/inbox`, or stay in `internal/a2a` with `internal/inbox`
importing it back (the documented narrow exception)?

**Decision: it moved.** Before moving anything, a repo-wide grep for
`SwarmMessage` (excluding the false-positive substring match inside the
unrelated identifier `handleSwarmMessage` in `runtime.go`) found the type
used ONLY inside `internal/a2a/discovery.go` itself — its own definition
plus the inbox functions being extracted. No other file in the module
referenced `a2a.SwarmMessage` as a named type (verified with
`grep -rn 'a2a\.SwarmMessage'` across the whole `swarm-sdk` tree: zero
matches). Since moving it created no ripple, `SwarmMessage`'s struct
definition now lives here as `inbox.Message` (the "Inbox"/"Swarm" prefix
dropped as redundant inside this package, per the brief's allowance for
idiomatic in-package naming), and `internal/a2a/discovery.go` keeps
`a2a.SwarmMessage` alive as a **type alias**:

```go
type SwarmMessage = inbox.Message
```

A type alias (not a redefinition/copy) means `a2a.SwarmMessage` and
`inbox.Message` are the exact same type to the Go compiler — so
`ReadInbox`'s return type `[]SwarmMessage` is directly assignable
to/from `[]inbox.Message` with no conversion, and every existing caller's
field access, JSON (un)marshaling, and struct literals continue to work
unchanged.

This resolves the seam more cleanly than the "leave `SwarmMessage` in
`internal/a2a`, have `internal/inbox` import it back" fallback the
CONTRACT anticipated as the default: `internal/inbox` ends up importing
**nothing** from `internal/a2a` (verified: `go list -deps ./internal/inbox
| grep swarm-sdk` prints only `internal/inbox` itself), which is a
strictly narrower, cycle-proof dependency graph than the anticipated
one-directional `inbox -> a2a` exception. `internal/a2a` importing
`internal/inbox` (the reverse direction, for the thin wrapper functions)
was confirmed safe by `go build ./...` succeeding and by inspecting every
file in `internal/a2a` for any `import "github.com/Swarm-Code/mono/swarm-sdk/internal/inbox"`
occurrence in the OTHER direction (there is none — only
`discovery.go` imports `internal/inbox`, and nothing in `internal/inbox`
imports `internal/a2a`, so there is no cycle to even reason carefully
about).

## Shared filesystem primitives: constructor injection, not duplication

`internal/a2a/discovery.go` owns a set of registry-wide filesystem
security chokepoints — `secureDir`, `resolveRegistryPath`,
`withRegistryHandle` (per-path process-local mutex + stable cross-process
OS lock), `safeReadFile` (symlink/ownership-safe read),
`registryReadFile`/`registryRemove` (unix/windows-specific raw
read/remove), `writeFileAtomicLocked`/`writeFileAtomic` (temp-file +
same-directory-rename, the second variant also acquiring its own lock),
and `isUnsafeEntryError`. These are used well beyond inbox messaging
(peer presence files in `JoinSwarm`/`LeaveSwarm`, LAN registry ingestion
in `lan_registry.go`), so per CONTRACT.md's explicit instruction ("if
shared, they stay in internal/a2a and internal/inbox takes them as
constructor parameters/interfaces rather than duplicating them"), they
were **not** copied into this package.

Instead, `inbox.go` defines a `FileSystem` interface with one method per
primitive inbox actually needs, and `NewStore(fs FileSystem) *Store`
takes an implementation by constructor injection. `internal/a2a/discovery.go`
defines a zero-field adapter type `registryFS` whose methods each call
straight through to the package's existing unexported functions
(`func (registryFS) SecureDir(dir string) error { return secureDir(dir) }`,
etc.), and constructs the package-level `var inboxStore =
inbox.NewStore(registryFS{})` used by the three wrapper functions. This
keeps `internal/inbox` filesystem-implementation-agnostic (its own tests
use a much simpler `testFS` backed directly by `os.*` — see
`inbox_test.go` — with no dependency on `internal/a2a`'s security
internals) while still routing every real write through
`internal/a2a`'s hardened chokepoints in production.

## Preserved-but-still-unused helpers (exact parity with the pre-extraction file)

`readLegacyArrayInbox`, `writeNDJSONInbox`, and `maybeCompactInbox` were
already dead code in `internal/a2a/discovery.go` before this extraction —
`SendMessage`/`ReadInbox` did (and still do) their own inline legacy-array
detection and inline truncation on every write, never calling these three
helpers. They are ported here verbatim (as unexported `*Store` methods)
rather than dropped, to preserve exact byte-for-byte behavioral parity and
because they remain independently useful/testable (see
`TestReadLegacyArrayInbox_Standalone` and
`TestMaybeCompactInbox_CompactsAboveThreshold` in `inbox_test.go`, which
cover them directly even though the production call path still doesn't
invoke them — matching the original file's structure exactly).

## Files

- `inbox.go` — `Message` type, `MaxMessages`/`CompactionThreshold`
  constants, `FileSystem` interface, `Store` type and its
  `SendMessage`/`ReadInbox`/`ClearInbox` methods, plus the
  preserved-but-unused `readLegacyArrayInbox`/`writeNDJSONInbox`/
  `maybeCompactInbox` helpers and the package-level `parseNDJSONLines`.
- `inbox_test.go` — NDJSON round-trip, legacy-array read/upgrade-on-write,
  `MaxMessages` truncation (FIFO eviction), `ClearInbox` file removal,
  never-sent-to empty-read, concurrent-`SendMessage`-to-the-same-handle
  safety (proves no lost writes under `-race`), and direct coverage of the
  two preserved-but-unused legacy/compaction helpers.

**Entry:** `inbox.go:L1`

## Scope (per CONTRACT.md's deferred-scope list)

This phase is **extraction and reorganization only** — moving existing
code to a leaf package and reducing `discovery.go` to thin wrappers. It
deliberately does **not**:

- change queue semantics (still a per-recipient flat file, still
  poll-read via `ReadInbox`, still capped at `MaxMessages` with FIFO
  eviction — identical to the pre-extraction behavior);
- attempt "inbox retirement" in the broader sense TaskManage's full task
  list describes (e.g. replacing polling-based inbox reads with push
  delivery over the presentation/engine control planes) — that is
  explicitly out of scope this phase and is not attempted here;
- add new locking, new compaction triggers, or wire the previously-dead
  `maybeCompactInbox` into the production `SendMessage` path — the
  inline-truncation-on-every-write behavior is preserved exactly as it
  was, not "fixed" or "improved" during the move.

Any future phase that wants push-delivery or queue-semantics changes
should treat this package's `Store`/`FileSystem` split as the extension
point (e.g. a new push-capable implementation could satisfy a broader
interface built on top of `FileSystem`), but that work is not started
here.
