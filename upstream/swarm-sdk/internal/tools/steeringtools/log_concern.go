package steeringtools

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// LogConcernParams carries a tagged observation the steering agent wants
// recorded for later review without acting on it now.
type LogConcernParams struct {
	Tag  string `json:"tag" description:"Short tag for grouping (e.g., 'drift', 'tool_misuse', 'token_burn')" required:"true"`
	Note string `json:"note" description:"One- or two-sentence description of the concern" required:"true"`
}

// LogConcernTool is the telemetry-only primitive. It writes an audit row but
// performs no intervention. Useful for collecting drift data without acting,
// so we can correlate the steering agent's self-assessment with outcomes
// before promoting it to a decision signal.
type LogConcernTool struct{ tools.BaseTool }

// NewLogConcernTool constructs the typed log_concern tool.
func NewLogConcernTool() tools.Tool { return tools.Typed[LogConcernParams](&LogConcernTool{}) }

// Name returns the tool name.
func (t *LogConcernTool) Name() string { return "log_concern" }

// Description tells the steering agent when to use this tool.
func (t *LogConcernTool) Description() string {
	return `Record a tagged concern without acting on it.

Use this for observations that are notable but not actionable on their own —
mild drift, repeated tool patterns, peer-status oddities. Concerns accumulate
into the audit log for later evaluation, but do not affect the subject's
execution. Pair with observe_only when you want to say "I saw something
worth noting but I'm not intervening."`
}

// Parameters returns the JSON schema for the params struct.
func (t *LogConcernTool) Parameters() any { return tools.SchemaFor[LogConcernParams]() }

// Run records the concern on the SteeringTarget. The tag is used as
// the SteeringTarget's "severity" slot — they're orthogonal labels but
// reusing the slot keeps the target's API narrow for Phase 2.
func (t *LogConcernTool) Run(ctx context.Context, params LogConcernParams) (*tools.ToolResult, error) {
	if target, ok := agent.SteeringTargetFromContext(ctx); ok {
		target.LogConcern(params.Note, params.Tag)
	}
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "log_concern"))
	result.Metadata["tag"] = params.Tag
	result.Metadata["note"] = params.Note
	return result, nil
}
