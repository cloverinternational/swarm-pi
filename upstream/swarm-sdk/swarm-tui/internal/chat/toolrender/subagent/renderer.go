package subagent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// outputRecord mirrors agent.OutputRecord for NDJSON parsing without importing the SDK agent package.
type outputRecord struct {
	Type    string          `json:"type"`
	Content string          `json:"content,omitempty"`
	Name    string          `json:"name,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Output  string          `json:"output,omitempty"`
	Append  bool            `json:"append,omitempty"`
}

// SubagentRenderer handles rendering of Subagent and SubagentOutput tool results.
// It detects and formats JSON/TOON output specially.
type SubagentRenderer struct{}

// New creates a new SubagentRenderer.
func New() *SubagentRenderer {
	return &SubagentRenderer{}
}

// CanRender returns true for all sub-agent invocation tools.
func (r *SubagentRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	switch ctx.ToolName {
	case "Subagent", "SubagentOutput", "Task", "BackgroundTask", "TaskOutput":
		return true
	}
	return false
}

// Render returns styled output lines for sub-agent results.
// It detects JSON/TOON format and renders it nicely.
func (r *SubagentRenderer) Render(ctx *toolrender.RenderContext, _ toolrender.CachedResult) []string {
	// WHY THE FINAL CLAMP: every branch below sizes content with a FLOOR
	// (max(width-10, 20), max(width-14, 20)) rather than a cap, so on a narrow
	// pane they hand themselves more room than exists; renderJSON and
	// renderSubagentOutputStructured do not measure at all; and renderTOON's
	// `width-8` / `width-11` arithmetic goes negative below width 11, which
	// would panic on a byte slice. Clamping the assembled line is the one place
	// the true left edge and each branch's differing indent are both visible.
	//
	// FitLines also expands tabs: sub-agent transcripts embed tool output
	// verbatim, and a tab measures one column but draws up to four.
	return shared.FitLines(r.render(ctx), ctx.Width)
}

// render produces the unclamped lines; Render applies the viewport bound.
func (r *SubagentRenderer) render(ctx *toolrender.RenderContext) []string {
	width := ctx.Width
	if width <= 0 {
		width = 80
	}

	// Error rendering
	if ctx.Error != "" {
		return r.renderError(ctx.Error, width, ctx.BgColor)
	}

	// Subagent / Task / BackgroundTask launch response — JSON with agent_id + message.
	// The sub_agent_activity block handles live display; suppress the raw JSON
	// and show only a compact task-description stub.
	isAgentLaunch := ctx.ToolName == "Subagent" || ctx.ToolName == "Task" || ctx.ToolName == "BackgroundTask"
	if isAgentLaunch && r.looksLikeJSON(ctx.Output) {
		if stub := r.renderSubagentLaunchStub(ctx.Output, width); stub != nil {
			return stub
		}
	}

	// TaskOutput result: show action tree + last message instead of raw NDJSON.
	if ctx.ToolName == "TaskOutput" {
		if lines := r.renderTaskOutput(ctx.Output, width, ctx.BgColor); lines != nil {
			return lines
		}
	}

	// For SubagentOutput/TaskOutput with structured format (agent_id, status, etc.)
	if (ctx.ToolName == "SubagentOutput" || ctx.ToolName == "TaskOutput") && strings.Contains(ctx.Output, "agent_id:") {
		return r.renderSubagentOutputStructured(ctx.Output, width, ctx.BgColor)
	}

	// Try to detect and parse JSON
	if r.looksLikeJSON(ctx.Output) {
		if lines := r.renderJSON(ctx.Output, width, ctx.BgColor); lines != nil {
			return lines
		}
	}

	// Try to detect TOON format (tool output notification)
	if r.looksLikeTOON(ctx.Output) {
		return r.renderTOON(ctx.Output, width, ctx.BgColor)
	}

	// Default: render as plain text
	return r.renderPlainOutput(ctx.Output, width, ctx.BgColor)
}

// renderSubagentLaunchStub handles the Subagent launch-acknowledgment JSON.
// Instead of dumping the whole JSON (agent_id, description, message, output_file, ...),
// it shows a single compact line with just the task the agent was given.
// Returns nil if the JSON doesn't look like a launch response.
func (r *SubagentRenderer) renderSubagentLaunchStub(output string, width int) []string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(output), &obj); err != nil {
		return nil
	}

	// Must have agent_id to be a launch response
	agentID, _ := obj["agent_id"].(string)
	if agentID == "" {
		return nil
	}

	// Extract the task from description: [TASK]...[/TASK]
	task := ""
	if desc, ok := obj["description"].(string); ok && desc != "" {
		task = extractTask(desc)
	}
	// Fall back to message field
	if task == "" {
		if msg, ok := obj["message"].(string); ok {
			task = msg
		}
	}
	if task == "" {
		task = i18n.T("toolrender.subagent.launched")
	}

	// Truncate to fit
	maxLen := max(
		// account for "    ⎿ " prefix + padding
		width-14, 20)
	task = strings.Join(strings.Fields(task), " ") // collapse whitespace
	// Width-aware, not byte-aware: `task[:n]` sliced bytes, which mismeasured
	// wide runes and could cut a UTF-8 sequence in half.
	if shared.PrintableWidth(task) > maxLen {
		task = shared.TruncateANSI(task, maxLen, "...")
	}

	return []string{
		"    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + shared.AnsiFgDim + task + shared.AnsiReset,
	}
}

// extractTask pulls the content of the [TASK]...[/TASK] block from a description string.
func extractTask(desc string) string {
	start := strings.Index(desc, "[TASK]")
	end := strings.Index(desc, "[/TASK]")
	if start < 0 || end < 0 || end <= start {
		return ""
	}
	task := strings.TrimSpace(desc[start+len("[TASK]") : end])
	return task
}

// PreProcess returns nil. Subagent rendering is done live.
func (r *SubagentRenderer) PreProcess(_ *toolrender.RenderContext) toolrender.CachedResult {
	return nil
}

// renderTaskOutput renders a TaskOutput tool result (action=result returns NDJSON).
// Instead of dumping raw NDJSON it shows: status header, action tree, last message.
// Returns nil if the output doesn't match the expected structured format.
func (r *SubagentRenderer) renderTaskOutput(output string, width int, bgColor string) []string {
	// buildFileResult format: header key:value lines, then "───..." separator, then NDJSON.
	sepIdx := strings.Index(output, "───")
	if sepIdx < 0 {
		return nil // status/cancel JSON — handled by renderJSON below
	}

	header := strings.TrimSpace(output[:sepIdx])
	rest := output[sepIdx:]
	// Skip the separator line itself.
	if nlIdx := strings.Index(rest, "\n"); nlIdx >= 0 {
		rest = rest[nlIdx+1:]
	}

	// Parse header metadata.
	statusVal, agentIDVal, durationVal := "", "", ""
	for line := range strings.SplitSeq(header, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.TrimSpace(parts[0]) {
		case "status":
			statusVal = strings.TrimSpace(parts[1])
		case "agent_id":
			agentIDVal = strings.TrimSpace(parts[1])
		case "duration":
			durationVal = strings.TrimSpace(parts[1])
		}
	}
	if agentIDVal == "" {
		return nil
	}

	// Parse NDJSON records for action tree and last message.
	var toolCalls []string
	var lastMsg strings.Builder
	var lastMsgAppend bool
	for line := range strings.SplitSeq(rest, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var rec outputRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "tool_call":
			if rec.Name != "" {
				toolCalls = append(toolCalls, briefToolCall(rec.Name, rec.Params))
			}
		case "content", "final":
			if rec.Content == "" {
				continue
			}
			if rec.Append && lastMsgAppend {
				lastMsg.WriteString(rec.Content)
			} else {
				lastMsg.Reset()
				lastMsg.WriteString(rec.Content)
				lastMsgAppend = rec.Append
			}
		}
	}

	// Status icon.
	statusIcon := "⏿"
	switch statusVal {
	case "completed":
		statusIcon = "✓"
	case "failed":
		statusIcon = "✗"
	case "cancelled":
		statusIcon = "⊘"
	case "running":
		statusIcon = "⟳"
	}

	var result []string

	// Header line: "⎿ ✓ <agentID>  <duration>"
	meta := agentIDVal
	if durationVal != "" {
		meta += "  " + durationVal
	}
	headerLine := "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset +
		" " + statusIcon + " " + shared.AnsiFgDim + meta + shared.AnsiReset
	result = append(result, shared.ReapplyBackground(headerLine, bgColor))

	// Action tree.
	if len(toolCalls) > 0 {
		label := "      " + shared.AnsiFgMuted + i18n.T("toolrender.subagent.tools") + shared.AnsiReset
		result = append(result, shared.ReapplyBackground(label, bgColor))
		for i, tc := range toolCalls {
			connector := "├─"
			if i == len(toolCalls)-1 {
				connector = "└─"
			}
			line := "      " + shared.AnsiFgMuted + connector + shared.AnsiReset + " " + tc
			result = append(result, shared.ReapplyBackground(line, bgColor))
		}
	}

	// Last message (capped at 6 lines).
	if msg := strings.TrimSpace(lastMsg.String()); msg != "" {
		label := "      " + shared.AnsiFgMuted + i18n.T("toolrender.subagent.last_message") + shared.AnsiReset
		result = append(result, shared.ReapplyBackground(label, bgColor))
		msgLines := strings.Split(msg, "\n")
		const maxMsgLines = 6
		if len(msgLines) > maxMsgLines {
			msgLines = msgLines[len(msgLines)-maxMsgLines:]
		}
		contentWidth := max(width-10, 20)
		for _, ml := range msgLines {
			ml = strings.TrimSpace(ml)
			// Width-aware truncation; see renderSubagentLaunchStub.
			ml = shared.ExpandTabsANSI(ml)
			if shared.PrintableWidth(ml) > contentWidth {
				ml = shared.TruncateANSI(ml, contentWidth, "...")
			}
			if ml == "" {
				continue
			}
			result = append(result, shared.ReapplyBackground("        "+ml, bgColor))
		}
	}

	return result
}

// briefToolCall returns a human-readable one-liner for a tool call.
func briefToolCall(name string, rawParams json.RawMessage) string {
	if len(rawParams) == 0 {
		return name
	}
	var params map[string]any
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return name
	}
	// Common primary-param extraction by tool family.
	switch name {
	case "Read", "Write", "Edit", "EditLegacy", "NotebookRead", "NotebookEdit":
		if v, _ := params["file_path"].(string); v != "" {
			return name + " " + truncatePath(v, 40)
		}
	case "Bash", "BashOutput":
		if v, _ := params["command"].(string); v != "" {
			return name + ": " + brief(v, 50)
		}
	case "Grep":
		if v, _ := params["pattern"].(string); v != "" {
			return name + ": " + brief(v, 50)
		}
	case "Glob", "LS":
		if v, _ := params["pattern"].(string); v != "" {
			return name + " " + brief(v, 50)
		}
		if v, _ := params["path"].(string); v != "" {
			return name + " " + brief(v, 50)
		}
	case "WebFetch", "WebSearch":
		if v, _ := params["url"].(string); v != "" {
			return name + ": " + brief(v, 50)
		}
		if v, _ := params["query"].(string); v != "" {
			return name + ": " + brief(v, 50)
		}
	case "Task", "Subagent", "BackgroundTask":
		if v, _ := params["task"].(string); v != "" {
			return name + ": " + brief(v, 50)
		}
	}
	return name
}

// brief collapses a tool parameter to one line and caps it at maxWidth display
// COLUMNS. The call sites above used to do `v[:47] + "..."`, a BYTE slice
// compared against a column budget: a CJK or emoji parameter was cut in the
// middle of a UTF-8 sequence and the invalid bytes reached the frame, and the
// untruncated cases (Grep/Glob/WebFetch) had no bound at all. This is only a
// work bound; Render applies the real viewport bound.
func brief(v string, maxWidth int) string {
	v = strings.Join(strings.Fields(v), " ")
	if shared.PrintableWidth(v) > maxWidth {
		v = shared.TruncateANSI(v, maxWidth, "...")
	}
	return v
}

// truncatePath shortens a file path to fit within maxLen display COLUMNS.
//
// Both bounds here used to count bytes. `path[len(path)-maxLen+3:]` indexed
// from the end in bytes, so for any non-ASCII path it started in the middle of
// a rune and emitted invalid UTF-8; and a filename longer than maxLen was
// returned whole, so a 150-rune CJK basename escaped the bound entirely.
func truncatePath(path string, maxLen int) string {
	if shared.PrintableWidth(path) <= maxLen {
		return path
	}
	// Keep the filename and as much of the path as fits.
	parts := strings.Split(path, "/")
	filename := parts[len(parts)-1]
	if len(parts) <= 1 {
		return shared.TruncateANSI(path, maxLen, "...")
	}
	return ".../" + shared.TruncateANSI(filename, max(maxLen-4, 1), "…")
}

// renderError renders an error message in red with text wrapping.
func (r *SubagentRenderer) renderError(errMsg string, width int, bgColor string) []string {
	var result []string
	contentWidth := max(width-14, 20)

	wrapped := shared.WrapText(errMsg, contentWidth)
	for i, line := range wrapped {
		var rendered string
		if i == 0 {
			rendered = "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + shared.AnsiFgRed + i18n.T("toolrender.subagent.error_prefix") + line + shared.AnsiReset
		} else {
			rendered = "      " + shared.AnsiFgRed + line + shared.AnsiReset
		}
		result = append(result, shared.ReapplyBackground(rendered, bgColor))
	}

	return result
}

// renderSubagentOutputStructured handles the structured output from SubagentOutput tool.
func (r *SubagentRenderer) renderSubagentOutputStructured(output string, width int, bgColor string) []string {
	lines := strings.Split(output, "\n")
	var result []string

	inContent := false
	for _, line := range lines {
		// Header lines (agent_id, status, etc.)
		if strings.Contains(line, ":") && !inContent {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				rendered := fmt.Sprintf("    %s%s:%s %s%s%s",
					shared.AnsiFgDim, key, shared.AnsiReset,
					shared.AnsiFgBlue, value, shared.AnsiReset)
				result = append(result, shared.ReapplyBackground(rendered, bgColor))
			} else {
				rendered := "    " + line
				result = append(result, shared.ReapplyBackground(rendered, bgColor))
			}
		} else if strings.HasPrefix(line, "───") {
			// Separator line
			rendered := "    " + shared.AnsiFgMuted + line + shared.AnsiReset
			result = append(result, shared.ReapplyBackground(rendered, bgColor))
			inContent = true
		} else if inContent {
			// Content area - check if it's JSON
			remaining := strings.Join(lines[len(result):], "\n")
			if r.looksLikeJSON(remaining) {
				jsonLines := r.renderJSON(remaining, width-4, bgColor)
				if jsonLines != nil {
					for _, jl := range jsonLines {
						result = append(result, jl)
					}
					break
				}
			}
			// Otherwise render as plain text
			rendered := "    " + line
			result = append(result, shared.ReapplyBackground(rendered, bgColor))
		} else {
			rendered := "    " + line
			result = append(result, shared.ReapplyBackground(rendered, bgColor))
		}
	}

	return result
}

// looksLikeJSON checks if the output appears to be JSON.
func (r *SubagentRenderer) looksLikeJSON(output string) bool {
	trimmed := strings.TrimSpace(output)
	return (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]"))
}

// renderJSON attempts to parse and pretty-print JSON.
func (r *SubagentRenderer) renderJSON(output string, width int, bgColor string) []string {
	var obj any
	if err := json.Unmarshal([]byte(output), &obj); err != nil {
		return nil
	}

	// Pretty-print with indentation
	pretty, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return nil
	}

	lines := strings.Split(string(pretty), "\n")
	var result []string

	// Add syntax highlighting for JSON
	for i, line := range lines {
		// Simple syntax highlighting
		line = r.highlightJSON(line)
		// Standard indentation: first line gets connector, rest get continuation indent
		var rendered string
		if i == 0 {
			rendered = "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + line
		} else {
			rendered = "      " + line
		}
		result = append(result, shared.ReapplyBackground(rendered, bgColor))
	}

	return result
}

// highlightJSON adds ANSI color codes for JSON syntax highlighting.
func (r *SubagentRenderer) highlightJSON(line string) string {
	// Detect and color JSON keys (quoted strings followed by colon)
	if idx := strings.Index(line, `":`); idx > 0 {
		// Find the opening quote
		startIdx := strings.LastIndex(line[:idx], `"`)
		if startIdx >= 0 {
			key := line[startIdx : idx+1]
			before := line[:startIdx]
			after := line[idx+1:]
			line = before + shared.AnsiFgGreen + key + shared.AnsiReset + after
		}
	}

	// Color string values
	line = r.colorJSONStrings(line)

	// Color numbers
	line = r.colorJSONNumbers(line)

	// Color booleans and null
	line = strings.ReplaceAll(line, "true", shared.AnsiFgYellow+"true"+shared.AnsiReset)
	line = strings.ReplaceAll(line, "false", shared.AnsiFgYellow+"false"+shared.AnsiReset)
	line = strings.ReplaceAll(line, "null", shared.AnsiFgMuted+"null"+shared.AnsiReset)

	return line
}

