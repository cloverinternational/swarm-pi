package subagent

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/uitypes"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// ColorSubAgentIdentity is the canonical cyan identity color for all sub-agent UI elements.
// Named explicitly so it is grep-able and never accidentally replaced with palette.Accent
// or palette.Warning. Every place that colors an agent name, border, or header uses this.
const ColorSubAgentIdentity = "#39D2C0"

// Tool-specific colors for visual differentiation
const (
	colorRead   = "#39D2C0" // Teal - file reading
	colorWrite  = "#F4C95D" // Yellow/warning - file writing
	colorBash   = "#A78BFA" // Purple - command execution
	colorSearch = "#4C9AFF" // Blue - grep/glob/search
	colorTask   = "#6E64E8" // Accent - sub-agents
	colorOther  = "#8A93A6" // Dim - other tools
)

// --------------------------------------------------------------------------
// Verb sampling — mirrors Claude Code's spinnerVerbs / turnCompletionVerbs
// Sampled once at sub-agent creation and frozen in SubAgentDisplay.SpinnerVerb
// so the label stays stable across re-renders (same as React useState approach).
// --------------------------------------------------------------------------

// SpinnerVerbs is the full list of present-tense activity descriptions.
var SpinnerVerbs = []string{
	"Thinking…",
	"Searching…",
	"Analyzing…",
	"Exploring…",
	"Processing…",
	"Reasoning…",
	"Investigating…",
	"Examining…",
	"Computing…",
	"Evaluating…",
	"Reviewing…",
	"Planning…",
	"Working…",
	"Executing…",
	"Verifying…",
}

var spinnerVerbIDs = []string{
	"classic_chat_3.subagent.thinking",
	"classic_chat_3.subagent.searching",
	"classic_chat_3.subagent.analyzing",
	"classic_chat_3.subagent.exploring",
	"classic_chat_3.subagent.processing",
	"classic_chat_3.subagent.reasoning",
	"classic_chat_3.subagent.investigating",
	"classic_chat_3.subagent.examining",
	"classic_chat_3.subagent.computing",
	"classic_chat_3.subagent.evaluating",
	"classic_chat_3.subagent.reviewing",
	"classic_chat_3.subagent.planning",
	"classic_chat_3.subagent.working",
	"classic_chat_3.subagent.executing",
	"classic_chat_3.subagent.verifying",
}

// CompletionVerbs is the full list of past-tense completions.
var CompletionVerbs = []string{
	"Thought",
	"Searched",
	"Analyzed",
	"Explored",
	"Processed",
	"Reasoned",
	"Investigated",
	"Examined",
	"Computed",
	"Evaluated",
	"Reviewed",
	"Planned",
	"Worked",
	"Executed",
	"Verified",
}

var completionVerbIDs = []string{
	"classic_chat_3.subagent.thought",
	"classic_chat_3.subagent.searched",
	"classic_chat_3.subagent.analyzed",
	"classic_chat_3.subagent.explored",
	"classic_chat_3.subagent.processed",
	"classic_chat_3.subagent.reasoned",
	"classic_chat_3.subagent.investigated",
	"classic_chat_3.subagent.examined",
	"classic_chat_3.subagent.computed",
	"classic_chat_3.subagent.evaluated",
	"classic_chat_3.subagent.reviewed",
	"classic_chat_3.subagent.planned",
	"classic_chat_3.subagent.worked",
	"classic_chat_3.subagent.executed",
	"classic_chat_3.subagent.verified",
}

// SampleSpinnerVerb returns a stable present-tense verb for the given agent name.
// Uses a fast string hash so the same agent name always gets the same verb.
func SampleSpinnerVerb(agentName string) string {
	if len(SpinnerVerbs) == 0 {
		return i18n.T("classic_chat_3.subagent.working")
	}
	h := fnvHash(agentName)
	idx := int(h % uint32(len(SpinnerVerbs)))
	if idx < len(spinnerVerbIDs) {
		return i18n.T(spinnerVerbIDs[idx])
	}
	return SpinnerVerbs[idx]
}

