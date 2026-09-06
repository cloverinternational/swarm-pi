package builtin

import (
	"context"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/lifecycle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// TaskNudgeHook provides a built-in hook for task nudging.
// It can be registered as a lifecycle hook that fires after agent responses (EventPostMessage)
// when the task list is empty, suggesting task creation for complex work.
//
// Cache-safe because it operates OUTSIDE the system prompt and agent execution flow,
// preventing byte-level instability that breaks prompt caching.
//
// Usage in lifecycle hooks config:
//
//	PostMessage:
//	  - matcher: "*"
//	    hooks:
//	      - name: "task-nudge"
//	        type: "command"
//	        command: "swarmos task-nudge"
type TaskNudgeHook struct{}

const taskNudgeText = "[Task Nudge] Multi-step work detected with no active tasks — consider TaskManage."

// TaskNudgeConfig controls the turn budget for task nudges. Like the skill
// budget config, its zero value is safe and WithDefaults applies product
// defaults.
type TaskNudgeConfig struct {
	NudgeInterval     uint `json:"nudge_interval"`
	ToolCallThreshold uint `json:"tool_call_threshold"`
}

// WithDefaults returns a copy with the default turn cadence and activity
// threshold applied.
func (c TaskNudgeConfig) WithDefaults() TaskNudgeConfig {
	if c.NudgeInterval == 0 {
		c.NudgeInterval = 5
	}
	if c.ToolCallThreshold == 0 {
		c.ToolCallThreshold = 2
	}
	return c
}

type taskNudgeState struct {
	turns     uint
	toolCalls uint
	lastNudge uint
}

// TaskNudgeBudget tracks per-conversation turns and tool activity.
type TaskNudgeBudget struct {
	mu     sync.Mutex
	cfg    TaskNudgeConfig
	states map[string]taskNudgeState
}

// NewTaskNudgeBudget creates a turn-budgeted task nudge tracker.
func NewTaskNudgeBudget(cfg TaskNudgeConfig) *TaskNudgeBudget {
	return &TaskNudgeBudget{
		cfg:    cfg.WithDefaults(),
		states: make(map[string]taskNudgeState),
	}
}

// RecordToolCall records evidence of multi-step work for a conversation.
func (b *TaskNudgeBudget) RecordToolCall(conversationID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	state := b.states[conversationID]
	state.toolCalls++
	b.states[conversationID] = state
}

// CheckTurn advances the user-turn budget and returns a nudge only after
// multi-step tool activity, never on turn one, and no more often than the
// configured cadence.
func (b *TaskNudgeBudget) CheckTurn(ctx context.Context, conversationID, prompt string) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	hooks.DefaultMetaNudgeBudget().RecordUserTurn(conversationID)
	state := b.states[conversationID]
	state.turns++
	b.states[conversationID] = state

	if state.turns == 1 || state.toolCalls < b.cfg.ToolCallThreshold {
		return "", false
	}
	if state.lastNudge != 0 && state.turns-state.lastNudge < b.cfg.NudgeInterval {
		return "", false
	}
	if !canTaskNudge(ctx, prompt) {
		return "", false
	}
	seq, ok := hooks.DefaultMetaNudgeBudget().TryClaim(conversationID, hooks.MetaNudgeTask)
	if !ok {
		return "", false
	}

	state.lastNudge = state.turns
	b.states[conversationID] = state
	return hooks.WrapReminder("task-nudge", "nudge", seq, taskNudgeMessage()), true
}

// CheckAndNudge examines the current task state and returns a nudge message if appropriate.
// This is called after agent responses to determine if a nudge is warranted.
//
// Nudge only fires when the task list is entirely empty — i.e., no task has ever
// been created in this session. Once the agent has created any task (even if it's
// since been completed), the nudge stays silent; the agent has already adopted the
// tracking habit and re-nudging becomes noise. Previously we checked only pending
// + in_progress, which caused the nudge to re-fire after every short task was
// marked completed.
func CheckAndNudge(ctx context.Context) (string, bool) {
	return CheckAndNudgeWithPrompt(ctx, "")
}

