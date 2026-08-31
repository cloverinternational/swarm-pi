// Package mode - Operating Mode Tool Filtering
//
// This file provides functionality to filter tools based on operating modes.
// When an agent is in a specific mode (PLAN, ACT, AUTO), only certain tools
// should be exposed to the LLM to constrain its behavior appropriately.

package mode

// ModeFilterConfig is a simple config struct that can be passed to the agent
// without creating circular dependencies. It mirrors the tool filtering fields
// from OperatingMode but is standalone.
type ModeFilterConfig struct {
	// ModeName is the name of the mode for logging (e.g., "PLAN", "ACT")
	ModeName string `json:"mode_name"`

	// AllowedTools lists tool name patterns that are allowed.
	AllowedTools []string `json:"allowed_tools"`

	// BlockedTools lists tool name patterns that are blocked.
	BlockedTools []string `json:"blocked_tools"`

	// HideBlockedTools: if true, blocked tools are not exposed to LLM.
	HideBlockedTools bool `json:"hide_blocked_tools"`
}

// ToFilterConfig converts an OperatingMode to a ModeFilterConfig.
// This is used when passing mode config to the agent without importing
// the full mode package.
func (m *OperatingMode) ToFilterConfig() *ModeFilterConfig {
	if m == nil {
		return nil
	}
	return &ModeFilterConfig{
		ModeName:         m.Name,
		AllowedTools:     m.AllowedTools,
		BlockedTools:     m.BlockedTools,
		HideBlockedTools: m.HideBlockedTools,
	}
}

// ToolNamer is a minimal interface for anything that has a name.
// This allows filtering without importing the full tools package,
// avoiding circular dependencies.
type ToolNamer interface {
	Name() string
}

// FilterToolsByMode filters a list of tools based on the operating mode.
// Tools that are not allowed by the mode are removed from the returned list.
// If mode.HideBlockedTools is true, blocked tools are not exposed at all.
// If mode.HideBlockedTools is false, blocked tools are still returned but
// will error at execution time (handled elsewhere).
//
// Parameters:
//   - tools: The full list of available tools (anything with Name() method)
//   - operatingMode: The current operating mode (PLAN, ACT, AUTO, etc.)
//
// Returns:
//   - Filtered list of tools allowed in this mode
//
// Example:
//
//	// In PLAN mode, only read-only tools are exposed
//	planMode := GetBuiltinMode(ModePlan)
//	filteredTools := FilterToolsByMode(allTools, planMode)
//	// filteredTools now only contains: file_read, grep, glob, etc.
func FilterToolsByMode[T ToolNamer](tools []T, operatingMode *OperatingMode) []T {
	// If no mode specified, return all tools (default behavior)
	if operatingMode == nil {
		return tools
	}

	// If mode allows all tools and doesn't block any, return all
	if len(operatingMode.AllowedTools) == 1 && operatingMode.AllowedTools[0] == "*" &&
		len(operatingMode.BlockedTools) == 0 {
		return tools
	}

	// Filter tools based on mode
	filtered := make([]T, 0, len(tools))
	for _, tool := range tools {
		toolName := tool.Name()

		// Check if tool is allowed by the mode
		if operatingMode.IsToolAllowed(toolName) {
			filtered = append(filtered, tool)
		} else if !operatingMode.HideBlockedTools {
			// If HideBlockedTools is false, still include the tool
			// (it will be blocked at execution time instead)
			filtered = append(filtered, tool)
		}
		// If HideBlockedTools is true and tool is not allowed, it's excluded
	}

	return filtered
}

// FilterResult contains the result of filtering tools by mode.
// This provides more detail about what was filtered and why.
type FilterResult[T ToolNamer] struct {
	// Allowed contains tools that are allowed in this mode
	Allowed []T

	// Blocked contains tools that were blocked (if HideBlockedTools is false)
	Blocked []T

	// Hidden contains tools that were hidden from LLM (if HideBlockedTools is true)
	Hidden []string // Just names, since they won't be returned

	// TotalOriginal is the count of tools before filtering
	TotalOriginal int

	// TotalAllowed is the count of allowed tools
	TotalAllowed int

	// TotalBlocked is the count of blocked tools
	TotalBlocked int
}

// FilterToolsByModeDetailed filters tools and returns detailed information
// about what was allowed, blocked, and hidden. This is useful for debugging
// and displaying mode status to users.
func FilterToolsByModeDetailed[T ToolNamer](tools []T, operatingMode *OperatingMode) *FilterResult[T] {
	result := &FilterResult[T]{
		Allowed:       make([]T, 0),
		Blocked:       make([]T, 0),
		Hidden:        make([]string, 0),
		TotalOriginal: len(tools),
	}

	// If no mode specified, all tools are allowed
	if operatingMode == nil {
		result.Allowed = tools
		result.TotalAllowed = len(tools)
		return result
	}

	for _, tool := range tools {
		toolName := tool.Name()

		if operatingMode.IsToolAllowed(toolName) {
			result.Allowed = append(result.Allowed, tool)
		} else {
			if operatingMode.HideBlockedTools {
				// Tool is hidden from LLM entirely
				result.Hidden = append(result.Hidden, toolName)
			} else {
				// Tool is blocked but still visible
				result.Blocked = append(result.Blocked, tool)
			}
		}
	}

	result.TotalAllowed = len(result.Allowed)
	result.TotalBlocked = len(result.Blocked) + len(result.Hidden)

	return result
}

// GetToolNamesForMode returns just the names of tools allowed in a mode.
// This is a lightweight function useful for logging or configuration.
func GetToolNamesForMode[T ToolNamer](tools []T, operatingMode *OperatingMode) []string {
	filtered := FilterToolsByMode(tools, operatingMode)
	names := make([]string, len(filtered))
	for i, tool := range filtered {
		names[i] = tool.Name()
	}
	return names
}

// PartitionToolsByMode separates tools into allowed and blocked lists.
// This is useful when you need to track which tools were blocked.
func PartitionToolsByMode[T ToolNamer](tools []T, operatingMode *OperatingMode) (allowed []T, blocked []T) {
	if operatingMode == nil {
		return tools, nil
	}

	allowed = make([]T, 0)
	blocked = make([]T, 0)

	for _, tool := range tools {
		if operatingMode.IsToolAllowed(tool.Name()) {
			allowed = append(allowed, tool)
		} else {
			blocked = append(blocked, tool)
		}
	}

	return allowed, blocked
}

// GetBlockedToolNames returns the names of tools that would be blocked in a mode.
// This is useful for displaying what tools are unavailable.
func GetBlockedToolNames[T ToolNamer](tools []T, operatingMode *OperatingMode) []string {
	if operatingMode == nil {
		return nil
	}

	var blocked []string
	for _, tool := range tools {
		if !operatingMode.IsToolAllowed(tool.Name()) {
			blocked = append(blocked, tool.Name())
		}
	}
	return blocked
}
