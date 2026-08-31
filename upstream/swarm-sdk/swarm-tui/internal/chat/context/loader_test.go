package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadContext_CurrentDate(t *testing.T) {
	previousNow := nowFunc
	nowFunc = func() time.Time {
		return time.Date(2026, 2, 5, 12, 30, 0, 0, time.UTC)
	}
	defer func() {
		nowFunc = previousNow
	}()

	loader := &FileLoader{}
	config := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDCurrentDate, Enabled: true},
		},
	}

	loaded, err := loader.LoadContext(config, ".")
	if err != nil {
		t.Fatalf("LoadContext returned error: %v", err)
	}

	var found bool
	for _, src := range loaded.Sources {
		if src.Name == "currentDate" {
			found = true
			if src.Content != "2026-02-05" {
				t.Fatalf("expected currentDate to be 2026-02-05, got %q", src.Content)
			}
		}
	}

	if !found {
		t.Fatalf("expected currentDate source to be present")
	}
}

func TestLegacyConfigMigration(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "context_config.json")

	legacyJSON := `{
  "refresh_mode": "ttl",
  "refresh_ttl_seconds": 120,
  "current_date": false,
  "global_claude_md": true,
  "project_claude_md": false,
  "mcp_sources": [
    {
      "server_name": "test-server",
      "uri": "resource://foo",
      "kind": "resource",
      "label": "Test Resource",
      "enabled": true
    }
  ]
}`
	if err := os.WriteFile(globalPath, []byte(legacyJSON), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	loader := &FileLoader{configPath: globalPath}
	cfg := loader.GetConfig()

	if cfg.Defaults.RefreshMode != RefreshTTL {
		t.Fatalf("expected defaults.refresh_mode ttl, got %q", cfg.Defaults.RefreshMode)
	}
	if cfg.Defaults.TTLSeconds == nil || *cfg.Defaults.TTLSeconds != 120 {
		t.Fatalf("expected defaults.ttl_seconds 120, got %v", cfg.Defaults.TTLSeconds)
	}

	if source, _, ok := FindSourceByID(cfg.Sources, SourceIDGlobalClaudeMd); !ok || !source.Enabled {
		t.Fatalf("expected global_claude_md enabled after migration")
	}
	if source, _, ok := FindSourceByID(cfg.Sources, SourceIDProjectClaudeMd); !ok || source.Enabled {
		t.Fatalf("expected project_claude_md disabled after migration")
	}
	if source, _, ok := FindSourceByID(cfg.Sources, SourceIDCurrentDate); !ok || source.Enabled {
		t.Fatalf("expected current_date disabled after migration")
	}

	mcpSources := ExtractMCPSources(cfg)
	if len(mcpSources) != 1 {
		t.Fatalf("expected 1 mcp source, got %d", len(mcpSources))
	}
	expectedID := deriveMCPSourceID(MCPContextSource{
		Kind:       MCPSourceResource,
		ServerName: "test-server",
		URI:        "resource://foo",
	})
	if mcpSources[0].ID != expectedID {
		t.Fatalf("expected mcp id %q, got %q", expectedID, mcpSources[0].ID)
	}

	updated, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("failed to read migrated config: %v", err)
	}
	if !strings.Contains(string(updated), "\"sources\"") {
		t.Fatalf("expected migrated config to contain sources list")
	}
}

// TestEnsureBuiltinSources verifies that missing built-in sources are automatically
// added when loading an existing config. This ensures that when new built-in sources
// (like currentDate, projectName) are added to the codebase, existing user configs
// will automatically include them with default settings.
func TestEnsureBuiltinSources(t *testing.T) {
	// Simulate an old config that only has one source
	oldSources := []ContextSourceConfig{
		{ID: SourceIDProjectClaudeMd, Enabled: true},
	}

	// After normalization, missing built-in sources should be added
	normalized := EnsureBuiltinSources(oldSources)

	// Verify the original source is still present
	if _, _, ok := FindSourceByID(normalized, SourceIDProjectClaudeMd); !ok {
		t.Fatalf("expected original source to be preserved")
	}

	// Verify missing built-in sources were added
	if _, _, ok := FindSourceByID(normalized, SourceIDCurrentDate); !ok {
		t.Fatalf("expected currentDate to be added to config")
	}
	if _, _, ok := FindSourceByID(normalized, SourceIDProjectName); !ok {
		t.Fatalf("expected projectName to be added to config")
	}
	if _, _, ok := FindSourceByID(normalized, SourceIDGitStatus); !ok {
		t.Fatalf("expected gitStatus to be added to config")
	}
}

// TestNormalizeConfig_AddsMissingBuiltinSources verifies the full integration
// where NormalizeConfig adds missing built-in sources.
func TestNormalizeConfig_AddsMissingBuiltinSources(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "context_config.json")

	// Config with only one source (simulating old config before currentDate was added)
	oldConfig := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDProjectClaudeMd, Enabled: true},
		},
	}
	writeJSON(t, configPath, oldConfig)

	loader := &FileLoader{configPath: configPath}
	cfg := loader.GetConfig()

	// Should have added currentDate and other missing built-in sources
	if _, _, ok := FindSourceByID(cfg.Sources, SourceIDCurrentDate); !ok {
		t.Fatalf("expected currentDate to be added to config")
	}
	if _, _, ok := FindSourceByID(cfg.Sources, SourceIDProjectName); !ok {
		t.Fatalf("expected projectName to be added to config")
	}
	// Original source should still be there
	if _, _, ok := FindSourceByID(cfg.Sources, SourceIDProjectClaudeMd); !ok {
		t.Fatalf("expected original projectClaudeMd source to be preserved")
	}
}

