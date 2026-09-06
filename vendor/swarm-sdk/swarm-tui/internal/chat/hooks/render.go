package hooks

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ColorHookIdentity is the canonical color for hook UI elements.
// This distinct purple/orange color provides visual differentiation from
// sub-agents (cyan) and tools (varied).
const ColorHookIdentity = "#A78BFA" // Purple - hooks are "watchers"

// HookCategory defines the type of hook for visual differentiation
type HookCategory int

const (
	// HookCategorySecurity are enforcement/blocking hooks (highest visibility)
	HookCategorySecurity HookCategory = iota
	// HookCategoryLogging are data collection hooks
	HookCategoryLogging
	// HookCategoryAnalysis are processing/analyzer hooks
	HookCategoryAnalysis
	// HookCategorySystem are lifecycle/session hooks
	HookCategorySystem
	// HookCategoryUser are custom user-defined hooks
	HookCategoryUser
)

// String returns the human-readable category name
func (c HookCategory) String() string {
	switch c {
	case HookCategorySecurity:
		return "security"
	case HookCategoryLogging:
		return "logging"
	case HookCategoryAnalysis:
		return "analysis"
	case HookCategorySystem:
		return "system"
	case HookCategoryUser:
		return "user"
	default:
		return "unknown"
	}
}

// CategoryColor returns the theme color for this hook category
func (c HookCategory) CategoryColor() string {
	switch c {
	case HookCategorySecurity:
		return palette.Error // Red for enforcement
	case HookCategoryLogging:
		return palette.Info // Blue for logging
	case HookCategoryAnalysis:
		return palette.AccentSoft // Purple for analysis
	case HookCategorySystem:
		return palette.Teal // Teal for system
	case HookCategoryUser:
		return ColorHookIdentity // Yellow for user hooks
	default:
		return palette.TextDim
	}
}

// CategoryIcon returns the icon for this hook category
func (c HookCategory) CategoryIcon() string {
	switch c {
	case HookCategorySecurity:
		return "⛔"
	case HookCategoryLogging:
		return "◉"
	case HookCategoryAnalysis:
		return "◈"
	case HookCategorySystem:
		return "◇"
	case HookCategoryUser:
		return "⚙"
	default:
		return "◌"
	}
}

// HookExecutionDisplay represents a hook execution for UI rendering
type HookExecutionDisplay struct {
	HookName          string
	ToolName          string
	Phase             string // "before" or "after"
	Success           bool
	Output            string
	Blocked           bool
	Error             string
	Duration          int // milliseconds
	ExitCode          int
	MatchedPattern    string
	TimeoutConfigured int    // seconds
	StartedAt         int64  // Unix timestamp (milliseconds)
	Status            string // "started", "running", "completed"
}

// HookRenderer renders hook execution blocks in the TUI
type HookRenderer struct {
	width              int
	showFullOutput     bool
	identityStyle      lipgloss.Style
	dimStyle           lipgloss.Style
	mutedStyle         lipgloss.Style
	successStyle       lipgloss.Style
	errorStyle         lipgloss.Style
	connectorStyle     lipgloss.Style
	toolSpecificStyles map[string]lipgloss.Style
}

// NewHookRenderer creates a new hook renderer with default styles
func NewHookRenderer(width int, showFullOutput bool) *HookRenderer {
	r := &HookRenderer{
		width:          width,
		showFullOutput: showFullOutput,
	}

	r.identityStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(ColorHookIdentity)).
		Bold(true)

	r.dimStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	r.mutedStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextMuted))

	r.successStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Success))

	r.errorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Error))

	r.connectorStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Border))

	// Tool-specific colors (same as sub-agent renderer)
	r.toolSpecificStyles = map[string]lipgloss.Style{
		"Read":     lipgloss.NewStyle().Foreground(lipgloss.Color("#39D2C0")), // Teal
		"Write":    lipgloss.NewStyle().Foreground(lipgloss.Color("#F4C95D")), // Yellow
		"Edit":     lipgloss.NewStyle().Foreground(lipgloss.Color("#F4C95D")), // Yellow
		"Bash":     lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")), // Purple
		"Grep":     lipgloss.NewStyle().Foreground(lipgloss.Color("#4C9AFF")), // Blue
		"Glob":     lipgloss.NewStyle().Foreground(lipgloss.Color("#4C9AFF")), // Blue
		"Task":     lipgloss.NewStyle().Foreground(lipgloss.Color("#6E64E8")), // Accent
		"Subagent": lipgloss.NewStyle().Foreground(lipgloss.Color("#39D2C0")), // Cyan
	}

	return r
}

