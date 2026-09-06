package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestBuildProviderWithOverrideSelectsOpenAIProtocolWithoutStoredCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prov, _, token, isOAuth, err := buildProviderWithOverride(
		"openai",
		"test-model",
		nil,
		nil,
		false,
		&ProviderEndpointOverride{
			APIType: "openai",
			APIKey:  "run-scoped-key",
			BaseURL: "https://openai-compatible.example/v1",
		},
	)
	if err != nil {
		t.Fatalf("buildProviderWithOverride: %v", err)
	}
	if got := fmt.Sprintf("%T", prov); !strings.Contains(got, "openai.Provider") {
		t.Fatalf("provider type = %s, want OpenAI transport", got)
	}
	if token != "run-scoped-key" || isOAuth {
		t.Fatalf("token=%q isOAuth=%v", token, isOAuth)
	}
}

func TestEndpointOverrideWithEmptyKeyDoesNotLoadPersistedEnvironmentCredential(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "persisted-environment-key")

	_, _, token, isOAuth, err := buildProviderWithOverride(
		"openai",
		"test-model",
		nil,
		nil,
		false,
		&ProviderEndpointOverride{
			APIType: "openai",
			BaseURL: "https://openai-compatible.example/v1",
		},
	)
	if err == nil {
		t.Fatal("expected provider construction to reject an empty run-scoped key")
	}
	if token != "" || isOAuth || strings.Contains(err.Error(), "persisted-environment-key") {
		t.Fatalf("run-scoped override inherited or exposed persisted auth: token=%q isOAuth=%v err=%v", token, isOAuth, err)
	}
}

func TestBuildProviderWithOverrideSelectsAnthropicProtocolWithoutStoredCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	prov, _, token, isOAuth, err := buildProviderWithOverride(
		"anthropic",
		"test-model",
		nil,
		nil,
		false,
		&ProviderEndpointOverride{
			APIType: "anthropic",
			APIKey:  "run-scoped-key",
			BaseURL: "https://anthropic-compatible.example",
		},
	)
	if err != nil {
		t.Fatalf("buildProviderWithOverride: %v", err)
	}
	if got := fmt.Sprintf("%T", prov); !strings.Contains(got, "anthropic.Provider") {
		t.Fatalf("provider type = %s, want Anthropic transport", got)
	}
	if token != "run-scoped-key" || isOAuth {
		t.Fatalf("token=%q isOAuth=%v", token, isOAuth)
	}
}

func TestEndpointOverrideBypassesPersistedProxyRoutingAndCredential(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proxyConfig := `[
		{
			"name": "persisted-proxy",
			"display_name": "Persisted Proxy",
			"base_url": "https://proxy.example/v1",
			"api_key": "persisted-proxy-key",
			"enabled": true,
			"is_default": true
		}
	]`
	configDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "proxies.json"), []byte(proxyConfig), 0600); err != nil {
		t.Fatal(err)
	}

	proxyURL, proxyKey := resolveProviderProxy("openai", nil)
	if proxyURL == "" || proxyKey != "persisted-proxy-key" {
		t.Fatalf("test setup did not load persisted proxy: url=%q key=%q", proxyURL, proxyKey)
	}
	proxyURL, proxyKey = resolveProviderProxy("openai", &ProviderEndpointOverride{
		APIType: "openai",
		APIKey:  "run-scoped-key",
	})
	if proxyURL != "" || proxyKey != "" {
		t.Fatalf("run-scoped override inherited persisted proxy: url=%q key=%q", proxyURL, proxyKey)
	}
}

func TestEndpointOverrideDisablesPersistedCrossProviderFallback(t *testing.T) {
	primary := &testChatProvider{name: "openai"}
	chain := fallback.NewChain("openai", "custom-model")
	chain.AddFallback("anthropic", "stored-fallback-model")
	builds := 0

	_, err := buildRuntimeProviderStack(
		primary,
		"openai",
		"custom-model",
		providerBuildDeps{EndpointOverride: &ProviderEndpointOverride{APIType: "openai"}},
		func() (*commands.SwarmOSConfig, error) {
			return &commands.SwarmOSConfig{
				RetrySettings: &commands.RetrySettingsConfig{
					Enabled:               true,
					MaxRetriesPerProvider: 2,
				},
				ChatFallbackChain: chain,
			}, nil
		},
		func(string, string, providerBuildDeps) (provider.Provider, string, string, bool, error) {
			builds++
			return &testChatProvider{name: "unexpected"}, "", "", false, nil
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("buildRuntimeProviderStack: %v", err)
	}
	if builds != 0 {
		t.Fatalf("persisted fallback builder called %d times for run-scoped endpoint", builds)
	}
}

func TestProviderSlotTracksReloadedProvider(t *testing.T) {
	first := &testChatProvider{name: "first"}
	second := &testChatProvider{name: "second"}
	slot := &providerSlot{provider: first}
	if got := slot.Get(); got != first {
		t.Fatalf("initial provider = %T, want first", got)
	}
	slot.Set(second)
	if got := slot.Get(); got != second {
		t.Fatalf("reloaded provider = %T, want second", got)
	}
}
