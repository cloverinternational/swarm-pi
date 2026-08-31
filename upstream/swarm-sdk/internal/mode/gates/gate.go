// Package gates provides workflow control through gates
package gates

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// Gate represents a control point in workflow execution
type Gate interface {
	// ID returns the gate's unique identifier
	ID() string

	// Type returns the gate type
	Type() GateType

	// Evaluate determines if the gate should allow progression
	Evaluate(ctx context.Context, eval *EvaluationContext) (*GateDecision, error)

	// Trigger returns when this gate should be evaluated
	Trigger() GateTrigger
}

// GateType defines the category of gate
type GateType string

const (
	GateTypeQuality    GateType = "quality"
	GateTypeConsensus  GateType = "consensus"
	GateTypeApproval   GateType = "approval"
	GateTypeDependency GateType = "dependency"
	GateTypeResource   GateType = "resource"
	GateTypeSteering   GateType = "steering"
	GateTypeCustom     GateType = "custom"
)

// GateTrigger defines when a gate should be evaluated
type GateTrigger string

const (
	TriggerWorkflowStarting  GateTrigger = "workflow_starting"
	TriggerWorkflowRunning   GateTrigger = "workflow_running"
	TriggerWorkflowCompleted GateTrigger = "workflow_completed"
	TriggerGroupStarting     GateTrigger = "group_starting"
	TriggerGroupRunning      GateTrigger = "group_running"
	TriggerGroupCompleted    GateTrigger = "group_completed"
	TriggerAgentStarting     GateTrigger = "agent_starting"
	TriggerAgentCompleted    GateTrigger = "agent_completed"
)

// GateDecision represents the result of gate evaluation
type GateDecision struct {
	// Allow indicates if the gate allows progression
	Allow bool

	// Confidence in the decision (0.0 to 1.0)
	Confidence float64

	// Reason for the decision
	Reason string

	// Action to take if gate blocks
	Action GateAction

	// Metadata about the decision
	Metadata map[string]any

	// Timestamp of decision
	Timestamp time.Time
}

// GateAction defines what action to take based on gate decision
type GateAction string

const (
	ActionContinue              GateAction = "continue"
	ActionBlock                 GateAction = "block"
	ActionRetry                 GateAction = "retry"
	ActionRetryWithFeedback     GateAction = "retry_with_feedback"
	ActionSkipGroup             GateAction = "skip_group"
	ActionRouteToGroup          GateAction = "route_to_group"
	ActionRequestApproval       GateAction = "request_approval"
	ActionStartConversation     GateAction = "start_conversation"
	ActionEscalateToAdversarial GateAction = "escalate_to_adversarial"
	ActionHumanIntervention     GateAction = "human_intervention"
	ActionCompleteWorkflow      GateAction = "complete_workflow"
	ActionFailWorkflow          GateAction = "fail_workflow"
)

// EvaluationContext contains the context for gate evaluation
// Uses any for workflow/group/result to avoid circular dependencies
type EvaluationContext struct {
	// Workflow being executed (as interface to avoid import cycle)
	Workflow any

	// Current group being evaluated (as interface to avoid import cycle)
	Group any

	// Group result (for post-execution gates, as interface to avoid import cycle)
	GroupResult any

	// All group results so far
	AllGroupResults map[string]any

	// Workflow execution context
	WorkflowContext map[string]any

	// Elapsed time
	ElapsedTime time.Duration

	// Total cost so far
	TotalCost float64

	// Total tokens used
	TotalTokens int

	// Logger
	Logger observability.Logger

	// Tracer
	Tracer observability.Tracer
}

// GateConfig represents gate configuration from YAML
type GateConfig struct {
	ID        string         `yaml:"id"`
	Type      GateType       `yaml:"type"`
	Trigger   GateTrigger    `yaml:"trigger"`
	Policy    map[string]any `yaml:"policy"`
	Actions   GateActions    `yaml:"actions"`
	DependsOn []string       `yaml:"depends_on,omitempty"`
	Priority  int            `yaml:"priority,omitempty"`
	Timeout   time.Duration  `yaml:"timeout,omitempty"`
	Enabled   bool           `yaml:"enabled"`
	Metadata  map[string]any `yaml:"metadata,omitempty"`
}

// GateActions defines actions for different gate outcomes
type GateActions struct {
	Pass    ActionConfig `yaml:"pass,omitempty"`
	Fail    ActionConfig `yaml:"fail,omitempty"`
	Timeout ActionConfig `yaml:"timeout,omitempty"`
}

