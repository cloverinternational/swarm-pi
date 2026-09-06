package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"

	// OpenRouter models that support audio input
	OpenRouterModelGemini25Flash  = "google/gemini-2.5-flash"
	OpenRouterModelGemini25Pro    = "google/gemini-2.5-pro"
	OpenRouterModelGemini20Flash  = "google/gemini-2.0-flash-001"
	OpenRouterModelClaude35Sonnet = "anthropic/claude-3.5-sonnet"
)

// OpenRouterProvider implements STT using OpenRouter's audio-capable models
type OpenRouterProvider struct {
	*RESTProvider

	// model is the OpenRouter model to use
	model string

	// systemPrompt is the system prompt for transcription
	systemPrompt string

	// userPrompt is the user prompt template
	userPrompt string
}

// OpenRouterConfig contains OpenRouter-specific configuration
type OpenRouterConfig struct {
	// APIKey is the OpenRouter API key
	APIKey string

	// Model is the model to use
	Model string

	// SampleRate is the audio sample rate
	SampleRate int

	// AudioFormat is the audio format (wav, mp3, flac, etc.)
	AudioFormat string

	// ChunkDuration is the chunk duration in seconds
	ChunkDuration float64

	// SystemPrompt is an optional system prompt
	SystemPrompt string

	// UserPrompt is an optional custom user prompt
	UserPrompt string

	// SiteURL is your site URL (for OpenRouter rankings)
	SiteURL string

	// SiteName is your site name
	SiteName string
}

// NewOpenRouterProvider creates a new OpenRouter provider
func NewOpenRouterProvider(cfg *OpenRouterConfig) *OpenRouterProvider {
	if cfg.Model == "" {
		cfg.Model = OpenRouterModelGemini25Flash
	}
	if cfg.AudioFormat == "" {
		cfg.AudioFormat = "wav"
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.ChunkDuration == 0 {
		cfg.ChunkDuration = 30.0 // Gemini supports up to ~1 minute
	}
	if cfg.UserPrompt == "" {
		cfg.UserPrompt = "Please transcribe this audio file. Only output the transcribed text, no additional commentary."
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "You are a precise audio transcription assistant. Transcribe the provided audio accurately, preserving the original meaning and any relevant nuances."
	}

	providerConfig := &ProviderConfig{
		Type:           ProviderOpenRouter,
		APIKey:         cfg.APIKey,
		Model:          cfg.Model,
		SampleRate:     cfg.SampleRate,
		Channels:       1,
		Encoding:       WAV,
		ChunkDuration:  cfg.ChunkDuration,
		Overlap:        0.5,
		RequestTimeout: 120 * int64(time.Second), // Longer timeout for multimodal
	}

	o := &OpenRouterProvider{
		RESTProvider: NewRESTProvider(providerConfig),
		model:        cfg.Model,
		systemPrompt: cfg.SystemPrompt,
		userPrompt:   cfg.UserPrompt,
	}

	// Set the transcription function for virtual dispatch
	o.RESTProvider.doTranscribeFunc = o.doTranscribe

	return o
}

// OpenRouterRequest is the request structure
type OpenRouterRequest struct {
	Model    string              `json:"model"`
	Messages []OpenRouterMessage `json:"messages"`
	Stream   bool                `json:"stream"`
}

// OpenRouterMessage is a message in the request
type OpenRouterMessage struct {
	Role    string              `json:"role"`
	Content []OpenRouterContent `json:"content"`
}

// OpenRouterContent is content in a message
type OpenRouterContent struct {
	Type       string                `json:"type"`
	Text       string                `json:"text,omitempty"`
	InputAudio *OpenRouterInputAudio `json:"inputAudio,omitempty"`
}

// OpenRouterInputAudio is the audio input
type OpenRouterInputAudio struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}

// OpenRouterResponse is the response structure
type OpenRouterResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// OpenRouterError is an error response
type OpenRouterErrorResponse struct {
	ErrorResp struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (e *OpenRouterErrorResponse) Error() string {
	return e.ErrorResp.Message
}

// doTranscribe implements the RESTProvider interface
func (o *OpenRouterProvider) doTranscribe(ctx context.Context, audio []byte) (*TranscriptResult, error) {
	// Encode audio to base64
	base64Audio := base64.StdEncoding.EncodeToString(audio)

	// Build request
	req := OpenRouterRequest{
		Model: o.model,
		Messages: []OpenRouterMessage{
			{
				Role: "system",
				Content: []OpenRouterContent{
					{Type: "text", Text: o.systemPrompt},
				},
			},
			{
				Role: "user",
				Content: []OpenRouterContent{
					{Type: "text", Text: o.userPrompt},
					{
						Type: "input_audio",
						InputAudio: &OpenRouterInputAudio{
							Data:   base64Audio,
							Format: "wav",
						},
					},
				},
			},
		},
		Stream: false,
	}

	// Marshal request
	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := OpenRouterBaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.config.APIKey)
	httpReq.Header.Set("HTTP-Referer", "https://swarmos.ai") // For rankings
	httpReq.Header.Set("X-Title", "SwarmOS Voice")

	// Send request
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if resp.StatusCode >= 400 {
		var apiErr OpenRouterErrorResponse
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, &apiErr
		}
		return nil, fmt.Errorf("OpenRouter API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var orResp OpenRouterResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Extract transcription
	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("no transcription in response")
	}

	text := orResp.Choices[0].Message.Content

	// Calculate duration from audio size
	duration := float64(len(audio)) / float64(o.config.SampleRate*2) // 16-bit mono

	result := &TranscriptResult{
		Text:       text,
		IsFinal:    true,
		Duration:   duration,
		Confidence: 1.0,
	}

	return result, nil
}

// SetModel sets the model to use
func (o *OpenRouterProvider) SetModel(model string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.model = model
	o.config.Model = model
}

// SetPrompts sets custom system and user prompts
func (o *OpenRouterProvider) SetPrompts(systemPrompt, userPrompt string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if systemPrompt != "" {
		o.systemPrompt = systemPrompt
	}
	if userPrompt != "" {
		o.userPrompt = userPrompt
	}
}

// SetAudioFormat sets the audio format
func (o *OpenRouterProvider) SetAudioFormat(format string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Update the encoding based on format
	switch format {
	case "wav":
		o.config.Encoding = WAV
	case "mp3":
		o.config.Encoding = MP3
	case "flac":
		o.config.Encoding = FLAC
	case "ogg":
		o.config.Encoding = OGG
	}
}
