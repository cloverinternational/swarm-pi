package agent

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Observation without authority: the agent-side half of the sub-agent /
// background hook-coverage gap (PLAN.md G3).
// ---------------------------------------------------------------------------
//
// HooksManager (agent.go:437) is the full, blocking-capable hook surface:
// EmitToolBeforeExecute returns ([]HookResult, error) and the agent enforces
// both channels — the error and the per-result Blocked flag — as a hard block
// on the tool call (agent_tools.go:219). It also carries AdditionalContext,
// which the agent injects into conversation history as a first-class user
// message. That is authority: block, steer, inject.
//
// ObservationalHooks is deliberately a DIFFERENT interface rather than a
// wrapper around the same one, because the guarantee we need is a property of
// the method signatures:
//
//	EmitToolBeforeExecute(...) ([]HookResult, error)   <- can block
//	ObserveToolBeforeExecute(...)                      <- cannot; no returns
//
// Every method here returns nothing. There is no []HookResult to carry a
// Blocked flag, no error to abort a tool call, no *Event to substitute, and no
// AdditionalContext string to append to the conversation. The absence is
// enforced by the compiler at every call site, and asserted mechanically by
// TestObservationalHooks_InterfaceHasNoVerdictChannel.
//
// This is what lets a sub-agent gain coverage without gaining a steering
// surface, which is the exact objection recorded in the skip comment at
// internal/tools/builtin/subagent.go:694.

// ObservationalHooks receives tool lifecycle events for recording only.
//
// Implementations MUST NOT block the caller: the agent calls these methods
// inline on the tool path and does not run them in a goroutine, because doing
// so at this layer would reorder observations. The asynchrony lives one layer
// down, in hooks.ObservationalView.Observe, which does a bounded copy and a
// non-blocking channel send. Implementations that cannot make that promise
// must not be attached.
type ObservationalHooks interface {
	// ObserveToolBeforeExecute records that a tool call is about to run.
	//
	// params is the live parameter map. Implementations MUST copy anything
	// they retain past the call and MUST NOT mutate it: the agent passes the
	// same map it is about to hand to the tool.
	ObserveToolBeforeExecute(ctx context.Context, toolName string, params map[string]any)

	// ObserveToolAfterExecute records that a tool call finished.
	//
	// outcome is the typed structured outcome (nil when the tool does not
	// report one — never read nil as success). output is the tool's rendered
	// text; Go strings are immutable so passing it costs nothing and cannot be
	// tampered with. execErr is the execution error, or nil.
	//
	// The live *tools.ToolResult is deliberately NOT passed. It is a pointer
	// the agent is still using when this returns; handing it to an observer
	// would be both a data race and a mutation channel into the tool result
	// the model is about to read.
	ObserveToolAfterExecute(ctx context.Context, toolName string, params map[string]any, outcome *toolout.Outcome, output string, execErr error)
}

// ObservationalHooksProvider is implemented by hook managers that can hand out
// a blocking-incapable view of themselves.
//
// Delegation sites use this instead of wrapping an arbitrary HooksManager. The
// difference matters: wrapping would run EVERY hook (including blocking ones)
// and merely discard their verdicts, which pays their latency and their side
// effects. Requiring the manager to produce the view lets the manager restrict
// it to hooks that declared themselves observational.
//
// A manager that does not implement this yields NO observation. That is the
// intended failure mode: silence that can be measured, not a fallback that
// quietly re-enables the full hook set inside a sub-agent.
type ObservationalHooksProvider interface {
	ObservationalHooks() ObservationalHooks
}

// SetObservationalHooks attaches an observation-only hook surface to this
// agent.
//
// This is NOT a substitute for SetHooksManager and does not enable steering,
// blocking, or context injection. It is set on sub-agents and background
// agents, which have no HooksManager by design.
//
// Passing nil detaches. Attaching is the only thing that makes observation
// happen: there is no enabled/disabled bool for this feature to lie about — an
// agent either holds a non-nil surface or emits nothing.
func (a *Agent) SetObservationalHooks(oh ObservationalHooks) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observationalHooks = oh
}

// HasObservationalHooks reports whether an observation-only surface is
// attached. Exposed so callers and tests can assert the truth about what is
// wired rather than infer it from a configuration flag.
func (a *Agent) HasObservationalHooks() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.observationalHooks != nil
}

// observationalHooksActive returns the attached surface, or nil.
//
// Observation runs only when the full hook path did NOT run for this turn.
// This keeps the change strictly additive: an agent with a HooksManager
// behaves exactly as before and cannot emit an event twice, while a sub-agent
// (which has no HooksManager) gains coverage.
//
// Note on DisableHooks: it is deliberately not consulted here. DisableHooks
// and --no-hooks exist to stop hooks from steering, blocking, and injecting
// text — none of which an observational surface can do (see ObservationalHooks
// above). Suppressing observation under those flags would recreate the exact
// coverage hole PLAN.md §4(a) forbids. In practice --no-hooks still yields no
// observation, because it prevents the parent hooks manager from being built
// at all and therefore there is no view to derive; that residual hole is
// documented, not silently closed here.
func (a *Agent) observationalHooksActive() ObservationalHooks {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	oh := a.observationalHooks
	hasFullPath := a.hooksManager != nil && !a.reqDisableHooks
	a.mu.RUnlock()
	if hasFullPath {
		return nil
	}
	return oh
}

// observeToolBefore emits a before-tool observation if a surface is attached.
// It returns nothing, so no caller can act on it.
func (a *Agent) observeToolBefore(ctx context.Context, toolName string, params map[string]any) {
	oh := a.observationalHooksActive()
	if oh == nil {
		return
	}
	defer recoverObservation()
	oh.ObserveToolBeforeExecute(ctx, toolName, params)
}

// observeToolAfter emits an after-tool observation if a surface is attached.
//
// It converts the live result into value-typed evidence here, on the agent's
// own goroutine, so nothing downstream ever holds a reference to an object the
// agent is still using.
func (a *Agent) observeToolAfter(ctx context.Context, toolName string, params map[string]any, result any, execErr error) {
	oh := a.observationalHooksActive()
	if oh == nil {
		return
	}
	defer recoverObservation()
	// NamedOutcomeOf returns a value the tool built for exactly this purpose
	// (internal/tools/outcome.go). Nil means "this tool reports no structured
	// outcome", which consumers must not read as success.
	outcome := tools.NamedOutcomeOf(toolName, result)
	output := ""
	if tr, ok := result.(*tools.ToolResult); ok && tr != nil {
		output = tr.Output
	}
	oh.ObserveToolAfterExecute(ctx, toolName, params, outcome, output, execErr)
}

// recoverObservation contains a panic originating anywhere in the observation
// chain so it cannot kill the agent's turn.
//
// hooks.ObservationalView already recovers each hook body, but that only
// protects hooks reached through that specific implementation. This boundary
// makes the guarantee hold for ANY ObservationalHooks implementation — a buggy
// third-party adapter, or a nil map write inside an event constructor. An
// observer must never be able to take down the run it is watching.
//
// The panic is swallowed rather than logged: this runs on the tool hot path,
// a.logger may itself be the thing that panicked, and a measurement subsystem
// that can spam the agent's log is a measurement subsystem that can change the
// run. Loss is accounted for downstream by hooks.ObservationalStats.
func recoverObservation() {
	_ = recover()
}
