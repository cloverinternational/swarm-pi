package chat

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

const historyPreviewTargetLimit = 72

type historyPreviewAction struct {
	TurnPrompt string
	Label      string
	Target     string
	Status     string
}

type historyPreviewDetail struct {
	ConversationID  string
	FirstUserPrompt string
	LastUserPrompt  string
	Actions         []historyPreviewAction
	Err             error
}

type historyPreviewLoadedMsg struct {
	detail historyPreviewDetail
}

func buildHistoryPreviewDetail(convID string, messages []*conversation.Message) historyPreviewDetail {
	detail := historyPreviewDetail{ConversationID: convID}
	results := make(map[string]*conversation.ToolResult)
	for _, msg := range messages {
		if isGeneratedCompactionMessage(msg) {
			continue
		}
		for i := range msg.ToolResults {
			result := &msg.ToolResults[i]
			results[result.CallID] = result
		}
	}

	currentPrompt := ""
	for _, msg := range messages {
		if isGeneratedCompactionMessage(msg) {
			continue
		}
		if msg.Role == conversation.RoleUser {
			prompt := cleanHistoryPreviewText(msg.Content)
			if prompt != "" {
				if detail.FirstUserPrompt == "" {
					detail.FirstUserPrompt = prompt
				}
				detail.LastUserPrompt = prompt
				currentPrompt = prompt
			}
		}
		for _, call := range msg.ToolCalls {
			status := "pending"
			if result, ok := results[call.ID]; ok {
				status = "done"
				if result.Error != nil {
					status = "error"
				}
			}
			detail.Actions = append(detail.Actions, historyPreviewAction{
				TurnPrompt: currentPrompt,
				Label:      historyToolLabel(call.Name),
				Target:     historyToolTarget(call.Parameters),
				Status:     status,
			})
		}
	}
	return detail
}

