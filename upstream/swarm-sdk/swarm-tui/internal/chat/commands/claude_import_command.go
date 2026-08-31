// Package commands provides the /import-claude command for importing Claude Code conversations
package commands

import (
	"fmt"
	"os"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/converters/claude"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// debugLog writes a message to the debug log file
func debugLog(message string) {
	logFile := "/tmp/swarm-import-debug.log"
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	logEntry := fmt.Sprintf("[TUI] [%s] %s\n", timestamp, message)

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(logEntry)
}

// ClaudeImportCommand handles importing Claude Code conversations
type ClaudeImportCommand struct {
	visible       bool
	state         string // "projects", "sessions", "preview", "importing", "done"
	width, height int

	// Data
	converter       *claude.ClaudeCodeConverter
	projects        []string // Project paths
	sessions        []claude.ClaudeSessionIndexEntry
	selectedProject int
	selectedSession int
	scrollOffset    int
	maxVisible      int

	// Result
	importing             bool
	importedCount         int
	importedIDs           []string
	importedConversations []*conversation.Conversation // Store actual conversations for saving
	importError           string
}

// NewClaudeImportCommand creates a new Claude import command
func NewClaudeImportCommand() *ClaudeImportCommand {
	return &ClaudeImportCommand{
		converter:  claude.NewClaudeCodeConverter(),
		state:      "projects",
		maxVisible: 10,
	}
}

func (c *ClaudeImportCommand) Name() string { return "import-claude" }
func (c *ClaudeImportCommand) Description() string {
	return i18n.T("commands.claude_import.description")
}
func (c *ClaudeImportCommand) Aliases() []string   { return []string{"claude-import", "import-cc"} }
func (c *ClaudeImportCommand) IsInteractive() bool { return c.visible }

func (c *ClaudeImportCommand) Execute(args []string) tea.Cmd {
	c.visible = true
	c.state = "projects"
	c.selectedProject = 0
	c.selectedSession = 0
	c.scrollOffset = 0
	c.importError = ""
	c.importedCount = 0
	c.importedIDs = []string{}

	// Log to debug file
	cwd, _ := os.Getwd()
	debugLog(fmt.Sprintf("=== /import-claude Execute called ==="))
	debugLog(fmt.Sprintf("Current working directory: %s", cwd))

	// Scan for projects
	projects, err := c.converter.ListClaudeProjects()
	debugLog(fmt.Sprintf("ListClaudeProjects returned: %d projects, error: %v", len(projects), err))

	if err != nil {
		debugLog(fmt.Sprintf("Error listing projects: %v", err))
		c.importError = i18n.T("commands.claude_import.error.list_projects", err)
		c.state = "done"
		return nil
	}

	// Sort projects alphabetically
	sort.Strings(projects)
	c.projects = projects

	debugLog(fmt.Sprintf("After sorting: %d projects", len(c.projects)))
	for i, p := range c.projects {
		debugLog(fmt.Sprintf("  Project %d: %s", i, p))
	}

	if len(c.projects) == 0 {
		debugLog(fmt.Sprintf("No projects found"))
		c.importError = i18n.T("commands.claude_import.error.no_projects")
		c.state = "done"
		return nil
	}

	// Note: Auto-detection of CWD is disabled because it often matches repo roots
	// (like /home/user/swarm) instead of actual Claude Code projects.
	// Users should manually select their project for clarity.
	// If you want smart detection, it should check if cwd is directly inside a project
	// (e.g., /home/user/project/.claude/projects/) rather than matching the workspace path.

	debugLog(fmt.Sprintf("=== /import-claude ready to display projects ==="))
	return nil
}

func (c *ClaudeImportCommand) View() string {
	if !c.visible {
		return ""
	}

	switch c.state {
	case "projects":
		return c.renderProjects()
	case "sessions":
		return c.renderSessions()
	case "preview":
		return c.renderPreview()
	case "importing":
		return c.renderImporting()
	case "done":
		return c.renderDone()
	default:
		return ""
	}
}

func (c *ClaudeImportCommand) Update(msg tea.Msg) (Command, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return c.handleEscape()
		case "up", "k":
			c.handleUp()
		case "down", "j":
			c.handleDown()
		case "enter":
			return c.handleEnter()
		case "i":
			if c.state == "sessions" || c.state == "preview" {
				return c.handleImport()
			}
		}

	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		c.maxVisible = max((msg.Height - 10), 5)

	case ClaudeImportResultMsg:
		c.importing = false
		if msg.Error != "" {
			debugLog(fmt.Sprintf("ClaudeImportResultMsg: error=%s", msg.Error))
			c.importError = msg.Error
			c.state = "done"
		} else {
			debugLog(fmt.Sprintf("ClaudeImportResultMsg: imported %d conversations", msg.ImportedCount))
			c.importedCount = msg.ImportedCount
			c.importedIDs = msg.ImportedIDs
			c.importedConversations = msg.ImportedConversations
			c.state = "done"
		}
	}

	return c, nil
}

