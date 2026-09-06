package tools

import "github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"

// OutcomeOf extracts the typed structured outcome from a tool execution
// result, returning nil when the tool did not report one.
//
// It exists so that every hook adapter that builds an AfterTool event
// (internal/hooks/agentbridge, client's harness hooks manager, the TUI hooks
// manager) needs exactly one additive line and no per-tool special-casing.
// Those adapters receive the result as `any` because agent.HooksManager is
// deliberately decoupled from the tool types; this is the single place that
// knows the concrete shape.
//
// The returned outcome is a deep copy: a hook observing an event can never
// reach back and mutate the live *ToolResult the agent is about to return.
// Observation must not be able to change execution.
//
// This helper lives in package tools rather than package hooks on purpose —
// internal/tools already depends transitively on internal/hooks, so hooks can
// never import tools. The shared vocabulary lives in the dependency-free
// internal/toolout leaf that both sides import.
func OutcomeOf(result any) *toolout.Outcome {
	switch v := result.(type) {
	case *ToolResult:
		if v == nil {
			return nil
		}
		return v.Outcome.Clone()
	case ToolResult:
		return v.Outcome.Clone()
	case *toolout.Outcome:
		return v.Clone()
	default:
		return nil
	}
}

// NamedOutcomeOf is OutcomeOf with the tool name backfilled when the tool
// itself left Outcome.Tool empty. Adapters always know the tool name; most
// tools would have to thread their own name through helper functions to set
// it, so the adapter fills it instead.
func NamedOutcomeOf(toolName string, result any) *toolout.Outcome {
	outcome := OutcomeOf(result)
	if outcome != nil && outcome.Tool == "" {
		outcome.Tool = toolName
	}
	return outcome
}