// ActionConfig defines configuration for a gate action
type ActionConfig struct {
	Action             GateAction     `yaml:"action"`
	TargetGroup        string         `yaml:"target_group,omitempty"`
	MaxRetries         int            `yaml:"max_retries,omitempty"`
	FeedbackPrompt     string         `yaml:"feedback_prompt,omitempty"`
	ConversationConfig map[string]any `yaml:"conversation_config,omitempty"`
	Metadata           map[string]any `yaml:"metadata,omitempty"`
}

// GateEvaluator is the interface for custom gate evaluators
type GateEvaluator interface {
	Evaluate(ctx context.Context, eval *EvaluationContext, policy map[string]any) (*GateDecision, error)
}

// BaseGate provides common gate functionality
type BaseGate struct {
	id        string
	gateType  GateType
	trigger   GateTrigger
	policy    map[string]any
	actions   GateActions
	dependsOn []string
	priority  int
	timeout   time.Duration
	logger    observability.Logger
	tracer    observability.Tracer
}

// NewBaseGate creates a new base gate
func NewBaseGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) *BaseGate {
	return &BaseGate{
		id:        config.ID,
		gateType:  config.Type,
		trigger:   config.Trigger,
		policy:    config.Policy,
		actions:   config.Actions,
		dependsOn: config.DependsOn,
		priority:  config.Priority,
		timeout:   config.Timeout,
		logger:    logger,
		tracer:    tracer,
	}
}

func (g *BaseGate) ID() string {
	return g.id
}

func (g *BaseGate) Type() GateType {
	return g.gateType
}

func (g *BaseGate) Trigger() GateTrigger {
	return g.trigger
}

func (g *BaseGate) DependsOn() []string {
	return g.dependsOn
}

func (g *BaseGate) Priority() int {
	return g.priority
}

// GetPolicyValue retrieves a typed value from policy configuration
func (g *BaseGate) GetPolicyValue(key string, defaultValue any) any {
	if val, ok := g.policy[key]; ok {
		return val
	}
	return defaultValue
}

// GetPolicyFloat retrieves a float64 from policy
func (g *BaseGate) GetPolicyFloat(key string, defaultValue float64) float64 {
	if val, ok := g.policy[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		}
	}
	return defaultValue
}

// GetPolicyString retrieves a string from policy
func (g *BaseGate) GetPolicyString(key string, defaultValue string) string {
	if val, ok := g.policy[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultValue
}

// GetPolicyBool retrieves a bool from policy
func (g *BaseGate) GetPolicyBool(key string, defaultValue bool) bool {
	if val, ok := g.policy[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultValue
}

// GetPolicyInt retrieves an int from policy
func (g *BaseGate) GetPolicyInt(key string, defaultValue int) int {
	if val, ok := g.policy[key]; ok {
		switch v := val.(type) {
		case int:
			return v
		case float64:
			return int(v)
		}
	}
	return defaultValue
}

// DetermineAction returns the action based on gate decision
func (g *BaseGate) DetermineAction(allow bool, timedOut bool) ActionConfig {
	if timedOut && g.actions.Timeout.Action != "" {
		return g.actions.Timeout
	}

	if allow {
		if g.actions.Pass.Action != "" {
			return g.actions.Pass
		}
		return ActionConfig{Action: ActionContinue}
	}

	if g.actions.Fail.Action != "" {
		return g.actions.Fail
	}
	return ActionConfig{Action: ActionBlock}
}

// GateRegistry manages gate types and evaluators
type GateRegistry struct {
	evaluators map[GateType]GateEvaluator
	factories  map[GateType]GateFactory
	mu         sync.RWMutex
}

// GateFactory creates gates of a specific type
type GateFactory func(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error)

// NewGateRegistry creates a new gate registry
func NewGateRegistry() *GateRegistry {
	return &GateRegistry{
		evaluators: make(map[GateType]GateEvaluator),
		factories:  make(map[GateType]GateFactory),
	}
}

// RegisterEvaluator registers a gate evaluator
func (r *GateRegistry) RegisterEvaluator(gateType GateType, evaluator GateEvaluator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evaluators[gateType] = evaluator
}

// RegisterFactory registers a gate factory
func (r *GateRegistry) RegisterFactory(gateType GateType, factory GateFactory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[gateType] = factory
}

// GetEvaluator retrieves a gate evaluator
func (r *GateRegistry) GetEvaluator(gateType GateType) (GateEvaluator, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	evaluator, ok := r.evaluators[gateType]
	if !ok {
		return nil, fmt.Errorf("no evaluator registered for gate type: %s", gateType)
	}
	return evaluator, nil
}

// CreateGate creates a gate from configuration
func (r *GateRegistry) CreateGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error) {
	r.mu.RLock()
	factory, ok := r.factories[config.Type]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no factory registered for gate type: %s", config.Type)
	}

	return factory(config, logger, tracer)
}

// GlobalGateRegistry is the global registry for gates
var GlobalGateRegistry = NewGateRegistry()
