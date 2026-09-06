// Package chat provides tool rendering utilities
package chat

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ToolColors defines the semantic color palette for tool rendering
type ToolColors struct {
	ToolName    string // Accent - tool names
	FilePath    string // Info - file paths
	Command     string // Teal - shell commands
	Success     string // Success - success states
	Running     string // Warning - in-progress
	Error       string // Error - errors
	Output      string // Text dim - output content
	Muted       string // Text muted - dimmed text
	Border      string // Border - borders
	Connector   string // Text muted - tree connectors
	HookSuccess string // Text muted - hook success (subtle)
}

// DefaultToolColors returns the model picker palette for tool output
func DefaultToolColors() ToolColors {
	return ToolColors{
		ToolName:    palette.Accent,
		FilePath:    palette.Info,
		Command:     palette.Teal,
		Success:     palette.Success,
		Running:     palette.Warning,
		Error:       palette.Error,
		Output:      palette.TextDim,
		Muted:       palette.TextMuted,
		Border:      palette.Border,
		Connector:   palette.TextMuted,
		HookSuccess: palette.TextMuted,
	}
}

// ToolIcon returns the appropriate icon for a tool
func ToolIcon(toolName string) string {
	icons := map[string]string{
		// Shell/System
		"bash":     "󰆍", // Terminal icon (nerd font)
		"Bash":     "󰆍",
		"shell":    "󰆍",
		"computer": "󰆍",

		// File Operations
		"Read":        "󰈔", // File read (hashline)
		"read":        "󰈔",
		"file_read":   "󰈔",
		"ReadLegacy":  "󰈔", // Legacy read (cat -n format)
		"Edit":        "󰏫", // Edit/pencil
		"edit":        "󰏫",
		"file_edit":   "󰏫",
		"str_replace": "󰏫", // Explicit string ops
		"file_undo":   "󰕌", // Undo arrow
		"Write":       "󰎞", // File write
		"write":       "󰎞",
		"file_write":  "󰎞",
		"MultiEdit":   "󰏫",

		// Search
		"Grep":          "󰍉", // Search
		"grep":          "󰍉",
		"semantic_grep": "󰍉", // LSP semantic search
		"Glob":          "󰉋", // Folder search
		"glob":          "󰉋",
		"LS":            "󰉋",
		"ls":            "󰉋",

		// Web
		"WebFetch":   "󰖟", // Globe
		"web_fetch":  "󰖟",
		"WebSearch":  "󰖟",
		"web_search": "󰖟",

		// Tasks/Todos
		"TodoWrite":     "󰄲", // Checklist (legacy)
		"TodoRead":      "󰄲",
		"todo_write":    "󰄲",
		"todo_read":     "󰄲",
		"TaskManage":    "󰄲",
		"HistorySearch": "󰍉",
		"HistoryGet":    "󰋚",

		// Agent/AI
		"Task":           "󰚩", // Robot
		"task":           "󰚩",
		"Agent":          "󰚩",
		"agent":          "󰚩",
		"BackgroundTask": "󰚩",
		"TaskOutput":     "󰚩",

		// Notebook
		"NotebookRead": "󰠮", // Notebook
		"NotebookEdit": "󰠮",

		// Git
		"git": "󰊢", // Git icon

		// Default
		"default": "󰊕", // Tool/wrench
	}

	if icon, ok := icons[toolName]; ok {
		return icon
	}
	return icons["default"]
}

// ToolIconFallback returns emoji fallback for terminals without nerd fonts
func ToolIconFallback(toolName string) string {
	icons := map[string]string{
		"bash":          "▸",
		"Bash":          "▸",
		"Read":          "◇",
		"read":          "◇",
		"ReadLegacy":    "◇",
		"Edit":          "◆",
		"edit":          "◆",
		"str_replace":   "◆",
		"Write":         "◆",
		"write":         "◆",
		"Grep":          "○",
		"grep":          "○",
		"semantic_grep": "○",
		"HistorySearch": "○",
		"HistoryGet":    "◇",
		"file_read":     "◇",
		"file_write":    "◆",
		"MultiEdit":     "◆",

		// Git
		"git": "◇", // Git icon

		// Default
		"default": "⚙", // Tool/wrench
	}

	if icon, ok := icons[toolName]; ok {
		return icon
	}
	return icons["default"]
}

// StatusIcon returns a status indicator
func StatusIcon(success bool, running bool) string {
	if running {
		return "◐" // Half-filled circle for running
	}
	if success {
		return "✓"
	}
	return "✗"
}

// TreeConnector represents tree-drawing characters
type TreeConnector struct {
	Vertical   string // │
	Corner     string // └
	Tee        string // ├
	Horizontal string // ─
}

