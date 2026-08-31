package chat

import (
	"strings"
	"testing"
)

// TestGetSelectedTextStripsANSIAndBorders reproduces the reported "copy adds
// garbage" bug: m.lines holds fully-rendered ANSI/lipgloss output (colors
// plus box-drawing message borders), and the previous GetSelectedText
// implementation byte-sliced that raw styled string directly, leaking
// escape sequences and border glyphs into whatever got copied to the
// clipboard. GetSelectedText must return clean, human-readable text.
func TestGetSelectedTextStripsANSIAndBorders(t *testing.T) {
	const (
		reset = "\x1b[0m"
		red   = "\x1b[38;2;255;0;0m"
	)
	styledLine := "│ " + red + "Hello" + reset + " world │"

	ml := NewMessageList(80, 10)
	ml.SetContent(styledLine)

	// Select the whole visible line (well past its rendered width so the
	// selection covers everything that was drawn).
	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   0,
		EndCol:    200,
		Active:    true,
	}

	got := ml.GetSelectedText()

	if strings.Contains(got, "\x1b") {
		t.Fatalf("GetSelectedText leaked an ANSI escape sequence: %q", got)
	}
	if strings.ContainsAny(got, "│┌┐└┘├┤─═║") {
		t.Fatalf("GetSelectedText leaked a box-drawing border glyph: %q", got)
	}
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Fatalf("GetSelectedText dropped real content, got %q", got)
	}
}

// TestGetSelectedTextMultiLine verifies multi-line selections still extract
// each line's plain text in order once ANSI styling is present on the
// interior lines (the common case: assistant messages render each line with
// their own color codes).
func TestGetSelectedTextMultiLine(t *testing.T) {
	lines := []string{
		"\x1b[32mfirst line\x1b[0m",
		"\x1b[33msecond line\x1b[0m",
		"\x1b[34mthird line\x1b[0m",
	}
	ml := NewMessageList(80, 10)
	ml.SetContent(strings.Join(lines, "\n"))

	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   2,
		EndCol:    200,
		Active:    true,
	}

	got := ml.GetSelectedText()
	want := "first line\nsecond line\nthird line"
	if got != want {
		t.Fatalf("GetSelectedText = %q, want %q", got, want)
	}
}

// TestSelectionSurvivesStreamingAppend reproduces the reported bug: a
// selection on already-completed content disappears the instant the
// actively-streaming message at the bottom grows. SetContent previously
// cleared any active selection on every content-hash change, which fires
// continuously while a response streams in. Appending new lines/text AFTER
// the selected range must not clear the selection.
func TestSelectionSurvivesStreamingAppend(t *testing.T) {
	ml := NewMessageList(80, 10)
	ml.SetContent("first message line one\nfirst message line two\n\nsecond message: ")

	// Select the (already complete) first message.
	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   1,
		EndCol:    len("first message line two"),
		Active:    true,
	}

	if !ml.IsSelectionActive() {
		t.Fatalf("selection should be active before the streaming update")
	}

	// Simulate the streaming message at the bottom growing — the earlier,
	// selected lines are byte-identical (they came from cached pre-rendered
	// lines), only the tail after the selection changes.
	ml.SetContent("first message line one\nfirst message line two\n\nsecond message: more tokens arriving")

	if !ml.IsSelectionActive() {
		t.Fatal("selection was cleared by an append that occurred entirely after the selected range (streaming-deselect bug)")
	}
	if got, want := ml.GetSelectedText(), "first message line one\nfirst message line two"; got != want {
		t.Fatalf("selected text changed after streaming append: got %q, want %q", got, want)
	}
}

// TestSelectionClearedWhenSelectedTextActuallyChanges ensures the new,
// narrower invalidation logic still clears the selection when the text it
// covers is genuinely rewritten (e.g. an error message replacing the
// selected content), rather than only ever preserving selections.
func TestSelectionClearedWhenSelectedTextActuallyChanges(t *testing.T) {
	ml := NewMessageList(80, 10)
	ml.SetContent("line one\nline two\nline three")

	ml.Selection = Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   1,
		EndCol:    len("line two"),
		Active:    true,
	}
	if !ml.IsSelectionActive() {
		t.Fatalf("selection should be active before the content rewrite")
	}

	// Rewrite the exact lines the selection covers.
	ml.SetContent("totally different\ncontent here\nline three")

	if ml.IsSelectionActive() {
		t.Fatal("selection should be cleared when the selected text is actually rewritten")
	}
}
