package autogenskills

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// focusGlobalTask seeds the global TodoManager singleton with a single
// active, in-progress task, so BudgetEnforcementHook's task-gated clock
// (see hasFocusedTask in budget_enforcement.go) starts counting immediately
// — matching a session where the agent has already satisfied
// task-enforcement-hook. Tests that specifically exercise the pre-focus
// "budget clock paused" behavior call ii.ResetGlobalManager() themselves
// instead of this helper.
func focusGlobalTask(t *testing.T) {
	t.Helper()
	tm := ii.GetTodoManager()
	tm.ClearTodos()
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "focused", Status: ii.TodoStatusInProgress, Active: true, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatalf("focusGlobalTask: %v", err)
	}
	t.Cleanup(func() { ii.GetTodoManager().ClearTodos() })
}

func TestBudgetEnforcementHook_Name(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeAuto
	h := NewBudgetEnforcementHook(nil, cfg)
	if h != nil {
		t.Error("expected nil hook when service is nil")
	}

	// Now with a real service
	h = makeEnforcementHook(t, 5, 90)
	if h.Name() != "autogenskills-budget-enforcement" {
		t.Errorf("expected name 'autogenskills-budget-enforcement', got %s", h.Name())
	}
}

func TestBudgetEnforcementHook_NilOnDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeNever
	h := NewBudgetEnforcementHook(nil, cfg)
	if h != nil {
		t.Error("expected nil hook for ModeNever")
	}
}

func TestBudgetEnforcementHook_NilOnNilService(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeAuto
	cfg.Trigger.ToolCallBudget = 5
	h := NewBudgetEnforcementHook(nil, cfg)
	if h != nil {
		t.Error("expected nil hook when service is nil")
	}
}

func TestBudgetEnforcementHook_Priority(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)
	if h.Priority() != 90 {
		t.Errorf("expected priority 90, got %d", h.Priority())
	}
}

func TestBudgetEnforcementHook_Filter(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)

	tests := []struct {
		eventType  string
		shouldPass bool
	}{
		{hooks.EventToolBeforeExecute, true},
		{hooks.EventToolAfterExecute, true},
		{"user.prompt_submit", false},
		{"agent.stop", false},
	}

	for _, tt := range tests {
		event := hooks.Event{Type: tt.eventType}
		result := h.Filter(event)
		if result != tt.shouldPass {
			t.Errorf("Filter(%s) = %v, expected %v", tt.eventType, result, tt.shouldPass)
		}
	}
}

func TestBudgetEnforcementHook_ReadOnlyToolsExemptFromBudget(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)
	ctx := context.Background()

	readOnlyTools := []string{
		"Read", "read", "ReadFile", "read_file",
		"Grep", "grep",
		"Glob", "glob",
		"git_log", "git_status", "git_diff",
		"ls", "list", "list_dir",
		"web_search", "web_fetch",
		"lsp", "lsp_definition",
	}

	// Exceed the budget with regular tools
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Read-only research tools are exempt from the skill budget entirely
	// (mirrors task-enforcement-hook's own read-only exemption — see
	// hooks.IsReadOnlyExplorationTool). They must stay allowed even after
	// the budget has been exceeded by other tools, and must not themselves
	// count toward the budget.
	before := h.GetToolCalls()
	for _, toolName := range readOnlyTools {
		result, _ := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		})
		if result.Action != hooks.ActionContinue {
			t.Errorf("expected ActionContinue for exempt read-only tool %s, got %v", toolName, result.Action)
		}
	}
	if got := h.GetToolCalls(); got != before {
		t.Errorf("read-only tools must not count toward the budget: before=%d after=%d", before, got)
	}
}

func TestBudgetEnforcementHook_ExemptSkillTools(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)
	ctx := context.Background()

	// Exceed budget
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Skill tools should still be allowed
	skillTools := []string{"SkillManage", "skill_manage", "skillinvoke", "use_skill", "Skill"}
	for _, toolName := range skillTools {
		result, err := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		})
		if err != nil {
			t.Errorf("OnEvent for %s returned error: %v", toolName, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("expected ActionContinue for skill tool %s, got %v", toolName, result.Action)
		}
	}
}

