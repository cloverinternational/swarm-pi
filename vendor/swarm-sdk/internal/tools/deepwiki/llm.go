package deepwiki

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai/profiles"
)

// LLMClient is the interface for generating text from an LLM.
type LLMClient interface {
	// Generate sends a prompt and returns the full response text.
	Generate(ctx context.Context, system, prompt string) (string, error)
	// GenerateStream sends a prompt and streams response chunks to a callback.
	GenerateStream(ctx context.Context, system, prompt string, onChunk func(text string)) error
	// IsAvailable checks if the LLM backend is reachable.
	IsAvailable() bool
	// Provider returns the provider name (e.g., "anthropic", "openai", "ollama").
	Provider() string
	// ModelName returns the model name.
	// ModelName returns the model name.
	ModelName() string
}

// LoggingLLMClient wraps an LLMClient and logs all requests.
type LoggingLLMClient struct {
	inner  LLMClient
	logger *RequestLogger
	phase  string
	stage  string
}

// NewLoggingLLMClient creates a logging wrapper around an LLMClient.
func NewLoggingLLMClient(inner LLMClient, logger *RequestLogger, phase, stage string) *LoggingLLMClient {
	return &LoggingLLMClient{
		inner:  inner,
		logger: logger,
		phase:  phase,
		stage:  stage,
	}
}

func (c *LoggingLLMClient) Generate(ctx context.Context, system, prompt string) (string, error) {
	t0 := time.Now()
	out, err := c.inner.Generate(ctx, system, prompt)
	duration := time.Since(t0)

	if c.logger != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		c.logger.Log(&RequestLog{
			Timestamp: t0,
			Phase:     c.phase,
			Stage:     c.stage,
			Provider:  c.inner.Provider(),
			Model:     c.inner.ModelName(),
			Request: RequestPayload{
				System: system,
				User:   prompt,
			},
			Response: ResponsePayload{
				Raw: out,
			},
			Timing: TimingInfo{
				Start:    t0,
				End:      time.Now(),
				Duration: duration,
			},
			Metadata: map[string]any{
				"error": errMsg,
			},
		})
	}

	return out, err
}

func (c *LoggingLLMClient) GenerateStream(ctx context.Context, system, prompt string, onChunk func(text string)) error {
	// For streaming, we don't log each chunk - just the overall request
	t0 := time.Now()
	err := c.inner.GenerateStream(ctx, system, prompt, onChunk)
	duration := time.Since(t0)

	if c.logger != nil {
		errMsg := ""
		if err != nil {
			errMsg = err.Error()
		}
		c.logger.Log(&RequestLog{
			Timestamp: t0,
			Phase:     c.phase,
			Stage:     c.stage + "_stream",
			Provider:  c.inner.Provider(),
			Model:     c.inner.ModelName(),
			Request: RequestPayload{
				System: system,
				User:   prompt,
			},
			Timing: TimingInfo{
				Start:    t0,
				End:      time.Now(),
				Duration: duration,
			},
			Metadata: map[string]any{
				"error": errMsg,
			},
		})
	}

	return err
}

func (c *LoggingLLMClient) IsAvailable() bool {
	return c.inner.IsAvailable()
}

func (c *LoggingLLMClient) Provider() string {
	return c.inner.Provider()
}

func (c *LoggingLLMClient) ModelName() string {
	return c.inner.ModelName()
}

// OllamaLLM implements LLMClient using the Ollama REST API.
type OllamaLLM struct {
	BaseURL string
	Model   string
	client  *http.Client
}

// NewOllamaLLM creates an Ollama LLM client.
func NewOllamaLLM(model string) *OllamaLLM {
	baseURL := os.Getenv("OLLAMA_HOST")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &OllamaLLM{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		client:  &http.Client{Timeout: 15 * time.Minute},
	}
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Options  map[string]any      `json:"options,omitempty"`
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
	Done bool `json:"done"`
}

