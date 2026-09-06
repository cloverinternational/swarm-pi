// Package client provides an ergonomic top-level interface to the Swarm SDK.
//
// # Settings API
//
// The SettingsManager interface and CoreConfig provide a standardized way to
// manage client configuration across all Swarm applications (TUI, Desktop, IC).
//
// Basic usage:
//
//	// Create settings manager
//	manager := client.NewSettingsManager("~/.swarm/config/config.yaml")
//
//	// Load settings (returns defaults if file doesn't exist)
//	cfg, err := manager.Load()
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Create client with settings
//	c, err := client.New(
//	    client.WithProvider(cfg.Provider, cfg.Model),
//	    client.WithAPIKey(cfg.APIKey),
//	    client.WithBaseURL(cfg.Endpoint),
//	    client.WithTemperature(cfg.Temperature),
//	    client.WithMaxTokens(cfg.MaxTokens),
//	    client.WithSystemPrompt(cfg.SystemPrompt),
//	)
//
//	// Modify and save settings
//	cfg.Model = "claude-opus-4-7"
//	if err := manager.Save(cfg); err != nil {
//	    log.Fatal(err)
//	}
//
// CONTRACT: CoreConfig.Provider must be a valid Provider constant
// CONTRACT: Use NewSettingsManager() to create a settings manager
// CONTRACT: Always handle errors from Load() and Save()
//
// See swarm-sdk/docs/settings.md for detailed usage examples.
package client

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// CoreConfig holds the essential settings needed by all Swarm clients.
// This struct is designed to be embedded by client-specific config types.
//
// The zero value is NOT usable - always use Defaults() or Load() to get
// a properly initialized configuration.
//
// CONTRACT: Provider must be one of the predefined constants
// CONTRACT: Model must be non-empty
//
// Example:
//
//	cfg := (&CoreConfig{}).Defaults()
//	cfg.Provider = client.ProviderOpenAI
//	cfg.Model = "gpt-4o"
type CoreConfig struct {
	// Provider is the LLM provider to use.
	// Must be one of: ProviderAnthropic, ProviderOpenAI, ProviderGemini
	//
	// CONTRACT: Use ParseProvider() for dynamic provider selection from strings
	Provider Provider `json:"provider"`

	// OriginalProvider is the original provider name string before normalization.
	// This preserves the alias name (e.g., "fireworks", "groq") for credential lookup
	// while Provider stores the canonical type (e.g., ProviderOpenAI).
	// If empty, Provider.String() is used as fallback.
	OriginalProvider string `json:"originalProvider,omitempty"`

	// Model is the specific model ID to use (e.g., "claude-sonnet-4-5", "gpt-4o")
	Model string `json:"model"`

	// APIKey is the authentication credential for the provider.
	// Not needed for OAuth-based providers.
	APIKey string `json:"apiKey,omitempty"`

	// Endpoint is the base URL for API requests.
	// Used for custom endpoints or OpenAI-compatible providers.
	Endpoint string `json:"endpoint,omitempty"`

	// Temperature controls randomness (0.0 = deterministic, 1.0 = creative)
	// Default: 0.7
	Temperature float64 `json:"temperature,omitempty"`

	// MaxTokens limits the maximum response length
	// Default: 4096
	MaxTokens int `json:"maxTokens,omitempty"`

	// ContextWindow sets the maximum context size for the conversation
	// Default: 200000
	ContextWindow int `json:"contextWindow,omitempty"`

	// SystemPrompt sets the default system prompt for new conversations
	// Default: "You are a helpful coding assistant."
	SystemPrompt string `json:"systemPrompt,omitempty"`

	// Feature flags - common subset across all clients

	// EnableWebSearch allows the agent to search the web
	// Default: true
	EnableWebSearch bool `json:"enableWebSearch,omitempty"`

	// EnableProjectMemory enables the project-specific memory system
	// Default: true
	EnableProjectMemory bool `json:"enableProjectMemory,omitempty"`

	// EnableSteering enables tool-call interception and meta-cognitive control
	// Default: true
	EnableSteering bool `json:"enableSteering,omitempty"`

	// EnableAutoCompaction enables automatic context compaction when token threshold is reached
	// Default: true
	EnableAutoCompaction bool `json:"enableAutoCompaction,omitempty"`

	// AutoCompactionThresholdPercent triggers compaction at this percentage of context window
	// Default: 0.85 (85%)
	AutoCompactionThresholdPercent float64 `json:"autoCompactionThresholdPercent,omitempty"`

	// StreamResponse enables streaming responses from the LLM
	// Default: true
	StreamResponse bool `json:"streamResponse,omitempty"`

	// CachePrompts enables prompt caching where supported by the provider
	// Default: true
	CachePrompts bool `json:"cachePrompts,omitempty"`

	// ReasoningEffort controls the reasoning level for models that support it
	// Values: "auto", "low", "medium", "high"
	// Default: "auto"
	ReasoningEffort string `json:"reasoningEffort,omitempty"`

	// ReasoningEffortByModel is per-model reasoning effort overrides (model key -> effort level)
	ReasoningEffortByModel map[string]string `json:"reasoningByModel,omitempty"`

	// EnableMicroCompaction enables automatic compaction of micro-decisions
	// Default: true
	EnableMicroCompaction bool `json:"enableMicroCompaction,omitempty"`

	// MicroRetentionCount is the number of recent messages to keep when micro-compacting
	// Default: 3
	MicroRetentionCount int `json:"microRetentionCount,omitempty"`

	// AdvancedToolMode enables deferred tool loading for token optimization
	// Default: false
	AdvancedToolMode bool `json:"advancedToolMode,omitempty"`

	// DeferTokenThreshold auto-defers tools above this token estimate
	// Default: 500
	DeferTokenThreshold int `json:"deferTokenThreshold,omitempty"`

	// PlanModeEnabled registers enter/exit_plan_mode tools
	// Default: false
	PlanModeEnabled bool `json:"planModeEnabled,omitempty"`

	// PlanModeAutoClear auto-compacts context after plan approval
	// Default: true
	PlanModeAutoClear bool `json:"planModeAutoClear,omitempty"`

	// ComputerUseEnabled registers screenshot, mouse, keyboard tools (Linux only)
	// Default: false
	ComputerUseEnabled bool `json:"computerUseEnabled,omitempty"`

	// EnableCodeMode enables JavaScript sandbox for batched tool calls
	// Default: false
	EnableCodeMode bool `json:"enableCodeMode,omitempty"`

	// CompletionConfirm asks the agent to verify completed work before returning.
	// Default: false
	CompletionConfirm bool `json:"completion_confirm,omitempty"`

	// CompletionConfirmMax bounds completion verification prompts.
	// Default: 1
	CompletionConfirmMax int `json:"completion_confirm_max,omitempty"`

	// ProactiveSummarizeThreshold optionally lowers the auto-compaction threshold.
	// Default: 0 (disabled)
	ProactiveSummarizeThreshold float64 `json:"proactive_summarize_threshold,omitempty"`

	// AutoCompactionContinueIfRunning allows compaction even when tools are running
	// Default: false
	AutoCompactionContinueIfRunning bool `json:"autoCompactionContinueIfRunning,omitempty"`

	// PlanModeFileName is the filename for plan mode (default: PLAN.md)
	// Default: "PLAN.md"
	PlanModeFileName string `json:"planModeFileName,omitempty"`
}

