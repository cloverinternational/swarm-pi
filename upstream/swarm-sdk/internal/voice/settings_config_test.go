package voice

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTranscriptionSettingsMigratesLegacyAndPreservesUnknown(t *testing.T) {
	input := []byte(`{
  "selected_provider": "rest",
  "api_keys": {"rest": "secret"},
  "custom_urls": {"rest": "http://localhost:8001"},
  "selected_device": "mic-1",
  "future_setting": {"enabled": true}
}`)
	var settings TranscriptionSettings
	if err := json.Unmarshal(input, &settings); err != nil {
		t.Fatal(err)
	}
	provider := settings.Provider()
	if provider.APIKey != "secret" || provider.BaseURL != "http://localhost:8001" {
		t.Fatalf("legacy provider not migrated: %#v", provider)
	}
	output, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(output, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if _, ok := roundTrip["future_setting"]; !ok {
		t.Fatal("unknown field was not preserved")
	}
	if _, ok := roundTrip["api_keys"]; ok {
		t.Fatal("legacy api_keys field should be migrated")
	}
}

func TestSaveTranscriptionSettingsIsPrivateAndAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "voice_config.json")
	settings := DefaultTranscriptionSettings()
	if err := SaveTranscriptionSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	loaded, err := LoadTranscriptionSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SelectedProvider != ProviderOpenAICompatible {
		t.Fatalf("provider = %q", loaded.SelectedProvider)
	}
	if loaded.Provider().BaseURL != DefaultLocalTranscriptionURL {
		t.Fatalf("base URL = %q", loaded.Provider().BaseURL)
	}
}

func TestManagedRuntimePortNormalizesProviderEndpoint(t *testing.T) {
	settings := DefaultTranscriptionSettings()
	settings.Runtime.Port = 9100
	settings.Normalize()
	if got := settings.Provider().BaseURL; got != "http://127.0.0.1:9100" {
		t.Fatalf("managed provider base URL = %q, want configured runtime port", got)
	}
}

func TestConfigCloneKeepsRESTOptions(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Model = "model-a"
	cfg.ChunkDuration = 12
	cfg.Keyterms = []string{"swarm"}
	cfg.UseConversation = true
	clone := cfg.Clone()
	if clone.Model != cfg.Model || clone.ChunkDuration != cfg.ChunkDuration || !clone.UseConversation {
		t.Fatalf("clone lost REST settings: %#v", clone)
	}
	clone.Keyterms[0] = "changed"
	if cfg.Keyterms[0] != "swarm" {
		t.Fatal("clone shares keyterms backing array")
	}
}
