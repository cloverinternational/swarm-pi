package chat

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/interaction"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

type questionFocus int

const (
	questionFocusInput questionFocus = iota
	questionFocusContext
)

// QuestionModal renders an interactive question prompt for the AskUser tool.
type QuestionModal struct {
	request    interaction.QuestionRequest
	queueIndex int
	queueTotal int
	onSubmit   func(interaction.QuestionResponse)
	onCancel   func()

	// Common state
	focus questionFocus

	// Text/number input
	textInput  []rune
	textCursor int

	// Choice selection (single)
	selectedIdx int

	// Multi-choice selection
	selectedItems []bool
	multiCursor   int

	// Confirm
	confirmed bool

	// Context input (Tab to toggle)
	showContext   bool
	contextInput  []rune
	contextCursor int

	// Scroll support for long content
	scrollOffset int
	contentLines int // Total lines in the rendered content
	visibleLines int // Visible lines based on viewport height

	// Questionnaire state is indexed by request.Questions so answers persist
	// while moving between question tabs and the final review.
	questionnaireIndex    int
	questionnaireReview   bool
	questionnaireCursors  []int
	questionnaireAnswers  []interaction.QuestionnaireAnswer
	questionnaireAnswered []bool
	questionnaireCustom   [][]rune
	questionnaireEditing  bool
	questionnaireCursor   int
}

// NewQuestionModal creates a new question modal.
func NewQuestionModal(req interaction.QuestionRequest, idx, total int, onSubmit func(interaction.QuestionResponse), onCancel func()) *QuestionModal {
	m := &QuestionModal{
		request:    req,
		queueIndex: idx,
		queueTotal: total,
		onSubmit:   onSubmit,
		onCancel:   onCancel,
		focus:      questionFocusInput,
	}

	// Initialize type-specific state
	switch req.Type {
	case interaction.QuestionTypeText, interaction.QuestionTypeNumber:
		if req.Default != "" {
			m.textInput = []rune(req.Default)
			m.textCursor = len(m.textInput)
		}
	case interaction.QuestionTypeChoice:
		m.selectedIdx = 0
		for i := 0; i < m.choiceCount(); i++ {
			value, label, _ := m.choiceAt(i)
			if value == req.Default || label == req.Default {
				m.selectedIdx = i
				break
			}
		}
	case interaction.QuestionTypeMultiChoice:
		m.selectedItems = make([]bool, m.choiceCount())
		m.multiCursor = 0
	case interaction.QuestionTypeConfirm:
		m.confirmed = req.Default != "no" && req.Default != "false" && req.Default != "n"
	case interaction.QuestionTypeQuestionnaire:
		m.questionnaireCursors = make([]int, len(req.Questions))
		m.questionnaireAnswers = make([]interaction.QuestionnaireAnswer, len(req.Questions))
		m.questionnaireAnswered = make([]bool, len(req.Questions))
		m.questionnaireCustom = make([][]rune, len(req.Questions))
	}

	return m
}

// SetQueueInfo updates queue position info.
func (m *QuestionModal) SetQueueInfo(idx, total int) {
	m.queueIndex = idx
	m.queueTotal = total
}

// IsChoiceMode reports whether this modal is a single-choice prompt. Used by
// the plan-approval key router so it can free the arrow/j/k keys for scrolling
// the plan viewport and rely on letter/number shortcuts for selection instead.
func (m *QuestionModal) IsChoiceMode() bool {
	return m.request.Type == interaction.QuestionTypeChoice
}

// SelectChoiceByPrefix selects (and submits) the choice whose label begins with
// the given case-insensitive prefix, e.g. "y" matches "y: Approve". Returns true
// if a matching choice was found and submitted. This lets the plan-approval bar
// accept the y/c/n hotkeys directly while leaving the arrow keys free for
// scrolling the plan above it.
func (m *QuestionModal) SelectChoiceByPrefix(prefix string) bool {
	if m.request.Type != interaction.QuestionTypeChoice {
		return false
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return false
	}
	for i, c := range m.request.Choices {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(c)), prefix) {
			m.selectedIdx = i
			m.submit()
			return true
		}
	}
	return false
}

// Update handles key input.
func (m *QuestionModal) Update(key string) {
	// Handle scroll keys (works for all question types when content is scrollable)
	if m.contentLines > m.visibleLines && m.visibleLines > 0 {
		switch key {
		case "pgup", "ctrl+b":
			if m.scrollOffset > 0 {
				m.scrollOffset -= m.visibleLines
				if m.scrollOffset < 0 {
					m.scrollOffset = 0
				}
			}
			return
		case "pgdown", "ctrl+f":
			maxScroll := m.contentLines - m.visibleLines
			if m.scrollOffset < maxScroll {
				m.scrollOffset += m.visibleLines
				if m.scrollOffset > maxScroll {
					m.scrollOffset = maxScroll
				}
			}
			return
		case "home":
			m.scrollOffset = 0
			return
		case "end":
			maxScroll := m.contentLines - m.visibleLines
			if maxScroll > 0 {
				m.scrollOffset = maxScroll
			}
			return
		}
	}

	if m.focus == questionFocusContext {
		m.updateContextInput(key)
		return
	}

	switch m.request.Type {
	case interaction.QuestionTypeText:
		m.updateTextInput(key)
	case interaction.QuestionTypeNumber:
		m.updateNumberInput(key)
	case interaction.QuestionTypeChoice:
		m.updateChoice(key)
	case interaction.QuestionTypeMultiChoice:
		m.updateMultiChoice(key)
	case interaction.QuestionTypeConfirm:
		m.updateConfirm(key)
	case interaction.QuestionTypeQuestionnaire:
		m.updateQuestionnaire(key)
	default:
		m.updateTextInput(key)
	}
}

