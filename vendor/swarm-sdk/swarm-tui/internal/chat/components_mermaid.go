package chat

// components_mermaid.go — ASCII/Unicode art renderers for Mermaid diagram types.
//
// Integration point: parseMarkdownWithVerbose in components.go calls
// renderMermaidDiagram when codeLanguage == "mermaid".

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	mermaidpkg "github.com/Swarm-Code/mono/swarm-sdk/internal/markdown/mermaid"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/charmbracelet/lipgloss"
)

// ── Entry point ──────────────────────────────────────────────────────────────

func renderMermaidDiagram(lines []string, theme Theme, width int, verbose bool) []MarkdownLine {
	lines = mermaidSanitizeLines(lines)
	diagType := detectMermaidType(lines)
	switch diagType {
	case "pie":
		return renderMermaidPie(lines, theme, width)
	case "xychart-beta", "xychart":
		return renderMermaidXYChart(lines, theme, width)
	case "sequenceDiagram":
		return renderMermaidSequence(lines, theme, width)
	case "flowchart", "graph":
		return renderMermaidFlowchart(lines, theme, width)
	default:
		return renderMermaidFallback(lines, diagType, theme, width, verbose)
	}
}

func detectMermaidType(lines []string) string {
	return mermaidpkg.DetectType(lines)
}

// ── Shared utilities ─────────────────────────────────────────────────────────

func parseMermaidTitle(lines []string) string {
	return mermaidpkg.ParseTitle(lines)
}

func mermaidBoxWidth(width int) int {
	return newMermaidViewport(width).Outer
}

func mermaidPalette(theme Theme) []string {
	return []string{
		theme.Primary, theme.Accent, theme.Success,
		theme.Warning, theme.Error, theme.Info, theme.Secondary,
	}
}

// mermaidMakeBox wraps body lines in a titled border box.
// boxWidth is the total outer width including border characters.
func mermaidMakeBox(title string, body []string, theme Theme, boxWidth int) []MarkdownLine {
	borderSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Border))
	titleSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
	viewport := newMermaidViewport(boxWidth)
	boxWidth = viewport.Outer

	// On truly tiny screens the border and its padding consume a third of the
	// viewport. Use the full row for information; framed rendering resumes once
	// there is enough room for a useful content column.
	if boxWidth < 16 {
		result := make([]MarkdownLine, 0, len(body)+1)
		if title != "" {
			result = append(result, MarkdownLine{
				Content: mermaidTruncateCell(titleSt.Render(title), boxWidth),
				Type:    "mermaid",
			})
		}
		for _, line := range body {
			for _, wrapped := range mermaidWrapCell(line, boxWidth) {
				result = append(result, MarkdownLine{
					Content: mermaidTruncateCell(wrapped, boxWidth),
					Type:    "mermaid",
				})
			}
		}
		if len(result) == 0 {
			result = append(result, MarkdownLine{Content: "", Type: "mermaid"})
		}
		return result
	}

	inner := boxWidth - 2
	contentWidth := boxWidth - 4

	var topLine string
	if title != "" {
		rendered := titleSt.Render(mermaidTruncateCell(" "+title+" ", inner))
		right := inner - lipgloss.Width(rendered)
		topLine = borderSt.Render("┌") + rendered +
			borderSt.Render(strings.Repeat("─", right)+"┐")
	} else {
		topLine = borderSt.Render("┌" + strings.Repeat("─", inner) + "┐")
	}
	bottomLine := borderSt.Render("└" + strings.Repeat("─", inner) + "┘")

	result := []MarkdownLine{{Content: topLine, Type: "mermaid"}}
	for _, line := range body {
		for _, wrapped := range mermaidWrapCell(line, contentWidth) {
			row := borderSt.Render("│") + " " +
				mermaidPadCell(wrapped, contentWidth) + " " +
				borderSt.Render("│")
			result = append(result, MarkdownLine{Content: row, Type: "mermaid"})
		}
	}
	result = append(result, MarkdownLine{Content: bottomLine, Type: "mermaid"})
	return result
}

