package commands

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestInitRegistryRegistersModelCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	registry := InitRegistry(nil)
	command, ok := registry.Get("model")
	if !ok {
		t.Fatal("model command is not registered")
	}
	if _, ok := command.(*ModelCommand); !ok {
		t.Fatalf("model command has unexpected type %T", command)
	}
}

func TestModelCommandRunsBeforeOpenAsynchronously(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	command := NewModelCommand()
	calls := 0
	command.SetBeforeOpen(func() tea.Cmd {
		return func() tea.Msg {
			calls++
			return ProviderCatalogRefreshedMsg{Provider: "plexus"}
		}
	})
	cmd := command.Execute(nil)
	if calls != 0 {
		t.Fatal("before-open network work ran synchronously")
	}
	if cmd == nil {
		t.Fatal("before-open command was not returned")
	}
	msg := cmd()
	if calls != 1 {
		t.Fatalf("before-open calls = %d, want 1", calls)
	}
	command.Update(msg)
}
