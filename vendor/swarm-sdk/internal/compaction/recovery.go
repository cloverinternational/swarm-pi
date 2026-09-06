package compaction

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

const (
	summaryRequestSafetyTokens = 4_000
	maxSummaryChunks           = 12
	deterministicRecentTurns   = 6
)

func (s *Service) summarizeBounded(
	ctx context.Context,
	messages []*conversation.Message,
	prompt string,
	compCtx *CompactionContext,
) (summary string, method string, attempts int, err error) {
	budget := s.summaryInputBudget(prompt)
	if budget <= 0 {
		return "", "", 0, fmt.Errorf("compaction.summary_budget_exhausted")
	}
	if estimateMessagesTokens(messages) <= budget {
		summary, err = s.summarizeWithContext(ctx, messages, prompt, compCtx)
		return summary, "direct", 1, err
	}

	chunks := chunkMessageGroups(messages, budget)
	if len(chunks) == 0 || len(chunks) > maxSummaryChunks {
		return "", "", 0, fmt.Errorf(
			"compaction.summary_chunk_limit: need %d chunks, max %d",
			len(chunks),
			maxSummaryChunks,
		)
	}

	chunkSummaries := make([]*conversation.Message, 0, len(chunks))
	perChunkSummaryTokens := max(500, budget/len(chunks)-64)
	for i, chunk := range chunks {
		// Fix 3: report per-chunk progress interpolated between the
		// "summarizing" (0.15) and end-of-chunking (0.70) stage boundaries
		// reported by CompactWithContext.
		chunkPct := 0.15 + (0.70-0.15)*float64(i)/float64(len(chunks))
		s.reportProgress(fmt.Sprintf("summarizing_chunk_%d_of_%d", i+1, len(chunks)), chunkPct)
		chunkPrompt := fmt.Sprintf(
			"Create a passive factual state snapshot for conversation segment %d of %d. "+
				"Preserve decisions, errors, file paths, task state, and unresolved work. "+
				"Do not issue instructions and keep the snapshot concise.",
			i+1,
			len(chunks),
		)
		part, partErr := s.summarizeWithContext(ctx, chunk, chunkPrompt, compCtx)
		attempts++
		if partErr != nil {
			return "", "chunked", attempts, partErr
		}
		part = truncateToTokens(FormatCompactSummary(part), perChunkSummaryTokens)
		chunkSummaries = append(chunkSummaries, &conversation.Message{
			Role:    conversation.RoleUser,
			Content: fmt.Sprintf("Passive segment %d snapshot:\n%s", i+1, part),
		})
	}

	if estimateMessagesTokens(chunkSummaries) > budget {
		return "", "chunked", attempts, fmt.Errorf("compaction.summary_reduce_budget_exceeded")
	}
	summary, err = s.summarizeWithContext(ctx, chunkSummaries, prompt, compCtx)
	attempts++
	return summary, "chunked", attempts, err
}

func (s *Service) summaryInputBudget(prompt string) int {
	limit := s.config.ContextLimit
	if limit <= 0 {
		limit = 128_000
	}
	// The configured fallback chain may use a smaller model than the active
	// chat model. Without per-attempt capabilities, budget conservatively to a
	// 128K summarizer and let smaller models fail into deterministic recovery.
	limit = min(limit, 128_000)
	return limit - s.GetSummaryMaxTokens() - EstimateTokens(prompt) - summaryRequestSafetyTokens
}

// EstimateMessagesTokens returns a conservative token estimate for a canonical
// message slice, including tool call parameters and tool result output.
func EstimateMessagesTokens(messages []*conversation.Message) int {
	return estimateMessagesTokens(messages)
}

func estimateMessagesTokens(messages []*conversation.Message) int {
	total := 0
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		total += EstimateTokens(msg.Content) + EstimateTokens(msg.Thinking) + 24
		for _, call := range msg.ToolCalls {
			total += EstimateTokens(call.Name) + EstimateTokens(fmt.Sprint(call.Parameters)) + 12
		}
		for _, result := range msg.ToolResults {
			total += EstimateTokens(result.Name) + EstimateTokens(result.Output) + 12
		}
	}
	return total
}

