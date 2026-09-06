package agent_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent/agenttest"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolout"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

// nonceTool returns a value that cannot appear in an observation unless the
// tool genuinely executed. This is the guard against the failure mode where a
// tool is silently never run (e.g. denied by the permission gate) and the test
// "passes" while asserting nothing.
type nonceTool struct {
	nonce string
	calls int
	mu    sync.Mutex
}

func (n *nonceTool) Name() string        { return "nonce_tool" }
func (n *nonceTool) Description() string { return "returns a per-run nonce" }
func (n *nonceTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (n *nonceTool) Execute(_ context.Context, _ map[string]any) (*tools.ToolResult, error) {
	n.mu.Lock()
	n.calls++
	n.mu.Unlock()
	// Files() gives the result a typed structured outcome, so the test can also
	// assert the outcome survives the observation path (G4 evidence).
	res := tools.NewToolResult("tool ran and produced " + n.nonce)
	res.Outcome = toolout.Files(toolout.FileEffect{
		Path:         "/tmp/" + n.nonce,
		Op:           toolout.FileOpCreate,
		BytesWritten: 7,
	})
	return res, nil
}

func (n *nonceTool) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

// recordedObservation is one captured observational event.
type recordedObservation struct {
	phase   string
	tool    string
	output  string
	outcome *toolout.Outcome
	err     error
}

// recordingObserver implements agent.ObservationalHooks.
type recordingObserver struct {
	mu       sync.Mutex
	seen     []recordedObservation
	panicNow bool
}

var _ agent.ObservationalHooks = (*recordingObserver)(nil)

func (r *recordingObserver) ObserveToolBeforeExecute(_ context.Context, toolName string, _ map[string]any) {
	if r.panicNow {
		panic("observer panicked on purpose (before)")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, recordedObservation{phase: "before", tool: toolName})
}

func (r *recordingObserver) ObserveToolAfterExecute(_ context.Context, toolName string, _ map[string]any, outcome *toolout.Outcome, output string, execErr error) {
	if r.panicNow {
		panic("observer panicked on purpose (after)")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, recordedObservation{
		phase:   "after",
		tool:    toolName,
		output:  output,
		outcome: outcome,
		err:     execErr,
	})
}

func (r *recordingObserver) observations() []recordedObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedObservation, len(r.seen))
	copy(out, r.seen)
	return out
}

// blockingHooksManager implements the FULL agent.HooksManager and always
// blocks. It is the contrast case: this is what authority looks like.
type blockingHooksManager struct {
	mu     sync.Mutex
	before int
	after  int
	block  bool
}

var _ agent.HooksManager = (*blockingHooksManager)(nil)

func (b *blockingHooksManager) EmitToolBeforeExecute(_ context.Context, _ string, _ map[string]any) ([]agent.HookResult, error) {
	b.mu.Lock()
	b.before++
	blocking := b.block
	b.mu.Unlock()
	if blocking {
		return []agent.HookResult{{HookName: "blocker", Blocked: true, Output: "denied"}}, nil
	}
	return nil, nil
}

func (b *blockingHooksManager) EmitToolAfterExecute(_ context.Context, _ string, _ map[string]any, _ any, _ error) []agent.HookResult {
	b.mu.Lock()
	b.after++
	b.mu.Unlock()
	return nil
}

func (b *blockingHooksManager) EmitProviderResponse(_ context.Context, _, _ string, _, _ int, _ int64) {
}

func (b *blockingHooksManager) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.before, b.after
}

