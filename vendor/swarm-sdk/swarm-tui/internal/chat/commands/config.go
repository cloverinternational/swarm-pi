package commands

import (
	// "context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkcredentials "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/credentials"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/registry"
	tuianalytics "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	// "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	// "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	// "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	// "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/catalogmerge"
	// "github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

// configFileCache is a package-level mtime-based cache so repeated LoadConfig
// calls within the same "quiet period" are free (no disk I/O, no JSON parse).
// This is safe because SaveConfig always writes the file, bumping its mtime.
var configFileCache struct {
	mu      sync.Mutex
	path    string
	modTime time.Time
	config  *SwarmOSConfig
}

// ProviderConfig represents a provider configuration
type ProviderConfig struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"display_name"`
	Color            string            `json:"color"`
	Type             string            `json:"type"`               // "api_key" or "oauth"
	APIType          string            `json:"api_type"`           // "anthropic", "openai", "openai-compatible", "exa", etc.
	BaseURL          string            `json:"base_url,omitempty"` // Custom base URL for openai-compatible providers
	HTTPMaxRetries   *int              `json:"http_max_retries,omitempty"`
	APIKey           string            `json:"api_key,omitempty"`            // Legacy plaintext API key; prefer APIKeySecretRef
	APIKeySecretRef  string            `json:"api_key_secret_ref,omitempty"` // Encrypted vault credential ID
	Source           string            `json:"source,omitempty"`             // "cloud", "local", "user"
	Available        bool              `json:"available"`
	LastRefreshed    string            `json:"last_refreshed,omitempty"`
	Models           []ModelConfig     `json:"models"`
	CodexQueryParams map[string]string `json:"codex_query_params,omitempty"`
	CodexHTTPHeaders map[string]string `json:"codex_http_headers,omitempty"`
}

// ModelConfig represents a model configuration
type ModelConfig struct {
	ID                      string   `json:"id"`
	DisplayName             string   `json:"display_name"`
	Context                 string   `json:"context"`
	ContextWindow           int      `json:"context_window,omitempty"`
	Description             string   `json:"description,omitempty"`
	SupportsReasoningEffort *bool    `json:"supports_reasoning_effort,omitempty"`
	ReasoningEfforts        []string `json:"reasoning_efforts,omitempty"`
	// Per-model generation overrides. Nil pointers and MaxTokens == 0 inherit
	// the provider default; pointers allow an explicit numeric zero.
	Temperature *float64 `json:"temperature,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`

	// Extended Thinking Settings (per model)
	ThinkingEnabled    bool   `json:"thinking_enabled,omitempty"` // Enable extended thinking for this model
	ThinkingEnabledSet bool   `json:"-"`                          // Distinguishes an explicit false from an omitted catalog field
	ThinkingBudget     int    `json:"thinking_budget,omitempty"`  // Token budget for manual thinking mode (1024-100000)
	ThinkingEffort     string `json:"thinking_effort,omitempty"`  // Effort level: "low", "medium", "high", "max" (Opus 4.6+)

	// Diffusion marks text-diffusion models (e.g. DiffusionGemma) whose
	// completions arrive as whole denoised blocks rather than token streams.
	// The TUI renders their output with a denoising reveal animation.
	Diffusion bool `json:"diffusion,omitempty"`

	// Enriched from models.dev
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	CostInput       float64 `json:"cost_input,omitempty"`  // Cost per 1M input tokens (USD)
	CostOutput      float64 `json:"cost_output,omitempty"` // Cost per 1M output tokens (USD)
	Reasoning       bool    `json:"reasoning,omitempty"`
	ToolCall        bool    `json:"tool_call,omitempty"`
}

type modelConfigJSON ModelConfig

// UnmarshalJSON records whether thinking_enabled was present so an explicit
// false remains distinguishable from a catalog entry that omitted the field.
func (m *ModelConfig) UnmarshalJSON(data []byte) error {
	var decoded modelConfigJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var presence struct {
		ThinkingEnabled *bool `json:"thinking_enabled"`
	}
	if err := json.Unmarshal(data, &presence); err != nil {
		return err
	}
	*m = ModelConfig(decoded)
	m.ThinkingEnabledSet = presence.ThinkingEnabled != nil
	return nil
}

// MarshalJSON preserves an explicit false while continuing to omit an
// unspecified thinking_enabled field from catalog-generated model records.
func (m ModelConfig) MarshalJSON() ([]byte, error) {
	var thinkingEnabled *bool
	if m.ThinkingEnabled || m.ThinkingEnabledSet {
		value := m.ThinkingEnabled
		thinkingEnabled = &value
	}
	return json.Marshal(struct {
		modelConfigJSON
		ThinkingEnabled *bool `json:"thinking_enabled,omitempty"`
	}{
		modelConfigJSON: modelConfigJSON(m),
		ThinkingEnabled: thinkingEnabled,
	})
}

const (
	defaultCurrentProvider       = "ClaudeCode"
	defaultCurrentModel          = "claude-opus-4-20250514"
	defaultMicroRetentionCount   = 0 // DISABLED: Set to 0 to prevent context loss
	defaultMaxRetriesPerProvider = 3
	defaultRetryAfterFallbackMs  = 30000
)

// RetrySettingsConfig controls retry/fallback behavior for primary chat execution.
type RetrySettingsConfig struct {
	Enabled               bool `json:"enabled"`
	MaxRetriesPerProvider int  `json:"max_retries_per_provider,omitempty"`
	RotateOnRateLimit     bool `json:"rotate_on_rate_limit,omitempty"`
	RetryAfterFallbackMs  int  `json:"retry_after_fallback_ms,omitempty"`
}

// ModelRateLimitConfig configures per-provider/model request limits.
type ModelRateLimitConfig struct {
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	RequestsPerMinute int    `json:"requests_per_minute"`
	Enabled           bool   `json:"enabled,omitempty"`
	Burst             int    `json:"burst,omitempty"`
}

