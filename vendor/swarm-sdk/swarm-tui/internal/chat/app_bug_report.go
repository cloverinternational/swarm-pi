package chat

import (
	"encoding/json"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"
)

// handleBugReport is called when the user runs /bug.  It collects a complete
// snapshot of the in-memory client state and sends it to Sentry as an
// informational event (not an error), then displays a confirmation notification.
func (a *App) handleBugReport(msg commands.BugReportMsg) (tea.Model, tea.Cmd) {
	a.addNotification("info", tr("classic.bug.collecting"))

	state := BugReportState{
		Description:  msg.Description,
		CapturedAt:   time.Now(),
		Version:      version.LogFields(),
		RuntimeStats: collectRuntimeStats(),
	}

	// ── In-memory logs ────────────────────────────────────────────────────────
	if a.debugScreen != nil && a.debugScreen.enhancedLogsView != nil {
		entries := a.debugScreen.enhancedLogsView.buffer.GetAll()
		logs := make([]string, 0, len(entries))
		for _, e := range entries {
			logs = append(logs, e.Raw)
		}
		state.Logs = logs
	}

	// ── API request summaries ─────────────────────────────────────────────────
	if a.debugScreen != nil {
		reqs := a.debugScreen.requests
		summaries := make([]DebugRequestSummary, 0, len(reqs))
		for _, r := range reqs {
			s := DebugRequestSummary{
				ID:           r.ID,
				Timestamp:    r.Timestamp,
				Method:       r.Method,
				URL:          r.URL,
				ResponseCode: r.ResponseCode,
				DurationMS:   r.Duration.Milliseconds(),
				TokensIn:     r.TokensInput,
				TokensOut:    r.TokensOutput,
				Model:        r.Model,
			}
			if r.Error != "" {
				s.HasError = true
				s.Error = r.Error
			}
			summaries = append(summaries, s)
		}
		state.DebugRequests = summaries
	}

	// ── Message summaries (no content — privacy) ──────────────────────────────
	msgSummaries := make([]MessageSummary, 0, len(a.messages))
	for _, m := range a.messages {
		s := MessageSummary{
			Role:         m.Role,
			ContentLen:   len(m.Content),
			HasToolCalls: len(m.ToolCalls) > 0,
			HasThinking:  m.Thinking != "",
			InputTokens:  m.InputTokens,
			OutputTokens: m.OutputTokens,
			Model:        m.Model,
		}
		msgSummaries = append(msgSummaries, s)
	}
	state.MessageSummaries = msgSummaries

	// ── Config summary (sanitized) ────────────────────────────────────────────
	// ── Config summary (sanitized) ────────────────────────────────────────────
	if cfg := a.GetConfig(); cfg != nil {
		// Marshal → unmarshal into a generic map, then sanitize
		if raw, err := json.Marshal(cfg); err == nil {
			var cfgMap map[string]any
			if err := json.Unmarshal(raw, &cfgMap); err == nil {
				state.ConfigSummary = sanitizeConfigMap(cfgMap)
			}
		}
	}

	// ── Screen / session state ────────────────────────────────────────────────
	screenName := screenName(a.screen)
	screenState := map[string]any{
		"screen":          screenName,
		"width":           a.width,
		"height":          a.height,
		"conversation_id": a.currentConvID,
		"message_count":   len(a.messages),
		"token_count":     a.tokenCount,
	}
	if a.sdk != nil {
		screenState["provider"] = a.sdk.GetProviderName()
		screenState["model"] = a.sdk.GetCurrentModel()
		screenState["operating_mode"] = a.sdk.GetOperatingMode()
	}
	state.ScreenState = screenState

	// ── Send to Sentry ────────────────────────────────────────────────────────
	eventID := CaptureBugReportEvent(state)

	if eventID != nil && *eventID != "" {
		a.addNotification("success", tr("classic.bug.sent", string(*eventID)))
	} else {
		// Sentry may not be initialised in dev builds; still thank the user.
		a.addNotification("info", tr("classic.bug.collected"))
	}

	return a, nil
}

// screenName returns a human-readable string for the current Screen constant.
func screenName(s Screen) string {
	switch s {
	case ScreenHome:
		return "ScreenHome"
	case ScreenChats:
		return "ScreenChats"
	case ScreenChat:
		return "ScreenChat"
	case ScreenSettings:
		return "ScreenSettings"
	case ScreenViewer:
		return "ScreenViewer"
	case ScreenNewChatUI:
		return "ScreenNewChatUI"
	case ScreenGit:
		return "ScreenGit"
	default:
		return fmt.Sprintf("Screen(%d)", int(s))
	}
}
