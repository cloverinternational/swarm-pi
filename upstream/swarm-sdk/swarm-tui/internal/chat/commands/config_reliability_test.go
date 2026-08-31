package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func newTestConfigManager(t *testing.T) *ConfigManager {
	t.Helper()
	return &ConfigManager{configDir: t.TempDir()}
}

func writeTestConfigJSON(t *testing.T, cm *ConfigManager, raw string) {
	t.Helper()
	configPath := filepath.Join(cm.configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(raw), 0644); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}
}

func TestLoadConfigLegacyDefaultsReliability(t *testing.T) {
	cm := newTestConfigManager(t)
	writeTestConfigJSON(t, cm, `{
  "current_provider": "Anthropic",
  "current_model": "claude-sonnet-4-20250514",
  "compaction_provider": "OpenAI",
  "compaction_model": "gpt-5.1-codex"
}`)

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	retry := cfg.GetRetrySettings()
	if !retry.Enabled {
		t.Fatalf("expected retry defaults to be enabled")
	}
	if retry.MaxRetriesPerProvider != 3 {
		t.Fatalf("expected default max retries 3, got %d", retry.MaxRetriesPerProvider)
	}
	if retry.RotateOnRateLimit {
		t.Fatalf("expected default rotate on rate limit to be false")
	}
	if retry.RetryAfterFallbackMs != 30000 {
		t.Fatalf("expected default retry-after-fallback 30000ms, got %d", retry.RetryAfterFallbackMs)
	}

	chatChain := cfg.GetChatFallbackChain()
	if chatChain == nil {
		t.Fatalf("expected chat fallback chain default")
	}
	if chatChain.Primary.Provider != "Anthropic" || chatChain.Primary.Model != "claude-sonnet-4-20250514" {
		t.Fatalf("unexpected default chat chain primary: %s/%s", chatChain.Primary.Provider, chatChain.Primary.Model)
	}

	limits := cfg.GetModelRateLimits()
	if len(limits) != 0 {
		t.Fatalf("expected no default model rate limits, got %d", len(limits))
	}
	if got := cfg.GetReasoningEffort(); got != sdkprovider.ReasoningEffortAuto {
		t.Fatalf("expected default reasoning effort auto, got %s", got)
	}

	compactionChain := cfg.GetCompactionChain()
	if compactionChain.Primary.Provider != "OpenAI" || compactionChain.Primary.Model != "gpt-5.1-codex" {
		t.Fatalf("legacy compaction migration regression: got %s/%s", compactionChain.Primary.Provider, compactionChain.Primary.Model)
	}
}

func TestConfigReliabilityRoundTrip(t *testing.T) {
	cm := newTestConfigManager(t)
	cfg := &SwarmOSConfig{
		CurrentProvider: "OpenAI",
		CurrentModel:    "gpt-5.1",
		ReasoningEffort: "med",
		RetrySettings: &RetrySettingsConfig{
			Enabled:               true,
			MaxRetriesPerProvider: 5,
			RotateOnRateLimit:     true,
			RetryAfterFallbackMs:  4500,
		},
		ChatFallbackChain: &fallback.Chain{
			Primary: fallback.ModelRef{Provider: "OpenAI", Model: "gpt-5.1"},
			Fallbacks: []fallback.ModelRef{
				{Provider: "Anthropic", Model: "claude-sonnet-4-20250514"},
			},
		},
		ModelRateLimits: []ModelRateLimitConfig{
			{
				Provider:          "OpenAI",
				Model:             "gpt-5.1",
				RequestsPerMinute: 20,
				Burst:             5,
			},
			{
				Provider:          "Anthropic",
				Model:             "claude-sonnet-4-20250514",
				RequestsPerMinute: 12,
				Enabled:           false,
			},
		},
	}

	if err := cm.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	reloaded, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	retry := reloaded.GetRetrySettings()
	if retry.MaxRetriesPerProvider != 5 || !retry.RotateOnRateLimit || retry.RetryAfterFallbackMs != 4500 {
		t.Fatalf("retry settings not preserved: %+v", retry)
	}
	if !retry.Enabled {
		t.Fatalf("retry enabled value should persist")
	}

	if reloaded.ChatFallbackChain == nil || reloaded.ChatFallbackChain.Len() != 2 {
		t.Fatalf("chat fallback chain not preserved")
	}
	if reloaded.ChatFallbackChain.Primary.Provider != "OpenAI" || reloaded.ChatFallbackChain.Primary.Model != "gpt-5.1" {
		t.Fatalf("chat fallback primary not preserved: %+v", reloaded.ChatFallbackChain.Primary)
	}
	if reloaded.ChatFallbackChain.Fallbacks[0].Provider != "Anthropic" {
		t.Fatalf("chat fallback order changed")
	}

	limits := reloaded.GetModelRateLimits()
	if len(limits) != 2 {
		t.Fatalf("expected 2 model rate limits, got %d", len(limits))
	}
	if limits[0].Provider != "OpenAI" || limits[0].RequestsPerMinute != 20 || limits[0].Burst != 5 {
		t.Fatalf("first rate limit not preserved: %+v", limits[0])
	}
	if limits[1].Provider != "Anthropic" || limits[1].Enabled {
		t.Fatalf("second rate limit not preserved: %+v", limits[1])
	}
	if got := reloaded.GetReasoningEffort(); got != sdkprovider.ReasoningEffortMedium {
		t.Fatalf("reasoning effort not normalized on load: got %s", got)
	}
}

