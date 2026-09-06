package builtin

import (
	"context"
	"fmt"
	"slices"
)

// SimulationHook provides a built-in hook that instructs agents to simulate
// their planned actions before executing them. Based on research from
// "From Control to Foresight: Simulation as a New Paradigm for Human–Agent Collaboration" (arXiv:2603.11677)
//
// This hook fires after plan mode exits (PostToolUse for plan-related tools),
// instructing the agent to:
// 1. CHOREOGRAPH their actions step-by-step (like a dance rehearsal)
// 2. Walk through the sequence in order, understanding dependencies
// 3. Identify where the "dance" could fail - missed steps, wrong timing
// 4. Do this IN THE OPEN - show your reasoning, don't hide it
//
// This transforms execution from reactive to proactive, informed by foresight.
type SimulationHook struct{}

// SimulationReminderMessage returns the instruction for simulation before execution.
// Designed to encourage step-by-step choreography of actions.
func SimulationReminderMessage() string {
	return fmt.Sprintf(`[PRE-EXECUTION SIMULATION - CHOREOGRAPH YOUR DANCE]

═══════════════════════════════════════════════════════════════════════════════
           BEFORE YOU PERFORM, REHEARSE THE DANCE IN YOUR MIND
═══════════════════════════════════════════════════════════════════════════════

Before executing your plan, CHOREOGRAPH your actions step-by-step.
Like a dress rehearsal before the play - walk through the sequence to catch issues.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 1: CHOREOGRAPH THE SEQUENCE
─────────────────────────────────────────────────────────────────────────────────

Walk through your actions IN ORDER, like steps in a dance:

   "First, I'll read file X to understand the current structure..."
   "Then, I'll modify function Y because it needs to handle Z..."
   "After that, I'll update the tests in file A to cover the new behavior..."
   "Finally, I'll run the build to verify everything compiles..."

Show the DEPENDENCIES between steps:
   "Step 2 depends on what I find in Step 1 - if X has pattern P, I do Q"
   "Step 3 can only happen after Step 2 succeeds"

This is the CHOREOGRAPHY - the planned sequence of movements.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 2: SPOT THE BREAKING POINTS
─────────────────────────────────────────────────────────────────────────────────

Where could your dance fall apart? Look for:

   • Steps that depend on assumptions: "I assume X exists" → verify first
   • Steps that could fail: "If Y doesn't have Z, this breaks"
   • Steps that affect others: "Changing A might break B"
   • Missing steps: "Did I forget to test the edge case?"

List EVERY potential breaking point. Hidden problems become bugs.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 3: UNDERSTAND THE RIPPLES
─────────────────────────────────────────────────────────────────────────────────

For each change, trace the IMPACT:

   "If I modify function X, what calls it?"
   "If I change file Y, what imports it?"
   "If I update struct Z, what uses it?"

Changes don't happen in isolation - every step creates ripples.

─────────────────────────────────────────────────────────────────────────────────
                    STEP 4: REHEARSE OUT LOUD
─────────────────────────────────────────────────────────────────────────────────

SAY your choreography out loud - don't hide it:

   "Here's my sequence:
    1. [action] → because [reason] → expect [outcome]
    2. [action] → because [reason] → expect [outcome]
    3. [action] → because [reason] → expect [outcome]"

This is your DRESS REHEARSAL. Do it in the open so you can spot mistakes.

═══════════════════════════════════════════════════════════════════════════════
                              WHY THIS MATTERS
═══════════════════════════════════════════════════════════════════════════════

You wouldn't perform a dance without rehearsing.
You wouldn't stage a play without a dress rehearsal.
Don't execute code changes without choreographing first.

                              EVERY SKIPPED REHEARSAL IS A BUG WAITING ON STAGE

═══════════════════════════════════════════════════════════════════════════════

After completing ALL FOUR STEPS above, you may execute.
Skip ANY step = you are NOT ready. Go back and rehearse.`)
}

// CheckShouldSimulate returns true if the agent should simulate after plan exit.
// Currently always returns true for plan-exit events - the hook system calls this
// after plan-related tools complete.
func CheckShouldSimulate(ctx context.Context) (string, bool) {
	return SimulationReminderMessage(), true
}

// Execute runs the simulation check for use in lifecycle hooks.
func (h *SimulationHook) Execute(ctx context.Context) (string, error) {
	msg, _ := CheckShouldSimulate(ctx)
	return msg, nil
}

// ExecuteAsHook is the standard lifecycle hook execution interface.
func (h *SimulationHook) ExecuteAsHook(ctx context.Context, hookCtx *HookContext) (*HookDecision, error) {
	decision := &HookDecision{
		Decision: "allow",
	}

	msg, shouldSimulate := CheckShouldSimulate(ctx)
	if shouldSimulate {
		decision.SystemMessage = msg
	}

	return decision, nil
}

// PlanExitDetected checks if the tool being used is plan-related
// and should trigger the simulation hook.
func PlanExitDetected(toolName string) bool {
	planTools := []string{
		"plan",
		"Plan",
		"enter_plan_mode",
		"exit_plan_mode",
		"create_plan",
		"TodoWrite",
		"task_create",
	}

	return slices.Contains(planTools, toolName)
}
