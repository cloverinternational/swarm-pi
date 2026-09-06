// Package configbundle provides a unified configuration system for Swarm.
// It consolidates all configuration aspects (system, prompts, tools, agents, hooks,
// skills, profiles, providers, credentials, MCP servers) into a single config file
// with support for hierarchical layering (global + project).
package configbundle

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

// SchemaVersion is the current version of the config bundle schema.
// Increment this when making breaking changes to enable migration.
const SchemaVersion = 1

// ConfigBundle is the unified configuration structure stored at:
// - Global: ~/.swarm/config.json (or as a bundle file)
// - Project: .swarm/config.json
type ConfigBundle struct {
	// SchemaVersion enables forward/backward migration
	SchemaVersion int `json:"schemaVersion"`

	// Metadata
	Name        string    `json:"name,omitempty"`        // Display name for this config
	Description string    `json:"description,omitempty"` // Human-readable description
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`

	// Source tracking (not serialized to disk, set at runtime)
	Source ConfigSource `json:"-"` // "global" or "project"
	Path   string       `json:"-"` // File path where this bundle was loaded from

	// === Core Settings ===

	// System contains system-level settings (display, behavior, etc.)
	// This merges with global config when at project level.
	System SystemConfig `json:"system"`

	// === Agent Configuration ===

	// Agents contains agent definitions and their configurations.
	// Project-level agents can override or extend global agents.
	Agents AgentsConfig `json:"agents"`

	// Profiles contains agent profile configurations.
	// Can reference external files or be embedded inline.
	Profiles ProfilesConfig `json:"profiles"`

	// === Prompts and Tools ===

	// Prompts contains system prompt configurations.
	// Project prompts can override global prompts.
	Prompts PromptsConfig `json:"prompts"`

	// Tools contains tool configurations and permissions.
	// Project tools can extend or override global tools.
	Tools ToolsConfig `json:"tools"`

	// === Extensions ===

	// Hooks contains hook configurations.
	// Project hooks extend global hooks (both are active).
	Hooks HooksConfig `json:"hooks"`

	// Skills contains skill configurations.
	// Project skills extend global skills.
	Skills SkillsConfig `json:"skills"`

	// === Providers and Credentials ===

	// Providers contains provider definitions and model aliases.
	// Project providers can add to or override global providers.
	Providers ProvidersConfig `json:"providers"`

	// Credentials contains API keys and authentication data.
	// SECURITY: Project credentials ADD to global, never replace.
	// This prevents accidental exposure of global secrets.
	Credentials CredentialsConfig `json:"credentials"`

	// === MCP Servers ===

	// MCPServers contains MCP server configurations.
	// Project servers extend global servers.
	MCPServers MCPServersConfig `json:"mcpServers"`

	// === Context and Environment ===

	// ContextSources contains context source configurations.
	// Project sources extend global sources.
	ContextSources ContextSourcesConfig `json:"contextSources"`

	// Environments contains environment-specific configurations.
	Environments EnvironmentsConfig `json:"environments"`

	// === Merge Policy ===

	// MergePolicy defines how this config merges with its parent.
	// Only applies to project-level configs.
	MergePolicy *MergePolicy `json:"mergePolicy,omitempty"`
}

// ConfigSource indicates where a config bundle originated.
type ConfigSource string

const (
	SourceGlobal  ConfigSource = "global"  // ~/.swarm/
	SourceProject ConfigSource = "project" // .swarm/
)

// SystemConfig contains system-level settings.
// Fields here override corresponding fields in global config.
type SystemConfig struct {
	// Provider/Model Selection
	DefaultProvider string `json:"defaultProvider,omitempty"`
	DefaultModel    string `json:"defaultModel,omitempty"`
	CurrentProvider string `json:"currentProvider,omitempty"`
	CurrentModel    string `json:"currentModel,omitempty"`
	DefaultMode     string `json:"defaultMode,omitempty"` // "plan", "act", "auto"
	DefaultAgent    string `json:"defaultAgent,omitempty"`

	// Display Settings
	Theme                       string   `json:"theme,omitempty"` // "dark", "light", "auto"
	ShowThinking                *bool    `json:"showThinking,omitempty"`
	ShowTokenCount              *bool    `json:"showTokenCount,omitempty"`
	ShowToolOutput              *bool    `json:"showToolOutput,omitempty"`
	CompactMode                 *bool    `json:"compactMode,omitempty"`
	MaxOutputLines              int      `json:"maxOutputLines,omitempty"`
	SyntaxHighlighting          *bool    `json:"syntaxHighlighting,omitempty"`
	MaxConcurrentTools          int      `json:"maxConcurrentTools,omitempty"`
	ToolTimeout                 int      `json:"toolTimeout,omitempty"`
	EnableSandbox               *bool    `json:"enableSandbox,omitempty"`
	SandboxPaths                []string `json:"sandboxPaths,omitempty"`
	EnableCodeMode              *bool    `json:"enableCodeMode,omitempty"`
	CompletionConfirm           *bool    `json:"completion_confirm,omitempty"`
	CompletionConfirmMax        *int     `json:"completion_confirm_max,omitempty"`
	ProactiveSummarizeThreshold *float64 `json:"proactive_summarize_threshold,omitempty"`
	MemoryBackend               string   `json:"memoryBackend,omitempty"`

	// Editor Settings
	Editor            string `json:"editor,omitempty"`
	EditorArgs        string `json:"editorArgs,omitempty"`
	ExternalEditorCmd string `json:"externalEditorCmd,omitempty"`

	// Behavior Settings
	AutoSaveConversations *bool  `json:"autoSaveConversations,omitempty"`
	ConfirmBeforeExit     *bool  `json:"confirmBeforeExit,omitempty"`
	EnableLogging         *bool  `json:"enableLogging,omitempty"`
	LogLevel              string `json:"logLevel,omitempty"`

	// Compaction Settings
	EnableCompaction       *bool `json:"enableCompaction,omitempty"`
	CompactionThreshold    int   `json:"compactionThreshold,omitempty"`
	WarningThreshold       int   `json:"warningThreshold,omitempty"`
	PreserveRecentMessages int   `json:"preserveRecentMessages,omitempty"`

	// Micro-compaction Settings
	EnableMicroCompaction *bool `json:"enableMicroCompaction,omitempty"`
	MicroRetentionCount   int   `json:"microRetentionCount,omitempty"`

	// Cache Settings
	EnableCache     *bool  `json:"enableCache,omitempty"`
	CacheDir        string `json:"cacheDir,omitempty"`
	CacheMaxSizeMB  int    `json:"cacheMaxSizeMB,omitempty"`
	CacheExpiryDays int    `json:"cacheExpiryDays,omitempty"`

	// Cloud Sync Settings
	SyncConversations  *bool  `json:"syncConversations,omitempty"`
	SyncSettings       *bool  `json:"syncSettings,omitempty"`
	SyncSettingsScope  string `json:"syncSettingsScope,omitempty"`
	SyncSettingsTeamID string `json:"syncSettingsTeamId,omitempty"`
	SyncProfiles       *bool  `json:"syncProfiles,omitempty"`
	EncryptCloudData   *bool  `json:"encryptCloudData,omitempty"`
	AnonymousAnalytics *bool  `json:"anonymousAnalytics,omitempty"`

	// Advanced Features
	HybridConfig   *core.HybridConfig    `json:"hybridConfig,omitempty"`
	WebSearch      *core.WebSearchConfig `json:"webSearch,omitempty"`
	SteeringConfig *core.SteeringConfig  `json:"steeringConfig,omitempty"`

	// Voice Settings
	EnableVoice   *bool  `json:"enableVoice,omitempty"`
	VoiceProvider string `json:"voiceProvider,omitempty"`

	// Custom Settings (for extensions)
	Custom map[string]any `json:"custom,omitempty"`
}

// AgentsConfig contains agent definitions.
type AgentsConfig struct {
	// DefaultAgent is the agent to use when none specified.
	DefaultAgent string `json:"defaultAgent,omitempty"`

	// Definitions contains custom agent definitions.
	// Project definitions can override global ones with same ID.
	Definitions []AgentDefinition `json:"definitions,omitempty"`

	// Mode determines how this config interacts with parent.
	// - "merge": Combine with parent definitions (default)
	// - "override": Replace parent definitions entirely
	Mode MergeMode `json:"mode,omitempty"`
}

// AgentDefinition defines a custom agent.
type AgentDefinition struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	SystemPrompt string            `json:"systemPrompt,omitempty"`
	ModelAlias   string            `json:"modelAlias,omitempty"`
	Tools        []string          `json:"tools,omitempty"`
	Capabilities []string          `json:"capabilities,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ProfilesConfig contains agent profile configurations.
type ProfilesConfig struct {
	// Mode determines how profiles are loaded:
	// - "reference": Use external profile file (legacy mode)
	// - "inline": Profiles are embedded in this bundle
	// - "merge": Combine inline profiles with referenced file
	Mode ProfileMode `json:"mode,omitempty"`

	// ReferencePath is the path to external profile file.
	// Used when Mode is "reference" or "merge".
	ReferencePath string `json:"referencePath,omitempty"`

	// Inline contains profiles embedded directly in this bundle.
	// Used when Mode is "inline" or "merge".
	Inline []ProfileDefinition `json:"inline,omitempty"`
}

// ProfileMode determines how profiles are loaded.
type ProfileMode string

const (
	ProfileModeReference ProfileMode = "reference"
	ProfileModeInline    ProfileMode = "inline"
	ProfileModeMerge     ProfileMode = "merge"
)

// ProfileDefinition defines an agent profile.
type ProfileDefinition struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Model          string            `json:"model,omitempty"`
	ModelAlias     string            `json:"modelAlias,omitempty"`
	SystemPrompt   string            `json:"systemPrompt,omitempty"`
	Temperature    *float64          `json:"temperature,omitempty"`
	MaxTokens      int               `json:"maxTokens,omitempty"`
	TopP           *float64          `json:"topP,omitempty"`
	Tools          []string          `json:"tools,omitempty"`
	DisabledTools  []string          `json:"disabledTools,omitempty"`
	EnableVision   *bool             `json:"enableVision,omitempty"`
	EnableVoice    *bool             `json:"enableVoice,omitempty"`
	VoiceProvider  string            `json:"voiceProvider,omitempty"`
	VoiceModel     string            `json:"voiceModel,omitempty"`
	VoiceStability string            `json:"voiceStability,omitempty"`
	VoiceSpeed     *float64          `json:"voiceSpeed,omitempty"`
	Capabilities   []string          `json:"capabilities,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

// PromptsConfig contains system prompt configurations.
type PromptsConfig struct {
	// Custom contains custom prompt templates by ID.
	Custom map[string]string `json:"custom,omitempty"`

	// Overrides contains overrides for built-in prompts.
	Overrides map[string]string `json:"overrides,omitempty"`

	// Mode determines how prompts interact with parent config.
	Mode MergeMode `json:"mode,omitempty"`
}

// ToolsConfig contains tool configurations.
type ToolsConfig struct {
	// Enabled tools list (whitelist).
	// If non-empty, only these tools are available.
	Enabled []string `json:"enabled,omitempty"`

	// Disabled tools list (blacklist).
	// These tools are always disabled regardless of Enabled list.
	Disabled []string `json:"disabled,omitempty"`

	// Custom contains custom tool definitions.
	Custom []ToolDefinition `json:"custom,omitempty"`

	// Overrides contains tool-specific configurations.
	Overrides map[string]ToolOverride `json:"overrides,omitempty"`

	// PermissionPolicy contains tool permission settings.
	PermissionPolicy *core.PermissionConfig `json:"permissionPolicy,omitempty"`

	// Mode determines how this config interacts with parent.
	Mode MergeMode `json:"mode,omitempty"`
}

// ToolDefinition defines a custom tool.
type ToolDefinition struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"` // "function", "script", "mcp"
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Schema      map[string]any    `json:"schema,omitempty"`
}

