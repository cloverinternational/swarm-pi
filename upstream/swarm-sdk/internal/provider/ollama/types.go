package ollama

import (
	"encoding/json"
)

// ChatRequest represents a request to the Ollama chat API
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  *ModelOptions `json:"options,omitempty"`
	Format   any           `json:"format,omitempty"`
}

// ChatMessage represents a single message in a chat conversation
type ChatMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
}

// ModelOptions contains optional model parameters
type ModelOptions struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	TopK             *int     `json:"top_k,omitempty"`
	NumCtx           int      `json:"num_ctx,omitempty"`
	NumPredict       int      `json:"num_predict,omitempty"`
	Seed             int      `json:"seed,omitempty"`
	RepeatPenalty    float64  `json:"repeat_penalty,omitempty"`
	FrequencyPenalty float64  `json:"frequency_penalty,omitempty"`
	PresencePenalty  float64  `json:"presence_penalty,omitempty"`
	Stop             []string `json:"stop,omitempty"`
}

// ChatResponse represents a response from the Ollama chat API
type ChatResponse struct {
	Model     string      `json:"model"`
	CreatedAt string      `json:"created_at"`
	Message   ChatMessage `json:"message"`
	Done      bool        `json:"done"`
	// Statistics (present in final response)
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
	DoneReason         string `json:"done_reason,omitempty"`
}

// TagsResponse represents the response from /api/tags endpoint
type TagsResponse struct {
	Models []ModelInfo `json:"models"`
}

// ModelInfo contains information about an available model
type ModelInfo struct {
	Name       string `json:"name"`
	ModifiedAt string `json:"modified_at"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
	Details    struct {
		ParentModel       string   `json:"parent_model"`
		Format            string   `json:"format"`
		Family            string   `json:"family"`
		Families          []string `json:"families"`
		ParameterSize     string   `json:"parameter_size"`
		QuantizationLevel string   `json:"quantization_level"`
	} `json:"details"`
}

// GenerateRequest represents a request to the Ollama generate API
type GenerateRequest struct {
	Model    string        `json:"model"`
	Prompt   string        `json:"prompt"`
	Stream   bool          `json:"stream"`
	Options  *ModelOptions `json:"options,omitempty"`
	Format   any           `json:"format,omitempty"`
	System   string        `json:"system,omitempty"`
	Template string        `json:"template,omitempty"`
	Context  []int         `json:"context,omitempty"`
	Raw      bool          `json:"raw,omitempty"`
}

// GenerateResponse represents a response from the Ollama generate API
type GenerateResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Response  string `json:"response"`
	Done      bool   `json:"done"`
	Context   []int  `json:"context,omitempty"`
	// Statistics
	TotalDuration      int64  `json:"total_duration,omitempty"`
	LoadDuration       int64  `json:"load_duration,omitempty"`
	PromptEvalCount    int    `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64  `json:"prompt_eval_duration,omitempty"`
	EvalCount          int    `json:"eval_count,omitempty"`
	EvalDuration       int64  `json:"eval_duration,omitempty"`
	DoneReason         string `json:"done_reason,omitempty"`
}

// ErrorResponse represents an error from the Ollama API
type ErrorResponse struct {
	Error string `json:"error"`
}

// IsError checks if the response is an error
func (e *ErrorResponse) IsError() bool {
	return e.Error != ""
}

// ParseErrorResponse attempts to parse an error response
func ParseErrorResponse(data []byte) *ErrorResponse {
	var errResp ErrorResponse
	if err := json.Unmarshal(data, &errResp); err == nil && errResp.Error != "" {
		return &errResp
	}
	return nil
}
