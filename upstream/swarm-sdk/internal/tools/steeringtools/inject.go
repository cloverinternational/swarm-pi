package steeringtools

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// InjectSystemNoteParams carries a short note the steering agent wants
// prepended to the subject agent's system prompt on its next turn.
type InjectSystemNoteParams struct {
	Text     string `json:"text" description:"Brief steering note to inject into the subject agent's system prompt (≤ 280 chars recommended)" required:"true"`
	Priority int    `json:"priority,omitempty" description:"Optional priority (0 = lowest, 100 = critical). Higher priority notes are placed earlier."`
}

// InjectSystemNoteTool prepares to mutate the subject agent's system prompt
// with a brief steering instruction. Phase 1: records intent only.
type InjectSystemNoteTool struct{ tools.BaseTool }

// NewInjectSystemNoteTool constructs the typed inject_system_note tool.
func NewInjectSystemNoteTool() tools.Tool {
	return tools.Typed[InjectSystemNoteParams](&InjectSystemNoteTool{})
}

// Name returns the tool name.
func (t *InjectSystemNoteTool) Name() string { return "inject_system_note" }

// Description tells the steering agent when to use this tool.
func (t *InjectSystemNoteTool) Description() string {
	return `Inject a brief steering note into the subject agent's system prompt on its next turn.

Use this when you observe the subject drifting from the user's intent but the
drift is mild — a sentence-long course correction is enough. Notes are
appended to the prompt (not replacing it). Keep them short.`
}

// Parameters returns the JSON schema for the params struct.
func (t *InjectSystemNoteTool) Parameters() any {
	return tools.SchemaFor[InjectSystemNoteParams]()
}

// Run queues the note on the SteeringTarget when present; otherwise
// falls back to the Phase-1 stub response (safe in target-less tests).
func (t *InjectSystemNoteTool) Run(ctx context.Context, params InjectSystemNoteParams) (*tools.ToolResult, error) {
	if target, ok := agent.SteeringTargetFromContext(ctx); ok {
		target.QueueSystemNote(params.Text, params.Priority)
	}
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "inject_system_note"))
	result.Metadata["text"] = params.Text
	if params.Priority != 0 {
		result.Metadata["priority"] = params.Priority
	}
	return result, nil
}

// ---------------------------------------------------------------------------

// RefocusParams carries an optional task anchor the steering agent wants the
// subject to return to.
type RefocusParams struct {
	Anchor   string `json:"anchor" description:"Short identifier or title of the task the subject should return focus to" required:"true"`
	Reminder string `json:"reminder,omitempty" description:"Optional one-sentence reminder about that task"`
}

// RefocusTool is a stronger variant of inject_system_note that emphasises
// returning to a specific anchor task. Phase 1: records intent only.
type RefocusTool struct{ tools.BaseTool }

// NewRefocusTool constructs the typed refocus tool.
func NewRefocusTool() tools.Tool { return tools.Typed[RefocusParams](&RefocusTool{}) }

// Name returns the tool name.
func (t *RefocusTool) Name() string { return "refocus" }

// Description tells the steering agent when to use this tool.
func (t *RefocusTool) Description() string {
	return `Refocus the subject agent on a specific anchor task.

Use this when the subject has drifted clearly off-task (e.g., wandered into
a tangent during a peer-DM exchange). Stronger than inject_system_note: this
sets an explicit anchor the subject should return its attention to.`
}

// Parameters returns the JSON schema for the params struct.
func (t *RefocusTool) Parameters() any { return tools.SchemaFor[RefocusParams]() }

// Run queues the refocus on the SteeringTarget when present.
func (t *RefocusTool) Run(ctx context.Context, params RefocusParams) (*tools.ToolResult, error) {
	if target, ok := agent.SteeringTargetFromContext(ctx); ok {
		target.QueueRefocus(params.Anchor, params.Reminder)
	}
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "refocus"))
	result.Metadata["anchor"] = params.Anchor
	if params.Reminder != "" {
		result.Metadata["reminder"] = params.Reminder
	}
	return result, nil
}