// Render methods

func (c *ClaudeImportCommand) renderProjects() string {
	title := i18n.T("commands.claude_import.projects.title")
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	normalStyle := lipgloss.NewStyle()

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	var lines []string
	lines = append(lines, titleStyle.Render("═══ "+title+" ═══"))
	lines = append(lines, "")

	if len(c.projects) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.projects.none")))
	} else {
		lines = append(lines, normalStyle.Render(i18n.T("commands.claude_import.projects.select")))
		lines = append(lines, "")

		for i := c.scrollOffset; i < len(c.projects) && i < c.scrollOffset+c.maxVisible; i++ {
			cursor := "  "
			style := normalStyle
			if i == c.selectedProject {
				cursor = "→ "
				style = selectedStyle
			}

			project := c.projects[i]
			line := cursor + style.Render(project)
			lines = append(lines, line)
		}
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.hint.projects")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2).
		Width(75)

	return boxStyle.Render(content)
}

func (c *ClaudeImportCommand) renderSessions() string {
	title := i18n.T("commands.claude_import.sessions.title")
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	normalStyle := lipgloss.NewStyle()

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	var lines []string
	lines = append(lines, titleStyle.Render("═══ "+title+" ═══"))
	lines = append(lines, "")

	if c.selectedProject < len(c.projects) {
		projectPath := c.projects[c.selectedProject]
		lines = append(lines, normalStyle.Render(i18n.T("commands.claude_import.project", projectPath)))
		lines = append(lines, "")
	}

	if len(c.sessions) == 0 {
		lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.sessions.none")))
	} else {
		for i := c.scrollOffset; i < len(c.sessions) && i < c.scrollOffset+c.maxVisible; i++ {
			cursor := "  "
			style := normalStyle
			if i == c.selectedSession {
				cursor = "→ "
				style = selectedStyle
			}

			session := c.sessions[i]
			summary := session.Summary
			if len(summary) > 40 {
				summary = summary[:40] + "..."
			}
			if summary == "" {
				summary = session.FirstPrompt
				if len(summary) > 40 {
					summary = summary[:40] + "..."
				}
			}

			line := cursor + style.Render(i18n.T("commands.claude_import.sessions.item", summary, session.MessageCount))
			lines = append(lines, line)
		}
	}

	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.hint.sessions")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2).
		Width(70)

	return boxStyle.Render(content)
}

