package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent/agenttest"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/agentbridge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// End-to-end: does a REAL sub-agent spawned through (*SubagentTool).Run emit
// observational tool events? (PLAN.md G3)
//
// This is the test that has to be honest. The trap it is written against: an
// e2e sub-agent test where the tool never actually executes (denied by the
// permission gate, or the sub-agent never created) "passes" while asserting
// nothing. Every assertion below therefore keys on a per-run nonce that is
// produced ONLY inside the tool's Execute body, and the test fails loudly if
// the tool's call count is not exactly 1.
// ---------------------------------------------------------------------------

// subNonceTool is the tool the sub-agent will call. It has no
// RequiresPermission method, so the permission gate is skipped entirely
// (internal/agent/tool_permissions.go) — that is deliberate: the subject of
// this test is hook coverage, not permissions, and a silent denial here would
// make the test vacuous.
type subNonceTool struct {
	nonce string

	mu    sync.Mutex
	calls int
}

func (s *subNonceTool) Name() string        { return "sub_nonce_tool" }
func (s *subNonceTool) Description() string { return "returns a per-run nonce" }
func (s *subNonceTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (s *subNonceTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return tools.NewToolResult("sub-agent tool ran: " + s.nonce), nil
}

func (s *subNonceTool) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// observingHook is an in-process hook that DECLARES itself observational and
// records what it sees. It is the consumer a bench recorder will be.
type observingHook struct {
	mu   sync.Mutex
	seen []hooks.Event
}

func (o *observingHook) Name() string       { return "test-observer" }
func (o *observingHook) Priority() int      { return 50 }
func (o *observingHook) ObservationalOnly() {}
func (o *observingHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute || event.Type == hooks.EventToolAfterExecute
}

func (o *observingHook) OnEvent(_ context.Context, event hooks.Event) (hooks.HookResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seen = append(o.seen, event)
	// Deliberately attempts to block. Through the observational view this must
	// be discarded; asserting that is the point of BlockAttempted below.
	return hooks.Block("observational hook tried to block the sub-agent"), nil
}

func (o *observingHook) events() []hooks.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]hooks.Event, len(o.seen))
	copy(out, o.seen)
	return out
}

var _ hooks.ObservationalHook = (*observingHook)(nil)

// blockingNonObservationalHook does NOT declare itself observational. It must
// never run inside the sub-agent.
type blockingNonObservationalHook struct {
	calls int
	mu    sync.Mutex
}

func (b *blockingNonObservationalHook) Name() string              { return "test-blocker" }
func (b *blockingNonObservationalHook) Priority() int             { return 99 }
func (b *blockingNonObservationalHook) Filter(_ hooks.Event) bool { return true }
func (b *blockingNonObservationalHook) OnEvent(_ context.Context, _ hooks.Event) (hooks.HookResult, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	return hooks.Block("no tools for sub-agents"), nil
}

func (b *blockingNonObservationalHook) callCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// subagentHarness is everything needed to drive a real sub-agent spawn.
type subagentHarness struct {
	tool     *SubagentTool
	ctx      context.Context
	nonce    string
	subTool  *subNonceTool
	observer *observingHook
	blocker  *blockingNonObservationalHook
	manager  *hooks.Manager
	view     *hooks.ObservationalView
}