// SwarmOSConfig represents the main config
// WebSearchConfig holds web search preferences (mirrors core.WebSearchConfig).
// JSON tags must match core so both packages share the same config.json field.
type WebSearchConfig struct {
	// PreferredBackend: "auto" (default), "anthropic", "exa"
	PreferredBackend string `json:"preferredBackend,omitempty"`
	// ExaSearchType: "auto" (default), "neural", "keyword"
	ExaSearchType string `json:"exaSearchType,omitempty"`
	// MaxResults caps results per query (default 10, max 20)
	MaxResults int `json:"maxResults,omitempty"`
	// TimeoutSeconds sets the HTTP timeout for search requests (default 30)
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

// FindingsConfig holds local findings (AAR) database settings.
type FindingsConfig struct {
	ServerURL         string   `json:"serverUrl,omitempty"`
	Enabled           bool     `json:"enabled"`
	AutoCaptureTools  []string `json:"autoCaptureTools,omitempty"`
	AnalysisMode      string   `json:"analysisMode,omitempty"`      // on_compaction, on_session_end, every_n_tools, disabled
	AnalysisBatchSize int      `json:"analysisBatchSize,omitempty"` // 5, 10, 20, 50
}

type SwarmOSConfig struct {
	CurrentProvider      string            `json:"current_provider"`
	CurrentModel         string            `json:"current_model"`
	ReasoningEffort      string            `json:"reasoning_effort,omitempty"`
	ReasoningByModel     map[string]string `json:"reasoning_by_model,omitempty"`
	CompactionProvider   string            `json:"compaction_provider,omitempty"`    // DEPRECATED: Use CompactionChain
	CompactionModel      string            `json:"compaction_model,omitempty"`       // DEPRECATED: Use CompactionChain
	CompactionChain      *fallback.Chain   `json:"compaction_chain,omitempty"`       // Fallback chain for compaction
	DisabledBuiltinTools map[string]bool   `json:"disabled_builtin_tools,omitempty"` // tool name -> disabled
	FavoriteModels       []ModelRef        `json:"favorite_models,omitempty"`
	RecentModels         []ModelRef        `json:"recent_models,omitempty"`

	// General UI settings
	SidePanelEnabled *bool  `json:"side_panel_enabled,omitempty"`
	CompactMode      bool   `json:"compact_mode,omitempty"`
	DebugMode        bool   `json:"debug_mode,omitempty"`
	MaxHistory       int    `json:"max_history,omitempty"`
	Language         string `json:"language"`

	// Telemetry / analytics opt-in. OFF by default. When true the TUI exports
	// SWARM_ANALYTICS_ENABLED=1 at startup so the analytics pipeline may run;
	// the user must still supply their own collector URL + token (no endpoint
	// ships in the open-source build). TelemetryConsentAcknowledged records
	// that the one-time consent modal has been shown and answered.
	TelemetryEnabled             bool `json:"telemetry_enabled,omitempty"`
	TelemetryConsentAcknowledged bool `json:"telemetry_consent_acknowledged,omitempty"`

	// Micro-compaction settings
	EnableMicroCompaction bool `json:"enableMicroCompaction,omitempty"`
	MicroRetentionCount   int  `json:"microRetentionCount,omitempty"`

	// Advanced session settings
	MaxTokens      int     `json:"max_tokens,omitempty"`
	Temperature    float64 `json:"temperature,omitempty"`
	StreamResponse *bool   `json:"stream_response,omitempty"`
	CachePrompts   *bool   `json:"cache_prompts,omitempty"`

	// Auto-compaction settings
	EnableAutoCompaction            *bool   `json:"enableAutoCompaction,omitempty"`
	AutoCompactionContinueIfRunning bool    `json:"autoCompactionContinueIfRunning"`
	AutoCompactionThresholdPercent  float64 `json:"autoCompactionThresholdPercent,omitempty"` // DEPRECATED: use AutoCompactionThreshold

	// AutoCompactionThreshold is the global trigger threshold. Migrates from
	// AutoCompactionThresholdPercent on load. In "percent" mode Value is a
	// fraction of the model context window (0.5–0.95); in "fixed_tokens"
	// mode Value is an absolute token count.
	AutoCompactionThreshold      CompactionThreshold            `json:"autoCompactionThreshold,omitempty"`
	CompactionThresholdOverrides map[string]CompactionThreshold `json:"compactionThresholdOverrides,omitempty"` // "provider/model" -> threshold

	// Advanced Tool Use Framework settings
	AdvancedToolMode    bool            `json:"advanced_tool_mode,omitempty"`    // Enable deferred tool loading for token optimization
	DeferTokenThreshold int             `json:"defer_token_threshold,omitempty"` // Auto-defer tools above this token estimate (default 500)
	ToolOverrides       map[string]bool `json:"tool_overrides,omitempty"`        // toolName -> shouldDefer (true=defer, false=eager)

	// Reliability settings for primary chat execution
	RetrySettings     *RetrySettingsConfig   `json:"retry_settings,omitempty"`
	ChatFallbackChain *fallback.Chain        `json:"chat_fallback_chain,omitempty"`
	ModelRateLimits   []ModelRateLimitConfig `json:"model_rate_limits,omitempty"`

	// Dream / background memory consolidation
	AutoDreamEnabled bool    `json:"auto_dream_enabled,omitempty"`
	DreamMinHours    float64 `json:"dream_min_hours,omitempty"` // default: 24
	DreamModel       string  `json:"dream_model,omitempty"`     // "" = use current model
	DreamProvider    string  `json:"dream_provider,omitempty"`  // "" = use current provider

	// Dream auto-trigger (0 = off; N > 0 = trigger every N sessions regardless of thresholds)
	DreamAutoTriggerEvery int `json:"dream_auto_trigger_every,omitempty"`

	// Plan Mode – plan-before-act workflow
	PlanModeEnabled   bool   `json:"plan_mode_enabled,omitempty"`    // registers enter/exit_plan_mode tools
	PlanModeAutoClear bool   `json:"plan_mode_auto_clear,omitempty"` // auto-compact context after approval
	PlanModeFileName  string `json:"plan_mode_file_name,omitempty"`  // plan file name (default: PLAN.md)

	// Computer Use – screen control and input automation (Linux only)
	ComputerUseEnabled bool `json:"computer_use_enabled,omitempty"` // registers screenshot, mouse, keyboard tools

	// Code Mode – JavaScript sandbox for batched tool calls via Promise.all()
	EnableCodeMode bool `json:"enableCodeMode,omitempty"`

	// Opt-in agent completion verification and early context summarization.
	CompletionConfirm           bool    `json:"completion_confirm,omitempty"`
	CompletionConfirmMax        int     `json:"completion_confirm_max,omitempty"`
	ProactiveSummarizeThreshold float64 `json:"proactive_summarize_threshold,omitempty"`

	// MemoryBackend selects the dream-memory backend used to populate the
	// system-prompt memory index. "" or "flat" (default) uses the legacy
	// flat .md store under ~/.swarm/projects/<hash>/memory/. "palace" uses
	// the structured MemPalace backend with 4-layer loading. Switch via
	// the /memory backend slash command.
	MemoryBackend string `json:"memoryBackend,omitempty"`

	// Web search backend and options
	WebSearch *WebSearchConfig `json:"webSearch,omitempty"`

	// Local findings (AAR) database settings
	Findings *FindingsConfig `json:"findings,omitempty"`

	// Steering configuration (tool-call interception and meta-cognitive control)
	// JSON tag matches core.Config.SteeringConfig so both packages share config.json.
	SteeringConfig *core.SteeringConfig `json:"steeringConfig,omitempty"`

	// Swarm agent settings (Dream + Steering model selection)
	SwarmAgents *SwarmAgentsConfig `json:"swarmAgents,omitempty"`
}

// SwarmAgentsConfig holds the global model selection for the Steering agent.
type SwarmAgentsConfig struct {
	SteeringProvider string `json:"steeringProvider,omitempty"`
	SteeringModel    string `json:"steeringModel,omitempty"`
}

// ToCoreConfig converts SwarmOSConfig to SDK CoreConfig.
// This bridges TUI-specific config with the standardized SDK config format.
func (c *SwarmOSConfig) ToCoreConfig() sdkclient.CoreConfig {
	if c == nil {
		return sdkclient.CoreConfig{}
	}

	// Parse provider string to typed Provider, but preserve original name
	// for credential lookup to ensure OpenAI-compatible providers (fireworks,
	// groq, etc.) look up their specific API keys.
	var provider sdkclient.Provider
	if c.CurrentProvider != "" {
		if p, err := sdkclient.ParseProvider(c.CurrentProvider); err == nil {
			provider = p
		}
	}

	// Handle nullable bool fields with defaults
	streamResponse := true
	if c.StreamResponse != nil {
		streamResponse = *c.StreamResponse
	}
	cachePrompts := true
	if c.CachePrompts != nil {
		cachePrompts = *c.CachePrompts
	}

	cfg := (&sdkclient.CoreConfig{}).Defaults()
	cfg.Provider = provider
	cfg.OriginalProvider = c.CurrentProvider // Preserve original for credential lookup
	cfg.Model = c.CurrentModel
	cfg.Temperature = c.Temperature
	cfg.MaxTokens = c.MaxTokens
	cfg.EnableAutoCompaction = c.GetEnableAutoCompaction()
	cfg.AutoCompactionThresholdPercent = c.AutoCompactionThresholdPercent
	cfg.AutoCompactionContinueIfRunning = c.AutoCompactionContinueIfRunning
	cfg.StreamResponse = streamResponse
	cfg.CachePrompts = cachePrompts
	cfg.ReasoningEffort = c.ReasoningEffort
	cfg.ReasoningEffortByModel = c.ReasoningByModel
	cfg.EnableMicroCompaction = c.EnableMicroCompaction
	cfg.MicroRetentionCount = c.MicroRetentionCount
	cfg.AdvancedToolMode = c.AdvancedToolMode
	cfg.DeferTokenThreshold = c.DeferTokenThreshold
	cfg.PlanModeEnabled = c.PlanModeEnabled
	cfg.PlanModeAutoClear = c.PlanModeAutoClear
	cfg.PlanModeFileName = c.PlanModeFileName
	cfg.ComputerUseEnabled = c.ComputerUseEnabled
	cfg.EnableCodeMode = c.EnableCodeMode
	cfg.CompletionConfirm = c.CompletionConfirm
	cfg.CompletionConfirmMax = c.CompletionConfirmMax
	cfg.ProactiveSummarizeThreshold = c.ProactiveSummarizeThreshold
	return cfg
}

// ApplyCoreConfig applies SDK CoreConfig values to this SwarmOSConfig.
// Use this when loading settings from the SDK format.
func (c *SwarmOSConfig) ApplyCoreConfig(cfg sdkclient.CoreConfig) {
	if c == nil {
		return
	}
	c.CurrentProvider = string(cfg.Provider)
	c.CurrentModel = cfg.Model
	c.Temperature = cfg.Temperature
	c.MaxTokens = cfg.MaxTokens
	c.SetEnableAutoCompaction(cfg.EnableAutoCompaction)
	c.AutoCompactionThresholdPercent = cfg.AutoCompactionThresholdPercent
	c.AutoCompactionContinueIfRunning = cfg.AutoCompactionContinueIfRunning
	c.EnableMicroCompaction = cfg.EnableMicroCompaction
	c.MicroRetentionCount = cfg.MicroRetentionCount
	c.AdvancedToolMode = cfg.AdvancedToolMode
	c.DeferTokenThreshold = cfg.DeferTokenThreshold
	c.PlanModeEnabled = cfg.PlanModeEnabled
	c.PlanModeAutoClear = cfg.PlanModeAutoClear
	c.PlanModeFileName = cfg.PlanModeFileName
	if c.PlanModeFileName == "" {
		c.PlanModeFileName = "PLAN.md"
	}
	c.ComputerUseEnabled = cfg.ComputerUseEnabled
	c.EnableCodeMode = cfg.EnableCodeMode
	c.CompletionConfirm = cfg.CompletionConfirm
	c.CompletionConfirmMax = cfg.CompletionConfirmMax
	c.ProactiveSummarizeThreshold = cfg.ProactiveSummarizeThreshold
	c.ReasoningEffort = cfg.ReasoningEffort
	c.ReasoningByModel = cfg.ReasoningEffortByModel

	// Handle *bool fields - only set if SDK has explicit values
	streamResponse := cfg.StreamResponse
	c.StreamResponse = &streamResponse
	cachePrompts := cfg.CachePrompts
	c.CachePrompts = &cachePrompts
}

// DefaultRetrySettings returns deterministic defaults for retry behavior.
func DefaultRetrySettings() RetrySettingsConfig {
	return RetrySettingsConfig{
		Enabled:               true,
		MaxRetriesPerProvider: defaultMaxRetriesPerProvider,
		RotateOnRateLimit:     false,
		RetryAfterFallbackMs:  defaultRetryAfterFallbackMs,
	}
}

// GetRetrySettings returns retry settings with defaults for nil/zero values.
func (c *SwarmOSConfig) GetRetrySettings() RetrySettingsConfig {
	defaults := DefaultRetrySettings()
	if c == nil || c.RetrySettings == nil {
		return defaults
	}

	settings := *c.RetrySettings
	if settings.MaxRetriesPerProvider <= 0 {
		settings.MaxRetriesPerProvider = defaults.MaxRetriesPerProvider
	}
	if settings.RetryAfterFallbackMs <= 0 {
		settings.RetryAfterFallbackMs = defaults.RetryAfterFallbackMs
	}
	return settings
}

// SetRetrySettings updates retry settings.
func (c *SwarmOSConfig) SetRetrySettings(settings RetrySettingsConfig) {
	if c == nil {
		return
	}
	c.RetrySettings = &settings
}

// GetChatFallbackChain returns the primary chat fallback chain with safe defaults.
func (c *SwarmOSConfig) GetChatFallbackChain() *fallback.Chain {
	if c == nil {
		return fallback.NewChain(defaultCurrentProvider, defaultCurrentModel)
	}
	if c.ChatFallbackChain != nil && !c.ChatFallbackChain.IsEmpty() {
		// Strip any known non-chat-completion model (legacy completions,
		// image, audio, embedding, moderation) before handing the chain to
		// callers. A persisted chain that only contains such models (e.g. a
		// stale/poisoned "openai/babbage-002" primary with no fallbacks,
		// Swarm-Code/mono#66) falls through to the CurrentProvider/CurrentModel
		// default below instead of being returned as-is.
		if sanitized := fallback.SanitizeChatChain(c.ChatFallbackChain); sanitized != nil {
			return sanitized
		}
	}

	provider := strings.TrimSpace(c.CurrentProvider)
	model := strings.TrimSpace(c.CurrentModel)
	if provider == "" {
		provider = defaultCurrentProvider
	}
	if model == "" {
		model = defaultCurrentModel
	}
	return fallback.NewChain(provider, model)
}

// SetChatFallbackChain updates the primary chat fallback chain.
func (c *SwarmOSConfig) SetChatFallbackChain(chain *fallback.Chain) {
	if c == nil {
		return
	}
	c.ChatFallbackChain = chain
}

// GetModelRateLimits returns sanitized per-model limits.
func (c *SwarmOSConfig) GetModelRateLimits() []ModelRateLimitConfig {
	if c == nil || len(c.ModelRateLimits) == 0 {
		return []ModelRateLimitConfig{}
	}

	limits := make([]ModelRateLimitConfig, 0, len(c.ModelRateLimits))
	for _, limit := range c.ModelRateLimits {
		limit.Provider = strings.TrimSpace(limit.Provider)
		limit.Model = strings.TrimSpace(limit.Model)
		if limit.Provider == "" || limit.Model == "" {
			continue
		}
		if limit.RequestsPerMinute <= 0 {
			continue
		}
		limits = append(limits, limit)
	}
	return limits
}

// SetModelRateLimits updates per-model limits.
func (c *SwarmOSConfig) SetModelRateLimits(limits []ModelRateLimitConfig) {
	if c == nil {
		return
	}
	c.ModelRateLimits = limits
}

// GetDreamMinHours returns DreamMinHours with a safe default of 24h.
func (c *SwarmOSConfig) GetDreamMinHours() float64 {
	if c == nil || c.DreamMinHours <= 0 {
		return 24.0
	}
	return c.DreamMinHours
}

// GetDreamAutoTriggerEvery returns the auto-trigger cadence in sessions.
// Returns 0 if unset (disabled).
func (c *SwarmOSConfig) GetDreamAutoTriggerEvery() int {
	if c == nil || c.DreamAutoTriggerEvery < 0 {
		return 0
	}
	return c.DreamAutoTriggerEvery
}

// GetMemoryBackend returns the configured memory backend, normalized to a
// known value. Empty/unknown values default to "flat" so existing configs
// keep their previous behavior.
func (c *SwarmOSConfig) GetMemoryBackend() string {
	if c == nil {
		return "flat"
	}
	switch c.MemoryBackend {
	case "palace":
		return "palace"
	case "flat", "":
		return "flat"
	default:
		return "flat"
	}
}

// SetMemoryBackend validates and persists the memory backend selection
// in-memory. Caller is responsible for invoking ConfigManager.SaveConfig.
// Returns an error if the value is not one of the known backends.
func (c *SwarmOSConfig) SetMemoryBackend(value string) error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	switch value {
	case "flat", "palace":
		c.MemoryBackend = value
		return nil
	case "":
		c.MemoryBackend = ""
		return nil
	default:
		return fmt.Errorf("unknown memory backend %q (want flat or palace)", value)
	}
}

