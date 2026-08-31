package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
)

func newLanguageConfigManager(t *testing.T) *ConfigManager {
	t.Helper()
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)
	return &ConfigManager{configDir: t.TempDir()}
}

func TestLanguageDefaultIsEnglish(t *testing.T) {
	cfg, err := newLanguageConfigManager(t).LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Language != "en" {
		t.Fatalf("Language = %q, want %q", cfg.Language, "en")
	}
}

func TestLanguageLoadsFromYAML(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "spanish canonicalized", value: " ES ", want: "es"},
		{name: "empty defaults to English", value: "", want: "en"},
		{name: "invalid defaults to English", value: "fr", want: "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newLanguageConfigManager(t)
			data := []byte("language: " + `"` + tt.value + `"` + "\n")
			if err := os.WriteFile(cm.GetConfigPath("config.yaml"), data, 0o600); err != nil {
				t.Fatalf("write config.yaml: %v", err)
			}

			cfg, err := cm.LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if cfg.Language != tt.want {
				t.Fatalf("Language = %q, want %q", cfg.Language, tt.want)
			}
		})
	}
}

func TestLanguageLoadsFromJSON(t *testing.T) {
	cm := newLanguageConfigManager(t)
	if err := os.WriteFile(cm.GetConfigPath("config.json"), []byte(`{"language":"ES"}`), 0o600); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Language != "es" {
		t.Fatalf("Language = %q, want %q", cfg.Language, "es")
	}
}

func TestLanguageSaveRoundTripPreservesCanonicalValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "Spanish", value: " ES ", want: "es"},
		{name: "invalid falls back to English", value: "de", want: "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newLanguageConfigManager(t)
			cfg := &SwarmOSConfig{Language: tt.value}
			if err := cm.SaveConfig(cfg); err != nil {
				t.Fatalf("SaveConfig: %v", err)
			}
			if cfg.Language != tt.want {
				t.Fatalf("saved config Language = %q, want %q", cfg.Language, tt.want)
			}

			path := filepath.Join(cm.configDir, "config.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read config.yaml: %v", err)
			}
			var persisted struct {
				Language string `json:"language"`
			}
			if err := configformat.Unmarshal(data, configformat.FormatForPath(path), &persisted); err != nil {
				t.Fatalf("parse config.yaml: %v", err)
			}
			if persisted.Language != tt.want {
				t.Fatalf("persisted Language = %q, want %q", persisted.Language, tt.want)
			}

			reloaded, err := cm.LoadConfig()
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if reloaded.Language != tt.want {
				t.Fatalf("round-trip Language = %q, want %q", reloaded.Language, tt.want)
			}
		})
	}
}
