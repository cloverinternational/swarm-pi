package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSimpleInputAcceptsPrintableKeyCodeWithoutText(t *testing.T) {
	input := NewSimpleInput()
	input.Focus()

	input.Update(tea.KeyPressMsg{Code: 'c'})

	if got := input.Value(); got != "c" {
		t.Fatalf("Value() = %q, want %q", got, "c")
	}
}

func TestSimpleInputCommitsRapidKeyEventsImmediately(t *testing.T) {
	input := NewSimpleInput()
	input.Focus()

	input.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	input.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})

	if got := input.Value(); got != "ac" {
		t.Fatalf("Value() = %q, want %q", got, "ac")
	}
}

func TestSimpleInputDoesNotInsertModifiedPrintableKeyCode(t *testing.T) {
	input := NewSimpleInput()
	input.Focus()

	input.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	if got := input.Value(); got != "" {
		t.Fatalf("Value() = %q, want empty input", got)
	}
}