func (m *QuestionModal) updateQuestionnaire(key string) {
	if m.questionnaireEditing {
		m.updateQuestionnaireOther(key)
		return
	}

	n := len(m.request.Questions)
	if n == 0 {
		if key == "esc" {
			m.cancel()
		}
		return
	}

	if m.questionnaireReview {
		switch key {
		case "esc":
			m.cancel()
		case "left", "shift+tab", "backspace":
			m.questionnaireReview = false
			m.questionnaireIndex = n - 1
		case "enter":
			if m.questionnaireComplete() {
				m.submit()
			}
		default:
			if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
				idx := int(key[0] - '1')
				if idx < n {
					m.questionnaireReview = false
					m.questionnaireIndex = idx
				}
			}
		}
		return
	}

	q := m.request.Questions[m.questionnaireIndex]
	options := m.questionnaireOptions(q)
	optionCount := len(options)
	if q.AllowOther {
		optionCount++
	}

	switch key {
	case "esc":
		m.cancel()
	case "left", "shift+tab":
		if m.questionnaireIndex > 0 {
			m.questionnaireIndex--
		}
	case "right", "tab":
		if m.questionnaireIndex+1 < n {
			m.questionnaireIndex++
		} else {
			m.questionnaireReview = true
		}
	case "up", "k":
		if optionCount > 0 {
			m.questionnaireCursors[m.questionnaireIndex] =
				(m.questionnaireCursors[m.questionnaireIndex] + optionCount - 1) % optionCount
		}
	case "down", "j":
		if optionCount > 0 {
			m.questionnaireCursors[m.questionnaireIndex] =
				(m.questionnaireCursors[m.questionnaireIndex] + 1) % optionCount
		}
	case "enter", "space", " ":
		m.chooseQuestionnaireOption()
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			idx := int(key[0] - '1')
			if idx < optionCount {
				m.questionnaireCursors[m.questionnaireIndex] = idx
				m.chooseQuestionnaireOption()
			}
		}
	}
}

func (m *QuestionModal) questionnaireOptions(q interaction.QuestionnaireQuestion) []interaction.QuestionOption {
	if len(q.Options) > 0 {
		return q.Options
	}
	return m.request.Options
}

func (m *QuestionModal) chooseQuestionnaireOption() {
	i := m.questionnaireIndex
	if i < 0 || i >= len(m.request.Questions) {
		return
	}
	q := m.request.Questions[i]
	options := m.questionnaireOptions(q)
	cursor := m.questionnaireCursors[i]
	if cursor < 0 {
		return
	}
	if cursor >= len(options) {
		if q.AllowOther && cursor == len(options) {
			m.questionnaireEditing = true
			m.questionnaireCursor = len(m.questionnaireCustom[i])
		}
		return
	}

	option := options[cursor]
	label := option.Label
	if label == "" {
		label = option.Value
	}
	index := cursor
	m.questionnaireAnswers[i] = interaction.QuestionnaireAnswer{
		ID:    q.ID,
		Value: option.Value,
		Label: label,
		Index: &index,
	}
	m.questionnaireAnswered[i] = true
	m.advanceQuestionnaireAfterAnswer()
}

func (m *QuestionModal) advanceQuestionnaireAfterAnswer() {
	if len(m.request.Questions) == 1 {
		m.submit()
		return
	}
	if m.questionnaireIndex+1 < len(m.request.Questions) {
		m.questionnaireIndex++
		return
	}
	m.questionnaireReview = true
}

func (m *QuestionModal) updateQuestionnaireOther(key string) {
	i := m.questionnaireIndex
	if i < 0 || i >= len(m.questionnaireCustom) {
		m.questionnaireEditing = false
		return
	}
	input := m.questionnaireCustom[i]
	switch key {
	case "esc":
		m.questionnaireEditing = false
	case "enter":
		value := strings.TrimSpace(string(input))
		if value == "" {
			return
		}
		q := m.request.Questions[i]
		m.questionnaireAnswers[i] = interaction.QuestionnaireAnswer{
			ID:        q.ID,
			Value:     value,
			Label:     value,
			WasCustom: true,
		}
		m.questionnaireAnswered[i] = true
		m.questionnaireEditing = false
		m.advanceQuestionnaireAfterAnswer()
	case "backspace":
		if m.questionnaireCursor > 0 {
			input = append(input[:m.questionnaireCursor-1], input[m.questionnaireCursor:]...)
			m.questionnaireCursor--
		}
	case "delete":
		if m.questionnaireCursor < len(input) {
			input = append(input[:m.questionnaireCursor], input[m.questionnaireCursor+1:]...)
		}
	case "left":
		if m.questionnaireCursor > 0 {
			m.questionnaireCursor--
		}
	case "right":
		if m.questionnaireCursor < len(input) {
			m.questionnaireCursor++
		}
	case "home", "ctrl+a":
		m.questionnaireCursor = 0
	case "end", "ctrl+e":
		m.questionnaireCursor = len(input)
	case "ctrl+u":
		input = nil
		m.questionnaireCursor = 0
	default:
		r := runeFromKey(key)
		if r >= 32 {
			input = append(input[:m.questionnaireCursor], append([]rune{r}, input[m.questionnaireCursor:]...)...)
			m.questionnaireCursor++
		}
	}
	m.questionnaireCustom[i] = input
}