func (c *ClaudeImportCommand) renderPreview() string {
	if c.selectedSession < 0 || c.selectedSession >= len(c.sessions) {
		return ""
	}

	session := c.sessions[c.selectedSession]
	debugLog(fmt.Sprintf("renderPreview: session SessionID=%s, MessageCount=%d, FirstPrompt=%q", session.SessionID, session.MessageCount, session.FirstPrompt[:min(50, len(session.FirstPrompt))]))

	title := i18n.T("commands.claude_import.preview.title")
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	normalStyle := lipgloss.NewStyle()

	var lines []string
	lines = append(lines, titleStyle.Render("═══ "+title+" ═══"))
	lines = append(lines, "")

	lines = append(lines, labelStyle.Render(i18n.T("commands.claude_import.preview.first_prompt")))
	firstPrompt := session.FirstPrompt
	if len(firstPrompt) > 60 {
		firstPrompt = firstPrompt[:60] + "..."
	}
	lines = append(lines, "  "+normalStyle.Render(firstPrompt))
	lines = append(lines, "")

	lines = append(lines, labelStyle.Render(i18n.T("commands.claude_import.preview.summary")))
	summary := session.Summary
	if len(summary) > 60 {
		summary = summary[:60] + "..."
	}
	if summary == "" {
		summary = i18n.T("commands.claude_import.preview.no_summary")
	}
	lines = append(lines, "  "+normalStyle.Render(summary))
	lines = append(lines, "")

	lines = append(lines, labelStyle.Render(i18n.T("commands.claude_import.preview.details")))
	lines = append(lines, i18n.T("commands.claude_import.preview.messages", session.MessageCount))
	lines = append(lines, i18n.T("commands.claude_import.preview.created", session.Created))
	lines = append(lines, "")

	lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.hint.preview")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2).
		Width(70)

	return boxStyle.Render(content)
}

func (c *ClaudeImportCommand) renderImporting() string {
	title := i18n.T("commands.claude_import.importing.title")
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))

	var lines []string
	lines = append(lines, titleStyle.Render(title))
	lines = append(lines, "")
	lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.importing.wait")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2).
		Width(50)

	return boxStyle.Render(content)
}

func (c *ClaudeImportCommand) renderDone() string {
	title := i18n.T("commands.claude_import.done.title")
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Accent)).
		Bold(true)

	var lines []string
	lines = append(lines, titleStyle.Render("═══ "+title+" ═══"))
	lines = append(lines, "")

	if c.importError != "" {
		errorStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Error))
		lines = append(lines, errorStyle.Render("✗ "+c.importError))
	} else {
		successStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.Success))
		lines = append(lines, successStyle.Render(i18n.T("commands.claude_import.done.success", c.importedCount)))

		if len(c.importedIDs) > 0 {
			dimStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(palette.TextDim))
			lines = append(lines, "")
			lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.done.ids")))
			for _, id := range c.importedIDs {
				lines = append(lines, "  "+dimStyle.Render(id))
			}
		}
	}

	lines = append(lines, "")
	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.TextDim))
	lines = append(lines, dimStyle.Render(i18n.T("commands.claude_import.hint.close")))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(palette.Border)).
		Padding(1, 2).
		Width(65)

	return boxStyle.Render(content)
}

// Event handlers

func (c *ClaudeImportCommand) handleEscape() (Command, tea.Cmd) {
	debugLog(fmt.Sprintf("handleEscape: current state=%s", c.state))
	switch c.state {
	case "projects":
		debugLog("handleEscape: closing command")
		c.visible = false
	case "sessions":
		debugLog("handleEscape: back to projects")
		c.state = "projects"
		c.selectedSession = 0
		c.scrollOffset = 0
	case "preview":
		debugLog("handleEscape: back to sessions")
		c.state = "sessions"
		c.scrollOffset = 0
	case "done":
		debugLog("handleEscape: closing after import complete")
		c.visible = false
	case "importing":
		debugLog("handleEscape: import in progress, ignoring")
		// Can't close while importing
	}
	return c, nil
}

func (c *ClaudeImportCommand) handleUp() {
	switch c.state {
	case "projects":
		if c.selectedProject > 0 {
			c.selectedProject--
			if c.selectedProject < c.scrollOffset {
				c.scrollOffset = c.selectedProject
			}
		}
	case "sessions", "preview":
		if c.selectedSession > 0 {
			c.selectedSession--
			if c.selectedSession < c.scrollOffset {
				c.scrollOffset = c.selectedSession
			}
		}
	}
}

