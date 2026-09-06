package commands

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ThinkingCommand toggles extended thinking mode
type ThinkingCommand struct {
	interactive bool
}

// NewThinkingCommand creates a new thinking command
func NewThinkingCommand() *ThinkingCommand {
	return &ThinkingCommand{
		interactive: false,
	}
}

func (c *ThinkingCommand) Name() string {
	return "thinking"
}

func (c *ThinkingCommand) Description() string {
	return i18n.T("commands.thinking.description")
}

func (c *ThinkingCommand) Aliases() []string {
	return []string{"think", "extended"}
}

func (c *ThinkingCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg {
		// Parse arguments:
		// /thinking        -> toggle
		// /thinking on     -> enable with default budget
		// /thinking off    -> disable
		// /thinking 4096   -> enable with specific budget

		if len(args) == 0 {
			// Toggle mode
			return ThinkingToggleMsg{}
		}

		// Check for on/off
		if args[0] == "on" {
			budget := 2048 // Default
			if len(args) > 1 {
				if b, err := strconv.Atoi(args[1]); err == nil && b >= 1024 {
					budget = b
				}
			}
			return ThinkingEnableMsg{Budget: budget}
		}

		if args[0] == "off" {
			return ThinkingDisableMsg{}
		}

		// Try to parse as budget number
		if budget, err := strconv.Atoi(args[0]); err == nil && budget >= 1024 {
			return ThinkingEnableMsg{Budget: budget}
		}

		// Invalid argument
		return ThinkingErrorMsg{
			Error: i18n.T("commands.thinking.invalid_argument"),
		}
	}
}

func (c *ThinkingCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *ThinkingCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *ThinkingCommand) IsInteractive() bool {
	return c.interactive
}

// Message types for thinking command

// ThinkingToggleMsg toggles thinking mode
type ThinkingToggleMsg struct{}

// ThinkingEnableMsg enables thinking with specific budget
type ThinkingEnableMsg struct {
	Budget int
}

// ThinkingDisableMsg disables thinking
type ThinkingDisableMsg struct{}

// ThinkingErrorMsg carries error message
type ThinkingErrorMsg struct {
	Error string
}
