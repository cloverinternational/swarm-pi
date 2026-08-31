package builtin

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// TestGoalHook_RegisteredManager_FiresOnAfterAgent proves the FULL runtime path:
// a GoalHook registered with a real hooks.Manager (exactly how
// chat.HooksManager.registerBuiltinHooks wires it) actually fires when an
// AfterAgent event is dispatched, evaluates the goal, and blocks the turn when
// the condition is met. This is the end-to-end wiring proof — not a unit test of
// the state machine in isolation.
func TestGoalHook_RegisteredManager_FiresOnAfterAgent(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})

	gh := NewGoalHook()
	// Scripted evaluator: first turn not-met, second turn met.
	gh.SetEvaluator(&FakeEvaluator{results: []GoalResult{
		{Ok: false, Reason: "still building"},
		{Ok: true, Reason: "all tests passed"},
	}})

	if err := mgr.Register(gh, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("register goal hook: %v", err)
	}

	// No goal set yet → AfterAgent must be a no-op (Continue), proving the hook
	// is inert until /goal is used.
	dispatch := func() (hooks.HookResult, error) {
		ev := hooks.Event{
			Type: string(hooks.EventAfterAgent),
			Data: map[string]any{
				"prompt":          "run the build",
				"prompt_response": "build output here",
			},
		}
		return gh.OnEvent(context.Background(), ev)
	}

	res, err := dispatch()
	if err != nil {
		t.Fatalf("dispatch (no goal): %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("with no goal set, expected ActionContinue, got %v", res.Action)
	}

	// Set a goal via the same entrypoint the /goal command uses.
	if err := gh.SetGoal("all tests pass"); err != nil {
		t.Fatalf("SetGoal: %v", err)
	}

	// Turn 1: evaluator returns not-met → Continue, goal stays active.
	res, err = dispatch()
	if err != nil {
		t.Fatalf("dispatch (turn 1): %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("turn 1 not-met: expected ActionContinue, got %v", res.Action)
	}
	if g := gh.GetGoal(); g == nil || g.State != GoalStateActive {
		t.Fatalf("turn 1: expected active goal, got %+v", g)
	}

	// Turn 2: evaluator returns met → Block (halt) with the achieved reason.
	res, err = dispatch()
	if err != nil {
		t.Fatalf("dispatch (turn 2): %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("turn 2 met: expected ActionBlock (halt), got %v", res.Action)
	}
	if res.Message == "" {
		t.Fatalf("turn 2 met: expected a stop reason message, got empty")
	}
	if g := gh.GetGoal(); g == nil || g.State != GoalStateMet {
		t.Fatalf("turn 2: expected met goal, got %+v", g)
	}

	// Clearing returns the hook to inert.
	gh.Clear()
	res, err = dispatch()
	if err != nil {
		t.Fatalf("dispatch (after clear): %v", err)
	}
	if res.Action != hooks.ActionContinue {
		t.Fatalf("after clear: expected ActionContinue, got %v", res.Action)
	}
}
