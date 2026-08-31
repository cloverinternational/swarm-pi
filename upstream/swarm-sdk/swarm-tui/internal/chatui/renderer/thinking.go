package renderer

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ThinkingRenderer renders extended thinking blocks.
type ThinkingRenderer struct {
	theme  theme.Theme
	styles *theme.StyleSet
	width  int
}

// NewThinkingRenderer creates a thinking renderer.
func NewThinkingRenderer(th theme.Theme, width int) *ThinkingRenderer {
	return &ThinkingRenderer{
		theme:  th,
		styles: theme.NewStyleSet(th),
		width:  width,
	}
}

// SetWidth updates the rendering width.
func (r *ThinkingRenderer) SetWidth(width int) {
	r.width = width
}

// Render renders a thinking block.
// If isBlock is true, this is a streaming block; otherwise it's the message-level thinking.
func (r *ThinkingRenderer) Render(content string, isBlock bool) []string {
	if content == "" {
		return []string{}
	}

	var lines []string

	// Header
	header := r.styles.ThinkingHeader.Render(i18n.T("chatui.thinking.title"))
	if isBlock {
		header = r.styles.ContentDim.Render("  ") + header
	}
	lines = append(lines, header)

	// Content with styling
	contentLines := r.wrapContent(content)
	for _, line := range contentLines {
		styledLine := r.styles.Thinking.Render("  │ " + line)
		lines = append(lines, styledLine)
	}

	// Footer
	footer := r.styles.Thinking.Render("  └─")
	lines = append(lines, footer)

	return lines
}

// RenderCollapsed renders a collapsed thinking indicator.
func (r *ThinkingRenderer) RenderCollapsed(wordCount int) []string {
	summary := r.styles.ThinkingHeader.Render(i18n.T("chatui.thinking.title")) +
		r.styles.ContentMuted.Render(i18n.T("chatui.thinking.collapsed"))

	return []string{summary}
}

// wrapContent wraps content to fit within the available width.
func (r *ThinkingRenderer) wrapContent(content string) []string {
	maxWidth := max(
		// Account for border and padding
		r.width-10, 20)

	var result []string

	paragraphs := strings.Split(content, "\n\n")
	for i, para := range paragraphs {
		if i > 0 {
			result = append(result, "") // Empty line between paragraphs
		}

		lines := strings.SplitSeq(para, "\n")
		for line := range lines {
			wrappedLines := r.wrapLine(line, maxWidth)
			result = append(result, wrappedLines...)
		}
	}

	return result
}

// wrapLine wraps a single line to the given width.
func (r *ThinkingRenderer) wrapLine(line string, maxWidth int) []string {
	if len(line) <= maxWidth {
		return []string{line}
	}

	var result []string
	words := strings.Fields(line)
	currentLine := ""

	for _, word := range words {
		if currentLine == "" {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= maxWidth {
			currentLine += " " + word
		} else {
			result = append(result, currentLine)
			currentLine = word
		}
	}

	if currentLine != "" {
		result = append(result, currentLine)
	}

	return result
}

// CountWords counts the number of words in content.
func CountWords(content string) int {
	return len(strings.Fields(content))
}