// Defaults returns a CoreConfig with sensible default values.
// Use this to initialize a new configuration.
//
// Example:
//
//	cfg := (&CoreConfig{}).Defaults()
//	// Modify as needed
//	cfg.Provider = client.ProviderGemini
func (c *CoreConfig) Defaults() CoreConfig {
	return CoreConfig{
		Provider:                        ProviderAnthropic,
		Model:                           "claude-sonnet-4-5",
		Temperature:                     0.7,
		MaxTokens:                       4096,
		ContextWindow:                   200000,
		SystemPrompt:                    "You are a helpful coding assistant.",
		EnableWebSearch:                 true,
		EnableProjectMemory:             true,
		EnableSteering:                  true,
		EnableAutoCompaction:            true,
		AutoCompactionThresholdPercent:  0.85,
		StreamResponse:                  true,
		CachePrompts:                    true,
		ReasoningEffort:                 "auto",
		ReasoningEffortByModel:          make(map[string]string),
		EnableMicroCompaction:           true,
		MicroRetentionCount:             3,
		AdvancedToolMode:                false,
		DeferTokenThreshold:             500,
		PlanModeEnabled:                 false,
		PlanModeAutoClear:               true,
		ComputerUseEnabled:              false,
		EnableCodeMode:                  false,
		CompletionConfirm:               false,
		CompletionConfirmMax:            1,
		ProactiveSummarizeThreshold:     0,
		AutoCompactionContinueIfRunning: false,
		PlanModeFileName:                "PLAN.md",
	}
}

