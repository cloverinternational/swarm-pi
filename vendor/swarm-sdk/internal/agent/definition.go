// Package agent implements the agent runtime layer (Ring 2).
// Agents are stateless executors that operate on conversations using providers and tools.
package agent

import (
	"bytes"
	"errors"
	"maps"
	"text/template"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
)

// ExecutionMode determines where an agent executes its tasks.
type ExecutionMode string

const (
	// ExecutionModeLocal runs the agent locally in the same process.
	// This is the default behavior - all tool calls and LLM interactions
	// happen directly in the calling process.
	ExecutionModeLocal ExecutionMode = "local"

	// ExecutionModeManaged runs the agent on a remote managed compute node.
	// Tool calls and LLM interactions are proxied through the managed service.
	// This enables distributed execution, isolated environments, and
	// centralized resource management.
	ExecutionModeManaged ExecutionMode = "managed"

	// ExecutionModeHybrid allows dynamic switching between local and managed
	// execution based on task requirements, resource availability, or
	// explicit steering commands.
	ExecutionModeHybrid ExecutionMode = "hybrid"
)

// ManagedConfig contains configuration for managed execution mode.
type ManagedConfig struct {
	// Endpoint is the URL of the managed agents service.
	// Example: "https://cloud.swarmcode.ai" or "http://149.28.63.81:8080"
	Endpoint string `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`

	// APIKey is the authentication key for the managed service.
	APIKey string `json:"-" yaml:"-"` // Never serialize API keys

	// ProjectID is the project identifier for workspace isolation.
	ProjectID string `json:"project_id,omitempty" yaml:"project_id,omitempty"`

	// SessionID is an optional existing session to attach to.
	// If empty, a new session will be created.
	SessionID string `json:"session_id,omitempty" yaml:"session_id,omitempty"`

	// Timeout is the timeout for managed session operations.
	Timeout time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// MaxRetries is the maximum number of retries for transient errors.
	MaxRetries int `json:"max_retries,omitempty" yaml:"max_retries,omitempty"`

	// EnableSSE enables Server-Sent Events for real-time streaming.
	EnableSSE bool `json:"enable_sse,omitempty" yaml:"enable_sse,omitempty"`
}