// colorJSONStrings highlights string values in JSON.
func (r *SubagentRenderer) colorJSONStrings(line string) string {
	// Skip if line has a key (already colored)
	if strings.Contains(line, `":`) {
		// Only color the value part
		parts := strings.SplitN(line, `":`, 2)
		if len(parts) == 2 {
			value := parts[1]
			if strings.TrimSpace(value) != "" && strings.Contains(value, `"`) {
				// Simple approach: color quoted strings in the value part
				value = strings.ReplaceAll(value, `"`, shared.AnsiFgString+`"`+shared.AnsiReset)
				line = parts[0] + `":` + value
			}
		}
	}
	return line
}

// colorJSONNumbers highlights numeric values.
func (r *SubagentRenderer) colorJSONNumbers(line string) string {
	// This is simplified - a full implementation would use regex
	words := strings.Fields(line)
	for i, word := range words {
		// Remove trailing comma
		num := strings.TrimSuffix(word, ",")
		// Check if it's a number
		if r.isNumber(num) {
			words[i] = shared.AnsiFgNumber + num + shared.AnsiReset + strings.TrimPrefix(word, num)
		}
	}
	return strings.Join(words, " ")
}

// isNumber checks if a string is a numeric value.
func (r *SubagentRenderer) isNumber(s string) bool {
	if s == "" {
		return false
	}
	for i, ch := range s {
		if !((ch >= '0' && ch <= '9') || ch == '.' || ch == '-' || ch == '+') {
			return false
		}
		if (ch == '-' || ch == '+') && i != 0 {
			return false
		}
	}
	return true
}

