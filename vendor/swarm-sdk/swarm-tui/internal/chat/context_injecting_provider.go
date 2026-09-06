package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/prompttrace"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
)

// contextInjectingProvider wraps a provider to inject real-time context into the system prompt.
type contextInjectingProvider struct {
	base         provider.Provider
	orchestrator *chatcontext.ContextOrchestrator
	capture      *providerContextCaptureStore
}

// NewContextInjectingProvider wraps a provider to inject real-time context into the system prompt.
// This is exported for use by non-TUI entrypoints (for example, the headless IPC server).
func NewContextInjectingProvider(base provider.Provider, orchestrator *chatcontext.ContextOrchestrator) provider.Provider {
	return newContextInjectingProvider(base, orchestrator)
}

func newContextInjectingProvider(base provider.Provider, orchestrator *chatcontext.ContextOrchestrator) provider.Provider {
	return newContextInjectingProviderWithCapture(base, orchestrator, nil)
}

func newContextInjectingProviderWithCapture(
	base provider.Provider,
	orchestrator *chatcontext.ContextOrchestrator,
	capture *providerContextCaptureStore,
) provider.Provider {
	if base == nil || (orchestrator == nil && capture == nil) {
		return base
	}
	return &contextInjectingProvider{
		base:         base,
		orchestrator: orchestrator,
		capture:      capture,
	}
}

func (p *contextInjectingProvider) Name() string {
	return p.base.Name()
}

func (p *contextInjectingProvider) Capabilities() provider.Capabilities {
	return p.base.Capabilities()
}

func (p *contextInjectingProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	updated := p.applyContext(ctx, req)
	candidate := p.capture.begin(updated, p.base.Name(), "chat")
	resp, err := p.base.Chat(ctx, updated)
	if err != nil {
		candidate.finish(nil, contextCallOutcomeError, err)
		return resp, err
	}
	if resp == nil {
		candidate.finish(nil, contextCallOutcomeClosed, nil)
		return nil, nil
	}
	candidate.finish(resp.Usage, contextCallOutcomeDone, nil)
	return resp, nil
}

func (p *contextInjectingProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	updated := p.applyContext(ctx, req)
	candidate := p.capture.begin(updated, p.base.Name(), "stream")
	source, err := p.base.Stream(ctx, updated)
	if err != nil {
		candidate.finish(nil, contextCallOutcomeError, err)
		return nil, err
	}
	if source == nil {
		candidate.finish(nil, contextCallOutcomeClosed, nil)
		return nil, nil
	}
	if candidate == nil {
		return source, nil
	}

	out := make(chan provider.StreamChunk)
	go func() {
		defer close(out)
		var latestUsage *conversation.TokenUsage
		var terminalChunks []provider.StreamChunk
		var terminalErr error
		sawDone := false
		for {
			select {
			case <-ctx.Done():
				candidate.finish(latestUsage, contextCallOutcomeCancelled, ctx.Err())
				return
			case chunk, ok := <-source:
				if !ok {
					outcome := contextCallOutcomeClosed
					if terminalErr != nil {
						outcome = contextCallOutcomeError
					} else if sawDone {
						outcome = contextCallOutcomeDone
					}
					// Finalize from the complete source stream before exposing its
					// terminal suffix. This preserves latest usage/error attribution
					// and guarantees ExecuteMessage's deferred drain sees the call.
					candidate.finish(latestUsage, outcome, terminalErr)
					for _, terminalChunk := range terminalChunks {
						select {
						case out <- terminalChunk:
						case <-ctx.Done():
							return
						}
					}
					return
				}
				if chunk.Usage != nil {
					copyUsage := *chunk.Usage
					latestUsage = &copyUsage
				}
				if chunk.Error != nil {
					terminalErr = chunk.Error
				}
				if chunk.Done {
					sawDone = true
				}
				if len(terminalChunks) > 0 || chunk.Error != nil || chunk.Done {
					terminalChunks = append(terminalChunks, chunk)
					continue
				}
				select {
				case out <- chunk:
				case <-ctx.Done():
					candidate.finish(latestUsage, contextCallOutcomeCancelled, ctx.Err())
					return
				}
			}
		}
	}()
	return out, nil
}

func (p *contextInjectingProvider) LastProviderJSON() json.RawMessage {
	if debugProvider, ok := p.base.(provider.DebugProvider); ok {
		return debugProvider.LastProviderJSON()
	}
	return nil
}

// SetRawEventCallback forwards the raw-event callback to the wrapped provider so
// the Debug Inspector "Raw Events" tab receives events even when the concrete
// provider is wrapped for context injection.
func (p *contextInjectingProvider) SetRawEventCallback(callback provider.RawEventCallback) {
	if capable, ok := p.base.(rawEventCapable); ok {
		capable.SetRawEventCallback(callback)
	}
}

func (p *contextInjectingProvider) applyContext(ctx context.Context, req provider.ChatRequest) provider.ChatRequest {
	if p.orchestrator == nil {
		return req
	}

	runID := ""
	if req.Metadata != nil {
		if value, ok := req.Metadata["context_run_id"].(string); ok {
			runID = value
		}
	}

	block := p.orchestrator.GetContextBlock(ctx, chatcontext.ContextBlockOptions{
		RunID: runID,
		Now:   time.Now,
	})

	cachedBlock, ephemeralBlock := chatcontext.SplitContextBlock(block)
	if isCodexBackedRequest(p.base.Name(), req.Model, p.base) {
		req.Messages = upsertSwarmRuntimeGuidanceSectionOnLatestUserMessage(
			req.Messages,
			"context",
			formatRuntimeContextGuidance(cachedBlock, ephemeralBlock),
		)
		// Codex variant: the cached + ephemeral blocks are injected onto the
		// most recent user message rather than the system prompt. Record one
		// entry capturing the combined size.
		prompttrace.From(ctx).Append("runtime_context_guidance_codex", cachedBlock+ephemeralBlock)
		return req
	}

	req.SystemPrompt = chatcontext.InjectContextBlocks(req.SystemPrompt, cachedBlock, ephemeralBlock)
	// Record the two pieces separately so the banner shows the cached
	// (heavy, stable) block distinctly from the ephemeral (changing) block.
	prompttrace.From(ctx).Append("swarmos_cached_context", cachedBlock)
	prompttrace.From(ctx).Append("swarmos_context_ephemeral", ephemeralBlock)
	return req
}

func formatRuntimeContextGuidance(cachedBlock string, ephemeralBlock string) string {
	parts := make([]string, 0, 2)

	var trimmedCached string = strings.TrimSpace(cachedBlock)
	if trimmedCached != "" {
		parts = append(parts, "<cached_context>\n"+trimmedCached+"\n</cached_context>")
	}

	var trimmedEphemeral string = strings.TrimSpace(ephemeralBlock)
	if trimmedEphemeral != "" {
		parts = append(parts, "<ephemeral_context>\n"+trimmedEphemeral+"\n</ephemeral_context>")
	}

	return strings.Join(parts, "\n\n")
}
