package commands

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ReindexCommand regenerates titles and summaries for conversations that lack them
type ReindexCommand struct {
	interactive bool
}

// NewReindexCommand creates a new reindex command
func NewReindexCommand() *ReindexCommand {
	return &ReindexCommand{
		interactive: false,
	}
}

func (c *ReindexCommand) Name() string {
	return "reindex"
}

func (c *ReindexCommand) Description() string {
	return i18n.T("commands.reindex.description")
}

func (c *ReindexCommand) Aliases() []string {
	return []string{"retitle"}
}

func (c *ReindexCommand) Execute(args []string) tea.Cmd {
	// Default scope: only conversations in the current workspace ("this folder").
	// Pass "all" to consider conversations across all workspaces.
	scopeAll := false
	if len(args) > 0 {
		arg := strings.TrimSpace(args[0])
		if strings.EqualFold(arg, "all") {
			scopeAll = true
		}
	}

	return func() tea.Msg {
		return ReindexRequestMsg{ScopeAll: scopeAll}
	}
}

func (c *ReindexCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *ReindexCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *ReindexCommand) IsInteractive() bool {
	return c.interactive
}

// Message types for reindex command

// ReindexRequestMsg requests reindexing of conversation titles + summaries.
// ScopeAll=false limits to the current workspace ("this folder"); true covers all.
// The handler caps every run at maxReindexBatch (10) conversations.
type ReindexRequestMsg struct {
	ScopeAll bool
}

// ReindexCompletedMsg indicates reindexing finished
type ReindexCompletedMsg struct {
	Processed int
}

// ReindexErrorMsg indicates reindexing failed
type ReindexErrorMsg struct {
	Error string
}

// ReindexStartedMsg indicates reindexing has begun
type ReindexStartedMsg struct {
	Count int
}