func (m *QuestionModal) questionnaireComplete() bool {
	if len(m.questionnaireAnswered) == 0 {
		return false
	}
	for _, answered := range m.questionnaireAnswered {
		if !answered {
			return false
		}
	}
	return true
}

func (m *QuestionModal) updateTextInput(key string) {
	switch key {
	case "enter":
		m.submit()
	case "esc":
		m.cancel()
	case "tab":
		m.focus = questionFocusContext
		m.showContext = true
	case "backspace":
		if m.textCursor > 0 {
			m.textInput = append(m.textInput[:m.textCursor-1], m.textInput[m.textCursor:]...)
			m.textCursor--
		}
	case "delete":
		if m.textCursor < len(m.textInput) {
			m.textInput = append(m.textInput[:m.textCursor], m.textInput[m.textCursor+1:]...)
		}
	case "left":
		if m.textCursor > 0 {
			m.textCursor--
		}
	case "right":
		if m.textCursor < len(m.textInput) {
			m.textCursor++
		}
	case "home", "ctrl+a":
		m.textCursor = 0
	case "end", "ctrl+e":
		m.textCursor = len(m.textInput)
	case "ctrl+u":
		m.textInput = nil
		m.textCursor = 0
	default:
		// Handle space explicitly — Bubble Tea may send it as "space" or " "
		r := runeFromKey(key)
		if r >= 32 {
			m.textInput = append(m.textInput[:m.textCursor], append([]rune{r}, m.textInput[m.textCursor:]...)...)
			m.textCursor++
		}
	}
}

func (m *QuestionModal) updateNumberInput(key string) {
	switch key {
	case "enter":
		m.submit()
	case "esc":
		m.cancel()
	case "tab":
		m.focus = questionFocusContext
		m.showContext = true
	case "backspace":
		if m.textCursor > 0 {
			m.textInput = append(m.textInput[:m.textCursor-1], m.textInput[m.textCursor:]...)
			m.textCursor--
		}
	case "delete":
		if m.textCursor < len(m.textInput) {
			m.textInput = append(m.textInput[:m.textCursor], m.textInput[m.textCursor+1:]...)
		}
	case "left":
		if m.textCursor > 0 {
			m.textCursor--
		}
	case "right":
		if m.textCursor < len(m.textInput) {
			m.textCursor++
		}
	case "home", "ctrl+a":
		m.textCursor = 0
	case "end", "ctrl+e":
		m.textCursor = len(m.textInput)
	case "ctrl+u":
		m.textInput = nil
		m.textCursor = 0
	default:
		if utf8.RuneCountInString(key) == 1 {
			r := []rune(key)[0]
			// Only allow digits, minus, decimal point
			if unicode.IsDigit(r) || r == '-' || r == '.' {
				m.textInput = append(m.textInput[:m.textCursor], append([]rune{r}, m.textInput[m.textCursor:]...)...)
				m.textCursor++
			}
		}
	}
}

func (m *QuestionModal) updateChoice(key string) {
	n := m.choiceCount()
	if n == 0 {
		if key == "esc" {
			m.cancel()
		}
		return
	}
	if isSpaceKey(key) {
		m.submit()
		return
	}
	switch key {
	case "up", "k":
		m.selectedIdx = (m.selectedIdx + n - 1) % n
	case "down", "j":
		m.selectedIdx = (m.selectedIdx + 1) % n
	case "enter":
		m.submit()
	case "esc":
		m.cancel()
	case "tab":
		m.focus = questionFocusContext
		m.showContext = true
	default:
		// Number keys for quick selection
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			idx := int(key[0] - '1')
			if idx < n {
				m.selectedIdx = idx
				m.submit()
			}
		}
	}
}

func (m *QuestionModal) updateMultiChoice(key string) {
	n := m.choiceCount()
	if n == 0 {
		if key == "esc" {
			m.cancel()
		}
		return
	}
	if isSpaceKey(key) {
		m.selectedItems[m.multiCursor] = !m.selectedItems[m.multiCursor]
		return
	}
	switch key {
	case "up", "k":
		m.multiCursor = (m.multiCursor + n - 1) % n
	case "down", "j":
		m.multiCursor = (m.multiCursor + 1) % n
	case "enter":
		m.submit()
	case "esc":
		m.cancel()
	case "tab":
		m.focus = questionFocusContext
		m.showContext = true
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			idx := int(key[0] - '1')
			if idx < n {
				m.selectedItems[idx] = !m.selectedItems[idx]
			}
		}
	}
}

