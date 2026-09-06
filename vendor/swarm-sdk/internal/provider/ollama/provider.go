// Package ollama implements the Ollama provider for both cloud and local modes.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Provider implements the provider.Provider interface for Ollama
type Provider struct {
	config     *Config
	httpClient *http.Client
	name       string
}

// New creates a new Ollama provider
func New(config *Config) *Provider {
	if config == nil {
		config = DefaultLocalConfig()
	}
	config.Validate()

	httpClient := &http.Client{
		Timeout: config.Timeout,
	}

	// Wrap with debug transport if RawDebugWriter is set
	if config.RawDebugWriter != nil {
		httpClient.Transport = provider.NewDebugTransport(http.DefaultTransport, config.RawDebugWriter)
	}

	return &Provider{
		config:     config,
		name:       "ollama",
		httpClient: httpClient,
	}
}

// NewWithAPIKey creates a new Ollama Cloud provider with an API key
func NewWithAPIKey(apiKey string) *Provider {
	config := DefaultCloudConfig()
	config.APIKey = apiKey
	return New(config)
}

// Name returns the provider name
func (p *Provider) Name() string {
	return p.name
}

// Chat sends a synchronous chat request
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	// Build Ollama request
	ollamaReq := p.buildOllamaRequest(req, false)

	// Make request
	resp, err := p.doRequest(ctx, "/api/chat", ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("ollama chat request failed: %w", err)
	}
	defer resp.Body.Close()

	// Parse response
	var ollamaResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Convert to canonical format
	return p.toCanonicalResponse(&ollamaResp), nil
}

// Stream sends a chat request and streams the response
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	// Build Ollama request with streaming enabled
	ollamaReq := p.buildOllamaRequest(req, true)

	// Make request
	resp, err := p.doRequest(ctx, "/api/chat", ollamaReq)
	if err != nil {
		return nil, fmt.Errorf("ollama stream request failed: %w", err)
	}

	// Create output channel
	chunkChan := make(chan provider.StreamChunk, 100)

	// Start streaming goroutine
	go p.streamResponse(resp, chunkChan)

	return chunkChan, nil
}

// Capabilities returns the provider capabilities
func (p *Provider) Capabilities() provider.Capabilities {
	return provider.Capabilities{
		Streaming:            true,
		FunctionCalling:      true,
		Vision:               true,
		MaxContextWindow:     128000,
		MaxOutputTokens:      4096,
		SupportsSystemPrompt: true,
		SupportsTemperature:  true,
		SupportsJSON:         true,
		PromptCaching:        false,
		SupportedModels:      p.getSupportedModels(),
	}
}

// buildOllamaRequest converts a canonical request to Ollama format
func (p *Provider) buildOllamaRequest(req provider.ChatRequest, stream bool) *ChatRequest {
	ollamaReq := &ChatRequest{
		Model:    req.Model,
		Messages: make([]ChatMessage, 0, len(req.Messages)),
		Stream:   stream,
	}

	// Set model from config if not specified
	if ollamaReq.Model == "" {
		ollamaReq.Model = p.config.Model
	}

	// Convert messages
	for _, msg := range provider.PrepareMessagesForLLM(req.Messages) {
		ollamaMsg := ChatMessage{
			Role:    string(msg.Role),
			Content: msg.Content,
		}
		ollamaReq.Messages = append(ollamaReq.Messages, ollamaMsg)
	}

	// Add system prompt if provided
	if req.SystemPrompt != "" {
		// Prepend system message
		systemMsg := ChatMessage{
			Role:    "system",
			Content: req.SystemPrompt,
		}
		ollamaReq.Messages = append([]ChatMessage{systemMsg}, ollamaReq.Messages...)
	}

	// Set options
	if req.Temperature != nil || req.TopP != nil || req.TopK != nil || req.MaxTokens != nil || len(req.StopSequences) > 0 {
		ollamaReq.Options = &ModelOptions{}
		if req.Temperature != nil {
			ollamaReq.Options.Temperature = req.Temperature
		}
		if req.TopP != nil {
			ollamaReq.Options.TopP = req.TopP
		}
		if req.TopK != nil {
			ollamaReq.Options.TopK = req.TopK
		}
		if req.MaxTokens != nil {
			ollamaReq.Options.NumPredict = *req.MaxTokens
		}
		if len(req.StopSequences) > 0 {
			ollamaReq.Options.Stop = req.StopSequences
		}
	}

	return ollamaReq
}

// doRequest makes an HTTP request to the Ollama API
func (p *Provider) doRequest(ctx context.Context, endpoint string, payload any) (*http.Response, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := p.config.BaseURL + endpoint
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Add authentication for cloud mode
	if p.config.HasAuth() {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Check for error response
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)
		if errResp := ParseErrorResponse(bodyBytes); errResp != nil {
			return nil, fmt.Errorf("API error: %s", errResp.Error)
		}
		return nil, fmt.Errorf("API error: status %d - %s", resp.StatusCode, string(bodyBytes))
	}

	return resp, nil
}

