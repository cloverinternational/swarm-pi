package settings

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestCompactionSettingsRenderIsThresholdOnly(t *testing.T) {
	cs := NewCompactionSettings(testConfigManager(t))
	cs.SetCurrentChatModel("Anthropic", "claude-opus-4-7")
	state := NewState()
	state.Focus = FocusContent

	view := cs.Render(100, 40, state, testTheme())
	for _, want := range []string{"Auto-Compaction", "Mode:", "Per-Model Threshold", "claude-opus-4-7"} {
		if !strings.Contains(view, want) {
			t.Errorf("threshold-only render missing %q:\n%s", want, view)
		}
	}
	for _, removed := range []string{"Fallback Models", "Edit Fallback Chain", "Primary:"} {
		if strings.Contains(view, removed) {
			t.Errorf("threshold-only render still contains %q:\n%s", removed, view)
		}
	}
}

// Probe: CompactionSettings persistence bug
//
// BUG (FIXED): saveChain() and saveAutoCompactionSettings() had SaveConfig commented out.
// They only set hasChanges=true in memory but NEVER persisted to disk.
// This meant compaction model selections were lost on restart.
//
// Compare with DreamSettings.save() which DOES call SaveConfig.

func TestCompactionSettingsAutoCompactTogglePersists(t *testing.T) {
	cm := testConfigManager(t)

	cs := NewCompactionSettings(cm)

	if !cs.GetEnableAutoCompaction() {
		t.Fatal("auto-compaction should default on when the setting is absent")
	}

	// Toggle auto-compaction off (calls saveAutoCompactionSettings).
	cs.ToggleAutoCompaction()
	if cs.GetEnableAutoCompaction() {
		t.Fatal("expected auto-compaction to be disabled after toggle")
	}

	// Reload a fresh CompactionSettings from the same config.
	reloaded := NewCompactionSettings(cm)
	if reloaded.GetEnableAutoCompaction() {
		t.Error("explicit auto-compaction=false did NOT persist to disk. " +
			"saveAutoCompactionSettings() must call SaveConfig.")
	}
}

func TestCompactionSettingsThresholdPersists(t *testing.T) {
	cm := testConfigManager(t)

	cs := NewCompactionSettings(cm)

	// Adjust threshold (calls saveAutoCompactionSettings)
	cs.SetAutoCompactionThreshold(0.9)
	if cs.GetAutoCompactionThresholdPercent() != 0.9 {
		t.Fatalf("expected 0.9, got %f", cs.GetAutoCompactionThresholdPercent())
	}

	// Reload from disk
	reloaded := NewCompactionSettings(cm)
	if reloaded.GetAutoCompactionThresholdPercent() != 0.9 {
		t.Errorf("threshold 0.9 did NOT persist (got %f). "+
			"saveAutoCompactionSettings() must call SaveConfig.",
			reloaded.GetAutoCompactionThresholdPercent())
	}
}

func TestCompactionSettingsPreservesLegacyChainWithoutUsingIt(t *testing.T) {
	cm := testConfigManager(t)

	// Write a custom chain directly to config
	cfg, _ := cm.LoadConfig()
	if cfg == nil {
		cfg = &commands.SwarmOSConfig{}
	}
	newChain := fallback.NewChain("OpenAI", "gpt-4.1-mini")
	newChain.AddFallback("Gemini", "gemini-2.5-flash")
	cfg.SetCompactionChain(newChain)
	if err := cm.SaveConfig(cfg); err != nil {
		t.Fatalf("failed to write initial config: %v", err)
	}

	// Compaction settings no longer expose or modify the legacy summarizer
	// chain. Saving a threshold must still preserve the field so old configs
	// remain backward-compatible.
	cs := NewCompactionSettings(cm)
	cs.SetAutoCompactionThreshold(0.9)
	reloaded, err := cm.LoadConfig()
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	chain := reloaded.GetCompactionChain()
	if chain == nil || chain.Primary.Provider != "OpenAI" || chain.Primary.Model != "gpt-4.1-mini" {
		t.Errorf("chain did NOT load from disk: got %v", chain)
	}
	if len(chain.Fallbacks) != 1 || chain.Fallbacks[0].Provider != "Gemini" {
		t.Errorf("fallback did NOT load from disk: got %v", chain.Fallbacks)
	}
}

func TestCompactionSettingsContinueIfRunningPersists(t *testing.T) {
	cm := testConfigManager(t)

	cs := NewCompactionSettings(cm)
	cs.ToggleAutoCompactionContinue()

	if !cs.GetAutoCompactionContinueIfRunning() {
		t.Fatal("expected auto-compaction continue to be enabled after toggle")
	}

	// Reload from disk
	reloaded := NewCompactionSettings(cm)
	if !reloaded.GetAutoCompactionContinueIfRunning() {
		t.Error("auto-compaction continue toggle did NOT persist. " +
			"saveAutoCompactionSettings() must call SaveConfig.")
	}
}