// SampleCompletionVerb returns a stable past-tense verb for the given agent name.
func SampleCompletionVerb(agentName string) string {
	if len(CompletionVerbs) == 0 {
		return i18n.T("classic_chat_3.subagent.worked")
	}
	h := fnvHash(agentName)
	idx := int(h % uint32(len(CompletionVerbs)))
	if idx < len(completionVerbIDs) {
		return i18n.T(completionVerbIDs[idx])
	}
	return CompletionVerbs[idx]
}

// fnvHash returns a quick hash of a string, used for stable verb selection.
func fnvHash(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// formatElapsed formats a duration into a compact string: "3s", "1m 12s", "2h 5m"
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// formatCompactNum formats a count compactly: 1200 	 "1.2k", 999 	 "999"
func formatCompactNum(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// SubAgentRenderConfig configures sub-agent box rendering
type SubAgentRenderConfig struct {
	// DefaultMaxLines is the max content lines shown in compact mode
	DefaultMaxLines int
	// VerboseMaxLines is the max lines shown in verbose mode
	VerboseMaxLines int
	// MaxToolLines is max output lines per tool in verbose mode
	MaxToolLines int
	// ShowThinking controls whether thinking content is shown
	ShowThinking bool
	// MaxFinalOutputLines is the max lines of final output to show in compact mode
	MaxFinalOutputLines int
	// LiveProgressLines is the number of most-recent tool-call detail lines to
	// show under the 2-line summary while the sub-agent is still streaming.
	// Mirrors claude-code's MAX_PROGRESS_MESSAGES_TO_SHOW (default 6 in the
	// reference, 4 here to fit terminal real estate). When more tool calls
	// exist beyond the window, a "+N more tool uses" hint is rendered.
	// Set to 0 to disable the live progress window (legacy 2-line behaviour).
	LiveProgressLines int
}

// DefaultSubAgentRenderConfig returns default configuration
func DefaultSubAgentRenderConfig() SubAgentRenderConfig {
	return SubAgentRenderConfig{
		DefaultMaxLines:     12,
		VerboseMaxLines:     50,
		MaxToolLines:        3,
		ShowThinking:        true,
		MaxFinalOutputLines: 6,
		LiveProgressLines:   4,
	}
}

// SubAgentRenderer renders sub-agent activity in a boxed format
type SubAgentRenderer struct {
	config SubAgentRenderConfig
	styles *uitypes.SubAgentRenderStyles
	width  int
}

// NewSubAgentRenderer creates a new sub-agent renderer
func NewSubAgentRenderer(width int, styles *uitypes.SubAgentRenderStyles) *SubAgentRenderer {
	return &SubAgentRenderer{
		config: DefaultSubAgentRenderConfig(),
		styles: styles,
		width:  width,
	}
}

// SetWidth updates the rendering width
func (r *SubAgentRenderer) SetWidth(width int) {
	r.width = width
}

// GetConfig returns the current render configuration
func (r *SubAgentRenderer) GetConfig() SubAgentRenderConfig {
	return r.config
}

// SetConfig updates the render configuration
func (r *SubAgentRenderer) SetConfig(config SubAgentRenderConfig) {
	r.config = config
}

// getToolColor returns the appropriate color for a tool name
func (r *SubAgentRenderer) getToolColor(toolName string) string {
	switch toolName {
	case "Read", "ReadLegacy", "NotebookRead":
		return colorRead
	case "Write", "Edit", "EditLegacy", "NotebookEdit":
		return colorWrite
	case "Bash", "BashOutput", "KillShell":
		return colorBash
	case "Grep", "Glob", "LS":
		return colorSearch
	case "Task", "BackgroundTask":
		return colorTask
	default:
		return colorOther
	}
}

// --------------------------------------------------------------------------
// Data extraction helpers — separate concerns from rendering
// --------------------------------------------------------------------------

// subAgentData holds extracted, pre-processed data from a SubAgentDisplay
type subAgentData struct {
	sa              *uitypes.SubAgentDisplay // reference back to source (for verb/timing fields)
	taskInstruction string
	thinkingLines   []string
	contentBlocks   []string // each entry is one cleaned content block
	toolCalls       []toolCallInfo
	resultMap       map[string]*uitypes.ToolResultDisplay
	toolCount       int
	completedTools  int
	errorCount      int
	finalOutput     string // last non-empty content block (the agent's answer)
}

type toolCallInfo struct {
	tc     *uitypes.ToolCallDisplay
	result *uitypes.ToolResultDisplay
}

// extractData pulls all relevant data from a SubAgentDisplay into a flat struct
func (r *SubAgentRenderer) extractData(sa *uitypes.SubAgentDisplay) subAgentData {
	d := subAgentData{
		sa:              sa,
		taskInstruction: sa.TaskInstruction,
		resultMap:       make(map[string]*uitypes.ToolResultDisplay, len(sa.Blocks)/2),
	}

	// First pass: build result map
	for _, block := range sa.Blocks {
		if block.Type == "tool_result" && block.ToolResult != nil {
			d.resultMap[block.ToolResult.CallID] = block.ToolResult
		}
	}

	// Second pass: extract everything
	for _, block := range sa.Blocks {
		switch block.Type {
		case "thinking":
			content := block.GetContent()
			if r.config.ShowThinking && content != "" {
				d.thinkingLines = append(d.thinkingLines, content)
			}

		case "content":
			content := block.GetContent()
			if content != "" {
				content = cleanContent(content)
				content = r.filterXMLMarkup(content)
				content = strings.TrimSpace(content)
				if content != "" {
					d.contentBlocks = append(d.contentBlocks, content)
				}
			}

		case "tool_call":
			if block.ToolCall != nil {
				tc := block.ToolCall
				d.toolCount++
				result := d.resultMap[tc.ID]
				if result != nil {
					d.completedTools++
					if result.Error != "" {
						d.errorCount++
					}
				}
				d.toolCalls = append(d.toolCalls, toolCallInfo{tc: tc, result: result})
			}
		}
	}

	// Final output is the LAST content block — that's the agent's answer
	if len(d.contentBlocks) > 0 {
		d.finalOutput = d.contentBlocks[len(d.contentBlocks)-1]
	}

	return d
}

// --------------------------------------------------------------------------
// RenderSubAgent — Claude Code AgentProgressLine format
// --------------------------------------------------------------------------
//
// Produces 2 lines per sub-agent (matching Claude Code's AgentProgressLine):
//
//	Line 1:  "   ├─ " @agent-name  task description…  · 3 tools
//	Line 2:  "   │  ⏿  " lastToolInfo / "Initializing…" / "Done"
//
// isLast controls whether ├─/│ (more agents follow) or └─/spaces (last agent).
//
// RenderSubAgent renders a sub-agent activity block.
// isStreaming: whether this message is still actively being generated
// hasPendingTools: whether any tool calls lack results
// verboseMode: when true, appends per-tool detail lines after the 2-line summary
// isLast: true when this is the last sub-agent block in the current message
func (r *SubAgentRenderer) RenderSubAgent(
	sa *uitypes.SubAgentDisplay,
	isStreaming bool,
	hasPendingTools bool,
	verboseMode bool,
	spinner string,
	animFrame int,
) []string {
	return r.RenderSubAgentWithPosition(sa, isStreaming, hasPendingTools, verboseMode, spinner, animFrame, true)
}

// RenderSubAgentWithPosition is like RenderSubAgent but takes an explicit isLast flag
// so the caller can control whether ├─ (more follow) or └─ (last) is used.
func (r *SubAgentRenderer) RenderSubAgentWithPosition(
	sa *uitypes.SubAgentDisplay,
	isStreaming bool,
	hasPendingTools bool,
	verboseMode bool,
	spinner string,
	animFrame int,
	isLast bool,
) []string {
	if sa == nil {
		return nil
	}

	data := r.extractData(sa)

	var lines []string

	// ── Line 1: summary ──────────────────────────────────────────────────
	lines = append(lines, r.renderSummaryLine(sa, data, isStreaming, isLast))

	// ── Line 2: activity / status ─────────────────────────────────────────
	lines = append(lines, r.renderActivityLine(data, sa, isStreaming, hasPendingTools, spinner, animFrame, isLast))

	// ── Optional verbose detail lines ─────────────────────────────────────
	//
	// Three modes, matching claude-code's renderToolUseProgressMessage:
	//
	//   1. verboseMode == true → show ALL tool calls (transcript view).
	//   2. verboseMode == false AND isStreaming AND toolCount > 0 →
	//      show the LAST N tool calls (live progress window). N is taken
	//      from r.config.LiveProgressLines (default 4). If more exist,
	//      a "+X more tool uses" line is prepended so the user knows the
	//      window is condensed. This is what gives the parent agent a
	//      real-time view of what each sub-agent is doing instead of
	//      staring at a frozen "Initializing…" / single-line "tool name".
	//   3. Otherwise (completed sub-agent in compact mode) → no detail
	//      lines; the activity line already shows "Done" verb.
	contPrefix := r.continuationPrefix(isLast)
	switch {
	case verboseMode && data.toolCount > 0:
		for _, tc := range data.toolCalls {
			lines = append(lines, r.renderVerboseToolLine(tc, isStreaming, spinner, contPrefix)...)
		}
	case !verboseMode && isStreaming && data.toolCount > 0 && r.config.LiveProgressLines > 0:
		window := r.config.LiveProgressLines
		hidden := data.toolCount - window
		start := 0
		if hidden > 0 {
			start = hidden
			dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Italic(true)
			hiddenLine := i18n.T("chat_b.subagent.more_tool_uses", hidden)
			lines = append(lines, contPrefix+dimStyle.Render(hiddenLine))
		}
		for _, tc := range data.toolCalls[start:] {
			lines = append(lines, r.renderVerboseToolLine(tc, isStreaming, spinner, contPrefix)...)
		}
	}

	return lines
}

// plural returns "s" when n != 1, for use in compact English ("1 tool use" /
// "5 tool uses"). Kept inline (not a templating helper) so callers don't pay
// the cost of reflection or formatting for a one-letter suffix.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// renderSummaryLine produces the first line:
//
//	"   ├─ " @name  task…  · N tools
func (r *SubAgentRenderer) renderSummaryLine(
	sa *uitypes.SubAgentDisplay,
	data subAgentData,
	isStreaming bool,
	isLast bool,
) string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted))
	nameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubAgentIdentity)).Bold(true)
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextDim))

	// Prefix: 3-space indent + tree char
	treeChar := "├─"
	if isLast {
		treeChar = "└─"
	}
	prefix := dimStyle.Render("   " + treeChar + " ")

	// Agent name
	agentName := sa.AgentName
	if agentName == "" {
		agentName = "agent"
	}
	name := nameStyle.Render("@" + agentName)

	// Task description (dim, italic, truncated)
	taskPart := ""
	if data.taskInstruction != "" {
		maxTask := max(r.width-30, 20)
		task := data.taskInstruction
		// collapse newlines
		task = strings.Join(strings.Fields(task), " ")
		if len(task) > maxTask {
			task = task[:maxTask-3] + "..."
		}
		taskStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(palette.TextMuted)).
			Italic(true)
		taskPart = "  " + taskStyle.Render(task)
	}

	// Stats: tool count
	statsPart := ""
	if data.toolCount > 0 {
		statsPart = sepStyle.Render(" · ") +
			dimStyle.Render(i18n.T("classic_chat_3.subagent.tool_uses", data.toolCount))
	}
	// Token count when available
	if sa.TokenCount > 0 {
		statsPart += sepStyle.Render(" · ") + dimStyle.Render(i18n.T("chat_b.subagent.tokens", formatCompactNum(sa.TokenCount)))
	}
	// Elapsed time when set
	if !sa.StartTime.IsZero() && isStreaming {
		statsPart += sepStyle.Render(" · ") + dimStyle.Render(formatElapsed(time.Since(sa.StartTime)))
	}

	return prefix + name + taskPart + statsPart
}

