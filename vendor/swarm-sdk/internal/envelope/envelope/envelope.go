// Package envelope re-exports the internal envelope types for use by external
// packages such as swarm-tui. This is a thin public wrapper around
// swarm-sdk/internal/envelope.
package envelope

import (
	"context"

	internal "github.com/Swarm-Code/mono/swarm-sdk/internal/envelope"
)

// Type aliases — all methods and fields are available via the aliases.

// ProviderID identifies the source of the JSON payload.
type ProviderID = internal.ProviderID

// EventType categorizes the type of event.
type EventType = internal.EventType

// Envelope is the Universal Wrapper that standardizes all provider JSON.
type Envelope = internal.Envelope

// CanonicalPayload contains the transformed, provider-agnostic data.
type CanonicalPayload = internal.CanonicalPayload

// CanonicalToolCall represents a single tool invocation in canonical form.
type CanonicalToolCall = internal.CanonicalToolCall

// CanonicalUsage holds unified token-usage metrics.
type CanonicalUsage = internal.CanonicalUsage

// CanonicalError holds a provider-agnostic error description.
type CanonicalError = internal.CanonicalError

// Handler is the high-level SSE/message processing handler.
type Handler = internal.Handler

// AccumulatedState holds the accumulated canonical state across events.
type AccumulatedState = internal.AccumulatedState

// TransformRegistry maps provider IDs to their transform schemas.
type TransformRegistry = internal.TransformRegistry

// TransformSchema describes how to transform a provider's JSON into canonical form.
type TransformSchema = internal.TransformSchema

// FieldMapping maps a provider JSON path to a canonical field name.
type FieldMapping = internal.FieldMapping

// EventHandler maps an event type string to a list of field mappings.
type EventHandler = internal.EventHandler

// TransformFunc is a function that transforms raw JSON into any value.
type TransformFunc = internal.TransformFunc

// EnvelopeStore is a thread-safe in-memory store for envelopes.
type EnvelopeStore = internal.EnvelopeStore

// ProviderID constants.
const (
	ProviderAnthropic  ProviderID = "anthropic"
	ProviderOpenAI     ProviderID = "openai"
	ProviderGemini     ProviderID = "gemini"
	ProviderCodex      ProviderID = "codex"
	ProviderOpenRouter ProviderID = "openrouter"
	ProviderClaudeCode ProviderID = "claude_code"
)

// EventType constants.
const (
	EventTypeSSE         EventType = "sse"
	EventTypeMessage     EventType = "message"
	EventTypeStreamChunk EventType = "stream_chunk"
	EventTypeToolCall    EventType = "tool_call"
	EventTypeToolResult  EventType = "tool_result"
	EventTypeUsage       EventType = "usage"
	EventTypeError       EventType = "error"
)

// NewEnvelope creates a new Envelope with the given provider, event type, and raw JSON.
func NewEnvelope(providerID ProviderID, eventType EventType, rawJSON []byte) *Envelope {
	return internal.NewEnvelope(providerID, eventType, rawJSON)
}

// NewHandler creates a new Handler with the given session ID.
func NewHandler(sessionID string) *Handler {
	return internal.NewHandler(sessionID)
}

// NewTransformRegistry creates a new, empty TransformRegistry.
func NewTransformRegistry() *TransformRegistry {
	return internal.NewTransformRegistry()
}

// NewAnthropicSchema returns the built-in Anthropic transform schema.
func NewAnthropicSchema() *TransformSchema {
	return internal.NewAnthropicSchema()
}

// NewOpenAISchema returns the built-in OpenAI transform schema.
func NewOpenAISchema() *TransformSchema {
	return internal.NewOpenAISchema()
}

// NewGeminiSchema returns the built-in Gemini transform schema.
func NewGeminiSchema() *TransformSchema {
	return internal.NewGeminiSchema()
}

// NewClaudeCodeSchema returns the built-in Claude Code transform schema.
func NewClaudeCodeSchema() *TransformSchema {
	return internal.NewClaudeCodeSchema()
}

// NewStreamHandler creates a Handler wired for streaming with delta and done callbacks.
func NewStreamHandler(sessionID string, onDelta func(string), onDone func(AccumulatedState)) *Handler {
	return internal.NewStreamHandler(sessionID, onDelta, onDone)
}

// NewDebugHandler creates a Handler that logs every envelope to stderr.
func NewDebugHandler(sessionID string) *Handler {
	return internal.NewDebugHandler(sessionID)
}

// NewEnvelopeStore creates a new EnvelopeStore for the given session.
func NewEnvelopeStore(sessionID string) *EnvelopeStore {
	return internal.NewEnvelopeStore(sessionID)
}

// Process is a convenience function that creates a one-shot Handler and processes
// a single raw JSON payload. Prefer (*Handler).Process for streaming use-cases.
func Process(ctx context.Context, sessionID string, providerID ProviderID, eventType EventType, rawJSON []byte) (*Envelope, error) {
	h := internal.NewHandler(sessionID)
	return h.Process(ctx, providerID, eventType, rawJSON)
}
