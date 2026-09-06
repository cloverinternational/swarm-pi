package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

const (
	OpenAIBaseURL = "https://api.openai.com/v1"

	// OpenAI Whisper models
	OpenAIModelWhisper1 = "whisper-1"
)

// OpenAIProvider implements STT using OpenAI's Whisper API
type OpenAIProvider struct {
	*RESTProvider

	// model is the OpenAI model to use
	model string

	// responseFormat specifies the response format
	responseFormat string

	// temperature is the sampling temperature
	temperature float64
}

// OpenAIConfig contains OpenAI-specific configuration
type OpenAIConfig struct {
	// APIKey is the OpenAI API key
	APIKey string

	// Model is the Whisper model to use
	Model string

	// Language is the expected language
	Language string

	// SampleRate is the audio sample rate
	SampleRate int

	// ResponseFormat is the response format (json, verbose_json, text, srt, vtt)
	ResponseFormat string

	// EnableWordTimestamps enables word-level timestamps
	EnableWordTimestamps bool

	// ChunkDuration is the chunk duration in seconds
	ChunkDuration float64

	// Temperature is the sampling temperature (0.0 to 1.0)
	Temperature float64

	// Prompt is an optional prompt to guide transcription
	Prompt string
}

// NewOpenAIProvider creates a new OpenAI provider
func NewOpenAIProvider(cfg *OpenAIConfig) *OpenAIProvider {
	if cfg.Model == "" {
		cfg.Model = OpenAIModelWhisper1
	}
	if cfg.ResponseFormat == "" {
		cfg.ResponseFormat = "verbose_json"
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.ChunkDuration == 0 {
		cfg.ChunkDuration = 60.0 // OpenAI supports longer audio
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.0
	}

	providerConfig := &ProviderConfig{
		Type:                 ProviderOpenAI,
		APIKey:               cfg.APIKey,
		Model:                cfg.Model,
		Language:             cfg.Language,
		SampleRate:           cfg.SampleRate,
		Channels:             1,
		Encoding:             MP3, // OpenAI prefers MP3
		ChunkDuration:        cfg.ChunkDuration,
		Overlap:              1.0,
		EnableWordTimestamps: cfg.EnableWordTimestamps,
		RequestTimeout:       120 * int64(time.Second),
	}

	o := &OpenAIProvider{
		RESTProvider:   NewRESTProvider(providerConfig),
		model:          cfg.Model,
		responseFormat: cfg.ResponseFormat,
		temperature:    cfg.Temperature,
	}

	// Set the transcription function for virtual dispatch
	o.RESTProvider.doTranscribeFunc = o.doTranscribe

	return o
}

// OpenAITranscriptionResponse is the response from OpenAI's transcription API
type OpenAITranscriptionResponse struct {
	Task     string          `json:"task"`
	Language string          `json:"language"`
	Duration float64         `json:"duration"`
	Text     string          `json:"text"`
	Words    []OpenAIWord    `json:"words,omitempty"`
	Segments []OpenAISegment `json:"segments,omitempty"`
}

// OpenAIWord represents a word with timing
type OpenAIWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// OpenAISegment represents a transcription segment
type OpenAISegment struct {
	ID               int     `json:"id"`
	Seek             int     `json:"seek"`
	Start            float64 `json:"start"`
	End              float64 `json:"end"`
	Text             string  `json:"text"`
	Tokens           []int   `json:"tokens"`
	Temperature      float64 `json:"temperature"`
	AvgLogprob       float64 `json:"avg_logprob"`
	CompressionRatio float64 `json:"compression_ratio"`
	NoSpeechProb     float64 `json:"no_speech_prob"`
}

// doTranscribe implements the RESTProvider interface
func (o *OpenAIProvider) doTranscribe(ctx context.Context, audio []byte) (*TranscriptResult, error) {
	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	part, err := writer.CreateFormFile("file", "audio.mp3")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	_, err = part.Write(audio)
	if err != nil {
		return nil, fmt.Errorf("failed to write audio data: %w", err)
	}

	// Add other fields
	_ = writer.WriteField("model", o.model)
	_ = writer.WriteField("response_format", o.responseFormat)

	if o.config.Language != "" {
		_ = writer.WriteField("language", o.config.Language)
	}

	if o.temperature > 0 {
		_ = writer.WriteField("temperature", fmt.Sprintf("%.2f", o.temperature))
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	url := OpenAIBaseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+o.config.APIKey)

	// Send request
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response based on format
	if o.responseFormat == "text" {
		text, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
		return &TranscriptResult{
			Text:     string(text),
			IsFinal:  true,
			Duration: float64(len(audio)) / float64(o.config.SampleRate*2),
		}, nil
	}

	var openaiResp OpenAITranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&openaiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to TranscriptResult
	result := &TranscriptResult{
		Text:       openaiResp.Text,
		IsFinal:    true,
		Language:   openaiResp.Language,
		Duration:   openaiResp.Duration,
		Confidence: 1.0,
	}

	// Convert words if available
	if len(openaiResp.Words) > 0 {
		result.Words = make([]WordTimestamp, len(openaiResp.Words))
		for i, w := range openaiResp.Words {
			result.Words[i] = WordTimestamp{
				Word:  w.Word,
				Start: w.Start,
				End:   w.End,
			}
		}
	}

	return result, nil
}

// SetModel sets the model to use
func (o *OpenAIProvider) SetModel(model string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.model = model
	o.config.Model = model
}

// SetTemperature sets the sampling temperature
func (o *OpenAIProvider) SetTemperature(temp float64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.temperature = temp
}
