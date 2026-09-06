package commands

import (
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// RefreshPreviewsCommand regenerates preview text for conversations that have broken previews
type RefreshPreviewsCommand struct {
	interactive bool
}

// NewRefreshPreviewsCommand creates a new refresh-previews command
func NewRefreshPreviewsCommand() *RefreshPreviewsCommand {
	return &RefreshPreviewsCommand{
		interactive: false,
	}
}

func (c *RefreshPreviewsCommand) Name() string {
	return "refresh-previews"
}

func (c *RefreshPreviewsCommand) Description() string {
	return i18n.T("commands.refresh_previews.description")
}

func (c *RefreshPreviewsCommand) Aliases() []string {
	return []string{"fix-previews", "regenerate-previews"}
}

func (c *RefreshPreviewsCommand) Execute(args []string) tea.Cmd {
	count := 100 // Default to 100 conversations

	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			count = n
		}
	}

	return func() tea.Msg {
		return RefreshPreviewsRequestMsg{Count: count}
	}
}

func (c *RefreshPreviewsCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *RefreshPreviewsCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *RefreshPreviewsCommand) IsInteractive() bool {
	return c.interactive
}

// Message types for refresh-previews command

// RefreshPreviewsRequestMsg requests regeneration of conversation previews
type RefreshPreviewsRequestMsg struct {
	Count int
}

// RefreshPreviewsCompletedMsg indicates preview regeneration finished
type RefreshPreviewsCompletedMsg struct {
	Processed int
	Updated   int
	Errors    int
}

// RefreshPreviewsErrorMsg indicates preview regeneration failed
type RefreshPreviewsErrorMsg struct {
	Error string
}

// RefreshPreviewsStartedMsg indicates preview regeneration has begun
type RefreshPreviewsStartedMsg struct {
	Count int
}