func cleanHistoryPreviewText(value string) string {
	value = stripANSI(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func historyToolLabel(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	switch normalized {
	case "read":
		return i18n.T("classic_chat_2.history.action.read")
	case "grep", "historysearch", "websearch", "x_search", "xai_web_search":
		return i18n.T("classic_chat_2.history.action.search")
	case "bash":
		return i18n.T("classic_chat_2.history.action.run")
	case "apply_patch", "edit", "write":
		return i18n.T("classic_chat_2.history.action.edit")
	case "subagent", "delegate", "backgroundtask":
		return i18n.T("classic_chat_2.history.action.delegate")
	case "taskmanage":
		return i18n.T("classic_chat_2.history.action.tasks")
	case "skill", "skillmanage":
		return i18n.T("classic_chat_2.history.action.skill")
	}
	if name == "" {
		return i18n.T("classic_chat_2.history.action.tool")
	}
	runes := []rune(strings.ReplaceAll(name, "_", " "))
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func historyToolTarget(parameters map[string]any) string {
	if len(parameters) == 0 {
		return ""
	}
	keys := []string{
		"file_path", "path", "command", "query", "pattern", "task",
		"url", "skill", "name", "subject", "description",
	}
	for _, key := range keys {
		if value, ok := parameters[key]; ok {
			if target := boundedHistoryTarget(fmt.Sprint(value)); target != "" {
				return target
			}
		}
	}

	sorted := make([]string, 0, len(parameters))
	for key := range parameters {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	return boundedHistoryTarget(strings.Join(sorted, ", "))
}

func boundedHistoryTarget(value string) string {
	value = cleanHistoryPreviewText(value)
	if value == "" {
		return ""
	}
	runes := []rune(value)
	if len(runes) > historyPreviewTargetLimit {
		return string(runes[:historyPreviewTargetLimit-1]) + "…"
	}
	return value
}

func (a *App) loadHistoryPreviewAsync(convID string) {
	if convID == "" || a.sdk == nil {
		return
	}
	if a.historyPreview.ConversationID == convID && !a.historyPreviewLoading {
		return
	}
	a.historyPreviewLoading = true
	a.historyPreview = historyPreviewDetail{ConversationID: convID}

	sdk := a.sdk
	queue := a.updateQueue
	go func() {
		messages, err := sdk.GetMessages(context.Background(), convID)
		detail := buildHistoryPreviewDetail(convID, messages)
		detail.Err = err
		queue <- historyPreviewLoadedMsg{detail: detail}
		a.wakeRuntimeAsync()
	}()
}

func (a *App) loadSelectedHistoryPreview() {
	if a.selectedIdx < 0 || a.selectedIdx >= len(a.conversations) {
		return
	}
	a.loadHistoryPreviewAsync(a.conversations[a.selectedIdx].ID)
}

func (a *App) applyHistoryPreviewLoaded(msg historyPreviewLoadedMsg) {
	if a.selectedIdx < 0 || a.selectedIdx >= len(a.conversations) {
		return
	}
	if a.conversations[a.selectedIdx].ID != msg.detail.ConversationID {
		return
	}
	a.historyPreview = msg.detail
	a.historyPreviewLoading = false
}

func (a *App) renderHistoryPreview(contentWidth, height int) string {
	th := a.theme
	if contentWidth < 1 || height < 1 {
		return ""
	}
	if a.selectedIdx < 0 || a.selectedIdx >= len(a.conversations) {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color(th.TextMuted)).
			Render(i18n.T("classic_chat_2.history.no_session"))
	}

	conv := a.conversations[a.selectedIdx]
	detail := a.historyPreview
	first := cleanHistoryPreviewText(conv.FirstUserPrompt)
	if detail.ConversationID == conv.ID && detail.FirstUserPrompt != "" {
		first = detail.FirstUserPrompt
	}
	last := ""
	if detail.ConversationID == conv.ID && detail.LastUserPrompt != "" {
		last = detail.LastUserPrompt
	}
	if last == "" {
		if a.historyPreviewLoading {
			last = i18n.T("classic_chat_2.history.loading_last_prompt")
		} else {
			last = i18n.T("classic_chat_2.history.no_prompt")
		}
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Primary)).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Text))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Success))
	errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Error))

	lines := []string{titleStyle.Render(truncateString(conv.Title, contentWidth))}
	if height <= 6 {
		summary := i18n.T("classic_chat_2.history.summary", conv.MessageCount, conv.ToolCallCount)
		if a.historyPreviewLoading {
			summary += i18n.T("classic_chat_2.history.loading_suffix")
		}
		lines = append(lines, mutedStyle.Render(truncateString(summary, contentWidth)))
		if first != "" && len(lines) < height {
			label := i18n.T("classic_chat_2.history.first") + " "
			lines = append(lines, labelStyle.Render(label)+bodyStyle.Render(truncateString(first, max(1, contentWidth-lipgloss.Width(label)))))
		}
		if len(lines) < height {
			label := i18n.T("classic_chat_2.history.last") + "  "
			lines = append(lines, labelStyle.Render(label)+bodyStyle.Render(truncateString(last, max(1, contentWidth-lipgloss.Width(label)))))
		}
		return strings.Join(lines[:min(len(lines), height)], "\n")
	}

	appendWrapped := func(label, text string) {
		if len(lines) >= height {
			return
		}
		if text == "" {
			text = i18n.T("classic_chat_2.history.no_prompt")
		}
		prefix := label + "  "
		wrapWidth := max(1, contentWidth-lipgloss.Width(prefix))
		wrapped := wordWrapText(text, wrapWidth)
		if len(wrapped) == 0 {
			wrapped = []string{text}
		}
		lines = append(lines, labelStyle.Render(prefix)+bodyStyle.Render(wrapped[0]))
		for _, line := range wrapped[1:] {
			if len(lines) >= height {
				break
			}
			lines = append(lines, strings.Repeat(" ", lipgloss.Width(prefix))+bodyStyle.Render(line))
		}
	}

	appendWrapped(i18n.T("classic_chat_2.history.first"), first)
	if len(lines) < height {
		lines = append(lines, labelStyle.Render(i18n.T("classic_chat_2.history.actions")))
	}
	if a.historyPreviewLoading && detail.ConversationID == conv.ID {
		if len(lines) < height {
			lines = append(lines, mutedStyle.Render(i18n.T("classic_chat_2.history.loading_actions")))
		}
	} else if detail.Err != nil {
		if len(lines) < height {
			lines = append(lines, errorStyle.Render(i18n.T("classic_chat_2.history.load_actions_failed")))
		}
	} else if len(detail.Actions) == 0 {
		if len(lines) < height {
			lines = append(lines, mutedStyle.Render(i18n.T("classic_chat_2.history.no_actions")))
		}
	} else {
		shown := 0
		currentTurn := ""
		for i, action := range detail.Actions {
			if len(lines) >= height-1 {
				break
			}
			if action.TurnPrompt != "" && action.TurnPrompt != currentTurn {
				currentTurn = action.TurnPrompt
				if len(lines) < height-2 {
					turn := "  • " + truncateString(currentTurn, max(1, contentWidth-4))
					lines = append(lines, mutedStyle.Render(turn))
				}
			}
			if len(lines) >= height-1 {
				break
			}
			branch := "├─"
			if i == len(detail.Actions)-1 {
				branch = "└─"
			}
			status := successStyle.Render("✓")
			if action.Status == "error" {
				status = errorStyle.Render("×")
			} else if action.Status == "pending" {
				status = mutedStyle.Render("·")
			}
			text := fmt.Sprintf("  %s %s %s", branch, status, action.Label)
			if action.Target != "" {
				text += "  " + action.Target
			}
			lines = append(lines, truncateString(text, contentWidth))
			shown++
		}
		if remaining := len(detail.Actions) - shown; remaining > 0 && len(lines) < height-1 {
			lines = append(lines, mutedStyle.Render(i18n.T("classic_chat_2.history.more_actions", remaining)))
		}
	}
	if len(lines) < height {
		appendWrapped(i18n.T("classic_chat_2.history.last"), last)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}
