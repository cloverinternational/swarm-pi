// Package autogenskills implements the Hermes-style closed learning loop for
// the Swarm SDK skill system.
package autogenskills

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// LifecycleHook adapts swarm-sdk hook events into autogenskills service calls.
//
// CONTRACT:
//   - Construct via NewLifecycleHook; nil service is rejected.
//   - Implements hooks.Hook interface for registration in HookSystem.
//   - Observes tool events (after_execute, execution_failed) and delivers nudges.
//   - Never blocks tool execution (always returns ActionContinue).
//   - Priority 20 (runs after permission/steering hooks, delivers nudges).
//   - Calls Service.RunTurn() on each EventToolAfterExecute.
//   - Tracks itersSinceSkill counter for intermediate nudge delivery.
//   - Detects error→resolved pattern and fires error-resolution nudges.
type LifecycleHook struct {
	service *Service

	mu              sync.Mutex
	itersSinceSkill uint
	hadRecentError  bool
	lastNudgeAt     uint
}

// NewLifecycleHook creates a hook that drives the given service.
// CONTRACT: svc must be non-nil.
func NewLifecycleHook(svc *Service) (*LifecycleHook, error) {
	if svc == nil {
		return nil, ErrDisabled
	}
	return &LifecycleHook{service: svc}, nil
}

// Name returns the hook identifier.
func (h *LifecycleHook) Name() string {
	return "autogenskills"
}

// Priority returns 91 so skill review wins shared-budget arbitration over task
// maintenance (90), while remaining below block-resolution enforcement (95).
func (h *LifecycleHook) Priority() int {
	return 91
}

// Filter returns true for tool events we care about.
func (h *LifecycleHook) Filter(event hooks.Event) bool {
	switch event.Type {
	case hooks.EventToolAfterExecute, hooks.EventToolExecutionFailed:
		return true
	}
	return false
}

// OnEvent translates hook events into service metric updates and delivers nudges.
// CONTRACT:
//   - Always returns ActionContinue (never blocks tool execution).
//   - Errors are logged but do not stop event processing.
//   - On EventToolAfterExecute: tracks itersSinceSkill, detects error resolution,
//     calls RunTurn(), and returns ContinueWithMessage if a nudge is warranted.
//   - On EventToolExecutionFailed: records the error for resolution detection.
func (h *LifecycleHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	switch event.Type {
	case hooks.EventToolAfterExecute:
		return h.handleToolAfterExecute(ctx, event)
	case hooks.EventToolExecutionFailed:
		return h.handleToolExecutionFailed(ctx, event)
	}
	return hooks.Continue(), nil
}

// eventIndicatesFailure reports whether an EventToolAfterExecute payload
// carries a failure signal. The TUI and client SDK never emit
// EventToolExecutionFailed — they fold failures into after-execute via
// Data["error"] and tool_output{success:false} — so the hook must detect
// failure from the data shape or the whole error-tracking loop is dead.
func eventIndicatesFailure(event hooks.Event) bool {
	return hooks.ClassifyToolFailure(event).Failed
}

