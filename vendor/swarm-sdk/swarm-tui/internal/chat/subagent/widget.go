package subagent

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// SubAgentWidgetConfig configures sub-agent widget appearance
type SubAgentWidgetConfig struct {
	// Symbols
	CollapsedSymbol string // Symbol when collapsed (e.g., "▶")
	ExpandedSymbol  string // Symbol when expanded (e.g., "▼")
	AgentIcon       string // Icon for sub-agent (e.g., "◆")

	// Tree connectors
	TreeTee      string // ├──
	TreeCorner   string // ╰──
	TreeVertical string // │

	// Colors
	AgentNameColor string
	MetadataColor  string
	FocusColor     string
	ErrorColor     string
	ConnectorColor string
	OutputColor    string
	ToolColor      string
	BorderColor    string // Color for box border

	// Layout
	MaxToolOutputLines int // Max lines per tool output (default: 3)
	IndentWidth        int // Characters per indent level

	// Streaming settings
	MaxStreamLines int // Max lines during streaming (default: 50)
}

// DefaultSubAgentWidgetConfig returns sensible defaults
func DefaultSubAgentWidgetConfig() SubAgentWidgetConfig {
	return SubAgentWidgetConfig{
		CollapsedSymbol:    "▶",
		ExpandedSymbol:     "▼",
		AgentIcon:          "◆",
		TreeTee:            "├──",
		TreeCorner:         "╰──",
		TreeVertical:       "│",
		AgentNameColor:     ColorSubAgentIdentity, // single canonical identity color
		MetadataColor:      palette.TextMuted,
		FocusColor:         ColorSubAgentIdentity,
		ErrorColor:         palette.Error,
		ConnectorColor:     palette.TextMuted,
		OutputColor:        palette.TextDim,
		ToolColor:          palette.Accent,
		BorderColor:        ColorSubAgentIdentity, // identity color for sub-agent borders
		MaxToolOutputLines: 3,
		IndentWidth:        4,
		MaxStreamLines:     50,
	}
}

// SubAgentWidget renders sub-agent blocks with collapsible hierarchy
type SubAgentWidget struct {
	config SubAgentWidgetConfig
}

// NewSubAgentWidget creates a new widget with given config
func NewSubAgentWidget(config SubAgentWidgetConfig) *SubAgentWidget {
	return &SubAgentWidget{config: config}
}

// NewDefaultSubAgentWidget creates widget with defaults
func NewDefaultSubAgentWidget() *SubAgentWidget {
	return NewSubAgentWidget(DefaultSubAgentWidgetConfig())
}

// GetConfig returns current config
func (w *SubAgentWidget) GetConfig() SubAgentWidgetConfig {
	return w.config
}

// SetConfig updates the config
func (w *SubAgentWidget) SetConfig(config SubAgentWidgetConfig) {
	w.config = config
}

// RenderHeader renders the sub-agent header line with collapse indicator
// Returns: "├── ◆ ▶ Agent Name [3 tools, 45 lines]" or similar
func (w *SubAgentWidget) RenderHeader(state *SubAgentState, isLast bool) string {
	// Styles
	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	agentStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.AgentNameColor)).Bold(true)
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.MetadataColor))

	// Focus styling
	if state != nil && state.IsFocused {
		agentStyle = agentStyle.Foreground(lipgloss.Color(w.config.FocusColor))
	}

	// Error styling
	if state != nil && state.HasErrors {
		agentStyle = agentStyle.Foreground(lipgloss.Color(w.config.ErrorColor))
	}

	// Tree connector
	connector := w.config.TreeTee
	if isLast {
		connector = w.config.TreeCorner
	}

	// Collapse symbol
	symbol := w.config.CollapsedSymbol
	if state != nil && state.IsExpanded() {
		symbol = w.config.ExpandedSymbol
	}

	// Agent name
	name := i18n.T("classic_chat_3.subagent.unknown_agent")
	if state != nil {
		name = state.AgentName
	}

	// Metadata
	var meta []string
	if state != nil {
		if state.ToolCount > 0 {
			meta = append(meta, i18n.T("chat_b.subagent.tools", state.ToolCount))
		}
		if state.TotalLines > 0 && !state.IsExpanded() {
			meta = append(meta, i18n.T("chat_b.subagent.lines", state.TotalLines))
		}
		if state.HasErrors {
			meta = append(meta, i18n.T("classic_chat_3.subagent.errors"))
		}
	}

	metaStr := ""
	if len(meta) > 0 {
		metaStr = " " + metaStyle.Render("["+strings.Join(meta, ", ")+"]")
	}

	// Build header
	return connectorStyle.Render(connector+" ") +
		agentStyle.Render(w.config.AgentIcon+" "+symbol+" "+name) +
		metaStr
}