// Definition defines an agent's configuration and capabilities.
// This is the data structure that describes what an agent is before it executes.
type Definition struct {
	// ID is the unique identifier for this agent.
	ID string `json:"id" yaml:"id"`

	// Name is the human-readable name.
	Name string `json:"name" yaml:"name"`

	// Description explains what this agent does.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Provider is the LLM provider name (e.g., "anthropic", "openai").
	Provider string `json:"provider" yaml:"provider"`

	// Model is the specific model to use (e.g., "claude-3-5-sonnet-20241022").
	Model string `json:"model" yaml:"model"`

	// ExecutionMode determines where the agent executes.
	// Default is ExecutionModeLocal for in-process execution.
	// Use ExecutionModeManaged for remote managed execution.
	ExecutionMode ExecutionMode `json:"execution_mode,omitempty" yaml:"execution_mode,omitempty"`

	// ManagedConfig contains configuration for managed execution.
	// Only used when ExecutionMode is "managed" or "hybrid".
	ManagedConfig *ManagedConfig `json:"managed_config,omitempty" yaml:"managed_config,omitempty"`

	// ToolHints controls which tools the agent may use at execution time.
	//
	// Semantics:
	//
	//   nil / empty  — agent may use ALL tools currently registered in its registry
	//                  (default for agents created via NewQuick or New without ToolHints)
	//   ["*"]        — explicit "all tools" — equivalent to nil/empty
	//   ["a","b"]    — agent may only use the named tools; others are hidden from the LLM
	//
	// Note: ToolHints is an allow-list filter applied at execution time, not at
	// registration time. Tools must still be registered via ToolRegistry.Register or
	// Config.Tools — ToolHints only controls which registered tools are visible to
	// the LLM on each request.
	//
	// The json/yaml key is "tools" for backward-compatible serialisation.
	ToolHints []string `json:"tools,omitempty" yaml:"tools,omitempty"`

	// SystemPrompt is the agent's system prompt (static).
	SystemPrompt string `json:"system_prompt,omitempty" yaml:"system_prompt,omitempty"`

	// SystemPromptTemplate is a Go template for dynamic system prompts.
	SystemPromptTemplate string `json:"system_prompt_template,omitempty" yaml:"system_prompt_template,omitempty"`

	// PromptVariables are variables for SystemPromptTemplate.
	PromptVariables map[string]string `json:"prompt_variables,omitempty" yaml:"prompt_variables,omitempty"`

	// Capabilities defines the agent's capabilities and constraints.
	Capabilities *Capabilities `json:"capabilities,omitempty" yaml:"capabilities,omitempty"`

	// ProviderConfig contains provider-specific request hints merged into
	// each LLM call's metadata. Use the typed fields for known hints; put
	// arbitrary/experimental keys in ProviderConfig.Custom.
	ProviderConfig ProviderHints `json:"provider_config" yaml:"provider_config,omitempty"`

	// ContextSources defines where this agent gets additional context.
	// Examples: "groups.planning.output", "conversation.summary"
	ContextSources []string `json:"context_sources,omitempty" yaml:"context_sources,omitempty"`

	// Metadata contains arbitrary custom fields.
	Metadata map[string]any `json:"metadata,omitempty" yaml:"metadata,omitempty"`

	// Chain is the full model pool chain (Feature 026).
	// If set, this chain will be used for execution instead of single Provider/Model.
	Chain *fallback.Chain `json:"-" yaml:"-"`

	// VisionModel optionally specifies a model to use for vision-requiring operations.
	// When the primary model lacks vision capability and a tool needs to process images,
	// this model will be used as a fallback for those specific operations.
	// Format: "provider/model" (e.g., "anthropic/claude-3-5-sonnet-20241022").
	// If not set, the runtime will look for AliasVision in the profile, or use the
	// first vision-capable model in the primary chain.
	VisionModel string `json:"vision_model,omitempty" yaml:"vision_model,omitempty"`

	// VisionChain optionally specifies a fallback chain for vision operations.
	// This allows multiple vision-capable models with fallback order, providing
	// resilience when the primary vision model is unavailable.
	// If not set, VisionModel (or profile AliasVision) is used as a single model.
	VisionChain *fallback.Chain `json:"-" yaml:"-"`

	// SteeringMode selects how the steering subsystem evaluates this agent's
	// actions. Empty defaults to SteeringModePoll (legacy per-intervention-
	// point evaluator). Set to SteeringModeStream to opt this agent into the
	// long-lived streaming steering driver. See steering_stream.go.
	//
	// Note: the streaming hook still must be installed by the consumer
	// runtime (e.g. TUI's HooksManager.EnableSteeringStream). This field
	// only carries the configured intent so consumers can branch.
	SteeringMode SteeringMode `json:"steering_mode,omitempty" yaml:"steering_mode,omitempty"`

	// ClientType identifies the runtime surface that created this agent.
	// Typical values: "tui", "headless", "managed", "sdk".
	// Set at runtime by the client layer; never serialised — use
	// client.WithClientType to supply this at construction time.
	ClientType string `json:"-" yaml:"-"`

	// MachineIDHash is the privacy-preserving hash of the host machine
	// identity, resolved once at client construction time via
	// analytics.DeviceIDHash. Empty when the host ID cannot be determined.
	// Never serialised; not a secret (already hashed).
	MachineIDHash string `json:"-" yaml:"-"`
}

