# judge — Test-Honesty Probe & LLM-as-a-Judge Framework

> Swarm SDK · SWA-10 (parent SWA-27) · verification gate SWA-41

## Why this exists

A green test suite is supposed to mean "the behavior is verified." In practice,
tests routinely **pass while the code under test is broken** — they over-mock so
they exercise the mock instead of the real code, assert nothing, assert
constants, or `t.Skip`. We call a test that actually exercises real behavior a
**probe**. This package audits every test in the suite and classifies it as:

| Verdict  | Meaning                                                        |
|----------|---------------------------------------------------------------|
| `real`   | Genuinely exercises and verifies behavior.                    |
| `weak`   | Asserts something but shallow / over-mocked (improve).        |
| `fake`   | Verifies nothing; **passes even when the code is broken**.    |
| `helper` | Not a test (factory, `TestMain`, exported helper) — excluded. |

## Three layers of evidence (kept separate by design)

The anti-trick principle: the LLM is **never** the sole arbiter of a hard fail.
Mechanical facts are decided deterministically; the empirical truth is decided
by mutation; the LLM only judges the fuzzy middle.

1. **Deterministic (`analyzer.go`)** — `go/ast` static signals: assertion count,
   mock-only body, `t.Skip`, tautologies, empty body, whether it touches the
   real package. Cannot be argued with.
2. **LLM (`llmjudge.go`)** — a sub-agent (same factory/executor pattern as the
   autogenskills curator) answers only: *"does this test meaningfully verify the
   behavior, and would it fail if the code regressed?"* Best-effort: a parse
   failure or missing provider yields `nil` and we fall back to the other layers.
3. **Mutation (`mutation.go`)** — the ground truth. Break the **referenced**
   code (negate a bool return, flip a comparison, swap arithmetic) in place,
   re-run the single test, then restore. A test that stays green under mutation
   is `fake`, no matter what Layers 1–2 think.

`Reduce` (`record.go`) combines them with strict precedence: helper → mutation
(authoritative when it ran) → deterministic disqualifiers → LLM downgrade → real.

## Usage

```bash
# Report-only over the whole SDK, with mutation on suspicious tests:
go run ./cmd/test-judge --root . --mutate --report-only --out judge_records.jsonl

# Generate the grandfather baseline of currently-known fakes:
go run ./cmd/test-judge --root . --mutate --write-baseline judge/baseline.json

# Hard gate: fail CI on any NEW fake (not in the baseline):
go run ./cmd/test-judge --root . --mutate --baseline judge/baseline.json --gate
```

Exit codes: `0` clean / report-only · `1` new fake tests · `2` setup error.

### Rolling out the hard gate without freezing the repo

`--write-baseline` snapshots the existing fake-test backlog. The committed gate
then fails only on **new or regressed** fakes (`Baseline.NewFakes`), so the
backlog can be burned down via follow-up issues instead of a 412-file bigbang.
A legitimately skipped test is exempted with an audited directive:

```go
// judge:allow-skip needs a live OpenAI key
func TestThatNeedsNetwork(t *testing.T) { t.Skip("offline") ... }
```

## Baseline: the SDK's current honesty

A full-SDK run (`--write-baseline judge/baseline.json`) classifies **3,539 test
functions**:

| Verdict | Count | Notes |
|---------|------:|-------|
| real    | 2,532 | assert against real code (incl. correctly-gated conditional skips) |
| weak    |   280 | mock-only / concurrency / assertion-delegating — routed to LLM/mutation |
| fake    |     0 | **zero fakes** — the baseline is empty; the gate fails on ANY new fake |
| helper  |   728 | factories / `TestMain` / exported helpers, excluded |

**The fake count is now zero.** `baseline.json` is empty (`{"fakes": []}`), so the
gate fails on *any* test that becomes fake — there is no grandfathered backlog left.

The journey: the judge first flagged 27 fakes = 6 disabled (unconditional `t.Skip`)
+ 21 no-assertion. All 27 are fixed:

