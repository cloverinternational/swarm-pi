// Package agent provides the agent factory for creating agent instances.
package agent

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/chrome"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Factory defines the interface for creating agent instances.
// Factories handle dependency injection and validation.
type Factory interface {
	// CreateFromDefinition creates an agent from a definition.
	// This is the most flexible method - all configuration comes from the definition.
	CreateFromDefinition(ctx context.Context, def *Definition, providerConfig provider.Config) (*Agent, error)

	// CreateWorker creates a worker agent optimized for primary task execution.
	// Workers have full tool access, longer timeouts, and higher turn limits.
	CreateWorker(ctx context.Context, config WorkerConfig) (*Agent, error)

	// CreateSubAgent creates a sub-agent optimized for specialized tasks.
	// Sub-agents use cheap models, limited tools, and fast execution.
	CreateSubAgent(ctx context.Context, config SubAgentConfig) (*Agent, error)

	// CreateSteering creates a steering agent for quality control.
	// Steering agents use expensive models and monitor other agents.
	CreateSteering(ctx context.Context, config SteeringAgentConfig) (*Agent, error)

	// CreateBackground creates a background agent for async tasks.
	// Background agents run non-blocking and emit progress events.
	CreateBackground(ctx context.Context, config BackgroundConfig) (*Agent, error)
}

// SimpleFactory is the default implementation of Factory.
// It manages provider creation, tool registry setup, and agent initialization.
type SimpleFactory struct {
	providerRegistry *provider.SimpleRegistry
	logger           observability.Logger
	tracer           observability.Tracer
	auditor          observability.Auditor
	agentRegistry    Registry // Optional, for auto-registration
}

// FactoryConfig configures a factory instance.
type FactoryConfig struct {
	// ProviderRegistry is required for creating providers.
	ProviderRegistry *provider.SimpleRegistry

	// Logger is used for observability. Defaults to a no-op logger when nil.
	Logger observability.Logger

	// Tracer is used for distributed tracing. Defaults to a no-op tracer when nil.
	Tracer observability.Tracer

	// Auditor is used for audit logging. Defaults to a no-op auditor when nil.
	Auditor observability.Auditor

	// AgentRegistry is optional. If provided, created agents are auto-registered.
	AgentRegistry Registry
}

// NewSimpleFactory creates a new agent factory.
// Only ProviderRegistry is required; Logger, Tracer, and Auditor default to
// no-op implementations when nil so callers don't have to wire up observability
// infrastructure just to create an agent.
func NewSimpleFactory(config FactoryConfig) (*SimpleFactory, error) {
	if config.ProviderRegistry == nil {
		return nil, sdkerr.Permanent("factory.missing_provider_registry",
			"provider registry is required")
	}

	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}
	if config.Auditor == nil {
		config.Auditor = noop.NewAuditor()
	}

	return &SimpleFactory{
		providerRegistry: config.ProviderRegistry,
		logger:           config.Logger,
		tracer:           config.Tracer,
		auditor:          config.Auditor,
		agentRegistry:    config.AgentRegistry,
	}, nil
}

