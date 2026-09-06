package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ModeCommand switches between operating modes (PLAN/ACT/AUTO)
type ModeCommand struct {
	interactive bool
}

// NewModeCommand creates a new mode command
func NewModeCommand() *ModeCommand {
	return &ModeCommand{
		interactive: false,
	}
}

func (c *ModeCommand) Name() string {
	return "mode"
}

func (c *ModeCommand) Description() string {
	return i18n.T("commands_b.mode.description")
}

func (c *ModeCommand) Aliases() []string {
	return []string{"m"}
}

func (c *ModeCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg {
		// Parse arguments:
		// /mode        -> show current mode
		// /mode off    -> switch to OFF mode (no filtering)
		// /mode act    -> switch to ACT mode
		// /mode plan   -> switch to PLAN mode
		// /mode auto   -> switch to AUTO mode

		if len(args) == 0 {
			// Show current mode
			return ModeShowMsg{}
		}

		mode := args[0]
		switch mode {
		case "off", "OFF":
			return ModeSwitchMsg{Mode: "off"}
		case "act", "ACT":
			return ModeSwitchMsg{Mode: "act"}
		case "plan", "PLAN":
			return ModeSwitchMsg{Mode: "plan"}
		case "auto", "AUTO":
			return ModeSwitchMsg{Mode: "auto"}
		case "debug", "DEBUG":
			return ModeSwitchMsg{Mode: "debug"}
		default:
			return ModeErrorMsg{
				Error: i18n.T("commands_b.mode.invalid"),
			}
		}
	}
}

func (c *ModeCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *ModeCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *ModeCommand) IsInteractive() bool {
	return c.interactive
}

// Message types for mode command

// ModeShowMsg requests showing the current mode
type ModeShowMsg struct{}

// ModeSwitchMsg requests switching to a specific mode
type ModeSwitchMsg struct {
	Mode string // "plan", "act", "auto", "debug"
}

// ModeErrorMsg carries error message
type ModeErrorMsg struct {
	Error string
}