// DefaultTreeConnector returns standard tree connectors
func DefaultTreeConnector() TreeConnector {
	return TreeConnector{
		Vertical:   "│",
		Corner:     "└",
		Tee:        "├",
		Horizontal: "─",
	}
}

// RenderToolHeader creates a styled tool header line
func RenderToolHeader(toolName string, params string, width int, colors ToolColors, running bool, success bool, duration string) string {
	icon := ToolIconFallback(toolName)

	// Styles
	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.ToolName))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.ToolName)).Bold(true)
	paramStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Border))

	var statusStyle lipgloss.Style
	var statusIcon string
	if running {
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Running))
		statusIcon = "◐"
	} else if success {
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Success))
		statusIcon = "✓"
	} else {
		statusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error))
		statusIcon = "✗"
	}

	// Build header: "├─ ◇ Read ─ file.go ──────────────────── ✓ 0.3s"
	connector := DefaultTreeConnector()

	prefix := borderStyle.Render(connector.Tee + connector.Horizontal + " ")
	iconPart := iconStyle.Render(icon) + " "
	namePart := nameStyle.Render(toolName)

	// Calculate remaining width for separator line
	leftPart := prefix + iconPart + namePart
	leftWidth := lipgloss.Width(leftPart)

	var paramPart string
	if params != "" {
		// Truncate params if too long
		maxParamLen := width - leftWidth - 20 // Leave room for status
		if len(params) > maxParamLen && maxParamLen > 3 {
			params = params[:maxParamLen-3] + "..."
		}
		paramPart = " " + borderStyle.Render(connector.Horizontal) + " " + paramStyle.Render(params)
	}

	// Status part on right
	statusPart := statusStyle.Render(statusIcon)
	if duration != "" {
		statusPart += " " + paramStyle.Render(duration)
	}

	// Calculate fill width
	contentWidth := leftWidth + lipgloss.Width(paramPart)
	statusWidth := lipgloss.Width(statusPart)
	fillWidth := width - contentWidth - statusWidth - 2
	if fillWidth < 2 {
		fillWidth = 2
	}

	fill := borderStyle.Render(strings.Repeat(connector.Horizontal, fillWidth))

	return leftPart + paramPart + " " + fill + " " + statusPart
}

// RenderToolOutput creates styled output lines with tree connector
func RenderToolOutput(lines []string, width int, colors ToolColors, maxLines int, isLast bool) []string {
	connector := DefaultTreeConnector()
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Border))
	outputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Output))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))

	var result []string

	// Determine connector character
	lineConnector := connector.Vertical
	if isLast && len(lines) == 0 {
		lineConnector = " "
	}

	prefix := borderStyle.Render(lineConnector) + "   "
	prefixWidth := 4
	contentWidth := width - prefixWidth
	if contentWidth < 20 {
		contentWidth = 20
	}

	// Truncate if needed
	displayLines := lines
	truncated := false
	if maxLines > 0 && len(lines) > maxLines {
		displayLines = lines[:maxLines]
		truncated = true
	}

	for i, line := range displayLines {
		// Use corner connector for last line if this is the last block
		if isLast && i == len(displayLines)-1 && !truncated {
			prefix = borderStyle.Render(connector.Corner) + "   "
		}

		// Wrap long lines
		if len(line) > contentWidth {
			line = line[:contentWidth-3] + "..."
		}

		result = append(result, prefix+outputStyle.Render(line))
	}

	if truncated {
		remaining := len(lines) - maxLines
		truncMsg := i18n.T("chat_b.tool.more_lines", remaining)
		lastPrefix := borderStyle.Render(connector.Corner) + "   "
		if !isLast {
			lastPrefix = borderStyle.Render(connector.Vertical) + "   "
		}
		result = append(result, lastPrefix+mutedStyle.Render(truncMsg))
	}

	return result
}

// RenderToolError creates styled error output
func RenderToolError(errorMsg string, width int, colors ToolColors) []string {
	connector := DefaultTreeConnector()
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Border))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error))

	prefix := borderStyle.Render(connector.Corner) + "   "
	prefixWidth := 4
	contentWidth := width - prefixWidth

	// Wrap error message
	lines := wrapText(errorMsg, contentWidth)
	var result []string

	for i, line := range lines {
		if i == 0 {
			result = append(result, prefix+errorStyle.Render(i18n.T("chat_b.tool.error_line", line)))
		} else {
			result = append(result, "    "+errorStyle.Render(line))
		}
	}

	return result
}

