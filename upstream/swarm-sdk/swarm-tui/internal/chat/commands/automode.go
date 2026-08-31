package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// AutoModeCommand toggles builtin.AutoModeHook opt-in at runtime so the
// hook can classify tool calls against its allow / soft-deny lists and
// auto-approve or auto-deny without prompting the user.
type AutoModeCommand struct{}

// NewAutoModeCommand creates a new /automode command.
func NewAutoModeCommand() *AutoModeCommand { return &AutoModeCommand{} }

func (c *AutoModeCommand) Name() string { return "automode" }

func (c *AutoModeCommand) Description() string {
	return i18n.T("commands.automode.description")
}

func (c *AutoModeCommand) Aliases() []string { return []string{"auto-mode"} }

// Execute dispatches a message the App's update loop translates into a
// hook-config mutation. Supports: /automode (toggle), /automode on, /automode off.
func (c *AutoModeCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg {
		if len(args) == 0 {
			return AutoModeToggleMsg{}
		}
		switch args[0] {
		case "on", "ON", "true", "enable":
			return AutoModeSetMsg{Enabled: true}
		case "off", "OFF", "false", "disable":
			return AutoModeSetMsg{Enabled: false}
		case "status", "?":
			return AutoModeStatusMsg{}
		default:
			return AutoModeErrorMsg{Error: i18n.T("commands.automode.usage")}
		}
	}
}

func (c *AutoModeCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }

func (c *AutoModeCommand) View() string { return "" }

func (c *AutoModeCommand) IsInteractive() bool { return false }

// AutoModeToggleMsg flips the current opt-in state.
type AutoModeToggleMsg struct{}

// AutoModeSetMsg sets opt-in to an explicit value.
type AutoModeSetMsg struct{ Enabled bool }

// AutoModeStatusMsg requests a status notification.
type AutoModeStatusMsg struct{}

// AutoModeErrorMsg surfaces usage errors back to the user.
type AutoModeErrorMsg struct{ Error string }