- **6 disabled tests** — the 3 MCP HTTP-stream transport tests (`_Connect`,
  `_SSEParsing`, `_Reconnection`) were un-skipped and `_SSEParsing` rewritten to
  drive the real SSE parser through the public `Send`→`Receive` path
  (mutation-verified). The 2 conversation context-window tests tested a removed
  feature and were replaced with positive tests asserting the *current* contract
  (full history preserved on round-trip). `TestLiveExtendedThinking`'s
  unconditional skip became a conditional model-capability check plus real
  assertions on the thinking output.
- **21 no-assertion tests** — each got a real assertion: no-op/idempotency tests
  now assert the no-op actually happened (e.g. record dropped after Close,
  existing items unchanged after removing a nonexistent one); compile-time
  interface checks keep a package-level `var _ Iface = (*T)(nil)` AND add a
  runtime dynamic type assertion; "returns empty / no error" tests were
  reconstructed with real storage so the empty path is genuinely exercised;
  "report" loops gained sound invariant assertions on the computed stats; and
  stub tests (e.g. `TestTranslateTools`, `TestSubagentTool_ParameterPrecedence`)
  now call the real function and assert its output. Making these honest also
  surfaced a real heuristic bug (recap `extractModifiedFiles` misses "Modified"/
  "Updated") and several pre-existing compaction failures — exactly what honest
  tests are supposed to expose.

### Avoiding false positives (hard-won)

Auditing a real suite surfaced patterns that pure static analysis must NOT
hard-fail:

- **Conditional skips** (`if testing.Short() { t.Skip() }`, `if env == ""`)
  are legitimate gating patterns — they are NOT fake. Only unconditional skips
  (no surrounding `if`/`switch`/`select`) are `fake`.
- **mock-only / no-`TouchesRealCode`** is unreliable for external test packages
  (`package foo_test`), local `newXxx(t,...)` helper indirection, and legitimate
  provider doubles (`providermock`). These are downgraded to `weak`; only the LLM
  (or mutation) can promote them to `fake`.
- **concurrency probes** (goroutines + `-race`) have no `t.Error` because the
  race detector is the assertion → `weak`, never `fake`.

Only empty body, unconditional skip, tautology, and zero-assertion
non-concurrency tests are `fake` on static evidence alone.

## Correctness: mutations are scoped to referenced symbols

Mutation only ever breaks code the test actually references
(`MutationConfig.TargetSymbols`, populated from the analyzer's `RealSymbols`).
Mutating unrelated code would always "survive" and produce **false-positive**
`fake` verdicts — an early conductor run flagged 8 honest tests this way before
scoping was added. With no resolvable target, mutation returns *no signal*
(`Mutation.Error`) and `Reduce` falls back to Layers 1–2 rather than lying.

## Known gaps (SWA-41)

- **`export_test.go` shims:** tests that reach unexported code through an
  in-package export shim (e.g. `BackoffDelaysForTest` wrapping `backoffDelay`)
  are not name-resolved to the underlying unexported symbol, so mutation gets no
  target and falls back to deterministic judgement. A future pass can map shim →
  underlying target.
- **LLM layer** is wired but defaults to `NoopJudge` in `cmd/test-judge` so CI
  runs without provider credentials; plug an `SDKJudge` in when a provider is
  configured.
- Mutation operator set is intentionally small (3 high-signal operators); expand
  as needed.

## Files

| File            | Responsibility                                             |
|-----------------|------------------------------------------------------------|
| `record.go`     | `JudgeRecord` schema + `Reduce` verdict precedence.        |
| `analyzer.go`   | Layer 1 AST signals.                                       |
| `llmjudge.go`   | Layer 2 LLM judge (`Judge` iface, `SDKJudge`, `NoopJudge`).|
| `mutation.go`   | Layer 3 scoped mutation testing.                           |
| `batch.go`      | Package/tree runner, summary, baseline allowlist diff.     |
| `cmd/test-judge`| CLI entrypoint, JSONL output, gate.                        |

Every file ships with honest probes (`*_test.go`) — including a keystone
mutation test that builds a throwaway module with one real and one fake test and
asserts the engine kills the former's mutants and lets the latter's survive.