// GetReasoningEffort returns the global reasoning effort with defaults.
func (c *SwarmOSConfig) GetReasoningEffort() string {
	if c == nil {
		return sdkprovider.ReasoningEffortAuto
	}
	return sdkprovider.NormalizeReasoningEffortSetting(c.ReasoningEffort)
}

// SetReasoningEffort sets the global reasoning effort.
func (c *SwarmOSConfig) SetReasoningEffort(value string) {
	if c == nil {
		return
	}
	c.ReasoningEffort = sdkprovider.NormalizeReasoningEffortSetting(value)
}

// GetReasoningEffortForModel returns per-model override if present, else global value.
func (c *SwarmOSConfig) GetReasoningEffortForModel(providerName string, model string) string {
	if c == nil {
		return sdkprovider.ReasoningEffortAuto
	}
	if len(c.ReasoningByModel) > 0 {
		if value, ok := c.ReasoningByModel[reasoningModelKey(providerName, model)]; ok {
			return sdkprovider.NormalizeReasoningEffortSetting(value)
		}
	}
	return c.GetReasoningEffort()
}

// SetReasoningEffortForModel updates per-model reasoning effort override.
// Setting "auto" removes the override.
func (c *SwarmOSConfig) SetReasoningEffortForModel(providerName string, model string, value string) {
	if c == nil {
		return
	}
	var key string = reasoningModelKey(providerName, model)
	if key == "" {
		return
	}
	var normalized string = sdkprovider.NormalizeReasoningEffortSetting(value)
	if normalized == sdkprovider.ReasoningEffortAuto {
		if len(c.ReasoningByModel) > 0 {
			delete(c.ReasoningByModel, key)
		}
		return
	}
	if c.ReasoningByModel == nil {
		c.ReasoningByModel = make(map[string]string)
	}
	c.ReasoningByModel[key] = normalized
}

