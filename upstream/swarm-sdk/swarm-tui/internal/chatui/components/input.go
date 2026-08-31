// Package components provides reusable UI components for the chat UI.
package components

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Input is a text input component with history support.
type Input struct {
	// State
	value       string
	cursorPos   int
	width       int
	placeholder string

	// History
	history      []string
	historyIndex int
	tempValue    string // Saved value when navigating history

	// Styling
	theme  theme.Theme
	styles *theme.StyleSet
}

// NewInput creates a new input component.
func NewInput(th theme.Theme, width int) *Input {
	return &Input{
		width:        width,
		historyIndex: -1,
		theme:        th,
		styles:       theme.NewStyleSet(th),
	}
}

// SetWidth updates the input width.
func (i *Input) SetWidth(width int) {
	i.width = width
}

// SetPlaceholder sets the placeholder text.
func (i *Input) SetPlaceholder(placeholder string) {
	i.placeholder = placeholder
}

// Value returns the current input value.
func (i *Input) Value() string {
	return i.value
}

// SetValue sets the input value.
func (i *Input) SetValue(value string) {
	i.value = value
	i.cursorPos = len(value)
}

// Clear clears the input.
func (i *Input) Clear() {
	i.value = ""
	i.cursorPos = 0
	i.historyIndex = -1
}

// CursorPos returns the cursor position.
func (i *Input) CursorPos() int {
	return i.cursorPos
}

// Focus prepares the input for user interaction.
func (i *Input) Focus() {
	// No-op in this implementation, but could track focus state
}

// Blur removes focus from the input.
func (i *Input) Blur() {
	// No-op in this implementation
}

// Update handles input events.
func (i *Input) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return i.handleKey(msg)
	}
	return nil
}

// handleKey processes keyboard input.
func (i *Input) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	// Navigation
	case "left":
		if i.cursorPos > 0 {
			i.cursorPos--
		}
	case "right":
		if i.cursorPos < len(i.value) {
			i.cursorPos++
		}
	case "home", "ctrl+a":
		i.cursorPos = 0
	case "end", "ctrl+e":
		i.cursorPos = len(i.value)

	// Editing
	case "backspace":
		if i.cursorPos > 0 {
			i.value = i.value[:i.cursorPos-1] + i.value[i.cursorPos:]
			i.cursorPos--
		}
	case "delete", "ctrl+d":
		if i.cursorPos < len(i.value) {
			i.value = i.value[:i.cursorPos] + i.value[i.cursorPos+1:]
		}
	case "ctrl+k":
		// Kill to end of line
		i.value = i.value[:i.cursorPos]
	case "ctrl+u":
		// Kill to beginning of line
		i.value = i.value[i.cursorPos:]
		i.cursorPos = 0
	case "ctrl+w":
		// Kill previous word
		i.deleteWord()

	// History navigation
	case "up":
		i.historyUp()
	case "down":
		i.historyDown()

	case "space":
		i.insertChar(" ")

	default:
		// Insert character
		if len(msg.String()) == 1 {
			i.insertChar(msg.String())
		}
	}

	return nil
}

// insertChar inserts a character at the cursor position.
func (i *Input) insertChar(ch string) {
	i.value = i.value[:i.cursorPos] + ch + i.value[i.cursorPos:]
	i.cursorPos++
}

// InsertString inserts a string at the cursor position.
func (i *Input) InsertString(s string) {
	i.value = i.value[:i.cursorPos] + s + i.value[i.cursorPos:]
	i.cursorPos += len(s)
}

// deleteWord deletes the previous word.
func (i *Input) deleteWord() {
	if i.cursorPos == 0 {
		return
	}

	// Find start of previous word
	pos := i.cursorPos - 1

	// Skip trailing spaces
	for pos > 0 && i.value[pos] == ' ' {
		pos--
	}

	// Skip word characters
	for pos > 0 && i.value[pos-1] != ' ' {
		pos--
	}

	i.value = i.value[:pos] + i.value[i.cursorPos:]
	i.cursorPos = pos
}

// AddToHistory adds a value to the history.
func (i *Input) AddToHistory(value string) {
	if value == "" {
		return
	}

	// Don't add duplicates
	if len(i.history) > 0 && i.history[len(i.history)-1] == value {
		return
	}

	i.history = append(i.history, value)

	// Limit history size
	maxHistory := 100
	if len(i.history) > maxHistory {
		i.history = i.history[len(i.history)-maxHistory:]
	}

	i.historyIndex = -1
}

// historyUp navigates to the previous history entry.
func (i *Input) historyUp() {
	if len(i.history) == 0 {
		return
	}

	if i.historyIndex == -1 {
		// Save current value
		i.tempValue = i.value
		i.historyIndex = len(i.history) - 1
	} else if i.historyIndex > 0 {
		i.historyIndex--
	} else {
		return
	}

	i.value = i.history[i.historyIndex]
	i.cursorPos = len(i.value)
}

// historyDown navigates to the next history entry.
func (i *Input) historyDown() {
	if i.historyIndex == -1 {
		return
	}

	if i.historyIndex < len(i.history)-1 {
		i.historyIndex++
		i.value = i.history[i.historyIndex]
	} else {
		// Return to temp value
		i.historyIndex = -1
		i.value = i.tempValue
	}

	i.cursorPos = len(i.value)
}

// View renders the input.
func (i *Input) View() string {
	// Build content with cursor
	var content string

	if i.value == "" {
		// Show placeholder
		content = i.styles.ContentMuted.Render(i.placeholderText())
	} else {
		// Show value with cursor
		before := i.value[:i.cursorPos]
		after := i.value[i.cursorPos:]

		cursor := i.styles.Header.Render("│")
		content = before + cursor + after
	}

	// Wrap in input style
	style := lipgloss.NewStyle().
		Width(i.width).
		Padding(0, 1).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(i.theme.BorderColor()))

	return style.Render(content)
}

// ViewCompact renders a compact single-line version.
func (i *Input) ViewCompact() string {
	prompt := i.styles.Header.Render("> ")

	var content string
	if i.value == "" {
		content = i.styles.ContentMuted.Render(i.placeholderText())
	} else {
		before := i.value[:i.cursorPos]
		after := i.value[i.cursorPos:]
		cursor := i.styles.Header.Render("│")
		content = before + cursor + after
	}

	// Truncate if too long
	maxWidth := i.width - 4
	if len(stripANSI(content)) > maxWidth {
		// Show end of content if cursor is near end
		visibleContent := content
		if i.cursorPos > maxWidth-10 {
			visibleContent = "..." + i.value[i.cursorPos-maxWidth+13:i.cursorPos] +
				i.styles.Header.Render("│") +
				i.value[i.cursorPos:]
		}
		content = visibleContent
	}

	return prompt + content
}

func (i *Input) placeholderText() string {
	if i.placeholder != "" {
		return i.placeholder
	}
	return i18n.T("chatui.input.placeholder")
}

// stripANSI removes ANSI codes from a string (simplified).
func stripANSI(s string) string {
	// Simple implementation
	result := strings.Builder{}
	inEscape := false

	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		result.WriteRune(r)
	}

	return result.String()
}

// History returns the command history.
func (i *Input) History() []string {
	return i.history
}

// SetHistory sets the command history.
func (i *Input) SetHistory(history []string) {
	i.history = history
	i.historyIndex = -1
}
