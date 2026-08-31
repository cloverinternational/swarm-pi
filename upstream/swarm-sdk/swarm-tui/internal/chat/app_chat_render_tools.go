package chat

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// toolResultFailed normalizes the two failure signals used by tool results:
// an explicit renderer error and a non-zero process exit code in metadata.
func toolResultFailed(tr *ToolResultDisplay) bool {
	if tr == nil {
		return false
	}
	if tr.Error != "" {
		return true
	}
	if tr.Metadata == nil {
		return false
	}
	raw, ok := tr.Metadata["exit_code"]
	if !ok {
		return false
	}
	exitCode, err := strconv.Atoi(fmt.Sprint(raw))
	if err == nil {
		return exitCode != 0
	}
	if value, ok := raw.(float64); ok {
		return int(value) != 0
	}
	return false
}

// app_chat_render_tools.go
// Tool rendering: tool results, images, bash detection, system and tool messages

type renderedImageBlock struct {
	Lines  []string
	Images []nativeImageAnchor
}

// isBashToolName returns true if the tool name identifies a bash/shell tool.
// These tools render errors inside their terminal box rather than using uniform error rendering.
func isBashToolName(name string) bool {
	switch name {
	case "Bash", "bash", "shell", "computer":
		return true
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "mcp") {
		return strings.Contains(lower, "run_command") || strings.Contains(lower, "run-command") ||
			strings.Contains(lower, "execute") || strings.Contains(lower, "terminal") ||
			strings.Contains(lower, "shell") || strings.Contains(lower, "run_script")
	}
	return false
}

// backgroundTaskID returns the background task_id from tool-result metadata when
// the result is marked as backgrounded, or "" otherwise. This mirrors the
// metadata contract set by every backgrounding path in bgprocess/bash_tool.go.
func backgroundTaskID(metadata map[string]any) string {
	if metadata == nil {
		return ""
	}
	isBg := false
	switch v := metadata["background"].(type) {
	case bool:
		isBg = v
	case string:
		isBg = v == "true"
	}
	if !isBg {
		return ""
	}
	if id, ok := metadata["task_id"].(string); ok {
		return id
	}
	return ""
}

// messageHasBackgroundResult reports whether a message contains any backgrounded
// bash tool result (used to target live-terminal cache invalidation).
func messageHasBackgroundResult(msg *Message) bool {
	if msg == nil {
		return false
	}
	for i := range msg.ToolResults {
		if backgroundTaskID(msg.ToolResults[i].Metadata) != "" {
			return true
		}
	}
	for _, blk := range msg.OrderedBlocks {
		if blk.ToolResult != nil && backgroundTaskID(blk.ToolResult.Metadata) != "" {
			return true
		}
	}
	return false
}

// appendBackgroundTaskIDs collects every backgrounded bash task ID in a message.
func appendBackgroundTaskIDs(dst []string, msg *Message) []string {
	if msg == nil {
		return dst
	}
	for i := range msg.ToolResults {
		if id := backgroundTaskID(msg.ToolResults[i].Metadata); id != "" {
			dst = append(dst, id)
		}
	}
	for _, blk := range msg.OrderedBlocks {
		if blk.ToolResult != nil {
			if id := backgroundTaskID(blk.ToolResult.Metadata); id != "" {
				dst = append(dst, id)
			}
		}
	}
	return dst
}

// liveTerminalHeartbeat bounds how long a live terminal block can go without a
// redraw while its process produces no output. Elapsed-time readouts only need
// about this cadence; new output is picked up immediately regardless.
const liveTerminalHeartbeat = time.Second

