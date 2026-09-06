package compaction

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// SubAgentCompactionThreshold is the context window percentage at which
// sub-agents automatically trigger compaction. Fixed at 80% to ensure
// sufficient headroom for the compaction response.
const SubAgentCompactionThreshold = 0.80

// ProviderSummarizeFunc creates a SummarizeFunc that uses the given provider
// to generate compaction summaries. This allows agents to perform compaction
// without external dependencies - they use their own provider.
//
// The function signature matches compaction.SummarizeFunc:
//
//	func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error)
func ProviderSummarizeFunc(prov provider.Provider, model string) func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error) {
	return func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error) {
		return SummarizeWithProvider(ctx, prov, model, messages, prompt, nil, nil)
	}
}

// AgentCompactionConfig contains the configuration needed to create
// a self-contained compaction setup for an agent.
type AgentCompactionConfig struct {
	// Provider is the LLM provider to use for summarization
	Provider provider.Provider

	// Model is the model name to use for summarization
	Model string

	// ContextLimit is the maximum context window size in tokens
	ContextLimit int

	// Service optionally reuses an existing long-lived compaction service
	// (e.g. the TUI's shared service) so in-loop agent compaction runs the
	// same pipeline as manual compaction: same summarizer, file-access
	// tracking, progress reporting, and persisted context state. When nil, a
	// self-contained service is created from Provider/Model. When set, the
	// service's SummarizeFunc/ReadFileFunc/ProgressFunc are used as-is and
	// are NOT overwritten here.
	Service *Service

	// PostCompactBudgetTokens optionally caps the post-compaction active
	// context budget (e.g. the user's configured auto-compaction threshold).
	// It can only lower the default safe budget, never raise it.
	PostCompactBudgetTokens int
}

// CompactFuncResult contains the result of CreateCompactFunc.
type CompactFuncResult struct {
	// CompactFunc is the function to set on agent.AutoCompactionConfig.CompactFunc
	// Signature: func(ctx context.Context, messages []*conversation.Message, compCtx *CompactionContext) ([]*conversation.Message, error)
	CompactFunc func(ctx context.Context, messages []*conversation.Message, compCtx *CompactionContext) ([]*conversation.Message, error)

	// Service is the underlying compaction service (for advanced use)
	Service *Service
}

func agentPostCompactBudget(config AgentCompactionConfig) int {
	const (
		maxOutputReservation = 20_000
		compactionMargin     = 13_000
		minimumBudget        = 1_000
	)
	percentageBudget := int(float64(config.ContextLimit) * SubAgentCompactionThreshold)
	maxOutput := 0
	if config.Provider != nil {
		maxOutput = config.Provider.Capabilities().MaxOutputTokens
	}
	safeInputBudget := config.ContextLimit - min(maxOutput, maxOutputReservation) - compactionMargin
	return max(min(percentageBudget, safeInputBudget), minimumBudget)
}

