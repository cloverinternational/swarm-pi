package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// NewChatModalState represents the current state of the new chat modal
type NewChatModalState int

const (
	StateInput NewChatModalState = iota
	StateBranchSelect
	StateNewBranchInput
	StateConfirmCreate
)

// Available modes for new chat
var availableModes = []string{"off", "plan", "act", "auto"}

// FileMention handles @-mention file autocomplete
type FileMention struct {
	Active          bool
	Query           string
	Items           []FileItem // Files and folders in current view
	SelectedIdx     int
	MentionStartPos int      // Position where @ was typed
	CurrentPath     string   // Current directory being browsed
	PathStack       []string // Navigation history (for going back)
}

// NewChatModal provides a ChatGPT-style input for starting new conversations
type NewChatModal struct {
	// Input state
	input       string
	cursorPos   int
	placeholder string

	// File mention autocomplete
	fileMention FileMention

	// Branch state
	branches          []GitBranch
	selectedBranchIdx int
	suggestedBranch   string
	branchInput       string // For custom branch name

	// Mode state
	selectedModeIdx int // Index into availableModes

	// Modal state
	state           NewChatModalState
	gitAvailable    bool
	showBranches    bool
	createNewBranch bool

	// Dimensions
	width  int
	height int

	// Git helper for file listing
	gitHelper *GitHelper

	// Callbacks - now includes mode
	onSubmit func(prompt string, branch string, createNew bool, mode string)
	onCancel func()
}

// NewNewChatModal creates a new chat input modal
func NewNewChatModal(gitHelper *GitHelper, onSubmit func(string, string, bool, string), onCancel func()) *NewChatModal {
	m := &NewChatModal{
		placeholder:       i18n.T("classic_chat_2.new_chat.placeholder"),
		state:             StateInput,
		selectedBranchIdx: -1, // -1 means no branch selected
		selectedModeIdx:   2,  // Default to "act" mode
		onSubmit:          onSubmit,
		onCancel:          onCancel,
		showBranches:      true,
		width:             80,
		height:            30,
		gitHelper:         gitHelper,
	}

	// Load branches if git is available
	if gitHelper != nil && gitHelper.IsRepo() {
		m.gitAvailable = true
		branches, err := gitHelper.RecentBranches(8)
		if err == nil {
			m.branches = branches
		}
	}

	return m
}

// SetSize updates the modal dimensions
func (m *NewChatModal) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Update handles input for the modal
func (m *NewChatModal) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg.String())
	}
	return nil
}

func (m *NewChatModal) handleKey(key string) tea.Cmd {
	switch m.state {
	case StateInput:
		return m.handleInputKey(key)
	case StateBranchSelect:
		return m.handleBranchSelectKey(key)
	case StateNewBranchInput:
		return m.handleNewBranchInputKey(key)
	case StateConfirmCreate:
		return m.handleConfirmKey(key)
	}
	return nil
}

