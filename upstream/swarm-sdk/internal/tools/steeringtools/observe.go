package steeringtools

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ObserveOnlyParams carries an optional note explaining why the steering
// agent chose not to intervene.
type ObserveOnlyParams struct {
	Note string `json:"note,omitempty" description:"Optional brief note explaining why no intervention is needed"`
}

// ObserveOnlyTool is the steering agent's no-op acknowledgement. It exists
// so that the agent can explicitly mark events as "seen, nothing to do" —
// which is far better for evals than the agent silently producing no tool
// call at all (which is indistinguishable from a hang).
type ObserveOnlyTool struct{ tools.BaseTool }

// NewObserveOnlyTool constructs the typed observe_only tool.
func NewObserveOnlyTool() tools.Tool { return tools.Typed[ObserveOnlyParams](&ObserveOnlyTool{}) }

// Name returns the tool name.
func (t *ObserveOnlyTool) Name() string { return "observe_only" }

// Description tells the steering agent when to use this tool.
func (t *ObserveOnlyTool) Description() string {
	return `Acknowledge an observed event without intervening.

Use this when the subject agent's action appears on-task and no steering is needed.
This emits an audit row so the absence of intervention is explicit and reviewable.
Always preferable to emitting no tool call at all.`
}

// Parameters returns the JSON schema for the params struct.
func (t *ObserveOnlyTool) Parameters() any { return tools.SchemaFor[ObserveOnlyParams]() }

// Run records the observation. Phase 1: stub — returns ok without
// side-effects. Phase 4 will route this through observability.Audit.
func (t *ObserveOnlyTool) Run(_ context.Context, params ObserveOnlyParams) (*tools.ToolResult, error) {
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "observe_only"))
	if params.Note != "" {
		result.Metadata["note"] = params.Note
	}
	return result, nil
}
