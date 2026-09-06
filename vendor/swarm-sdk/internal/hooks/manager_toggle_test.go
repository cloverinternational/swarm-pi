package hooks

import (
	"context"
	"sync/atomic"
	"testing"
)

// countingHook records how many OnEvent invocations it has received.
type countingHook struct {
	name  string
	prio  int
	calls atomic.Int64
}

func (c *countingHook) OnEvent(_ context.Context, _ Event) (HookResult, error) {
	c.calls.Add(1)
	return Continue(), nil
}

func (c *countingHook) Filter(_ Event) bool { return true }
func (c *countingHook) Priority() int       { return c.prio }
func (c *countingHook) Name() string        { return c.name }

// TestManager_SetEnabled_LiveToggle asserts that flipping a hook's
// Enabled bit at runtime suppresses dispatch on the very next Emit,
// with no need to unregister or rebuild state.
func TestManager_SetEnabled_LiveToggle(t *testing.T) {
	m := NewManager(ManagerConfig{})
	h := &countingHook{name: "live-toggle-hook", prio: 50}
	if err := m.Register(h, ScopeGlobal, ""); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	ctx := context.Background()
	evt := Event{Type: EventToolBeforeExecute, Data: map[string]any{"tool_name": "noop"}}

	if _, err := m.Emit(ctx, evt); err != nil {
		t.Fatalf("Emit #1 failed: %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("hook calls after enabled Emit = %d, want 1", got)
	}

	if err := m.SetEnabled("live-toggle-hook", false); err != nil {
		t.Fatalf("SetEnabled(false) failed: %v", err)
	}
	if _, err := m.Emit(ctx, evt); err != nil {
		t.Fatalf("Emit #2 failed: %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("hook calls after disabled Emit = %d, want 1 (no new fire)", got)
	}

	if err := m.SetEnabled("live-toggle-hook", true); err != nil {
		t.Fatalf("SetEnabled(true) failed: %v", err)
	}
	if _, err := m.Emit(ctx, evt); err != nil {
		t.Fatalf("Emit #3 failed: %v", err)
	}
	if got := h.calls.Load(); got != 2 {
		t.Fatalf("hook calls after re-enabled Emit = %d, want 2", got)
	}
}

// TestManager_SetEnabled_UnknownHook confirms SetEnabled surfaces an error
// for names that aren't registered, so callers don't silently miss typos.
func TestManager_SetEnabled_UnknownHook(t *testing.T) {
	m := NewManager(ManagerConfig{})
	if err := m.SetEnabled("no-such-hook", false); err == nil {
		t.Fatal("SetEnabled on unknown hook should return error, got nil")
	}
}

// TestManager_List_IncludesDisabled confirms that List() returns both
// enabled and disabled hooks. UI consumers need this to render toggles
// without losing track of hooks that are currently off.
func TestManager_List_IncludesDisabled(t *testing.T) {
	m := NewManager(ManagerConfig{})
	a := &countingHook{name: "hook-a", prio: 10}
	b := &countingHook{name: "hook-b", prio: 20}
	_ = m.Register(a, ScopeGlobal, "")
	_ = m.Register(b, ScopeGlobal, "")

	if err := m.Disable("hook-a"); err != nil {
		t.Fatalf("Disable failed: %v", err)
	}

	regs := m.List()
	if len(regs) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(regs))
	}

	var sawDisabled, sawEnabled bool
	for _, r := range regs {
		switch r.Hook.Name() {
		case "hook-a":
			if r.Enabled {
				t.Error("hook-a should be disabled")
			}
			sawDisabled = true
		case "hook-b":
			if !r.Enabled {
				t.Error("hook-b should be enabled")
			}
			sawEnabled = true
		}
	}
	if !sawDisabled || !sawEnabled {
		t.Errorf("List missing entries: sawDisabled=%v sawEnabled=%v", sawDisabled, sawEnabled)
	}
}