func newSubagentHarness(t *testing.T) *subagentHarness {
	t.Helper()

	nonce := fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	subTool := &subNonceTool{nonce: nonce}

	// A provider registry whose "mock" provider scripts: call the tool, then
	// answer. The sub-agent factory resolves the provider by name from here.
	logger := noop.NewLogger()
	tracer := noop.NewTracer()
	mock := agenttest.NewMockProvider().
		ThenCallTool(subTool.Name(), map[string]any{}).
		ThenRespondWith("sub-agent finished")

	provReg := provider.NewSimpleRegistry(logger)
	if err := provReg.Register("mock", func(_ provider.Config) (provider.Provider, error) {
		return mock, nil
	}); err != nil {
		t.Fatalf("provider registry Register: %v", err)
	}

	factory, err := agent.NewSimpleFactory(agent.FactoryConfig{
		ProviderRegistry: provReg,
		Logger:           logger,
		Tracer:           tracer,
	})
	if err != nil {
		t.Fatalf("NewSimpleFactory: %v", err)
	}

	// Parent tool registry: the sub-agent's tools are copied from here.
	parentReg := tools.NewSimpleRegistry(logger, tracer)
	if err := parentReg.Register(subTool); err != nil {
		t.Fatalf("parent registry Register: %v", err)
	}

	subTool2, err := NewSubagentTool(DelegateTaskConfig{
		Factory:        factory,
		ProviderConfig: provider.Config{Name: "mock", Model: "mock"},
		ParentToolReg:  parentReg,
		Logger:         logger,
		Tracer:         tracer,
	})
	if err != nil {
		t.Fatalf("NewSubagentTool: %v", err)
	}

	// A hooks manager holding one declared observational hook and one
	// undeclared blocking hook, wired into ctx exactly as a parent agent does.
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	observer := &observingHook{}
	blocker := &blockingNonObservationalHook{}
	if err := mgr.Register(observer, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register observer: %v", err)
	}
	if err := mgr.Register(blocker, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register blocker: %v", err)
	}

	bridge := agentbridge.New(mgr)
	ctx := agent.WithHooksManager(context.Background(), bridge)

	h := &subagentHarness{
		tool:     subTool2,
		ctx:      ctx,
		nonce:    nonce,
		subTool:  subTool,
		observer: observer,
		blocker:  blocker,
		manager:  mgr,
		view:     mgr.ObservationalView(),
	}
	t.Cleanup(h.view.Close)
	return h
}

func (h *subagentHarness) run(t *testing.T) *tools.ToolResult {
	t.Helper()
	res, err := h.tool.Run(h.ctx, SubagentParams{Task: "call the tool and report the nonce"})
	if err != nil {
		t.Fatalf("SubagentTool.Run: %v", err)
	}
	return res
}

// TestSubagent_GateOn_EmitsObservationalToolEvents is requirement (a): with the
// gate ON a real sub-agent run emits observational tool events.
func TestSubagent_GateOn_EmitsObservationalToolEvents(t *testing.T) {
	restore := bench.SetObservationalHooksEnabled(true)
	defer restore()

	h := newSubagentHarness(t)
	result := h.run(t)

	// ANTI-VACUITY GATE. If the tool did not execute, nothing below means
	// anything. This is the assertion the previous attempt was missing: a
	// "permission denied for tool bash" run would stop here.
	if got := h.subTool.callCount(); got != 1 {
		t.Fatalf("the sub-agent's tool did not execute exactly once (calls=%d); "+
			"result=%q — every assertion below would be vacuous", got, result.Output)
	}

	if !h.view.WaitDrained(10 * time.Second) {
		t.Fatalf("observational queue never drained: %+v", h.view.Stats())
	}

	seen := h.observer.events()
	if len(seen) == 0 {
		t.Fatalf("sub-agent emitted NO observational events with the gate ON; stats=%+v", h.view.Stats())
	}

	var sawBefore, sawAfter bool
	var afterOutput string
	for _, ev := range seen {
		name, _ := ev.Data["tool_name"].(string)
		if name != h.subTool.Name() {
			continue
		}
		if observational, _ := ev.Data["observational"].(bool); !observational {
			t.Fatalf("observational event missing its provenance marker: %+v", ev.Data)
		}
		switch ev.Type {
		case hooks.EventToolBeforeExecute:
			sawBefore = true
		case hooks.EventToolAfterExecute:
			sawAfter = true
			afterOutput, _ = ev.Data["output"].(string)
		}
	}
	if !sawBefore {
		t.Fatalf("no tool.before_execute observation for %s; got %d events", h.subTool.Name(), len(seen))
	}
	if !sawAfter {
		t.Fatalf("no tool.after_execute observation for %s; got %d events", h.subTool.Name(), len(seen))
	}

	// THE VALUE THAT CANNOT APPEAR UNLESS THE REAL PATH RAN. The nonce is
	// generated per test and produced only inside subNonceTool.Execute, so its
	// presence in an observed after-event proves the sub-agent really executed
	// the tool and the observation really carried its result.
	if !strings.Contains(afterOutput, h.nonce) {
		t.Fatalf("observed after-event did not carry the tool's real output nonce %q; got %q", h.nonce, afterOutput)
	}

	// The observational hook returned Block() on every event. The sub-agent
	// must have been completely unaffected: the tool ran (asserted above) and
	// the run produced a normal result.
	if strings.Contains(strings.ToLower(result.Output), "blocked") {
		t.Fatalf("an observational hook's Block() leaked into the sub-agent run: %q", result.Output)
	}

	// The UNDECLARED blocking hook must never have been invoked inside the
	// sub-agent — not merely ignored.
	if got := h.blocker.callCount(); got != 0 {
		t.Fatalf("undeclared blocking hook ran %d times inside the sub-agent; enrolment gate is broken", got)
	}
}