// renderActivityLine produces the second line with the ⏿ status indicator:
//
//	"   │  ⏿  " lastToolInfo / "Initializing…" / "Done"
func (r *SubAgentRenderer) renderActivityLine(
	data subAgentData,
	sa *uitypes.SubAgentDisplay,
	isStreaming bool,
	hasPendingTools bool,
	spinner string,
	animFrame int,
	isLast bool,
) string {
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted))

	// Prefix: 3-space indent + continuation char + ⏿ glyph
	// Claude Code: isLast ? "   ⏿  " : "│  ⏿  " (then padded by paddingLeft=3)
	var contPrefix string
	if isLast {
		contPrefix = "      ⏿  " // 6 spaces + ⏿ + 2 spaces
	} else {
		contPrefix = "   │  ⏿  " // 3 spaces + │ + 2 spaces + ⏿ + 2 spaces
	}

	statusText := r.getStatusText(data, sa, isStreaming, hasPendingTools)

	return dimStyle.Render(contPrefix) + dimStyle.Render(statusText)
}

// getStatusText returns the activity/status string for the second line.
// Priority (mirrors Claude Code's getStatusText):
//  1. Not resolved (streaming): lastToolInfo or sampled verb
//  2. Resolved (done): "Done"
func (r *SubAgentRenderer) getStatusText(
	data subAgentData,
	sa *uitypes.SubAgentDisplay,
	isStreaming bool,
	hasPendingTools bool,
) string {
	if !isStreaming {
		// Completed — show past-tense verb or "Done"
		verb := i18n.T("classic_chat_3.subagent.done")
		if sa != nil && sa.CompletionVerb != "" {
			verb = sa.CompletionVerb
		}
		return verb
	}
	// Detect "callback never fired" — long elapsed, zero events, no heartbeat.
	// The sync delegate_task path emits a HeartbeatUpdate every 5s when the
	// progress callback IS wired, so the only way to stay silent past this
	// threshold is for the wiring itself to be broken. Without this branch,
	// a wiring bug looked identical to a normal "agent is thinking" state
	// and could persist for hours before the user noticed.
	const noProgressThreshold = 30 * time.Second
	if sa != nil && !sa.StartTime.IsZero() {
		elapsed := time.Since(sa.StartTime)
		if elapsed > noProgressThreshold && sa.EventCount == 0 && sa.LastHeartbeat.IsZero() {
			return "(no progress callback — streaming wiring may be broken)"
		}
	}
	// Active — show last tool activity or sampled spinner verb
	if lastActivity := r.getLastActivityLine(data); lastActivity != "" {
		return lastActivity
	}
	// Fall back to sampled verb (frozen at creation)
	if sa != nil && sa.SpinnerVerb != "" {
		return sa.SpinnerVerb
	}
	return i18n.T("classic_chat_3.subagent.initializing")
}