// SetWidth updates the rendering width
func (r *HookRenderer) SetWidth(width int) {
	r.width = width
}

// SetShowFullOutput toggles verbose output mode
func (r *HookRenderer) SetShowFullOutput(show bool) {
	r.showFullOutput = show
}

// DetectCategory determines the hook category from its name
func DetectCategory(hookName string) HookCategory {
	hookLower := strings.ToLower(hookName)

	// Security/Enforcement hooks
	securityHooks := []string{
		"task-enforcement",
		"verification-protocol",
		"post-acting",
		"safety",
		"block",
		"guardrail",
	}
	for _, pattern := range securityHooks {
		if strings.Contains(hookLower, pattern) {
			return HookCategorySecurity
		}
	}

	// Logging hooks
	loggingHooks := []string{
		"bronze-event",
		"logging",
		"audit",
		"telemetry",
	}
	for _, pattern := range loggingHooks {
		if strings.Contains(hookLower, pattern) {
			return HookCategoryLogging
		}
	}

	// Analysis hooks
	analysisHooks := []string{
		"tool-result-analysis",
		"findings-analyzer",
		"dream-steering",
		"analysis",
		"analyzer",
	}
	for _, pattern := range analysisHooks {
		if strings.Contains(hookLower, pattern) {
			return HookCategoryAnalysis
		}
	}

	// System hooks
	systemHooks := []string{
		"session-start",
		"session-end",
		"init",
		"cleanup",
	}
	for _, pattern := range systemHooks {
		if strings.Contains(hookLower, pattern) {
			return HookCategorySystem
		}
	}

	return HookCategoryUser
}

// RenderHook renders a hook execution block
func (r *HookRenderer) RenderHook(hook *HookExecutionDisplay, isStreaming bool, isLast bool) []string {
	if hook == nil {
		return nil
	}

	category := DetectCategory(hook.HookName)
	var lines []string

	// Line 1: Summary line with tree connector
	lines = append(lines, r.renderSummaryLine(hook, category, isLast))

	// Line 2: Status/activity line with ⏿ glyph
	lines = append(lines, r.renderActivityLine(hook, category, isStreaming, isLast))

	// Additional lines for output if verbose mode or important
	if r.shouldShowOutput(hook, category) && hook.Output != "" {
		outputLines := r.renderOutput(hook.Output, isLast)
		lines = append(lines, outputLines...)
	}

	// Error line if present
	if hook.Error != "" {
		lines = append(lines, r.renderErrorLine(hook.Error, isLast))
	}

	return lines
}

// renderSummaryLine produces the first line:
//
//	"  ├─ " hook-icon hook-name [tool-name] · phase · status
func (r *HookRenderer) renderSummaryLine(hook *HookExecutionDisplay, category HookCategory, isLast bool) string {
	// Prefix: 2-space indent + tree char
	treeChar := "├─"
	if isLast {
		treeChar = "└─"
	}
	prefix := r.connectorStyle.Render("  " + treeChar + " ")

	// Category icon
	icon := category.CategoryIcon()

	// Hook name with identity color
	hookName := r.identityStyle.Render(icon + " " + hook.HookName)

	// Tool indicator (if tool-matched)
	toolPart := ""
	if hook.ToolName != "" && hook.MatchedPattern != "" {
		toolStyle := r.getToolStyle(hook.ToolName)
		toolPart = " " + r.dimStyle.Render("[") +
			toolStyle.Render(hook.ToolName) +
			r.dimStyle.Render("]")
	}

	// Phase indicator
	phaseStr := ""
	if hook.Phase != "" {
		phaseIcon := "▸"
		if hook.Phase == "before" {
			phaseIcon = "◂"
		}
		phaseStr = " " + r.mutedStyle.Render(phaseIcon+" "+hook.Phase)
	}

	// Status icon
	statusIcon := r.getStatusIcon(hook, category)

	return prefix + hookName + toolPart + phaseStr + " " + statusIcon
}