// streamResponse handles streaming response from Ollama
func (p *Provider) streamResponse(resp *http.Response, chunkChan chan<- provider.StreamChunk) {
	defer close(chunkChan)
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)

	// accumulatedContent captures the full text so the final chunk can
	// expose OrderedBlocks — Ollama's chat stream currently surfaces only
	// text content, so a single content block suffices.
	var accumulatedContent strings.Builder

	buildOrderedBlocks := func() []conversation.MessageBlock {
		if accumulatedContent.Len() == 0 {
			return nil
		}
		return []conversation.MessageBlock{{
			Type:     conversation.BlockTypeContent,
			Content:  accumulatedContent.String(),
			Sequence: 0,
		}}
	}

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				chunkChan <- provider.StreamChunk{
					Done:          true,
					OrderedBlocks: buildOrderedBlocks(),
				}
				return
			}
			chunkChan <- provider.StreamChunk{
				Error: fmt.Errorf("stream read error: %w", err),
				Done:  true,
			}
			return
		}

		// Skip empty lines
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// Parse the JSON response
		var ollamaResp ChatResponse
		if err := json.Unmarshal(line, &ollamaResp); err != nil {
			chunkChan <- provider.StreamChunk{
				Error: fmt.Errorf("failed to parse stream chunk: %w", err),
			}
			continue
		}

		if ollamaResp.Message.Content != "" {
			accumulatedContent.WriteString(ollamaResp.Message.Content)
		}

		// Convert to canonical chunk
		chunk := provider.StreamChunk{
			Delta: ollamaResp.Message.Content,
			Done:  ollamaResp.Done,
		}

		// Set finish reason on final chunk
		if ollamaResp.Done {
			chunk.FinishReason = provider.FinishReasonStop
			chunk.OrderedBlocks = buildOrderedBlocks()
			// Include usage stats if available
			if ollamaResp.PromptEvalCount > 0 || ollamaResp.EvalCount > 0 {
				chunk.Usage = &conversation.TokenUsage{
					Input:  ollamaResp.PromptEvalCount,
					Output: ollamaResp.EvalCount,
					Total:  ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
				}
			}
		}

		chunkChan <- chunk

		// Exit if done
		if ollamaResp.Done {
			return
		}
	}
}

// toCanonicalResponse converts an Ollama response to canonical format
func (p *Provider) toCanonicalResponse(resp *ChatResponse) *provider.ChatResponse {
	// Create canonical message
	msg := &conversation.Message{
		Role:      conversation.RoleAssistant,
		Content:   resp.Message.Content,
		Timestamp: time.Now(),
		Provider:  p.name,
		Model:     resp.Model,
	}

	// Set tokens if available
	if resp.PromptEvalCount > 0 || resp.EvalCount > 0 {
		msg.Tokens = &conversation.TokenUsage{
			Input:  resp.PromptEvalCount,
			Output: resp.EvalCount,
			Total:  resp.PromptEvalCount + resp.EvalCount,
		}
	}

	return &provider.ChatResponse{
		Message:      msg,
		FinishReason: provider.FinishReasonStop,
		Usage:        msg.Tokens,
		RawResponse:  resp,
	}
}

// getSupportedModels returns the list of supported models
func (p *Provider) getSupportedModels() []string {
	models := GetAllModels()
	result := make([]string, len(models))
	for i, m := range models {
		result[i] = m.ID
	}
	return result
}

// ListModels fetches available models from the Ollama instance
func (p *Provider) ListModels(ctx context.Context) ([]ModelInfo, error) {
	url := p.config.BaseURL + "/api/tags"
	httpReq, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authentication for cloud mode
	if p.config.HasAuth() {
		httpReq.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var tagsResp TagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tagsResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return tagsResp.Models, nil
}

// SetBaseURL allows changing the base URL (useful for testing or custom endpoints)
func (p *Provider) SetBaseURL(url string) {
	p.config.BaseURL = strings.TrimSuffix(url, "/")
}

// ============================================================
// Model Sync Functions
// ============================================================

// SyncedModels represents models fetched from Ollama Cloud
type SyncedModels struct {
	Models    []ModelInfo `json:"models"`
	Timestamp time.Time   `json:"timestamp"`
	Error     string      `json:"error,omitempty"`
}

// cachedModels holds the last fetched models
var cachedModels *SyncedModels
var cacheDuration = 5 * time.Minute

// SyncModelsFromCloud fetches the latest models from Ollama Cloud
// It caches results for 5 minutes to avoid excessive API calls
func SyncModelsFromCloud(ctx context.Context, apiKey string) (*SyncedModels, error) {
	// Check cache
	if cachedModels != nil && time.Since(cachedModels.Timestamp) < cacheDuration {
		return cachedModels, nil
	}

	provider := NewWithAPIKey(apiKey)
	models, err := provider.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}

	result := &SyncedModels{
		Models:    models,
		Timestamp: time.Now(),
	}

	cachedModels = result
	return result, nil
}

// GetCachedModels returns the cached models if available
func GetCachedModels() *SyncedModels {
	return cachedModels
}

// ConvertToModelConfigs converts Ollama ModelInfo to provider model configs
func ConvertToModelConfigs(models []ModelInfo) []ModelConfig {
	configs := make([]ModelConfig, 0, len(models))
	for _, m := range models {
		config := ModelConfig{
			ID:            m.Name,
			Name:          m.Name,
			ContextWindow: 128000, // Default context window
		}
		// Try to determine context window from model details
		if m.Details.Family != "" {
			config.Description = m.Details.Family
		}
		configs = append(configs, config)
	}
	return configs
}

// ModelConfig represents a model configuration for provider config
type ModelConfig struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
	Description   string `json:"description,omitempty"`
}
