package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestSlashCommandSavedToHistory is the regression test for the bug where a
// previously-sent "/" command (e.g. "/goal ...") could not be recalled by
// pressing Up in the chat box. Slash commands are intercepted in the Enter
// handler (app_chat_key.go) and return BEFORE handleSendMessage(), which is the
// only place regular messages get added to inputHistory. The fix adds the raw
// slash command to history at the dispatch site so Up can recall/resend it.
//
// This test exercises the exact contract the dispatch relies on: the value used
// for both dispatch and history is GetSubmitValue() (so pastes are expanded),
// and once Added the command is the newest entry returned by NavigateUp.
func TestSlashCommandSavedToHistory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	hist := NewInputHistory(t.TempDir())

	// Simulate what the Enter dispatch does for a "/" command.
	const cmd = "/goal ship the release"
	hist.Add(cmd)

	got, ok := hist.NavigateUp("")
	if !ok {
		t.Fatalf("slash command was not saved to history")
	}
	if got != cmd {
		t.Fatalf("history recalled wrong value: got %q want %q", got, cmd)
	}
}

// TestSlashCommandHistoryExpandsPaste guards that the value routed to both the
// slash dispatch AND input history is GetSubmitValue() (pastes expanded), not
// the raw Value() (which hides pastes behind a "[#N Pasted X lines]" chip).
// Recalling the command from history must reproduce the full, resendable text.
func TestSlashCommandHistoryExpandsPaste(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)
	t.Setenv("HOME", tmp)

	in := NewSimpleInput()
	in.Focus()
	in.SetValue("/goal ")

	const pasted = "line one\nline two\nline three"
	in.Update(tea.PasteMsg{Content: pasted})

	// The visible value hides the paste behind a chip...
	if raw := in.Value(); raw == "/goal "+pasted {
		t.Fatalf("expected paste to be hidden behind a chip in Value(), got full text")
	}
	// ...but the submit value (what dispatch + history use) is the full text.
	submit := in.GetSubmitValue()
	want := "/goal " + pasted
	if submit != want {
		t.Fatalf("GetSubmitValue mismatch: got %q want %q", submit, want)
	}

	// That full value must round-trip through history.
	hist := NewInputHistory(t.TempDir())
	hist.Add(submit)
	got, ok := hist.NavigateUp("")
	if !ok || got != want {
		t.Fatalf("history did not round-trip expanded slash command: ok=%v got=%q want=%q", ok, got, want)
	}
}
