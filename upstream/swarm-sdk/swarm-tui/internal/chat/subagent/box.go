package subagent

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// SubAgentBoxConfig configures the bordered box appearance for sub-agents
type SubAgentBoxConfig struct {
	// BorderColor is the color of the box border
	BorderColor string
	// HeaderColor is the color for the box header/title area
	HeaderColor string
	// BackgroundColor is the background inside the box (empty for transparent)
	BackgroundColor string
	// PaddingH is horizontal padding inside the box
	PaddingH int
	// PaddingV is vertical padding inside the box
	PaddingV int
	// MinWidth is the minimum box width
	MinWidth int
}

// DefaultSubAgentBoxConfig returns sensible defaults for sub-agent boxes
func DefaultSubAgentBoxConfig() SubAgentBoxConfig {
	return SubAgentBoxConfig{
		BorderColor:     ColorSubAgentIdentity, // identity cyan for all sub-agent borders
		HeaderColor:     ColorSubAgentIdentity, // identity cyan for header text
		BackgroundColor: "",                    // Transparent
		PaddingH:        1,
		PaddingV:        0,
		MinWidth:        30,
	}
}

// SubAgentBoxRenderer renders bordered boxes for sub-agent content
type SubAgentBoxRenderer struct {
	config SubAgentBoxConfig
}

// NewSubAgentBoxRenderer creates a new box renderer with given config
func NewSubAgentBoxRenderer(config SubAgentBoxConfig) *SubAgentBoxRenderer {
	return &SubAgentBoxRenderer{config: config}
}

// NewDefaultSubAgentBoxRenderer creates a box renderer with default config
func NewDefaultSubAgentBoxRenderer() *SubAgentBoxRenderer {
	return NewSubAgentBoxRenderer(DefaultSubAgentBoxConfig())
}

// GetConfig returns the current configuration
func (r *SubAgentBoxRenderer) GetConfig() SubAgentBoxConfig {
	return r.config
}

// SetConfig updates the configuration
func (r *SubAgentBoxRenderer) SetConfig(config SubAgentBoxConfig) {
	r.config = config
}

// RenderBox wraps content in a bordered box
// content: pre-rendered content lines
// width: available width for the box
// Returns: the boxed content as a single string
func (r *SubAgentBoxRenderer) RenderBox(content []string, width int) string {
	if len(content) == 0 {
		return ""
	}

	// Calculate content width (account for border and padding)
	contentWidth := max(
		// 2 for borders
		width-2-(r.config.PaddingH*2), r.config.MinWidth)

	// Join content
	contentStr := strings.Join(content, "\n")

	// Create box style
	boxStyle := lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(r.config.BorderColor)).
		Padding(r.config.PaddingV, r.config.PaddingH)

	// Apply background if configured
	if r.config.BackgroundColor != "" {
		boxStyle = boxStyle.Background(lipgloss.Color(r.config.BackgroundColor))
	}

	return boxStyle.Render(contentStr)
}

// RenderBoxWithHeader wraps content in a bordered box with a header line
// header: the header text (e.g., agent name)
// content: pre-rendered content lines
// width: available width for the box
// isStreaming: whether to show streaming indicator in header
// Returns: the boxed content as a single string
func (r *SubAgentBoxRenderer) RenderBoxWithHeader(header string, content []string, width int, isStreaming bool) string {
	// Calculate content width
	contentWidth := max(width-2-(r.config.PaddingH*2), r.config.MinWidth)

	// Build header with optional streaming indicator
	headerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(r.config.HeaderColor)).
		Bold(true)

	headerText := headerStyle.Render(header)
	if isStreaming {
		streamIndicator := lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColorSubAgentIdentity)).
			Render(" [streaming]")
		headerText += streamIndicator
	}

	// Combine header and content
	var allContent []string
	allContent = append(allContent, headerText)
	if len(content) > 0 {
		allContent = append(allContent, "") // Separator
		allContent = append(allContent, content...)
	}

	contentStr := strings.Join(allContent, "\n")

	// Create box style
	boxStyle := lipgloss.NewStyle().
		Width(contentWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(r.config.BorderColor)).
		Padding(r.config.PaddingV, r.config.PaddingH)

	if r.config.BackgroundColor != "" {
		boxStyle = boxStyle.Background(lipgloss.Color(r.config.BackgroundColor))
	}

	return boxStyle.Render(contentStr)
}

// WrapLinesInBox takes content lines and returns lines with box decoration
// This is useful when you need line-by-line output instead of a single string
// content: pre-rendered content lines
// width: available width for the box
// Returns: slice of lines representing the boxed content
func (r *SubAgentBoxRenderer) WrapLinesInBox(content []string, width int) []string {
	boxed := r.RenderBox(content, width)
	if boxed == "" {
		return nil
	}
	return strings.Split(boxed, "\n")
}

// RenderMinimalBox renders a simpler box with just top/bottom borders
// Useful for inline sub-agent content that shouldn't feel too heavy
// content: pre-rendered content lines
// width: available width
// Returns: content with horizontal rule above and below
func (r *SubAgentBoxRenderer) RenderMinimalBox(content []string, width int) []string {
	if len(content) == 0 {
		return nil
	}

	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(r.config.BorderColor))

	// Create horizontal rule
	ruleWidth := max(width-4, r.config.MinWidth)
	rule := borderStyle.Render(strings.Repeat("─", ruleWidth))

	var result []string
	result = append(result, rule)
	result = append(result, content...)
	result = append(result, rule)

	return result
}
