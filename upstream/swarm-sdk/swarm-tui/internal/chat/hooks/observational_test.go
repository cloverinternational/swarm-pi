package hooks

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	sdkhooks "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// tuiObservingHook declares itself observational and records what it sees.
// It also tries to block, which must have no effect.
type tuiObservingHook struct {
	mu   sync.Mutex
	seen []sdkhooks.Event
}

func (h *tuiObservingHook) Name() string       { return "tui-test-observer" }
func (h *tuiObservingHook) Priority() int      { return 10 }
func (h *tuiObservingHook) ObservationalOnly() {}
func (h *tuiObservingHook) Filter(_ sdkhooks.Event) bool {
	return true
}

func (h *tuiObservingHook) OnEvent(_ context.Context, event sdkhooks.Event) (sdkhooks.HookResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen = append(h.seen, event)
	return sdkhooks.Block("tui observational hook tried to block"), nil
}

func (h *tuiObservingHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.seen)
}

var _ sdkhooks.ObservationalHook = (*tuiObservingHook)(nil)

// tuiBlockingHook does NOT declare itself observational.
type tuiBlockingHook struct {
	mu    sync.Mutex
	calls int
}

func (h *tuiBlockingHook) Name() string                 { return "tui-test-blocker" }
func (h *tuiBlockingHook) Priority() int                { return 90 }
func (h *tuiBlockingHook) Filter(_ sdkhooks.Event) bool { return true }
func (h *tuiBlockingHook) OnEvent(_ context.Context, _ sdkhooks.Event) (sdkhooks.HookResult, error) {
	h.mu.Lock()
	h.calls++
	h.mu.Unlock()
	return sdkhooks.Block("blocked"), nil
}

func (h *tuiBlockingHook) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

// TestHooksManager_ImplementsObservationalHooksProvider is the regression guard
// that matters most in this package.
//
// The observational view a sub-agent receives is derived from whatever hooks
// manager the parent put on the context. In a TUI session that is *this* type.
// If this assertion ever fails, the opt-in gate can be switched ON and every
// TUI sub-agent stays dark while the SDK-level tests keep passing — a feature
// that works in tests and does nothing in the product.
func TestHooksManager_ImplementsObservationalHooksProvider(t *testing.T) {
	var m any = &HooksManager{}
	provider, ok := m.(agent.ObservationalHooksProvider)
	if !ok {
		t.Fatalf("*HooksManager must implement agent.ObservationalHooksProvider, " +
			"otherwise TUI sub-agents get no observational coverage")
	}
	// A zero-value manager has no *hooks.Manager, so it must yield nothing
	// rather than panicking on the sub-agent spawn path.
	if got := provider.ObservationalHooks(); got != nil {
		t.Fatalf("a manager with no underlying hooks.Manager must yield nil, got %#v", got)
	}

	var nilManager *HooksManager
	if got := nilManager.ObservationalHooks(); got != nil {
		t.Fatalf("a nil *HooksManager must yield nil, got %#v", got)
	}
}

// TestHooksManager_ObservationalSurfaceCannotBlock asserts the surface handed to
// TUI sub-agents carries no verdict channel and that a declared hook returning
// Block() through it is simply recorded and ignored.
func TestHooksManager_ObservationalSurfaceCannotBlock(t *testing.T) {
	logger := noop.NewLogger()
	tracer := noop.NewTracer()
	m := NewHooksManager(logger, tracer, "")
	if m == nil {
		t.Fatalf("NewHooksManager returned nil")
	}

	observer := &tuiObservingHook{}
	blocker := &tuiBlockingHook{}
	if err := m.manager.Register(observer, sdkhooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register observer: %v", err)
	}
	if err := m.manager.Register(blocker, sdkhooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register blocker: %v", err)
	}

	surface := m.ObservationalHooks()
	if surface == nil {
		t.Fatalf("expected an observational surface from a real TUI hooks manager")
	}

	// No method on the surface may return a verdict. This is the same
	// invariant as internal/agent, re-asserted at the concrete TUI type so a
	// future TUI-specific adapter cannot quietly reintroduce one.
	surfaceType := reflect.TypeOf(surface)
	for i := 0; i < surfaceType.NumMethod(); i++ {
		method := surfaceType.Method(i)
		if method.Type.NumOut() != 0 && (method.Name == "ObserveToolBeforeExecute" || method.Name == "ObserveToolAfterExecute") {
			t.Fatalf("%s must return nothing, got %d return value(s)", method.Name, method.Type.NumOut())
		}
	}

	params := map[string]any{"command": "echo tui"}
	surface.ObserveToolBeforeExecute(context.Background(), "bash", params)
	surface.ObserveToolAfterExecute(context.Background(), "bash", params, nil, "ok", nil)

	// Dispatch is asynchronous; poll rather than sleep a fixed amount.
	deadline := time.Now().Add(5 * time.Second)
	for observer.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := observer.count(); got != 2 {
		t.Fatalf("declared observational hook should have seen 2 events, got %d", got)
	}
	if got := blocker.count(); got != 0 {
		t.Fatalf("undeclared blocking hook must never run through the TUI observational surface, ran %d times", got)
	}
	if params["command"] != "echo tui" {
		t.Fatalf("live params map was mutated through the TUI surface: %#v", params)
	}
}