func TestSwarmOSConfigReliabilityGettersNilSafe(t *testing.T) {
	var nilCfg *SwarmOSConfig

	retry := nilCfg.GetRetrySettings()
	if !retry.Enabled || retry.MaxRetriesPerProvider != 3 || retry.RetryAfterFallbackMs != 30000 {
		t.Fatalf("unexpected nil-config retry defaults: %+v", retry)
	}

	chain := nilCfg.GetChatFallbackChain()
	if chain == nil || chain.Primary.Provider == "" || chain.Primary.Model == "" {
		t.Fatalf("expected non-empty default chat fallback chain for nil config")
	}

	limits := nilCfg.GetModelRateLimits()
	if len(limits) != 0 {
		t.Fatalf("expected no limits for nil config, got %d", len(limits))
	}
	if got := nilCfg.GetReasoningEffort(); got != sdkprovider.ReasoningEffortAuto {
		t.Fatalf("expected nil config to default reasoning effort to auto, got %s", got)
	}
}

func TestLoadConfigEmptyObjectAndInvalidJSONSafety(t *testing.T) {
	t.Run("empty object defaults", func(t *testing.T) {
		cm := newTestConfigManager(t)
		writeTestConfigJSON(t, cm, `{}`)

		cfg, err := cm.LoadConfig()
		if err != nil {
			t.Fatalf("LoadConfig failed: %v", err)
		}

		retry := cfg.GetRetrySettings()
		if !retry.Enabled || retry.MaxRetriesPerProvider != 3 {
			t.Fatalf("expected retry defaults for empty object, got %+v", retry)
		}
		if cfg.MicroRetentionCount != 0 {
			t.Fatalf("expected micro-compaction default to be disabled (0), got %d", cfg.MicroRetentionCount)
		}
	})

	t.Run("invalid json returns error", func(t *testing.T) {
		cm := newTestConfigManager(t)
		writeTestConfigJSON(t, cm, `{"current_provider":`)

		if _, err := cm.LoadConfig(); err == nil {
			t.Fatalf("expected parse error for invalid json")
		}
	})
}

func TestReasoningEffortPerModelOverride(t *testing.T) {
	cfg := &SwarmOSConfig{
		CurrentProvider: "OpenAI",
		CurrentModel:    "gpt-5.2-codex",
		ReasoningEffort: "low",
		ReasoningByModel: map[string]string{
			"openai/gpt-5.2-codex": "x-high",
		},
	}
	cfg.applyReliabilityDefaults()

	if got := cfg.GetReasoningEffort(); got != sdkprovider.ReasoningEffortLow {
		t.Fatalf("expected normalized global reasoning effort low, got %s", got)
	}
	if got := cfg.GetReasoningEffortForModel("OpenAI", "gpt-5.2-codex"); got != sdkprovider.ReasoningEffortXHigh {
		t.Fatalf("expected per-model override xhigh, got %s", got)
	}
	if got := cfg.GetReasoningEffortForModel("OpenAI", "gpt-5.1"); got != sdkprovider.ReasoningEffortLow {
		t.Fatalf("expected global fallback low, got %s", got)
	}

	cfg.SetReasoningEffortForModel("OpenAI", "gpt-5.2-codex", "auto")
	if got := cfg.GetReasoningEffortForModel("OpenAI", "gpt-5.2-codex"); got != sdkprovider.ReasoningEffortLow {
		t.Fatalf("expected override removal to fallback to global low, got %s", got)
	}
}

// TestGetChatFallbackChainSanitizesPersistedNonChatPrimary reproduces
// Swarm-Code/mono#66 against the exact JSON shape observed in a live,
// broken ~/.swarmos/config.json: a chat_fallback_chain whose only entry
// (primary, no fallbacks) is a legacy non-chat OpenAI model. Returning
// that chain as-is means every headless run that touches the chat/fallback
// path 400s and aborts. GetChatFallbackChain must instead fall through to
// the CurrentProvider/CurrentModel default.
func TestGetChatFallbackChainSanitizesPersistedNonChatPrimary(t *testing.T) {
	cm := newTestConfigManager(t)
	writeTestConfigJSON(t, cm, `{
  "current_provider": "ClaudeCode",
  "current_model": "claude-sonnet-4-6",
  "chat_fallback_chain": {
    "primary": {
      "provider": "openai",
      "model": "babbage-002"
    }
  }
}`)

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	chain := cfg.GetChatFallbackChain()
	if chain == nil {
		t.Fatalf("expected a sanitized fallback chain, got nil")
	}
	if chain.Primary.Provider != "ClaudeCode" || chain.Primary.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected sanitized chain to fall through to current provider/model, got %s/%s",
			chain.Primary.Provider, chain.Primary.Model)
	}
}

// TestGetChatFallbackChainSanitizesNonChatFallbackEntry verifies a
// non-chat entry mixed into an otherwise-valid persisted chain is dropped
// rather than poisoning the whole chain or being silently kept.
func TestGetChatFallbackChainSanitizesNonChatFallbackEntry(t *testing.T) {
	cm := newTestConfigManager(t)
	writeTestConfigJSON(t, cm, `{
  "chat_fallback_chain": {
    "primary": {"provider": "Anthropic", "model": "claude-sonnet-4-6"},
    "fallbacks": [
      {"provider": "openai", "model": "babbage-002"},
      {"provider": "Cerebras", "model": "llama-3.3-70b"}
    ]
  }
}`)

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	chain := cfg.GetChatFallbackChain()
	if chain == nil {
		t.Fatalf("expected a sanitized fallback chain, got nil")
	}
	if chain.Primary.Provider != "Anthropic" || chain.Primary.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected primary to survive sanitization unchanged, got %s/%s", chain.Primary.Provider, chain.Primary.Model)
	}
	if len(chain.Fallbacks) != 1 || chain.Fallbacks[0].Provider != "Cerebras" || chain.Fallbacks[0].Model != "llama-3.3-70b" {
		t.Fatalf("expected only the legitimate fallback to survive, got %+v", chain.Fallbacks)
	}
}
