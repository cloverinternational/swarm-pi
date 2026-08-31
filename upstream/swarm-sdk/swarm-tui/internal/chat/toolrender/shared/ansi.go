package shared

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ANSI true color escape codes - hardcoded to prevent terminal theme override.
// These are shared constants used across multiple rendering systems.
const (
	// Reset all attributes
	AnsiReset = "\x1b[0m"

	// True color backgrounds (24-bit) - these can't be overridden by terminal themes
	// Dark green background for additions: RGB(30, 60, 30)
	AnsiBgGreen = "\x1b[48;2;30;60;30m"
	// Dark red background for deletions: RGB(75, 30, 30) - more visible red
	AnsiBgRed = "\x1b[48;2;75;30;30m"

	// Foreground colors
	AnsiFgGreen  = "\x1b[38;2;166;227;161m" // Light green for + symbol
	AnsiFgRed    = "\x1b[38;2;243;139;168m" // Light red for - symbol
	AnsiFgDim    = "\x1b[38;2;140;140;140m" // Dim gray for line numbers
	AnsiFgWhite  = "\x1b[38;2;255;255;255m" // Bright white for code text
	AnsiFgMuted  = "\x1b[38;2;147;153;178m" // Muted for connectors
	AnsiFgBlue   = "\x1b[38;2;137;180;250m" // Blue for types/links
	AnsiFgYellow = "\x1b[38;2;249;226;175m" // Yellow for functions

	// Syntax highlighting colors (hardcoded ANSI)
	AnsiFgKeyword = "\x1b[38;2;203;166;247m" // Purple for keywords
	AnsiFgString  = "\x1b[38;2;166;227;161m" // Green for strings
	AnsiFgComment = "\x1b[38;2;108;112;134m" // Gray italic for comments
	AnsiFgNumber  = "\x1b[38;2;250;179;135m" // Orange for numbers
	AnsiFgType    = "\x1b[38;2;137;180;250m" // Blue for types
	AnsiFgFunc    = "\x1b[38;2;249;226;175m" // Yellow for functions

	// Line number color (swarm blue) - used for line numbers across all tool renderers
	AnsiFgLineNum = "\x1b[38;2;137;180;250m" // Swarm blue for line numbers

	// Text attributes
	AnsiItalic = "\x1b[3m" // Italic
	AnsiBold   = "\x1b[1m" // Bold

	// Strikethrough + dim (used for completed items)
	AnsiStrikethroughDim = "\x1b[9;2m"
)

// PrintableWidth returns the visual width of a string, ignoring ANSI escape sequences.
// This correctly handles:
//   - ANSI color codes (e.g., \x1b[31m)
//   - Multi-byte UTF-8 characters
//   - Wide characters (CJK, emoji)
//   - Combining characters
func PrintableWidth(s string) int {
	return ansi.StringWidth(s)
}

// TruncateANSI truncates a string to the specified visual width while preserving ANSI codes.
// It properly handles:
//   - ANSI escape sequences (colors, styles)
//   - Multi-byte UTF-8 characters
//   - Wide characters (CJK, emoji)
//
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

// WordWrapBreakpoints are the characters (besides spaces) that WordWrapLineWithANSI
// is allowed to break a line after. Hyphens keep compound words readable.
const WordWrapBreakpoints = "-"