func (m *QuestionModal) updateConfirm(key string) {
	if isSpaceKey(key) {
		m.submit()
		return
	}
	switch key {
	case "left", "h":
		m.confirmed = true
	case "right", "l":
		m.confirmed = false
	case "y":
		m.confirmed = true
		m.submit()
	case "n":
		m.confirmed = false
		m.submit()
	case "enter":
		m.submit()
	case "esc":
		m.cancel()
	case "tab":
		m.focus = questionFocusContext
		m.showContext = true
	}
}

func (m *QuestionModal) updateContextInput(key string) {
	switch key {
	case "tab", "esc":
		m.focus = questionFocusInput
	case "enter":
		m.submit()
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
		r := runeFromKey(key)
		if r >= 32 {
			m.contextInput = append(m.contextInput[:m.contextCursor], append([]rune{r}, m.contextInput[m.contextCursor:]...)...)
			m.contextCursor++
		}
	}
}

// HandlePaste inserts pasted text at the current cursor position.
func (m *QuestionModal) HandlePaste(text string) {
	if text == "" {
		return
	}
	runes := []rune(text)

	if m.focus == questionFocusContext {
		// Insert into context input
		newInput := make([]rune, 0, len(m.contextInput)+len(runes))
		newInput = append(newInput, m.contextInput[:m.contextCursor]...)
		newInput = append(newInput, runes...)
		newInput = append(newInput, m.contextInput[m.contextCursor:]...)
		m.contextInput = newInput
		m.contextCursor += len(runes)
		return
	}

	switch m.request.Type {
	case interaction.QuestionTypeText:
		newInput := make([]rune, 0, len(m.textInput)+len(runes))
		newInput = append(newInput, m.textInput[:m.textCursor]...)
		newInput = append(newInput, runes...)
		newInput = append(newInput, m.textInput[m.textCursor:]...)
		m.textInput = newInput
		m.textCursor += len(runes)
	case interaction.QuestionTypeNumber:
		// Filter to digits, minus, decimal only
		var filtered []rune
		for _, r := range runes {
			if unicode.IsDigit(r) || r == '-' || r == '.' {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) > 0 {
			newInput := make([]rune, 0, len(m.textInput)+len(filtered))
			newInput = append(newInput, m.textInput[:m.textCursor]...)
			newInput = append(newInput, filtered...)
			newInput = append(newInput, m.textInput[m.textCursor:]...)
			m.textInput = newInput
			m.textCursor += len(filtered)
		}
	case interaction.QuestionTypeQuestionnaire:
		if m.questionnaireEditing && m.questionnaireIndex >= 0 &&
			m.questionnaireIndex < len(m.questionnaireCustom) {
			i := m.questionnaireIndex
			input := m.questionnaireCustom[i]
			newInput := make([]rune, 0, len(input)+len(runes))
			newInput = append(newInput, input[:m.questionnaireCursor]...)
			newInput = append(newInput, runes...)
			newInput = append(newInput, input[m.questionnaireCursor:]...)
			m.questionnaireCustom[i] = newInput
			m.questionnaireCursor += len(runes)
		}
	}
}

func (m *QuestionModal) submit() {
	if m.onSubmit == nil {
		return
	}

	resp := interaction.QuestionResponse{
		RespondedAt: time.Now(),
	}

	switch m.request.Type {
	case interaction.QuestionTypeText, interaction.QuestionTypeNumber:
		resp.Answer = string(m.textInput)
	case interaction.QuestionTypeChoice:
		if m.selectedIdx >= 0 && m.selectedIdx < m.choiceCount() {
			resp.Answer, _, _ = m.choiceAt(m.selectedIdx)
		}
	case interaction.QuestionTypeMultiChoice:
		var answers []string
		for i, selected := range m.selectedItems {
			if selected && i < m.choiceCount() {
				value, _, _ := m.choiceAt(i)
				answers = append(answers, value)
			}
		}
		resp.Answers = answers
		if len(answers) > 0 {
			resp.Answer = strings.Join(answers, ", ")
		}
	case interaction.QuestionTypeConfirm:
		resp.Confirmed = m.confirmed
		if m.confirmed {
			resp.Answer = "yes"
		} else {
			resp.Answer = "no"
		}
	case interaction.QuestionTypeQuestionnaire:
		if !m.questionnaireComplete() {
			return
		}
		resp.QuestionnaireAnswers = append([]interaction.QuestionnaireAnswer(nil), m.questionnaireAnswers...)
	}

	m.onSubmit(resp)
}

func (m *QuestionModal) cancel() {
	if m.onCancel != nil {
		m.onCancel()
	}
}

// Render renders the full question modal.
func (m *QuestionModal) Render(width, height int, theme Theme) string {
	if width <= 0 {
		return ""
	}
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
	if contentWidth < 1 {
		contentWidth = 1
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))

	title := i18n.T("chat_b.question.title")
	if m.queueTotal > 1 {
		title = i18n.T("chat_b.question.title_queued", m.queueIndex, m.queueTotal)
	}

	lines := []string{""}
	lines = append(lines, titleStyle.Render(title))
	lines = append(lines, "")

	// Show metadata
	if m.request.Metadata.Title != "" {
		lines = append(lines, valueStyle.Bold(true).Render(m.request.Metadata.Title))
		lines = append(lines, "")
	}

	// Question text
	questionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Bold(true)
	for _, line := range wrapText(m.request.Question, contentWidth) {
		lines = append(lines, questionStyle.Render(line))
	}
	lines = append(lines, "")

	// Description
	if m.request.Metadata.Description != "" {
		for _, line := range wrapText(m.request.Metadata.Description, contentWidth) {
			lines = append(lines, labelStyle.Render(line))
		}
		lines = append(lines, "")
	}

	// Priority badge
	if m.request.Metadata.Priority != "" && m.request.Metadata.Priority != "medium" {
		badgeColor := theme.TextMuted
		switch m.request.Metadata.Priority {
		case "critical":
			badgeColor = theme.Error
		case "high":
			badgeColor = theme.Warning
		case "low":
			badgeColor = theme.TextMuted
		}
		badge := lipgloss.NewStyle().
			Foreground(lipgloss.Color(badgeColor)).
			Bold(true).
			Render(fmt.Sprintf("[%s]", strings.ToUpper(m.request.Metadata.Priority)))
		lines = append(lines, badge)
		lines = append(lines, "")
	}

	// Agent info
	if m.request.Metadata.AgentID != "" {
		lines = append(lines, wrapField(i18n.T("classic_chat_3.question.agent"),
			m.request.Metadata.AgentID, contentWidth, labelStyle, valueStyle)...)
	}

	// Type-specific input area
	lines = append(lines, m.renderInput(contentWidth, theme))

	// Context input
	if m.showContext || len(m.contextInput) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderQuestionContext(contentWidth, theme))
	}

	lines = append(lines, "")
	lines = append(lines, m.renderQuestionHints(theme))

	// Calculate scroll info
	m.contentLines = len(lines)
	// Reserve space for border (2 lines top + 2 lines bottom) and padding
	borderOverhead := 6
	m.visibleLines = height - borderOverhead
	if m.visibleLines < 5 {
		m.visibleLines = 5
	}

	// Apply scroll offset
	if m.scrollOffset > 0 || m.contentLines > m.visibleLines {
		// Clamp scroll offset
		maxScroll := m.contentLines - m.visibleLines
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollOffset > maxScroll {
			m.scrollOffset = maxScroll
		}
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}

		// Slice lines based on scroll offset
		endIdx := m.scrollOffset + m.visibleLines
		if endIdx > m.contentLines {
			endIdx = m.contentLines
		}
		lines = lines[m.scrollOffset:endIdx]

		// Add scroll indicator if there's more content
		if m.scrollOffset > 0 || endIdx < m.contentLines {
			scrollInfo := i18n.T("classic_chat_3.question.scroll_info",
				m.scrollOffset+1, endIdx, m.contentLines)
			lines = append([]string{labelStyle.Render(scrollInfo)}, lines...)
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	modalStyle := lipgloss.NewStyle().
		Width(modalWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(1, 2)

	return modalStyle.Render(content)
}

// RenderBar renders a prominent question card for the chat input area.
// This replaces the chat input when active.
func (m *QuestionModal) RenderBar(width int, height int, theme Theme) string {
	if width <= 0 {
		return ""
	}

	barWidth := width - 4
	if barWidth < 40 {
		barWidth = width
	}
	contentWidth := barWidth - 6
	if contentWidth < 1 {
		contentWidth = 1
	}

	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))

	title := i18n.T("chat_b.question.title")
	if m.queueTotal > 1 {
		title = i18n.T("chat_b.question.title_queued", m.queueIndex, m.queueTotal)
	}

	lines := []string{""}
	lines = append(lines, titleStyle.Render(title))
	lines = append(lines, "")

	// Question text - prominent
	questionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Bold(true)
	for _, line := range wrapText(m.request.Question, contentWidth) {
		lines = append(lines, questionStyle.Render(line))
	}
	lines = append(lines, "")

	// Full interactive input area
	lines = append(lines, m.renderInput(contentWidth, theme))

	// Context input
	if m.showContext || len(m.contextInput) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.renderQuestionContext(contentWidth, theme))
	}

	lines = append(lines, "")
	lines = append(lines, m.renderQuestionHints(theme))

	// Calculate scroll info for bar mode
	m.contentLines = len(lines)
	// Reserve space for border (2 lines top + 2 lines bottom) and padding
	borderOverhead := 6
	m.visibleLines = height - borderOverhead
	if m.visibleLines < 5 {
		m.visibleLines = 5
	}

	// Apply scroll offset if content exceeds visible area
	if m.scrollOffset > 0 || m.contentLines > m.visibleLines {
		// Clamp scroll offset
		maxScroll := m.contentLines - m.visibleLines
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.scrollOffset > maxScroll {
			m.scrollOffset = maxScroll
		}
		if m.scrollOffset < 0 {
			m.scrollOffset = 0
		}

		// Slice lines based on scroll offset
		endIdx := m.scrollOffset + m.visibleLines
		if endIdx > m.contentLines {
			endIdx = m.contentLines
		}
		lines = lines[m.scrollOffset:endIdx]

		// Add scroll indicator if there's more content
		if m.scrollOffset > 0 || endIdx < m.contentLines {
			scrollInfo := i18n.T("classic_chat_3.question.scroll_info",
				m.scrollOffset+1, endIdx, m.contentLines)
			lines = append([]string{labelStyle.Render(scrollInfo)}, lines...)
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	cardStyle := lipgloss.NewStyle().
		Width(barWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(theme.Primary)).
		Padding(0, 2).
		MarginLeft(2)

	return cardStyle.Render(content)
}

func (m *QuestionModal) renderInput(width int, theme Theme) string {
	switch m.request.Type {
	case interaction.QuestionTypeText, interaction.QuestionTypeNumber:
		return m.renderTextInput(width, theme)
	case interaction.QuestionTypeChoice:
		return m.renderChoiceInput(width, theme)
	case interaction.QuestionTypeMultiChoice:
		return m.renderMultiChoiceInput(width, theme)
	case interaction.QuestionTypeConfirm:
		return m.renderConfirmInput(theme)
	case interaction.QuestionTypeQuestionnaire:
		return m.renderQuestionnaireInput(width, theme)
	default:
		return m.renderTextInput(width, theme)
	}
}

func (m *QuestionModal) renderQuestionnaireInput(width int, theme Theme) string {
	if width < 1 {
		width = 1
	}
	labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))

	var tabs []string
	for i, q := range m.request.Questions {
		label := q.Label
		if label == "" {
			label = fmt.Sprintf("%d", i+1)
		}
		marker := "○"
		if i < len(m.questionnaireAnswered) && m.questionnaireAnswered[i] {
			marker = "✓"
		}
		tab := fmt.Sprintf("%s %s", marker, label)
		if !m.questionnaireReview && i == m.questionnaireIndex {
			tab = "[" + tab + "]"
		}
		tabs = append(tabs, tab)
	}
	reviewTab := i18n.T("classic_chat_3.question.questionnaire_review")
	if m.questionnaireReview {
		reviewTab = "[" + reviewTab + "]"
	}
	tabs = append(tabs, reviewTab)
	tabLine := activeStyle.Render(truncateToVisualWidth(strings.Join(tabs, "  "), width))

	if m.questionnaireReview {
		lines := []string{tabLine, "", activeStyle.Render(i18n.T("classic_chat_3.question.questionnaire_review_title"))}
		for i, q := range m.request.Questions {
			label := q.Label
			if label == "" {
				label = q.Prompt
			}
			answer := i18n.T("classic_chat_3.question.questionnaire_unanswered")
			if i < len(m.questionnaireAnswered) && m.questionnaireAnswered[i] {
				answer = m.questionnaireAnswers[i].Label
			}
			for _, line := range wrapText(fmt.Sprintf("%d. %s: %s", i+1, label, answer), width) {
				lines = append(lines, valueStyle.Render(line))
			}
		}
		if !m.questionnaireComplete() {
			lines = append(lines, "", lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.Warning)).
				Render(i18n.T("classic_chat_3.question.questionnaire_incomplete")))
		}
		return strings.Join(lines, "\n")
	}

	if len(m.request.Questions) == 0 {
		return tabLine + "\n\n" + labelStyle.Render(i18n.T("classic_chat_3.question.questionnaire_empty"))
	}

	q := m.request.Questions[m.questionnaireIndex]
	lines := []string{tabLine, ""}
	for _, line := range wrapText(q.Prompt, width) {
		lines = append(lines, valueStyle.Bold(true).Render(line))
	}
	lines = append(lines, "")
	options := m.questionnaireOptions(q)
	cursor := m.questionnaireCursors[m.questionnaireIndex]
	for i, option := range options {
		label := option.Label
		if label == "" {
			label = option.Value
		}
		prefix := fmt.Sprintf("  %d. ", i+1)
		style := labelStyle
		if i == cursor {
			prefix = fmt.Sprintf("> %d. ", i+1)
			style = activeStyle
		}
		if lipgloss.Width(prefix) >= width {
			if i == cursor {
				prefix = fmt.Sprintf(">%d.", i+1)
			} else {
				prefix = fmt.Sprintf("%d.", i+1)
			}
		}
		optionWidth := width - lipgloss.Width(prefix)
		if optionWidth < 1 {
			prefix = ""
			optionWidth = 1
		}
		wrapped := wrapText(label, optionWidth)
		lines = append(lines, style.Render(prefix+wrapped[0]))
		indent := strings.Repeat(" ", lipgloss.Width(prefix))
		for _, line := range wrapped[1:] {
			lines = append(lines, style.Render(indent+line))
		}
		if option.Description != "" {
			descWidth := width - 4
			if descWidth < 1 {
				descWidth = 1
			}
			for _, line := range wrapText(option.Description, descWidth) {
				lines = append(lines, labelStyle.Render("    "+line))
			}
		}
	}
	if q.AllowOther {
		otherIndex := len(options)
		prefix := fmt.Sprintf("  %d. ", otherIndex+1)
		style := labelStyle
		if cursor == otherIndex {
			prefix = fmt.Sprintf("> %d. ", otherIndex+1)
			style = activeStyle
		}
		if lipgloss.Width(prefix) >= width {
			if cursor == otherIndex {
				prefix = fmt.Sprintf(">%d.", otherIndex+1)
			} else {
				prefix = fmt.Sprintf("%d.", otherIndex+1)
			}
		}
		otherWidth := width - lipgloss.Width(prefix)
		if otherWidth < 1 {
			prefix = ""
			otherWidth = 1
		}
		wrapped := wrapText(i18n.T("classic_chat_3.question.questionnaire_other"), otherWidth)
		lines = append(lines, style.Render(prefix+wrapped[0]))
		indent := strings.Repeat(" ", lipgloss.Width(prefix))
		for _, line := range wrapped[1:] {
			lines = append(lines, style.Render(indent+line))
		}
	}
	if m.questionnaireEditing {
		input := m.questionnaireCustom[m.questionnaireIndex]
		before := string(input[:m.questionnaireCursor])
		after := string(input[m.questionnaireCursor:])
		editorWidth := width - 4
		if editorWidth < 1 {
			editorWidth = 1
		}
		editor := truncateToVisualWidth(before+"█"+after, editorWidth)
		lines = append(lines, "", lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color(theme.Primary)).
			Padding(0, 1).
			Render(editor))
	}
	return strings.Join(lines, "\n")
}

