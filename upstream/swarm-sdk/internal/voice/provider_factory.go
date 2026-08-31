package voice

import (
	"context"
	"fmt"
	"os"
	"time"
)

// ProviderFactory creates STT providers based on configuration
type ProviderFactory struct {
	// DefaultProvider is the default provider type to use
	DefaultProvider ProviderType

	// APIKeys contains API keys for each provider
	APIKeys map[ProviderType]string

	// BaseURLs contains custom base URLs for each provider
	BaseURLs map[ProviderType]string

	// FallbackChain is the order of providers to try if one fails
	FallbackChain []ProviderType
}

// NewProviderFactory creates a new provider factory
func NewProviderFactory() *ProviderFactory {
	return &ProviderFactory{
		DefaultProvider: ProviderGroq, // Default to Groq (fast, affordable)
		APIKeys:         make(map[ProviderType]string),
		BaseURLs:        make(map[ProviderType]string),
		FallbackChain: []ProviderType{
			ProviderGroq,             // Try Groq first (fast, affordable)
			ProviderOpenAI,           // Then OpenAI
			ProviderOpenRouter,       // Then OpenRouter
			ProviderOpenAICompatible, // Then local/OpenAI-compatible endpoints
			ProviderREST,             // Finally a legacy custom REST server
		},
	}
}

// ProviderFactoryConfig contains configuration for the provider factory
type ProviderFactoryConfig struct {
	// DefaultProvider is the default provider type
	DefaultProvider ProviderType

	// APIKeys is a map of provider types to API keys
	APIKeys map[string]string

	// BaseURLs is a map of provider types to custom base URLs
	BaseURLs map[string]string

	// FallbackChain is the order of providers to try
	FallbackChain []ProviderType

	// Common configuration
	SampleRate           int
	Language             string
	ChunkDuration        float64
	EnableWordTimestamps bool
}

// NewProviderFactoryWithConfig creates a provider factory with configuration
func NewProviderFactoryWithConfig(cfg *ProviderFactoryConfig) *ProviderFactory {
	f := NewProviderFactory()

	if cfg.DefaultProvider != "" {
		f.DefaultProvider = cfg.DefaultProvider
	}

	if cfg.APIKeys != nil {
		for k, v := range cfg.APIKeys {
			f.APIKeys[ProviderType(k)] = v
		}
	}

	if cfg.BaseURLs != nil {
		for k, v := range cfg.BaseURLs {
			f.BaseURLs[ProviderType(k)] = v
		}
	}

	if len(cfg.FallbackChain) > 0 {
		f.FallbackChain = cfg.FallbackChain
	}

	return f
}

// Create creates a provider of the specified type
func (f *ProviderFactory) Create(providerType ProviderType, opts ...ProviderOption) (Provider, error) {
	apiKey := f.APIKeys[providerType]
	baseURL := f.BaseURLs[providerType]

	// Apply options
	cfg := &ProviderConfig{
		Type:           providerType,
		APIKey:         apiKey,
		BaseURL:        baseURL,
		SampleRate:     16000,
		Channels:       1,
		Encoding:       Linear16,
		ChunkDuration:  25.0,
		Language:       "en",
		RequestTimeout: 60000000000, // 60 seconds
		Extra:          make(map[string]any),
	}

	for _, opt := range opts {
		opt(cfg)
	}

	// Ensure API key is set
	if cfg.APIKey == "" {
		cfg.APIKey = f.getEnvAPIKey(providerType)
	}

	switch providerType {
	case ProviderGroq:
		return f.createGroqProvider(cfg)
	case ProviderOpenAI:
		return f.createOpenAIProvider(cfg)
	case ProviderOpenRouter:
		return f.createOpenRouterProvider(cfg)
	case ProviderWebSocket:
		return f.createWebSocketProvider(cfg)
	case ProviderDeepgram:
		return f.createDeepgramProvider(cfg)
	case ProviderREST:
		return f.createRESTProvider(cfg)
	case ProviderOpenAICompatible:
		return f.createOpenAICompatibleProvider(cfg)
	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}
}

