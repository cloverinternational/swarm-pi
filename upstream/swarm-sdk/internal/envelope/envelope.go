// Package envelope provides the Universal Envelope architecture for handling
// multiple provider JSON formats. Based on the "Trampoline" pattern:
// - Every JSON payload gets wrapped in a standard structure
// - The envelope captures provider_id for routing to correct transformation
// - Original raw JSON is preserved for perfect reconstruction
package envelope

import (
	"encoding/json"
	"time"
)

// ProviderID identifies the source of the JSON payload
type ProviderID string

const (
	ProviderAnthropic  ProviderID = "anthropic"
	ProviderOpenAI     ProviderID = "openai"
	ProviderGemini     ProviderID = "gemini"
	ProviderCodex      ProviderID = "codex"
	ProviderOpenRouter ProviderID = "openrouter"
	ProviderClaudeCode ProviderID = "claude_code" // Claude Code's internal format
)

// EventType categorizes the type of event
type EventType string

const (
	EventTypeSSE         EventType = "sse"          // Server-sent event
	EventTypeMessage     EventType = "message"      // Complete message
	EventTypeStreamChunk EventType = "stream_chunk" // Streaming chunk
	EventTypeToolCall    EventType = "tool_call"    // Tool invocation
	EventTypeToolResult  EventType = "tool_result"  // Tool response
	EventTypeUsage       EventType = "usage"        // Token usage update
	EventTypeError       EventType = "error"        // Error event
)

// Envelope is the Universal Wrapper that standardizes all provider JSON.
// This is the "Trampoline" - every JSON payload enters the system through this structure.
//
// Principle 1: Universal Wrapper
// - Captures provider_id for routing
// - Preserves raw JSON blob for reconstruction
// - Provides canonical fields for unified processing
type Envelope struct {
	// === IDENTITY (The "Address" in trampoline terms) ===
	ID         string     `json:"id"`          // Unique envelope ID
	ProviderID ProviderID `json:"provider_id"` // Which provider sent this
	EventType  EventType  `json:"event_type"`  // Type of event
	Timestamp  time.Time  `json:"timestamp"`   // When received
	Sequence   int        `json:"sequence"`    // Order in stream

	// === RAW STATE (Principle 3: State Preservation) ===
	// Store the EXACT JSON as received - never modified
	RawJSON json.RawMessage `json:"raw_json"`

	// === CANONICAL FIELDS (Transformed data) ===
	Canonical *CanonicalPayload `json:"canonical,omitempty"`

	// === METADATA ===
	SessionID string         `json:"session_id,omitempty"`
	MessageID string         `json:"message_id,omitempty"`
	ParentID  string         `json:"parent_id,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// CanonicalPayload contains the transformed, provider-agnostic data.
// This is what your application logic works with.
type CanonicalPayload struct {
	// Content
	Role     string `json:"role,omitempty"`     // "user", "assistant", "system"
	Content  string `json:"content,omitempty"`  // Text content
	Thinking string `json:"thinking,omitempty"` // Extended thinking (if supported)

	// Tool Calls
	ToolCalls []CanonicalToolCall `json:"tool_calls,omitempty"`

	// Usage (unified token metrics)
	Usage *CanonicalUsage `json:"usage,omitempty"`

	// Stream State
	Delta        string `json:"delta,omitempty"`         // Incremental content
	FinishReason string `json:"finish_reason,omitempty"` // Why generation stopped
	Done         bool   `json:"done,omitempty"`          // Is this the final chunk?

	// Error
	Error *CanonicalError `json:"error,omitempty"`
}

// CanonicalToolCall represents a tool call in canonical format
type CanonicalToolCall struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Input any    `json:"input"`
}

// CanonicalUsage represents token usage in canonical format.
// This unifies all provider-specific token metrics.
type CanonicalUsage struct {
	// Base tokens
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	// Cache metrics (unified from all providers)
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`

	// Cache breakdown by TTL (Anthropic-specific but canonical)
	CacheCreation1hTokens int `json:"cache_creation_1h_tokens,omitempty"`
	CacheCreation5mTokens int `json:"cache_creation_5m_tokens,omitempty"`

	// Thinking tokens (for extended thinking)
	ThinkingInputTokens  int `json:"thinking_input_tokens,omitempty"`
	ThinkingOutputTokens int `json:"thinking_output_tokens,omitempty"`

	// Calculated fields
	TotalInputWithCache int `json:"total_input_with_cache"` // input + cache_read + cache_creation
}

// CanonicalError represents an error in canonical format
type CanonicalError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

// NewEnvelope creates a new envelope wrapping raw JSON from a provider.
// This is the entry point - the "trampoline" that catches all incoming JSON.
func NewEnvelope(providerID ProviderID, eventType EventType, rawJSON []byte) *Envelope {
	return &Envelope{
		ID:         generateID(),
		ProviderID: providerID,
		EventType:  eventType,
		Timestamp:  time.Now(),
		RawJSON:    json.RawMessage(rawJSON),
		Metadata:   make(map[string]any),
	}
}

// WithSequence sets the sequence number for ordering
func (e *Envelope) WithSequence(seq int) *Envelope {
	e.Sequence = seq
	return e
}

// WithSession sets session and message IDs
func (e *Envelope) WithSession(sessionID, messageID string) *Envelope {
	e.SessionID = sessionID
	e.MessageID = messageID
	return e
}

// WithParent sets the parent envelope ID (for threaded events)
func (e *Envelope) WithParent(parentID string) *Envelope {
	e.ParentID = parentID
	return e
}

// Transform applies the transformation rules to populate canonical fields.
// This uses the Side Table (registry) to look up the correct schema.
func (e *Envelope) Transform(registry *TransformRegistry) error {
	schema, err := registry.Schema(e.ProviderID)
	if err != nil {
		return err
	}

	canonical, err := schema.Transform(e.RawJSON, e.EventType)
	if err != nil {
		return err
	}

	e.Canonical = canonical
	return nil
}

// GetRaw returns the original JSON exactly as received.
// Use this when you need to reconstruct the provider-specific format.
func (e *Envelope) GetRaw() json.RawMessage {
	return e.RawJSON
}

// GetCanonical returns the transformed canonical payload.
// Use this for application logic that doesn't care about provider differences.
func (e *Envelope) GetCanonical() *CanonicalPayload {
	return e.Canonical
}

// MarshalNDJSON returns the envelope as newline-delimited JSON (for logging)
func (e *Envelope) MarshalNDJSON() ([]byte, error) {
	return json.Marshal(e)
}

// generateID creates a unique envelope ID
func generateID() string {
	return time.Now().Format("20060102150405.000000000")
}
