package autogenskills

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
)

// --- Part C of the "join task-enforcement + skill-budget-enforcement"
// change: the "[SKILL REVIEW]" nudge fires preferentially when a task
// transitions to completed, instead of purely on a raw tool-call count.

// TestLifecycleHook_TaskCompletionTriggersSkillReviewNudge verifies that a
// legacy TaskUpdate call marking a task completed, after some prior
// non-skill work, fires a completion-triggered nudge (not the count-based
// one — NudgeInterval is set very high so the count-based path could not
// possibly fire on its own).
func TestLifecycleHook_TaskCompletionTriggersSkillReviewNudge(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100})
	ctx := context.Background()

	// A few non-skill tool calls first (the "work" the task covered).
	for range 3 {
		_, _ = hook.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	result, err := hook.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "TaskUpdate",
			"params":    map[string]any{"taskId": "1", "status": "completed"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Message, "[SKILL REVIEW]") || !strings.Contains(result.Message, "completed a task") {
		t.Fatalf("expected a task-completion skill-review nudge, got: %q", result.Message)
	}
}

// TestLifecycleHook_TaskCompletionViaTaskManageTriggersNudge verifies the
// same behavior via a TaskManage batch call whose tool_output shows an
// update operation that succeeded with status "completed".
func TestLifecycleHook_TaskCompletionViaTaskManageTriggersNudge(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100})
	ctx := context.Background()

	for range 3 {
		_, _ = hook.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
	}

	result, err := hook.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "TaskManage",
			"params": map[string]any{"operations": []any{
				map[string]any{"key": "done", "op": "update", "taskId": "1", "status": "completed"},
			}},
			"tool_output": `{"status":"succeeded","results":[{"key":"done","op":"update","status":"succeeded","data":{"task":{"id":"1","content":"x","status":"completed","priority":"medium"}}}]}`,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Message, "[SKILL REVIEW]") || !strings.Contains(result.Message, "completed a task") {
		t.Fatalf("expected a task-completion skill-review nudge via TaskManage, got: %q", result.Message)
	}
}

// TestLifecycleHook_TrivialTaskCompletionDoesNotNudge verifies a task
// completed with NO other non-skill tool calls in between (iters==1, just
// the completion call itself) does not trigger the completion nudge.
func TestLifecycleHook_TrivialTaskCompletionDoesNotNudge(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100})
	ctx := context.Background()

	result, err := hook.OnEvent(ctx, hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "TaskUpdate",
			"params":    map[string]any{"taskId": "1", "status": "completed"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result.Message, "completed a task") {
		t.Fatalf("did not expect a completion nudge for a trivial (no prior work) completion, got: %q", result.Message)
	}
}

// TestLifecycleHook_CountBasedNudgeStillWorksWithoutCompletion verifies the
// raw tool-count nudge still fires as a fallback when no task ever
// completes — the completion path must not have replaced it.
func TestLifecycleHook_CountBasedNudgeStillWorksWithoutCompletion(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 3})
	ctx := context.Background()

	var lastMsg string
	for range 5 {
		result, _ := hook.OnEvent(ctx, hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "bash"},
		})
		if result.Message != "" {
			lastMsg = result.Message
		}
	}
	if !strings.Contains(lastMsg, "[SKILL REVIEW] You've made") {
		t.Fatalf("expected the count-based nudge to still fire without any completion, got: %q", lastMsg)
	}
}

// helper: create a service and lifecycle hook for testing.
func newTestHook(t *testing.T, trigger TriggerConfig) (*LifecycleHook, *Service, *Metrics) {
	t.Helper()
	reg := skills.NewRegistry()
	m := &Metrics{}
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: trigger}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	hook, err := NewLifecycleHook(svc)
	if err != nil {
		t.Fatalf("NewLifecycleHook: %v", err)
	}
	return hook, svc, m
}

// TestLifecycleHook_New_nilService rejects nil service.
func TestLifecycleHook_New_nilService(t *testing.T) {
	_, err := NewLifecycleHook(nil)
	if err == nil {
		t.Error("expected error for nil service")
	}
}