func reasoningModelKey(providerName string, model string) string {
	var providerID string = strings.ToLower(strings.TrimSpace(providerName))
	var modelID string = strings.ToLower(strings.TrimSpace(model))
	if providerID == "" || modelID == "" {
		return ""
	}
	return providerID + "/" + modelID
}

func (c *SwarmOSConfig) applyReliabilityDefaults() {
	if c == nil {
		return
	}

	if c.RetrySettings == nil {
		defaults := DefaultRetrySettings()
		c.RetrySettings = &defaults
	} else {
		if c.RetrySettings.MaxRetriesPerProvider <= 0 {
			c.RetrySettings.MaxRetriesPerProvider = defaultMaxRetriesPerProvider
		}
		if c.RetrySettings.RetryAfterFallbackMs <= 0 {
			c.RetrySettings.RetryAfterFallbackMs = defaultRetryAfterFallbackMs
		}
	}

	if c.ChatFallbackChain == nil || c.ChatFallbackChain.IsEmpty() {
		c.ChatFallbackChain = c.GetChatFallbackChain()
	}

	c.ModelRateLimits = c.GetModelRateLimits()
	c.SetReasoningEffort(c.ReasoningEffort)
	if len(c.ReasoningByModel) > 0 {
		normalized := make(map[string]string, len(c.ReasoningByModel))
		for key, value := range c.ReasoningByModel {
			var trimmedKey string = strings.ToLower(strings.TrimSpace(key))
			if trimmedKey == "" {
				continue
			}
			var normalizedValue string = sdkprovider.NormalizeReasoningEffortSetting(value)
			if normalizedValue == sdkprovider.ReasoningEffortAuto {
				continue
			}
			normalized[trimmedKey] = normalizedValue
		}
		c.ReasoningByModel = normalized
	}
}

// GetCompactionChain returns the compaction fallback chain, migrating from legacy fields if needed.
func (c *SwarmOSConfig) GetCompactionChain() *fallback.Chain {
	// Handle nil receiver
	if c == nil {
		return fallback.NewChainWithDefaults()
	}

	// If new chain exists, use it
	if c.CompactionChain != nil && !c.CompactionChain.IsEmpty() {
		return c.CompactionChain
	}

	// Migrate from legacy single model config
	if c.CompactionProvider != "" && c.CompactionModel != "" {
		return fallback.NewChain(c.CompactionProvider, c.CompactionModel)
	}

	// Return default chain with sensible defaults
	return fallback.NewChainWithDefaults()
}

// SetCompactionChain updates the compaction fallback chain and clears legacy fields.
func (c *SwarmOSConfig) SetCompactionChain(chain *fallback.Chain) {
	c.CompactionChain = chain
	// Clear legacy fields on next save
	c.CompactionProvider = ""
	c.CompactionModel = ""
}

// GetEnableAutoCompaction resolves the persisted optional setting. Automatic
// compaction is enabled when the field is absent, while an explicit false is
// preserved across JSON/YAML round trips.
func (c *SwarmOSConfig) GetEnableAutoCompaction() bool {
	if c == nil || c.EnableAutoCompaction == nil {
		return true
	}
	return *c.EnableAutoCompaction
}

// SetEnableAutoCompaction records an explicit auto-compaction preference.
func (c *SwarmOSConfig) SetEnableAutoCompaction(enabled bool) {
	if c == nil {
		return
	}
	c.EnableAutoCompaction = &enabled
}

// CompactionThresholdMode selects how the auto-compaction trigger is interpreted.
type CompactionThresholdMode string

const (
	// CompactionThresholdPercent means Value is a fraction of the model context
	// window (0.5–0.95).
	CompactionThresholdPercent CompactionThresholdMode = "percent"
	// CompactionThresholdFixedTokens means Value is an absolute token count.
	CompactionThresholdFixedTokens CompactionThresholdMode = "fixed_tokens"
)

// CompactionThreshold describes when auto-compaction triggers.
// In "percent" mode, Value is a fraction of the model context window (0.5–0.95).
// In "fixed_tokens" mode, Value is an absolute token count.
type CompactionThreshold struct {
	Mode  CompactionThresholdMode `json:"mode,omitempty"`
	Value float64                 `json:"value,omitempty"`
}

// DefaultCompactionThreshold returns the default threshold (85% percent).
func DefaultCompactionThreshold() CompactionThreshold {
	return CompactionThreshold{Mode: CompactionThresholdPercent, Value: 0.85}
}

