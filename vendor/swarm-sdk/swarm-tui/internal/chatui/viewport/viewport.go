// Package viewport provides viewport management for the chat UI.
//
// The viewport handles scrolling, text selection, and efficient
// rendering of visible content within bounded dimensions.
package viewport

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
)

// Viewport manages scrolling and selection for chat content.
type Viewport struct {
	state *types.ViewportState
	cache *RenderCache

	// Content
	lines       []string // All content lines
	lineOffsets []int    // Byte offsets for efficient line access
}

// New creates a viewport with the given dimensions.
func New(width, height int) *Viewport {
	return &Viewport{
		state: &types.ViewportState{
			Width:      width,
			Height:     height,
			AutoScroll: true,
		},
		cache:       NewRenderCache(),
		lines:       []string{},
		lineOffsets: []int{},
	}
}

// SetContent updates the viewport content.
func (v *Viewport) SetContent(content string) {
	v.lines = strings.Split(content, "\n")
	v.buildLineOffsets()
	v.cache.Invalidate()

	// Auto-scroll to bottom if enabled and user hasn't scrolled away
	if v.state.AutoScroll && !v.state.UserScrolledAway {
		v.ScrollToBottom()
	}
}

// SetLines updates the viewport with pre-split lines.
func (v *Viewport) SetLines(lines []string) {
	v.lines = lines
	v.buildLineOffsets()
	v.cache.Invalidate()

	if v.state.AutoScroll && !v.state.UserScrolledAway {
		v.ScrollToBottom()
	}
}

// buildLineOffsets precomputes byte offsets for O(1) line access.
func (v *Viewport) buildLineOffsets() {
	v.lineOffsets = make([]int, len(v.lines)+1)
	offset := 0
	for i, line := range v.lines {
		v.lineOffsets[i] = offset
		offset += len(line) + 1 // +1 for newline
	}
	v.lineOffsets[len(v.lines)] = offset
}

// Resize updates the viewport dimensions.
func (v *Viewport) Resize(width, height int) {
	v.state.Width = width
	v.state.Height = height
	v.cache.Invalidate()

	// Ensure scroll position is still valid
	v.clampScroll()
}

// Width returns the viewport width.
func (v *Viewport) Width() int {
	return v.state.Width
}

// Height returns the viewport height.
func (v *Viewport) Height() int {
	return v.state.Height
}

// TotalLines returns the total number of content lines.
func (v *Viewport) TotalLines() int {
	return len(v.lines)
}

// YOffset returns the current vertical scroll position.
func (v *Viewport) YOffset() int {
	return v.state.YOffset
}

// AtTop returns true if scrolled to the top.
func (v *Viewport) AtTop() bool {
	return v.state.YOffset == 0
}

// AtBottom returns true if scrolled to the bottom.
func (v *Viewport) AtBottom() bool {
	return v.state.YOffset >= v.maxYOffset()
}

// maxYOffset returns the maximum valid scroll position.
func (v *Viewport) maxYOffset() int {
	max := len(v.lines) - v.state.Height
	if max < 0 {
		return 0
	}
	return max
}

// clampScroll ensures scroll position is within valid bounds.
func (v *Viewport) clampScroll() {
	if v.state.YOffset < 0 {
		v.state.YOffset = 0
	}
	max := v.maxYOffset()
	if v.state.YOffset > max {
		v.state.YOffset = max
	}
}

// ScrollTo scrolls to a specific line offset.
func (v *Viewport) ScrollTo(line int) {
	v.state.YOffset = line
	v.clampScroll()
	v.state.UserScrolledAway = !v.AtBottom()
	v.cache.Invalidate()
}

// ScrollUp scrolls up by n lines.
func (v *Viewport) ScrollUp(n int) {
	v.state.YOffset -= n
	v.clampScroll()
	v.state.UserScrolledAway = true
	v.cache.Invalidate()
}

// ScrollDown scrolls down by n lines.
func (v *Viewport) ScrollDown(n int) {
	v.state.YOffset += n
	v.clampScroll()
	v.state.UserScrolledAway = !v.AtBottom()
	v.cache.Invalidate()
}

// PageUp scrolls up by one page.
func (v *Viewport) PageUp() {
	v.ScrollUp(v.state.Height)
}

