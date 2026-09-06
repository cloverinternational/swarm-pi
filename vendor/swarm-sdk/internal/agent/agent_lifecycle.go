package agent

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Initialize prepares the agent for execution.
// It is called automatically by New() — you do not need to call it manually.
//
// Calling Initialize() on an already-initialized agent is a no-op that returns nil.
// This preserves backward compatibility for callers that explicitly call Initialize()
// after creating an agent.
func (a *Agent) Initialize() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Idempotent: no-op if already initialized (New() calls this automatically).
	if a.initialized {
		return nil
	}
	if a.state != StateIdle {
		return sdkerr.Permanent("agent.invalid_state",
			fmt.Sprintf("cannot initialize agent in state %s", a.state))
	}
	// Initialize memory
	a.memory = NewInMemoryMemory()
	// Render system prompt template if needed
	systemPrompt, err := a.definition.RenderSystemPrompt()
	if err != nil {
		return sdkerr.Permanent("agent.system_prompt_error", err.Error())
	}
	// Store rendered system prompt back to definition
	a.definition.SystemPrompt = systemPrompt
	// Log initialization
	a.logger.Info(a.ctx, "agent.initialized",
		observability.F("agent_id", a.definition.ID),
		observability.F("provider", a.definition.Provider),
		observability.F("model", a.definition.Model))
	a.startedAt = time.Now()
	a.lastActivityAt = time.Now()
	a.initialized = true
	return nil
}

// Stop gracefully stops the agent.
func (a *Agent) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == StateStopped {
		return nil // Already stopped
	}
	a.state = StateStopped
	a.cancel() // Cancel context
	a.logger.Info(a.ctx, "agent.stopped",
		observability.F("agent_id", a.definition.ID))
	return nil
}

// InterruptTurn aborts the CURRENTLY-RUNNING turn without tearing the agent
// down for reuse. Unlike Stop(), it does NOT cancel the agent's root context
// (a.ctx) and does NOT move the agent into StateStopped — both of which would
// permanently degrade every subsequent Execute (a dead root context forces
// retries/timeouts on later turns, and StateStopped blocks ExecuteWhenIdle).
//
// The in-flight turn is expected to be cancelled by the caller via the
// per-turn context it passed to Execute (the SDK client does this through
// sessExecCancel). InterruptTurn additionally restores the state machine to
// Idle so the very next ExecuteWhenIdle proceeds immediately instead of
// spinning on a stale StateExecuting that a racing teardown left behind.
//
// This is the method the SDK client's Cancel() should use: it returns the
// agent to a clean idle state, fixing the observed "next turn after cancel is
// degraded/slow until daemon restart" regression.
func (a *Agent) InterruptTurn() {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Never resurrect a fully stopped/destroyed agent — only nudge a live one
	// (executing or already idle) back to a clean idle baseline.
	if a.state == StateStopped {
		return
	}
	a.state = StateIdle
	a.lastActivityAt = time.Now()
	// Drop per-request overrides so a cancelled turn cannot leak its one-shot
	// settings (system-prompt override, tool/hook kill switches) into the next.
	a.reqSystemPromptOverride = ""
	a.reqDisableTools = false
	a.reqDisableHooks = false
	a.logger.Info(a.ctx, "agent.turn_interrupted",
		observability.F("agent_id", a.definition.ID))
}

// Destroy cleans up agent resources.
func (a *Agent) Destroy() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Cancel context if not already cancelled
	if a.cancel != nil {
		a.cancel()
	}
	// Remove tool change listener from registry
	if a.toolChangeHandler != nil {
		if observableReg, ok := a.toolReg.(tools.ObservableRegistry); ok {
			observableReg.RemoveListener(a.toolChangeHandler)
		}
	}
	if a.a2aRuntime != nil {
		_ = a.a2aRuntime.Close()
		a.a2aRuntime = nil
	}
	a.state = StateStopped
	a.logger.Info(a.ctx, "agent.destroyed",
		observability.F("agent_id", a.definition.ID))
	return nil
}

// ResetConversationState resets the agent's conversation-scoped state for a new conversation.
// This is called during compaction or conversation switching to prevent state bleed.
// It resets the conversation ID, clears conversation-scoped state, and resets counters.
func (a *Agent) ResetConversationState(newConvID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	oldConvID := a.conversationID

	// Reset conversation ID to the new one
	a.conversationID = newConvID

	// Clear conversation-scoped state
	a.requestContext = make(map[string]any)
	a.responseMetadata = make(map[string]any)

	// Reset counters for the new conversation
	a.turnCount = 0
	a.toolCallsTotal = 0
	a.inputTokens = 0
	a.outputTokens = 0
	a.totalCost = 0

	// Reset activity timestamp
	a.lastActivityAt = time.Now()

	a.logger.Info(a.ctx, "agent.conversation_state_reset",
		observability.F("agent_id", a.definition.ID),
		observability.F("old_conv_id", oldConvID),
		observability.F("new_conv_id", newConvID))
}
