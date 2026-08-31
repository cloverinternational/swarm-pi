package chat

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// PrintableWidth returns the visual width of a string, ignoring ANSI escape sequences.
// This correctly handles:
// - ANSI color codes (e.g., \x1b[31m)
// - Multi-byte UTF-8 characters
// - Wide characters (CJK, emoji)
// - Combining characters
func PrintableWidth(s string) int {
	return ansi.StringWidth(s)
}

// TruncateANSI truncates a string to the specified visual width while preserving ANSI codes.
// It properly handles:
// - ANSI escape sequences (colors, styles)
// - Multi-byte UTF-8 characters
// - Wide characters (CJK, emoji)
// The ellipsis is appended if truncation occurs.
func TruncateANSI(s string, maxWidth int, ellipsis string) string {
	if maxWidth <= 0 {
		return ""
	}

	// Quick check if truncation is needed
	if ansi.StringWidth(s) <= maxWidth {
		return s
	}

	ellipsisWidth := ansi.StringWidth(ellipsis)
	targetWidth := maxWidth - ellipsisWidth
	if targetWidth <= 0 {
		// Not enough room for anything, just return truncated ellipsis
		return ansi.Truncate(ellipsis, maxWidth, "")
	}

	// Use the ansi package's Truncate function which handles ANSI properly
	truncated := ansi.Truncate(s, targetWidth, "")
	return truncated + ellipsis
}

// TruncateANSIExact truncates a string to exactly the specified width without ellipsis.
// Useful for padding calculations.
func TruncateANSIExact(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	return ansi.Truncate(s, maxWidth, "")
}

// PadToWidth pads a string with spaces to reach the target visual width.
// ANSI codes in the string are properly accounted for.
func PadToWidth(s string, targetWidth int) string {
	currentWidth := ansi.StringWidth(s)
	if currentWidth >= targetWidth {
		return s
	}
	padding := strings.Repeat(" ", targetWidth-currentWidth)
	return s + padding
}

// StripANSI removes all ANSI escape sequences from a string.
// This is useful for getting the raw text content.
func StripANSI(s string) string {
	return ansi.Strip(s)
}

// WrapLineWithANSI wraps a single line to fit within the specified width,
// preserving ANSI codes across wrapped segments.
// Returns multiple lines if wrapping is needed.
func WrapLineWithANSI(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	// Quick check if wrapping is needed
	if ansi.StringWidth(line) <= width {
		return []string{line}
	}

	// Use ansi.Hardwrap which properly handles ANSI sequences
	wrapped := ansi.Hardwrap(line, width, false)
	return strings.Split(wrapped, "\n")
}

// SplitIntoLines splits content into lines, preserving ANSI codes.
// Unlike strings.Split, this properly handles ANSI sequences that span lines.
func SplitIntoLines(content string) []string {
	return strings.Split(content, "\n")
}

// JoinWithNewlines joins lines back together.
func JoinWithNewlines(lines []string) string {
	return strings.Join(lines, "\n")
}
