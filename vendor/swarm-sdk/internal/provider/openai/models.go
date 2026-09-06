// Package openai provides OpenAI API provider implementation.
package openai

// ChatCompletionRequest matches OpenAI's chat completion request format.
type ChatCompletionRequest struct {
	Model               string            `json:"model"`
	Messages            []OpenAIMessage   `json:"messages"`
	MaxTokens           *int              `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int              `json:"max_completion_tokens,omitempty"` // For o1/o3/gpt-5+ models
	Temperature         *float64          `json:"temperature,omitempty"`
	TopP                *float64          `json:"top_p,omitempty"`
	N                   *int              `json:"n,omitempty"`
	Stream              bool              `json:"stream,omitempty"`
	StreamOptions       *StreamOptions    `json:"stream_options,omitempty"` // Request token usage in streaming responses
	Stop                []string          `json:"stop,omitempty"`
	PresencePenalty     *float64          `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64          `json:"frequency_penalty,omitempty"`
	User                string            `json:"user,omitempty"`
	Tools               []Tool            `json:"tools,omitempty"`
	ToolChoice          any               `json:"tool_choice,omitempty"`
	ResponseFormat      *ResponseFormat   `json:"response_format,omitempty"`
	ParallelToolCalls   *bool             `json:"parallel_tool_calls,omitempty"`
	Text                *TextOptions      `json:"text,omitempty"`             // GPT-5+ text options
	Reasoning           *ReasoningOptions `json:"reasoning,omitempty"`        // GPT-5+ reasoning options
	ReasoningEffort     *string           `json:"reasoning_effort,omitempty"` // Cerebras reasoning effort
}

// StreamOptions controls streaming behavior for OpenAI API.
type StreamOptions struct {
	// IncludeUsage requests token usage information in streaming responses.
	// When true, the final chunk will include usage data.
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// TextOptions contains GPT-5+ text generation options.
type TextOptions struct {
	// Verbosity controls response expansiveness.
	// Values: "low", "medium", "high"
	Verbosity string `json:"verbosity,omitempty"`

	// Format controls output format.
	Format *TextFormat `json:"format,omitempty"`
}

// TextFormat specifies output formatting constraints.
type TextFormat struct {
	// Type is the format type.
	// Values: "text" (default), "json_object"
	Type string `json:"type,omitempty"`
}

// ReasoningOptions contains GPT-5+ reasoning control options.
type ReasoningOptions struct {
	// Effort controls reasoning token usage.
	// Values: "minimal", "medium", "high"
	Effort string `json:"effort,omitempty"`
}

// OpenAIMessage can have content as string OR []ContentPart (vision).
type OpenAIMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`                     // string OR []ContentPart
	Reasoning        string     `json:"reasoning,omitempty"`         // Provider-specific reasoning (e.g., Cerebras)
	ReasoningContent string     `json:"reasoning_content,omitempty"` // GLM-specific reasoning field
	Name             *string    `json:"name,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       *string    `json:"tool_call_id,omitempty"`
}

// ContentPart for vision/multimodal.
type ContentPart struct {
	Type     string    `json:"type"` // "text" or "image_url"
	Text     string    `json:"text,omitempty"`
	ImageURL *ImageURL `json:"image_url,omitempty"`
}

// ImageURL represents an image in multimodal messages.
type ImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"` // "auto", "low", "high"
}

// ChatCompletionResponse matches OpenAI's response.
type ChatCompletionResponse struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint string   `json:"system_fingerprint,omitempty"`
}

// Choice represents a single choice in the response.
type Choice struct {
	Index        int           `json:"index"`
	Message      OpenAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"` // "stop", "length", "tool_calls", "content_filter"
}