// GetAutoCompactionThreshold returns the global compaction threshold,
// migrating from the legacy AutoCompactionThresholdPercent field if the new
// struct is empty. Returns the default (85% percent) if nothing is set.
func (c *SwarmOSConfig) GetAutoCompactionThreshold() CompactionThreshold {
	if c == nil {
		return DefaultCompactionThreshold()
	}
	th := c.AutoCompactionThreshold
	if th.Mode != "" {
		return th
	}
	// Migrate from legacy percent field.
	if c.AutoCompactionThresholdPercent > 0 {
		return CompactionThreshold{Mode: CompactionThresholdPercent, Value: c.AutoCompactionThresholdPercent}
	}
	return DefaultCompactionThreshold()
}

// CompactionOverrideKey builds the per-model override key as "provider/model",
// matching the ReasoningByModel convention. Exported so the settings UI can
// reuse the same keying scheme.
func CompactionOverrideKey(provider, model string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.ToLower(strings.TrimSpace(model))
	if p == "" || m == "" {
		return ""
	}
	return p + "/" + m
}

// GetCompactionThresholdForModel returns the per-model override if present,
// otherwise the global threshold. Provider/model are normalized to lower-case
// "provider/model" keys.
func (c *SwarmOSConfig) GetCompactionThresholdForModel(provider, model string) CompactionThreshold {
	if c == nil {
		return DefaultCompactionThreshold()
	}
	if key := CompactionOverrideKey(provider, model); key != "" {
		if len(c.CompactionThresholdOverrides) > 0 {
			if ov, ok := c.CompactionThresholdOverrides[key]; ok && ov.Mode != "" {
				return ov
			}
		}
	}
	return c.GetAutoCompactionThreshold()
}

// SetCompactionThresholdOverride sets a per-model override. Setting a zero
// threshold (Mode=="") removes the override.
func (c *SwarmOSConfig) SetCompactionThresholdOverride(provider, model string, th CompactionThreshold) {
	if c == nil {
		return
	}
	key := CompactionOverrideKey(provider, model)
	if key == "" {
		return
	}
	if th.Mode == "" {
		if len(c.CompactionThresholdOverrides) > 0 {
			delete(c.CompactionThresholdOverrides, key)
		}
		return
	}
	if c.CompactionThresholdOverrides == nil {
		c.CompactionThresholdOverrides = make(map[string]CompactionThreshold)
	}
	c.CompactionThresholdOverrides[key] = th
}

// ThresholdTokens computes the absolute token count at which compaction
// triggers, given the model context window.
func (th CompactionThreshold) ThresholdTokens(contextLimit int) int {
	if contextLimit <= 0 {
		contextLimit = sdkprovider.DefaultUnknownContextWindow
	}
	switch th.Mode {
	case CompactionThresholdFixedTokens:
		if th.Value < 1000 {
			return 1000
		}
		if int(th.Value) > contextLimit {
			return contextLimit
		}
		return int(th.Value)
	default: // percent
		v := th.Value
		if v <= 0 {
			v = 0.85
		}
		if v < 0.5 {
			v = 0.5
		}
		if v > 0.95 {
			v = 0.95
		}
		return int(float64(contextLimit) * v)
	}
}

// AsPercent returns the threshold expressed as a fraction of the context
// window (for display and back-compat). For fixed_tokens mode this is
// value/contextLimit.
func (th CompactionThreshold) AsPercent(contextLimit int) float64 {
	if contextLimit <= 0 {
		contextLimit = sdkprovider.DefaultUnknownContextWindow
	}
	switch th.Mode {
	case CompactionThresholdFixedTokens:
		return th.Value / float64(contextLimit)
	default:
		if th.Value <= 0 {
			return 0.85
		}
		return th.Value
	}
}

// ConfigManager handles loading/saving configuration
type ConfigManager struct {
	configDir string
}

// NewConfigManager creates a new config manager
func NewConfigManager() (*ConfigManager, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	configDir := filepath.Join(homeDir, ".swarmos")

	// Create config directory if it doesn't exist
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create config directory: %w", err)
	}

	return &ConfigManager{
		configDir: configDir,
	}, nil
}

// GetConfigPath returns the path to a config file
func (cm *ConfigManager) GetConfigPath(filename string) string {
	return filepath.Join(cm.configDir, filename)
}

// LoadProviders loads provider configuration
func (cm *ConfigManager) LoadProviders() ([]ProviderConfig, error) {
	unlock, err := cm.lockProvidersJSON()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return cm.loadProvidersLocked()
}

func (cm *ConfigManager) loadProvidersLocked() ([]ProviderConfig, error) {
	path := cm.GetConfigPath("providers.json")

	// If file doesn't exist, return defaults without saving
	if _, err := os.Stat(path); os.IsNotExist(err) {
		providers := cm.getDefaultProviders()
		cm.updateProviderAvailability(&providers)
		// Don't save here - just return defaults
		// The providers will be saved when user explicitly saves settings
		return providers, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read providers.json: %w", err)
	}

	var providers []ProviderConfig
	if err := json.Unmarshal(data, &providers); err != nil {
		return nil, fmt.Errorf("failed to parse providers.json: %w", err)
	}

	// Apply migrations (e.g., switch OpenAI to Codex OAuth models)
	changed := cm.applyProviderMigrations(&providers)

	// Merge cloud catalog providers into local providers.
	if cm.mergeProvidersWithCatalog(&providers) {
		changed = true
	}

	// Derive availability from real credentials; persist flips so other
	// providers.json consumers (desktop) see the same state.
	if cm.updateProviderAvailability(&providers) {
		changed = true
	}

	if changed {
		_ = cm.saveProvidersLocked(providers)
	}

	return providers, nil
}

// ProvidersJSONMu serializes every in-process read-modify-write of
// ~/.swarmos/providers.json. Multiple refreshers (OpenRouter, Cursor,
// Anthropic, Codex, capabilities enrichment) and the settings UI rewrite the
// whole file; without a shared lock, concurrent writers silently drop each
// other's models (last-writer-wins).
var ProvidersJSONMu sync.Mutex

// AtomicWriteFile writes data via a temp file + rename in the target's
// directory so readers never observe a truncated/partial file.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// SaveProviders saves provider configuration
func (cm *ConfigManager) SaveProviders(providers []ProviderConfig) error {
	unlock, err := cm.lockProvidersJSON()
	if err != nil {
		return err
	}
	defer unlock()
	return cm.saveProvidersLocked(providers)
}

func (cm *ConfigManager) saveProvidersLocked(providers []ProviderConfig) error {
	path := cm.GetConfigPath("providers.json")
	if err := observability.GuardTestWrite(path); err != nil {
		return err
	}
	observability.RecordConfigWrite(path, "save")

	data, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal providers: %w", err)
	}

	if err := AtomicWriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write providers.json: %w", err)
	}
	if manager := tuianalytics.DefaultManager(); manager != nil {
		analyticsProviders := make([]ProviderConfig, len(providers))
		copy(analyticsProviders, providers)
		for i := range analyticsProviders {
			analyticsProviders[i].APIKey = ""
		}
		manager.CaptureSettingsMutation("chat.providers", nil, analyticsProviders)
	}

	return nil
}

