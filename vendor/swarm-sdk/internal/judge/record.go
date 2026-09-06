// Package judge audits the honesty of a Go test suite.
//
// The motivating problem: tests can pass while the code under test is broken.
// Agents (and humans) game test suites by over-mocking so the test exercises
// the mock instead of the real code, writing trivial or tautological
// assertions, skipping tests, or asserting nothing at all. A green suite then
// means "nobody noticed," not "the behavior is verified."
//
// A "probe" is a test that actually exercises real behavior. This package
// classifies every test into one of four verdicts by combining three
// independent layers of evidence:
//
//   - Layer 1 (Deterministic): pure AST signals that cannot be argued with —
//     assertion count, mock-only bodies, t.Skip, tautologies, empty bodies.
//   - Layer 2 (LLM): a model judges the non-deterministic question, "does this
//     test meaningfully verify the behavior and would it fail if the code
//     broke?"
//   - Layer 3 (Mutation): the empirical ground truth — break the target code in
//     an isolated copy and re-run the test; a test that stays green under
//     mutation is fake, no matter what Layers 1 and 2 think.
//
// record.go defines the shared schema (JudgeRecord) and the Reduce function
// that combines the three layers into a single Verdict. It has no external
// dependencies so it compiles and tests standalone.
package judge

// Verdict is the final honesty classification of a single test function.
type Verdict string

const (
	// VerdictReal means the test genuinely exercises and verifies behavior.
	VerdictReal Verdict = "real"
	// VerdictWeak means the test asserts something but is shallow, over-mocked,
	// or otherwise of low value — it should be improved but is not a hard fail.
	VerdictWeak Verdict = "weak"
	// VerdictFake means the test provides no real verification: it has no
	// assertions, only exercises mocks, is tautological, or — most damningly —
	// stays green when the target code is mutated. This is a gate failure.
	VerdictFake Verdict = "fake"
	// VerdictHelper means the analyzed function is not actually a test (e.g. a
	// contract factory, a TestMain, or an exported helper used by other
	// packages). Helpers are excluded from honesty scoring and never fail the
	// gate.
	VerdictHelper Verdict = "helper"
)

// Deterministic holds the Layer 1 signals extracted statically from the AST.
// Every field is a mechanical fact about the test's source; no judgement.
type Deterministic struct {
	// IsTestFunc reports whether the function is a real `func TestXxx(t *testing.T)`.
	// When false the record is a helper and skips honesty scoring.
	IsTestFunc bool `json:"is_test_func"`
	// Assertions counts assertion sites: t.Error/Fatal(f), require./assert.,
	// and explicit `if got != want` comparisons that lead to a failure call.
	Assertions int `json:"assertions"`
	// MockOnly is true when the body references only mock/fake/stub identifiers
	// and never the real package under test.
	MockOnly bool `json:"mock_only"`
	// TouchesRealCode is true when the body references a symbol from the
	// non-test package under test (not just mocks/stdlib/testing).
	TouchesRealCode bool `json:"touches_real_code"`
	// Skipped is true when the body calls t.Skip / t.Skipf unconditionally.
	Skipped bool `json:"skipped"`
	// Tautology is true when an assertion compares two constants or a value to
	// itself (e.g. assert.Equal(t, 1, 1)).
	Tautology bool `json:"tautology"`
	// EmptyBody is true when the function body has no statements.
	EmptyBody bool `json:"empty_body"`
	// ConcurrencyProbe is true when the test launches goroutines / uses
	// sync primitives. Such tests often have no explicit assertion because the
	// -race detector IS the assertion; they must not be hard-failed for "no
	// assertions" on static evidence alone.
	ConcurrencyProbe bool `json:"concurrency_probe"`
	// DelegatesAssertion is true when the test passes its *testing.T to a helper
	// (e.g. ContractTest_All(t, factory)); the assertions live in the callee, so
	// a zero inline-assertion count does not mean the test verifies nothing.
	DelegatesAssertion bool `json:"delegates_assertion"`
	// AllowSkipReason carries the audited reason from a `// judge:allow-skip`
	// directive, if present. A skip with a reason is not penalized.
	AllowSkipReason string `json:"allow_skip_reason,omitempty"`
}

