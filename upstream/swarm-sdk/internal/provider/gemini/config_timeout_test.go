package gemini

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestConfigDefaultTimeout(t *testing.T) {
	cfg := Config{AuthMode: AuthModeAPIKey, APIKey: "test"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %d, want %d", cfg.Timeout, DefaultTimeout)
	}
}

func TestRegisterWithConfigPreservesUnlimitedTimeout(t *testing.T) {
	registry := provider.NewSimpleRegistry(nil)
	err := RegisterWithConfig(registry, Config{
		AuthMode: AuthModeAPIKey,
		APIKey:   "test",
		Timeout:  DefaultTimeout,
	})
	if err != nil {
		t.Fatalf("RegisterWithConfig() error = %v", err)
	}

	created, err := registry.Create(provider.Config{Name: "gemini", Timeout: -1})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if timeout := created.(*Provider).config.Timeout; timeout != -1 {
		t.Fatalf("Timeout = %d, want -1", timeout)
	}
}
