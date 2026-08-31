package builtin

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// FakeEvaluator for testing
type FakeEvaluator struct {
	results []GoalResult
	index   int
}

func (e *FakeEvaluator) Evaluate(ctx context.Context, condition string, transcript string) (GoalResult, error) {
	if e.index >= len(e.results) {
		return GoalResult{Ok: false, Reason: "no more results"}, nil
	}
	result := e.results[e.index]
	e.index++
	return result, nil
}

func TestGoalStateTransitions(t *testing.T) {
	tests := []struct {
		name           string
		initialState   string
		result         GoalResult
		expectedState  string
		expectedReason string
	}{
		{
			name:           "not_yet_evaluated to active",
			initialState:   GoalStateNotYetEvaluated,
			result:         GoalResult{Ok: false, Reason: "not ready"},
			expectedState:  GoalStateActive,
			expectedReason: "not ready",
		},
		{
			name:           "active to met",
			initialState:   GoalStateActive,
			result:         GoalResult{Ok: true, Reason: "condition satisfied"},
			expectedState:  GoalStateMet,
			expectedReason: "condition satisfied",
		},
		{
			name:           "active to impossible",
			initialState:   GoalStateActive,
			result:         GoalResult{Ok: false, Reason: "cannot be done", Impossible: true},
			expectedState:  GoalStateImpossible,
			expectedReason: "cannot be done",
		},
		{
			name:           "active stays active",
			initialState:   GoalStateActive,
			result:         GoalResult{Ok: false, Reason: "still working"},
			expectedState:  GoalStateActive,
			expectedReason: "still working",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goal := &Goal{
				Condition:  "test condition",
				State:      tt.initialState,
				CreatedAt:  time.Now(),
				Iterations: 0,
			}

			goal.Evaluate(tt.result)

			if goal.State != tt.expectedState {
				t.Errorf("expected state %s, got %s", tt.expectedState, goal.State)
			}
			if goal.LastReason != tt.expectedReason {
				t.Errorf("expected reason %s, got %s", tt.expectedReason, goal.LastReason)
			}
			if tt.result.Ok && goal.AchievedAt == nil {
				t.Error("expected AchievedAt to be set when goal is met")
			}
		})
	}
}

func TestGoalClear(t *testing.T) {
	goal := &Goal{
		Condition: "test condition",
		State:     GoalStateActive,
		CreatedAt: time.Now(),
	}

	goal.Clear()

	if goal.State != GoalStateCleared {
		t.Errorf("expected state %s, got %s", GoalStateCleared, goal.State)
	}
}

func TestSetGoalValidation(t *testing.T) {
	tests := []struct {
		name               string
		condition          string
		isTrustedWorkspace bool
		areHooksRestricted bool
		expectedError      string
	}{
		{
			name:               "untrusted workspace",
			condition:          "test",
			isTrustedWorkspace: false,
			areHooksRestricted: false,
			expectedError:      "/goal is only available in trusted workspaces",
		},
		{
			name:               "hooks restricted",
			condition:          "test",
			isTrustedWorkspace: true,
			areHooksRestricted: true,
			expectedError:      "/goal can't run while hooks are restricted",
		},
		{
			name:               "condition too long",
			condition:          string(make([]byte, MaxConditionLength+1)),
			isTrustedWorkspace: true,
			areHooksRestricted: false,
			expectedError:      fmt.Sprintf("Goal condition is limited to %d characters (got %d)", MaxConditionLength, MaxConditionLength+1),
		},
		{
			name:               "valid goal",
			condition:          "all tests pass",
			isTrustedWorkspace: true,
			areHooksRestricted: false,
			expectedError:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewGoalHook()
			hook.SetTrustedWorkspacePredicate(func() bool { return tt.isTrustedWorkspace })
			hook.SetHooksRestrictedPredicate(func() bool { return tt.areHooksRestricted })

			err := hook.SetGoal(tt.condition)

			if tt.expectedError == "" {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if hook.GetGoal() == nil {
					t.Error("expected goal to be set")
				}
			} else {
				if err == nil {
					t.Error("expected error, got nil")
				} else if err.Error() != tt.expectedError {
					t.Errorf("expected error %q, got %q", tt.expectedError, err.Error())
				}
			}
		})
	}
}

