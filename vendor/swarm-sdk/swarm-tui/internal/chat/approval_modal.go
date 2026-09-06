package chat

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

type approvalOption struct {
	label    string
	decision tools.Decision
	hotkey   string // display hint for the hotkey
	color    func(Theme) string
}

// primaryOptions are the 3 main visible buttons.
var primaryOptions = []approvalOption{
	{label: "commands.common.yes", decision: tools.DecisionApproveOnce, hotkey: "y", color: func(th Theme) string { return th.Success }},
	{label: "final.approval.session", decision: tools.DecisionApproveSession, hotkey: "s", color: func(th Theme) string { return th.Primary }},
	{label: "commands.common.no", decision: tools.DecisionDeny, hotkey: "n", color: func(th Theme) string { return th.Error }},
}

type approvalFocus int

const (
	focusButtons approvalFocus = iota
	focusContext
)

// ApprovalModal renders a permission approval prompt.
type ApprovalModal struct {
	request    tools.PermissionApprovalRequest
	selected   int
	queueIndex int
	queueTotal int
	onSelect   func(tools.Decision, string) // decision + user context

	// Context input state
	focus         approvalFocus
	contextInput  []rune
	contextCursor int

	// Auto-mode annotation (populated when the tool was classified as
	// "prompt" by builtin.AutoModeHook). When zero-valued, the modal
	// renders without the risk banner.
	autoModeRisk       string // "low" | "medium" | "high"
	autoModeReason     string
	autoModeConfidence float64
}

// SetAutoModeAnnotation attaches the auto-mode classifier's verdict so the
// modal can surface a risk banner above the buttons. Call this before the
// modal is rendered; it is a no-op when risk is empty.
func (m *ApprovalModal) SetAutoModeAnnotation(risk, reason string, confidence float64) {
	m.autoModeRisk = risk
	m.autoModeReason = reason
	m.autoModeConfidence = confidence
}

// NewApprovalModal creates a new approval modal.
func NewApprovalModal(req tools.PermissionApprovalRequest, idx, total int, onSelect func(tools.Decision, string)) *ApprovalModal {
	return &ApprovalModal{
		request:    req,
		selected:   0,
		queueIndex: idx,
		queueTotal: total,
		onSelect:   onSelect,
		focus:      focusButtons,
	}
}

// SetQueueInfo updates queue position info.
func (m *ApprovalModal) SetQueueInfo(idx, total int) {
	m.queueIndex = idx
	m.queueTotal = total
}

// Update handles key input for the modal.
func (m *ApprovalModal) Update(key string) {
	if m.focus == focusContext {
		m.updateContextInput(key)
		return
	}
	m.updateButtons(key)
}

func (m *ApprovalModal) updateButtons(key string) {
	n := len(primaryOptions)
	switch key {
	case "h", "left":
		m.selected = (m.selected + n - 1) % n
	case "l", "right":
		m.selected = (m.selected + 1) % n
	case "y", "1":
		m.submitDecision(tools.DecisionApproveOnce)
	case "s", "2":
		m.submitDecision(tools.DecisionApproveSession)
	case "n", "3":
		m.submitDecision(tools.DecisionDeny)
	case "enter", " ":
		m.submitDecision(primaryOptions[m.selected].decision)
	case "esc":
		m.submitDecision(tools.DecisionDeny)
	case "tab":
		m.focus = focusContext
	case "ctrl+p":
		m.submitDecision(tools.DecisionSaveProject)
	case "ctrl+a":
		m.submitDecision(tools.DecisionApproveAlways)
	case "ctrl+x":
		m.submitDecision(tools.DecisionDenyStop)
	}
}

