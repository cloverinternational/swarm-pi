package builtin

import (
	"strings"
	"testing"
	"time"
)

// TestPlanModeFirstTool_HydratedFirstToolUsed asserts that when a hydrated
// snapshot says the first post-plan-mode tool already ran, the breakdown
// prompt is NOT re-injected on subsequent tool calls. This is the regression
// guard for the reload-spam bug observed when the TUI reattached to a
// conversation that had already entered plan mode.
func TestPlanModeFirstTool_HydratedFirstToolUsed(t *testing.T) {
	defer ResetPlanModeForTest()

	HydratePlanModeSnapshot(PlanModeSnapshot{
		InPlanMode:    true,
		FirstToolUsed: true,
		EverUsed:      true,
		LastEntryAt:   time.Now().Add(-5 * time.Minute),
	})

	msg, inject := CheckPlanModeFirstTool("Read")
	if inject || msg != "" {
		t.Fatalf("expected no injection on hydrated firstToolUsed=true, got inject=%v msg_len=%d",
			inject, len(msg))
	}
}

// TestPlanModeFirstTool_HydratedInPlanModeFirstUseInjectsOnce asserts that a
// hydrated snapshot with InPlanMode=true but FirstToolUsed=false (entered
// plan mode in a prior session, never ran a tool, then reloaded) produces
// exactly ONE injection on the next tool call.
func TestPlanModeFirstTool_HydratedInPlanModeFirstUseInjectsOnce(t *testing.T) {
	defer ResetPlanModeForTest()

	HydratePlanModeSnapshot(PlanModeSnapshot{
		InPlanMode:    true,
		FirstToolUsed: false,
		EverUsed:      true,
		LastEntryAt:   time.Now().Add(-5 * time.Minute),
	})

	msg1, inject1 := CheckPlanModeFirstTool("Read")
	if !inject1 || msg1 == "" {
		t.Fatalf("expected one injection on first hydrated tool, got inject=%v msg_len=%d",
			inject1, len(msg1))
	}

	msg2, inject2 := CheckPlanModeFirstTool("Read")
	if inject2 || msg2 != "" {
		t.Fatalf("expected no injection on second tool after first injection, got inject=%v msg_len=%d",
			inject2, len(msg2))
	}
}