// Capabilities defines what an agent can do and its constraints.
type Capabilities struct {
	// MaxTokens is the maximum tokens per request.
	MaxTokens int `json:"max_tokens,omitempty" yaml:"max_tokens,omitempty"`

	// Temperature controls randomness (0.0-1.0).
	Temperature float64 `json:"temperature,omitempty" yaml:"temperature,omitempty"`

	// ForceTemperature forces the configured Temperature to be sent on every
	// request even when it is exactly 0.0. Without this, a Temperature of 0.0
	// is treated as "unset" (omitempty), and no temperature is sent, so the
	// provider falls back to its non-deterministic default. Headless/benchmark
	// callers set this to true to pin greedy (temperature=0) decoding. Provider
	// translators that reject non-default sampling params still drop it.
	ForceTemperature bool `json:"force_temperature,omitempty" yaml:"force_temperature,omitempty"`

	// TopP controls nucleus sampling.
	TopP float64 `json:"top_p,omitempty" yaml:"top_p,omitempty"`

	// MaxTurns is the maximum number of turns this agent can execute.
	MaxTurns int `json:"max_turns,omitempty" yaml:"max_turns,omitempty"`

	// Timeout is the maximum execution time. Use standard time.Duration values (e.g. 5*time.Minute).
	Timeout time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// SupportsTools indicates if the agent can use tools.
	SupportsTools bool `json:"supports_tools,omitempty" yaml:"supports_tools,omitempty"`

	// SupportsVision indicates if the agent can process images.
	SupportsVision bool `json:"supports_vision,omitempty" yaml:"supports_vision,omitempty"`

	// SupportsStreaming indicates if the agent supports streaming responses.
	SupportsStreaming bool `json:"supports_streaming,omitempty" yaml:"supports_streaming,omitempty"`
}

// ProviderHints holds provider-specific request-level configuration hints.
// The typed fields cover the most common provider features; put any
// experimental or provider-specific keys not listed here in Custom.
//
// These values are merged into the LLM provider request's metadata on every
// call (see agent_execute.go:buildProviderRequest).
type ProviderHints struct {
	// ThinkingEnabled turns on extended thinking mode.
	// Supported by Anthropic claude-3-7-sonnet and later models.
	ThinkingEnabled bool `json:"thinking_enabled,omitempty" yaml:"thinking_enabled,omitempty"`

	// ThinkingBudget is the maximum token budget for the thinking block.
	// Minimum value accepted by Anthropic is 1024.
	// Ignored when ThinkingEnabled is false.
	ThinkingBudget int `json:"thinking_budget,omitempty" yaml:"thinking_budget,omitempty"`

	// SystemCacheControl enables prompt-caching on the system prompt.
	// Anthropic-specific; typical value: {"type":"ephemeral"}.
	SystemCacheControl map[string]string `json:"system_cache_control,omitempty" yaml:"system_cache_control,omitempty"`

	// ToolCacheControl enables prompt-caching on tool definitions.
	// Anthropic-specific; typical value: {"type":"ephemeral"}.
	ToolCacheControl map[string]string `json:"tool_cache_control,omitempty" yaml:"tool_cache_control,omitempty"`

	// MessageCacheControl enables prompt-caching on the message turn boundary.
	// Anthropic-specific; typical value: {"type":"ephemeral"}.
	MessageCacheControl map[string]string `json:"message_cache_control,omitempty" yaml:"message_cache_control,omitempty"`

	// Custom holds arbitrary provider-specific keys not covered above.
	// Values are merged into req.Metadata with the same precedence as the
	// typed fields, so Custom keys that clash with typed field names will
	// overwrite the typed value.
	Custom map[string]any `json:"custom,omitempty" yaml:"custom,omitempty"`
}

// IsZero reports whether the hints are empty (no fields set).
func (h ProviderHints) IsZero() bool {
	return !h.ThinkingEnabled &&
		h.ThinkingBudget == 0 &&
		len(h.SystemCacheControl) == 0 &&
		len(h.ToolCacheControl) == 0 &&
		len(h.MessageCacheControl) == 0 &&
		len(h.Custom) == 0
}

