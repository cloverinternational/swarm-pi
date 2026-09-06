package mode

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// yamlModeDefinition represents the YAML structure for a mode.
type yamlModeDefinition struct {
	ID          string                  `yaml:"id,omitempty"`
	Name        string                  `yaml:"name"`
	Description string                  `yaml:"description,omitempty"`
	Version     string                  `yaml:"version,omitempty"`
	Config      yamlModeConfig          `yaml:"config,omitempty"`
	Groups      []yamlAgentGroup        `yaml:"groups"`
	Transitions []yamlTransition        `yaml:"transitions,omitempty"`
	EntryHooks  []string                `yaml:"entry_hooks,omitempty"`
	ExitHooks   []string                `yaml:"exit_hooks,omitempty"`
	Steering    *yamlSteeringConfig     `yaml:"steering,omitempty"`
	Parameters  []yamlWorkflowParameter `yaml:"parameters,omitempty"`
	Metadata    map[string]any          `yaml:"metadata,omitempty"`
}

// yamlWorkflowParameter represents a workflow input parameter definition.
type yamlWorkflowParameter struct {
	ID          string                   `yaml:"id"`
	Question    string                   `yaml:"question"`
	Description string                   `yaml:"description,omitempty"`
	Type        string                   `yaml:"type"` // text, number, choice, multi_choice, confirm
	Required    bool                     `yaml:"required,omitempty"`
	Default     any                      `yaml:"default,omitempty"`
	Choices     []string                 `yaml:"choices,omitempty"`
	Validation  *yamlParameterValidation `yaml:"validation,omitempty"`
	Metadata    map[string]any           `yaml:"metadata,omitempty"`
}

// yamlParameterValidation represents parameter validation rules.
type yamlParameterValidation struct {
	MinLength int     `yaml:"min_length,omitempty"`
	MaxLength int     `yaml:"max_length,omitempty"`
	Pattern   string  `yaml:"pattern,omitempty"`
	Min       float64 `yaml:"min,omitempty"`
	Max       float64 `yaml:"max,omitempty"`
	Step      float64 `yaml:"step,omitempty"`
}

// yamlModeConfig represents YAML mode configuration.
type yamlModeConfig struct {
	MaxDuration            string         `yaml:"max_duration,omitempty"`
	AllowHumanIntervention *bool          `yaml:"allow_human_intervention,omitempty"`
	FailOnSteeringBlock    *bool          `yaml:"fail_on_steering_block,omitempty"`
	MaxRetries             *int           `yaml:"max_retries,omitempty"`
	TimeoutBehavior        string         `yaml:"timeout_behavior,omitempty"`
	Custom                 map[string]any `yaml:"custom,omitempty"`
}

// yamlAgentGroup represents YAML agent group.
type yamlAgentGroup struct {
	ID             string                   `yaml:"id,omitempty"`
	Name           string                   `yaml:"name"`
	Description    string                   `yaml:"description,omitempty"`
	Execution      string                   `yaml:"execution"`
	Agents         []yamlAgentDefinition    `yaml:"agents"`
	DependsOn      []string                 `yaml:"depends_on,omitempty"`
	Completion     yamlCompletionCriteria   `yaml:"completion,omitempty"`
	Timeout        string                   `yaml:"timeout,omitempty"`
	Steering       *yamlGroupSteeringConfig `yaml:"steering,omitempty"`
	Coordinator    *yamlAgentDefinition     `yaml:"coordinator,omitempty"`
	OutputStrategy string                   `yaml:"output_strategy,omitempty"`
	Metadata       map[string]any           `yaml:"metadata,omitempty"`
}

// yamlAgentDefinition represents YAML agent definition.
type yamlAgentDefinition struct {
	ID                   string              `yaml:"id,omitempty"`
	Name                 string              `yaml:"name"`
	Description          string              `yaml:"description,omitempty"`
	Provider             string              `yaml:"provider"`
	Model                string              `yaml:"model"`
	Tools                []string            `yaml:"tools,omitempty"`
	SystemPrompt         string              `yaml:"system_prompt,omitempty"`
	SystemPromptTemplate string              `yaml:"system_prompt_template,omitempty"`
	PromptVariables      map[string]string   `yaml:"prompt_variables,omitempty"`
	ProviderConfig       agent.ProviderHints `yaml:"provider_config,omitempty"`
	ContextSources       []string            `yaml:"context_sources,omitempty"`
	Capabilities         *yamlCapabilities   `yaml:"capabilities,omitempty"`
}

