package chat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestSlashCommandIncludesPastedContent is a regression test for the bug where
// pasting text while typing a "/" command (e.g. /goal) dropped the pasted
// content from the dispatched command.
//
// The input stores pasted text behind a visual indicator chip
// ("[#1 Pasted N lines]") in Value(), and only reconstitutes the real content
// via GetSubmitValue(). Slash-command dispatch used to read Value(), so the
// literal chip text was sent instead of the pasted content. Dispatch now reads
// GetSubmitValue(); this test locks that in.
func TestSlashCommandIncludesPastedContent(t *testing.T) {
	in := NewSimpleInput()
	in.Focus()

	// User types "/goal " then pastes a multi-word goal description.
	in.SetValue("/goal ")
	pasted := "make the tests pass and ship it"
	in.Update(tea.PasteMsg{Content: pasted})

	// Raw display value hides the pasted text behind a chip — this is what the
	// buggy code dispatched, and it must NOT contain the pasted content.
	rawValue := in.Value()
	if strings.Contains(rawValue, pasted) {
		t.Fatalf("expected raw Value() to hide pasted text behind an indicator, got %q", rawValue)
	}
	if !strings.HasPrefix(rawValue, "/goal ") {
		t.Fatalf("expected raw Value() to keep the /goal prefix, got %q", rawValue)
	}

	// The submit value (used by slash dispatch) MUST expand the paste to the
	// full command including the pasted content.
	submit := strings.TrimSpace(in.GetSubmitValue())
	want := "/goal " + pasted
	if submit != want {
		t.Fatalf("GetSubmitValue() = %q, want %q", submit, want)
	}
	if !strings.HasPrefix(submit, "/") {
		t.Fatalf("submit value lost its slash prefix: %q", submit)
	}
}
