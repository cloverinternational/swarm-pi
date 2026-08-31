package client

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// countingHook records every event whose Type matches match. It is used as a
// lightweight observer to assert lifecycle emissions without spinning up a
// full shell hook executor.
type countingHook struct {
	match string
	name  string

	mu     sync.Mutex
	events []hooks.Event
}

func (h *countingHook) Name() string  { return h.name }
func (h *countingHook) Priority() int { return 50 }
func (h *countingHook) Filter(e hooks.Event) bool {
	return e.Type == h.match
}
func (h *countingHook) OnEvent(_ context.Context, e hooks.Event) (hooks.HookResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, e)
	return hooks.Continue(), nil
}
func (h *countingHook) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}
func (h *countingHook) LastEvent() *hooks.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.events) == 0 {
		return nil
	}
	e := h.events[len(h.events)-1]
	return &e
}

// erroringHook always fails OnEvent. Used to confirm lifecycle emission
// tolerates hook errors (the fire-and-forget contract).
type erroringHook struct{ match string }

func (h *erroringHook) Name() string  { return "erroring" }
func (h *erroringHook) Priority() int { return 50 }
func (h *erroringHook) Filter(e hooks.Event) bool {
	return e.Type == h.match
}
func (h *erroringHook) OnEvent(_ context.Context, _ hooks.Event) (hooks.HookResult, error) {
	return hooks.HookResult{}, errors.New("boom")
}

// TestEmitLifecycle_MatchingHookReceivesEvent confirms emitLifecycle dispatches
// to hooks whose Filter matches the event type.
func TestEmitLifecycle_MatchingHookReceivesEvent(t *testing.T) {
	h := &countingHook{match: string(hooks.EventSessionStart), name: "session-counter"}
	c := &Client{}
	c.opts.extraHooks = []hooks.Hook{h}

	c.emitLifecycle(context.Background(), string(hooks.EventSessionStart), map[string]any{
		"conversation_id": "abc",
	})

	if got := h.Count(); got != 1 {
		t.Fatalf("Count: got %d want 1", got)
	}
	last := h.LastEvent()
	if last == nil {
		t.Fatal("expected last event")
	}
	if cid, _ := last.Data["conversation_id"].(string); cid != "abc" {
		t.Errorf("conversation_id: got %q want abc", cid)
	}
}

// TestEmitLifecycle_NonMatchingHookSkipped confirms Filter gating works.
func TestEmitLifecycle_NonMatchingHookSkipped(t *testing.T) {
	session := &countingHook{match: string(hooks.EventSessionStart), name: "session-counter"}
	stopped := &countingHook{match: hooks.EventAgentStopped, name: "stopped-counter"}

	c := &Client{}
	c.opts.extraHooks = []hooks.Hook{session, stopped}

	c.emitLifecycle(context.Background(), string(hooks.EventSessionStart), nil)

	if session.Count() != 1 {
		t.Errorf("session hook expected 1 call, got %d", session.Count())
	}
	if stopped.Count() != 0 {
		t.Errorf("stopped hook should not have fired, got %d", stopped.Count())
	}
}

// TestEmitLifecycle_HookErrorIsSwallowed confirms that a failing hook doesn't
// abort downstream hooks — lifecycle emission is fire-and-forget.
func TestEmitLifecycle_HookErrorIsSwallowed(t *testing.T) {
	bad := &erroringHook{match: string(hooks.EventSessionStart)}
	good := &countingHook{match: string(hooks.EventSessionStart), name: "after-error"}

	c := &Client{}
	c.opts.extraHooks = []hooks.Hook{bad, good}

	c.emitLifecycle(context.Background(), string(hooks.EventSessionStart), nil)
	if good.Count() != 1 {
		t.Errorf("downstream hook should still run after an erroring hook; got %d", good.Count())
	}
}

// TestEmitAgentStopped_SuccessPopulatesFinishReason confirms the success-path
// payload carries the provider's FinishReason and per-turn token counts.
func TestEmitAgentStopped_SuccessPopulatesFinishReason(t *testing.T) {
	h := &countingHook{match: hooks.EventAgentStopped, name: "stop-observer"}
	c := &Client{}
	c.opts.extraHooks = []hooks.Hook{h}

	resp := &agent.ExecuteResponse{
		FinishReason: "stop",
		TurnCount:    3,
		InputTokens:  100,
		OutputTokens: 25,
	}
	c.emitAgentStopped(context.Background(), "conv-1", resp, nil)

	last := h.LastEvent()
	if last == nil {
		t.Fatal("expected event")
	}
	if fr, _ := last.Data["finish_reason"].(string); fr != "stop" {
		t.Errorf("finish_reason: got %q want stop", fr)
	}
	if last.Data["input_tokens"].(int) != 100 || last.Data["output_tokens"].(int) != 25 {
		t.Errorf("tokens: got %v/%v", last.Data["input_tokens"], last.Data["output_tokens"])
	}
	if last.Data["turn_count"].(int) != 3 {
		t.Errorf("turn_count: got %v", last.Data["turn_count"])
	}
}

// TestEmitAgentStopped_ErrorRecordsErrorReason confirms the error path tags
// finish_reason as "error" and carries the error string for observability.
func TestEmitAgentStopped_ErrorRecordsErrorReason(t *testing.T) {
	h := &countingHook{match: hooks.EventAgentStopped, name: "stop-observer"}
	c := &Client{}
	c.opts.extraHooks = []hooks.Hook{h}

	c.emitAgentStopped(context.Background(), "conv-1", nil, errors.New("provider down"))

	last := h.LastEvent()
	if last == nil {
		t.Fatal("expected event")
	}
	if fr, _ := last.Data["finish_reason"].(string); fr != "error" {
		t.Errorf("finish_reason: got %q want error", fr)
	}
	if es, _ := last.Data["error"].(string); es != "provider down" {
		t.Errorf("error: got %q", es)
	}
}

// TestEmitLifecycle_NoHooksIsNoOp confirms the common case (no subscribers)
// doesn't panic and doesn't deliver events to hooks that were never registered.
func TestEmitLifecycle_NoHooksIsNoOp(t *testing.T) {
	// A hook that WOULD match the event, but which we deliberately do not register.
	unregistered := &countingHook{match: hooks.EventAgentStopped, name: "unregistered"}

	c := &Client{} // no extraHooks set
	// Must not panic; returns silently with no subscribers.
	c.emitLifecycle(context.Background(), hooks.EventAgentStopped, nil)

	if got := unregistered.Count(); got != 0 {
		t.Errorf("unregistered hook must not receive events, got %d deliveries", got)
	}
}