// TestPlanModeFirstTool_PersisterFiresOnTransition asserts that
// PlanModeEntered/Exited invoke the registered persister with the new
// snapshot so the host can write it to ConversationMetadata.Custom.
func TestPlanModeFirstTool_PersisterFiresOnTransition(t *testing.T) {
	defer ResetPlanModeForTest()

	type capture struct {
		snap PlanModeSnapshot
	}
	calls := make(chan capture, 8)
	SetPlanModePersister(func(s PlanModeSnapshot) {
		calls <- capture{snap: s}
	})

	PlanModeEntered()
	select {
	case got := <-calls:
		if !got.snap.InPlanMode || !got.snap.EverUsed {
			t.Fatalf("expected InPlanMode=true,EverUsed=true after PlanModeEntered, got %+v", got.snap)
		}
		if got.snap.LastEntryAt.IsZero() {
			t.Fatalf("expected non-zero LastEntryAt after PlanModeEntered")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("persister was not called for PlanModeEntered within 2s")
	}

	PlanModeExited()
	select {
	case got := <-calls:
		if got.snap.InPlanMode {
			t.Fatalf("expected InPlanMode=false after PlanModeExited, got %+v", got.snap)
		}
		if !got.snap.EverUsed {
			t.Fatalf("EverUsed should remain true after exit, got %+v", got.snap)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("persister was not called for PlanModeExited within 2s")
	}
}

// TestPlanModeFirstTool_DumpRoundTrip asserts that a snapshot taken via
// DumpPlanModeSnapshot and fed back through HydratePlanModeSnapshot
// reproduces the same in-memory state.
func TestPlanModeFirstTool_DumpRoundTrip(t *testing.T) {
	defer ResetPlanModeForTest()

	PlanModeEntered()
	// Simulate the first tool running so firstToolUsed flips.
	if _, ok := CheckPlanModeFirstTool("Read"); !ok {
		t.Fatalf("expected first tool to inject before dump")
	}

	snap := DumpPlanModeSnapshot()
	if !snap.InPlanMode || !snap.FirstToolUsed || !snap.EverUsed {
		t.Fatalf("unexpected dumped snapshot: %+v", snap)
	}

	// Reset and re-hydrate.
	ResetPlanModeForTest()
	HydratePlanModeSnapshot(snap)

	got := DumpPlanModeSnapshot()
	if got.InPlanMode != snap.InPlanMode ||
		got.FirstToolUsed != snap.FirstToolUsed ||
		got.EverUsed != snap.EverUsed {
		t.Fatalf("round-trip mismatch: dumped %+v, hydrated %+v", snap, got)
	}
}

// TestPlanModeFirstTool_PlanIDMintedOnEnter asserts that PlanModeEntered
// mints a fresh PlanID and appends it to PlanIDHistory.
func TestPlanModeFirstTool_PlanIDMintedOnEnter(t *testing.T) {
	defer ResetPlanModeForTest()

	if got := CurrentPlanID(); got != "" {
		t.Fatalf("expected empty PlanID before entering plan mode, got %q", got)
	}

	PlanModeEntered()
	id1 := CurrentPlanID()
	if id1 == "" {
		t.Fatalf("expected non-empty PlanID after PlanModeEntered")
	}
	hist1 := PlanIDHistorySnapshot()
	if len(hist1) != 1 || hist1[0] != id1 {
		t.Fatalf("expected PlanIDHistory == [%q], got %v", id1, hist1)
	}
}

// TestPlanModeFirstTool_ExitClearsCurrentButPreservesHistory asserts that
// PlanModeExited clears the live PlanID (so subsequent tasks are not tagged
// with a stale plan) but leaves the entry in PlanIDHistory for archival.
func TestPlanModeFirstTool_ExitClearsCurrentButPreservesHistory(t *testing.T) {
	defer ResetPlanModeForTest()

	PlanModeEntered()
	id1 := CurrentPlanID()
	PlanModeExited()

	if got := CurrentPlanID(); got != "" {
		t.Fatalf("expected CurrentPlanID() == \"\" after exit, got %q", got)
	}
	hist := PlanIDHistorySnapshot()
	if len(hist) != 1 || hist[0] != id1 {
		t.Fatalf("PlanIDHistory should retain %q after exit, got %v", id1, hist)
	}
}

// TestPlanModeFirstTool_MultipleEntriesAppendHistory asserts that entering
// plan mode twice in one session yields two distinct ids in PlanIDHistory
// (use case: user planned, exited, re-planned later — both plans must be
// recoverable from PlanArchive after compaction).
func TestPlanModeFirstTool_MultipleEntriesAppendHistory(t *testing.T) {
	defer ResetPlanModeForTest()

	PlanModeEntered()
	id1 := CurrentPlanID()
	PlanModeExited()

	PlanModeEntered()
	id2 := CurrentPlanID()
	PlanModeExited()

	if id1 == id2 {
		t.Fatalf("two PlanModeEntered calls minted the same id: %q", id1)
	}
	hist := PlanIDHistorySnapshot()
	if len(hist) != 2 || hist[0] != id1 || hist[1] != id2 {
		t.Fatalf("expected history [%q, %q], got %v", id1, id2, hist)
	}
}

// TestPlanModeFirstTool_HydrateRestoresPlanIDAndHistory asserts that
// PlanID and PlanIDHistory survive the persist → reload cycle. This is
// the regression guard for the cross-process resume scenario.
func TestPlanModeFirstTool_HydrateRestoresPlanIDAndHistory(t *testing.T) {
	defer ResetPlanModeForTest()

	PlanModeEntered()
	id1 := CurrentPlanID()
	PlanModeExited()
	PlanModeEntered()
	id2 := CurrentPlanID()
	snap := DumpPlanModeSnapshot()

	ResetPlanModeForTest()
	HydratePlanModeSnapshot(snap)

	if got := CurrentPlanID(); got != id2 {
		t.Fatalf("expected hydrated CurrentPlanID == %q, got %q", id2, got)
	}
	hist := PlanIDHistorySnapshot()
	if len(hist) != 2 || hist[0] != id1 || hist[1] != id2 {
		t.Fatalf("expected hydrated history [%q, %q], got %v", id1, id2, hist)
	}
}

func TestExitPlanModeInvocationWaitsForApprovalBeforeExiting(t *testing.T) {
	defer ResetPlanModeForTest()
	PlanModeEntered()

	if _, injected := CheckPlanModeFirstTool("exit_plan_mode"); injected {
		t.Fatal("exit_plan_mode should not receive the first-tool prompt")
	}
	if !IsInPlanMode() {
		t.Fatal("exit_plan_mode invocation exited before approval")
	}

	PlanModeExited()
	if IsInPlanMode() {
		t.Fatal("approved plan did not exit after PlanModeExited")
	}
}

func TestProblemBreakdownPromptDescribesCeremonyNotToolRestrictions(t *testing.T) {
	prompt := ProblemBreakdownPrompt()
	if strings.Contains(prompt, "ALLOWED NOW") || strings.Contains(prompt, "BLOCKED UNTIL") {
		t.Fatalf("problem breakdown prompt still describes plan-only tool restrictions: %q", prompt)
	}
	if !strings.Contains(prompt, "approval ceremony") || !strings.Contains(prompt, "exactly as they do outside plan mode") {
		t.Fatalf("problem breakdown prompt missing ceremony semantics: %q", prompt)
	}
}
