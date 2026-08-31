// Package skills implements the Claude Code-style skills system.
// Skills are folders of instructions, scripts, and resources that agents
// can discover and load dynamically to perform better at specific tasks.
package skills

import (
	"time"
)

// Skill represents a loaded skill with all its components.
type Skill struct {
	// Metadata from SKILL.md frontmatter
	Metadata SkillMetadata `json:"metadata" yaml:"metadata"`

	// Instructions from SKILL.md content
	Instructions string `json:"instructions" yaml:"instructions"`

	// Path to the skill directory
	Path string `json:"path" yaml:"path"`

	// Scripts available in the skill
	Scripts []SkillScript `json:"scripts,omitempty" yaml:"scripts,omitempty"`

	// References available in the skill
	References []SkillReference `json:"references,omitempty" yaml:"references,omitempty"`

	// Assets available in the skill
	Assets []SkillAsset `json:"assets,omitempty" yaml:"assets,omitempty"`

	// Hooks configuration from skill.
	//
	// Deprecated: Skill hooks are not executed in the Claude-style on-demand
	// invocation model. This field is preserved for backward compatibility
	// (e.g. TUI registerSkillHooks) but has no effect on skill invocation.
	Hooks *SkillHooks `json:"hooks,omitempty" yaml:"hooks,omitempty"`

	// LoadedAt timestamp
	LoadedAt time.Time `json:"loaded_at" yaml:"loaded_at"`

	// LoadedFrom indicates where the skill was discovered: "user", "project", "builtin", "mcp", "policy".
	// Set during discovery to track the skill's source for UI grouping and trust decisions.
	LoadedFrom string `json:"loaded_from,omitempty" yaml:"loaded_from,omitempty"`

	// Source indicates the skill's discovery source for UI grouping: "policy", "user", "project", "builtin", "mcp".
	// Set during discovery to enable the skills menu to group skills by source,
	// mirroring Claude Code's SkillsMenu.tsx grouping (policySettings, userSettings, etc).
	Source string `json:"source,omitempty" yaml:"source,omitempty"`

	// ContentLoaded tracks whether the full Instructions have been lazily loaded.
	// When false, only frontmatter was parsed — content is deferred until SkillTool invocation.
	ContentLoaded bool `json:"-" yaml:"-"`
}

// SkillMetadata contains SKILL.md frontmatter fields.
// Follows the agentskills.io specification for interoperability.
type SkillMetadata struct {
	// Name is the skill identifier (required, max 64 chars)
	// Must be lowercase alphanumeric + hyphens, no start/end hyphen, no consecutive hyphens
	Name string `json:"name" yaml:"name"`

	// Description explains what the skill does (required, max
	// skills.MaxDescriptionLength chars — 1500)
	Description string `json:"description" yaml:"description"`

	// Version of the skill
	Version string `json:"version,omitempty" yaml:"version,omitempty"`

	// Author who created the skill
	Author string `json:"author,omitempty" yaml:"author,omitempty"`

	// Category for organization
	Category string `json:"category,omitempty" yaml:"category,omitempty"`

	// Tags for discovery
	Tags []string `json:"tags,omitempty" yaml:"tags,omitempty"`

	// Dependencies on other skills
	Dependencies []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`

	// Enabled indicates if skill is active by default
	Enabled bool `json:"enabled,omitempty" yaml:"enabled,omitempty"`

	// Priority for skill loading order (higher = loaded first)
	Priority int `json:"priority,omitempty" yaml:"priority,omitempty"`

	// Triggers define when to auto-activate the skill
	Triggers []SkillTrigger `json:"triggers,omitempty" yaml:"triggers,omitempty"`

	// Icon for UI display
	Icon string `json:"icon,omitempty" yaml:"icon,omitempty"`

	// Homepage URL for more info
	Homepage string `json:"homepage,omitempty" yaml:"homepage,omitempty"`

	// License for the skill (optional, max 256 chars per agentskills.io spec)
	License string `json:"license,omitempty" yaml:"license,omitempty"`

	// Compatibility requirements (optional, max 500 chars per agentskills.io spec)
	Compatibility string `json:"compatibility,omitempty" yaml:"compatibility,omitempty"`

	// AllowedTools is a space-delimited list of pre-approved tools (experimental per agentskills.io spec)
	AllowedTools string `json:"allowed-tools,omitempty" yaml:"allowed-tools,omitempty"`

	// WhenToUse describes when the LLM should activate this skill.
	// Included in the SkillTool listing for smart matching.
	// Mirrors Claude Code's when_to_use frontmatter field.
	WhenToUse string `json:"when_to_use,omitempty" yaml:"when_to_use,omitempty"`

	// Arguments declares named arguments the skill accepts.
	// Used with SubstituteArguments for {{arg}} placeholder replacement.
	// Example: ["repo", "branch"] means the skill content can use {{repo}} and {{branch}}.
	Arguments []string `json:"arguments,omitempty" yaml:"arguments,omitempty"`

	// ArgumentHint provides a usage hint for arguments (e.g., "repo branch").
	// Shown in the SkillTool parameter description.
	ArgumentHint string `json:"argument-hint,omitempty" yaml:"argument-hint,omitempty"`

	// Context determines the execution mode: "inline" or "fork".
	// "fork" runs the skill in an isolated sub-agent with its own token budget.
	// Default (empty) is "inline".
	Context string `json:"context,omitempty" yaml:"context,omitempty"`

	// Model overrides the session model for this skill invocation.
	// Example: "haiku", "opus". Mirrors Claude Code's model frontmatter field.
	Model string `json:"model,omitempty" yaml:"model,omitempty"`

	// DisableModelInvocation hides this skill from the SkillTool listing.
	// The LLM cannot invoke it via the Skill tool, but users can still use /skill-name.
	DisableModelInvocation bool `json:"disable-model-invocation,omitempty" yaml:"disable-model-invocation,omitempty"`

	// UserInvocable controls whether the skill appears in user-facing menus.
	// When false, the skill is model-only (activated by auto-triggers or SkillTool).
	// Defaults to true when not set (Go bool defaults to false, so logic must treat
	// false + not-set as true — check UserInvocableOrDefault()).
	UserInvocable bool `json:"user-invocable,omitempty" yaml:"user-invocable,omitempty"`

	// Effort level override for the skill invocation.
	// Mirrors Claude Code's effort frontmatter field.
	Effort string `json:"effort,omitempty" yaml:"effort,omitempty"`

	// Paths for path-based activation (gitignore-style patterns).
	// The skill auto-activates when the user is working in a matching directory.
	Paths []string `json:"paths,omitempty" yaml:"paths,omitempty"`

	// CustomMetadata for arbitrary key-value pairs per agentskills.io spec
	CustomMetadata map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// SkillTrigger defines automatic skill activation conditions.
type SkillTrigger struct {
	// Type of trigger: "file_pattern", "keyword", "tool", "mode"
	Type string `json:"type" yaml:"type"`

	// Pattern for matching (regex for file_pattern/keyword, exact for tool/mode)
	Pattern string `json:"pattern" yaml:"pattern"`
}

// SkillScript represents an executable script in the skill.
type SkillScript struct {
	// Name of the script file
	Name string `json:"name" yaml:"name"`

	// Path relative to skill directory
	Path string `json:"path" yaml:"path"`

	// Description of what the script does
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Language of the script (python, bash, etc.)
	Language string `json:"language,omitempty" yaml:"language,omitempty"`

	// Entrypoint command to run
	Entrypoint string `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
}

