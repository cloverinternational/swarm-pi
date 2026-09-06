package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// RefreshMode defines how often dynamic context sources should refresh.
type RefreshMode string

const (
	RefreshInherit      RefreshMode = "inherit"
	RefreshOnChange     RefreshMode = "on_change"
	RefreshEveryMessage RefreshMode = "every_message"
	RefreshEveryTurn    RefreshMode = "every_turn"
	RefreshTTL          RefreshMode = "ttl"
)

// CachePolicy controls prompt caching behavior for a source.
type CachePolicy string

const (
	CacheInherit   CachePolicy = "inherit"
	CacheCached    CachePolicy = "cached"
	CacheEphemeral CachePolicy = "ephemeral"
)

// ContextSourceKind defines the type of source used for configuration.
type ContextSourceKind string

const (
	SourceKindFile        ContextSourceKind = "file"
	SourceKindDynamic     ContextSourceKind = "dynamic"
	SourceKindMCPResource ContextSourceKind = "mcp_resource"
	SourceKindMCPPrompt   ContextSourceKind = "mcp_prompt"
	// SourceKindInjection marks prompt contributors that don't render into the
	// <swarmos_context> block. The orchestrator skips them; their Enabled flag
	// gates a separate injection mechanism (skills XML, workspace env block,
	// dream contract, hook context) at its own injection site.
	SourceKindInjection ContextSourceKind = "injection"
)

// Stable IDs for built-in sources.
const (
	SourceIDGlobalClaudeMd  = "global_claude_md"
	SourceIDGlobalSwarmMd   = "global_swarm_md"
	SourceIDProjectClaudeMd = "project_claude_md"
	SourceIDProjectSwarmMd  = "project_swarm_md"
	SourceIDAgentsMd        = "agents_md"
	SourceIDIndexMd         = "index_md"
	SourceIDGitStatus       = "git_status"
	SourceIDProjectName     = "project_name"
	SourceIDCurrentDate     = "current_date"

	// Injection-kind sources (gate prompt contributors outside the context block).
	SourceIDSkills       = "skills"
	SourceIDWorkspaceEnv = "workspace_env"
	SourceIDHookContext  = "hook_context"
)

// MCPSourceType defines the kind of MCP context source.
type MCPSourceType string

const (
	MCPSourceResource MCPSourceType = "resource"
	MCPSourcePrompt   MCPSourceType = "prompt"
)

// MCPContextSource defines a single MCP-backed context source.
// Fields are best-effort and may be omitted depending on the source type.
type MCPContextSource struct {
	ID         string            `json:"id"`
	Enabled    bool              `json:"enabled"`
	Kind       MCPSourceType     `json:"kind"`
	ServerName string            `json:"server_name"`
	URI        string            `json:"uri,omitempty"`
	PromptName string            `json:"prompt_name,omitempty"`
	PromptArgs map[string]string `json:"prompt_args,omitempty"`
	Label      string            `json:"label,omitempty"`
}

// UnmarshalJSON supports legacy MCP source fields while normalizing values.
func (m *MCPContextSource) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	getString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := raw[key]; ok {
				var out string
				if err := json.Unmarshal(value, &out); err == nil {
					return out
				}
			}
		}
		return ""
	}

	var out MCPContextSource
	out.ID = getString("id")
	out.Label = getString("label", "name")
	out.ServerName = getString("server_name", "server")
	out.URI = getString("uri")
	out.PromptName = getString("prompt_name", "prompt")

	kind := getString("kind", "type")
	if kind != "" {
		out.Kind = MCPSourceType(kind)
	}

	if value, ok := raw["prompt_args"]; ok {
		_ = json.Unmarshal(value, &out.PromptArgs)
	} else if value, ok := raw["args"]; ok {
		_ = json.Unmarshal(value, &out.PromptArgs)
	}

	enabledSet := false
	if value, ok := raw["enabled"]; ok {
		enabledSet = true
		_ = json.Unmarshal(value, &out.Enabled)
	}
	if !enabledSet {
		out.Enabled = true
	}

	*m = NormalizeMCPSource(out)
	return nil
}

// NormalizeMCPSource fills in derived fields and ensures stable IDs.
func NormalizeMCPSource(source MCPContextSource) MCPContextSource {
	if source.Kind == "" {
		if source.PromptName != "" {
			source.Kind = MCPSourcePrompt
		} else if source.URI != "" {
			source.Kind = MCPSourceResource
		}
	}

	if source.ID == "" {
		source.ID = deriveMCPSourceID(source)
	}

	return source
}

