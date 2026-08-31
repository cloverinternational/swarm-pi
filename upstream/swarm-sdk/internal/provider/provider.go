// Package provider defines the interface for LLM providers.
// This is Ring 0 - pure interface definition with no implementations.
package provider

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// DefaultUnknownContextWindow is the conservative context window used only when
// neither model configuration nor provider capabilities report a limit.
//
// It is a fallback, not a floor: known 128K/131K models must keep their real
// limits or providers will reject oversized requests.
const DefaultUnknownContextWindow = 200_000

// DefaultContextWindowCap is the maximum context window the SDK advertises for
// any model by default, regardless of the model's published capability.
// Most frontier models now publish 1M+ token windows (Claude Opus 4.6+/Sonnet
// 4.6+/Fable 5, GPT-5.4/5.5, Gemini 3, Grok 4.x), but running agents against
// the full window balloons cost and latency and defers compaction until the
// conversation is enormous. 300K is the deliberate policy ceiling; an explicit
// per-model user configuration (configuredContextWindow) still overrides it.
const DefaultContextWindowCap = 300_000

// ClampContextWindow applies DefaultContextWindowCap to a model's published
// context window. Values at or below the cap (including 0/unknown) pass
// through unchanged.
func ClampContextWindow(window int) int {
	if window > DefaultContextWindowCap {
		return DefaultContextWindowCap
	}
	return window
}

// Provider defines the interface that all LLM providers must implement.
// Providers translate between the SDK's canonical message format and
// their native API format.
type Provider interface {
	// Name returns the unique identifier for this provider.
	// Examples: "anthropic", "openai", "gemini", "openrouter"
	Name() string

	// Chat sends a synchronous chat request and returns the complete response.
	// The request uses canonical message format and will be translated to the
	// provider's native format automatically by the implementation.
	//
	// Returns errors categorized as:
	// - Transient: Network errors, rate limits, timeouts
	// - Permanent: Invalid API key, unsupported model, malformed request
	// - Steering: Low quality response, content policy violation
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Stream sends a chat request and streams the response in chunks.
	// Returns a channel that emits response chunks as they arrive.
	// The channel will be closed when the stream completes or an error occurs.
	//
	// Callers must consume the channel to avoid goroutine leaks.
	Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)

	// Capabilities returns the capabilities and limits of this provider.
	Capabilities() Capabilities
}

// DebugProvider is an optional interface that providers can implement to expose debug information.
type DebugProvider interface {
	// GetLastProviderJSON returns the raw JSON of the last translated request.
	// This shows exactly what was sent to the provider's API after translation.
	LastProviderJSON() json.RawMessage
}

// RawHTTPRequestCapture captures the complete raw HTTP request before sending.
// This includes URL, method, headers, and the complete untruncated body.
type RawHTTPRequestCapture struct {
	Timestamp time.Time         `json:"timestamp"`
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Body      []byte            `json:"body"` // COMPLETE untruncated body
	BodySize  int               `json:"body_size"`
}

// RawHTTPResponseCapture captures the complete raw HTTP response.
type RawHTTPResponseCapture struct {
	Timestamp  time.Time         `json:"timestamp"`
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"` // COMPLETE untruncated body
	BodySize   int               `json:"body_size"`
}

// HTTPDebugProvider is an optional interface for providers that capture raw HTTP traffic.
type HTTPDebugProvider interface {
	// LastRawHTTPRequest returns the complete raw HTTP request that was sent.
	// This includes full headers and complete body with zero truncation.
	LastRawHTTPRequest() *RawHTTPRequestCapture

	// LastRawHTTPResponse returns the complete raw HTTP response that was received.
	LastRawHTTPResponse() *RawHTTPResponseCapture
}

// ThinkingConfig enables extended reasoning for models that support it (e.g. Claude 3.7+).
type ThinkingConfig struct {
	// Enabled turns on extended thinking.
	Enabled bool
	// Budget is the maximum number of tokens the model may use for internal reasoning.
	// Higher values allow deeper reasoning at the cost of latency and token usage.
	Budget int
}

// CacheStats reports prompt-cache performance for a single request.
type CacheStats struct {
	// ReadTokens is the number of tokens served from the cache.
	ReadTokens int
	// CreationTokens is the number of tokens written to the cache this turn.
	CreationTokens int
}

// ChatRequest represents a request to a provider using canonical format.
type ChatRequest struct {
	// Messages is the conversation history in canonical format.
	Messages []*conversation.Message

	// Model is the specific model to use (provider-specific identifier).
	Model string

	// Temperature controls randomness (0.0 to 1.0, provider may have different ranges).
	Temperature *float64

	// TopP controls nucleus sampling. Nil leaves the provider default unchanged.
	TopP *float64

	// TopK limits sampling to the K most likely tokens. Nil leaves the provider default unchanged.
	TopK *int

	// MaxTokens limits the response length.
	MaxTokens *int

	// Tools are the available tools for function calling.
	Tools []Tool

	// SystemPrompt is an optional system message (some providers handle this specially).
	SystemPrompt string

	// StopSequences are sequences that will halt generation.
	StopSequences []string

	// Verbosity controls response expansiveness (GPT-5+).
	// Values: "low" (terse), "medium" (balanced), "high" (verbose).
	Verbosity string

	// ReasoningEffort controls reasoning token usage (GPT-5+).
	// Values: "none", "minimal", "low", "medium", "high", "xhigh" (provider support varies).
	ReasoningEffort string

	// Thinking enables extended reasoning for providers that support it.
	// When set, takes precedence over Metadata["thinking_enabled"].
	Thinking *ThinkingConfig

	// Metadata contains provider-specific options not covered by the fields above.
	Metadata map[string]any
}