func (m *ApprovalModal) updateContextInput(key string) {
	switch key {
	case "tab", "esc":
		m.focus = focusButtons
	case "enter":
		m.submitDecision(primaryOptions[m.selected].decision)
	case "backspace":
		if m.contextCursor > 0 {
			m.contextInput = append(m.contextInput[:m.contextCursor-1], m.contextInput[m.contextCursor:]...)
			m.contextCursor--
		}
	case "delete":
		if m.contextCursor < len(m.contextInput) {
			m.contextInput = append(m.contextInput[:m.contextCursor], m.contextInput[m.contextCursor+1:]...)
		}
	case "left":
		if m.contextCursor > 0 {
			m.contextCursor--
		}
	case "right":
		if m.contextCursor < len(m.contextInput) {
			m.contextCursor++
		}
	case "home", "ctrl+a":
		m.contextCursor = 0
	case "end", "ctrl+e":
		m.contextCursor = len(m.contextInput)
	case "ctrl+u":
		m.contextInput = nil
		m.contextCursor = 0
	default:
		// Insert printable characters
		if utf8.RuneCountInString(key) == 1 {
			r := []rune(key)[0]
			if r >= 32 { // printable
				m.contextInput = append(m.contextInput[:m.contextCursor], append([]rune{r}, m.contextInput[m.contextCursor:]...)...)
				m.contextCursor++
			}
		}
	}
}

// HandlePaste inserts pasted text into the context input.
func (m *ApprovalModal) HandlePaste(text string) {
	if text == "" {
		return
	}
	// Only paste into context input (buttons don't accept text)
	if m.focus != focusContext {
		// Auto-switch to context mode when pasting
		m.focus = focusContext
	}
	runes := []rune(text)
	newInput := make([]rune, 0, len(m.contextInput)+len(runes))
	newInput = append(newInput, m.contextInput[:m.contextCursor]...)
	newInput = append(newInput, runes...)
	newInput = append(newInput, m.contextInput[m.contextCursor:]...)
	m.contextInput = newInput
	m.contextCursor += len(runes)
}

func (m *ApprovalModal) submitDecision(decision tools.Decision) {
	if m.onSelect != nil {
		m.onSelect(decision, string(m.contextInput))
	}
}

// UserContext returns the current context input text.
func (m *ApprovalModal) UserContext() string {
	return string(m.contextInput)
}

// Render renders the approval modal.
func (m *ApprovalModal) Render(width, height int, theme Theme) string {
	modalWidth := 84
	if modalWidth > width-8 {
		modalWidth = width - 8
	}
	if modalWidth < 60 {
		modalWidth = width - 2
		if modalWidth < 40 {
			modalWidth = width
		}
	}

	contentWidth := modalWidth - 6

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Warning)).
		Bold(true)

	title := i18n.T("final.approval.title")
	if m.queueTotal > 1 {
		title = i18n.T("final.approval.title_queued", m.queueIndex, m.queueTotal)
	}

	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	valueStyle := lipgloss.NewStyle()

	lines := []string{""}
	lines = append(lines, titleStyle.Render(title))
	lines = append(lines, "")

	lines = append(lines, wrapField(i18n.T("final.approval.tool"), m.request.Tool, contentWidth, labelStyle, valueStyle)...)
	lines = append(lines, wrapField(i18n.T("final.approval.permission"), m.request.Permission, contentWidth, labelStyle, valueStyle)...)
	if m.request.Target != "" {
		lines = append(lines, wrapField(i18n.T("final.approval.target"), m.request.Target, contentWidth, labelStyle, valueStyle)...)
	}
	if m.request.Reason != "" {
		lines = append(lines, wrapField(i18n.T("final.approval.reason"), m.request.Reason, contentWidth, labelStyle, valueStyle)...)
	}
	if m.request.Context != nil {
		if m.request.Context.AgentID != "" {
			lines = append(lines, wrapField(i18n.T("classic_chat_3.question.agent"), m.request.Context.AgentID, contentWidth, labelStyle, valueStyle)...)
		}
		if m.request.Context.TaskDescription != "" {
			lines = append(lines, wrapField(i18n.T("final.approval.task"), m.request.Context.TaskDescription, contentWidth, labelStyle, valueStyle)...)
		}
	}

	if m.request.Preview != nil && m.request.Preview.Content != "" {
		lines = append(lines, "")
		previewTitle := i18n.T("final.approval.preview_type", m.request.Preview.Type)
		lines = append(lines, labelStyle.Render(previewTitle))

		maxPreviewLines := 10
		if height > 0 {
			maxPreviewLines = min(10, max(4, height/4))
		}

		previewLines := buildPreviewLines(m.request.Preview, contentWidth, maxPreviewLines)
		previewBox := lipgloss.NewStyle().
			Width(contentWidth).
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(theme.Border)).
			Padding(0, 1)
		lines = append(lines, previewBox.Render(strings.Join(previewLines, "\n")))
	}

	if banner := m.renderAutoModeBanner(theme); banner != "" {
		lines = append(lines, "")
		lines = append(lines, banner)
	}

	lines = append(lines, "")
	lines = append(lines, m.renderButtons(theme))

	// Context input area
	if m.focus == focusContext || len(m.contextInput) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderContextInput(contentWidth, theme))
	}

	lines = append(lines, "")
	lines = append(lines, m.renderHints(theme))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Warning)).
		Padding(1, 2)

	return modalStyle.Render(content)
}