// refreshLiveTerminals invalidates the render cache for messages holding a
// backgrounded bash result whose output actually changed, so the inline
// terminal window redraws with fresh output.
//
// It returns (found, changed):
//   - found   — at least one live terminal block exists
//   - changed — at least one produced new output or changed state since the
//     last call, which is what justifies ticking at the fast cadence
//
// Previously this invalidated EVERY message holding a background result on
// every tick. Because the tick escalates to 10 Hz whenever a live block
// exists, a background process that produces no output — a dev server idling,
// a watcher waiting on a file — still forced a full viewport string rebuild
// ten times a second for as long as it ran. Invalidating on content change
// instead makes an idle process cost nothing beyond a once-per-second
// heartbeat that keeps elapsed timers moving.
// A force=true call redraws every live block unconditionally. Use it for the
// final refresh after the last process exits so blocks freeze showing their
// exit code even if the terminal state was already recorded.
func (a *App) refreshLiveTerminals(force bool) (found bool, changed bool) {
	if a.bgTerminalSigs == nil {
		a.bgTerminalSigs = make(map[string]BackgroundProcessSignature)
	}
	heartbeat := force || time.Since(a.bgTerminalHeartbeatAt) >= liveTerminalHeartbeat

	ids := make([]string, 0, 4)
	for i := range a.messages {
		ids = appendBackgroundTaskIDs(ids[:0], &a.messages[i])
		if len(ids) == 0 {
			continue
		}
		found = true

		msgChanged := false
		for _, id := range ids {
			sig, ok := a.sdk.GetBackgroundProcessSignature(id)
			if !ok {
				// Signature unavailable (process not tracked yet, or the
				// manager is gone): fall back to redrawing so we never freeze
				// a block that is genuinely updating.
				msgChanged = true
				continue
			}
			if prev, seen := a.bgTerminalSigs[id]; !seen || prev != sig {
				a.bgTerminalSigs[id] = sig
				msgChanged = true
			}
		}

		if msgChanged || heartbeat {
			a.invalidateMessageCache(i)
		}
		if msgChanged {
			changed = true
		}
	}

	if heartbeat {
		a.bgTerminalHeartbeatAt = time.Now()
	}
	return found, changed
}

// renderInlineImage renders a user-pasted image attachment for display in a message.
// Returns a slice of styled lines for rendering inline image preview.
func (a *App) renderInlineImage(img ImageAttachment, width int, bgColor string) renderedImageBlock {
	if len(img.Data) == 0 || !strings.HasPrefix(img.MimeType, "image/") {
		return renderedImageBlock{}
	}
	if block, err := a.renderNativeImage(
		img.Data, "", img.MimeType, fmt.Sprintf("user-image:%d", img.ID), width-4,
	); err == nil {
		for i := range block.Images {
			block.Images[i].Column = 2
		}
		return block
	} else {
		block := a.renderImageUnavailable(img.Data, "", img.MimeType, err)
		style := lipgloss.NewStyle().Background(lipgloss.Color(bgColor))
		for i := range block.Lines {
			block.Lines[i] = reapplyBackground(style.Render(block.Lines[i]), bgColor)
		}
		return block
	}
}

// renderImageAttachment renders an inline image from tool attachments.
// Returns nil if no renderable image attachment is found.
func (a *App) renderImageAttachment(tr *ToolResultDisplay, trOutput string, width int) renderedImageBlock {
	for attachmentIndex, att := range tr.Attachments {
		if strings.HasPrefix(att.MimeType, "image/") {
			data := att.Content
			if len(data) == 0 && att.FilePath != "" {
				var err error
				data, err = snapshotImageFile(att.FilePath)
				if err != nil {
					card := a.renderImageUnavailable(nil, att.FileName, att.MimeType, err)
					return prependImageResultHeader(card, trOutput, a.theme.BG)
				}
				tr.Attachments[attachmentIndex].Content = data
				tr.Attachments[attachmentIndex].Size = int64(len(data))
			}
			if len(data) == 0 {
				continue
			}
			if block, err := a.renderNativeImage(
				data,
				att.FileName,
				att.MimeType,
				fmt.Sprintf("tool-image:%s:%d", tr.CallID, attachmentIndex),
				width-10,
			); err == nil {
				headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Background(lipgloss.Color(a.theme.BG))
				lines := []string{"    " + headerStyle.Render("⎿ ") + trOutput}
				for _, line := range block.Lines {
					lines = append(lines, "      "+line)
				}
				for i := range block.Images {
					block.Images[i].Line++
					block.Images[i].Column = 8
				}
				block.Lines = lines
				return block
			} else {
				card := a.renderImageUnavailable(data, att.FileName, att.MimeType, err)
				return prependImageResultHeader(card, trOutput, a.theme.BG)
			}
		}
	}
	return renderedImageBlock{}
}

func snapshotImageFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("image file is unavailable: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot inspect image file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("image source is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxNativeImageBytes {
		return nil, fmt.Errorf("image payload size %d is unsupported", info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(file, maxNativeImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("cannot read image file: %w", err)
	}
	if len(data) > maxNativeImageBytes {
		return nil, fmt.Errorf("image payload size exceeds %d bytes", maxNativeImageBytes)
	}
	return data, nil
}

func prependImageResultHeader(card renderedImageBlock, output, background string) renderedImageBlock {
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Background(lipgloss.Color(background))
	lines := []string{"    " + headerStyle.Render("⎿ ") + output}
	for _, line := range card.Lines {
		lines = append(lines, "      "+line)
	}
	card.Lines = lines
	return card
}

func (a *App) renderToolResultUnified(toolName string, toolResult *ToolResultDisplay, toolParams map[string]any, msg *Message, width int, isActive bool, hasSubAgentActivity bool) renderedImageBlock {
	trOutput := toolResult.GetOutput()

	// Display mode check
	displayMode := a.renderSettings.GetDisplayModeEnum(toolName)
	if displayMode == DisplayHidden {
		return renderedImageBlock{}
	}

	// Task tool with sub-agent activity: show compact summary (special case)
	if toolName == "Task" && toolResult.Error == "" && hasSubAgentActivity {
		successStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.Success)).Background(lipgloss.Color(a.theme.BG))
		resultPreview := extractTaskResult(trOutput)
		if resultPreview != "" {
			return renderedImageBlock{Lines: []string{"  " + successStyle.Render(tr("classic.subagent.completed_preview")) + a.subAgentStyles.Meta.Render(resultPreview)}}
		}
		return renderedImageBlock{Lines: []string{"  " + successStyle.Render(tr("classic.subagent.completed"))}}
	}

	// Error rendering for non-bash tools (bash renders errors inside its terminal box)
	if toolResult.Error != "" && !isBashToolName(toolName) {
		effectiveLevel := CollapseLevelCompact
		if a.showFullToolOutput {
			effectiveLevel = CollapseLevelFull
		}
		errLines, remaining := a.collapseWidget.RenderError(&ToolCallState{CollapseLevel: effectiveLevel}, toolResult.Error, width)
		result := errLines
		if remaining > 0 {
			result = append(result, a.collapseWidget.RenderMoreIndicator(remaining, effectiveLevel))
		}
		return renderedImageBlock{Lines: result}
	}

	// Image content special case: MIME type is authoritative; a temporary or
	// extensionless source path must not suppress a valid image attachment.
	if toolResult.Error == "" && len(toolResult.Attachments) > 0 {
		if imageBlock := a.renderImageAttachment(toolResult, trOutput, width); len(imageBlock.Lines) > 0 {
			return imageBlock
		}
	}

	// Build RenderContext for the registry
	ctx := &toolrender.RenderContext{
		ToolName: toolName,
		Output:   trOutput,
		Error:    toolResult.Error,
		Params:   toolParams,
		Metadata: toolResult.Metadata,
		CallID:   toolResult.CallID,
		Width:    width,
		IsActive: isActive,
		ShowFull: a.showFullToolOutput,
		BgColor:  a.theme.BG,
	}
	for _, att := range toolResult.Attachments {
		ctx.Attachments = append(ctx.Attachments, toolrender.Attachment{
			FilePath: att.FilePath,
			FileName: att.FileName,
			MimeType: att.MimeType,
			Content:  att.Content,
			Size:     att.Size,
		})
	}

	// Look up unified cached result
	var cached toolrender.CachedResult
	if msg != nil && msg.CachedToolResults != nil {
		cached = msg.CachedToolResults[toolResult.CallID]
	}

	// Render via registry (handles caching, content detection, and styled output)
	lines := a.toolRegistry.Render(ctx, cached)

	// Apply line truncation based on render settings.
	// Write tools (AlwaysShowFull), bash tools (self-truncating bordered box),
	// and Ctrl+O (showFullToolOutput) bypass chat-level truncation.
	if !a.showFullToolOutput && a.renderSettings != nil && !isBashToolName(toolName) {
		toolConfig := a.renderSettings.getToolConfigInternal(toolName)
		if !toolConfig.AlwaysShowFull {
			maxLines := toolConfig.MaxLines
			if maxLines <= 0 {
				maxLines = a.renderSettings.DefaultMaxLines
			}
			if len(lines) > maxLines {
				remaining := len(lines) - maxLines
				lines = lines[:maxLines]
				hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(palette.TextMuted)).Background(lipgloss.Color(a.theme.BG)).Italic(true)
				lines = append(lines, "    "+hintStyle.Render(tr("classic.tool.more_lines_expand", remaining)))
			}
		}
	}

	return renderedImageBlock{Lines: lines}
}

