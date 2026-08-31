// Package builtin provides AgentWaitSleepBlocker hook for blocking wasteful
// sleep/shell commands during agent waits.
package builtin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// blockedToolsDuringAgentWait lists tools that are blocked when agents are waiting.
// These are typically wasteful operations that shouldn't happen during coordination.
var blockedToolsDuringAgentWait = map[string]bool{
	"Bash":  true,
	"Shell": true,
}

// AgentWaitSleepBlocker prevents wasteful sleep/shell commands when waiting for agents.
// This hook tracks which agents are being waited for and blocks sleep operations.
type AgentWaitSleepBlocker struct {
	// pendingWaits tracks agents currently being waited for
	pendingWaits map[string]*waitEntry
	mu           sync.RWMutex
}

// waitEntry tracks a single wait operation
type waitEntry struct {
	agentID   string
	startTime time.Time
}

// NewAgentWaitSleepBlocker creates a new sleep blocker hook
func NewAgentWaitSleepBlocker() *AgentWaitSleepBlocker {
	return &AgentWaitSleepBlocker{
		pendingWaits: make(map[string]*waitEntry),
	}
}

// Name returns the hook name
func (h *AgentWaitSleepBlocker) Name() string {
	return "agent_wait_sleep_blocker"
}

// Filter returns true for tool-related events
func (h *AgentWaitSleepBlocker) Filter(event hooks.Event) bool {
	return event.Type == "tool.before_execute"
}

// Priority returns the hook priority (high priority for blocking)
func (h *AgentWaitSleepBlocker) Priority() int {
	return 90 // High priority
}

// OnEvent blocks sleep/shell commands during agent waits
func (h *AgentWaitSleepBlocker) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	h.mu.RLock()
	hasPendingWaits := len(h.pendingWaits) > 0
	h.mu.RUnlock()

	// If no agents are being waited for, allow everything
	if !hasPendingWaits {
		return hooks.Continue(), nil
	}

	// Get tool name from event data
	toolName, _ := event.Data["tool_name"].(string)

	// Check if tool is blocked
	if blockedToolsDuringAgentWait[toolName] {
		return h.blockTool(toolName), nil
	}

	return hooks.Continue(), nil
}

// blockTool generates a block result for the given tool
func (h *AgentWaitSleepBlocker) blockTool(toolName string) hooks.HookResult {
	var message string

	if toolName == "Bash" || toolName == "Shell" {
		message = fmt.Sprintf("%s blocked: agent is waiting for sub-agents to complete. Avoid running shell commands during coordination - the agent should wait or perform other useful work instead.", toolName)
	} else {
		message = fmt.Sprintf("%s blocked during agent wait: this operation is not allowed while waiting for background agents", toolName)
	}

	return hooks.Block(message)
}

// RegisterAgentWait registers that an agent is being waited for
func (h *AgentWaitSleepBlocker) RegisterAgentWait(agentID string) error {
	if agentID == "" {
		return fmt.Errorf("agent_id cannot be empty")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.pendingWaits[agentID] = &waitEntry{
		agentID:   agentID,
		startTime: time.Now(),
	}

	return nil
}

// UnregisterAgentWait removes an agent from the wait tracking
func (h *AgentWaitSleepBlocker) UnregisterAgentWait(agentID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.pendingWaits, agentID)
}

// GetActiveWaits returns the count of active agent waits
func (h *AgentWaitSleepBlocker) GetActiveWaits() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.pendingWaits)
}
