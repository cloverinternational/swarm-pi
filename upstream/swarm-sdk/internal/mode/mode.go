// Package mode provides orchestration capabilities for multi-agent workflows.
//
// The mode package implements Ring 3 of the SDK architecture, providing:
// - Mode definitions (YAML-based workflow configurations)
// - Agent group coordination (parallel, sequential, adversarial)
// - Workflow execution engine (DAG-based dependency resolution)
// - Steering system (rule-based, LLM-based, hybrid)
//
// Modes define complete multi-agent workflows with groups of agents,
// execution strategies, completion criteria, and steering logic.
package mode

import (
	"maps"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode/gates"
)

// Mode represents a complete multi-agent workflow configuration.
//
// Modes are typically loaded from YAML files and define:
// - Agent groups with execution strategies
// - Dependencies between groups
// - Transition rules between groups
// - Steering logic for decision-making
// - Entry/exit hooks
type Mode struct {
	// Unique identifier for this mode
	ID string

	// Human-readable name
	Name string

	// Description of what this mode does
	Description string

	// Version string (semantic versioning recommended)
	Version string

	// Mode configuration
	Config ModeConfig

	// Agent groups in this mode
	// Groups can have dependencies on other groups
	Groups []*AgentGroup

	// Transition rules between groups
	Transitions []*Transition

	// Hook names to execute on mode entry
	EntryHooks []string

	// Hook names to execute on mode exit
	ExitHooks []string

	// Steering configuration for this mode
	Steering *SteeringConfig

	// Global gates for the entire workflow
	Gates []gates.GateConfig

	// Workflow parameters for interactive execution
	Parameters []*WorkflowParameter

	// Additional metadata
	Metadata map[string]any
}

// WorkflowParameter defines an input parameter for workflow execution.
type WorkflowParameter struct {
	// Unique identifier for this parameter
	ID string

	// Question text shown to user
	Question string

	// Extended description/help text
	Description string

	// Parameter type (text, number, choice, multi_choice, confirm)
	Type string

	// Whether this parameter is required
	Required bool

	// Default value (can be nil)
	Default any

	// Available choices (for choice/multi_choice types)
	Choices []string

	// Validation rules
	Validation *ParameterValidation

	// Additional metadata
	Metadata map[string]any
}

// ParameterValidation defines validation rules for a parameter.
type ParameterValidation struct {
	// For text parameters
	MinLength int
	MaxLength int
	Pattern   string // Regex pattern

	// For number parameters
	Min  float64
	Max  float64
	Step float64
}

// ModeConfig contains mode-level configuration options.
type ModeConfig struct {
	// Maximum duration for entire mode execution
	// Zero means no timeout
	MaxDuration time.Duration

	// Allow human intervention during execution
	AllowHumanIntervention bool

	// Fail immediately if steering blocks execution
	// If false, mode will try to recover or continue
	FailOnSteeringBlock bool

	// Maximum number of retries for failed groups
	MaxRetries int

	// Behavior when timeout is reached
	// Options: "fail", "partial", "continue"
	TimeoutBehavior string

	// Custom configuration fields
	Custom map[string]any
}

// NewMode creates a new Mode with default configuration.
func NewMode(id, name string) *Mode {
	return &Mode{
		ID:          id,
		Name:        name,
		Version:     "1.0.0",
		Config:      DefaultModeConfig(),
		Groups:      make([]*AgentGroup, 0),
		Transitions: make([]*Transition, 0),
		EntryHooks:  make([]string, 0),
		ExitHooks:   make([]string, 0),
		Parameters:  make([]*WorkflowParameter, 0),
		Metadata:    make(map[string]any),
	}
}

// DefaultModeConfig returns default mode configuration.
func DefaultModeConfig() ModeConfig {
	return ModeConfig{
		MaxDuration:            30 * time.Minute,
		AllowHumanIntervention: true,
		FailOnSteeringBlock:    false,
		MaxRetries:             3,
		TimeoutBehavior:        "partial", // Return partial results on timeout
		Custom:                 make(map[string]any),
	}
}

// AddGroup adds an agent group to this mode.
func (m *Mode) AddGroup(group *AgentGroup) {
	m.Groups = append(m.Groups, group)
}

// AddTransition adds a transition rule to this mode.
func (m *Mode) AddTransition(transition *Transition) {
	m.Transitions = append(m.Transitions, transition)
}

// GetGroup retrieves a group by ID.
func (m *Mode) GetGroup(id string) *AgentGroup {
	for _, group := range m.Groups {
		if group.ID == id {
			return group
		}
	}
	return nil
}

// GetTransitions returns all transitions from a specific group.
func (m *Mode) GetTransitions(fromGroupID string) []*Transition {
	transitions := make([]*Transition, 0)
	for _, t := range m.Transitions {
		if t.From == fromGroupID {
			transitions = append(transitions, t)
		}
	}
	return transitions
}