// RenderCollapsedSummary renders hint when sub-agent is collapsed
func (w *SubAgentWidget) RenderCollapsedSummary(state *SubAgentState) []string {
	if state == nil || state.IsExpanded() {
		return nil
	}

	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.config.MetadataColor)).
		Italic(true)

	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))

	hint := i18n.T("classic_chat_3.subagent.expand_hint")

	return []string{
		connectorStyle.Render(w.config.TreeVertical) + "   " + metaStyle.Render(hint),
	}
}

// RenderNestedToolCall renders a tool call inside sub-agent with proper indentation
func (w *SubAgentWidget) RenderNestedToolCall(toolName string, params map[string]any,
	hasResult bool, isLast bool, isStreaming bool, spinner string) string {

	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ToolColor))
	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.MetadataColor))

	// Vertical line continuation from parent
	prefix := connectorStyle.Render(w.config.TreeVertical + "   ")

	// Nested tree connector
	nestedConnector := w.config.TreeTee
	if isLast {
		nestedConnector = w.config.TreeCorner
	}

	// Bullet (spinner or static)
	bullet := "○"
	if isStreaming && !hasResult {
		bullet = spinner
	}

	// Build param string
	paramStr := w.formatParams(params)

	toolLine := prefix + connectorStyle.Render(nestedConnector+" ") +
		toolStyle.Render(bullet+" "+toolName)

	if paramStr != "" {
		toolLine += " " + metaStyle.Render(paramStr)
	}

	return toolLine
}

// RenderNestedToolOutput renders tool output with truncation
func (w *SubAgentWidget) RenderNestedToolOutput(output string, isError bool, width int) []string {
	if output == "" {
		return nil
	}

	lines := strings.Split(output, "\n")
	maxLines := w.config.MaxToolOutputLines

	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))

	var outputStyle lipgloss.Style
	if isError {
		outputStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ErrorColor))
	} else {
		outputStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.OutputColor))
	}

	// Prefix for nested output: "│       ⎿ "
	prefix := connectorStyle.Render(w.config.TreeVertical) + "       "
	contPrefix := connectorStyle.Render(w.config.TreeVertical) + "         "

	var result []string

	displayLines := lines
	truncated := false
	if len(lines) > maxLines {
		displayLines = lines[:maxLines]
		truncated = true
	}

	for i, line := range displayLines {
		// Truncate long lines
		availWidth := max(width-12, 20)
		if len(line) > availWidth {
			line = line[:availWidth-3] + "..."
		}

		if i == 0 {
			label := ""
			if isError {
				label = i18n.T("classic_chat_3.subagent.error_prefix")
			}
			result = append(result, prefix+connectorStyle.Render("⎿")+" "+outputStyle.Render(label+line))
		} else {
			result = append(result, contPrefix+outputStyle.Render(line))
		}
	}

	if truncated {
		remaining := len(lines) - maxLines
		metaStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(w.config.MetadataColor)).
			Italic(true)
		result = append(result, contPrefix+metaStyle.Render(i18n.T("chat_b.subagent.more_lines", remaining)))
	}

	return result
}

