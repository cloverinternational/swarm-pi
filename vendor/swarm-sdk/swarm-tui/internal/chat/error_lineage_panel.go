package chat

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	sdkobs "github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerror "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

const maxErrorLineageDepth = 16

var (
	errorIDPattern = regexp.MustCompile(`\s*\(error_id=[^)]+\)`)
	tracePattern   = regexp.MustCompile(`\s*\((?:trace|trace_id|span|span_id)\s*[:=][^)]+\)`)
	codeTagPattern = regexp.MustCompile(`\s*\[([^\[\]]+)\]\s*$`)
)

type errorLineageFrame struct {
	Message       string
	ErrorID       string
	Code          string
	Operation     string
	Component     string
	TraceID       string
	SpanID        string
	ParentErrorID string
	OccurredAt    time.Time
	Attributes    map[string]any
	File          string // Source file where error was created
	Line          int    // Source line number
	Category      string // Error category: transient, permanent, steering, critical
}

type errorLineagePanel struct {
	Summary           string
	ErrorID           string
	Code              string
	Operation         string
	Component         string
	TraceID           string
	SpanID            string
	ParentErrorID     string
	OccurredAt        time.Time
	Attributes        map[string]any
	Frames            []errorLineageFrame
	OperationSequence []string
	Report            *sdkobs.LineageReport
	Expanded          bool
}

func buildErrorLineagePanel(err error, expanded bool) *errorLineagePanel {
	if err == nil {
		return nil
	}

	panel := &errorLineagePanel{
		Summary:       normalizeLineageMessage(err.Error()),
		ErrorID:       sdkerror.GetErrorID(err),
		Code:          sdkerror.GetCode(err),
		Operation:     sdkerror.GetOperation(err),
		Component:     sdkerror.GetComponent(err),
		TraceID:       sdkerror.GetTraceID(err),
		SpanID:        sdkerror.GetSpanID(err),
		ParentErrorID: sdkerror.GetParentErrorID(err),
		OccurredAt:    sdkerror.GetOccurredAt(err),
		Attributes:    copyAttributesForPanel(sdkerror.GetAttributes(err)),
		Expanded:      expanded,
	}

	panel.Frames = collectErrorLineageFrames(err)
	fillTraceSpanFromFrames(panel)
	fillAttributesFromRootFrame(panel)
	panel.OperationSequence = collectOperationSequence(panel.Frames)
	if panel.Summary == "" && len(panel.Frames) > 0 {
		panel.Summary = panel.Frames[0].Message
	}
	if panel.ErrorID == "" && len(panel.Frames) > 0 {
		panel.ErrorID = panel.Frames[0].ErrorID
	}

	return panel
}

func fillTraceSpanFromFrames(panel *errorLineagePanel) {
	if panel == nil || len(panel.Frames) == 0 {
		return
	}
	if panel.TraceID != "" && panel.SpanID != "" {
		return
	}

	// Some call-sites only attach trace/span metadata to inner SDK errors. Fall back to the first
	// non-empty trace/span seen in the causal frames so the UI doesn't show "trace none" when present.
	for _, frame := range panel.Frames {
		if panel.TraceID == "" && frame.TraceID != "" {
			panel.TraceID = frame.TraceID
		}
		if panel.SpanID == "" && frame.SpanID != "" {
			panel.SpanID = frame.SpanID
		}
		if panel.TraceID != "" && panel.SpanID != "" {
			return
		}
	}
}

func fillAttributesFromRootFrame(panel *errorLineagePanel) {
	if panel == nil || len(panel.Frames) == 0 || len(panel.Attributes) > 0 {
		return
	}

	// Prefer the root-most frame that actually has attributes so the header can show
	// useful context like status codes or provider without requiring wrappers to re-attach.
	for i := len(panel.Frames) - 1; i >= 0; i-- {
		if len(panel.Frames[i].Attributes) > 0 {
			panel.Attributes = copyAttributesForPanel(panel.Frames[i].Attributes)
			return
		}
	}
}