func (c *ClaudeImportCommand) handleDown() {
	switch c.state {
	case "projects":
		if c.selectedProject < len(c.projects)-1 {
			c.selectedProject++
			if c.selectedProject >= c.scrollOffset+c.maxVisible {
				c.scrollOffset = c.selectedProject - c.maxVisible + 1
			}
		}
	case "sessions":
		if c.selectedSession < len(c.sessions)-1 {
			c.selectedSession++
			if c.selectedSession >= c.scrollOffset+c.maxVisible {
				c.scrollOffset = c.selectedSession - c.maxVisible + 1
			}
		}
	}
}

func (c *ClaudeImportCommand) handleEnter() (Command, tea.Cmd) {
	switch c.state {
	case "projects":
		// Load sessions for selected project
		if c.selectedProject < 0 || c.selectedProject >= len(c.projects) {
			debugLog(fmt.Sprintf("handleEnter: invalid project selection: %d (total: %d)", c.selectedProject, len(c.projects)))
			return c, nil
		}

		projectPath := c.projects[c.selectedProject]
		debugLog(fmt.Sprintf("handleEnter: loading sessions for project: %s", projectPath))

		sessions, err := c.converter.ListProjectSessions(projectPath)
		debugLog(fmt.Sprintf("handleEnter: ListProjectSessions returned %d sessions, error: %v", len(sessions), err))

		if err != nil {
			debugLog(fmt.Sprintf("handleEnter: error loading sessions: %v", err))
			c.importError = i18n.T("commands.claude_import.error.load_sessions", err)
			c.state = "done"
			return c, nil
		}

		// Sort sessions by modified time (newest first)
		sort.Slice(sessions, func(i, j int) bool {
			return sessions[i].Modified > sessions[j].Modified
		})

		c.sessions = sessions
		debugLog(fmt.Sprintf("handleEnter: transitioning to sessions state with %d sessions", len(c.sessions)))
		c.state = "sessions"
		c.selectedSession = 0
		c.scrollOffset = 0

	case "sessions":
		debugLog(fmt.Sprintf("handleEnter: transitioning to preview state"))
		c.state = "preview"
		c.scrollOffset = 0

	case "preview":
		debugLog(fmt.Sprintf("handleEnter: calling handleImport"))
		return c.handleImport()
	}
	return c, nil
}

func (c *ClaudeImportCommand) handleImport() (Command, tea.Cmd) {
	c.state = "importing"
	c.importing = true

	// Run import asynchronously
	return c, func() tea.Msg {
		if c.selectedProject < 0 || c.selectedProject >= len(c.projects) {
			return ClaudeImportResultMsg{Error: i18n.T("commands.claude_import.error.invalid_project")}
		}

		projectPath := c.projects[c.selectedProject]
		debugLog(fmt.Sprintf("handleImport: importing from %s", projectPath))

		// Import the project
		convs, err := c.converter.ImportProjectConversations(projectPath)
		if err != nil {
			debugLog(fmt.Sprintf("handleImport: import error: %v", err))
			return ClaudeImportResultMsg{Error: i18n.T("commands.claude_import.error.import_failed", err)}
		}

		debugLog(fmt.Sprintf("handleImport: imported %d conversations", len(convs)))

		// Store conversations for later saving
		c.importedConversations = convs

		// Create IDs list for display
		ids := make([]string, len(convs))
		for i, conv := range convs {
			ids[i] = conv.ID
			debugLog(fmt.Sprintf("  Conversation %d: %s (%d messages)", i+1, conv.ID, len(conv.Messages)))
		}

		return ClaudeImportResultMsg{
			ImportedCount:         len(convs),
			ImportedIDs:           ids,
			ImportedConversations: convs,
		}
	}
}

// ClaudeImportResultMsg is sent when import completes
type ClaudeImportResultMsg struct {
	ImportedCount         int
	ImportedIDs           []string
	ImportedConversations []*conversation.Conversation
	Error                 string
}