func (cm *ConfigManager) lockProvidersJSON() (func(), error) {
	ProvidersJSONMu.Lock()
	lockFile, err := os.OpenFile(cm.GetConfigPath("providers.json")+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		ProvidersJSONMu.Unlock()
		return nil, fmt.Errorf("failed to open providers.json lock: %w", err)
	}
	if err := lockFile.Chmod(0o600); err != nil {
		lockFile.Close()
		ProvidersJSONMu.Unlock()
		return nil, fmt.Errorf("failed to secure providers.json lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		ProvidersJSONMu.Unlock()
		return nil, fmt.Errorf("failed to lock providers.json: %w", err)
	}
	return func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		_ = lockFile.Close()
		ProvidersJSONMu.Unlock()
	}, nil
}

// MarkOAuthProviderAuthenticated persists availability and refresh metadata
// after credentials have been accepted by the live provider builder.
func (cm *ConfigManager) MarkOAuthProviderAuthenticated(providerName string) error {
	unlock, err := cm.lockProvidersJSON()
	if err != nil {
		return err
	}
	defer unlock()
	providers, err := cm.loadProvidersLocked()
	if err != nil {
		return err
	}
	found := false
	for i := range providers {
		if strings.EqualFold(providers[i].Name, providerName) &&
			strings.EqualFold(providers[i].Type, "oauth") {
			providers[i].Available = true
			providers[i].LastRefreshed = time.Now().Format(time.RFC3339)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("OAuth provider %q not found in providers.json", providerName)
	}
	return cm.saveProvidersLocked(providers)
}

// LoadConfig loads the main config. Repeated calls are cheap: it caches the
// parsed result keyed on the file's mtime, so no disk I/O or JSON work is done
// unless config.json has actually been written since the last call.
//
// Callers always receive a fresh copy of the cached config so that mutations
// do not bleed back into the cache.
func (cm *ConfigManager) LoadConfig() (*SwarmOSConfig, error) {
	// Resolve config.yaml -> config.yml -> config.json (YAML is the new default
	// write format; legacy JSON is still read transparently). If none exist,
	// path is the YAML write target and the os.Stat below takes the
	// default-without-save branch.
	path, _ := configformat.ResolvePath(cm.configDir, "config")

	// Check mtime — if file is unchanged, return a copy of the cached result.
	if fi, err := os.Stat(path); err == nil {
		configFileCache.mu.Lock()
		if configFileCache.path == path && configFileCache.config != nil &&
			fi.ModTime().Equal(configFileCache.modTime) {
			// Return a shallow copy so callers cannot mutate the cached snapshot.
			cp := *configFileCache.config
			configFileCache.mu.Unlock()
			return &cp, nil
		}
		configFileCache.mu.Unlock()
	} else if os.IsNotExist(err) {
		// File doesn't exist — return default config without saving
		// This prevents auto-creation on read operations
		config := &SwarmOSConfig{
			CurrentProvider:       defaultCurrentProvider,
			CurrentModel:          defaultCurrentModel,
			Language:              string(i18n.LanguageEnglish),
			ReasoningEffort:       sdkprovider.ReasoningEffortAuto,
			EnableMicroCompaction: false, // DISABLED by default - prevents context loss
			MicroRetentionCount:   defaultMicroRetentionCount,
			CompletionConfirmMax:  1,
			RetrySettings: func() *RetrySettingsConfig {
				defaults := DefaultRetrySettings()
				return &defaults
			}(),
			ChatFallbackChain: fallback.NewChain(defaultCurrentProvider, defaultCurrentModel),
			ModelRateLimits:   []ModelRateLimitConfig{},
		}
		// Don't save here - just return defaults
		// The config will be saved when user explicitly saves settings
		return config, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", filepath.Base(path), err)
	}

	var config SwarmOSConfig
	if err := configformat.Unmarshal(data, configformat.FormatForPath(path), &config); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", filepath.Base(path), err)
	}

	// Set defaults for micro-compaction if not present
	if config.MicroRetentionCount == 0 {
		config.MicroRetentionCount = defaultMicroRetentionCount
	}
	if config.CompletionConfirmMax <= 0 {
		config.CompletionConfirmMax = 1
	}
	// Note: EnableMicroCompaction defaults to false (zero value for bool)
	// but we want it enabled by default, so only set if config is empty
	if config.CurrentProvider == "" {
		config.EnableMicroCompaction = true
		config.CurrentProvider = defaultCurrentProvider
	}
	if config.CurrentModel == "" {
		config.CurrentModel = defaultCurrentModel
	}
	config.Language = string(i18n.NormalizeLanguage(config.Language))
	config.SetReasoningEffort(config.ReasoningEffort)
	config.applyReliabilityDefaults()

	// Cache an immutable snapshot keyed on file path + mtime.
	if fi, err := os.Stat(path); err == nil {
		snapshot := config // copy into cache
		configFileCache.mu.Lock()
		configFileCache.path = path
		configFileCache.modTime = fi.ModTime()
		configFileCache.config = &snapshot
		configFileCache.mu.Unlock()
	}

	// Return a copy so the caller cannot mutate the cached snapshot.
	cp := config
	return &cp, nil
}

// SaveConfig saves the main config. It writes config.yaml by default (the new
// canonical format); any pre-existing config.json is left untouched on disk and
// is shadowed by config.yaml via read precedence, so the switch is reversible.
func (cm *ConfigManager) SaveConfig(config *SwarmOSConfig) error {
	path := configformat.WritePath(cm.configDir, "config") // config.yaml
	if err := observability.GuardTestWrite(path); err != nil {
		return err
	}
	observability.RecordConfigWrite(path, "save")

	config.Language = string(i18n.NormalizeLanguage(config.Language))
	if err := configformat.SaveAs(path, config, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", filepath.Base(path), err)
	}

	// Invalidate the LoadConfig mtime cache so the next read picks up the new file.
	configFileCache.mu.Lock()
	configFileCache.config = nil
	configFileCache.mu.Unlock()
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureSettingsMutation("chat.config", nil, config)
	}

	return nil
}

// InvalidateConfigCache clears the package-level config cache.
// This MUST be called when the config source changes (e.g., switching between
// global and project config) to ensure subsequent LoadConfig calls read fresh data.
func InvalidateConfigCache() {
	configFileCache.mu.Lock()
	configFileCache.config = nil
	configFileCache.path = "" // Clear path to force re-detection of config location
	configFileCache.mu.Unlock()
}

// UpdateConfig loads the config, applies updates, and saves it back.
// The update closure runs on a private copy; the cache is only updated after a
// successful save so a SaveConfig failure never leaves memory ahead of disk.
func (cm *ConfigManager) UpdateConfig(update func(*SwarmOSConfig)) (*SwarmOSConfig, error) {
	config, err := cm.LoadConfig()
	if err != nil {
		return nil, err
	}
	if config == nil {
		config = &SwarmOSConfig{}
	}
	// Apply mutations to the copy returned by LoadConfig.
	update(config)
	// Persist — SaveConfig already invalidates the cache on success.
	if err := cm.SaveConfig(config); err != nil {
		return nil, err
	}
	return config, nil
}

// UpdateCurrentModel updates the active provider/model and records it in recents.
func (cm *ConfigManager) UpdateCurrentModel(provider, model string) (*SwarmOSConfig, error) {
	return cm.UpdateConfig(func(cfg *SwarmOSConfig) {
		cfg.CurrentProvider = provider
		cfg.CurrentModel = model
		cfg.RecentModels = AddRecentModels(cfg.RecentModels, provider, model, MaxRecentModels)
	})
}

