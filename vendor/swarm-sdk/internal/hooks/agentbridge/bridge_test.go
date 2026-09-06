package agentbridge

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// countingHook is the minimal Hook implementation tests need to observe
// dispatch without pulling the whole builtin suite.
type countingHook struct {
	name    string
	calls   atomic.Int64
	blockOn bool
}

func (h *countingHook) OnEvent(_ context.Context, _ hooks.Event) (hooks.HookResult, error) {
	h.calls.Add(1)
	if h.blockOn {
		return hooks.Block("policy"), nil
	}
	return hooks.ContinueWithMessage("observed"), nil
}
func (h *countingHook) Filter(_ hooks.Event) bool { return true }
func (h *countingHook) Priority() int             { return 50 }
func (h *countingHook) Name() string              { return h.name }

// TestBridge_EmitToolBeforeExecute_ConvertsOutput confirms the bridge fires
// the hook and maps its HookOutput onto agent.HookResult fields the agent
// pipeline expects (HookName, Success, Output, AdditionalContext).
func TestBridge_EmitToolBeforeExecute_ConvertsOutput(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &countingHook{name: "observer"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	b := New(mgr)
	results, err := b.EmitToolBeforeExecute(context.Background(), "noop", map[string]any{"k": 1})
	if err != nil {
		t.Fatalf("EmitToolBeforeExecute: %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("hook calls = %d, want 1", got)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	r := results[0]
	if r.HookName != "observer" {
		t.Errorf("HookName = %q, want observer", r.HookName)
	}
	if !r.Success {
		t.Errorf("Success = false, want true")
	}
	if r.Output != "observed" {
		t.Errorf("Output = %q, want observed", r.Output)
	}
	if r.AdditionalContext != "observed" {
		t.Errorf("AdditionalContext = %q, want observed (mirrors Output)", r.AdditionalContext)
	}
	if r.Blocked {
		t.Errorf("Blocked = true, want false")
	}
}

// TestBridge_EmitToolBeforeExecute_Blocking confirms that when a hook blocks,
// the bridge surfaces both a non-nil error (so the agent aborts the tool)
// and a Blocked=true result entry (so the UI can render the block reason).
func TestBridge_EmitToolBeforeExecute_Blocking(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &countingHook{name: "gatekeeper", blockOn: true}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	b := New(mgr)
	results, err := b.EmitToolBeforeExecute(context.Background(), "dangerous", nil)
	if err == nil {
		t.Fatal("expected blocking error, got nil")
	}
	if len(results) != 1 || !results[0].Blocked {
		t.Fatalf("expected single Blocked result, got %+v", results)
	}
}

// TestBridge_LiveToggle_Roundtrip is the canonical pattern consumers use:
// build a manager, wrap it, flip SetEnabled between emissions, and observe
// that dispatch obeys the flipped bit without rebuilding the bridge.
func TestBridge_LiveToggle_Roundtrip(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &countingHook{name: "togglable"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := New(mgr)

	ctx := context.Background()
	if _, err := b.EmitToolBeforeExecute(ctx, "noop", nil); err != nil {
		t.Fatalf("emit #1: %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("calls after emit #1 = %d, want 1", got)
	}

	if err := mgr.SetEnabled("togglable", false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if _, err := b.EmitToolBeforeExecute(ctx, "noop", nil); err != nil {
		t.Fatalf("emit #2: %v", err)
	}
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("calls after emit #2 (disabled) = %d, want 1", got)
	}

	if err := mgr.SetEnabled("togglable", true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if _, err := b.EmitToolBeforeExecute(ctx, "noop", nil); err != nil {
		t.Fatalf("emit #3: %v", err)
	}
	if got := h.calls.Load(); got != 2 {
		t.Fatalf("calls after emit #3 (re-enabled) = %d, want 2", got)
	}
}

// TestBridge_Manager_Roundtrip confirms Manager() returns the same reference
// the caller supplied, so consumers can keep one variable for both wiring
// and runtime mutation without extra bookkeeping.
func TestBridge_Manager_Roundtrip(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	b := New(mgr)
	if b.Manager() != mgr {
		t.Fatal("Bridge.Manager() should return the exact manager passed to New")
	}
}

// TestBridge_ConcurrentToggleDuringDispatch simulates the TUI's live-execution
// scenario: one goroutine repeatedly emits (the agent running tool calls) while
// another flips SetEnabled on and off (the user mashing Ctrl+S). The test
// passes if -race reports no data races and the counter lands in a coherent
// range — nothing short-circuits, nothing double-fires under the lock.
//
// The bound isn't exact because we intentionally do not synchronize the toggle
// and emit goroutines beyond the manager's own locking; the point is that the
// runtime guarantees hold without callers adding their own mutex.
func TestBridge_ConcurrentToggleDuringDispatch(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &countingHook{name: "live"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := New(mgr)

	const emits = 500
	done := make(chan struct{})

	go func() {
		for range emits {
			_, _ = b.EmitToolBeforeExecute(context.Background(), "noop", nil)
		}
		close(done)
	}()

	// Hammer the toggle while emits are in flight. If either side needed
	// external synchronization, -race would flag it here.
	for i := range emits {
		enabled := i%2 == 0
		if err := mgr.SetEnabled("live", enabled); err != nil {
			t.Fatalf("SetEnabled(%v): %v", enabled, err)
		}
	}
	<-done

	// Final state: leave it enabled and emit once so the counter is sensibly
	// nonzero regardless of toggle interleaving.
	_ = mgr.SetEnabled("live", true)
	if _, err := b.EmitToolBeforeExecute(context.Background(), "noop", nil); err != nil {
		t.Fatalf("final emit: %v", err)
	}
	if got := h.calls.Load(); got < 1 || got > int64(emits+1) {
		t.Errorf("hook calls = %d, want within [1, %d]", got, emits+1)
	}
}

// TestBridge_NilSafety guards against panics when consumers accidentally
// pass a nil bridge or nil manager (e.g. during partial initialization).
func TestBridge_NilSafety(t *testing.T) {
	var b *Bridge
	if _, err := b.EmitToolBeforeExecute(context.Background(), "x", nil); err != nil {
		t.Errorf("nil bridge before: got err=%v, want nil", err)
	}
	if got := b.EmitToolAfterExecute(context.Background(), "x", nil, nil, errors.New("x")); got != nil {
		t.Errorf("nil bridge after: got %v, want nil", got)
	}

	b2 := &Bridge{}
	if _, err := b2.EmitToolBeforeExecute(context.Background(), "x", nil); err != nil {
		t.Errorf("empty bridge before: got err=%v, want nil", err)
	}
}