// TestLifecycleHook_New_success creates hook with valid service.
func TestLifecycleHook_New_success(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	hook, err := NewLifecycleHook(svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hook == nil {
		t.Fatal("expected non-nil hook")
	}
}

// TestLifecycleHook_Name returns expected identifier.
func TestLifecycleHook_Name(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	hook, _ := NewLifecycleHook(svc)

	if hook.Name() != "autogenskills" {
		t.Errorf("Name() = %q, want autogenskills", hook.Name())
	}
}

// TestLifecycleHook_Priority returns nudge-delivering priority.
func TestLifecycleHook_Priority(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	hook, _ := NewLifecycleHook(svc)

	if hook.Priority() != 91 {
		t.Errorf("Priority() = %d, want 91", hook.Priority())
	}
}

// TestLifecycleHook_Filter_toolAfterExecute matches.
func TestLifecycleHook_Filter_toolAfterExecute(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	hook, _ := NewLifecycleHook(svc)

	if !hook.Filter(hooks.Event{Type: hooks.EventToolAfterExecute}) {
		t.Error("should match EventToolAfterExecute")
	}
}

// TestLifecycleHook_Filter_toolExecutionFailed matches.
func TestLifecycleHook_Filter_toolExecutionFailed(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	hook, _ := NewLifecycleHook(svc)

	if !hook.Filter(hooks.Event{Type: hooks.EventToolExecutionFailed}) {
		t.Error("should match EventToolExecutionFailed")
	}
}

// TestLifecycleHook_Filter_otherEvent ignores non-tool events.
func TestLifecycleHook_Filter_otherEvent(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)
	hook, _ := NewLifecycleHook(svc)

	if hook.Filter(hooks.Event{Type: hooks.EventConversationCreated}) {
		t.Error("should not match conversation events")
	}
	if hook.Filter(hooks.Event{Type: hooks.EventAgentStarted}) {
		t.Error("should not match agent events")
	}
}

// TestLifecycleHook_OnEvent_success increments tool call counter.
func TestLifecycleHook_OnEvent_success(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	result, err := hook.OnEvent(context.Background(), hooks.Event{Type: hooks.EventToolAfterExecute})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Error("should always continue")
	}

	snap := m.Snapshot()
	if snap.ToolCallCount != 1 {
		t.Errorf("ToolCallCount = %d, want 1", snap.ToolCallCount)
	}
}

// TestLifecycleHook_OnEvent_failure increments error counter.
func TestLifecycleHook_OnEvent_failure(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	result, err := hook.OnEvent(context.Background(), hooks.Event{Type: hooks.EventToolExecutionFailed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Error("should always continue")
	}

	snap := m.Snapshot()
	if snap.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", snap.ErrorCount)
	}
}

// TestMustRegister_success returns hook for valid service.
func TestMustRegister_success(t *testing.T) {
	reg := skills.NewRegistry()
	cfg := Config{Mode: ModeManual, AutogenDir: t.TempDir()}
	svc, _ := NewService(cfg, reg, nil)

	hook := MustRegister(svc)
	if hook == nil {
		t.Error("expected non-nil hook")
	}
}

// TestMustRegister_panic panics on nil service.
func TestMustRegister_panic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil service")
		}
	}()
	MustRegister(nil)
}

// ── New tests for itersSinceSkill ───────────────────────────────────────

// TestLifecycleHook_ItersSinceSkill_Increments verifies that itersSinceSkill
// increments on each non-skill tool call.
func TestLifecycleHook_ItersSinceSkill_Increments(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	for i := 1; i <= 5; i++ {
		_, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
		if err != nil {
			t.Fatalf("OnEvent %d: %v", i, err)
		}
	}

	hook.mu.Lock()
	iters := hook.itersSinceSkill
	hook.mu.Unlock()

	if iters != 5 {
		t.Errorf("itersSinceSkill = %d, want 5", iters)
	}
}

// TestLifecycleHook_ItersSinceSkill_ResetsOnSkillTool verifies that
// itersSinceSkill resets to 0 when a skill tool is used.
func TestLifecycleHook_ItersSinceSkill_ResetsOnSkillTool(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	// Make some non-skill calls
	for i := range 3 {
		_, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
		if err != nil {
			t.Fatalf("OnEvent non-skill %d: %v", i, err)
		}
	}

	hook.mu.Lock()
	iters := hook.itersSinceSkill
	hook.mu.Unlock()
	if iters != 3 {
		t.Fatalf("itersSinceSkill before skill = %d, want 3", iters)
	}

	// Now use a skill tool
	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "SkillManage"},
	})
	if err != nil {
		t.Fatalf("OnEvent skill tool: %v", err)
	}

	hook.mu.Lock()
	iters = hook.itersSinceSkill
	hook.mu.Unlock()
	if iters != 0 {
		t.Errorf("itersSinceSkill after skill tool = %d, want 0", iters)
	}
}

