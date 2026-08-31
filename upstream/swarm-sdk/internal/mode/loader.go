package mode

import (
	"fmt"
	"os"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"gopkg.in/yaml.v3"
)

// ModeLoader loads mode definitions from YAML files.
type ModeLoader struct {
	logger    observability.Logger
	tracer    observability.Tracer
	validator *ModeValidator
}

// NewModeLoader creates a new mode loader.
func NewModeLoader(logger observability.Logger, tracer observability.Tracer) *ModeLoader {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	return &ModeLoader{
		logger:    logger,
		tracer:    tracer,
		validator: NewModeValidator(logger),
	}
}

// LoadFromFile loads a mode from a YAML file.
func (l *ModeLoader) LoadFromFile(path string) (*Mode, error) {
	ctx, span := l.tracer.StartSpan(nil, "mode.load_from_file")
	defer span.End()

	l.logger.Info(ctx, "loading mode from file",
		observability.F("path", path))

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ModeError{
			Type:    ErrorTypeNotFound,
			Message: fmt.Sprintf("failed to read file: %s", path),
			Err:     err,
		}
	}

	// Parse YAML
	mode, err := l.LoadFromBytes(data)
	if err != nil {
		return nil, err
	}

	l.logger.Info(ctx, "mode loaded successfully",
		observability.F("mode_id", mode.ID),
		observability.F("groups", len(mode.Groups)))

	return mode, nil
}

// LoadFromBytes loads a mode from YAML bytes.
func (l *ModeLoader) LoadFromBytes(data []byte) (*Mode, error) {
	ctx, span := l.tracer.StartSpan(nil, "mode.load_from_bytes")
	defer span.End()

	// Parse YAML into intermediate structure
	var yamlMode yamlModeDefinition
	if err := yaml.Unmarshal(data, &yamlMode); err != nil {
		return nil, &ModeError{
			Type:    ErrorTypeValidation,
			Message: "failed to parse YAML",
			Err:     err,
		}
	}

	// Convert to Mode struct
	mode, err := l.convertFromYAML(&yamlMode)
	if err != nil {
		return nil, err
	}

	// Validate
	if err := l.validator.Validate(mode); err != nil {
		return nil, err
	}

	l.logger.Info(ctx, "mode loaded from bytes",
		observability.F("mode_id", mode.ID))

	return mode, nil
}

// LoadFromString loads a mode from a YAML string.
func (l *ModeLoader) LoadFromString(yaml string) (*Mode, error) {
	return l.LoadFromBytes([]byte(yaml))
}

// convertFromYAML converts YAML structure to Mode.
func (l *ModeLoader) convertFromYAML(ym *yamlModeDefinition) (*Mode, error) {
	// Auto-generate ID from name if not provided
	id := ym.ID
	if id == "" {
		id = ym.Name
	}

	// Create mode
	mode := &Mode{
		ID:          id,
		Name:        ym.Name,
		Description: ym.Description,
		Version:     ym.Version,
		Config:      l.convertConfig(ym.Config),
		Groups:      make([]*AgentGroup, 0, len(ym.Groups)),
		Transitions: make([]*Transition, 0, len(ym.Transitions)),
		EntryHooks:  ym.EntryHooks,
		ExitHooks:   ym.ExitHooks,
		Metadata:    ym.Metadata,
	}

	// Set defaults if not provided
	if mode.Version == "" {
		mode.Version = "1.0.0"
	}

	// Convert steering config
	if ym.Steering != nil {
		steering, err := l.convertSteeringConfig(ym.Steering)
		if err != nil {
			return nil, err
		}
		mode.Steering = steering
	}

	// Convert groups
	for i, yg := range ym.Groups {
		group, err := l.convertGroup(yg)
		if err != nil {
			return nil, &ModeError{
				Type:    ErrorTypeValidation,
				Message: fmt.Sprintf("failed to convert group at index %d", i),
				Err:     err,
			}
		}
		mode.Groups = append(mode.Groups, group)
	}

	// Convert transitions
	for i, yt := range ym.Transitions {
		transition, err := l.convertTransition(yt)
		if err != nil {
			return nil, &ModeError{
				Type:    ErrorTypeValidation,
				Message: fmt.Sprintf("failed to convert transition at index %d", i),
				Err:     err,
			}
		}
		mode.Transitions = append(mode.Transitions, transition)
	}

	// Convert parameters
	if len(ym.Parameters) > 0 {
		mode.Parameters = make([]*WorkflowParameter, 0, len(ym.Parameters))
		for i, yp := range ym.Parameters {
			param, err := l.convertParameter(yp)
			if err != nil {
				return nil, &ModeError{
					Type:    ErrorTypeValidation,
					Message: fmt.Sprintf("failed to convert parameter at index %d", i),
					Err:     err,
				}
			}
			mode.Parameters = append(mode.Parameters, param)
		}
	}

	return mode, nil
}

// convertConfig converts YAML config to ModeConfig.
func (l *ModeLoader) convertConfig(yc yamlModeConfig) ModeConfig {
	config := DefaultModeConfig()

	if yc.MaxDuration != "" {
		if duration, err := parseDuration(yc.MaxDuration); err == nil {
			config.MaxDuration = duration
		}
	}

	if yc.AllowHumanIntervention != nil {
		config.AllowHumanIntervention = *yc.AllowHumanIntervention
	}

	if yc.FailOnSteeringBlock != nil {
		config.FailOnSteeringBlock = *yc.FailOnSteeringBlock
	}

	if yc.MaxRetries != nil {
		config.MaxRetries = *yc.MaxRetries
	}

	if yc.TimeoutBehavior != "" {
		config.TimeoutBehavior = yc.TimeoutBehavior
	}

	if yc.Custom != nil {
		config.Custom = yc.Custom
	}

	return config
}

