package compaction

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// SummarizeRequest is the structured input to SummarizeWithProvider when a
// caller wants visibility into the request body before it is dispatched.
// Returned via the optional SummarizeHook so loggers can capture the exact
// payload (e.g. for ~/.swarm/logs/compaction.log) without re-marshaling.
type SummarizeRequest struct {
	// Provider is the provider name as supplied by the caller (e.g. "anthropic").
	// May be empty if the caller used a default provider.
	Provider string

	// Model is the model name used for summarization.
	Model string

	// Messages is the conversation slice being summarized.
	Messages []*conversation.Message

	// Prompt is the compaction prompt prepended to the rendered conversation
	// content (typically CompressionPrompt with optional custom instructions).
	Prompt string

	// ChatRequest is the fully constructed chat request that will be sent to
	// the provider. Useful for callers that want to log it verbatim.
	ChatRequest provider.ChatRequest
}

// SummarizeHook lets callers observe a summarization attempt. All callbacks
// are optional — leave any of them nil to skip that event. Hooks are invoked
// synchronously and must not panic.
type SummarizeHook struct {
	// OnStart fires once just before the provider call is made. The request
	// has been fully constructed at this point.
	OnStart func(req SummarizeRequest)

	// OnSuccess fires after the provider returns a non-empty summary.
	OnSuccess func(summary string)

	// OnError fires when the provider call fails or the summary is empty.
	OnError func(err error)
}

// SummarizeWithProvider invokes the given provider/model to generate a
// compaction summary for the supplied messages.
//
// The chat request uses compaction.SummarizationSystemPrompt as the system
// prompt, caps output at compaction.DefaultSummaryMaxTokens, and renders
// the conversation through BuildCompactionContent before prepending the
// caller-supplied prompt. The byte layout matches what the Swarm TUI
// previously sent so existing prompt templates remain valid.
//
// `logger` is optional; pass nil to skip structured logging.
// `hook` is optional; pass nil to skip lifecycle callbacks. The hook lets a
// caller (e.g. the TUI) attach its own logging or attach a provider.RawLogger
// to the context before this call (see provider.WithRawLogger).
func SummarizeWithProvider(
	ctx context.Context,
	prov provider.Provider,
	model string,
	messages []*conversation.Message,
	prompt string,
	logger observability.Logger,
	hook *SummarizeHook,
) (string, error) {
	if prov == nil {
		return "", fmt.Errorf("compaction.summarize: provider is nil")
	}
	if model == "" {
		return "", fmt.Errorf("compaction.summarize: model is empty")
	}

	conversationContent := BuildCompactionContent(messages)

	maxTokens := DefaultSummaryMaxTokens
	if providerMax := prov.Capabilities().MaxOutputTokens; providerMax > 0 {
		maxTokens = min(maxTokens, providerMax)
	}
	req := provider.ChatRequest{
		Model:        model,
		SystemPrompt: SummarizationSystemPrompt,
		MaxTokens:    &maxTokens,
		Messages: []*conversation.Message{
			{
				Role:    conversation.RoleUser,
				Content: prompt + "\n\n" + conversationContent,
			},
		},
	}

	if hook != nil && hook.OnStart != nil {
		hook.OnStart(SummarizeRequest{
			Model:       model,
			Messages:    messages,
			Prompt:      prompt,
			ChatRequest: req,
		})
	}

	if logger != nil {
		logger.Info(ctx, "compaction.summarize.start",
			observability.F("model", model),
			observability.F("message_count", len(messages)),
			observability.F("content_length", len(conversationContent)),
		)
	}

	resp, err := prov.Chat(ctx, req)
	if err != nil {
		wrapped := fmt.Errorf("compaction.summarize: provider chat failed: %w", err)
		if hook != nil && hook.OnError != nil {
			hook.OnError(wrapped)
		}
		if logger != nil {
			logger.Error(ctx, "compaction.summarize.error",
				observability.F("model", model),
				observability.F("error", err.Error()),
			)
		}
		return "", wrapped
	}
	if resp.Message == nil || resp.Message.Content == "" {
		emptyErr := fmt.Errorf("compaction.summarize: empty response from %s", model)
		if hook != nil && hook.OnError != nil {
			hook.OnError(emptyErr)
		}
		if logger != nil {
			logger.Error(ctx, "compaction.summarize.empty",
				observability.F("model", model),
			)
		}
		return "", emptyErr
	}

	summary := resp.Message.Content
	if hook != nil && hook.OnSuccess != nil {
		hook.OnSuccess(summary)
	}
	if logger != nil {
		logger.Info(ctx, "compaction.summarize.success",
			observability.F("model", model),
			observability.F("summary_length", len(summary)),
		)
	}
	return summary, nil
}