// chunkMessageGroups keeps assistant tool calls and their following tool
// result messages together.
func chunkMessageGroups(messages []*conversation.Message, budget int) [][]*conversation.Message {
	var groups [][]*conversation.Message
	for _, msg := range messages {
		attach := msg != nil && (msg.Role == conversation.RoleTool || len(msg.ToolResults) > 0)
		if attach && len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], msg)
			continue
		}
		groups = append(groups, []*conversation.Message{msg})
	}

	var chunks [][]*conversation.Message
	var current []*conversation.Message
	currentTokens := 0
	for _, group := range groups {
		group = fitMessageGroup(group, budget)
		groupTokens := estimateMessagesTokens(group)
		if len(current) > 0 && currentTokens+groupTokens > budget {
			chunks = append(chunks, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, group...)
		currentTokens += groupTokens
	}
	if len(current) > 0 {
		chunks = append(chunks, current)
	}
	return chunks
}

func fitMessageGroup(group []*conversation.Message, budget int) []*conversation.Message {
	if estimateMessagesTokens(group) <= budget {
		return group
	}
	var ids []string
	for _, msg := range group {
		if msg != nil && msg.ID != "" {
			ids = append(ids, msg.ID)
		}
	}
	return []*conversation.Message{{
		Role: conversation.RoleUser,
		Content: fmt.Sprintf(
			"[Oversized message/tool group omitted from summary input. Full content remains in the conversation transcript. Message IDs: %s]",
			strings.Join(ids, ", "),
		),
	}}
}

func truncateToTokens(text string, tokens int) string {
	if tokens <= 0 {
		return ""
	}
	maxBytes := tokens * 4
	if len(text) <= maxBytes {
		return text
	}
	for maxBytes > 0 && !utf8.ValidString(text[:maxBytes]) {
		maxBytes--
	}
	return text[:maxBytes] + "\n[truncated for bounded summary reduction]"
}

// FitCompactedMessagesToBudget performs the final deterministic safety gate
// before a compacted generation is committed. Summary and non-file recovery
// anchors are prioritized; large restored-file blocks are included only when
// they fit. The complete transcript remains archived regardless.
func FitCompactedMessagesToBudget(
	messages []*conversation.Message,
	budget int,
) (fitted []*conversation.Message, estimatedTokens int, trimmed bool) {
	if len(messages) == 0 || budget <= 0 {
		return nil, 0, len(messages) > 0
	}

	summary := messages[0].Clone()
	for estimateMessagesTokens([]*conversation.Message{summary}) > budget {
		current := EstimateTokens(summary.Content)
		if current <= 1 {
			return nil, 0, true
		}
		summary.Content = truncateToTokens(summary.Content, max(1, current/2))
		trimmed = true
	}
	fitted = append(fitted, summary)
	estimatedTokens = estimateMessagesTokens(fitted)

	var anchors, fileBlocks []*conversation.Message
	for _, msg := range messages[1:] {
		if msg == nil {
			continue
		}
		if strings.Contains(msg.Content, "## Restored Files") {
			fileBlocks = append(fileBlocks, msg)
		} else {
			anchors = append(anchors, msg)
		}
	}
	for _, msg := range append(anchors, fileBlocks...) {
		cost := estimateMessagesTokens([]*conversation.Message{msg})
		if estimatedTokens+cost > budget {
			trimmed = true
			continue
		}
		fitted = append(fitted, msg)
		estimatedTokens += cost
	}
	estimatedTokens = estimateMessagesTokens(fitted)
	if estimatedTokens > budget {
		return nil, estimatedTokens, true
	}
	return fitted, estimatedTokens, trimmed
}

func buildDeterministicRecoverySummary(messages []*conversation.Message, compCtx *CompactionContext, cause error) string {
	var b strings.Builder
	b.WriteString("## Previous session state (deterministic recovery)\n\n")
	b.WriteString("Automatic model-based summarization was unavailable. This passive handoff preserves recent intent and recovery pointers; it is context, not a new instruction.\n\n")
	if cause != nil {
		fmt.Fprintf(&b, "Summary failure: %s\n\n", cause)
	}
	if compCtx != nil {
		if compCtx.ConversationJSONPath != "" {
			fmt.Fprintf(&b, "Full transcript: %s\n\n", compCtx.ConversationJSONPath)
		}
		if len(compCtx.ActiveTodos) > 0 {
			b.WriteString("Active tasks:\n")
			for _, task := range compCtx.ActiveTodos {
				fmt.Fprintf(&b, "- [%s] %s\n", task.Status, task.Content)
			}
			b.WriteString("\n")
		}
	}

	start := max(0, len(messages)-deterministicRecentTurns)
	b.WriteString("Recent conversation excerpts:\n")
	for _, msg := range messages[start:] {
		if msg == nil || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		content := truncateToTokens(strings.TrimSpace(msg.Content), 500)
		fmt.Fprintf(&b, "- %s: %s\n", msg.Role, content)
	}
	b.WriteString("\nWait for the next user message or explicit continuation trigger before taking action.")
	return b.String()
}