func (m *QuestionModal) renderTextInput(width int, theme Theme) string {
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))

	inputText := string(m.textInput)
	if m.focus == questionFocusInput {
		before := string(m.textInput[:m.textCursor])
		after := string(m.textInput[m.textCursor:])
		inputText = before + "\u2588" + after
	}

	inputBox := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Padding(0, 1)

	if m.focus == questionFocusInput {
		inputBox = inputBox.BorderForeground(lipgloss.Color(theme.Primary))
	}

	content := valueStyle.Render(truncateLine(inputText, width-4))
	if len(m.textInput) == 0 && m.focus == questionFocusInput {
		placeholder := i18n.T("classic_chat_3.question.type_answer")
		if m.request.Type == interaction.QuestionTypeNumber {
			placeholder = i18n.T("classic_chat_3.question.enter_number")
		}
		content = lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Italic(true).
			Render(placeholder + "\u2588")
	}

	return inputBox.Render(content)
}

func (m *QuestionModal) renderChoiceInput(width int, theme Theme) string {
	var lines []string
	for i := 0; i < m.choiceCount(); i++ {
		_, choice, description := m.choiceAt(i)
		prefix := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		if i == m.selectedIdx {
			prefix = "> "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		}
		// Build the prefix part (e.g., "> 1. ") which stays on first line
		prefixPart := fmt.Sprintf("%s%d. ", prefix, i+1)
		prefixWidth := lipgloss.Width(prefixPart)
		// Wrap the choice text, reserving space for prefix on first line
		availWidth := width - prefixWidth
		if availWidth <= 0 {
			availWidth = 10
		}
		wrappedChoice := wrapText(choice, availWidth)
		// First line has the prefix
		lines = append(lines, style.Render(prefixPart+wrappedChoice[0]))
		// Continuation lines are indented to align with the text
		indent := strings.Repeat(" ", prefixWidth)
		for _, contLine := range wrappedChoice[1:] {
			lines = append(lines, style.Render(indent+contLine))
		}
		for _, descLine := range wrapOptionalText(description, availWidth) {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextMuted)).
				Render(indent+descLine))
		}
	}
	return strings.Join(lines, "\n")
}