func TestBudgetEnforcementHook_ExemptTaskTools(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)
	ctx := context.Background()

	// Exceed budget
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	taskTools := []string{"task_create", "task_list", "task_get", "task_update", "TodoWrite", "TodoRead"}
	for _, toolName := range taskTools {
		result, err := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		})
		if err != nil {
			t.Errorf("OnEvent for %s returned error: %v", toolName, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Errorf("expected ActionContinue for task tool %s, got %v", toolName, result.Action)
		}
	}
}

func TestBudgetEnforcementHook_PlanModeToolsExemptFromBudget(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)
	ctx := context.Background()

	// Exceed budget
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Plan mode tools are exempt from the skill budget, same as
	// task-enforcement-hook exempts them from needing a task.
	before := h.GetToolCalls()
	planTools := []string{"enter_plan_mode", "exit_plan_mode", "enterplanmode", "ExitPlanMode"}
	for _, toolName := range planTools {
		result, _ := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": toolName},
		})
		if result.Action != hooks.ActionContinue {
			t.Errorf("expected ActionContinue for exempt plan tool %s, got %v", toolName, result.Action)
		}
	}
	if got := h.GetToolCalls(); got != before {
		t.Errorf("plan mode tools must not count toward the budget: before=%d after=%d", before, got)
	}
}

func TestBudgetEnforcementHook_Tier1HardBlock(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// First 3 calls should be allowed (budget is 3)
	for i := range 3 {
		result, err := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Fatalf("call %d: expected ActionContinue, got %v", i, result.Action)
		}
	}

	// 4th call should HARD BLOCK (Tier 1 — onboarding budget exceeded)
	result, err := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if err != nil {
		t.Fatalf("4th call: unexpected error: %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Fatalf("4th call: expected ActionBlock, got %v", result.Action)
	}
	if !strings.Contains(result.Message, "SKILL BUDGET ENFORCEMENT") {
		t.Errorf("expected block message to contain 'SKILL BUDGET ENFORCEMENT', got: %s", result.Message)
	}
	if !strings.Contains(result.Message, "onboarding budget") {
		t.Errorf("expected block message to mention 'onboarding budget', got: %s", result.Message)
	}
}

func TestBudgetEnforcementHook_NoBlockWhenBudgetZero(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Mode = ModeAuto
	cfg.Trigger.ToolCallBudget = 0

	factory := &SkillFactory{}
	metrics := &Metrics{}
	svc := &Service{
		cfg:     cfg,
		factory: factory,
		metrics: metrics,
		builder: NewNudgeBuilder(),
	}

	h := NewBudgetEnforcementHook(svc, cfg)
	if h != nil {
		t.Error("expected nil hook when budget=0 (enforcement disabled)")
	}
}

func TestBudgetEnforcementHook_BashCommandsCountTowardBudgetUnlessReadOnly(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// Exceed budget
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Read-only bash commands are exempt from the budget (mirror of
	// task-enforcement-hook's own hooks.IsBashReadOnly exemption) and stay
	// allowed even after the budget is exceeded by other commands.
	readOnlyCmds := []string{
		"ls -la",
		"cat file.txt",
		"cd /tmp && cat file.txt",
		"git status",
		"git log --oneline",
		"go list ./...",
		"grep -r pattern .",
	}
	for _, cmd := range readOnlyCmds {
		result, _ := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{
				"tool_name": "bash",
				"params":    map[string]any{"command": cmd},
			},
		})
		if result.Action != hooks.ActionContinue {
			t.Errorf("expected ActionContinue for read-only bash %q, got %v", cmd, result.Action)
		}
	}

	// Mutating bash commands are NOT exempt and should still be blocked.
	writeCmds := []string{"rm file.txt", "git commit -m \"msg\"", "go build"}
	for _, cmd := range writeCmds {
		result, _ := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{
				"tool_name": "bash",
				"params":    map[string]any{"command": cmd},
			},
		})
		if result.Action != hooks.ActionBlock {
			t.Errorf("expected ActionBlock for bash %q after budget, got %v", cmd, result.Action)
		}
	}
}

