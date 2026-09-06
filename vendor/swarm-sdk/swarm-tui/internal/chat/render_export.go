package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// RenderSnapshot captures a serialized view of the chat UI along with structured metadata.
type RenderSnapshot struct {
	Timestamp        string                `json:"timestamp"`
	Screen           string                `json:"screen"`
	Terminal         TerminalSnapshot      `json:"terminal"`
	Conversation     *ConversationSnapshot `json:"conversation,omitempty"`
	Input            InputSnapshot         `json:"input"`
	Viewport         ViewportSnapshot      `json:"viewport"`
	Messages         []MessageSnapshot     `json:"messages"`
	SidePanel        *SidePanelSnapshot    `json:"sidePanel,omitempty"`
	Notifications    []Notification        `json:"notifications,omitempty"`
	AdditionalFields map[string]any        `json:"additionalFields,omitempty"`
	Rendered         string                `json:"rendered"`
	RenderedStripped string                `json:"renderedStripped"`
}

// TerminalSnapshot describes the current terminal sizing and panel layout.
type TerminalSnapshot struct {
	Width          int  `json:"width"`
	Height         int  `json:"height"`
	ChatWidth      int  `json:"chatWidth"`
	ShowSidePanel  bool `json:"showSidePanel"`
	SidePanelWidth int  `json:"sidePanelWidth"`
}

// ConversationSnapshot provides high-level conversation metadata.
type ConversationSnapshot struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Status             string `json:"status"`
	MessageCount       int    `json:"messageCount"`
	TokenCount         int    `json:"tokenCount"`
	WaitingFor         string `json:"waitingFor"`
	AgentThought       string `json:"agentThought"`
	ShowThinking       bool   `json:"showThinking"`
	ShowFullToolOutput bool   `json:"showFullToolOutput"`
	Provider           string `json:"provider"`
	ProviderDisplay    string `json:"providerDisplay"`
	Model              string `json:"model"`
	ModelDisplay       string `json:"modelDisplay"`
}