// TestLifecycleHook_IntermediateNudge_Fires verifies that an intermediate nudge
// fires when itersSinceSkill exceeds the nudge interval.
func TestLifecycleHook_IntermediateNudge_Fires(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 3})

	// Make 3 non-skill calls — should not nudge yet (need > NudgeInterval)
	for i := range 3 {
		result, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
		if err != nil {
			t.Fatalf("OnEvent %d: %v", i, err)
		}
		if result.Message != "" {
			t.Errorf("call %d: unexpected nudge message: %q", i+1, result.Message)
		}
	}

	// 4th call: itersSinceSkill=4 > NudgeInterval=3, should nudge
	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent nudge call: %v", err)
	}
	if result.Message == "" {
		t.Error("expected nudge message when itersSinceSkill exceeds interval")
	}
	if !strings.Contains(result.Message, "SKILL REVIEW") {
		t.Errorf("nudge should contain SKILL REVIEW, got: %q", result.Message)
	}
	if !strings.Contains(result.Message, "tool calls since the last skill review") {
		t.Errorf("nudge should mention tool calls since last skill review, got: %q", result.Message)
	}
}

// TestLifecycleHook_IntermediateNudge_ResetsCounter verifies that
// itersSinceSkill resets to 0 after an intermediate nudge fires.
func TestLifecycleHook_IntermediateNudge_ResetsCounter(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 2})

	// Make 3 calls to trigger nudge (itersSinceSkill=3 > 2)
	for i := range 3 {
		_, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
		if err != nil {
			t.Fatalf("OnEvent %d: %v", i, err)
		}
	}

	hook.mu.Lock()
	iters := hook.itersSinceSkill
	hook.mu.Unlock()
	if iters != 0 {
		t.Errorf("itersSinceSkill after nudge = %d, want 0", iters)
	}
}

// TestLifecycleHook_NoNudgeBelowInterval verifies no nudge when
// itersSinceSkill is below the interval.
func TestLifecycleHook_NoNudgeBelowInterval(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 10})

	// Make 5 non-skill calls (well below interval of 10)
	for i := range 5 {
		result, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
		if err != nil {
			t.Fatalf("OnEvent %d: %v", i, err)
		}
		if result.Message != "" {
			t.Errorf("call %d: unexpected nudge message: %q", i+1, result.Message)
		}
	}
}

// ── Error-resolution nudge tests ───────────────────────────────────────

// TestLifecycleHook_HadRecentError_SetOnFailure verifies that hadRecentError
// is set on EventToolExecutionFailed.
func TestLifecycleHook_HadRecentError_SetOnFailure(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolExecutionFailed,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	hook.mu.Lock()
	hadErr := hook.hadRecentError
	hook.mu.Unlock()

	if !hadErr {
		t.Error("hadRecentError should be true after EventToolExecutionFailed")
	}
}

// TestLifecycleHook_HadRecentError_ClearedOnSuccess verifies that
// hadRecentError is cleared on the next successful tool execution.
func TestLifecycleHook_HadRecentError_ClearedOnSuccess(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100, ErrorResolutionThreshold: 100})

	// Trigger an error
	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolExecutionFailed,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	// Now a successful tool call should clear hadRecentError
	_, err = hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent success: %v", err)
	}

	hook.mu.Lock()
	hadErr := hook.hadRecentError
	hook.mu.Unlock()

	if hadErr {
		t.Error("hadRecentError should be false after successful tool execution")
	}
}

// TestLifecycleHook_HandleErrorResolved_Called verifies that
// HandleErrorResolved() is called when an error is resolved.
func TestLifecycleHook_HandleErrorResolved_Called(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100, ErrorResolutionThreshold: 100})

	// Trigger an error
	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolExecutionFailed,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	snap := m.Snapshot()
	if snap.ErrorCount != 1 {
		t.Fatalf("ErrorCount = %d, want 1", snap.ErrorCount)
	}
	if snap.ErrorResolvedCount != 0 {
		t.Errorf("ErrorResolvedCount = %d, want 0 before resolution", snap.ErrorResolvedCount)
	}

	// Resolve the error with a successful tool call
	_, err = hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent success: %v", err)
	}

	snap = m.Snapshot()
	if snap.ErrorResolvedCount != 1 {
		t.Errorf("ErrorResolvedCount = %d, want 1 after resolution", snap.ErrorResolvedCount)
	}
}

// TestLifecycleHook_ErrorResolutionNudge_Fires verifies that the
// error-resolution nudge fires when hadRecentError is true and threshold is met.
func TestLifecycleHook_ErrorResolutionNudge_Fires(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100, ErrorResolutionThreshold: 1})

	// Trigger an error
	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolExecutionFailed,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	// Resolve the error — threshold=1 so nudge should fire
	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent success: %v", err)
	}
	if result.Message == "" {
		t.Error("expected error-resolution nudge message")
	}
	if !strings.Contains(result.Message, "resolved an error") {
		t.Errorf("nudge should mention resolved error, got: %q", result.Message)
	}
	if !strings.Contains(result.Message, "SKILL REVIEW") {
		t.Errorf("nudge should contain SKILL REVIEW, got: %q", result.Message)
	}
}

