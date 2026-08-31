package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MicroCompactCommand toggles micro-compaction on/off
type MicroCompactCommand struct {
	interactive bool
}

// NewMicroCompactCommand creates a new micro-compact command
func NewMicroCompactCommand() *MicroCompactCommand {
	return &MicroCompactCommand{
		interactive: false,
	}
}

func (c *MicroCompactCommand) Name() string {
	return "microcompact"
}

func (c *MicroCompactCommand) Description() string {
	return i18n.T("commands_b.microcompact.description")
}

func (c *MicroCompactCommand) Aliases() []string {
	return []string{"mc", "microcomp"}
}

func (c *MicroCompactCommand) Execute(args []string) tea.Cmd {
	// Parse arguments
	var action string
	if len(args) > 0 {
		action = args[0]
	}

	return func() tea.Msg {
		switch action {
		case "on", "enable", "true":
			return MicroCompactToggleMsg{Enable: true}
		case "off", "disable", "false":
			return MicroCompactToggleMsg{Enable: false}
		case "status", "info":
			return MicroCompactStatusRequestMsg{}
		default:
			// No args = toggle
			return MicroCompactToggleMsg{Toggle: true}
		}
	}
}

func (c *MicroCompactCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *MicroCompactCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *MicroCompactCommand) IsInteractive() bool {
	return c.interactive
}

// Implement SubcommandProvider for better autocomplete
func (c *MicroCompactCommand) Subcommands() []Subcommand {
	return []Subcommand{
		{Name: "on", Description: i18n.T("commands_b.microcompact.enable")},
		{Name: "off", Description: i18n.T("commands_b.microcompact.disable")},
		{Name: "status", Description: i18n.T("commands_b.microcompact.status")},
	}
}

func (c *MicroCompactCommand) ArgumentCompletions(subcommand string) []string {
	return nil
}

// Message types for micro-compact command

// MicroCompactToggleMsg requests toggling micro-compaction
type MicroCompactToggleMsg struct {
	Toggle bool // If true, toggle current state
	Enable bool // If Toggle is false, set to this value
}

// MicroCompactStatusRequestMsg requests current micro-compaction status
type MicroCompactStatusRequestMsg struct{}

// MicroCompactStatusMsg reports current micro-compaction status
type MicroCompactStatusMsg struct {
	Enabled        bool
	RetentionCount int
	MessagesSaved  int
	TokensSaved    int
	LastCompaction string
}

// MicroCompactUpdatedMsg indicates micro-compaction settings were updated
type MicroCompactUpdatedMsg struct {
	Enabled        bool
	RetentionCount int
	Message        string
}

// FormatStatusMessage formats the status for display
func (m MicroCompactStatusMsg) FormatStatusMessage() string {
	if !m.Enabled {
		return i18n.T("commands_b.microcompact.disabled")
	}

	status := i18n.T("commands_b.microcompact.enabled", m.RetentionCount)

	if m.MessagesSaved > 0 {
		status += i18n.T("commands_b.microcompact.messages", m.MessagesSaved)
		status += i18n.T("commands_b.microcompact.tokens", m.TokensSaved)
		if m.LastCompaction != "" {
			status += i18n.T("commands_b.microcompact.last_run", m.LastCompaction)
		}
	} else {
		status += i18n.T("commands_b.microcompact.no_activity")
	}

	return status
}
