package chat

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

// DetailViewer is a generic full-screen "takeover" pane that replaces the chat
// area until dismissed. It is used for the Enter-to-view detail of a background
// bash command or a sub-agent, and follows the same takeover model as PlanViewer
// (viewChat early-returns its View() while it is active).
//
// Content is produced by a refresh closure so the pane can update live while the
// underlying process/agent runs. On refresh the scroll position is preserved,
// unless the user is following the tail (was at the bottom), in which case it
// snaps to the newest output.
type DetailViewer struct {
	viewport *MessageList
	title    string
	status   func() string // dynamic status shown in the header (may be nil)
	refresh  func() string // produces the body content (may be nil)
	width    int
	height   int
}

// NewDetailViewer creates a takeover viewer with the given title, a dynamic
// status function, and a body refresh function. Either function may be nil.
func NewDetailViewer(title string, status func() string, refresh func() string) *DetailViewer {
	v := &DetailViewer{
		viewport: NewMessageList(80, 24),
		title:    title,
		status:   status,
		refresh:  refresh,
	}
	if refresh != nil {
		v.viewport.SetContent(refresh())
		v.viewport.GotoBottom() // start following the newest output
	}
	return v
}

// Refresh re-runs the body closure, preserving the scroll position unless the
// user was at the bottom (tail-follow).
func (v *DetailViewer) Refresh() {
	if v == nil || v.refresh == nil {
		return
	}
	wasBottom := v.viewport.AtBottom()
	off := v.viewport.YOffset
	v.viewport.SetContent(v.refresh())
	if wasBottom {
		v.viewport.GotoBottom()
	} else {
		v.viewport.SetYOffset(off)
	}
}

// SetSize sets the viewer dimensions, reserving 2 lines for the header.
func (v *DetailViewer) SetSize(width, height int) {
	if width < 20 {
		width = 20
	}
	if height < 5 {
		height = 5
	}
	v.width = width
	v.height = height
	contentHeight := height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	v.viewport.SetSize(width, contentHeight)
}

// SetOrigin sets the viewport's screen position for mouse mapping.
func (v *DetailViewer) SetOrigin(x, y int) { v.viewport.SetOrigin(x, y) }

// Scroll passthroughs.
func (v *DetailViewer) ScrollUp(n int)   { v.viewport.ScrollUp(n) }
func (v *DetailViewer) ScrollDown(n int) { v.viewport.ScrollDown(n) }
func (v *DetailViewer) PageUp()          { v.viewport.PageUp() }
func (v *DetailViewer) PageDown()        { v.viewport.PageDown() }
func (v *DetailViewer) HalfPageUp()      { v.viewport.HalfPageUp() }
func (v *DetailViewer) HalfPageDown()    { v.viewport.HalfPageDown() }
func (v *DetailViewer) GotoTop()         { v.viewport.GotoTop() }
func (v *DetailViewer) GotoBottom()      { v.viewport.GotoBottom() }

// View renders the takeover pane: a header line + separator + scrollable body.
func (v *DetailViewer) View() string {
	return lipgloss.JoinVertical(lipgloss.Left, v.renderHeader(), v.viewport.View())
}

func (v *DetailViewer) renderHeader() string {
	titleSty := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.Accent))
	statusSty := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim))
	hintSty := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted))
	borderSty := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Border))

	left := titleSty.Render("▸ " + v.title)
	if v.status != nil {
		if s := v.status(); s != "" {
			left += statusSty.Render("  " + s)
		}
	}
	back := hintSty.Render(i18n.T("classic_chat_2.detail.back_hint"))

	// Right-align the back hint.
	pad := v.width - ansi.StringWidth(ansi.Strip(left)) - ansi.StringWidth(ansi.Strip(back))
	if pad < 1 {
		pad = 1
	}
	headerLine := left + strings.Repeat(" ", pad) + back
	sep := borderSty.Render(strings.Repeat("─", v.width))
	return lipgloss.JoinVertical(lipgloss.Left, headerLine, sep)
}

// detailBodyFromLines formats a header block + output lines (ANSI preserved)
// into a single string for the viewer body. Shared by bash + sub-agent openers.
func detailBodyFromLines(headerLines []string, outputLines []string) string {
	var b strings.Builder
	for _, h := range headerLines {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	if len(headerLines) > 0 {
		b.WriteByte('\n')
	}
	for _, l := range outputLines {
		b.WriteString(shared.SanitizeANSI(l))
		b.WriteByte('\n')
	}
	return b.String()
}

// fmtDetailHeader is a tiny helper for "key: value" header lines.
func fmtDetailHeader(key, value string) string {
	return fmt.Sprintf("%s: %s", key, value)
}
