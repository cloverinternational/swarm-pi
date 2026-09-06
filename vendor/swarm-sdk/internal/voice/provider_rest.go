package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sync"
	"time"
)

// RESTProvider is a base implementation for REST-based STT providers
// that use chunked audio uploads
type RESTProvider struct {
	config      *ProviderConfig
	buffer      *AudioBuffer
	events      chan VoiceEvent
	client      *http.Client
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	isRunning   bool
	isClosed    bool
	closeDone   chan struct{}
	shutdown    chan struct{}
	chunkNum    int
	wg          sync.WaitGroup
	publishTail chan struct{}
	// doTranscribeFunc is the provider-specific transcription function
	// Concrete providers should assign their implementation to this field
	doTranscribeFunc func(ctx context.Context, audio []byte) (*TranscriptResult, error)
}

// NewRESTProvider creates a new REST-based provider
func NewRESTProvider(config *ProviderConfig) *RESTProvider {
	if config.ChunkDuration <= 0 {
		config.ChunkDuration = 10.0 // Default 10 second chunks
	}
	if config.Overlap <= 0 {
		config.Overlap = 0.5 // Default 0.5 second overlap
	}

	publishTail := make(chan struct{})
	close(publishTail)

	return &RESTProvider{
		config:      config,
		buffer:      NewAudioBuffer(config.SampleRate, config.Channels),
		events:      make(chan VoiceEvent, 100),
		closeDone:   make(chan struct{}),
		shutdown:    make(chan struct{}),
		publishTail: publishTail,
		client: &http.Client{
			Timeout: time.Duration(config.RequestTimeout) * time.Nanosecond,
		},
	}
}

// Initialize sets up the provider
func (p *RESTProvider) Initialize(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosed {
		return fmt.Errorf("provider closed")
	}
	if p.isRunning {
		return fmt.Errorf("provider already initialized")
	}

	p.ctx, p.cancel = context.WithCancel(ctx)
	p.isRunning = true

	select {
	case p.events <- EventReady{Provider: p.config.Type}:
		return nil
	case <-p.ctx.Done():
		p.isRunning = false
		return p.ctx.Err()
	}
}

// SendAudio adds audio data to the buffer
func (p *RESTProvider) SendAudio(ctx context.Context, audio []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isRunning {
		return fmt.Errorf("provider not initialized")
	}

	_, err := p.buffer.Write(audio)
	if err != nil {
		return fmt.Errorf("failed to buffer audio: %w", err)
	}

	// Check if we should flush (chunk duration reached).
	// Intermediate chunks retain a small overlap to avoid clipping words.
	if p.buffer.Duration() >= p.config.ChunkDuration {
		return p.flushInternal(true)
	}

	return nil
}

// Flush triggers transcription of buffered audio
func (p *RESTProvider) Flush(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.isRunning {
		return fmt.Errorf("provider not initialized")
	}

	if p.buffer.Len() == 0 {
		return nil
	}

	return p.flushInternal(false)
}

// flushInternal sends buffered audio for transcription. Intermediate chunks may
// retain overlap; a final flush always clears the buffer so Close cannot submit
// the same tail twice.
// Must be called with lock held.
func (p *RESTProvider) flushInternal(retainOverlap bool) error {
	if p.buffer.Len() == 0 {
		return nil
	}

	p.chunkNum++

	// Clone buffer for async processing
	audioData := p.buffer.Clone()

	// Keep overlap only between intermediate requests. Final flushes consume the
	// entire buffer so shutdown is exactly-once.
	overlapBytes := int(float64(p.config.SampleRate*p.config.Channels*2) * p.config.Overlap)
	if retainOverlap && overlapBytes > 0 && audioData.Len() > overlapBytes {
		p.buffer.Truncate(overlapBytes)
	} else {
		p.buffer.Clear()
	}

	// Requests run asynchronously, but each chunk waits for the preceding chunk's
	// publication turn before emitting its terminal event.
	publishAfter := p.publishTail
	publishDone := make(chan struct{})
	p.publishTail = publishDone
	p.wg.Add(1)
	go p.transcribeChunk(p.ctx, audioData, p.chunkNum, publishAfter, publishDone)

	return nil
}

