package agent

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ModeFilterConfig contains tool filtering rules for an operating mode.
// This is passed in ExecuteRequest.Context["mode_filter"] to filter tools
// without creating a circular dependency with the mode package.
type ModeFilterConfig struct {
	// ModeName is the name of the mode for logging (e.g., "PLAN", "ACT")
	ModeName string `json:"mode_name"`

	// AllowedTools lists tool name patterns that are allowed.
	// Use "*" to allow all tools. Empty means no tools.
	AllowedTools []string `json:"allowed_tools"`

	// BlockedTools lists tool name patterns that are blocked.
	// BlockedTools takes precedence over AllowedTools.
	BlockedTools []string `json:"blocked_tools"`

	// HideBlockedTools: if true, blocked tools are not exposed to LLM at all.
	HideBlockedTools bool `json:"hide_blocked_tools"`
}

// isToolAllowedByModeFilter checks if a tool is allowed by the mode filter.
func isToolAllowedByModeFilter(toolName string, config *ModeFilterConfig) bool {
	if config == nil {
		return true // No filter = all allowed
	}

	// First check if tool is blocked
	for _, pattern := range config.BlockedTools {
		if matchesModePattern(toolName, pattern) {
			return false
		}
	}

	// Then check if tool is allowed
	if len(config.AllowedTools) == 0 {
		return false // Empty means nothing allowed
	}

	for _, pattern := range config.AllowedTools {
		if pattern == "*" {
			return true
		}
		if matchesModePattern(toolName, pattern) {
			return true
		}
	}

	return false
}

// matchesModePattern checks if a tool name matches a pattern.
// Supports exact match, prefix (*), and suffix (*) patterns.
func matchesModePattern(toolName, pattern string) bool {
	if pattern == toolName {
		return true
	}

	if len(pattern) == 0 {
		return false
	}

	// "*" matches everything
	if pattern == "*" {
		return true
	}

	// Prefix match: "file_*"
	if pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(toolName) >= len(prefix) && toolName[:len(prefix)] == prefix
	}

	// Suffix match: "*_read"
	if pattern[0] == '*' {
		suffix := pattern[1:]
		return len(toolName) >= len(suffix) && toolName[len(toolName)-len(suffix):] == suffix
	}

	return false
}

// getModeFilterFromContext extracts ModeFilterConfig from request context.
func getModeFilterFromContext(ctx map[string]any) *ModeFilterConfig {
	if ctx == nil {
		return nil
	}

	// Check for mode_filter key
	filterVal, ok := ctx["mode_filter"]
	if !ok {
		return nil
	}

	// Handle both direct ModeFilterConfig and map[string]any
	switch v := filterVal.(type) {
	case *ModeFilterConfig:
		return v
	case ModeFilterConfig:
		return &v
	case map[string]any:
		// Parse from map
		config := &ModeFilterConfig{}
		if name, ok := v["mode_name"].(string); ok {
			config.ModeName = name
		}
		if allowed, ok := v["allowed_tools"].([]string); ok {
			config.AllowedTools = allowed
		} else if allowed, ok := v["allowed_tools"].([]any); ok {
			for _, a := range allowed {
				if s, ok := a.(string); ok {
					config.AllowedTools = append(config.AllowedTools, s)
				}
			}
		}
		if blocked, ok := v["blocked_tools"].([]string); ok {
			config.BlockedTools = blocked
		} else if blocked, ok := v["blocked_tools"].([]any); ok {
			for _, b := range blocked {
				if s, ok := b.(string); ok {
					config.BlockedTools = append(config.BlockedTools, s)
				}
			}
		}
		if hide, ok := v["hide_blocked_tools"].(bool); ok {
			config.HideBlockedTools = hide
		}
		return config
	}

	return nil
}

// ProviderTools returns the exact tool list that would ship in the next
// request's Tools array (after mode filtering), for inspection/diagnostics.
// It is a public wrapper over buildProviderTools so callers outside the agent
// package (e.g. the client's system-prompt composition view) can surface the
// real tool payload without duplicating the filter logic.
func (a *Agent) ProviderTools() []provider.Tool {
	return a.buildProviderTools()
}

// ProviderToolsForContext returns the exact provider tool payload for a
// prospective request without mutating the agent's request-scoped state.
func (a *Agent) ProviderToolsForContext(requestContext map[string]any, disableTools bool) []provider.Tool {
	return a.buildProviderToolsFor(requestContext, disableTools)
}

// buildProviderTools converts agent's tool list to provider tools.
// If a ModeFilterConfig is present in requestContext["mode_filter"],
// tools will be filtered according to the mode's allowed/blocked lists.
func (a *Agent) buildProviderTools() []provider.Tool {
	a.mu.RLock()
	requestContext := a.requestContext
	disableTools := a.reqDisableTools
	a.mu.RUnlock()
	return a.buildProviderToolsFor(requestContext, disableTools)
}