func (m *QuestionModal) renderMultiChoiceInput(width int, theme Theme) string {
	var lines []string
	for i := 0; i < m.choiceCount(); i++ {
		_, choice, description := m.choiceAt(i)
		checkbox := "[ ]"
		if i < len(m.selectedItems) && m.selectedItems[i] {
			checkbox = "[x]"
		}
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
		if i == m.multiCursor {
			cursor = "> "
			style = lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
		}
		// Build the prefix part (e.g., "> [x] 1. ") which stays on first line
		prefixPart := fmt.Sprintf("%s%s %d. ", cursor, checkbox, i+1)
		prefixWidth := lipgloss.Width(prefixPart)
		// Wrap the choice text, reserving space for prefix on first line
		availWidth := width - prefixWidth
		if availWidth <= 0 {
			availWidth = 10
		}
		wrappedChoice := wrapText(choice, availWidth)
		// First line has the prefix
		lines = append(lines, style.Render(prefixPart+wrappedChoice[0]))
		// Continuation lines are indented to align with the text
		indent := strings.Repeat(" ", prefixWidth)
		for _, contLine := range wrappedChoice[1:] {
			lines = append(lines, style.Render(indent+contLine))
		}
		for _, descLine := range wrapOptionalText(description, availWidth) {
			lines = append(lines, lipgloss.NewStyle().
				Foreground(lipgloss.Color(theme.TextMuted)).
				Render(indent+descLine))
		}
	}
	return strings.Join(lines, "\n")
}