func collectErrorLineageFrames(err error) []errorLineageFrame {
	frames := make([]errorLineageFrame, 0, 4)
	seen := make(map[string]struct{})

	for current, depth := err, 0; current != nil && depth < maxErrorLineageDepth; depth++ {
		frame := errorLineageFrame{
			Message: normalizeLineageMessage(current.Error()),
		}

		// Use errors.As (not direct type assertion) so that fmt.Errorf-wrapped
		// SDK errors and other intermediate wrappers still resolve to *sdkerror.Error.
		var sdkErr *sdkerror.Error
		if errors.As(current, &sdkErr) && sdkErr != nil {
			frame.ErrorID = sdkErr.ErrorID
			frame.Code = sdkErr.Code
			frame.Operation = sdkErr.Operation
			frame.Component = sdkErr.Component
			frame.TraceID = sdkErr.TraceID
			frame.SpanID = sdkErr.SpanID
			frame.ParentErrorID = sdkErr.ParentErrorID
			frame.OccurredAt = sdkErr.OccurredAt
			frame.Attributes = copyAttributesForPanel(sdkErr.Attributes)
			frame.File = sdkErr.File
			frame.Line = sdkErr.Line
			frame.Category = sdkErr.Category.String()
		} else {
			// Non-SDK error (stdlib, fmt.Errorf, etc.): infer a code from context.
			frame.Code = inferErrorCode(current)
		}

		key := frame.ErrorID + "|" + frame.Code + "|" + frame.Message
		// For non-SDK errors with no ErrorID, also dedup by Code alone to avoid
		// consecutive identical frames (e.g., context.canceled appearing twice).
		codeOnlyKey := "|" + frame.Code + "|"
		_, existsByFullKey := seen[key]
		_, existsByCode := seen[codeOnlyKey]
		if !existsByFullKey && !existsByCode {
			seen[key] = struct{}{}
			if frame.ErrorID == "" {
				seen[codeOnlyKey] = struct{}{}
			}
			frames = append(frames, frame)
		}

		current = errors.Unwrap(current)
	}

	if len(frames) == 0 {
		frames = append(frames, errorLineageFrame{
			Message: normalizeLineageMessage(err.Error()),
		})
	}

	return frames
}

// inferErrorCode derives a dot-notation error code for non-SDK errors
// (stdlib errors, fmt.Errorf wrappers, etc.) so frames never show "unknown".
func inferErrorCode(err error) string {
	if err == nil {
		return ""
	}
	// Context errors
	if errors.Is(err, context.Canceled) {
		return "context.canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "context.deadline_exceeded"
	}
	// Try to extract a bracket code tag from the message
	msg := err.Error()
	if m := codeTagPattern.FindStringSubmatch(msg); len(m) > 1 {
		return m[1]
	}
	// Fallback: use the Go type name
	typeName := strings.TrimPrefix(fmt.Sprintf("%T", err), "*")
	// Shorten package paths: "fmt.wrapError" → "fmt.wrapError"
	parts := strings.Split(typeName, ".")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "." + parts[len(parts)-1]
	}
	return typeName
}

func collectOperationSequence(frames []errorLineageFrame) []string {
	sequence := make([]string, 0, len(frames))
	for _, frame := range frames {
		if frame.Operation == "" {
			continue
		}
		if len(sequence) > 0 && sequence[len(sequence)-1] == frame.Operation {
			continue
		}
		sequence = append(sequence, frame.Operation)
	}
	return sequence
}

func copyAttributesForPanel(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]any, len(attrs))
	maps.Copy(out, attrs)
	return out
}

func compactWhitespace(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func normalizeLineageMessage(input string) string {
	if input == "" {
		return ""
	}

	normalized := errorIDPattern.ReplaceAllString(input, "")
	normalized = tracePattern.ReplaceAllString(normalized, "")
	normalized = compactWhitespace(normalized)
	normalized = strings.Trim(normalized, " \t\n\r|,;")
	if normalized == "" {
		return "unknown error"
	}
	return normalized
}

func (a *App) errorLineageExpandedForConversation(convID string) bool {
	if convID == "" || len(a.errorLineageExpandedByConv) == 0 {
		return false
	}
	return a.errorLineageExpandedByConv[convID]
}

func (a *App) latestAssistantMessageWithLineage() *Message {
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == "assistant" && a.messages[i].ErrorLineage != nil {
			return &a.messages[i]
		}
	}
	return nil
}

