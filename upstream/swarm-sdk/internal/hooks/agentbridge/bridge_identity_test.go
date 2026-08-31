package agentbridge

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// recordingHook captures every Event it observes so a test can assert on the
// identity fields the dispatcher actually delivered to hook code. It records
// the event as received (not a copy of what the test built), which is what
// makes this a real end-to-end assertion rather than a restatement of the
// literal under test.
type recordingHook struct {
	name string
	seen []hooks.Event
}

func (h *recordingHook) OnEvent(_ context.Context, e hooks.Event) (hooks.HookResult, error) {
	h.seen = append(h.seen, e)
	return hooks.Continue(), nil
}
func (h *recordingHook) Filter(_ hooks.Event) bool { return true }
func (h *recordingHook) Priority() int             { return 50 }
func (h *recordingHook) Name() string              { return h.name }

// TestBridge_ToolEvents_CarryOwnerIdentityFromContext is the G2 regression test.
//
// It drives the normal production path — a real hooks.Manager, a real executor,
// and the real Bridge the agent calls through — with a context prepared exactly
// the way internal/agent/agent_tools.go prepares it (tools.WithOwnerInfo), and
// asserts that the Event delivered to hook code carries the agent and
// conversation identity.
//
// Before the fix, agentbridge built hooks.Event without AgentID/ConversationID,
// so both assertions below fail with "" — every tool event was un-attributable.
func TestBridge_ToolEvents_CarryOwnerIdentityFromContext(t *testing.T) {
	const (
		wantAgent = "agent-alpha"
		wantConv  = "conv-12345"
	)

	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &recordingHook{name: "recorder"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := New(mgr)

	// Exactly what the agent does before executing tools
	// (internal/agent/agent_tools.go: tools.WithOwnerInfo(ctx, agentID, "", convID)).
	ctx := tools.WithOwnerInfo(context.Background(), wantAgent, "", wantConv)

	if _, err := b.EmitToolBeforeExecute(ctx, "noop", map[string]any{"k": 1}); err != nil {
		t.Fatalf("EmitToolBeforeExecute: %v", err)
	}
	b.EmitToolAfterExecute(ctx, "noop", map[string]any{"k": 1}, "ok", nil)

	if len(h.seen) != 2 {
		t.Fatalf("observed %d events, want 2 (before+after)", len(h.seen))
	}

	for _, e := range h.seen {
		if e.AgentID != wantAgent {
			t.Errorf("event %q: AgentID = %q, want %q (identity dropped between ctx and Event)",
				e.Type, e.AgentID, wantAgent)
		}
		if e.ConversationID != wantConv {
			t.Errorf("event %q: ConversationID = %q, want %q (identity dropped between ctx and Event)",
				e.Type, e.ConversationID, wantConv)
		}
	}

	// Sanity: the two events are the tool lifecycle pair, so the assertions
	// above covered both the blocking and observational tool paths.
	if h.seen[0].Type != hooks.EventToolBeforeExecute {
		t.Errorf("first event type = %q, want %q", h.seen[0].Type, hooks.EventToolBeforeExecute)
	}
	if h.seen[1].Type != hooks.EventToolAfterExecute {
		t.Errorf("second event type = %q, want %q", h.seen[1].Type, hooks.EventToolAfterExecute)
	}
}

// TestBridge_ToolEvents_IdentityAbsentWithoutOwnerInfo is the negative control
// that keeps the test above honest. If identity were hardcoded (or read from
// somewhere other than the context) the test above would still pass while this
// one failed. Identity must be absent precisely when the context lacks it.
func TestBridge_ToolEvents_IdentityAbsentWithoutOwnerInfo(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &recordingHook{name: "recorder"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := New(mgr)

	if _, err := b.EmitToolBeforeExecute(context.Background(), "noop", nil); err != nil {
		t.Fatalf("EmitToolBeforeExecute: %v", err)
	}
	if len(h.seen) != 1 {
		t.Fatalf("observed %d events, want 1", len(h.seen))
	}
	if got := h.seen[0].AgentID; got != "" {
		t.Errorf("AgentID = %q, want empty when ctx carries no owner info", got)
	}
	if got := h.seen[0].ConversationID; got != "" {
		t.Errorf("ConversationID = %q, want empty when ctx carries no owner info", got)
	}
}

// TestBridge_IdentityDoesNotChangeDispatch pins HARD CONSTRAINT #1: populating
// ConversationID must not activate the dormant conversation-scoped dispatch
// branch in Manager.getApplicableHooks. Today no hook in the repo is registered
// with ScopeConversation, so a populated ConversationID must still select
// exactly the globally-registered hooks — no more, no fewer.
//
// If someone later enables conversation-scoped registration, this test is the
// tripwire: it will start failing and force that change to be evaluated on its
// own merits instead of riding along with an identity fix.
func TestBridge_IdentityDoesNotChangeDispatch(t *testing.T) {
	newBridge := func() (*Bridge, *recordingHook) {
		mgr := hooks.NewManager(hooks.ManagerConfig{})
		h := &recordingHook{name: "global-hook"}
		if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
			t.Fatalf("Register: %v", err)
		}
		return New(mgr), h
	}

	bAnon, hAnon := newBridge()
	if _, err := bAnon.EmitToolBeforeExecute(context.Background(), "noop", nil); err != nil {
		t.Fatalf("anonymous EmitToolBeforeExecute: %v", err)
	}

	bIdent, hIdent := newBridge()
	identCtx := tools.WithOwnerInfo(context.Background(), "agent-alpha", "", "conv-12345")
	if _, err := bIdent.EmitToolBeforeExecute(identCtx, "noop", nil); err != nil {
		t.Fatalf("identified EmitToolBeforeExecute: %v", err)
	}

	if len(hAnon.seen) != len(hIdent.seen) {
		t.Fatalf("dispatch changed: %d hook invocations without identity vs %d with identity; "+
			"populating ConversationID must not alter hook selection",
			len(hAnon.seen), len(hIdent.seen))
	}
	if len(hIdent.seen) != 1 {
		t.Fatalf("global hook invocations = %d, want 1", len(hIdent.seen))
	}
}

// TestBridge_EmitProviderResponse_CarriesOwnerIdentityFromContext is the
// follow-up regression test for the identity gap PLAN.md's G2 task list
// carried forward: EmitProviderResponse built its Event without
// AgentID/ConversationID at all (unlike EmitToolBeforeExecute/AfterExecute,
// which G2 already fixed). Same pattern as
// TestBridge_ToolEvents_CarryOwnerIdentityFromContext above — a context
// prepared exactly the way internal/agent/agent_execute_chain.go now
// prepares it before calling this method.
func TestBridge_EmitProviderResponse_CarriesOwnerIdentityFromContext(t *testing.T) {
	const (
		wantAgent = "agent-beta"
		wantConv  = "conv-67890"
	)

	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &recordingHook{name: "recorder"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := New(mgr)

	ctx := tools.WithOwnerInfo(context.Background(), wantAgent, "", wantConv)
	b.EmitProviderResponse(ctx, "test-provider", "test-model", 10, 20, 5)

	if len(h.seen) != 1 {
		t.Fatalf("observed %d events, want 1", len(h.seen))
	}
	e := h.seen[0]
	if e.Type != hooks.EventProviderAfterResponse {
		t.Fatalf("event type = %q, want %q", e.Type, hooks.EventProviderAfterResponse)
	}
	if e.AgentID != wantAgent {
		t.Errorf("AgentID = %q, want %q (identity dropped between ctx and Event)", e.AgentID, wantAgent)
	}
	if e.ConversationID != wantConv {
		t.Errorf("ConversationID = %q, want %q (identity dropped between ctx and Event)", e.ConversationID, wantConv)
	}
}

// TestBridge_EmitProviderResponse_IdentityAbsentWithoutOwnerInfo is the
// negative control for the test above, same rationale as
// TestBridge_ToolEvents_IdentityAbsentWithoutOwnerInfo.
func TestBridge_EmitProviderResponse_IdentityAbsentWithoutOwnerInfo(t *testing.T) {
	mgr := hooks.NewManager(hooks.ManagerConfig{})
	h := &recordingHook{name: "recorder"}
	if err := mgr.Register(h, hooks.ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}
	b := New(mgr)

	b.EmitProviderResponse(context.Background(), "test-provider", "test-model", 10, 20, 5)

	if len(h.seen) != 1 {
		t.Fatalf("observed %d events, want 1", len(h.seen))
	}
	if got := h.seen[0].AgentID; got != "" {
		t.Errorf("AgentID = %q, want empty when ctx carries no owner info", got)
	}
	if got := h.seen[0].ConversationID; got != "" {
		t.Errorf("ConversationID = %q, want empty when ctx carries no owner info", got)
	}
}
