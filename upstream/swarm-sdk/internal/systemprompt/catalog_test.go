package systemprompt

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFrom_MissingReturnsSentinel(t *testing.T) {
	_, err := LoadFrom(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if !errors.Is(err, ErrCatalogMissing) {
		t.Fatalf("expected ErrCatalogMissing, got %v", err)
	}
}

func TestLoadFrom_ParsesShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "system_prompts.json")
	payload := `{
		"active_prompt": "Coding Assistant",
		"prompts": [
			{"name": "Default Assistant", "content": "hi", "builtin": true},
			{"name": "Coding Assistant", "content": "code", "builtin": true, "workspace_context": true}
		]
	}`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ActivePrompt != "Coding Assistant" {
		t.Errorf("ActivePrompt=%q", c.ActivePrompt)
	}
	if got := c.Names(); len(got) != 2 || got[1] != "Coding Assistant" {
		t.Errorf("Names=%v", got)
	}
	e, ok := c.ByName("Coding Assistant")
	if !ok || e.Content != "code" || !e.WorkspaceContext {
		t.Errorf("ByName=%+v ok=%v", e, ok)
	}
	if _, ok := c.ByName("missing"); ok {
		t.Error("ByName(missing) should return false")
	}
	active, ok := c.Active()
	if !ok || active.Name != "Coding Assistant" {
		t.Errorf("Active=%+v ok=%v", active, ok)
	}
}

func TestActive_UnsetReturnsFalse(t *testing.T) {
	c := &Catalog{Prompts: []Entry{{Name: "x", Content: "y"}}}
	if _, ok := c.Active(); ok {
		t.Error("Active() should be false when ActivePrompt unset")
	}
	c.ActivePrompt = "does-not-exist"
	if _, ok := c.Active(); ok {
		t.Error("Active() should be false when ActivePrompt points to missing entry")
	}
}

func TestResolve_FallbackWhenCatalogEmpty(t *testing.T) {
	c := &Catalog{}
	got := c.Resolve(Request{Role: RoleShotgunPlanner, Fallback: "FALLBACK"})
	if got != "FALLBACK" {
		t.Errorf("want FALLBACK, got %q", got)
	}
}

func TestResolve_ExplicitNameWins(t *testing.T) {
	c := &Catalog{Prompts: []Entry{
		{Name: "Custom Planner", Content: "explicit-hit", Role: string(RoleShotgunPlanner)},
		{Name: "Alt Planner", Content: "alt-hit", Role: string(RoleShotgunPlanner)},
	}}
	got := c.Resolve(Request{Role: RoleShotgunPlanner, ExplicitName: "Alt Planner", Fallback: "fb"})
	if got != "alt-hit" {
		t.Errorf("ExplicitName should win, got %q", got)
	}
}

func TestResolve_RoleMatch(t *testing.T) {
	c := &Catalog{Prompts: []Entry{
		{Name: "Agent", Content: "agent-content"},
		{Name: "Planner", Content: "planner-content", Role: string(RoleShotgunPlanner)},
	}}
	got := c.Resolve(Request{Role: RoleShotgunPlanner, Fallback: "fb"})
	if got != "planner-content" {
		t.Errorf("Role match should return planner-content, got %q", got)
	}
}

func TestResolve_ActivePromptBeatsRoleOrderWhenRolesMatch(t *testing.T) {
	c := &Catalog{
		ActivePrompt: "Second",
		Prompts: []Entry{
			{Name: "First", Content: "first-content", Role: string(RoleShotgunAgent)},
			{Name: "Second", Content: "second-content", Role: string(RoleShotgunAgent)},
		},
	}
	got := c.Resolve(Request{Role: RoleShotgunAgent, Fallback: "fb"})
	if got != "second-content" {
		t.Errorf("Active should beat FirstByRole when role matches, got %q", got)
	}
}

func TestResolve_ExplicitMissingFallsThroughToRole(t *testing.T) {
	c := &Catalog{Prompts: []Entry{
		{Name: "Planner", Content: "planner-content", Role: string(RoleShotgunPlanner)},
	}}
	got := c.Resolve(Request{
		Role:         RoleShotgunPlanner,
		ExplicitName: "nonexistent",
		Fallback:     "fb",
	})
	if got != "planner-content" {
		t.Errorf("missing ExplicitName should fall through to role match, got %q", got)
	}
}

func TestByRole_FiltersEntries(t *testing.T) {
	c := &Catalog{Prompts: []Entry{
		{Name: "A", Role: string(RoleShotgunPlanner)},
		{Name: "B", Role: string(RoleShotgunAgent)},
		{Name: "C", Role: string(RoleShotgunPlanner)},
	}}
	got := c.ByRole(RoleShotgunPlanner)
	if len(got) != 2 || got[0].Name != "A" || got[1].Name != "C" {
		t.Errorf("ByRole filter wrong: %+v", got)
	}
}