func (a *App) toggleActiveErrorLineage() (bool, bool) {
	msg := a.latestAssistantMessageWithLineage()
	if msg == nil {
		return false, false
	}
	msg.ErrorLineage.Expanded = !msg.ErrorLineage.Expanded
	if a.currentConvID != "" {
		if a.errorLineageExpandedByConv == nil {
			a.errorLineageExpandedByConv = make(map[string]bool)
		}
		a.errorLineageExpandedByConv[a.currentConvID] = msg.ErrorLineage.Expanded
	}
	return true, msg.ErrorLineage.Expanded
}

func (a *App) copyActiveErrorLineage() bool {
	msg := a.latestAssistantMessageWithLineage()
	if msg == nil || msg.ErrorLineage == nil {
		return false
	}

	clipboardContent := formatErrorLineageForClipboard(msg.ErrorLineage, a.currentConvID)
	successCount := writeClipboard(clipboardContent)
	if successCount == 0 {
		a.addNotification("warning", i18n.T("classic_chat_2.error.copy_failed"))
		return false
	}

	a.addNotification("success", i18n.T("classic_chat_2.error.copied", successCount))
	return true
}

func formatChatErrorLine(panel *errorLineagePanel) string {
	if panel == nil {
		return i18n.T("classic_chat_2.error.unknown_line")
	}

	// Find the root cause frame (last in the chain)
	rootCause := panel.rootCauseFrame()

	surfacedText := normalizeLineageMessage(panel.Summary)
	codeTag := compactCodeTag(panel.Code)
	if rootCause != nil {
		rootMsg := normalizeLineageMessage(rootCause.Message)
		if rootMsg != "" {
			surfacedText = rootMsg
		}
		if rootCause.Code != "" {
			codeTag = compactCodeTag(rootCause.Code)
		}
	}
	if surfacedText == "" {
		surfacedText = i18n.T("classic_chat_2.error.unknown")
	}

	line := i18n.T("classic_chat_2.error.line", surfacedText)
	if codeTag != "" {
		line += " [" + codeTag + "]"
	}

	var ids []string
	if panel.ErrorID != "" {
		ids = append(ids, i18n.T("classic_chat_2.error.id_short", shortIdentifier(panel.ErrorID)))
	}
	traceID := panel.TraceID
	if traceID == "" && panel.Report != nil && panel.Report.TraceID != "" {
		traceID = panel.Report.TraceID
	}
	if traceID != "" {
		ids = append(ids, i18n.T("classic_chat_2.error.trace_short", shortIdentifier(traceID)))
	}
	if len(ids) > 0 {
		line += " (" + strings.Join(ids, ", ") + ")"
	}

	return line
}

func formatErrorLineageForClipboard(panel *errorLineagePanel, convID string) string {
	if panel == nil {
		return ""
	}

	lines := []string{
		"Swarm SDK Error Lineage",
		"=======================",
	}
	if convID != "" {
		lines = append(lines, fmt.Sprintf("Conversation: %s", convID))
	}
	if panel.ErrorID != "" {
		lines = append(lines, fmt.Sprintf("ErrorID: %s", panel.ErrorID))
	}
	if panel.Code != "" {
		lines = append(lines, fmt.Sprintf("Code: %s", panel.Code))
	}
	if panel.TraceID != "" {
		lines = append(lines, fmt.Sprintf("TraceID: %s", panel.TraceID))
	}
	if panel.Operation != "" {
		lines = append(lines, fmt.Sprintf("Operation: %s", panel.Operation))
	}
	if panel.Component != "" {
		lines = append(lines, fmt.Sprintf("Component: %s", panel.Component))
	}
	if !panel.OccurredAt.IsZero() {
		lines = append(lines, fmt.Sprintf("OccurredAt: %s", panel.OccurredAt.UTC().Format(time.RFC3339Nano)))
	}

	lines = append(lines, "")
	lines = append(lines, "Summary:")
	lines = append(lines, panel.Summary)

	if len(panel.Frames) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Causal Chain:")
		for i, frame := range panel.Frames {
			isRoot := i == len(panel.Frames)-1
			prefix := fmt.Sprintf("[%d]", i+1)
			if isRoot {
				prefix += " ROOT CAUSE"
			}
			lines = append(lines, fmt.Sprintf("%s %s", prefix, frame.Code))
			if frame.Operation != "" {
				lines = append(lines, "  operation="+frame.Operation)
			}
			if frame.Component != "" {
				lines = append(lines, "  component="+frame.Component)
			}
			if frame.ErrorID != "" {
				lines = append(lines, "  error_id="+frame.ErrorID)
			}
			if frame.SpanID != "" {
				lines = append(lines, "  span_id="+frame.SpanID)
			}
			if !frame.OccurredAt.IsZero() {
				lines = append(lines, "  at="+frame.OccurredAt.UTC().Format(time.RFC3339))
			}
			if frame.File != "" {
				srcLoc := frame.File
				if frame.Line > 0 {
					srcLoc = fmt.Sprintf("%s:%d", frame.File, frame.Line)
				}
				lines = append(lines, "  file="+srcLoc)
			}
			if len(frame.Attributes) > 0 {
				attrParts := summarizeAttributes(frame.Attributes, 5)
				if len(attrParts) > 0 {
					lines = append(lines, "  attrs="+strings.Join(attrParts, ", "))
				}
			}
			lines = append(lines, "  message="+frame.Message)
			if !isRoot {
				lines = append(lines, "  caused by:")
			}
		}
	}

	// Span Timeline
	idx := buildTimingIndex(panel.Report)
	timelineLines := buildSpanTimeline(panel, idx)
	if len(timelineLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Span Timeline:")
		for _, tl := range timelineLines {
			lines = append(lines, "  "+tl)
		}
	}

	// Remediation
	remediationLines := renderRemediationSection(panel)
	if len(remediationLines) > 0 {
		lines = append(lines, "")
		lines = append(lines, "Remediation:")
		for _, rl := range remediationLines {
			lines = append(lines, "  "+rl)
		}
	}

	return strings.Join(lines, "\n")
}

