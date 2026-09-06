package context

import "encoding/json"

type legacyContextConfig struct {
	RefreshMode       RefreshMode        `json:"refresh_mode"`
	RefreshTTLSeconds int                `json:"refresh_ttl_seconds"`
	CurrentDate       bool               `json:"current_date"`
	MCPSources        []MCPContextSource `json:"mcp_sources,omitempty"`

	GlobalClaudeMd bool `json:"global_claude_md"`
	GlobalSwarmMd  bool `json:"global_swarm_md"`

	ProjectClaudeMd bool `json:"project_claude_md"`
	ProjectSwarmMd  bool `json:"project_swarm_md"`
	AgentsMd        bool `json:"agents_md"`
	IndexMd         bool `json:"index_md"`

	GitStatus   bool `json:"git_status"`
	ProjectName bool `json:"project_name"`
}

func defaultLegacyConfig() legacyContextConfig {
	return legacyContextConfig{
		RefreshMode:       RefreshEveryMessage,
		RefreshTTLSeconds: 0,
		CurrentDate:       true,
		GlobalClaudeMd:    false,
		GlobalSwarmMd:     false,
		ProjectClaudeMd:   true,
		ProjectSwarmMd:    true,
		AgentsMd:          true,
		IndexMd:           true,
		GitStatus:         true,
		ProjectName:       true,
	}
}

func isValidLegacyRefreshMode(mode RefreshMode) bool {
	switch mode {
	case RefreshEveryMessage, RefreshEveryTurn, RefreshTTL:
		return true
	default:
		return false
	}
}

func applyLegacyDefaults(config legacyContextConfig, raw map[string]json.RawMessage) legacyContextConfig {
	defaults := defaultLegacyConfig()

	if _, ok := raw["refresh_mode"]; !ok || config.RefreshMode == "" || !isValidLegacyRefreshMode(config.RefreshMode) {
		config.RefreshMode = defaults.RefreshMode
	}
	if _, ok := raw["refresh_ttl_seconds"]; !ok {
		config.RefreshTTLSeconds = defaults.RefreshTTLSeconds
	}
	if _, ok := raw["current_date"]; !ok {
		config.CurrentDate = defaults.CurrentDate
	}
	if _, ok := raw["mcp_sources"]; !ok {
		config.MCPSources = defaults.MCPSources
	}
	config.MCPSources = NormalizeMCPSources(config.MCPSources)

	if _, ok := raw["global_claude_md"]; !ok {
		config.GlobalClaudeMd = defaults.GlobalClaudeMd
	}
	if _, ok := raw["global_swarm_md"]; !ok {
		config.GlobalSwarmMd = defaults.GlobalSwarmMd
	}
	if _, ok := raw["project_claude_md"]; !ok {
		config.ProjectClaudeMd = defaults.ProjectClaudeMd
	}
	if _, ok := raw["project_swarm_md"]; !ok {
		config.ProjectSwarmMd = defaults.ProjectSwarmMd
	}
	if _, ok := raw["agents_md"]; !ok {
		config.AgentsMd = defaults.AgentsMd
	}
	if _, ok := raw["index_md"]; !ok {
		config.IndexMd = defaults.IndexMd
	}
	if _, ok := raw["git_status"]; !ok {
		config.GitStatus = defaults.GitStatus
	}
	if _, ok := raw["project_name"]; !ok {
		config.ProjectName = defaults.ProjectName
	}

	return config
}

func migrateLegacyConfig(legacy legacyContextConfig) ContextConfig {
	config := ContextConfig{
		Defaults: ContextDefaults{
			RefreshMode: legacy.RefreshMode,
		},
	}
	ttlSeconds := legacy.RefreshTTLSeconds
	config.Defaults.TTLSeconds = &ttlSeconds

	sources := make([]ContextSourceConfig, 0, len(builtinSources)+len(legacy.MCPSources))
	for _, spec := range builtinSources {
		source := defaultSourceConfig(spec)
		source.Enabled = legacyEnabledForID(legacy, spec.ID)
		if spec.Kind == SourceKindFile {
			source.RefreshMode = RefreshOnChange
		} else {
			source.RefreshMode = RefreshInherit
		}
		sources = append(sources, source)
	}

	for _, mcpSource := range NormalizeMCPSources(legacy.MCPSources) {
		sources = append(sources, ConfigSourceFromMCP(mcpSource))
	}

	config.Sources = sources
	return config
}

func legacyEnabledForID(legacy legacyContextConfig, id string) bool {
	switch id {
	case SourceIDGlobalClaudeMd:
		return legacy.GlobalClaudeMd
	case SourceIDGlobalSwarmMd:
		return legacy.GlobalSwarmMd
	case SourceIDProjectClaudeMd:
		return legacy.ProjectClaudeMd
	case SourceIDProjectSwarmMd:
		return legacy.ProjectSwarmMd
	case SourceIDAgentsMd:
		return legacy.AgentsMd
	case SourceIDIndexMd:
		return legacy.IndexMd
	case SourceIDGitStatus:
		return legacy.GitStatus
	case SourceIDProjectName:
		return legacy.ProjectName
	case SourceIDCurrentDate:
		return legacy.CurrentDate
	default:
		return true
	}
}