// Validate ensures the configuration has valid values.
// Returns a ContractViolation if validation fails.
//
// Checks:
//   - Provider is valid
//   - Model is non-empty
//   - Temperature is in range [0.0, 2.0]
//   - MaxTokens is positive
//   - ContextWindow is positive
//
// Example:
//
//	if err := cfg.Validate(); err != nil {
//	    var cv *client.ContractViolation
//	    if errors.As(err, &cv) {
//	        fmt.Printf("Invalid config: %s\n", cv.Hint)
//	    }
//	}
func (c *CoreConfig) Validate() error {
	if !c.Provider.IsValid() {
		return &ContractViolation{
			Violation: fmt.Sprintf("Invalid provider: %s", c.Provider),
			Required:  "Provider must be one of: anthropic, openai, gemini",
			Hint:      "Use client.ParseProvider() or client.ProviderAnthropic/ProviderOpenAI/ProviderGemini",
		}
	}

	if c.Model == "" {
		return &ContractViolation{
			Violation: "Model is empty",
			Required:  "Model must be a non-empty string",
			Hint:      "Set cfg.Model to a valid model ID (e.g., 'claude-sonnet-4-5', 'gpt-4o')",
		}
	}

	if c.Temperature < 0.0 || c.Temperature > 2.0 {
		return &ContractViolation{
			Violation: fmt.Sprintf("Temperature out of range: %f", c.Temperature),
			Required:  "Temperature must be between 0.0 and 2.0",
			Hint:      "Set cfg.Temperature to a value in [0.0, 2.0] (default: 0.7)",
		}
	}

	if c.MaxTokens <= 0 {
		return &ContractViolation{
			Violation: fmt.Sprintf("Invalid maxTokens: %d", c.MaxTokens),
			Required:  "MaxTokens must be positive",
			Hint:      "Set cfg.MaxTokens to a positive integer (default: 4096)",
		}
	}

	if c.ContextWindow <= 0 {
		return &ContractViolation{
			Violation: fmt.Sprintf("Invalid contextWindow: %d", c.ContextWindow),
			Required:  "ContextWindow must be positive",
			Hint:      "Set cfg.ContextWindow to a positive integer (default: 200000)",
		}
	}

	return nil
}

// ToOptions converts CoreConfig to a slice of client.Option functions.
// Use this to create a client from loaded settings.
//
// Example:
//
//	cfg, _ := manager.Load()
//	opts := cfg.ToOptions()
//	c, err := client.New(opts...)
//
// CONTRACT: Validate() should be called before ToOptions()
func (c *CoreConfig) ToOptions() []Option {
	// Use original provider name for credential lookup if available.
	// This ensures OpenAI-compatible providers (fireworks, groq, etc.)
	// look up their specific API keys (FIREWORKS_API_KEY) rather than
	// the generic OPENAI_API_KEY.
	providerName := string(c.Provider)
	if c.OriginalProvider != "" {
		providerName = c.OriginalProvider
	}

	opts := []Option{
		WithProviderString(providerName, c.Model),
		WithMaxTokens(c.MaxTokens),
		WithTemperature(c.Temperature),
		WithContextWindow(c.ContextWindow),
		WithSystemPrompt(c.SystemPrompt),
	}

	if c.APIKey != "" {
		opts = append(opts, WithAPIKey(c.APIKey))
	}

	if c.Endpoint != "" {
		opts = append(opts, WithBaseURL(c.Endpoint))
	}

	return opts
}

