package agentbridge

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// This file is additive: it does not change how Bridge behaves on the full,
// blocking-capable path in bridge.go. It adds a second, narrower adapter for
// sub-agent and background runs (PLAN.md G3).
//
// The asymmetry between the two adapters IS the design:
//
//	Bridge.EmitToolBeforeExecute -> mgr.EmitWithResult -> Executor -> can block
//	observationalBridge.Observe* -> view.Observe        -> no Executor, no return
//
// Compile-time proof that the observational adapter satisfies the narrow
// interface and nothing wider.
var _ agent.ObservationalHooks = (*observationalBridge)(nil)
var _ agent.ObservationalHooksProvider = (*Bridge)(nil)

// ObservationalHooks returns a blocking-incapable view of the wrapped manager,
// restricted to hooks that implement hooks.ObservationalHook.
//
// Returns nil when there is no manager to observe. A nil agent.ObservationalHooks
// is what an agent treats as "emit nothing", so the caller does not need a
// special case.
//
// The underlying hooks.ObservationalView is memoized per manager, so repeated
// calls (one per sub-agent spawn) do not allocate additional goroutines or
// queues. Delegation sites must NOT close what they receive — they do not own
// it. CloseObservational exists for whoever owns the manager (embedders, tests).
func (b *Bridge) ObservationalHooks() agent.ObservationalHooks {
	if b == nil || b.mgr == nil {
		return nil
	}
	view := b.mgr.ObservationalView()
	if view == nil {
		return nil
	}
	return &observationalBridge{view: view}
}

// observationalBridge adapts hooks.ObservationalView to agent.ObservationalHooks.
//
// Note what is absent: no method here has a return value, so there is no line
// of code in this file that could propagate a hook verdict upward even by
// mistake. The compiler enforces it — adding a return would break the
// agent.ObservationalHooks assertion above.
type observationalBridge struct {
	view *hooks.ObservationalView
}

// ObserveToolBeforeExecute records a pending tool call.
func (o *observationalBridge) ObserveToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) {
	if o == nil || o.view == nil {
		return
	}
	o.view.Observe(ctx, hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		// Identity from ctx, exactly as the full path does (G2). Without it the
		// resulting rows are un-attributable, which is the failure mode that
		// made sub-agent work unmeasurable in the first place.
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"tool_name": toolName,
			"params":    params,
			// Marks the row's provenance so a consumer can tell an observed
			// sub-agent event from a fully-hooked parent event and compute the
			// coverage metric PLAN.md §6 requires.
			"observational": true,
		},
	})
}

// ObserveToolAfterExecute records a completed tool call, including the typed
// structured outcome that rung-2 evidence is computed from.
func (o *observationalBridge) ObserveToolAfterExecute(
	ctx context.Context,
	toolName string,
	params map[string]any,
	outcome *toolout.Outcome,
	output string,
	execErr error,
) {
	if o == nil || o.view == nil {
		return
	}
	data := map[string]any{
		"tool_name":     toolName,
		"params":        params,
		"observational": true,
		// The rendered tool text, not the live *tools.ToolResult. Go strings
		// are immutable, so this is both race-free and tamper-proof.
		"output": output,
	}
	if execErr != nil {
		data["error"] = execErr.Error()
	}
	o.view.Observe(ctx, hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data:           data,
		ToolOutcome:    outcome,
	})
}

// Stats exposes the underlying view's honest accounting (including dropped
// events) so a caller can report coverage rather than assume it.
func (o *observationalBridge) Stats() hooks.ObservationalStats {
	if o == nil || o.view == nil {
		return hooks.ObservationalStats{}
	}
	return o.view.Stats()
}

// CloseObservational shuts down an observational surface produced by
// ObservationalHooks, if it is one.
//
// This is for the OWNER of the hooks manager (an embedder shutting down, or a
// test). Delegation sites must not call it: the view is shared across every
// sub-agent derived from the same manager, so closing it from one spawn would
// silently blind all the others.
func CloseObservational(oh agent.ObservationalHooks) {
	if ob, ok := oh.(*observationalBridge); ok && ob != nil && ob.view != nil {
		ob.view.Close()
	}
}
