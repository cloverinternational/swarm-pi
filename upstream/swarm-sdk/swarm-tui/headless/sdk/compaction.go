package sdk

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

const maxToolResultSummaryChars = 500

// CompactConversation implements core.SDKBridge.CompactConversation.
func (b *Bridge) CompactConversation(ctx context.Context, req core.CompactionRequest) (core.CompactionResult, error) {
	if b.convManager == nil {
		return core.CompactionResult{}, errors.New("compaction requires conversation manager")
	}
	if req.ConvID == "" {
		return core.CompactionResult{}, errors.New("compaction requires conversation id")
	}

	if strings.EqualFold(req.Strategy, "truncate") {
		req.Strategy = "summarize"
	}

	b.mu.RLock()
	providerName := b.currentProvider
	modelName := b.currentModel
	prov := b.providers[providerName]
	b.mu.RUnlock()

	if prov == nil {
		return core.CompactionResult{}, fmt.Errorf("provider not configured: %s", providerName)
	}

	conv, err := b.convManager.Resume(ctx, req.ConvID)
	if err != nil {
		return core.CompactionResult{}, err
	}

	beforeTokens := conv.CurrentContextSize
	if beforeTokens <= 0 {
		beforeTokens = conv.TotalTokens
	}
	if beforeTokens <= 0 {
		beforeTokens = req.InputTokens
	}

	approach := req.Approach
	if approach == "" {
		approach = "direct"
	}

	if approach == "direct" && req.InputTokenBudget > 0 {
		estimated := estimateConversationTokens(conv.Messages)
		if estimated > req.InputTokenBudget {
			return core.CompactionResult{
				Compacted:    false,
				BeforeTokens: beforeTokens,
				Approach:     approach,
			}, errors.New("budget exceeded")
		}
	}

	contextLimit := prov.Capabilities().MaxContextWindow
	if contextLimit <= 0 {
		contextLimit = 200000
	}

	compactionService := compaction.NewService(compaction.CompactionConfig{
		ContextLimit:         contextLimit,
		AutoCompactThreshold: compaction.AutoCompactThreshold,
		MaxFilesToRecover:    compaction.MaxFilesToRecover,
		MaxTokensPerFile:     compaction.MaxTokensPerFile,
		MaxTotalFileTokens:   compaction.MaxTotalFileTokens,
		SummarizeFunc: func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error) {
			return b.generateCompactionSummary(ctx, prov, modelName, messages, prompt)
		},
	})

	var compactedMessages []*conversation.Message
	var afterTokens int

	switch approach {
	case "hierarchical":
		budget := req.ChunkTokenBudget
		if budget <= 0 {
			budget = req.InputTokenBudget
		}
		if budget <= 0 {
			return core.CompactionResult{
				Compacted:    false,
				BeforeTokens: beforeTokens,
				Approach:     approach,
			}, errors.New("budget exceeded")
		}

		summary, preserved, err := b.summarizeHierarchical(ctx, conv.Messages, req.PreserveRecentMessages, budget, prov, modelName)
		if err != nil {
			return core.CompactionResult{
				Compacted:    false,
				BeforeTokens: beforeTokens,
				Approach:     approach,
			}, err
		}

		compactionResult := &compaction.CompactionResult{
			Compacted: true,
			Summary:   compaction.SummaryPrefix + summary,
		}
		compactedMessages = compactionService.BuildCompactedMessages(compactionResult)
		compactedMessages = append(compactedMessages, preserved...)
		afterTokens = countMessageTokens(compactedMessages)

	default:
		// Build CompactionContext from request (Phase 2 - Full Context)
		compCtx := &compaction.CompactionContext{
			Strategy:               compaction.StrategyStandard,
			PreserveSystemMessages: req.PreserveSystemMessages,
			PreserveToolResults:    req.PreserveToolResults,

			// Mode state
			CurrentMode: req.OperatingMode,
			ModeName:    formatModeName(req.OperatingMode),
			ModeHistory: req.ModeHistory,

			// Task state
			ActiveTodos:    convertTodos(req.ActiveTodos),
			CompletedTodos: convertTodos(req.CompletedTodos),

			// File state
			ModifiedFiles: req.ModifiedFiles,
			ReadFiles:     req.ReadFiles,

			// MCP state
			ActiveMCPServers: req.MCPServers,
			RecentMCPTools:   req.MCPTools,

			// Hooks state
			ActiveHooks: req.ActiveHooks,

			// Conversation metadata
			MessageCount: req.MessageCount,
			TokensUsed:   beforeTokens,

			// Auto-compact suppresses follow-up questions (mirrors CC).
			// Manual compact leaves the model free to respond naturally.
			SuppressFollowUpQuestions: req.Mode != "manual",
		}

		// Set start time if conversation age provided
		if req.ConversationAge > 0 {
			compCtx.StartTime = time.Now().Add(-time.Duration(req.ConversationAge) * time.Second)
		}

		compactionResult, err := compactionService.CompactWithContext(ctx, conv, req.Mode == "manual", compCtx)
		if err != nil {
			return core.CompactionResult{
				Compacted:    false,
				BeforeTokens: beforeTokens,
				Approach:     approach,
			}, err
		}
		if compactionResult.Error != nil {
			return core.CompactionResult{
				Compacted:    false,
				BeforeTokens: beforeTokens,
				Approach:     approach,
			}, compactionResult.Error
		}

		compactedMessages = compactionService.BuildCompactedMessagesWithContext(compactionResult, compCtx)
		afterTokens = compactionResult.CompactedTokens
		if afterTokens <= 0 {
			afterTokens = countMessageTokens(compactedMessages)
		}
	}

	newConvID, totalTokens, err := b.createCompactedConversation(ctx, conv, compactedMessages)
	if err != nil {
		return core.CompactionResult{
			Compacted:    false,
			BeforeTokens: beforeTokens,
			Approach:     approach,
		}, err
	}
	if totalTokens > 0 {
		afterTokens = totalTokens
	}

	return core.CompactionResult{
		Compacted:    true,
		BeforeTokens: beforeTokens,
		AfterTokens:  afterTokens,
		Approach:     approach,
		NewConvID:    newConvID,
	}, nil
}

