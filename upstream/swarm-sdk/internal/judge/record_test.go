package judge

import "testing"

// These tests are themselves honest probes: each asserts a specific Reduce
// outcome and would turn red if the verdict precedence regressed. There are no
// mocks — Reduce is pure logic exercised against real JudgeRecord fixtures.

func TestReduce_HelperWhenNotTestFunc(t *testing.T) {
	r := &JudgeRecord{Deterministic: Deterministic{IsTestFunc: false}}
	Reduce(r)
	if r.Verdict != VerdictHelper {
		t.Fatalf("non-test func: got %q, want %q (reason: %s)", r.Verdict, VerdictHelper, r.Reason)
	}
}

func TestReduce_MutationSurvivedOverridesEverything(t *testing.T) {
	// Even with assertions and a positive LLM verdict, a surviving mutant means
	// the test does not catch broken code -> fake. Mutation is authoritative.
	r := &JudgeRecord{
		Deterministic: Deterministic{IsTestFunc: true, Assertions: 5, TouchesRealCode: true},
		LLM:           &LLM{VerifiesBehavior: 3, WouldFailIfBroken: true},
		Mutation:      &Mutation{Attempted: 4, Killed: 3, Survived: 1},
	}
	Reduce(r)
	if r.Verdict != VerdictFake {
		t.Fatalf("surviving mutant: got %q, want %q", r.Verdict, VerdictFake)
	}
}

func TestReduce_MutationAllKilledIsReal(t *testing.T) {
	// Mutation killed all mutants: empirical proof the test fails on broken code.
	r := &JudgeRecord{
		Deterministic: Deterministic{IsTestFunc: true, Assertions: 1, TouchesRealCode: true},
		Mutation:      &Mutation{Attempted: 3, Killed: 3, Survived: 0},
	}
	Reduce(r)
	if r.Verdict != VerdictReal {
		t.Fatalf("all mutants killed: got %q, want %q", r.Verdict, VerdictReal)
	}
}

func TestReduce_ErroredMutationIsNotASignal(t *testing.T) {
	// A mutation that errored (build failure/timeout) must not be read as a
	// pass; we fall through to deterministic evidence. Here zero assertions.
	r := &JudgeRecord{
		Deterministic: Deterministic{IsTestFunc: true, Assertions: 0, TouchesRealCode: true},
		Mutation:      &Mutation{Attempted: 2, Error: "build failed in temp tree"},
	}
	Reduce(r)
	if r.Verdict != VerdictFake {
		t.Fatalf("errored mutation + no assertions: got %q, want %q", r.Verdict, VerdictFake)
	}
}

func TestReduce_DeterministicDisqualifiers(t *testing.T) {
	cases := []struct {
		name string
		det  Deterministic
	}{
		{"empty body", Deterministic{IsTestFunc: true, EmptyBody: true}},
		{"unconditional skip", Deterministic{IsTestFunc: true, Skipped: true, Assertions: 2, TouchesRealCode: true}},
		{"tautology", Deterministic{IsTestFunc: true, Tautology: true, Assertions: 1, TouchesRealCode: true}},
		{"zero assertions", Deterministic{IsTestFunc: true, Assertions: 0, TouchesRealCode: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &JudgeRecord{Deterministic: c.det}
			Reduce(r)
			if r.Verdict != VerdictFake {
				t.Fatalf("%s: got %q, want %q (reason: %s)", c.name, r.Verdict, VerdictFake, r.Reason)
			}
		})
	}
}

func TestReduce_DelegatedAssertionNoInlineIsWeak(t *testing.T) {
	// A test that passes its *testing.T to a contract helper delegates its
	// assertions; zero inline assertions must NOT be read as fake.
	r := &JudgeRecord{Deterministic: Deterministic{
		IsTestFunc: true, Assertions: 0, DelegatesAssertion: true, TouchesRealCode: true,
	}}
	Reduce(r)
	if r.Verdict != VerdictWeak {
		t.Fatalf("delegated assertion: got %q, want %q (reason: %s)", r.Verdict, VerdictWeak, r.Reason)
	}
}

