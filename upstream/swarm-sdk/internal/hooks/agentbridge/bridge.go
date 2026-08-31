// Package agentbridge adapts a *hooks.Manager into the agent.HooksManager
// interface so SDK consumers can attach any hooks.Manager to an agent without
// hand-rolling a translation layer.
//
// Typical usage:
//
//	mgr := hooks.NewManager(hooks.ManagerConfig{})
//	mgr.Register(myHook, hooks.ScopeGlobal, "")
//
//	ag, _ := agent.New(agent.Config{
//	    ...
//	    HooksManager: agentbridge.New(mgr),
//	})
//
// The caller retains the *hooks.Manager reference, which means runtime hook
// toggling works naturally: mgr.SetEnabled(name, false) flips the bit under
// the manager's RWMutex and the next tool emission sees the new state — no
// agent restart, no unregister/register dance, no dropped in-flight calls.
package agentbridge

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Bridge wraps a *hooks.Manager and implements agent.HooksManager.
// The zero value is not useful — construct with New.
type Bridge struct {
	mgr *hooks.Manager
}

// New returns a Bridge that forwards agent tool-lifecycle events to mgr.
// The returned *Bridge implements agent.HooksManager and is safe to share
// across goroutines (the wrapped manager handles its own locking).
func New(mgr *hooks.Manager) *Bridge {
	return &Bridge{mgr: mgr}
}

// Manager returns the wrapped *hooks.Manager so callers can keep a single
// reference for both agent wiring and runtime mutation (Enable, Disable,
// SetEnabled, Register, Unregister, List).
func (b *Bridge) Manager() *hooks.Manager {
	return b.mgr
}

// EmitToolBeforeExecute fires an EventToolBeforeExecute through the manager
// and translates each HookOutput into an agent.HookResult. A blocking hook
// surfaces both via the returned error (so the agent aborts the tool call)
// and via the results slice's Blocked flag (for UI display).
func (b *Bridge) EmitToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) ([]agent.HookResult, error) {
	if b == nil || b.mgr == nil {
		return nil, nil
	}
	evt := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		// Identity is carried on the context by the agent (tools.WithOwnerInfo,
		// internal/agent/agent_tools.go) and was previously dropped here, leaving
		// every tool event un-attributable. Read-only: population does not change
		// which hooks run (no hook is registered with ScopeConversation).
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"tool_name": toolName,
			"params":    params,
		},
	}
	res, err := b.mgr.EmitWithResult(ctx, evt)
	return convert(res), err
}

// EmitToolAfterExecute fires an EventToolAfterExecute through the manager.
// The returned slice never carries an error — after-tool hooks are strictly
// observational from the agent's perspective.
func (b *Bridge) EmitToolAfterExecute(ctx context.Context, toolName string, params map[string]any, result any, execErr error) []agent.HookResult {
	if b == nil || b.mgr == nil {
		return nil
	}
	data := map[string]any{
		"tool_name": toolName,
		"params":    params,
		"result":    result,
	}
	if execErr != nil {
		data["error"] = execErr.Error()
	}
	evt := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data:           data,
		// Typed structured outcome (G4). The tool already computed its own
		// exit code / file effects; this lifts them onto the event so hooks
		// read a typed struct instead of regexing the tool's prose. Nil when
		// the tool reports no structured outcome. Constructed here, above
		// internal/tools, because internal/tools depends transitively on
		// internal/hooks — hooks itself can never import tools.
		ToolOutcome: tools.NamedOutcomeOf(toolName, result),
	}
	res, _ := b.mgr.EmitWithResult(ctx, evt)
	return convert(res)
}

// EmitProviderResponse fires an EventProviderAfterResponse through the manager.
// Provider-response hooks are observational (no blocking behaviour), so this
// method returns nothing and ignores hook output.
//
// Identity (G2 follow-up, PLAN.md): populated the same way as
// EmitToolBeforeExecute/EmitToolAfterExecute above — from ctx via
// tools.WithOwnerInfo, not from any new parameter. The caller
// (agent_execute_chain.go) must stamp ctx before invoking this method; the
// provider round-trip's own context is not a tool-call context and does not
// carry owner info for free.
func (b *Bridge) EmitProviderResponse(ctx context.Context, providerName, model string, inputTokens, outputTokens int, durationMs int64) {
	if b == nil || b.mgr == nil {
		return
	}
	evt := hooks.Event{
		Type:           hooks.EventProviderAfterResponse,
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"provider":    providerName,
			"model":       model,
			"tokens_used": inputTokens + outputTokens,
			"usage": map[string]any{
				"input":  inputTokens,
				"output": outputTokens,
			},
			"duration_ms": durationMs,
		},
	}
	_, _ = b.mgr.Emit(ctx, evt)
}

// convert translates ExecutionResult.HookOutputs into []agent.HookResult. A
// nil res is treated as "no hooks fired" and yields a nil slice — callers
// distinguish "hook ran and returned empty output" from "no hook ran" by
// slice length.
func convert(res *hooks.ExecutionResult) []agent.HookResult {
	if res == nil || len(res.HookOutputs) == 0 {
		return nil
	}
	out := make([]agent.HookResult, 0, len(res.HookOutputs))
	for _, ho := range res.HookOutputs {
		out = append(out, agent.HookResult{
			HookName: ho.HookName,
			Success:  ho.Success,
			Output:   ho.Output,
			Blocked:  res.Blocked && res.BlockedBy == ho.HookName,
			Error:    ho.Error,
			// AdditionalContext mirrors Output so hooks that emit guidance text
			// land in the agent's next-message context (matching the TUI's
			// existing behaviour in EmitToolBeforeExecute).
			AdditionalContext: ho.Output,
		})
	}
	return out
}
