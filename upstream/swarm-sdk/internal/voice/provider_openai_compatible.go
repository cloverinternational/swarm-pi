package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// OpenAICompatibleDefaultModel is served by the local NVIDIA NeMo Parakeet server.
	OpenAICompatibleDefaultModel = "nvidia/parakeet-tdt-0.6b-v3"

	openAICompatibleErrorBodyLimit   = 64 << 10
	openAICompatibleSuccessBodyLimit = 4 << 20
	openAICompatibleDefaultTimeout   = 120 * time.Second
)

// OpenAICompatibleProvider transcribes PCM16LE audio through an
// OpenAI-compatible /v1/audio/transcriptions endpoint.
type OpenAICompatibleProvider struct {
	*RESTProvider

	endpoint string
	model    string
}

// NewOpenAICompatibleProvider creates a provider from the common voice config,
// making it directly suitable for later ProviderFactory wiring. BaseURL is the
// server root (for example, http://127.0.0.1:8001); a trailing /v1 is accepted.
func NewOpenAICompatibleProvider(config *ProviderConfig) (*OpenAICompatibleProvider, error) {
	if config == nil {
		return nil, fmt.Errorf("OpenAI-compatible provider config is required")
	}

	endpoint, err := openAICompatibleTranscriptionURL(config.BaseURL)
	if err != nil {
		return nil, err
	}

	cfg := *config
	if cfg.Type == "" {
		cfg.Type = ProviderOpenAICompatible
	}
	if cfg.Model == "" {
		cfg.Model = OpenAICompatibleDefaultModel
	}
	if cfg.SampleRate == 0 {
		cfg.SampleRate = 16000
	}
	if cfg.Channels == 0 {
		cfg.Channels = 1
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = int64(openAICompatibleDefaultTimeout)
	}
	if cfg.ChunkDuration <= 0 {
		cfg.ChunkDuration = 60
	}
	cfg.Encoding = Linear16

	if cfg.SampleRate < 0 {
		return nil, fmt.Errorf("sample rate must be positive, got %d", cfg.SampleRate)
	}
	if cfg.Channels < 0 {
		return nil, fmt.Errorf("channels must be positive, got %d", cfg.Channels)
	}

	provider := &OpenAICompatibleProvider{
		RESTProvider: NewRESTProvider(&cfg),
		endpoint:     endpoint,
		model:        cfg.Model,
	}
	provider.RESTProvider.doTranscribeFunc = provider.doTranscribe
	return provider, nil
}

// Transcribe sends one raw PCM16LE buffer immediately and returns its result.
// Callers using the Provider interface can instead use Initialize, SendAudio,
// Flush, and Events through the embedded RESTProvider.
func (p *OpenAICompatibleProvider) Transcribe(ctx context.Context, pcm []byte) (*TranscriptResult, error) {
	return p.doTranscribe(ctx, pcm)
}

func (p *OpenAICompatibleProvider) doTranscribe(ctx context.Context, pcm []byte) (*TranscriptResult, error) {
	wav, err := EncodePCM16LEToWAV(pcm, p.config.SampleRate, p.config.Channels)
	if err != nil {
		return nil, fmt.Errorf("encode audio as WAV: %w", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return nil, fmt.Errorf("create audio multipart field: %w", err)
	}
	if _, err := part.Write(wav); err != nil {
		return nil, fmt.Errorf("write WAV multipart field: %w", err)
	}
	if err := writer.WriteField("model", p.model); err != nil {
		return nil, fmt.Errorf("write model multipart field: %w", err)
	}
	if p.config.Language != "" {
		if err := writer.WriteField("language", p.config.Language); err != nil {
			return nil, fmt.Errorf("write language multipart field: %w", err)
		}
	}
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return nil, fmt.Errorf("write response format multipart field: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close transcription multipart body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, &body)
	if err != nil {
		return nil, fmt.Errorf("create transcription request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send transcription request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message, readErr := readBoundedOpenAICompatibleError(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("OpenAI-compatible transcription error %s (read response: %v)", resp.Status, readErr)
		}
		return nil, fmt.Errorf("OpenAI-compatible transcription error %s: %s", resp.Status, message)
	}

	var response struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Words    []struct {
			Word       string  `json:"word"`
			Start      float64 `json:"start"`
			End        float64 `json:"end"`
			Confidence float64 `json:"confidence"`
		} `json:"words"`
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, openAICompatibleSuccessBodyLimit+1))
	if err != nil {
		return nil, fmt.Errorf("read transcription response: %w", err)
	}
	if len(data) > openAICompatibleSuccessBodyLimit {
		return nil, fmt.Errorf("transcription response exceeds %d bytes", openAICompatibleSuccessBodyLimit)
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode transcription response: %w", err)
	}

	result := &TranscriptResult{
		Text:       response.Text,
		IsFinal:    true,
		Language:   response.Language,
		Duration:   response.Duration,
		Confidence: 1,
	}
	if len(response.Words) > 0 {
		result.Words = make([]WordTimestamp, len(response.Words))
		for i, word := range response.Words {
			result.Words[i] = WordTimestamp{
				Word:       word.Word,
				Start:      word.Start,
				End:        word.End,
				Confidence: word.Confidence,
			}
		}
	}

	return result, nil
}

func openAICompatibleTranscriptionURL(baseURL string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("OpenAI-compatible base URL is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse OpenAI-compatible base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("OpenAI-compatible base URL must use http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("OpenAI-compatible base URL must include a host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("OpenAI-compatible base URL must not include a query or fragment")
	}

	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case strings.HasSuffix(path, "/v1/audio/transcriptions"):
		// Already a complete endpoint.
	case strings.HasSuffix(path, "/v1"):
		path += "/audio/transcriptions"
	default:
		path += "/v1/audio/transcriptions"
	}
	parsed.Path = path
	return parsed.String(), nil
}

func readBoundedOpenAICompatibleError(body io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(body, openAICompatibleErrorBodyLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > openAICompatibleErrorBodyLimit {
		return string(data[:openAICompatibleErrorBodyLimit]) + "… (truncated)", nil
	}
	return string(data), nil
}

var _ Provider = (*OpenAICompatibleProvider)(nil)