// LLM holds the Layer 2 result. A nil *LLM means the model was not run or its
// output failed to parse; Reduce then falls back to deterministic evidence and
// never crashes.
type LLM struct {
	// VerifiesBehavior is the model's 0-3 score: 0 = verifies nothing,
	// 3 = thoroughly verifies the behavior it claims to test.
	VerifiesBehavior int `json:"verifies_behavior"`
	// WouldFailIfBroken is the model's judgement of whether the test would turn
	// red if the target code regressed.
	WouldFailIfBroken bool `json:"would_fail_if_broken"`
	// MockOveruse is the model's judgement that mocking has hollowed out the
	// test (it tests the mock, not the code).
	MockOveruse bool `json:"mock_overuse"`
	// Reasoning is a short human-readable justification.
	Reasoning string `json:"reasoning"`
}

// Mutation holds the Layer 3 result. A nil *Mutation means mutation testing was
// not run for this test (e.g. it was not flagged suspicious, or mutation was
// disabled).
type Mutation struct {
	// Attempted is the number of mutants applied to the target code.
	Attempted int `json:"attempted"`
	// Killed is the number of mutants that caused the test to fail (good).
	Killed int `json:"killed"`
	// Survived is the number of mutants the test failed to catch (bad). Any
	// survivor against a test that claims to cover the mutated code is strong
	// evidence the test is fake.
	Survived int `json:"survived"`
	// TargetSymbol is the symbol that was mutated, for traceability.
	TargetSymbol string `json:"target_symbol,omitempty"`
	// Error carries a non-empty message when mutation could not run (build
	// failure in the temp tree, timeout); Reduce treats an errored mutation as
	// "no signal" rather than a pass or fail.
	Error string `json:"error,omitempty"`
}

// Ran reports whether the mutation layer produced a usable signal.
func (m *Mutation) Ran() bool {
	return m != nil && m.Error == "" && m.Attempted > 0
}

