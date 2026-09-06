package renderer

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ToolRenderer renders tool calls and results.
type ToolRenderer struct {
	theme          theme.Theme
	styles         *theme.StyleSet
	width          int
	showFullOutput bool
}

// NewToolRenderer creates a tool renderer with the given theme and width.
func NewToolRenderer(th theme.Theme, width int) *ToolRenderer {
	return &ToolRenderer{
		theme:          th,
		styles:         theme.NewStyleSet(th),
		width:          width,
		showFullOutput: false,
	}
}

// SetWidth updates the rendering width.
func (r *ToolRenderer) SetWidth(width int) {
	r.width = width
}

// SetShowFullOutput controls output verbosity.
func (r *ToolRenderer) SetShowFullOutput(show bool) {
	r.showFullOutput = show
}

// RenderCall renders a tool invocation.
func (r *ToolRenderer) RenderCall(call *types.ToolCallDisplay) []string {
	var lines []string

	// Get tool icon
	icon := theme.ToolIcon(call.Name)

	// Build header: "├─ ◇ Read ─ file.go ────────"
	preview := r.getParamsPreview(call.Name, call.Parameters)
	header := r.buildToolHeader(icon, call.Name, preview)
	lines = append(lines, header)

	// Show parameters if expanded
	if r.showFullOutput && len(call.Parameters) > 0 {
		paramLines := r.renderParameters(call.Parameters)
		for _, pl := range paramLines {
			lines = append(lines, r.styles.ContentDim.Render("  │  ")+pl)
		}
	}

	return lines
}

// RenderResult renders a tool result.
func (r *ToolRenderer) RenderResult(result *types.ToolResultDisplay) []string {
	if result.Error != "" {
		return r.renderError(result)
	}
	return r.renderSuccess(result)
}

// renderSuccess renders a successful tool result.
func (r *ToolRenderer) renderSuccess(result *types.ToolResultDisplay) []string {
	var lines []string

	// Success indicator
	statusLine := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeCorner+"─ ") +
		r.styles.ToolSuccess.Render(i18n.T("chatui.tool.success"))
	lines = append(lines, statusLine)

	// Show output if enabled and present
	if r.showFullOutput && result.Output != "" {
		outputLines := r.formatOutput(result.Output)
		maxLines := 20 // Limit output lines
		if len(outputLines) > maxLines {
			for i := 0; i < maxLines-1; i++ {
				lines = append(lines, r.styles.ToolOutput.Render("     "+outputLines[i]))
			}
			remaining := len(outputLines) - maxLines + 1
			lines = append(lines, r.styles.ContentMuted.Render(i18n.T("chatui.tool.more_lines", remaining)))
		} else {
			for _, ol := range outputLines {
				lines = append(lines, r.styles.ToolOutput.Render("     "+ol))
			}
		}
	}

	return lines
}

// renderError renders a failed tool result.
func (r *ToolRenderer) renderError(result *types.ToolResultDisplay) []string {
	var lines []string

	// Error indicator
	statusLine := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeCorner+"─ ") +
		r.styles.ToolError.Render(i18n.T("chatui.tool.error"))
	lines = append(lines, statusLine)

	// Show error message
	errorLines := strings.SplitSeq(result.Error, "\n")
	for el := range errorLines {
		lines = append(lines, r.styles.ToolError.Render("     "+el))
	}

	return lines
}

// buildToolHeader creates the tool header line with styling.
func (r *ToolRenderer) buildToolHeader(icon, name, params string) string {
	prefix := r.styles.ContentDim.Render("  " + theme.BorderChars.TreeBranch + "─ ")
	iconPart := icon + " "
	namePart := r.styles.ToolName.Render(name)

	var paramPart string
	if params != "" {
		paramPart = r.styles.ContentDim.Render(" ─ " + params)
	}

	// Calculate remaining width for separator
	contentWidth := len("  ├─ ") + len(iconPart) + len(name) + len(" ─ ") + len(params)
	separatorWidth := max(r.width-contentWidth-4, 0)

	separator := ""
	if separatorWidth > 0 {
		separator = r.styles.ContentDim.Render(" " + strings.Repeat("─", separatorWidth))
	}

	return prefix + iconPart + namePart + paramPart + separator
}

