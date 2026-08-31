package loader

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// TestLoadFromFile_MissingReturnsNil confirms a non-existent path yields
// (nil, nil) so callers can probe optional locations cheaply.
func TestLoadFromFile_MissingReturnsNil(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/does/not/exist.json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil Config, got %+v", cfg)
	}
}

// TestLoadFromFile_MalformedErrors verifies unparseable JSON surfaces as an
// error (vs being silently skipped).
func TestLoadFromFile_MalformedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFromFile(path)
	if err == nil {
		t.Error("expected parse error, got nil")
	}
}

// TestLoadFromFile_ClaudeCodeFormat parses a Claude Code settings.json and
// confirms the translation to ShellHookConfig.
func TestLoadFromFile_ClaudeCodeFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{{
				"matcher": "Bash",
				"hooks": []map[string]any{{
					"type":    "command",
					"command": "echo pre-bash",
					"timeout": 30,
				}},
			}},
			"Stop": []map[string]any{{
				"hooks": []map[string]any{{
					"type":    "command",
					"command": "echo stop",
				}},
			}},
		},
	}
	data, _ := json.Marshal(settings)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if cfg == nil || len(cfg.Hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %v", cfg)
	}

	// Sort-independent assertions: collect events and commands.
	events := map[string]string{}
	for _, h := range cfg.Hooks {
		if len(h.EventPatterns) != 1 {
			t.Fatalf("hook %q: want 1 event pattern, got %v", h.Name, h.EventPatterns)
		}
		events[h.EventPatterns[0]] = h.Command
	}
	if got, want := events[hooks.EventToolBeforeExecute], "echo pre-bash"; got != want {
		t.Errorf("PreToolUse → %q, want %q", got, want)
	}
	if got, want := events[hooks.EventAgentStopped], "echo stop"; got != want {
		t.Errorf("Stop → %q, want %q", got, want)
	}

	// 30s timeout should round-trip through the string form.
	for _, h := range cfg.Hooks {
		if h.Command == "echo pre-bash" && h.Timeout != "30s" {
			t.Errorf("timeout: got %q want 30s", h.Timeout)
		}
	}
}

// TestLoadFromFile_SwarmOSFormat parses the native ~/.swarm/config/hooks.json
// schema (custom_hooks list of ShellHookConfig).
func TestLoadFromFile_SwarmOSFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	body := `{
        "custom_hooks": [
            {
                "name": "my-hook",
                "event_patterns": ["tool.before_execute"],
                "command": "/usr/local/bin/audit.sh",
                "priority": 50,
                "timeout": "10s",
                "action": "block",
                "enabled": true
            }
        ]
    }`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if cfg == nil || len(cfg.Hooks) != 1 {
		t.Fatalf("want 1 hook, got %v", cfg)
	}
	if cfg.Hooks[0].Name != "my-hook" {
		t.Errorf("name: got %q want my-hook", cfg.Hooks[0].Name)
	}
}

// TestLoadDefault_ProjectWinsOverUser stages both a user-level and a
// project-level settings.json and confirms both load and both entries end
// up in the merged config (last-write-wins is resolved at execution time
// via Priority, not at load time).
func TestLoadDefault_MergesAllSources(t *testing.T) {
	tmpHome := t.TempDir()
	tmpProj := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// User .claude/settings.json
	userDir := filepath.Join(tmpHome, ".claude")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userJSON := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"user-hook"}]}]}}`
	if err := os.WriteFile(filepath.Join(userDir, "settings.json"), []byte(userJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project .claude/settings.json
	projDir := filepath.Join(tmpProj, ".claude")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projJSON := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"project-hook"}]}]}}`
	if err := os.WriteFile(filepath.Join(projDir, "settings.json"), []byte(projJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDefault(tmpProj)
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if cfg == nil || len(cfg.Hooks) != 2 {
		t.Fatalf("want 2 merged hooks, got %v", cfg)
	}
	cmds := []string{cfg.Hooks[0].Command, cfg.Hooks[1].Command}
	sawUser, sawProj := false, false
	for _, c := range cmds {
		if c == "user-hook" {
			sawUser = true
		}
		if c == "project-hook" {
			sawProj = true
		}
	}
	if !sawUser || !sawProj {
		t.Errorf("both user-hook and project-hook expected; got %v", cmds)
	}
}

// TestLoadDefault_AllMissingIsNotAnError confirms a pristine filesystem
// yields an empty Config rather than an error.
func TestLoadDefault_AllMissingIsNotAnError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	cfg, err := LoadDefault("")
	if err != nil {
		t.Fatalf("LoadDefault: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg should be non-nil even when empty")
	}
	if len(cfg.Hooks) != 0 {
		t.Errorf("want 0 hooks, got %d", len(cfg.Hooks))
	}
}

// TestConfig_ToHooks_SkipsDisabled builds two configs (one enabled, one not)
// and confirms only the enabled one round-trips.
func TestConfig_ToHooks_SkipsDisabled(t *testing.T) {
	cfg := &Config{
		Hooks: []*hooks.ShellHookConfig{
			{Name: "on", EventPatterns: []string{"tool.before_execute"}, Command: "echo on", Enabled: true, Timeout: "10s"},
			{Name: "off", EventPatterns: []string{"tool.before_execute"}, Command: "echo off", Enabled: false, Timeout: "10s"},
		},
	}
	built, err := cfg.ToHooks()
	if err != nil {
		t.Fatalf("ToHooks: %v", err)
	}
	if len(built) != 1 {
		t.Fatalf("want 1 built hook, got %d", len(built))
	}
}

// TestClaudeCode_UnknownEventFallsBackToLowercase confirms we don't drop
// events we don't have an explicit mapping for.
func TestClaudeCode_UnknownEventFallsBackToLowercase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	body := `{"hooks":{"MyCustomEvent":[{"hooks":[{"type":"command","command":"custom"}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if cfg == nil || len(cfg.Hooks) != 1 {
		t.Fatalf("want 1 hook, got %v", cfg)
	}
	if got, want := cfg.Hooks[0].EventPatterns[0], "mycustomevent"; got != want {
		t.Errorf("event: got %q want %q", got, want)
	}
}