// JudgeRecord is the full audit record for a single test function. The three
// evidence layers are kept separate so a reader can always see WHY a verdict
// was reached and which layer drove it.
type JudgeRecord struct {
	Package  string `json:"package"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	TestName string `json:"test_name"`

	Deterministic Deterministic `json:"deterministic"`
	LLM           *LLM          `json:"llm,omitempty"`
	Mutation      *Mutation     `json:"mutation,omitempty"`

	// Verdict is the reduced classification (populated by Reduce).
	Verdict Verdict `json:"verdict"`
	// Reason is a short explanation of which layer drove the verdict.
	Reason string `json:"reason"`
}

// Suspicious reports whether the deterministic layer alone gives reason to
// distrust the test. The batch runner uses this to decide which tests are
// worth the expense of the LLM and mutation layers.
func (d Deterministic) Suspicious() bool {
	if !d.IsTestFunc {
		return false // helpers are not suspicious, they are excluded
	}
	if d.EmptyBody || d.Tautology || d.MockOnly {
		return true
	}
	if d.Skipped && d.AllowSkipReason == "" {
		return true
	}
	if d.Assertions == 0 {
		return true
	}
	if !d.TouchesRealCode {
		return true
	}
	return false
}

// Reduce combines the three evidence layers into a final Verdict, following a
// strict precedence so the result is deterministic and explainable:
//
//  1. Not a test func        -> helper   (excluded from scoring)
//  2. Mutation survived      -> fake     (empirical: code broke, test stayed green)
//  3. Mutation killed (clean) -> real    (empirical: code broke, test caught it)
//  4. No mutation signal, deterministic disqualifiers -> fake
//     (empty body, tautology, unconditional skip, mock-only, zero assertions)
//  5. No mutation signal, LLM says it would not fail / overuses mocks -> weak
//  6. Otherwise               -> real
//
// Mutation, when it ran, is authoritative because it is the only layer that
// directly measures the property we care about: "does this test fail when the
// code is wrong?"
func Reduce(r *JudgeRecord) {
	d := r.Deterministic

	// (1) Helpers are not tests.
	if !d.IsTestFunc {
		r.Verdict = VerdictHelper
		r.Reason = "not a test function (helper/contract/TestMain)"
		return
	}

	// (2)/(3) Mutation is empirical ground truth when it ran.
	if r.Mutation.Ran() {
		if r.Mutation.Survived > 0 {
			r.Verdict = VerdictFake
			r.Reason = "mutation survived: target code was broken but the test stayed green"
			return
		}
		// All mutants killed: the test demonstrably fails when the code breaks.
		r.Verdict = VerdictReal
		r.Reason = "mutation killed: test fails when the target code is broken"
		return
	}

	// (4) Deterministic disqualifiers that are fake REGARDLESS of package
	// structure — these cannot be false positives from cross-package symbol
	// resolution, because they are about the test's own shape.
	switch {
	case d.EmptyBody:
		r.Verdict = VerdictFake
		r.Reason = "empty test body: verifies nothing"
		return
	case d.Skipped && d.AllowSkipReason == "":
		r.Verdict = VerdictFake
		r.Reason = "unconditional t.Skip with no // judge:allow-skip reason"
		return
	case d.Tautology:
		r.Verdict = VerdictFake
		r.Reason = "tautological assertion (compares constants / value to itself)"
		return
	case d.Assertions == 0:
		// A concurrency probe with no explicit assertion is verified by the
		// -race detector, not by t.Error. Treat as weak (worth review) rather
		// than fake.
		if d.ConcurrencyProbe {
			r.Verdict = VerdictWeak
			r.Reason = "concurrency probe with no explicit assertion (relies on -race detector)"
			return
		}
		// A test that hands its *testing.T to a helper delegates its assertions
		// to that callee (e.g. a shared contract suite). Not a no-op.
		if d.DelegatesAssertion {
			r.Verdict = VerdictWeak
			r.Reason = "no inline assertions but delegates *testing.T to a helper (assertions in callee)"
			return
		}
		r.Verdict = VerdictFake
		r.Reason = "no assertions: cannot fail, verifies nothing"
		return
	}

	// (4b) Soft smells: mock-only is NOT hard-failed on static evidence alone.
	// TouchesRealCode is unreliable for external test packages and helper
	// indirection (a test may reach real code via a local newXxx() helper while
	// only `mock` identifiers appear inline). We downgrade to weak and let the
	// LLM/mutation layers make the real call.
	if d.MockOnly {
		if r.LLM != nil && r.LLM.MockOveruse {
			r.Verdict = VerdictFake
			r.Reason = "mock-only confirmed by llm: exercises mocks, not real code"
			return
		}
		r.Verdict = VerdictWeak
		r.Reason = "mock-only smell: assertions present but no real-code reference detected (needs LLM/mutation confirmation)"
		return
	}

	// (5) LLM judgement for the non-deterministic quality question.
	if r.LLM != nil {
		if !r.LLM.WouldFailIfBroken || r.LLM.MockOveruse || r.LLM.VerifiesBehavior <= 1 {
			r.Verdict = VerdictWeak
			r.Reason = "llm: shallow/over-mocked test of limited verification value"
			return
		}
	}

	// (6) Default: passes deterministic checks; treated as real. If the test
	// does not touch real code it was already flagged at (4)/Suspicious, so
	// reaching here means it asserts against real behavior.
	r.Verdict = VerdictReal
	if r.LLM != nil {
		r.Reason = "asserts against real code; llm confirms meaningful verification"
	} else {
		r.Reason = "asserts against real code (deterministic only)"
	}
}
