package commands

import (
	"strings"
	"testing"
)

func TestClearCommandContract(t *testing.T) {
	cmd := NewClearCommand()
	if cmd.Name() != "clear" {
		t.Fatalf("Name() = %q, want clear", cmd.Name())
	}
	if !strings.Contains(strings.ToLower(cmd.Description()), "new chat") {
		t.Fatalf("Description() should mention new chat, got %q", cmd.Description())
	}
	if cmd.IsInteractive() {
		t.Fatalf("clear command must be non-interactive")
	}
	if view := cmd.View(); view != "" {
		t.Fatalf("View() = %q, want empty", view)
	}
	if aliases := cmd.Aliases(); len(aliases) != 0 {
		t.Fatalf("Aliases() = %v, want none", aliases)
	}
}

func TestClearCommandExecuteEmitsMessage(t *testing.T) {
	cmd := NewClearCommand().Execute(nil)
	if cmd == nil {
		t.Fatalf("Execute returned nil tea.Cmd")
	}
	msg := cmd()
	if _, ok := msg.(ClearConversationMsg); !ok {
		t.Fatalf("Execute emitted %T, want ClearConversationMsg", msg)
	}
}

func TestInitRegistryIncludesClearCommand(t *testing.T) {
	reg := InitRegistry(nil)
	cmd, ok := reg.Get("clear")
	if !ok {
		t.Fatalf("/clear not registered")
	}
	if cmd.Name() != "clear" {
		t.Fatalf("registered command Name() = %q, want clear", cmd.Name())
	}
}