// SettingsManager handles persistence of CoreConfig.
// Implementations may use file storage, databases, or remote APIs.
//
// CONTRACT: Load() must return defaults if configuration doesn't exist
// CONTRACT: Save() must write atomically to prevent corruption
// CONTRACT: Path() must return the canonical configuration location
type SettingsManager interface {
	// Load reads settings from storage, returning defaults if not found.
	// Returns an error only for I/O or parse failures, not for missing files.
	Load() (*CoreConfig, error)

	// Save persists settings to storage atomically.
	// Creates parent directories if needed.
	Save(*CoreConfig) error

	// Path returns the configuration storage location.
	// For file-based managers, this is the file path.
	Path() string
}

// NewSettingsManager creates a file-based settings manager.
// The configPath should be an absolute or user-home-relative path.
//
// For the standard TUI/CLI location:
//
//	home, _ := os.UserHomeDir()
//	manager := client.NewSettingsManager(filepath.Join(home, ".swarm", "config.json"))
//
// For IC workspace-relative:
//
//	manager := client.NewSettingsManager(filepath.Join(workspaceRoot, ".swarm-ic", "settings.json"))
//
// CONTRACT: configPath must be non-empty
func NewSettingsManager(configPath string) SettingsManager {
	return &fileSettingsManager{path: configPath}
}

// fileSettingsManager implements SettingsManager with JSON file storage.
type fileSettingsManager struct {
	path string
}

// Load reads settings from disk, returning defaults if file doesn't exist.
// Implements error handling per the SettingsManager contract.
func (f *fileSettingsManager) Load() (*CoreConfig, error) {
	var cfg CoreConfig
	// Decode by the path's extension (.yaml/.yml via YAML, .json via JSON).
	if err := configformat.LoadFile(f.path, &cfg); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Return defaults for new installations
			defaults := (&CoreConfig{}).Defaults()
			return &defaults, nil
		}
		return nil, fmt.Errorf("settings load: %w", err)
	}
	return &cfg, nil
}

// Save writes settings to disk atomically.
// Creates parent directories if they don't exist.
// Uses 0600 permissions for security (API keys in config).
func (f *fileSettingsManager) Save(cfg *CoreConfig) error {
	if cfg == nil {
		return &ContractViolation{
			Violation: "Cannot save nil config",
			Required:  "Config must be non-nil",
			Hint:      "Ensure cfg is initialized before calling Save()",
		}
	}

	// Encode by the path's extension and write atomically (temp file + rename).
	// .json paths stay JSON; .yaml/.yml paths emit YAML. Honors json tags.
	if err := configformat.SaveAs(f.path, cfg, 0600, configformat.Atomic()); err != nil {
		return fmt.Errorf("settings save: %w", err)
	}
	return nil
}

// Path returns the configuration file path.
func (f *fileSettingsManager) Path() string {
	return f.path
}

// SettingsManagerForPath is a convenience function that creates a SettingsManager
// for the standard Swarm configuration path (~/.swarm/config/config.yaml).
//
// Example:
//
//	manager, err := client.SettingsManagerForPath()
//	if err != nil {
//	    log.Fatal(err)
//	}
//	cfg, err := manager.Load()
func SettingsManagerForPath() (SettingsManager, error) {
	// Resolve config.yaml -> .yml -> .json so we read the migrated file (and
	// write YAML by default for new installs). Canonical dir: ~/.swarm/config.
	path, _ := configformat.ResolvePath(paths.Config(), "config")
	return NewSettingsManager(path), nil
}