func (a *App) processToolResultForRendering(msg *Message, callID, toolName string, params map[string]any, output string, metadata ...map[string]any) {
	// Calculate width for rendering
	width := a.width - 4 // Leave room for margins
	if width < 40 {
		width = 40
	}

	// Build RenderContext for the registry
	ctx := &toolrender.RenderContext{
		ToolName: toolName,
		Output:   output,
		Params:   params,
		CallID:   callID,
		Width:    width,
		BgColor:  a.theme.BG,
	}

	if len(metadata) > 0 && metadata[0] != nil {
		ctx.Metadata = metadata[0]
	}

	// Pre-process via registry (each renderer knows how to cache its own results)
	if result := a.toolRegistry.PreProcess(ctx); result != nil {
		if msg.CachedToolResults == nil {
			msg.CachedToolResults = make(map[string]toolrender.CachedResult)
		}
		msg.CachedToolResults[callID] = result
		logDebug("[ToolRegistry] Pre-processed tool result (tool=%s, callID=%s)", toolName, callID)
	}
}

// sectionAccentColor returns a distinct theme colour per system-message section
// type, so the decomposed prompt is visually separable at a glance.
func (a *App) sectionAccentColor(t SectionType) string {
	th := a.theme
	switch t {
	case SectionBasePrompt:
		return th.Primary
	case SectionSkills:
		return th.Accent
	case SectionContext:
		return th.Info
	case SectionGitContext:
		return th.Success
	case SectionTools:
		return th.Warning
	case SectionMemories:
		return th.Secondary
	case SectionMode:
		return th.Info
	default:
		return th.Primary
	}
}

// renderSystemSectionLines renders one system-message section: a coloured header
// with a character count, then (when expanded) the section's RAW content
// verbatim — wrapped to width but never markdown-parsed or restyled. Shared by
// both system-message render paths so colours and counts stay consistent.
func (a *App) renderSystemSectionLines(section SystemMessageSection, width int) []string {
	if section.IsEmpty {
		return nil
	}
	th := a.theme
	accent := a.sectionAccentColor(section.Type)

	iconStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(accent)).Background(lipgloss.Color(th.BG))
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(accent)).Background(lipgloss.Color(th.BG)).Bold(true)
	countStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted)).Background(lipgloss.Color(th.BG))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextDim)).Background(lipgloss.Color(th.BG))
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Background(lipgloss.Color(th.BG))
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).Background(lipgloss.Color(th.BG))

	count := section.CharCount
	if count == 0 {
		count = len(section.Content)
	}
	header := iconStyle.Render("  "+section.Icon) + " " +
		titleStyle.Render(section.Title) + " " +
		countStyle.Render(tr("classic.tool.chars", commaInt(count)))

	isExpanded := a.showFullToolOutput || !section.Collapsed
	var out []string
	if !isExpanded && section.LineCount > 3 {
		out = append(out, reapplyBackground(header+"  "+hintStyle.Render(tr("classic.common.expand_hint")), th.BG))
		return out
	}

	// Expanded: header then the RAW content, verbatim. We use rawWrap (not
	// wrapText) so internal whitespace and column alignment are preserved and
	// nothing is dropped or collapsed — long lines are hard-wrapped, never
	// truncated. (wrapText runs strings.Fields, which collapses runs of spaces.)
	out = append(out, reapplyBackground(header, th.BG))
	for _, line := range rawWrap(section.Content, width-8) {
		out = append(out, reapplyBackground("    "+bodyStyle.Render(line), th.BG))
	}
	out = append(out, reapplyBackground("", th.BG))
	out = append(out, reapplyBackground("  "+sepStyle.Render(strings.Repeat("─", width-4)), th.BG))
	out = append(out, reapplyBackground("", th.BG))
	return out
}

// rawWrap splits content into display lines while preserving every character:
// newlines are kept, internal whitespace (and thus column alignment) is never
// collapsed, and content is never truncated. Lines longer than width wrap at
// the last space at/before the width when there is one (so prose stays
// readable), otherwise they hard-wrap at the rune boundary (so long
// unbreakable tokens — paths, base64, code — are kept in full, just wrapped).
// Unlike wrapText it never runs strings.Fields, so runs of spaces survive.
func rawWrap(content string, width int) []string {
	lines := strings.Split(content, "\n")
	if width <= 0 {
		return lines
	}
	var out []string
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) == 0 {
			out = append(out, "")
			continue
		}
		for len(runes) > width {
			brk := -1
			for i := width; i > 0; i-- {
				if runes[i] == ' ' || runes[i] == '\t' {
					brk = i
					break
				}
			}
			if brk <= 0 {
				brk = width // no break point: hard-wrap, never truncate
			}
			out = append(out, string(runes[:brk]))
			rest := runes[brk:]
			if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
				rest = rest[1:] // consume the single space we broke on
			}
			runes = rest
		}
		out = append(out, string(runes))
	}
	return out
}

// commaInt formats an integer with thousands separators (12345 -> "12,345").
func commaInt(n int) string {
	if n < 0 {
		return "-" + commaInt(-n)
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
