package renderer

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
)

// Pre-compiled regular expressions for inline styles
var (
	boldRe   = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	italicRe = regexp.MustCompile(`\*([^*]+)\*|_([^_]+)_`)
	codeRe   = regexp.MustCompile("`([^`]+)`")
	linkRe   = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

// MarkdownRenderer renders markdown content with styling.
type MarkdownRenderer struct {
	theme  theme.Theme
	styles *theme.StyleSet
	width  int
}

// NewMarkdownRenderer creates a markdown renderer.
func NewMarkdownRenderer(th theme.Theme, width int) *MarkdownRenderer {
	return &MarkdownRenderer{
		theme:  th,
		styles: theme.NewStyleSet(th),
		width:  width,
	}
}

// SetWidth updates the rendering width.
func (r *MarkdownRenderer) SetWidth(width int) {
	r.width = width
}

// Render renders markdown content to styled lines.
func (r *MarkdownRenderer) Render(content string) []string {
	if content == "" {
		return []string{}
	}

	var result []string
	lines := strings.Split(content, "\n")

	inCodeBlock := false
	codeBlockLang := ""
	var codeBlockLines []string

	for _, line := range lines {
		// Handle code blocks
		if after, ok := strings.CutPrefix(line, "```"); ok {
			if inCodeBlock {
				// End code block
				renderedCode := r.renderCodeBlock(codeBlockLines, codeBlockLang)
				result = append(result, renderedCode...)
				inCodeBlock = false
				codeBlockLang = ""
				codeBlockLines = nil
			} else {
				// Start code block
				inCodeBlock = true
				codeBlockLang = after
			}
			continue
		}

		if inCodeBlock {
			codeBlockLines = append(codeBlockLines, line)
			continue
		}

		// Handle headers
		if after, ok := strings.CutPrefix(line, "# "); ok {
			result = append(result, r.styles.ContentBold.Render(after))
			continue
		}
		if after, ok := strings.CutPrefix(line, "## "); ok {
			result = append(result, r.styles.ContentBold.Render(after))
			continue
		}
		if after, ok := strings.CutPrefix(line, "### "); ok {
			result = append(result, r.styles.ContentBold.Render(after))
			continue
		}

		// Handle bullet points
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			bullet := r.styles.ContentDim.Render("•")
			text := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
			result = append(result, " "+bullet+" "+r.renderInlineStyles(text))
			continue
		}

		// Handle numbered lists
		if matched, _ := regexp.MatchString(`^\d+\. `, line); matched {
			result = append(result, r.renderInlineStyles(line))
			continue
		}

		// Handle blockquotes
		if after, ok := strings.CutPrefix(line, "> "); ok {
			quoted := after
			result = append(result, r.styles.ContentDim.Render("│ ")+r.styles.ContentItalic.Render(quoted))
			continue
		}

		// Handle horizontal rules
		if line == "---" || line == "***" || line == "___" {
			hr := strings.Repeat("─", r.width-4)
			result = append(result, r.styles.ContentDim.Render(hr))
			continue
		}

		// Regular line with inline styles
		if line == "" {
			result = append(result, "")
		} else {
			wrapped := r.wrapLine(r.renderInlineStyles(line))
			result = append(result, wrapped...)
		}
	}

	// Handle unclosed code block
	if inCodeBlock && len(codeBlockLines) > 0 {
		renderedCode := r.renderCodeBlock(codeBlockLines, codeBlockLang)
		result = append(result, renderedCode...)
	}

	return result
}

// renderInlineStyles applies inline markdown styling.
func (r *MarkdownRenderer) renderInlineStyles(text string) string {
	// Bold: **text** or __text__
	text = boldRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.Trim(match, "*_")
		return r.styles.ContentBold.Render(inner)
	})

	// Italic: *text* or _text_
	text = italicRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.Trim(match, "*_")
		return r.styles.ContentItalic.Render(inner)
	})

	// Inline code: `code`
	text = codeRe.ReplaceAllStringFunc(text, func(match string) string {
		inner := strings.Trim(match, "`")
		return r.styles.CodeBlock.Render(" " + inner + " ")
	})

	// Links: [text](url) - show text in blue
	text = linkRe.ReplaceAllStringFunc(text, func(match string) string {
		// Extract link text
		start := strings.Index(match, "[")
		end := strings.Index(match, "]")
		if start >= 0 && end > start {
			linkText := match[start+1 : end]
			return r.styles.FilePath.Render(linkText)
		}
		return match
	})

	return text
}

// renderCodeBlock renders a fenced code block.
func (r *MarkdownRenderer) renderCodeBlock(lines []string, language string) []string {
	var result []string

	// Header with language
	if language != "" {
		langLine := r.styles.CodeLanguage.Render("  " + language)
		result = append(result, langLine)
	}

	// Top border
	width := max(r.width-4, 20)
	topBorder := r.styles.ContentDim.Render("  " + theme.BorderChars.TopLeft + strings.Repeat(theme.BorderChars.Horizontal, width-2) + theme.BorderChars.TopRight)
	result = append(result, topBorder)

	// Code lines with line numbers
	for i, line := range lines {
		lineNum := r.styles.LineNumber.Render(padLeft(i+1, 3))
		codeLine := r.styles.CodeBlock.Render(line)

		// Truncate if too long
		if len(line) > width-8 {
			line = line[:width-11] + "..."
			codeLine = r.styles.CodeBlock.Render(line)
		}

		result = append(result, "  "+theme.BorderChars.Vertical+" "+lineNum+" "+codeLine)
	}

	// Bottom border
	bottomBorder := r.styles.ContentDim.Render("  " + theme.BorderChars.BottomLeft + strings.Repeat(theme.BorderChars.Horizontal, width-2) + theme.BorderChars.BottomRight)
	result = append(result, bottomBorder)

	return result
}

// wrapLine wraps a line to fit within the available width.
func (r *MarkdownRenderer) wrapLine(line string) []string {
	maxWidth := max(r.width-4, 20)

	// Simple length check (doesn't account for ANSI codes perfectly)
	if len(stripANSI(line)) <= maxWidth {
		return []string{line}
	}

	// Word wrap
	var result []string
	words := strings.Fields(line)
	currentLine := ""

	for _, word := range words {
		testLine := currentLine
		if currentLine != "" {
			testLine += " "
		}
		testLine += word

		if len(stripANSI(testLine)) <= maxWidth {
			currentLine = testLine
		} else {
			if currentLine != "" {
				result = append(result, currentLine)
			}
			currentLine = word
		}
	}

	if currentLine != "" {
		result = append(result, currentLine)
	}

	return result
}

// stripANSI removes ANSI escape codes from a string.
func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// padLeft pads a number with spaces to the given width.
func padLeft(n, width int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}