// looksLikeTOON checks if output appears to be in TOON format.
func (r *SubagentRenderer) looksLikeTOON(output string) bool {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return false
	}

	// TOON format typically has structured output with headers
	firstLine := lines[0]
	return strings.Contains(firstLine, "│") || strings.Contains(firstLine, "├") ||
		strings.Contains(firstLine, "└") || strings.Contains(firstLine, "─")
}

// renderTOON renders TOON-formatted output with colors preserved.
func (r *SubagentRenderer) renderTOON(output string, width int, bgColor string) []string {
	lines := strings.Split(output, "\n")
	var result []string

	for i, line := range lines {
		// TOON already has formatting, just ensure it fits width
		// This used to be `line[:width-11]`, which panics with a negative index
		// for any width below 11 and slices bytes rather than columns. Both the
		// bound and the cut must be width-aware.
		displayLine := shared.ExpandTabsANSI(line)
		if limit := max(width-8, 1); shared.PrintableWidth(displayLine) > limit {
			displayLine = shared.TruncateANSI(displayLine, limit, "...")
		}
		// Standard indentation: first line gets connector, rest get continuation indent
		var rendered string
		if i == 0 {
			rendered = "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + displayLine
		} else {
			rendered = "      " + displayLine
		}
		result = append(result, shared.ReapplyBackground(rendered, bgColor))
	}

	return result
}