func (o *OllamaLLM) Generate(ctx context.Context, system, prompt string) (string, error) {
	t0 := time.Now()
	log.Printf("[deepwiki/llm] → ollama model=%s url=%s prompt=%d chars", o.Model, o.BaseURL, len(system)+len(prompt))
	messages := []ollamaChatMessage{}
	if system != "" {
		messages = append(messages, ollamaChatMessage{Role: "system", Content: system})
	}
	messages = append(messages, ollamaChatMessage{Role: "user", Content: prompt})

	body := ollamaChatRequest{
		Model:    o.Model,
		Messages: messages,
		Stream:   false,
		Options: map[string]any{
			"temperature": 0.7,
			"num_ctx":     8192,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		log.Printf("[deepwiki/llm] ✗ ollama model=%s err=%v (%s)", o.Model, err, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		logErr := fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
		log.Printf("[deepwiki/llm] ✗ ollama model=%s err=%v (%s)", o.Model, logErr, time.Since(t0).Round(time.Millisecond))
		return "", logErr
	}

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		log.Printf("[deepwiki/llm] ✗ ollama model=%s decode err=%v (%s)", o.Model, err, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("decode response: %w", err)
	}

	log.Printf("[deepwiki/llm] ✓ ollama model=%s response=%d chars (%s)", o.Model, len(chatResp.Message.Content), time.Since(t0).Round(time.Millisecond))
	return chatResp.Message.Content, nil
}

func (o *OllamaLLM) GenerateStream(ctx context.Context, system, prompt string, onChunk func(string)) error {
	messages := []ollamaChatMessage{}
	if system != "" {
		messages = append(messages, ollamaChatMessage{Role: "system", Content: system})
	}
	messages = append(messages, ollamaChatMessage{Role: "user", Content: prompt})

	body := ollamaChatRequest{
		Model:    o.Model,
		Messages: messages,
		Stream:   true,
		Options: map[string]any{
			"temperature": 0.7,
			"num_ctx":     8192,
		},
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(respBody))
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk ollamaChatResponse
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("decode stream chunk: %w", err)
		}
		if chunk.Message.Content != "" {
			onChunk(chunk.Message.Content)
		}
		if chunk.Done {
			break
		}
	}

	return nil
}

func (o *OllamaLLM) IsAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.BaseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (o *OllamaLLM) Provider() string  { return "ollama" }
func (o *OllamaLLM) ModelName() string { return o.Model }

// --- OpenAI-Compatible LLM Client ---

// OpenAILLM implements LLMClient using the OpenAI chat completions API.
// Works with OpenAI and any OpenAI-compatible endpoint (Cerebras, Groq, OpenRouter, …).
type OpenAILLM struct {
	BaseURL string
	APIKey  string
	Model   string
	// authHeaderName overrides "Authorization" when a provider uses a different
	// header name (e.g. Azure uses "api-key").
	authHeaderName string
	// authHeaderValue is the pre-formatted header value (e.g. "Bearer sk-xxx").
	// When empty, defaults to "Bearer {APIKey}".
	authHeaderValue string
	client          *http.Client
}

// setAuthHeader applies the correct authentication header to r.
func (o *OpenAILLM) setAuthHeader(r *http.Request) {
	if o.APIKey == "" && o.authHeaderValue == "" {
		return
	}
	name := o.authHeaderName
	if name == "" {
		name = "Authorization"
	}
	value := o.authHeaderValue
	if value == "" {
		value = "Bearer " + o.APIKey
	}
	r.Header.Set(name, value)
}

// NewOpenAILLM creates an OpenAI-compatible LLM client.
func NewOpenAILLM(model string) *OpenAILLM {
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	apiKey := os.Getenv("OPENAI_API_KEY")

	return &OpenAILLM{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		client:  &http.Client{Timeout: 15 * time.Minute},
	}
}