func (a *App) renderErrorLineagePanel(msg *Message, width int) []string {
	if msg == nil || msg.ErrorLineage == nil {
		return nil
	}

	panel := msg.ErrorLineage
	th := a.theme
	timing := buildTimingIndex(panel.Report)

	maxWidth := width - 6
	if maxWidth < 20 {
		maxWidth = 20
	}
	contentWidth := width - 10
	if contentWidth < 24 {
		contentWidth = 24
	}
	if contentWidth > maxWidth {
		contentWidth = maxWidth
	}
	if contentWidth > 120 {
		contentWidth = 120
	}

	borderStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Border)).
		Background(lipgloss.Color(th.BG))
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Error)).
		Background(lipgloss.Color(th.BG)).
		Bold(true)
	metaStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Background(lipgloss.Color(th.BG))
	bodyStyle := lipgloss.NewStyle()
	rootSignalStyle := bodyStyle.Foreground(lipgloss.Color(th.Error)).Bold(true)
	pathStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Primary)).
		Background(lipgloss.Color(th.BG)).
		Bold(true)
	sectionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.Secondary)).
		Background(lipgloss.Color(th.BG)).
		Bold(true)
	hintStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextDim)).
		Background(lipgloss.Color(th.BG)).
		Italic(true)

	lines := []string{reapplyBackground("", th.BG)}

	cardTop := "+" + strings.Repeat("-", contentWidth+2) + "+"
	lines = append(lines, reapplyBackground("  "+borderStyle.Render(cardTop), th.BG))

	appendCardLine := func(raw string, style lipgloss.Style) {
		if raw == "" {
			raw = "-"
		}
		wrapped := wrapText(raw, contentWidth)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, line := range wrapped {
			display := truncateLineageText(line, contentWidth)
			padding := strings.Repeat(" ", maxInt(0, contentWidth-lipgloss.Width(display)))
			cell := " " + display + padding + " "
			framed := borderStyle.Render("|") + style.Render(cell) + borderStyle.Render("|")
			lines = append(lines, reapplyBackground("  "+framed, th.BG))
		}
	}

	// appendCardFixedLine renders a pre-formatted line without re-wrapping so spacing stays intact.
	appendCardFixedLine := func(raw string, style lipgloss.Style) {
		if raw == "" {
			raw = "-"
		}
		display := raw
		if lipgloss.Width(display) > contentWidth {
			display = truncateLineageText(display, contentWidth)
		}
		padding := strings.Repeat(" ", maxInt(0, contentWidth-lipgloss.Width(display)))
		cell := " " + display + padding + " "
		framed := borderStyle.Render("|") + style.Render(cell) + borderStyle.Render("|")
		lines = append(lines, reapplyBackground("  "+framed, th.BG))
	}

	appendCardSeparator := func() {
		separator := "|" + strings.Repeat("-", contentWidth+2) + "|"
		lines = append(lines, reapplyBackground("  "+borderStyle.Render(separator), th.BG))
	}

	header := i18n.T("classic_chat_2.error.report_title")
	if panel.ErrorID != "" {
		header = fmt.Sprintf("%s  [%s]", header, shortIdentifier(panel.ErrorID))
	}
	appendCardLine(header, titleStyle)

	codeValue := panel.Code
	if codeValue == "" {
		codeValue = "unknown"
	}
	traceValue := panel.TraceID
	if traceValue == "" && panel.Report != nil && panel.Report.TraceID != "" {
		traceValue = panel.Report.TraceID
	}
	if traceValue == "" {
		traceValue = "none"
	}
	metaLine := i18n.T("classic_chat_2.error.meta", compactCode(codeValue), shortIdentifier(traceValue))
	if total := timing.traceWallDurationLabel(); total != "" {
		metaLine = i18n.T("classic_chat_2.error.meta_total", metaLine, total)
	}
	metaLine = i18n.T("classic_chat_2.error.meta_frames", metaLine, len(panel.Frames))
	if panel.Report != nil && len(panel.Report.Events) > 0 {
		metaLine = i18n.T("classic_chat_2.error.meta_events", metaLine, len(panel.Report.Events))
	}
	appendCardLine(metaLine, metaStyle)

	summary := normalizeLineageMessage(panel.Summary)
	if !panel.Expanded {
		rootCause := panel.rootCauseFrame()
		cause := ""
		if rootCause != nil {
			cause = normalizeLineageMessage(rootCause.Message)
			if cause != "" && rootCause.Code != "" {
				cause += " [" + compactCodeTag(rootCause.Code) + "]"
			}
		}
		if cause == "" {
			cause = summary
		}
		appendCardLine(cause, bodyStyle)
	}

	if !panel.Expanded && len(panel.OperationSequence) > 0 {
		compactPath := compactOperationPathForUI(panel.OperationSequence)
		if compactPath != "" {
			appendCardLine(i18n.T("classic_chat_2.error.path", compactPath), pathStyle)
		}
	}

	if !panel.Expanded && len(panel.Frames) > 1 {
		codes := make([]string, 0, len(panel.Frames))
		for _, f := range panel.Frames {
			if f.Code != "" {
				codes = append(codes, compactCodeTag(f.Code))
			}
		}
		if len(codes) > 1 {
			appendCardLine(i18n.T("classic_chat_2.error.codes", strings.Join(codes, " → ")), pathStyle)
		}
	}

	if len(panel.Attributes) > 0 {
		attrParts := summarizeAttributes(panel.Attributes, 3)
		if len(attrParts) > 0 {
			appendCardLine(i18n.T("classic_chat_2.error.attrs", strings.Join(attrParts, " | ")), metaStyle)
		}
	}

	stateLabel := i18n.T("classic_chat_2.error.collapsed")
	if panel.Expanded {
		stateLabel = i18n.T("classic_chat_2.error.expanded")
	}
	appendCardLine(i18n.T("classic_chat_2.error.hint", stateLabel), hintStyle)

	if panel.Expanded {
		appendCardSeparator()
		appendCardLine(i18n.T("classic_chat_2.error.causal_chain"), sectionStyle)
		appendCardLine(i18n.T("classic_chat_2.error.frames", len(panel.Frames)), metaStyle)
		appendCardFixedLine(" ", metaStyle)

		for i, frame := range panel.Frames {
			isRoot := i == len(panel.Frames)-1
			frameCode := frame.Code
			if frameCode == "" {
				frameCode = "unknown"
			}

			frameLabel := fmt.Sprintf("[%d] %s", i+1, frameCode)
			if isRoot {
				frameLabel += i18n.T("classic_chat_2.error.root_cause_suffix")
			}
			frameStyle := bodyStyle
			if isRoot {
				frameStyle = rootSignalStyle
			}
			appendCardLine(frameLabel, frameStyle)

			if frame.Operation != "" {
				appendCardLine(i18n.T("classic_chat_2.error.frame.operation", frame.Operation), metaStyle)
			}
			if frame.Component != "" {
				appendCardLine(i18n.T("classic_chat_2.error.frame.component", frame.Component), metaStyle)
			}
			if frame.ErrorID != "" {
				appendCardLine(i18n.T("classic_chat_2.error.frame.error_id", shortIdentifier(frame.ErrorID)), metaStyle)
			}
			if !frame.OccurredAt.IsZero() {
				appendCardLine(i18n.T("classic_chat_2.error.frame.at", frame.OccurredAt.UTC().Format("15:04:05.000 UTC")), metaStyle)
			}
			if frame.File != "" {
				srcLoc := frame.File
				if frame.Line > 0 {
					srcLoc = fmt.Sprintf("%s:%d", frame.File, frame.Line)
				}
				appendCardLine(i18n.T("classic_chat_2.error.frame.file", srcLoc), metaStyle)
			}
			if frame.SpanID != "" {
				appendCardLine(i18n.T("classic_chat_2.error.frame.span", shortIdentifier(frame.SpanID)), metaStyle)
			}
			if frame.Category != "" {
				appendCardLine(i18n.T("classic_chat_2.error.frame.category", frame.Category), metaStyle)
			}
			if len(frame.Attributes) > 0 {
				attrParts := summarizeAttributes(frame.Attributes, 3)
				if len(attrParts) > 0 {
					appendCardLine(i18n.T("classic_chat_2.error.frame.attrs", strings.Join(attrParts, " | ")), metaStyle)
				}
			}

			if isRoot {
				appendCardLine(frame.Message, rootSignalStyle)
			}

			if !isRoot {
				appendCardLine(i18n.T("classic_chat_2.error.caused_by"), pathStyle)
			}
			appendCardFixedLine(" ", metaStyle)
		}

		// SPAN TIMELINE
		timelineLines := buildSpanTimeline(panel, timing)
		if len(timelineLines) > 0 {
			appendCardSeparator()
			appendCardLine(i18n.T("classic_chat_2.error.span_timeline"), sectionStyle)
			for _, tl := range timelineLines {
				appendCardFixedLine(tl, bodyStyle)
			}
		}

		// REMEDIATION
		remediationLines := renderRemediationSection(panel)
		if len(remediationLines) > 0 {
			appendCardSeparator()
			appendCardLine(i18n.T("classic_chat_2.error.remediation"), sectionStyle)
			for _, rl := range remediationLines {
				appendCardLine(rl, hintStyle)
			}
		}
	}

	lines = append(lines, reapplyBackground("  "+borderStyle.Render(cardTop), th.BG))
	return lines
}

