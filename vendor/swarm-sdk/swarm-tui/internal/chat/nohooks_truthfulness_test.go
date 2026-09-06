package chat

import (
	"testing"

	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
)

// TestNoHooks_NilHooksManagerIsSafe verifies the nil-safety contract that the
// --no-hooks / SDKIntegrationOptions.NoHooks path depends on.
//
// Bug 2 (truthfulness audit): --no-hooks was historically checked AFTER the SDK
// already built + attached the builtin hooks manager, so task-enforcement,
// protected-branch, autogenskills, plan-mode and task-nudge hooks still fired.
// The fix makes NewSDKIntegrationWithOptions skip creating/attaching the hooks
// manager entirely when NoHooks is set, leaving sdk.hooksManager == nil.
//
// This test asserts the accessors + goal-persistence path all tolerate a nil
// hooks manager (i.e. the NoHooks construction cannot panic downstream).
func TestNoHooks_NilHooksManagerIsSafe(t *testing.T) {
	logger := tuiobs.NewTUILogger()
	sdk := &SDKIntegration{
		providerName: "openai",
		currentModel: "gpt-5.1",
		logger:       logger,
		tracer:       tuiobs.NewTUITracer(logger),
		// hooksManager intentionally left nil (simulates NoHooks: true)
	}

	// GetHooksManager must return nil, not panic.
	if hm := sdk.GetHooksManager(); hm != nil {
		t.Fatalf("expected nil hooks manager under NoHooks, got %#v", hm)
	}

	// SetupGoalPersistence must be a no-op (guarded) when hooks are disabled.
	// Both the empty-convID and non-empty-convID branches must not panic.
	sdk.SetupGoalPersistence("")
	sdk.SetupGoalPersistence("some-conversation-id")

	// SetHooksManager followed by GetHooksManager must round-trip, proving the
	// enabled path is still wired correctly (regression guard for the field).
	mgr := NewHooksManager(sdk.logger, sdk.tracer, "")
	sdk.SetHooksManager(mgr)
	if got := sdk.GetHooksManager(); got != mgr {
		t.Fatalf("SetHooksManager/GetHooksManager did not round-trip: got %#v want %#v", got, mgr)
	}
}
