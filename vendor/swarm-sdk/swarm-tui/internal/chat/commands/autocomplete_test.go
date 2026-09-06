package commands

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Mock command for testing
type mockCommand struct {
	name string
	desc string
}

func (m mockCommand) Name() string                      { return m.name }
func (m mockCommand) Description() string               { return m.desc }
func (m mockCommand) Aliases() []string                 { return nil }
func (m mockCommand) Execute([]string) tea.Cmd          { return nil }
func (m mockCommand) View() string                      { return "" }
func (m mockCommand) Update(tea.Msg) (Command, tea.Cmd) { return m, nil }
func (m mockCommand) IsInteractive() bool               { return false }

func TestAutocompleteView(t *testing.T) {
	// Create registry and add test commands
	registry := NewRegistry()
	registry.Register(mockCommand{name: "model", desc: "Change AI model"})
	registry.Register(mockCommand{name: "auth", desc: "Login with OAuth"})
	registry.Register(mockCommand{name: "render", desc: "Configure display"})
	registry.Register(mockCommand{name: "hooks", desc: "Manage hooks"})

	// Create autocomplete
	ac := NewAutocomplete(registry)
	ac.SetInput("/")

	// Check that it's visible
	if !ac.IsVisible() {
		t.Error("Expected autocomplete to be visible")
	}

	// Get the view
	view := ac.View()

	// Check for key elements (the view renders command rows without header/footer hints)
	checks := []string{
		"model", // Should show commands
		"auth",
		"render",
		"hooks",
	}

	for _, check := range checks {
		if !strings.Contains(view, check) {
			t.Errorf("View should contain '%s', got:\n%s", check, view)
		}
	}

	// Test with partial input
	ac.SetInput("/mod")
	view = ac.View()
	if len(ac.matches) != 1 || ac.matches[0].Name() != "model" {
		t.Error("Should match only model command")
	}
	if !strings.Contains(view, "/model") {
		t.Error("View should contain the matched command")
	}
}

