package mode

import (
	"strings"
	"testing"
)

// TestPlanModeExposesEveryToolAndGatesAtCallTime pins the inverted plan-mode
// contract: capability is decided by the phase-integrity hook from real call
// arguments, not by a tool-name allowlist here.
//
// The previous contract named the tools planning could use. That list silently
// rotted when the tool surface was consolidated — it kept naming grep/glob/
// semantic_grep/file_read after all four were deleted, never named apply_patch
// after it became the only editor, and blocked Bash, which by then was the only
// way to read a text file. Plan mode was left advertising research it could not
// perform (#276, #277, #281, #293, #294). Asserting "*" here is what keeps a
// future tool rename from re-creating that hole.
func TestPlanModeExposesEveryToolAndGatesAtCallTime(t *testing.T) {
	// The tools an agent actually needs to plan must all be reachable,
	// including the ones the old allowlist refused.
	for _, tool := range []string{
		"Write", "Edit", "apply_patch", "Bash",
		"TaskManage", "TaskGet", "TaskList", "TaskCreate", "TaskUpdate", "TodoWrite",
		"Subagent", "HistorySearch", "HistoryGet", "annoyed",
		"ask_user_question", "exit_plan_mode",
	} {
		if !PlanMode.IsToolAllowed(tool) {
			t.Errorf("PlanMode.IsToolAllowed(%q) = false, want true", tool)
		}
	}

	// Legacy string-replacement editors remain off: they carry no path the
	// hook can check against the authorized plan artifact.
	for _, tool := range []string{"EditLegacy", "NotebookEdit"} {
		if PlanMode.IsToolAllowed(tool) {
			t.Errorf("PlanMode.IsToolAllowed(%q) = true, want false", tool)
		}
	}
}

// TestPlanModeInstructionMatchesEnforcedPolicy guards against the system
// prompt drifting away from what the hook actually does — the failure mode
// that produced the "advertises research it cannot perform" cluster.
func TestPlanModeInstructionMatchesEnforcedPolicy(t *testing.T) {
	instr := PlanMode.SystemInstruction

	if !strings.Contains(instr, "exact session plan artifact") {
		t.Error("plan mode instruction must explain the exact plan artifact exception")
	}

	// It must not *direct the agent to call* tools that no longer exist.
	// Naming them to say they are gone is fine and in fact desirable, so the
	// assertion targets invocation shapes and imperatives, not bare mentions.
	for _, gone := range []string{
		"semantic_grep(",
		"Use semantic_grep",
		"use semantic_grep",
		"file_read(",
		"Use grep for text content search",
	} {
		if strings.Contains(instr, gone) {
			t.Errorf("plan mode instruction still directs the agent to a removed tool: %q", gone)
		}
	}

	// It must not claim bash is unavailable, because it is available.
	if strings.Contains(instr, "Bash tool is not available") {
		t.Error("plan mode instruction claims Bash is unavailable, but plan mode now allows read-only and analytical shell commands")
	}

	// It must not claim TaskManage is inspection-only, because it is not.
	if strings.Contains(instr, "get/list") {
		t.Error("plan mode instruction still describes TaskManage as inspection-only")
	}
}
