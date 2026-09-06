package anthropic

import "encoding/json"

// Anthropic Messages API request/response types
// Based on official API: https://docs.anthropic.com/en/api/messages

// MessageRequest represents a request to the Anthropic Messages API.
type MessageRequest struct {
	Model         string           `json:"model"`
	Messages      []Message        `json:"messages"`
	MaxTokens     int              `json:"max_tokens"`
	System        any              `json:"system,omitempty"`      // string or []SystemBlock
	Temperature   *float64         `json:"temperature,omitempty"` // 0.0-1.0
	TopP          *float64         `json:"top_p,omitempty"`       // 0.0-1.0
	TopK          *int             `json:"top_k,omitempty"`       // Integer
	StopSequences []string         `json:"stop_sequences,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	Metadata      *Metadata        `json:"metadata,omitempty"`
	Tools         []Tool           `json:"tools,omitempty"`
	ToolChoice    any              `json:"tool_choice,omitempty"`   // auto, any, tool, or nil
	Thinking      *ThinkingConfig  `json:"thinking,omitempty"`      // Extended thinking
	OutputConfig  *OutputConfig    `json:"output_config,omitempty"` // Output configuration (effort, etc.)
	Citations     *CitationsConfig `json:"citations,omitempty"`     // Citations (beta)
}

// Message represents a single message in the conversation.
type Message struct {
	Role    string `json:"role"`    // "user" or "assistant"
	Content any    `json:"content"` // string or []ContentBlock
}

// ContentBlock represents a content block in a message (multimodal support).
type ContentBlock struct {
	Type         string         `json:"type"` // "text", "image", "document", "tool_use", "tool_result"
	Text         string         `json:"text,omitempty"`
	Source       *ContentSource `json:"source,omitempty"`        // For images/documents
	ID           string         `json:"id,omitempty"`            // For tool_use/tool_result
	Name         string         `json:"name,omitempty"`          // For tool_use
	Input        any            `json:"input,omitempty"`         // For tool_use
	ToolUseID    string         `json:"tool_use_id,omitempty"`   // For tool_result
	Content      any            `json:"content,omitempty"`       // For tool_result (string or []ContentBlock)
	IsError      bool           `json:"is_error,omitempty"`      // For tool_result
	CacheControl *CacheControl  `json:"cache_control,omitempty"` // Prompt caching
	Thinking     string         `json:"thinking,omitempty"`      // Extended thinking content
	Signature    string         `json:"signature,omitempty"`     // Thinking signature (required when sending thinking blocks as input)
	Citations    []Citation     `json:"citations,omitempty"`     // Citations (beta)
}

// MarshalJSON implements custom JSON marshaling for ContentBlock to ensure
// tool_result blocks always include the content field (even if empty).
func (c ContentBlock) MarshalJSON() ([]byte, error) {
	// For tool_result blocks, we need to ensure content is always present
	if c.Type == "tool_result" {
		type Alias ContentBlock
		return json.Marshal(&struct {
			Content any `json:"content"` // Always include, no omitempty
			*Alias
		}{
			Content: c.Content,
			Alias:   (*Alias)(&c),
		})
	}

	// For all other block types, use default marshaling
	type Alias ContentBlock
	return json.Marshal((*Alias)(&c))
}

// ContentSource represents the source of an image or document.
type ContentSource struct {
	Type      string  `json:"type"`                 // "base64", "url", "file"
	MediaType *string `json:"media_type,omitempty"` // "image/jpeg", "application/pdf", etc. (only for base64)
	Data      string  `json:"data,omitempty"`       // Base64 data
	URL       string  `json:"url,omitempty"`        // URL
	FileID    string  `json:"file_id,omitempty"`    // File upload ID
}

// SystemBlock represents a system prompt block with optional cache control.
type SystemBlock struct {
	Type         string        `json:"type"` // "text"
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// CacheControl configures prompt caching for a content block.
type CacheControl struct {
	Type string `json:"type"`          // "ephemeral"
	TTL  string `json:"ttl,omitempty"` // "5m" or "1h"
}

// ThinkingConfig enables extended thinking with a token budget or adaptive mode.
// - Adaptive mode (Opus 4.6+): type="adaptive", Claude decides when and how much to think
// - Manual mode (all models): type="enabled" with budget_tokens
// - Disabled: type="disabled" or omit the config
type ThinkingConfig struct {
	Type         string `json:"type"`                    // "enabled", "disabled", or "adaptive" (Claude 4.6+)
	BudgetTokens int    `json:"budget_tokens,omitempty"` // Min 1024, less than max_tokens (only for type="enabled")
	Display      string `json:"display,omitempty"`       // "summarized" or "omitted" (adaptive/modern enabled thinking)
}

// CitationsConfig enables citations in responses.
type CitationsConfig struct {
	Enabled bool `json:"enabled"` // Whether citations are enabled
}

// OutputConfig controls output generation behavior (Opus 4.6+).
type OutputConfig struct {
	Effort string `json:"effort,omitempty"` // "low", "medium", "high" (default), "max" (Opus 4.6 only)
}

// Citation represents a citation reference in the response.
// Supports multiple citation types: char_location, page_location, content_block_location, web_search_result_location
type Citation struct {
	Type string `json:"type"` // "char_location", "page_location", "content_block_location", "web_search_result_location"

	// Character-level citation fields
	StartCharIndex *int `json:"start_char_index,omitempty"` // Start character index (char_location)
	EndCharIndex   *int `json:"end_char_index,omitempty"`   // End character index (char_location)

	// Page-level citation fields
	StartPageNumber *int `json:"start_page_number,omitempty"` // Start page number (page_location)
	EndPageNumber   *int `json:"end_page_number,omitempty"`   // End page number (page_location)

	// Content block citation fields
	StartBlockIndex *int `json:"start_block_index,omitempty"` // Start block index (content_block_location)
	EndBlockIndex   *int `json:"end_block_index,omitempty"`   // End block index (content_block_location)

	// Web search result citation fields
	URL string `json:"url,omitempty"` // URL of web search result (web_search_result_location)

	// Common fields
	CitedText         string `json:"cited_text,omitempty"`          // The text that was cited
	DocumentIndex     *int   `json:"document_index,omitempty"`      // Index of source document
	DocumentTitle     string `json:"document_title,omitempty"`      // Title of source document
	FileID            string `json:"file_id,omitempty"`             // File ID of source
	EncryptedIndex    string `json:"encrypted_index,omitempty"`     // Encrypted index for web results
	Title             string `json:"title,omitempty"`               // Title (for web/search results)
	Source            string `json:"source,omitempty"`              // Source (for search results)
	SearchResultIndex *int   `json:"search_result_index,omitempty"` // Index of search result
}

// Tool represents a tool definition for function calling.
// Supports both standard function tools and beta tools (computer use, text editor, bash, web search).
type Tool struct {
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Type         string        `json:"type,omitempty"`          // Beta tool type (e.g., "computer_20241022")
	InputSchema  any           `json:"input_schema,omitempty"`  // JSON Schema (omitted for beta tools)
	CacheControl *CacheControl `json:"cache_control,omitempty"` // Prompt caching

	// Beta tool specific fields - Computer Use
	DisplayWidthPx  int `json:"display_width_px,omitempty"`  // Computer use: screen width
	DisplayHeightPx int `json:"display_height_px,omitempty"` // Computer use: screen height
	DisplayNumber   int `json:"display_number,omitempty"`    // Computer use: display number (default: 1)

	// Beta tool specific fields - Web Search (web_search_20250305)
	MaxUses        *int          `json:"max_uses,omitempty"`        // Web search: max number of searches
	AllowedDomains []string      `json:"allowed_domains,omitempty"` // Web search: allowed domains
	BlockedDomains []string      `json:"blocked_domains,omitempty"` // Web search: blocked domains
	UserLocation   *UserLocation `json:"user_location,omitempty"`   // Web search: user location for localization
}

// Metadata contains request metadata for tracking.
type Metadata struct {
	UserID string `json:"user_id,omitempty"`
}

// MessageResponse represents a response from the Anthropic Messages API.
type MessageResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // "message"
	Role         string         `json:"role"` // "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"` // "end_turn", "max_tokens", "stop_sequence", "tool_use"
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        Usage          `json:"usage"`
}