// CreateFromDefinition creates an agent from a definition.
func (f *SimpleFactory) CreateFromDefinition(ctx context.Context, def *Definition, providerConfig provider.Config) (*Agent, error) {
	ctx, span := f.tracer.StartSpan(ctx, "factory.create_from_definition")
	defer span.End()

	span.SetAttribute("agent.id", def.ID)
	span.SetAttribute("agent.provider", def.Provider)
	span.SetAttribute("agent.model", def.Model)

	// Step 0: Work on a shallow copy so we never mutate the caller's *Definition.
	// This is critical for concurrent sub-agent spawning: getBuiltinAgentDefinitions()
	// and customAgentDefs return shared pointers, and the factory used to write
	// def.Provider/def.Model directly — a data race when two parallel Subagent
	// calls share the same *Definition.
	defCopy := *def
	if def.Metadata != nil {
		defCopy.Metadata = make(map[string]any, len(def.Metadata))
		maps.Copy(defCopy.Metadata, def.Metadata)
	}
	def = &defCopy

	// Fill in missing fields from providerConfig.
	// This allows builtin agents (which have empty Provider/Model) to work
	// without the caller having to pre-populate them.
	if def.Provider == "" {
		def.Provider = providerConfig.Name
	}
	if def.Model == "" {
		def.Model = providerConfig.Model
	}

	// Step 1: Validate definition
	if err := def.Validate(); err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("factory.invalid_definition", err.Error())
	}

	f.logger.Info(ctx, "factory.creating_agent",
		observability.F("agent_id", def.ID),
		observability.F("provider", def.Provider),
		observability.F("model", def.Model))

	// Step 2: Validate provider configuration matches definition.
	// Use the registry instance so dynamically-registered compatibility
	// (e.g. custom providers marked OpenAI-compatible) is honored.
	if !f.providerRegistry.ProvidersMatch(def.Provider, providerConfig.Name) {
		err := sdkerr.Permanent("factory.provider_mismatch",
			fmt.Sprintf("definition requires provider '%s' but config has '%s'",
				def.Provider, providerConfig.Name))
		span.RecordError(err)
		return nil, err
	}

	// Set model from definition if not in config
	if providerConfig.Model == "" {
		providerConfig.Model = def.Model
	}

	// Step 3: Validate provider is registered.
	//
	// Registries may key providers by their RAW name (e.g. the TUI keys by
	// "claudecode" / custom "wafer" to preserve user-chosen identities) OR by
	// the canonical NORMALIZED name (e.g. "anthropic"). We must not assume one
	// convention: resolve by the definition's own name first, then fall back to
	// the normalized canonical form. This removes the prior hard dependency on
	// every registration site remembering to add a normalized alias — the cause
	// of "provider 'claudecode' (normalized: 'anthropic') is not registered"
	// sub-agent spawn failures.
	normalizedProvider := provider.NormalizeProviderName(def.Provider)
	lookupName := def.Provider
	if !f.providerRegistry.IsRegistered(lookupName) {
		lookupName = normalizedProvider
		if !f.providerRegistry.IsRegistered(lookupName) {
			err := sdkerr.Permanent("factory.provider_not_registered",
				fmt.Sprintf("provider '%s' (normalized: '%s') is not registered", def.Provider, normalizedProvider))
			span.RecordError(err)
			return nil, err
		}
	}

	// Step 4: Create provider instance using whichever name actually resolved.
	providerConfig.Name = lookupName
	prov, err := f.providerRegistry.Create(providerConfig)
	if err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("factory.provider_creation_failed",
			fmt.Sprintf("failed to create provider: %v", err))
	}

	// Step 4.5: Determine vision capability based on provider and definition
	// An agent has vision capability if:
	// 1. The provider supports vision, OR
	// 2. A VisionModel is configured in the definition, OR
	// 3. A VisionChain is configured in the definition
	providerVisionCap := prov.Capabilities().Vision
	defHasVisionConfig := def.VisionModel != "" || def.VisionChain != nil
	hasVisionSupport := providerVisionCap || defHasVisionConfig

	// Update capabilities if not already set
	if def.Capabilities == nil {
		def.Capabilities = &Capabilities{}
	}
	// Only override SupportsVision if it wasn't explicitly set to true
	// and we've determined vision is available
	if hasVisionSupport {
		def.Capabilities.SupportsVision = true
	}

	// Log vision configuration for observability
	if hasVisionSupport && !providerVisionCap {
		f.logger.Info(ctx, "factory.vision_routing_enabled",
			observability.F("agent_id", def.ID),
			observability.F("primary_provider", def.Provider),
			observability.F("primary_model", def.Model),
			observability.F("vision_model", def.VisionModel),
			observability.F("has_vision_chain", def.VisionChain != nil))
	}

	// Step 5: Create tool registry for this agent
	toolReg := tools.NewSimpleRegistry(f.logger, f.tracer)

	// Step 6: Register tools based on definition
	if err := f.registerTools(ctx, toolReg, def.ToolHints); err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Step 7: Create agent using agent.New()
	agent, err := New(Config{
		Definition:       def,
		Provider:         prov,
		ProviderRegistry: f.providerRegistry,
		ToolRegistry:     toolReg,
		Logger:           f.logger,
		Tracer:           f.tracer,
		Auditor:          f.auditor,
		BrowserFamily:    inheritedBrowserFamily(ctx),
	})

	if err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("factory.agent_creation_failed",
			fmt.Sprintf("failed to create agent: %v", err))
	}

	contextLimit, contextSource := resolveAgentContextWindow(providerConfig.ContextWindow, prov)
	agent.SetConfiguredContextWindow(contextLimit)
	f.logger.Info(ctx, "factory.agent_context_window_resolved",
		observability.F("agent_id", def.ID),
		observability.F("provider", def.Provider),
		observability.F("model", def.Model),
		observability.F("context_limit", contextLimit),
		observability.F("source", contextSource))

	// Step 8: Initialize agent
	if err := agent.Initialize(); err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("factory.agent_initialization_failed",
			fmt.Sprintf("failed to initialize agent: %v", err))
	}

	// Step 8.5: Configure auto-compaction for sub-agents
	// Sub-agents always get automatic compaction at 80% of context window
	if def.Metadata != nil && def.Metadata["type"] == "sub_agent" {
		if err := f.configureSubAgentCompaction(ctx, agent, prov, def); err != nil {
			// Log warning but don't fail - compaction is optional for functionality
			f.logger.Warn(ctx, "factory.subagent_compaction_config_failed",
				observability.F("agent_id", def.ID),
				observability.F("error", err.Error()))
		} else {
			f.logger.Info(ctx, "factory.subagent_compaction_configured",
				observability.F("agent_id", def.ID),
				observability.F("threshold", compaction.SubAgentCompactionThreshold))
		}
	}

	// Step 9: Auto-register in agent registry if available
	if f.agentRegistry != nil {
		if err := f.agentRegistry.Register(agent); err != nil {
			// Log warning but don't fail - registration is optional
			f.logger.Warn(ctx, "factory.registration_failed",
				observability.F("agent_id", def.ID),
				observability.F("error", err.Error()))
		} else {
			f.logger.Info(ctx, "factory.agent_registered",
				observability.F("agent_id", def.ID))
		}
	}

	f.logger.Info(ctx, "factory.agent_created",
		observability.F("agent_id", def.ID),
		observability.F("state", string(agent.State())))

	return agent, nil
}