// TestCompleteSelectionPreservesMessage verifies that pressing Tab to complete
// a slash command does NOT wipe out any message the user typed after the
// command portion.
func TestCompleteSelectionPreservesMessage(t *testing.T) {
	registry := NewRegistry()
	registry.Register(mockCommand{name: "model", desc: "Change AI model"})

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"partial command only", "/mod", "/model "},
		{"complete command only", "/model", "/model "},
		{"command with trailing message", "/mod use the fast one", "/model use the fast one"},
		{"command already complete with message", "/model use the fast one", "/model use the fast one"},
		{"message with extra internal spaces", "/mod  keep   spacing", "/model keep   spacing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ac := NewAutocomplete(registry)
			ac.SetInput(tt.input)
			if !ac.IsVisible() {
				t.Fatalf("autocomplete not visible for input %q", tt.input)
			}
			got := ac.CompleteSelection()
			if got != tt.want {
				t.Errorf("CompleteSelection(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAutocompleteIcon(t *testing.T) {
	ac := NewAutocomplete(nil)

	tests := []struct {
		cmd  string
		icon string
		desc string
	}{
		{"model", iconModel, "model should have cube icon"},
		{"auth", iconAuth, "auth should have key icon"},
		{"render", iconView, "render should have eye icon"},
		{"hooks", iconCode, "hooks should have code icon"},
		{"unknown", iconCommand, "unknown should have terminal icon"},
	}

	for _, tt := range tests {
		icon, _ := ac.getCommandIcon(mockCommand{name: tt.cmd})
		if icon != tt.icon {
			t.Errorf("%s: expected icon %q, got %q", tt.desc, tt.icon, icon)
		}
	}
}

type fakeArgumentOptionProvider map[string][]ArgumentOption

func (p fakeArgumentOptionProvider) CommandArgumentOptions(commandName string) []ArgumentOption {
	return p[commandName]
}

func TestArgumentAutocompleteFiltersRendersAndCompletes(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewProfileSwitchCommand())
	ac := NewAutocomplete(registry)
	ac.SetArgumentOptionProvider(fakeArgumentOptionProvider{
		"profile": {
			{
				Value:       "prod",
				Label:       "Production",
				Description: "Stable primary profile",
				Badges:      []string{"current", "default"},
			},
			{
				Value:       "fast-dev",
				Label:       "Fast Development",
				Description: "Low-latency coding profile",
			},
		},
	})

	ac.SetInput("/profile ")
	if !ac.IsVisible() || !ac.argumentMode {
		t.Fatal("expected live argument autocomplete for /profile ")
	}
	view := ac.View()
	for _, want := range []string{"Production", "prod", "[current]", "[default]", "Tab fill", "Enter run"} {
		if !strings.Contains(view, want) {
			t.Errorf("argument view missing %q:\n%s", want, view)
		}
	}

	// Arrow navigation changes which exact value Tab stages.
	ac.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := ac.CompleteSelection(); got != "/profile fast-dev" {
		t.Fatalf("Tab completion = %q, want %q", got, "/profile fast-dev")
	}

	// Filtering searches label/description as well as ID.
	ac.SetInput("/profile latency")
	if len(ac.argumentMatches) != 1 || ac.argumentMatches[0].Value != "fast-dev" {
		t.Fatalf("description filter got %+v", ac.argumentMatches)
	}
	ac.SetInput("/profile production")
	if len(ac.argumentMatches) != 1 || ac.argumentMatches[0].Value != "prod" {
		t.Fatalf("label filter got %+v", ac.argumentMatches)
	}
}

func TestArgumentAutocompleteSupportsSpacesAliasesAndActions(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewPromptCommand())
	registry.Register(NewAgentsCommand())
	registry.Register(NewCompactionCommand())
	ac := NewAutocomplete(registry)
	ac.SetArgumentOptionProvider(fakeArgumentOptionProvider{
		"prompt": {{
			Value: "Careful Reviewer",
			Label: "Careful Reviewer",
		}},
		"agents": {{
			Value: "code-reviewer",
			Label: "Code Reviewer",
		}},
		"compaction": {{
			Label:  "Open Settings → Compaction",
			Action: true,
		}},
	})

	ac.SetInput("/prompt careful rev")
	if !ac.IsVisible() || len(ac.argumentMatches) != 1 {
		t.Fatalf("prompt-with-spaces filter failed: %+v", ac.argumentMatches)
	}
	if got := ac.CompleteSelection(); got != "/prompt Careful Reviewer" {
		t.Errorf("space-containing completion = %q", got)
	}

	// Aliases resolve through the registry and complete to the canonical command.
	ac.SetInput("/subagents ")
	if got := ac.CompleteSelection(); got != "/agents code-reviewer" {
		t.Errorf("alias completion = %q", got)
	}

	ac.SetInput("/compaction ")
	if !strings.Contains(ac.View(), "Open Settings") {
		t.Fatalf("action row not rendered:\n%s", ac.View())
	}
	if got := ac.CompleteSelection(); got != "/compaction" {
		t.Errorf("action completion = %q, want /compaction", got)
	}
}

func TestArgumentAutocompleteRespectsNarrowWidth(t *testing.T) {
	registry := NewRegistry()
	registry.Register(NewProfileSwitchCommand())
	ac := NewAutocomplete(registry)
	ac.SetArgumentOptionProvider(fakeArgumentOptionProvider{
		"profile": {{
			Value:       "long-profile-id",
			Label:       "A Very Long Profile Display Name",
			Description: "A description that must be truncated on a narrow terminal",
			Badges:      []string{"current", "default"},
		}},
	})
	ac.SetSize(24, 10)
	ac.SetInput("/profile ")
	for i, line := range strings.Split(ac.View(), "\n") {
		if width := ansi.StringWidth(line); width > 22 {
			t.Errorf("line %d width=%d exceeds available width 22: %q", i, width, line)
		}
	}
}