// InputSnapshot captures the current input buffer and cursor state.
type InputSnapshot struct {
	Value        string `json:"value"`
	Cursor       int    `json:"cursor"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	ScrollOffset int    `json:"scrollOffset"`
	Focused      bool   `json:"focused"`
}

// ViewportSnapshot captures the current message viewport state.
type ViewportSnapshot struct {
	Width                int      `json:"width"`
	Height               int      `json:"height"`
	YOffset              int      `json:"yOffset"`
	XOffset              int      `json:"xOffset"`
	TotalLines           int      `json:"totalLines"`
	VisibleLineCount     int      `json:"visibleLineCount"`
	LongestLineWidth     int      `json:"longestLineWidth"`
	VisibleLines         []string `json:"visibleLines"`
	VisibleLinesStripped []string `json:"visibleLinesStripped"`
	MessageNavMode       bool     `json:"messageNavMode"`
	FocusedMessageIndex  int      `json:"focusedMessageIndex"`
}

// MessageSnapshot represents a single message with ordered blocks/tool info.
type MessageSnapshot struct {
	Index         int                 `json:"index"`
	Role          string              `json:"role"`
	Content       string              `json:"content"`
	Thinking      string              `json:"thinking,omitempty"`
	ToolCalls     []ToolCallDisplay   `json:"toolCalls,omitempty"`
	ToolResults   []ToolResultDisplay `json:"toolResults,omitempty"`
	OrderedBlocks []MessageBlock      `json:"orderedBlocks,omitempty"`
	Metadata      map[string]any      `json:"metadata,omitempty"`
}

// CacheSnapshot captures prompt caching state.
type CacheSnapshot struct {
	Enabled bool           `json:"enabled"`
	TTL     string         `json:"ttl"`
	HitRate float64        `json:"hitRate"`
	Metrics map[string]int `json:"metrics,omitempty"`
}

// SidePanelSnapshot summarizes side panel information and its rendered view.
type SidePanelSnapshot struct {
	Provider        string        `json:"provider"`
	ProviderDisplay string        `json:"providerDisplay"`
	Model           string        `json:"model"`
	ModelDisplay    string        `json:"modelDisplay"`
	TokenCount      int           `json:"tokenCount"`
	Cache           CacheSnapshot `json:"cache"`
	ActiveModes     []string      `json:"activeModes,omitempty"`
	Rendered        string        `json:"rendered"`
}

// exportRenderSnapshot captures the current chat screen render plus structured metadata and
// copies both the human-readable render + JSON payload to the clipboard and disk.
func (a *App) exportRenderSnapshot() error {
	if a.width == 0 || a.height == 0 {
		return fmt.Errorf("screen size not initialized")
	}

	if a.screen != ScreenChat {
		return fmt.Errorf("render export is only available on the chat screen")
	}

	showSidePanel := a.showSidePanel && a.width >= MinWidthForSidePanel
	chatWidth := a.width
	if showSidePanel {
		chatWidth = a.width - SidePanelWidth
	}

	// Temporarily adjust width/viewport to match the on-screen render.
	oldWidth := a.width
	oldViewportWidth := a.msgViewport.Width
	oldViewportHeight := a.msgViewport.Height

	a.width = chatWidth
	a.msgViewport.Width = chatWidth - 4

	chatContent := a.renderChatContent()

	// Restore dimensions
	a.width = oldWidth
	a.msgViewport.Width = oldViewportWidth
	a.msgViewport.Height = oldViewportHeight

	var combined string
	var sidePanelRendered string

	if showSidePanel {
		panel := NewSidePanel(SidePanelWidth, a.height)
		sidePanelRendered = panel.Render(a)
		combined = lipgloss.JoinHorizontal(
			lipgloss.Top,
			chatContent,
			sidePanelRendered,
		)
	} else {
		combined = chatContent
	}

	snapshot := a.buildRenderSnapshot(combined, chatWidth, showSidePanel, sidePanelRendered)

	jsonBytes, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal render snapshot: %w", err)
	}

	var export strings.Builder
	export.WriteString("========== SWARMOS RENDER SNAPSHOT ==========\n")
	export.WriteString(fmt.Sprintf("Captured: %s\n", snapshot.Timestamp))
	export.WriteString(fmt.Sprintf("Screen: %s | size: %dx%d | chat: %d | side panel: %v\n\n",
		snapshot.Screen, snapshot.Terminal.Width, snapshot.Terminal.Height, chatWidth, showSidePanel))
	export.WriteString("----- Rendered View (ANSI preserved) -----\n")
	export.WriteString(combined)
	export.WriteString("\n\n----- JSON Snapshot -----\n")
	export.Write(jsonBytes)
	export.WriteString("\n")

	timestamp := time.Now().Format("20060102_150405")
	homeDir, _ := os.UserHomeDir()
	txtPath := filepath.Join(homeDir, fmt.Sprintf("swarmos_render_snapshot_%s.txt", timestamp))
	jsonPath := filepath.Join(homeDir, fmt.Sprintf("swarmos_render_snapshot_%s.json", timestamp))

	if err := os.WriteFile(txtPath, []byte(export.String()), 0644); err != nil {
		logDebug("Failed to write render snapshot txt: %v", err)
	}
	if err := os.WriteFile(jsonPath, jsonBytes, 0644); err != nil {
		logDebug("Failed to write render snapshot json: %v", err)
	}

	clipboardCount := writeClipboard(export.String())
	logDebug("Render snapshot captured: clipboards=%d, txt=%s, json=%s", clipboardCount, txtPath, jsonPath)

	if clipboardCount == 0 {
		a.addNotification("warning", i18n.T("classic_chat_3.export.saved_no_clipboard", txtPath))
	} else {
		a.addNotification("success", i18n.T("classic_chat_3.export.copied_saved", clipboardCount, txtPath))
	}

	return nil
}

// buildRenderSnapshot collects the structured metadata for a render export.
func (a *App) buildRenderSnapshot(rendered string, chatWidth int, showSidePanel bool, sidePanelRendered string) RenderSnapshot {
	screenName := func() string {
		switch a.screen {
		case ScreenHome:
			return "home"
		case ScreenChats:
			return "conversations"
		case ScreenChat:
			return "chat"
		default:
			return "unknown"
		}
	}()

	viewportLines := a.msgViewport.visibleLines()
	viewportLinesStripped := make([]string, 0, len(viewportLines))
	for _, line := range viewportLines {
		viewportLinesStripped = append(viewportLinesStripped, stripANSI(line))
	}

	snapshot := RenderSnapshot{
		Timestamp: time.Now().Format(time.RFC3339Nano),
		Screen:    screenName,
		Terminal: TerminalSnapshot{
			Width:          a.width,
			Height:         a.height,
			ChatWidth:      chatWidth,
			ShowSidePanel:  showSidePanel,
			SidePanelWidth: SidePanelWidth,
		},
		Input: InputSnapshot{
			Value:        a.textInput.Value(),
			Cursor:       a.textInput.cursor,
			Width:        a.textInput.width,
			Height:       a.textInput.height,
			ScrollOffset: a.textInput.scrollOffset,
			Focused:      a.textInput.focused,
		},
		Viewport: ViewportSnapshot{
			Width:                a.msgViewport.Width,
			Height:               a.msgViewport.Height,
			YOffset:              a.msgViewport.YOffset,
			XOffset:              a.msgViewport.xOffset,
			TotalLines:           a.msgViewport.TotalLineCount(),
			VisibleLineCount:     len(viewportLines),
			LongestLineWidth:     a.msgViewport.longestLineWidth,
			VisibleLines:         viewportLines,
			VisibleLinesStripped: viewportLinesStripped,
			MessageNavMode:       a.messageNavMode,
			FocusedMessageIndex:  a.focusedMessageIdx,
		},
		Notifications:    a.notifications,
		Rendered:         rendered,
		RenderedStripped: stripANSI(rendered),
	}

	if a.activeConv != nil {
		snapshot.Conversation = &ConversationSnapshot{
			ID:                 a.activeConv.ID,
			Title:              a.activeConv.Title,
			Status:             a.activeConv.Status,
			MessageCount:       len(a.messages),
			TokenCount:         a.tokenCount,
			WaitingFor:         a.activeConv.WaitingFor,
			AgentThought:       a.activeConv.AgentThought,
			ShowThinking:       a.showThinking,
			ShowFullToolOutput: a.showFullToolOutput,
			Provider:           a.currentProvider,
			ProviderDisplay:    a.currentProviderDisplay,
			Model:              a.currentModel,
			ModelDisplay:       a.currentModelDisplay,
		}
	}

	for idx, msg := range a.messages {
		snapshot.Messages = append(snapshot.Messages, MessageSnapshot{
			Index:         idx,
			Role:          msg.Role,
			Content:       msg.Content,
			Thinking:      msg.Thinking,
			ToolCalls:     msg.ToolCalls,
			ToolResults:   msg.ToolResults,
			OrderedBlocks: msg.OrderedBlocks,
			Metadata:      msg.Metadata,
		})
	}

	if showSidePanel {
		snapshot.SidePanel = &SidePanelSnapshot{
			Provider:        a.currentProvider,
			ProviderDisplay: a.currentProviderDisplay,
			Model:           a.currentModel,
			ModelDisplay:    a.currentModelDisplay,
			TokenCount:      a.tokenCount,
			Cache: CacheSnapshot{
				Enabled: a.sdk != nil && a.sdk.IsCachingEnabled(),
			},
			Rendered: sidePanelRendered,
		}

		if a.sdk != nil {
			snapshot.SidePanel.Cache.TTL = a.sdk.GetCacheTTL()
			snapshot.SidePanel.Cache.HitRate = a.sdk.GetCacheHitRate()
			snapshot.SidePanel.Cache.Metrics = a.sdk.GetCacheMetrics()
		}

		var activeModes []string
		if a.showFullToolOutput {
			activeModes = append(activeModes, "full_tool_output")
		}
		if a.showThinking {
			activeModes = append(activeModes, "thinking_visible")
		}
		if a.workspaceMode {
			activeModes = append(activeModes, "workspace_mode")
		}
		if len(activeModes) > 0 {
			snapshot.SidePanel.ActiveModes = activeModes
		}
	}

	return snapshot
}
