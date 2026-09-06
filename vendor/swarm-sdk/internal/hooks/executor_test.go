package hooks

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestExecutor_BlockWithError_ActuallyBlocks is the regression test for the
// bug PLAN.md tracked as "(Block, err) does not block - error suppresses
// Action": internal/hooks/executor.go used to discard the HookResult
// entirely whenever a hook's OnEvent returned a non-nil error, so a hook
// that legitimately wanted to block AND explain why via a Go error (e.g.
// "block this, validation failed: <err>") silently downgraded to "log the
// error and continue to the next hook" — never actually blocking.
//
// Found by internal/hooks/observational_test.go's positive control
// ("ActionBlock_with_error" shape); this test exercises the same defect
// directly against Manager.EmitWithResult rather than only as a side
// assertion inside the observational-view spine test.
func TestExecutor_BlockWithError_ActuallyBlocks(t *testing.T) {
	mgr := NewManager(ManagerConfig{})
	hook := &recordingHook{
		name:     "blocker-with-error",
		priority: 50,
		verdict:  Block("validation failed"),
		err:      errors.New("underlying validation error"),
	}
	if err := mgr.Register(hook, ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := mgr.EmitWithResult(context.Background(), toolEvent("bash", map[string]any{"command": "rm -rf /"}))

	if err == nil {
		t.Fatalf("EmitWithResult returned no error for a hook that returned (Block, err); the event was not actually blocked")
	}
	if result == nil || !result.Blocked {
		t.Fatalf("result.Blocked = %v, want true — a hook returning (Block, err) must block exactly like a hook returning (Block, nil)", result)
	}
	if result.BlockedBy != hook.Name() {
		t.Errorf("BlockedBy = %q, want %q", result.BlockedBy, hook.Name())
	}
	if result.BlockReason == "" {
		t.Errorf("BlockReason is empty; the hook's Block() message should have survived alongside the error")
	}
	if got := hook.calls.Load(); got != 1 {
		t.Fatalf("hook should have been invoked exactly once, got %d", got)
	}
}

// TestExecutor_ErrorWithoutBlock_SurfacesButDoesNotBlock is the negative
// control for the test above: an error alone (Action != ActionBlock) must
// still not block, and must still be visible in HookOutputs — the fix must
// only change behavior for the (Block, err) combination, not for every
// error.
func TestExecutor_ErrorWithoutBlock_SurfacesButDoesNotBlock(t *testing.T) {
	mgr := NewManager(ManagerConfig{})
	hook := &recordingHook{
		name:     "erroring-continue",
		priority: 50,
		verdict:  Continue(),
		err:      errors.New("hook says no, but does not block"),
	}
	if err := mgr.Register(hook, ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := mgr.EmitWithResult(context.Background(), toolEvent("bash", map[string]any{"command": "echo hello"}))

	if err != nil {
		t.Fatalf("EmitWithResult returned an error for a non-blocking hook error: %v", err)
	}
	if result == nil || result.Blocked {
		t.Fatalf("result.Blocked = %v, want false — Continue()+err must not block", result)
	}
	if result == nil || len(result.HookOutputs) == 0 || result.HookOutputs[0].Error == "" {
		t.Fatalf("hook's error was not surfaced in HookOutputs: %#v", result)
	}
}

// TestExecutor_Timeout_NeverTreatedAsBlock guards the branch in
// executeWithRecovery that never got a real HookResult from the hook at all
// (deadline exceeded): it must keep returning a zero-value HookResult
// (Action == ActionContinue, the zero value — NOT ActionBlock) so the new
// "honor ActionBlock even when err is set" branch in Execute() (added by
// this same fix) can never accidentally treat a plain timeout as a hook
// asking to block. A timeout surfaces via HookOutputs[i].Error, exactly as
// before this fix — EmitWithResult's own error return is reserved for an
// actual block (see manager.go's "return result, fmt.Errorf(\"event
// blocked...\")"), so a timeout alone must leave both err and Blocked at
// their non-blocking zero values.
func TestExecutor_Timeout_NeverTreatedAsBlock(t *testing.T) {
	mgr := NewManager(ManagerConfig{MaxExecutionTime: 1 * time.Millisecond})
	hook := &recordingHook{
		name:     "slow-hook",
		priority: 50,
		verdict:  Continue(),
		delay:    50 * time.Millisecond, // comfortably over the 1ms timeout above
	}
	if err := mgr.Register(hook, ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	result, err := mgr.EmitWithResult(context.Background(), toolEvent("bash", map[string]any{"command": "sleep"}))
	if err != nil {
		t.Fatalf("EmitWithResult returned an error for a plain timeout (not a block): %v", err)
	}
	if result == nil {
		t.Fatalf("result is nil")
	}
	if result.Blocked {
		t.Fatalf("result.Blocked = true — a timeout must never be reported as a hook-initiated block")
	}
	if len(result.HookOutputs) != 1 || result.HookOutputs[0].Error == "" {
		t.Fatalf("expected the timeout to surface in HookOutputs[0].Error, got %#v", result.HookOutputs)
	}
}
