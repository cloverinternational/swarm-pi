package hooks

import (
	"context"
	"testing"

	sdkhooks "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// stubHook is a minimal Hook implementation for exercising the TUI's
// toggle wrapper. It does not fire any real logic — we only care about
// registration state tracking.
type stubHook struct {
	name string
}

func (s *stubHook) OnEvent(_ context.Context, _ sdkhooks.Event) (sdkhooks.HookResult, error) {
	return sdkhooks.Continue(), nil
}
func (s *stubHook) Filter(_ sdkhooks.Event) bool { return true }
func (s *stubHook) Priority() int                { return 50 }
func (s *stubHook) Name() string                 { return s.name }

// newTestHooksManager builds a HooksManager backed by a fresh SDK
// Manager with no built-in hooks registered, so tests have full control
// over what's in the registry.
func newTestHooksManager(t *testing.T) *HooksManager {
	t.Helper()
	return &HooksManager{
		manager:    sdkhooks.NewManager(sdkhooks.ManagerConfig{}),
		hookStates: map[string]bool{},
	}
}

// TestToggleGroupByPrefix_FlipsOnlyMatching confirms the prefix match
// is exact and scoped — "steering" must not knock out "task-enforcement".
func TestToggleGroupByPrefix_FlipsOnlyMatching(t *testing.T) {
	hm := newTestHooksManager(t)

	steering := &stubHook{name: "steering-pretool"}
	steeringExtra := &stubHook{name: "steering-posttool"}
	unrelated := &stubHook{name: "task-enforcement"}

	for _, h := range []*stubHook{steering, steeringExtra, unrelated} {
		if err := hm.manager.Register(h, sdkhooks.ScopeGlobal, ""); err != nil {
			t.Fatalf("Register %s: %v", h.Name(), err)
		}
	}

	count, enabled, err := hm.ToggleGroupByPrefix("steering")
	if err != nil {
		t.Fatalf("ToggleGroupByPrefix: %v", err)
	}
	if enabled {
		t.Errorf("expected newState=false after first flip (both steering hooks were enabled)")
	}
	if count != 2 {
		t.Errorf("flipped count = %d, want 2", count)
	}

	states := map[string]bool{}
	for _, s := range hm.ListHookStates() {
		states[s.Name] = s.Enabled
	}
	if states["steering-pretool"] || states["steering-posttool"] {
		t.Errorf("steering hooks should be disabled: %+v", states)
	}
	if !states["task-enforcement"] {
		t.Errorf("task-enforcement must not be affected: %+v", states)
	}

	count2, enabled2, err := hm.ToggleGroupByPrefix("steering")
	if err != nil {
		t.Fatalf("ToggleGroupByPrefix 2nd: %v", err)
	}
	if !enabled2 {
		t.Errorf("second flip should re-enable steering hooks")
	}
	if count2 != 2 {
		t.Errorf("second flip count = %d, want 2", count2)
	}
}

// TestToggleGroupByPrefix_NoMatch returns (0, false, nil) when nothing
// matches — the UI path relies on this to show a "no steering hooks
// registered" notification instead of silently flipping zero hooks to
// "off" and misleading the user.
func TestToggleGroupByPrefix_NoMatch(t *testing.T) {
	hm := newTestHooksManager(t)
	count, enabled, err := hm.ToggleGroupByPrefix("steering")
	if err != nil {
		t.Fatalf("ToggleGroupByPrefix: %v", err)
	}
	if count != 0 || enabled {
		t.Errorf("empty registry: got count=%d enabled=%v, want 0/false", count, enabled)
	}
}

// TestToggleGroupByPrefix_SyncsHookStates guards against a regression where
// Ctrl+S would flip the SDK manager's Enabled bit but leave the TUI's
// hookStates cache stale, causing IsEnabled (used by the hooks dashboard
// renderer) to lie about the live state.
func TestToggleGroupByPrefix_SyncsHookStates(t *testing.T) {
	hm := newTestHooksManager(t)
	h := &stubHook{name: "steering-pretool"}
	if err := hm.manager.Register(h, sdkhooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Seed the cache the way Enable* helpers do.
	hm.hookStates["steering-pretool"] = true

	if _, _, err := hm.ToggleGroupByPrefix("steering"); err != nil {
		t.Fatalf("ToggleGroupByPrefix: %v", err)
	}
	if hm.IsEnabled("steering-pretool") {
		t.Error("after disable, IsEnabled must return false (cache synced with SDK)")
	}

	if _, _, err := hm.ToggleGroupByPrefix("steering"); err != nil {
		t.Fatalf("ToggleGroupByPrefix re-enable: %v", err)
	}
	if !hm.IsEnabled("steering-pretool") {
		t.Error("after re-enable, IsEnabled must return true (cache synced with SDK)")
	}
}

// TestSetHookEnabledByName_SyncsCache mirrors the above for the single-hook
// entrypoint that external callers use directly.
func TestSetHookEnabledByName_SyncsCache(t *testing.T) {
	hm := newTestHooksManager(t)
	h := &stubHook{name: "lone-hook"}
	if err := hm.manager.Register(h, sdkhooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	hm.hookStates["lone-hook"] = true

	if err := hm.SetHookEnabledByName("lone-hook", false); err != nil {
		t.Fatalf("SetHookEnabledByName: %v", err)
	}
	if hm.IsEnabled("lone-hook") {
		t.Error("IsEnabled should be false after SetHookEnabledByName(false)")
	}
}

// TestToggleAllHooks_ActsAsKillSwitch confirms that ToggleAllHooks flips
// every hook regardless of prefix — the Ctrl+Shift+S master kill-switch
// path.
func TestToggleAllHooks_ActsAsKillSwitch(t *testing.T) {
	hm := newTestHooksManager(t)
	names := []string{"a-hook", "b-hook", "c-hook"}
	for _, n := range names {
		if err := hm.manager.Register(&stubHook{name: n}, sdkhooks.ScopeGlobal, ""); err != nil {
			t.Fatalf("Register %s: %v", n, err)
		}
	}

	count, enabled, err := hm.ToggleAllHooks()
	if err != nil {
		t.Fatalf("ToggleAllHooks: %v", err)
	}
	if enabled {
		t.Errorf("after first flip all hooks should be disabled")
	}
	if count != len(names) {
		t.Errorf("count = %d, want %d", count, len(names))
	}

	for _, s := range hm.ListHookStates() {
		if s.Enabled {
			t.Errorf("hook %s still enabled after master-off", s.Name)
		}
	}
}
