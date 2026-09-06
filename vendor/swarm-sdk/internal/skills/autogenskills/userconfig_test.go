package autogenskills

import (
	"os"
	"path/filepath"
	"testing"
)

// TestApplyUserConfigOverrides gives the curator a Hermes-style user config
// surface (hermes: curator.* in ~/.hermes/config.yaml). Before this existed
// the TUI hardcoded the whole autogenskills Config, so Curator.Consolidate
// could never be enabled by a user — the consolidation pass was unreachable
// in every product.
func TestApplyUserConfigOverrides(t *testing.T) {
	dir := t.TempDir()
	yamlBody := []byte(`curator:
  consolidate: true
  stale_after_days: 10
  archive_after_days: 45
  min_run_gap: "6h"
  max_turns: 321
  timeout: "17m"
trigger:
  nudge_interval: 9
`)
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), yamlBody, 0644); err != nil {
		t.Fatal(err)
	}

	base := Config{
		Mode:       ModeAuto,
		AutogenDir: dir,
		Trigger:    TriggerConfig{NudgeInterval: 5, ToolCallBudget: 5},
		Curator:    CuratorConfig{},
	}

	got, path := ApplyUserConfig(base)
	if path == "" {
		t.Fatal("override file not detected")
	}
	if !got.Curator.Consolidate {
		t.Error("consolidate override not applied")
	}
	if got.Curator.StaleAfterDays != 10 {
		t.Errorf("stale_after_days: got %d", got.Curator.StaleAfterDays)
	}
	if got.Curator.ArchiveAfterDays != 45 {
		t.Errorf("archive_after_days: got %d", got.Curator.ArchiveAfterDays)
	}
	if got.Curator.MinRunGap != "6h" {
		t.Errorf("min_run_gap: got %q", got.Curator.MinRunGap)
	}
	if got.Curator.MaxTurns != 321 || got.Curator.Timeout != "17m" {
		t.Errorf("curator execution overrides not applied: turns=%d timeout=%q", got.Curator.MaxTurns, got.Curator.Timeout)
	}
	if got.Trigger.NudgeInterval != 9 {
		t.Errorf("nudge_interval: got %d", got.Trigger.NudgeInterval)
	}
	// Untouched fields keep their base values.
	if got.Trigger.ToolCallBudget != 5 {
		t.Errorf("tool_call_budget clobbered: got %d", got.Trigger.ToolCallBudget)
	}
	if got.Mode != ModeAuto {
		t.Errorf("mode clobbered: got %q", got.Mode)
	}
}

// TestApplyUserConfigNoFile: absence of the override file is not an error and
// changes nothing.
func TestApplyUserConfigNoFile(t *testing.T) {
	base := Config{Mode: ModeAuto, AutogenDir: t.TempDir()}
	got, path := ApplyUserConfig(base)
	if path != "" {
		t.Errorf("unexpected path %q", path)
	}
	if got.Curator.Consolidate {
		t.Error("consolidate should stay false")
	}
}

// TestApplyUserConfigModeOverride: mode can be set to disable the feature.
func TestApplyUserConfigModeOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("mode: never\n"), 0644); err != nil {
		t.Fatal(err)
	}
	base := Config{Mode: ModeAuto, AutogenDir: dir}
	got, _ := ApplyUserConfig(base)
	if got.Mode != ModeNever {
		t.Errorf("mode override not applied: %q", got.Mode)
	}
}

// TestApplyUserConfigCorruptFile: a corrupt file must not take down the
// feature — base config survives.
func TestApplyUserConfigCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("{{{not yaml"), 0644); err != nil {
		t.Fatal(err)
	}
	base := Config{Mode: ModeAuto, AutogenDir: dir}
	got, path := ApplyUserConfig(base)
	if path != "" {
		t.Errorf("corrupt file should report empty path, got %q", path)
	}
	if got.Mode != ModeAuto {
		t.Errorf("base config damaged: %q", got.Mode)
	}
}