// NormalizeMCPSources applies normalization to each MCP source entry.
func NormalizeMCPSources(sources []MCPContextSource) []MCPContextSource {
	if len(sources) == 0 {
		return sources
	}

	normalized := make([]MCPContextSource, len(sources))
	for i, source := range sources {
		normalized[i] = NormalizeMCPSource(source)
	}
	return normalized
}

func deriveMCPSourceID(source MCPContextSource) string {
	var seed strings.Builder
	seed.WriteString(string(source.Kind))
	seed.WriteString("|")
	seed.WriteString(source.ServerName)
	seed.WriteString("|")
	seed.WriteString(source.URI)
	seed.WriteString("|")
	seed.WriteString(source.PromptName)
	seed.WriteString("|")

	if len(source.PromptArgs) > 0 {
		keys := make([]string, 0, len(source.PromptArgs))
		for key := range source.PromptArgs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for i, key := range keys {
			if i > 0 {
				seed.WriteString("&")
			}
			seed.WriteString(key)
			seed.WriteString("=")
			seed.WriteString(source.PromptArgs[key])
		}
	}

	sum := sha256.Sum256([]byte(seed.String()))
	return "mcp_" + hex.EncodeToString(sum[:12])
}

// ContextDefaults stores shared defaults for sources.
type ContextDefaults struct {
	RefreshMode    RefreshMode `json:"refresh_mode,omitempty"`
	TTLSeconds     *int        `json:"ttl_seconds,omitempty"`
	CachePolicy    CachePolicy `json:"cache_policy,omitempty"`
	MCPTimeoutMS   *int        `json:"mcp_timeout_ms,omitempty"`
	MCPConcurrency *int        `json:"mcp_concurrency,omitempty"`
}

// ContextSourceConfig defines configuration for a single source.
type ContextSourceConfig struct {
	ID          string            `json:"id"`
	Label       string            `json:"label,omitempty"`
	Kind        ContextSourceKind `json:"kind,omitempty"`
	Enabled     bool              `json:"enabled"`
	RefreshMode RefreshMode       `json:"refresh_mode,omitempty"`
	TTLSeconds  *int              `json:"ttl_seconds,omitempty"`
	CachePolicy CachePolicy       `json:"cache_policy,omitempty"`

	ServerName string            `json:"server_name,omitempty"`
	URI        string            `json:"uri,omitempty"`
	PromptName string            `json:"prompt_name,omitempty"`
	PromptArgs map[string]string `json:"prompt_args,omitempty"`
}

// UnmarshalJSON defaults missing flags and normalizes fields.
func (c *ContextSourceConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	type alias ContextSourceConfig
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}

	if out.Label == "" {
		if value, ok := raw["name"]; ok {
			var name string
			if err := json.Unmarshal(value, &name); err == nil {
				out.Label = name
			}
		}
	}

	enabledSet := false
	if value, ok := raw["enabled"]; ok {
		enabledSet = true
		_ = json.Unmarshal(value, &out.Enabled)
	}
	if !enabledSet {
		out.Enabled = true
	}

	*c = NormalizeSourceConfig(ContextSourceConfig(out))
	return nil
}

// ContextConfig stores user preferences for which context sources to load.
type ContextConfig struct {
	Defaults ContextDefaults       `json:"defaults"`
	Sources  []ContextSourceConfig `json:"sources,omitempty"`
}

// isValidRefreshMode returns true if the refresh mode is supported.
func isValidRefreshMode(mode RefreshMode) bool {
	switch mode {
	case RefreshInherit, RefreshOnChange, RefreshEveryMessage, RefreshEveryTurn, RefreshTTL:
		return true
	default:
		return false
	}
}

func isValidCachePolicy(policy CachePolicy) bool {
	switch policy {
	case CacheInherit, CacheCached, CacheEphemeral:
		return true
	default:
		return false
	}
}

type builtinSourceSpec struct {
	ID                 string
	Label              string
	Kind               ContextSourceKind
	DefaultEnabled     bool
	DefaultRefreshMode RefreshMode
	DefaultCachePolicy CachePolicy
}

