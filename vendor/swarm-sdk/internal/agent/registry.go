// Package agent implements the agent runtime layer (Ring 2).
package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Registry manages a collection of agents and prevents circular calls.
type Registry interface {
	// Register adds an agent to the registry.
	Register(agent *Agent) error

	// Get retrieves an agent by ID.
	Get(id string) (*Agent, error)

	// List returns all registered agent IDs.
	List() []string

	// Unregister removes an agent from the registry.
	Unregister(id string) error

	// Exists checks if an agent is registered.
	Exists(id string) bool

	// RegisterAsToolIn registers an agent as a tool in a tool registry.
	// This automatically wraps the agent with AgentTool.
	RegisterAsToolIn(agentID string, toolRegistry tools.Registry) error

	// RegisterAllAsTools registers all agents as tools in a tool registry.
	RegisterAllAsTools(toolRegistry tools.Registry) error

	// CheckCircularCall checks if calling targetID would create a circular call.
	// Returns error if circular call detected.
	CheckCircularCall(ctx context.Context, targetID string) error

	// GetCallDepth returns the current call depth from context.
	CallDepth(ctx context.Context) int
}

// SimpleRegistry is a thread-safe registry implementation.
type SimpleRegistry struct {
	mu     sync.RWMutex
	agents map[string]*Agent

	// Configuration
	maxDepth int // Maximum allowed call depth (default: 5)
}

// RegistryConfig configures the agent registry.
type RegistryConfig struct {
	// MaxDepth is the maximum allowed call depth (default: 5)
	MaxDepth int
}

// NewRegistry creates a new agent registry with default configuration.
func NewRegistry() Registry {
	return NewRegistryWithConfig(RegistryConfig{
		MaxDepth: 5,
	})
}

// NewRegistryWithConfig creates a new agent registry with custom configuration.
func NewRegistryWithConfig(config RegistryConfig) Registry {
	if config.MaxDepth <= 0 {
		config.MaxDepth = 5
	}

	return &SimpleRegistry{
		agents:   make(map[string]*Agent),
		maxDepth: config.MaxDepth,
	}
}

// Register adds an agent to the registry.
func (r *SimpleRegistry) Register(agent *Agent) error {
	if agent == nil {
		return sdkerr.Permanent("registry.nil_agent", "cannot register nil agent")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	id := agent.ID()
	if id == "" {
		return sdkerr.Permanent("registry.empty_id", "agent ID cannot be empty")
	}

	// Check if already registered
	if _, exists := r.agents[id]; exists {
		return sdkerr.Permanent("registry.duplicate_id",
			fmt.Sprintf("agent with ID '%s' is already registered", id))
	}

	r.agents[id] = agent
	return nil
}

// Get retrieves an agent by ID.
func (r *SimpleRegistry) Get(id string) (*Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[id]
	if !exists {
		return nil, sdkerr.Permanent("registry.not_found",
			fmt.Sprintf("agent '%s' not found in registry", id))
	}

	return agent, nil
}

// List returns all registered agent IDs.
func (r *SimpleRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}

	return ids
}

// Unregister removes an agent from the registry.
func (r *SimpleRegistry) Unregister(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[id]; !exists {
		return sdkerr.Permanent("registry.not_found",
			fmt.Sprintf("agent '%s' not found in registry", id))
	}

	delete(r.agents, id)
	return nil
}

// Exists checks if an agent is registered.
func (r *SimpleRegistry) Exists(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, exists := r.agents[id]
	return exists
}

// RegisterAsToolIn registers an agent as a tool in a tool registry.
func (r *SimpleRegistry) RegisterAsToolIn(agentID string, toolRegistry tools.Registry) error {
	// Get the agent
	agent, err := r.Get(agentID)
	if err != nil {
		return err
	}

	// Wrap as tool
	agentTool := NewAgentTool(agent)

	// Register in tool registry
	if err := toolRegistry.Register(agentTool); err != nil {
		return sdkerr.Permanent("registry.tool_registration_failed",
			fmt.Sprintf("failed to register agent '%s' as tool: %v", agentID, err))
	}

	return nil
}

// RegisterAllAsTools registers all agents as tools in a tool registry.
func (r *SimpleRegistry) RegisterAllAsTools(toolRegistry tools.Registry) error {
	r.mu.RLock()
	ids := make([]string, 0, len(r.agents))
	for id := range r.agents {
		ids = append(ids, id)
	}
	r.mu.RUnlock()

	// Register each agent as a tool
	for _, id := range ids {
		if err := r.RegisterAsToolIn(id, toolRegistry); err != nil {
			return err
		}
	}

	return nil
}

// CheckCircularCall checks if calling targetID would create a circular call.
func (r *SimpleRegistry) CheckCircularCall(ctx context.Context, targetID string) error {
	// Get call stack from context
	stack := getCallStack(ctx)

	// Check if target is already in the stack
	if slices.Contains(stack, targetID) {
		return sdkerr.Permanent("registry.circular_call",
			fmt.Sprintf("circular call detected: %s → %s (already in call stack)",
				formatCallStack(stack), targetID))
	}

	// Check depth limit
	if len(stack) >= r.maxDepth {
		return sdkerr.Permanent("registry.max_depth_exceeded",
			fmt.Sprintf("maximum call depth (%d) exceeded. Call stack: %s → %s",
				r.maxDepth, formatCallStack(stack), targetID))
	}

	return nil
}

// GetCallDepth returns the current call depth from context.
func (r *SimpleRegistry) CallDepth(ctx context.Context) int {
	stack := getCallStack(ctx)
	return len(stack)
}

// --- Context helpers for call stack tracking ---

type callStackKey struct{}

// getCallStack retrieves the call stack from context.
func getCallStack(ctx context.Context) []string {
	if stack, ok := ctx.Value(callStackKey{}).([]string); ok {
		return stack
	}
	return []string{}
}

// WithCallStack adds an agent ID to the call stack in context.
func WithCallStack(ctx context.Context, agentID string) context.Context {
	stack := getCallStack(ctx)
	newStack := make([]string, len(stack)+1)
	copy(newStack, stack)
	newStack[len(stack)] = agentID

	return context.WithValue(ctx, callStackKey{}, newStack)
}

// formatCallStack formats a call stack for error messages.
func formatCallStack(stack []string) string {
	if len(stack) == 0 {
		return "(empty)"
	}

	var result strings.Builder
	result.WriteString(stack[0])
	for i := 1; i < len(stack); i++ {
		result.WriteString(" → " + stack[i])
	}
	return result.String()
}