// RenderBar renders a prominent permission card for the chat input area.
// This replaces the chat input when active.
func (m *ApprovalModal) RenderBar(width int, theme Theme) string {
	if width <= 0 {
		return ""
	}

	barWidth := width - 4
	if barWidth < 40 {
		barWidth = width
	}
	contentWidth := barWidth - 6

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Warning)).
		Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	valueStyle := lipgloss.NewStyle()

	title := i18n.T("final.approval.title")
	if m.queueTotal > 1 {
		title = i18n.T("final.approval.title_queued", m.queueIndex, m.queueTotal)
	}

	lines := []string{""}
	lines = append(lines, titleStyle.Render(title))
	lines = append(lines, "")

	// Show fields with proper formatting
	lines = append(lines, wrapField(i18n.T("final.approval.tool"), m.request.Tool, contentWidth, labelStyle, valueStyle)...)
	lines = append(lines, wrapField(i18n.T("final.approval.permission"), m.request.Permission, contentWidth, labelStyle, valueStyle)...)
	if m.request.Target != "" {
		lines = append(lines, wrapField(i18n.T("final.approval.target"), m.request.Target, contentWidth, labelStyle, valueStyle)...)
	}
	if m.request.Reason != "" {
		lines = append(lines, wrapField(i18n.T("final.approval.reason"), m.request.Reason, contentWidth, labelStyle, valueStyle)...)
	}

	// Preview (compact, single line)
	if m.request.Preview != nil && m.request.Preview.Content != "" {
		preview := strings.ReplaceAll(m.request.Preview.Content, "\n", " ")
		preview = strings.Join(strings.Fields(preview), " ")
		previewLine := truncateLine(preview, contentWidth-10)
		lines = append(lines, labelStyle.Render(i18n.T("final.approval.preview_label"))+valueStyle.Render(previewLine))
		lines = append(lines, "")
	}

	// Buttons
	lines = append(lines, m.renderButtons(theme))

	// Context input
	if m.focus == focusContext || len(m.contextInput) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderContextInput(contentWidth, theme))
	}

	lines = append(lines, "")
	lines = append(lines, m.renderHints(theme))

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	cardStyle := lipgloss.NewStyle().
		Width(barWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Warning)).
		Padding(0, 2).
		MarginLeft(2)

	return cardStyle.Render(content)
}

func (m *ApprovalModal) renderButtons(theme Theme) string {
	var buttons []string
	for i, opt := range primaryOptions {
		btnStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Padding(0, 1)

		label := i18n.T(opt.label)
		if i == m.selected {
			btnStyle = btnStyle.
				Foreground(lipgloss.Color(opt.color(theme))).
				Bold(true)
			label = "> " + label + " <"
		}
		buttons = append(buttons, btnStyle.Render(label))
	}
	return lipgloss.JoinHorizontal(lipgloss.Left, buttons...)
}