// CreateDefault creates the default provider
func (f *ProviderFactory) CreateDefault(opts ...ProviderOption) (Provider, error) {
	return f.Create(f.DefaultProvider, opts...)
}

// CreateWithFallback creates a provider with fallback support
func (f *ProviderFactory) CreateWithFallback(opts ...ProviderOption) (Provider, error) {
	var lastErr error

	for _, providerType := range f.FallbackChain {
		provider, err := f.Create(providerType, opts...)
		if err != nil {
			lastErr = err
			continue
		}

		// Try to initialize
		ctx := context.Background()
		if err := provider.Initialize(ctx); err != nil {
			lastErr = err
			_ = provider.Close()
			continue
		}

		return provider, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all providers failed: %w", lastErr)
	}
	return nil, fmt.Errorf("no providers available")
}

func (f *ProviderFactory) createGroqProvider(cfg *ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("Groq API key required")
	}

	groqCfg := &GroqConfig{
		APIKey:               cfg.APIKey,
		Model:                GroqModelWhisperLargeV3Turbo,
		Language:             cfg.Language,
		SampleRate:           cfg.SampleRate,
		ResponseFormat:       "verbose_json",
		EnableWordTimestamps: cfg.EnableWordTimestamps,
		ChunkDuration:        cfg.ChunkDuration,
		Overlap:              0.5,
		Temperature:          0.0,
	}

	return NewGroqProvider(groqCfg), nil
}

func (f *ProviderFactory) createOpenAIProvider(cfg *ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OpenAI API key required")
	}

	openaiCfg := &OpenAIConfig{
		APIKey:               cfg.APIKey,
		Model:                OpenAIModelWhisper1,
		Language:             cfg.Language,
		SampleRate:           cfg.SampleRate,
		ResponseFormat:       "verbose_json",
		EnableWordTimestamps: cfg.EnableWordTimestamps,
		ChunkDuration:        cfg.ChunkDuration,
		Temperature:          0.0,
	}

	return NewOpenAIProvider(openaiCfg), nil
}

func (f *ProviderFactory) createOpenRouterProvider(cfg *ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("OpenRouter API key required")
	}

	orCfg := &OpenRouterConfig{
		APIKey:        cfg.APIKey,
		Model:         OpenRouterModelGemini25Flash,
		SampleRate:    cfg.SampleRate,
		AudioFormat:   "wav",
		ChunkDuration: cfg.ChunkDuration,
	}

	return NewOpenRouterProvider(orCfg), nil
}

func (f *ProviderFactory) createWebSocketProvider(cfg *ProviderConfig) (Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("API key required for WebSocket provider")
	}

	// Use the existing WebSocket client
	wsCfg := &Config{
		BaseURL:        cfg.BaseURL,
		AuthToken:      cfg.APIKey,
		SampleRate:     cfg.SampleRate,
		Channels:       cfg.Channels,
		Encoding:       Linear16,
		Language:       cfg.Language,
		ConnectTimeout: time.Duration(cfg.RequestTimeout),
	}

	client, err := NewClient(wsCfg)
	if err != nil {
		return nil, err
	}

	return &WebSocketProviderAdapter{client: client}, nil
}

func (f *ProviderFactory) createDeepgramProvider(cfg *ProviderConfig) (Provider, error) {
	// TODO: Implement direct Deepgram provider
	return nil, fmt.Errorf("Deepgram direct provider not yet implemented")
}

func (f *ProviderFactory) createOpenAICompatibleProvider(cfg *ProviderConfig) (Provider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = f.BaseURLs[ProviderOpenAICompatible]
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = os.Getenv("VOICE_OPENAI_COMPATIBLE_URL")
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("OpenAI-compatible provider requires a base URL")
	}
	return NewOpenAICompatibleProvider(cfg)
}