var builtinSources = []builtinSourceSpec{
	{
		ID:                 SourceIDGlobalClaudeMd,
		Label:              "Global CLAUDE.md",
		Kind:               SourceKindFile,
		DefaultEnabled:     false,
		DefaultRefreshMode: RefreshOnChange,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDGlobalSwarmMd,
		Label:              "Global SWARM.md",
		Kind:               SourceKindFile,
		DefaultEnabled:     false,
		DefaultRefreshMode: RefreshOnChange,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDProjectClaudeMd,
		Label:              "Project CLAUDE.md",
		Kind:               SourceKindFile,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshOnChange,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDProjectSwarmMd,
		Label:              "Project SWARM.md",
		Kind:               SourceKindFile,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshOnChange,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDAgentsMd,
		Label:              "AGENTS.md",
		Kind:               SourceKindFile,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshOnChange,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDIndexMd,
		Label:              "INDEX.md",
		Kind:               SourceKindFile,
		DefaultEnabled:     false,
		DefaultRefreshMode: RefreshOnChange,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDGitStatus,
		Label:              "Git Status",
		Kind:               SourceKindDynamic,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshInherit,
		DefaultCachePolicy: CacheEphemeral,
	},
	{
		ID:                 SourceIDProjectName,
		Label:              "Project Name",
		Kind:               SourceKindDynamic,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshInherit,
		DefaultCachePolicy: CacheCached,
	},
	{
		ID:                 SourceIDCurrentDate,
		Label:              "Current Date",
		Kind:               SourceKindDynamic,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshInherit,
		DefaultCachePolicy: CacheEphemeral,
	},
	{
		ID:                 SourceIDSkills,
		Label:              "Skills",
		Kind:               SourceKindInjection,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshInherit,
		DefaultCachePolicy: CacheInherit,
	},
	{
		ID:                 SourceIDWorkspaceEnv,
		Label:              "Workspace Environment",
		Kind:               SourceKindInjection,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshInherit,
		DefaultCachePolicy: CacheInherit,
	},
	{
		ID:                 SourceIDHookContext,
		Label:              "Hook Context",
		Kind:               SourceKindInjection,
		DefaultEnabled:     true,
		DefaultRefreshMode: RefreshInherit,
		DefaultCachePolicy: CacheInherit,
	},
}

func builtinSourceSpecByID(id string) (builtinSourceSpec, bool) {
	for _, spec := range builtinSources {
		if spec.ID == id {
			return spec, true
		}
	}
	return builtinSourceSpec{}, false
}

// DefaultSourceConfig returns the default configuration for a builtin source ID.
func DefaultSourceConfig(id string) (ContextSourceConfig, bool) {
	spec, ok := builtinSourceSpecByID(id)
	if !ok {
		return ContextSourceConfig{}, false
	}
	return defaultSourceConfig(spec), true
}

func defaultSourceConfig(spec builtinSourceSpec) ContextSourceConfig {
	return ContextSourceConfig{
		ID:          spec.ID,
		Label:       spec.Label,
		Kind:        spec.Kind,
		Enabled:     spec.DefaultEnabled,
		RefreshMode: spec.DefaultRefreshMode,
		CachePolicy: spec.DefaultCachePolicy,
	}
}

// DefaultConfig returns the default context configuration.
func DefaultConfig() ContextConfig {
	sources := make([]ContextSourceConfig, 0, len(builtinSources))
	for _, spec := range builtinSources {
		sources = append(sources, defaultSourceConfig(spec))
	}

	return ContextConfig{
		Defaults: ContextDefaults{
			RefreshMode: RefreshEveryMessage,
		},
		Sources: sources,
	}
}

// NormalizeDefaults validates and normalizes defaults.
func NormalizeDefaults(defaults ContextDefaults) ContextDefaults {
	if defaults.RefreshMode == RefreshInherit || !isValidRefreshMode(defaults.RefreshMode) {
		defaults.RefreshMode = ""
	}
	if defaults.CachePolicy == CacheInherit || !isValidCachePolicy(defaults.CachePolicy) {
		defaults.CachePolicy = ""
	}
	if defaults.MCPTimeoutMS != nil && *defaults.MCPTimeoutMS <= 0 {
		defaults.MCPTimeoutMS = nil
	}
	if defaults.MCPConcurrency != nil && *defaults.MCPConcurrency <= 0 {
		defaults.MCPConcurrency = nil
	}
	return defaults
}

