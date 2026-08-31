package chat

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
)

const mermaidMaxViewportWidth = 120

// mermaidViewport describes the terminal-cell widths available to a Mermaid
// renderer. Outer includes the border, Inner excludes it, and Content excludes
// both the border and the optional one-cell padding inside it.
type mermaidViewport struct {
	Available int
	Outer     int
	Inner     int
	Content   int
	Tiny      bool
	Compact   bool
	Wide      bool
}

func newMermaidViewport(width int) mermaidViewport {
	available := width
	if available < 1 {
		available = 1
	}

	outer := available
	if outer > mermaidMaxViewportWidth {
		outer = mermaidMaxViewportWidth
	}

	inner := outer - 2
	if inner < 1 {
		inner = 1
	}

	content := inner - 2
	if content < 1 {
		content = 1
	}

	return mermaidViewport{
		Available: available,
		Outer:     outer,
		Inner:     inner,
		Content:   content,
		Tiny:      available < 24,
		Compact:   available < 48,
		Wide:      available >= 88,
	}
}

// mermaidTruncateCell truncates s to width terminal cells. ANSI escape
// sequences do not consume width and are kept well-formed by ansi.Truncate.
func mermaidTruncateCell(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

// mermaidWrapCell hard-wraps s at terminal-cell boundaries. Explicit newlines,
// including empty logical lines, are preserved. A non-positive width has one
// deterministic, zero-cell result instead of exposing unbounded input.
func mermaidWrapCell(s string, width int) []string {
	if width <= 0 {
		return []string{""}
	}

	logicalLines := strings.Split(s, "\n")
	wrapped := make([]string, 0, len(logicalLines))
	for _, line := range logicalLines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		if ansi.StringWidth(line) <= width {
			wrapped = append(wrapped, line)
			continue
		}
		wordWrapped := strings.Split(ansi.Wordwrap(line, width, " \t-"), "\n")
		for _, segment := range wordWrapped {
			if ansi.StringWidth(segment) <= width {
				wrapped = append(wrapped, segment)
				continue
			}
			wrapped = append(wrapped, strings.Split(ansi.Hardwrap(segment, width, false), "\n")...)
		}
	}
	return wrapped
}

// mermaidPadCell returns exactly width terminal cells, truncating first when
// necessary and appending unstyled spaces when the value is shorter.
func mermaidPadCell(s string, width int) string {
	if width <= 0 {
		return ""
	}

	s = mermaidTruncateCell(s, width)
	padding := width - ansi.StringWidth(s)
	if padding <= 0 {
		return s
	}
	return s + strings.Repeat(" ", padding)
}

// mermaidSanitizeLines removes terminal control sequences supplied inside
// Mermaid source. Renderer-owned ANSI styling is applied only after this step.
func mermaidSanitizeLines(lines []string) []string {
	sanitized := make([]string, len(lines))
	for i, line := range lines {
		stripped := ansi.Strip(line)
		sanitized[i] = strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, stripped)
	}
	return sanitized
}
