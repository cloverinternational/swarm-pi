package builtin

import (
	"context"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// VerificationProtocolHook implements the 5-layer verification protocol
// that runs before an agent declares work complete.
//
// This hook intercepts agent stop events and prompts the agent to verify:
//  1. Assumption Audit - Did I assume anything I didn't verify?
//  2. Output Verification - Did I verify the output, or just create it?
//  3. Hallucination Check - Did I generate anything I cannot prove?
//  4. Completeness Check - Is this actually done, or just "done enough"?
//  5. Linting - Is the output internally consistent and clean?

const (
	// VerificationProtocolPriority runs late to catch stop events
	VerificationProtocolPriority = 80
)

// VerificationProtocolHook enforces the 5-layer verification protocol
type VerificationProtocolHook struct{}

// NewVerificationProtocolHook creates a new verification protocol hook
func NewVerificationProtocolHook() *VerificationProtocolHook {
	return &VerificationProtocolHook{}
}

// Name returns the hook name
func (h *VerificationProtocolHook) Name() string {
	return "verification-protocol-hook"
}

// Priority returns the hook priority
func (h *VerificationProtocolHook) Priority() int {
	return VerificationProtocolPriority
}

// Filter returns true for agent stop events
func (h *VerificationProtocolHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventAgentStopped
}

// OnEvent runs the verification protocol before agent stops
func (h *VerificationProtocolHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Get the TodoManager
	tm := ii.GetTodoManager()
	if tm == nil {
		return hooks.Continue(), nil
	}

	// Check if there are any completed tasks
	completed := tm.ByStatus(ii.TodoStatusCompleted)
	if len(completed) == 0 {
		return hooks.Continue(), nil
	}

	// Check if there are in-progress tasks (shouldn't stop with active work)
	inProgress := tm.ByStatus(ii.TodoStatusInProgress)
	if len(inProgress) > 0 {
		return hooks.ContinueWithMessage(h.incompleteWorkMessage(inProgress)), nil
	}

	// Check for acting tasks without corresponding verifying/documenting tasks
	acting := tm.ByCategory(ii.TaskCategoryActing)
	verifying := tm.ByCategory(ii.TaskCategoryVerifying)
	documenting := tm.ByCategory(ii.TaskCategoryDocumenting)

	// If acting tasks were completed but no verifying/documenting, prompt
	var completedActing []ii.TodoItem
	for _, task := range acting {
		if task.Status == ii.TodoStatusCompleted {
			completedActing = append(completedActing, task)
		}
	}

	if len(completedActing) > 0 && len(verifying) == 0 && len(documenting) == 0 {
		return hooks.ContinueWithMessage(h.verificationProtocolMessage()), nil
	}

	return hooks.Continue(), nil
}

// incompleteWorkMessage warns about in-progress tasks
func (h *VerificationProtocolHook) incompleteWorkMessage(tasks []ii.TodoItem) string {
	var msg strings.Builder
	msg.WriteString(`[VERIFICATION PROTOCOL - INCOMPLETE WORK DETECTED]

You are about to stop, but you have ` + string(rune('0'+len(tasks))) + ` task(s) still in progress:

┌─────────────────────────────────────────────────────────────────────────────┐
│  IN-PROGRESS TASKS:                                                         │
├─────────────────────────────────────────────────────────────────────────────┤
`)

	for _, task := range tasks {
		msg.WriteString("│  • " + task.Content + "\n")
	}
	msg.WriteString(`└─────────────────────────────────────────────────────────────────────────────┘

Before stopping, either:
  1. Complete these tasks and mark them as completed
  2. Mark them as pending if blocked
  3. Document why they cannot be completed

Do not stop with incomplete work unless absolutely necessary.`)
	return msg.String()
}

// verificationProtocolMessage returns the full 5-layer verification protocol
func (h *VerificationProtocolHook) verificationProtocolMessage() string {
	return `[VERIFICATION PROTOCOL - BEFORE YOU DECLARE DONE]

You are about to declare this task complete. Before you do, run all 5 layers
of the VERIFICATION PROTOCOL.

═══════════════════════════════════════════════════════════════════════════════
                          LAYER 1 — ASSUMPTION AUDIT
═══════════════════════════════════════════════════════════════════════════════

"Did I assume anything I didn't verify?"

• List every value, fact, or behavior you treated as true without confirming it
• For each assumption: go verify it right now (run the code, read the file, check the source)
• Common traps:
  - "This should import fine" → did you actually run the import?
  - "The API returns X" → did you check the actual response?
  - "The figure was $4.2B" → do you have a citation with date and source?

═══════════════════════════════════════════════════════════════════════════════
                     LAYER 2 — OUTPUT VERIFICATION (not just creation)
═══════════════════════════════════════════════════════════════════════════════

"Did I verify the output, or just that I produced it?"

┌──────────┬───────────────────────────────────────────────────────────────────┐
│  Domain  │                      What to actually verify                      │
├──────────┼───────────────────────────────────────────────────────────────────┤
│  Code    │ Tests pass (not just imports). Linter clean. No TODO/FIXME left.  │
│          │ Edge cases run.                                                   │
├──────────┼───────────────────────────────────────────────────────────────────┤
│  Finance │ Every number has a source + date. Calculations reproduced        │
│          │ independently. No projections presented as facts.                │
├──────────┼───────────────────────────────────────────────────────────────────┤
│  Legal   │ Every citation is real (full case name, jurisdiction, year).      │
│          │ Qualifications present. No absolute statements.                   │
├──────────┼───────────────────────────────────────────────────────────────────┤
│ Research │ Every factual claim has a cited source. Conflicting evidence     │
│          │ acknowledged. Confidence level stated.                           │
└──────────┴───────────────────────────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════════════════
                        LAYER 3 — HALLUCINATION CHECK
═══════════════════════════════════════════════════════════════════════════════

"Did I generate anything I cannot prove?"

• Re-read your own output. Flag any sentence that contains a specific fact,
  figure, name, date, or behavior
• For each flagged item: can you point to the exact source/evidence right now?
• If the answer is "I believe" or "typically" or "usually" — that is not
  verification, that is assumption
• Replace unverified claims with either: confirmed citation, or explicit
  uncertainty marker ([unverified], [needs source])

═══════════════════════════════════════════════════════════════════════════════
                        LAYER 4 — COMPLETENESS CHECK
═══════════════════════════════════════════════════════════════════════════════

"Is this actually done, or is it 'done enough'?"

• Re-read the original request word for word
• List every requirement stated or implied
• For each requirement: mark it as fully done, partially done, or not done
• Partial = not done. Return to ACT and finish it
• Common incompleteness patterns:
  - Tests created but not run
  - Feature built but not integrated
  - Research written but sources not linked
  - Numbers cited but methodology not explained

═══════════════════════════════════════════════════════════════════════════════
                  LAYER 5 — LINTING (universal, not just code)
═══════════════════════════════════════════════════════════════════════════════

"Is the output internally consistent and clean?"

┌──────────┬───────────────────────────────────────────────────────────────────┐
│  Domain  │                        Lint equivalent                            │
├──────────┼───────────────────────────────────────────────────────────────────┤
│  Code    │ Run linter, type checker, test suite. Zero warnings = pass.       │
├──────────┼───────────────────────────────────────────────────────────────────┤
│  Finance │ Units consistent throughout (all $M or all $B, not mixed).        │
│          │ Dates in same format. No rounding inconsistencies.               │
├──────────┼───────────────────────────────────────────────────────────────────┤
│  Legal   │ Citation format consistent. All defined terms used consistently. │
│          │ No contradictions between sections.                               │
├──────────┼───────────────────────────────────────────────────────────────────┤
│ Research │ Claim in section 2 doesn't contradict claim in section 5.        │
│          │ All referenced figures exist in the document.                     │
└──────────┴───────────────────────────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════════════════
                   WHY EACH LAYER CATCHES SPECIFIC FAILURES
═══════════════════════════════════════════════════════════════════════════════

┌──────────────────────────────┬───────────────────────────────────────────────┐
│        Failure               │                 Layer that catches it         │
├──────────────────────────────┼───────────────────────────────────────────────┤
│ Declared done without tests  │ Layer 2 — "tests pass, not just created"      │
├──────────────────────────────┼───────────────────────────────────────────────┤
│ Assumed imports would work   │ Layer 1 — verify don't assume                 │
├──────────────────────────────┼───────────────────────────────────────────────┤
│ Needed user prompt to verify │ Layer 4 — self-check completeness vs request  │
├──────────────────────────────┼───────────────────────────────────────────────┤
│ No test run until asked      │ Layer 2 + Layer 5                             │
└──────────────────────────────┴───────────────────────────────────────────────┘

═══════════════════════════════════════════════════════════════════════════════

Only after passing all 5 layers may you output a completion summary.
If any layer fails, FIX IT FIRST.

THE KEY PRINCIPLE: Creation ≠ Verification.
The agent made something — that's 50% of the job.
The other 50% is proving it works.`
}