func TestBudgetEnforcementHook_BudgetCounterIncrements(t *testing.T) {
	h := makeEnforcementHook(t, 10, 90)
	ctx := context.Background()

	// 3 non-exempt calls
	for range 3 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	if h.GetToolCalls() != 3 {
		t.Errorf("expected 3 tool calls, got %d", h.GetToolCalls())
	}

	// Read-only tools are exempt — must NOT count
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "Read"},
	})

	if h.GetToolCalls() != 3 {
		t.Errorf("read-only tools should not increment counter, expected 3, got %d", h.GetToolCalls())
	}

	// 1 skill tool (should NOT increment)
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "SkillManage"},
	})

	if h.GetToolCalls() != 3 {
		t.Errorf("skill tools should not increment counter, expected 3, got %d", h.GetToolCalls())
	}

	// 1 task tool (should NOT increment)
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "task_list"},
	})

	if h.GetToolCalls() != 3 {
		t.Errorf("task tools should not increment counter, expected 3, got %d", h.GetToolCalls())
	}
}

func TestBudgetEnforcementHook_BudgetUpgradeOnFirstSkillCreate(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// Exceed onboarding budget
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Should be blocked (Tier 1)
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionBlock {
		t.Fatal("expected block after exceeding onboarding budget")
	}

	if h.IsSkilled() {
		t.Fatal("expected skilled=false before first skill creation")
	}

	// Simulate successful skill creation via EventToolAfterExecute
	_, err := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "my-skill"},
			"result":    "skill created",
		},
	})
	if err != nil {
		t.Fatalf("handleSkillTool via OnEvent returned error: %v", err)
	}

	// Should have upgraded to skilled mode
	if !h.IsSkilled() {
		t.Fatal("expected skilled=true after first skill creation")
	}

	// Budget counter should have been reset
	if h.GetToolCalls() != 0 {
		t.Fatalf("expected toolCalls=0 after skill creation, got %d", h.GetToolCalls())
	}

	// Should be allowed again (now using working budget of 90)
	result, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected ActionContinue after budget upgrade, got %v", result.Action)
	}
}

func TestBudgetEnforcementHook_BudgetUpgradeOnSkillInvocation(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// Exceed onboarding budget
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Should be blocked (Tier 1)
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionBlock {
		t.Fatal("expected block after exceeding onboarding budget")
	}

	// Simulate successful skill INVOCATION (not creation) via the "Skill" tool
	_, err := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Skill",
			"params":    map[string]any{"name": "credential-discovery-cli"},
			"result":    "skill instructions loaded",
		},
	})
	if err != nil {
		t.Fatalf("handleSkillTool via OnEvent returned error: %v", err)
	}

	// Should have upgraded to skilled mode via skill invocation
	if !h.IsSkilled() {
		t.Fatal("expected skilled=true after skill invocation")
	}

	// Budget counter should have been reset
	if h.GetToolCalls() != 0 {
		t.Fatalf("expected toolCalls=0 after skill invocation, got %d", h.GetToolCalls())
	}

	// Should be allowed again (now using working budget of 90)
	result, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected ActionContinue after budget upgrade via skill invocation, got %v", result.Action)
	}
}

func TestBudgetEnforcementHook_WorkingBudgetSoftNudge(t *testing.T) {
	h := makeEnforcementHook(t, 3, 5) // small working budget for test
	ctx := context.Background()

	// First, upgrade to skilled mode
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "my-skill"},
			"result":    "skill created",
		},
	})

	if !h.IsSkilled() {
		t.Fatal("expected skilled=true after skill creation")
	}

	// Now use up the working budget (5 calls)
	for i := range 5 {
		result, _ := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
		if result.Action != hooks.ActionContinue {
			t.Fatalf("call %d: expected ActionContinue within working budget, got %v", i+1, result.Action)
		}
	}

	// Next call should be a soft nudge (ContinueWithMessage), not a hard block
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected ActionContinue (soft nudge) after working budget, got %v", result.Action)
	}
	if !strings.Contains(result.Message, "SKILL BUDGET NUDGE") {
		t.Errorf("expected nudge message, got: %s", result.Message)
	}
	if h.GetNudgeIgnores() != 1 {
		t.Errorf("expected nudgeIgnores=1, got %d", h.GetNudgeIgnores())
	}
}

