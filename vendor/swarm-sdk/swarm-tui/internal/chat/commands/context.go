package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ContextCommand implements /context (alias /audit) — open the debug screen's
// Context tab, which breaks down the most recent assembled provider request:
// where the tokens go across system prompt, tool schemas, and messages, with
// hidden/ephemeral injections (system-reminders, task/skill nudges,
// swarmos_context/cached-context blocks) explicitly flagged so nothing is
// invisible.
type ContextCommand struct{}

// NewContextCommand creates a new context command.
func NewContextCommand() *ContextCommand { return &ContextCommand{} }

func (c *ContextCommand) Name() string { return "context" }
func (c *ContextCommand) Description() string {
	return i18n.T("commands.context.description")
}
func (c *ContextCommand) Aliases() []string { return []string{"audit"} }

// Execute emits an OpenContextTabMsg for the App to handle because only the
// chat App owns the debug screen and the captured request bodies it analyzes.
func (c *ContextCommand) Execute(args []string) tea.Cmd {
	return func() tea.Msg { return OpenContextTabMsg{} }
}

func (c *ContextCommand) Update(msg tea.Msg) (Command, tea.Cmd) { return c, nil }
func (c *ContextCommand) View() string                          { return "" }
func (c *ContextCommand) IsInteractive() bool                   { return false }

// OpenContextTabMsg asks the chat App to open the debug screen on its Context
// tab (the live context-composition audit).
type OpenContextTabMsg struct{}
