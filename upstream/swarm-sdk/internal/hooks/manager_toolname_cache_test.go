package hooks

import (
	"context"
	"testing"
)

// bashOnlyHook accepts only tool.before_execute events whose tool is Bash,
// mirroring SleepBlockerHook and StdinConflictHook.
type bashOnlyHook struct{ calls int }

func (h *bashOnlyHook) Name() string  { return "bash-only-probe" }
func (h *bashOnlyHook) Priority() int { return 50 }

func (h *bashOnlyHook) Filter(event Event) bool {
	if event.Type != EventToolBeforeExecute {
		return false
	}
	name, _ := event.Data["tool_name"].(string)
	return name == "Bash" || name == "bash"
}

func (h *bashOnlyHook) OnEvent(_ context.Context, _ Event) (HookResult, error) {
	h.calls++
	return Block("blocked by bash-only-probe"), nil
}

func bashProbeEvent(conv, tool string) Event {
	return Event{
		Type:           EventToolBeforeExecute,
		ConversationID: conv,
		Data: map[string]any{
			"tool_name": tool,
			"params":    map[string]any{"command": "echo hi"},
		},
	}
}

// TestApplicableHookCacheIsKeyedByToolName is a regression test for the bug
// where the hook-applicability cache was keyed only on type/conversation/mode.
// Because the cache stores the RESULT of every hook's Filter(event), the first
// tool.before_execute of a conversation permanently decided the hook set. If
// that first tool was not Bash, tool-name-scoped hooks (sleep-blocker,
// stdin-conflict-advisory) were pruned once and never consulted again — they
// silently stopped working for the rest of the session. That is what made
// issue #118 intermittent: the sleep blocker only ever engaged in conversations
// whose very first tool call happened to be Bash.
func TestApplicableHookCacheIsKeyedByToolName(t *testing.T) {
	m := NewManager(ManagerConfig{})
	probe := &bashOnlyHook{}
	if err := m.Register(probe, ScopeGlobal, ""); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	ctx := context.Background()
	const conv = "conv-1"

	// First event of the conversation is a NON-Bash tool. Before the fix this
	// poisoned the cache for every later event of the same type.
	if _, err := m.EmitWithResult(ctx, bashProbeEvent(conv, "Read")); err != nil {
		t.Fatalf("EmitWithResult(Read) error = %v", err)
	}
	if probe.calls != 0 {
		t.Fatalf("probe ran for Read: calls = %d, want 0", probe.calls)
	}

	// Now a Bash tool call in the SAME conversation. The hook must run.
	res, err := m.EmitWithResult(ctx, bashProbeEvent(conv, "Bash"))
	if probe.calls != 1 {
		t.Fatalf("hook did not run for Bash after a non-Bash first event: calls = %d, want 1 "+
			"(regression: applicability cache ignoring tool name)", probe.calls)
	}
	if err == nil && (res == nil || !res.Blocked) {
		t.Errorf("Bash event was not blocked: err = %v, result = %+v", err, res)
	}

	// Lowercase spelling must also be honoured.
	if _, err := m.EmitWithResult(ctx, bashProbeEvent(conv, "bash")); err != nil && probe.calls != 2 {
		t.Fatalf("lowercase bash did not run the hook: calls = %d, want 2", probe.calls)
	}
	if probe.calls != 2 {
		t.Errorf("after lowercase bash: calls = %d, want 2", probe.calls)
	}

	// A different non-Bash tool must still not run it.
	if _, err := m.EmitWithResult(ctx, bashProbeEvent(conv, "TaskManage")); err != nil {
		t.Fatalf("EmitWithResult(TaskManage) error = %v", err)
	}
	if probe.calls != 2 {
		t.Errorf("hook ran for TaskManage: calls = %d, want 2", probe.calls)
	}
}
