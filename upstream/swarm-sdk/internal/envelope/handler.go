package envelope

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Handler is the Polymorphic Handler that processes all provider data.
// Principle 4: One handler adapts behavior via injected schema
type Handler struct {
	mu       sync.RWMutex
	registry *TransformRegistry
	store    *EnvelopeStore

	onEnvelope  func(*Envelope)
	onCanonical func(*CanonicalPayload)
	onError     func(error)

	accumulatedText     string
	accumulatedThinking string
	accumulatedUsage    *CanonicalUsage
	toolCalls           []CanonicalToolCall
}

// NewHandler creates a new polymorphic handler
func NewHandler(sessionID string) *Handler {
	return &Handler{
		registry:         NewTransformRegistry(),
		store:            NewEnvelopeStore(sessionID),
		accumulatedUsage: &CanonicalUsage{},
	}
}

// WithRegistry sets a custom transform registry
func (h *Handler) WithRegistry(r *TransformRegistry) *Handler {
	h.registry = r
	return h
}

// OnEnvelope sets a callback for processed envelopes
func (h *Handler) OnEnvelope(fn func(*Envelope)) *Handler {
	h.onEnvelope = fn
	return h
}

// OnCanonical sets a callback for canonical data
func (h *Handler) OnCanonical(fn func(*CanonicalPayload)) *Handler {
	h.onCanonical = fn
	return h
}

// OnError sets a callback for errors
func (h *Handler) OnError(fn func(error)) *Handler {
	h.onError = fn
	return h
}

// Process is the main entry point
func (h *Handler) Process(ctx context.Context, providerID ProviderID, eventType EventType, rawJSON []byte) (*Envelope, error) {
	env := NewEnvelope(providerID, eventType, rawJSON)

	if err := env.Transform(h.registry); err != nil {
		if h.onError != nil {
			h.onError(err)
		}
		return nil, fmt.Errorf("transform failed: %w", err)
	}

	h.store.Store(env)
	h.updateAccumulated(env.Canonical)

	if h.onEnvelope != nil {
		h.onEnvelope(env)
	}
	if h.onCanonical != nil && env.Canonical != nil {
		h.onCanonical(env.Canonical)
	}

	return env, nil
}

// ProcessSSE processes a Server-Sent Event
func (h *Handler) ProcessSSE(ctx context.Context, providerID ProviderID, sseData string) (*Envelope, error) {
	return h.Process(ctx, providerID, EventTypeSSE, []byte(sseData))
}

// ProcessMessage processes a complete message
func (h *Handler) ProcessMessage(ctx context.Context, providerID ProviderID, rawJSON []byte) (*Envelope, error) {
	return h.Process(ctx, providerID, EventTypeMessage, rawJSON)
}

func (h *Handler) updateAccumulated(c *CanonicalPayload) {
	if c == nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if c.Delta != "" {
		h.accumulatedText += c.Delta
	}
	if c.Content != "" {
		h.accumulatedText = c.Content
	}
	if c.Thinking != "" {
		h.accumulatedThinking += c.Thinking
	}
	if c.Usage != nil {
		h.accumulatedUsage = c.Usage
	}
	h.toolCalls = append(h.toolCalls, c.ToolCalls...)
}

// AccumulatedState represents running state
type AccumulatedState struct {
	Text      string
	Thinking  string
	Usage     *CanonicalUsage
	ToolCalls []CanonicalToolCall
}

// GetAccumulated returns accumulated state
func (h *Handler) Accumulated() AccumulatedState {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return AccumulatedState{
		Text:      h.accumulatedText,
		Thinking:  h.accumulatedThinking,
		Usage:     h.accumulatedUsage,
		ToolCalls: h.toolCalls,
	}
}

// Reset clears accumulated state
func (h *Handler) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.accumulatedText = ""
	h.accumulatedThinking = ""
	h.accumulatedUsage = &CanonicalUsage{}
	h.toolCalls = nil
}

// GetStore returns the envelope store
func (h *Handler) Store() *EnvelopeStore {
	return h.store
}

// GetRegistry returns the transform registry
func (h *Handler) GetRegistry() *TransformRegistry {
	return h.registry
}

// WriteTrace writes complete trace to disk
func (h *Handler) WriteTrace() (string, error) {
	return h.store.WriteNDJSON()
}

// ReconstructRaw reconstructs original provider JSON
func (h *Handler) ReconstructRaw(ids ...string) ([]json.RawMessage, error) {
	return h.store.ReconstructProviderJSON(ids...)
}

// Summary returns processing summary
func (h *Handler) Summary() string {
	return h.store.Summary()
}

// Provider-specific convenience methods

// ProcessAnthropicSSE processes an Anthropic SSE event
func (h *Handler) ProcessAnthropicSSE(ctx context.Context, sseData string) (*Envelope, error) {
	return h.ProcessSSE(ctx, ProviderAnthropic, sseData)
}

// ProcessOpenAISSE processes an OpenAI SSE event
func (h *Handler) ProcessOpenAISSE(ctx context.Context, sseData string) (*Envelope, error) {
	return h.ProcessSSE(ctx, ProviderOpenAI, sseData)
}

// ProcessGeminiSSE processes a Gemini SSE event
func (h *Handler) ProcessGeminiSSE(ctx context.Context, sseData string) (*Envelope, error) {
	return h.ProcessSSE(ctx, ProviderGemini, sseData)
}

// ProcessClaudeCodeEvent processes a Claude Code internal event
func (h *Handler) ProcessClaudeCodeEvent(ctx context.Context, rawJSON []byte) (*Envelope, error) {
	return h.Process(ctx, ProviderClaudeCode, EventTypeMessage, rawJSON)
}

// NewStreamHandler creates a handler for streaming
func NewStreamHandler(sessionID string, onDelta func(string), onDone func(AccumulatedState)) *Handler {
	h := NewHandler(sessionID)

	h.OnCanonical(func(c *CanonicalPayload) {
		if c.Delta != "" && onDelta != nil {
			onDelta(c.Delta)
		}
		if c.Done && onDone != nil {
			onDone(h.Accumulated())
		}
	})

	return h
}

// NewDebugHandler creates a handler that logs everything
func NewDebugHandler(sessionID string) *Handler {
	h := NewHandler(sessionID)

	h.OnEnvelope(func(env *Envelope) {
		fmt.Printf("[%d] %s/%s: %s\n",
			env.Sequence, env.ProviderID, env.EventType,
			string(env.RawJSON[:min(100, len(env.RawJSON))]))
	})

	h.OnCanonical(func(c *CanonicalPayload) {
		if c.Usage != nil {
			fmt.Printf("  Usage: in=%d out=%d cache_create=%d cache_read=%d\n",
				c.Usage.InputTokens, c.Usage.OutputTokens,
				c.Usage.CacheCreationTokens, c.Usage.CacheReadTokens)
		}
	})

	return h
}