type openaiRequest struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature,omitempty"`
}

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func (o *OpenAILLM) Generate(ctx context.Context, system, prompt string) (string, error) {
	t0 := time.Now()
	log.Printf("[deepwiki/llm] → openai model=%s url=%s prompt=%d chars", o.Model, o.BaseURL, len(system)+len(prompt))
	messages := []openaiMessage{}
	if system != "" {
		messages = append(messages, openaiMessage{Role: "system", Content: system})
	}
	messages = append(messages, openaiMessage{Role: "user", Content: prompt})

	body := openaiRequest{
		Model:       o.Model,
		Messages:    messages,
		Stream:      false,
		Temperature: 0.7,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	o.setAuthHeader(req)

	resp, err := o.client.Do(req)
	if err != nil {
		log.Printf("[deepwiki/llm] ✗ openai model=%s url=%s err=%v (%s)", o.Model, o.BaseURL, err, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		logErr := fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
		log.Printf("[deepwiki/llm] ✗ openai model=%s url=%s err=%v (%s)", o.Model, o.BaseURL, logErr, time.Since(t0).Round(time.Millisecond))
		return "", logErr
	}

	var chatResp openaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		log.Printf("[deepwiki/llm] ✗ openai model=%s decode err=%v (%s)", o.Model, err, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		log.Printf("[deepwiki/llm] ✗ openai model=%s no choices (%s)", o.Model, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("no choices in response")
	}

	log.Printf("[deepwiki/llm] ✓ openai model=%s url=%s response=%d chars (%s)", o.Model, o.BaseURL, len(chatResp.Choices[0].Message.Content), time.Since(t0).Round(time.Millisecond))
	return chatResp.Choices[0].Message.Content, nil
}

func (o *OpenAILLM) GenerateStream(ctx context.Context, system, prompt string, onChunk func(string)) error {
	messages := []openaiMessage{}
	if system != "" {
		messages = append(messages, openaiMessage{Role: "system", Content: system})
	}
	messages = append(messages, openaiMessage{Role: "user", Content: prompt})

	body := openaiRequest{
		Model:       o.Model,
		Messages:    messages,
		Stream:      true,
		Temperature: 0.7,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	o.setAuthHeader(req)

	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("openai request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai returned %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse SSE stream
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			lines := strings.SplitSeq(string(buf[:n]), "\n")
			for line := range lines {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				payload := strings.TrimPrefix(line, "data: ")
				if payload == "[DONE]" {
					return nil
				}
				var chunk openaiResponse
				if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
					continue
				}
				if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
					onChunk(chunk.Choices[0].Delta.Content)
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read stream: %w", err)
		}
	}

	return nil
}

func (o *OpenAILLM) IsAvailable() bool {
	// API key is sufficient for cloud providers.
	// A non-empty BaseURL without a key is sufficient for local no-auth providers.
	return o.APIKey != "" || o.BaseURL != ""
}

func (o *OpenAILLM) Provider() string  { return "openai" }
func (o *OpenAILLM) ModelName() string { return o.Model }

// NewLLMClient creates the appropriate LLM client based on provider string.
//
// Named providers with first-class support:
//   - ollama               — local Ollama server
//   - openai               — OpenAI (respects OPENAI_BASE_URL / OPENAI_API_KEY)
//   - anthropic / claude   — Anthropic (tries OAuth file, then ANTHROPIC_API_KEY)
//   - gemini / google      — Google Gemini (GEMINI_API_KEY or GOOGLE_API_KEY)
//
// Any other name is matched against the SDK's OpenAI-compatible provider
// profiles (cerebras, groq, openrouter, glm, together, fireworks, perplexity…).
// API key is read from <UPPER_PROVIDER>_API_KEY, falling back to OPENAI_API_KEY.
func NewLLMClient(provider, model string) (LLMClient, error) {
	switch strings.ToLower(provider) {
	case "ollama":
		if model == "" {
			model = "qwen3:1.7b"
		}
		client := NewOllamaLLM(model)
		if !client.IsAvailable() {
			return nil, fmt.Errorf("ollama is not available at %s", client.BaseURL)
		}
		return client, nil

	case "openai":
		if model == "" {
			model = "gpt-4o-mini"
		}
		client := NewOpenAILLM(model)
		if !client.IsAvailable() {
			// Env var not set — try credentials.json via SwarmOS config.
			if c, err := newClientFromSwarmOSConfig("openai", model); err == nil {
				return c, nil
			}
			return nil, fmt.Errorf("no OPENAI_API_KEY set for provider %q", provider)
		}
		return client, nil

	case "anthropic", "claude":
		if model == "" {
			model = "claude-haiku-4-5-20251001"
		}
		// Try OAuth file first (daemon-safe), then ANTHROPIC_API_KEY env var.
		client, err := NewLLMFromOAuthFile(model)
		if err != nil {
			return nil, fmt.Errorf("no Anthropic credentials for provider %q: %w", provider, err)
		}
		return client, nil

	case "gemini", "google":
		if model == "" {
			model = "gemini-2.5-flash-preview-05-20"
		}
		client := NewGeminiLLM(model)
		if !client.IsAvailable() {
			return nil, fmt.Errorf("no GEMINI_API_KEY or GOOGLE_API_KEY set for provider %q", provider)
		}
		return client, nil

	default:
		// Look up the provider in the SDK's OpenAI-compatible profiles registry.
		// This handles cerebras, groq, openrouter, glm, together, fireworks,
		// perplexity, and any future providers added to profiles.toml.
		return newOpenAICompatibleClient(provider, model)
	}
}

// --- OpenAI-compatible profile-based factory ---

// profileRegistrySingleton caches the loaded profile registry.
var (
	_profileReg     *profiles.Registry
	_profileRegOnce sync.Once
	_profileRegErr  error
)

func profilesRegistry() (*profiles.Registry, error) {
	_profileRegOnce.Do(func() {
		_profileReg, _profileRegErr = profiles.LoadBuiltinProfiles()
	})
	return _profileReg, _profileRegErr
}

// newOpenAICompatibleClient resolves an LLM client for any OpenAI-compatible
// provider using a two-stage lookup:
//
//  1. SDK built-in profiles (profiles.toml) — handles cerebras, groq, openrouter,
//     glm, together, fireworks, perplexity, …
//
//  2. SwarmOS user config (~/.swarm/config/providers.json + credentials.json) — handles
//     "local", "inceptionlabs", "z.ai", and any other custom provider the user
//     has configured in the TUI settings.
//
// API key lookup order for profile-based providers:
//
//	<UPPER_PROVIDER>_API_KEY → OPENAI_API_KEY (legacy fallback)
//
// Local/no-auth providers (base URL only, empty key) are accepted.
func newOpenAICompatibleClient(provider, model string) (LLMClient, error) {
	if model == "" {
		model = "default"
	}

	// Stage 1: SDK built-in profiles.
	if client, err := newClientFromProfile(provider, model); err == nil {
		return client, nil
	}

	// Stage 2: SwarmOS user config.
	if client, err := newClientFromSwarmOSConfig(provider, model); err == nil {
		return client, nil
	}

	// Build helpful error listing all known sources.
	reg, _ := profilesRegistry()
	profileNames := []string{}
	if reg != nil {
		profileNames = reg.List()
	}
	return nil, fmt.Errorf(
		"unsupported LLM provider: %q — known providers: ollama, openai, anthropic, gemini; "+
			"SDK profiles: %s; "+
			"or configure a custom provider in SwarmOS settings",
		provider, strings.Join(profileNames, ", "),
	)
}

// newClientFromProfile tries to build an OpenAILLM from the SDK's built-in profiles.toml.
func newClientFromProfile(provider, model string) (LLMClient, error) {
	reg, err := profilesRegistry()
	if err != nil {
		return nil, err
	}

	profile, ok := reg.Get(strings.ToLower(provider))
	if !ok {
		return nil, fmt.Errorf("provider %q not in profiles", provider)
	}

	// API key: <UPPER_PROVIDER>_API_KEY → OPENAI_API_KEY (legacy fallback)
	envKey := strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_API_KEY"
	apiKey := os.Getenv(envKey)
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("no API key for profile %q: set %s", provider, envKey)
	}

	// Base URL from profile with optional env override.
	baseURL := strings.TrimRight(profile.BaseURL, "/")
	envBase := strings.ToUpper(strings.ReplaceAll(provider, "-", "_")) + "_BASE_URL"
	if override := os.Getenv(envBase); override != "" {
		baseURL = strings.TrimRight(override, "/")
	}

	// Auth header from profile config.
	authName := profile.Auth.HeaderName
	if authName == "" {
		authName = "Authorization"
	}
	authValue := strings.ReplaceAll(profile.Auth.HeaderFormat, "{api_key}", apiKey)
	if authValue == profile.Auth.HeaderFormat {
		authValue = "Bearer " + apiKey
	}

	return &OpenAILLM{
		BaseURL:         baseURL,
		APIKey:          apiKey,
		Model:           model,
		authHeaderName:  authName,
		authHeaderValue: authValue,
		client:          &http.Client{Timeout: 15 * time.Minute},
	}, nil
}

// newClientFromSwarmOSConfig resolves a provider via the canonical SwarmOS
// configuration bridge.  Resolution order (highest priority first):
//
//  1. Provider-specific environment variable (e.g. <PROVIDER>_API_KEY)
//  2. credentials.json in ~/.swarm/config
//  3. base_url from providers.json in ~/.swarm/config (for local/no-auth providers)
//
// An empty API key is allowed for local/no-auth providers that only need a
// base URL.
func newClientFromSwarmOSConfig(provider, model string) (LLMClient, error) {
	sharedConfigDir := paths.Config()

	// Build a config manager for the shared dir so the bridge can look up
	// providers.json and credentials.json without re-reading the files manually.
	cfgMgr := core.NewFileConfigManager(sharedConfigDir, core.OsFileSystem())
	if err := cfgMgr.Load(context.Background()); err != nil {
		// Non-fatal: some files may simply not exist yet.
		log.Printf("[deepwiki/llm] warning: load swarm config: %v", err)
	}

	auth, err := core.ResolveProviderAuth(provider, cfgMgr)
	if err != nil {
		return nil, fmt.Errorf("provider %q: %w", provider, err)
	}
	if auth.BaseURL == "" {
		return nil, fmt.Errorf("provider %q not found in ~/.swarm/config/providers.json", provider)
	}

	return &OpenAILLM{
		BaseURL: auth.BaseURL,
		APIKey:  auth.APIKey,
		Model:   model,
		client:  &http.Client{Timeout: 15 * time.Minute},
	}, nil
}

// ---------------------------------------------------------------------------
// Gemini LLM Client (via Google's OpenAI-compatible endpoint)
// ---------------------------------------------------------------------------

// NewGeminiLLM creates a Gemini LLM client using Google's OpenAI-compat endpoint.
// Reads GEMINI_API_KEY (preferred) or GOOGLE_API_KEY.
func NewGeminiLLM(model string) *OpenAILLM {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if model == "" {
		model = "gemini-2.5-flash-preview-05-20"
	}
	return &OpenAILLM{
		BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
		APIKey:  apiKey,
		Model:   model,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

// NewDefaultFallbackLLM builds the auto-fallback LLM used when the primary
// provider hits a timeout or context-length error.
//
// Priority order:
//  1. Gemini 2.5 Flash (GEMINI_API_KEY / GOOGLE_API_KEY)
//  2. OpenAI gpt-4o-mini (OPENAI_API_KEY)
//
// Returns nil if no fallback credentials are available — callers must handle
// a nil fallback gracefully (no fallback → fail after primary retries).
func NewDefaultFallbackLLM() LLMClient {
	if key := os.Getenv("GEMINI_API_KEY"); key == "" {
		if key2 := os.Getenv("GOOGLE_API_KEY"); key2 != "" {
			return NewGeminiLLM("gemini-2.5-flash-preview-05-20")
		}
	} else {
		return NewGeminiLLM("gemini-2.5-flash-preview-05-20")
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		return NewOpenAILLM("gpt-4o-mini")
	}
	return nil
}

// ---------------------------------------------------------------------------
// RetryLLM — wraps any LLMClient with retry + exponential back-off
// ---------------------------------------------------------------------------

// isRateLimitErr returns true when err looks like an HTTP 429 / rate-limit
// response from any supported provider.  We match on the serialised error
// string because all providers return a wrapped fmt.Errorf containing the
// HTTP status code and body.
func isRateLimitErr(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "429") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "rate_limit") ||
		strings.Contains(s, "too many requests") ||
		strings.Contains(s, "quota exceeded") ||
		strings.Contains(s, "requests per") ||
		strings.Contains(s, "ratelimit")
}

// RetryLLM wraps an LLMClient and retries transient failures with jittered
// exponential back-off. It does NOT retry context.Canceled or parent-context
// deadline errors — those are propagated immediately.
//
// Rate-limit errors (HTTP 429) receive special treatment:
//   - The initial back-off is extended (rateLimitBase) to respect provider
//     cooling windows.
//   - If a fallback LLMClient is configured, rate-limit errors immediately
//     route to it so the caller never stalls on a saturated primary provider.
//     After the primary cools down it resumes being used on the next
//     RetryLLM instance call.
type RetryLLM struct {
	inner    LLMClient
	fallback LLMClient // optional; used on rate-limit errors
	maxTry   int
	baseWait time.Duration // initial back-off for non-rate-limit errors
}

// NewRetryLLM wraps inner with up to maxTry attempts.
// baseWait is the initial back-off interval (doubled each attempt, jittered).
func NewRetryLLM(inner LLMClient, maxTry int, baseWait time.Duration) *RetryLLM {
	if maxTry < 1 {
		maxTry = 1
	}
	return &RetryLLM{inner: inner, maxTry: maxTry, baseWait: baseWait}
}

// NewRetryLLMWithFallback is like NewRetryLLM but also accepts a fallback
// LLMClient that is tried immediately on rate-limit errors before the
// normal back-off loop continues on the primary.  Pass nil to disable.
func NewRetryLLMWithFallback(inner, fallback LLMClient, maxTry int, baseWait time.Duration) *RetryLLM {
	if maxTry < 1 {
		maxTry = 1
	}
	return &RetryLLM{inner: inner, fallback: fallback, maxTry: maxTry, baseWait: baseWait}
}

func (r *RetryLLM) Generate(ctx context.Context, system, prompt string) (string, error) {
	var lastErr error
	wait := r.baseWait
	for attempt := 0; attempt < r.maxTry; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		out, err := r.inner.Generate(ctx, system, prompt)
		if err == nil {
			return out, nil
		}
		lastErr = err
		// Don't retry if the parent context is done.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		// Rate-limit path: try fallback immediately, then use a longer back-off.
		if isRateLimitErr(err) {
			log.Printf("[deepwiki/retry] rate-limit on attempt %d/%d: %v", attempt+1, r.maxTry, err)
			if r.fallback != nil {
				out2, err2 := r.fallback.Generate(ctx, system, prompt)
				if err2 == nil {
					return out2, nil
				}
				log.Printf("[deepwiki/retry] fallback also failed: %v", err2)
			}
			// Extended back-off for rate limits: start at 10s, double each time.
			rlWait := min(10*time.Second*time.Duration(1<<uint(attempt)), 2*time.Minute)
			log.Printf("[deepwiki/retry] backing off %s before retry", rlWait)
			select {
			case <-time.After(rlWait):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			continue
		}

		if attempt < r.maxTry-1 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return "", ctx.Err()
			}
			wait *= 2
		}
	}
	return "", lastErr
}