// transcribeChunk sends a chunk to the API and processes the response
func (p *RESTProvider) transcribeChunk(
	ctx context.Context,
	audio *AudioBuffer,
	chunkNum int,
	publishAfter <-chan struct{},
	publishDone chan<- struct{},
) {
	defer p.wg.Done()
	defer close(publishDone)

	// Use the provider-specific transcription function
	result, err := p.doTranscribeFunc(ctx, audio.Bytes())
	<-publishAfter
	if err != nil {
		p.emit(ctx, EventError{Error: err})
		return
	}

	if result.Text != "" {
		p.emit(ctx, EventFinal{
			Text:      result.Text,
			StartTime: result.Start,
			EndTime:   result.End,
		})
	}
}

func (p *RESTProvider) emit(ctx context.Context, event VoiceEvent) {
	// Preserve normal final events whenever the consumer has capacity, including
	// during Close. Shutdown only abandons a send that would otherwise block.
	select {
	case p.events <- event:
		return
	default:
	}
	select {
	case p.events <- event:
	case <-ctx.Done():
	case <-p.shutdown:
	}
}

// Close cleans up the provider.
func (p *RESTProvider) Close() error {
	p.mu.Lock()
	if p.isClosed {
		done := p.closeDone
		p.mu.Unlock()
		<-done
		return nil
	}
	p.isClosed = true

	// Flush remaining audio exactly once before preventing further sends.
	if p.isRunning && p.buffer.Len() > 0 {
		_ = p.flushInternal(false)
	}
	p.isRunning = false
	close(p.shutdown)
	cancel := p.cancel
	p.mu.Unlock()

	// On normal completion this preserves final results. If the owning session
	// was canceled, transcriptions and event sends observe that cancellation.
	p.wg.Wait()
	if cancel != nil {
		cancel()
	}
	close(p.events)
	close(p.closeDone)
	return nil
}

// Events returns the events channel
func (p *RESTProvider) Events() <-chan VoiceEvent {
	return p.events
}

// Name returns the provider name
func (p *RESTProvider) Name() string {
	return string(p.config.Type)
}

// IsStreaming returns false for REST providers
func (p *RESTProvider) IsStreaming() bool {
	return false
}

// SetChunkDuration sets the chunk duration
func (p *RESTProvider) SetChunkDuration(seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config.ChunkDuration = seconds
}

// SetOverlap sets the overlap duration
func (p *RESTProvider) SetOverlap(seconds float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config.Overlap = seconds
}

// GetBufferedDuration returns the current buffered duration
func (p *RESTProvider) GetBufferedDuration() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buffer.Duration()
}

// transcribeGenericREST sends audio to a generic REST endpoint
// This is used for local/custom transcription servers
func (p *RESTProvider) transcribeGenericREST(ctx context.Context, audio []byte) (*TranscriptResult, error) {
	// Create multipart form request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add audio file part
	part, err := writer.CreateFormFile("audio", "audio.wav")
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return nil, fmt.Errorf("failed to write audio data: %w", err)
	}

	// Add optional parameters
	if p.config.Language != "" {
		_ = writer.WriteField("language", p.config.Language)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, "POST", p.config.BaseURL+"/transcribe", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	if p.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	}

	// Send request
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse response - support multiple response formats
	var result TranscriptResult
	var genericResp struct {
		Text       string  `json:"text"`
		Transcript string  `json:"transcript"`
		Message    string  `json:"message"`
		Start      float64 `json:"start"`
		End        float64 `json:"end"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&genericResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Handle various response formats
	result.Text = genericResp.Text
	if result.Text == "" {
		result.Text = genericResp.Transcript
	}
	if result.Text == "" {
		result.Text = genericResp.Message
	}
	result.Start = genericResp.Start
	result.End = genericResp.End

	return &result, nil
}
