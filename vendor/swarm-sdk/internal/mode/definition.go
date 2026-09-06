// Package mode - Operating Mode Definitions
//
// Operating modes control tool access and system behavior.
// This is separate from orchestration modes (multi-agent workflows).
//
// An OperatingMode defines:
// - Which tools are exposed to the LLM
// - What system instruction is injected
// - Permission overrides for the mode

package mode

// OperatingMode defines a tool filtering and instruction mode.
// Examples: PLAN (read-only), ACT (full access), AUTO (autonomous)
type OperatingMode struct {
	// Unique identifier for this mode
	ID string `json:"id" yaml:"id"`

	// Human-readable name
	Name string `json:"name" yaml:"name"`

	// Description of what this mode does
	Description string `json:"description" yaml:"description"`

	// Tool Control
	// AllowedTools lists tool name patterns that are exposed in this mode.
	// Use "*" to allow all tools. Empty means no tools.
	// Patterns can be exact names ("file_read") or prefixes ("file_*")
	AllowedTools []string `json:"allowed_tools" yaml:"allowed_tools"`

	// BlockedTools lists tool name patterns that are blocked in this mode.
	// BlockedTools takes precedence over AllowedTools.
	BlockedTools []string `json:"blocked_tools" yaml:"blocked_tools"`

	// HideBlockedTools: if true, blocked tools are not exposed to LLM at all.
	// If false, blocked tools are exposed but will error if called.
	HideBlockedTools bool `json:"hide_blocked_tools" yaml:"hide_blocked_tools"`

	// System Instruction
	// SystemInstruction is appended to the system prompt when in this mode.
	// Example: "You are in PLAN mode. Explore and gather context only."
	SystemInstruction string `json:"system_instruction" yaml:"system_instruction"`

	// ReadOnly indicates this mode should not modify files/state.
	// This is a hint for UI and validation, not enforced here.
	ReadOnly bool `json:"read_only" yaml:"read_only"`

	// AllowModeSwitch indicates if the AI can switch out of this mode.
	// If false, only the user can switch modes.
	AllowModeSwitch bool `json:"allow_mode_switch" yaml:"allow_mode_switch"`

	// CanSwitchTo lists modes that can be switched to from this mode.
	// Empty means can switch to any mode (if AllowModeSwitch is true).
	CanSwitchTo []string `json:"can_switch_to" yaml:"can_switch_to"`
}

// IsToolAllowed checks if a tool is allowed in this mode.
func (m *OperatingMode) IsToolAllowed(toolName string) bool {
	// First check if tool is blocked
	if m.isToolBlocked(toolName) {
		return false
	}

	// Then check if tool is allowed
	return m.isToolInAllowList(toolName)
}

// isToolBlocked checks if tool matches any blocked pattern.
func (m *OperatingMode) isToolBlocked(toolName string) bool {
	for _, pattern := range m.BlockedTools {
		if matchesPattern(toolName, pattern) {
			return true
		}
	}
	return false
}

// isToolInAllowList checks if tool matches any allowed pattern.
func (m *OperatingMode) isToolInAllowList(toolName string) bool {
	// Empty AllowedTools means nothing is allowed
	if len(m.AllowedTools) == 0 {
		return false
	}

	for _, pattern := range m.AllowedTools {
		if pattern == "*" {
			return true
		}
		if matchesPattern(toolName, pattern) {
			return true
		}
	}
	return false
}

// matchesPattern checks if a tool name matches a pattern.
// Supports:
// - Exact match: "file_read" matches "file_read"
// - Prefix match: "file_*" matches "file_read", "file_write"
// - Suffix match: "*_read" matches "file_read", "memory_read"
// - Contains: "*edit*" matches "file_edit", "multi_edit_tool"
func matchesPattern(toolName, pattern string) bool {
	if pattern == toolName {
		return true
	}

	// Handle wildcard patterns
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

// Clone creates a deep copy of the operating mode.
func (m *OperatingMode) Clone() *OperatingMode {
	clone := &OperatingMode{
		ID:                m.ID,
		Name:              m.Name,
		Description:       m.Description,
		SystemInstruction: m.SystemInstruction,
		ReadOnly:          m.ReadOnly,
		HideBlockedTools:  m.HideBlockedTools,
		AllowModeSwitch:   m.AllowModeSwitch,
	}

	// Deep copy slices
	if m.AllowedTools != nil {
		clone.AllowedTools = make([]string, len(m.AllowedTools))
		copy(clone.AllowedTools, m.AllowedTools)
	}
	if m.BlockedTools != nil {
		clone.BlockedTools = make([]string, len(m.BlockedTools))
		copy(clone.BlockedTools, m.BlockedTools)
	}
	if m.CanSwitchTo != nil {
		clone.CanSwitchTo = make([]string, len(m.CanSwitchTo))
		copy(clone.CanSwitchTo, m.CanSwitchTo)
	}

	return clone
}
