package chat

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// TaskNudgeActive returns true when the task nudge would fire for the given
// message history. Exported for diagnostics and tests.
//
// NOTE: The actual task nudge is implemented as a built-in PostToolUse hook
// inside HooksManager.EmitToolAfterExecute. It fires every ii.TaskNudgeInterval
// tool calls, surfaces in the TUI as a BlockHook, and injects its text
// ephemerally into the next API request system prompt -- never as a stored
// conversation message.
func TaskNudgeActive(msgs []*conversation.Message) bool {
	fn := ii.BuildTaskNudgeFn(nil)
	return fn(msgs) != ""
}

// taskNudgeReminderText returns the raw reminder text for snapshot testing.
func taskNudgeReminderText() string {
	return ii.TaskNudgeReminderText()
}
