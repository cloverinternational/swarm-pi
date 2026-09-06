// Package renderer provides message and content rendering for the chat UI.
//
// The renderer package is responsible for converting message state
// into styled strings for display. It delegates to specialized
// sub-renderers for different content types.
package renderer

import (
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// MessageRenderer renders chat messages to strings.
type MessageRenderer struct {
	theme  theme.Theme
	styles *theme.StyleSet
	width  int

	// Sub-renderers for different content types
	markdown     *MarkdownRenderer
	tool         *ToolRenderer
	thinking     *ThinkingRenderer
	hookRenderer *hooks.HookRenderer

	// Display options
	showThinking       bool
	showFullToolOutput bool
	showHooks          bool
}

// NewMessageRenderer creates a message renderer with the given theme and width.
func NewMessageRenderer(th theme.Theme, width int) *MessageRenderer {
	styles := theme.NewStyleSet(th)
	return &MessageRenderer{
		theme:        th,
		styles:       styles,
		width:        width,
		markdown:     NewMarkdownRenderer(th, width),
		tool:         NewToolRenderer(th, width),
		thinking:     NewThinkingRenderer(th, width),
		hookRenderer: hooks.NewHookRenderer(width, false),
	}
}

// SetWidth updates the rendering width.
func (r *MessageRenderer) SetWidth(width int) {
	r.width = width
	r.markdown.SetWidth(width)
	r.tool.SetWidth(width)
	r.thinking.SetWidth(width)
	r.hookRenderer.SetWidth(width)
}

// SetShowThinking controls whether thinking blocks are displayed.
func (r *MessageRenderer) SetShowThinking(show bool) {
	r.showThinking = show
}

// SetShowFullToolOutput controls tool output verbosity.
func (r *MessageRenderer) SetShowFullToolOutput(show bool) {
	r.showFullToolOutput = show
	r.tool.SetShowFullOutput(show)
}

// SetShowHooks controls whether hook executions are displayed.
func (r *MessageRenderer) SetShowHooks(show bool) {
	r.showHooks = show
}

// Render renders a single message to lines.
func (r *MessageRenderer) Render(msg types.Message, focused bool) []string {
	if msg.Role == "user" {
		return r.renderUserMessage(msg, focused)
	}
	return r.renderAssistantMessage(msg, focused)
}

// renderUserMessage renders a user message with a highlighted "You" header
// and full background fill, matching Claude Code's userMessageBackground
// treatment so text stays readable on both light and dark terminals.
//
// Layout:
//
//	You                      ← header: accent fg bold + bg fill
//	 <message content>       ← 2-space indent + bg fill
func (r *MessageRenderer) renderUserMessage(msg types.Message, focused bool) []string {
	var lines []string

	// Pick styles based on focus state
	labelStyle := r.styles.UserLabel
	lineStyle := r.styles.UserMessage
	if focused {
		labelStyle = r.styles.UserLabelHover
		lineStyle = r.styles.UserMessageHover
	}

	// Full usable width (leave 1 col safety margin)
	maxWidth := max(r.width-1, 4)

	// --- Header row ---
	// Bold "You" in accent color on message background, padded to full width.
	// This is the visual "pane" treatment like Claude Code's permission/yolo
	// status bar: a full-width colored block that makes the section pop.
	lines = append(lines, labelStyle.Width(maxWidth).Render(i18n.T("chatui.message.you")))

	// --- Content rows ---
	// Content styles no longer bake in background (see styles.go), so the
	// outer lineStyle background fills through for unstyled spans.
	// Width(maxWidth) ensures each line is padded to full width with bg.
	contentLines := r.markdown.Render(msg.Content)
	for _, line := range contentLines {
		lines = append(lines, lineStyle.Width(maxWidth).Render("  "+line))
	}

	// Empty message fallback (shouldn't happen in practice)
	if len(contentLines) == 0 {
		lines = append(lines, lineStyle.Width(maxWidth).Render(""))
	}

	// --- Attachments ---
	for _, att := range msg.Attachments {
		lines = append(lines, lineStyle.Width(maxWidth).Render("  📎 "+att.FileName))
	}

	return lines
}

// renderAssistantMessage renders an assistant message with ordered blocks and background.
func (r *MessageRenderer) renderAssistantMessage(msg types.Message, focused bool) []string {
	var lines []string

	// Background style for assistant messages
	bgStyle := r.styles.AssistantMessage

	// CONTRACT: Use GetOrderedBlocks() for guaranteed correct order
	blocks := msg.GetOrderedBlocks()

	// The scalar is a fallback for legacy messages. Ordered blocks are the
	// authoritative interleaved trace and may already contain the same thinking.
	hasThinkingBlock := false
	for _, block := range blocks {
		if block.Type == types.BlockThinking {
			hasThinkingBlock = true
			break
		}
	}
	if r.showThinking && msg.Thinking != "" && !hasThinkingBlock {
		thinkingLines := r.thinking.Render(msg.Thinking, false)
		lines = append(lines, thinkingLines...)
	}

	// Track if we've rendered any content for border continuity
	firstContent := true
	border := "▌"
	if focused {
		border = "█"
	}
	borderStyle := r.styles.AssistantBorder

	// Calculate max width for consistent background
	maxWidth := r.width - 2 // Account for border

	for _, block := range blocks {
		switch block.Type {
		case types.BlockThinking:
			if r.showThinking {
				thinkingLines := r.thinking.Render(block.Content, true)
				lines = append(lines, thinkingLines...)
			}

		case types.BlockContent:
			contentLines := r.markdown.Render(block.Content)
			for i, line := range contentLines {
				// Pad line for consistent background
				paddedLine := line
				lineLen := len([]rune(stripANSI(line)))
				if lineLen < maxWidth {
					paddedLine = line + strings.Repeat(" ", maxWidth-lineLen)
				}

				// Apply text color and background
				styledLine := bgStyle.Render(r.styles.Content.Render(paddedLine))
				if firstContent && i == 0 {
					lines = append(lines, borderStyle.Render(border)+" "+styledLine)
					firstContent = false
				} else {
					lines = append(lines, borderStyle.Render(border)+" "+styledLine)
				}
			}

		case types.BlockToolCall:
			if block.ToolCall != nil {
				toolLines := r.tool.RenderCall(block.ToolCall)
				lines = append(lines, toolLines...)
			}

		case types.BlockToolResult:
			if block.ToolResult != nil {
				resultLines := r.tool.RenderResult(block.ToolResult)
				lines = append(lines, resultLines...)
			}

		case types.BlockHook:
			// Skip hook rendering if showHooks is disabled
			if !r.showHooks {
				continue
			}
			if block.HookExecution != nil {
				hookLines := r.renderHookExecution(block.HookExecution)
				lines = append(lines, hookLines...)
			}

		case types.BlockSubAgent:
			if block.SubAgentActivity != nil {
				subLines := r.renderSubAgentActivity(block.SubAgentActivity)
				lines = append(lines, subLines...)
			}
		}
	}

	// If no blocks but has content, render content directly
	if len(blocks) == 0 && msg.Content != "" {
		contentLines := r.markdown.Render(msg.Content)
		for i, line := range contentLines {
			// Pad line for consistent background
			paddedLine := line
			lineLen := len([]rune(stripANSI(line)))
			if lineLen < maxWidth {
				paddedLine = line + strings.Repeat(" ", maxWidth-lineLen)
			}

			// Apply text color and background
			styledLine := bgStyle.Render(r.styles.Content.Render(paddedLine))
			if i == 0 {
				lines = append(lines, borderStyle.Render(border)+" "+styledLine)
			} else {
				lines = append(lines, borderStyle.Render(border)+" "+styledLine)
			}
		}
	}

	// Add completion info if complete
	if msg.IsComplete && msg.ElapsedTime > 0 {
		elapsed := formatDuration(msg.ElapsedTime)
		infoLine := r.styles.ContentMuted.Render("  ✓ " + elapsed)
		if msg.Model != "" {
			infoLine += r.styles.ContentMuted.Render(" • " + msg.Model)
		}
		lines = append(lines, infoLine)
	}

	return lines
}

// renderHookExecution renders a hook execution block.
func (r *MessageRenderer) renderHookExecution(hook *types.HookExecutionDisplay) []string {
	var lines []string

	// Header with phase and status
	var statusIcon string

	if hook.Blocked {
		statusIcon = "⛔"
	} else if hook.Success {
		statusIcon = "✓"
	} else {
		statusIcon = "✗"
	}

	header := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeBranch+"─ ") +
		r.styles.ToolName.Render(i18n.T("chatui.message.hook", hook.HookName)) +
		r.styles.ContentDim.Render(" ("+hook.Phase+") ") +
		statusIcon

	lines = append(lines, header)

	// ALWAYS show output for blocked hooks (these are critical enforcement messages)
	// Also always show for specific important hooks regardless of showFullToolOutput setting
	importantHooks := map[string]bool{
		"task-enforcement-hook":      true,
		"post-acting-hook":           true,
		"verification-protocol-hook": true,
		"session-start-hook":         true,
		"steering-pretool":           true, // Show steering decisions in TUI
	}

	shouldShowOutput := hook.Blocked ||
		importantHooks[hook.HookName] ||
		r.showFullToolOutput

	// Show output if present and conditions met
	if hook.Output != "" && shouldShowOutput {
		outputLines := strings.SplitSeq(hook.Output, "\n")
		for ol := range outputLines {
			lines = append(lines, r.styles.ContentDim.Render("  "+theme.BorderChars.TreeVertical+"  "+ol))
		}
	}

	// Show error if present
	if hook.Error != "" {
		lines = append(lines, r.styles.ToolError.Render("  "+theme.BorderChars.TreeCorner+"─ "+i18n.T("chatui.message.error", hook.Error)))
	}

	return lines
}

