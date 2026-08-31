// Package plugins implements Claude Code-compatible plugin support for SwarmOS.
// Plugins are directories containing a .claude-plugin/plugin.json manifest
// along with commands, agents, skills, hooks, MCP servers, and LSP servers.
package plugins

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// Plugin represents a Claude Code-compatible plugin with all its components.
type Plugin struct {
	// Manifest from .claude-plugin/plugin.json
	Manifest PluginManifest `json:"manifest"`

	// Commands from commands/*.md
	Commands []Command `json:"commands,omitempty"`

	// Agents from agents/*.md
	Agents []Agent `json:"agents,omitempty"`

	// Skills from skills/*/SKILL.md
	Skills []*skills.Skill `json:"skills,omitempty"`

	// Hooks from hooks/hooks.json
	Hooks *PluginHooks `json:"hooks,omitempty"`

	// MCPServers from .mcp.json
	MCPServers []MCPServer `json:"mcp_servers,omitempty"`

	// LSPServers from .lsp.json
	LSPServers []LSPServer `json:"lsp_servers,omitempty"`

	// Path to the plugin directory
	Path string `json:"path"`

	// Source indicates where the plugin came from
	Source PluginSource `json:"source"`

	// MarketplaceName is the marketplace this plugin was installed from
	MarketplaceName string `json:"marketplace_name,omitempty"`

	// Enabled indicates if the plugin is currently active
	Enabled bool `json:"enabled"`

	// InstalledAt timestamp
	InstalledAt time.Time `json:"installed_at"`

	// LoadedAt timestamp
	LoadedAt time.Time `json:"loaded_at"`
}

// PluginSource indicates where a plugin originated from.
type PluginSource string

const (
	SourceLocal       PluginSource = "local"       // Local directory
	SourceMarketplace PluginSource = "marketplace" // Installed from marketplace
	SourceGit         PluginSource = "git"         // Cloned from git repository
	SourceUser        PluginSource = "user"        // User's ~/.swarm/plugins
	SourceProject     PluginSource = "project"     // Project's .swarm/plugins
)

// PluginManifest represents the .claude-plugin/plugin.json file.
type PluginManifest struct {
	// Name is the unique identifier and namespace prefix (required)
	// Commands are accessed as /name:command
	Name string `json:"name"`

	// Description shown in plugin manager (required)
	Description string `json:"description"`

	// Version using semantic versioning
	Version string `json:"version"`

	// Author information
	Author AuthorInfo `json:"author"`

	// Homepage URL for more information
	Homepage string `json:"homepage,omitempty"`

	// Repository URL for source code
	Repository string `json:"repository,omitempty"`

	// License identifier (e.g., "MIT", "Apache-2.0")
	License string `json:"license,omitempty"`

	// Keywords for search discovery
	Keywords []string `json:"keywords,omitempty"`

	// Category for organization
	Category string `json:"category,omitempty"`

	// Dependencies on other plugins
	Dependencies []string `json:"dependencies,omitempty"`

	// MinVersion of SwarmOS required
	MinVersion string `json:"min_version,omitempty"`
}

// AuthorInfo contains plugin author details.
type AuthorInfo struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

// Command represents a slash command from commands/*.md.
type Command struct {
	// Name derived from filename (hello.md -> hello)
	Name string `json:"name"`

	// Description from frontmatter
	Description string `json:"description,omitempty"`

	// AllowedInPlanMode indicates if command can run in plan mode
	AllowedInPlanMode bool `json:"allowed_in_plan_mode,omitempty"`

	// Content is the markdown instruction content
	Content string `json:"content"`

	// Arguments defines expected arguments
	// Supports $ARGUMENTS (all args), $1, $2, etc.
	Arguments []CommandArgument `json:"arguments,omitempty"`

	// ArgumentHint is a user-friendly hint shown in autocomplete (e.g., "Optional feature description")
	ArgumentHint string `json:"argument_hint,omitempty"`

	// Path to the source file
	Path string `json:"path"`
}

// CommandArgument defines a command argument.
type CommandArgument struct {
	// Name of the argument
	Name string `json:"name"`

	// Description of what the argument does
	Description string `json:"description,omitempty"`

	// Required indicates if argument must be provided
	Required bool `json:"required,omitempty"`

	// Default value if not provided
	Default string `json:"default,omitempty"`
}

// Agent represents a custom agent from agents/*.md.
type Agent struct {
	// Name derived from filename
	Name string `json:"name"`

	// Description of the agent's purpose
	Description string `json:"description,omitempty"`

	// Model to use (sonnet, opus, haiku)
	Model string `json:"model,omitempty"`

	// Tools the agent has access to
	Tools []string `json:"tools,omitempty"`

	// Instructions for the agent (markdown content)
	Instructions string `json:"instructions"`

	// MaxTurns limits agent execution
	MaxTurns int `json:"max_turns,omitempty"`

	// Path to the source file
	Path string `json:"path"`
}