// renderPlainOutput renders output as plain text with basic formatting.
func (r *SubagentRenderer) renderPlainOutput(output string, width int, bgColor string) []string {
	lines := strings.Split(output, "\n")

	// Filter out empty trailing lines
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}

	contentWidth := max(width-14, 20)

	var result []string
	const maxLines = 20 // Show more lines for sub-agents

	for i, line := range lines {
		if i >= maxLines {
			remaining := len(lines) - maxLines
			hint := shared.AnsiFgMuted + i18n.T("toolrender.subagent.more_lines", remaining) + shared.AnsiReset
			result = append(result, shared.ReapplyBackground("      "+hint, bgColor))
			break
		}

		// Wrap long lines
		if shared.PrintableWidth(line) > contentWidth {
			wrapped := shared.WrapText(line, contentWidth)
			for j, wl := range wrapped {
				var rendered string
				if j == 0 && i == 0 {
					// First line of entire output gets connector
					rendered = "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + wl
				} else if j == 0 {
					// First line of subsequent original lines just get indent
					rendered = "      " + wl
				} else {
					// Continuation lines get continuation indent
					rendered = "        " + wl
				}
				result = append(result, shared.ReapplyBackground(rendered, bgColor))
			}
		} else {
			var rendered string
			if i == 0 {
				// First line gets connector
				rendered = "    " + shared.AnsiFgMuted + "⎿" + shared.AnsiReset + " " + line
			} else {
				rendered = "      " + line
			}
			result = append(result, shared.ReapplyBackground(rendered, bgColor))
		}
	}

	return result
}
