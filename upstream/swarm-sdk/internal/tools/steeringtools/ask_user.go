package steeringtools

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// AskUserParams asks the user a single question through the interaction
// surface, pausing the subject until the user replies (or the timeout fires).
type AskUserParams struct {
	Question       string `json:"question" description:"Question to surface to the user. Must be answerable in one sentence." required:"true"`
	Default        string `json:"default,omitempty" description:"Default answer used if the user does not respond within the timeout"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" description:"How long to wait for the user's response. Defaults to 60s."`
}

// AskUserTool is the escalation primitive. Phase 4: when an AskUserPusher
// is present on ctx, calls it synchronously and surfaces the answer back
// to the observer LLM. When no pusher is present (Phase 3 path), records
// intent on the SteeringTarget and returns the default immediately.
type AskUserTool struct{ tools.BaseTool }

// NewAskUserTool constructs the typed ask_user tool.
func NewAskUserTool() tools.Tool { return tools.Typed[AskUserParams](&AskUserTool{}) }

// Name returns the tool name.
func (t *AskUserTool) Name() string { return "ask_user" }

// Description tells the steering agent when to use this tool.
func (t *AskUserTool) Description() string {
	return `Escalate a steering decision to the user.

Use this when a soft cap is breached and a mechanical decision would be wrong
(e.g., the agents may be in a productive divergence rather than a dead loop).
Surfaces a single yes/no or short-answer prompt to the user, then returns
their choice. Use sparingly — escalations are interruptive.`
}

// Parameters returns the JSON schema for the params struct.
func (t *AskUserTool) Parameters() any { return tools.SchemaFor[AskUserParams]() }

// Run dispatches based on whether an AskUserPusher is present on ctx.
//
// Phase 4 (sync) path: pusher present → call Ask synchronously, return
// user's answer in result.Metadata["answer"]. On error or empty answer,
// fall back to params.Default if supplied.
//
// Phase 3 (record) path: no pusher → record intent on the target and
// return params.Default. Preserves backward compatibility with the older
// fire-and-forget setup.
func (t *AskUserTool) Run(ctx context.Context, params AskUserParams) (*tools.ToolResult, error) {
	timeoutSecs := params.TimeoutSeconds
	if timeoutSecs <= 0 {
		timeoutSecs = 60
	}
	timeout := time.Duration(timeoutSecs) * time.Second

	if pusher, ok := agent.AskUserPusherFromContext(ctx); ok {
		answer, err := pusher.Ask(ctx, params.Question, "normal", timeout)
		result := tools.NewXMLResult(tools.NewXML("result").
			Attr("status", "ok").
			Attr("action", "ask_user").
			Attr("mode", "sync"))
		result.Metadata["question"] = params.Question
		result.Metadata["timeout_seconds"] = timeoutSecs
		if err != nil {
			result.Metadata["error"] = err.Error()
			if params.Default != "" {
				result.Metadata["answer"] = params.Default
			}
			return result, nil
		}
		if answer == "" {
			// Pusher returned empty (e.g., nil-fn fallback). Treat as
			// timeout: surface default if supplied.
			if params.Default != "" {
				result.Metadata["answer"] = params.Default
			}
			return result, nil
		}
		result.Metadata["answer"] = answer
		return result, nil
	}

	// Fall back to Phase 3 behavior: record intent, return default.
	if target, ok := agent.SteeringTargetFromContext(ctx); ok {
		target.RecordAskUser(params.Question, "normal")
	}
	result := tools.NewXMLResult(tools.NewXML("result").
		Attr("status", "ok").
		Attr("action", "ask_user").
		Attr("mode", "stub"))
	result.Metadata["question"] = params.Question
	result.Metadata["timeout_seconds"] = timeoutSecs
	if params.Default != "" {
		result.Metadata["answer"] = params.Default
	}
	return result, nil
}