// GetReasoningEffort returns the reasoning effort level, normalized to a valid value.
// Returns "auto" if not set or empty.
func (c *CoreConfig) GetReasoningEffort() string {
	if c == nil || c.ReasoningEffort == "" {
		return "auto"
	}
	effort := strings.ToLower(strings.TrimSpace(c.ReasoningEffort))
	switch effort {
	case "low", "medium", "high", "max":
		return effort
	default:
		return "auto"
	}
}

// SetReasoningEffort sets the reasoning effort level, normalizing the value.
// Use "auto" to reset to automatic selection.
func (c *CoreConfig) SetReasoningEffort(effort string) {
	if c == nil {
		return
	}
	normalized := strings.ToLower(strings.TrimSpace(effort))
	switch normalized {
	case "low", "medium", "high", "max":
		c.ReasoningEffort = normalized
	default:
		c.ReasoningEffort = "auto"
	}
}

// reasoningModelKey generates a unique key for provider/model combinations.
func reasoningModelKey(p Provider, model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	if string(p) == "" || m == "" {
		return ""
	}
	return string(p) + "/" + m
}

// GetReasoningEffortForModel returns per-model reasoning effort, falling back to global setting.
func (c *CoreConfig) GetReasoningEffortForModel(p Provider, model string) string {
	if c == nil {
		return "auto"
	}
	if len(c.ReasoningEffortByModel) > 0 {
		if effort, ok := c.ReasoningEffortByModel[reasoningModelKey(p, model)]; ok {
			normalized := strings.ToLower(strings.TrimSpace(effort))
			switch normalized {
			case "low", "medium", "high", "max":
				return normalized
			default:
				return "auto"
			}
		}
	}
	return c.GetReasoningEffort()
}

// SetReasoningEffortForModel sets per-model reasoning effort override.
// Setting "auto" removes the override.
func (c *CoreConfig) SetReasoningEffortForModel(p Provider, model, effort string) {
	if c == nil {
		return
	}
	key := reasoningModelKey(p, model)
	if key == "" {
		return
	}
	normalized := strings.ToLower(strings.TrimSpace(effort))
	if normalized == "auto" {
		if len(c.ReasoningEffortByModel) > 0 {
			delete(c.ReasoningEffortByModel, key)
		}
		return
	}
	if c.ReasoningEffortByModel == nil {
		c.ReasoningEffortByModel = make(map[string]string)
	}
	switch normalized {
	case "low", "medium", "high", "max":
		c.ReasoningEffortByModel[key] = normalized
	default:
		c.ReasoningEffortByModel[key] = "auto"
	}
}

// ToProviderConfig creates a ProviderConfig from CoreConfig for SDK client creation.
// This is useful when bridging to lower-level SDK components that expect the
// provider package types.
func (c *CoreConfig) ToProviderConfig() (string, string, error) {
	if c == nil {
		return "", "", &ContractViolation{
			Violation: "Cannot convert nil config to provider config",
			Required:  "Config must be initialized",
			Hint:      "Use cfg.Defaults() or NewSettingsManager to create a config",
		}
	}
	if err := c.Validate(); err != nil {
		return "", "", err
	}
	return string(c.Provider), c.Model, nil
}

// MergeFromProviderString parses a string provider name and updates the config.
// This is useful for dynamic provider selection from user input or legacy configs.
// CONTRACT: Returns ContractViolation if the provider name is unknown.
func (c *CoreConfig) MergeFromProviderString(providerName string) error {
	if c == nil {
		return &ContractViolation{
			Violation: "Cannot merge into nil config",
			Required:  "Config must be initialized",
			Hint:      "Use cfg.Defaults() or NewSettingsManager to create a config",
		}
	}
	p, err := ParseProvider(providerName)
	if err != nil {
		return err
	}
	c.Provider = p
	return nil
}
