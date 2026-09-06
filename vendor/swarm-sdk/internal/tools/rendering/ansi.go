package rendering

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/ansi"
)

// ansiStripRegex is used to remove ANSI escape codes
var ansiStripRegex = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes ANSI escape codes from a string
func stripANSI(s string) string {
	return ansiStripRegex.ReplaceAllString(s, "")
}

// ANSIRenderable renders content with ANSI escape codes preserved
type ANSIRenderable struct {
	Content        string
	BaseStyle      lipgloss.Style
	PreserveColors bool
	options        RenderOptions
}

// NewANSIRenderable creates a new ANSI renderable
func NewANSIRenderable(content string, options RenderOptions) *ANSIRenderable {
	return &ANSIRenderable{
		Content:        content,
		BaseStyle:      lipgloss.NewStyle(),
		PreserveColors: options.PreserveColors,
		options:        options,
	}
}

// Render implements the Renderable interface
func (a *ANSIRenderable) Render(width int, maxLines int) []string {
	if !a.PreserveColors {
		// Strip ANSI codes if not preserving colors
		stripped := stripANSI(a.Content)
		return a.renderPlain(stripped, width, maxLines)
	}

	// Split into lines while preserving ANSI codes
	lines := strings.Split(a.Content, "\n")

	var result []string
	for i, line := range lines {
		if maxLines > 0 && i >= maxLines {
			// Add truncation indicator
			remaining := len(lines) - maxLines
			if remaining > 0 {
				result = append(result, lipgloss.NewStyle().
					Foreground(lipgloss.Color("240")).
					Render("... ("+formatNumber(remaining)+" more lines)"))
			}
			break
		}

		// Wrap line if necessary while preserving ANSI codes
		if a.options.WrapLines && ansi.PrintableRuneWidth(line) > width {
			wrapped := a.wrapLineWithANSI(line, width)
			result = append(result, wrapped...)
		} else {
			result = append(result, line)
		}
	}

	return result
}

// Style implements the Renderable interface
func (a *ANSIRenderable) Style() lipgloss.Style {
	return a.BaseStyle
}

// IsCollapsible implements the Renderable interface
func (a *ANSIRenderable) IsCollapsible() bool {
	return strings.Count(a.Content, "\n") > 10
}

// ContentType implements the Renderable interface
func (a *ANSIRenderable) ContentType() ContentType {
	return ContentTypeANSI
}

// renderPlain renders content as plain text
func (a *ANSIRenderable) renderPlain(content string, width int, maxLines int) []string {
	lines := strings.Split(content, "\n")

	var result []string
	for i, line := range lines {
		if maxLines > 0 && i >= maxLines {
			remaining := len(lines) - maxLines
			if remaining > 0 {
				result = append(result, "... ("+formatNumber(remaining)+" more lines)")
			}
			break
		}

		if a.options.WrapLines && len(line) > width {
			wrapped := wrapText(line, width)
			result = append(result, wrapped...)
		} else {
			result = append(result, line)
		}
	}

	return result
}

// wrapLineWithANSI wraps a line that contains ANSI codes
func (a *ANSIRenderable) wrapLineWithANSI(line string, width int) []string {
	// Get the printable width
	printableWidth := ansi.PrintableRuneWidth(line)

	if printableWidth <= width {
		return []string{line}
	}

	// For now, use a simple approach: split at width boundaries
	// A more sophisticated approach would track ANSI state across splits
	var wrapped []string
	currentLine := ""
	currentWidth := 0

	// Parse the line to extract ANSI sequences and text
	parser := &ansiParser{input: line}

	for {
		token := parser.next()
		if token == nil {
			break
		}

		if token.isANSI {
			// ANSI codes don't count toward width, just append
			currentLine += token.text
		} else {
			// Regular text - check width
			tokenWidth := ansi.PrintableRuneWidth(token.text)

			if currentWidth+tokenWidth > width {
				// Need to wrap
				if currentLine != "" {
					wrapped = append(wrapped, currentLine)
				}
				currentLine = token.text
				currentWidth = tokenWidth
			} else {
				currentLine += token.text
				currentWidth += tokenWidth
			}
		}
	}

	if currentLine != "" {
		wrapped = append(wrapped, currentLine)
	}

	return wrapped
}

// ansiParser is a simple parser for ANSI escape sequences
type ansiParser struct {
	input string
	pos   int
}

type ansiToken struct {
	text   string
	isANSI bool
}

// next returns the next token (ANSI sequence or regular text)
func (p *ansiParser) next() *ansiToken {
	if p.pos >= len(p.input) {
		return nil
	}

	// Check if current position is an ANSI escape sequence
	if p.pos < len(p.input) && p.input[p.pos] == '\x1b' {
		// Find the end of the ANSI sequence
		end := p.pos + 1
		if end < len(p.input) && p.input[end] == '[' {
			end++
			// CSI sequence - find the terminator
			for end < len(p.input) {
				ch := p.input[end]
				if ch >= 0x40 && ch <= 0x7E {
					end++
					break
				}
				end++
			}
		}

		token := &ansiToken{
			text:   p.input[p.pos:end],
			isANSI: true,
		}
		p.pos = end
		return token
	}

	// Regular text - read until next ANSI sequence or end
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != '\x1b' {
		p.pos++
	}

	return &ansiToken{
		text:   p.input[start:p.pos],
		isANSI: false,
	}
}

// PlainTextRenderable renders plain text without special formatting
type PlainTextRenderable struct {
	Content   string
	BaseStyle lipgloss.Style
	options   RenderOptions
}

// NewPlainTextRenderable creates a new plain text renderable
func NewPlainTextRenderable(content string, style lipgloss.Style, options RenderOptions) *PlainTextRenderable {
	return &PlainTextRenderable{
		Content:   content,
		BaseStyle: style,
		options:   options,
	}
}

// Render implements the Renderable interface
func (p *PlainTextRenderable) Render(width int, maxLines int) []string {
	lines := strings.Split(p.Content, "\n")

	var result []string
	for i, line := range lines {
		if maxLines > 0 && i >= maxLines {
			remaining := len(lines) - maxLines
			if remaining > 0 {
				result = append(result, "... ("+formatNumber(remaining)+" more lines)")
			}
			break
		}

		if p.options.WrapLines && len(line) > width {
			wrapped := wrapText(line, width)
			result = append(result, wrapped...)
		} else {
			result = append(result, line)
		}
	}

	return result
}

// Style implements the Renderable interface
func (p *PlainTextRenderable) Style() lipgloss.Style {
	return p.BaseStyle
}

// IsCollapsible implements the Renderable interface
func (p *PlainTextRenderable) IsCollapsible() bool {
	return strings.Count(p.Content, "\n") > 10
}

// ContentType implements the Renderable interface
func (p *PlainTextRenderable) ContentType() ContentType {
	return ContentTypePlainText
}

// wrapText wraps text to the specified width
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}

	var lines []string
	currentLine := ""

	for _, word := range words {
		// If word is longer than width, split it
		if len(word) > width {
			if currentLine != "" {
				lines = append(lines, currentLine)
				currentLine = ""
			}

			// Split long word
			for len(word) > width {
				lines = append(lines, word[:width])
				word = word[width:]
			}

			if len(word) > 0 {
				currentLine = word
			}
			continue
		}

		// Try to add word to current line
		if currentLine == "" {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return lines
}

// formatNumber formats a number with commas
func formatNumber(n int) string {
	if n < 1000 {
		return string(rune(n + '0'))
	}
	return strings.TrimSpace(lipgloss.NewStyle().Render(string(rune(n))))
}
