package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// ============================================================================
// AGENT PROFILE INTEGRATION
// ============================================================================

// CreateAgentForRole creates an agent using the active profile's configuration.
//
// This is the CORE METHOD that makes profiles work - it resolves an alias
// (like "main" or "steering") to an actual provider/model combination and
// creates the appropriate agent type.
//
// Logic:
// 1. Check if profile system is available
// 2. Resolve alias → ModelPointer via active profile
// 3. Extract provider, model, capabilities from pointer
// 4. Choose appropriate factory method based on role
// 5. Create and return agent
//
// Parameters:
//
//	ctx: Context for the operation
//	role: The agent role alias (main, steering, background, etc.)
//
// Returns:
//
//	(*agent.Agent, error)
//	- Success: (configured agent, nil)
//	- Failure: (nil, error with details)
//
// Fallback behavior:
//
//	If profile not available, returns error (caller should use default creation)
//
// Example:
//
//	mainAgent, err := sdk.CreateAgentForRole(ctx, settings.AliasMain)
//	// Creates agent using profile's main → provider/model mapping
func (sdk *SDKIntegration) CreateAgentForRole(ctx context.Context, role settings.ModelAlias) (*agent.Agent, error) {
	// Step 1: Validate profile system is available
	if sdk.profileManager == nil {
		return nil, fmt.Errorf("profile manager not initialized")
	}

	if sdk.activeProfile == nil {
		return nil, fmt.Errorf("no active profile loaded")
	}

	// Step 2: Resolve alias → ModelPointer
	pointer, err := sdk.profileManager.ResolveAlias(role)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve role %s: %w", role, err)
	}

	sdk.logger.Info(ctx, "profile.creating_agent_for_role",
		observability.F("role", string(role)),
		observability.F("provider", pointer.Provider),
		observability.F("model", pointer.Model),
		observability.F("profile", sdk.activeProfile.ID))

	// Step 3: Create provider config
	providerConfig := provider.Config{
		Name:  pointer.Provider,
		Model: pointer.Model,
	}

	// Step 4: Extract capabilities (with defaults)
	temperature := 0.7
	maxTurns := 0 // Unlimited by default
	timeout := time.Duration(300) * time.Second

	if pointer.Capabilities != nil {
		if pointer.Capabilities.Temperature >= 0 {
			temperature = pointer.Capabilities.Temperature
		}
		if pointer.Capabilities.MaxTurns >= 0 {
			maxTurns = pointer.Capabilities.MaxTurns
		}
		if pointer.Capabilities.Timeout > 0 {
			timeout = time.Duration(pointer.Capabilities.Timeout) * time.Second
		}
	}

	// Step 5: Choose factory method based on role
	if sdk.agentFactory == nil {
		return nil, fmt.Errorf("agent factory not available")
	}

	var createdAgent *agent.Agent

	switch role {
	case settings.AliasMain:
		// Main agent - full-featured worker
		createdAgent, err = sdk.agentFactory.CreateWorker(ctx, agent.WorkerConfig{
			AgentID:        fmt.Sprintf("main-%d", time.Now().Unix()),
			AgentName:      "Main Agent",
			Description:    "Primary interaction agent",
			ProviderConfig: providerConfig,
			Tools:          []string{"*"}, // All tools
			SystemPrompt:   pointer.SystemPrompt,
			MaxTurns:       maxTurns,
			Timeout:        timeout,
			Temperature:    temperature,
		})

	case settings.AliasSteering:
		// Steering agent - quality control/evaluation
		createdAgent, err = sdk.agentFactory.CreateSteering(ctx, agent.SteeringAgentConfig{
			AgentID:        fmt.Sprintf("steering-%d", time.Now().Unix()),
			AgentName:      "Steering Agent",
			Description:    "Quality control and evaluation",
			ProviderConfig: providerConfig,
			SystemPrompt:   pointer.SystemPrompt,
			MaxTurns:       maxTurns,
			Timeout:        timeout,
			Temperature:    temperature,
		})

	case settings.AliasBackground:
		// Background agent - long-running async tasks
		createdAgent, err = sdk.agentFactory.CreateBackground(ctx, agent.BackgroundConfig{
			AgentID:        fmt.Sprintf("background-%d", time.Now().Unix()),
			AgentName:      "Background Agent",
			Description:    "Long-running async operations",
			ProviderConfig: providerConfig,
			Tools:          []string{"*"},
			SystemPrompt:   pointer.SystemPrompt,
			Timeout:        timeout,
			Temperature:    temperature,
		})

	case settings.AliasSubAgent:
		// Sub-agent - specialized quick tasks
		createdAgent, err = sdk.agentFactory.CreateSubAgent(ctx, agent.SubAgentConfig{
			AgentID:        fmt.Sprintf("sub-%d", time.Now().Unix()),
			AgentName:      "Sub-Agent",
			Description:    "Specialized task execution",
			ProviderConfig: providerConfig,
			Tools:          []string{}, // Limited tools (caller can configure)
			SystemPrompt:   pointer.SystemPrompt,
			MaxTurns:       maxTurns,
			Timeout:        timeout,
			Temperature:    temperature,
		})

	case settings.AliasThinking, settings.AliasLongContext:
		// Thinking/Long Context - use worker config with custom settings
		createdAgent, err = sdk.agentFactory.CreateWorker(ctx, agent.WorkerConfig{
			AgentID:        fmt.Sprintf("%s-%d", string(role), time.Now().Unix()),
			AgentName:      fmt.Sprintf("%s Agent", role),
			Description:    fmt.Sprintf("Agent for %s operations", role),
			ProviderConfig: providerConfig,
			Tools:          []string{"*"},
			SystemPrompt:   pointer.SystemPrompt,
			MaxTurns:       maxTurns,
			Timeout:        timeout,
			Temperature:    temperature,
		})

	default:
		return nil, fmt.Errorf("unknown role: %s", role)
	}

	if err != nil {
		sdk.logger.Error(ctx, "profile.agent_creation_failed",
			observability.F("role", string(role)),
			observability.F("error", err.Error()))
		return nil, fmt.Errorf("failed to create %s agent: %w", role, err)
	}

	sdk.logger.Info(ctx, "profile.agent_created",
		observability.F("role", string(role)),
		observability.F("agent_id", createdAgent.Definition().ID))

	return createdAgent, nil
}