// CheckAndNudgeWithPrompt is the prompt-aware variant of CheckAndNudge. It
// suppresses the nudge in two additional cases that empirically produce noise
// rather than signal:
//
//   - Direct imperative prompts ("just run X", "merge Y", "show me Z"). Forcing
//     formal planning on a single-step user command is exactly the friction the
//     2026-04-27 survey flagged as the top issue ("just run bash sentry dumbass").
//   - Continuation prompts ("continue", "go ahead", "keep going", "proceed"). If
//     the user is resuming prior work, we trust that prior context; we do not
//     interrupt with a "you have no tasks" banner.
//
// Pass an empty prompt (or use CheckAndNudge) when the caller has no prompt
// context available — the function falls back to the simple count-based rule.
func CheckAndNudgeWithPrompt(ctx context.Context, prompt string) (string, bool) {
	if !canTaskNudge(ctx, prompt) {
		return "", false
	}
	return taskNudgeMessage(), true
}

func canTaskNudge(ctx context.Context, prompt string) bool {
	mgr := ii.TodoManagerFromContext(ctx)
	if mgr == nil {
		mgr = ii.GetTodoManager()
	}
	if mgr == nil {
		return false
	}

	if len(mgr.ByOwner("")) != 0 {
		return false
	}

	if promptSuggestsImmediateExecution(prompt) {
		return false
	}
	return true
}

// promptSuggestsImmediateExecution returns true when the user's prompt is a
// short imperative or continuation that does not warrant interrupting the turn
// with a task-tracking banner. This is intentionally conservative: it only
// suppresses on signals strong enough that a human reviewer would agree "the
// user just told the agent what to do". Anything ambiguous still gets the nudge.
func promptSuggestsImmediateExecution(prompt string) bool {
	trimmed := strings.TrimSpace(prompt)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)

	// Continuation signals. If the user is resuming prior work, the prior
	// context already anchors intent — a "no active tasks" banner is noise.
	continuations := []string{
		"continue", "keep going", "go ahead", "proceed", "resume", "carry on",
	}
	for _, c := range continuations {
		if lower == c || strings.HasPrefix(lower, c+" ") || strings.HasPrefix(lower, c+",") {
			return true
		}
	}

	// Direct imperatives. We only suppress when the imperative is short
	// (<100 chars) — a long prompt that happens to start with "run" is
	// probably describing a larger task worth tracking.
	if len(trimmed) < 100 {
		imperatives := []string{
			"run ", "just run ", "please run ", "go run ",
			"show ", "show me ", "print ", "cat ", "ls ", "echo ",
			"check ", "verify ", "confirm ",
			"merge ", "pull ", "push ", "commit ", "rebase ", "fetch ",
			"fix this ", "fix it", "undo ", "revert ",
			"open ", "build ", "test ", "deploy ", "restart ",
		}
		for _, im := range imperatives {
			if strings.HasPrefix(lower, im) {
				return true
			}
		}
	}

	return false
}

// taskNudgeMessage returns the terse, single-line nudge text.
func taskNudgeMessage() string {
	return taskNudgeText
}

// CanNudge returns true if a task nudge would be shown (for testing/visibility).
func CanNudge() bool {
	mgr := ii.GetTodoManager()
	if mgr == nil {
		return false
	}
	return mgr.Count() == 0
}

// Execute runs the task nudge check for use in lifecycle hooks.
// Returns the nudge message if applicable, empty string otherwise.
func (h *TaskNudgeHook) Execute(ctx context.Context) (string, error) {
	msg, _ := CheckAndNudge(ctx)
	if msg != "" {
		msg = hooks.WrapReminder("task-nudge", "nudge", hooks.NextReminderSeq("task-nudge"), msg)
	}
	return msg, nil
}

// ExecuteAsHook is the standard lifecycle hook execution interface
// This returns a HookDecision with the nudge as SystemMessage
func (h *TaskNudgeHook) ExecuteAsHook(ctx context.Context, hookCtx *lifecycle.HookContext) (*lifecycle.HookDecision, error) {
	msg, shouldNudge := CheckAndNudge(ctx)

	decision := &lifecycle.HookDecision{
		Decision: "allow", // Always allow - this is informational only
	}

	if shouldNudge && msg != "" {
		seq, ok := hooks.DefaultMetaNudgeBudget().TryClaim(hookCtx.ConversationID, hooks.MetaNudgeTask)
		if ok {
			decision.SystemMessage = hooks.WrapReminder("task-nudge", "nudge", seq, msg)
		}
	}

	return decision, nil
}
