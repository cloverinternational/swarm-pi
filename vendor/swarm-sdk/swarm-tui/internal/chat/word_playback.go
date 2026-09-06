package chat

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

const defaultWordPlaybackStepInterval = 220 * time.Millisecond

type wordPlaybackState struct {
	Active          bool
	MessageIdx      int
	WordIdx         int
	Words           []string
	StartedAt       time.Time
	LastStepAt      time.Time
	StepInterval    time.Duration
	clockSubscribed bool
}

func (a *App) latestPlayableAgentMessageIndex() int {
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role != "assistant" && msg.Role != string(conversation.RolePeer) {
			continue
		}
		if strings.TrimSpace(playableMessageText(msg)) == "" {
			continue
		}
		return i
	}
	return -1
}

func playableMessageText(msg *Message) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, block := range msg.OrderedBlocks {
		if block.Type == "content" {
			if content := strings.TrimSpace(block.GetContent()); content != "" {
				parts = append(parts, content)
			}
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n\n")
	}
	return msg.GetContent()
}

func splitPlayableWords(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool {
		return unicode.IsSpace(r)
	})
}

func (a *App) toggleWordPlayback() tea.Cmd {
	if a.streamingMessage || a.streamingInProgress {
		a.addNotification("info", "Agent is still responding; replay will be available when complete")
		return nil
	}

	idx := a.latestPlayableAgentMessageIndex()
	if idx < 0 {
		a.addNotification("info", "No assistant message available to replay")
		return nil
	}

	if a.wordPlayback.Active && a.wordPlayback.MessageIdx == idx {
		a.stopWordPlayback()
		a.messages[idx].MarkDirty()
		a.updateViewportIncremental()
		return nil
	}

	return a.startWordPlayback(idx)
}

func (a *App) startWordPlayback(idx int) tea.Cmd {
	if a.wordPlayback.Active {
		a.stopWordPlayback()
	}
	if idx < 0 || idx >= len(a.messages) {
		return nil
	}
	words := splitPlayableWords(playableMessageText(&a.messages[idx]))
	if len(words) == 0 {
		a.addNotification("info", "No words available to replay")
		return nil
	}

	now := time.Now()
	interval := defaultWordPlaybackStepInterval
	a.wordPlayback = wordPlaybackState{
		Active:       true,
		MessageIdx:   idx,
		WordIdx:      0,
		Words:        words,
		StartedAt:    now,
		LastStepAt:   now,
		StepInterval: interval,
	}
	if a.animationClock != nil {
		// Word playback participates in the shared animation clock, but it must
		// not own the clock mode. Streaming/fast-vs-idle cadence is governed by
		// the central activity/animation lifecycle (spinner, loading row,
		// diffusion reveal), so playback only subscribes for ticks.
		a.wordPlayback.clockSubscribed = true
	}
	a.messages[idx].MarkDirty()
	a.updateViewportIncremental()
	if a.msgViewport != nil && !a.userScrolledAway {
		a.msgViewport.GotoBottom()
	}
	if a.animationClock != nil {
		return a.animationClock.Subscribe()
	}
	return nil
}

func (a *App) stopWordPlayback() {
	a.finishWordPlayback()
}

// finishWordPlayback clears playback state. Returns whether playback was active.
func (a *App) finishWordPlayback() bool {
	wasActive := a.wordPlayback.Active
	wasSubscribed := a.wordPlayback.clockSubscribed
	idx := a.wordPlayback.MessageIdx
	a.wordPlayback = wordPlaybackState{MessageIdx: -1}
	if wasSubscribed && a.animationClock != nil {
		a.animationClock.Unsubscribe()
	}
	if wasActive && idx >= 0 && idx < len(a.messages) {
		a.messages[idx].MarkDirty()
	}
	return wasActive
}

func (a *App) advanceWordPlayback(now time.Time) bool {
	if !a.wordPlayback.Active {
		return false
	}
	idx := a.wordPlayback.MessageIdx
	if idx < 0 || idx >= len(a.messages) {
		a.stopWordPlayback()
		return true
	}
	if len(a.wordPlayback.Words) == 0 {
		a.stopWordPlayback()
		return true
	}
	interval := a.wordPlayback.StepInterval
	if interval <= 0 {
		interval = defaultWordPlaybackStepInterval
	}
	if !a.wordPlayback.LastStepAt.IsZero() && now.Sub(a.wordPlayback.LastStepAt) < interval {
		return false
	}
	a.wordPlayback.LastStepAt = now
	a.wordPlayback.WordIdx++
	if a.wordPlayback.WordIdx >= len(a.wordPlayback.Words) {
		a.finishWordPlayback()
		return true
	}
	return true
}

func (a *App) wordPlaybackLine(width int) string {
	if !a.wordPlayback.Active || len(a.wordPlayback.Words) == 0 {
		return ""
	}
	idx := a.wordPlayback.WordIdx
	if idx < 0 {
		idx = 0
	}
	if idx >= len(a.wordPlayback.Words) {
		idx = len(a.wordPlayback.Words) - 1
	}
	word := a.wordPlayback.Words[idx]
	label := "▶ Reading"
	if idx > 0 {
		label = "▶ Reading"
	}
	text := fmt.Sprintf("%s %d/%d “%s” · Alt+R pause", label, idx+1, len(a.wordPlayback.Words), word)
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(a.theme.Accent)).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(a.theme.TextMuted))
	line := accent.Render("▶ Reading") + muted.Render(fmt.Sprintf(" %d/%d “%s” · Alt+R pause", idx+1, len(a.wordPlayback.Words), word))
	if width > 4 && lipgloss.Width(line) > width-2 {
		return "  " + muted.Render(truncateVisual(text, width-2))
	}
	return "  " + line
}

func truncateVisual(s string, maxWidth int) string {
	if maxWidth <= 0 || lipgloss.Width(s) <= maxWidth {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > maxWidth {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