// ChatResponse represents a complete response from a provider.
type ChatResponse struct {
	// Message is the response in canonical format.
	Message *conversation.Message

	// FinishReason indicates why generation stopped.
	FinishReason FinishReason

	// Usage tracks token consumption.
	Usage *conversation.TokenUsage

	// Cache reports prompt-cache performance. Nil if the provider does not support caching.
	Cache *CacheStats

	// RawResponse contains the provider's raw response (for debugging).
	RawResponse any

	// Metadata contains provider-specific data not covered by the fields above.
	Metadata map[string]any

	// StreamedContent indicates whether the response content was delivered via
	// streaming. When true, ContentUpdate callbacks were already emitted during
	// streaming, so downstream code should not emit them again.
	StreamedContent bool
}

// StreamChunk represents a single chunk in a streaming response.
type StreamChunk struct {
	// Delta is the incremental content added in this chunk.
	Delta string

	// Thinking contains extended thinking content (for providers that support it).
	// This must be preserved and sent back in conversation history.
	Thinking string

	// ThinkingSignature is the signature for the thinking block (required by Anthropic).
	// Must be preserved from the original response to send back in future requests.
	ThinkingSignature string

	// FinishReason is set on the final chunk to indicate why generation stopped.
	FinishReason FinishReason

	// Usage is set on the final chunk with total token usage.
	Usage *conversation.TokenUsage

	// ToolCalls contains any tool calls made by the assistant.
	ToolCalls []conversation.ToolCall

	// OrderedBlocks preserves provider-native ordering of content/thinking/
	// tool_call blocks for this assistant turn. Set only on the final chunk
	// (Done=true), alongside ToolCalls. When empty, the agent derives order
	// from the flat accumulators above; see conversation.BlocksInDisplayOrder.
	OrderedBlocks []conversation.MessageBlock

	// Done indicates this is the final chunk.
	Done bool

	// Error contains any error that occurred during streaming.
	Error error

	// Metadata contains provider-specific data like cache metrics.
	// For Anthropic, this includes "cache_metrics" with cache_read_tokens, cache_creation_tokens, etc.
	Metadata map[string]any
}

// FinishReason indicates why generation stopped.
type FinishReason string

const (
	// FinishReasonStop indicates natural completion.
	FinishReasonStop FinishReason = "stop"

	// FinishReasonLength indicates max token limit reached.
	FinishReasonLength FinishReason = "length"

	// FinishReasonToolCalls indicates the model wants to call tools.
	FinishReasonToolCalls FinishReason = "tool_calls"

	// FinishReasonContentFilter indicates content was filtered.
	FinishReasonContentFilter FinishReason = "content_filter"

	// FinishReasonError indicates an error occurred.
	FinishReasonError FinishReason = "error"
)

// Tool represents a tool available for function calling.
type Tool struct {
	// Name is the tool identifier.
	Name string

	// Description explains what the tool does.
	Description string

	// Parameters is the JSON Schema for the tool's parameters.
	// For custom/freeform tools (GPT-5), this may be nil.
	Parameters any

	// Type specifies the tool type (GPT-5+).
	// Values: "function" (default, structured JSON), "custom" (freeform text output).
	// For Anthropic beta tools: "computer_20241022", "text_editor_20241022", "bash_20241022"
	Type string

	// Format specifies output constraints for custom tools (GPT-5+).
	// Only used when Type == "custom".
	Format *ToolFormat

	// Metadata contains provider-specific tool options.
	// Used for beta tool configurations (e.g., display dimensions for computer use).
	Metadata map[string]any
}

// ToolFormat defines output constraints for custom tools (GPT-5+).
type ToolFormat struct {
	// Type is the format type.
	// Values: "text" (freeform), "grammar" (CFG-constrained).
	Type string

	// Syntax specifies the grammar syntax (when Type == "grammar").
	// Values: "lark", "regex".
	Syntax string

	// Definition is the grammar/regex definition (when Type == "grammar").
	Definition string
}

// Capabilities describes what a provider supports.
type Capabilities struct {
	// Streaming indicates if the provider supports streaming responses.
	Streaming bool

	// FunctionCalling indicates if the provider supports tool/function calling.
	FunctionCalling bool

	// Vision indicates if the provider supports image inputs.
	Vision bool

	// MaxContextWindow is the maximum context window size in tokens.
	MaxContextWindow int

	// MaxOutputTokens is the maximum output length in tokens.
	MaxOutputTokens int

	// SupportsSystemPrompt indicates if system prompts are natively supported.
	SupportsSystemPrompt bool

	// SupportsTemperature indicates if temperature is supported.
	SupportsTemperature bool

	// SupportedModels lists all available models for this provider.
	SupportedModels []string

	// PromptCaching indicates if the provider supports prompt caching.
	PromptCaching bool

	// SupportsJSON indicates if the provider supports JSON mode.
	SupportsJSON bool
}

// RawEventCallback is an optional interface for capturing raw API events before transformation.
// This allows debugging of actual API responses without any SDK processing.
// IMPORTANT: This callback should NOT log to file/terminal - only to debug screen.
type RawEventCallback interface {
	// OnRawEvent is called with raw SSE event data before parsing/transforming.
	// The eventType is SSE event type (e.g., "message_start", "content_block_delta", etc.)
	// The rawData is the unparsed JSON string from the API.
	// This is useful for debugging token count parsing and response transformation issues.
	OnRawEvent(eventType string, rawData string)
}