// continuationPrefix returns the left-margin string for verbose detail lines.
func (r *SubAgentRenderer) continuationPrefix(isLast bool) string {
	if isLast {
		return "       " // 7 spaces
	}
	return "   │   " // 3 spaces + │ + 3 spaces
}

// renderVerboseToolLine renders a single tool call as a detail line (verbose mode only).
func (r *SubAgentRenderer) renderVerboseToolLine(tc toolCallInfo, isStreaming bool, spinner string, prefix string) []string {
	toolColor := r.getToolColor(tc.tc.Name)
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(toolColor))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted))

	// Status glyph
	var statusGlyph string
	if tc.result != nil {
		if tc.result.Error != "" {
			statusGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Error)).Render("✗")
		} else {
			statusGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success)).Render("✓")
		}
	} else if isStreaming {
		statusGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(ColorSubAgentIdentity)).Render(spinner)
	} else {
		statusGlyph = dimStyle.Render("○")
	}

	preview := r.getToolPreview(tc.tc)
	toolPart := toolStyle.Render(tc.tc.Name)
	if preview != "" {
		toolPart += " " + dimStyle.Render(preview)
	}

	line := prefix + statusGlyph + " " + toolPart

	var result []string
	result = append(result, line)

	// Inline error
	if tc.result != nil && tc.result.Error != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Error))
		errLines := strings.SplitN(strings.TrimSpace(tc.result.Error), "\n", 3)
		for _, el := range errLines {
			result = append(result, prefix+"  "+errStyle.Render(el))
		}
	}

	return result
}

