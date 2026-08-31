package mode

import (
	"maps"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode/gates"
)

// AgentGroup represents a collection of agents executed together.
//
// Groups can execute agents in different strategies:
// - Parallel: All agents run concurrently
// - Sequential: Agents run one after another
// - Adversarial: Agents debate until consensus or timeout
type AgentGroup struct {
	// Unique identifier for this group
	ID string

	// Human-readable name
	Name string

	// Description of this group's purpose
	Description string

	// Execution strategy for agents in this group
	Execution ExecutionStrategy

	// Agent definitions in this group
	Agents []*agent.Definition

	// IDs of groups that must complete before this group can start
	DependsOn []string

	// Completion criteria for this group
	Completion CompletionCriteria

	// Maximum time for this group to complete
	// Zero means no timeout (inherits from mode)
	Timeout time.Duration

	// Group-specific steering configuration
	Steering *GroupSteeringConfig

	// Gates for this group (quality, consensus, resource, etc.)
	Gates []gates.GateConfig

	// Additional metadata
	Metadata map[string]any

	// Coordinator agent for this group.
	// If set, this agent runs after the group's primary agents complete.
	// It is responsible for synthesizing agent outputs into a final group response.
	Coordinator *agent.Definition

	// OutputStrategy defines how agent outputs are combined into the group's final output.
	// Defaults to OutputStrategyRaw if not specified.
	OutputStrategy OutputStrategy
}

// ExecutionStrategy defines how agents in a group are executed.
type ExecutionStrategy string

const (
	// ExecutionParallel runs all agents concurrently
	ExecutionParallel ExecutionStrategy = "parallel"

	// ExecutionSequential runs agents one after another
	ExecutionSequential ExecutionStrategy = "sequential"

	// ExecutionAdversarial runs agents in debate mode
	ExecutionAdversarial ExecutionStrategy = "adversarial"
)

// OutputStrategy defines how a group's agent outputs are combined into the final group output.
type OutputStrategy string

const (
	// OutputStrategyRaw passes all agent outputs through without synthesis.
	// For single-agent groups, uses that agent's output directly.
	// For multi-agent groups, concatenates all outputs separated by dividers.
	OutputStrategyRaw OutputStrategy = "raw"

	// OutputStrategySynthesize uses a coordinator agent to synthesize
	// agent outputs into a single cohesive response.
	OutputStrategySynthesize OutputStrategy = "synthesize"

	// OutputStrategyFirst uses the first successful agent's output.
	OutputStrategyFirst OutputStrategy = "first"
)

// NewAgentGroup creates a new agent group.
func NewAgentGroup(id, name string, execution ExecutionStrategy) *AgentGroup {
	return &AgentGroup{
		ID:             id,
		Name:           name,
		Execution:      execution,
		Agents:         make([]*agent.Definition, 0),
		DependsOn:      make([]string, 0),
		Completion:     DefaultCompletionCriteria(),
		OutputStrategy: OutputStrategyRaw,
		Metadata:       make(map[string]any),
	}
}

// AddAgent adds an agent definition to this group.
func (g *AgentGroup) AddAgent(agentDef *agent.Definition) {
	g.Agents = append(g.Agents, agentDef)
}

// AddDependency adds a dependency on another group.
func (g *AgentGroup) AddDependency(groupID string) {
	g.DependsOn = append(g.DependsOn, groupID)
}

// Validate performs validation on the agent group.
func (g *AgentGroup) Validate() error {
	if g.ID == "" {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "group ID cannot be empty",
			GroupID: g.ID,
		}
	}

	if len(g.Agents) == 0 {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "group has no agents",
			GroupID: g.ID,
		}
	}

	// Validate execution strategy
	validStrategies := map[ExecutionStrategy]bool{
		ExecutionParallel:    true,
		ExecutionSequential:  true,
		ExecutionAdversarial: true,
	}
	if !validStrategies[g.Execution] {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "invalid execution strategy: " + string(g.Execution),
			GroupID: g.ID,
		}
	}

	// Validate completion criteria
	if err := g.Completion.Validate(); err != nil {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "invalid completion criteria: " + err.Error(),
			GroupID: g.ID,
		}
	}

	// Validate each agent definition
	for i, agentDef := range g.Agents {
		if agentDef.Name == "" {
			return &ModeError{
				Type:    ErrorTypeValidation,
				Message: "agent at index " + string(rune(i)) + " has no name",
				GroupID: g.ID,
			}
		}
	}

	return nil
}

