package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// renderContextView renders a timeline of paired provider-call snapshots. The
// component report describes the final canonical request after runtime context
// injection; actual usage comes from that exact Chat/Stream call. Provider
// translation and HTTP framing can differ, so the component totals stay
// explicitly labeled as estimates rather than exact wire measurements.
func (d *DebugScreen) renderContextView(layout debugLayout, height int) string {
	th := d.theme
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

	call, idx, ok := d.contextCallIndexed()
	if !ok {
		return muted.Padding(2, 2).Render(
			i18n.T("classic_chat_2.debug.context.no_calls"))
	}
	repJSON := call.Report

	width := layout.columnWidth
	if width < 24 {
		width = 24
	}

	var lines []string
	lines = append(lines, lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).Bold(true).
		Render(i18n.T("classic_chat_2.debug.context.title",
			repJSON.Format, idx+1, d.contextCallCount())))
	lines = append(lines, strings.Repeat("─", min(60, width)))
	lines = append(lines, i18n.T("classic_chat_2.debug.context.run",
		ctxTrunc(call.RunID, 24), call.Ordinal, call.Provider, call.Model, call.Path))
	status := call.Outcome
	if status == "" {
		status = "unknown"
	}
	if call.Error != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8")).
			Render(i18n.T("classic_chat_2.debug.context.status_error", status, ctxTrunc(call.Error, max(20, width-12)))))
	} else {
		lines = append(lines, muted.Render(i18n.T("classic_chat_2.debug.context.status", status)))
	}
	lines = append(lines, i18n.T("classic_chat_2.debug.context.estimate",
		ctxCommas(repJSON.GrandTotalTokens), ctxCommas(repJSON.GrandTotalBytes)))
	if call.Usage != nil {
		lines = append(lines, i18n.T(
			"classic_chat_2.debug.context.actual_usage",
			ctxCommas(call.Usage.InputTokens), ctxCommas(call.Usage.OutputTokens)))
		if call.Usage.CacheCreationTokens > 0 || call.Usage.CacheReadTokens > 0 {
			lines = append(lines, muted.Render(i18n.T(
				"classic_chat_2.debug.context.cache_usage",
				ctxCommas(call.Usage.UncachedInputTokens),
				ctxCommas(call.Usage.CacheCreationTokens),
				ctxCommas(call.Usage.CacheReadTokens))))
		}
	} else {
		lines = append(lines, muted.Render(i18n.T("classic_chat_2.debug.context.usage_unavailable")))
	}

	if prev, pidx, pok := d.previousContextCall(idx); pok {
		actualDelta := ""
		if call.Usage != nil && prev.Usage != nil {
			actualDelta = i18n.T("classic_chat_2.debug.context.actual_delta",
				call.Usage.InputTokens-prev.Usage.InputTokens)
		}
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.Accent)).
			Render(i18n.T("classic_chat_2.debug.context.delta",
				pidx+1, repJSON.GrandTotalTokens-prev.Report.GrandTotalTokens, actualDelta)))
	}
	lines = append(lines, muted.Render(i18n.T("classic_chat_2.debug.context.step_calls")))
	lines = append(lines, muted.Render(
		i18n.T("classic_chat_2.debug.context.attribution_note")))

	hid := repJSON.Hidden
	if hid.Count > 0 {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F38BA8")).Bold(true).
			Render(i18n.T("classic_chat_2.debug.context.hidden_summary",
				hid.Count, ctxCommas(hid.Tokens), hid.PctOfTotal)))
		lines = append(lines, muted.Render(i18n.T("classic_chat_2.debug.context.sources", strings.Join(hid.Sources, ", "))))
	} else {
		lines = append(lines, muted.Render(i18n.T("classic_chat_2.debug.context.no_hidden")))
	}
	lines = append(lines, "")

	lines = append(lines, d.renderContextBucket(repJSON.System, width)...)
	lines = append(lines, d.renderContextBucket(repJSON.Tools, width)...)
	lines = append(lines, d.renderContextBucket(repJSON.Messages, width)...)

	return d.applyScrolling(lines, layout, height)
}