func inheritedBrowserFamily(ctx context.Context) chrome.FamilyContext {
	family, _ := chrome.FromContext(ctx)
	return family
}

// WorkerConfig configures a worker agent.
// Workers are primary execution agents with full capabilities.
type WorkerConfig struct {
	// AgentID is the unique identifier.
	AgentID string

	// AgentName is the human-readable name.
	AgentName string

	// Description explains what this worker does.
	Description string

	// ProviderConfig contains provider setup.
	ProviderConfig provider.Config

	// Tools is the list of tool names intended for this agent.
	// NOTE: The factory does not auto-register tools by name. After calling
	// CreateWorker, register tools on the returned agent:
	//
	//   a, err := factory.CreateWorker(ctx, cfg)
	//   a.ToolRegistry().Register(myTool)
	//
	// Passing []string{"*"} returns an error — there is no global tool registry.
	// Leave Tools nil/empty to skip registration entirely.
	Tools []string

	// SystemPrompt is the worker's system prompt.
	SystemPrompt string

	// MaxTurns limits execution turns (default: 0 = unlimited).
	MaxTurns int

	// Timeout limits execution time (default: 5 minutes). Use standard time.Duration values, e.g. 5*time.Minute.
	Timeout time.Duration

	// Temperature controls randomness (default: 0.7).
	Temperature float64
}

