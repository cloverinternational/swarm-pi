// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"context"
	"fmt"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// BudgetEnforcementHook enforces the two-tier budget system:
//
//   - Tier 1 (onboarding): ToolCallBudget (default 5) — HARD BLOCK.
//     The agent must create or use a skill before continuing.
//   - Tier 2 (working): WorkingBudget (default 90) — soft NUDGE.
//     After creating/using a skill, the agent gets the working budget.
//     When exceeded, a soft nudge is injected via ContinueWithMessage.
//     After MaxNudgeIgnores nudges are ignored, it escalates to hard block.
//
// CONTRACT:
//   - Priority 90 (after task-enforcement at 95, before observe-only hooks)
//   - Hooks EventToolBeforeExecute and EventToolAfterExecute
//   - Exempt: SkillManage, skill invocation tools, task management tools
//   - ALL other tools count toward the budget (no read-only exemption)
//   - Budget upgrades from ToolCallBudget → WorkingBudget after first skill creation/use
//   - Budget refills to WorkingBudget after subsequent skill creation/use
//   - Thread-safe counters per session
type BudgetEnforcementHook struct {
	service *Service
	cfg     Config

	mu           sync.Mutex
	toolCalls    int  // non-exempt tool calls since last skill creation/use
	skilled      bool // true after first skill creation/use (upgraded to working budget)
	budgetExceed int  // number of times budget was exceeded (for metrics)
	nudgeIgnores int  // number of soft nudges ignored (for escalation)
	lastSkillAt  int  // tool call count when last skill was created

	// taskEverFocused is sticky: once true (the agent has focused at least
	// one in_progress+active task, matching task-enforcement-hook's own
	// gate), it never reverts to false, even if the task list later empties
	// out between tasks. Before it's ever true, the budget clock is paused
	// entirely — see hasEverFocusedTask / the "join task-enforcement +
	// skill-budget-enforcement" change. This exists because, without it,
	// read-only tools that task-enforcement lets through before any task
	// exists (see hooks.IsReadOnlyExplorationTool) would otherwise still
	// tick this hook's independent counter and could exhaust the onboarding
	// budget purely from pre-planning research.
	taskEverFocused bool
}

// NewBudgetEnforcementHook creates the enforcement hook.
// CONTRACT: svc may be nil (returns nil hook); cfg provides budget thresholds.
func NewBudgetEnforcementHook(svc *Service, cfg Config) *BudgetEnforcementHook {
	if svc == nil || !cfg.IsEnabled() || cfg.Mode == ModeNever {
		return nil
	}
	// Budget of 0 means "disabled" — don't create the hook at all
	if cfg.Trigger.ToolCallBudget == 0 {
		return nil
	}
	return &BudgetEnforcementHook{
		service: svc,
		cfg:     cfg,
	}
}

// Name returns the hook identifier.
func (h *BudgetEnforcementHook) Name() string {
	return "autogenskills-budget-enforcement"
}

// Priority returns 90 — enforcement runs before observe-only hooks.
func (h *BudgetEnforcementHook) Priority() int {
	return 90
}

// Filter returns true for tool before-execute and after-execute events.
// Before-execute: enforcement (block/allow/nudge).
// After-execute: detect successful skill tool usage to upgrade/refill budget.
func (h *BudgetEnforcementHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute || event.Type == hooks.EventToolAfterExecute
}