// RenderThinkingBlock renders thinking content with tree connector
func (w *SubAgentWidget) RenderThinkingBlock(content string, width int) []string {
	if content == "" {
		return nil
	}

	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	thinkingStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(w.config.OutputColor)).
		Italic(true)

	prefix := connectorStyle.Render(w.config.TreeVertical + "   ")

	// Wrap text
	availWidth := max(width-8, 20)

	wrapped := wrapText(content, availWidth)

	var result []string
	for i, line := range wrapped {
		if i == 0 {
			result = append(result, prefix+thinkingStyle.Render("⎿ "+line))
		} else {
			result = append(result, prefix+"  "+thinkingStyle.Render(line))
		}
	}

	return result
}

// RenderContentBlock renders content with tree connector
func (w *SubAgentWidget) RenderContentBlock(lines []string) []string {
	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	prefix := connectorStyle.Render(w.config.TreeVertical + "   ")

	var result []string
	for _, line := range lines {
		result = append(result, prefix+line)
	}
	return result
}

// formatParams formats parameters for display
func (w *SubAgentWidget) formatParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}

	// Priority order for display
	if cmd, ok := params["command"].(string); ok {
		return truncateSubAgentStr(cmd, 50)
	}
	if path, ok := params["file_path"].(string); ok {
		return truncateSubAgentStr(path, 50)
	}
	if path, ok := params["path"].(string); ok {
		return truncateSubAgentStr(path, 50)
	}
	if pattern, ok := params["pattern"].(string); ok {
		return truncateSubAgentStr(pattern, 40)
	}

	// Show first param
	for k, v := range params {
		return fmt.Sprintf("%s=%v", k, truncateSubAgentStr(fmt.Sprint(v), 40))
	}

	return ""
}

// truncateSubAgentStr truncates string with ellipsis
func truncateSubAgentStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// RenderHeaderWithAnimation renders the header with pulsing status text during streaming
// state: the sub-agent state
// isLast: whether this is the last block in the message
// frame: current animation frame from AnimationClock
// spinner: the spinner string for active tools
// Returns: styled header string with optional pulsing status
func (w *SubAgentWidget) RenderHeaderWithAnimation(state *SubAgentState, isLast bool, frame int, spinner string) string {
	// Base header
	baseHeader := w.RenderHeader(state, isLast)

	// Add pulsing status if streaming and collapsed
	if state != nil && state.IsStreaming && !state.IsExpanded() {
		status := state.StreamingStatus
		if status == "" {
			status = "Working"
		}

		// Use pulsing text with animated dots
		pulseConfig := uitypes.PulseTextConfig{
			BaseColor:    w.config.FocusColor,
			DimColor:     w.config.MetadataColor,
			PulseSpeed:   0.15,
			MinIntensity: 0.3,
			MaxIntensity: 1.0,
		}

		pulsingStatus := " " + uitypes.RenderPulseTextWithDots(status, frame, pulseConfig)
		return baseHeader + pulsingStatus
	}

	return baseHeader
}

// RenderCollapsedWithStatus renders collapsed view with spinner and pulsing status
// state: the sub-agent state
// spinner: the spinner string for animation
// frame: current animation frame
// Returns: lines for the collapsed view
func (w *SubAgentWidget) RenderCollapsedWithStatus(state *SubAgentState, spinner string, frame int) []string {
	if state == nil {
		return nil
	}

	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	prefix := connectorStyle.Render(w.config.TreeVertical + "   ")

	if state.IsStreaming {
		// Show spinner with pulsing status
		status := state.StreamingStatus
		if status == "" {
			status = "Working"
		}

		pulseConfig := uitypes.PulseTextConfig{
			BaseColor:    w.config.FocusColor,
			DimColor:     w.config.MetadataColor,
			PulseSpeed:   0.15,
			MinIntensity: 0.3,
			MaxIntensity: 1.0,
		}

		statusLine := prefix + spinner + " " + uitypes.RenderPulseTextWithDots(status, frame, pulseConfig)
		return []string{statusLine}
	}

	// Not streaming - show static summary
	return w.RenderCollapsedSummary(state)
}