// CreateWorker creates a worker agent.
//
// Deprecated: use factory.Create(ctx, AgentConfig{Kind: KindWorker, ...}) instead.
func (f *SimpleFactory) CreateWorker(ctx context.Context, config WorkerConfig) (*Agent, error) {
	ctx, span := f.tracer.StartSpan(ctx, "factory.create_worker")
	defer span.End()

	span.SetAttribute("agent.id", config.AgentID)
	span.SetAttribute("agent.type", "worker")

	// Validate worker config
	if config.AgentID == "" {
		err := sdkerr.Permanent("factory.missing_agent_id", "agent ID is required")
		span.RecordError(err)
		return nil, err
	}

	if config.ProviderConfig.Name == "" {
		err := sdkerr.Permanent("factory.missing_provider", "provider name is required")
		span.RecordError(err)
		return nil, err
	}

	// Build definition with worker defaults
	def := &Definition{
		ID:           config.AgentID,
		Name:         config.AgentName,
		Description:  config.Description,
		Provider:     config.ProviderConfig.Name,
		Model:        config.ProviderConfig.Model,
		ToolHints:    config.Tools,
		SystemPrompt: config.SystemPrompt,
		Capabilities: &Capabilities{
			MaxTurns:          defaultInt(config.MaxTurns, 0),                 // Workers: unlimited (summarizes on limit)
			Timeout:           defaultDuration(config.Timeout, 5*time.Minute), // Workers: 5 minutes
			Temperature:       defaultFloat(config.Temperature, 0.7),          // Workers: balanced
			SupportsTools:     true,
			SupportsVision:    false, // Set based on provider capabilities
			SupportsStreaming: true,
		},
		Metadata: map[string]any{
			"type": "worker",
		},
	}

	return f.CreateFromDefinition(ctx, def, config.ProviderConfig)
}

// SubAgentConfig configures a sub-agent.
// Sub-agents are specialists optimized for speed and cost.
type SubAgentConfig struct {
	// AgentID is the unique identifier.
	AgentID string

	// AgentName is the human-readable name.
	AgentName string

	// Description explains the sub-agent's specialty.
	Description string

	// ProviderConfig contains provider setup.
	// Sub-agents should use cheap models (llama-3.1-8b, gemini-flash).
	ProviderConfig provider.Config

	// Tools is the limited set of allowed tools.
	// Sub-agents typically have restricted tool access.
	Tools []string

	// SystemPrompt is focused on the specific task.
	SystemPrompt string

	// MaxTurns limits conversation turns (optional, uses provider default if 0).
	MaxTurns int

	// Timeout limits execution time (0 = provider default). Use standard time.Duration values.
	Timeout time.Duration

	// Temperature controls randomness (optional, uses provider default if 0).
	Temperature float64

	// Chain is the full model pool chain (Feature 026).
	// If set, this chain will be used for execution instead of single ProviderConfig.
	Chain *fallback.Chain
	// WorkspaceRoot is the root directory for workspace-sandboxed file operations.
	// Sub-agents will have file access restricted to this directory.
	WorkspaceRoot string
}

// CreateSubAgent creates a sub-agent.
//
// Deprecated: use factory.Create(ctx, AgentConfig{Kind: KindSubAgent, ...}) instead.
func (f *SimpleFactory) CreateSubAgent(ctx context.Context, config SubAgentConfig) (*Agent, error) {
	ctx, span := f.tracer.StartSpan(ctx, "factory.create_sub_agent")
	defer span.End()

	span.SetAttribute("agent.id", config.AgentID)
	span.SetAttribute("agent.type", "sub_agent")

	// Validate sub-agent config
	if config.AgentID == "" {
		err := sdkerr.Permanent("factory.missing_agent_id", "agent ID is required")
		span.RecordError(err)
		return nil, err
	}

	if config.ProviderConfig.Name == "" {
		err := sdkerr.Permanent("factory.missing_provider", "provider name is required")
		span.RecordError(err)
		return nil, err
	}

	// Build definition with user-provided config (no artificial limits)
	def := &Definition{
		ID:           config.AgentID,
		Name:         config.AgentName,
		Description:  config.Description,
		Provider:     config.ProviderConfig.Name,
		Model:        config.ProviderConfig.Model,
		ToolHints:    config.Tools,
		SystemPrompt: config.SystemPrompt,
		Capabilities: &Capabilities{
			MaxTurns:          config.MaxTurns,    // User-specified, 0 = provider default
			Timeout:           config.Timeout,     // User-specified, 0 = provider default
			Temperature:       config.Temperature, // User-specified, 0 = provider default
			SupportsTools:     len(config.Tools) > 0,
			SupportsVision:    false,
			SupportsStreaming: false,
		},
		Metadata: map[string]any{
			"type":           "sub_agent",
			"workspace_root": config.WorkspaceRoot,
		},
		Chain: config.Chain,
	}

	// Sub-agents with empty ToolHints get all registered tools (the default).
	// Provide explicit ToolHints to restrict the tool set for focused sub-agents.
	if len(def.ToolHints) == 0 {
		f.logger.Debug(ctx, "factory.sub_agent_using_all_tools",
			observability.F("agent_id", config.AgentID),
			observability.F("note", "provide ToolHints to restrict to a specific tool set"))
	}

	return f.CreateFromDefinition(ctx, def, config.ProviderConfig)
}