func wrapOptionalText(text string, width int) []string {
	if text == "" {
		return nil
	}
	return wrapText(text, width)
}

func (m *QuestionModal) choiceCount() int {
	if len(m.request.Options) > 0 {
		return len(m.request.Options)
	}
	return len(m.request.Choices)
}

func (m *QuestionModal) choiceAt(index int) (value, label, description string) {
	if len(m.request.Options) > 0 {
		if index < 0 || index >= len(m.request.Options) {
			return "", "", ""
		}
		option := m.request.Options[index]
		return option.Value, option.Label, option.Description
	}
	if index < 0 || index >= len(m.request.Choices) {
		return "", "", ""
	}
	choice := m.request.Choices[index]
	return choice, choice, ""
}

func (m *QuestionModal) renderConfirmInput(theme Theme) string {
	yesStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Padding(0, 1)
	noStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Padding(0, 1)

	yesLabel := i18n.T("classic_chat_3.question.yes")
	noLabel := i18n.T("classic_chat_3.question.no")

	if m.confirmed {
		yesStyle = yesStyle.
			Foreground(lipgloss.Color(theme.Success)).
			Bold(true)
		yesLabel = i18n.T("classic_chat_3.question.yes_selected")
	} else {
		noStyle = noStyle.
			Foreground(lipgloss.Color(theme.Error)).
			Bold(true)
		noLabel = i18n.T("classic_chat_3.question.no_selected")
	}

	return lipgloss.JoinHorizontal(lipgloss.Left, yesStyle.Render(yesLabel), noStyle.Render(noLabel))
}