func (m *NewChatModal) handleInputKey(key string) tea.Cmd {
	// Handle file mention autocomplete if active
	if m.fileMention.Active {
		switch key {
		case "up":
			if m.fileMention.SelectedIdx > 0 {
				m.fileMention.SelectedIdx--
			}
			return nil
		case "down":
			if m.fileMention.SelectedIdx < len(m.fileMention.Items)-1 {
				m.fileMention.SelectedIdx++
			}
			return nil
		case "enter", "tab":
			m.selectCurrentFile()
			return nil
		case "esc":
			m.fileMention.Active = false
			return nil
		}
	}

	switch key {
	case "esc":
		if m.onCancel != nil {
			m.onCancel()
		}
		return nil

	case "enter":
		if strings.TrimSpace(m.input) == "" {
			return nil
		}
		// Generate suggested branch name
		m.suggestedBranch = SuggestBranchName(m.input)

		if m.gitAvailable {
			m.state = StateBranchSelect
			// Check for matching branch
			if len(m.branches) > 0 {
				m.selectedBranchIdx = 0 // Default to first (most recent) branch
			}
		} else {
			// No git, submit without branch
			if m.onSubmit != nil {
				m.onSubmit(m.input, "", false, availableModes[m.selectedModeIdx])
			}
		}
		return nil

	case "tab":
		// Toggle branch list visibility
		m.showBranches = !m.showBranches
		return nil

	case "shift+tab":
		// Cycle through modes
		m.selectedModeIdx = (m.selectedModeIdx + 1) % len(availableModes)
		return nil

	case " ", "space":
		// Explicit space handling (Bubble Tea v2 uses "space")
		m.input = m.input[:m.cursorPos] + " " + m.input[m.cursorPos:]
		m.cursorPos++
		m.detectFileMention()
		return nil

	case "backspace":
		if m.cursorPos > 0 && len(m.input) > 0 {
			m.input = m.input[:m.cursorPos-1] + m.input[m.cursorPos:]
			m.cursorPos--
			m.detectFileMention()
			if m.fileMention.Active {
				m.updateFileMentionFiles()
			}
		}
		return nil

	case "delete":
		if m.cursorPos < len(m.input) {
			m.input = m.input[:m.cursorPos] + m.input[m.cursorPos+1:]
			m.detectFileMention()
			if m.fileMention.Active {
				m.updateFileMentionFiles()
			}
		}
		return nil

	case "left":
		if m.cursorPos > 0 {
			m.cursorPos--
			m.detectFileMention()
		}
		return nil

	case "right":
		if m.cursorPos < len(m.input) {
			m.cursorPos++
			m.detectFileMention()
		}
		return nil

	case "home", "ctrl+a":
		m.cursorPos = 0
		m.fileMention.Active = false
		return nil

	case "end", "ctrl+e":
		m.cursorPos = len(m.input)
		m.detectFileMention()
		return nil

	case "ctrl+u":
		m.input = m.input[m.cursorPos:]
		m.cursorPos = 0
		m.fileMention.Active = false
		return nil

	case "ctrl+k":
		m.input = m.input[:m.cursorPos]
		m.detectFileMention()
		if m.fileMention.Active {
			m.updateFileMentionFiles()
		}
		return nil

	default:
		// Insert character
		if len(key) == 1 && key[0] >= 32 && key[0] < 127 {
			m.input = m.input[:m.cursorPos] + key + m.input[m.cursorPos:]
			m.cursorPos++
			m.detectFileMention()
			if m.fileMention.Active {
				m.updateFileMentionFiles()
			}
		}
		return nil
	}
}

func (m *NewChatModal) handleBranchSelectKey(key string) tea.Cmd {
	switch key {
	case "esc":
		m.state = StateInput
		return nil

	case "up", "k":
		if m.selectedBranchIdx > -1 {
			m.selectedBranchIdx--
		}
		return nil

	case "down", "j":
		maxIdx := len(m.branches) // +1 for "Create New" option
		if m.selectedBranchIdx < maxIdx {
			m.selectedBranchIdx++
		}
		return nil

	case "enter":
		if m.selectedBranchIdx == -1 {
			// No branch selected, continue without branch
			if m.onSubmit != nil {
				m.onSubmit(m.input, "", false, availableModes[m.selectedModeIdx])
			}
		} else if m.selectedBranchIdx < len(m.branches) {
			// Existing branch selected
			branch := m.branches[m.selectedBranchIdx]
			if m.onSubmit != nil {
				m.onSubmit(m.input, branch.Name, false, availableModes[m.selectedModeIdx])
			}
		} else {
			// "Create New Branch" selected
			m.branchInput = m.suggestedBranch
			m.state = StateNewBranchInput
		}
		return nil

	case "n":
		// Quick shortcut to create new branch
		m.branchInput = m.suggestedBranch
		m.state = StateNewBranchInput
		return nil

	case "c":
		// Continue without branch
		if m.onSubmit != nil {
			m.onSubmit(m.input, "", false, availableModes[m.selectedModeIdx])
		}
		return nil
	}
	return nil
}