// handleToolAfterExecute processes tool execution completion events.
// Failed executions (detected from the event payload) are routed to the
// failure path — they must not be treated as the success that "resolves" a
// prior error.
func (h *LifecycleHook) handleToolAfterExecute(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	if eventIndicatesFailure(event) {
		return h.handleToolExecutionFailed(ctx, event)
	}

	toolName, _ := event.Data["tool_name"].(string)

	h.mu.Lock()

	// Update itersSinceSkill counter
	skillUsed := false
	if isSkillTool(toolName) {
		h.itersSinceSkill = 0
		h.lastNudgeAt = 0
		skillUsed = true
	} else {
		h.itersSinceSkill++
	}

	// Capture and clear error state before unlocking
	hadError := h.hadRecentError
	h.hadRecentError = false

	iters := h.itersSinceSkill
	lastNudge := h.lastNudgeAt

	h.mu.Unlock()

	// Call service metric updates (existing behavior)
	h.service.HandleToolCall()

	// Track skill usage for curator lifecycle tracking (last_used_at)
	if skillUsed {
		// Extract skill name from event data. SkillManage uses "name";
		// other skill tools may use "skill_name". We check both.
		var skillName string
		if sn, ok := event.Data["skill_name"].(string); ok && sn != "" {
			skillName = sn
		} else if n, ok := event.Data["name"].(string); ok && n != "" {
			skillName = n
		}
		if skillName != "" {
			h.service.MarkSkillUsed(skillName)
		}
	}

	// Handle error resolution
	if hadError {
		h.service.HandleErrorResolved()
	}

	// Build nudge message from all sources
	var nudgeParts []string

	// Error-resolution nudge
	if hadError {
		trigger := h.service.cfg.Trigger.WithDefaults()
		if trigger.ErrorResolutionThreshold > 0 {
			snap := h.service.Snapshot()
			if snap.ErrorResolvedCount >= uint64(trigger.ErrorResolutionThreshold) {
				nudgeParts = append(nudgeParts,
					"[SKILL REVIEW] You just resolved an error. Preserve the reusable part class-first: patch the loaded skill, extend an existing umbrella, or add a support file before creating a new class-level skill. If the fix was environment-specific or non-reusable, record a no-mutation review.")
			}
		}
	}

	// Intermediate nudge (itersSinceSkill exceeded nudge interval)
	trigger := h.service.cfg.Trigger.WithDefaults()
	if completionNudge := taskCompletionSkillReviewNudge(event, iters); completionNudge != "" {
		// Part of the "join task-enforcement + skill-budget-enforcement"
		// change: a task just transitioning to completed is a more natural
		// moment to ask "was there a reusable pattern?" than an arbitrary
		// tool-count threshold, so it preempts the count-based nudge below
		// for this turn. The count-based nudge remains as a ceiling/fallback
		// for long single tasks that never complete (see the else branch).
		nudgeParts = append(nudgeParts, completionNudge)

		h.mu.Lock()
		h.itersSinceSkill = 0
		h.lastNudgeAt = 0
		h.mu.Unlock()
	} else if iters > trigger.NudgeInterval && iters > lastNudge {
		nudgeParts = append(nudgeParts,
			fmt.Sprintf("[SKILL REVIEW] You've made %d tool calls since the last skill review. Preserve useful learning without creating one-session clutter: first patch a loaded skill, then an existing class-level umbrella, then add a support file. Create a new class-level skill only if none fits. If there is genuinely nothing reusable, call SkillManage(action: \"review\", review_reason: \"nothing reusable to save\") so work can continue without manufacturing a skill.", iters))

		h.mu.Lock()
		h.itersSinceSkill = 0
		h.lastNudgeAt = 0
		h.mu.Unlock()
	}

	// Call RunTurn and incorporate any fragment
	turnFrag := h.service.RunTurn()
	if !turnFrag.IsZero() {
		nudgeParts = append(nudgeParts, turnFrag.String())
	}

	if len(nudgeParts) > 0 {
		var msg strings.Builder
		for i, p := range nudgeParts {
			if i > 0 {
				msg.WriteString("\n\n")
			}
			msg.WriteString(p)
		}
		seq, ok := hooks.DefaultMetaNudgeBudget().TryClaim(
			hooks.NudgeSessionID(ctx, event), hooks.MetaNudgeSkillReview)
		if !ok {
			return hooks.Continue(), nil
		}
		return hooks.ContinueWithMessage(
			hooks.WrapReminder(h.Name(), "review", seq, msg.String())), nil
	}

	return hooks.Continue(), nil
}

// handleToolExecutionFailed processes tool error events.
func (h *LifecycleHook) handleToolExecutionFailed(_ context.Context, _ hooks.Event) (hooks.HookResult, error) {
	h.service.HandleError()

	h.mu.Lock()
	h.hadRecentError = true
	h.mu.Unlock()

	// No nudge on error — the nudge comes after resolution
	return hooks.Continue(), nil
}

// taskCompletionSkillReviewNudge returns a "[SKILL REVIEW]" nudge message if
// this after-execute event is a TaskManage (or legacy TaskUpdate) call that
// just completed a task, and at least one non-skill tool call has happened
// since the last skill review (iters > 0 — a task created and immediately
// marked complete with zero work in between is not worth nudging about).
// Returns "" if the event isn't a task-completion event or iters is 0.
//
// This is what makes the skill-review nudge lifecycle-linked rather than
// purely tool-count-based: finishing a chunk of work is a more natural
// moment to ask "was there a reusable pattern here?" than an arbitrary
// count of tool calls landing mid-task. See the "join task-enforcement +
// skill-budget-enforcement" change.
func taskCompletionSkillReviewNudge(event hooks.Event, iters uint) string {
	// iters is the POST-increment itersSinceSkill count, and this
	// completion call itself always counts as one non-skill tool call (it's
	// not a skill tool). So iters == 1 means "this completion event was the
	// ONLY non-skill call since the last skill review" — i.e. a task
	// created and immediately marked complete with no other work in
	// between, which isn't worth nudging about.
	if iters <= 1 {
		return ""
	}
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		toolName, _ = event.Data["name"].(string)
	}

	completed := false
	switch {
	case ii.IsTaskManageEventName(toolName):
		operations, _, ok := ii.SuccessfulTaskManageEventOperations(event.Data)
		if ok {
			for _, operation := range operations {
				if operation.Kind == ii.TaskOperationUpdate && operation.Status == "completed" {
					completed = true
					break
				}
			}
		}
	case hooks.NormalizeToolName(toolName) == "taskupdate":
		if params, ok := event.Data["params"].(map[string]any); ok {
			if status, _ := params["status"].(string); status == "completed" {
				completed = true
			}
		}
	}
	if !completed {
		return ""
	}

	return fmt.Sprintf(
		"[SKILL REVIEW] You just completed a task after %d tool call(s) since the last skill review. "+
			"Before moving on: was there a reusable pattern? Patch a loaded skill, extend an existing "+
			"umbrella, add a support file, or record a no-mutation review via "+
			"SkillManage(action:\"review\", review_reason:\"...\").",
		iters)
}

// MustRegister is a convenience helper that creates and registers the hook.
// Panics if the service is nil (use only in init/startup paths where this
// is a programming error, not a runtime condition).
// CONTRACT: Only panics on programmer error; never on valid config.
func MustRegister(svc *Service) *LifecycleHook {
	hook, err := NewLifecycleHook(svc)
	if err != nil {
		panic("autogenskills: MustRegister called with nil service")
	}
	return hook
}
