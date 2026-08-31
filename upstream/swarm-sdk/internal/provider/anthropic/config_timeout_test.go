package anthropic

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestConfigDefaultTimeout(t *testing.T) {
	cfg := Config{}
	cfg.ApplyDefaults()
	if cfg.Timeout != defaultRequestTimeoutSeconds {
		t.Fatalf("Timeout = %d, want %d", cfg.Timeout, defaultRequestTimeoutSeconds)
	}

	cfg = WithDefaults()
	if cfg.Timeout != defaultRequestTimeoutSeconds {
		t.Fatalf("WithDefaults Timeout = %d, want %d", cfg.Timeout, defaultRequestTimeoutSeconds)
	}
}

func TestConfigAllowsFifteenMinuteTimeout(t *testing.T) {
	cfg := Config{APIKey: "test", Timeout: defaultRequestTimeoutSeconds}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected 15-minute timeout: %v", err)
	}
}

func TestConfigAllowsExplicitTimeoutAboveDefault(t *testing.T) {
	cfg := Config{APIKey: "test", Timeout: 30 * 60}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() rejected explicit timeout above default: %v", err)
	}
}

func TestRegistryConstructorsPreserveUnlimitedTimeout(t *testing.T) {
	created, err := NewFromRegistry(provider.Config{APIKey: "test", Timeout: -1})
	if err != nil {
		t.Fatalf("NewFromRegistry() error = %v", err)
	}
	if timeout := created.(*Provider).config.Timeout; timeout != -1 {
		t.Fatalf("NewFromRegistry Timeout = %d, want -1", timeout)
	}

	created, err = NewFromConfig(map[string]any{"api_key": "test", "timeout": -1}, nil, nil)
	if err != nil {
		t.Fatalf("NewFromConfig() error = %v", err)
	}
	if timeout := created.(*Provider).config.Timeout; timeout != -1 {
		t.Fatalf("NewFromConfig Timeout = %d, want -1", timeout)
	}
}