func (m *NewChatModal) handleNewBranchInputKey(key string) tea.Cmd {
	switch key {
	case "esc":
		m.state = StateBranchSelect
		return nil

	case "enter":
		if strings.TrimSpace(m.branchInput) != "" {
			if m.onSubmit != nil {
				m.onSubmit(m.input, m.branchInput, true, availableModes[m.selectedModeIdx])
			}
		}
		return nil

	case "backspace":
		if len(m.branchInput) > 0 {
			m.branchInput = m.branchInput[:len(m.branchInput)-1]
		}
		return nil

	default:
		// Insert character (only valid branch name chars)
		if len(key) == 1 {
			char := key[0]
			if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
				(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '/' {
				m.branchInput += key
			}
		}
		return nil
	}
}

func (m *NewChatModal) handleConfirmKey(key string) tea.Cmd {
	switch key {
	case "y", "Y", "enter":
		if m.onSubmit != nil {
			m.onSubmit(m.input, m.branchInput, true, availableModes[m.selectedModeIdx])
		}
		return nil
	case "n", "N", "esc":
		m.state = StateNewBranchInput
		return nil
	}
	return nil
}

// View renders the modal as a full-screen takeover
func (m *NewChatModal) View(width, height int, theme Theme) string {
	m.width = width
	m.height = height

	// Calculate modal dimensions (60% of screen width, max 80 chars for readability)
	modalWidth := int(float64(width) * 0.6)
	if modalWidth > 80 {
		modalWidth = 80
	}
	if modalWidth < 50 {
		modalWidth = 50
	}

	contentWidth := modalWidth - 6 // Account for borders and padding

	// Build content based on state
	var content string
	switch m.state {
	case StateInput:
		content = m.renderInputState(contentWidth, theme)
	case StateBranchSelect:
		content = m.renderBranchSelectState(contentWidth, theme)
	case StateNewBranchInput:
		content = m.renderNewBranchInputState(contentWidth, theme)
	case StateConfirmCreate:
		content = m.renderConfirmState(contentWidth, theme)
	}

	// Modal container - no outer border, just centered content
	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Align(lipgloss.Center)

	modalRendered := modalStyle.Render(content)

	// Calculate modal height from rendered content
	modalLines := strings.Split(modalRendered, "\n")
	modalHeight := len(modalLines)

	// Create full-screen container with centered modal
	// Vertical padding to center
	topPadding := (height - modalHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}

	// Horizontal padding to center
	leftPadding := (width - modalWidth) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}

	// Build the full screen output
	var output strings.Builder

	// Top empty space
	emptyLine := strings.Repeat(" ", width)
	for i := 0; i < topPadding; i++ {
		output.WriteString(emptyLine)
		output.WriteString("\n")
	}

	// Modal with left padding
	leftPad := strings.Repeat(" ", leftPadding)
	for _, line := range modalLines {
		output.WriteString(leftPad)
		output.WriteString(line)
		// Pad right to full width
		lineLen := lipgloss.Width(line)
		if lineLen+leftPadding < width {
			output.WriteString(strings.Repeat(" ", width-lineLen-leftPadding))
		}
		output.WriteString("\n")
	}

	// Bottom empty space to fill screen
	remaining := height - topPadding - modalHeight
	for range remaining {
		output.WriteString(emptyLine)
		output.WriteString("\n")
	}

	return output.String()
}

