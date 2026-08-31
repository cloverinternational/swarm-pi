package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// padViewportFast mirrors the inlined replacement in renderChatContent for the
// lipgloss Width/Height/Padding style that used to render the viewport.
func padViewportFast(content string, height int) string {
	lines := strings.Split(content, "\n")
	padTo := height
	if len(lines) > padTo {
		padTo = len(lines)
	}
	var b strings.Builder
	b.Grow(len(content) + 3*padTo)
	for i := 0; i < padTo; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("  ")
		if i < len(lines) {
			b.WriteString(lines[i])
		}
	}
	return b.String()
}

// The old code padded the viewport with lipgloss, then app_view.go ran
// normalizeFrameForTerminal over the composed frame anyway. This asserts the
// cheap version is byte-identical to the lipgloss version once that
// normalization has run — which is the only output the terminal ever sees.
func TestViewportPaddingMatchesLipglossAfterNormalize(t *testing.T) {
	const width = 40
	cases := []struct {
		name    string
		content string
		height  int
	}{
		{"plain", "hello\nworld", 4},
		{"empty lines", "a\n\n\nb", 5},
		{"exact height", "one\ntwo\nthree", 3},
		{"content taller than height", "1\n2\n3\n4\n5", 2},
		{"single empty string", "", 3},
		{"ansi styled", "\x1b[31mred\x1b[0m\nplain", 3},
		{"wide runes", "日本語テキスト\nascii", 3},
		{"emoji grapheme cluster", "👨‍👩‍👧‍👦 family\nx", 3},
		{"trailing newline", "a\nb\n", 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := lipgloss.NewStyle().
				Width(width).
				Height(tc.height).
				Padding(0, 2).
				Render(tc.content)

			gotOld := normalizeFrameForTerminal(old, width, tc.height)
			gotNew := normalizeFrameForTerminal(padViewportFast(tc.content, tc.height), width, tc.height)

			if gotOld != gotNew {
				t.Errorf("normalized output differs\n old: %q\n new: %q", gotOld, gotNew)
			}
		})
	}
}

// Line count feeds a.inputOverlayY via countLines(viewportRendered), so it must
// not drift from what lipgloss produced.
func TestViewportPaddingLineCountMatchesLipgloss(t *testing.T) {
	const width = 40
	for _, tc := range []struct {
		content string
		height  int
	}{
		{"hello\nworld", 6},
		{"", 3},
		{"1\n2\n3\n4\n5", 2},
		{"only", 1},
	} {
		old := lipgloss.NewStyle().
			Width(width).Height(tc.height).Padding(0, 2).Render(tc.content)
		oldN := strings.Count(old, "\n")
		newN := strings.Count(padViewportFast(tc.content, tc.height), "\n")
		if oldN != newN {
			t.Errorf("line count drift for %q h=%d: lipgloss=%d new=%d",
				tc.content, tc.height, oldN+1, newN+1)
		}
	}
}