// Usage tracks token consumption.
// Supports both OpenAI format (prompt_tokens/completion_tokens) and
// Anthropic-style format (input_tokens/output_tokens) used by Cerebras, OpenRouter, etc.
type Usage struct {
	// OpenAI standard format
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	// Anthropic-style format (used by Cerebras, some OpenRouter models, etc.)
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// PromptTokensDetails carries the cache breakdown for OpenAI-compatible
	// providers (Fireworks, Z.ai/GLM, Kimi, OpenRouter, DeepSeek, etc.) that
	// follow the OpenAI prompt-caching convention. CachedTokens reports the
	// portion of prompt_tokens that hit the provider-side cache.
	PromptTokensDetails *PromptTokensDetails `json:"prompt_tokens_details,omitempty"`

	// CompletionTokensDetails carries reasoning token counts for providers that
	// emit them (o1/o3/GLM-style). Currently captured for logging only.
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails breaks down prompt_tokens into cached vs uncached.
// OpenAI convention: prompt_tokens is the TOTAL (cached + uncached);
// cached_tokens is the portion served from cache.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// CompletionTokensDetails breaks down completion tokens for reasoning models.
type CompletionTokensDetails struct {
	ReasoningTokens int `json:"reasoning_tokens"`
}

// CachedTokens returns the number of prompt tokens that were served from the
// provider-side prompt cache, or 0 if not reported.
func (u Usage) CachedTokens() int {
	if u.PromptTokensDetails == nil {
		return 0
	}
	return u.PromptTokensDetails.CachedTokens
}

// InputCount returns the input token count, normalising between OpenAI and Anthropic-style response formats.
func (u Usage) InputCount() int {
	if u.PromptTokens > 0 {
		return u.PromptTokens
	}
	return u.InputTokens
}

// OutputCount returns the output token count, normalising between OpenAI and Anthropic-style response formats.
func (u Usage) OutputCount() int {
	if u.CompletionTokens > 0 {
		return u.CompletionTokens
	}
	return u.OutputTokens
}

// TotalCount returns the total token count.
func (u Usage) TotalCount() int {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.InputCount() + u.OutputCount()
}

// Tool definition for function calling.
type Tool struct {
	Type        string      `json:"type"` // "function" or "custom" (GPT-5+)
	Function    *Function   `json:"function,omitempty"`
	Name        string      `json:"name,omitempty"`        // For custom tools (GPT-5+)
	Description string      `json:"description,omitempty"` // For custom tools (GPT-5+)
	Format      *ToolFormat `json:"format,omitempty"`      // For custom tools (GPT-5+)
}

// Function describes a tool function (traditional structured calling).
type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"` // JSON Schema
}

// ToolFormat defines output constraints for custom tools (GPT-5+).
type ToolFormat struct {
	// Type is the format type.
	// Values: "text" (freeform), "grammar" (CFG-constrained)
	Type string `json:"type,omitempty"`

	// Syntax specifies the grammar syntax (when Type == "grammar").
	// Values: "lark", "regex"
	Syntax string `json:"syntax,omitempty"`

	// Definition is the grammar/regex definition (when Type == "grammar").
	Definition string `json:"definition,omitempty"`
}

// ToolCall represents a tool invocation.
type ToolCall struct {
	Index    int          `json:"index"` // Index for streaming tool call accumulation
	ID       string       `json:"id"`
	Type     string       `json:"type"` // "function"
	Function FunctionCall `json:"function"`
}

// FunctionCall represents a function invocation.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// ResponseFormat controls output format.
type ResponseFormat struct {
	Type string `json:"type"` // "text" or "json_object"
}

// APIStreamError is the error format some providers (e.g. xAI) embed in SSE chunks.
type APIStreamError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code,omitempty"`
}

// ChatCompletionChunk represents a streaming chunk.
type ChatCompletionChunk struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"` // "chat.completion.chunk"
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []ChunkChoice   `json:"choices"`
	Usage   *Usage          `json:"usage,omitempty"` // Token usage (when stream_options.include_usage=true)
	Error   *APIStreamError `json:"error,omitempty"` // xAI and some others embed errors in SSE data
}

// ChunkChoice represents a choice in a streaming chunk.
type ChunkChoice struct {
	Index        int        `json:"index"`
	Delta        ChunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// ChunkDelta represents incremental content in a chunk.
type ChunkDelta struct {
	Role             string        `json:"role,omitempty"`
	Content          any           `json:"content,omitempty"`
	Reasoning        string        `json:"reasoning,omitempty"`         // Provider-specific reasoning (e.g., Cerebras)
	ReasoningContent string        `json:"reasoning_content,omitempty"` // GLM-specific reasoning field
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	FunctionCall     *FunctionCall `json:"function_call,omitempty"` // Legacy function_call format (some providers)
}