// yamlCapabilities represents YAML agent capabilities.
type yamlCapabilities struct {
	MaxTokens      *int     `yaml:"max_tokens,omitempty"`
	Temperature    *float64 `yaml:"temperature,omitempty"`
	MaxTurns       *int     `yaml:"max_turns,omitempty"`
	TimeoutSeconds *int     `yaml:"timeout_seconds,omitempty"`
	Streaming      *bool    `yaml:"streaming,omitempty"`
	CachePrompts   *bool    `yaml:"cache_prompts,omitempty"`
	ParallelTools  *bool    `yaml:"parallel_tools,omitempty"`
}

// yamlCompletionCriteria represents YAML completion criteria.
type yamlCompletionCriteria struct {
	Type          string   `yaml:"type,omitempty"`
	Threshold     *float64 `yaml:"threshold,omitempty"`
	MinAgents     *int     `yaml:"min_agents,omitempty"`
	MaxFailures   *int     `yaml:"max_failures,omitempty"`
	RequireOutput *bool    `yaml:"require_output,omitempty"`
}

// yamlGroupSteeringConfig represents YAML group steering config.
type yamlGroupSteeringConfig struct {
	ValidatePlan       *yamlValidationConfig `yaml:"validate_plan,omitempty"`
	SynthesisStrategy  string                `yaml:"synthesis_strategy,omitempty"`
	ConflictResolution string                `yaml:"conflict_resolution,omitempty"`
	Custom             map[string]any        `yaml:"custom,omitempty"`
}

// yamlValidationConfig represents YAML validation config.
type yamlValidationConfig struct {
	Type          string   `yaml:"type"`
	Prompt        string   `yaml:"prompt,omitempty"`
	Rules         []string `yaml:"rules,omitempty"`
	MinConfidence float64  `yaml:"min_confidence,omitempty"`
}

// yamlTransition represents YAML transition.
type yamlTransition struct {
	From       string         `yaml:"from"`
	To         string         `yaml:"to"`
	Condition  string         `yaml:"condition"`
	MaxRetries int            `yaml:"max_retries,omitempty"`
	Priority   int            `yaml:"priority,omitempty"`
	Metadata   map[string]any `yaml:"metadata,omitempty"`
}

// yamlSteeringConfig represents YAML steering config.
type yamlSteeringConfig struct {
	Type         string               `yaml:"type"`
	LLMMetaAgent *yamlAgentDefinition `yaml:"llm_meta_agent,omitempty"`
	Rules        []yamlSteeringRule   `yaml:"rules,omitempty"`
	Custom       map[string]any       `yaml:"custom,omitempty"`
}

// yamlSteeringRule represents YAML steering rule.
type yamlSteeringRule struct {
	ID         string         `yaml:"id"`
	Condition  string         `yaml:"condition"`
	Action     string         `yaml:"action"`
	Priority   int            `yaml:"priority,omitempty"`
	Parameters map[string]any `yaml:"parameters,omitempty"`
}

// convertAgentDefinition converts YAML agent to agent.Definition.
func convertAgentDefinition(ya yamlAgentDefinition) (*agent.Definition, error) {
	def := &agent.Definition{
		ID:                   ya.ID,
		Name:                 ya.Name,
		Description:          ya.Description,
		Provider:             ya.Provider,
		Model:                ya.Model,
		ToolHints:            ya.Tools,
		SystemPrompt:         ya.SystemPrompt,
		SystemPromptTemplate: ya.SystemPromptTemplate,
		PromptVariables:      ya.PromptVariables,
		ProviderConfig:       ya.ProviderConfig,
		ContextSources:       ya.ContextSources,
	}

	// Generate ID if not provided
	if def.ID == "" {
		def.ID = fmt.Sprintf("%s-%s", strings.ToLower(strings.ReplaceAll(def.Name, " ", "-")), def.Model)
	}

	// Convert capabilities
	if ya.Capabilities != nil {
		caps := &agent.Capabilities{}

		if ya.Capabilities.MaxTokens != nil {
			caps.MaxTokens = *ya.Capabilities.MaxTokens
		}

		if ya.Capabilities.Temperature != nil {
			caps.Temperature = *ya.Capabilities.Temperature
		}

		if ya.Capabilities.MaxTurns != nil {
			caps.MaxTurns = *ya.Capabilities.MaxTurns
		}

		// Note: TimeoutSeconds, Streaming, CachePrompts, ParallelTools
		// are not in agent.Capabilities struct yet, but kept in YAML for future use

		def.Capabilities = caps
	}

	return def, nil
}

// parseDuration parses duration strings like "30m", "1h", "5s".
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration string")
	}

	// Handle common formats: "30m", "1h", "5s", "2h30m"
	duration, err := time.ParseDuration(s)
	if err == nil {
		return duration, nil
	}

	// Try parsing as seconds if no unit
	if seconds, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), nil
	}

	return 0, fmt.Errorf("invalid duration: %s", s)
}
