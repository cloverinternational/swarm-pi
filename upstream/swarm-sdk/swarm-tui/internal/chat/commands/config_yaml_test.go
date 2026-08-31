package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
)

func TestAutoCompactionEnabledPresenceRoundTrip(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		format      configformat.Format
		wantEnabled bool
		wantField   bool
	}{
		{name: "json omitted defaults on", input: `{}`, format: configformat.FormatJSON, wantEnabled: true},
		{name: "json explicit false", input: `{"enableAutoCompaction":false}`, format: configformat.FormatJSON, wantEnabled: false, wantField: true},
		{name: "json explicit true", input: `{"enableAutoCompaction":true}`, format: configformat.FormatJSON, wantEnabled: true, wantField: true},
		{name: "yaml omitted defaults on", input: `{}`, format: configformat.FormatYAML, wantEnabled: true},
		{name: "yaml explicit false", input: "enableAutoCompaction: false\n", format: configformat.FormatYAML, wantEnabled: false, wantField: true},
		{name: "yaml explicit true", input: "enableAutoCompaction: true\n", format: configformat.FormatYAML, wantEnabled: true, wantField: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg SwarmOSConfig
			if err := configformat.Unmarshal([]byte(tt.input), tt.format, &cfg); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := cfg.GetEnableAutoCompaction(); got != tt.wantEnabled {
				t.Fatalf("enabled = %v, want %v", got, tt.wantEnabled)
			}
			if got := cfg.EnableAutoCompaction != nil; got != tt.wantField {
				t.Fatalf("field present = %v, want %v", got, tt.wantField)
			}

			out, err := configformat.Marshal(&cfg, tt.format)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			hasField := strings.Contains(string(out), "enableAutoCompaction")
			if hasField != tt.wantField {
				t.Fatalf("serialized field present = %v, want %v: %s", hasField, tt.wantField, out)
			}
		})
	}
}

// TestConfigManagerWritesYAMLAndReadsLegacyJSON proves the JSON->YAML migration
// contract for the main TUI settings file:
//   - an existing config.json still loads,
//   - SaveConfig writes config.yaml (the new default),
//   - the legacy config.json is left on disk (reversible),
//   - subsequent loads prefer config.yaml.
func TestConfigManagerWritesYAMLAndReadsLegacyJSON(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	// Seed a LEGACY config.json directly (flat snake_case, as older builds wrote).
	jsonPath := cm.GetConfigPath("config.json")
	legacy := map[string]any{
		"current_provider": "OpenAI",
		"current_model":    "gpt-5",
		"reasoning_effort": "auto",
	}
	jb, _ := json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(jsonPath, jb, 0o644); err != nil {
		t.Fatalf("seed config.json: %v", err)
	}

	// Legacy JSON must load.
	cfg, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (json): %v", err)
	}
	if cfg.CurrentModel != "gpt-5" || cfg.CurrentProvider != "OpenAI" {
		t.Fatalf("legacy JSON not read: provider=%q model=%q", cfg.CurrentProvider, cfg.CurrentModel)
	}
	if !cfg.GetEnableAutoCompaction() || cfg.EnableAutoCompaction != nil {
		t.Fatal("missing legacy auto-compaction field must default on without being materialized")
	}

	// Mutate + save -> must write config.yaml, leave config.json.
	cfg.CurrentModel = "claude-opus-4-8"
	if err := cm.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	yamlPath := filepath.Join(cm.configDir, "config.yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		t.Fatalf("config.yaml should have been written: %v", err)
	}
	if _, err := os.Stat(jsonPath); err != nil {
		t.Fatalf("legacy config.json must remain on disk (reversible): %v", err)
	}

	// Reload -> YAML wins by precedence and carries the new value.
	InvalidateConfigCache()
	got, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (yaml): %v", err)
	}
	if got.CurrentModel != "claude-opus-4-8" {
		t.Fatalf("reload did not prefer config.yaml: got model=%q", got.CurrentModel)
	}

	// Sanity: the written file is real YAML (not JSON braces).
	data, _ := os.ReadFile(yamlPath)
	if len(data) > 0 && data[0] == '{' {
		t.Fatalf("config.yaml looks like JSON, want YAML:\n%s", data)
	}
	if strings.Contains(string(data), "enableAutoCompaction") {
		t.Fatalf("unrelated save materialized omitted auto-compaction setting:\n%s", data)
	}
}

// TestBundleIntegrationWritesYAML proves the configbundle global file migrates to
// YAML and still reads a pre-existing legacy bundle.json.
func TestBundleIntegrationWritesYAML(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	// Pre-existing legacy bundle.json (nested bundle schema).
	bundleJSON := cm.GetConfigPath("bundle.json")
	if err := os.WriteFile(bundleJSON, []byte(`{"schemaVersion":1,"system":{"theme":"dark"}}`), 0o600); err != nil {
		t.Fatalf("seed bundle.json: %v", err)
	}

	integ, err := NewConfigBundleIntegration(context.Background(), ConfigBundleIntegrationOptions{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewConfigBundleIntegration: %v", err)
	}
	t.Cleanup(integ.Stop)

	if err := integ.UpdateConfig(func(c *core.Config) { c.EnableCodeMode = true }); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	// Bundle must have migrated to bundle.yaml; legacy bundle.json stays.
	if _, err := os.Stat(filepath.Join(cm.configDir, "bundle.yaml")); err != nil {
		t.Fatalf("bundle.yaml should have been written: %v", err)
	}
	if _, err := os.Stat(bundleJSON); err != nil {
		t.Fatalf("legacy bundle.json must remain on disk: %v", err)
	}
}
