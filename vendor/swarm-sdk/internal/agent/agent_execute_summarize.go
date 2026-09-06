package agent

import (
	"context"
	"fmt"
	"maps"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// extractTextContent extracts the text content from a message.
func (a *Agent) extractTextContent(msg *conversation.Message) string {
	// For now, just return Content field
	// In the future, this might extract from Content blocks
	return msg.Content
}

// doFinalSummarization performs a 1-shot completion call with no tools to let
// the agent summarize its work before being stopped. Called when a limit is
// reached (max turns or token limit) instead of silently cancelling the agent.
func (a *Agent) doFinalSummarization(ctx context.Context, messages []*conversation.Message, reason string, reasonContext string) (string, error) {
	// Build a summarization request with no tools (forces text-only response)
	summarizePrompt := fmt.Sprintf(
		"[SYSTEM NOTICE: You have reached your %s limit. %s "+
			"You MUST now provide a final response to the user. "+
			"Summarize what you have accomplished, any results or findings, "+
			"and clearly list any remaining work that was not completed. "+
			"Do NOT request any tool calls - provide your final answer directly.]",
		reason, reasonContext,
	)

	// Add the system notice as a user message to trigger a response
	summarizeMsg := &conversation.Message{
		Role:      conversation.RoleUser,
		Content:   summarizePrompt,
		Timestamp: time.Now(),
	}
	summaryMessages := make([]*conversation.Message, len(messages), len(messages)+1)
	copy(summaryMessages, messages)
	summaryMessages = append(summaryMessages, summarizeMsg)

	// Build request with NO tools to force a text-only response
	req := provider.ChatRequest{
		Messages:     summaryMessages,
		Model:        a.definition.Model,
		SystemPrompt: a.definition.SystemPrompt,
		Tools:        nil, // No tools - force text response
		Metadata:     make(map[string]any),
	}

	// Apply request context (thinking config, etc.)
	a.mu.RLock()
	if len(a.requestContext) > 0 {
		maps.Copy(req.Metadata, a.requestContext)
	}
	a.mu.RUnlock()

	// Make the summarization call
	a.logger.Info(ctx, "agent.final_summarization",
		observability.F("reason", reason),
		observability.F("agent_id", a.definition.ID),
	)

	var resp *provider.ChatResponse
	var err error

	if a.provider.Capabilities().Streaming {
		resp, err = a.streamChat(ctx, req)
	} else {
		resp, err = a.provider.Chat(ctx, req)
	}

	if err != nil {
		return "", fmt.Errorf("summarization call failed: %w", err)
	}

	if resp == nil || resp.Message == nil {
		return "", fmt.Errorf("summarization returned empty response")
	}

	// Update token tracking for the summarization turn
	if resp.Usage != nil {
		a.mu.Lock()
		a.inputTokens = resp.Usage.InputContextSize()
		a.outputTokens += resp.Usage.Output
		a.mu.Unlock()
	}

	summary := a.extractTextContent(resp.Message)
	if summary == "" {
		return "", fmt.Errorf("summarization returned empty content")
	}

	return summary, nil
}
