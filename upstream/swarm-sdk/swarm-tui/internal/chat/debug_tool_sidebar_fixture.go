package chat

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	debugToolSidebarWidth  = 188
	debugToolSidebarHeight = 60
)

// ToolSidebarFixture is a deterministic full-frame visual fixture. It delegates
// rendering to the production App.View while locking logical geometry so VHS
// versions with an 80-column PTY fallback can still audit the real 188×60 frame.
type ToolSidebarFixture struct {
	app *App
}

type toolSidebarFixtureToggleMsg struct{}

func NewToolSidebarFixture() *ToolSidebarFixture {
	app := NewApp()
	app.bootstrapPending = false
	app.width = debugToolSidebarWidth
	app.height = debugToolSidebarHeight
	app.screen = ScreenChat
	app.workspaceMode = false
	app.twoPaneMode = false
	app.sidebarVisible = false
	app.showSidePanel = true
	app.settingsManager = nil
	app.sdk = nil
	app.sdkInitError = ""
	app.notifications = nil
	app.theme = AdaptThemeForTerminal(DefaultTheme, true)
	app.renderSettings = NewDefaultRenderSettings()
	app.showFullToolOutput = false
	app.sidePanelCache.valid = false

	t0 := time.Date(2026, time.August, 10, 17, 0, 0, 0, time.UTC)
	app.messages = debugToolSidebarMessages(t0)
	app.conversations = []Conversation{{
		ID:           "debug-tool-sidebar",
		Title:        "Tool Color + Side Menu Audit",
		Preview:      "Deterministic production renderer fixture",
		Status:       "idle",
		LastMessage:  t0.Add(5 * time.Second),
		MessageCount: len(app.messages),
	}}
	app.activeConv = &app.conversations[0]
	app.setCurrentConversationID(app.activeConv.ID)
	app.applyChatAreaLayout(true)
	app.invalidateViewportCache()
	app.updateViewportContent()
	app.msgViewport.GotoBottom()

	return &ToolSidebarFixture{app: app}
}

func (f *ToolSidebarFixture) Init() tea.Cmd {
	return tea.Tick(20*time.Second, func(time.Time) tea.Msg {
		return toolSidebarFixtureToggleMsg{}
	})
}

func (f *ToolSidebarFixture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case tea.WindowSizeMsg:
		// VHS v0.11 can report an 80-column PTY for a 1600px canvas. Keep the
		// fixture's measured production geometry instead of accepting fallback.
		return f, nil
	case toolSidebarFixtureToggleMsg:
		f.app.showSidePanel = !f.app.showSidePanel
		f.app.sidePanelCache.valid = false
		f.app.applyChatAreaLayout(true)
		f.app.updateViewportContent()
		f.app.msgViewport.GotoBottom()
		return f, nil
	case tea.KeyMsg:
		switch typed.String() {
		case "q", "ctrl+c":
			return f, tea.Quit
		}
	}
	return f, nil
}

func (f *ToolSidebarFixture) View() tea.View {
	return f.app.View()
}

func debugToolSidebarMessages(t0 time.Time) []Message {
	longOutput := make([]string, 30)
	for i := range longOutput {
		longOutput[i] = fmt.Sprintf("line %02d", i+1)
	}

	return []Message{
		{
			Role:      "user",
			Content:   "Audit successful, failed, long, and generic tool calls with the side menu open.",
			Timestamp: t0,
		},
		{
			Role:       "assistant",
			Timestamp:  t0.Add(time.Second),
			IsComplete: true,
			OrderedBlocks: []MessageBlock{
				{
					Type:     "content",
					Sequence: 1,
					Content:  "Checking the shared tool hierarchy at the measured terminal width.",
				},
				{
					Type:     "tool_call",
					Sequence: 2,
					ToolCall: &ToolCallDisplay{
						ID:   "read-ok",
						Name: "Read",
						Parameters: map[string]any{
							"file_path": "/workspace/internal/chat/app_chat_render.go",
							"offset":    1298,
							"limit":     24,
						},
					},
				},
				{
					Type:     "tool_result",
					Sequence: 3,
					ToolResult: &ToolResultDisplay{
						CallID:   "read-ok",
						ToolName: "Read",
						Output:   "1298→case \"tool_call\":\n1299→    if block.ToolCall != nil {\n1300→        tc := block.ToolCall",
					},
				},
				{
					Type:     "tool_call",
					Sequence: 4,
					ToolCall: &ToolCallDisplay{
						ID:   "bash-ok",
						Name: "Bash",
						Parameters: map[string]any{
							"command":         "printf 'success first\\nsuccess final\\n'",
							"timeout_seconds": 20,
							"description":     "Render successful command",
						},
					},
				},
				{
					Type:     "tool_result",
					Sequence: 5,
					ToolResult: &ToolResultDisplay{
						CallID:   "bash-ok",
						ToolName: "Bash",
						Output:   "success first\nsuccess final",
						Metadata: map[string]any{"exit_code": float64(0)},
					},
				},
				{
					Type:     "tool_call",
					Sequence: 6,
					ToolCall: &ToolCallDisplay{
						ID:         "bash-fail",
						Name:       "Bash",
						Parameters: map[string]any{"command": "sh -c 'echo expected failure >&2; exit 7'"},
					},
				},
				{
					Type:     "tool_result",
					Sequence: 7,
					ToolResult: &ToolResultDisplay{
						CallID:   "bash-fail",
						ToolName: "Bash",
						Output:   "expected failure",
						Error:    "Process exited with code 7",
						Metadata: map[string]any{"exit_code": float64(7)},
					},
				},
				{
					Type:     "tool_call",
					Sequence: 8,
					ToolCall: &ToolCallDisplay{
						ID:         "bash-long",
						Name:       "Bash",
						Parameters: map[string]any{"command": "seq 1 30"},
					},
				},
				{
					Type:     "tool_result",
					Sequence: 9,
					ToolResult: &ToolResultDisplay{
						CallID:   "bash-long",
						ToolName: "Bash",
						Output:   strings.Join(longOutput, "\n"),
						Metadata: map[string]any{"exit_code": float64(0)},
					},
				},
				{
					Type:     "tool_call",
					Sequence: 10,
					ToolCall: &ToolCallDisplay{
						ID:   "lookup",
						Name: "CustomLookup",
						Parameters: map[string]any{
							"query": "tool color contrast",
							"limit": 3,
						},
					},
				},
				{
					Type:     "tool_result",
					Sequence: 11,
					ToolResult: &ToolResultDisplay{
						CallID:   "lookup",
						ToolName: "CustomLookup",
						Output:   "3 matching renderer references",
					},
				},
				{
					Type:     "content",
					Sequence: 12,
					Content:  "All tool states remain inside the chat viewport.",
				},
			},
		},
	}
}