// TestSubagent_GateOff_BehaviourUnchanged is requirement (b): with the gate OFF
// nothing is attached and nothing is emitted.
//
// It asserts the ABSENCE structurally rather than by a flag: the sub-agent's
// observational field is never set, so there is no "registered but disabled"
// state — which is precisely how --no-hooks came to lie for months.
func TestSubagent_GateOff_BehaviourUnchanged(t *testing.T) {
	restore := bench.SetObservationalHooksEnabled(false)
	defer restore()

	h := newSubagentHarness(t)
	result := h.run(t)

	// The sub-agent still works exactly as before.
	if got := h.subTool.callCount(); got != 1 {
		t.Fatalf("gate OFF must not change sub-agent behaviour; tool calls=%d, result=%q", got, result.Output)
	}
	if !strings.Contains(result.Output, "sub-agent finished") {
		t.Fatalf("unexpected sub-agent result with the gate OFF: %q", result.Output)
	}

	// Give any (incorrectly) queued observation ample time to land before
	// asserting silence, so this test cannot pass by racing.
	h.view.WaitDrained(2 * time.Second)
	time.Sleep(250 * time.Millisecond)

	if seen := h.observer.events(); len(seen) != 0 {
		t.Fatalf("gate OFF must emit nothing; got %d observational events: %+v", len(seen), seen)
	}
	if stats := h.view.Stats(); stats.Accepted != 0 || stats.Dropped != 0 {
		t.Fatalf("gate OFF must not even enqueue: %+v", stats)
	}
	if got := h.blocker.callCount(); got != 0 {
		t.Fatalf("gate OFF: no hook should have run inside the sub-agent, blocker ran %d times", got)
	}
}

// TestSubagent_GateOn_PanickingObservationalHookDoesNotBreakRun is requirement
// (d): a panicking observational hook must not kill, stall, or alter the
// sub-agent run.
func TestSubagent_GateOn_PanickingObservationalHookDoesNotBreakRun(t *testing.T) {
	restore := bench.SetObservationalHooksEnabled(true)
	defer restore()

	h := newSubagentHarness(t)

	// Register an additional declared-observational hook that panics on every
	// event, at a higher priority so it runs BEFORE the recording observer.
	panicker := &panickingObservationalHook{}
	if err := h.manager.Register(panicker, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register panicker: %v", err)
	}

	result := h.run(t)

	if got := h.subTool.callCount(); got != 1 {
		t.Fatalf("a panicking observational hook prevented the tool from running (calls=%d)", got)
	}
	if !strings.Contains(result.Output, "sub-agent finished") {
		t.Fatalf("a panicking observational hook altered the sub-agent's result: %q", result.Output)
	}

	if !h.view.WaitDrained(10 * time.Second) {
		t.Fatalf("queue did not drain after a panicking hook: %+v", h.view.Stats())
	}
	if got := panicker.callCount(); got == 0 {
		t.Fatalf("the panicking hook never ran, so this test proves nothing")
	}
	// The recording observer, which runs after the panicker, still saw events —
	// proving the panic was contained per-hook rather than aborting dispatch.
	if len(h.observer.events()) == 0 {
		t.Fatalf("a panicking hook suppressed all later observations")
	}
	if stats := h.view.Stats(); stats.Panics == 0 {
		t.Fatalf("view should have recorded recovered panics: %+v", stats)
	}
}

// panickingObservationalHook declares itself observational and always panics.
type panickingObservationalHook struct {
	mu    sync.Mutex
	calls int
}

func (p *panickingObservationalHook) Name() string       { return "test-panicker" }
func (p *panickingObservationalHook) Priority() int      { return 100 }
func (p *panickingObservationalHook) ObservationalOnly() {}
func (p *panickingObservationalHook) Filter(_ hooks.Event) bool {
	return true
}

func (p *panickingObservationalHook) OnEvent(_ context.Context, _ hooks.Event) (hooks.HookResult, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	panic("observational hook panicked inside a sub-agent run")
}

func (p *panickingObservationalHook) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

var _ hooks.ObservationalHook = (*panickingObservationalHook)(nil)