// NormalizeSourceConfig fills derived fields and normalizes values.
func NormalizeSourceConfig(source ContextSourceConfig) ContextSourceConfig {
	source.ID = strings.TrimSpace(source.ID)
	source.Label = strings.TrimSpace(source.Label)
	source.Kind = normalizeSourceKind(source.Kind, source)

	if source.ID == "" && isMCPKind(source.Kind) {
		source.ID = deriveMCPSourceID(MCPContextSource{
			Kind:       mcpKindForSource(source.Kind),
			ServerName: source.ServerName,
			URI:        source.URI,
			PromptName: source.PromptName,
			PromptArgs: source.PromptArgs,
		})
	}

	if spec, ok := builtinSourceSpecByID(source.ID); ok {
		if source.Label == "" {
			source.Label = spec.Label
		}
		if source.Kind == "" {
			source.Kind = spec.Kind
		}
	}

	if source.RefreshMode == "" || !isValidRefreshMode(source.RefreshMode) {
		source.RefreshMode = RefreshInherit
	}
	if source.CachePolicy == "" || !isValidCachePolicy(source.CachePolicy) {
		source.CachePolicy = CacheInherit
	}

	return source
}

// NormalizeSourceConfigs applies normalization and drops duplicate IDs.
func NormalizeSourceConfigs(sources []ContextSourceConfig) []ContextSourceConfig {
	if len(sources) == 0 {
		return sources
	}

	normalized := make([]ContextSourceConfig, 0, len(sources))
	seen := make(map[string]bool)
	for _, source := range sources {
		source = NormalizeSourceConfig(source)
		if source.ID != "" {
			if seen[source.ID] {
				continue
			}
			seen[source.ID] = true
		}
		normalized = append(normalized, source)
	}

	return normalized
}

// EnsureBuiltinSources adds any missing built-in sources to the config.
// This ensures new built-in sources automatically appear in existing configs
// when the codebase adds new sources that users haven't explicitly configured.
func EnsureBuiltinSources(sources []ContextSourceConfig) []ContextSourceConfig {
	// Build set of existing source IDs
	existing := make(map[string]bool)
	for _, s := range sources {
		if s.ID != "" {
			existing[s.ID] = true
		}
	}

	// Add missing built-in sources with default settings
	for _, spec := range builtinSources {
		if !existing[spec.ID] {
			sources = append(sources, defaultSourceConfig(spec))
		}
	}

	return sources
}

// NormalizeConfig applies normalization to defaults and sources.
func NormalizeConfig(cfg ContextConfig) ContextConfig {
	cfg.Defaults = NormalizeDefaults(cfg.Defaults)
	cfg.Sources = NormalizeSourceConfigs(cfg.Sources)
	cfg.Sources = EnsureBuiltinSources(cfg.Sources)
	return cfg
}

// FindSourceByID returns the source and index by ID.
func FindSourceByID(sources []ContextSourceConfig, id string) (ContextSourceConfig, int, bool) {
	for i, source := range sources {
		if source.ID == id {
			return source, i, true
		}
	}
	return ContextSourceConfig{}, -1, false
}

// IsInjectionEnabled reports whether the injection-kind source with the given
// ID is enabled in the (merged) config. Missing entries fall back to the
// builtin default, and unknown IDs default to enabled so a stale config can
// never silently disable a new injection site.
func IsInjectionEnabled(cfg ContextConfig, id string) bool {
	if source, _, ok := FindSourceByID(cfg.Sources, id); ok {
		return source.Enabled
	}
	if spec, ok := builtinSourceSpecByID(id); ok {
		return spec.DefaultEnabled
	}
	return true
}

// IsMCPSourceConfig reports whether a source config represents an MCP source.
func IsMCPSourceConfig(source ContextSourceConfig) bool {
	kind := normalizeSourceKind(source.Kind, source)
	return kind == SourceKindMCPResource || kind == SourceKindMCPPrompt
}