func (b *Bridge) summarizeHierarchical(ctx context.Context, messages []*conversation.Message, preserveRecent, budget int, prov provider.Provider, modelName string) (string, []*conversation.Message, error) {
	if preserveRecent < 0 {
		preserveRecent = 0
	}
	if preserveRecent > len(messages) {
		preserveRecent = len(messages)
	}

	preserved := make([]*conversation.Message, preserveRecent)
	if preserveRecent > 0 {
		copy(preserved, messages[len(messages)-preserveRecent:])
	}
	older := messages[:len(messages)-preserveRecent]

	if len(older) == 0 {
		summary, err := b.generateCompactionSummary(ctx, prov, modelName, preserved, compaction.CompressionPrompt)
		return summary, preserved, err
	}

	chunks := splitMessagesByBudget(older, budget)
	if len(chunks) == 0 {
		return "", nil, errors.New("budget exceeded")
	}

	chunkSummaries := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if estimateConversationTokens(chunk) > budget {
			return "", nil, errors.New("budget exceeded")
		}
		summary, err := b.generateCompactionSummary(ctx, prov, modelName, chunk, compaction.CompressionPrompt)
		if err != nil {
			return "", nil, err
		}
		chunkSummaries = append(chunkSummaries, summary)
	}

	summaryMessages := make([]*conversation.Message, 0, len(chunkSummaries))
	for i, summary := range chunkSummaries {
		content := summary
		if len(chunkSummaries) > 1 {
			content = fmt.Sprintf("Chunk %d summary:\n%s", i+1, summary)
		}
		summaryMessages = append(summaryMessages, &conversation.Message{
			Role:    conversation.RoleAssistant,
			Content: content,
		})
	}

	finalSummary, err := b.generateCompactionSummary(ctx, prov, modelName, summaryMessages, compaction.CompressionPrompt)
	if err != nil {
		return "", nil, err
	}
	return finalSummary, preserved, nil
}

func (b *Bridge) generateCompactionSummary(ctx context.Context, prov provider.Provider, modelName string, messages []*conversation.Message, prompt string) (string, error) {
	if prov == nil {
		return "", errors.New("compaction provider unavailable")
	}

	var conversationContent strings.Builder
	conversationContent.WriteString("Here is the conversation to summarize:\n\n")

	for _, msg := range messages {
		formatted := formatMessageForSummary(msg)
		if formatted == "" {
			continue
		}
		conversationContent.WriteString(formatted)
		conversationContent.WriteString("\n\n")
	}

	req := provider.ChatRequest{
		Model:        modelName,
		SystemPrompt: compaction.SummarizationSystemPrompt,
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: prompt + "\n\n" + conversationContent.String(),
			},
		},
	}

	resp, err := prov.Chat(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Message == nil || resp.Message.Content == "" {
		return "", errors.New("empty summary response")
	}

	return resp.Message.Content, nil
}