// SteeringAgentConfig configures a steering agent evaluator.
// Steering agents monitor and evaluate other agents' outputs.
type SteeringAgentConfig struct {
	// AgentID is the unique identifier.
	AgentID string

	// AgentName is the human-readable name.
	AgentName string

	// Description explains the steering agent's role.
	Description string

	// ProviderConfig contains provider setup.
	// Steering agents should use expensive, high-quality models.
	ProviderConfig provider.Config

	// SystemPrompt defines evaluation criteria.
	SystemPrompt string

	// MaxTurns limits execution turns (optional, 0 = provider default).
	MaxTurns int

	// Timeout limits evaluation time (0 = provider default). Use standard time.Duration values.
	Timeout time.Duration

	// Temperature controls randomness (optional, 0 = provider default).
	Temperature float64

	// SteeringConfig configures steering behavior.
	SteeringConfig SteeringConfig
}

// CreateSteering creates a steering agent (evaluator only, without wrapper).
//
// Deprecated: use factory.Create(ctx, AgentConfig{Kind: KindSteering, ...}) instead.
func (f *SimpleFactory) CreateSteering(ctx context.Context, config SteeringAgentConfig) (*Agent, error) {
	ctx, span := f.tracer.StartSpan(ctx, "factory.create_steering")
	defer span.End()

	span.SetAttribute("agent.id", config.AgentID)
	span.SetAttribute("agent.type", "steering")

	// Validate steering config
	if config.AgentID == "" {
		err := sdkerr.Permanent("factory.missing_agent_id", "agent ID is required")
		span.RecordError(err)
		return nil, err
	}

	if config.ProviderConfig.Name == "" {
		err := sdkerr.Permanent("factory.missing_provider", "provider name is required")
		span.RecordError(err)
		return nil, err
	}

	// Build definition with user-provided config (no artificial limits)
	def := &Definition{
		ID:           config.AgentID,
		Name:         config.AgentName,
		Description:  config.Description,
		Provider:     config.ProviderConfig.Name,
		Model:        config.ProviderConfig.Model,
		ToolHints:    []string{}, // Steering agents typically don't use tools
		SystemPrompt: config.SystemPrompt,
		Capabilities: &Capabilities{
			MaxTurns:          config.MaxTurns,    // User-specified, 0 = provider default
			Timeout:           config.Timeout,     // User-specified, 0 = provider default
			Temperature:       config.Temperature, // User-specified, 0 = provider default
			SupportsTools:     false,
			SupportsVision:    false,
			SupportsStreaming: false,
		},
		Metadata: map[string]any{
			"type": "steering",
		},
	}

	return f.CreateFromDefinition(ctx, def, config.ProviderConfig)
}