// convertSteeringConfig converts YAML steering to SteeringConfig.
func (l *ModeLoader) convertSteeringConfig(ys *yamlSteeringConfig) (*SteeringConfig, error) {
	steering := &SteeringConfig{
		Type:   ys.Type,
		Rules:  make([]*SteeringRule, 0, len(ys.Rules)),
		Custom: ys.Custom,
	}

	// Convert LLM meta-agent if present
	if ys.LLMMetaAgent != nil {
		agentDef, err := convertAgentDefinition(*ys.LLMMetaAgent)
		if err != nil {
			return nil, err
		}
		steering.LLMMetaAgent = agentDef
	}

	// Convert rules
	for _, yr := range ys.Rules {
		rule := &SteeringRule{
			ID:         yr.ID,
			Condition:  yr.Condition,
			Action:     yr.Action,
			Priority:   yr.Priority,
			Parameters: yr.Parameters,
		}
		steering.Rules = append(steering.Rules, rule)
	}

	return steering, nil
}

// convertGroup converts YAML group to AgentGroup.
func (l *ModeLoader) convertGroup(yg yamlAgentGroup) (*AgentGroup, error) {
	// Auto-generate ID from name if not provided
	id := yg.ID
	if id == "" {
		id = yg.Name
	}

	group := &AgentGroup{
		ID:          id,
		Name:        yg.Name,
		Description: yg.Description,
		Execution:   ExecutionStrategy(yg.Execution),
		Agents:      make([]*agent.Definition, 0, len(yg.Agents)),
		DependsOn:   yg.DependsOn,
		Completion:  l.convertCompletionCriteria(yg.Completion),
		Metadata:    yg.Metadata,
	}

	// Parse timeout
	if yg.Timeout != "" {
		if duration, err := parseDuration(yg.Timeout); err == nil {
			group.Timeout = duration
		}
	}

	// Convert agents
	for i, ya := range yg.Agents {
		agent, err := convertAgentDefinition(ya)
		if err != nil {
			return nil, &ModeError{
				Type:    ErrorTypeValidation,
				Message: fmt.Sprintf("failed to convert agent at index %d in group %s", i, yg.Name),
				Err:     err,
			}
		}
		group.Agents = append(group.Agents, agent)
	}

	// Convert steering config
	if yg.Steering != nil {
		steering := &GroupSteeringConfig{
			SynthesisStrategy:  yg.Steering.SynthesisStrategy,
			ConflictResolution: yg.Steering.ConflictResolution,
			Custom:             yg.Steering.Custom,
		}

		if yg.Steering.ValidatePlan != nil {
			steering.ValidatePlan = &ValidationConfig{
				Type:          yg.Steering.ValidatePlan.Type,
				Prompt:        yg.Steering.ValidatePlan.Prompt,
				Rules:         yg.Steering.ValidatePlan.Rules,
				MinConfidence: yg.Steering.ValidatePlan.MinConfidence,
			}
		}

		group.Steering = steering
	}

	// Parse output strategy
	switch OutputStrategy(yg.OutputStrategy) {
	case OutputStrategySynthesize:
		group.OutputStrategy = OutputStrategySynthesize
	case OutputStrategyFirst:
		group.OutputStrategy = OutputStrategyFirst
	default:
		group.OutputStrategy = OutputStrategyRaw
	}

	// Convert coordinator if present
	if yg.Coordinator != nil {
		coordinator, err := convertAgentDefinition(*yg.Coordinator)
		if err != nil {
			return nil, &ModeError{
				Type:    ErrorTypeValidation,
				Message: fmt.Sprintf("failed to convert coordinator agent in group %s", yg.Name),
				Err:     err,
			}
		}
		group.Coordinator = coordinator
	}

	return group, nil
}

// convertCompletionCriteria converts YAML completion to CompletionCriteria.
func (l *ModeLoader) convertCompletionCriteria(yc yamlCompletionCriteria) CompletionCriteria {
	criteria := DefaultCompletionCriteria()
	if yc.Type != "" {
		criteria.Type = yc.Type
	}
	if yc.Threshold != nil {
		criteria.Threshold = *yc.Threshold
	}
	if yc.MinAgents != nil {
		criteria.MinAgents = *yc.MinAgents
	}
	if yc.MaxFailures != nil {
		criteria.MaxFailures = *yc.MaxFailures
	}
	if yc.RequireOutput != nil {
		criteria.RequireOutput = *yc.RequireOutput
	}
	return criteria
}

// convertTransition converts YAML transition to Transition.
func (l *ModeLoader) convertTransition(yt yamlTransition) (*Transition, error) {
	transition := &Transition{
		From:       yt.From,
		To:         yt.To,
		Condition:  yt.Condition,
		MaxRetries: yt.MaxRetries,
		Priority:   yt.Priority,
		Metadata:   yt.Metadata,
	}

	return transition, nil
}

// convertParameter converts YAML parameter to WorkflowParameter.
func (l *ModeLoader) convertParameter(yp yamlWorkflowParameter) (*WorkflowParameter, error) {
	param := &WorkflowParameter{
		ID:          yp.ID,
		Question:    yp.Question,
		Description: yp.Description,
		Type:        yp.Type,
		Required:    yp.Required,
		Default:     yp.Default,
		Choices:     yp.Choices,
		Metadata:    yp.Metadata,
	}

	// Convert validation if present
	if yp.Validation != nil {
		param.Validation = &ParameterValidation{
			MinLength: yp.Validation.MinLength,
			MaxLength: yp.Validation.MaxLength,
			Pattern:   yp.Validation.Pattern,
			Min:       yp.Validation.Min,
			Max:       yp.Validation.Max,
			Step:      yp.Validation.Step,
		}
	}

	return param, nil
}
