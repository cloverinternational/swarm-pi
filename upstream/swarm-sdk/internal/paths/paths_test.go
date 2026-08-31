package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allHelpers returns every string-producing helper's output for scanning.
func allHelpers() map[string]string {
	return map[string]string{
		"Root":              Root(),
		"Config":            Config(),
		"ConfigFile":        ConfigFile(),
		"ProvidersFile":     ProvidersFile(),
		"CredentialsFile":   CredentialsFile(),
		"OAuthFile":         OAuthFile("anthropic"),
		"HooksFile":         HooksFile(),
		"McpServersFile":    McpServersFile(),
		"AgentProfilesFile": AgentProfilesFile(),
		"SystemPromptsFile": SystemPromptsFile(),
		"CustomAgentsFile":  CustomAgentsFile(),
		"SkillsDir":         SkillsDir(),
		"AutogenSkillsDir":  AutogenSkillsDir(),
		"ConversationsDir":  ConversationsDir(),
		"LogFile":           LogFile(),
		"VaultDir":          VaultDir(),
		"A2ARegistryFile":   A2ARegistryFile(),
		"BackupsDir":        BackupsDir(),
		"LocksDir":          LocksDir(),
		"In":                In("config", "nested", "x.json"),
	}
}

func TestRootOverrideViaSwarmHome(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWARM_HOME", tmp)

	if got := Root(); got != tmp {
		t.Fatalf("Root() = %q, want %q", got, tmp)
	}
}

func TestNoHelperContainsSwarmos(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWARM_HOME", tmp)

	for name, p := range allHelpers() {
		if strings.Contains(p, ".swarmos") {
			t.Errorf("%s returned path containing \".swarmos\": %q", name, p)
		}
	}
}

func TestConfigUnderRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWARM_HOME", tmp)

	cfg := Config()
	root := Root()
	rel, err := filepath.Rel(root, cfg)
	if err != nil {
		t.Fatalf("filepath.Rel(%q,%q): %v", root, cfg, err)
	}
	if strings.HasPrefix(rel, "..") {
		t.Fatalf("Config() %q is not under Root() %q (rel=%q)", cfg, root, rel)
	}
	if rel != "config" {
		t.Fatalf("Config() rel to Root() = %q, want \"config\"", rel)
	}
}

func TestDirAccessorsCreateAt0700(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWARM_HOME", tmp)

	dir := Config()
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %q: %v", dir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", dir)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("Config() dir perm = %o, want 0700", perm)
	}
}

func TestInEnsuresParent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SWARM_HOME", tmp)

	p := In("config", "sub", "leaf.json")
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatalf("In() did not ensure parent dir: %v", err)
	}
}