// mermaidHBar renders a proportional horizontal bar: filled=█ empty=░
func mermaidHBar(fraction float64, barWidth int, fillColor, emptyColor string) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	filled := int(math.Round(fraction * float64(barWidth)))
	if filled > barWidth {
		filled = barWidth
	}
	empty := barWidth - filled
	fs := lipgloss.NewStyle().Foreground(lipgloss.Color(fillColor))
	es := lipgloss.NewStyle().Foreground(lipgloss.Color(emptyColor))
	return fs.Render(strings.Repeat("█", filled)) + es.Render(strings.Repeat("░", empty))
}

func mermaidOrDefault(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ── 🥧 Pie chart ─────────────────────────────────────────────────────────────

func renderMermaidPie(lines []string, theme Theme, width int) []MarkdownLine {
	lines = mermaidSanitizeLines(lines)
	title := parseMermaidTitle(lines)

	type slice struct {
		label string
		value float64
	}

	sliceRe := regexp.MustCompile(`^\s*"(.+?)"\s*:\s*([0-9]+\.?[0-9]*)\s*$`)
	var slices []slice
	for _, line := range lines {
		if m := sliceRe.FindStringSubmatch(line); m != nil {
			v, err := strconv.ParseFloat(m[2], 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			slices = append(slices, slice{label: m[1], value: v})
		}
	}
	if len(slices) == 0 {
		return renderMermaidFallback(lines, "pie", theme, width, true)
	}

	maxValue := 0.0
	for _, s := range slices {
		if s.value > maxValue {
			maxValue = s.value
		}
	}
	if maxValue == 0 {
		maxValue = 1
	}
	normalizedTotal := 0.0
	for _, s := range slices {
		normalizedTotal += s.value / maxValue
	}
	if normalizedTotal == 0 {
		normalizedTotal = 1
	}
	sliceFraction := func(value float64) float64 {
		return (value / maxValue) / normalizedTotal
	}

	colors := mermaidPalette(theme)

	// FIX: use lipgloss.Width (visual cell count) not len() (byte count).
	// Unicode labels like "SDK · RAG" contain multi-byte chars that are
	// only 1 terminal column wide; len() over-counts them.
	maxLabelVW := 0
	for _, s := range slices {
		vw := lipgloss.Width(s.label)
		if vw > maxLabelVW {
			maxLabelVW = vw
		}
	}

	boxWidth := mermaidBoxWidth(width)
	viewport := newMermaidViewport(width)

	if viewport.Compact {
		barWidth := viewport.Content - 9
		if barWidth < 1 {
			barWidth = 1
		}
		textSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		pctSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))
		var body []string
		for i, s := range slices {
			color := colors[i%len(colors)]
			frac := sliceFraction(s.value)
			bulletSt := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
			body = append(body, bulletSt.Render("●")+" "+textSt.Render(s.label))
			body = append(body,
				"  "+mermaidHBar(frac, barWidth, color, theme.TextMuted)+" "+
					pctSt.Render(fmt.Sprintf("%5.1f%%", frac*100)))
		}
		headerTitle := "🥧 " + mermaidOrDefault(title, "Pie Chart")
		return mermaidMakeBox(headerTitle, body, theme, boxWidth)
	}

	// Row: "● " + label(padded) + "  " + bar + "  " + "100.0%"
	const pctW = 7    // " 100.0%"
	const bulletW = 2 // "● "
	const gapW = 4    // two 2-space gaps
	contentW := boxWidth - 4
	barW := contentW - bulletW - maxLabelVW - gapW - pctW
	if barW < 6 {
		barW = 6
	}

	textSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	pctSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))

	var body []string
	for i, s := range slices {
		color := colors[i%len(colors)]
		frac := sliceFraction(s.value)
		bulletSt := lipgloss.NewStyle().Foreground(lipgloss.Color(color))

		// FIX: pad using visual width, not byte length
		labelPad := maxLabelVW - lipgloss.Width(s.label)
		if labelPad < 0 {
			labelPad = 0
		}
		padLabel := s.label + strings.Repeat(" ", labelPad)

		bar := mermaidHBar(frac, barW, color, theme.TextMuted)
		pct := pctSt.Render(fmt.Sprintf("%5.1f%%", frac*100))
		row := bulletSt.Render("●") + " " + textSt.Render(padLabel) + "  " + bar + "  " + pct
		body = append(body, row)
	}

	headerTitle := "🥧 " + mermaidOrDefault(title, "Pie Chart")
	return mermaidMakeBox(headerTitle, body, theme, boxWidth)
}

