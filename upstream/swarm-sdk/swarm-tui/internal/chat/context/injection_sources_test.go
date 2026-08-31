package context

import (
	"context"
	"strings"
	"testing"
)

// TestInjectionSourcesInDefaults verifies the injection-gate sources ship as
// built-ins (default enabled) so they appear in the Context settings screen
// and get added to pre-existing configs via EnsureBuiltinSources.
func TestInjectionSourcesInDefaults(t *testing.T) {
	ids := []string{
		SourceIDSkills,
		SourceIDWorkspaceEnv,
		SourceIDHookContext,
	}

	cfg := DefaultConfig()
	for _, id := range ids {
		source, _, ok := FindSourceByID(cfg.Sources, id)
		if !ok {
			t.Fatalf("DefaultConfig missing injection source %q", id)
		}
		if source.Kind != SourceKindInjection {
			t.Errorf("source %q kind = %q, want %q", id, source.Kind, SourceKindInjection)
		}
		if !source.Enabled {
			t.Errorf("source %q should default to enabled", id)
		}
	}

	// A legacy config without the new sources must gain them on normalize.
	legacy := NormalizeConfig(ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDAgentsMd, Kind: SourceKindFile, Enabled: true},
		},
	})
	for _, id := range ids {
		if _, _, ok := FindSourceByID(legacy.Sources, id); !ok {
			t.Errorf("NormalizeConfig did not add missing injection source %q", id)
		}
	}
}

// TestIsInjectionEnabled verifies the gate lookup: explicit value wins,
// missing entries fall back to the builtin default, unknown IDs are enabled.
func TestIsInjectionEnabled(t *testing.T) {
	cfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDSkills, Kind: SourceKindInjection, Enabled: false},
		},
	}

	if IsInjectionEnabled(cfg, SourceIDSkills) {
		t.Error("explicitly disabled skills source should report disabled")
	}
	if !IsInjectionEnabled(cfg, SourceIDWorkspaceEnv) {
		t.Error("missing workspace_env entry should fall back to builtin default (enabled)")
	}
	if !IsInjectionEnabled(cfg, "some_future_injection") {
		t.Error("unknown IDs must default to enabled")
	}
}

// TestIsInjectionEnabledProjectOverride verifies a project config disabling a
// gate wins over an enabled global config after the merge.
func TestIsInjectionEnabledProjectOverride(t *testing.T) {
	global := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDSkills, Kind: SourceKindInjection, Enabled: true},
		},
	}
	project := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDSkills, Kind: SourceKindInjection, Enabled: false},
		},
	}

	merged := mergeConfigs(global, project)
	if IsInjectionEnabled(merged, SourceIDSkills) {
		t.Error("project-level disable should win over global enable")
	}
}

// TestOrchestratorSkipsInjectionSources verifies enabled injection gates never
// render content into the context block.
func TestOrchestratorSkipsInjectionSources(t *testing.T) {
	cfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDSkills, Kind: SourceKindInjection, Enabled: true},
			{ID: SourceIDCurrentDate, Kind: SourceKindDynamic, Enabled: true, RefreshMode: RefreshEveryMessage, CachePolicy: CacheEphemeral},
		},
	}
	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, t.TempDir(), loader, nil, nil)

	block := orch.GetContextBlock(context.Background(), ContextBlockOptions{RunID: "run-1"})
	if strings.Contains(block, "skills") {
		t.Errorf("injection source leaked into context block:\n%s", block)
	}
	if !strings.Contains(block, "currentDate") {
		t.Errorf("expected dynamic currentDate source in block:\n%s", block)
	}
}

// TestOrchestratorIsInjectionEnabledLive verifies the orchestrator re-reads
// the loader so settings-screen toggles apply without a restart.
func TestOrchestratorIsInjectionEnabledLive(t *testing.T) {
	cfg := DefaultConfig()
	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, t.TempDir(), loader, nil, nil)

	if !orch.IsInjectionEnabled(SourceIDSkills) {
		t.Fatal("skills gate should start enabled")
	}

	// Flip the toggle in the loader (what the settings screen does on disk).
	updated := loader.GetConfig()
	if source, idx, ok := FindSourceByID(updated.Sources, SourceIDSkills); ok {
		source.Enabled = false
		updated.Sources[idx] = source
	}
	_ = loader.SetConfig(updated)

	if orch.IsInjectionEnabled(SourceIDSkills) {
		t.Error("orchestrator should observe the loader's updated config without restart")
	}
}
