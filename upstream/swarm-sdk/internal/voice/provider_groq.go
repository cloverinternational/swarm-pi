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
	GroqBaseURL = "https://api.groq.com/openai/v1"

	// Groq models
	GroqModelWhisperLargeV3       = "whisper-large-v3"
	GroqModelWhisperLargeV3Turbo  = "whisper-large-v3-turbo"
	GroqModelDistilWhisperLargeV3 = "distil-whisper-large-v3-en"
)

// GroqProvider implements STT using Groq's Whisper API
type GroqProvider struct {
	*RESTProvider

	// model is the Groq model to use
	model string

	// responseFormat specifies the response format
	responseFormat string

	// timestampGranularities specifies timestamp granularity
	timestampGranularities []string
}

// GroqConfig contains Groq-specific configuration
type GroqConfig struct {
	// APIKey is the Groq API key
	APIKey string

	// Model is the Whisper model to use
	Model string

	// Language is the expected language
	Language string

	// SampleRate is the audio sample rate
	SampleRate int

	// ResponseFormat is the response format (json, verbose_json, text)
	ResponseFormat string

	// EnableWordTimestamps enables word-level timestamps
	EnableWordTimestamps bool

	// ChunkDuration is the chunk duration in seconds
	ChunkDuration float64

	// Overlap is the chunk overlap in seconds
	Overlap float64

	// Temperature is the sampling temperature (0.0 to 1.0)
	Temperature float64
}

// NewGroqProvider creates a new Groq provider
func NewGroqProvider(cfg *GroqConfig) *GroqProvider {
	if cfg.Model == "" {
		cfg.Model = GroqModelWhisperLargeV3Turbo
	}
	if cfg.ResponseFormat == "" {
		cfg.ResponseFormat = "verbose_json"
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.ChunkDuration == 0 {
		cfg.ChunkDuration = 25.0 // Groq limit is 25MB, ~25 min at 16kHz
	}
	if cfg.Temperature == 0 {
		cfg.Temperature = 0.0
	}

	providerConfig := &ProviderConfig{
		Type:                 ProviderGroq,
		APIKey:               cfg.APIKey,
		Model:                cfg.Model,
		Language:             cfg.Language,
		SampleRate:           cfg.SampleRate,
		Channels:             1,
		Encoding:             FLAC,
		ChunkDuration:        cfg.ChunkDuration,
		Overlap:              cfg.Overlap,
		EnableWordTimestamps: cfg.EnableWordTimestamps,
		RequestTimeout:       60 * int64(time.Second),
	}

	g := &GroqProvider{
		RESTProvider:   NewRESTProvider(providerConfig),
		model:          cfg.Model,
		responseFormat: cfg.ResponseFormat,
	}

	if cfg.EnableWordTimestamps {
		g.timestampGranularities = []string{"word"}
	}

	// Set the transcription function for virtual dispatch
	g.RESTProvider.doTranscribeFunc = g.doTranscribe

	return g
}

// GroqTranscriptionResponse is the response from Groq's transcription API
type GroqTranscriptionResponse struct {
	Task     string        `json:"task"`
	Language string        `json:"language"`
	Duration float64       `json:"duration"`
	Text     string        `json:"text"`
	Words    []GroqWord    `json:"words,omitempty"`
	Segments []GroqSegment `json:"segments,omitempty"`
}

// GroqWord represents a word with timing
type GroqWord struct {
	Word  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// GroqSegment represents a transcription segment
type GroqSegment struct {
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
func (g *GroqProvider) doTranscribe(ctx context.Context, audio []byte) (*TranscriptResult, error) {
	// Convert audio to FLAC for better compression
	flacData, err := g.convertToFLAC(audio)
	if err != nil {
		// If conversion fails, use original data
		flacData = audio
	}

	// Create multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add file
	part, err := writer.CreateFormFile("file", "audio.flac")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	_, err = part.Write(flacData)
	if err != nil {
		return nil, fmt.Errorf("failed to write audio data: %w", err)
	}

	// Add other fields
	_ = writer.WriteField("model", g.model) // WriteField fails only if writer is closed; checked below
	_ = writer.WriteField("response_format", g.responseFormat)

	if g.config.Language != "" {
		_ = writer.WriteField("language", g.config.Language)
	}

	if len(g.timestampGranularities) > 0 {
		// Add timestamp_granularities
		for _, tg := range g.timestampGranularities {
			_ = writer.WriteField("timestamp_granularities[]", tg)
		}
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	url := GroqBaseURL + "/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+g.config.APIKey)

	// Send request
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Groq API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response
	var groqResp GroqTranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&groqResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Convert to TranscriptResult
	result := &TranscriptResult{
		Text:       groqResp.Text,
		IsFinal:    true,
		Language:   groqResp.Language,
		Duration:   groqResp.Duration,
		Confidence: 1.0, // Groq doesn't provide confidence
	}

	// Convert words if available
	if len(groqResp.Words) > 0 {
		result.Words = make([]WordTimestamp, len(groqResp.Words))
		for i, w := range groqResp.Words {
			result.Words[i] = WordTimestamp{
				Word:  w.Word,
				Start: w.Start,
				End:   w.End,
			}
		}
	}

	return result, nil
}

// convertToFLAC converts audio data to FLAC format
func (g *GroqProvider) convertToFLAC(audio []byte) ([]byte, error) {
	// For now, return the audio as-is
	// In production, you would use a library like github.com/mjibson/go-dsp/flac
	// or call ffmpeg to convert
	return audio, nil
}

// SetModel sets the Whisper model to use
func (g *GroqProvider) SetModel(model string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.model = model
	g.config.Model = model
}

// SetTemperature sets the sampling temperature
func (g *GroqProvider) SetTemperature(temp float64) {
	// Temperature would be added to the API request
	// Store it in config for use in doTranscribe
}