func (f *ProviderFactory) createRESTProvider(cfg *ProviderConfig) (Provider, error) {
	// Get base URL for REST provider
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = f.BaseURLs[ProviderREST]
	}
	if baseURL == "" {
		baseURL = os.Getenv("VOICE_REST_URL")
	}
	if baseURL == "" {
		return nil, fmt.Errorf("REST provider requires a base URL (set VOICE_REST_URL or configure BaseURL)")
	}

	// Create ProviderConfig for REST provider
	restCfg := &ProviderConfig{
		Type:           ProviderREST,
		BaseURL:        baseURL,
		APIKey:         cfg.APIKey,
		SampleRate:     cfg.SampleRate,
		Channels:       cfg.Channels,
		Language:       cfg.Language,
		ChunkDuration:  cfg.ChunkDuration,
		RequestTimeout: cfg.RequestTimeout,
		Encoding:       Linear16,
	}

	provider := NewRESTProvider(restCfg)
	// Set the transcription function for generic REST endpoint
	provider.doTranscribeFunc = provider.transcribeGenericREST

	return provider, nil
}

// getEnvAPIKey gets the API key from environment variables
func (f *ProviderFactory) getEnvAPIKey(providerType ProviderType) string {
	switch providerType {
	case ProviderGroq:
		return os.Getenv("GROQ_API_KEY")
	case ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	case ProviderOpenRouter:
		return os.Getenv("OPENROUTER_API_KEY")
	case ProviderDeepgram:
		return os.Getenv("DEEPGRAM_API_KEY")
	case ProviderWebSocket:
		return os.Getenv("ANTHROPIC_API_KEY")
	case ProviderREST:
		// REST provider may not need an API key if using local server
		return os.Getenv("VOICE_REST_API_KEY")
	case ProviderOpenAICompatible:
		return os.Getenv("VOICE_OPENAI_COMPATIBLE_API_KEY")
	default:
		return ""
	}
}

// ProviderOption is a function that modifies provider configuration
type ProviderOption func(*ProviderConfig)

// WithAPIKey sets the API key
func WithAPIKey(key string) ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.APIKey = key
	}
}

// WithBaseURL sets the base URL
func WithBaseURL(url string) ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.BaseURL = url
	}
}

// WithModel sets the model
func WithModel(model string) ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.Model = model
	}
}

// WithLanguage sets the language
func WithLanguage(lang string) ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.Language = lang
	}
}

// WithSampleRate sets the sample rate
func WithSampleRate(rate int) ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.SampleRate = rate
	}
}

// WithChunkDuration sets the chunk duration
func WithChunkDuration(seconds float64) ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.ChunkDuration = seconds
	}
}

// WithWordTimestamps enables word-level timestamps
func WithWordTimestamps() ProviderOption {
	return func(cfg *ProviderConfig) {
		cfg.EnableWordTimestamps = true
	}
}

// WebSocketProviderAdapter adapts the existing Client to the Provider interface
type WebSocketProviderAdapter struct {
	client *Client
}

func (a *WebSocketProviderAdapter) Initialize(ctx context.Context) error {
	// Connect to the WebSocket server
	return a.client.Connect(ctx)
}

func (a *WebSocketProviderAdapter) SendAudio(ctx context.Context, audio []byte) error {
	// Send audio chunk directly through the WebSocket connection
	// Access the internal ws field since we're in the same package
	if a.client.ws == nil {
		return fmt.Errorf("WebSocket connection not initialized")
	}
	return a.client.ws.SendAudio(audio)
}

func (a *WebSocketProviderAdapter) Flush(ctx context.Context) error {
	// Close the stream to get any remaining transcripts
	if a.client.ws == nil {
		return fmt.Errorf("WebSocket connection not initialized")
	}
	return a.client.ws.CloseStream()
}

func (a *WebSocketProviderAdapter) Close() error {
	return a.client.Close()
}

func (a *WebSocketProviderAdapter) Events() <-chan VoiceEvent {
	return a.client.Events()
}

func (a *WebSocketProviderAdapter) Name() string {
	return "websocket"
}

func (a *WebSocketProviderAdapter) IsStreaming() bool {
	return true
}