// TestLifecycleHook_ErrorResolutionNudge_ThresholdNotMet verifies that
// no error-resolution nudge fires when the threshold is not met.
func TestLifecycleHook_ErrorResolutionNudge_ThresholdNotMet(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 100, ErrorResolutionThreshold: 2})

	// Trigger one error
	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolExecutionFailed,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	// Resolve it — ErrorResolvedCount=1 but threshold=2, so no nudge
	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent success: %v", err)
	}
	if result.Message != "" {
		t.Errorf("expected no nudge when threshold not met, got: %q", result.Message)
	}
}

// ── Combined nudge test ────────────────────────────────────────────────

// TestLifecycleHook_CombinedNudge verifies that both intermediate and
// error-resolution nudges appear in a single message when both conditions are met.
func TestLifecycleHook_CombinedNudge(t *testing.T) {
	hook, _, _ := newTestHook(t, TriggerConfig{NudgeInterval: 2, ErrorResolutionThreshold: 1})

	// Make 2 non-skill calls to get itersSinceSkill=2
	for i := range 2 {
		_, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{"tool_name": "Bash"},
		})
		if err != nil {
			t.Fatalf("OnEvent %d: %v", i, err)
		}
	}

	// Trigger an error
	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolExecutionFailed,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent error: %v", err)
	}

	// Next successful call: itersSinceSkill will be 3 (> 2) and error is resolved
	// Both nudges should fire and be combined
	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent success: %v", err)
	}
	if result.Message == "" {
		t.Fatal("expected combined nudge message")
	}
	if !strings.Contains(result.Message, "tool calls since the last skill review") {
		t.Errorf("combined nudge should contain intermediate nudge, got: %q", result.Message)
	}
	if !strings.Contains(result.Message, "resolved an error") {
		t.Errorf("combined nudge should contain error-resolution nudge, got: %q", result.Message)
	}
}

// ── RunTurn integration test ────────────────────────────────────────────

// TestLifecycleHook_RunTurn_Called verifies that RunTurn() is called
// on each EventToolAfterExecute by checking that the turn counter increments.
func TestLifecycleHook_RunTurn_Called(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	_, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Bash"},
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	snap := m.Snapshot()
	if snap.TurnCount != 1 {
		t.Errorf("TurnCount = %d, want 1 (RunTurn should have been called)", snap.TurnCount)
	}
}

// TestLifecycleHook_MarkSkillUsed_SkillManage verifies that skill usage is
// tracked when SkillManage is invoked with the "name" parameter.
func TestLifecycleHook_MarkSkillUsed_SkillManage(t *testing.T) {
	t.Helper()
	reg := skills.NewRegistry()
	m := &Metrics{}
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{NudgeInterval: 100}}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	// Wire up a curator so MarkSkillUsed has somewhere to write
	curator := NewCurator(CuratorConfig{}, svc.factory, "")
	svc.SetCurator(curator)

	// Pre-seed the curator state with a skill entry (simulating it was created earlier)
	curator.MarkUsed("test-skill")
	before := curator.GetState().SkillStates["test-skill"].LastUsedAt

	hook, err := NewLifecycleHook(svc)
	if err != nil {
		t.Fatalf("NewLifecycleHook: %v", err)
	}

	// Simulate SkillManage(action: "view", name: "test-skill")
	_, err = hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "SkillManage", "action": "view", "name": "test-skill"},
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	after := curator.GetState().SkillStates["test-skill"].LastUsedAt
	if !after.After(before) {
		t.Error("MarkSkillUsed should update LastUsedAt when SkillManage event has 'name'")
	}
}

// TestLifecycleHook_MarkSkillUsed_SkillNameKey verifies backward compatibility
// with tools that use the "skill_name" parameter.
func TestLifecycleHook_MarkSkillUsed_SkillNameKey(t *testing.T) {
	t.Helper()
	reg := skills.NewRegistry()
	m := &Metrics{}
	cfg := Config{Mode: ModeAuto, AutogenDir: t.TempDir(), Trigger: TriggerConfig{NudgeInterval: 100}}
	svc, err := NewService(cfg, reg, m)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	curator := NewCurator(CuratorConfig{}, svc.factory, "")
	svc.SetCurator(curator)
	curator.MarkUsed("legacy-skill")
	before := curator.GetState().SkillStates["legacy-skill"].LastUsedAt

	hook, err := NewLifecycleHook(svc)
	if err != nil {
		t.Fatalf("NewLifecycleHook: %v", err)
	}

	// Simulate a tool that uses "skill_name" instead of "name"
	_, err = hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{"tool_name": "Skill", "skill_name": "legacy-skill"},
	})
	if err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	after := curator.GetState().SkillStates["legacy-skill"].LastUsedAt
	if !after.After(before) {
		t.Error("MarkSkillUsed should update LastUsedAt when event has 'skill_name'")
	}
}
