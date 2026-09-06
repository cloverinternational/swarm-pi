// Package bash provides a flat terminal-style renderer for Bash/shell tool output.
// It renders command output in a clean format without box borders.
package bash

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

// Color palette
const (
	borderColorNormal   = "#555555" // Dim gray borders (idle)
	borderColorActive   = "#4EC9B0" // Green borders (running)
	headerTitleColor    = "#FFFFFF" // Bold white for "Shell" title
	durationColor       = "#888888" // Dim/muted for duration
	promptDollarColor   = "#FFFFFF" // Bold white for $
	promptCmdColor      = "#CCCCCC" // Normal white for command text
	outputTextColor     = "#CCCCCC" // Normal text for output
	exitSuccessColor    = "#4EC9B0" // Green for exit 0
	exitErrorColor      = "#F44747" // Red for non-zero exit
	hintColor           = "#666666" // Dim for hidden lines hint
	cursorColor         = "#4EC9B0" // Green cursor block
	truncationHintColor = "#888888" // Dim for truncation notice
)

// Line limits
const (
	maxOutputLines          = 5 // Compact completed output, matching Codex's screen-line budget
	maxStreamingOutputLines = 8 // Show last N lines when streaming (tail: most recent)
)

// BashRenderer renders Bash/shell tool output in a bordered box.
type BashRenderer struct{}

// New creates a new BashRenderer instance.
func New() *BashRenderer {
	return &BashRenderer{}
}

// CanRender returns true if this renderer can handle the given tool context.
func (r *BashRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	switch name {
	case "Bash", "bash", "shell", "computer":
		return true
	}

	if strings.HasPrefix(name, "mcp_") || strings.HasPrefix(name, "mcp__") {
		lower := strings.ToLower(name)
		return strings.Contains(lower, "run_command") ||
			strings.Contains(lower, "run-command") ||
			strings.Contains(lower, "execute") ||
			strings.Contains(lower, "terminal") ||
			strings.Contains(lower, "shell") ||
			strings.Contains(lower, "run_script")
	}

	return false
}

// Render produces styled output lines forming a flat terminal view.
func (r *BashRenderer) Render(ctx *toolrender.RenderContext, cached toolrender.CachedResult) []string {
	command := ""
	if ctx.Params != nil {
		if cmd, ok := ctx.Params["command"].(string); ok {
			command = cmd
		}
	}

	// Detect backgrounded command output. Metadata is the authoritative
	// signal (set by all backgrounding paths in bgprocess/bash_tool.go);
	// fall back to parsing the JSON body for legacy conversations that were
	// recorded before the metadata was unified. Live output for a running
	// background command is shown in the compact bash dock under the chatbox,
	// not inline here — so this stays a small static status marker.
	output := ctx.Output
	if isBackgroundedMeta(ctx.Metadata) || isBackgroundedOutput(output) {
		taskID := metaTaskID(ctx.Metadata)
		reason := metaString(ctx.Metadata, "background_reason")
		return renderBackgroundedStatus(command, output, taskID, reason, ctx.Width, ctx.BgColor)
	}

	// SANITIZE all output to remove problematic ANSI sequences before rendering
	// This removes:
	// - Incomplete escape sequences (\x1b at EOF, \x1b[38 without 'm')
	// - Non-SGR CSI sequences (cursor movement, erase: \x1b[1A, \x1b[0K, etc.)
	// - OSC sequences (hyperlinks, window title)
	// Preserves all valid SGR color codes
	output = shared.SanitizeANSI(output)

	// Combine output + error
	if ctx.Error != "" && output != "" {
		output = output + "\n" + ctx.Error
	} else if ctx.Error != "" && output == "" {
		output = ctx.Error
	}

	// Also sanitize error in case it has problematic sequences
	if ctx.Error != "" {
		// Error was already included above, but make sure it's sanitized
		// (it will be as part of the combined output sanitization)
	}

	return renderTerminal(command, output, ctx, ctx.Width, ctx.BgColor)
}

