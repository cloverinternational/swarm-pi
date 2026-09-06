package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// SteeringStreamHook is the subject-side counterpart of the long-lived
// streaming steering driver (agent.StreamingSteeringDriver). It plays
// two roles on every tool boundary:
//
//  1. CONSUMER of armed effects — at pre-tool, it asks the driver's
//     SteeringTarget whether a block, refocus, or note is queued for
//     the next call. Armed blocks become hooks.Block(...) results;
//     queued notes/refocus become hooks.ContinueWithMessage(...).
//
//  2. PRODUCER of stream events — every relevant event is enqueued to
//     the driver so the observer agent (running in the pump goroutine)
//     can see it during its next flush. Enqueue is non-blocking and
//     drops oldest under backpressure (driver counts these).
//
// Reentrancy guard: the observer's own ctx is marked via
// agent.WithSteeringReentrancy; on those events the hook short-circuits
// to Continue without enqueueing or consuming effects. This is the
// same pattern the polling hook uses.
//
// Mutual exclusion with SteeringPreToolHook: the factory installs
// exactly ONE of these per agent based on def.SteeringMode. Both
// declare Priority 95 because they will never co-exist in the chain.
type SteeringStreamHook struct {
	driver *agent.StreamingSteeringDriver
	logger observability.Logger

	mu      sync.Mutex
	enabled bool
}

// NewSteeringStreamHook wires the hook to a driver. The driver may be
// nil at construction time (caller may build the driver later), in
// which case the hook stays inert until SetDriver is called.
func NewSteeringStreamHook(driver *agent.StreamingSteeringDriver, logger observability.Logger) *SteeringStreamHook {
	return &SteeringStreamHook{
		driver:  driver,
		logger:  logger,
		enabled: true,
	}
}

// Name returns the hook name.
func (h *SteeringStreamHook) Name() string { return "steering-stream" }

// Priority runs this hook very early in the pre-tool chain. Matches
// the polling hook's slot because the two are mutually exclusive.
func (h *SteeringStreamHook) Priority() int { return 95 }

// Filter selects the three event types the hook reacts to.
func (h *SteeringStreamHook) Filter(event hooks.Event) bool {
	h.mu.Lock()
	enabled := h.enabled
	h.mu.Unlock()
	if !enabled {
		return false
	}
	switch event.Type {
	case hooks.EventToolBeforeExecute,
		hooks.EventToolAfterExecute,
		hooks.EventAgentStopped,
		hooks.EventA2AMessageProjected:
		return true
	}
	return false
}

// SetEnabled toggles the hook at runtime. Useful for tests and for
// runtime kill-switches.
func (h *SteeringStreamHook) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = enabled
}

// IsEnabled reports whether the hook is active.
func (h *SteeringStreamHook) IsEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled
}

// SetDriver swaps the underlying driver. Safe to call concurrently
// with OnEvent — the driver pointer is updated under the same mutex
// that guards `enabled`.
func (h *SteeringStreamHook) SetDriver(d *agent.StreamingSteeringDriver) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.driver = d
}

// OnEvent is the core dispatcher. See the type comment for the role
// played at each event kind.
func (h *SteeringStreamHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Reentrancy: the observer's own LLM/tool calls must NEVER produce
	// recursive intervention. The polling hook uses the same guard.
	if agent.IsSteeringReentrant(ctx) {
		return hooks.Continue(), nil
	}

	h.mu.Lock()
	driver := h.driver
	h.mu.Unlock()
	if driver == nil {
		return hooks.Continue(), nil
	}

	// Sub-agents bypass steering entirely (same policy as the polling
	// hook). Sub-agents execute tools without task-relevance evaluation
	// to reduce latency and token cost.
	if agent.IsSubAgent(ctx) {
		return hooks.Continue(), nil
	}

	switch event.Type {
	case hooks.EventToolBeforeExecute:
		return h.onToolBefore(ctx, driver, event)

	case hooks.EventToolAfterExecute:
		driver.Enqueue(event)
		return hooks.Continue(), nil

	case hooks.EventA2AMessageProjected:
		// Phase 4: peer-DM drift detection. Forward the event so the
		// driver's DriftTracker can count per-peer DMs. No consumption
		// logic — the observer LLM still decides when to intervene.
		driver.Enqueue(event)
		return hooks.Continue(), nil

	case hooks.EventAgentStopped:
		driver.Enqueue(event)
		// Stop() waits on the pump goroutine; do it asynchronously so
		// the agent's shutdown path isn't blocked by an in-flight
		// observer flush.
		go func() {
			_ = driver.Stop()
		}()
		return hooks.Continue(), nil
	}

	return hooks.Continue(), nil
}

// onToolBefore handles a pre-tool event: consume pending effects from
// the SteeringTarget, then enqueue the event regardless of outcome
// (the observer wants to see attempts even when they're blocked).
func (h *SteeringStreamHook) onToolBefore(ctx context.Context, driver *agent.StreamingSteeringDriver, event hooks.Event) (hooks.HookResult, error) {
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		if tn, ok := event.Data["name"].(string); ok {
			toolName = tn
		}
	}

	// Enqueue first so the observer always sees the attempt — even
	// when we end up blocking. The buffer is bounded, so this can't
	// stall the hot path.
	driver.Enqueue(event)

	// The default driver's target exposes inspection helpers; tests
	// can substitute a different SteeringTarget impl that doesn't
	// expose ConsumeBlockFor, in which case we Continue silently.
	insp, ok := driver.Target().(agent.DefaultSteeringTargetInspector)
	if !ok {
		return hooks.Continue(), nil
	}

	// 1. Hard block wins over any soft directive.
	if pb, found := insp.ConsumeBlockFor(toolName); found {
		msg := fmt.Sprintf("[Steering] Blocked %s: %s", toolName, pb.Reason)
		if h.logger != nil {
			h.logger.Info(ctx, "steering_stream.block",
				observability.F("tool", toolName),
				observability.F("reason", pb.Reason))
		}
		return hooks.Block(msg), nil
	}

	// 2. Refocus is stronger than free-form notes; consume it first
	// and prepend its text.
	var parts []string
	if rf, found := insp.ConsumeRefocus(); found {
		parts = append(parts, fmt.Sprintf("[Steering] Refocus on %q: %s", rf.Anchor, rf.Reminder))
	}

	// 3. Drain free-form notes (already priority-sorted descending).
	for _, note := range insp.DrainNotes() {
		parts = append(parts, fmt.Sprintf("[Steering] %s", note.Text))
	}

	if len(parts) > 0 {
		return hooks.ContinueWithMessage(strings.Join(parts, "\n")), nil
	}
	return hooks.Continue(), nil
}

// Ensure SteeringStreamHook satisfies the hooks.Hook interface at
// compile time.
var _ hooks.Hook = (*SteeringStreamHook)(nil)
