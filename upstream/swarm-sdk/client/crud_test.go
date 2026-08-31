// Package client — crud_test.go
//
// Tests migrated from session/client_session_crud_test.go.  These exercise
// the agent/profile/hook/context-source/tool/config/theme CRUD methods
// directly on *client.Client (the canonical implementation now that
// session.ClientSession is a type alias for *client.Client).
package client

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// newCrudClient builds a *Client backed by a real configbundle.Manager
// rooted at a temporary directory.
func newCrudClient(t *testing.T) (*Client, *configbundle.Manager) {
	t.Helper()
	dir := t.TempDir()

	globalPath := filepath.Join(dir, "global.json")
	if err := os.WriteFile(globalPath, []byte(`{"schemaVersion":1}`), 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	mgr, err := configbundle.NewManager(context.Background(), configbundle.Options{
		WorkDir:    dir,
		GlobalPath: globalPath,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	c := &Client{}
	c.SetSessionState("act", "", "")
	c.SetConfigManager(mgr)
	return c, mgr
}

// captureFirstEventOfKind installs a handler, runs do(), and returns the
// first event of kind k that fires.
func captureFirstEventOfKind(t *testing.T, c *Client, k EventKind, do func()) Event {
	t.Helper()
	ch := make(chan Event, 16)
	var once sync.Once
	unsub := c.Subscribe(func(ev Event) error {
		if ev.Kind == k {
			once.Do(func() { ch <- ev })
		}
		return nil
	})
	defer unsub()
	do()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatalf("captureFirstEventOfKind: no %s event", k)
		return Event{}
	}
}

// ─── Agent CRUD ──────────────────────────────────────────────────────────────

func TestClient_SetAgent_UpdatesStateAndDispatches(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	ev := captureFirstEventOfKind(t, c, EventAgentChanged, func() {
		if err := c.SetAgent(context.Background(), "engineer"); err != nil {
			t.Fatalf("SetAgent: %v", err)
		}
	})
	ap, ok := ev.Payload.(AgentPayload)
	if !ok {
		t.Fatalf("Payload type: %T want AgentPayload", ev.Payload)
	}
	if ap.Name != "engineer" || ap.Action != "set" {
		t.Errorf("Payload: %+v", ap)
	}
	if got := c.ActiveAgent(); got != "engineer" {
		t.Errorf("ActiveAgent: got %q want engineer", got)
	}
}

func TestClient_SetAgent_RequiresName(t *testing.T) {
	c := newSessClient(t)
	if err := c.SetAgent(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestClient_CreateAgent_PersistsToConfig(t *testing.T) {
	c, mgr := newCrudClient(t)
	spec := AgentSpec{Name: "writer", Description: "drafts copy", Model: "claude-sonnet-4-5"}

	ev := captureFirstEventOfKind(t, c, EventAgentChanged, func() {
		if err := c.CreateAgent(context.Background(), spec); err != nil {
			t.Fatalf("CreateAgent: %v", err)
		}
	})
	ap, ok := ev.Payload.(AgentPayload)
	if !ok || ap.Action != "created" || ap.Name != "writer" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	cb := mgr.Active()
	if cb == nil || len(cb.Agents.Definitions) != 1 || cb.Agents.Definitions[0].Name != "writer" {
		t.Fatalf("agent not persisted: %+v", cb)
	}
}

func TestClient_CreateAgent_NoCfgMgr_ReturnsError(t *testing.T) {
	c := newSessClient(t)
	err := c.CreateAgent(context.Background(), AgentSpec{Name: "x"})
	if err == nil || err.Error() != ErrNoConfigManager.Error() {
		t.Fatalf("expected ErrNoConfigManager, got %v", err)
	}
}

func TestClient_UpdateAgent_ReplacesExisting(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateAgent(context.Background(), AgentSpec{Name: "writer", Description: "draft v1"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := c.UpdateAgent(context.Background(), AgentSpec{Name: "writer", Description: "draft v2"}); err != nil {
		t.Fatalf("UpdateAgent: %v", err)
	}
	defs := mgr.Active().Agents.Definitions
	if len(defs) != 1 || defs[0].Description != "draft v2" {
		t.Fatalf("expected single updated def, got %+v", defs)
	}
}

func TestClient_DeleteAgent_RemovesFromConfig(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateAgent(context.Background(), AgentSpec{Name: "writer"}); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if err := c.DeleteAgent(context.Background(), "writer"); err != nil {
		t.Fatalf("DeleteAgent: %v", err)
	}
	if defs := mgr.Active().Agents.Definitions; len(defs) != 0 {
		t.Fatalf("expected empty defs, got %+v", defs)
	}
}

// ─── Profile CRUD ───────────────────────────────────────────────────────────

func TestClient_CreateProfile_PersistsAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	ev := captureFirstEventOfKind(t, c, EventProfileChanged, func() {
		if err := c.CreateProfile(context.Background(), ProfileSpec{
			Name:        "fast",
			Description: "speedy",
			Model:       "claude-sonnet-4-5",
		}); err != nil {
			t.Fatalf("CreateProfile: %v", err)
		}
	})
	pp, ok := ev.Payload.(ProfilePayload)
	if !ok || pp.Name != "fast" || pp.Action != "created" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	if defs := mgr.Active().Profiles.Inline; len(defs) != 1 || defs[0].Name != "fast" {
		t.Fatalf("expected one profile, got %+v", defs)
	}
}

func TestClient_UpdateProfile_ReplacesExisting(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateProfile(context.Background(), ProfileSpec{Name: "fast", Description: "v1"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := c.UpdateProfile(context.Background(), ProfileSpec{Name: "fast", Description: "v2"}); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	defs := mgr.Active().Profiles.Inline
	if len(defs) != 1 || defs[0].Description != "v2" {
		t.Fatalf("expected single replaced def, got %+v", defs)
	}
}

func TestClient_DeleteProfile_RemovesFromConfig(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateProfile(context.Background(), ProfileSpec{Name: "fast"}); err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if err := c.DeleteProfile(context.Background(), "fast"); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if defs := mgr.Active().Profiles.Inline; len(defs) != 0 {
		t.Fatalf("expected empty profiles, got %+v", defs)
	}
}

// ─── Hook CRUD ──────────────────────────────────────────────────────────────

func TestClient_CreateHook_PersistsAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	spec := HookSpec{Name: "preflight", Event: "tool_call", Command: "echo hi", Enabled: true}
	ev := captureFirstEventOfKind(t, c, EventHookChanged, func() {
		if err := c.CreateHook(context.Background(), spec); err != nil {
			t.Fatalf("CreateHook: %v", err)
		}
	})
	hp, ok := ev.Payload.(HookPayload)
	if !ok || hp.Name != "preflight" || hp.Action != "created" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	if defs := mgr.Active().Hooks.Definitions; len(defs) != 1 || defs[0].Name != "preflight" {
		t.Fatalf("expected one hook, got %+v", defs)
	}
}

func TestClient_ToggleHook_FlipsEnabledFlag(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateHook(context.Background(), HookSpec{
		Name: "preflight", Event: "tool_call", Command: "echo hi", Enabled: true,
	}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	ev := captureFirstEventOfKind(t, c, EventHookChanged, func() {
		if err := c.ToggleHook(context.Background(), "preflight"); err != nil {
			t.Fatalf("ToggleHook: %v", err)
		}
	})
	hp, ok := ev.Payload.(HookPayload)
	if !ok || hp.Action != "toggled" || hp.Enabled {
		t.Fatalf("expected toggled to disabled, got payload: %+v", ev.Payload)
	}
	dis := mgr.Active().Hooks.Disabled
	found := false
	for _, n := range dis {
		if n == "preflight" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected preflight in Disabled list: got %+v", dis)
	}
}

func TestClient_UpdateHook_ReplacesExisting(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateHook(context.Background(), HookSpec{Name: "preflight", Event: "tool_call", Command: "echo a"}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	if err := c.UpdateHook(context.Background(), HookSpec{Name: "preflight", Event: "tool_call", Command: "echo b"}); err != nil {
		t.Fatalf("UpdateHook: %v", err)
	}
	defs := mgr.Active().Hooks.Definitions
	if len(defs) != 1 || defs[0].Command != "echo b" {
		t.Fatalf("expected single updated hook, got %+v", defs)
	}
}

func TestClient_DeleteHook_RemovesFromConfig(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.CreateHook(context.Background(), HookSpec{Name: "preflight", Event: "tool_call"}); err != nil {
		t.Fatalf("CreateHook: %v", err)
	}
	if err := c.DeleteHook(context.Background(), "preflight"); err != nil {
		t.Fatalf("DeleteHook: %v", err)
	}
	if defs := mgr.Active().Hooks.Definitions; len(defs) != 0 {
		t.Fatalf("expected empty defs, got %+v", defs)
	}
}

// ─── System prompt + Context source ─────────────────────────────────────────

func TestClient_SetSystemPrompt_PersistsAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	ev := captureFirstEventOfKind(t, c, EventSystemPromptChanged, func() {
		if err := c.SetSystemPrompt(context.Background(), "concise", "Be brief."); err != nil {
			t.Fatalf("SetSystemPrompt: %v", err)
		}
	})
	pp, ok := ev.Payload.(SystemPromptPayload)
	if !ok || pp.Name != "concise" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	cb := mgr.Active()
	if cb.Prompts.Custom["concise"] != "Be brief." {
		t.Errorf("Custom prompt not persisted: %+v", cb.Prompts.Custom)
	}
	if cb.System.Custom["activeSystemPrompt"] != "concise" {
		t.Errorf("activeSystemPrompt not pinned: %+v", cb.System.Custom)
	}
}

func TestClient_AddContextSource_PersistsAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	spec := ContextSourceSpec{Name: "manual", Type: "file", Path: "/tmp/m.txt", Enabled: true}
	ev := captureFirstEventOfKind(t, c, EventContextSourceChanged, func() {
		if err := c.AddContextSource(context.Background(), spec); err != nil {
			t.Fatalf("AddContextSource: %v", err)
		}
	})
	cp, ok := ev.Payload.(ContextSourcePayload)
	if !ok || cp.Name != "manual" || cp.Action != "added" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	if srcs := mgr.Active().ContextSources.Sources; len(srcs) != 1 || srcs[0].Name != "manual" {
		t.Fatalf("expected one source, got %+v", srcs)
	}
}

func TestClient_ToggleContextSource_FlipsEnabled(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.AddContextSource(context.Background(), ContextSourceSpec{Name: "manual", Type: "file", Enabled: true}); err != nil {
		t.Fatalf("AddContextSource: %v", err)
	}
	ev := captureFirstEventOfKind(t, c, EventContextSourceChanged, func() {
		if err := c.ToggleContextSource(context.Background(), "manual"); err != nil {
			t.Fatalf("ToggleContextSource: %v", err)
		}
	})
	cp, ok := ev.Payload.(ContextSourcePayload)
	if !ok || cp.Action != "toggled" || cp.Enabled {
		t.Fatalf("expected toggled to disabled, payload: %+v", ev.Payload)
	}
	src := mgr.Active().ContextSources.Sources[0]
	if src.Enabled == nil || *src.Enabled {
		t.Errorf("expected disabled, got %+v", src)
	}
}

func TestClient_RemoveContextSource_RemovesFromConfig(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.AddContextSource(context.Background(), ContextSourceSpec{Name: "manual", Type: "file", Enabled: true}); err != nil {
		t.Fatalf("AddContextSource: %v", err)
	}
	if err := c.RemoveContextSource(context.Background(), "manual"); err != nil {
		t.Fatalf("RemoveContextSource: %v", err)
	}
	if srcs := mgr.Active().ContextSources.Sources; len(srcs) != 0 {
		t.Fatalf("expected empty sources, got %+v", srcs)
	}
}

// ─── Tool toggle ────────────────────────────────────────────────────────────

func TestClient_ToggleTool_DispatchesEventToolChanged(t *testing.T) {
	c := newSessClient(t)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer c.Stop(context.Background())

	ev := captureFirstEventOfKind(t, c, EventToolChanged, func() {
		if err := c.ToggleTool(context.Background(), "Bash", false); err != nil {
			t.Fatalf("ToggleTool: %v", err)
		}
	})
	tp, ok := ev.Payload.(ToolPayload)
	if !ok || tp.Name != "Bash" || tp.Enabled {
		t.Fatalf("payload: %+v", ev.Payload)
	}
}

func TestClient_ToggleTool_PersistsToConfig(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.ToggleTool(context.Background(), "Bash", false); err != nil {
		t.Fatalf("ToggleTool: %v", err)
	}
	dis := mgr.Active().Tools.Disabled
	found := false
	for _, n := range dis {
		if n == "Bash" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Bash in Disabled, got %+v", dis)
	}
	if err := c.ToggleTool(context.Background(), "Bash", true); err != nil {
		t.Fatalf("ToggleTool re-enable: %v", err)
	}
	for _, n := range mgr.Active().Tools.Disabled {
		if n == "Bash" {
			t.Errorf("Bash still in Disabled after re-enable")
		}
	}
}

// ─── Config + display ───────────────────────────────────────────────────────

func TestClient_SetConfig_AppliesAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	ev := captureFirstEventOfKind(t, c, EventConfigChanged, func() {
		if err := c.SetConfig(context.Background(), "logLevel", "debug"); err != nil {
			t.Fatalf("SetConfig: %v", err)
		}
	})
	cp, ok := ev.Payload.(ConfigPayload)
	if !ok || cp.Key != "logLevel" || cp.NewValue != "debug" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	if mgr.Active().System.LogLevel != "debug" {
		t.Errorf("LogLevel not persisted")
	}
}

func TestClient_SetConfig_UnknownKeyLandsInCustom(t *testing.T) {
	c, mgr := newCrudClient(t)
	if err := c.SetConfig(context.Background(), "myExperimental", 42); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if v := mgr.Active().System.Custom["myExperimental"]; v != 42 {
		t.Errorf("custom field not stored: %+v", mgr.Active().System.Custom)
	}
}

func TestClient_LoadAndSaveConfig_DispatchEvents(t *testing.T) {
	c, _ := newCrudClient(t)
	loaded := captureFirstEventOfKind(t, c, EventConfigLoaded, func() {
		if err := c.LoadConfig(context.Background()); err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
	})
	if loaded.Kind != EventConfigLoaded {
		t.Errorf("expected EventConfigLoaded")
	}
	saved := captureFirstEventOfKind(t, c, EventConfigSaved, func() {
		if err := c.SaveConfig(context.Background()); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}
	})
	if saved.Kind != EventConfigSaved {
		t.Errorf("expected EventConfigSaved")
	}
}

func TestClient_SetTheme_PersistsAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	ev := captureFirstEventOfKind(t, c, EventThemeChanged, func() {
		if err := c.SetTheme(context.Background(), "dark"); err != nil {
			t.Fatalf("SetTheme: %v", err)
		}
	})
	tp, ok := ev.Payload.(ThemePayload)
	if !ok || tp.Theme != "dark" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	if mgr.Active().System.Theme != "dark" {
		t.Errorf("theme not persisted")
	}
}

func TestClient_ToggleCompactMode_FlipsAndDispatches(t *testing.T) {
	c, mgr := newCrudClient(t)
	ev := captureFirstEventOfKind(t, c, EventConfigChanged, func() {
		if err := c.ToggleCompactMode(context.Background()); err != nil {
			t.Fatalf("ToggleCompactMode: %v", err)
		}
	})
	cp, ok := ev.Payload.(ConfigPayload)
	if !ok || cp.Key != "compactMode" {
		t.Fatalf("payload: %+v", ev.Payload)
	}
	if cm := mgr.Active().System.CompactMode; cm == nil || !*cm {
		t.Errorf("expected CompactMode flipped to true; got %+v", cm)
	}
}

// ─── History ─────────────────────────────────────────────────────────────────

func TestClient_SearchHistory_NoConvs_ReturnsEmpty(t *testing.T) {
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	results, err := c.SearchHistory(context.Background(), "foo")
	if err != nil {
		t.Fatalf("SearchHistory with empty storage should not error, got: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results with no conversations, got %d", len(results))
	}
}

func TestClient_ClearHistory_NoConvs_NoError(t *testing.T) {
	c, err := New(
		WithoutAutoConfig(),
		WithProvider("anthropic", "claude-sonnet-4-5"),
		WithAPIKey("dummy"),
		WithStorageDir(t.TempDir()),
	)
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	if err := c.ClearHistory(context.Background()); err != nil {
		t.Errorf("ClearHistory with empty storage should not error, got: %v", err)
	}
}