// PreProcess returns nil because BashRenderer is stateless.
func (r *BashRenderer) PreProcess(ctx *toolrender.RenderContext) toolrender.CachedResult {
	return nil
}

// ── Main render function ────────────────────────────────────────────────────

// renderTerminal renders command + output in a clean flat format (no box borders).
func renderTerminal(command, output string, ctx *toolrender.RenderContext, width int, bgColor string) []string {
	// The command is rendered once in the tool header. Keep this argument for
	// compatibility with focused renderer tests and legacy callers.
	_ = command
	isRunning := ctx != nil && ctx.IsActive
	showFull := ctx != nil && ctx.ShowFull

	wrap := func(s string) string {
		return shared.ReapplyBackground(s, bgColor)
	}

	outputSty := lipgloss.NewStyle().
		Foreground(lipgloss.Color(outputTextColor)).
		Background(lipgloss.Color(bgColor))
	connectorSty := lipgloss.NewStyle().
		Foreground(lipgloss.Color(hintColor)).
		Background(lipgloss.Color(bgColor))
	hintSty := lipgloss.NewStyle().
		Foreground(lipgloss.Color(hintColor)).
		Background(lipgloss.Color(bgColor)).
		Italic(true)
	runSty := lipgloss.NewStyle().
		Foreground(lipgloss.Color(borderColorActive)).
		Background(lipgloss.Color(bgColor)).
		Italic(true)

	var lines []string

	// ── Running indicator
	if isRunning {
		lines = append(lines, wrap("  "+connectorSty.Render("└")+" "+runSty.Render(i18n.T("toolrender.bash.running"))))
	}

	// ── Output section
	if output != "" {
		outputLines := strings.Split(output, "\n")
		// Trim trailing empty lines
		for len(outputLines) > 0 && strings.TrimSpace(outputLines[len(outputLines)-1]) == "" {
			outputLines = outputLines[:len(outputLines)-1]
		}

		// Wrap before applying the line budget so one very long logical line
		// cannot flood a narrow terminal.
		maxContentW := max(width-6, 1)
		var screenLines []string
		for _, ol := range outputLines {
			ol = expandTabs(ol)
			screenLines = append(screenLines, strings.Split(ansi.Hardwrap(ol, maxContentW, false), "\n")...)
		}
		outputLines = screenLines

		lineLimit := maxOutputLines
		if isRunning {
			lineLimit = maxStreamingOutputLines
		}

		var hiddenLines int
		if !showFull && len(outputLines) > lineLimit {
			hiddenLines = len(outputLines) - lineLimit
			if isRunning {
				// Streaming: tail — show newest lines, hint above
				outputLines = outputLines[len(outputLines)-lineLimit:]
				lines = append(lines, wrap("  "+connectorSty.Render("└")+" "+hintSty.Render(i18n.T("toolrender.bash.lines_hidden", hiddenLines))))
			} else {
				// Completed: preserve both setup and outcome with an explicit,
				// counted omission in the middle.
				headCount := lineLimit / 2
				tailCount := lineLimit - headCount - 1
				hiddenLines = len(outputLines) - headCount - tailCount
				compacted := append([]string{}, outputLines[:headCount]...)
				compacted = append(compacted, fmt.Sprintf("… +%d lines", hiddenLines))
				compacted = append(compacted, outputLines[len(outputLines)-tailCount:]...)
				outputLines = compacted
			}
		}

		// Render a quiet tree-connected result block.
		for i, ol := range outputLines {
			prefix := "    "
			if i == 0 {
				prefix = "  " + connectorSty.Render("└") + " "
			}
			if strings.HasPrefix(ol, "… +") {
				lines = append(lines, wrap(prefix+hintSty.Render(ol)))
			} else {
				lines = append(lines, wrap(prefix+outputSty.Render(ol)))
			}
		}

		// SDK truncation hint (output was saved to disk)
		if ctx != nil && isTruncated(ctx.Metadata) {
			truncPath := getTruncationPath(ctx.Metadata)
			truncSty := lipgloss.NewStyle().
				Foreground(lipgloss.Color(truncationHintColor)).
				Background(lipgloss.Color(bgColor)).
				Italic(true)
			hint := i18n.T("toolrender.bash.output_truncated")
			if truncPath != "" {
				hint += i18n.T("toolrender.bash.full_output", truncPath)
			}
			if ansi.StringWidth(hint) > width-4 {
				hint = string(ansi.Truncate(hint, width-5, "")) + "…"
			}
			lines = append(lines, wrap("    "+truncSty.Render(hint)))
		}
	} else if isRunning {
		// Running with no output yet — indicator already shown above
	}

	// ── Error (only when no output, to avoid duplication)
	if ctx != nil && ctx.Error != "" && output == "" {
		errSty := lipgloss.NewStyle().
			Foreground(lipgloss.Color(exitErrorColor)).
			Background(lipgloss.Color(bgColor))
		lines = append(lines, wrap("    "+errSty.Render(ctx.Error)))
	}

	// ── Exit status is always actionable; keep it visible but subordinate.
	if !isRunning && ctx != nil {
		exitCode := getExitCode(ctx.Metadata)
		if exitCode >= 0 {
			exitClr := exitSuccessColor
			if exitCode != 0 {
				exitClr = exitErrorColor
			}
			exitSty := lipgloss.NewStyle().
				Foreground(lipgloss.Color(exitClr)).
				Background(lipgloss.Color(bgColor))
			prefix := "    "
			if len(lines) == 0 {
				prefix = "  " + connectorSty.Render("└") + " "
			}
			lines = append(lines, wrap(prefix+exitSty.Render(i18n.T("toolrender.bash.exit_code", exitCode))))
		}
	}

	// Final clamp. Several pieces above (the "running" indicator, the exit-code
	// footer, the i18n hints) are fixed-length strings that are longer than a
	// narrow pane no matter what the input was, and every content width here is
	// max(width-N, 1) — a floor, not a cap. Clamping the assembled line is the
	// only place the true left edge and the indent are both accounted for.
	return shared.FitLines(lines, width)
}

