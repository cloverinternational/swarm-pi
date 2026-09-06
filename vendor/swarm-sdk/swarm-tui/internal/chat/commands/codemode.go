package commands

import (
	tea "charm.land/bubbletea/v2"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// CodeModeCommand toggles code mode on/off.
// Code mode wraps tools into a JavaScript sandbox (run_code) so the LLM can
// batch multiple tool calls in one turn via Promise.all().
type CodeModeCommand struct {
	interactive bool
}

// NewCodeModeCommand creates a new codemode command.
func NewCodeModeCommand() *CodeModeCommand {
	return &CodeModeCommand{
		interactive: false,
	}
}

func (c *CodeModeCommand) Name() string {
	return "codemode"
}

func (c *CodeModeCommand) Description() string {
	return i18n.T("commands.codemode.description")
}

func (c *CodeModeCommand) Aliases() []string {
	return []string{"cm", "code-mode"}
}

func (c *CodeModeCommand) Execute(args []string) tea.Cmd {
	var action string
	if len(args) > 0 {
		action = args[0]
	}
	return func() tea.Msg {
		switch action {
		case "on", "enable", "true":
			return CodeModeToggleMsg{Enable: true}
		case "off", "disable", "false":
			return CodeModeToggleMsg{Enable: false}
		case "status", "info":
			return CodeModeStatusRequestMsg{}
		default:
			// No args = toggle
			return CodeModeToggleMsg{Toggle: true}
		}
	}
}

func (c *CodeModeCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	return c, nil
}

func (c *CodeModeCommand) View() string {
	return ""
}

func (c *CodeModeCommand) IsInteractive() bool {
	return c.interactive
}

// Implement SubcommandProvider for autocomplete.
func (c *CodeModeCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "on", Description: i18n.T("commands.codemode.subcommand.on")},
		{Name: "off", Description: i18n.T("commands.codemode.subcommand.off")},
		{Name: "status", Description: i18n.T("commands.codemode.subcommand.status")},
	}
}

func (c *CodeModeCommand) ArgumentCompletions(subcommand string) []string {
	return nil
}

// ─── Message types ──────────────────────────────────────────────────────────

// CodeModeToggleMsg requests toggling code mode.
type CodeModeToggleMsg struct {
	Toggle bool // If true, toggle current state
	Enable bool // If Toggle is false, set to this value
}

// CodeModeStatusRequestMsg requests current code mode status.
type CodeModeStatusRequestMsg struct{}

// CodeModeUpdatedMsg indicates code mode settings were updated and the SDK
// needs to be reinitialised for the change to take effect.
type CodeModeUpdatedMsg struct {
	Enabled bool
	Message string
}

// FormatStatusMessage formats the status for display.
func FormatCodeModeStatus(enabled bool) string {
	if enabled {
		return i18n.T("commands.codemode.status.enabled", codemode.SystemPrompt)
	}
	return i18n.T("commands.codemode.status.disabled")
}