// --------------------------------------------------------------------------
// getLastActivityLine — mirrors Claude Code's lastToolInfo
// --------------------------------------------------------------------------

// getLastActivityLine returns a short description of the most recent in-flight tool call,
// or the most recently completed one if all are done. Returns "" when nothing is available.
func (r *SubAgentRenderer) getLastActivityLine(data subAgentData) string {
	// Walk in reverse to find the last pending tool call first
	for i := len(data.toolCalls) - 1; i >= 0; i-- {
		tc := data.toolCalls[i]
		if tc.result != nil {
			continue // already done
		}
		name := tc.tc.Name
		preview := r.getToolPreview(tc.tc)
		if preview != "" {
			return name + " " + preview
		}
		return name + "\u2026"
	}
	// All done — show last completed tool as activity hint
	if len(data.toolCalls) > 0 {
		last := data.toolCalls[len(data.toolCalls)-1]
		preview := r.getToolPreview(last.tc)
		if preview != "" {
			return last.tc.Name + " " + preview
		}
	}
	return ""
}

// --------------------------------------------------------------------------
// Utility helpers
// --------------------------------------------------------------------------

// cleanContent removes noise from content strings.
func cleanContent(content string) string {
	content = strings.ReplaceAll(content, "System: Please continue.", "")
	content = strings.ReplaceAll(content, "Please continue.", "")
	return content
}