// SkillReference represents a documentation file in the skill.
type SkillReference struct {
	// Name of the reference file
	Name string `json:"name" yaml:"name"`

	// Path relative to skill directory
	Path string `json:"path" yaml:"path"`

	// Description of the reference content
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Format of the file (markdown, json, etc.)
	Format string `json:"format,omitempty" yaml:"format,omitempty"`
}

// SkillAsset represents a resource file in the skill.
type SkillAsset struct {
	// Name of the asset
	Name string `json:"name" yaml:"name"`

	// Path relative to skill directory
	Path string `json:"path" yaml:"path"`

	// MimeType of the asset
	MimeType string `json:"mime_type,omitempty" yaml:"mime_type,omitempty"`
}

// SkillHooks contains hook configurations for the skill.
//
// Deprecated: Skill hooks are not executed in the Claude-style on-demand
// invocation model. They are preserved for backward compatibility but have
// no effect. The TUI reads these fields for display purposes only.
type SkillHooks struct {
	// PreToolUse hooks
	PreToolUse []SkillHookConfig `json:"PreToolUse,omitempty" yaml:"PreToolUse,omitempty"`

	// PostToolUse hooks
	PostToolUse []SkillHookConfig `json:"PostToolUse,omitempty" yaml:"PostToolUse,omitempty"`

	// Stop hooks
	Stop []SkillHookConfig `json:"Stop,omitempty" yaml:"Stop,omitempty"`

	// SessionStart hooks
	SessionStart []SkillHookConfig `json:"SessionStart,omitempty" yaml:"SessionStart,omitempty"`
}

// SkillHookConfig is a simplified hook configuration within a skill.
//
// Deprecated: See SkillHooks.
type SkillHookConfig struct {
	// Matcher pattern
	Matcher string `json:"matcher" yaml:"matcher"`

	// Type: "command" or "prompt"
	Type string `json:"type" yaml:"type"`

	// Command to execute (for command type)
	Command string `json:"command,omitempty" yaml:"command,omitempty"`

	// Prompt for LLM (for prompt type)
	Prompt string `json:"prompt,omitempty" yaml:"prompt,omitempty"`

	// Timeout in seconds
	Timeout int `json:"timeout,omitempty" yaml:"timeout,omitempty"`

	// Once indicates the hook should auto-remove after first execution.
	// Mirrors Claude Code's once: true frontmatter hook option.
	Once bool `json:"once,omitempty" yaml:"once,omitempty"`
}

// UserInvocableOrDefault returns the effective user-invocable status.
// Claude Code defaults user-invocable to true when not explicitly set,
// but Go's bool zero-value is false. This helper bridges the gap:
// if UserInvocable is false AND the YAML key was absent, it returns true.
// Callers should use this instead of reading UserInvocable directly.
func (m *SkillMetadata) UserInvocableOrDefault() bool {
	// If explicitly set to true, honor it.
	// If explicitly set to false (via YAML), honor it.
	// The zero-value false is treated as "not set" → default true.
	// We detect "not set" by checking if the YAML tag was present.
	// Since we can't detect that after unmarshalling, we use a simple
	// convention: if DisableModelInvocation is true, user-invocable
	// follows the explicit value; otherwise default to true.
	return !m.DisableModelInvocation
}

// SkillSearchResult represents a skill found in the registry.
type SkillSearchResult struct {
	// Name of the skill
	Name string `json:"name"`

	// Description
	Description string `json:"description"`

	// Version
	Version string `json:"version"`

	// Author
	Author string `json:"author"`

	// Category
	Category string `json:"category"`

	// Tags
	Tags []string `json:"tags"`

	// Source repository or registry
	Source string `json:"source"`

	// InstallPath suggests where to install
	InstallPath string `json:"install_path,omitempty"`

	// Downloads count
	Downloads int `json:"downloads,omitempty"`

	// Rating score
	Rating float64 `json:"rating,omitempty"`

	// Installed indicates if already installed
	Installed bool `json:"installed,omitempty"`
}