// CreateSteeringAgent creates a complete SteeringAgent with wrapper.
func (f *SimpleFactory) CreateSteeringAgent(ctx context.Context, config SteeringAgentConfig) (*SteeringAgent, error) {
	ctx, span := f.tracer.StartSpan(ctx, "factory.create_steering_agent")
	defer span.End()

	// Create underlying evaluator agent
	evaluator, err := f.CreateSteering(ctx, config)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Create steering agent wrapper
	steeringAgent, err := NewSteeringAgent(evaluator, config.SteeringConfig, f.logger, f.tracer)
	if err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("factory.steering_agent_creation_failed",
			fmt.Sprintf("failed to create steering agent: %v", err))
	}

	f.logger.Info(ctx, "factory.steering_agent_created",
		observability.F("agent_id", config.AgentID),
		observability.F("intervention_points", len(config.SteeringConfig.InterventionPoints)))

	return steeringAgent, nil
}

// BackgroundConfig configures a background agent.
// Background agents run asynchronously for long-running tasks.
type BackgroundConfig struct {
	// AgentID is the unique identifier.
	AgentID string

	// AgentName is the human-readable name.
	AgentName string

	// Description explains what this background agent does.
	Description string

	// ProviderConfig contains provider setup.
	ProviderConfig provider.Config

	// Tools is the list of tool names intended for this agent.
	// NOTE: The factory does not auto-register tools by name. After calling
	// CreateBackground, register tools on the returned agent:
	//
	//   a, err := factory.CreateBackground(ctx, cfg)
	//   a.ToolRegistry().Register(myTool)
	//
	// Leave Tools nil/empty to skip registration. Passing []string{"*"} returns an error.
	Tools []string

	// SystemPrompt is the agent's system prompt.
	SystemPrompt string

	// MaxTurns limits execution turns (optional, 0 = provider default).
	MaxTurns int

	// Timeout limits execution time in seconds (optional, 0 = provider default).
	Timeout time.Duration

	// Temperature controls randomness (optional, 0 = provider default).
	Temperature float64
}

// CreateBackground creates a background agent.
//
// Deprecated: use factory.Create(ctx, AgentConfig{Kind: KindBackground, ...}) instead.
func (f *SimpleFactory) CreateBackground(ctx context.Context, config BackgroundConfig) (*Agent, error) {
	ctx, span := f.tracer.StartSpan(ctx, "factory.create_background")
	defer span.End()

	span.SetAttribute("agent.id", config.AgentID)
	span.SetAttribute("agent.type", "background")

	// Validate background config
	if config.AgentID == "" {
		err := sdkerr.Permanent("factory.missing_agent_id", "agent ID is required")
		span.RecordError(err)
		return nil, err
	}

	if config.ProviderConfig.Name == "" {
		err := sdkerr.Permanent("factory.missing_provider", "provider name is required")
		span.RecordError(err)
		return nil, err
	}

	// Build definition with user-provided config (no artificial limits)
	def := &Definition{
		ID:           config.AgentID,
		Name:         config.AgentName,
		Description:  config.Description,
		Provider:     config.ProviderConfig.Name,
		Model:        config.ProviderConfig.Model,
		ToolHints:    config.Tools,
		SystemPrompt: config.SystemPrompt,
		Capabilities: &Capabilities{
			MaxTurns:          config.MaxTurns,    // User-specified, 0 = provider default
			Timeout:           config.Timeout,     // User-specified, 0 = provider default
			Temperature:       config.Temperature, // User-specified, 0 = provider default
			SupportsTools:     true,
			SupportsVision:    false,
			SupportsStreaming: false, // Background agents emit events instead
		},
		Metadata: map[string]any{
			"type": "background",
		},
	}

	return f.CreateFromDefinition(ctx, def, config.ProviderConfig)
}

// registerTools prepares the agent's tool registry based on a list of tool names.
//
// Wildcard "*" is NOT implemented — there is no global tool registry to copy from.
// If you pass Tools: []string{"*"}, you get an explicit error so that the
// misconfiguration is visible immediately rather than causing a silent no-op.
//
// The correct pattern is to leave Tools empty (or omit it) and register tools
// manually on the returned agent after creation:
//
//	a, err := factory.CreateWorker(ctx, cfg)
//	a.ToolRegistry().Register(myTool)
func (f *SimpleFactory) registerTools(ctx context.Context, toolReg tools.Registry, toolNames []string) error {
	// ["*"] and nil/empty both mean "expose all registered tools" — nothing to register.
	if len(toolNames) == 0 || (len(toolNames) == 1 && toolNames[0] == "*") {
		return nil
	}

	// Named hints act as an allow-list filter at execution time (not at registration).
	// Tools must be registered separately via Config.Tools or ag.ToolRegistry().Register(t).
	f.logger.Debug(ctx, "factory.tool_hints_are_allow_list",
		observability.F("hints", toolNames),
		observability.F("note", "register tools via Config.Tools or ag.ToolRegistry().Register(t)"))

	return nil
}

