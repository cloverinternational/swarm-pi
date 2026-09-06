package chatui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/components"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/layout"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Layout constants
const (
	topBarHeight    = 2 // title + separator
	inputAreaHeight = 3 // input box height
	statusBarHeight = 1
)

// ChatScreen is the main chat UI screen that composes all components.
// It manages layout calculation and coordinates rendering.
type ChatScreen struct {
	// Dimensions
	width  int
	height int

	// Layout calculator
	layout *layout.Calculator

	// Components
	theme     theme.Theme
	topBar    *components.TopBar
	sidePanel *components.SidePanel
	chatPanel *Panel
	input     *components.Input

	// State
	showSidePanel bool
	focused       string // "input", "chat", "side"
}

// NewChatScreen creates a new chat screen with all components.
func NewChatScreen(width, height int) *ChatScreen {
	th := theme.DefaultTheme()

	s := &ChatScreen{
		width:         width,
		height:        height,
		layout:        layout.NewCalculator(width, height),
		theme:         th,
		topBar:        components.NewTopBar(th),
		sidePanel:     components.NewSidePanel(th),
		chatPanel:     NewPanel(80, 20, WithTheme(th)),
		input:         components.NewInput(th, width-8),
		showSidePanel: width >= 90, // Auto-show if wide enough
		focused:       "input",
	}

	s.recalculateLayout()
	return s
}

// recalculateLayout updates all component sizes based on terminal dimensions.
func (s *ChatScreen) recalculateLayout() {
	s.layout.Resize(s.width, s.height)
	s.layout.SetShowSidePanel(s.showSidePanel)

	contentArea := s.layout.ContentArea()
	sidePanelArea := s.layout.SidePanelArea()

	// Calculate heights for different regions
	// Only subtract topBarHeight when side panel is visible
	contentHeight := s.height - inputAreaHeight - statusBarHeight
	if s.showSidePanel {
		contentHeight -= topBarHeight
	} else {
		// Add spacing below input when side panel is hidden (3-4 lines)
		spacingLines := 4
		if s.chatPanel.IsStreaming() {
			spacingLines = 3
		}
		contentHeight -= spacingLines
	}
	if contentHeight < 5 {
		contentHeight = 5
	}

	// Update component sizes
	s.topBar.SetWidth(s.width)

	// Side panel gets full content height
	if sidePanelArea.Width > 0 {
		s.sidePanel.SetSize(sidePanelArea.Width, contentHeight)
	}

	// Chat panel fills content area minus side panel
	s.chatPanel.Resize(contentArea.Width, contentHeight)

	// Input gets content width minus padding
	s.input.SetWidth(contentArea.Width - 4)
}

// Init initializes the chat screen.
func (s *ChatScreen) Init() tea.Cmd {
	return tea.Batch(
		s.chatPanel.Init(),
	)
}

// Update handles messages.
func (s *ChatScreen) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return s, tea.Quit

		case "tab":
			// Cycle focus
			switch s.focused {
			case "input":
				s.focused = "chat"
			case "chat":
				if s.showSidePanel {
					s.focused = "side"
				} else {
					s.focused = "input"
				}
			case "side":
				s.focused = "input"
			}

		case "ctrl+b":
			// Toggle side panel
			s.showSidePanel = !s.showSidePanel
			s.recalculateLayout()

		case "enter":
			if s.focused == "input" {
				// Submit message
				text := strings.TrimSpace(s.input.Value())
				if text != "" {
					s.addUserMessage(text)
					s.input.SetValue("")
				}
			}

		default:
			// Forward to focused component
			switch s.focused {
			case "input":
				cmd := s.input.Update(msg)
				cmds = append(cmds, cmd)
			case "chat":
				_, cmd := s.chatPanel.Update(msg)
				cmds = append(cmds, cmd)
			}
		}

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.recalculateLayout()

	default:
		// Forward to chat panel
		_, cmd := s.chatPanel.Update(msg)
		cmds = append(cmds, cmd)
	}

	return s, tea.Batch(cmds...)
}

// View renders the chat screen.
func (s *ChatScreen) View() tea.View {
	var v tea.View

	// Build the screen from regions
	var lines []string

	// Top bar - only show when side panel is visible
	if s.showSidePanel {
		lines = append(lines, s.topBar.View())
	}

	// Content area (chat + side panel)
	contentLines := s.renderContent()
	lines = append(lines, contentLines...)

	// Separator
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(s.theme.BorderColor()))
	contentArea := s.layout.ContentArea()
	lines = append(lines, sepStyle.Render(strings.Repeat("─", contentArea.Width)))

	// Input area
	inputStyle := lipgloss.NewStyle().Padding(0, 2)
	lines = append(lines, inputStyle.Render(s.input.View()))
	// Add spacing below input when side panel is hidden (to raise the input box)
	if !s.showSidePanel {
		// Add 4 lines of spacing when not streaming, 3 when streaming
		spacingLines := 4
		if s.chatPanel.IsStreaming() {
			spacingLines = 3
		}
		for i := 0; i < spacingLines; i++ {
			lines = append(lines, "")
		}
	}

	// Status bar
	lines = append(lines, s.renderStatusBar())

	v.SetContent(strings.Join(lines, "\n"))
	return v
}