type timingIndex struct {
	spanDurationMs map[string]int64
	errorToSpanID  map[string]string
	traceWall      time.Duration
}

func buildTimingIndex(report *sdkobs.LineageReport) timingIndex {
	if report == nil || len(report.Events) == 0 {
		return timingIndex{}
	}

	idx := timingIndex{
		spanDurationMs: make(map[string]int64),
		errorToSpanID:  make(map[string]string),
	}

	var minStart time.Time
	var maxEnd time.Time

	for _, ev := range report.Events {
		if ev.ErrorID != "" && ev.SpanID != "" && idx.errorToSpanID[ev.ErrorID] == "" {
			idx.errorToSpanID[ev.ErrorID] = ev.SpanID
		}
		if ev.EventType == sdkobs.EventTypeSpanEnd && ev.SpanID != "" && ev.DurationMs > 0 {
			idx.spanDurationMs[ev.SpanID] = ev.DurationMs

			end := ev.Timestamp
			start := end.Add(-time.Duration(ev.DurationMs) * time.Millisecond)
			if minStart.IsZero() || start.Before(minStart) {
				minStart = start
			}
			if maxEnd.IsZero() || end.After(maxEnd) {
				maxEnd = end
			}
		}
	}

	if !minStart.IsZero() && !maxEnd.IsZero() && maxEnd.After(minStart) {
		idx.traceWall = maxEnd.Sub(minStart)
	}

	return idx
}