func (m *NewChatModal) renderInputState(width int, theme Theme) string {
	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	lines = append(lines, titleStyle.Render(i18n.T("classic_chat_2.new_chat.title")))

	// Instruction
	instructStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted))
	instrText := i18n.T("classic_chat_2.new_chat.instruction")
	lines = append(lines, instructStyle.Render(instrText))
	lines = append(lines, "")

	// Input box with cursor
	inputContent := m.input
	if inputContent == "" {
		inputContent = " " // Ensure box renders
	}

	// Show cursor
	if m.cursorPos <= len(m.input) {
		before := inputContent[:m.cursorPos]
		after := ""
		if m.cursorPos < len(inputContent) {
			after = inputContent[m.cursorPos:]
		}
		cursorStyle := lipgloss.NewStyle().Reverse(true)
		cursorChar := " "
		if m.cursorPos < len(inputContent) {
			cursorChar = string(inputContent[m.cursorPos])
			after = inputContent[m.cursorPos+1:]
		}
		inputContent = before + cursorStyle.Render(cursorChar) + after
	}

	// Use 85% of terminal width for input box (max 100 chars)
	inputWidth := int(float64(width) * 0.85)
	if inputWidth > 100 {
		inputWidth = 100
	}
	if inputWidth < 40 {
		inputWidth = 40
	}

	inputBoxStyle := lipgloss.NewStyle().
		Width(inputWidth).
		Padding(0, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border))
	lines = append(lines, inputBoxStyle.Render(inputContent))

	// Show file autocomplete if active
	if m.fileMention.Active && len(m.fileMention.Items) > 0 {
		lines = append(lines, "")

		fileHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.Primary)).
			Bold(true)
		lines = append(lines, fileHeaderStyle.Render(i18n.T("classic_chat_2.new_chat.files")))

		// Show up to 5 files
		maxFiles := 5
		if len(m.fileMention.Items) < maxFiles {
			maxFiles = len(m.fileMention.Items)
		}

		for i := 0; i < maxFiles; i++ {
			file := m.fileMention.Items[i]
			fileStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Text))

			prefix := "  "
			if i == m.fileMention.SelectedIdx {
				prefix = "► "
				fileStyle = fileStyle.
					Foreground(lipgloss.Color(theme.Primary)).
					Bold(true)
			}

			// Truncate long paths
			displayName := file.Icon + " " + file.Name
			if len(displayName) > 60 {
				displayName = file.Icon + " ..." + file.Name[len(file.Name)-54:]
			}

			lines = append(lines, fileStyle.Render(prefix+displayName))
		}
	}

	// Mode selector
	// Compact mode selector
	selectedMode := availableModes[m.selectedModeIdx]
	modeText := i18n.T("classic_chat_2.new_chat.mode", strings.ToUpper(selectedMode))
	modeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	lines = append(lines, modeStyle.Render(modeText))

	// Branch preview if git available
	if m.gitAvailable && m.showBranches && len(m.branches) > 0 && !m.fileMention.Active {

		branchHeaderStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted))
		lines = append(lines, branchHeaderStyle.Render(i18n.T("classic_chat_2.new_chat.recent_branches")))

		// Show up to 3 branches as preview (more minimal)
		maxPreview := 3
		if len(m.branches) < maxPreview {
			maxPreview = len(m.branches)
		}

		for i := 0; i < maxPreview; i++ {
			branch := m.branches[i]
			branchStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextMuted))

			currentMarker := "  "
			if branch.IsCurrent {
				currentMarker = "* "
				branchStyle = branchStyle.Foreground(lipgloss.Color(theme.Success))
			}

			age := FormatBranchAge(branch.LastCommitTime)
			branchLine := fmt.Sprintf("%s%s (%s)", currentMarker, branch.Name, age)
			lines = append(lines, branchStyle.Render(branchLine))
		}
	}

	// Hints
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted))

	// Build hints based on available features
	var hints string
	if m.fileMention.Active {
		hints = i18n.T("classic_chat_2.new_chat.hint.autocomplete")
	} else if m.gitAvailable {
		hints = i18n.T("classic_chat_2.new_chat.hint.with_branches")
	} else {
		hints = i18n.T("classic_chat_2.new_chat.hint.no_branches")
	}
	lines = append(lines, hintStyle.Render(hints))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *NewChatModal) renderBranchSelectState(width int, theme Theme) string {
	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	lines = append(lines, titleStyle.Render(i18n.T("classic_chat_2.new_chat.branch.select_title")))
	lines = append(lines, "")

	// Show prompt summary
	promptStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))
	promptPreview := m.input
	if len(promptPreview) > 50 {
		promptPreview = promptPreview[:47] + "..."
	}
	lines = append(lines, promptStyle.Render(i18n.T("classic_chat_2.new_chat.branch.task", promptPreview)))
	lines = append(lines, "")

	// Suggested branch
	suggestStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Info))
	lines = append(lines, suggestStyle.Render(i18n.T("classic_chat_2.new_chat.branch.suggested", m.suggestedBranch)))
	lines = append(lines, "")

	// Branch list
	branchHeaderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Bold(true)
	lines = append(lines, branchHeaderStyle.Render(i18n.T("classic_chat_2.new_chat.branch.use_existing")))

	// Option: No branch
	noBranchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	noBranchIndicator := "  "
	if m.selectedBranchIdx == -1 {
		noBranchStyle = noBranchStyle.Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		noBranchIndicator = "› "
	}
	lines = append(lines, noBranchStyle.Render(noBranchIndicator+i18n.T("classic_chat_2.new_chat.branch.none")))

	// Existing branches
	for i, branch := range m.branches {
		branchStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		indicator := "  "
		if i == m.selectedBranchIdx {
			branchStyle = branchStyle.Foreground(lipgloss.Color(theme.Primary)).Bold(true)
			indicator = "› "
		}
		currentMarker := ""
		if branch.IsCurrent {
			currentMarker = i18n.T("classic_chat_2.new_chat.branch.current")
		}
		age := FormatBranchAge(branch.LastCommitTime)
		branchLine := fmt.Sprintf("%s%s%s - %s", indicator, branch.Name, currentMarker, age)
		lines = append(lines, branchStyle.Render(branchLine))
	}

	// Option: Create new
	createStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Success))
	createIndicator := "  "
	if m.selectedBranchIdx == len(m.branches) {
		createStyle = createStyle.Bold(true)
		createIndicator = "› "
	}
	lines = append(lines, createStyle.Render(createIndicator+i18n.T("classic_chat_2.new_chat.branch.create")))

	// Hints
	lines = append(lines, "")
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)
	lines = append(lines, hintStyle.Render(i18n.T("classic_chat_2.new_chat.branch.select_hint")))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *NewChatModal) renderNewBranchInputState(width int, theme Theme) string {
	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	lines = append(lines, titleStyle.Render(i18n.T("classic_chat_2.new_chat.branch.create_title")))
	lines = append(lines, "")

	// Instruction
	instructStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))
	lines = append(lines, instructStyle.Render(i18n.T("classic_chat_2.new_chat.branch.name_prompt")))
	lines = append(lines, "")

	// Branch input with cursor
	inputContent := m.branchInput
	if inputContent == "" {
		inputContent = " "
	}
	// Add cursor at end
	cursorStyle := lipgloss.NewStyle().Reverse(true)
	inputContent = inputContent + cursorStyle.Render(" ")

	inputBoxStyle := lipgloss.NewStyle().
		Width(width-4).
		Padding(0, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Border))
	lines = append(lines, inputBoxStyle.Render(inputContent))

	// Hints
	lines = append(lines, "")
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)
	lines = append(lines, hintStyle.Render(i18n.T("classic_chat_2.new_chat.branch.create_hint")))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *NewChatModal) renderConfirmState(width int, theme Theme) string {
	var lines []string

	// Title
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Warning)).
		Bold(true)
	lines = append(lines, titleStyle.Render(i18n.T("classic_chat_2.new_chat.branch.confirm_title")))
	lines = append(lines, "")

	// Message
	msgStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))
	lines = append(lines, msgStyle.Render(i18n.T("classic_chat_2.new_chat.branch.confirm_message", m.branchInput)))
	lines = append(lines, "")

	// Options
	optStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted))
	lines = append(lines, optStyle.Render(i18n.T("classic_chat_2.new_chat.branch.yes_no")))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// GetInput returns the current input text
