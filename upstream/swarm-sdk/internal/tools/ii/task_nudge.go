// Package ii provides shared state management for productivity tools.
package ii

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/reminder"
)

// TaskNudgeInterval is the default minimum number of user turns between task
// nudges. It mirrors the configurable budget/cadence pattern used by the skill
// budget nudge.
const TaskNudgeInterval = 5

// TaskNudgeReminder is the hidden prompt text injected after multi-step tool
// activity when no task has ever been created.
//
// It is appended to the system prompt for that one request only — never stored,
// never shown in the TUI, never on intermediate tool-call turns.
const TaskNudgeReminder = `[Task Nudge] Multi-step work detected with no active tasks — consider TaskManage.`

// BuildTaskNudgeFn returns a closure implementing intelligent task-nudge injection.
//
// DEPRECATED: This function produces ephemeral system prompt text that is
// invisible, not persistent, and breaks prompt caching. The TUI now implements
// task nudging as a PostToolUse hook that returns persistent RoleUser messages.
// Headless servers should migrate to the same hook-based approach.
//
// toolCallsGetter is optional (may be nil).  When provided it should return
// the number of tool calls executed in the current Execute() lifecycle.
// Typically callers pass agt.ToolCallsTotal.
//
// DEPRECATED: Do not use with SetEphemeralSystemFn. The TUI implements task
// nudging as a PostToolUse hook returning persistent RoleUser messages.
func BuildTaskNudgeFn(toolCallsGetter func() int) func([]*conversation.Message) string {
	return BuildTaskNudgeFnWithInterval(toolCallsGetter, TaskNudgeInterval)
}

// BuildTaskNudgeFnWithInterval is BuildTaskNudgeFn with a configurable
// user-turn cadence.
func BuildTaskNudgeFnWithInterval(toolCallsGetter func() int, interval int) func([]*conversation.Message) string {
	mgr := GetTodoManager()
	if interval <= 0 {
		interval = TaskNudgeInterval
	}
	lastNudgeTurn := 0
	seq := 0
	return func(msgs []*conversation.Message) string {
		if len(msgs) == 0 {
			return ""
		}
		lastMsg := msgs[len(msgs)-1]
		if lastMsg.Role != conversation.RoleUser {
			return ""
		}
		// Also skip if the last user message itself contains tool results.
		if len(lastMsg.ToolResults) > 0 {
			return ""
		}
		userTurns := 0
		for _, msg := range msgs {
			if msg.Role == conversation.RoleUser && len(msg.ToolResults) == 0 {
				userTurns++
			}
		}
		if userTurns <= 1 || toolCallsGetter == nil || toolCallsGetter() < 2 {
			return ""
		}
		if lastNudgeTurn != 0 && userTurns-lastNudgeTurn < interval {
			return ""
		}
		if mgr == nil || mgr.Count() != 0 {
			return ""
		}
		lastNudgeTurn = userTurns
		seq++
		return reminder.Wrap("task-nudge", "nudge", seq, TaskNudgeReminder)
	}
}

// BuildPeriodicNudgeText is retained for API compatibility. Periodic tool-call
// nudges were replaced by the turn budget; callers now receive only the terse
// no-task message, and no reminder when a task exists.
func BuildPeriodicNudgeText(_ int, inProgress, pending []TodoItem) string {
	if len(inProgress) > 0 || len(pending) > 0 {
		return ""
	}
	return reminder.Wrap("task-nudge", "nudge", 1, TaskNudgeReminder)
}

// TaskNudgeReminderText returns the raw reminder text for testing purposes.
func TaskNudgeReminderText() string {
	return strings.TrimSpace(TaskNudgeReminder)
}
