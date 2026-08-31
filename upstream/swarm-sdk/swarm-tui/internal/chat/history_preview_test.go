package chat

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestBuildHistoryPreviewDetailGroupsPromptsAndActions(t *testing.T) {
	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "  Build the history screen\nplease "},
		{
			Role: conversation.RoleAssistant,
			ToolCalls: []conversation.ToolCall{
				{ID: "read-1", Name: "Read", Parameters: map[string]any{"file_path": "/tmp/history.go"}},
				{ID: "test-1", Name: "Bash", Parameters: map[string]any{"command": "go test ./..."}},
			},
		},
		{
			Role: conversation.RoleTool,
			ToolResults: []conversation.ToolResult{
				{CallID: "read-1", Output: "ok"},
				{CallID: "test-1", Error: &conversation.ToolError{Message: "failed"}},
			},
		},
		{
			Role:    conversation.RoleUser,
			Content: "internal compaction summary",
			Metadata: map[string]any{
				conversation.CompactionGeneratedMetadataKey: true,
			},
		},
		{Role: conversation.RoleUser, Content: "Preview it at several sizes"},
		{
			Role: conversation.RoleAssistant,
			ToolCalls: []conversation.ToolCall{
				{ID: "search-1", Name: "grep", Parameters: map[string]any{"pattern": "renderTwoPane"}},
			},
		},
	}

	got := buildHistoryPreviewDetail("conv-1", messages)
	if got.FirstUserPrompt != "Build the history screen please" {
		t.Fatalf("first prompt = %q", got.FirstUserPrompt)
	}
	if got.LastUserPrompt != "Preview it at several sizes" {
		t.Fatalf("last prompt = %q", got.LastUserPrompt)
	}
	if len(got.Actions) != 3 {
		t.Fatalf("actions = %d, want 3", len(got.Actions))
	}
	if got.Actions[0].Label != "Read" || got.Actions[0].Status != "done" || got.Actions[0].Target != "/tmp/history.go" {
		t.Fatalf("first action = %#v", got.Actions[0])
	}
	if got.Actions[1].Label != "Run" || got.Actions[1].Status != "error" {
		t.Fatalf("second action = %#v", got.Actions[1])
	}
	if got.Actions[2].TurnPrompt != "Preview it at several sizes" || got.Actions[2].Status != "pending" {
		t.Fatalf("third action = %#v", got.Actions[2])
	}
}

func TestHistoryStackHeightsStayWithinViewport(t *testing.T) {
	for _, height := range []int{1, 4, 8, 18, 28, 42} {
		list, preview := historyStackHeights(height)
		if list < 0 || preview < 0 || list+preview != height {
			t.Errorf("height %d => list=%d preview=%d", height, list, preview)
		}
	}
}

func TestCleanHistoryPreviewTextRemovesTerminalControls(t *testing.T) {
	got := cleanHistoryPreviewText("hello\x1b[31m red\nworld")
	if strings.ContainsRune(got, '\x1b') || got != "hello red world" {
		t.Fatalf("cleaned text = %q", got)
	}
}

func TestApplyHistoryPreviewLoadedDropsStaleSelection(t *testing.T) {
	a := &App{
		conversations: []Conversation{{ID: "new"}},
		selectedIdx:   0,
		historyPreview: historyPreviewDetail{
			ConversationID: "new",
		},
		historyPreviewLoading: true,
	}
	a.applyHistoryPreviewLoaded(historyPreviewLoadedMsg{
		detail: historyPreviewDetail{ConversationID: "old", LastUserPrompt: "stale"},
	})
	if a.historyPreview.ConversationID != "new" || !a.historyPreviewLoading {
		t.Fatalf("stale preview was applied: %#v", a.historyPreview)
	}
}

func TestRenderStackedHistoryAcrossViewports(t *testing.T) {
	a := &App{
		theme:          DefaultTheme,
		twoPaneMode:    true,
		sidebarVisible: true,
		selectedIdx:    0,
		conversations: []Conversation{{
			ID:              "conv-1",
			Title:           "Improve the History TUI",
			FirstUserPrompt: "Show the selected conversation in the lower half of History.",
			Preview:         "Please preview it across different viewports.",
			MessageCount:    18,
			ToolCallCount:   3,
			LastMessage:     time.Now(),
		}},
		historyPreview: historyPreviewDetail{
			ConversationID: "conv-1",
			LastUserPrompt: "Please preview it across different viewports.",
			Actions: []historyPreviewAction{
				{Label: "Search", Target: "app_conversations_layout.go", Status: "done"},
				{Label: "Read", Target: "ConversationSummary", Status: "done"},
				{Label: "Edit", Target: "responsive stacked layout", Status: "done"},
			},
		},
	}

	for _, size := range []struct {
		width, height int
	}{{140, 42}, {90, 28}, {70, 18}, {40, 8}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			rendered := a.renderTwoPane(size.width, size.height)
			t.Logf("history preview %dx%d:\n%s", size.width, size.height, stripANSI(rendered))
			if got := lipgloss.Width(rendered); got > size.width {
				t.Fatalf("%dx%d width = %d", size.width, size.height, got)
			}
			if got := lipgloss.Height(rendered); got > size.height {
				t.Fatalf("%dx%d height = %d", size.width, size.height, got)
			}
			plain := stripANSI(rendered)
			if !strings.Contains(plain, "Sessions") || !strings.Contains(plain, "Improve the History TUI") {
				t.Fatalf("%dx%d missing list/title:\n%s", size.width, size.height, plain)
			}
			if size.height >= 24 {
				for _, want := range []string{"FIRST", "ACTIONS", "LAST"} {
					if !strings.Contains(plain, want) {
						t.Fatalf("%dx%d missing %q:\n%s", size.width, size.height, want, plain)
					}
				}
			} else if size.height >= 12 && !strings.Contains(plain, "actions") {
				t.Fatalf("%dx%d missing compact action digest:\n%s", size.width, size.height, plain)
			} else if size.height >= 12 && !strings.Contains(plain, "LAST") {
				t.Fatalf("%dx%d missing compact last prompt:\n%s", size.width, size.height, plain)
			}
		})
	}
}