func (m *NewChatModal) GetInput() string {
	return m.input
}

// GetSelectedBranch returns the selected branch name (empty if none)
func (m *NewChatModal) GetSelectedBranch() string {
	if m.selectedBranchIdx >= 0 && m.selectedBranchIdx < len(m.branches) {
		return m.branches[m.selectedBranchIdx].Name
	}
	return ""
}

// IsCreatingNewBranch returns true if user chose to create a new branch
func (m *NewChatModal) IsCreatingNewBranch() bool {
	return m.createNewBranch
}

// ============================================================================
// FILE MENTION AUTOCOMPLETE
// ============================================================================

// detectFileMention checks if we should activate file autocomplete
func (m *NewChatModal) detectFileMention() {
	text := m.input[:m.cursorPos]
	lastAt := strings.LastIndex(text, "@")

	if lastAt >= 0 && (lastAt == 0 || m.input[lastAt-1] == ' ' || m.input[lastAt-1] == '\n') {
		wasActive := m.fileMention.Active
		m.fileMention.Active = true
		m.fileMention.MentionStartPos = lastAt
		m.fileMention.Query = text[lastAt+1:]
		m.fileMention.SelectedIdx = 0

		// Initialize path on first activation
		if !wasActive {
			m.fileMention.CurrentPath = "."
			m.fileMention.PathStack = []string{}
		}

		m.updateFileMentionFiles()
		return
	}

	// Deactivate if no @ found or cursor moved away
	m.fileMention.Active = false
}