// PluginHooks contains hook configurations from hooks/hooks.json.
type PluginHooks struct {
	// PreToolUse hooks run before tool invocation
	PreToolUse []HookConfig `json:"PreToolUse,omitempty"`

	// PostToolUse hooks run after tool completes
	PostToolUse []HookConfig `json:"PostToolUse,omitempty"`

	// Stop hooks run when conversation stops
	Stop []HookConfig `json:"Stop,omitempty"`

	// SessionStart hooks run at session initialization
	SessionStart []HookConfig `json:"SessionStart,omitempty"`

	// UserPromptSubmit hooks run when user submits a prompt
	UserPromptSubmit []HookConfig `json:"UserPromptSubmit,omitempty"`

	// Notification hooks for background task completion
	Notification []HookConfig `json:"Notification,omitempty"`
}

// HookConfig defines a single hook configuration.
type HookConfig struct {
	// Matcher pattern for tools (e.g., "Write|Edit")
	Matcher string `json:"matcher,omitempty"`

	// Hooks to execute when matched
	Hooks []HookAction `json:"hooks,omitempty"`
}

// HookAction defines an action to take when a hook matches.
type HookAction struct {
	// Type: "command" or "prompt"
	Type string `json:"type"`

	// Command to execute (for command type)
	Command string `json:"command,omitempty"`

	// Prompt for LLM (for prompt type)
	Prompt string `json:"prompt,omitempty"`

	// Timeout in seconds
	Timeout int `json:"timeout,omitempty"`

	// Environment variables to set
	Environment map[string]string `json:"environment,omitempty"`
}

// MCPServer represents an MCP server configuration from .mcp.json.
type MCPServer struct {
	// Name of the MCP server
	Name string `json:"name"`

	// Type: "stdio", "sse", or "http"
	Type string `json:"type,omitempty"`

	// Command to start the server (for stdio type)
	Command string `json:"command,omitempty"`

	// Args for the command
	Args []string `json:"args,omitempty"`

	// URL for SSE/HTTP servers
	URL string `json:"url,omitempty"`

	// Environment variables
	Environment map[string]string `json:"env,omitempty"`

	// Enabled status
	Enabled bool `json:"enabled"`
}

// LSPServer represents a language server configuration from .lsp.json.
type LSPServer struct {
	// Language identifier (e.g., "go", "typescript", "python")
	Language string `json:"language"`

	// Command to start the language server
	Command string `json:"command"`

	// Args for the command
	Args []string `json:"args,omitempty"`

	// ExtensionToLanguage maps file extensions to language IDs
	ExtensionToLanguage map[string]string `json:"extensionToLanguage,omitempty"`

	// Enabled status
	Enabled bool `json:"enabled"`
}

// PluginSearchResult represents a plugin found in search.
type PluginSearchResult struct {
	// Type distinguishes from skill results
	Type string `json:"type"` // "plugin"

	// Name of the plugin
	Name string `json:"name"`

	// Description
	Description string `json:"description"`

	// Version
	Version string `json:"version"`

	// Author name
	Author string `json:"author"`

	// Category
	Category string `json:"category"`

	// Keywords/tags
	Keywords []string `json:"keywords"`

	// Source marketplace or path
	Source string `json:"source"`

	// Installed status
	Installed bool `json:"installed"`

	// Enabled status (if installed)
	Enabled bool `json:"enabled"`

	// CommandCount in the plugin
	CommandCount int `json:"command_count"`

	// AgentCount in the plugin
	AgentCount int `json:"agent_count"`

	// SkillCount in the plugin
	SkillCount int `json:"skill_count"`

	// HasMCP indicates plugin has MCP servers
	HasMCP bool `json:"has_mcp"`

	// HasLSP indicates plugin has LSP servers
	HasLSP bool `json:"has_lsp"`

	// Downloads count (from marketplace)
	Downloads int `json:"downloads,omitempty"`

	// Rating score (from marketplace)
	Rating float64 `json:"rating,omitempty"`

	// Verified by marketplace owner
	Verified bool `json:"verified,omitempty"`

	// Featured plugin
	Featured bool `json:"featured,omitempty"`
}