// getDefaultProviders returns the default provider configuration.
//
// The set of built-in providers (identity, OAuth vs. API-key auth, display
// name, color, base URL) is owned by the SDK catalog — sdkprovider.BuiltinProviders()
// — so there is a single source of truth shared with applyProviderMigrations'
// self-heal path. Model metadata is joined in from the catwalk-backed registry
// by the catalog entry's RegistryID.
func (cm *ConfigManager) getDefaultProviders() []ProviderConfig {
	// Index registry models by provider id for an O(1) join below.
	modelsByRegistryID := make(map[string][]ModelConfig)
	for _, rp := range registry.GetProviders() {
		modelsByRegistryID[rp.ID] = mapRegistryModels(rp.Models)
	}

	var providers []ProviderConfig
	for _, bp := range sdkprovider.BuiltinProviders() {
		providers = append(providers, providerConfigFromBuiltin(bp, modelsByRegistryID))
	}

	// Fallback for custom/empty setups where the catalog yields nothing.
	if len(providers) == 0 {
		return []ProviderConfig{
			{
				Name:        "ClaudeCode",
				DisplayName: "Claude Code (OAuth)",
				Color:       "#00FFFF",
				Type:        "oauth",
				APIType:     "anthropic",
				Available:   true,
				Models: []ModelConfig{
					{ID: "claude-sonnet-4-6", DisplayName: "Claude 4.6 Sonnet", Context: "300K", ContextWindow: sdkprovider.DefaultContextWindowCap},
				},
			},
		}
	}

	return providers
}

// providerConfigFromBuiltin converts an SDK catalog entry into the TUI's
// ProviderConfig, attaching models from the registry (keyed by RegistryID,
// defaulting to APIType). xAI falls back to a hardcoded model list when catwalk
// does not yet include it.
func providerConfigFromBuiltin(bp sdkprovider.BuiltinProvider, modelsByRegistryID map[string][]ModelConfig) ProviderConfig {
	regID := bp.RegistryID
	if regID == "" {
		regID = bp.APIType
	}
	models := modelsByRegistryID[regID]
	if len(models) == 0 && strings.EqualFold(bp.APIType, "xai") {
		models = xaiBuiltinModels()
	}
	// Codex is not catwalk-backed (RegistryID "codex"): seed the ChatGPT-backend
	// model list so the provider is usable immediately after OAuth login. Codex
	// model discovery replaces these with the live /models catalog when it runs.
	if len(models) == 0 && strings.EqualFold(bp.Name, "codex") {
		models = codexBuiltinModels()
	}
	// Cursor is not catwalk-backed; seed a small built-in list so the provider
	// is usable before the user triggers a live refresh (which replaces these
	// with the full AvailableModels catalog from api2.cursor.sh).
	if len(models) == 0 && strings.EqualFold(bp.Name, "Cursor") {
		models = cursorBuiltinModels()
	}
	return ProviderConfig{
		Name:           bp.Name,
		DisplayName:    bp.DisplayName,
		Color:          bp.Color,
		Type:           bp.AuthType,
		APIType:        bp.APIType,
		BaseURL:        bp.BaseURL,
		HTTPMaxRetries: bp.HTTPMaxRetries,
		Available:      bp.DefaultAvailable,
		Models:         models,
	}
}

// mapRegistryModels converts registry models into the TUI's ModelConfig format.
func mapRegistryModels(models []registry.Model) []ModelConfig {
	var res []ModelConfig
	for _, m := range models {
		context := ""
		if m.ContextWindow > 0 {
			if m.ContextWindow >= 1000000 {
				context = fmt.Sprintf("%dM", m.ContextWindow/1000000)
			} else {
				context = fmt.Sprintf("%dK", m.ContextWindow/1000)
			}
		}
		supportsReasoning := m.CanReason
		res = append(res, ModelConfig{
			ID:                      m.ID,
			DisplayName:             m.Name,
			Context:                 context,
			ContextWindow:           int(m.ContextWindow),
			SupportsReasoningEffort: &supportsReasoning,
			ReasoningEfforts:        m.ReasoningLevels,
			CostInput:               m.CostPer1MIn,
			CostOutput:              m.CostPer1MOut,
			Reasoning:               m.CanReason,
			ToolCall:                true, // Most SOTA models support tool use
		})
	}
	return res
}

