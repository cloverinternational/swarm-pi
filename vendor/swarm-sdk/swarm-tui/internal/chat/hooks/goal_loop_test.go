package hooks

import (
	"context"
	"testing"

	builtin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
)

// TestGoalEvaluationFiresOnAgentStopped verifies the whole /goal evaluation
// chain: SetGoal → EmitAgentStopped → GoalHook.OnEvent → evaluator → state
// transition. This is the chain the TUI's loop driver (maybeContinueGoal)
// gates on — if the state never leaves not_yet_evaluated, the loop is dead.
func TestGoalEvaluationFiresOnAgentStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	hm := NewHooksManager(nil, nil, t.TempDir())
	gh := hm.GetGoalHook()
	if gh == nil {
		t.Fatal("GoalHook not registered")
	}

	// Stub evaluator: not met until the transcript contains "done".
	var evaluations int
	hm.EnableGoalLLMEvaluator(func(_ context.Context, condition, transcript string) (builtin.GoalResult, error) {
		evaluations++
		if condition == "" {
			t.Error("evaluator received empty condition")
		}
		met := transcript != "" && containsStr(transcript, "done")
		reason := "still working"
		if met {
			reason = "transcript says done"
		}
		return builtin.GoalResult{Ok: met, Reason: reason}, nil
	})

	if err := gh.SetGoal("the task is done"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}

	// Turn 1: unmet — state must become active with the evaluator's reason.
	hm.EmitAgentStopped(context.Background(), "sess-1", "stop", "do the thing", "I am working on it")

	g := gh.GetGoal()
	if g == nil {
		t.Fatal("goal vanished after first turn")
	}
	if evaluations == 0 {
		t.Fatal("evaluator never ran — EmitAgentStopped did not reach GoalHook (event type / registration mismatch)")
	}
	if g.State != builtin.GoalStateActive {
		t.Fatalf("after unmet evaluation state = %q, want %q (loop driver gates on this)", g.State, builtin.GoalStateActive)
	}
	if g.LastReason != "still working" {
		t.Errorf("LastReason = %q, want evaluator reason", g.LastReason)
	}
	if g.Iterations != 1 {
		t.Errorf("Iterations = %d, want 1", g.Iterations)
	}

	// Turn 2: met — state must become met.
	hm.EmitAgentStopped(context.Background(), "sess-1", "stop", "finish it", "It is done now")

	g = gh.GetGoal()
	if g.State != builtin.GoalStateMet {
		t.Fatalf("after met evaluation state = %q, want %q", g.State, builtin.GoalStateMet)
	}

	// Turn 3: met goals must not re-evaluate.
	before := evaluations
	hm.EmitAgentStopped(context.Background(), "sess-1", "stop", "anything", "anything")
	if evaluations != before {
		t.Error("met goal was re-evaluated on a later turn")
	}
}

func TestGoalEvaluationPrefersFullTurnTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	hm := NewHooksManager(nil, nil, t.TempDir())
	gh := hm.GetGoalHook()
	var received string
	hm.EnableGoalLLMEvaluator(func(_ context.Context, _, transcript string) (builtin.GoalResult, error) {
		received = transcript
		return builtin.GoalResult{Ok: true, Reason: "tool evidence observed"}, nil
	})
	if err := gh.SetGoal("targeted tests pass"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}

	full := "User: run tests\n\nAssistant tool call: Bash\n\nTool result (Bash, succeeded):\nok package"
	hm.EmitAgentStopped(context.Background(), "sess-1", "stop", "run tests", "done", full)

	if received != full {
		t.Fatalf("evaluator received legacy prompt/response instead of full transcript:\n%s", received)
	}
}

func containsStr(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		}())
}