func TestReduce_ConcurrencyProbeNoAssertIsWeak(t *testing.T) {
	// A goroutine/-race probe with no t.Error is verified by the race detector,
	// not a static assertion. Must be weak, not fake.
	r := &JudgeRecord{Deterministic: Deterministic{
		IsTestFunc: true, Assertions: 0, ConcurrencyProbe: true, TouchesRealCode: true,
	}}
	Reduce(r)
	if r.Verdict != VerdictWeak {
		t.Fatalf("concurrency probe: got %q, want %q (reason: %s)", r.Verdict, VerdictWeak, r.Reason)
	}
}

func TestReduce_MockOnlyIsWeakNotFakeWithoutLLM(t *testing.T) {
	// mock-only is a smell, not proof: TouchesRealCode is unreliable across
	// external test packages, so static evidence alone must not hard-fail it.
	r := &JudgeRecord{Deterministic: Deterministic{IsTestFunc: true, MockOnly: true, Assertions: 3}}
	Reduce(r)
	if r.Verdict != VerdictWeak {
		t.Fatalf("mock-only without LLM: got %q, want %q (reason: %s)", r.Verdict, VerdictWeak, r.Reason)
	}
}

func TestReduce_MockOnlyFakeWhenLLMConfirms(t *testing.T) {
	r := &JudgeRecord{
		Deterministic: Deterministic{IsTestFunc: true, MockOnly: true, Assertions: 3},
		LLM:           &LLM{MockOveruse: true, WouldFailIfBroken: false},
	}
	Reduce(r)
	if r.Verdict != VerdictFake {
		t.Fatalf("mock-only confirmed by LLM: got %q, want %q", r.Verdict, VerdictFake)
	}
}

func TestReduce_AllowSkipReasonRescuesSkip(t *testing.T) {
	// A skip WITH an audited reason is legitimate (e.g. platform-gated) and must
	// not be marked fake when the test otherwise asserts real behavior.
	r := &JudgeRecord{Deterministic: Deterministic{
		IsTestFunc: true, Skipped: true, AllowSkipReason: "windows-only path",
		Assertions: 2, TouchesRealCode: true,
	}}
	Reduce(r)
	if r.Verdict == VerdictFake {
		t.Fatalf("allow-skip reason should rescue skip, got fake (reason: %s)", r.Reason)
	}
}

func TestReduce_LLMDowngradesToWeak(t *testing.T) {
	// Passes deterministic checks but the LLM judges it would not fail if broken.
	r := &JudgeRecord{
		Deterministic: Deterministic{IsTestFunc: true, Assertions: 2, TouchesRealCode: true},
		LLM:           &LLM{VerifiesBehavior: 1, WouldFailIfBroken: false},
	}
	Reduce(r)
	if r.Verdict != VerdictWeak {
		t.Fatalf("llm negative: got %q, want %q", r.Verdict, VerdictWeak)
	}
}

func TestReduce_RealWhenAllSignalsPositive(t *testing.T) {
	r := &JudgeRecord{
		Deterministic: Deterministic{IsTestFunc: true, Assertions: 4, TouchesRealCode: true},
		LLM:           &LLM{VerifiesBehavior: 3, WouldFailIfBroken: true},
	}
	Reduce(r)
	if r.Verdict != VerdictReal {
		t.Fatalf("all positive: got %q, want %q", r.Verdict, VerdictReal)
	}
}

func TestDeterministic_Suspicious(t *testing.T) {
	if (Deterministic{IsTestFunc: false}).Suspicious() {
		t.Error("helper should not be suspicious")
	}
	if !(Deterministic{IsTestFunc: true, Assertions: 0, TouchesRealCode: true}).Suspicious() {
		t.Error("zero-assertion test should be suspicious")
	}
	if !(Deterministic{IsTestFunc: true, Assertions: 3, MockOnly: true}).Suspicious() {
		t.Error("mock-only test should be suspicious")
	}
	if (Deterministic{IsTestFunc: true, Assertions: 3, TouchesRealCode: true}).Suspicious() {
		t.Error("asserting real-code test should not be suspicious")
	}
}

func TestMutation_Ran(t *testing.T) {
	if (&Mutation{Attempted: 0}).Ran() {
		t.Error("zero attempts should not count as ran")
	}
	if (&Mutation{Attempted: 2, Error: "x"}).Ran() {
		t.Error("errored mutation should not count as ran")
	}
	if !(&Mutation{Attempted: 2}).Ran() {
		t.Error("clean attempt should count as ran")
	}
	var nilM *Mutation
	if nilM.Ran() {
		t.Error("nil mutation should not count as ran")
	}
}