// ── 📊 XY / Bar chart ─────────────────────────────────────────────────────────

func renderMermaidXYChart(lines []string, theme Theme, width int) []MarkdownLine {
	lines = mermaidSanitizeLines(lines)
	title := parseMermaidTitle(lines)

	var xLabels []string
	xRe := regexp.MustCompile(`(?i)^\s*x-axis\s*\[(.+)\]`)
	for _, line := range lines {
		if m := xRe.FindStringSubmatch(line); m != nil {
			for p := range strings.SplitSeq(m[1], ",") {
				xLabels = append(xLabels, strings.TrimSpace(p))
			}
			break
		}
	}

	yMin, yMax := 0.0, 100.0
	yRe := regexp.MustCompile(`(?i)^\s*y-axis\s+(?:"[^"]*"\s+)?([0-9]+\.?[0-9]*)\s+-->\s+([0-9]+\.?[0-9]*)`)
	for _, line := range lines {
		if m := yRe.FindStringSubmatch(line); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil {
				yMin = v
			}
			if v, err := strconv.ParseFloat(m[2], 64); err == nil {
				yMax = v
			}
			break
		}
	}
	if yMax <= yMin {
		yMax = yMin + 100
	}

	var barVals []float64
	barRe := regexp.MustCompile(`(?i)^\s*bar\s*\[(.+)\]`)
	for _, line := range lines {
		if m := barRe.FindStringSubmatch(line); m != nil {
			for p := range strings.SplitSeq(m[1], ",") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(p), 64); err == nil {
					barVals = append(barVals, v)
				}
			}
			break
		}
	}
	if len(barVals) == 0 {
		return renderMermaidFallback(lines, "xychart", theme, width, true)
	}

	if len(xLabels) == 0 {
		for i := range barVals {
			xLabels = append(xLabels, fmt.Sprintf("%d", i+1))
		}
	}

	const chartH = 8
	boxWidth := mermaidBoxWidth(width)
	viewport := newMermaidViewport(width)

	if viewport.Compact {
		labelWidth := viewport.Content / 3
		if labelWidth < 1 {
			labelWidth = 1
		}
		barWidth := viewport.Content - labelWidth - 10
		if barWidth < 1 {
			barWidth = 1
		}
		labelSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))
		valueSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
		var body []string
		for i, value := range barVals {
			label := fmt.Sprintf("%d", i+1)
			if i < len(xLabels) {
				label = xLabels[i]
			}
			fraction := (value - yMin) / (yMax - yMin)
			body = append(body,
				labelSt.Render(mermaidPadCell(label, labelWidth))+" "+
					mermaidHBar(fraction, barWidth, theme.Primary, theme.TextMuted)+" "+
					valueSt.Render(fmt.Sprintf("%.0f", value)))
		}
		headerTitle := "📊 " + mermaidOrDefault(title, "Chart")
		return mermaidMakeBox(headerTitle, body, theme, boxWidth)
	}

	yLblW := len(fmt.Sprintf("%.0f", yMax)) + 1
	if yLblW < 5 {
		yLblW = 5
	}

	numBars := len(barVals)
	innerW := boxWidth - 4 - yLblW - 2
	colW := innerW / numBars
	if colW < 3 {
		colW = 3
	}

	yAxisSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted))
	borderSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Border))
	barSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary))
	emptySt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.BGLighter))
	lblSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))

	var body []string

	for row := range chartH {
		rowY := yMax - float64(row)/float64(chartH-1)*(yMax-yMin)

		var yLbl, axChar string
		if row%2 == 0 {
			yLbl = fmt.Sprintf("%*.0f", yLblW, rowY)
			axChar = "┤"
		} else {
			yLbl = strings.Repeat(" ", yLblW)
			axChar = "│"
		}

		var cols strings.Builder
		for _, val := range barVals {
			frac := (val - yMin) / (yMax - yMin)
			if frac < 0 {
				frac = 0
			}
			if frac > 1 {
				frac = 1
			}
			barHeight := int(math.Round(frac * float64(chartH)))
			if chartH-1-row < barHeight {
				cols.WriteString(barSt.Render(strings.Repeat("▓", colW)))
			} else {
				cols.WriteString(emptySt.Render(strings.Repeat("░", colW)))
			}
		}
		body = append(body, yAxisSt.Render(yLbl)+" "+borderSt.Render(axChar)+cols.String())
	}

	axisW := numBars * colW
	body = append(body,
		yAxisSt.Render(strings.Repeat(" ", yLblW))+" "+
			borderSt.Render("┴")+borderSt.Render(strings.Repeat("─", axisW)))

	var xRow strings.Builder
	xRow.WriteString(strings.Repeat(" ", yLblW+2))
	for i, lbl := range xLabels {
		if i >= numBars {
			break
		}
		lbl = mermaidTruncateCell(lbl, colW)
		labelWidth := lipgloss.Width(lbl)
		left := (colW - labelWidth) / 2
		right := colW - labelWidth - left
		xRow.WriteString(strings.Repeat(" ", left) + lblSt.Render(lbl) + strings.Repeat(" ", right))
	}
	body = append(body, xRow.String())

	headerTitle := "📊 " + mermaidOrDefault(title, "Chart")
	return mermaidMakeBox(headerTitle, body, theme, boxWidth)
}

