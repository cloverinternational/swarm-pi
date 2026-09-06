package mode

import (
	"context"
	"fmt"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"os"
)

// Special provider/model values that resolve to the user's current configuration
const (
	CurrentProvider = "@current" // Resolves to user's currently active provider
	CurrentModel    = "@current" // Resolves to user's currently active model
)

// ProfileModelPointer represents a model pointer in a profile
type ProfileModelPointer struct {
	Provider string
	Model    string
}

// WorkflowAgentFactory wraps an agent.Factory and provides credentials management for workflow execution.
// It resolves API keys from environment variables and credential stores for each provider used in a workflow.
// It also supports "@current" provider/model resolution to use the user's active configuration.
type WorkflowAgentFactory struct {
	baseFactory     agent.Factory
	credentials     map[string]ProviderCredentials
	currentProvider string                         // User's currently active provider (for @current resolution)
	currentModel    string                         // User's currently active model (for @current resolution)
	profileModels   map[string]ProfileModelPointer // Profile role aliases to model pointers
	toolSource      tools.Registry                 // Source registry to copy tools from into workflow agents
	logger          observability.Logger
}

// ProviderCredentials holds authentication details for a provider
type ProviderCredentials struct {
	APIKey  string
	BaseURL string
}

// NewWorkflowAgentFactory creates a factory for workflow execution with credential management
func NewWorkflowAgentFactory(baseFactory agent.Factory) *WorkflowAgentFactory {
	return &WorkflowAgentFactory{
		baseFactory:   baseFactory,
		credentials:   make(map[string]ProviderCredentials),
		profileModels: make(map[string]ProfileModelPointer),
		logger:        observability.NewNopLogger(),
	}
}

// SetToolSource sets a source tool registry that workflow agents will copy tools from.
func (wf *WorkflowAgentFactory) SetToolSource(source tools.Registry) {
	wf.toolSource = source
}

// SetLogger sets the logger for the workflow factory
func (wf *WorkflowAgentFactory) SetLogger(logger observability.Logger) {
	if logger != nil {
		wf.logger = logger
	}
}

// SetCredentials manually sets credentials for a provider
func (wf *WorkflowAgentFactory) SetCredentials(providerName string, apiKey, baseURL string) {
	wf.credentials[provider.NormalizeProviderName(providerName)] = ProviderCredentials{
		APIKey:  apiKey,
		BaseURL: baseURL,
	}
}

// SetCurrentConfig sets the user's currently active provider and model for @current resolution
func (wf *WorkflowAgentFactory) SetCurrentConfig(providerName, modelName string) {
	wf.currentProvider = provider.NormalizeProviderName(providerName)
	wf.currentModel = modelName
}

// GetCurrentConfig returns the currently configured provider and model
func (wf *WorkflowAgentFactory) GetCurrentConfig() (string, string) {
	return wf.currentProvider, wf.currentModel
}

// SetProfileModels sets the profile role aliases to model pointers mapping
// This allows resolution of role aliases like @main, @steering, @sub_agent to actual models
func (wf *WorkflowAgentFactory) SetProfileModels(models map[string]ProfileModelPointer) {
	wf.profileModels = models
}

// GetProfileModels returns the current profile models mapping
func (wf *WorkflowAgentFactory) GetProfileModels() map[string]ProfileModelPointer {
	return wf.profileModels
}