func (t timingIndex) traceWallDurationLabel() string {
	if t.traceWall <= 0 {
		return ""
	}
	return t.traceWall.String()
}

func compactOperationName(op string) string {
	if op == "" {
		return op
	}
	parts := strings.Split(op, ".")
	if len(parts) <= 3 {
		return op
	}
	return strings.Join(parts[len(parts)-3:], ".")
}

func compactOperationPathForUI(ops []string) string {
	filtered := filterOperationSequenceForUI(ops)
	if len(filtered) == 0 {
		return ""
	}

	compact := make([]string, 0, len(filtered))
	for _, op := range filtered {
		compact = append(compact, compactOperationName(op))
	}

	if len(compact) <= 5 {
		return strings.Join(compact, " -> ")
	}

	return compact[0] + " -> ... -> " + compact[len(compact)-1]
}

func filterOperationSequenceForUI(ops []string) []string {
	if len(ops) == 0 {
		return nil
	}

	out := make([]string, 0, len(ops))
	for _, op := range ops {
		op = strings.TrimSpace(op)
		if op == "" {
			continue
		}
		if isNoisyOperationForPath(op) {
			continue
		}
		out = append(out, op)
	}
	return out
}

func isNoisyOperationForPath(op string) bool {
	switch {
	case strings.HasPrefix(op, "manager."):
		return true
	case strings.HasPrefix(op, "conversation."):
		return true
	case strings.HasPrefix(op, "storage."):
		return true
	case strings.HasPrefix(op, "hooks."):
		return true
	case strings.HasPrefix(op, "skills."):
		return true
	case strings.HasPrefix(op, "plugins."):
		return true
	default:
		return false
	}
}

