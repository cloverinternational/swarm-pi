package client

import (
	"context"
	"testing"
)

// eventCount reads the recorded event count under the lock.
func (f *fakeAuditor) eventCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// TestHostStateSurvivesConsecutiveHotApplies is the regression test for the
// defect found while verifying Phase 11b: harness_apply.go rebuilt the
// harnessConstruction on a successful hot apply as {plan, allowYolo} only,
// then assigned it over c.opts — silently discarding the HOST-supplied
// auditor (Phase 11b) and bindings (Phase 9d).
//
// Every single-apply test passed, because the first apply reads host state
// that is still intact. The bug only appears on the SECOND apply. That is the
// worst possible shape for an audit defect: the log works exactly long enough
// to be trusted, then goes dark on every subsequent privileged change.
//
// Before the fix this test fails at the second apply with 1 event, not 2.
func TestHostStateSurvivesConsecutiveHotApplies(t *testing.T) {
	dir := t.TempDir()
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(compilePlanIn(t, dir, baseApplyManifest)), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	manifestWithPrompt := func(p string) string {
		return `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "` + p + `"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	}

	for i, prompt := range []string{"PROMPT-HOT-ONE", "PROMPT-HOT-TWO"} {
		plan := compilePlanIn(t, dir, manifestWithPrompt(prompt))
		res, err := c.ApplyHarnessPlanAudited(context.Background(), plan, ApplyHarnessOptions{})
		if err != nil {
			t.Fatalf("apply #%d: %v", i+1, err)
		}
		if len(res.Applied) == 0 {
			t.Fatalf("apply #%d: expected a hot apply, got zero Applied fields", i+1)
		}
		if got, want := fa.eventCount(), i+1; got != want {
			t.Fatalf("after apply #%d: got %d audit event(s), want %d — "+
				"the host-supplied auditor was dropped by the previous apply", i+1, got, want)
		}
	}

	// The auditor must still be reachable through the accessor after both
	// applies, not merely have fired via a value read before the apply ran.
	if c.harnessSuppliedAuditor() == nil {
		t.Error("harnessSuppliedAuditor() == nil after two hot applies; host state was discarded")
	}
}