func (m *QuestionModal) renderQuestionContext(width int, theme Theme) string {
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Primary)).
		Bold(m.focus == questionFocusContext)
	valueStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text))

	ctxText := string(m.contextInput)
	if m.focus == questionFocusContext {
		before := string(m.contextInput[:m.contextCursor])
		after := string(m.contextInput[m.contextCursor:])
		ctxText = before + "\u2588" + after
	}

	inputBox := lipgloss.NewStyle().
		Width(width).
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(theme.Border)).
		Padding(0, 1)

	if m.focus == questionFocusContext {
		inputBox = inputBox.BorderForeground(lipgloss.Color(theme.Primary))
	}

	content := valueStyle.Render(truncateLine(ctxText, width-4))
	if len(m.contextInput) == 0 && m.focus == questionFocusContext {
		content = lipgloss.NewStyle().
			Foreground(lipgloss.Color(theme.TextMuted)).
			Italic(true).
			Render(i18n.T("classic_chat_3.question.additional_context") + "\u2588")
	}

	return labelStyle.Render(i18n.T("chat_b.question.context_label")) + "\n" + inputBox.Render(content)
}

func (m *QuestionModal) renderQuestionHints(theme Theme) string {
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.TextMuted)).
		Italic(true)

	if m.focus == questionFocusContext {
		return hintStyle.Render(i18n.T("chat_b.question.hint_context"))
	}

	switch m.request.Type {
	case interaction.QuestionTypeChoice:
		if m.request.ID == "plan-approval" {
			// In the plan-approval bar the arrow/PgUp/PgDn keys scroll the plan
			// above, and y/c/n select directly.
			return hintStyle.Render(i18n.T("chat_b.question.hint_plan"))
		}
		return hintStyle.Render(i18n.T("chat_b.question.hint_single"))
	case interaction.QuestionTypeMultiChoice:
		return hintStyle.Render(i18n.T("chat_b.question.hint_multiple"))
	case interaction.QuestionTypeConfirm:
		return hintStyle.Render(i18n.T("chat_b.question.hint_confirm"))
	case interaction.QuestionTypeQuestionnaire:
		if m.questionnaireEditing {
			return hintStyle.Render(i18n.T("chat_b.question.hint_questionnaire_other"))
		}
		if m.questionnaireReview {
			if m.questionnaireComplete() {
				return hintStyle.Render(i18n.T("chat_b.question.hint_questionnaire_review"))
			}
			return hintStyle.Render(i18n.T("chat_b.question.hint_questionnaire_incomplete"))
		}
		return hintStyle.Render(i18n.T("chat_b.question.hint_questionnaire"))
	default:
		return hintStyle.Render(i18n.T("chat_b.question.hint_text"))
	}
}

// runeFromKey converts a Bubble Tea key string to a rune for text insertion.
// Bubble Tea v2 may send space as "space" (named) or " " (literal).
func runeFromKey(key string) rune {
	if isSpaceKey(key) {
		return ' '
	}
	if utf8.RuneCountInString(key) == 1 {
		return []rune(key)[0]
	}
	return 0
}

func isSpaceKey(key string) bool {
	return key == "space" || key == " "
}
