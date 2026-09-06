package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPasteIndicatorFormat(t *testing.T) {
	cases := []struct {
		text string
		seq  int
		want string
	}{
		{"hello", 1, "[#1 Pasted 1 line]"},
		{"a\nb\nc", 2, "[#2 Pasted 3 lines]"},
		{strings.Repeat("x\n", 1233) + "x", 7, "[#7 Pasted 1,234 lines]"},
	}
	for _, c := range cases {
		if got := getPastedTextPrompt(c.text, c.seq); got != c.want {
			t.Errorf("getPastedTextPrompt(%q,%d) = %q, want %q", c.text, c.seq, got, c.want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	cases := map[int]string{0: "0", 12: "12", 123: "123", 1234: "1,234", 1234567: "1,234,567"}
	for n, want := range cases {
		if got := formatCount(n); got != want {
			t.Errorf("formatCount(%d) = %q, want %q", n, got, want)
		}
	}
}

// Two identical-length pastes must remain individually restorable because each
// indicator carries a unique sequence number.
func TestPasteUniqueIndicatorsRestoreCorrectly(t *testing.T) {
	in := NewSimpleInput()
	in.Focus()
	in.Update(tea.PasteMsg{Content: "AAA"})
	in.Update(tea.PasteMsg{Content: "BBB"})
	if got := in.GetSubmitValue(); got != "AAABBB" {
		t.Fatalf("submit = %q, want %q", got, "AAABBB")
	}
	// Indicators are distinct.
	if in.pasteEntries[0].indicator == in.pasteEntries[1].indicator {
		t.Fatalf("indicators collided: %q", in.pasteEntries[0].indicator)
	}
}

// A paste over an active selection replaces the selected text.
func TestPasteReplacesSelection(t *testing.T) {
	in := NewSimpleInput()
	in.Focus()
	in.SetValue("hello world")
	in.SetWidth(120)
	// Select "hello".
	in.StartSelection(0)
	in.UpdateSelectionEnd(5)
	in.EndSelection()
	in.Update(tea.PasteMsg{Content: "X"})
	// "hello" replaced by the paste indicator, " world" preserved.
	if !strings.HasSuffix(in.Value(), " world") {
		t.Fatalf("expected ' world' suffix, got %q", in.Value())
	}
	if strings.Contains(in.Value(), "hello") {
		t.Fatalf("selection not replaced, value=%q", in.Value())
	}
	if got := in.GetSubmitValue(); got != "X world" {
		t.Fatalf("submit = %q, want %q", got, "X world")
	}
}

// InsertNewline replaces an active selection (consistent with typing).
func TestInsertNewlineReplacesSelection(t *testing.T) {
	in := NewSimpleInput()
	in.Focus()
	in.SetValue("abcdef")
	in.SetWidth(120)
	in.StartSelection(2)
	in.UpdateSelectionEnd(4) // select "cd"
	in.EndSelection()
	in.InsertNewline()
	if in.Value() != "ab\nef" {
		t.Fatalf("InsertNewline over selection = %q, want %q", in.Value(), "ab\nef")
	}
	if in.HasSelection() {
		t.Fatal("selection should be cleared after InsertNewline")
	}
}

// Styled paste chips must survive a round-trip: stripping ANSI yields the chip.
func TestStylePasteIndicatorsKeepsText(t *testing.T) {
	line := "before [#1 Pasted 3 lines] after"
	styled := stylePasteIndicators(line)
	if !strings.Contains(styled, "Pasted 3 lines") {
		t.Fatalf("styled output lost chip text: %q", styled)
	}
	// Non-paste text without the marker is returned unchanged.
	plain := "no chips here"
	if stylePasteIndicators(plain) != plain {
		t.Fatalf("plain line modified: %q", stylePasteIndicators(plain))
	}
}
