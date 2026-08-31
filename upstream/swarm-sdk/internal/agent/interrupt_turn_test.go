package agent

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// TestInterruptTurn_ReturnsIdleWithoutKillingRootContext locks in the post-cancel
// fix: InterruptTurn must reset the state machine to Idle (so the NEXT
// ExecuteWhenIdle proceeds immediately) and must NOT cancel the agent's root
// context (which would degrade every subsequent turn) — unlike Stop().
func TestInterruptTurn_ReturnsIdleWithoutKillingRootContext(t *testing.T) {
	a := &Agent{
		definition: &Definition{ID: "intr-agent", Name: "intr"},
		toolReg:    tools.NewRegistry(),
		logger:     noop.NewLogger(),
		tracer:     noop.NewTracer(),
		auditor:    noop.NewAuditor(),
		state:      StateExecuting,
		// Per-turn overrides that a cancelled turn must not leak forward.
		reqSystemPromptOverride: "leaked",
		reqDisableTools:         true,
		reqDisableHooks:         true,
	}
	a.ctx, a.cancel = context.WithCancel(context.Background())

	a.InterruptTurn()

	if a.state != StateIdle {
		t.Fatalf("expected StateIdle after InterruptTurn, got %s", a.state)
	}
	if a.ctx.Err() != nil {
		t.Fatalf("root context must stay live after InterruptTurn, got err=%v", a.ctx.Err())
	}
	if a.reqSystemPromptOverride != "" || a.reqDisableTools || a.reqDisableHooks {
		t.Fatalf("per-turn overrides leaked past InterruptTurn: prompt=%q tools=%v hooks=%v",
			a.reqSystemPromptOverride, a.reqDisableTools, a.reqDisableHooks)
	}

	// Contrast: Stop() DOES cancel the root context (permanent teardown).
	_ = a.Stop()
	if a.ctx.Err() == nil {
		t.Fatalf("expected Stop() to cancel the root context")
	}
	if a.state != StateStopped {
		t.Fatalf("expected StateStopped after Stop(), got %s", a.state)
	}

	// InterruptTurn on a stopped agent is a no-op (does not resurrect it).
	a.InterruptTurn()
	if a.state != StateStopped {
		t.Fatalf("InterruptTurn must not revive a stopped agent, got %s", a.state)
	}
}