// PageDown scrolls down by one page.
func (v *Viewport) PageDown() {
	v.ScrollDown(v.state.Height)
}

// HalfPageUp scrolls up by half a page.
func (v *Viewport) HalfPageUp() {
	v.ScrollUp(v.state.Height / 2)
}

// HalfPageDown scrolls down by half a page.
func (v *Viewport) HalfPageDown() {
	v.ScrollDown(v.state.Height / 2)
}

// ScrollToTop scrolls to the top of the content.
func (v *Viewport) ScrollToTop() {
	v.state.YOffset = 0
	v.state.UserScrolledAway = len(v.lines) > v.state.Height
	v.cache.Invalidate()
}

// ScrollToBottom scrolls to the bottom of the content.
func (v *Viewport) ScrollToBottom() {
	v.state.YOffset = v.maxYOffset()
	v.state.UserScrolledAway = false
	v.cache.Invalidate()
}

// Update handles viewport-specific messages.
func (v *Viewport) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		return v.handleMouseWheel(msg)
	case tea.MouseClickMsg:
		return v.handleMouseClick(msg)
	case tea.MouseReleaseMsg:
		return v.handleMouseRelease(msg)
	case tea.MouseMotionMsg:
		return v.handleMouseMotion(msg)
	case tea.KeyMsg:
		return v.handleKey(msg)
	case types.ScrollToBottomMsg:
		v.ScrollToBottom()
	}
	return nil
}

// handleKey processes keyboard input for scrolling.
func (v *Viewport) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		v.ScrollUp(1)
	case "down", "j":
		v.ScrollDown(1)
	case "pgup", "ctrl+b":
		v.PageUp()
	case "pgdown", "ctrl+f":
		v.PageDown()
	case "ctrl+u":
		v.HalfPageUp()
	case "ctrl+d":
		v.HalfPageDown()
	case "home", "g":
		v.ScrollToTop()
	case "end", "G":
		v.ScrollToBottom()
	}
	return nil
}

// handleMouseWheel processes mouse wheel events.
func (v *Viewport) handleMouseWheel(msg tea.MouseWheelMsg) tea.Cmd {
	direction := msg.String()
	if direction == "wheelup" {
		v.ScrollUp(3)
	} else if direction == "wheeldown" {
		v.ScrollDown(3)
	}
	return nil
}

// handleMouseClick processes mouse click events.
func (v *Viewport) handleMouseClick(msg tea.MouseClickMsg) tea.Cmd {
	mouse := tea.Mouse(msg)
	v.startSelection(mouse.X, mouse.Y)
	return nil
}

// handleMouseRelease processes mouse release events.
func (v *Viewport) handleMouseRelease(msg tea.MouseReleaseMsg) tea.Cmd {
	mouse := tea.Mouse(msg)
	v.endSelection(mouse.X, mouse.Y)
	return nil
}

// handleMouseMotion processes mouse motion events.
func (v *Viewport) handleMouseMotion(msg tea.MouseMotionMsg) tea.Cmd {
	if v.state.Selecting {
		mouse := tea.Mouse(msg)
		v.updateSelection(mouse.X, mouse.Y)
	}
	return nil
}

// View renders the visible portion of the content.
func (v *Viewport) View() string {
	// Check cache
	if v.cache.IsValid(v.lines, v.state.YOffset, v.state.Selection) {
		return v.cache.Get()
	}

	// Render visible lines
	visibleLines := v.getVisibleLines()

	var result string
	if v.state.Selection.Active {
		result = v.applySelection(visibleLines)
	} else {
		result = strings.Join(visibleLines, "\n")
	}

	// Update cache
	v.cache.Set(result, v.lines, v.state.YOffset, v.state.Selection)

	return result
}

// getVisibleLines returns the lines visible in the viewport.
func (v *Viewport) getVisibleLines() []string {
	if len(v.lines) == 0 {
		return []string{}
	}

	start := v.state.YOffset
	end := start + v.state.Height

	if start >= len(v.lines) {
		return []string{}
	}
	if end > len(v.lines) {
		end = len(v.lines)
	}

	return v.lines[start:end]
}

// Selection methods