// ── ↔ Sequence diagram ────────────────────────────────────────────────────────

func renderMermaidSequence(lines []string, theme Theme, width int) []MarkdownLine {
	lines = mermaidSanitizeLines(lines)
	title := parseMermaidTitle(lines)

	var actors []string
	actorSet := map[string]bool{}
	participantRe := regexp.MustCompile(`(?i)^\s*(?:participant|actor)\s+(\S+)`)
	for _, line := range lines {
		if m := participantRe.FindStringSubmatch(line); m != nil {
			name := m[1]
			if !actorSet[name] {
				actors = append(actors, name)
				actorSet[name] = true
			}
		}
	}

	type seqMsg struct {
		from, to, text string
		dashed         bool
	}
	msgRe := regexp.MustCompile(`^\s*(\w[\w\s]*?)\s*(-{1,2}>{1,2})\s*([\w][\w\s]*?)\s*:\s*(.+)$`)
	var messages []seqMsg
	for _, line := range lines {
		if m := msgRe.FindStringSubmatch(line); m != nil {
			from := strings.TrimSpace(m[1])
			arrow := m[2]
			to := strings.TrimSpace(m[3])
			text := strings.TrimSpace(m[4])
			for _, name := range []string{from, to} {
				if !actorSet[name] {
					actors = append(actors, name)
					actorSet[name] = true
				}
			}
			messages = append(messages, seqMsg{
				from:   from,
				to:     to,
				text:   text,
				dashed: strings.Contains(arrow, "--"),
			})
		}
	}

	if len(actors) == 0 || len(messages) == 0 {
		return renderMermaidFallback(lines, "sequenceDiagram", theme, width, true)
	}

	boxWidth := mermaidBoxWidth(width)
	numActors := len(actors)
	actorSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Primary)).Bold(true)
	lifeSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Border))
	arrowSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent))
	msgSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))

	minColW := 16
	requiresCellAwareFallback := false
	for _, name := range actors {
		if lipgloss.Width(name)+6 > minColW {
			minColW = lipgloss.Width(name) + 6
		}
		if lipgloss.Width(name) != len([]rune(name)) {
			requiresCellAwareFallback = true
		}
	}
	for _, message := range messages {
		if lipgloss.Width(message.text) != len([]rune(message.text)) {
			requiresCellAwareFallback = true
		}
	}
	viewport := newMermaidViewport(width)
	useCompactLayout := viewport.Compact ||
		numActors*minColW > viewport.Content ||
		requiresCellAwareFallback

	if useCompactLayout {
		var body []string
		body = append(body, actorSt.Render(i18n.T("classic_chat.mermaid.actors")))
		for _, actor := range actors {
			body = append(body, "• "+actorSt.Render(actor))
		}
		for _, message := range messages {
			arrow := "→"
			if message.dashed {
				arrow = "⇢"
			}
			body = append(body,
				actorSt.Render(message.from)+" "+
					arrowSt.Render(arrow)+" "+
					actorSt.Render(message.to))
			body = append(body, "  "+msgSt.Render(message.text))
		}
		headerTitle := "↔ " + mermaidOrDefault(title, "Sequence Diagram")
		return mermaidMakeBox(headerTitle, body, theme, boxWidth)
	}

	colW := (boxWidth - 4) / numActors
	if colW < minColW {
		colW = minColW
	}
	totalW := numActors * colW

	// Actor centre positions in the raw rune buffer
	actorCol := map[string]int{}
	for i, name := range actors {
		actorCol[name] = i*colW + colW/2
	}

	// lifelineBuf: totalW-wide rune slice with '|' at each actor centre
	lifelineBuf := func() []rune {
		buf := []rune(strings.Repeat(" ", totalW))
		for _, name := range actors {
			c := actorCol[name]
			if c < totalW {
				buf[c] = '|'
			}
		}
		return buf
	}

	// renderLifeline: colour '|' chars, leave spaces as-is
	renderLifeline := func(buf []rune) string {
		var out strings.Builder
		for _, r := range buf {
			if r == '|' {
				out.WriteString(lifeSt.Render("|"))
			} else {
				out.WriteString(string(r))
			}
		}
		return out.String()
	}

	var body []string

	// ── Actor header row ─────────────────────────────────────────────────────
	headerBuf := []rune(strings.Repeat(" ", totalW))
	for _, name := range actors {
		c := actorCol[name]
		// FIX: centre using visual width, not byte count
		start := c - lipgloss.Width(name)/2
		if start < 0 {
			start = 0
		}
		for i, r := range []rune(name) {
			if start+i < totalW {
				headerBuf[start+i] = r
			}
		}
	}
	// Apply actor colouring by scanning for known name positions
	var hStr strings.Builder
	i := 0
	for i < len(headerBuf) {
		found := false
		for _, name := range actors {
			c := actorCol[name]
			start := c - lipgloss.Width(name)/2
			if start < 0 {
				start = 0
			}
			if i == start {
				hStr.WriteString(actorSt.Render(name))
				i += len([]rune(name))
				found = true
				break
			}
		}
		if !found {
			hStr.WriteString(string(headerBuf[i]))
			i++
		}
	}
	body = append(body, hStr.String())

	// One spacer line between header and first message
	body = append(body, renderLifeline(lifelineBuf()))

	// ── Messages ─────────────────────────────────────────────────────────────
	for mi, m := range messages {
		// FIX: blank lifeline BETWEEN messages only, not before the first one.
		if mi > 0 {
			body = append(body, renderLifeline(lifelineBuf()))
		}

		fromC := actorCol[m.from]
		toC := actorCol[m.to]
		goRight := fromC <= toC

		left, right := fromC, toC
		if !goRight {
			left, right = toC, fromC
		}

		// ── FIX: text label on its OWN LINE above the arrow ──────────────────
		// This eliminates text-inside-dashes truncation artifacts entirely.
		textRunes := []rune(m.text)
		// Available horizontal space between the two actor lifelines (exclusive)
		available := right - left - 2
		if available < 1 {
			available = 1
		}
		// FIX: truncate with visible '…' indicator
		if len(textRunes) > available {
			if available >= 2 {
				textRunes = []rune(string(textRunes[:available-1]) + "…")
			} else {
				textRunes = textRunes[:available]
			}
		}

		// Build text-label buffer (lifelines + centred text)
		labelBuf := lifelineBuf()
		textStart := -1
		if len(textRunes) > 0 {
			mid := (left + right) / 2
			textStart = mid - len(textRunes)/2
			if textStart < left+1 {
				textStart = left + 1
			}
			if textStart+len(textRunes) > right {
				textStart = right - len(textRunes)
			}
			if textStart < left+1 {
				textStart = left + 1
			}
			for ci, r := range textRunes {
				if textStart+ci < totalW {
					labelBuf[textStart+ci] = r
				}
			}
		}

		// Render text-label line
		var labelStr strings.Builder
		for ci, r := range labelBuf {
			inText := textStart >= 0 && ci >= textStart && ci < textStart+len(textRunes)
			switch {
			case inText:
				labelStr.WriteString(msgSt.Render(string(r)))
			case r == '|':
				labelStr.WriteString(lifeSt.Render("|"))
			default:
				labelStr.WriteString(string(r))
			}
		}
		body = append(body, labelStr.String())

		// ── Pure arrow line (dashes only, no embedded text) ───────────────────
		arrowBuf := lifelineBuf()

		dashCh := '─'
		if m.dashed {
			dashCh = '╌'
		}

		// Fill the span with dashes (from left+1 to right-1)
		for ci := left + 1; ci < right; ci++ {
			arrowBuf[ci] = dashCh
		}

		// Arrow head at destination
		if goRight {
			if right < totalW {
				arrowBuf[right] = '>'
			}
		} else {
			if left < totalW {
				arrowBuf[left] = '<'
			}
		}

		// Render arrow line
		var arrowStr strings.Builder
		for _, r := range arrowBuf {
			switch r {
			case '|':
				arrowStr.WriteString(lifeSt.Render("|"))
			case '>', '<':
				arrowStr.WriteString(arrowSt.Render(string(r)))
			case '─', '╌':
				arrowStr.WriteString(arrowSt.Render(string(r)))
			default:
				arrowStr.WriteString(string(r))
			}
		}
		body = append(body, arrowStr.String())
	}

	// Final lifeline row
	body = append(body, renderLifeline(lifelineBuf()))

	headerTitle := "↔ " + mermaidOrDefault(title, "Sequence Diagram")
	return mermaidMakeBox(headerTitle, body, theme, boxWidth)
}

