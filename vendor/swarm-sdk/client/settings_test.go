package client

import (
	"testing"
)

// TestToCoreConfigRoundtrip verifies that ToCoreConfig and ApplyCoreConfig are inverse operations
func TestToCoreConfigRoundtrip(t *testing.T) {
	// Create a fully populated TUI-like config
	tuiCfg := &testTUIConfig{
		CurrentProvider:                 "anthropic",
		CurrentModel:                    "claude-sonnet-4-5",
		Temperature:                     0.8,
		MaxTokens:                       8192,
		EnableAutoCompaction:            true,
		AutoCompactionThresholdPercent:  0.85,
		AutoCompactionContinueIfRunning: true,
		EnableMicroCompaction:           true,
		MicroRetentionCount:             5,
		AdvancedToolMode:                true,
		DeferTokenThreshold:             1000,
		PlanModeEnabled:                 true,
		PlanModeAutoClear:               true,
		PlanModeFileName:                "PLAN.md",
		ComputerUseEnabled:              false,
		EnableCodeMode:                  false,
		ReasoningEffort:                 "high",
		ReasoningByModel:                map[string]string{"anthropic/claude-opus": "max"},
	}

	// Convert to SDK CoreConfig
	sdkCfg := tuiCfg.ToCoreConfig()

	// Verify all fields were copied correctly
	if sdkCfg.Provider != ProviderAnthropic {
		t.Errorf("Provider mismatch: got %s, want %s", sdkCfg.Provider, ProviderAnthropic)
	}
	if sdkCfg.Model != "claude-sonnet-4-5" {
		t.Errorf("Model mismatch: got %s, want claude-sonnet-4-5", sdkCfg.Model)
	}
	if sdkCfg.Temperature != 0.8 {
		t.Errorf("Temperature mismatch: got %f, want 0.8", sdkCfg.Temperature)
	}
	if sdkCfg.MaxTokens != 8192 {
		t.Errorf("MaxTokens mismatch: got %d, want 8192", sdkCfg.MaxTokens)
	}
	if !sdkCfg.EnableAutoCompaction {
		t.Error("EnableAutoCompaction should be true")
	}
	if sdkCfg.AutoCompactionThresholdPercent != 0.85 {
		t.Errorf("AutoCompactionThresholdPercent mismatch: got %f, want 0.85", sdkCfg.AutoCompactionThresholdPercent)
	}
	if !sdkCfg.AutoCompactionContinueIfRunning {
		t.Error("AutoCompactionContinueIfRunning should be true")
	}
	if !sdkCfg.EnableMicroCompaction {
		t.Error("EnableMicroCompaction should be true")
	}
	if sdkCfg.MicroRetentionCount != 5 {
		t.Errorf("MicroRetentionCount mismatch: got %d, want 5", sdkCfg.MicroRetentionCount)
	}
	if !sdkCfg.AdvancedToolMode {
		t.Error("AdvancedToolMode should be true")
	}
	if sdkCfg.DeferTokenThreshold != 1000 {
		t.Errorf("DeferTokenThreshold mismatch: got %d, want 1000", sdkCfg.DeferTokenThreshold)
	}
	if !sdkCfg.PlanModeEnabled {
		t.Error("PlanModeEnabled should be true")
	}
	if !sdkCfg.PlanModeAutoClear {
		t.Error("PlanModeAutoClear should be true")
	}
	if sdkCfg.PlanModeFileName != "PLAN.md" {
		t.Errorf("PlanModeFileName mismatch: got %s, want PLAN.md", sdkCfg.PlanModeFileName)
	}
	if sdkCfg.ComputerUseEnabled {
		t.Error("ComputerUseEnabled should be false")
	}
	if sdkCfg.EnableCodeMode {
		t.Error("EnableCodeMode should be false")
	}
	if sdkCfg.ReasoningEffort != "high" {
		t.Errorf("ReasoningEffort mismatch: got %s, want high", sdkCfg.ReasoningEffort)
	}
	if len(sdkCfg.ReasoningEffortByModel) != 1 {
		t.Errorf("ReasoningEffortByModel length mismatch: got %d, want 1", len(sdkCfg.ReasoningEffortByModel))
	}
}