func (b *Bridge) createCompactedConversation(ctx context.Context, oldConv *conversation.Conversation, messages []*conversation.Message) (string, int, error) {
	if b.convManager == nil {
		return "", 0, errors.New("conversation manager unavailable")
	}

	mode := "act"
	metadata := &conversation.ConversationMetadata{}
	if oldConv != nil {
		if oldConv.Mode != "" {
			mode = oldConv.Mode
		}
		metadata = cloneConversationMetadata(oldConv.Metadata)
	}

	if metadata.Custom == nil {
		metadata.Custom = make(map[string]any)
	}
	// The clone above carries the old conversation's process-scoped join keys
	// forward; clear them so the compacted conversation is stamped with the
	// current session and HEAD instead of a previous run's. Lineage keys
	// (origin, agent_id, compacted_from) are intentionally preserved.
	conversation.ClearProcessScopedJoinKeys(metadata.Custom)
	if oldConv != nil {
		metadata.Custom["compacted_from"] = oldConv.ID
	}
	metadata.Custom["compacted_at"] = time.Now().Format(time.RFC3339)

	// Preserve workspace path from the old conversation
	workspacePath := ""
	if oldConv != nil {
		workspacePath = oldConv.WorkspacePath
		if workspacePath == "" {
			if wp, ok := oldConv.Metadata.Custom["workspace_path"].(string); ok {
				workspacePath = wp
			}
		}
	}

	conv, err := b.convManager.Create(ctx, manager.CreateOptions{
		Mode:          mode,
		WorkspacePath: workspacePath,
		Metadata:      metadata,
	})
	if err != nil {
		return "", 0, err
	}

	totalTokens := 0
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if err := b.convManager.AddMessage(ctx, conv.ID, msg); err != nil {
			return "", 0, err
		}
		totalTokens += messageTokens(msg)
	}

	if updated, err := b.convManager.Resume(ctx, conv.ID); err == nil && updated != nil {
		updated.TotalTokens = totalTokens
		updated.CurrentContextSize = totalTokens
		_ = b.convManager.Save(ctx, updated)
	}

	if oldConv != nil && oldConv.ID != "" {
		_ = b.convManager.Archive(ctx, oldConv.ID)
	}

	return conv.ID, totalTokens, nil
}

func cloneConversationMetadata(meta conversation.ConversationMetadata) *conversation.ConversationMetadata {
	copied := &conversation.ConversationMetadata{
		UserID:    meta.UserID,
		ProjectID: meta.ProjectID,
	}
	if len(meta.Tags) > 0 {
		copied.Tags = append([]string(nil), meta.Tags...)
	}
	if meta.Custom != nil {
		copied.Custom = make(map[string]any, len(meta.Custom))
		maps.Copy(copied.Custom, meta.Custom)
	}
	return copied
}

func splitMessagesByBudget(messages []*conversation.Message, budget int) [][]*conversation.Message {
	if budget <= 0 {
		return [][]*conversation.Message{messages}
	}

	var chunks [][]*conversation.Message
	var current []*conversation.Message
	currentTokens := 0

	for _, msg := range messages {
		msgTokens := estimateMessageTokens(msg)
		if len(current) > 0 && currentTokens+msgTokens > budget {
			chunks = append(chunks, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, msg)
		currentTokens += msgTokens
	}

	if len(current) > 0 {
		chunks = append(chunks, current)
	}

	return chunks
}

func estimateConversationTokens(messages []*conversation.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

func estimateMessageTokens(msg *conversation.Message) int {
	formatted := formatMessageForSummary(msg)
	if formatted == "" {
		return 0
	}
	return compaction.EstimateTokens(formatted)
}

func countMessageTokens(messages []*conversation.Message) int {
	total := 0
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		total += messageTokens(msg)
	}
	return total
}

func messageTokens(msg *conversation.Message) int {
	if msg.Tokens != nil && msg.Tokens.Total > 0 {
		return msg.Tokens.Total
	}
	return compaction.EstimateTokens(msg.Content)
}

func formatMessageForSummary(msg *conversation.Message) string {
	if msg == nil {
		return ""
	}

	content := msg.Content

	if len(msg.ToolCalls) > 0 {
		toolInfo := make([]string, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if tc.Name != "" {
				toolInfo = append(toolInfo, fmt.Sprintf("[Tool: %s]", tc.Name))
			}
		}
		if len(toolInfo) > 0 {
			if content != "" {
				content += "\n" + strings.Join(toolInfo, " ")
			} else {
				content = strings.Join(toolInfo, " ")
			}
		}
	}

	if len(msg.ToolResults) > 0 {
		for _, tr := range msg.ToolResults {
			output := tr.Output
			if len(output) > maxToolResultSummaryChars {
				output = output[:maxToolResultSummaryChars] + "...[truncated]"
			}
			if output == "" {
				continue
			}
			if content != "" {
				content += "\n"
			}
			content += fmt.Sprintf("[Tool Result: %s]", output)
		}
	}

	if strings.TrimSpace(content) == "" {
		return ""
	}

	return fmt.Sprintf("[%s]: %s", msg.Role, content)
}

// formatModeName converts operating mode ID to human-readable name
func formatModeName(mode string) string {
	switch strings.ToLower(mode) {
	case "plan":
		return "PLAN Mode"
	case "act":
		return "ACT Mode"
	case "auto":
		return "AUTO Mode"
	case "debug":
		return "DEBUG Mode"
	case "off", "":
		return "OFF"
	default:
		return strings.ToUpper(mode) + " Mode"
	}
}

// convertTodos converts core.TodoItem to compaction.Todo
func convertTodos(items []core.TodoItem) []compaction.Todo {
	todos := make([]compaction.Todo, len(items))
	for i, item := range items {
		todos[i] = compaction.Todo{
			Content:    item.Content,
			Status:     item.Status,
			ActiveForm: "", // Not tracked in core.TodoItem yet
		}
	}
	return todos
}
