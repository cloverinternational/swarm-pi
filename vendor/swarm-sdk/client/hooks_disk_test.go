package client

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWithHooksFromPath_OptionSetsField confirms the option writes into the
// options struct so initAgent can see it.
func TestWithHooksFromPath_OptionSetsField(t *testing.T) {
	o := options{}
	WithHooksFromPath("/some/path.json")(&o)
	if o.hooksFromPath != "/some/path.json" {
		t.Errorf("hooksFromPath: got %q, want /some/path.json", o.hooksFromPath)
	}
}

// TestWithHooksFromConfig_OptionSetsField confirms the default-locations
// flag option also flows through.
func TestWithHooksFromConfig_OptionSetsField(t *testing.T) {
	o := options{}
	WithHooksFromConfig()(&o)
	if !o.hooksFromConfig {
		t.Error("hooksFromConfig: expected true after WithHooksFromConfig")
	}
}

// TestClient_WithHooksFromPath_RegistersHook builds a Client pointed at a
// SwarmOS-format hooks.json and confirms the hook ends up in
// `Client.opts.extraHooks` (the registration slot initAgent reads from).
func TestClient_WithHooksFromPath_RegistersHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hooks.json")
	body := `{
        "custom_hooks": [
            {
                "name": "disk-loaded",
                "event_patterns": ["tool.before_execute"],
                "command": "/bin/true",
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

	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"), // avoid env dependency // avoid env dependency
		WithHooksFromPath(path),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()

	c.mu.RLock()
	defer c.mu.RUnlock()
	found := false
	for _, h := range c.opts.extraHooks {
		if h != nil && h.Name() == "disk-loaded" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected disk-loaded hook in opts.extraHooks; got %d hooks", len(c.opts.extraHooks))
	}
}

// TestClient_WithHooksFromPath_MalformedErrors surfaces bad JSON from the
// explicit-path loader as a Client.New error.
func TestClient_WithHooksFromPath_MalformedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithHooksFromPath(path),
	)
	if err == nil {
		t.Error("expected error for malformed hooks file, got nil")
	}
}