// updateFileMentionFiles refreshes the file list based on current query
func (m *NewChatModal) updateFileMentionFiles() {
	if !m.fileMention.Active {
		return
	}

	// Get all candidate files
	allFiles := m.getAllFiles()

	// Filter by query
	query := strings.ToLower(m.fileMention.Query)
	var matches []FileItem

	for _, file := range allFiles {
		fileName := strings.ToLower(filepath.Base(file))

		// Match on filename or full path
		if query == "" || strings.Contains(fileName, query) {
			item := FileItem{
				Name:  filepath.Base(file),
				Path:  file,
				IsDir: false,
				Icon:  GetFileIcon(FileItem{Name: file, IsDir: false}),
			}
			matches = append(matches, item)
			if len(matches) >= 10 { // Limit to 10 suggestions
				break
			}
		}
	}

	m.fileMention.Items = matches

	// Reset selection if out of bounds
	if m.fileMention.SelectedIdx >= len(matches) {
		m.fileMention.SelectedIdx = 0
	}
}

// getAllFiles returns a list of files to suggest
func (m *NewChatModal) getAllFiles() []string {
	var files []string

	// Get recently modified git files
	if m.gitHelper != nil && m.gitHelper.IsRepo() {
		gitFiles, _ := m.gitHelper.GetModifiedFiles(20) // Get last 20 modified files
		files = append(files, gitFiles...)
	}

	// Add files from current directory
	cwd, err := os.Getwd()
	if err == nil {
		filepath.Walk(cwd, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}

			// Skip hidden files and common ignore patterns
			relPath, _ := filepath.Rel(cwd, path)
			if strings.HasPrefix(relPath, ".") ||
				strings.Contains(relPath, "node_modules") ||
				strings.Contains(relPath, ".git") ||
				strings.Contains(relPath, "vendor") {
				return filepath.SkipDir
			}

			files = append(files, relPath)

			// Limit total files scanned
			if len(files) > 100 {
				return filepath.SkipDir
			}

			return nil
		})
	}

	// Remove duplicates and sort
	fileSet := make(map[string]bool)
	var unique []string
	for _, f := range files {
		if !fileSet[f] {
			fileSet[f] = true
			unique = append(unique, f)
		}
	}

	sort.Strings(unique)
	return unique
}

// selectCurrentFile inserts the selected file into the input
func (m *NewChatModal) selectCurrentFile() {
	if !m.fileMention.Active || len(m.fileMention.Items) == 0 {
		return
	}

	if m.fileMention.SelectedIdx < 0 || m.fileMention.SelectedIdx >= len(m.fileMention.Items) {
		return
	}

	selectedFile := m.fileMention.Items[m.fileMention.SelectedIdx]

	// Replace @query with the full filepath
	before := m.input[:m.fileMention.MentionStartPos]
	after := ""
	if m.cursorPos < len(m.input) {
		after = m.input[m.cursorPos:]
	}

	m.input = before + selectedFile.Path + after
	m.cursorPos = len(before + selectedFile.Path)

	// Deactivate file mention
	m.fileMention.Active = false
	m.fileMention.Items = nil
}