// maxSubAgentDepth caps how deeply nested sub-agent activity blocks are
// rendered. Without a cap, recursively nested sub-sub-...-agents can produce
// runaway indentation and tens of thousands of lines for a deep delegation
// tree. Two levels is the practical visual limit on a terminal.
const maxSubAgentDepth = 2

// renderSubAgentActivity renders sub-agent activity with enriched data.
// Top-level callers should use depth=0. Recursive calls increment by 1.
func (r *MessageRenderer) renderSubAgentActivity(sub *types.SubAgentDisplay) []string {
	return r.renderSubAgentActivityDepth(sub, 0)
}

// renderSubAgentCompletionSummary renders the one-line "Done (N tool uses   M
// tokens   Xs)" summary used when IsComplete is true. Mirrors the format used
// by claude-code's renderToolResultMessage for finished sub-agents.
func (r *MessageRenderer) renderSubAgentCompletionSummary(sub *types.SubAgentDisplay) string {
	parts := make([]string, 0, 4)
	if sub.ToolUseCount > 0 {
		if sub.ToolUseCount == 1 {
			parts = append(parts, i18n.T("chatui.message.one_tool_use"))
		} else {
			parts = append(parts, i18n.T("chatui.message.tool_uses", sub.ToolUseCount))
		}
	}
	if sub.TurnCount > 0 {
		parts = append(parts, i18n.T("chatui.message.turns", sub.TurnCount))
	}
	if sub.TokenCount > 0 {
		parts = append(parts, i18n.T("chatui.message.tokens", sub.TokenCount))
	}
	if !sub.EndTime.IsZero() && !sub.StartTime.IsZero() {
		parts = append(parts, formatDuration(sub.EndTime.Sub(sub.StartTime)))
	}
	verb := sub.CompletionVerb
	if verb == "" {
		verb = i18n.T("chatui.message.done")
	}
	if len(parts) == 0 {
		return verb
	}
	return fmt.Sprintf("%s (%s)", verb, strings.Join(parts, "   "))
}