// FormatFilePath formats a file path for display
func FormatFilePath(path string, maxLen int, colors ToolColors) string {
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.FilePath))

	// Shorten path if needed
	if len(path) > maxLen {
		// Keep filename, truncate directory
		dir := filepath.Dir(path)
		base := filepath.Base(path)

		if len(base) > maxLen-4 {
			// Even filename is too long
			path = "..." + path[len(path)-maxLen+3:]
		} else {
			// Truncate directory
			availableForDir := maxLen - len(base) - 4
			if availableForDir > 0 && len(dir) > availableForDir {
				dir = "..." + dir[len(dir)-availableForDir:]
			}
			path = dir + "/" + base
		}
	}

	return pathStyle.Render(path)
}

// FormatCommand formats a shell command for display
func FormatCommand(cmd string, maxLen int, colors ToolColors) string {
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Command))

	// Remove newlines, collapse whitespace
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	cmd = strings.Join(strings.Fields(cmd), " ")

	if len(cmd) > maxLen {
		cmd = cmd[:maxLen-3] + "..."
	}

	return cmdStyle.Render(cmd)
}

// RenderHookStatus renders hook information (subtle, only shown when relevant)
func RenderHookStatus(hookName string, success bool, message string, colors ToolColors) string {
	// Hooks should be very subtle - only show on error or if specifically requested
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.HookSuccess))

	icon := "✓"
	if !success {
		mutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error))
		icon = "✗"
	}

	return mutedStyle.Render(fmt.Sprintf("    %s %s", icon, hookName))
}

// RenderDiffHeader creates a styled header for a diff block
// Format: "◆ Update(file.go) Added X lines, removed Y lines"
func RenderDiffHeader(filePath string, additions, deletions int, width int, colors ToolColors) string {
	icon := ToolIconFallback("Edit")
	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.ToolName))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.ToolName)).Bold(true)
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.FilePath))
	addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Success))
	delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Error))

	// Build header
	header := iconStyle.Render(icon) + " " + nameStyle.Render(i18n.T("chat_b.tool.update")) + "(" + pathStyle.Render(filePath) + ")"

	// Build stats
	stats := ""
	if additions > 0 || deletions > 0 {
		if additions > 0 {
			addText := "Added"
			if additions == 1 {
				addText = "Added 1 line"
			} else {
				addText = i18n.T("chat_b.tool.lines_added", additions)
			}
			stats = addStyle.Render(addText)
		}
		if deletions > 0 {
			delText := ""
			if deletions == 1 {
				delText = "removed 1 line"
			} else {
				delText = i18n.T("chat_b.tool.lines_removed", deletions)
			}
			if stats != "" {
				stats += ", "
			}
			stats += delStyle.Render(delText)
		}
	}

	if stats != "" {
		header += "\n   " + stats
	}

	return header
}

// IsEditTool checks if the tool name is an edit/write/patch tool.
// Used by debug_render.go for diff view in inspector.
func IsEditTool(toolName string) bool {
	editTools := map[string]bool{
		"Edit":        true,
		"edit":        true,
		"file_edit":   true,
		"Write":       true,
		"write":       true,
		"file_write":  true,
		"Patch":       true,
		"patch":       true,
		"apply_patch": true,
		"str_replace": true,
		"file_undo":   true,
		"MultiEdit":   true,
	}
	return editTools[toolName]
}

// isImageFile checks if a file path has an image extension
func isImageFile(filePath string) bool {
	ext := strings.ToLower(filepath.Ext(filePath))
	imageExtensions := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".gif":  true,
		".webp": true,
		".bmp":  true,
		".ico":  true,
	}
	return imageExtensions[ext]
}

// ========== Tool Aggregation for Compact Rendering ==========

// AggregatedToolCall represents a group of tool calls that can be rendered together
// when not in verbose mode.
type AggregatedToolCall struct {
	// ToolCategory is the normalized category: "read", "grep", "edit"
	ToolCategory string
	// ToolNames is the set of original tool names in this group
	ToolNames map[string]bool
	// Count is the number of tool calls in this group
	Count int
	// FilePaths collects file_path parameters for read/edit tools
	FilePaths []string
	// Pattern is the search pattern for grep tools
	Pattern string
	// IncludePattern is the include pattern for grep tools (glob filter)
	IncludePattern string
	// StartIndex is the index of the first tool call in the block list
	StartIndex int
	// EndIndex is the index of the last tool call in the block list (inclusive)
	EndIndex int
	// HasRunningTool indicates if any tool in the group is still running
	HasRunningTool bool
	// HasError indicates if any tool in the group has an error
	HasError bool
	// ToolCallIDs lists all the tool call IDs in this group
	ToolCallIDs []string
}