func TestBudgetEnforcementHook_EscalationAfterMaxNudgeIgnores(t *testing.T) {
	h := makeEnforcementHookWithNudge(t, 3, 5, 2) // max 2 nudges
	ctx := context.Background()

	// Upgrade to skilled mode
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "my-skill"},
			"result":    "skill created",
		},
	})

	// Use up working budget
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// First nudge (nudge 1) — soft
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected soft nudge 1, got %v", result.Action)
	}

	// Second nudge (nudge 2) — soft (this is the last allowed ignore)
	result, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected soft nudge 2, got %v", result.Action)
	}

	// Third nudge (nudge 3 > max 2) — ESCALATION hard block
	result, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionBlock {
		t.Fatalf("expected hard block after escalation, got %v", result.Action)
	}
	if !strings.Contains(result.Message, "ESCALATION") {
		t.Errorf("expected escalation message, got: %s", result.Message)
	}
}

func TestBudgetEnforcementHook_RefillAfterSubsequentSkillCreate(t *testing.T) {
	h := makeEnforcementHook(t, 3, 5) // small working budget for test
	ctx := context.Background()

	// Upgrade to skilled mode
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "skill-1"},
			"result":    "skill created",
		},
	})

	// Use up working budget and get nudged
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Get a soft nudge
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected soft nudge, got %v", result.Action)
	}
	if h.GetNudgeIgnores() != 1 {
		t.Fatalf("expected nudgeIgnores=1, got %d", h.GetNudgeIgnores())
	}

	// Create another skill → refill budget
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "skill-2"},
			"result":    "skill created",
		},
	})

	// Budget should be refilled
	if h.GetToolCalls() != 0 {
		t.Fatalf("expected toolCalls=0 after refill, got %d", h.GetToolCalls())
	}
	if h.GetNudgeIgnores() != 0 {
		t.Fatalf("expected nudgeIgnores=0 after refill, got %d", h.GetNudgeIgnores())
	}

	// Should be allowed again with full working budget
	result, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionContinue {
		t.Fatalf("expected ActionContinue after refill, got %v", result.Action)
	}
}

func TestBudgetEnforcementHook_NoResetOnFailedSkillCreate(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// Exceed budget
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Simulate FAILED skill creation (error present as string)
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"error":     "skill already exists",
		},
	})

	// Budget should NOT have been reset or upgraded
	if h.GetToolCalls() == 0 {
		t.Fatal("expected toolCalls > 0 — failed skill create should not reset budget")
	}
	if h.IsSkilled() {
		t.Fatal("expected skilled=false — failed skill create should not upgrade budget")
	}

	// Should still be blocked
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionBlock {
		t.Fatal("expected ActionBlock — failed skill create should not unblock tools")
	}
}

func TestBudgetEnforcementHook_NoResetOnNonSkillTool(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// Exceed budget
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Simulate after-execute for a non-skill tool (Bash)
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "bash",
			"result":    "done",
		},
	})

	// Budget should NOT have been reset
	if h.GetToolCalls() == 0 {
		t.Fatal("expected toolCalls > 0 — non-skill after-execute should not reset budget")
	}

	// Should still be blocked
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})
	if result.Action != hooks.ActionBlock {
		t.Fatal("expected ActionBlock — non-skill after-execute should not unblock tools")
	}
}

