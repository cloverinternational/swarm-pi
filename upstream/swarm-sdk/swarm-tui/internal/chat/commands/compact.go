package commands

import (
	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// CompactCommand triggers manual conversation compaction
type CompactCommand struct {
	interactive bool
}

// NewCompactCommand creates a new compact command
func NewCompactCommand() *CompactCommand {
	return &CompactCommand{
		interactive: false,
	}
}

func (c *CompactCommand) Name() string {
	return "compact"
}

func (c *CompactCommand) Description() string {
	return i18n.T("commands.compact.description")
}

func (c *CompactCommand) Aliases() []string {
	return []string{"compress", "summarize"}
}

func (c *CompactCommand) Execute(args []string) tea.Cmd {
	// Strategy is now unused since file recovery is disabled
	// Always use standard strategy (SwarmCode's summary-only approach)
	strategy := compaction.StrategyStandard

	return func() tea.Msg {
		// /compact always compacts immediately (no threshold check)
		// SwarmCode approach: summary-only, no file recovery
		return CompactRequestMsg{
			Manual:   true,
			Strategy: strategy,
		}
	}
}

func (c *CompactCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	// Non-interactive command
	return c, nil
}

func (c *CompactCommand) View() string {
	// Non-interactive command
	return ""
}

func (c *CompactCommand) IsInteractive() bool {
	return c.interactive
}

// Message types for compact command

// CompactRequestMsg requests conversation compaction
type CompactRequestMsg struct {
	// Manual indicates this is user-initiated via /compact command.
	Manual bool

	// Strategy is maintained for compatibility but no longer affects behavior
	// (file recovery is disabled, summary-only approach like SwarmCode)
	Strategy compaction.CompactionStrategy

	// PreserveFiles - no longer used (file recovery disabled)
	PreserveFiles []string

	// PreserveTodos - no longer used
	PreserveTodos bool

	// PreserveMode - no longer used
	PreserveMode bool
}

// CompactStartedMsg indicates compaction has begun
type CompactStartedMsg struct {
	OriginalTokens int
	Strategy       compaction.CompactionStrategy
}

// CompactCompletedMsg indicates compaction finished successfully
type CompactCompletedMsg struct {
	OriginalTokens  int
	CompactedTokens int
	Summary         string
	FileCount       int
	NewConvID       string // ID of the new conversation with compacted messages
	Manual          bool   // True for explicit /compact; false for automatic compaction
	RecoveryMethod  string // direct, chunked, or deterministic
	SummaryAttempts int
	SizeWarning     string
	FallbackUsed    bool
	UsedProvider    string
	UsedModel       string

	// New fields for post-compaction message
	Strategy          compaction.CompactionStrategy
	Mode              string   // Current operating mode preserved
	ActiveTodoCount   int      // Number of active todos preserved
	ModifiedFileCount int      // Number of modified files in context
	RecoveredFiles    []string // List of recovered file paths
}

// CompactErrorMsg indicates compaction failed
type CompactErrorMsg struct {
	Error  string
	Manual bool
}

// CompactSkippedMsg indicates compaction was skipped (below threshold)
type CompactSkippedMsg struct {
	CurrentTokens int
	Threshold     int
	PercentUsed   int
}