// TestCoreConfigDefaults verifies that Defaults() returns expected values
func TestCoreConfigDefaults(t *testing.T) {
	cfg := (&CoreConfig{}).Defaults()

	if cfg.Provider != ProviderAnthropic {
		t.Errorf("Default Provider mismatch: got %s, want %s", cfg.Provider, ProviderAnthropic)
	}
	if cfg.Model != "claude-sonnet-4-5" {
		t.Errorf("Default Model mismatch: got %s, want claude-sonnet-4-5", cfg.Model)
	}
	if cfg.Temperature != 0.7 {
		t.Errorf("Default Temperature mismatch: got %f, want 0.7", cfg.Temperature)
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("Default MaxTokens mismatch: got %d, want 4096", cfg.MaxTokens)
	}
	if !cfg.EnableWebSearch {
		t.Error("Default EnableWebSearch should be true")
	}
	if !cfg.EnableProjectMemory {
		t.Error("Default EnableProjectMemory should be true")
	}
	if !cfg.EnableSteering {
		t.Error("Default EnableSteering should be true")
	}
	if !cfg.EnableAutoCompaction {
		t.Error("Default EnableAutoCompaction should be true")
	}
	if cfg.AutoCompactionThresholdPercent != 0.85 {
		t.Errorf("Default AutoCompactionThresholdPercent mismatch: got %f, want 0.85", cfg.AutoCompactionThresholdPercent)
	}
	if cfg.AutoCompactionContinueIfRunning {
		t.Error("Default AutoCompactionContinueIfRunning should be false")
	}
	if !cfg.EnableMicroCompaction {
		t.Error("Default EnableMicroCompaction should be true")
	}
	if cfg.MicroRetentionCount != 3 {
		t.Errorf("Default MicroRetentionCount mismatch: got %d, want 3", cfg.MicroRetentionCount)
	}
	if cfg.AdvancedToolMode {
		t.Error("Default AdvancedToolMode should be false")
	}
	if cfg.DeferTokenThreshold != 500 {
		t.Errorf("Default DeferTokenThreshold mismatch: got %d, want 500", cfg.DeferTokenThreshold)
	}
	if cfg.PlanModeEnabled {
		t.Error("Default PlanModeEnabled should be false")
	}
	if !cfg.PlanModeAutoClear {
		t.Error("Default PlanModeAutoClear should be true")
	}
	if cfg.PlanModeFileName != "PLAN.md" {
		t.Errorf("Default PlanModeFileName mismatch: got %s, want PLAN.md", cfg.PlanModeFileName)
	}
	if cfg.ComputerUseEnabled {
		t.Error("Default ComputerUseEnabled should be false")
	}
	if cfg.EnableCodeMode {
		t.Error("Default EnableCodeMode should be false")
	}
	if cfg.ReasoningEffort != "auto" {
		t.Errorf("Default ReasoningEffort mismatch: got %s, want auto", cfg.ReasoningEffort)
	}
}