// xaiBuiltinModels returns the hardcoded xAI model list used when catwalk
// does not yet include xAI.
//
// Context windows per docs.x.ai (2026): Grok 4.3 publishes 1M tokens and
// Grok 4.1 publishes 2M — both capped at the SDK's 300K default policy
// (provider.DefaultContextWindowCap). The mini variants have no published
// window on docs.x.ai, so they keep the conservative legacy value.
func xaiBuiltinModels() []ModelConfig {
	b := func(v bool) *bool { return &v }
	return []ModelConfig{
		{ID: "grok-4.3", DisplayName: "Grok 4.3", Context: "300K", ContextWindow: sdkprovider.DefaultContextWindowCap, SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"}, Reasoning: true, ToolCall: true, CostInput: 1.25, CostOutput: 2.50},
		{ID: "grok-4.3-mini", DisplayName: "Grok 4.3 Mini", Context: "131K", ContextWindow: 131072, SupportsReasoningEffort: b(false), Reasoning: false, ToolCall: true, CostInput: 0.30, CostOutput: 0.50},
		{ID: "grok-4-1", DisplayName: "Grok 4.1", Context: "300K", ContextWindow: sdkprovider.DefaultContextWindowCap, SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"}, Reasoning: true, ToolCall: true, CostInput: 1.25, CostOutput: 2.50},
		{ID: "grok-4-1-mini", DisplayName: "Grok 4.1 Mini", Context: "131K", ContextWindow: 131072, SupportsReasoningEffort: b(false), Reasoning: false, ToolCall: true, CostInput: 0.20, CostOutput: 0.50},
	}
}

// codexBuiltinModels returns the seed model list for the codex (OpenAI Codex
// OAuth) provider — the ChatGPT-subscription models served by
// chatgpt.com/backend-api/codex. IDs and context windows mirror the bundled
// codex model catalog (visibility "list" entries only); a live /models refresh
// supersedes this seed when codex model discovery is available.
func codexBuiltinModels() []ModelConfig {
	b := func(v bool) *bool { return &v }
	m := func(id, name string, ctx int, ctxLabel string) ModelConfig {
		effective := sdkprovider.ClampContextWindow(ctx)
		if effective != ctx {
			ctx = effective
			ctxLabel = fmt.Sprintf("%dK", effective/1000)
		}
		return ModelConfig{
			ID: id, DisplayName: name, Context: ctxLabel, ContextWindow: ctx,
			SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"},
			Reasoning: true, ToolCall: true,
		}
	}
	return []ModelConfig{
		m("gpt-5.6-sol", "GPT-5.6-Sol", 372000, "372K"),
		m("gpt-5.6-terra", "GPT-5.6-Terra", 372000, "372K"),
		m("gpt-5.6-luna", "GPT-5.6-Luna", 372000, "372K"),
		m("gpt-5.5", "GPT-5.5", 272000, "272K"),
		m("gpt-5.2", "GPT-5.2", 272000, "272K"),
	}
}

// cursorBuiltinModels returns a small seed list of Cursor models used before a
// live refresh. The TUI's RefreshCursorModels action replaces these with the
// full AvailableModels catalog fetched from api2.cursor.sh.
func cursorBuiltinModels() []ModelConfig {
	b := func(v bool) *bool { return &v }
	return []ModelConfig{
		{ID: "auto", DisplayName: "Auto (Cursor picks)", Context: "", SupportsReasoningEffort: b(false), ToolCall: true},
		{ID: "composer-2.5", DisplayName: "Composer 2.5", Context: "256K", ContextWindow: 256000, SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"}, Reasoning: true, ToolCall: true},
		// Sonnet 5 publishes a 1M window (GA per Anthropic docs, 2026); capped
		// at the SDK's 300K default policy. Gemini 3 Pro (1M) likewise.
		{ID: "sonnet-5", DisplayName: "Claude Sonnet 5", Context: "300K", ContextWindow: sdkprovider.DefaultContextWindowCap, SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"}, Reasoning: true, ToolCall: true},
		{ID: "gpt-5.5", DisplayName: "GPT-5.5", Context: "256K", ContextWindow: 256000, SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"}, Reasoning: true, ToolCall: true},
		{ID: "gemini-3-pro", DisplayName: "Gemini 3 Pro", Context: "300K", ContextWindow: sdkprovider.DefaultContextWindowCap, SupportsReasoningEffort: b(true), ReasoningEfforts: []string{"low", "medium", "high"}, Reasoning: true, ToolCall: true},
	}
}

// providersContains reports whether any provider in the slice has one of the
// given names (case-insensitive).
func providersContains(providers []ProviderConfig, names ...string) bool {
	for _, p := range providers {
		for _, n := range names {
			if strings.EqualFold(p.Name, n) {
				return true
			}
		}
	}
	return false
}

func cloneBoolPtr(b *bool) *bool {
	if b == nil {
		return nil
	}
	cb := *b
	return &cb
}

func cloneStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	res := make([]string, len(s))
	copy(res, s)
	return res
}

// updateProviderAvailability derives every provider's Available flag from
// actual credential state (SDK catalog env vars, OAuth token stores,
// credentials.json, account registries — see internal/provider/credentials).
// Availability is no longer a hand-maintained flag: it is recomputed on every
// load, which is what makes "sign in via OAuth → provider appears" work with
// zero per-provider wiring.
//
// Rules:
//   - Built-in entries derive both directions: a stored token lights them up,
//     a vanished token store turns them honestly off. DefaultAvailable
//     (ClaudeCode) and a key saved on the entry itself always count.
//   - Custom entries never flip down — we cannot enumerate every way a user
//     supplies credentials to a custom endpoint — but they do flip up when a
//     key/env var/credentials.json entry is detected.
//
// Returns true when any flag changed, so LoadProviders persists the derived
// state for other providers.json consumers (e.g. desktop).
func (cm *ConfigManager) updateProviderAvailability(providers *[]ProviderConfig) bool {
	changed := false
	for i := range *providers {
		p := &(*providers)[i]
		var avail bool
		if bp, isBuiltin := sdkprovider.LookupBuiltinProvider(p.Name); isBuiltin {
			avail = bp.DefaultAvailable || p.APIKey != "" || p.APIKeySecretRef != "" || sdkcredentials.HasStored(p.Name)
		} else {
			avail = p.Available || p.APIKey != "" || p.APIKeySecretRef != "" || sdkcredentials.HasStored(p.Name)
		}
		if p.Available != avail {
			p.Available = avail
			changed = true
		}
	}
	return changed
}

// applyProviderMigrations reconciles a persisted provider list against the
// canonical built-in catalog, re-injecting any built-in provider that has gone
// missing. This is the self-heal path: LoadProviders only falls back to the
// defaults when providers.json is *absent*, so without this a built-in entry
// lost from an existing file (e.g. "ClaudeCode" dropped by a partial rewrite or
// repair) would never reappear and would silently vanish from the auth screen.
//
// It is driven by getDefaultProviders (and thus the SDK catalog), so it covers
// every built-in provider — not just one hard-coded name — and stays in sync
// automatically when the catalog changes. Returns true if anything was added,
// signaling LoadProviders to persist the repaired list.
func (cm *ConfigManager) applyProviderMigrations(providers *[]ProviderConfig) bool {
	changed := false
	for _, def := range cm.getDefaultProviders() {
		idx := indexOfProvider(*providers, def.Name)
		if idx < 0 {
			*providers = append(*providers, def)
			changed = true
			continue
		}
		// Reconcile the canonical identity fields of an existing built-in entry
		// against the catalog. This heals drift when a built-in's catalog
		// definition changes (e.g. Cursor switching from api_key to OAuth), which
		// the add-only path above cannot fix. User-owned data (APIKey, Models,
		// Source) is preserved.
		if reconcileBuiltinProvider(&(*providers)[idx], def) {
			changed = true
		}
	}
	return changed
}

// indexOfProvider returns the index of the provider with the given name
// (case-insensitive), or -1 if not present.
func indexOfProvider(providers []ProviderConfig, name string) int {
	for i := range providers {
		if strings.EqualFold(providers[i].Name, name) {
			return i
		}
	}
	return -1
}

// reconcileBuiltinProvider updates a persisted built-in provider's canonical
// identity fields (auth Type, DisplayName, Color, APIType) to match the catalog
// definition. User-owned fields, including a self-hosted BaseURL, are preserved;
// catalog BaseURL and retry defaults are seeded only when absent. Returns true
// if any field changed.
func reconcileBuiltinProvider(p *ProviderConfig, def ProviderConfig) bool {
	changed := false
	if def.Type != "" && p.Type != def.Type {
		p.Type = def.Type
		changed = true
	}
	if def.DisplayName != "" && p.DisplayName != def.DisplayName {
		p.DisplayName = def.DisplayName
		changed = true
	}
	if def.Color != "" && p.Color != def.Color {
		p.Color = def.Color
		changed = true
	}
	if def.APIType != "" && p.APIType != def.APIType {
		p.APIType = def.APIType
		changed = true
	}
	if p.BaseURL == "" && def.BaseURL != "" {
		p.BaseURL = def.BaseURL
		changed = true
	}
	if p.HTTPMaxRetries == nil && def.HTTPMaxRetries != nil {
		value := *def.HTTPMaxRetries
		p.HTTPMaxRetries = &value
		changed = true
	}
	// Seed models only when the persisted entry has none (don't clobber a list
	// the user has refreshed or customized).
	if len(p.Models) == 0 && len(def.Models) > 0 {
		p.Models = def.Models
		changed = true
	}
	return changed
}

func (cm *ConfigManager) mergeProvidersWithCatalog(providers *[]ProviderConfig) bool {
	// Simple stub for now to fix build
	return false
}

func (cm *ConfigManager) LoadCredentials(providerName string) (string, string, error) {
	providers, err := cm.LoadProviders()
	if err != nil {
		return "", "", err
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	for _, p := range providers {
		if strings.ToLower(p.Name) == name {
			return p.APIKey, p.BaseURL, nil
		}
	}
	return "", "", nil
}

func (cm *ConfigManager) SaveCredentials(providerName, apiKey, baseURL string) error {
	providers, err := cm.LoadProviders()
	if err != nil {
		return err
	}
	name := strings.ToLower(strings.TrimSpace(providerName))
	for i, p := range providers {
		if strings.ToLower(p.Name) == name {
			providers[i].APIKey = apiKey
			if baseURL != "" {
				providers[i].BaseURL = baseURL
			}
			return cm.SaveProviders(providers)
		}
	}
	// Provider not found — nothing to update.
	return nil
}

func (cm *ConfigManager) DeleteCredentials(providerName string) error {
	return cm.SaveCredentials(providerName, "", "")
}