// CanAggregateTool returns true if the tool should be aggregated in non-verbose mode.
// Only read, grep, and edit tools are aggregated (not bash, write, etc.).
func CanAggregateTool(toolName string) bool {
	switch toolName {
	case "Read", "read", "ReadLegacy", "read_legacy", "file_read":
		return true
	case "Grep", "grep":
		return true
	case "Edit", "edit", "file_edit", "str_replace", "file_undo", "MultiEdit":
		return true
	}
	return false
}

// GetToolCategory returns the normalized category for aggregation purposes.
func GetToolCategory(toolName string) string {
	switch toolName {
	case "Read", "read", "ReadLegacy", "read_legacy", "file_read":
		return "read"
	case "Grep", "grep":
		return "grep"
	case "Edit", "edit", "file_edit", "str_replace", "file_undo", "MultiEdit":
		return "edit"
	}
	return ""
}

// GetToolIcon returns the appropriate icon for a tool category.
func GetToolIcon(toolName string) string {
	return ToolIconFallback(toolName)
}

// ExtractFileParam extracts the file_path parameter from tool params.
func ExtractFileParam(params map[string]any) string {
	if params == nil {
		return ""
	}
	if fp, ok := params["file_path"].(string); ok && fp != "" {
		return fp
	}
	if fp, ok := params["path"].(string); ok && fp != "" {
		return fp
	}
	return ""
}

// ExtractGrepParams extracts the pattern and include pattern from grep tool params.
func ExtractGrepParams(params map[string]any) (pattern string, include string) {
	if params == nil {
		return "", ""
	}
	pattern, _ = params["pattern"].(string)
	include, _ = params["include"].(string)
	return pattern, include
}

// RenderAggregatedToolSummary renders a single summary line for aggregated tool calls.
func RenderAggregatedToolSummary(agg *AggregatedToolCall, width int, colors ToolColors, isRunning bool) string {
	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.ToolName))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.ToolName)).Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))

	var icon, label string
	var detail string

	switch agg.ToolCategory {
	case "read":
		icon = ToolIconFallback("Read")
		if agg.Count == 1 {
			label = "Read"
			if len(agg.FilePaths) > 0 {
				detail = truncateFilePath(agg.FilePaths[0], width-30)
			}
		} else {
			label = "Read"
			detail = i18n.T("chat_b.tool.files", agg.Count)
		}
	case "grep":
		icon = ToolIconFallback("Grep")
		label = "Grep"
		if agg.Pattern != "" {
			detail = i18n.T("chat_b.tool.pattern", truncateDisplayString(agg.Pattern, 40))
			if agg.IncludePattern != "" {
				detail += i18n.T("chat_b.tool.in_path", agg.IncludePattern)
			}
		} else {
			// AST/symbol grep calls don't carry a "pattern" param. Show an
			// honest generic detail instead of an empty blue "Grep" line.
			if agg.Count == 1 {
				detail = i18n.T("chat_b.tool.one_search")
			} else {
				detail = i18n.T("chat_b.tool.searches", agg.Count)
			}
		}
	case "edit":
		icon = ToolIconFallback("Edit")
		if agg.Count == 1 {
			label = "Edit"
			if len(agg.FilePaths) > 0 {
				detail = truncateFilePath(agg.FilePaths[0], width-30)
			}
		} else {
			label = "Edit"
			detail = i18n.T("chat_b.tool.files", agg.Count)
		}
	default:
		// Fallback: just show count
		icon = ToolIconFallback("default")
		label = "Tool"
		detail = i18n.T("chat_b.tool.calls", agg.Count)
	}

	// Build the line
	parts := []string{
		iconStyle.Render(icon),
		nameStyle.Render(label),
	}
	if detail != "" {
		parts = append(parts, metaStyle.Render(detail))
	}

	// Add running indicator
	if isRunning {
		runningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Running))
		parts = append(parts, runningStyle.Render("◐"))
	}

	return strings.Join(parts, " ")
}

// truncateFilePath shortens a file path for display, keeping the filename visible.
func truncateFilePath(path string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(path) <= maxLen {
		return path
	}
	// Keep the filename, truncate directory
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	if len(base) > maxLen-4 {
		// Even filename is too long — slice safely
		suffix := maxLen - 3
		if suffix <= 0 {
			return path[:maxLen]
		}
		start := len(path) - suffix
		if start < 0 {
			start = 0
		}
		return "..." + path[start:]
	}

	// Truncate directory
	availableForDir := maxLen - len(base) - 4
	if availableForDir > 0 && len(dir) > availableForDir {
		dir = "..." + dir[len(dir)-availableForDir:]
	}
	return dir + "/" + base
}
