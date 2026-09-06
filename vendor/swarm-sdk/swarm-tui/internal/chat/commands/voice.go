package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// VoiceCommand toggles voice input on/off
type VoiceCommand struct {
	interactive bool
}

// NewVoiceCommand creates a new voice command
func NewVoiceCommand() *VoiceCommand {
	return &VoiceCommand{
		interactive: false,
	}
}

func (c *VoiceCommand) Name() string {
	return "voice"
}

func (c *VoiceCommand) Description() string {
	return i18n.T("commands.voice.description")
}

func (c *VoiceCommand) Aliases() []string {
	return []string{"v"}
}

func (c *VoiceCommand) Execute(args []string) tea.Cmd {
	// Handle "/voice auto" subcommand
	if len(args) > 0 && args[0] == "auto" {
		return func() tea.Msg {
			return VoiceAutoToggleMsg{}
		}
	}
	return func() tea.Msg {
		// VoiceToggleMsg is emitted when user requests to toggle voice
		// This will start recording if idle, or stop recording if active
		return VoiceToggleMsg{}
	}
}

func (c *VoiceCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *VoiceCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *VoiceCommand) IsInteractive() bool {
	return c.interactive
}

// Message types for voice command

// VoiceToggleMsg requests toggling voice input on/off
type VoiceToggleMsg struct{}

// VoiceAutoToggleMsg requests toggling auto-record mode
type VoiceAutoToggleMsg struct{}