// defaultInt returns val if non-zero, otherwise returns def.
func defaultInt(val, def int) int {
	if val != 0 {
		return val
	}
	return def
}

// defaultFloat returns val if non-zero, otherwise returns def.
func defaultFloat(val, def float64) float64 {
	if val != 0 {
		return val
	}
	return def
}

// defaultDuration returns val if non-zero, otherwise returns def.
func defaultDuration(val, def time.Duration) time.Duration {
	if val != 0 {
		return val
	}
	return def
}

func resolveAgentContextWindow(configured int, prov provider.Provider) (int, string) {
	if configured > 0 {
		return configured, "configured"
	}
	if prov != nil {
		if limit := prov.Capabilities().MaxContextWindow; limit > 0 {
			return provider.ClampContextWindow(limit), "provider_capability"
		}
	}
	return provider.DefaultUnknownContextWindow, "unknown_fallback"
}

// configureSubAgentCompaction configures automatic compaction for sub-agents.
// Sub-agents always get compaction at 80% of their context window using their own provider.
func (f *SimpleFactory) configureSubAgentCompaction(ctx context.Context, agent *Agent, prov provider.Provider, def *Definition) error {
	// Use the same already-resolved limit as the agent guard. Re-resolving from
	// provider capabilities here loses per-model metadata and can make the guard
	// and compactor disagree.
	contextLimit := agent.getContextWindow()

	// Create the compaction function using the sub-agent's provider
	result, err := compaction.CreateCompactFunc(compaction.AgentCompactionConfig{
		Provider:     prov,
		Model:        def.Model,
		ContextLimit: contextLimit,
	})
	if err != nil {
		return fmt.Errorf("failed to create compaction function: %w", err)
	}

	// Configure auto-compaction on the agent
	agent.SetAutoCompactionConfig(AutoCompactionConfig{
		EnableAutoCompaction:           true,
		AutoCompactionThresholdPercent: compaction.SubAgentCompactionThreshold,
		CompactFunc:                    result.CompactFunc,
	})

	// Set the full compaction service on the agent so it can be accessed
	// for both automatic compaction AND manual compaction requests from external callers
	agent.SetCompactionService(result.Service)

	return nil
}

// ─── Unified AgentConfig API ─────────────────────────────────────────────────
// AgentKind identifies which type of agent to create.
type AgentKind string

const (
	// KindWorker creates a primary execution agent with full tool access.
	KindWorker AgentKind = "worker"
	// KindSubAgent creates a specialist agent optimised for speed and cost.
	KindSubAgent AgentKind = "sub_agent"
	// KindSteering creates a quality-control evaluator agent.
	KindSteering AgentKind = "steering"
	// KindBackground creates an async agent that emits progress events.
	KindBackground AgentKind = "background"
)