// MCPSourceFromConfig converts a config entry to an MCP source if applicable.
func MCPSourceFromConfig(source ContextSourceConfig) (MCPContextSource, bool) {
	kind := normalizeSourceKind(source.Kind, source)
	if kind != SourceKindMCPResource && kind != SourceKindMCPPrompt {
		return MCPContextSource{}, false
	}

	mcp := MCPContextSource{
		ID:         source.ID,
		Enabled:    source.Enabled,
		ServerName: source.ServerName,
		URI:        source.URI,
		PromptName: source.PromptName,
		PromptArgs: source.PromptArgs,
		Label:      source.Label,
	}
	if kind == SourceKindMCPPrompt {
		mcp.Kind = MCPSourcePrompt
	} else {
		mcp.Kind = MCPSourceResource
	}

	return NormalizeMCPSource(mcp), true
}

// ConfigSourceFromMCP converts an MCP source into a config entry.
func ConfigSourceFromMCP(source MCPContextSource) ContextSourceConfig {
	normalized := NormalizeMCPSource(source)
	kind := SourceKindMCPResource
	if normalized.Kind == MCPSourcePrompt {
		kind = SourceKindMCPPrompt
	}

	return ContextSourceConfig{
		ID:          normalized.ID,
		Label:       normalized.Label,
		Kind:        kind,
		Enabled:     normalized.Enabled,
		RefreshMode: RefreshInherit,
		CachePolicy: CacheEphemeral,
		ServerName:  normalized.ServerName,
		URI:         normalized.URI,
		PromptName:  normalized.PromptName,
		PromptArgs:  normalized.PromptArgs,
	}
}

// ExtractMCPSources collects MCP sources from a config.
func ExtractMCPSources(config ContextConfig) []MCPContextSource {
	sources := NormalizeSourceConfigs(config.Sources)
	out := make([]MCPContextSource, 0, len(sources))
	for _, source := range sources {
		if !IsMCPSourceConfig(source) {
			continue
		}
		if mcpSource, ok := MCPSourceFromConfig(source); ok {
			out = append(out, mcpSource)
		}
	}
	return out
}

func normalizeSourceKind(kind ContextSourceKind, source ContextSourceConfig) ContextSourceKind {
	switch kind {
	case SourceKindFile, SourceKindDynamic, SourceKindMCPResource, SourceKindMCPPrompt, SourceKindInjection:
		return kind
	case ContextSourceKind(MCPSourceResource):
		return SourceKindMCPResource
	case ContextSourceKind(MCPSourcePrompt):
		return SourceKindMCPPrompt
	}

	if source.PromptName != "" {
		return SourceKindMCPPrompt
	}
	if source.URI != "" {
		return SourceKindMCPResource
	}
	if spec, ok := builtinSourceSpecByID(source.ID); ok {
		return spec.Kind
	}
	return kind
}

func isMCPKind(kind ContextSourceKind) bool {
	return kind == SourceKindMCPResource || kind == SourceKindMCPPrompt
}

func mcpKindForSource(kind ContextSourceKind) MCPSourceType {
	if kind == SourceKindMCPPrompt {
		return MCPSourcePrompt
	}
	return MCPSourceResource
}

// ContextSource represents a single loaded context source.
type ContextSource struct {
	Name    string // e.g., "claudeMd", "swarmMd", "gitStatus"
	Path    string // File path (empty for dynamic sources)
	Content string // The loaded content
}

// LoadedContext contains all loaded context sources.
type LoadedContext struct {
	Sources []ContextSource
}

// ToMap converts loaded context to a map for easy iteration.
func (lc *LoadedContext) ToMap() map[string]string {
	result := make(map[string]string)
	for _, src := range lc.Sources {
		result[src.Name] = src.Content
	}
	return result
}

// FormatAsXML formats all context sources as XML blocks for injection.
func (lc *LoadedContext) FormatAsXML() string {
	if len(lc.Sources) == 0 {
		return ""
	}

	var result strings.Builder
	result.WriteString("\nAs you answer the user's questions, you can use the following context:\n")

	for _, src := range lc.Sources {
		if src.Content != "" {
			result.WriteString("<context name=\"" + src.Name + "\">\n")
			result.WriteString(src.Content)
			result.WriteString("\n</context>\n")
		}
	}

	return result.String()
}

// ContextLoader defines the interface for loading context.
type ContextLoader interface {
	// LoadContext loads all enabled context sources based on config.
	LoadContext(config ContextConfig, workDir string) (*LoadedContext, error)

	// GetConfig returns the current configuration.
	GetConfig() ContextConfig

	// SetConfig updates the configuration.
	SetConfig(config ContextConfig) error
}