func TestBudgetEnforcementHook_BlockMessageContent(t *testing.T) {
	h := makeEnforcementHook(t, 3, 90)
	ctx := context.Background()

	// Exceed budget
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})

	keyPhrases := []string{
		"SKILL BUDGET ENFORCEMENT",
		"BLOCKED",
		"YOU MUST CREATE OR USE A SKILL",
		"SkillManage",
		"Skill tool",
		"onboarding budget",
		"upgrades to 90",
	}
	for _, phrase := range keyPhrases {
		if !strings.Contains(result.Message, phrase) {
			t.Errorf("block message missing phrase: %s", phrase)
		}
	}
}

func TestBudgetEnforcementHook_NudgeMessageContent(t *testing.T) {
	h := makeEnforcementHookWithNudge(t, 3, 5, 3)
	ctx := context.Background()

	// Upgrade to skilled mode
	for range 4 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "my-skill"},
			"result":    "skill created",
		},
	})

	// Use up working budget
	for range 5 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	// Get nudge
	result, _ := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash"},
	})

	keyPhrases := []string{
		"SKILL BUDGET NUDGE",
		"working budget",
		"soft nudge",
		"SkillManage",
		"Skill tool",
		"refills to 5",
		"2 soft nudge(s) remaining", // max 3, already used 1, so 2 remaining
	}
	for _, phrase := range keyPhrases {
		if !strings.Contains(result.Message, phrase) {
			t.Errorf("nudge message missing phrase: %q\nfull message: %s", phrase, result.Message)
		}
	}
}

// makeEnforcementHook creates a BudgetEnforcementHook for testing with default MaxNudgeIgnores.
func TestBudgetEnforcementHook_GetBudgetSnapshot(t *testing.T) {
	h := makeEnforcementHook(t, 5, 90)
	ctx := context.Background()

	// Initial snapshot
	snap := h.GetBudgetSnapshot()
	if snap.ToolCalls != 0 {
		t.Errorf("expected 0 tool calls, got %d", snap.ToolCalls)
	}
	if snap.Skilled {
		t.Error("expected skilled=false initially")
	}
	if snap.OnboardingBudget != 5 {
		t.Errorf("expected onboarding budget 5, got %d", snap.OnboardingBudget)
	}
	if snap.WorkingBudget != 90 {
		t.Errorf("expected working budget 90, got %d", snap.WorkingBudget)
	}

	// Make some calls
	for range 3 {
		_, _ = h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	snap = h.GetBudgetSnapshot()
	if snap.ToolCalls != 3 {
		t.Errorf("expected 3 tool calls, got %d", snap.ToolCalls)
	}
	if snap.Skilled {
		t.Error("expected skilled=false before skill creation")
	}

	// Create a skill → upgrade
	_, _ = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "SkillManage",
			"params":    map[string]any{"action": "create", "name": "test"},
			"result":    "created",
		},
	})

	snap = h.GetBudgetSnapshot()
	if !snap.Skilled {
		t.Error("expected skilled=true after skill creation")
	}
	if snap.ToolCalls != 0 {
		t.Errorf("expected 0 tool calls after reset, got %d", snap.ToolCalls)
	}
}

func TestBudgetEnforcementHook_GetBudgetSnapshot_NilHook(t *testing.T) {
	var h *BudgetEnforcementHook
	snap := h.GetBudgetSnapshot()
	if snap.ToolCalls != 0 || snap.Skilled || snap.WorkingBudget != 0 {
		t.Error("nil hook should return zero snapshot")
	}
}

func makeEnforcementHook(t *testing.T, onboardingBudget, workingBudget uint) *BudgetEnforcementHook {
	t.Helper()
	return makeEnforcementHookWithNudge(t, onboardingBudget, workingBudget, 3)
}

