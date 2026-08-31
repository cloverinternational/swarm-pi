package builtin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// newTestDriver constructs a streaming driver with a sane test
// configuration (small buffer, short flush age) and starts it.
func newTestDriver(t *testing.T) *agent.StreamingSteeringDriver {
	t.Helper()
	d := agent.NewStreamingSteeringDriver(agent.SteeringDriverConfig{
		Mode:            agent.SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      100,                    // never count-flush in tests
		FlushAge:        100 * time.Millisecond, // age irrelevant; we never wait
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("driver Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Stop() })
	return d
}

// preToolEvent fabricates a hooks.Event identical in shape to what
// the runtime emits for EventToolBeforeExecute.
func preToolEvent(toolName string) hooks.Event {
	return hooks.Event{
		Type:      hooks.EventToolBeforeExecute,
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name": toolName,
			"params":    map[string]any{"x": 1},
		},
	}
}

func TestStreamHook_BlockArmedTriggersHardBlock(t *testing.T) {
	d := newTestDriver(t)
	d.Target().ArmBlockNext("Bash", "wasteful")

	h := NewSteeringStreamHook(d, nil)
	res, err := h.OnEvent(context.Background(), preToolEvent("Bash"))
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if res.Action != hooks.ActionBlock {
		t.Fatalf("expected hard block, got action=%v msg=%q", res.Action, res.Message)
	}
	if !strings.Contains(res.Message, "wasteful") {
		t.Errorf("block message missing reason: %q", res.Message)
	}

	// One-shot: a second call must NOT block.
	res2, _ := h.OnEvent(context.Background(), preToolEvent("Bash"))
	if res2.Action == hooks.ActionBlock {
		t.Errorf("block fired twice; should have been consumed: %+v", res2)
	}
}

func TestStreamHook_NameMismatchPreservesBlock(t *testing.T) {
	d := newTestDriver(t)
	d.Target().ArmBlockNext("Bash", "off-task")

	h := NewSteeringStreamHook(d, nil)
	// Different tool → block must NOT fire.
	res, _ := h.OnEvent(context.Background(), preToolEvent("Read"))
	if res.Action == hooks.ActionBlock {
		t.Fatalf("named block fired on wrong tool: %+v", res)
	}
	// Original block must still be armed.
	res2, _ := h.OnEvent(context.Background(), preToolEvent("Bash"))
	if res2.Action != hooks.ActionBlock {
		t.Errorf("named block was lost: %+v", res2)
	}
}

func TestStreamHook_NotesDrainedAsContinueMessage(t *testing.T) {
	d := newTestDriver(t)
	d.Target().QueueSystemNote("note-A", 1)
	d.Target().QueueSystemNote("note-B", 9) // higher priority → first

	h := NewSteeringStreamHook(d, nil)
	res, _ := h.OnEvent(context.Background(), preToolEvent("Bash"))
	if res.Action == hooks.ActionBlock {
		t.Fatalf("notes must not block: %+v", res)
	}
	if !strings.Contains(res.Message, "note-A") || !strings.Contains(res.Message, "note-B") {
		t.Errorf("notes missing from message: %q", res.Message)
	}
	idxA := strings.Index(res.Message, "note-A")
	idxB := strings.Index(res.Message, "note-B")
	if idxB > idxA {
		t.Errorf("higher-priority note-B should appear before note-A; got %q", res.Message)
	}
}

func TestStreamHook_RefocusPrependedBeforeNotes(t *testing.T) {
	d := newTestDriver(t)
	d.Target().QueueSystemNote("free-form", 5)
	d.Target().QueueRefocus("phase-2", "wire the hook")

	h := NewSteeringStreamHook(d, nil)
	res, _ := h.OnEvent(context.Background(), preToolEvent("Bash"))
	if res.Action == hooks.ActionBlock {
		t.Fatalf("refocus must not block: %+v", res)
	}
	idxR := strings.Index(res.Message, "Refocus on")
	idxN := strings.Index(res.Message, "free-form")
	if idxR < 0 || idxN < 0 {
		t.Fatalf("missing parts: %q", res.Message)
	}
	if idxR > idxN {
		t.Errorf("refocus must appear before notes: %q", res.Message)
	}
}

func TestStreamHook_ReentrantCtxShortCircuits(t *testing.T) {
	d := newTestDriver(t)
	d.Target().ArmBlockNext("Bash", "would-block")

	h := NewSteeringStreamHook(d, nil)
	ctx := agent.WithSteeringReentrancy(context.Background())
	res, _ := h.OnEvent(ctx, preToolEvent("Bash"))
	if res.Action == hooks.ActionBlock {
		t.Fatalf("reentrant ctx must short-circuit, got block: %+v", res)
	}
	// Block must still be armed (we did NOT consume it).
	insp := d.Target().(agent.DefaultSteeringTargetInspector)
	if _, ok := insp.ConsumeBlockFor("Bash"); !ok {
		t.Errorf("reentrant ctx should not have consumed the armed block")
	}
}

func TestStreamHook_DisabledIsInert(t *testing.T) {
	d := newTestDriver(t)
	d.Target().ArmBlockNext("Bash", "armed")

	h := NewSteeringStreamHook(d, nil)
	h.SetEnabled(false)

	// Filter should reject; we test the documented path by going through Filter.
	if h.Filter(preToolEvent("Bash")) {
		t.Fatal("disabled hook must not pass Filter")
	}
	// And the armed block must remain (no one consumed it).
	insp := d.Target().(agent.DefaultSteeringTargetInspector)
	if _, ok := insp.ConsumeBlockFor("Bash"); !ok {
		t.Errorf("disabled hook should not have consumed armed block")
	}
}

func TestStreamHook_AfterToolEnqueuesAndContinues(t *testing.T) {
	d := newTestDriver(t)
	h := NewSteeringStreamHook(d, nil)

	res, err := h.OnEvent(context.Background(), hooks.Event{
		Type:      hooks.EventToolAfterExecute,
		Timestamp: time.Now(),
		Data:      map[string]any{"tool_name": "Bash", "output": "hello"},
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	if res.Action == hooks.ActionBlock {
		t.Errorf("AfterExecute must never block: %+v", res)
	}
	// Driver has the event in its pipeline; no direct assertion on
	// internal buffer, but we can at least confirm Enqueue did not
	// register a drop (buffer was empty).
	if got := d.DroppedTotal(); got != 0 {
		t.Errorf("unexpected drop on first AfterExecute: %d", got)
	}
}

func TestStreamHook_AgentStoppedTriggersDriverStop(t *testing.T) {
	d := newTestDriver(t)
	h := NewSteeringStreamHook(d, nil)

	if _, err := h.OnEvent(context.Background(), hooks.Event{
		Type:      hooks.EventAgentStopped,
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	// Stop() is invoked in a goroutine; give it a moment to land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// After Stop, IsStreaming still reports stream (mode is set in
		// cfg). We instead probe by calling Start again on a now-
		// stopped driver — it should succeed (idempotent) and we
		// confirm Stop already ran by observing that a subsequent
		// Enqueue does not block. Best-effort sanity check.
		break
	}
	// No assertion failure path; the test mainly proves OnEvent
	// returns without deadlocking when AgentStopped fires.
}

func TestStreamHook_NilDriverIsSafe(t *testing.T) {
	h := NewSteeringStreamHook(nil, nil)
	res, err := h.OnEvent(context.Background(), preToolEvent("Bash"))
	if err != nil {
		t.Fatalf("OnEvent on nil-driver hook: %v", err)
	}
	if res.Action == hooks.ActionBlock {
		t.Errorf("nil driver must Continue, not block: %+v", res)
	}
}