// resolveProviderModel resolves @current, @profile, and role alias values to
// actual provider/model strings.
//
// Resolution priority:
//  1. @profile/<role>  — look up role in profileModels; fall back to @current
//     if the role is not found or the map is nil/empty.
//  2. <provider>/@<role> — model is a role alias; resolve via profileModels,
//     fall back to @current model if not found.
//  3. @current provider / @current model — resolve to the active session values.
//
// The function NEVER returns a token beginning with "@" as a provider name.
// Any unresolvable pointer degrades to the active session config (@current),
// and if that is also unset it degrades to the "anthropic" / sonnet hardcoded
// baseline so the agent can always be created.
func (wf *WorkflowAgentFactory) resolveProviderModel(providerName, modelName string) (string, string) {
	resolvedProvider := providerName
	resolvedModel := modelName

	// ── Step 1: @profile/<role> ──────────────────────────────────────────────
	// Provider is literally "@profile"; model holds the role alias (e.g. "@main").
	if providerName == "@profile" {
		roleAlias := modelName
		if len(roleAlias) > 0 && roleAlias[0] == '@' {
			roleAlias = roleAlias[1:]
		}
		if len(wf.profileModels) > 0 {
			if pointer, ok := wf.profileModels[roleAlias]; ok {
				resolvedProvider = pointer.Provider
				resolvedModel = pointer.Model
				// Hard-guarantee: resolved values must not themselves be tokens.
				if resolvedProvider == "" || resolvedProvider[0] == '@' {
					resolvedProvider = wf.currentProvider
				}
				if resolvedModel == "" || resolvedModel[0] == '@' {
					resolvedModel = wf.currentModel
				}
			} else {
				// Role not found in map — degrade to @current.
				resolvedProvider = wf.currentProvider
				resolvedModel = wf.currentModel
			}
		} else {
			// No profile map at all — degrade to @current.
			resolvedProvider = wf.currentProvider
			resolvedModel = wf.currentModel
		}
		// After profile resolution, apply @current defaults for any empty slots.
		if resolvedProvider == "" {
			resolvedProvider = wf.currentProvider
		}
		if resolvedModel == "" {
			resolvedModel = wf.currentModel
		}
	}

	// ── Step 2: model-only role alias (e.g. provider="anthropic", model="@steering") ──
	// Only applies when the provider was NOT already "@profile" (handled above).
	if providerName != "@profile" && len(resolvedModel) > 0 && resolvedModel[0] == '@' && resolvedModel != "@current" {
		roleAlias := resolvedModel[1:]
		if pointer, ok := wf.profileModels[roleAlias]; ok {
			resolvedProvider = pointer.Provider
			resolvedModel = pointer.Model
		} else {
			// Role not in profile map — use current model for this provider.
			resolvedModel = wf.currentModel
		}
	}

	// ── Step 3: @current provider ────────────────────────────────────────────
	if resolvedProvider == "@current" || resolvedProvider == "" {
		if wf.currentProvider != "" {
			resolvedProvider = wf.currentProvider
		} else {
			resolvedProvider = "anthropic"
		}
	}

	// ── Step 4: @current model ───────────────────────────────────────────────
	if resolvedModel == "@current" || resolvedModel == "" {
		if wf.currentModel != "" {
			resolvedModel = wf.currentModel
		} else {
			switch provider.NormalizeProviderName(resolvedProvider) {
			case "anthropic":
				resolvedModel = "claude-3-5-sonnet-20241022"
			case "openai":
				resolvedModel = "gpt-4o"
			default:
				resolvedModel = "default"
			}
		}
	}

	// ── Final safety net ─────────────────────────────────────────────────────
	// If either value is still a bare "@something" token (should never happen
	// after the steps above, but guards against future regressions), degrade to
	// the anthropic baseline so we don't send a malformed provider name to the
	// agent factory.
	if len(resolvedProvider) > 0 && resolvedProvider[0] == '@' {
		resolvedProvider = "anthropic"
	}
	if len(resolvedModel) > 0 && resolvedModel[0] == '@' {
		resolvedModel = "claude-3-5-sonnet-20241022"
	}

	return resolvedProvider, resolvedModel
}

// resolveFromMetadata resolves provider/model from definition metadata if profile info is present
func (wf *WorkflowAgentFactory) resolveFromMetadata(def *agent.Definition) (string, string) {
	if def.Metadata == nil {
		return def.Provider, def.Model
	}

	// Check for role alias in metadata
	roleAlias, hasRoleAlias := def.Metadata["role_alias"]
	if !hasRoleAlias {
		return def.Provider, def.Model
	}

	roleAliasStr, ok := roleAlias.(string)
	if !ok {
		return def.Provider, def.Model
	}

	// Look up the role alias in profile models
	if pointer, ok := wf.profileModels[roleAliasStr]; ok {
		return pointer.Provider, pointer.Model
	}

	return def.Provider, def.Model
}