func compactCode(code string) string {
	if code == "" {
		return ""
	}
	if len(code) <= 44 {
		return code
	}
	return compactOperationName(code)
}

// isGenericCodePart reports whether a code part suffix is generic (e.g. "failed", "error").
func isGenericCodePart(part string) bool {
	switch strings.ToLower(strings.TrimSpace(part)) {
	case "failed", "error", "unknown_error", "unknown":
		return true
	default:
		return false
	}
}

func compactCodeTag(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}

	parts := strings.Split(code, ".")
	if len(parts) == 1 {
		if len([]rune(code)) > 28 {
			return truncateLineageText(code, 28)
		}
		return code
	}

	// Prefer the most specific suffix, but include more context if the suffix is generic.
	n := 1
	last := parts[len(parts)-1]
	if isGenericCodePart(last) {
		n = 2
		if len(parts) >= 3 && isGenericCodePart(parts[len(parts)-2]) {
			n = 3
		}
	}
	if n > len(parts) {
		n = len(parts)
	}

	compact := strings.Join(parts[len(parts)-n:], ".")
	compact = strings.TrimSpace(compact)
	if compact == "" {
		return ""
	}
	if len([]rune(compact)) > 28 {
		return truncateLineageText(compact, 28)
	}
	return compact
}

func shortIdentifier(value string) string {
	if value == "" {
		return value
	}
	runes := []rune(value)
	if len(runes) <= 16 {
		return value
	}
	return string(runes[:6]) + "..." + string(runes[len(runes)-6:])
}

func truncateLineageText(text string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxWidth {
		return text
	}
	if maxWidth <= 3 {
		return string(runes[:maxWidth])
	}
	return string(runes[:maxWidth-3]) + "..."
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func summarizeAttributes(attrs map[string]any, limit int) []string {
	if len(attrs) == 0 || limit <= 0 {
		return nil
	}

	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		if isSensitiveAttrKey(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}

	summary := make([]string, 0, len(keys))
	for _, key := range keys {
		value := fmt.Sprintf("%v", attrs[key])
		if len(value) > 40 {
			value = value[:37] + "..."
		}
		summary = append(summary, fmt.Sprintf("%s=%s", key, value))
	}
	return summary
}

func isSensitiveAttrKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	switch {
	case strings.Contains(k, "api_key"):
		return true
	case strings.Contains(k, "apikey"):
		return true
	case strings.Contains(k, "authorization"):
		return true
	case strings.Contains(k, "token"):
		return true
	case strings.Contains(k, "password"):
		return true
	case strings.Contains(k, "secret"):
		return true
	default:
		return false
	}
}

// buildSpanTimeline returns lines representing a visual timeline of per-frame
// span durations. Each line shows a proportional ASCII bar like:
//
//	[1] agent.execute      ████████░░  130ms
//	[2] provider.http      ██░░░░░░░░   22ms
const spanBarMaxWidth = 20