// Usage tracks token consumption for a request.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`

	// Cache metrics (prompt caching) - legacy format
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`

	// Cache metrics (prompt caching) - new format with TTL breakdown
	CacheCreation *CacheCreation `json:"cache_creation,omitempty"`

	// Extended thinking metrics
	ThinkingInputTokens  *int `json:"thinking_input_tokens,omitempty"`
	ThinkingOutputTokens *int `json:"thinking_output_tokens,omitempty"`
}

// CacheCreation tracks cache creation by TTL duration.
type CacheCreation struct {
	Ephemeral1hInputTokens int `json:"ephemeral_1h_input_tokens"`
	Ephemeral5mInputTokens int `json:"ephemeral_5m_input_tokens"`
}

// StreamEvent represents a server-sent event in streaming responses.
type StreamEvent struct {
	Type  string `json:"type"` // "message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop", "error"
	Index *int   `json:"index,omitempty"`

	// message_start
	Message *MessageResponse `json:"message,omitempty"`

	// content_block_start
	ContentBlock *ContentBlock `json:"content_block,omitempty"`

	// content_block_delta
	Delta *Delta `json:"delta,omitempty"`

	// message_delta
	Usage *Usage `json:"usage,omitempty"`

	// error
	Error *APIError `json:"error,omitempty"`

	// context_management is sent on message_delta events when the
	// context-management-2025-06-27 beta header is set.
	ContextManagement map[string]any `json:"context_management,omitempty"`
}

// Delta represents incremental content in streaming responses.
type Delta struct {
	Type         string  `json:"type"` // "text_delta", "input_json_delta", "thinking_delta", "signature_delta"
	Text         string  `json:"text,omitempty"`
	PartialJSON  string  `json:"partial_json,omitempty"`
	Thinking     string  `json:"thinking,omitempty"`
	Signature    string  `json:"signature,omitempty"` // Thinking block signature
	StopReason   string  `json:"stop_reason,omitempty"`
	StopSequence *string `json:"stop_sequence,omitempty"`
}

// APIError represents an error response from the Anthropic API.
type APIError struct {
	Type  string      `json:"type"` // "error"
	Error ErrorDetail `json:"error"`
}

// ErrorDetail provides detailed error information.
type ErrorDetail struct {
	Type    string `json:"type"` // "invalid_request_error", "authentication_error", etc.
	Message string `json:"message"`
}

// BatchRequest represents a request to create a message batch.
type BatchRequest struct {
	Requests []BatchItem `json:"requests"`
}

// BatchItem represents a single request in a batch.
type BatchItem struct {
	CustomID string         `json:"custom_id"`
	Params   MessageRequest `json:"params"`
}

// BatchResponse represents the status of a message batch.
type BatchResponse struct {
	ID                string             `json:"id"`
	Type              string             `json:"type"`              // "message_batch"
	ProcessingStatus  string             `json:"processing_status"` // "in_progress", "canceling", "ended"
	RequestCounts     BatchRequestCounts `json:"request_counts"`
	EndedAt           *string            `json:"ended_at,omitempty"`
	CreatedAt         string             `json:"created_at"`
	ExpiresAt         string             `json:"expires_at"`
	ArchivedAt        *string            `json:"archived_at,omitempty"`
	CancelInitiatedAt *string            `json:"cancel_initiated_at,omitempty"`
	ResultsURL        *string            `json:"results_url,omitempty"`
}

// BatchRequestCounts tracks the status of requests in a batch.
type BatchRequestCounts struct {
	Processing int `json:"processing"`
	Succeeded  int `json:"succeeded"`
	Errored    int `json:"errored"`
	Canceled   int `json:"canceled"`
	Expired    int `json:"expired"`
}

// BatchResultResponse represents the results of a completed batch.
type BatchResultResponse struct {
	CustomID string      `json:"custom_id"`
	Result   BatchResult `json:"result"`
}

// BatchResult contains the result or error for a batch item.
type BatchResult struct {
	Type    string           `json:"type"` // "succeeded", "errored", "canceled", "expired"
	Message *MessageResponse `json:"message,omitempty"`
	Error   *ErrorDetail     `json:"error,omitempty"`
}