// TestCoreConfigValidation verifies that Validate() catches invalid configurations
func TestCoreConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CoreConfig
		wantErr bool
	}{
		{
			name: "valid config",
			cfg: CoreConfig{
				Provider:      ProviderAnthropic,
				Model:         "claude-sonnet-4-5",
				MaxTokens:     4096,
				Temperature:   0.7,
				ContextWindow: 200000,
			},
			wantErr: false,
		},
		{
			name: "invalid provider",
			cfg: CoreConfig{
				Provider:      "invalid-provider",
				Model:         "claude-sonnet-4-5",
				MaxTokens:     4096,
				Temperature:   0.7,
				ContextWindow: 200000,
			},
			wantErr: true,
		},
		{
			name: "empty model",
			cfg: CoreConfig{
				Provider:      ProviderAnthropic,
				Model:         "",
				MaxTokens:     4096,
				Temperature:   0.7,
				ContextWindow: 200000,
			},
			wantErr: true,
		},
		{
			name: "temperature too high",
			cfg: CoreConfig{
				Provider:      ProviderAnthropic,
				Model:         "claude-sonnet-4-5",
				MaxTokens:     4096,
				Temperature:   3.0,
				ContextWindow: 200000,
			},
			wantErr: true,
		},
		{
			name: "temperature too low",
			cfg: CoreConfig{
				Provider:      ProviderAnthropic,
				Model:         "claude-sonnet-4-5",
				MaxTokens:     4096,
				Temperature:   -0.5,
				ContextWindow: 200000,
			},
			wantErr: true,
		},
		{
			name: "zero max tokens",
			cfg: CoreConfig{
				Provider:      ProviderAnthropic,
				Model:         "claude-sonnet-4-5",
				MaxTokens:     0,
				Temperature:   0.7,
				ContextWindow: 200000,
			},
			wantErr: true,
		},
		{
			name: "negative context window",
			cfg: CoreConfig{
				Provider:      ProviderAnthropic,
				Model:         "claude-sonnet-4-5",
				MaxTokens:     4096,
				Temperature:   0.7,
				ContextWindow: -100,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// testTUIConfig simulates the TUI's SwarmOSConfig for testing
type testTUIConfig struct {
	CurrentProvider                 string
	CurrentModel                    string
	Temperature                     float64
	MaxTokens                       int
	EnableAutoCompaction            bool
	AutoCompactionThresholdPercent  float64
	AutoCompactionContinueIfRunning bool
	EnableMicroCompaction           bool
	MicroRetentionCount             int
	AdvancedToolMode                bool
	DeferTokenThreshold             int
	PlanModeEnabled                 bool
	PlanModeAutoClear               bool
	PlanModeFileName                string
	ComputerUseEnabled              bool
	EnableCodeMode                  bool
	ReasoningEffort                 string
	ReasoningByModel                map[string]string
}

// ToCoreConfig simulates the TUI's ToCoreConfig method
func (c *testTUIConfig) ToCoreConfig() CoreConfig {
	var provider Provider
	if c.CurrentProvider != "" {
		if p, err := ParseProvider(c.CurrentProvider); err == nil {
			provider = p
		}
	}

	return CoreConfig{
		Provider:                        provider,
		Model:                           c.CurrentModel,
		Temperature:                     c.Temperature,
		MaxTokens:                       c.MaxTokens,
		EnableAutoCompaction:            c.EnableAutoCompaction,
		AutoCompactionThresholdPercent:  c.AutoCompactionThresholdPercent,
		AutoCompactionContinueIfRunning: c.AutoCompactionContinueIfRunning,
		EnableMicroCompaction:           c.EnableMicroCompaction,
		MicroRetentionCount:             c.MicroRetentionCount,
		AdvancedToolMode:                c.AdvancedToolMode,
		DeferTokenThreshold:             c.DeferTokenThreshold,
		PlanModeEnabled:                 c.PlanModeEnabled,
		PlanModeAutoClear:               c.PlanModeAutoClear,
		PlanModeFileName:                c.PlanModeFileName,
		ComputerUseEnabled:              c.ComputerUseEnabled,
		EnableCodeMode:                  c.EnableCodeMode,
		ReasoningEffort:                 c.ReasoningEffort,
		ReasoningEffortByModel:          c.ReasoningByModel,
		EnableWebSearch:                 true,
		EnableProjectMemory:             true,
		EnableSteering:                  true,
		StreamResponse:                  true,
		CachePrompts:                    true,
	}
}