// RenderExpandedWithBox renders expanded content wrapped in a bordered box
// content: pre-rendered content lines
// state: the sub-agent state
// width: available width
// frame: current animation frame
// Returns: lines with box border
func (w *SubAgentWidget) RenderExpandedWithBox(content []string, state *SubAgentState, width int, frame int) []string {
	if len(content) == 0 {
		return nil
	}

	// Apply streaming truncation if needed
	if state != nil && state.IsStreaming && len(content) > w.config.MaxStreamLines {
		// Truncate and add indicator
		displayContent := content[:w.config.MaxStreamLines]
		remaining := len(content) - w.config.MaxStreamLines

		truncStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(w.config.MetadataColor)).
			Italic(true)
		truncLine := truncStyle.Render(i18n.T("chat_b.subagent.truncated_lines", remaining))
		displayContent = append(displayContent, truncLine)

		// Add streaming marker
		pulseConfig := uitypes.DefaultPulseTextConfig()
		streamMarker := uitypes.RenderPulseTextWithDots("streaming", frame, pulseConfig)
		displayContent = append(displayContent, "", streamMarker)

		content = displayContent
	}

	// Check if border should be shown
	if state == nil || !state.ShowBorder {
		// Return content with tree connectors (no box)
		connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
		prefix := connectorStyle.Render(w.config.TreeVertical + "   ")

		var result []string
		for _, line := range content {
			result = append(result, prefix+line)
		}
		return result
	}

	// Render with box border
	boxRenderer := NewSubAgentBoxRenderer(SubAgentBoxConfig{
		BorderColor:     w.config.BorderColor,
		HeaderColor:     w.config.AgentNameColor,
		BackgroundColor: "", // Transparent
		PaddingH:        1,
		PaddingV:        0,
		MinWidth:        30,
	})

	boxed := boxRenderer.RenderBox(content, width-4) // Account for tree prefix
	if boxed == "" {
		return nil
	}

	// Add tree connector prefix to each boxed line
	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	prefix := connectorStyle.Render(w.config.TreeVertical + " ")

	boxedLines := strings.Split(boxed, "\n")
	result := make([]string, len(boxedLines))
	for i, line := range boxedLines {
		result[i] = prefix + line
	}

	return result
}

// RenderStreamingIndicator renders a standalone streaming indicator line
// frame: current animation frame
// status: optional status text
// Returns: styled streaming indicator line
func (w *SubAgentWidget) RenderStreamingIndicator(frame int, status string) string {
	connectorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(w.config.ConnectorColor))
	prefix := connectorStyle.Render(w.config.TreeVertical + "   ")

	if status == "" {
		status = "streaming"
	}

	pulseConfig := uitypes.PulseTextConfig{
		BaseColor:    w.config.FocusColor,
		DimColor:     w.config.MetadataColor,
		PulseSpeed:   0.15,
		MinIntensity: 0.3,
		MaxIntensity: 1.0,
	}

	return prefix + uitypes.RenderPulseTextWithDots(status, frame, pulseConfig)
}

// wrapText wraps text to fit within the specified width
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	inputLines := strings.Split(text, "\n")
	var result []string

	for _, line := range inputLines {
		if len(line) == 0 {
			result = append(result, "")
			continue
		}

		words := strings.Fields(line)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}

		var current strings.Builder
		for _, word := range words {
			if len(word) > width {
				if current.Len() > 0 {
					result = append(result, current.String())
					current.Reset()
				}
				for len(word) > width {
					result = append(result, word[:width])
					word = word[width:]
				}
				if len(word) > 0 {
					current.WriteString(word)
				}
			} else if current.Len() == 0 {
				current.WriteString(word)
			} else if current.Len()+1+len(word) <= width {
				current.WriteString(" ")
				current.WriteString(word)
			} else {
				result = append(result, current.String())
				current.Reset()
				current.WriteString(word)
			}
		}

		if current.Len() > 0 {
			result = append(result, current.String())
		}
	}

	if len(result) == 0 {
		return []string{""}
	}

	return result
}