func TestGoalHookOnEvent(t *testing.T) {
	tests := []struct {
		name             string
		goalState        string
		evaluatorResults []GoalResult
		expectContinue   bool
		expectStopReason string
	}{
		{
			name:             "no goal set",
			goalState:        "",
			evaluatorResults: nil,
			expectContinue:   true,
		},
		{
			name:             "goal already met",
			goalState:        GoalStateMet,
			evaluatorResults: nil,
			expectContinue:   true,
		},
		{
			name:             "goal cleared",
			goalState:        GoalStateCleared,
			evaluatorResults: nil,
			expectContinue:   true,
		},
		{
			name:      "goal not met",
			goalState: GoalStateActive,
			evaluatorResults: []GoalResult{
				{Ok: false, Reason: "still working"},
			},
			expectContinue: true,
		},
		{
			name:      "goal met - should stop",
			goalState: GoalStateActive,
			evaluatorResults: []GoalResult{
				{Ok: true, Reason: "all tests passed"},
			},
			expectContinue:   false,
			expectStopReason: "Goal achieved: all tests passed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook := NewGoalHook()

			if tt.goalState != "" {
				hook.goal = &Goal{
					Condition: "test condition",
					State:     tt.goalState,
					CreatedAt: time.Now(),
				}
			}

			if tt.evaluatorResults != nil {
				hook.SetEvaluator(&FakeEvaluator{results: tt.evaluatorResults})
			}

			event := hooks.Event{
				Type: string(hooks.EventAfterAgent),
				Data: map[string]any{
					"prompt":          "test prompt",
					"prompt_response": "test response",
				},
			}

			result, err := hook.OnEvent(context.Background(), event)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			gotContinue := result.Action == hooks.ActionContinue
			if gotContinue != tt.expectContinue {
				t.Errorf("expected Continue=%v, got %v (action=%v)", tt.expectContinue, gotContinue, result.Action)
			}

			if tt.expectStopReason != "" && result.Message != tt.expectStopReason {
				t.Errorf("expected StopReason=%q, got %q", tt.expectStopReason, result.Message)
			}
		})
	}
}

func TestTruncationGuard(t *testing.T) {
	hook := NewGoalHook()
	hook.SetGoal("test condition")

	// Set up evaluator that would normally return "met"
	hook.SetEvaluator(&FakeEvaluator{
		results: []GoalResult{
			{Ok: true, Reason: "should not be used"},
		},
	})

	// But the runtime explicitly marks the transcript as incomplete.
	event := hooks.Event{
		Type: string(hooks.EventAfterAgent),
		Data: map[string]any{
			"prompt":               "test prompt",
			"prompt_response":      "partial response",
			"transcript_truncated": true,
		},
	}

	result, err := hook.OnEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should continue (not stop) because of insufficient evidence
	if result.Action != hooks.ActionContinue {
		t.Error("expected Continue (ActionContinue) when insufficient evidence")
	}

	// Check that the goal state was updated with the truncation reason
	goal := hook.GetGoal()
	if goal.LastReason != "insufficient evidence in transcript" {
		t.Errorf("expected LastReason to be 'insufficient evidence in transcript', got %q", goal.LastReason)
	}
}

func TestIterationCounter(t *testing.T) {
	hook := NewGoalHook()
	hook.SetGoal("test condition")

	// Set up evaluator that returns "not met" multiple times
	hook.SetEvaluator(&FakeEvaluator{
		results: []GoalResult{
			{Ok: false, Reason: "iteration 1"},
			{Ok: false, Reason: "iteration 2"},
			{Ok: false, Reason: "iteration 3"},
		},
	})

	event := hooks.Event{
		Type: string(hooks.EventAfterAgent),
		Data: map[string]any{
			"prompt":          "test",
			"prompt_response": "response",
		},
	}

	// Run 3 iterations
	for i := 0; i < 3; i++ {
		_, err := hook.OnEvent(context.Background(), event)
		if err != nil {
			t.Fatalf("unexpected error on iteration %d: %v", i+1, err)
		}
	}

	goal := hook.GetGoal()
	if goal.Iterations != 3 {
		t.Errorf("expected 3 iterations, got %d", goal.Iterations)
	}
	if goal.State != GoalStateActive {
		t.Errorf("expected state to remain active, got %s", goal.State)
	}
}

func TestGoalHookFilterAndPriority(t *testing.T) {
	hook := NewGoalHook()

	// Test Name
	if hook.Name() != "goal" {
		t.Errorf("expected name 'goal', got %q", hook.Name())
	}

	// Test Priority
	if hook.Priority() != GoalHookPriority {
		t.Errorf("expected priority %d, got %d", GoalHookPriority, hook.Priority())
	}

	// Test Filter - should only accept AfterAgent events
	events := []struct {
		eventType string
		expected  bool
	}{
		{string(hooks.EventAfterAgent), true},
		{string(hooks.EventBeforeAgent), false},
		{string(hooks.EventBeforeTool), false},
		{string(hooks.EventAfterTool), false},
		{string(hooks.EventSessionStart), false},
		{string(hooks.EventSessionEnd), false},
	}

	for _, e := range events {
		event := hooks.Event{Type: e.eventType}
		if hook.Filter(event) != e.expected {
			t.Errorf("Filter(%s) expected %v, got %v", e.eventType, e.expected, hook.Filter(event))
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	hook := NewGoalHook()
	hook.SetGoal("test condition")

	// Set up evaluator
	hook.SetEvaluator(&FakeEvaluator{
		results: []GoalResult{
			{Ok: false, Reason: "concurrent 1"},
			{Ok: false, Reason: "concurrent 2"},
		},
	})

	event := hooks.Event{
		Type: string(hooks.EventAfterAgent),
		Data: map[string]any{
			"prompt":          "test",
			"prompt_response": "response",
		},
	}

	// Run concurrent operations
	done := make(chan bool, 3)

	// Goroutine 1: OnEvent
	go func() {
		_, _ = hook.OnEvent(context.Background(), event)
		done <- true
	}()

	// Goroutine 2: GetGoal
	go func() {
		_ = hook.GetGoal()
		done <- true
	}()

	// Goroutine 3: Clear
	go func() {
		hook.Clear()
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// If we get here without deadlock, the test passes
}
