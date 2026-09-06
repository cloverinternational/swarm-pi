package builtin

import (
	"context"
	"testing"
)

func TestSimulationReminderMessage(t *testing.T) {
	msg := SimulationReminderMessage()

	if msg == "" {
		t.Error("SimulationReminderMessage() returned empty string")
	}

	// Check that message contains key elements from the choreography framing.
	// Terms mirror the structure of SimulationReminderMessage — update together.
	expectedSubstrings := []string{
		"SIMULATION",
		"CHOREOGRAPH",
		"STEP 1",
		"STEP 2",
		"STEP 3",
		"STEP 4",
		"DEPENDENCIES",
		"BREAKING POINTS",
		"REHEARSE",
	}

	for _, substr := range expectedSubstrings {
		if !containsSubstringCaseInsensitive(msg, substr) {
			t.Errorf("SimulationReminderMessage() should contain %q", substr)
		}
	}
}

func TestCheckShouldSimulate(t *testing.T) {
	ctx := context.Background()
	msg, shouldSimulate := CheckShouldSimulate(ctx)

	if !shouldSimulate {
		t.Error("CheckShouldSimulate() should always return true for simulation hook")
	}

	if msg == "" {
		t.Error("CheckShouldSimulate() returned empty message")
	}

	if msg != SimulationReminderMessage() {
		t.Error("CheckShouldSimulate() should return SimulationReminderMessage()")
	}
}

func TestPlanExitDetected(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"plan tool", "plan", true},
		{"Plan tool", "Plan", true},
		{"enter_plan_mode", "enter_plan_mode", true},
		{"exit_plan_mode", "exit_plan_mode", true},
		{"create_plan", "create_plan", true},
		{"TodoWrite", "TodoWrite", true},
		{"task_create", "task_create", true},
		{"Bash tool", "Bash", false},
		{"Read tool", "Read", false},
		{"Write tool", "Write", false},
		{"Edit tool", "Edit", false},
		{"grep tool", "grep", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PlanExitDetected(tt.toolName); got != tt.expected {
				t.Errorf("PlanExitDetected(%q) = %v, want %v", tt.toolName, got, tt.expected)
			}
		})
	}
}

func TestSimulationHook_Execute(t *testing.T) {
	h := &SimulationHook{}
	ctx := context.Background()

	msg, err := h.Execute(ctx)
	if err != nil {
		t.Errorf("SimulationHook.Execute() returned error: %v", err)
	}

	if msg == "" {
		t.Error("SimulationHook.Execute() returned empty message")
	}
}

func TestSimulationHook_ExecuteAsHook(t *testing.T) {
	h := &SimulationHook{}
	ctx := context.Background()
	hookCtx := &HookContext{
		ToolName:  "plan",
		ToolInput: map[string]any{},
	}

	decision, err := h.ExecuteAsHook(ctx, hookCtx)
	if err != nil {
		t.Errorf("SimulationHook.ExecuteAsHook() returned error: %v", err)
	}

	if decision == nil {
		t.Fatal("SimulationHook.ExecuteAsHook() returned nil decision")
	}

	if decision.Decision != "allow" {
		t.Errorf("SimulationHook.ExecuteAsHook() decision = %q, want 'allow'", decision.Decision)
	}

	if decision.SystemMessage == "" {
		t.Error("SimulationHook.ExecuteAsHook() should set SystemMessage")
	}
}