// renderContent renders the main content area with chat and optional side panel.
func (s *ChatScreen) renderContent() []string {
	contentArea := s.layout.ContentArea()
	sidePanelArea := s.layout.SidePanelArea()

	// Calculate content height
	// Only subtract topBarHeight when side panel is visible
	contentHeight := s.height - inputAreaHeight - statusBarHeight
	if s.showSidePanel {
		contentHeight -= topBarHeight
	} else {
		// Add spacing below input when side panel is hidden (3-4 lines)
		spacingLines := 4
		if s.chatPanel.IsStreaming() {
			spacingLines = 3
		}
		contentHeight -= spacingLines
	}
	if contentHeight < 5 {
		contentHeight = 5
	}

	chatContent := s.chatPanel.ViewString()
	chatLines := strings.Split(chatContent, "\n")

	// Ensure chat fills its height
	for len(chatLines) < contentHeight {
		chatLines = append(chatLines, "")
	}
	if len(chatLines) > contentHeight {
		chatLines = chatLines[:contentHeight]
	}

	if sidePanelArea.Width == 0 {
		// No side panel - just return chat lines
		return chatLines
	}

	// Render side panel
	sideContent := s.sidePanel.View()
	sideLines := strings.Split(sideContent, "\n")

	// Ensure side panel fills its height
	for len(sideLines) < contentHeight {
		sideLines = append(sideLines, strings.Repeat(" ", sidePanelArea.Width))
	}
	if len(sideLines) > contentHeight {
		sideLines = sideLines[:contentHeight]
	}

	// Combine horizontally
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(s.theme.BorderColor()))
	sep := sepStyle.Render("│")

	var combined []string
	for i := 0; i < contentHeight; i++ {
		chatLine := ""
		if i < len(chatLines) {
			chatLine = chatLines[i]
		}
		// Pad chat line to width
		chatLine = padToWidth(chatLine, contentArea.Width)

		sideLine := ""
		if i < len(sideLines) {
			sideLine = sideLines[i]
		}

		combined = append(combined, chatLine+sep+sideLine)
	}

	return combined
}

// renderStatusBar renders the status bar.
func (s *ChatScreen) renderStatusBar() string {
	th := s.theme

	style := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMutedColor())).
		Width(s.width)

	// Build status text
	status := i18n.T("chatui.screen.status")

	if s.focused == "chat" {
		status = i18n.T("chatui.screen.status_chat", status)
	}

	return style.Render(status)
}

// addUserMessage adds a user message to the chat.
func (s *ChatScreen) addUserMessage(text string) {
	msg := types.Message{
		Role: "user",
		OrderedBlocks: []types.MessageBlock{
			{
				Type:     types.BlockContent,
				Content:  text,
				Sequence: 0,
			},
		},
	}
	s.chatPanel.AddMessage(msg)
}

// SetModel sets the model display in the top bar.
func (s *ChatScreen) SetModel(model, provider string) {
	s.topBar.SetModel(model)
	s.topBar.SetProvider(provider)
}

// SetTokenInfo sets the token information.
func (s *ChatScreen) SetTokenInfo(count, window int) {
	s.topBar.SetTokenInfo(count, window)
	s.sidePanel.SetTokenInfo(count, window)
}

// SetTools sets the tools list in the side panel.
func (s *ChatScreen) SetTools(tools []components.ToolInfo) {
	s.sidePanel.SetTools(tools)
}

// SetToggles sets the toggle states in the side panel.
func (s *ChatScreen) SetToggles(showThinking, showVerbose, cachingOn bool) {
	s.sidePanel.SetToggles(showThinking, showVerbose, cachingOn)
}

// SetPermissionLevel sets the permission level in the side panel.
func (s *ChatScreen) SetPermissionLevel(level string) {
	s.sidePanel.SetPermissionLevel(level)
}

// GetChatPanel returns the chat panel for direct access.
func (s *ChatScreen) GetChatPanel() *Panel {
	return s.chatPanel
}

// ViewString returns the rendered content as a string.
func (s *ChatScreen) ViewString() string {
	// Build the screen from regions
	var lines []string

	// Top bar - only show when side panel is visible
	if s.showSidePanel {
		lines = append(lines, s.topBar.View())
	}

	// Content area (chat + side panel)
	contentLines := s.renderContent()
	lines = append(lines, contentLines...)

	// Separator
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(s.theme.BorderColor()))
	contentArea := s.layout.ContentArea()
	lines = append(lines, sepStyle.Render(strings.Repeat("─", contentArea.Width)))

	// Input area
	inputStyle := lipgloss.NewStyle().Padding(0, 2)
	lines = append(lines, inputStyle.Render(s.input.View()))
	// Add spacing below input when side panel is hidden (to raise the input box)
	if !s.showSidePanel {
		// Add 4 lines of spacing when not streaming, 3 when streaming
		spacingLines := 4
		if s.chatPanel.IsStreaming() {
			spacingLines = 3
		}
		for i := 0; i < spacingLines; i++ {
			lines = append(lines, "")
		}
	}

	// Status bar
	lines = append(lines, s.renderStatusBar())

	return strings.Join(lines, "\n")
}

// padToWidth pads a string to exact width, accounting for ANSI codes.
func padToWidth(s string, width int) string {
	// Simple visible width calculation (doesn't handle all ANSI)
	visible := lipgloss.Width(s)
	if visible >= width {
		return s
	}
	return s + strings.Repeat(" ", width-visible)
}