func (r *MessageRenderer) renderSubAgentActivityDepth(sub *types.SubAgentDisplay, depth int) []string {
	var lines []string

	// Header with agent name and optional task description
	agentLabel := i18n.T("chatui.message.agent", sub.AgentName)
	if sub.TaskInstruction != "" {
		agentLabel += " — " + sub.TaskInstruction
	}
	header := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeBranch+"─ ") +
		r.styles.ToolName.Render(agentLabel)
	lines = append(lines, header)

	// Completion summary takes precedence over in-flight status when IsComplete.
	if sub.IsComplete {
		summaryLine := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeVertical+"  ") +
			r.styles.ContentMuted.Render(r.renderSubAgentCompletionSummary(sub))
		lines = append(lines, summaryLine)
	} else if !sub.StartTime.IsZero() {
		// In-flight: show elapsed time, with stall detection only when we have
		// no heartbeats AND no events for a meaningful stretch. With the
		// HeartbeatUpdate ticker wired in the SDK, healthy agents will refresh
		// LastHeartbeat every ~5s, so this only trips when wiring is broken.
		elapsed := time.Since(sub.StartTime)
		statusText := formatDuration(elapsed)
		if sub.LastHeartbeat.IsZero() && sub.EventCount == 0 && elapsed > 30*time.Second {
			statusText = i18n.T("chatui.message.no_progress")
		}
		statusLine := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeVertical+"  ") +
			r.styles.ContentMuted.Render(statusText)
		lines = append(lines, statusLine)
	}

	// Render nested blocks with indentation
	for _, block := range sub.Blocks {
		switch block.Type {
		case types.BlockThinking:
			if r.showThinking && block.Content != "" {
				thinkingLines := r.thinking.Render(block.Content, true)
				for _, tl := range thinkingLines {
					lines = append(lines, "    "+tl)
				}
			}
		case types.BlockContent:
			contentLines := r.markdown.Render(block.Content)
			for _, cl := range contentLines {
				lines = append(lines, "    "+cl)
			}
		case types.BlockToolCall:
			if block.ToolCall != nil {
				toolLines := r.tool.RenderCall(block.ToolCall)
				for _, tl := range toolLines {
					lines = append(lines, "    "+tl)
				}
			}
		case types.BlockToolResult:
			if block.ToolResult != nil {
				resultLines := r.tool.RenderResult(block.ToolResult)
				for _, rl := range resultLines {
					lines = append(lines, "    "+rl)
				}
			}
		case types.BlockHook:
			if r.showHooks && block.HookExecution != nil {
				hookLines := r.renderHookExecution(block.HookExecution)
				for _, hl := range hookLines {
					lines = append(lines, "    "+hl)
				}
			}
		case types.BlockSubAgent:
			// Recursively render nested sub-sub-agents up to maxSubAgentDepth.
			// Past the cap, render a one-line collapsed summary so deep trees
			// don't blow up vertical space.
			if block.SubAgentActivity == nil {
				continue
			}
			if depth+1 >= maxSubAgentDepth {
				collapsed := r.styles.ContentDim.Render("  "+theme.BorderChars.TreeBranch+"─ ") +
					r.styles.ContentMuted.Render(i18n.T("chatui.message.nested_agent_collapsed",
						block.SubAgentActivity.AgentName, maxSubAgentDepth))
				lines = append(lines, "    "+collapsed)
				continue
			}
			nestedLines := r.renderSubAgentActivityDepth(block.SubAgentActivity, depth+1)
			for _, nl := range nestedLines {
				lines = append(lines, "    "+nl)
			}
		}
	}

	return lines
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return d.String()
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, mins)
}