// renderContextBucket renders one bucket (system / tools / messages) with its
// contributors, flagging hidden and ephemeral ones. It does NOT truncate the
// row list — every contributor (every tool schema, every message/turn) is
// shown, and the Context tab's scrolling (j/k, g/G, ^u/^d) pages through it.
// Labels are trimmed only to the pane width so each contributor stays on a
// single line (line-wrapping would corrupt the scroll offset math).
func (d *DebugScreen) renderContextBucket(b contextaudit.BucketJSON, width int) []string {
	th := d.theme
	lines := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color(th.Accent)).Bold(true).
			Render(i18n.T("classic_chat_2.debug.context.section",
				strings.ToUpper(b.Name), ctxCommas(b.Tokens), b.Pct)),
	}
	if len(b.Components) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Render(i18n.T("classic_chat_2.common.none_parenthesized")))
		lines = append(lines, "")
		return lines
	}

	// Label column adapts to the pane width so wide terminals show full names
	// while narrow ones still avoid wrapping. The suffix
	// (" NNN,NNN tok  PP.P%  <kind> [hidden]") needs ~44 cols, so reserve them —
	// keeping every contributor on exactly one line so the scroll offset math in
	// applyScrolling stays correct.
	labelW := width - 48
	if labelW < 24 {
		labelW = 24
	}
	hiddenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F38BA8"))
	ephemeralStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#F9E2AF"))
	for i := 0; i < len(b.Components); i++ {
		c := b.Components[i]
		flag := ""
		switch {
		case c.Hidden:
			flag = hiddenStyle.Render(i18n.T("classic_chat_2.debug.context.hidden_flag"))
		case c.Ephemeral:
			flag = ephemeralStyle.Render(i18n.T("classic_chat_2.debug.context.ephemeral_flag"))
		}
		lines = append(lines, i18n.T("classic_chat_2.debug.context.row",
			labelW, ctxTrunc(c.Label, labelW), ctxCommas(c.Tokens), c.Pct, c.Kind, flag))
	}
	lines = append(lines, "")
	return lines
}

func (d *DebugScreen) contextCallIndexed() (DebugContextCall, int, bool) {
	d.contextMu.RLock()
	defer d.contextMu.RUnlock()
	if d.selectedContextCall >= 0 && d.selectedContextCall < len(d.contextCalls) {
		return d.contextCalls[d.selectedContextCall], d.selectedContextCall, true
	}
	if len(d.contextCalls) == 0 {
		return DebugContextCall{}, -1, false
	}
	idx := len(d.contextCalls) - 1
	return d.contextCalls[idx], idx, true
}

func (d *DebugScreen) contextCallCount() int {
	d.contextMu.RLock()
	defer d.contextMu.RUnlock()
	return len(d.contextCalls)
}

func (d *DebugScreen) previousContextCall(idx int) (DebugContextCall, int, bool) {
	d.contextMu.RLock()
	defer d.contextMu.RUnlock()
	if idx <= 0 || idx > len(d.contextCalls)-1 {
		return DebugContextCall{}, -1, false
	}
	if d.contextCalls[idx-1].RunID != d.contextCalls[idx].RunID {
		return DebugContextCall{}, -1, false
	}
	return d.contextCalls[idx-1], idx - 1, true
}

// stepContextRequest moves through actual provider calls on the Context tab.
func (d *DebugScreen) stepContextRequest(delta int) bool {
	d.contextMu.Lock()
	defer d.contextMu.Unlock()
	if len(d.contextCalls) == 0 {
		return false
	}
	cur := d.selectedContextCall
	if cur < 0 || cur >= len(d.contextCalls) {
		cur = len(d.contextCalls) - 1
	}
	next := cur + delta
	if next < 0 || next >= len(d.contextCalls) {
		return false
	}
	d.selectedContextCall = next
	d.autoScroll = false
	d.resetScrollOffset()
	return true
}

// ctxCommas formats an int with thousands separators (12345 -> "12,345").
func ctxCommas(n int) string {
	if n < 0 {
		return "-" + ctxCommas(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
	}
	for i := pre; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// ctxTrunc trims a label to max runes with an ellipsis.
func ctxTrunc(label string, max int) string {
	if len(label) <= max {
		return label
	}
	if max <= 1 {
		return label[:max]
	}
	return label[:max-1] + "…"
}