// Validate performs basic validation on the mode configuration.
//
// Returns error if:
// - Mode has no groups
// - Group IDs are not unique
// - Transitions reference non-existent groups
// - Circular dependencies exist
// - Invalid configuration values
func (m *Mode) Validate() error {
	if len(m.Groups) == 0 {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "mode has no groups",
			ModeID:  m.ID,
		}
	}

	// Check for duplicate group IDs
	seen := make(map[string]bool)
	for _, group := range m.Groups {
		if seen[group.ID] {
			return &ModeError{
				Type:    ErrorTypeValidation,
				Message: "duplicate group ID: " + group.ID,
				ModeID:  m.ID,
				GroupID: group.ID,
			}
		}
		seen[group.ID] = true
	}

	// Validate transitions reference existing groups
	for _, transition := range m.Transitions {
		if m.GetGroup(transition.From) == nil {
			return &ModeError{
				Type:    ErrorTypeValidation,
				Message: "transition references non-existent source group: " + transition.From,
				ModeID:  m.ID,
			}
		}
		if m.GetGroup(transition.To) == nil {
			return &ModeError{
				Type:    ErrorTypeValidation,
				Message: "transition references non-existent target group: " + transition.To,
				ModeID:  m.ID,
			}
		}
	}

	// Validate each group
	for _, group := range m.Groups {
		if err := group.Validate(); err != nil {
			return err
		}
	}

	// Validate timeout behavior
	validTimeoutBehaviors := map[string]bool{
		"fail":     true,
		"partial":  true,
		"continue": true,
	}
	if !validTimeoutBehaviors[m.Config.TimeoutBehavior] {
		return &ModeError{
			Type:    ErrorTypeValidation,
			Message: "invalid timeout behavior: " + m.Config.TimeoutBehavior,
			ModeID:  m.ID,
		}
	}

	return nil
}

// Clone creates a deep copy of the mode.
// Useful for creating mode instances for concurrent execution.
func (m *Mode) Clone() *Mode {
	clone := &Mode{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Version:     m.Version,
		Config:      m.Config,
		Groups:      make([]*AgentGroup, len(m.Groups)),
		Transitions: make([]*Transition, len(m.Transitions)),
		EntryHooks:  make([]string, len(m.EntryHooks)),
		ExitHooks:   make([]string, len(m.ExitHooks)),
		Parameters:  make([]*WorkflowParameter, len(m.Parameters)),
		Metadata:    make(map[string]any),
	}

	// Deep copy groups
	for i, group := range m.Groups {
		clone.Groups[i] = group.Clone()
	}

	// Deep copy transitions
	for i, transition := range m.Transitions {
		clone.Transitions[i] = transition.Clone()
	}

	// Copy hooks
	copy(clone.EntryHooks, m.EntryHooks)
	copy(clone.ExitHooks, m.ExitHooks)

	// Deep copy parameters
	for i, param := range m.Parameters {
		clone.Parameters[i] = &WorkflowParameter{
			ID:          param.ID,
			Question:    param.Question,
			Description: param.Description,
			Type:        param.Type,
			Required:    param.Required,
			Default:     param.Default,
			Choices:     append([]string(nil), param.Choices...),
			Metadata:    make(map[string]any),
		}
		if param.Validation != nil {
			clone.Parameters[i].Validation = &ParameterValidation{
				MinLength: param.Validation.MinLength,
				MaxLength: param.Validation.MaxLength,
				Pattern:   param.Validation.Pattern,
				Min:       param.Validation.Min,
				Max:       param.Validation.Max,
				Step:      param.Validation.Step,
			}
		}
		maps.Copy(clone.Parameters[i].Metadata, param.Metadata)
	}

	// Deep copy metadata
	maps.Copy(clone.Metadata, m.Metadata)

	// Copy steering config if present
	if m.Steering != nil {
		clone.Steering = m.Steering.Clone()
	}

	return clone
}

// SteeringConfig defines steering behavior for the mode.
type SteeringConfig struct {
	// Type of steering: "rule-based", "llm-based", "hybrid", "none"
	Type string

	// LLM meta-agent for LLM-based or hybrid steering
	// This agent makes steering decisions
	LLMMetaAgent *agent.Definition

	// Rules for rule-based or hybrid steering
	Rules []*SteeringRule

	// Custom steering configuration
	Custom map[string]any
}

// Clone creates a deep copy of the steering config.
func (s *SteeringConfig) Clone() *SteeringConfig {
	if s == nil {
		return nil
	}

	clone := &SteeringConfig{
		Type:   s.Type,
		Rules:  make([]*SteeringRule, len(s.Rules)),
		Custom: make(map[string]any),
	}

	// Clone LLM meta-agent definition
	if s.LLMMetaAgent != nil {
		clone.LLMMetaAgent = s.LLMMetaAgent.Clone()
	}

	// Clone rules
	for i, rule := range s.Rules {
		clone.Rules[i] = rule.Clone()
	}

	// Copy custom fields
	maps.Copy(clone.Custom, s.Custom)

	return clone
}

// SteeringRule defines a rule-based steering decision.
type SteeringRule struct {
	// Unique identifier for this rule
	ID string

	// Condition expression to evaluate
	// Examples: "group.consensus_failed", "retry_count < 3"
	Condition string

	// Action to take when condition matches
	// Examples: "retry_with_more_context", "skip_group", "abort"
	Action string

	// Priority for this rule (higher = evaluated first)
	Priority int

	// Additional parameters for the action
	Parameters map[string]any
}

// Clone creates a deep copy of the steering rule.
func (r *SteeringRule) Clone() *SteeringRule {
	clone := &SteeringRule{
		ID:         r.ID,
		Condition:  r.Condition,
		Action:     r.Action,
		Priority:   r.Priority,
		Parameters: make(map[string]any),
	}

	maps.Copy(clone.Parameters, r.Parameters)

	return clone
}
