package agent

import (
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Builder constructs an [Agent] using a fluent chained API.
// Obtain one via [Build], configure it with method calls, then call [Builder.Create].
//
// Example — minimal agent:
//
//	ag, err := agent.Build().
//	    Provider(p).
//	    Model("claude-sonnet-4-5").
//	    Create()
//
// Example — full configuration:
//
//	ag, err := agent.Build().
//	    Provider(p).
//	    Model("claude-sonnet-4-5").
//	    ID("code-reviewer").
//	    SystemPrompt("You review Go code for correctness and style.").
//	    MaxTurns(30).
//	    Timeout(5 * time.Minute).
//	    Tools(myReadTool, myBashTool).
//	    Logger(slogLogger).
//	    WithThinking(8192).
//	    Create()
type Builder struct {
	id           string
	provider     provider.Provider
	model        string
	systemPrompt string
	maxTurns     int
	timeout      time.Duration
	toolsList    []tools.Tool
	logger       observability.Logger
	tracer       observability.Tracer
	hooks        HooksManager

	// Provider-level hints
	thinkingEnabled bool
	thinkingBudget  int

	// Execution mode configuration
	executionMode ExecutionMode
	managedConfig *ManagedConfig

	// Vision routing configuration
	visionModel string
	visionChain *fallback.Chain

	// A2A networking configuration
	a2a *a2a.Config
}

// Build returns a new [Builder] for creating agents fluently.
// It is the recommended single entry point for agent construction, replacing
// the three prior paths ([NewQuick], [New], and factory.Create).
//
// Call terminal method [Builder.Create] to obtain the agent.
func Build() *Builder {
	return &Builder{}
}

// Provider sets the LLM provider the agent will use.
// Required unless you use a package like "quick" that auto-detects the provider.
func (b *Builder) Provider(p provider.Provider) *Builder {
	b.provider = p
	return b
}

// Model sets the model identifier (e.g. "claude-sonnet-4-5", "gpt-4o").
// Required.
func (b *Builder) Model(model string) *Builder {
	b.model = model
	return b
}

// ID sets a custom agent identifier used in logs and traces.
// Defaults to "<provider-name>-agent" when not set.
func (b *Builder) ID(id string) *Builder {
	b.id = id
	return b
}

// SystemPrompt sets the agent's instruction preamble.
func (b *Builder) SystemPrompt(prompt string) *Builder {
	b.systemPrompt = prompt
	return b
}

// MaxTurns limits how many provider round-trips the agent may make per
// [Agent.Execute] call. 0 means no limit (agent runs until natural completion).
func (b *Builder) MaxTurns(n int) *Builder {
	b.maxTurns = n
	return b
}

// Timeout sets the maximum wall-clock time for a single [Agent.Execute] call.
// 0 means no timeout.
func (b *Builder) Timeout(d time.Duration) *Builder {
	b.timeout = d
	return b
}

// Tools registers one or more tools the agent may call.
// Can be called multiple times; all tools are accumulated.
//
// Example:
//
//	agent.Build().
//	    ...
//	    Tools(
//	        tools.Func[SearchParams]("search", "Search docs", searchFn),
//	        tools.Typed[ReadParams](&ReadTool{}),
//	    ).
//	    Create()
func (b *Builder) Tools(tt ...tools.Tool) *Builder {
	b.toolsList = append(b.toolsList, tt...)
	return b
}

// Logger attaches a structured logger. Defaults to a noop logger.
func (b *Builder) Logger(l observability.Logger) *Builder {
	b.logger = l
	return b
}

// Tracer attaches a distributed tracer. Defaults to a noop tracer.
func (b *Builder) Tracer(t observability.Tracer) *Builder {
	b.tracer = t
	return b
}

// Hooks attaches a hooks manager for before/after-tool event emission.
func (b *Builder) Hooks(h HooksManager) *Builder {
	b.hooks = h
	return b
}

// WithThinking enables extended thinking mode for providers that support it
// (currently Anthropic claude-3-7-sonnet and later).
//
// budgetTokens controls the maximum token budget for internal reasoning;
// the Anthropic minimum is 1024. A value of 0 uses the provider default (8192).
func (b *Builder) WithThinking(budgetTokens int) *Builder {
	b.thinkingEnabled = true
	if budgetTokens > 0 {
		b.thinkingBudget = budgetTokens
	} else {
		b.thinkingBudget = 8192
	}
	return b
}

// ExecutionMode sets where the agent executes its tasks.
// Use agent.ExecutionModeLocal for in-process execution (default),
// agent.ExecutionModeManaged for remote managed execution, or
// agent.ExecutionModeHybrid for dynamic switching.
func (b *Builder) ExecutionMode(mode ExecutionMode) *Builder {
	b.executionMode = mode
	return b
}

// Managed configures the agent for remote managed execution.
// This sets ExecutionMode to ExecutionModeManaged and configures the
// connection to the managed agents service.
//
// endpoint is the URL of the managed service (e.g., "https://cloud.swarmcode.ai").
// apiKey is the authentication key for the service.
// projectID is the project identifier for workspace isolation.
//
// Example:
//
//	agent.Build().
//	    Provider(p).
//	    Model("claude-sonnet-4-5").
//	    Managed("https://cloud.swarmcode.ai", "sk-xxx", "my-project").
//	    Create()
func (b *Builder) Managed(endpoint, apiKey, projectID string) *Builder {
	b.executionMode = ExecutionModeManaged
	b.managedConfig = &ManagedConfig{
		Endpoint:  endpoint,
		APIKey:    apiKey,
		ProjectID: projectID,
	}
	return b
}

// ManagedWithConfig configures managed execution with full control over options.
// Use this for advanced configurations like SSE streaming, custom timeouts, etc.
func (b *Builder) ManagedWithConfig(cfg *ManagedConfig) *Builder {
	b.executionMode = ExecutionModeManaged
	b.managedConfig = cfg
	return b
}

// Hybrid enables hybrid execution mode, allowing dynamic switching between
// local and managed execution based on task requirements or steering commands.
func (b *Builder) Hybrid(endpoint, apiKey, projectID string) *Builder {
	b.executionMode = ExecutionModeHybrid
	b.managedConfig = &ManagedConfig{
		Endpoint:  endpoint,
		APIKey:    apiKey,
		ProjectID: projectID,
	}
	return b
}

// WithVisionModel configures a vision-capable model for processing images
// when the primary model lacks vision support.
//
// When a tool like Read encounters an image file, or a browser tool captures
// a screenshot, the agent will automatically route those operations through
// this vision model instead of failing or returning unprocessed data.
//
// Format: "provider/model" (e.g., "anthropic/claude-3-5-sonnet-20241022").
//
// Example:
//
//	agent.Build().
//	    Provider(codersProvider).
//	    Model("deepseek-coder").  // Non-vision model
//	    WithVisionModel("anthropic/claude-3-5-sonnet-20241022").  // Vision fallback
//	    Create()
func (b *Builder) WithVisionModel(modelRef string) *Builder {
	b.visionModel = modelRef
	return b
}

// WithVisionChain configures a fallback chain of vision-capable models.
// This provides resilience when the primary vision model is unavailable.
//
// The chain is tried in order until one succeeds, similar to the primary
// model fallback chain.
//
// Example:
//
//	visionChain := fallback.NewChain("anthropic", "claude-3-5-sonnet-20241022").
//	    AddFallback("openai", "gpt-4-vision-preview").
//	    AddFallback("gemini", "gemini-1.5-pro")
//
//	agent.Build().
//	    Provider(p).
//	    Model("codex").  // Non-vision model
//	    WithVisionChain(visionChain).
//	    Create()
func (b *Builder) WithVisionChain(chain *fallback.Chain) *Builder {
	b.visionChain = chain
	return b
}

// WithA2A enables A2A-native peer communication for this agent.
func (b *Builder) WithA2A(cfg *a2a.Config) *Builder {
	b.a2a = cfg
	return b
}

// Create validates the builder configuration and returns a ready-to-use [Agent].
// Returns an error if any required fields are missing or invalid.
func (b *Builder) Create() (*Agent, error) {
	if b.provider == nil {
		return nil, fmt.Errorf("agent.Build: Provider is required — call .Provider(p) before .Create()")
	}
	if b.model == "" {
		return nil, fmt.Errorf("agent.Build: Model is required — call .Model(\"model-name\") before .Create()")
	}

	id := b.id
	if id == "" {
		id = b.provider.Name() + "-agent"
	}

	def := &Definition{
		ID:            id,
		Name:          id,
		Provider:      b.provider.Name(),
		Model:         b.model,
		ExecutionMode: b.executionMode,
	}

	if b.systemPrompt != "" {
		def.SystemPrompt = b.systemPrompt
	}

	if b.maxTurns > 0 || b.timeout > 0 {
		def.Capabilities = &Capabilities{
			MaxTurns: b.maxTurns,
			Timeout:  b.timeout,
		}
	}

	if b.thinkingEnabled {
		def.ProviderConfig = ProviderHints{
			ThinkingEnabled: true,
			ThinkingBudget:  b.thinkingBudget,
		}
	}

	// Set managed config if provided
	if b.managedConfig != nil {
		def.ManagedConfig = b.managedConfig
	}

	// Set vision routing configuration
	if b.visionModel != "" {
		def.VisionModel = b.visionModel
	}
	if b.visionChain != nil {
		def.VisionChain = b.visionChain
	}

	return New(Config{
		Definition:   def,
		Provider:     b.provider,
		Tools:        b.toolsList, // registered by New() into the auto-created registry
		Logger:       b.logger,
		Tracer:       b.tracer,
		HooksManager: b.hooks,
		A2A:          b.a2a,
	})
}

// MustCreate is like [Builder.Create] but panics on error.
// Useful in main() or test setup where misconfiguration is a programmer error.
func (b *Builder) MustCreate() *Agent {
	ag, err := b.Create()
	if err != nil {
		panic("agent.Build.MustCreate: " + err.Error())
	}
	return ag
}