func TestMergeConfig_GlobalProject(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	projectDir := filepath.Join(dir, "project")
	projectPath := filepath.Join(projectDir, ".swarm", "context_config.json")

	if err := os.MkdirAll(filepath.Dir(projectPath), 0755); err != nil {
		t.Fatalf("failed to create project config dir: %v", err)
	}

	globalCfg := ContextConfig{
		Defaults: ContextDefaults{RefreshMode: RefreshEveryMessage},
		Sources: []ContextSourceConfig{
			{ID: SourceIDGitStatus, Enabled: true},
			{ID: SourceIDGlobalSwarmMd, Enabled: false},
		},
	}
	projectCfg := ContextConfig{
		Defaults: ContextDefaults{RefreshMode: RefreshEveryTurn},
		Sources: []ContextSourceConfig{
			{ID: SourceIDGitStatus, Enabled: false},
			ConfigSourceFromMCP(MCPContextSource{
				ServerName: "project-server",
				URI:        "resource://bar",
				Kind:       MCPSourceResource,
				Enabled:    true,
			}),
		},
	}

	writeJSON(t, globalPath, globalCfg)
	writeJSON(t, projectPath, projectCfg)

	loader := &FileLoader{
		configPath:        globalPath,
		projectConfigPath: projectPath,
	}
	merged := loader.GetConfig()

	if merged.Defaults.RefreshMode != RefreshEveryTurn {
		t.Fatalf("expected project defaults to override refresh_mode, got %q", merged.Defaults.RefreshMode)
	}

	if source, _, ok := FindSourceByID(merged.Sources, SourceIDGitStatus); !ok || source.Enabled {
		t.Fatalf("expected git_status to be overridden to disabled")
	}
	if source, _, ok := FindSourceByID(merged.Sources, SourceIDGlobalSwarmMd); !ok || source.Enabled {
		t.Fatalf("expected global_swarm_md to remain disabled")
	}

	if len(ExtractMCPSources(merged)) != 1 {
		t.Fatalf("expected merged config to include project MCP source")
	}
}

func TestMergeConfig_ProjectOverrideReplaces(t *testing.T) {
	dir := t.TempDir()
	globalPath := filepath.Join(dir, "global.json")
	projectPath := filepath.Join(dir, "project.json")

	globalCfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDProjectName, Enabled: true, Label: "Project Name"},
		},
	}
	projectCfg := ContextConfig{
		Sources: []ContextSourceConfig{
			{ID: SourceIDProjectName, Enabled: false, Label: "Project Name (override)"},
		},
	}

	writeJSON(t, globalPath, globalCfg)
	writeJSON(t, projectPath, projectCfg)

	loader := &FileLoader{
		configPath:        globalPath,
		projectConfigPath: projectPath,
	}
	merged := loader.GetConfig()

	source, _, ok := FindSourceByID(merged.Sources, SourceIDProjectName)
	if !ok {
		t.Fatalf("expected project_name source in merged config")
	}
	if source.Enabled {
		t.Fatalf("expected project override to disable project_name")
	}
	if source.Label != "Project Name (override)" {
		t.Fatalf("expected project override to replace label, got %q", source.Label)
	}
}

func TestExcludeSourcesSuppressesGlobalMemoryOnly(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)

	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("global instructions"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "CLAUDE.md"), []byte("project instructions"), 0644); err != nil {
		t.Fatal(err)
	}

	loader := NewFileLoaderWithConfigDir(filepath.Join(home, ".swarmos"), workspace)
	loader.ExcludeSources(SourceIDGlobalClaudeMd, SourceIDGlobalSwarmMd)
	cfg := ContextConfig{Sources: []ContextSourceConfig{
		{ID: SourceIDGlobalClaudeMd, Enabled: true},
		{ID: SourceIDGlobalSwarmMd, Enabled: true},
		{ID: SourceIDProjectClaudeMd, Enabled: true},
	}}
	loaded, err := loader.LoadContext(cfg, workspace)
	if err != nil {
		t.Fatalf("LoadContext: %v", err)
	}
	if len(loaded.Sources) != 1 {
		t.Fatalf("sources = %#v, want only project context", loaded.Sources)
	}
	if loaded.Sources[0].Name != "claudeMd" || loaded.Sources[0].Content != "project instructions" {
		t.Fatalf("unexpected project source: %#v", loaded.Sources[0])
	}
}

func TestExcludeSourcesDisablesInjectionSourcesAcrossConfigReloads(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	loader := NewFileLoaderWithConfigDir(filepath.Join(home, ".swarmos"), workspace)
	loader.ExcludeSources(SourceIDSkills)

	for i := 0; i < 2; i++ {
		cfg := loader.GetConfig()
		if IsInjectionEnabled(cfg, SourceIDSkills) {
			t.Fatalf("reload %d re-enabled excluded skills injection", i)
		}
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal json: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write json: %v", err)
	}
}
