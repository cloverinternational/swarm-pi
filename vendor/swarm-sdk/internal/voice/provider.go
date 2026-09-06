package voice

import (
	"context"
	"io"
)

// ProviderType defines the type of STT provider
type ProviderType string

const (
	// ProviderWebSocket uses WebSocket streaming (Deepgram proxy)
	ProviderWebSocket ProviderType = "websocket"

	// ProviderGroq uses Groq's Whisper API (REST with chunking)
	ProviderGroq ProviderType = "groq"

	// ProviderOpenAI uses OpenAI's Whisper API (REST with chunking)
	ProviderOpenAI ProviderType = "openai"

	// ProviderOpenRouter uses OpenRouter's audio-capable models
	ProviderOpenRouter ProviderType = "openrouter"

	// ProviderDeepgram uses Deepgram's streaming API directly
	ProviderDeepgram ProviderType = "deepgram"

	// ProviderAssemblyAI uses AssemblyAI's streaming API
	ProviderAssemblyAI ProviderType = "assemblyai"

	// ProviderREST uses a generic REST endpoint (for local/custom servers)
	ProviderREST ProviderType = "rest"

	// ProviderOpenAICompatible uses the OpenAI audio transcription contract.
	// It supports local servers such as NVIDIA NeMo as well as compatible hosted endpoints.
	ProviderOpenAICompatible ProviderType = "openai-compatible"
)

// Provider is the interface that all STT providers must implement
type Provider interface {
	// Initialize sets up the provider with configuration
	Initialize(ctx context.Context) error

	// SendAudio sends audio data to the provider
	// For streaming providers, this sends chunks in real-time
	// For REST providers, this buffers data until Flush is called
	SendAudio(ctx context.Context, audio []byte) error

	// Flush signals that the current audio segment is complete
	// For REST providers, this triggers transcription of buffered audio
	Flush(ctx context.Context) error

	// Close terminates the provider connection and cleans up resources
	Close() error

	// Events returns the channel for receiving transcription events
	Events() <-chan VoiceEvent

	// Name returns the provider name for logging/debugging
	Name() string

	// IsStreaming returns true if the provider supports real-time streaming
	IsStreaming() bool
}

// StreamingProvider extends Provider for real-time streaming capabilities
type StreamingProvider interface {
	Provider

	// StartStreaming begins a streaming session
	StartStreaming(ctx context.Context) error

	// StopStreaming ends the streaming session
	StopStreaming(ctx context.Context) error
}

// ChunkingProvider extends Provider for REST-based chunking
type ChunkingProvider interface {
	Provider

	// SetChunkDuration sets the chunk duration in seconds
	SetChunkDuration(seconds float64)

	// SetOverlap sets the overlap between chunks in seconds
	SetOverlap(seconds float64)

	// GetBufferedDuration returns the current buffered audio duration
	GetBufferedDuration() float64
}

// ProviderConfig contains common configuration for all providers
type ProviderConfig struct {
	// Type specifies which provider to use
	Type ProviderType

	// APIKey is the authentication key for the provider
	APIKey string

	// BaseURL is the API endpoint (optional, uses default if empty)
	BaseURL string

	// Model specifies which model to use (provider-specific)
	Model string

	// Language specifies the expected language (e.g., "en", "es")
	Language string

	// SampleRate is the audio sample rate in Hz
	SampleRate int

	// Channels is the number of audio channels
	Channels int

	// Encoding is the audio encoding format
	Encoding AudioEncoding

	// ChunkDuration is the duration of each chunk for REST providers (seconds)
	ChunkDuration float64

	// Overlap is the overlap between chunks for REST providers (seconds)
	Overlap float64

	// EnableWordTimestamps enables word-level timestamps
	EnableWordTimestamps bool

	// EnableInterimResults enables interim/partial results
	EnableInterimResults bool

	// Timeout configuration
	ConnectTimeout int64 // nanoseconds
	RequestTimeout int64 // nanoseconds
	// Extra contains additional provider-specific options
	Extra map[string]any
}

// WordTimestamp represents a single word with timing information
type WordTimestamp struct {
	Word       string
	Start      float64
	End        float64
	Confidence float64
}

// ProviderError represents an error from a provider
type ProviderError struct {
	Provider ProviderType
	Code     string
	Message  string
	Retry    bool
}

func (e *ProviderError) Error() string {
	return string(e.Provider) + ": " + e.Code + " - " + e.Message
}

// NewProviderError creates a new provider error
func NewProviderError(provider ProviderType, code, message string, retry bool) *ProviderError {
	return &ProviderError{
		Provider: provider,
		Code:     code,
		Message:  message,
		Retry:    retry,
	}
}

// AudioBuffer manages buffered audio data for chunking providers
type AudioBuffer struct {
	data       []byte
	sampleRate int
	channels   int
}

// NewAudioBuffer creates a new audio buffer
func NewAudioBuffer(sampleRate, channels int) *AudioBuffer {
	return &AudioBuffer{
		data:       make([]byte, 0),
		sampleRate: sampleRate,
		channels:   channels,
	}
}

// Write appends audio data to the buffer
func (b *AudioBuffer) Write(p []byte) (n int, err error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

// Read reads all buffered data (implements io.Reader)
func (b *AudioBuffer) Read(p []byte) (n int, err error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	n = copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

// Bytes returns the buffered data
func (b *AudioBuffer) Bytes() []byte {
	return b.data
}

// Len returns the length of buffered data
func (b *AudioBuffer) Len() int {
	return len(b.data)
}

// Duration returns the duration of buffered audio in seconds
func (b *AudioBuffer) Duration() float64 {
	if b.sampleRate == 0 || b.channels == 0 {
		return 0
	}
	// 16-bit audio = 2 bytes per sample
	bytesPerSample := 2 * b.channels
	samples := float64(len(b.data)) / float64(bytesPerSample)
	return samples / float64(b.sampleRate)
}

// Clear clears the buffer
func (b *AudioBuffer) Clear() {
	b.data = b.data[:0]
}

// Truncate keeps only the last n bytes
func (b *AudioBuffer) Truncate(n int) {
	if n >= len(b.data) {
		return
	}
	b.data = b.data[len(b.data)-n:]
}

// Clone returns a copy of the buffer
func (b *AudioBuffer) Clone() *AudioBuffer {
	cpy := &AudioBuffer{
		sampleRate: b.sampleRate,
		channels:   b.channels,
		data:       make([]byte, len(b.data)),
	}
	copy(cpy.data, b.data)
	return cpy
}

// TranscriptResult represents a transcription result from any provider
type TranscriptResult struct {
	// Text is the transcribed text
	Text string

	// IsFinal indicates if this is a final (not interim) result
	IsFinal bool

	// Language is the detected or specified language
	Language string

	// Confidence is the confidence score (0.0 to 1.0)
	Confidence float64

	// Duration is the audio duration in seconds
	Duration float64

	// Start is the start time of the segment
	Start float64

	// End is the end time of the segment
	End float64

	// Words contains word-level timestamps (if enabled)
	Words []WordTimestamp
}