func (m *ApprovalModal) renderContextInput(width int, theme Theme) string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(m.focus == focusContext)
	valueStyle := lipgloss.NewStyle()

	ctxText := string(m.contextInput)
	if m.focus == focusContext {
		// Render with block cursor
		before := string(m.contextInput[:m.contextCursor])
		after := string(m.contextInput[m.contextCursor:])
		ctxText = before + "\u2588" + after
	}

	inputBox := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Padding(0, 1)

	if m.focus == focusContext {
		inputBox = inputBox.BorderForeground(lipgloss.Color(theme.Primary))
	}

	content := valueStyle.Render(truncateLine(ctxText, width-4))
	if len(m.contextInput) == 0 && m.focus == focusContext {
		content = lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Italic(true).
			Render(i18n.T("final.approval.context_placeholder") + "\u2588")
	}

	return labelStyle.Render(i18n.T("chat_b.question.context_label")) + "\n" + inputBox.Render(content)
}

// renderAutoModeBanner emits a one-line risk annotation above the buttons
// when the auto-mode classifier asked for explicit approval. Returns an
// empty string when no annotation is attached.
func (m *ApprovalModal) renderAutoModeBanner(theme Theme) string {
	if m.autoModeRisk == "" {
		return ""
	}
	var color string
	switch m.autoModeRisk {
	case "high":
		color = theme.Error
	case "medium":
		color = theme.Warning
	default:
		color = theme.Primary
	}
	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(color)).
		Bold(true).
		Render(i18n.T("final.approval.auto_mode_risk", localizedApprovalRisk(m.autoModeRisk)))
	body := ""
	if m.autoModeReason != "" {
		body = " — " + m.autoModeReason
	}
	if m.autoModeConfidence > 0 {
		body += i18n.T("final.approval.confidence", m.autoModeConfidence)
	}
	return label + lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Render(body)
}

func (m *ApprovalModal) renderHints(theme Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)

	if m.focus == focusContext {
		return hintStyle.Render(i18n.T("final.approval.context_hint"))
	}
	return hintStyle.Render(i18n.T("final.approval.buttons_hint"))
}

func localizedApprovalRisk(risk string) string {
	switch risk {
	case "high":
		return i18n.T("final.approval.risk_high")
	case "medium":
		return i18n.T("final.approval.risk_medium")
	case "low":
		return i18n.T("final.approval.risk_low")
	default:
		return risk
	}
}

func wrapField(label, value string, width int, labelStyle, valueStyle lipgloss.Style) []string {
	if value == "" {
		return nil
	}
	prefix := label + ": "
	available := width - len(prefix)
	if available < 10 {
		available = width
		prefix = label + ":"
	}
	wrapped := wrapText(value, available)
	if len(wrapped) == 0 {
		return nil
	}
	lines := []string{labelStyle.Render(prefix) + valueStyle.Render(wrapped[0])}
	indent := strings.Repeat(" ", len(prefix))
	for _, line := range wrapped[1:] {
		lines = append(lines, labelStyle.Render(indent)+valueStyle.Render(line))
	}
	lines = append(lines, "")
	return lines
}

func buildPreviewLines(preview *tools.ApprovalPreview, width, maxLines int) []string {
	if preview == nil {
		return []string{}
	}
	rawLines := strings.Split(preview.Content, "\n")
	lines := make([]string, 0, len(rawLines))

	if preview.Type == "text" {
		for _, line := range rawLines {
			if line == "" {
				lines = append(lines, "")
				continue
			}
			lines = append(lines, wrapText(line, width)...)
		}
	} else {
		for _, line := range rawLines {
			lines = append(lines, truncateLine(line, width))
		}
	}

	if maxLines > 0 && len(lines) > maxLines {
		lines = append(lines[:maxLines], i18n.T("final.approval.more_lines", len(lines)-maxLines))
	}
	if len(lines) == 0 {
		lines = append(lines, "")
	}
	return lines
}

func truncateLine(line string, width int) string {
	if width <= 0 || len(line) <= width {
		return line
	}
	if width <= 3 {
		return line[:width]
	}
	return line[:width-3] + "..."
}
