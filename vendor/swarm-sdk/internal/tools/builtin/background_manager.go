// Package builtin provides background agent management types.
package builtin

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// BackgroundAgentManager manages background agents spawned during the session.
// This interface allows the tool to register and track background agents.
type BackgroundAgentManager interface {
	// Add registers a new background agent with task description.
	Add(bg *agent.BackgroundAgent, task string) error

	// Get retrieves a background agent by ID.
	Get(agentID string) (*agent.BackgroundAgent, error)

	// List returns all background agents.
	List() []BackgroundAgentInfo

	// Cancel cancels a running background agent.
	Cancel(agentID string) error

	// Remove removes a background agent from tracking.
	Remove(agentID string) error
}

// BackgroundAgentInfo contains summary information about a background agent.
type BackgroundAgentInfo struct {
	AgentID   string
	ParentID  string
	Task      string
	Status    agent.BackgroundAgentStatus
	Progress  int
	StartTime time.Time
	Duration  time.Duration
	Result    *agent.BackgroundAgentResult
	// Summary is the latest content snippet from the running agent (updated every ~15s).
	// Empty until the agent has produced some output.
	Summary   string
	LastTool  string
	ToolCount int
}