// ToolOverride contains tool-specific configuration overrides.
type ToolOverride struct {
	Enabled     *bool             `json:"enabled,omitempty"`
	Timeout     *int              `json:"timeout,omitempty"`
	MaxRetries  *int              `json:"maxRetries,omitempty"`
	AutoApprove *bool             `json:"autoApprove,omitempty"`
	PromptUser  *bool             `json:"promptUser,omitempty"`
	Params      map[string]string `json:"params,omitempty"`
}

// HooksConfig contains hook configurations.
type HooksConfig struct {
	// Definitions contains hook definitions.
	Definitions []HookDefinition `json:"definitions,omitempty"`

	// Disabled contains IDs of disabled hooks.
	Disabled []string `json:"disabled,omitempty"`

	// Mode determines how this config interacts with parent.
	// Hooks default to "merge" mode - both global and project hooks run.
	Mode MergeMode `json:"mode,omitempty"`
}

// HookDefinition defines a hook.
type HookDefinition struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        string            `json:"type"` // "script", "command", "http"
	Event       string            `json:"event"`
	Command     string            `json:"command,omitempty"`
	Script      string            `json:"script,omitempty"`
	URL         string            `json:"url,omitempty"`
	Enabled     *bool             `json:"enabled,omitempty"`
	Priority    int               `json:"priority,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

// SkillsConfig contains skill configurations.
type SkillsConfig struct {
	// Installed contains installed skill references.
	Installed []SkillReference `json:"installed,omitempty"`

	// SearchPath contains directories to search for skills.
	SearchPath []string `json:"searchPath,omitempty"`

	// Mode determines how this config interacts with parent.
	Mode MergeMode `json:"mode,omitempty"`
}

// SkillReference references an installed skill.
type SkillReference struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
	Source  string `json:"source,omitempty"` // "global", "project", "builtin"
}

// ProvidersConfig contains provider definitions.
type ProvidersConfig struct {
	// Mode determines how providers are loaded:
	// - "reference": Use external provider file (legacy mode)
	// - "inline": Providers are embedded in this bundle
	// - "merge": Combine inline providers with referenced file
	Mode ProviderMode `json:"mode,omitempty"`

	// ReferencePath is the path to external provider file.
	// Used when Mode is "reference" or "merge".
	ReferencePath string `json:"referencePath,omitempty"`

	// Inline contains provider definitions embedded in this bundle.
	Inline []ProviderDefinition `json:"inline,omitempty"`
}

// ProviderMode determines how providers are loaded.
type ProviderMode string

const (
	ProviderModeReference ProviderMode = "reference"
	ProviderModeInline    ProviderMode = "inline"
	ProviderModeMerge     ProviderMode = "merge"
)

// ProviderDefinition defines a provider.
type ProviderDefinition struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"` // "anthropic", "openai", "custom", etc.
	Enabled      *bool             `json:"enabled,omitempty"`
	BaseURL      string            `json:"baseUrl,omitempty"`
	APIVersion   string            `json:"apiVersion,omitempty"`
	Models       []ModelDefinition `json:"models,omitempty"`
	ModelAliases map[string]string `json:"modelAliases,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Config       map[string]any    `json:"config,omitempty"`
}

// ModelDefinition defines a model.
type ModelDefinition struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Type              string   `json:"type,omitempty"` // "chat", "completion", "embedding"
	ContextWindow     int      `json:"contextWindow,omitempty"`
	MaxOutputTokens   int      `json:"maxOutputTokens,omitempty"`
	SupportsVision    *bool    `json:"supportsVision,omitempty"`
	SupportsVoice     *bool    `json:"supportsVoice,omitempty"`
	SupportsStreaming *bool    `json:"supportsStreaming,omitempty"`
	InputPrice        float64  `json:"inputPrice,omitempty"`  // Per 1M tokens
	OutputPrice       float64  `json:"outputPrice,omitempty"` // Per 1M tokens
	Aliases           []string `json:"aliases,omitempty"`
}

// CredentialsConfig contains API keys and authentication data.
// SECURITY NOTE: Project-level credentials ADD to global credentials,
// they NEVER replace them. This prevents accidental exposure of secrets.
type CredentialsConfig struct {
	// ProviderKeys contains API keys indexed by provider ID.
	// Project keys are ADDED to global keys (merge, not replace).
	ProviderKeys map[string]string `json:"providerKeys,omitempty"`

	// OAuthTokens contains OAuth tokens indexed by provider ID.
	OAuthTokens map[string]string `json:"oauthTokens,omitempty"`

	// MCPAuth contains MCP server authentication data.
	MCPAuth map[string]MCPAuthConfig `json:"mcpAuth,omitempty"`

	// Inherit determines whether to inherit credentials from parent.
	// Always true for project-level configs (cannot be disabled).
	Inherit bool `json:"inherit"`
}

// MCPAuthConfig contains MCP server authentication.
type MCPAuthConfig struct {
	Type    string            `json:"type"` // "bearer", "basic", "api_key"
	Token   string            `json:"token,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPServersConfig contains MCP server configurations.