func TestCompactionSettingsSaveMethodFlushesAll(t *testing.T) {
	cm := testConfigManager(t)

	cs := NewCompactionSettings(cm)
	// Make multiple changes without individual saves
	cs.enableAutoCompaction = true
	cs.autoCompactionContinueIfRunning = true
	cs.autoCompactionThresholdPercent = 0.75
	// Keep the new threshold descriptor in sync with the legacy percent field
	// so Save() flushes the resolved threshold too.
	cs.autoCompactionThreshold = commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdPercent,
		Value: 0.75,
	}
	cs.hasChanges = true

	// Save() should flush everything
	if err := cs.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	// Reload from disk
	reloaded := NewCompactionSettings(cm)
	if !reloaded.GetEnableAutoCompaction() {
		t.Error("Save() did not persist enableAutoCompaction")
	}
	if !reloaded.GetAutoCompactionContinueIfRunning() {
		t.Error("Save() did not persist autoCompactionContinueIfRunning")
	}
	if reloaded.GetAutoCompactionThresholdPercent() != 0.75 {
		t.Errorf("Save() did not persist threshold: got %f", reloaded.GetAutoCompactionThresholdPercent())
	}
}

// --- Manager-level probe: SavePendingChanges flushes compaction ---

func TestManagerSavePendingChangesFlushesCompaction(t *testing.T) {
	setTempHome(t)

	mgr := NewManager(
		"ClaudeCode", "claude-opus-4-20250514",
		true, false, false, false, false,
		"dot", "chat", nil,
	)

	// Make changes via compaction settings directly
	compSettings := mgr.GetCompactionSettings()
	if compSettings == nil {
		t.Fatal("compaction settings should not be nil")
	}
	wasEnabled := compSettings.GetEnableAutoCompaction()
	compSettings.ToggleAutoCompaction()

	// Call SavePendingChanges (this is what "Save & Exit" does)
	mgr.SavePendingChanges()

	// Reload config from disk
	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("NewConfigManager failed: %v", err)
	}
	cfg, _ := cm.LoadConfig()
	if cfg == nil {
		t.Fatal("config should not be nil after SavePendingChanges")
	}

	if cfg.GetEnableAutoCompaction() != !wasEnabled {
		t.Errorf("SavePendingChanges did NOT flush compaction settings. "+
			"Config has EnableAutoCompaction=%v but expected %v.",
			cfg.GetEnableAutoCompaction(), !wasEnabled)
	}
}

// --- Reliability probe: updateConfig now persists immediately ---

func TestReliabilityTogglePersistsImmediately(t *testing.T) {
	cm := testConfigManager(t)

	rs := NewReliabilitySettings(cm)
	initialEnabled := rs.GetRetrySettings().Enabled

	// ToggleRetryEnabled -> saveRetrySettings -> updateConfig
	// updateConfig now calls SaveConfig directly
	rs.ToggleRetryEnabled()

	// Reload from disk — should see the change immediately
	reloaded := NewReliabilitySettings(cm)
	reloadedEnabled := reloaded.GetRetrySettings().Enabled

	if reloadedEnabled == initialEnabled {
		t.Errorf("reliability toggle did NOT persist. Before=%v After=%v. "+
			"updateConfig() must call SaveConfig.",
			initialEnabled, reloadedEnabled)
	}
}

func TestReliabilitySaveMethodStillWorks(t *testing.T) {
	cm := testConfigManager(t)

	rs := NewReliabilitySettings(cm)
	rs.ToggleRetryEnabled()
	rs.ToggleRotateOnRateLimit()

	// Explicit Save() should also work
	if err := rs.Save(); err != nil {
		t.Fatalf("Save() failed: %v", err)
	}

	reloaded := NewReliabilitySettings(cm)
	if reloaded.GetRetrySettings().Enabled == rs.GetRetrySettings().Enabled {
		// They should be the same since Save() persisted
		t.Log("PASS: Reliability Save() method works correctly")
	} else {
		t.Error("Reliability Save() did not persist properly")
	}
}

// --- Threshold mode (percent / fixed_tokens) persistence ---

func TestCompactionThresholdModeFixedTokensPersists(t *testing.T) {
	cm := testConfigManager(t)
	cs := NewCompactionSettings(cm)

	cs.SetAutoCompactionThresholdDescriptor(commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdFixedTokens,
		Value: 100000,
	})

	reloaded := NewCompactionSettings(cm)
	got := reloaded.GetAutoCompactionThreshold()
	if got.Mode != commands.CompactionThresholdFixedTokens {
		t.Fatalf("mode did NOT persist: got %q want %q", got.Mode, commands.CompactionThresholdFixedTokens)
	}
	if got.Value != 100000 {
		t.Fatalf("value did NOT persist: got %v want 100000", got.Value)
	}
}

