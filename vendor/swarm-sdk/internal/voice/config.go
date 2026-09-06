package voice

import (
	"fmt"
	"net/url"
	"time"
)

// AudioEncoding represents the audio format for streaming
type AudioEncoding string

const (
	Linear16 AudioEncoding = "linear16"
	FLAC     AudioEncoding = "flac"
	MP3      AudioEncoding = "mp3"
	WAV      AudioEncoding = "wav"
	OGG      AudioEncoding = "ogg"
	WebM     AudioEncoding = "webm"
)

// Default configuration values
const (
	DefaultSampleRate = 16000 // 16kHz - standard for speech recognition
	DefaultChannels   = 1     // Mono audio
	DefaultLanguage   = "en"  // English
)

// Config holds the configuration for the voice client
type Config struct {
	// BaseURL is the API base URL (e.g., https://api.anthropic.com)
	BaseURL string

	// AuthToken is the authentication token
	AuthToken string

	// UserAgent is the user agent string
	UserAgent string

	// AppID is the application identifier
	AppID string

	// Audio settings
	SampleRate int           // Audio sample rate in Hz (default: 16000)
	Channels   int           // Number of audio channels (default: 1)
	Encoding   AudioEncoding // Audio encoding (default: linear16)

	// Transcription settings
	EndpointingMs int     // Silence detection threshold in ms (default: 300)
	UtteranceMs   int     // Maximum utterance duration in ms (default: 1000)
	Language      string  // Language code (default: "en")
	ChunkDuration float64 // Chunk duration for REST providers in seconds (default: 5.0)

	// Timeout settings
	ConnectTimeout time.Duration // Connection timeout
	SafetyTimeout  time.Duration // Safety timeout for operations
	NoDataTimeout  time.Duration // Timeout when no data is received
	KeepAliveInt   time.Duration // Keep-alive interval

	// Provider settings
	Provider string // STT provider (e.g., "deepgram-nova3")
	Model    string // Provider-specific transcription model

	// Additional options
	Keyterms        []string // Key terms for improved recognition
	UseConversation bool     // Use conversation mode
}

// DefaultConfig returns a Config with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		BaseURL:        "https://api.anthropic.com",
		SampleRate:     16000,
		Channels:       1,
		Encoding:       Linear16,
		EndpointingMs:  300,
		UtteranceMs:    1000,
		Language:       "en",
		ChunkDuration:  5.0, // 5 seconds for REST providers
		ConnectTimeout: 10 * time.Second,
		SafetyTimeout:  5 * time.Second,
		NoDataTimeout:  1500 * time.Millisecond,
		KeepAliveInt:   8 * time.Second,
		Provider:       "deepgram-nova3",
		UserAgent:      "SwarmSDK",
		AppID:          "swarm-sdk",
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.SampleRate < 8000 || c.SampleRate > 48000 {
		return fmt.Errorf("sample rate must be between 8000 and 48000")
	}

	if c.Channels < 1 || c.Channels > 2 {
		return fmt.Errorf("channels must be 1 or 2")
	}

	if c.Language == "" {
		return fmt.Errorf("language is required")
	}

	return nil
}

// ValidateForConnection validates the config for WebSocket connections
func (c *Config) ValidateForConnection() error {
	if err := c.Validate(); err != nil {
		return err
	}

	if c.BaseURL == "" {
		return fmt.Errorf("base URL is required")
	}

	if _, err := url.Parse(c.BaseURL); err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}

	if c.AuthToken == "" {
		return fmt.Errorf("auth token is required")
	}

	return nil
}

// ApplyDefaults applies default values to unset fields
func (c *Config) ApplyDefaults() {
	defaults := DefaultConfig()

	if c.BaseURL == "" {
		c.BaseURL = defaults.BaseURL
	}
	if c.SampleRate == 0 {
		c.SampleRate = defaults.SampleRate
	}
	if c.Channels == 0 {
		c.Channels = defaults.Channels
	}
	if c.Encoding == "" {
		c.Encoding = defaults.Encoding
	}
	if c.EndpointingMs == 0 {
		c.EndpointingMs = defaults.EndpointingMs
	}
	if c.UtteranceMs == 0 {
		c.UtteranceMs = defaults.UtteranceMs
	}
	if c.Language == "" {
		c.Language = defaults.Language
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = defaults.ConnectTimeout
	}
	if c.SafetyTimeout == 0 {
		c.SafetyTimeout = defaults.SafetyTimeout
	}
	if c.NoDataTimeout == 0 {
		c.NoDataTimeout = defaults.NoDataTimeout
	}
	if c.KeepAliveInt == 0 {
		c.KeepAliveInt = defaults.KeepAliveInt
	}
	if c.ChunkDuration == 0 {
		c.ChunkDuration = defaults.ChunkDuration
	}
	if c.Provider == "" {
		c.Provider = defaults.Provider
	}
	if c.UserAgent == "" {
		c.UserAgent = defaults.UserAgent
	}
	if c.AppID == "" {
		c.AppID = defaults.AppID
	}
}

// WebSocketEndpoint returns the WebSocket endpoint URL
func (c *Config) WebSocketEndpoint() string {
	return c.BaseURL + "/api/ws/speech_to_text/voice_stream"
}

// Clone creates a copy of the configuration
func (c *Config) Clone() *Config {
	return &Config{
		BaseURL:         c.BaseURL,
		AuthToken:       c.AuthToken,
		UserAgent:       c.UserAgent,
		AppID:           c.AppID,
		SampleRate:      c.SampleRate,
		Channels:        c.Channels,
		Encoding:        c.Encoding,
		EndpointingMs:   c.EndpointingMs,
		UtteranceMs:     c.UtteranceMs,
		Language:        c.Language,
		ChunkDuration:   c.ChunkDuration,
		ConnectTimeout:  c.ConnectTimeout,
		SafetyTimeout:   c.SafetyTimeout,
		NoDataTimeout:   c.NoDataTimeout,
		KeepAliveInt:    c.KeepAliveInt,
		Provider:        c.Provider,
		Model:           c.Model,
		Keyterms:        append([]string(nil), c.Keyterms...),
		UseConversation: c.UseConversation,
	}
}