// SwitchProfile changes the active profile.
//
// Logic:
// 1. Set new profile as default in ProfileManager
// 2. Reload active profile
// 3. Return error if switch fails
//
// Parameters:
//
//	profileID: ID of profile to activate
//
// Returns:
//
//	error or nil
//
// Side effects:
//   - Changes default profile in config file
//   - Updates sdk.activeProfile
//   - Future CreateAgentForRole calls use new profile
func (sdk *SDKIntegration) SwitchProfile(profileID string) error {
	if sdk.harnessGoverned() {
		return fmt.Errorf("profile switching is disabled while the session is governed by a harness")
	}
	if sdk.profileManager == nil {
		return fmt.Errorf("profile manager not initialized")
	}

	// Reload from disk first so we pick up any role/config changes made by the
	// UI's ProfileSettings manager (a separate in-memory instance). Without this,
	// SetDefaultProfile below would re-write the stale in-memory state to disk,
	// silently discarding the user's edits.
	if err := sdk.profileManager.Reload(); err != nil {
		// Non-fatal: continue with whatever is in memory — better than aborting.
		sdk.logger.Info(sdk.ctx, "profile.switch_reload_failed",
			observability.F("profile_id", profileID),
			observability.F("error", err.Error()),
		)
	}

	// Set as default
	err := sdk.profileManager.SetDefaultProfile(profileID)
	if err != nil {
		return fmt.Errorf("failed to set default profile: %w", err)
	}

	// Reload active profile
	sdk.activeProfile, err = sdk.profileManager.GetActiveProfile()
	if err != nil {
		return fmt.Errorf("failed to reload active profile: %w", err)
	}

	sdk.logger.Info(sdk.ctx, "profile.switched",
		observability.F("profile_id", profileID),
		observability.F("profile_name", sdk.activeProfile.Name))

	// Hot-swap the running agent to match the new profile's main alias. Without
	// this, switching profiles mid-conversation would leave the active agent
	// pinned to the old provider/model until the next CreateAgentForRole, which
	// the user observes as "my profile switch didn't take effect."
	//
	// Provider/model go through SwitchProvider so the full reload pipeline runs
	// (token refresh, context-window update, reasoning config, chain clear).
	// System prompt is pushed via applyUserSystemPromptToAgent after the reload
	// so the profile's prompt wins over whatever ReloadProvider re-applied.
	sdk.applyProfileToRunningAgent(profileID)

	return nil
}