// OnEvent is the enforcement logic.
//   - EventToolBeforeExecute: enforces the two-tier budget.
//   - EventToolAfterExecute: upgrades/refills budget when any skill tool succeeds.
func (h *BudgetEnforcementHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Sub-agents bypass budget enforcement — they execute tools on behalf
	// of a parent session where the budget is tracked.
	if agent.IsSubAgent(ctx) {
		return hooks.Continue(), nil
	}

	// After-execute: detect successful skill creation and upgrade/refill budget
	if event.Type == hooks.EventToolAfterExecute {
		toolName, _ := event.Data["tool_name"].(string)
		h.handleSkillTool(toolName, event)
		return hooks.Continue(), nil
	}

	// Before-execute: enforcement logic below
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		return hooks.Continue(), nil
	}

	// Always exempt: skill management and skill invocation tools
	if isSkillTool(toolName) {
		return hooks.Continue(), nil
	}

	// Always exempt: task management tools (planning must continue)
	if isTaskTool(toolName) {
		return hooks.Continue(), nil
	}

	// Always exempt: plan mode transitions and user-interaction tools —
	// same rationale as task-enforcement-hook's isExemptTool.
	if hooks.IsPlanModeTool(toolName) || hooks.IsUserInteractionTool(toolName) {
		return hooks.Continue(), nil
	}

	// Always exempt: read-only research tools (Read, Grep, WebSearch,
	// read-only git/LSP, ...) and read-only Bash commands. This mirrors
	// task-enforcement-hook's own exemption (hooks.IsReadOnlyExplorationTool
	// / hooks.IsBashReadOnly) exactly — before this fix, task-enforcement
	// let pure research through without requiring a task, but
	// budget-enforcement still silently counted that same research against
	// the skill budget, forcing an unrelated "create a skill" detour purely
	// from reading code. See the "join task-enforcement +
	// skill-budget-enforcement" change.
	if hooks.IsReadOnlyExplorationTool(toolName) {
		return hooks.Continue(), nil
	}
	if hooks.IsBashTool(toolName) {
		if params, ok := event.Data["params"].(map[string]any); ok {
			if cmd, ok := params["command"].(string); ok && hooks.IsBashReadOnly(cmd) {
				return hooks.Continue(), nil
			}
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// The budget clock does not start until the agent has focused at least
	// one task. Before that point, task-enforcement-hook (if enabled) is
	// already the sole gate on non-exempt tools; letting this hook's
	// counter run independently during that pre-planning phase means an
	// agent could get skill-budget-blocked before it has even been allowed
	// to do any real work. Once focused, taskEverFocused stays true for the
	// rest of the session (see the field doc comment) — the ordinary budget
	// logic below then applies to every non-exempt call as before.
	if !h.taskEverFocused {
		if hasFocusedTask() {
			h.taskEverFocused = true
		} else {
			return hooks.Continue(), nil
		}
	}

	trigger := h.cfg.Trigger.WithDefaults()

	// Determine which budget applies
	budget := trigger.ToolCallBudget // onboarding budget
	if h.skilled {
		budget = trigger.WorkingBudget // working budget
	}

	// Check if budget exceeded
	if uint(h.toolCalls) >= budget {
		if h.skilled {
			// Tier 2: working budget exceeded — soft nudge (not hard block)
			h.nudgeIgnores++
			if h.nudgeIgnores > trigger.MaxNudgeIgnores {
				// Escalation: too many nudges ignored → hard block
				return hooks.Block(hooks.WrapReminder(
					h.Name(), "block", hooks.NextReminderSeq(h.Name()), h.escalationBlockMessage())), nil
			}
			seq, ok := hooks.DefaultMetaNudgeBudget().TryClaim(
				hooks.NudgeSessionID(ctx, event), hooks.MetaNudgeSkillReview)
			if !ok {
				return hooks.Continue(), nil
			}
			return hooks.ContinueWithMessage(
				hooks.WrapReminder(h.Name(), "review", seq, h.nudgeMessage())), nil
		}
		// Tier 1: onboarding budget exceeded — HARD BLOCK
		return hooks.Block(hooks.WrapReminder(
			h.Name(), "block", hooks.NextReminderSeq(h.Name()), h.blockMessage())), nil
	}

	// Budget not exceeded yet — increment counter and allow
	h.toolCalls++
	return hooks.Continue(), nil
}

// handleSkillTool upgrades or refills the budget when any skill tool succeeds.
// Called on EventToolAfterExecute — the event Data contains "result" and
// optionally "error" so we can verify the tool actually succeeded.
//
// Both skill creation (SkillManage) and skill invocation (Skill, skillinvoke, etc.)
// trigger the budget upgrade/refill. The point of budget enforcement is to force
// the agent to work through skills — creating OR using a skill proves compliance.
func (h *BudgetEnforcementHook) handleSkillTool(toolName string, event hooks.Event) {
	if !isSkillTool(toolName) {
		return
	}

	// Verify the tool succeeded — if there was an execution error, don't upgrade/refill.
	if execErr, _ := event.Data["error"].(string); execErr != "" {
		return
	}
	// Also check for error as an error interface (TUI may pass raw error)
	if execErr, ok := event.Data["error"].(error); ok && execErr != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.lastSkillAt = h.toolCalls
	h.toolCalls = 0
	h.budgetExceed = 0
	h.nudgeIgnores = 0 // reset nudge ignores on skill creation

	if !h.skilled {
		// First skill creation → upgrade from onboarding to working budget
		h.skilled = true
	}
	// Subsequent skill creations → refill working budget (toolCalls already reset)
}

// blockMessage returns the Tier 1 enforcement message (onboarding budget exceeded).
func (h *BudgetEnforcementHook) blockMessage() string {
	trigger := h.cfg.Trigger.WithDefaults()
	return fmt.Sprintf(`[SKILL BUDGET ENFORCEMENT — BLOCKED]

You have used %d non-exempt tool calls. The onboarding budget is %d.

═══════════════════════════════════════════════════════════════
                    YOU MUST CREATE OR USE A SKILL
═══════════════════════════════════════════════════════════════

You cannot execute any further tools until you:

  1. Create a skill for the recurring pattern you're working on
     → Call SkillManage(action: "create", name: "...", description: "...", instructions: "...")
  2. Or use an existing skill that covers this workflow
     → Call the Skill tool to invoke a relevant skill

  After creating or using a skill, your budget upgrades to %d and ALL tools unlock.

═══════════════════════════════════════════════════════════════

This is a HARD REQUIREMENT. The onboarding budget exists to
ensure you capture patterns early. Once you create your first
skill, your budget expands to %d.`, h.toolCalls, trigger.ToolCallBudget, trigger.WorkingBudget, trigger.WorkingBudget)
}

// nudgeMessage returns the Tier 2 soft nudge (working budget exceeded).
func (h *BudgetEnforcementHook) nudgeMessage() string {
	trigger := h.cfg.Trigger.WithDefaults()
	remaining := max(trigger.MaxNudgeIgnores-h.nudgeIgnores, 0)
	return fmt.Sprintf(`[SKILL BUDGET NUDGE — %d non-exempt tool calls]

You've exceeded your working budget of %d. This is a soft nudge — your
tool call is still allowed, but you should consider:

  1. Creating a skill for the recurring pattern you're working on
     → Call SkillManage(action: "create", name: "...", description: "...", instructions: "...")
  2. Patching an existing skill that's incomplete
     → Call SkillManage(action: "patch", name: "...", instructions: "...")
  3. Using an existing skill that covers this workflow
     → Call the Skill tool to invoke a relevant skill

If you create, patch, or invoke a skill, your budget refills to %d.
You have %d soft nudge(s) remaining before hard enforcement.`, h.toolCalls, trigger.WorkingBudget, trigger.WorkingBudget, remaining)
}

// escalationBlockMessage returns the Tier 2 hard block (too many nudges ignored).
func (h *BudgetEnforcementHook) escalationBlockMessage() string {
	trigger := h.cfg.Trigger.WithDefaults()
	return fmt.Sprintf(`[SKILL BUDGET ENFORCEMENT — ESCALATION BLOCKED]

You have used %d non-exempt tool calls and ignored %d soft nudges.

═══════════════════════════════════════════════════════════════
                    YOU MUST CREATE OR USE A SKILL
═══════════════════════════════════════════════════════════════

Your working budget of %d has been exceeded and you've ignored
%d nudges. ALL non-exempt tools are now HARD-BLOCKED.

Create, patch, or invoke a skill to refill your budget:

  1. SkillManage(action: "create", name: "...", description: "...", instructions: "...")
  2. SkillManage(action: "patch", name: "...", instructions: "...")
  3. Skill tool to invoke an existing skill

═══════════════════════════════════════════════════════════════

After creating, patching, or invoking a skill, your budget refills to %d.`, h.toolCalls, h.nudgeIgnores, trigger.WorkingBudget, trigger.MaxNudgeIgnores, trigger.WorkingBudget)
}

// GetToolCalls returns the current non-exempt tool call count (for testing).
func (h *BudgetEnforcementHook) GetToolCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.toolCalls
}

