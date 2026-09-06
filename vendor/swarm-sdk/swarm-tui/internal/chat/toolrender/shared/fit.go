package shared

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// TabWidth is the tab stop interval assumed for tool output. It matches the
// bash renderer's own expandTabs and the default of every mainstream terminal.
const TabWidth = 4

// ExpandTabsANSI replaces tab characters with the spaces a terminal would draw,
// advancing to the next TabWidth-aligned tab stop.
//
// WHY: every width function we have (ansi.StringWidth, lipgloss.Width,
// runewidth) reports a tab as ONE column. A real terminal expands it to the
// next tab stop, so a line that measured as "fits" shears every glyph to the
// right of the tab out of the pane — and in a bordered layout, shears the
// border itself. Tabs must therefore be expanded BEFORE any measurement or
// truncation, and they must be expanded on the final assembled line so the
// column count starts at the pane's true left edge.
//
// ANSI escape sequences occupy zero columns, so they are copied through without
// advancing the column counter. Anything else advances by its display width.
func ExpandTabsANSI(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}

	var b strings.Builder
	b.Grow(len(s) + 16)

	col := 0
	rest := s
	for len(rest) > 0 {
		// Copy any escape sequence verbatim: it draws nothing.
		if rest[0] == '\x1b' || rest[0] == '\x9b' {
			seqLen := escapeSequenceLen(rest)
			b.WriteString(rest[:seqLen])
			rest = rest[seqLen:]
			continue
		}
		r, size := decodeRune(rest)
		if r == '\t' {
			spaces := TabWidth - (col % TabWidth)
			b.WriteString(strings.Repeat(" ", spaces))
			col += spaces
		} else {
			b.WriteString(rest[:size])
			col += ansi.StringWidth(rest[:size])
		}
		rest = rest[size:]
	}
	return b.String()
}

// escapeSequenceLen returns the byte length of the escape sequence at the head
// of s. It handles the seven-bit CSI ("\x1b["), the eight-bit C1 CSI (0x9b),
// OSC strings, and short two-byte escapes. An unterminated sequence consumes
// the remainder so the scanner always makes progress.
func escapeSequenceLen(s string) int {
	if s[0] == '\x9b' {
		return 1 + csiBodyLen(s[1:])
	}
	if len(s) < 2 {
		return len(s)
	}
	switch s[1] {
	case '[':
		return 2 + csiBodyLen(s[2:])
	case ']':
		// OSC: a command string that runs until BEL or ST (ESC \).
		//
		// Any OTHER control byte aborts it, for the same reason csiBodyLen
		// aborts: an OSC string may only contain printable bytes, and a
		// terminal acts on an embedded control byte rather than absorbing it.
		// The fuzzer found "\x1b]\t": scanning to end-of-string swallowed the
		// TAB into a pseudo-sequence, so it escaped tab expansion and sheared
		// the line. Aborting consumes only "\x1b]" and lets the TAB be
		// expanded normally.
		for i := 2; i < len(s); i++ {
			switch {
			case s[i] == '\x07':
				return i + 1
			case s[i] == '\x1b':
				if i+1 < len(s) && s[i+1] == '\\' {
					return i + 2 // ST terminator
				}
				return i // a new escape starts here; stop
			case s[i] < 0x20:
				return i // stray control byte aborts the string
			}
		}
		return len(s)
	default:
		// ESC + one final byte (0x30-0x7E), optionally preceded by
		// intermediates (0x20-0x2F): a complete two-byte escape such as ESC c
		// or ESC ( B.
		//
		// Anything else — notably ESC followed by a CONTROL character — is not
		// part of a sequence. Consuming it would swallow the control byte
		// verbatim: the fuzzer found "\x1b\t", where treating the pair as an
		// escape passed a raw TAB straight through to the terminal, which then
		// expanded it to a tab stop and sheared the line. Consume only the ESC
		// and let the next iteration handle the byte on its own merits.
		if s[1] >= 0x20 && s[1] <= 0x7e {
			return 2
		}
		return 1
	}
}

// csiBodyLen returns the byte length of a CSI body: parameter and intermediate
// bytes followed by one final byte in the range 0x40-0x7E.
//
// A byte outside 0x20-0x7E ABORTS the sequence rather than continuing it. Per
// ECMA-48 a control byte inside a CSI terminates it, and the terminal then
// acts on that control byte. The fuzzer found "\x1b[\t0": scanning to the end
// of the string swallowed the TAB into a pseudo-sequence, so it survived tab
// expansion and sheared the line on a real terminal. Aborting consumes only
// "\x1b[" and lets the TAB be expanded normally.
func csiBodyLen(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return i
		}
		if s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
	}
	return len(s)
}

// decodeRune returns the first rune of s and its byte length, treating an
// invalid byte as a single-byte rune so the caller always advances.
//
// It MUST report the number of bytes CONSUMED, not the encoded length of the
// decoded rune. `for _, r := range s` yields utf8.RuneError for an invalid or
// truncated sequence while advancing only one byte, but len(string(RuneError))
// is 3 — so the old implementation returned 3 for a one-byte input and the
// caller's rest[:size] panicked with "slice bounds out of range". A truncated
// multi-byte character at the end of a chunked/clipped tool output line (e.g.
// the two leading bytes of a three-byte rune) reproduces it exactly.
func decodeRune(s string) (rune, int) {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 {
		return 0, len(s)
	}
	return r, size
}

// FitLine clamps one fully assembled, styled output line to the pane width.
//
// WHY THIS IS NEEDED AT THE LINE LEVEL: every renderer here computes a content
// width as max(width-gutter, someMinimum) — for example max(width-8, 30). That
// minimum is a floor, not a cap, so on any pane narrower than the floor the
// renderer happily emits content wider than the pane, and the fixed gutter
// ("    ⎿ ", six columns) overflows on top of that. Clamping the finished line
// is the one place that is guaranteed to see the real terminal line, gutter and
// all, so it is the only place the bound can actually be enforced.
//
// A width of zero or less means "unconstrained" (the caller does not know the
// viewport) and the line is returned untouched apart from tab expansion.
//
// When the line is truncated a reset is appended so a clipped SGR sequence
// cannot bleed its color into the rest of the frame.
func FitLine(line string, width int) string {
	line = ExpandTabsANSI(line)
	if width <= 0 {
		return line
	}
	if ansi.StringWidth(line) <= width {
		return line
	}
	return ansi.Truncate(line, width, "") + AnsiReset
}

// FitLines applies FitLine to every line of a rendered block.
func FitLines(lines []string, width int) []string {
	for i, line := range lines {
		lines[i] = FitLine(line, width)
	}
	return lines
}