// makeEnforcementHookWithNudge creates a BudgetEnforcementHook for testing with explicit MaxNudgeIgnores.
func makeEnforcementHookWithNudge(t *testing.T, onboardingBudget, workingBudget uint, maxNudgeIgnores int) *BudgetEnforcementHook {
	t.Helper()
	// The budget clock is gated on task focus (see hasFocusedTask) — seed a
	// focused task by default so the existing budget-counting behavior
	// tests don't all need to duplicate that setup. Tests that specifically
	// exercise the pre-focus/gating behavior manage the global manager
	// themselves (see TestBudgetEnforcementHook_ClockPausedBeforeTaskFocused).
	focusGlobalTask(t)
	cfg := DefaultConfig()
	cfg.Mode = ModeAuto
	cfg.Trigger.ToolCallBudget = onboardingBudget
	cfg.Trigger.WorkingBudget = workingBudget
	cfg.Trigger.MaxNudgeIgnores = maxNudgeIgnores

	// Create a minimal service for the hook
	factory := &SkillFactory{}
	metrics := &Metrics{}
	svc := &Service{
		cfg:     cfg,
		factory: factory,
		metrics: metrics,
		builder: NewNudgeBuilder(),
	}

	h := NewBudgetEnforcementHook(svc, cfg)
	if h == nil {
		t.Fatal("NewBudgetEnforcementHook returned nil")
	}
	return h
}

// TestBudgetEnforcementHook_ClockPausedBeforeTaskFocused verifies Part B of
// the "join task-enforcement + skill-budget-enforcement" change: the budget
// clock does not start ticking until the agent has focused at least one
// task, and once focused, stays sticky-on even if the task list later
// empties out.
func TestBudgetEnforcementHook_ClockPausedBeforeTaskFocused(t *testing.T) {
	ii.ResetGlobalManager()
	defer ii.ResetGlobalManager()
	// No task ever created — the global manager has zero tasks.

	cfg := DefaultConfig()
	cfg.Mode = ModeAuto
	cfg.Trigger.ToolCallBudget = 2
	cfg.Trigger.WorkingBudget = 90
	factory := &SkillFactory{}
	metrics := &Metrics{}
	svc := &Service{cfg: cfg, factory: factory, metrics: metrics, builder: NewNudgeBuilder()}
	h := NewBudgetEnforcementHook(svc, cfg)
	if h == nil {
		t.Fatal("NewBudgetEnforcementHook returned nil")
	}
	ctx := context.Background()

	// Well beyond the onboarding budget of 2 — none of these should count
	// or block, because no task has ever been focused.
	for i := range 10 {
		result, err := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash", "params": map[string]any{"command": "go build ./..."}},
		})
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Fatalf("call %d: expected ActionContinue while unfocused, got %v", i, result.Action)
		}
	}
	if got := h.GetToolCalls(); got != 0 {
		t.Fatalf("expected 0 tool calls while unfocused, got %d", got)
	}

	// Focus a task — the clock should start from here.
	tm := ii.GetTodoManager()
	if err := tm.SetTodos([]ii.TodoItem{{ID: "1", Content: "focused", Status: ii.TodoStatusInProgress, Active: true, Priority: ii.TodoPriorityMedium}}); err != nil {
		t.Fatalf("SetTodos: %v", err)
	}

	for i := range 2 {
		result, err := h.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolBeforeExecute,
			Data: map[string]any{"tool_name": "bash", "params": map[string]any{"command": "go build ./..."}},
		})
		if err != nil {
			t.Fatalf("post-focus call %d: unexpected error: %v", i, err)
		}
		if result.Action != hooks.ActionContinue {
			t.Fatalf("post-focus call %d: expected ActionContinue within budget, got %v", i, result.Action)
		}
	}
	// 3rd call after focus exceeds the onboarding budget of 2 — must block.
	result, err := h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash", "params": map[string]any{"command": "go build ./..."}},
	})
	if err != nil {
		t.Fatalf("post-focus 3rd call: unexpected error: %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Fatalf("expected ActionBlock once the clock has started and budget is exceeded, got %v", result.Action)
	}

	// Sticky: clearing the task list must NOT pause the clock again.
	tm.ClearTodos()
	result, err = h.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{"tool_name": "bash", "params": map[string]any{"command": "go build ./..."}},
	})
	if err != nil {
		t.Fatalf("post-clear call: unexpected error: %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Fatalf("expected the clock to stay sticky-on (still blocked) after tasks were cleared, got %v", result.Action)
	}
}