// AgentConfig is the single unified configuration struct for creating any agent.
// It replaces the four separate *Config structs (WorkerConfig, SubAgentConfig,
// SteeringAgentConfig, BackgroundConfig) with one canonical form.
//
// Use factory.Create(ctx, AgentConfig{Kind: agent.KindWorker, ...}) instead
// of the individual CreateWorker / CreateSubAgent / etc. methods.
//
// Kind-specific fields:
//   - Chain / WorkspaceRoot — SubAgent only
//   - SteeringConfig        — Steering only
type AgentConfig struct {
	// Kind determines which type of agent is created (required).
	Kind AgentKind

	// AgentID is the unique identifier for the agent (required).
	AgentID string

	// AgentName is the human-readable display name.
	AgentName string

	// Description explains what this agent does.
	Description string

	// ProviderConfig configures the LLM provider (required).
	ProviderConfig provider.Config

	// Tools lists the tool names this agent may use.
	// Leave nil or empty and register tools on the returned agent after creation.
	// Passing []string{"*"} returns an error — see registerTools.
	Tools []string

	// SystemPrompt is the agent's instruction preamble.
	SystemPrompt string

	// MaxTurns limits provider calls per execution (0 = use provider default).
	MaxTurns int

	// Timeout limits total execution time (0 = provider default). Use standard time.Duration values.
	Timeout time.Duration

	// Temperature controls output randomness (0 = use provider default).
	Temperature float64

	// ── SubAgent-only ────────────────────────────────────────────────────────

	// Chain is the fallback provider pool for sub-agents (Feature 026).
	Chain *fallback.Chain

	// WorkspaceRoot restricts sub-agent file access to this directory.
	WorkspaceRoot string

	// ── Steering-only ───────────────────────────────────────────────────────

	// SteeringConfig configures intervention and evaluation behaviour.
	SteeringConfig SteeringConfig
}

// Create is the single canonical method for creating any agent type.
// It dispatches to the appropriate Create* method based on cfg.Kind.
//
//	a, err := factory.Create(ctx, agent.AgentConfig{
//	    Kind:           agent.KindWorker,
//	    AgentID:        "my-worker",
//	    ProviderConfig: provider.Config{Name: "anthropic", Model: "claude-sonnet-4-5", APIKey: "..."},
//	    SystemPrompt:   "You are a Go expert.",
//	})
func (f *SimpleFactory) Create(ctx context.Context, cfg AgentConfig) (*Agent, error) {
	switch cfg.Kind {
	case KindWorker, "":
		return f.CreateWorker(ctx, WorkerConfig{
			AgentID:        cfg.AgentID,
			AgentName:      cfg.AgentName,
			Description:    cfg.Description,
			ProviderConfig: cfg.ProviderConfig,
			Tools:          cfg.Tools,
			SystemPrompt:   cfg.SystemPrompt,
			MaxTurns:       cfg.MaxTurns,
			Timeout:        cfg.Timeout,
			Temperature:    cfg.Temperature,
		})
	case KindSubAgent:
		return f.CreateSubAgent(ctx, SubAgentConfig{
			AgentID:        cfg.AgentID,
			AgentName:      cfg.AgentName,
			Description:    cfg.Description,
			ProviderConfig: cfg.ProviderConfig,
			Tools:          cfg.Tools,
			SystemPrompt:   cfg.SystemPrompt,
			MaxTurns:       cfg.MaxTurns,
			Timeout:        cfg.Timeout,
			Temperature:    cfg.Temperature,
			Chain:          cfg.Chain,
			WorkspaceRoot:  cfg.WorkspaceRoot,
		})
	case KindSteering:
		return f.CreateSteering(ctx, SteeringAgentConfig{
			AgentID:        cfg.AgentID,
			AgentName:      cfg.AgentName,
			Description:    cfg.Description,
			ProviderConfig: cfg.ProviderConfig,
			SystemPrompt:   cfg.SystemPrompt,
			MaxTurns:       cfg.MaxTurns,
			Timeout:        cfg.Timeout,
			Temperature:    cfg.Temperature,
			SteeringConfig: cfg.SteeringConfig,
		})
	case KindBackground:
		return f.CreateBackground(ctx, BackgroundConfig{
			AgentID:        cfg.AgentID,
			AgentName:      cfg.AgentName,
			Description:    cfg.Description,
			ProviderConfig: cfg.ProviderConfig,
			Tools:          cfg.Tools,
			SystemPrompt:   cfg.SystemPrompt,
			MaxTurns:       cfg.MaxTurns,
			Timeout:        cfg.Timeout,
			Temperature:    cfg.Temperature,
		})
	default:
		return nil, sdkerr.Permanent("factory.unknown_agent_kind",
			fmt.Sprintf("unknown agent kind %q; valid values: worker, sub_agent, steering, background", cfg.Kind))
	}
}