// Clone creates a deep copy of the agent group.
func (g *AgentGroup) Clone() *AgentGroup {
	clone := &AgentGroup{
		ID:             g.ID,
		Name:           g.Name,
		Description:    g.Description,
		Execution:      g.Execution,
		Agents:         make([]*agent.Definition, len(g.Agents)),
		DependsOn:      make([]string, len(g.DependsOn)),
		Completion:     g.Completion,
		Timeout:        g.Timeout,
		OutputStrategy: g.OutputStrategy,
		Metadata:       make(map[string]any),
	}

	// Clone agents
	for i, agentDef := range g.Agents {
		clone.Agents[i] = agentDef.Clone()
	}

	// Copy dependencies
	copy(clone.DependsOn, g.DependsOn)

	// Copy metadata
	maps.Copy(clone.Metadata, g.Metadata)

	// Clone steering config
	if g.Steering != nil {
		clone.Steering = g.Steering.Clone()
	}

	return clone
}

// CompletionCriteria defines when a group is considered complete.
type CompletionCriteria struct {
	// Type of completion criteria.
	// Options: "all", "first", "majority", "consensus", "quality"
	//
	//  all      — every agent must succeed (subject to MaxFailures).
	//  first    — the first agent to produce output satisfies the group.
	//  majority — more than Threshold fraction of agents must succeed (default 0.5).
	//  consensus — agents must agree; Threshold controls confidence floor (default 0.5).
	//  quality  — reserved for future gate-based quality evaluation.
	Type string

	// Threshold is the minimum success ratio (0.0–1.0) used by "majority" and
	// "consensus" types. Ignored by "all" and "first".
	// Defaults to 0.5 when zero (not explicitly set).
	Threshold float64

	// MinAgents is currently reserved for future use.
	MinAgents int

	// MaxFailures is the number of agent failures tolerated before the group
	// fails for the "all" completion type. Zero means no failures allowed.
	MaxFailures int

	// RequireOutput controls whether a non-empty response message is required
	// for an agent to be counted as successful.
	//
	// Default: false.
	//
	// Set to false (the default) for agents that complete their work by calling
	// tools (writing files, running commands, updating databases) and may
	// return a brief or empty final message. The absence of output text does
	// NOT indicate failure for such agents.
	//
	// Set to true when the group's downstream stages depend on the agent's
	// text output being passed as context (e.g. sequential pipelines where
	// each agent's response is the next agent's input).
	RequireOutput bool
}

// DefaultCompletionCriteria returns default completion criteria.
func DefaultCompletionCriteria() CompletionCriteria {
	return CompletionCriteria{
		Type:          "all",
		Threshold:     0.0, // 0 sentinel: majority/consensus will use 0.5 at eval time
		MinAgents:     0,
		MaxFailures:   0,
		RequireOutput: false, // Tool-only agents are valid without text output
	}
}

// Validate validates the completion criteria.
func (c *CompletionCriteria) Validate() error {
	validTypes := map[string]bool{
		"consensus": true,
		"first":     true,
		"all":       true,
		"majority":  true,
		"quality":   true,
	}

	if !validTypes[c.Type] {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "invalid completion type: " + c.Type,
		}
	}

	if c.Threshold < 0.0 || c.Threshold > 1.0 {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "threshold must be between 0.0 and 1.0",
		}
	}

	if c.MinAgents < 0 {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "min_agents cannot be negative",
		}
	}

	if c.MaxFailures < 0 {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "max_failures cannot be negative",
		}
	}

	return nil
}

// GroupSteeringConfig defines steering behavior for a specific group.
type GroupSteeringConfig struct {
	// Validation configuration
	ValidatePlan *ValidationConfig

	// Synthesis strategy for combining agent outputs
	// Options: "voting", "llm-synthesis", "first-wins", "best-of-n"
	SynthesisStrategy string

	// Conflict resolution strategy
	// Options: "majority", "weighted", "llm-decide"
	ConflictResolution string

	// Custom configuration
	Custom map[string]any
}

// Clone creates a deep copy of the group steering config.
func (g *GroupSteeringConfig) Clone() *GroupSteeringConfig {
	if g == nil {
		return nil
	}

	clone := &GroupSteeringConfig{
		SynthesisStrategy:  g.SynthesisStrategy,
		ConflictResolution: g.ConflictResolution,
		Custom:             make(map[string]any),
	}

	if g.ValidatePlan != nil {
		clone.ValidatePlan = g.ValidatePlan.Clone()
	}

	maps.Copy(clone.Custom, g.Custom)

	return clone
}

// ValidationConfig defines validation behavior.
type ValidationConfig struct {
	// Type of validation: "llm", "rule", "none"
	Type string

	// Validation prompt for LLM-based validation
	Prompt string

	// Validation rules for rule-based validation
	Rules []string

	// Minimum confidence threshold (0.0-1.0)
	MinConfidence float64
}

// Clone creates a deep copy of the validation config.
func (v *ValidationConfig) Clone() *ValidationConfig {
	if v == nil {
		return nil
	}

	clone := &ValidationConfig{
		Type:          v.Type,
		Prompt:        v.Prompt,
		Rules:         make([]string, len(v.Rules)),
		MinConfidence: v.MinConfidence,
	}

	copy(clone.Rules, v.Rules)

	return clone
}