// getParamsPreview extracts a short preview from tool parameters.
func (r *ToolRenderer) getParamsPreview(toolName string, params map[string]any) string {
	switch toolName {
	case "Read", "ReadLegacy", "file_read":
		if path, ok := params["file_path"].(string); ok {
			return truncatePath(path, 40)
		}
		if path, ok := params["path"].(string); ok {
			return truncatePath(path, 40)
		}

	case "Edit", "EditLegacy", "file_edit":
		if path, ok := params["file_path"].(string); ok {
			return truncatePath(path, 40)
		}

	case "Write", "file_write":
		if path, ok := params["file_path"].(string); ok {
			return truncatePath(path, 40)
		}
		if path, ok := params["path"].(string); ok {
			return truncatePath(path, 40)
		}

	case "Bash", "bash":
		if cmd, ok := params["command"].(string); ok {
			return truncateCommand(cmd, 50)
		}

	case "Grep", "grep":
		if pattern, ok := params["pattern"].(string); ok {
			preview := pattern
			if path, ok := params["path"].(string); ok {
				preview = i18n.T("chatui.tool.preview_in", preview, truncatePath(path, 20))
			}
			return truncate(preview, 50)
		}

	case "Glob", "glob":
		if pattern, ok := params["pattern"].(string); ok {
			return truncate(pattern, 40)
		}

	case "WebFetch", "web_fetch":
		if url, ok := params["url"].(string); ok {
			return truncate(url, 50)
		}

	case "WebSearch", "web_search":
		if query, ok := params["query"].(string); ok {
			return truncate(query, 40)
		}

	case "Task":
		if desc, ok := params["description"].(string); ok {
			return truncate(desc, 40)
		}
	}

	return ""
}

// renderParameters renders tool parameters as indented lines.
func (r *ToolRenderer) renderParameters(params map[string]any) []string {
	var lines []string

	for key, value := range params {
		var valueStr string
		switch v := value.(type) {
		case string:
			if len(v) > 100 {
				valueStr = v[:97] + "..."
			} else {
				valueStr = v
			}
		case []any:
			valueStr = i18n.T("chatui.tool.items", len(v))
		case map[string]any:
			valueStr = i18n.T("chatui.tool.keys", len(v))
		default:
			valueStr = fmt.Sprintf("%v", v)
		}

		line := r.styles.ContentDim.Render(key+": ") + r.styles.Content.Render(valueStr)
		lines = append(lines, line)
	}

	return lines
}

// formatOutput splits and formats tool output.
func (r *ToolRenderer) formatOutput(output string) []string {
	lines := strings.Split(output, "\n")
	maxWidth := r.width - 10 // Account for indentation

	var result []string
	for _, line := range lines {
		if len(line) > maxWidth {
			// Wrap long lines
			for len(line) > maxWidth {
				result = append(result, line[:maxWidth])
				line = line[maxWidth:]
			}
			if len(line) > 0 {
				result = append(result, line)
			}
		} else {
			result = append(result, line)
		}
	}

	return result
}

// truncate truncates a string to max length with ellipsis.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// truncatePath truncates a file path, keeping the filename visible.
func truncatePath(path string, max int) string {
	if len(path) <= max {
		return path
	}

	// Try to keep the filename visible
	parts := strings.Split(path, "/")
	if len(parts) > 0 {
		filename := parts[len(parts)-1]
		if len(filename) < max-4 {
			return ".../" + filename
		}
	}

	return "..." + path[len(path)-max+3:]
}

// truncateCommand truncates a bash command.
func truncateCommand(cmd string, max int) string {
	// Clean up command - remove newlines and extra spaces
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	cmd = strings.ReplaceAll(cmd, "\t", " ")

	// Collapse multiple spaces
	parts := strings.Fields(cmd)
	cmd = strings.Join(parts, " ")

	return truncate(cmd, max)
}