func (a *Agent) buildProviderToolsFor(requestContext map[string]any, disableTools bool) []provider.Tool {
	// Per-request tool kill switch (ExecuteRequest.DisableTools): offer no tools
	// to the provider for this turn. The registry is left intact for later turns.
	if disableTools {
		a.logger.Info(a.ctx, "agent.tools_disabled_for_turn")
		return nil
	}

	definition := a.Definition()
	a.logger.Info(a.ctx, "agent.build_provider_tools_start",
		observability.F("tools_in_definition", definition.ToolHints),
		observability.F("has_all_tools", definition.HasAllTools()),
		observability.F("empty_hints_all_tools", len(definition.ToolHints) == 0))

	// Empty ToolHints means "use all tools from the registry" — the same
	// semantics as ToolHints = ["*"]. This allows agents created via
	// NewQuick or New(Config{...}) without explicit ToolHints to use
	// whatever tools are registered on the agent.
	usesAllTools := definition.HasAllTools() || len(definition.ToolHints) == 0

	// Get mode filter config from request context.
	modeFilter := getModeFilterFromContext(requestContext)

	if modeFilter != nil {
		a.logger.Info(a.ctx, "agent.mode_filter_active",
			observability.F("mode_name", modeFilter.ModeName),
			observability.F("allowed_count", len(modeFilter.AllowedTools)),
			observability.F("blocked_count", len(modeFilter.BlockedTools)),
			observability.F("hide_blocked", modeFilter.HideBlockedTools))
	}

	// Get tools from registry
	var providerTools []provider.Tool
	var blockedTools []string
	allToolsInRegistry := a.toolReg.List()
	a.logger.Debug(a.ctx, "agent.registry_tools",
		observability.F("tool_names", allToolsInRegistry),
		observability.F("count", len(allToolsInRegistry)))

	if usesAllTools {
		// Agent can use all tools (subject to mode filtering)
		a.logger.Info(a.ctx, "agent.using_all_tools")
		for _, toolName := range allToolsInRegistry {
			// Check mode filter
			if modeFilter != nil && !isToolAllowedByModeFilter(toolName, modeFilter) {
				if modeFilter.HideBlockedTools {
					// Don't expose blocked tool at all
					blockedTools = append(blockedTools, toolName)
					continue
				}
				// Tool is blocked but visible (will error at execution)
				// For now, we still add it to let the LLM see it
			}

			tool, err := a.toolReg.Get(toolName)
			if err != nil {
				a.logger.Warn(a.ctx, "agent.tool_get_failed",
					observability.F("tool", toolName),
					observability.F("error", err.Error()))
				continue
			}
			providerTool := a.convertToProviderTool(tool)
			a.logger.Debug(a.ctx, "agent.tool_converted",
				observability.F("tool_name", providerTool.Name),
				observability.F("tool_type", providerTool.Type))
			providerTools = append(providerTools, providerTool)
		}
	} else {
		// Agent can use specific tools (subject to mode filtering)
		a.logger.Info(a.ctx, "agent.using_specific_tools",
			observability.F("tools", definition.ToolHints))
		for _, toolName := range definition.ToolHints {
			// Check mode filter
			if modeFilter != nil && !isToolAllowedByModeFilter(toolName, modeFilter) {
				if modeFilter.HideBlockedTools {
					blockedTools = append(blockedTools, toolName)
					continue
				}
			}

			tool, err := a.toolReg.Get(toolName)
			if err != nil {
				a.logger.Warn(a.ctx, "agent.tool_not_found",
					observability.F("tool", toolName),
					observability.F("agent_id", definition.ID))
				continue
			}
			providerTools = append(providerTools, a.convertToProviderTool(tool))
		}
	}

	// Log blocked tools for debugging
	if len(blockedTools) > 0 {
		modeName := ""
		if modeFilter != nil {
			modeName = modeFilter.ModeName
		}
		a.logger.Info(a.ctx, "agent.tools_blocked_by_mode",
			observability.F("mode", modeName),
			observability.F("blocked_tools", blockedTools),
			observability.F("blocked_count", len(blockedTools)))
	}

	a.logger.Info(a.ctx, "agent.build_provider_tools_complete",
		observability.F("provider_tools_count", len(providerTools)))

	// Apply deferred tool filter if set (advanced tool mode).
	// This strips deferred tools from the list, reducing context tokens.
	a.mu.RLock()
	filter := a.toolFilter
	a.mu.RUnlock()

	if filter != nil {
		beforeCount := len(providerTools)
		providerTools = filter(providerTools)
		a.logger.Info(a.ctx, "agent.tool_filter_applied",
			observability.F("before_count", beforeCount),
			observability.F("after_count", len(providerTools)),
			observability.F("deferred_count", beforeCount-len(providerTools)))
	}

	return providerTools
}

// convertToProviderTool converts a tools.Tool to provider.Tool format.
func (a *Agent) convertToProviderTool(tool tools.Tool) provider.Tool {
	return provider.Tool{
		Name:        tool.Name(),
		Description: tool.Description(),
		Parameters:  tool.Parameters(),
		Type:        "function", // Default to function type
		Metadata:    make(map[string]any),
	}
}
