# attachclient

**1. Responsibility.** Implement the thin-client half of attach: resolve a
daemon, negotiate contract versions, establish a client session, select
workspace/conversation, issue RPC, consume/replay events, handle
approval/cancel, and detach without stopping the daemon. It is NOT a
second orchestrator, NOT a JSON-RPC method registry (that is `serve.Mux`),
and NEVER starts, stops, or supervises a daemon process.

**2. Owned state / persistence.** Client-session identity (`Session`),
negotiated capabilities, per-`Impl` selected workspace/conversation
(`Selection`), an acknowledged event `Cursor`, reconnect/backoff state
(`BackoffPolicy`), in-flight `Submit`/`Cancel` correlation
(`ExecutionID`), and an optional, namespaced, **non-authoritative**
`CursorStore` bookmark (default: in-memory, process-local
`memCursorStore`). It owns no durable engine state, no event retention (the
daemon's `EventLog` is authoritative), and no presence files.

**3. Public entry points / call sequence.** `New(Deps) *Impl` (satisfies
`Client`) then `Connect(ctx, Target)` then `Select`/`Submit`/`Events` then
`Approve`/`Cancel` as needed then `Detach`. `Impl` additionally exposes
`Call` (generic RPC passthrough for control methods outside the mandated
`Client` interface, e.g. mode/model/profile/session listing),
`Selection()`, and `SessionInfo()` for callers/tests that need to observe
local state. The default `Transport` is `NewHTTPTransport(rpcTimeout)`
(net/http + SSE); alternate transports/discoverers/credential sources are
injected via `Deps`.

**4. Allowed dependencies.** Standard library, versioned attach/RPC/event
contract values (interim local types this round — see client.go's package
doc for the `attachcontract` integration note), lifecycle status values,
presence discovery values/interfaces (via the injected `Discoverer`, never
a concrete presence implementation), neutral identity/error values.
**Forbidden dependencies.** `daemon`, `supervisor`, `presentationcontrol`,
concrete `lan`, engine internals, CLI parsing, TUI models, registry files,
OS process control. Verified by `go build`/`go vet` isolation (this
package imports nothing from `swarm-tui`, `daemon`, `supervisor`,
`presentationcontrol`, or `internal/lan`).

**5. Invariants (match test names below).**
1. Selection is session-scoped and cannot mutate another client —
   `TestTwoClients_SelectionIsolation`.
2. Every work item carries canonical session/execution/attempt identity —
   `TestSubmitThenCancelCorrelation_KnownIDReachesDaemonExactlyOnce`.
3. Reconnect resumes from an acknowledged cursor or reports a gap —
   `TestReconnect_ResumesFromAcknowledgedCursor`,
   `TestReconnect_ExplicitGapReported`.
4. Detach releases client resources only (never a daemon-side stop) —
   `TestDetach_ReleasesOnlyClientResources`.
5. Unknown protocol versions and capabilities fail with compatibility
   errors — `TestNegotiate_UnknownMajorRejected`,
   `TestNegotiate_NoOverlapRejected`,
   `TestNegotiate_MissingRequiredCapabilityRejected`,
   `TestConnect_NegotiationMatrix_IncompatibleMajorFails`.
6. Bounded retry: backoff has a ceiling, never retries forever —
   `TestReconnect_BoundedRetryHasCeiling`.
7. RPC calls carry an explicit deadline (never `http.Client{Timeout: 0}`) —
   `TestHTTPTransport_RPCTimeout`.

**6. Concurrency model.** `Impl` guards all mutable fields with one
`sync.Mutex`. Each `Events()` call spawns exactly one `reconnectLoop`
goroutine (reconnect.go), owned by the returned `EventStream.Close` (a
`context.CancelFunc`); `Detach` collects and calls every outstanding
stream's `Close` under the lock, then never touches the network. The
`Events` output channel is buffered (64) and always closed by the owning
goroutine's `defer close(out)` — callers only ever observe close via a
received-zero-value/`ok=false` read, never a panic-on-send.

**7. Error categories.** `*CompatibilityError` (negotiation: malformed
version, unknown major, no overlap, missing capability), `*AuthError`
(401/403), `*RPCError` (JSON-RPC `error` member), `*GapError` (explicit
retained-history-exceeded signal), `ErrDetached`/`ErrNotConnected`
(sentinels). No response body/secret is echoed into an error beyond a
4KiB-bounded, trimmed snippet.

**8. Focused tests / verification.**
`negotiate_test.go` (contract negotiation matrix, `Version` JSON string
encoding, malformed-version rejection), `reconnect_test.go`
(resume-from-cursor, explicit gap, bounded retry, prompt Close
termination, malformed-event decode), `client_test.go` (negotiation
through `Connect`, production-constructor harmless RPC smoke test,
two-client isolation, approval/cancel correlation, detach-does-not-stop,
HTTP timeout/auth-error/malformed-response/SSE round trip).
Run: `go test -count=1 ./attachclient && go test -race -count=1 ./attachclient && go vet ./attachclient`.

**File-size review note (package-boundaries.md's File-size policy).**
`client.go` is ~850 lines, over the 400-500 soft threshold. Single retained
concern: it is the Client interface, its public DTOs, the Impl
implementation, and the default net/http+SSE Transport, all of which this
phase's `# FILES:` header restricts to `client.go`, `cursor.go`,
`reconnect.go`, `negotiate.go` (plus tests/INDEX.md) — no additional
production file (e.g. a separate `transport.go`) could be created this
round without violating that restriction. Splitting the Transport
implementation out would not obscure an invariant, so this is NOT a
permanent "no split" decision: follow-up owner is the next
attachclient-scoped change, which should move `httpTransport`/
`httpEventStream`/`newRawEventFromSSE` into their own `transport.go` file.

**9. Compatibility adapters.** `client.go`'s negotiation/gap types
(`Version`, `VersionRange`, `Capabilities`, `CompatibilityError`,
`GapError`) are INTERIM local definitions, not
`swarm-sdk/internal/attachcontract`'s canonical types — that P04.A-owned
neutral-contract package did not exist/compile in this worktree when this
package was written (see CONTRACT.md). Owner: whichever phase completes
`attachcontract`. Removal gate: once `attachcontract` exports its
versioned value/validation types, replace these local types with type
aliases (or direct imports) in one mechanical change — every field name
and JSON shape here was chosen to match ADR-004 exactly for that reason.
See `docs/architecture/swarm-attach/migration-policy.md`.