// applyProfileToRunningAgent pushes the active profile's main alias into the
// live agent. Called from SwitchProfile; also safe to call after edits to the
// active profile to hot-apply them.
func (sdk *SDKIntegration) applyProfileToRunningAgent(profileID string) {
	if sdk == nil || sdk.harnessGoverned() || sdk.activeAgent() == nil || sdk.profileManager == nil {
		return
	}
	mainPtr, err := sdk.profileManager.ResolveAlias(settings.AliasMain)
	if err != nil {
		sdk.logger.Info(sdk.ctx, "profile.hotswap_no_main_alias",
			observability.F("profile_id", profileID),
			observability.F("error", err.Error()),
		)
		return
	}

	providerChanged := !strings.EqualFold(strings.TrimSpace(mainPtr.Provider), strings.TrimSpace(sdk.providerName))
	modelChanged := strings.TrimSpace(mainPtr.Model) != "" && mainPtr.Model != sdk.currentModel

	if providerChanged || modelChanged {
		if err := sdk.SwitchProvider(mainPtr.Provider, mainPtr.Model); err != nil {
			sdk.logger.Info(sdk.ctx, "profile.hotswap_switchprovider_failed",
				observability.F("profile_id", profileID),
				observability.F("error", err.Error()),
			)
			// Continue — we still want to try the system-prompt update below,
			// even if the provider rebuild failed.
		}
	}

	// Always clear the agent's chain on profile switch so stale chains from the
	// previous profile don't persist for executeWithChain. ReloadProvider already
	// calls SetChain(nil) when the provider/model changed, but when only the
	// system prompt differs the SwitchProvider call is skipped, leaving the old
	// chain active. Clearing it here is safe: the direct provider path is used
	// and the profile chain can be re-resolved if needed.
	if sdk.activeAgent() != nil {
		sdk.activeAgent().SetChain(nil)
	}

	if strings.TrimSpace(mainPtr.SystemPrompt) != "" {
		sdk.applyUserSystemPromptToAgent(sdk.ctx, mainPtr.SystemPrompt)
	}

	sdk.logger.Info(sdk.ctx, "profile.hotswap_applied",
		observability.F("profile_id", profileID),
		observability.F("provider", mainPtr.Provider),
		observability.F("model", mainPtr.Model),
		observability.F("provider_changed", providerChanged),
		observability.F("model_changed", modelChanged),
	)
}

// GetActiveProfile returns the currently active profile.
//
// Returns:
//
//	(*settings.AgentProfile, error)
//	- Success: (profile, nil)
//	- Failure: (nil, error)
func (sdk *SDKIntegration) GetActiveProfile() (*settings.AgentProfile, error) {
	if sdk.activeProfile == nil {
		return nil, fmt.Errorf("no active profile")
	}
	return sdk.activeProfile, nil
}

// GetProfileManager returns the profile manager for direct access.
//
// Returns:
//
//	*settings.ProfileManager or nil
func (sdk *SDKIntegration) GetProfileManager() *settings.ProfileManager {
	return sdk.profileManager
}

// GetModelForRole returns the provider/model configured for a role in the active profile.
//
// This is a convenience method for getting profile configuration without creating an agent.
//
// Parameters:
//
//	role: The agent role alias
//
// Returns:
//
//	(provider, model, error)
//	- Success: (provider name, model name, nil)
//	- Failure: ("", "", error)
//
// Example:
//
//	provider, model, err := sdk.GetModelForRole(settings.AliasBackground)
//	// Use these values to configure something manually
func (sdk *SDKIntegration) GetModelForRole(role settings.ModelAlias) (string, string, error) {
	if sdk.profileManager == nil || sdk.activeProfile == nil {
		return "", "", fmt.Errorf("profile system not available")
	}

	pointer, err := sdk.profileManager.ResolveAlias(role)
	if err != nil {
		return "", "", err
	}

	return pointer.Provider, pointer.Model, nil
}
