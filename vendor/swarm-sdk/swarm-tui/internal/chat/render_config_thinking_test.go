package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThinkingDisplayDefaultsVisible(t *testing.T) {
	if !NewDefaultRenderSettings().ShowThinking {
		t.Fatal("thinking display must be visible by default")
	}
}

func TestLoadRenderSettingsThinkingVisibilityMigration(t *testing.T) {
	tests := []struct {
		name string
		json string
		want bool
	}{
		{name: "legacy config without field uses visible default", json: `{"messageSpacing":1}`, want: true},
		{name: "explicit opt out is preserved", json: `{"show_thinking":false}`, want: false},
		{name: "explicit opt in is preserved", json: `{"show_thinking":true}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configDir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", configDir)
			swarmDir := filepath.Join(configDir, "swarmos")
			if err := os.MkdirAll(swarmDir, 0o755); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			if err := os.WriteFile(filepath.Join(swarmDir, "render_settings.json"), []byte(tt.json), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			settings, err := LoadRenderSettings()
			if err != nil {
				t.Fatalf("LoadRenderSettings: %v", err)
			}
			if settings.ShowThinking != tt.want {
				t.Fatalf("ShowThinking=%v, want %v", settings.ShowThinking, tt.want)
			}
		})
	}
}
