package autogenskills

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// The TUI (and the client SDK) never emit EventToolExecutionFailed — tool
// failures are folded into EventToolAfterExecute with an "error" entry in
// Data and success=false in tool_output. Before this fix the LifecycleHook
// only looked at the event type, so HandleError was never called in any real
// product and the error-resolution nudge was dead code. These tests pin the
// data-shape-based detection.

// TestAfterExecuteWithErrorCountsAsFailure: after-execute carrying a non-nil
// error must increment ErrorCount, not be treated as a success.
func TestAfterExecuteWithErrorCountsAsFailure(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"error":     errors.New("exit status 1"),
		},
	})
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
	if snap.ErrorResolvedCount != 0 {
		t.Errorf("ErrorResolvedCount = %d, want 0 (a failed call is not a resolution)", snap.ErrorResolvedCount)
	}
}

// TestAfterExecuteToolOutputFailureCountsAsFailure: the TUI also signals
// failure via tool_output{success: false, error: "..."} with a nil top-level
// error (v.IsError case).
func TestAfterExecuteToolOutputFailureCountsAsFailure(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100})

	if _, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"tool_output": map[string]any{
				"success": false,
				"error":   "command not found",
			},
		},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if snap := m.Snapshot(); snap.ErrorCount != 1 {
		t.Errorf("ErrorCount = %d, want 1", snap.ErrorCount)
	}
}

// TestErrorThenSuccessFiresResolutionNudge: the full error→resolved loop
// through after-execute events only (the real product event stream).
func TestErrorThenSuccessFiresResolutionNudge(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100, ErrorResolutionThreshold: 1})

	// Failing call.
	if _, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"error":     errors.New("exit status 1"),
		},
	}); err != nil {
		t.Fatal(err)
	}

	// Successful follow-up: this is the resolution.
	result, err := hook.OnEvent(context.Background(), hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"tool_output": map[string]any{
				"success": true,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := m.Snapshot()
	if snap.ErrorResolvedCount != 1 {
		t.Errorf("ErrorResolvedCount = %d, want 1", snap.ErrorResolvedCount)
	}
	if result.Action != hooks.ActionContinue || !strings.Contains(result.Message, "resolved an error") {
		t.Errorf("expected error-resolution nudge, got action=%v message=%q", result.Action, result.Message)
	}
}

// TestConsecutiveFailuresDoNotCountAsResolution: fail, fail — the second
// failure must not consume hadRecentError as a "resolution".
func TestConsecutiveFailuresDoNotCountAsResolution(t *testing.T) {
	hook, _, m := newTestHook(t, TriggerConfig{NudgeInterval: 100, ErrorResolutionThreshold: 1})

	for i := 0; i < 2; i++ {
		if _, err := hook.OnEvent(context.Background(), hooks.Event{
			Type: hooks.EventToolAfterExecute,
			Data: map[string]any{
				"tool_name": "Bash",
				"error":     errors.New("exit status 1"),
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	snap := m.Snapshot()
	if snap.ErrorCount != 2 {
		t.Errorf("ErrorCount = %d, want 2", snap.ErrorCount)
	}
	if snap.ErrorResolvedCount != 0 {
		t.Errorf("ErrorResolvedCount = %d, want 0", snap.ErrorResolvedCount)
	}
}