// UnifiedSearchResult can represent either a skill or plugin.
type UnifiedSearchResult struct {
	// ResultType: "skill" or "plugin"
	ResultType string `json:"result_type"`

	// Name of the skill or plugin
	Name string `json:"name"`

	// Description
	Description string `json:"description"`

	// Version
	Version string `json:"version"`

	// Author
	Author string `json:"author"`

	// Category
	Category string `json:"category"`

	// Tags/Keywords
	Tags []string `json:"tags"`

	// Source (marketplace name or "local")
	Source string `json:"source"`

	// Installed status
	Installed bool `json:"installed"`

	// Enabled/Active status
	Enabled bool `json:"enabled"`

	// --- Plugin-specific fields ---

	// CommandCount (plugins only)
	CommandCount int `json:"command_count,omitempty"`

	// AgentCount (plugins only)
	AgentCount int `json:"agent_count,omitempty"`

	// SkillCount (plugins only)
	SkillCount int `json:"skill_count,omitempty"`

	// HasMCP (plugins only)
	HasMCP bool `json:"has_mcp,omitempty"`

	// HasLSP (plugins only)
	HasLSP bool `json:"has_lsp,omitempty"`

	// --- Skill-specific fields ---

	// ScriptCount (skills only)
	ScriptCount int `json:"script_count,omitempty"`

	// ReferenceCount (skills only)
	ReferenceCount int `json:"reference_count,omitempty"`

	// --- Marketplace fields ---

	// Downloads count
	Downloads int `json:"downloads,omitempty"`

	// Rating score
	Rating float64 `json:"rating,omitempty"`

	// Verified status
	Verified bool `json:"verified,omitempty"`

	// Featured status
	Featured bool `json:"featured,omitempty"`
}

// SearchFilter defines search criteria for unified search.
type SearchFilter struct {
	// Query string to search for
	Query string `json:"query"`

	// Types to include: "skill", "plugin", or empty for both
	Types []string `json:"types,omitempty"`

	// Categories to filter by
	Categories []string `json:"categories,omitempty"`

	// Tags to filter by
	Tags []string `json:"tags,omitempty"`

	// InstalledOnly shows only installed items
	InstalledOnly bool `json:"installed_only,omitempty"`

	// EnabledOnly shows only enabled items
	EnabledOnly bool `json:"enabled_only,omitempty"`

	// FeaturedOnly shows only featured items
	FeaturedOnly bool `json:"featured_only,omitempty"`

	// VerifiedOnly shows only verified items
	VerifiedOnly bool `json:"verified_only,omitempty"`

	// Limit maximum results
	Limit int `json:"limit,omitempty"`

	// SortBy field (relevance, name, downloads, rating)
	SortBy string `json:"sort_by,omitempty"`

	// SortDesc for descending order
	SortDesc bool `json:"sort_desc,omitempty"`
}

// Namespace returns the command namespace for the plugin.
func (p *Plugin) Namespace() string {
	return p.Manifest.Name
}

// CommandNames returns all command names with namespace prefix.
func (p *Plugin) CommandNames() []string {
	names := make([]string, len(p.Commands))
	for i, cmd := range p.Commands {
		names[i] = p.Manifest.Name + ":" + cmd.Name
	}
	return names
}

// GetCommand returns a command by name (without namespace).
func (p *Plugin) GetCommand(name string) *Command {
	for i := range p.Commands {
		if p.Commands[i].Name == name {
			return &p.Commands[i]
		}
	}
	return nil
}

// GetAgent returns an agent by name.
func (p *Plugin) GetAgent(name string) *Agent {
	for i := range p.Agents {
		if p.Agents[i].Name == name {
			return &p.Agents[i]
		}
	}
	return nil
}

// ToSearchResult converts a Plugin to a PluginSearchResult.
func (p *Plugin) ToSearchResult() PluginSearchResult {
	author := ""
	if p.Manifest.Author.Name != "" {
		author = p.Manifest.Author.Name
	}

	return PluginSearchResult{
		Type:         "plugin",
		Name:         p.Manifest.Name,
		Description:  p.Manifest.Description,
		Version:      p.Manifest.Version,
		Author:       author,
		Category:     p.Manifest.Category,
		Keywords:     p.Manifest.Keywords,
		Source:       string(p.Source),
		Installed:    true,
		Enabled:      p.Enabled,
		CommandCount: len(p.Commands),
		AgentCount:   len(p.Agents),
		SkillCount:   len(p.Skills),
		HasMCP:       len(p.MCPServers) > 0,
		HasLSP:       len(p.LSPServers) > 0,
	}
}

// ToUnifiedResult converts a Plugin to a UnifiedSearchResult.
func (p *Plugin) ToUnifiedResult() UnifiedSearchResult {
	author := ""
	if p.Manifest.Author.Name != "" {
		author = p.Manifest.Author.Name
	}

	return UnifiedSearchResult{
		ResultType:   "plugin",
		Name:         p.Manifest.Name,
		Description:  p.Manifest.Description,
		Version:      p.Manifest.Version,
		Author:       author,
		Category:     p.Manifest.Category,
		Tags:         p.Manifest.Keywords,
		Source:       string(p.Source),
		Installed:    true,
		Enabled:      p.Enabled,
		CommandCount: len(p.Commands),
		AgentCount:   len(p.Agents),
		SkillCount:   len(p.Skills),
		HasMCP:       len(p.MCPServers) > 0,
		HasLSP:       len(p.LSPServers) > 0,
	}
}