func (r *RetryLLM) GenerateStream(ctx context.Context, system, prompt string, onChunk func(string)) error {
	var lastErr error
	wait := r.baseWait
	for attempt := 0; attempt < r.maxTry; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := r.inner.GenerateStream(ctx, system, prompt, onChunk)
		if err == nil {
			return nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Rate-limit path: try fallback immediately.
		if isRateLimitErr(err) {
			log.Printf("[deepwiki/retry] rate-limit (stream) on attempt %d/%d: %v", attempt+1, r.maxTry, err)
			if r.fallback != nil {
				err2 := r.fallback.GenerateStream(ctx, system, prompt, onChunk)
				if err2 == nil {
					return nil
				}
				log.Printf("[deepwiki/retry] fallback stream also failed: %v", err2)
			}
			rlWait := min(10*time.Second*time.Duration(1<<uint(attempt)), 2*time.Minute)
			select {
			case <-time.After(rlWait):
			case <-ctx.Done():
				return ctx.Err()
			}
			continue
		}

		if attempt < r.maxTry-1 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
			wait *= 2
		}
	}
	return lastErr
}

func (r *RetryLLM) IsAvailable() bool { return r.inner.IsAvailable() }

func (r *RetryLLM) Provider() string  { return r.inner.Provider() }
func (r *RetryLLM) ModelName() string { return r.inner.ModelName() }

