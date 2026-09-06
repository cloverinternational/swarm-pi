// Package commands provides the /export-claude command for exporting conversations to Claude Code format
package commands

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/converters/claude"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ClaudeExportResultMsg is sent when export completes
type ClaudeExportResultMsg struct {
	ConvID       string
	ExportedPath string
	Error        string
}

// ClaudeExportCommand handles exporting current conversation to Claude Code format
type ClaudeExportCommand struct {
	visible bool
}

// NewClaudeExportCommand creates a new export command
func NewClaudeExportCommand() *ClaudeExportCommand {
	return &ClaudeExportCommand{}
}

func (c *ClaudeExportCommand) Name() string { return "export-claude" }

func (c *ClaudeExportCommand) Description() string {
	return i18n.T("commands.claude_export.description")
}

func (c *ClaudeExportCommand) Aliases() []string { return []string{"claude-export", "export-cc"} }

func (c *ClaudeExportCommand) IsInteractive() bool { return c.visible }

// Execute triggers the export - the app will handle the actual export
func (c *ClaudeExportCommand) Execute(args []string) tea.Cmd {
	c.visible = true
	return func() tea.Msg {
		return ClaudeExportRequestMsg{}
	}
}

func (c *ClaudeExportCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			c.visible = false
		}
	}
	return c, nil
}

func (c *ClaudeExportCommand) View() string {
	return ""
}

// ClaudeExportRequestMsg signals that export should begin
type ClaudeExportRequestMsg struct{}

// DoClaudeExport exports a conversation to Claude Code JSONL format
// This is called from the app handler
func DoClaudeExport(conv *conversation.Conversation, convID string) (string, error) {
	if conv == nil {
		return "", fmt.Errorf("conversation is nil")
	}

	// Create converter
	converter := claude.NewClaudeCodeConverter()

	// Determine export location - Claude Code default
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %v", err)
	}

	claudeDir := fmt.Sprintf("%s/.claude/projects", homeDir)

	// Export to Claude Code JSONL format
	exportPath, err := converter.ExportToClaudeCodeJSONL(conv, claudeDir)
	if err != nil {
		return "", fmt.Errorf("export failed: %v", err)
	}

	return exportPath, nil
}
