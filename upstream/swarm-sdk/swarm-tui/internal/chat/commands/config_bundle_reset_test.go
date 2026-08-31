package commands

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
)

// TestBundleUpdateDoesNotWipeLegacyConfig is a regression test for the
// "settings keep resetting" bug.
//
// Root cause: the configbundle global file defaulted to the SAME path as the
// legacy ~/.swarmos/config.json. The two use incompatible JSON schemas
// (flat snake_case SwarmOSConfig vs. nested camelCase ConfigBundle), so any
// bundle save (model switch, code-mode toggle, micro-compaction toggle,
// /memory change, OAuth login) rewrote config.json in bundle format and the
// legacy loader then reset model / compaction / auto-compaction to defaults.
//
// This test seeds a realistic legacy config, performs a bundle-routed update
// exactly like the code-mode toggle handler does, and asserts every
// legacy-only setting survives — and that the bundle-routed change reaches the
// engine-visible legacy config.
func TestBundleUpdateDoesNotWipeLegacyConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	InvalidateConfigCache()
	t.Cleanup(InvalidateConfigCache)

	cm, err := NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	// 1. Seed a realistic legacy config.json.
	seed := &SwarmOSConfig{
		CurrentProvider:                 "OpenAI",
		CurrentModel:                    "gpt-5",
		ReasoningEffort:                 "auto",
		CompactionChain:                 fallback.NewChain("Gemini", "gemini-3-pro-preview"),
		AutoCompactionContinueIfRunning: true,
		AutoCompactionThresholdPercent:  0.8,
	}
	seed.SetEnableAutoCompaction(false)
	if err := cm.SaveConfig(seed); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// 2. Create the bundle integration and toggle a bundle-routed field, the
	//    same way handleCodeModeToggle / handleMicroCompactToggle / /memory do.
	integ, err := NewConfigBundleIntegration(context.Background(), ConfigBundleIntegrationOptions{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatalf("NewConfigBundleIntegration: %v", err)
	}
	t.Cleanup(integ.Stop)

	if err := integ.UpdateConfig(func(c *core.Config) {
		c.EnableCodeMode = true
		c.CompletionConfirm = true
		c.CompletionConfirmMax = 2
		c.ProactiveSummarizeThreshold = 0.6
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	// 3. Reload the legacy config — every legacy-only setting must survive.
	InvalidateConfigCache()
	got, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got.CurrentModel != "gpt-5" {
		t.Errorf("CurrentModel reset: got %q want gpt-5", got.CurrentModel)
	}
	if got.CurrentProvider != "OpenAI" {
		t.Errorf("CurrentProvider reset: got %q want OpenAI", got.CurrentProvider)
	}
	if got.CompactionChain == nil {
		t.Error("CompactionChain wiped to nil")
	}
	if got.GetEnableAutoCompaction() {
		t.Error("explicit EnableAutoCompaction=false was reset")
	}
	if got.AutoCompactionThresholdPercent != 0.8 {
		t.Errorf("AutoCompactionThresholdPercent reset: got %v want 0.8", got.AutoCompactionThresholdPercent)
	}

	// 4. The bundle-routed change must reach the engine-visible legacy config.
	if !got.EnableCodeMode {
		t.Error("EnableCodeMode not mirrored to legacy config.json (engine reads it from there)")
	}
	if !got.CompletionConfirm {
		t.Error("CompletionConfirm not mirrored to legacy config")
	}
	if got.CompletionConfirmMax != 2 {
		t.Errorf("CompletionConfirmMax = %d, want 2", got.CompletionConfirmMax)
	}
	if got.ProactiveSummarizeThreshold != 0.6 {
		t.Errorf("ProactiveSummarizeThreshold = %v, want 0.6", got.ProactiveSummarizeThreshold)
	}
}