func buildSpanTimeline(panel *errorLineagePanel, idx timingIndex) []string {
	if panel == nil || len(panel.Frames) == 0 {
		return nil
	}

	type frameDur struct {
		idx  int
		code string
		ms   int64
	}

	durations := make([]frameDur, 0, len(panel.Frames))
	var maxMs int64

	for i, f := range panel.Frames {
		spanID := f.SpanID
		if spanID == "" && f.ErrorID != "" && idx.errorToSpanID != nil {
			spanID = idx.errorToSpanID[f.ErrorID]
		}
		if spanID == "" {
			continue
		}
		ms, ok := idx.spanDurationMs[spanID]
		if !ok || ms <= 0 {
			continue
		}
		code := compactCodeTag(f.Code)
		if code == "" {
			code = "unknown"
		}
		durations = append(durations, frameDur{idx: i, code: code, ms: ms})
		if ms > maxMs {
			maxMs = ms
		}
	}

	if len(durations) == 0 || maxMs <= 0 {
		return nil
	}

	lines := make([]string, 0, len(durations))
	for _, d := range durations {
		filled := int(float64(d.ms) / float64(maxMs) * float64(spanBarMaxWidth))
		if filled < 1 {
			filled = 1
		}
		if filled > spanBarMaxWidth {
			filled = spanBarMaxWidth
		}
		unfilled := spanBarMaxWidth - filled
		bar := strings.Repeat("█", filled) + strings.Repeat("░", unfilled)
		label := fmt.Sprintf("[%d] %s", d.idx+1, d.code)
		// Pad label to 22 chars for alignment
		if len(label) < 22 {
			label += strings.Repeat(" ", 22-len(label))
		}
		lines = append(lines, fmt.Sprintf("%s %s  %dms", label, bar, d.ms))
	}

	return lines
}

// remediationHints maps error code substrings to user-actionable advice.
var remediationHints = map[string]string{
	"rate_limit":                "classic_chat_2.error.remediation.rate_limit",
	"payment_required":          "classic_chat_2.error.remediation.payment_required",
	"unauthorized":              "classic_chat_2.error.remediation.unauthorized",
	"forbidden":                 "classic_chat_2.error.remediation.forbidden",
	"not_found":                 "classic_chat_2.error.remediation.not_found",
	"timeout":                   "classic_chat_2.error.remediation.timeout",
	"connection_refused":        "classic_chat_2.error.remediation.connection_refused",
	"tls":                       "classic_chat_2.error.remediation.tls",
	"dns":                       "classic_chat_2.error.remediation.dns",
	"stream.interrupted":        "classic_chat_2.error.remediation.stream_interrupted",
	"context.canceled":          "classic_chat_2.error.remediation.context_canceled",
	"context.deadline_exceeded": "classic_chat_2.error.remediation.context_deadline",
	"permission_denied":         "classic_chat_2.error.remediation.permission_denied",
	"quota_exceeded":            "classic_chat_2.error.remediation.quota_exceeded",
	"model_not_found":           "classic_chat_2.error.remediation.model_not_found",
	"invalid_request":           "classic_chat_2.error.remediation.invalid_request",
	"internal_error":            "classic_chat_2.error.remediation.internal_error",
}

// remediationHintForCode returns the first matching remediation hint for the
// given error code by checking each key as a substring.
func remediationHintForCode(code string) string {
	if code == "" {
		return ""
	}
	for pattern, hint := range remediationHints {
		if strings.Contains(code, pattern) {
			return i18n.T(hint)
		}
	}
	return ""
}

// renderRemediationSection returns card lines for any applicable remediation
// hints found in the panel's frame chain. Deduplicates by hint text.
func renderRemediationSection(panel *errorLineagePanel) []string {
	if panel == nil || len(panel.Frames) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	type hint struct {
		code string
		text string
	}
	var hints []hint

	for _, f := range panel.Frames {
		text := remediationHintForCode(f.Code)
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		hints = append(hints, hint{code: compactCodeTag(f.Code), text: text})
	}

	if len(hints) == 0 {
		return nil
	}

	lines := make([]string, 0, len(hints)*2)
	for _, h := range hints {
		lines = append(lines, fmt.Sprintf("%s: %s", h.code, h.text))
	}
	return lines
}