// ── ⬡ Flowchart / Graph ───────────────────────────────────────────────────────

func renderMermaidFlowchart(lines []string, theme Theme, width int) []MarkdownLine {
	lines = mermaidSanitizeLines(lines)
	title := parseMermaidTitle(lines)
	direction := "LR"

	for _, line := range lines {
		t := strings.TrimSpace(line)
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "flowchart ") || strings.HasPrefix(lower, "graph ") {
			parts := strings.Fields(t)
			if len(parts) >= 2 {
				direction = strings.ToUpper(parts[1])
			}
			break
		}
	}

	type node struct {
		id, label, shape string
	}
	type edge struct {
		from, to, label string
		dashed          bool
	}

	nodes := map[string]*node{}
	var nodeOrder []string
	var edges []edge

	nodeRe := regexp.MustCompile(
		`([A-Za-z0-9_]+)(?:\[([^\]]+)\]|\{([^}]+)\}|\(\(([^)]+)\)\))?`)
	edgeTokenRe := regexp.MustCompile(`--\s*([^-\n]+?)\s*-->|-\.->|-->`)

	// extractNodeShapeInfo extracts (id, label, shape) from a regex match
	extractNodeShapeInfo := func(m []string) (id, label, shape string) {
		if len(m) < 5 {
			return "", "", ""
		}
		id = m[1]
		switch {
		case m[2] != "":
			label, shape = m[2], "rect"
		case m[3] != "":
			label, shape = m[3], "diamond"
		case m[4] != "":
			label, shape = m[4], "circle"
		default:
			label, shape = id, "rect"
		}
		return
	}

	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "flowchart") || strings.HasPrefix(lower, "graph") {
			continue
		}
		if strings.HasPrefix(lower, "title ") {
			continue
		}

		registerNode := func(m []string) {
			id, label, shape := extractNodeShapeInfo(m)
			if id == "" {
				return
			}
			if nodes[id] == nil {
				nodes[id] = &node{id: id, label: label, shape: shape}
				nodeOrder = append(nodeOrder, id)
			} else if nodes[id].label == nodes[id].id && label != id {
				nodes[id].label = label
				nodes[id].shape = shape
			}
		}

		arrowMatches := edgeTokenRe.FindAllStringSubmatchIndex(t, -1)
		if len(arrowMatches) == 0 {
			for _, match := range nodeRe.FindAllStringSubmatch(t, -1) {
				registerNode(match)
			}
			continue
		}

		segments := make([]string, 0, len(arrowMatches)+1)
		start := 0
		for _, arrowMatch := range arrowMatches {
			segments = append(segments, strings.TrimSpace(t[start:arrowMatch[0]]))
			start = arrowMatch[1]
		}
		segments = append(segments, strings.TrimSpace(t[start:]))

		endpoints := make([][]string, len(segments))
		for i, segment := range segments {
			endpoints[i] = nodeRe.FindStringSubmatch(segment)
			if endpoints[i] != nil {
				registerNode(endpoints[i])
			}
		}

		for i, arrowMatch := range arrowMatches {
			if endpoints[i] == nil || endpoints[i+1] == nil {
				continue
			}
			from, _, _ := extractNodeShapeInfo(endpoints[i])
			to, _, _ := extractNodeShapeInfo(endpoints[i+1])
			token := t[arrowMatch[0]:arrowMatch[1]]
			edgeLabel := ""
			if len(arrowMatch) >= 4 && arrowMatch[2] >= 0 {
				edgeLabel = strings.TrimSpace(t[arrowMatch[2]:arrowMatch[3]])
			}
			edges = append(edges, edge{
				from:   from,
				to:     to,
				label:  edgeLabel,
				dashed: strings.Contains(token, "-."),
			})
		}
	}

	if len(nodes) == 0 {
		return renderMermaidFallback(lines, "flowchart", theme, width, true)
	}

	// ── Topological sort into layers (BFS) ──────────────────────────────────
	inDeg := map[string]int{}
	adj := map[string][]string{}
	for id := range nodes {
		inDeg[id] = 0
	}
	for _, e := range edges {
		adj[e.from] = append(adj[e.from], e.to)
		inDeg[e.to]++
	}

	var layers [][]string
	queue := []string{}
	for _, id := range nodeOrder {
		if inDeg[id] == 0 {
			queue = append(queue, id)
		}
	}
	seen := map[string]bool{}
	for len(queue) > 0 {
		var layer []string
		var next []string
		for _, id := range queue {
			if seen[id] {
				continue
			}
			seen[id] = true
			layer = append(layer, id)
			for _, nbr := range adj[id] {
				inDeg[nbr]--
				if inDeg[nbr] == 0 {
					next = append(next, nbr)
				}
			}
		}
		if len(layer) > 0 {
			layers = append(layers, layer)
		}
		queue = next
	}
	for _, id := range nodeOrder {
		if !seen[id] {
			layers = append(layers, []string{id})
		}
	}

	// ── Rendering ───────────────────────────────────────────────────────────
	boxWidth := mermaidBoxWidth(width)
	borderSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Border))
	nodeSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Text))
	arrowSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Accent))
	lblSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextDim))
	diamondSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Warning))
	circleSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.Info))

	renderNode := func(n *node) string {
		switch n.shape {
		case "diamond":
			return diamondSt.Render("◇") + " " + nodeSt.Render(n.label)
		case "circle":
			return circleSt.Render("◉") + " " + nodeSt.Render(n.label)
		default:
			return borderSt.Render("[") + nodeSt.Render(n.label) + borderSt.Render("]")
		}
	}

	// FIX: edgeMeta now takes an explicit target so label lookup is exact,
	// not just "first edge from this node".
	edgeMeta := func(fromID, toID string) (label string, dashed bool) {
		for _, e := range edges {
			if e.from == fromID && (toID == "" || e.to == toID) {
				return e.label, e.dashed
			}
		}
		return "", false
	}

	makeArrow := func(fromID, toID string) string {
		lbl, dashed := edgeMeta(fromID, toID)
		if dashed {
			return arrowSt.Render(" ╌╌> ")
		}
		if lbl != "" {
			return arrowSt.Render(" ─") + lblSt.Render(lbl) + arrowSt.Render("─> ")
		}
		return arrowSt.Render(" ──> ")
	}

	var body []string

	if (direction == "LR" || direction == "RL") && !newMermaidViewport(width).Compact {
		connected := make(map[string]bool, len(nodes))
		for _, edge := range edges {
			connected[edge.from] = true
			connected[edge.to] = true
			if direction == "RL" {
				reverseArrow := arrowSt.Render(" <── ")
				if edge.dashed {
					reverseArrow = arrowSt.Render(" <╌╌ ")
				} else if edge.label != "" {
					reverseArrow = arrowSt.Render(" <─") +
						lblSt.Render(edge.label) +
						arrowSt.Render("─ ")
				}
				body = append(body,
					renderNode(nodes[edge.to])+reverseArrow+renderNode(nodes[edge.from]))
				continue
			}
			body = append(body,
				renderNode(nodes[edge.from])+makeArrow(edge.from, edge.to)+renderNode(nodes[edge.to]))
		}
		for _, nodeID := range nodeOrder {
			if !connected[nodeID] {
				body = append(body, renderNode(nodes[nodeID]))
			}
		}
	} else {
		// TD/TB — FIX: show parallel nodes as branches with ├──> / └──> rather
		// than dumping them on one line separated by spaces (which looked
		// like a connected sequence).  Also surface edge labels on ↓ arrows.
		for li, layer := range layers {
			if len(layer) == 1 {
				nodeID := layer[0]
				body = append(body, "  "+renderNode(nodes[nodeID]))
				if li < len(layers)-1 {
					// FIX: show edge label alongside the ↓ when one exists
					lbl, _ := edgeMeta(nodeID, "")
					if lbl != "" {
						body = append(body, arrowSt.Render("  │ ")+lblSt.Render(lbl))
					}
					body = append(body, arrowSt.Render("  ↓"))
				}
			} else {
				// FIX: multiple parallel nodes → branch indicators
				for ni, nodeID := range layer {
					connector := "  ├──> "
					if ni == len(layer)-1 {
						connector = "  └──> "
					}
					body = append(body, arrowSt.Render(connector)+renderNode(nodes[nodeID]))
				}
				if li < len(layers)-1 {
					body = append(body, arrowSt.Render("  ↓"))
				}
			}
		}
	}

	if len(body) == 0 {
		return renderMermaidFallback(lines, "flowchart", theme, width, true)
	}

	headerTitle := "⬡ " + mermaidOrDefault(title, "Flowchart")
	return mermaidMakeBox(headerTitle, body, theme, boxWidth)
}

