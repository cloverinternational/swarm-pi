package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ClearCommand implements /clear — cancel the current turn and start a fresh chat.
type ClearCommand struct{}

// NewClearCommand creates a new clear command.
func NewClearCommand() *ClearCommand { return &ClearCommand{} }

func (c *ClearCommand) Name() string { return "clear" }
func (c *ClearCommand) Description() string {
	return i18n.T("commands.clear.description")
}
func (c *ClearCommand) Aliases() []string { return nil }

// Execute emits a ClearConversationMsg for the App to handle because only the
// chat App owns runtime state such as background agents, pending queues, and UI.
func (c *ClearCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg { return ClearConversationMsg{} }
}

func (c *ClearCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *ClearCommand) View() string                          { return "" }
func (c *ClearCommand) IsInteractive() bool                   { return false }

// ClearConversationMsg asks the chat App to cancel the current turn, clear the
// visible conversation state, and start a fresh conversation in the same process.
type ClearConversationMsg struct{}