// newToolCallingAgent builds a real agent whose scripted provider calls the
// tool once and then answers.
func newToolCallingAgent(t *testing.T, tool tools.Tool) *agent.Agent {
	t.Helper()
	mock := agenttest.NewMockProvider().
		ThenCallTool(tool.Name(), map[string]any{}).
		ThenRespondWith("Done.")

	ag, err := agent.New(agent.Config{
		Definition: &agent.Definition{ID: "observational-test", Provider: "mock", Model: "mock"},
		Provider:   mock,
		Tools:      []tools.Tool{tool},
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	return ag
}

// ---------------------------------------------------------------------------
// The mechanical invariant (task constraint 2, agent layer)
// ---------------------------------------------------------------------------

// TestObservationalHooks_InterfaceHasNoVerdictChannel asserts that no method on
// agent.ObservationalHooks returns anything at all.
//
// This is the compile-time half of "a hook reached via the observational view
// cannot block": the agent's call sites have nothing to branch on, so no
// amount of hook misbehaviour can abort a tool call. If someone later adds an
// error return "just for logging", this test fails and the review conversation
// happens before the regression ships.
func TestObservationalHooks_InterfaceHasNoVerdictChannel(t *testing.T) {
	iface := reflect.TypeOf((*agent.ObservationalHooks)(nil)).Elem()
	if iface.Kind() != reflect.Interface {
		t.Fatalf("expected an interface, got %s", iface.Kind())
	}
	if iface.NumMethod() == 0 {
		t.Fatalf("ObservationalHooks has no methods; the invariant would hold vacuously")
	}
	for i := 0; i < iface.NumMethod(); i++ {
		m := iface.Method(i)
		if n := m.Type.NumOut(); n != 0 {
			outs := make([]string, 0, n)
			for j := 0; j < n; j++ {
				outs = append(outs, m.Type.Out(j).String())
			}
			t.Fatalf("ObservationalHooks.%s must return nothing; it returns %v. "+
				"Any return value is a channel through which an observer could block, "+
				"deny, ask, steer, or inject.", m.Name, outs)
		}
	}

	// Discrimination check: the FULL manager interface must still have a
	// verdict channel. Without this, the assertion above could be passing
	// because reflect.NumOut always returns 0 in this test's usage.
	full := reflect.TypeOf((*agent.HooksManager)(nil)).Elem()
	blockingMethod, ok := full.MethodByName("EmitToolBeforeExecute")
	if !ok {
		t.Fatalf("HooksManager.EmitToolBeforeExecute not found; fixture is stale")
	}
	if blockingMethod.Type.NumOut() == 0 {
		t.Fatalf("HooksManager.EmitToolBeforeExecute unexpectedly returns nothing; " +
			"this test can no longer tell a blocking interface from a non-blocking one")
	}
}

// TestObservationalHooks_DoesNotSatisfyHooksManager asserts the two surfaces
// are not interchangeable, so an observational surface cannot be passed to
// SetHooksManager and silently gain authority.
func TestObservationalHooks_DoesNotSatisfyHooksManager(t *testing.T) {
	var observer any = &recordingObserver{}
	if _, ok := observer.(agent.HooksManager); ok {
		t.Fatalf("an ObservationalHooks implementation must not also satisfy agent.HooksManager")
	}
	var manager any = &blockingHooksManager{}
	if _, ok := manager.(agent.ObservationalHooks); ok {
		t.Fatalf("a HooksManager must not accidentally satisfy agent.ObservationalHooks")
	}
}

// ---------------------------------------------------------------------------
// Behaviour
// ---------------------------------------------------------------------------

// TestAgent_ObservationalHooksReceiveRealToolEvents asserts an agent with an
// observational surface (and no HooksManager — the sub-agent configuration)
// emits before/after tool events carrying evidence that can only exist if the
// tool really ran.
func TestAgent_ObservationalHooksReceiveRealToolEvents(t *testing.T) {
	nonce := fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	tool := &nonceTool{nonce: nonce}
	ag := newToolCallingAgent(t, tool)

	observer := &recordingObserver{}
	ag.SetObservationalHooks(observer)
	if !ag.HasObservationalHooks() {
		t.Fatalf("HasObservationalHooks() should be true after attaching")
	}

	if _, err := ag.Run(context.Background(), "call the tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The anti-vacuity gate: if the tool never executed, everything below is
	// meaningless. This is the exact trap that made an earlier attempt's e2e
	// test pass while asserting nothing.
	if got := tool.callCount(); got != 1 {
		t.Fatalf("the tool did not actually execute (calls=%d); the rest of this test would be vacuous", got)
	}

	seen := observer.observations()
	var before, after int
	var afterEvent *recordedObservation
	for i := range seen {
		switch seen[i].phase {
		case "before":
			before++
		case "after":
			after++
			afterEvent = &seen[i]
		}
	}
	if before != 1 || after != 1 {
		t.Fatalf("expected exactly 1 before and 1 after observation, got before=%d after=%d (%+v)", before, after, seen)
	}
	if afterEvent.tool != "nonce_tool" {
		t.Fatalf("wrong tool observed: %q", afterEvent.tool)
	}
	// The nonce is generated per test run and only ever produced inside
	// Execute. Its presence proves the real path ran.
	if !strings.Contains(afterEvent.output, nonce) {
		t.Fatalf("after-observation did not carry the tool's real output nonce %q; got %q", nonce, afterEvent.output)
	}
	// Typed outcome evidence (G4) survives the observation path.
	if afterEvent.outcome == nil {
		t.Fatalf("after-observation lost the typed tool outcome")
	}
	if len(afterEvent.outcome.Effects) != 1 || afterEvent.outcome.Effects[0].Path != "/tmp/"+nonce {
		t.Fatalf("outcome effects not preserved: %+v", afterEvent.outcome.Effects)
	}
	if afterEvent.outcome.Tool != "nonce_tool" {
		t.Fatalf("outcome should be backfilled with the tool name, got %q", afterEvent.outcome.Tool)
	}
	if afterEvent.err != nil {
		t.Fatalf("unexpected execution error in observation: %v", afterEvent.err)
	}
}

// TestAgent_NoObservationalSurface_EmitsNothing is the gate-OFF equivalent at
// the agent layer: with nothing attached, the run proceeds and no observation
// machinery is consulted. There is no "attached but disabled" state.
func TestAgent_NoObservationalSurface_EmitsNothing(t *testing.T) {
	nonce := fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	tool := &nonceTool{nonce: nonce}
	ag := newToolCallingAgent(t, tool)

	if ag.HasObservationalHooks() {
		t.Fatalf("a freshly built agent must not have an observational surface attached")
	}

	res, err := ag.Run(context.Background(), "call the tool")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := tool.callCount(); got != 1 {
		t.Fatalf("tool should still run normally, calls=%d", got)
	}
	if !strings.Contains(res.Message, "Done.") {
		t.Fatalf("unexpected final message: %q", res.Message)
	}
}

// TestAgent_FullHooksSuppressObservationalEmission asserts the change is
// strictly additive: an agent that has a real HooksManager behaves exactly as
// before and does not double-emit through an observational surface.
func TestAgent_FullHooksSuppressObservationalEmission(t *testing.T) {
	tool := &nonceTool{nonce: "suppressed"}
	ag := newToolCallingAgent(t, tool)

	full := &blockingHooksManager{}
	observer := &recordingObserver{}
	ag.SetHooksManager(full)
	ag.SetObservationalHooks(observer)

	if _, err := ag.Run(context.Background(), "call the tool"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	beforeCount, afterCount := full.counts()
	if beforeCount == 0 || afterCount == 0 {
		t.Fatalf("full hooks path did not fire (before=%d after=%d); fixture is broken", beforeCount, afterCount)
	}
	if seen := observer.observations(); len(seen) != 0 {
		t.Fatalf("observational surface must stay silent when the full hook path ran; got %d events: %+v", len(seen), seen)
	}
}

// TestAgent_ObservationalSurfaceCannotBlockToolExecution is the behavioural
// counterpart to the reflect invariant: an observer that tries every trick
// available to it (including panicking) cannot stop the tool.
func TestAgent_ObservationalSurfaceCannotBlockToolExecution(t *testing.T) {
	nonce := fmt.Sprintf("nonce-%d", time.Now().UnixNano())
	tool := &nonceTool{nonce: nonce}
	ag := newToolCallingAgent(t, tool)

	// panicNow makes every observation call panic — the most violent thing an
	// observer can do to the goroutine it is called on.
	ag.SetObservationalHooks(&recordingObserver{panicNow: true})

	res, err := ag.Run(context.Background(), "call the tool")
	if err != nil {
		t.Fatalf("a panicking observer must not fail the run: %v", err)
	}
	if got := tool.callCount(); got != 1 {
		t.Fatalf("a panicking observer must not prevent the tool from running (calls=%d)", got)
	}
	if !strings.Contains(res.Message, "Done.") {
		t.Fatalf("a panicking observer altered the run's outcome: %q", res.Message)
	}

	// Contrast: the FULL hook path with a blocking hook genuinely does stop the
	// tool. Without this the test above could pass simply because nothing in
	// this harness is capable of blocking.
	blockedTool := &nonceTool{nonce: nonce}
	blockedAgent := newToolCallingAgent(t, blockedTool)
	blockedAgent.SetHooksManager(&blockingHooksManager{block: true})
	if _, err := blockedAgent.Run(context.Background(), "call the tool"); err != nil {
		t.Logf("blocked run returned err=%v (acceptable: a block is surfaced as a tool result)", err)
	}
	if got := blockedTool.callCount(); got != 0 {
		t.Fatalf("control failed: the full hook path did not block the tool (calls=%d), "+
			"so this harness cannot distinguish blocking from non-blocking", got)
	}
}