// getCredentials retrieves credentials for a provider, falling back to environment variables
func (wf *WorkflowAgentFactory) getCredentials(providerName string) (ProviderCredentials, error) {
	normalized := provider.NormalizeProviderName(providerName)

	// Check if credentials are explicitly set
	if cred, exists := wf.credentials[normalized]; exists {
		if cred.APIKey != "" {
			return cred, nil
		}
	}

	// Fall back to environment variables
	var envKey string
	switch normalized {
	case "anthropic":
		envKey = os.Getenv("ANTHROPIC_API_KEY")
		if envKey == "" {
			envKey = os.Getenv("CLAUDE_API_KEY")
		}
	case "openai":
		envKey = os.Getenv("OPENAI_API_KEY")
	case "gemini":
		envKey = os.Getenv("GEMINI_API_KEY")
	case "openrouter":
		envKey = os.Getenv("OPENROUTER_API_KEY")
	default:
		// Try generic pattern: PROVIDERNAME_API_KEY
		envVar := fmt.Sprintf("%s_API_KEY", normalized)
		envKey = os.Getenv(envVar)
	}

	if envKey == "" {
		return ProviderCredentials{}, fmt.Errorf("no credentials found for provider %s", providerName)
	}

	return ProviderCredentials{
		APIKey: envKey,
	}, nil
}

// CreateFromDefinition creates an agent from a definition with proper credentials
// Supports @current provider/model values that resolve to user's active configuration
// Also supports profile-based resolution using role aliases stored in metadata
func (wf *WorkflowAgentFactory) CreateFromDefinition(ctx context.Context, def *agent.Definition, providerConfig provider.Config) (*agent.Agent, error) {
	// First, try to resolve from metadata (profile role alias)
	resolvedProvider, resolvedModel := wf.resolveFromMetadata(def)

	// Then apply standard @current resolution
	resolvedProvider, resolvedModel = wf.resolveProviderModel(resolvedProvider, resolvedModel)

	// Get credentials for the resolved provider
	creds, err := wf.getCredentials(resolvedProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials for provider %s: %w", resolvedProvider, err)
	}

	// Enrich provider config with credentials
	providerConfig.APIKey = creds.APIKey
	if creds.BaseURL != "" {
		providerConfig.BaseURL = creds.BaseURL
	}

	// Ensure name and model are set with resolved values
	if providerConfig.Name == "" {
		providerConfig.Name = resolvedProvider
	}
	if providerConfig.Model == "" {
		providerConfig.Model = resolvedModel
	}

	// Create a copy of the definition with resolved provider/model for the base factory
	resolvedDef := *def
	resolvedDef.Provider = resolvedProvider
	resolvedDef.Model = resolvedModel

	// Delegate to base factory
	agentInstance, err := wf.baseFactory.CreateFromDefinition(ctx, &resolvedDef, providerConfig)
	if err != nil {
		return nil, err
	}

	// Register tools from the source registry into the agent's tool registry.
	if wf.toolSource != nil {
		wf.registerToolsForAgent(ctx, agentInstance, &resolvedDef)
	}

	return agentInstance, nil
}

// CreateWorker delegates to base factory
func (wf *WorkflowAgentFactory) CreateWorker(ctx context.Context, config agent.WorkerConfig) (*agent.Agent, error) {
	// Get credentials
	creds, err := wf.getCredentials(config.ProviderConfig.Name)
	if err != nil {
		return nil, err
	}
	config.ProviderConfig.APIKey = creds.APIKey
	if creds.BaseURL != "" && config.ProviderConfig.BaseURL == "" {
		config.ProviderConfig.BaseURL = creds.BaseURL
	}
	return wf.baseFactory.CreateWorker(ctx, config)
}

// CreateSubAgent delegates to base factory
func (wf *WorkflowAgentFactory) CreateSubAgent(ctx context.Context, config agent.SubAgentConfig) (*agent.Agent, error) {
	// Get credentials
	creds, err := wf.getCredentials(config.ProviderConfig.Name)
	if err != nil {
		return nil, err
	}
	config.ProviderConfig.APIKey = creds.APIKey
	if creds.BaseURL != "" && config.ProviderConfig.BaseURL == "" {
		config.ProviderConfig.BaseURL = creds.BaseURL
	}
	return wf.baseFactory.CreateSubAgent(ctx, config)
}