// ── Fallback ──────────────────────────────────────────────────────────────────

func renderMermaidFallback(lines []string, diagType string, theme Theme, width int, verbose bool) []MarkdownLine {
	lines = mermaidSanitizeLines(lines)
	typeLabel := diagType
	if len(typeLabel) > 0 {
		typeLabel = strings.ToUpper(string([]rune(typeLabel)[0])) + string([]rune(typeLabel)[1:])
	}

	noteSt := lipgloss.NewStyle().Foreground(lipgloss.Color(theme.TextMuted)).Italic(true)
	codeSt := lipgloss.NewStyle().
		Foreground(lipgloss.Color(theme.Text)).
		Background(lipgloss.Color(theme.BGLight))

	boxWidth := mermaidBoxWidth(width)

	maxShow := len(lines)
	if !verbose && maxShow > 6 {
		maxShow = 6
	}

	var body []string
	body = append(body, noteSt.Render(i18n.T("classic_chat.mermaid.text_preview", typeLabel)))
	body = append(body, "")

	for i := 0; i < maxShow && i < len(lines); i++ {
		body = append(body, codeSt.Render("  "+lines[i]))
	}
	if !verbose && len(lines) > maxShow {
		body = append(body, noteSt.Render(i18n.T("classic_chat.mermaid.more_lines", len(lines)-maxShow)))
	}

	headerTitle := "⬡ Mermaid: " + typeLabel
	return mermaidMakeBox(headerTitle, body, theme, boxWidth)
}