// AnthropicLLM implements LLMClient using the Anthropic Messages API.
// Supports both API key (sk-ant-api03-...) and OAuth token (sk-ant-oat01-...).
type AnthropicLLM struct {
	APIKey    string // API key or OAuth bearer token
	Model     string
	IsOAuth   bool
	MaxTokens int // output token cap; 0 → use modelMaxOutputTokens default
	client    *http.Client
}

// modelMaxOutputTokens returns the output-token cap to request for a given
// Anthropic model. Synthesis planner JSON can exceed the old 8k cap, so for
// Claude 4.x families we default to 32k (well within per-model ceilings) and
// for anything else we stay conservative at 8k.
func modelMaxOutputTokens(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude-opus-4"), strings.Contains(m, "claude-sonnet-4"), strings.Contains(m, "claude-haiku-4"):
		return 32768
	default:
		return 8192
	}
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Stream    bool               `json:"stream,omitempty"`
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type anthropicStreamEvent struct {
	Type  string `json:"type"`
	Delta *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta,omitempty"`
}

// NewAnthropicLLM creates an Anthropic LLM client.
// Reads ANTHROPIC_API_KEY env var; also accepts explicit token.
func NewAnthropicLLM(model, token string) *AnthropicLLM {
	if token == "" {
		token = os.Getenv("ANTHROPIC_API_KEY")
	}
	if token == "" {
		token = os.Getenv("CLAUDE_API_KEY")
	}
	isOAuth := strings.HasPrefix(token, "sk-ant-oat")
	if model == "" {
		model = "claude-haiku-4-5-20251001"
	}
	return &AnthropicLLM{
		APIKey:  token,
		Model:   model,
		IsOAuth: isOAuth,
		client:  &http.Client{Timeout: 10 * time.Minute},
	}
}