// CreateCompactFunc creates a self-contained compaction function for an agent.
// The returned CompactFunc can be directly assigned to agent.AutoCompactionConfig.CompactFunc.
//
// This enables agents to automatically compact at 80% of their context window
// using their own provider, with no external dependencies.
//
// Usage:
//
//	result := CreateCompactFunc(AgentCompactionConfig{
//	    Provider:     myProvider,
//	    Model:        "claude-3-haiku",
//	    ContextLimit: 200000,
//	})
//	agent.SetAutoCompactionConfig(agent.AutoCompactionConfig{
//	    EnableAutoCompaction:           true,
//	    AutoCompactionThresholdPercent: 0.80,
//	    CompactFunc:                    result.CompactFunc,
//	})
func CreateCompactFunc(config AgentCompactionConfig) (*CompactFuncResult, error) {
	if config.Provider == nil {
		return nil, fmt.Errorf("compaction.provider_required: Provider cannot be nil")
	}
	if config.Model == "" {
		return nil, fmt.Errorf("compaction.model_required: Model cannot be empty")
	}
	if config.ContextLimit <= 0 {
		return nil, fmt.Errorf("compaction.context_limit_required: ContextLimit must be positive")
	}

	// Reuse the caller's long-lived service when provided (shared pipeline);
	// otherwise create a self-contained one from the provider.
	service := config.Service
	if service == nil {
		// Create the summarization function using the provider
		summarizeFunc := ProviderSummarizeFunc(config.Provider, config.Model)

		// Create the compaction service with the summarization function
		compactionConfig := CompactionConfig{
			SummarizeFunc:        summarizeFunc,
			ContextLimit:         config.ContextLimit,
			AutoCompactThreshold: SubAgentCompactionThreshold,
			SummaryMaxTokens:     DefaultSummaryMaxTokens,
			MaxFilesToRecover:    MaxFilesToRecover,
			MaxTokensPerFile:     MaxTokensPerFile,
			MaxTotalFileTokens:   MaxTotalFileTokens,
			ReadFileFunc:         DefaultReadFileFunc(),
		}

		service = NewService(compactionConfig)
	}

	// Create the CompactFunc that the agent will call
	// Signature matches agent.AutoCompactionConfig.CompactFunc
	compactFunc := func(ctx context.Context, messages []*conversation.Message, compCtx *CompactionContext) ([]*conversation.Message, error) {
		// If no compaction context provided, use standard strategy
		if compCtx == nil {
			compCtx = &CompactionContext{
				Strategy: StrategyStandard,
			}
		}

		// Clone messages to avoid mutating the caller's slice on failed compaction
		cloned := make([]*conversation.Message, len(messages))
		copy(cloned, messages)

		// Create a temporary conversation for the compaction service
		conv := &conversation.Conversation{
			ID:        "compaction-temp",
			Messages:  cloned,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		// Perform compaction
		result, err := service.CompactWithContext(ctx, conv, false, compCtx)
		if err != nil {
			return nil, err
		}

		// CompactWithContext produces summary/result metadata. Message assembly is
		// a separate phase (the TUI outer-compaction path does this explicitly),
		// so returning result.CompactedMessages here used to return nil and made
		// every in-agent compaction fail validation with "returned no messages".
		compacted := service.BuildCompactedMessagesWithContext(result, compCtx, cloned...)
		postCompactBudget := agentPostCompactBudget(config)
		if config.PostCompactBudgetTokens > 0 {
			postCompactBudget = min(postCompactBudget, config.PostCompactBudgetTokens)
		}
		compacted, result.CompactedTokens, _ = FitCompactedMessagesToBudget(
			compacted,
			postCompactBudget,
		)
		if len(compacted) == 0 {
			return nil, fmt.Errorf("compaction.active_generation_empty: compacted messages could not fit %d-token budget", postCompactBudget)
		}
		result.CompactedMessages = compacted
		return compacted, nil
	}

	return &CompactFuncResult{
		CompactFunc: compactFunc,
		Service:     service,
	}, nil
}

// BuildCompactionContent converts conversation messages into a flat text
// block suitable for summarization. Each message is rendered as
// "[role]: content" with tool calls inlined as "[Tool: name]" markers and
// tool results inlined (truncated to 500 chars each) as "[Tool Result: …]"
// markers. Empty messages are skipped. The output is prefixed with a
// "Here is the conversation to summarize:" preamble.
//
// This format mirrors what the original Swarm TUI sent to the summarization
// model and is what the prompt template (CompressionPrompt) was tuned
// against. Changing it will alter byte-level request bodies.
func BuildCompactionContent(messages []*conversation.Message) string {
	var b strings.Builder
	b.WriteString("Here is the conversation to summarize:\n\n")
	for _, msg := range messages {
		role := string(msg.Role)
		content := msg.Content
		if len(msg.ToolCalls) > 0 {
			var tools []string
			for _, tc := range msg.ToolCalls {
				tools = append(tools, fmt.Sprintf("[Tool: %s]", tc.Name))
			}
			if content != "" {
				content += "\n" + strings.Join(tools, " ")
			} else {
				content = strings.Join(tools, " ")
			}
		}
		for _, tr := range msg.ToolResults {
			out := tr.Output
			if len(out) > 500 {
				out = out[:500] + "...[truncated]"
			}
			content += fmt.Sprintf("\n[Tool Result: %s]", out)
		}
		if content == "" {
			continue
		}
		b.WriteString(fmt.Sprintf("[%s]: %s\n\n", role, content))
	}
	return b.String()
}