// IsSkilled returns whether the agent has upgraded to the working budget (for testing).
func (h *BudgetEnforcementHook) IsSkilled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.skilled
}

// GetNudgeIgnores returns the number of soft nudges ignored (for testing).
func (h *BudgetEnforcementHook) GetNudgeIgnores() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.nudgeIgnores
}

// BudgetSnapshot is a read-only copy of the budget state for UI display.
type BudgetSnapshot struct {
	ToolCalls    int  // non-exempt calls since last skill creation/use
	Skilled      bool // true after first skill creation/use
	NudgeIgnores int  // soft nudges ignored
	BudgetExceed int  // times budget was exceeded

	// Config values (for display)
	OnboardingBudget uint
	WorkingBudget    uint
	MaxNudgeIgnores  int
}

// GetBudgetSnapshot returns a read-only copy of the current budget state.
// Thread-safe: acquires the mutex, copies, and returns.
func (h *BudgetEnforcementHook) GetBudgetSnapshot() BudgetSnapshot {
	if h == nil {
		return BudgetSnapshot{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	trigger := h.cfg.Trigger.WithDefaults()
	return BudgetSnapshot{
		ToolCalls:        h.toolCalls,
		Skilled:          h.skilled,
		NudgeIgnores:     h.nudgeIgnores,
		BudgetExceed:     h.budgetExceed,
		OnboardingBudget: trigger.ToolCallBudget,
		WorkingBudget:    trigger.WorkingBudget,
		MaxNudgeIgnores:  trigger.MaxNudgeIgnores,
	}
}

// hasFocusedTask reports whether the global TodoManager currently has an
// active, in-progress task — the same "focused" predicate
// task-enforcement-hook uses to decide whether tools may run at all. Fails
// open (returns true, i.e. don't pause the budget clock) if there is no
// TodoManager to check, so environments that don't wire up task tools at
// all keep the pre-existing budget behavior.
func hasFocusedTask() bool {
	tm := ii.GetTodoManager()
	if tm == nil {
		return true
	}
	for _, t := range tm.ByStatus(ii.TodoStatusInProgress) {
		if t.Active {
			return true
		}
	}
	return false
}

// isSkillTool checks if a tool is related to skill management or invocation.
// This includes the Zed "Skill" tool (for invoking skills in the editor)
// as well as SkillManage and skill invocation tools.
func isSkillTool(toolName string) bool {
	return hooks.IsSkillTool(toolName)
}

// isTaskTool checks if a tool is a task management tool.
func isTaskTool(toolName string) bool {
	return hooks.IsTaskManagementTool(toolName)
}
