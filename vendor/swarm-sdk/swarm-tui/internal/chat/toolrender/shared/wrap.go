package shared

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// truncateToVisualWidth truncates a string to fit within the given visual width,
// properly handling multi-byte Unicode characters. It returns the truncated string.
func truncateToVisualWidth(s string, maxVisualWidth int) string {
	if maxVisualWidth <= 0 {
		return ""
	}

	currentWidth := 0
	for i, r := range s {
		charWidth := runewidth.RuneWidth(r)
		if currentWidth+charWidth > maxVisualWidth {
			return s[:i]
		}
		currentWidth += charWidth
	}
	return s
}

// WrapText wraps text to fit within the specified width, preserving explicit newlines
// and performing word-level wrapping. Long words that exceed the width are broken
// into chunks.
//
// This is a direct export of the wrapText function from the chat package (components.go).
//
// Parameters:
//   - text: the input text to wrap (may contain newlines)
//   - width: maximum line width in characters (visual width, not byte length)
//
// Returns a slice of wrapped lines. Always returns at least one line.
func WrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}

	// Preserve newlines - split by newline first
	inputLines := strings.Split(text, "\n")
	var result []string

	for _, line := range inputLines {
		// For each line, word wrap it
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
		currentVisualWidth := 0
		for _, word := range words {
			wordVisualWidth := lipgloss.Width(word)
			// Check if word is too long - handle gracefully by breaking it
			if wordVisualWidth > width {
				// Word is too long for the width
				if current.Len() > 0 {
					// Flush current line first
					result = append(result, current.String())
					current.Reset()
					currentVisualWidth = 0
				}
				// Break long word into chunks using visual width
				remaining := word
				for lipgloss.Width(remaining) > width {
					chunk := truncateToVisualWidth(remaining, width)
					result = append(result, chunk)
					remaining = remaining[len(chunk):]
				}
				if len(remaining) > 0 {
					current.WriteString(remaining)
					currentVisualWidth = lipgloss.Width(remaining)
				}
			} else if current.Len() == 0 {
				// Start of new line
				current.WriteString(word)
				currentVisualWidth = wordVisualWidth
			} else if currentVisualWidth+1+wordVisualWidth <= width {
				// Word fits on current line with space
				current.WriteString(" ")
				current.WriteString(word)
				currentVisualWidth += 1 + wordVisualWidth
			} else {
				// Word doesn't fit - start new line
				result = append(result, current.String())
				current.Reset()
				current.WriteString(word)
				currentVisualWidth = wordVisualWidth
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

// WrapTextWithIndent wraps text while preserving indentation for continuation lines.
// The indent string is prepended to all lines after the first one.
//
// This is a direct export of the wrapTextWithIndent function from the chat package
// (components.go). It ensures text never wraps around — always respects indent.
//
// Parameters:
//   - text: the input text to wrap (may contain newlines)
//   - width: maximum line width in characters
//   - indent: string prepended to all continuation lines (can contain ANSI codes)
//
// Returns a slice of wrapped lines. The first line has no indent prefix; subsequent
// lines are prefixed with the indent string. Always returns at least one line.
func WrapTextWithIndent(text string, width int, indent string) []string {
	if width <= 0 {
		return []string{text}
	}

	// Calculate indent length (strip ANSI codes for accurate measurement)
	indentLen := len(StripANSI(indent))

	// First line gets full width
	firstLineWidth := width
	// Continuation lines get reduced width (total width minus indent)
	continuationWidth := max(width-indentLen,
		// Minimum usable width
		10)

	// Split text by newlines first to preserve explicit line breaks
	inputLines := strings.Split(text, "\n")
	var result []string

	for lineIdx, line := range inputLines {
		if lineIdx == 0 {
			// First line of entire text - use full width
			wrapped := WrapText(line, firstLineWidth)
			if len(wrapped) > 0 {
				result = append(result, wrapped[0])
				// Remaining wrapped parts get continuation indent
				for i := 1; i < len(wrapped); i++ {
					result = append(result, indent+wrapped[i])
				}
			}
		} else {
			// Subsequent lines - wrap with continuation width and add indent
			wrapped := WrapText(line, continuationWidth)
			for _, w := range wrapped {
				result = append(result, indent+w)
			}
		}
	}

	if len(result) == 0 {
		return []string{""}
	}

	return result
}