// ── Helper functions ────────────────────────────────────────────────────────

// formatDuration formats milliseconds into a human-readable duration string.
func formatDuration(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("0.%ds", int(ms/100))
	}
	secs := ms / 1000
	if secs < 60 {
		return fmt.Sprintf("%.1fs", secs)
	}
	mins := int(secs) / 60
	remainSecs := int(secs) % 60
	return fmt.Sprintf("%dm %ds", mins, remainSecs)
}

// getExitCode extracts exit code from metadata. Returns -1 if not present.
func getExitCode(metadata map[string]any) int {
	if metadata == nil {
		return -1
	}
	ec, ok := metadata["exit_code"]
	if !ok {
		return -1
	}
	switch v := ec.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return -1
}

// isTruncated checks if metadata indicates truncated output.
func isTruncated(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	t, ok := metadata["truncated"]
	if !ok {
		return false
	}
	switch v := t.(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// getTruncationPath extracts the path where full output was saved.
func getTruncationPath(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	for _, key := range []string{"output_path", "truncated_path"} {
		if v, ok := metadata[key]; ok {
			if p, ok := v.(string); ok && p != "" {
				return p
			}
		}
	}
	return ""
}

// isBackgroundedMeta reports whether the tool-result metadata marks this as a
// backgrounded command. This is the authoritative signal set by every
// backgrounding path in bgprocess/bash_tool.go.
func isBackgroundedMeta(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	switch v := metadata["background"].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	}
	return false
}

// metaTaskID extracts the background task_id from metadata, or "" if absent.
func metaTaskID(metadata map[string]any) string {
	return metaString(metadata, "task_id")
}

// metaString reads a string metadata value by key, or "" if absent.
func metaString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if s, ok := metadata[key].(string); ok {
		return s
	}
	return ""
}

// backgroundedPayload mirrors the JSON shape returned by the background bash
// paths. Used to robustly detect + parse legacy output that has no metadata.
type backgroundedPayload struct {
	Backgrounded bool   `json:"backgrounded"`
	Status       string `json:"status"`
	TaskID       string `json:"task_id"`
}