func (a *AnthropicLLM) buildRequest(system, prompt string, stream bool) (*http.Request, error) {
	maxTok := a.MaxTokens
	if maxTok <= 0 {
		maxTok = modelMaxOutputTokens(a.Model)
	}
	body := anthropicRequest{
		Model:     a.Model,
		MaxTokens: maxTok,
		Messages:  []anthropicMessage{{Role: "user", Content: prompt}},
		Stream:    stream,
	}
	if system != "" {
		body.System = system
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost,
		"https://api.anthropic.com/v1/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("Authorization", "Bearer "+a.APIKey)
	if a.IsOAuth {
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	}
	return req, nil
}

func (a *AnthropicLLM) Generate(ctx context.Context, system, prompt string) (string, error) {
	t0 := time.Now()
	log.Printf("[deepwiki/llm] → anthropic model=%s prompt=%d chars", a.Model, len(system)+len(prompt))
	req, err := a.buildRequest(system, prompt, false)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("[deepwiki/llm] ✗ anthropic model=%s err=%v (%s)", a.Model, err, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	var result anthropicResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Printf("[deepwiki/llm] ✗ anthropic model=%s decode err=%v (%s)", a.Model, err, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("decode anthropic response: %w", err)
	}
	if result.Error != nil {
		log.Printf("[deepwiki/llm] ✗ anthropic model=%s API err=%s (%s)", a.Model, result.Error.Message, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("anthropic error: %s", result.Error.Message)
	}
	if len(result.Content) == 0 {
		log.Printf("[deepwiki/llm] ✗ anthropic model=%s empty content (%s)", a.Model, time.Since(t0).Round(time.Millisecond))
		return "", fmt.Errorf("anthropic returned empty content")
	}
	log.Printf("[deepwiki/llm] ✓ anthropic model=%s response=%d chars (%s)", a.Model, len(result.Content[0].Text), time.Since(t0).Round(time.Millisecond))
	return result.Content[0].Text, nil
}

func (a *AnthropicLLM) GenerateStream(ctx context.Context, system, prompt string, onChunk func(string)) error {
	req, err := a.buildRequest(system, prompt, true)
	if err != nil {
		return err
	}
	req = req.WithContext(ctx)

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("anthropic returned %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		var event anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta != nil && event.Delta.Text != "" {
			onChunk(event.Delta.Text)
		}
	}
	return scanner.Err()
}

func (a *AnthropicLLM) IsAvailable() bool {
	return a.APIKey != ""
}

func (a *AnthropicLLM) Provider() string  { return "anthropic" }
func (a *AnthropicLLM) ModelName() string { return a.Model }

// NewLLMFromOAuthFile creates an Anthropic LLM client by reading the OAuth token
// from ~/.swarm/config/oauth/default.json, falling back to ANTHROPIC_API_KEY env var.
func NewLLMFromOAuthFile(model string) (LLMClient, error) {
	oauthPath := paths.OAuthFile("default")

	data, err := os.ReadFile(oauthPath)
	if err == nil {
		var cfg struct {
			Token *struct {
				AccessToken string `json:"access_token"`
			} `json:"token"`
		}
		if json.Unmarshal(data, &cfg) == nil && cfg.Token != nil && cfg.Token.AccessToken != "" {
			client := NewAnthropicLLM(model, cfg.Token.AccessToken)
			if client.IsAvailable() {
				return client, nil
			}
		}
	}

	// Fall back to env var
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		return NewAnthropicLLM(model, key), nil
	}
	return nil, fmt.Errorf("no Anthropic credentials found (tried %s and ANTHROPIC_API_KEY)", oauthPath)
}