// WordWrapLineWithANSI wraps a single line to fit within the specified width,
// breaking at word boundaries instead of mid-word, while preserving ANSI codes
// across wrapped segments.
//
// Unlike WrapLineWithANSI (which hard-wraps at the exact column and can split a
// word in half), this keeps whole words together. Words that are individually
// longer than the width are still broken so nothing ever overflows.
//
// Returns multiple lines if wrapping is needed.
func WordWrapLineWithANSI(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}

	// Quick check if wrapping is needed
	if ansi.StringWidth(line) <= width {
		return []string{line}
	}

	// ansi.Wrap wraps on word boundaries and force-breaks words that are
	// longer than the limit, so the result never exceeds width.
	wrapped := ansi.Wrap(line, width, WordWrapBreakpoints)
	segments := strings.Split(wrapped, "\n")

	// Defensive: if any segment still exceeds the width (pathological input
	// such as wide runes that cannot be split at the limit), fall back to a
	// hard wrap for that segment only.
	var out []string
	for _, seg := range segments {
		if ansi.StringWidth(seg) <= width {
			out = append(out, seg)
			continue
		}
		out = append(out, strings.Split(ansi.Hardwrap(seg, width, false), "\n")...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// SplitIntoLines splits content into lines, preserving ANSI codes.
// Unlike strings.Split, this properly handles ANSI sequences that span lines.
func SplitIntoLines(content string) []string {
	return strings.Split(content, "\n")
}

// SanitizeANSI removes problematic sequences that can break rendering:
// - Carriage returns (\r) - used for progress bars, cause text repositioning
// - Incomplete escape sequences (e.g., "\x1b[" or "\x1b[38" at end)
// - Non-SGR CSI sequences (cursor movement, erase: \x1b[1A, \x1b[0K, etc.)
// - OSC sequences (hyperlinks, window title: \x1b]8;;...\x07)
// - Other control characters (BEL, etc.)
//
// Preserves all valid SGR (Select Graphic Rendition) color codes:
// - \x1b[31m, \x1b[1;31m, \x1b[38;5;196m, \x1b[38;2;255;0;0m, \x1b[0m, \x1b[m
func SanitizeANSI(text string) string {
	return sanitize(text, false)
}

// SanitizeANSIKeepingHyperlinks is SanitizeANSI with one exception: OSC 8
// terminal hyperlinks are preserved, provided their target uses a scheme from
// hyperlinkSchemes.
//
// Use this ONLY for content that is about to be drawn in the viewport, never
// for raw tool output. The distinction matters because the URI in an OSC 8
// sequence is a capability: the terminal will hand it to a handler when the
// user clicks. Ordinary sanitization therefore drops hyperlinks along with
// every other OSC, and this variant re-admits exactly the ones whose target is
// inert (http/https/mailto) or a local file, after the renderers have already
// stripped anything hostile.
func SanitizeANSIKeepingHyperlinks(text string) string {
	return sanitize(text, true)
}

// hyperlinkSchemes mirrors the allowlist applied when links are created. It is
// repeated here deliberately: this is the last checkpoint before bytes reach
// the terminal, and it must hold even if a hyperlink arrives from a path that
// did not construct it through the hyperlink helpers.
var hyperlinkSchemes = []string{"http://", "https://", "mailto:", "file://"}

// isPermittedHyperlink reports whether an OSC payload is a hyperlink whose
// target may be kept. The payload looks like "8;params;URI"; an empty URI is
// the standard link terminator and is always allowed.
func isPermittedHyperlink(payload string) bool {
	if !strings.HasPrefix(payload, "8;") {
		return false
	}
	rest := payload[2:]
	semi := strings.IndexByte(rest, ';')
	if semi < 0 {
		return false
	}
	uri := rest[semi+1:]
	if uri == "" {
		return true // closing sequence
	}
	lower := strings.ToLower(uri)
	for _, scheme := range hyperlinkSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

func sanitize(text string, keepHyperlinks bool) string {
	// First pass: collapse lines with carriage returns
	// When a line contains \r, only keep the content AFTER the last \r
	// This simulates what a terminal does: \r moves cursor to start of line,
	// so subsequent text overwrites previous text
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.Contains(line, "\r") {
			// Split by \r and keep only the last non-empty segment
			parts := strings.Split(line, "\r")
			// Find the last non-empty part
			for j := len(parts) - 1; j >= 0; j-- {
				if parts[j] != "" {
					lines[i] = parts[j]
					break
				}
			}
		}
	}
	text = strings.Join(lines, "\n")

	if !strings.Contains(text, "\x1b") {
		return text // Fast path: no ANSI codes at all
	}

	var result strings.Builder
	i := 0
	for i < len(text) {
		// Look for ESC character
		if text[i] == '\x1b' {
			// Found escape sequence
			if i+1 >= len(text) {
				// Incomplete: ESC at EOF, skip it
				i++
				continue
			}

			nextChar := text[i+1]

			// ============ OSC Sequences: \x1b]...\x07 or \x1b]...\x1b\\ ============
			if nextChar == ']' {
				// OSC sequence (hyperlinks, window title, etc.)
				// Find terminator: \x07 (BEL) or \x1b\\ (ST)
				oscStart := i
				j := i + 2
				payloadEnd := -1
				for j < len(text) {
					if text[j] == '\x07' {
						// Found BEL terminator
						payloadEnd = j
						i = j + 1
						break
					}
					if j+1 < len(text) && text[j] == '\x1b' && text[j+1] == '\\' {
						// Found ST terminator
						payloadEnd = j
						i = j + 2
						break
					}
					j++
				}
				// Re-admit terminal hyperlinks when the caller asked for them
				// and the target's scheme is permitted. Every other OSC — window
				// title, clipboard writes, colour queries — is still dropped.
				if keepHyperlinks && payloadEnd > oscStart+2 {
					if payload := text[oscStart+2 : payloadEnd]; isPermittedHyperlink(payload) {
						result.WriteString(text[oscStart:i])
					}
				}
				if j >= len(text) {
					// Incomplete OSC, skip to end
					i = len(text)
				}
				continue
			}

			// ============ CSI Sequences: \x1b[...letter ============
			if nextChar == '[' {
				seqStart := i // remember where the ESC begins; i is mutated below
				j := i + 2
				isValidSGR := true

				// Scan to find the end of the sequence and determine type
				for j < len(text) {
					ch := text[j]
					// Check if this is the final character (letter)
					if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') {
						// Found end of CSI sequence
						finalChar := ch

						// SGR sequences end with 'm'
						// All others are non-SGR (cursor movement, erase, etc.)
						if finalChar != 'm' {
							// Non-SGR CSI: skip entire sequence
							isValidSGR = false
						}

						// Also validate the content is actually SGR-like
						// SGR content should only contain: digits, semicolons, and ?
						if finalChar == 'm' {
							params := text[i+2 : j]
							for _, c := range params {
								if !((c >= '0' && c <= '9') || c == ';' || c == '?') {
									// Invalid SGR content
									isValidSGR = false
									break
								}
							}
						}

						i = j + 1
						break
					}

					// If we hit a character that's not part of a valid CSI, stop
					if !((ch >= '0' && ch <= '9') || ch == ';' || ch == '?' || ch == ':') {
						// Invalid CSI sequence, skip just the \x1b
						isValidSGR = false
						i = i + 1
						break
					}

					j++
				}

				if j >= len(text) {
					// Incomplete CSI sequence, skip it
					i = len(text)
					continue
				}

				if isValidSGR {
					// This is a valid SGR sequence, keep it. Use seqStart because
					// i was already advanced to j+1 when the sequence ended.
					result.WriteString(text[seqStart : j+1])
				}
				// If not valid SGR, we already skipped it above
				continue
			}

			// ============ Other sequences starting with \x1b ============
			// Like \x1b( for character set selection, etc. - skip these too
			// These are usually followed by one character
			if nextChar >= 0x30 && nextChar <= 0x7E {
				// Skip single-char sequences
				i += 2
				continue
			}

			// Unknown \x1b sequence, skip just the escape
			i++
			continue
		}

		// Regular character, keep it
		result.WriteByte(text[i])
		i++
	}

	return result.String()
}

// ReapplyBackground replaces ANSI resets with reset+background to ensure background continuity.
// CRITICAL: Only prepend background if not already present to avoid duplicates.
func ReapplyBackground(text string, bgColor string) string {
	if bgColor == "" {
		return text
	}

	// Get the ANSI sequence for the theme background
	// We render a space and extract the sequence before the space
	style := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
	rendered := style.Render(" ")
	// rendered is like: <seq> <space> <reset>
	// We want <seq>
	// Find the space
	before, _, ok := strings.Cut(rendered, " ")
	if !ok {
		return text
	}
	bgSeq := before

	// Check if the text already starts with this background sequence
	// If so, don't prepend (avoids duplicates)
	if strings.HasPrefix(text, bgSeq) {
		// Already has background, just replace resets
		replaced := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+bgSeq)
		replaced = strings.ReplaceAll(replaced, "\x1b[m", "\x1b[m"+bgSeq)
		return replaced
	}

	// Replace resets with reset + bgSeq
	// Lipgloss typically uses \x1b[0m
	replaced := strings.ReplaceAll(text, "\x1b[0m", "\x1b[0m"+bgSeq)
	// Just in case it uses the shorter \x1b[m
	replaced = strings.ReplaceAll(replaced, "\x1b[m", "\x1b[m"+bgSeq)

	// CRITICAL: Prepend bgSeq to ensure the very start of the line has the background
	// Without this, any leading characters (spaces, plain text) before the first
	// ANSI code will fall through to the terminal default background
	return bgSeq + replaced
}

// JoinWithNewlines joins lines back together.
func JoinWithNewlines(lines []string) string {
	return strings.Join(lines, "\n")
}