// isBackgroundedOutput detects whether the tool output is a backgrounded
// command JSON response by PARSING it (not substring-sniffing). It accepts
// either an explicit "backgrounded": true flag, or a status+task_id shape.
func isBackgroundedOutput(output string) bool {
	trimmed := strings.TrimSpace(output)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var p backgroundedPayload
	if err := json.Unmarshal([]byte(trimmed), &p); err != nil {
		return false
	}
	if p.Backgrounded {
		return true
	}
	// Status-shaped payloads: "backgrounded" or "running" with a task_id.
	if p.TaskID != "" && (p.Status == "backgrounded" || p.Status == "running") {
		return true
	}
	return false
}

// parseTaskIDFromOutput pulls the task_id out of a backgrounded JSON body.
func parseTaskIDFromOutput(output string) string {
	var p backgroundedPayload
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &p); err == nil {
		return p.TaskID
	}
	return ""
}

// reasonLabel maps a background_reason metadata value to a human label.
func reasonLabel(reason string) string {
	switch reason {
	case "idle":
		return i18n.T("toolrender.bash.reason_idle_timeout")
	case "user":
		return "Ctrl+B"
	case "explicit":
		return i18n.T("toolrender.bash.reason_requested")
	default:
		return ""
	}
}

// renderBackgroundedStatus renders a compact status line for backgrounded commands.
// taskID/reason come from metadata when available; taskID falls back to parsing
// the JSON body for legacy conversations.
func renderBackgroundedStatus(command, output, taskID, reason string, width int, bgColor string) []string {

	wrap := func(s string) string {
		return shared.ReapplyBackground(s, bgColor)
	}

	if taskID == "" {
		taskID = parseTaskIDFromOutput(output)
	}

	bgS := lipgloss.NewStyle().
		Foreground(lipgloss.Color(palette.Warning)).
		Background(lipgloss.Color(bgColor)).
		Bold(true)

	cmdStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(outputTextColor)).
		Background(lipgloss.Color(bgColor))

	idStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(borderColorActive)).
		Background(lipgloss.Color(bgColor))

	cmdTruncated := flattenCommand(command)
	// Width-aware truncation: `cmdTruncated[:maxW-3]` sliced bytes, which both
	// split multi-byte runes (invalid UTF-8 on screen) and mismeasured wide
	// runes. It also panicked in principle for maxW in (0,3].
	if maxW := width - 20; maxW > 0 {
		cmdTruncated = shared.TruncateANSI(shared.ExpandTabsANSI(cmdTruncated), maxW, "...")
	}

	label := i18n.T("toolrender.bash.backgrounded")
	if rl := reasonLabel(reason); rl != "" {
		label = i18n.T("toolrender.bash.backgrounded_reason", rl)
	}

	var lines []string
	statusLine := "  " + bgS.Render(label) + cmdStyle.Render(" "+cmdTruncated)
	lines = append(lines, wrap(statusLine))
	if taskID != "" {
		idLine := "    " + idStyle.Render("task_id: "+taskID)
		lines = append(lines, wrap(idLine))
	}

	// The "Backgrounded (…)" label and the "task_id: " prefix are fixed-length
	// i18n strings that exceed a narrow pane on their own, so the assembled
	// line must still be clamped. See renderTerminal.
	return shared.FitLines(lines, width)
}

// expandTabs replaces tab characters with spaces using 4-wide tab stops.
//
// It delegates to shared.ExpandTabsANSI, which advances the column counter by
// DISPLAY WIDTH rather than by rune count. The previous local implementation
// did col++ per rune, so a tab following CJK text landed on the wrong tab stop
// and every column to its right was sheared by one per wide rune.
func expandTabs(s string) string {
	return shared.ExpandTabsANSI(s)
}

// flattenCommand collapses a multi-line command to a single line.
func flattenCommand(cmd string) string {
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	return strings.Join(strings.Fields(cmd), " ")
}