// getToolPreview extracts a short preview from tool parameters.
func (r *SubAgentRenderer) getToolPreview(tc *uitypes.ToolCallDisplay) string {
	if tc == nil || tc.Parameters == nil {
		return ""
	}
	params := tc.Parameters
	if cmd, ok := params["command"].(string); ok {
		return r.truncateStr(strings.ReplaceAll(cmd, "\n", " "), 40)
	}
	if path, ok := params["file_path"].(string); ok {
		return r.truncateStr(path, 40)
	}
	if path, ok := params["path"].(string); ok {
		return r.truncateStr(path, 40)
	}
	if pattern, ok := params["pattern"].(string); ok {
		return r.truncateStr(pattern, 30)
	}
	if desc, ok := params["description"].(string); ok {
		return r.truncateStr(desc, 40)
	}
	if query, ok := params["query"].(string); ok {
		return r.truncateStr(query, 40)
	}
	return ""
}

// truncateStr truncates string with ellipsis.
func (r *SubAgentRenderer) truncateStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// xmlTagPattern matches XML-like tags used in function call markup.
var xmlTagPattern = regexp.MustCompile(`</?(?:function_calls|antml:function_calls|antml:invoke|invoke|antml:parameter|parameter)[^>]*>`)

// filterXMLMarkup removes XML function call markup from content.
func (r *SubAgentRenderer) filterXMLMarkup(content string) string {
	lines := strings.Split(content, "\n")
	var filtered []string
	inXMLBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if xmlTagPattern.MatchString(trimmed) {
			if strings.Contains(trimmed, "function_calls") {
				if strings.HasPrefix(trimmed, "</") {
					inXMLBlock = false
				} else {
					inXMLBlock = true
				}
				continue
			}
			if strings.Contains(trimmed, "invoke") || strings.Contains(trimmed, "parameter") {
				continue
			}
		}
		if inXMLBlock {
			if strings.HasPrefix(trimmed, "<") || strings.HasPrefix(trimmed, "</") {
				continue
			}
		}
		cleaned := xmlTagPattern.ReplaceAllString(line, "")
		cleaned = strings.TrimSpace(cleaned)
		if cleaned != "" {
			filtered = append(filtered, cleaned)
		}
	}
	return strings.Join(filtered, "\n")
}