// ToMetadata converts the typed hints to the flat map[string]any expected by
// provider.Request.Metadata.  Typed fields are written first; Custom keys
// are merged last and may override typed fields.
func (h ProviderHints) ToMetadata() map[string]any {
	m := make(map[string]any)
	if h.ThinkingEnabled {
		m["thinking_enabled"] = true
	}
	if h.ThinkingBudget > 0 {
		m["thinking_budget"] = h.ThinkingBudget
	}
	if len(h.SystemCacheControl) > 0 {
		m["system_cache_control"] = h.SystemCacheControl
	}
	if len(h.ToolCacheControl) > 0 {
		m["tool_cache_control"] = h.ToolCacheControl
	}
	if len(h.MessageCacheControl) > 0 {
		m["message_cache_control"] = h.MessageCacheControl
	}
	maps.Copy(m, h.Custom)
	return m
}

// Validate checks if the definition is valid.
func (d *Definition) Validate() error {
	if d.ID == "" {
		return errors.New("agent ID is required")
	}

	if d.Provider == "" {
		return errors.New("agent provider is required")
	}

	if d.Model == "" {
		return errors.New("agent model is required")
	}

	// Name defaults to ID if not set
	if d.Name == "" {
		d.Name = d.ID
	}

	return nil
}

// HasAllTools returns true if the agent is configured to use all tools.
func (d *Definition) HasAllTools() bool {
	if len(d.ToolHints) == 0 {
		return false
	}
	return d.ToolHints[0] == "*"
}

// Clone creates a deep copy of the definition.
func (d *Definition) Clone() *Definition {
	clone := &Definition{
		ID:                   d.ID,
		Name:                 d.Name,
		Description:          d.Description,
		Provider:             d.Provider,
		Model:                d.Model,
		ExecutionMode:        d.ExecutionMode,
		SystemPrompt:         d.SystemPrompt,
		SystemPromptTemplate: d.SystemPromptTemplate,
		VisionModel:          d.VisionModel,
	}

	// Deep copy managed config
	if d.ManagedConfig != nil {
		clone.ManagedConfig = &ManagedConfig{
			Endpoint:   d.ManagedConfig.Endpoint,
			ProjectID:  d.ManagedConfig.ProjectID,
			SessionID:  d.ManagedConfig.SessionID,
			Timeout:    d.ManagedConfig.Timeout,
			MaxRetries: d.ManagedConfig.MaxRetries,
			EnableSSE:  d.ManagedConfig.EnableSSE,
			// APIKey intentionally NOT copied for security
		}
	}

	// Deep copy tools
	if len(d.ToolHints) > 0 {
		clone.ToolHints = make([]string, len(d.ToolHints))
		copy(clone.ToolHints, d.ToolHints)
	}

	// Deep copy context sources
	if len(d.ContextSources) > 0 {
		clone.ContextSources = make([]string, len(d.ContextSources))
		copy(clone.ContextSources, d.ContextSources)
	}

	// Deep copy prompt variables
	if len(d.PromptVariables) > 0 {
		clone.PromptVariables = make(map[string]string, len(d.PromptVariables))
		maps.Copy(clone.PromptVariables, d.PromptVariables)
	}

	// Deep copy provider hints (struct copy for scalars; maps need explicit copy)
	if !d.ProviderConfig.IsZero() {
		clone.ProviderConfig = ProviderHints{
			ThinkingEnabled: d.ProviderConfig.ThinkingEnabled,
			ThinkingBudget:  d.ProviderConfig.ThinkingBudget,
		}
		if len(d.ProviderConfig.SystemCacheControl) > 0 {
			clone.ProviderConfig.SystemCacheControl = make(map[string]string, len(d.ProviderConfig.SystemCacheControl))
			maps.Copy(clone.ProviderConfig.SystemCacheControl, d.ProviderConfig.SystemCacheControl)
		}
		if len(d.ProviderConfig.ToolCacheControl) > 0 {
			clone.ProviderConfig.ToolCacheControl = make(map[string]string, len(d.ProviderConfig.ToolCacheControl))
			maps.Copy(clone.ProviderConfig.ToolCacheControl, d.ProviderConfig.ToolCacheControl)
		}
		if len(d.ProviderConfig.MessageCacheControl) > 0 {
			clone.ProviderConfig.MessageCacheControl = make(map[string]string, len(d.ProviderConfig.MessageCacheControl))
			maps.Copy(clone.ProviderConfig.MessageCacheControl, d.ProviderConfig.MessageCacheControl)
		}
		if len(d.ProviderConfig.Custom) > 0 {
			clone.ProviderConfig.Custom = make(map[string]any, len(d.ProviderConfig.Custom))
			maps.Copy(clone.ProviderConfig.Custom, d.ProviderConfig.Custom)
		}
	}

	// Deep copy metadata
	if len(d.Metadata) > 0 {
		clone.Metadata = make(map[string]any, len(d.Metadata))
		maps.Copy(clone.Metadata, d.Metadata)
	}

	// Copy capabilities
	if d.Capabilities != nil {
		clone.Capabilities = &Capabilities{
			MaxTokens:         d.Capabilities.MaxTokens,
			Temperature:       d.Capabilities.Temperature,
			TopP:              d.Capabilities.TopP,
			MaxTurns:          d.Capabilities.MaxTurns,
			Timeout:           d.Capabilities.Timeout,
			SupportsTools:     d.Capabilities.SupportsTools,
			SupportsVision:    d.Capabilities.SupportsVision,
			SupportsStreaming: d.Capabilities.SupportsStreaming,
		}
	}

	return clone
}