// renderActivityLine produces the second line with ⏿ status:
//
//	"  │  ⏿ " duration · exit-code · matched-pattern
func (r *HookRenderer) renderActivityLine(hook *HookExecutionDisplay, category HookCategory, isStreaming bool, isLast bool) string {
	// Prefix: continuation with ⏿ glyph
	var contPrefix string
	if isLast {
		contPrefix = "     ⏿  " // 5 spaces + ⏿ + 2 spaces
	} else {
		contPrefix = "  │  ⏿  " // 2 spaces + │ + 2 spaces + ⏿ + 2 spaces
	}

	parts := []string{}

	// Duration
	if hook.Duration > 0 {
		durStr := formatDurationMs(hook.Duration)
		parts = append(parts, r.mutedStyle.Render(durStr))
	}

	// Exit code (only show if non-zero or explicitly configured)
	if hook.ExitCode != 0 || r.showFullOutput {
		exitStyle := r.mutedStyle
		if hook.ExitCode != 0 {
			exitStyle = r.errorStyle
		}
		parts = append(parts, exitStyle.Render(i18n.T("chat_b.hook.exit_code", hook.ExitCode)))
	}

	// Matched pattern (brief)
	if hook.MatchedPattern != "" && len(hook.MatchedPattern) < 30 {
		parts = append(parts, r.mutedStyle.Render(i18n.T("chat_b.hook.match", hook.MatchedPattern)))
	}

	// Current status for streaming
	if isStreaming && hook.Status == "running" {
		parts = append(parts, r.dimStyle.Render(i18n.T("chat_b.hook.running")))
	}

	// Join parts with separator
	content := ""
	if len(parts) > 0 {
		sep := r.dimStyle.Render(" · ")
		content = strings.Join(parts, sep)
	}

	return r.dimStyle.Render(contPrefix) + content
}

// renderOutput formats hook output for display
func (r *HookRenderer) renderOutput(output string, isLast bool) []string {
	var lines []string
	outputLines := strings.Split(output, "\n")

	// Limit output lines based on mode
	maxLines := 3
	if r.showFullOutput {
		maxLines = 20
	}

	if len(outputLines) > maxLines {
		outputLines = outputLines[:maxLines-1]
		outputLines = append(outputLines, "…")
	}

	prefix := "  │  "
	if isLast {
		prefix = "     "
	}

	for _, line := range outputLines {
		// Truncate long lines
		if len(line) > r.width-10 {
			line = line[:r.width-13] + "…"
		}
		styled := r.mutedStyle.Render(prefix + line)
		lines = append(lines, styled)
	}

	return lines
}

// renderErrorLine formats an error line
func (r *HookRenderer) renderErrorLine(error string, isLast bool) string {
	prefix := "  │  "
	if isLast {
		prefix = "     "
	}

	// Truncate error if too long
	if len(error) > r.width-15 {
		error = error[:r.width-18] + "…"
	}

	return r.errorStyle.Render(prefix+i18n.T("chat_b.hook.error_prefix")) + r.errorStyle.Render(error)
}

// getStatusIcon returns the appropriate status icon
func (r *HookRenderer) getStatusIcon(hook *HookExecutionDisplay, category HookCategory) string {
	if hook.Blocked {
		return r.errorStyle.Render(i18n.T("chat_b.hook.blocked"))
	}
	if !hook.Success {
		return r.errorStyle.Render(i18n.T("chat_b.hook.failed"))
	}
	return r.successStyle.Render("✓")
}

// getToolStyle returns the style for a tool name
func (r *HookRenderer) getToolStyle(toolName string) lipgloss.Style {
	// Try exact match
	if style, ok := r.toolSpecificStyles[toolName]; ok {
		return style
	}

	// Try partial match
	for name, style := range r.toolSpecificStyles {
		if strings.Contains(toolName, name) {
			return style
		}
	}

	return r.dimStyle
}

// shouldShowOutput determines if hook output should be displayed
func (r *HookRenderer) shouldShowOutput(hook *HookExecutionDisplay, category HookCategory) bool {
	// Always show for security hooks (enforcement messages are critical)
	if category == HookCategorySecurity {
		return true
	}

	// Always show if blocked
	if hook.Blocked {
		return true
	}

	// Show if error
	if hook.Error != "" {
		return true
	}

	// Otherwise respect the setting
	return r.showFullOutput
}

// formatDurationMs formats milliseconds into a compact string
func formatDurationMs(ms int) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", m, s)
}
