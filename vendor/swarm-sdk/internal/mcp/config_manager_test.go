package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-core/core"
)

type memoryCredentialsStore struct {
	creds *core.Credentials
}

func (m *memoryCredentialsStore) Load(ctx context.Context) (*core.Credentials, error) {
	if m.creds == nil {
		m.creds = &core.Credentials{}
	}
	return m.creds, nil
}

func (m *memoryCredentialsStore) Save(ctx context.Context, creds *core.Credentials) error {
	m.creds = creds
	return nil
}

func TestResolvePrecedence(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	workspace := t.TempDir()

	mgr := NewConfigManager(configDir, workspace, &osFS{}, &memoryCredentialsStore{})
	global := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers: []ServerConfig{{
			Name:    "alpha",
			Command: "npx",
			Enabled: false,
		}},
	}
	project := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers: []ServerConfig{{
			Name:    "alpha",
			Command: "npx",
			Enabled: true,
		}},
	}
	if err := mgr.SaveLayer(ctx, ScopeGlobal, global); err != nil {
		t.Fatalf("save global: %v", err)
	}
	if err := mgr.SaveLayer(ctx, ScopeProject, project); err != nil {
		t.Fatalf("save project: %v", err)
	}

	resolved, err := mgr.Resolve(ctx)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var projectEntry, globalEntry *ResolvedServer
	for i := range resolved.Servers {
		entry := &resolved.Servers[i]
		if entry.Config.Name != "alpha" {
			continue
		}
		if entry.Scope == ScopeProject {
			projectEntry = entry
		}
		if entry.Scope == ScopeGlobal {
			globalEntry = entry
		}
	}
	if projectEntry == nil {
		t.Fatalf("project entry missing")
	}
	if globalEntry == nil {
		t.Fatalf("global entry missing")
	}
	if len(projectEntry.ShadowedBy) != 0 {
		t.Fatalf("project entry should not be shadowed")
	}
	if len(globalEntry.ShadowedBy) == 0 {
		t.Fatalf("global entry should be shadowed")
	}
	if globalEntry.ShadowedBy[0].Origin != OriginProject {
		t.Fatalf("shadowed_by origin = %s, want %s", globalEntry.ShadowedBy[0].Origin, OriginProject)
	}
}

func TestPluginOverrideApplied(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	workspace := t.TempDir()

	mgr := NewConfigManager(configDir, workspace, &osFS{}, &memoryCredentialsStore{})
	mgr.SetPluginServers([]PluginServerEntry{
		{
			Plugin: PluginInfo{
				Name:       "example",
				Source:     "local",
				License:    "MIT",
				Repository: "https://example.com/repo",
				Enabled:    true,
			},
			Server: ServerConfig{
				Name:    "analytics",
				Command: "node",
				Args:    []string{"server.js"},
				Enabled: true,
			},
		},
	})
	global := &ConfigFile{
		SchemaVersion: schemaVersion,
		Servers:       []ServerConfig{},
		PluginOverrides: map[string]map[string]ServerPatch{
			"example": {
				"analytics": {Enabled: new(false)},
			},
		},
	}
	if err := mgr.SaveLayer(ctx, ScopeGlobal, global); err != nil {
		t.Fatalf("save global: %v", err)
	}

	resolved, err := mgr.Resolve(ctx)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	var overrideEntry, baseEntry *ResolvedServer
	for i := range resolved.Servers {
		entry := &resolved.Servers[i]
		if entry.Config.Name != "analytics" {
			continue
		}
		if entry.HasOverride {
			overrideEntry = entry
		} else if entry.Scope == ScopePlugin {
			baseEntry = entry
		}
	}
	if overrideEntry == nil {
		t.Fatalf("override entry missing")
	}
	if overrideEntry.Config.Enabled {
		t.Fatalf("override should disable server")
	}
	if len(overrideEntry.ShadowedBy) != 0 {
		t.Fatalf("override entry should not be shadowed")
	}
	if baseEntry == nil || len(baseEntry.ShadowedBy) == 0 {
		t.Fatalf("base entry should be shadowed")
	}
}

func TestResolveRuntimeServerCredentials(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()
	workspace := t.TempDir()

	credStore := &memoryCredentialsStore{creds: &core.Credentials{
		MCP: map[string]core.MCPCredential{
			"alpha": {
				Token: "t123",
				Headers: map[string]string{
					"API_KEY": "sek1",
					"X-Token": "sek2",
				},
			},
		},
	}}
	mgr := NewConfigManager(configDir, workspace, &osFS{}, credStore)

	server := ServerConfig{
		Name:          "alpha",
		Type:          "http",
		URL:           "https://example.com/mcp",
		SecretEnv:     []string{"API_KEY"},
		SecretHeaders: []string{"X-Token"},
		Auth:          &AuthConfig{Mode: "bearer"},
	}
	runtime, err := mgr.ResolveRuntimeServer(ctx, server)
	if err != nil {
		t.Fatalf("resolve runtime: %v", err)
	}
	if runtime.Env["API_KEY"] != "sek1" {
		t.Fatalf("secret env not applied")
	}
	if runtime.Headers["X-Token"] != "sek2" {
		t.Fatalf("secret header not applied")
	}
	if runtime.Headers["Authorization"] != "Bearer t123" {
		t.Fatalf("auth header not applied")
	}

	cfg := &ConfigFile{SchemaVersion: schemaVersion, Servers: []ServerConfig{server}}
	if err := mgr.SaveLayer(ctx, ScopeGlobal, cfg); err != nil {
		t.Fatalf("save layer: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "mcp_servers.json"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	contents := string(data)
	if strings.Contains(contents, "t123") || strings.Contains(contents, "sek1") || strings.Contains(contents, "sek2") {
		t.Fatalf("secrets leaked into config")
	}
}

func TestLoadConfigRejectsUnknownKeys(t *testing.T) {
	ctx := context.Background()
	configDir := t.TempDir()

	mgr := NewConfigManager(configDir, "", &osFS{}, &memoryCredentialsStore{})
	payload := `{"schema_version":1,"servers":[{"name":"alpha","command":"npx","unknown":"value"}]}`
	if err := os.WriteFile(filepath.Join(configDir, "mcp_servers.json"), []byte(payload), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	if _, err := mgr.LoadLayered(ctx); err == nil {
		t.Fatalf("expected error for unknown keys")
	}
}

//go:fix inline
func boolPtr(value bool) *bool {
	return new(value)
}