// IsManaged returns true if the agent runs in managed mode.
func (d *Definition) IsManaged() bool {
	return d.ExecutionMode == ExecutionModeManaged ||
		(d.ExecutionMode == ExecutionModeHybrid && d.ManagedConfig != nil)
}

// IsLocal returns true if the agent runs locally.
func (d *Definition) IsLocal() bool {
	return d.ExecutionMode == "" || d.ExecutionMode == ExecutionModeLocal
}

// IsHybrid returns true if the agent can switch between local and managed.
func (d *Definition) IsHybrid() bool {
	return d.ExecutionMode == ExecutionModeHybrid
}

// GetExecutionMode returns the execution mode, defaulting to local.
func (d *Definition) GetExecutionMode() ExecutionMode {
	if d.ExecutionMode == "" {
		return ExecutionModeLocal
	}
	return d.ExecutionMode
}

// HasVisionSupport returns true if the agent has vision capability configured.
// This is true if:
// - The primary provider/model supports vision (checked at runtime), OR
// - A VisionModel is explicitly configured, OR
// - A VisionChain is configured
func (d *Definition) HasVisionSupport() bool {
	return d.VisionModel != "" || d.VisionChain != nil
}

// GetVisionChain returns the vision fallback chain if configured.
// Returns nil if no vision chain is set.
func (d *Definition) GetVisionChain() *fallback.Chain {
	return d.VisionChain
}

// GetVisionModelRef parses VisionModel into a ModelRef.
// Returns an empty ModelRef if VisionModel is not set.
func (d *Definition) GetVisionModelRef() fallback.ModelRef {
	if d.VisionModel == "" {
		return fallback.ModelRef{}
	}
	return fallback.ParseModelRef(d.VisionModel)
}

// RenderSystemPrompt renders the system prompt using the template if provided.
func (d *Definition) RenderSystemPrompt() (string, error) {
	// If no template, return static prompt
	if d.SystemPromptTemplate == "" {
		return d.SystemPrompt, nil
	}

	// Parse template
	tmpl, err := template.New("system_prompt").Parse(d.SystemPromptTemplate)
	if err != nil {
		return "", err
	}

	// Render with variables
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, d.PromptVariables); err != nil {
		return "", err
	}

	return buf.String(), nil
}
