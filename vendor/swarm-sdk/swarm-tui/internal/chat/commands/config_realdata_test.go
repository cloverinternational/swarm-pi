package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestRealConfigRoundTripsToYAML loads a REAL legacy config.json (pointed to by
// SWARM_REAL_CONFIG), migrates it to YAML via SaveConfig, reloads from YAML, and
// asserts the entire config survives byte-identically (JSON-normalized). This is
// the empirical proof that a user's actual settings — including custom types like
// fallback.Chain (CompactionChain / ChatFallbackChain) — round-trip through YAML.
//
// It is skipped unless SWARM_REAL_CONFIG is set, so it never depends on a real
// home directory in normal CI.
func TestRealConfigRoundTripsToYAML(t *testing.T) {
	src := os.Getenv("SWARM_REAL_CONFIG")
	if src == "" {
		t.Skip("set SWARM_REAL_CONFIG=/path/to/config.json to run")
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read real config: %v", err)
	}

	t.Setenv("HOME", t.TempDir())
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	if err := os.WriteFile(cm.GetConfigPath("config.json"), raw, 0o644); err != nil {
		t.Fatalf("seed real config.json: %v", err)
	}

	// 1. Load the real legacy JSON.
	c1, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("load real config.json: %v", err)
	}

	// 2. Save -> migrates to config.yaml.
	if err := cm.SaveConfig(c1); err != nil {
		t.Fatalf("SaveConfig (migrate to yaml): %v", err)
	}
	if _, err := os.Stat(filepath.Join(cm.configDir, "config.yaml")); err != nil {
		t.Fatalf("config.yaml not written: %v", err)
	}

	// 3. Reload from YAML.
	InvalidateConfigCache()
	c2, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("reload config.yaml: %v", err)
	}

	// 4. The whole struct must be identical (JSON-normalized deep equality).
	j1, _ := json.Marshal(c1)
	j2, _ := json.Marshal(c2)
	if string(j1) != string(j2) {
		t.Errorf("config drifted across JSON->YAML->struct round-trip:\n--- before ---\n%s\n--- after ---\n%s", j1, j2)
	}

	t.Logf("real config round-tripped OK: provider=%s model=%s autoCompaction=%v threshold=%.3f compactionChain=%v",
		c2.CurrentProvider, c2.CurrentModel, c2.GetEnableAutoCompaction(), c2.AutoCompactionThresholdPercent, c2.CompactionChain != nil)
}