func TestCompactionThresholdModePercentPersists(t *testing.T) {
	cm := testConfigManager(t)
	cs := NewCompactionSettings(cm)

	cs.SetAutoCompactionThresholdDescriptor(commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdPercent,
		Value: 0.7,
	})

	reloaded := NewCompactionSettings(cm)
	got := reloaded.GetAutoCompactionThreshold()
	if got.Mode != commands.CompactionThresholdPercent {
		t.Fatalf("mode did NOT persist: got %q want %q", got.Mode, commands.CompactionThresholdPercent)
	}
	if got.Value != 0.7 {
		t.Fatalf("value did NOT persist: got %v want 0.7", got.Value)
	}
}

// --- Per-model override persistence ---

func TestCompactionThresholdOverridePersists(t *testing.T) {
	cm := testConfigManager(t)
	cs := NewCompactionSettings(cm)

	cs.SetThresholdOverride("OpenAI", "gpt-5", commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdFixedTokens,
		Value: 50000,
	})

	reloaded := NewCompactionSettings(cm)
	ov, ok := reloaded.GetThresholdOverride("OpenAI", "gpt-5")
	if !ok {
		t.Fatal("per-model override did NOT persist")
	}
	if ov.Mode != commands.CompactionThresholdFixedTokens || ov.Value != 50000 {
		t.Errorf("override value wrong: got %+v", ov)
	}
}

func TestCompactionThresholdOverrideClearPersists(t *testing.T) {
	cm := testConfigManager(t)
	cs := NewCompactionSettings(cm)

	cs.SetThresholdOverride("OpenAI", "gpt-5", commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdPercent,
		Value: 0.6,
	})
	cs.ClearThresholdOverride("OpenAI", "gpt-5")

	reloaded := NewCompactionSettings(cm)
	if _, ok := reloaded.GetThresholdOverride("OpenAI", "gpt-5"); ok {
		t.Error("override was NOT cleared on disk")
	}
}

// --- Override precedence over global ---

func TestCompactionThresholdOverridePrecedence(t *testing.T) {
	cm := testConfigManager(t)
	cs := NewCompactionSettings(cm)

	// Global = 85% percent
	cs.SetAutoCompactionThresholdDescriptor(commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdPercent,
		Value: 0.85,
	})
	// Override = 40000 fixed tokens for gpt-5
	cs.SetThresholdOverride("OpenAI", "gpt-5", commands.CompactionThreshold{
		Mode:  commands.CompactionThresholdFixedTokens,
		Value: 40000,
	})

	eff := cs.ResolveThresholdForModel("OpenAI", "gpt-5")
	if eff.Mode != commands.CompactionThresholdFixedTokens || eff.Value != 40000 {
		t.Errorf("override should win: got %+v", eff)
	}
	// A different model should fall back to global.
	eff2 := cs.ResolveThresholdForModel("OpenAI", "gpt-4o")
	if eff2.Mode != commands.CompactionThresholdPercent || eff2.Value != 0.85 {
		t.Errorf("non-overridden model should get global: got %+v", eff2)
	}
}

// --- ThresholdTokens math (percent vs fixed) ---

func TestThresholdTokensPercentMode(t *testing.T) {
	th := commands.CompactionThreshold{Mode: commands.CompactionThresholdPercent, Value: 0.5}
	if got := th.ThresholdTokens(200000); got != 100000 {
		t.Errorf("percent mode: got %d want 100000", got)
	}
	// Clamped to [0.5, 0.95]
	th.Value = 0.1
	if got := th.ThresholdTokens(200000); got != 100000 {
		t.Errorf("percent clamp low: got %d want 100000", got)
	}
	th.Value = 1.0
	if got := th.ThresholdTokens(200000); got != 190000 {
		t.Errorf("percent clamp high: got %d want 190000", got)
	}
}

func TestThresholdTokensFixedMode(t *testing.T) {
	th := commands.CompactionThreshold{Mode: commands.CompactionThresholdFixedTokens, Value: 120000}
	if got := th.ThresholdTokens(200000); got != 120000 {
		t.Errorf("fixed mode: got %d want 120000", got)
	}
	// Capped at context limit
	th.Value = 999999
	if got := th.ThresholdTokens(200000); got != 200000 {
		t.Errorf("fixed cap: got %d want 200000", got)
	}
	// Min 1000
	th.Value = 100
	if got := th.ThresholdTokens(200000); got != 1000 {
		t.Errorf("fixed min: got %d want 1000", got)
	}
}

// --- Legacy migration ---

func TestCompactionThresholdMigratesFromLegacyPercent(t *testing.T) {
	cm := testConfigManager(t)
	cfg, _ := cm.LoadConfig()
	if cfg == nil {
		cfg = &commands.SwarmOSConfig{}
	}
	cfg.AutoCompactionThresholdPercent = 0.8
	cfg.AutoCompactionThreshold = commands.CompactionThreshold{} // empty
	if err := cm.SaveConfig(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	cs := NewCompactionSettings(cm)
	got := cs.GetAutoCompactionThreshold()
	if got.Mode != commands.CompactionThresholdPercent || got.Value != 0.8 {
		t.Errorf("migration failed: got %+v want percent/0.8", got)
	}
}