type MCPServersConfig struct {
	// Servers contains MCP server configurations.
	Servers []MCPServerDefinition `json:"servers,omitempty"`

	// Disabled contains IDs of disabled servers.
	Disabled []string `json:"disabled,omitempty"`

	// Mode determines how this config interacts with parent.
	Mode MergeMode `json:"mode,omitempty"`
}

// MCPServerDefinition defines an MCP server.
type MCPServerDefinition struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"` // "stdio", "sse", "http", "oauth"
	Enabled      *bool             `json:"enabled,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	URL          string            `json:"url,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	WorkDir      string            `json:"workDir,omitempty"`
	Timeout      int               `json:"timeout,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Tools        []string          `json:"tools,omitempty"`        // Tool allowlist
	ExcludeTools []string          `json:"excludeTools,omitempty"` // Tool blocklist
}

// ContextSourcesConfig contains context source configurations.
type ContextSourcesConfig struct {
	// Sources contains context source definitions.
	Sources []ContextSourceDefinition `json:"sources,omitempty"`

	// Mode determines how this config interacts with parent.
	Mode MergeMode `json:"mode,omitempty"`
}

// ContextSourceDefinition defines a context source.
type ContextSourceDefinition struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Type    string         `json:"type"` // "rag", "file", "api", "custom"
	Enabled *bool          `json:"enabled,omitempty"`
	Path    string         `json:"path,omitempty"`
	URL     string         `json:"url,omitempty"`
	Config  map[string]any `json:"config,omitempty"`
}

// EnvironmentsConfig contains environment-specific configurations.
type EnvironmentsConfig struct {
	// Current is the active environment.
	Current string `json:"current,omitempty"`

	// Definitions contains environment definitions.
	Definitions []EnvironmentDefinition `json:"definitions,omitempty"`
}

// EnvironmentDefinition defines an environment.
type EnvironmentDefinition struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Inherits    []string          `json:"inherits,omitempty"`
	System      *SystemConfig     `json:"system,omitempty"`
	Providers   *ProvidersConfig  `json:"providers,omitempty"`
	Agents      *AgentsConfig     `json:"agents,omitempty"`
	Profiles    *ProfilesConfig   `json:"profiles,omitempty"`
	Tools       *ToolsConfig      `json:"tools,omitempty"`
	MCPServers  *MCPServersConfig `json:"mcpServers,omitempty"`
}

// MergePolicy defines how a project config merges with global config.
type MergePolicy struct {
	// Per-section merge modes.
	// If not specified, defaults to:
	// - System: MergeModeOverride
	// - Agents: MergeModeMerge
	// - Profiles: MergeModeMerge
	// - Prompts: MergeModeMerge
	// - Tools: MergeModeMerge
	// - Hooks: MergeModeAppend
	// - Skills: MergeModeAppend
	// - Providers: MergeModeMerge
	// - Credentials: MergeModeAppend (always, cannot change)
	// - MCPServers: MergeModeAppend
	// - ContextSources: MergeModeAppend
	// - Environments: MergeModeMerge
	System         MergeMode `json:"system,omitempty"`
	Agents         MergeMode `json:"agents,omitempty"`
	Profiles       MergeMode `json:"profiles,omitempty"`
	Prompts        MergeMode `json:"prompts,omitempty"`
	Tools          MergeMode `json:"tools,omitempty"`
	Hooks          MergeMode `json:"hooks,omitempty"`
	Skills         MergeMode `json:"skills,omitempty"`
	Providers      MergeMode `json:"providers,omitempty"`
	Credentials    MergeMode `json:"credentials,omitempty"` // Always "append"
	MCPServers     MergeMode `json:"mcpServers,omitempty"`
	ContextSources MergeMode `json:"contextSources,omitempty"`
	Environments   MergeMode `json:"environments,omitempty"`
}

// MergeMode determines how a section merges with its parent.
type MergeMode string

const (
	// MergeModeOverride replaces the parent section entirely.
	MergeModeOverride MergeMode = "override"

	// MergeModeMerge combines child with parent, child values override.
	MergeModeMerge MergeMode = "merge"

	// MergeModeAppend adds child items to parent list.
	MergeModeAppend MergeMode = "append"
)

// DefaultMergePolicy returns the default merge policy for project configs.
func DefaultMergePolicy() *MergePolicy {
	return &MergePolicy{
		System:         MergeModeMerge, // Merge individual fields
		Agents:         MergeModeMerge,
		Profiles:       MergeModeMerge,
		Prompts:        MergeModeMerge,
		Tools:          MergeModeMerge,
		Hooks:          MergeModeAppend,
		Skills:         MergeModeAppend,
		Providers:      MergeModeMerge,
		Credentials:    MergeModeAppend, // Always append, never replace
		MCPServers:     MergeModeAppend,
		ContextSources: MergeModeAppend,
		Environments:   MergeModeMerge,
	}
}