// CreateSteering delegates to base factory
func (wf *WorkflowAgentFactory) CreateSteering(ctx context.Context, config agent.SteeringAgentConfig) (*agent.Agent, error) {
	// Get credentials
	creds, err := wf.getCredentials(config.ProviderConfig.Name)
	if err != nil {
		return nil, err
	}
	config.ProviderConfig.APIKey = creds.APIKey
	if creds.BaseURL != "" && config.ProviderConfig.BaseURL == "" {
		config.ProviderConfig.BaseURL = creds.BaseURL
	}
	return wf.baseFactory.CreateSteering(ctx, config)
}

// CreateBackground delegates to base factory
func (wf *WorkflowAgentFactory) CreateBackground(ctx context.Context, config agent.BackgroundConfig) (*agent.Agent, error) {
	// Get credentials
	creds, err := wf.getCredentials(config.ProviderConfig.Name)
	if err != nil {
		return nil, err
	}
	config.ProviderConfig.APIKey = creds.APIKey
	if creds.BaseURL != "" && config.ProviderConfig.BaseURL == "" {
		config.ProviderConfig.BaseURL = creds.BaseURL
	}
	return wf.baseFactory.CreateBackground(ctx, config)
}

// registerToolsForAgent copies tools from the source registry into the agent's tool registry.
func (wf *WorkflowAgentFactory) registerToolsForAgent(ctx context.Context, agentInstance *agent.Agent, def *agent.Definition) {
	agentReg := agentInstance.ToolRegistry()
	if agentReg == nil {
		wf.logger.Warn(ctx, "workflow_factory.nil_agent_registry",
			observability.F("agent_id", def.ID))
		return
	}

	// Inherit permission checker from source registry
	if sourceSimpleReg, ok := wf.toolSource.(*tools.SimpleRegistry); ok {
		if agentSimpleReg, ok := agentReg.(*tools.SimpleRegistry); ok {
			if checker := sourceSimpleReg.PermissionChecker(); checker != nil {
				agentSimpleReg.SetPermissionChecker(checker)
			}
		}
	}

	if def.HasAllTools() {
		// Register all tools from source
		sourceTools := wf.toolSource.List()
		registered := 0
		for _, toolName := range sourceTools {
			tool, err := wf.toolSource.Get(toolName)
			if err != nil {
				wf.logger.Warn(ctx, "workflow_factory.tool_get_failed",
					observability.F("tool", toolName),
					observability.F("error", err.Error()))
				continue
			}
			if err := agentReg.Register(tool); err != nil {
				wf.logger.Warn(ctx, "workflow_factory.tool_register_failed",
					observability.F("tool", toolName),
					observability.F("error", err.Error()))
				continue
			}
			registered++
		}
		wf.logger.Info(ctx, "workflow_factory.tools_registered",
			observability.F("agent_id", def.ID),
			observability.F("registered", registered),
			observability.F("available", len(sourceTools)))
	} else {
		// Register specific tools by name
		registered := 0
		for _, toolName := range def.ToolHints {
			tool, err := wf.toolSource.Get(toolName)
			if err != nil {
				wf.logger.Warn(ctx, "workflow_factory.tool_not_found",
					observability.F("tool", toolName),
					observability.F("agent_id", def.ID))
				continue
			}
			if err := agentReg.Register(tool); err != nil {
				wf.logger.Warn(ctx, "workflow_factory.tool_register_failed",
					observability.F("tool", toolName),
					observability.F("error", err.Error()))
				continue
			}
			registered++
		}
		wf.logger.Info(ctx, "workflow_factory.specific_tools_registered",
			observability.F("agent_id", def.ID),
			observability.F("registered", registered),
			observability.F("requested", len(def.ToolHints)))
	}
}

// LoadCredentialsFromEnvironment loads all available provider credentials from environment
func (wf *WorkflowAgentFactory) LoadCredentialsFromEnvironment() {
	providers := []struct {
		name    string
		envVars []string
	}{
		{"anthropic", []string{"ANTHROPIC_API_KEY", "CLAUDE_API_KEY"}},
		{"openai", []string{"OPENAI_API_KEY"}},
		{"gemini", []string{"GEMINI_API_KEY"}},
		{"openrouter", []string{"OPENROUTER_API_KEY"}},
	}

	for _, p := range providers {
		for _, envVar := range p.envVars {
			if key := os.Getenv(envVar); key != "" {
				wf.SetCredentials(p.name, key, "")
				break
			}
		}
	}
}