// startSelection begins a new text selection.
func (v *Viewport) startSelection(x, y int) {
	line := v.state.YOffset + y
	v.state.Selection = types.Selection{
		StartLine: line,
		StartCol:  x,
		EndLine:   line,
		EndCol:    x,
		Active:    true,
	}
	v.state.Selecting = true
	v.cache.Invalidate()
}

// updateSelection updates the selection end point.
func (v *Viewport) updateSelection(x, y int) {
	if !v.state.Selecting {
		return
	}
	v.state.Selection.EndLine = v.state.YOffset + y
	v.state.Selection.EndCol = x
	v.cache.Invalidate()
}

// endSelection finalizes the selection.
func (v *Viewport) endSelection(x, y int) {
	v.updateSelection(x, y)
	v.state.Selecting = false
}

// ClearSelection removes the current selection.
func (v *Viewport) ClearSelection() {
	v.state.Selection = types.Selection{}
	v.state.Selecting = false
	v.cache.Invalidate()
}

// SelectAll selects all content.
func (v *Viewport) SelectAll() {
	if len(v.lines) == 0 {
		return
	}
	lastLine := len(v.lines) - 1
	lastCol := len(v.lines[lastLine])
	v.state.Selection = types.Selection{
		StartLine: 0,
		StartCol:  0,
		EndLine:   lastLine,
		EndCol:    lastCol,
		Active:    true,
	}
	v.state.Selecting = false
	v.cache.Invalidate()
}

// GetSelectedText returns the currently selected text.
func (v *Viewport) GetSelectedText() string {
	if !v.state.Selection.Active || v.state.Selection.IsEmpty() {
		return ""
	}

	sel := v.state.Selection.Normalize()

	if sel.StartLine == sel.EndLine {
		// Single line selection
		if sel.StartLine >= len(v.lines) {
			return ""
		}
		line := v.lines[sel.StartLine]
		start := min(sel.StartCol, len(line))
		end := min(sel.EndCol, len(line))
		return line[start:end]
	}

	// Multi-line selection
	var result strings.Builder

	for i := sel.StartLine; i <= sel.EndLine && i < len(v.lines); i++ {
		line := v.lines[i]
		if i == sel.StartLine {
			start := min(sel.StartCol, len(line))
			result.WriteString(line[start:])
		} else if i == sel.EndLine {
			end := min(sel.EndCol, len(line))
			result.WriteString(line[:end])
		} else {
			result.WriteString(line)
		}
		if i < sel.EndLine {
			result.WriteByte('\n')
		}
	}

	return result.String()
}

// applySelection renders lines with selection highlighting.
func (v *Viewport) applySelection(lines []string) string {
	sel := v.state.Selection.Normalize()
	var result strings.Builder

	for i, line := range lines {
		absoluteLine := v.state.YOffset + i

		if absoluteLine < sel.StartLine || absoluteLine > sel.EndLine {
			// Line not in selection
			result.WriteString(line)
		} else if absoluteLine == sel.StartLine && absoluteLine == sel.EndLine {
			// Selection within single line
			start := min(sel.StartCol, len(line))
			end := min(sel.EndCol, len(line))
			result.WriteString(line[:start])
			result.WriteString("\x1b[7m") // Reverse video for selection
			result.WriteString(line[start:end])
			result.WriteString("\x1b[0m") // Reset
			result.WriteString(line[end:])
		} else if absoluteLine == sel.StartLine {
			// Start of multi-line selection
			start := min(sel.StartCol, len(line))
			result.WriteString(line[:start])
			result.WriteString("\x1b[7m")
			result.WriteString(line[start:])
			result.WriteString("\x1b[0m")
		} else if absoluteLine == sel.EndLine {
			// End of multi-line selection
			end := min(sel.EndCol, len(line))
			result.WriteString("\x1b[7m")
			result.WriteString(line[:end])
			result.WriteString("\x1b[0m")
			result.WriteString(line[end:])
		} else {
			// Middle of multi-line selection
			result.WriteString("\x1b[7m")
			result.WriteString(line)
			result.WriteString("\x1b[0m")
		}

		if i < len(lines)-1 {
			result.WriteByte('\n')
		}
	}

	return result.String()
}

// State returns the current viewport state (read-only copy).
func (v *Viewport) State() types.ViewportState {
	return *v.state
}
