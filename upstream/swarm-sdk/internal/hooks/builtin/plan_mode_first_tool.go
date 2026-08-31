package builtin

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tasks"
)

// PlanModeFirstToolHook injects a structured problem breakdown prompt on the
// first tool execution after entering plan mode.
//
// This hook teaches agents to decompose problems at the lowest logical level -
// like a CS intro course: what you need, what you expect, how logic works.
//
// Usage: Call CheckPlanModeFirstTool directly in EmitToolBeforeExecute or
// EmitToolAfterExecute, similar to how CheckAndNudge and CheckShouldSimulate work.
//
// Flow:
//  1. Track when enter_plan_mode is called (via PlanModeEntered)
//  2. On first tool after plan mode, inject the breakdown prompt
//  3. Reset when exit_plan_mode is called (via PlanModeExited)
//  4. Subsequent tools in the same plan session are not prompted

const (
	// PlanModeFirstToolPriority runs just before task enforcement (95)
	PlanModeFirstToolPriority = 96
)

// planModeState tracks whether we're in plan mode and if the first tool has been used.
// Global state is acceptable here since plan mode is session-scoped.
// Defaults to OFF — the session start hook or an explicit enter_plan_mode call
// enables it. This avoids injecting an unexpected prompt on the first tool call
// in sessions that never enter plan mode.
var planModeState = struct {
	inPlanMode          bool
	firstToolUsed       bool
	everUsed            bool      // true once enter_plan_mode has been called in this session
	lastEntryAt         time.Time // wall-clock time of the most recent enter_plan_mode call
	planID              string    // current plan-id (minted at PlanModeEntered, cleared at PlanModeExited)
	planIDHistory       []string  // every plan-id minted in this session, append-only
	interactionOccurred bool      // true once ask_user_question is called while in plan mode
	persister           func(PlanModeSnapshot)
	mu                  sync.Mutex
}{
	inPlanMode: false,
}

// PlanModeSnapshot is the serializable form of plan-mode state. It is persisted
// in ConversationMetadata.Custom["plan_mode"] so that reattaching to a session
// after a TUI restart does not retrigger the breakdown prompt.
//
// PlanID and PlanIDHistory are part of the auditable session-tree feature
// (see swarm-sdk/tasks): every Task created during plan mode is tagged with
// the current PlanID, and every plan ever entered in this conversation
// remains in PlanIDHistory so the compaction service can populate
// CompactionState.PlanArchive.
type PlanModeSnapshot struct {
	InPlanMode          bool      `json:"in_plan_mode"`
	FirstToolUsed       bool      `json:"first_tool_used"`
	EverUsed            bool      `json:"ever_used"`
	LastEntryAt         time.Time `json:"last_entry_at"`
	PlanID              string    `json:"plan_id,omitempty"`
	PlanIDHistory       []string  `json:"plan_id_history,omitempty"`
	InteractionOccurred bool      `json:"interaction_occurred"`
}

// PlanModeSnapshotKey is the canonical key under ConversationMetadata.Custom
// where the snapshot is stored.
const PlanModeSnapshotKey = "plan_mode"

// DumpPlanModeSnapshot returns a copy of the current plan-mode state.
func DumpPlanModeSnapshot() PlanModeSnapshot {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	return PlanModeSnapshot{
		InPlanMode:          planModeState.inPlanMode,
		FirstToolUsed:       planModeState.firstToolUsed,
		EverUsed:            planModeState.everUsed,
		LastEntryAt:         planModeState.lastEntryAt,
		PlanID:              planModeState.planID,
		PlanIDHistory:       append([]string(nil), planModeState.planIDHistory...),
		InteractionOccurred: planModeState.interactionOccurred,
	}
}

// HydratePlanModeSnapshot restores plan-mode state from a previously persisted
// snapshot. Call this on agent boot, before any tool runs, when reattaching to
// a saved conversation. A zero-value snapshot is a no-op.
func HydratePlanModeSnapshot(s PlanModeSnapshot) {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	planModeState.inPlanMode = s.InPlanMode
	planModeState.firstToolUsed = s.FirstToolUsed
	planModeState.everUsed = s.EverUsed
	planModeState.lastEntryAt = s.LastEntryAt
	planModeState.planID = s.PlanID
	planModeState.planIDHistory = append([]string(nil), s.PlanIDHistory...)
	planModeState.interactionOccurred = s.InteractionOccurred
}

// SetPlanModePersister registers a callback invoked whenever plan-mode state
// transitions (PlanModeEntered / PlanModeExited). The host (TUI / SDK agent)
// is expected to write the snapshot to ConversationMetadata.Custom and trigger
// a conversation save. Pass nil to clear.
func SetPlanModePersister(fn func(PlanModeSnapshot)) {
	planModeState.mu.Lock()
	planModeState.persister = fn
	planModeState.mu.Unlock()
}

// notifyPlanModePersisterLocked must be called with planModeState.mu held; it
// snapshots the state and dispatches the persister outside the lock to avoid
// reentrancy.
func notifyPlanModePersisterLocked() {
	persister := planModeState.persister
	snap := PlanModeSnapshot{
		InPlanMode:          planModeState.inPlanMode,
		FirstToolUsed:       planModeState.firstToolUsed,
		EverUsed:            planModeState.everUsed,
		LastEntryAt:         planModeState.lastEntryAt,
		PlanID:              planModeState.planID,
		PlanIDHistory:       append([]string(nil), planModeState.planIDHistory...),
		InteractionOccurred: planModeState.interactionOccurred,
	}
	if persister == nil {
		return
	}
	// Run async so the hook fast-path is not stalled by disk I/O.
	go persister(snap)
}

// ResetPlanModeForTest resets plan-mode state. Test-only.
func ResetPlanModeForTest() {
	planModeState.mu.Lock()
	planModeState.inPlanMode = false
	planModeState.firstToolUsed = false
	planModeState.everUsed = false
	planModeState.lastEntryAt = time.Time{}
	planModeState.planID = ""
	planModeState.planIDHistory = nil
	planModeState.persister = nil
	planModeState.interactionOccurred = false
	planModeState.mu.Unlock()
}

// CurrentPlanID returns the active plan-id, or "" when plan mode is not in
// effect. Thread-safe; takes the state mutex internally. Used by the
// TaskCreate hook (9.2) to tag newly-created tasks with their PlanID.
func CurrentPlanID() string {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	return planModeState.planID
}

// PlanIDHistorySnapshot returns a copy of every plan-id minted in this
// session, in chronological order (uuid v7 sorts by mint time). Used by
// the compaction service (9.2) to enumerate plans for PlanArchive.
func PlanIDHistorySnapshot() []string {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	return append([]string(nil), planModeState.planIDHistory...)
}

// PlanModeEntered marks that we entered plan mode and mints a fresh PlanID
// for the new plan. The new id is appended to PlanIDHistory so the entire
// session's plan timeline is preserved across compactions.
//
// Call this when enter_plan_mode tool is detected.
func PlanModeEntered() {
	planModeState.mu.Lock()
	planModeState.inPlanMode = true
	planModeState.everUsed = true
	planModeState.firstToolUsed = false // Reset for new plan session
	planModeState.interactionOccurred = false
	planModeState.lastEntryAt = time.Now()
	planModeState.planID = tasks.MintPlanID()
	planModeState.planIDHistory = append(planModeState.planIDHistory, planModeState.planID)
	notifyPlanModePersisterLocked()
	planModeState.mu.Unlock()
}

// PlanModeExited marks that we exited plan mode. The current PlanID is
// cleared (so subsequent tasks are not tagged with a stale plan), but it
// remains in PlanIDHistory for archival.
//
// Call this when exit_plan_mode tool is detected.
func PlanModeExited() {
	planModeState.mu.Lock()
	planModeState.inPlanMode = false
	planModeState.firstToolUsed = false
	planModeState.planID = ""
	notifyPlanModePersisterLocked()
	planModeState.mu.Unlock()
}

// IsInPlanMode returns whether we're currently in plan mode.
func IsInPlanMode() bool {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	return planModeState.inPlanMode
}

// PlanModeEverUsed returns whether plan mode has been entered at any point
// in this session. Used by mid-session recovery to skip its nudge when the
// agent has already done planning.
func PlanModeEverUsed() bool {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	return planModeState.everUsed
}

// PlanModeInteractionOccurred returns whether ask_user_question has been called
// while in plan mode. Used by ExitPlanModeTool to warn if the agent skipped
// requirement discovery.
func PlanModeInteractionOccurred() bool {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	return planModeState.interactionOccurred
}

// RecordPlanModeInteraction marks that a user interaction happened during plan
// mode. Call this when ask_user_question is detected while in plan mode.
func RecordPlanModeInteraction() {
	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()
	if planModeState.inPlanMode {
		planModeState.interactionOccurred = true
	}
}

// CheckPlanModeFirstTool checks if this is the first tool after entering plan mode.
// Returns the problem breakdown prompt if this is the first tool, and marks
// that the first tool has been used.
//
// This follows the same pattern as CheckAndNudge and CheckShouldSimulate:
// called directly in the hooks manager, not via the registered hook system.
func CheckPlanModeFirstTool(toolName string) (string, bool) {
	lowerTool := strings.ToLower(strings.ReplaceAll(toolName, "_", ""))

	// Track plan mode entry/exit
	if lowerTool == "enterplanmode" {
		PlanModeEntered()
		logDebugToFile("plan-mode-first-tool: enter_plan_mode detected, plan mode ON")
		return "", false // Don't inject on enter_plan_mode itself
	}

	if lowerTool == "exitplanmode" {
		logDebugToFile("plan-mode-first-tool: exit_plan_mode submission detected; awaiting approval")
		return "", false // The tool marks exit only after actual approval.
	}

	// Track ask_user_question calls during plan mode
	if lowerTool == "askuserquestion" {
		planModeState.mu.Lock()
		if planModeState.inPlanMode {
			planModeState.interactionOccurred = true
			logDebugToFile("plan-mode-first-tool: ask_user_question detected in plan mode, interactionOccurred = true")
		}
		planModeState.mu.Unlock()
	}

	// If not in plan mode, no injection
	if !IsInPlanMode() {
		return "", false
	}

	planModeState.mu.Lock()
	defer planModeState.mu.Unlock()

	// If first tool already used, no injection
	if planModeState.firstToolUsed {
		return "", false
	}

	// This is the FIRST tool after entering plan mode (or first tool in session)
	// Mark as used so subsequent tools don't get the prompt
	planModeState.firstToolUsed = true

	logDebugToFile("plan-mode-first-tool: INJECTING problem breakdown prompt on tool=%s", toolName)

	// Return the problem breakdown prompt
	return ProblemBreakdownPrompt(), true
}

// ProblemBreakdownPrompt returns the structured problem decomposition prompt.
// This prompt teaches agents to break down problems at the lowest logical level.
func ProblemBreakdownPrompt() string {
	return `[PLAN MODE — PROBLEM BREAKDOWN REQUIRED]

═══════════════════════════════════════════════════════════════════════════════
          BEFORE YOU EXECUTE ANY TOOL, BREAK DOWN THE PROBLEM
═══════════════════════════════════════════════════════════════════════════════

You are in PLAN MODE. Before working on the problem, break it down almost as if
it was a beginning to CS intro course where you learn how to break down problems
based on what you need, what you expect and how logic works at the lowest level.

Understand and plan the logic and things needed at the most low level for things
to work.

─────────────────────────────────────────────────────────────────────────────────
                        STRUCTURED BREAKDOWN TEMPLATE
─────────────────────────────────────────────────────────────────────────────────

For each component of the problem, document:

┌─────────────────────────────────────────────────────────────────────────────┐
│  WHAT I NEED                                                                 │
│  ────────────                                                                │
│  • List every input, dependency, precondition required                      │
│  • What must exist before this can work?                                    │
│  • What state must be initialized?                                          │
│                                                                              │
│  WHAT I EXPECT                                                               │
│  ─────────────                                                               │
│  • Define the expected output for each step                                 │
│  • What does success look like at each level?                               │
│  • What are the failure modes and how to detect them?                       │
│                                                                              │
│  HOW LOGIC WORKS (LOWEST LEVEL)                                              │
│  ──────────────────────────────                                              │
│  • Trace the data flow step-by-step                                         │
│  • Identify every transformation and its invariants                         │
│  • What assumptions does each step make?                                    │
│  • Where could the chain break?                                             │
└─────────────────────────────────────────────────────────────────────────────┘

─────────────────────────────────────────────────────────────────────────────────
                              EXAMPLE BREAKDOWN
─────────────────────────────────────────────────────────────────────────────────

Problem: "Add user authentication to the API"

WHAT I NEED:
  • User database table with hashed passwords
  • Session management mechanism
  • Token generation/verification system
  • Protected route middleware

WHAT I EXPECT:
  • POST /login returns token on valid credentials
  • GET /profile returns 401 without valid token
  • Token expires after configured duration
  • Invalid credentials return 401 (not 500)

HOW LOGIC WORKS (LOWEST LEVEL):
  1. User submits credentials → server receives raw email/password
  2. Server looks up email in database → if not found, return 401
  3. Server hashes submitted password → compare with stored hash
  4. If match → generate JWT with user_id + expiration
  5. Token signed with secret → client stores in cookie/header
  6. Protected routes: extract token → verify signature → decode user_id
  7. Load user from DB → attach to request → continue to handler

  BREAKING POINTS:
  • Database connection fails → need retry/fallback
  • Hash comparison timing attack → use constant-time compare
  • Token on stolen device → need refresh token rotation
  • Secret compromised → need key rotation mechanism

─────────────────────────────────────────────────────────────────────────────────
                                WHY THIS MATTERS
─────────────────────────────────────────────────────────────────────────────────

  Agents who skip decomposition:
    • Miss hidden dependencies until they fail
    • Create incomplete solutions that need multiple fix iterations
    • Don't anticipate edge cases until users hit them
    • Produce unmaintainable code that others can't understand

  Agents who decompose first:
    • See the full picture before writing code
    • Identify blockers before hitting them
    • Create complete, tested, documented solutions
    • Write code that's self-documenting through clear structure

─────────────────────────────────────────────────────────────────────────────────
                         DECISION TREE (DEPENDENCY MAP)
─────────────────────────────────────────────────────────────────────────────────

After decomposing the problem, identify every decision that must be made.
Arrange them as a tree where downstream decisions depend on upstream ones.

For EACH decision:
┌─────────────────────────────────────────────────────────────────────────────┐
│  DECISION: <what needs to be decided>                                       │
│  DEPENDS ON: <upstream decisions that must be resolved first>               │
│  CAN RESOLVE FROM CODEBASE: <yes/no — if yes, explore instead of asking>   │
│  RECOMMENDED ANSWER: <your best guess based on codebase exploration>       │
│  STATUS: unresolved / resolved                                              │
└─────────────────────────────────────────────────────────────────────────────┘

Walk the tree depth-first. Resolve upstream decisions before downstream ones.

─────────────────────────────────────────────────────────────────────────────────
                         INTERROGATION PROTOCOL
─────────────────────────────────────────────────────────────────────────────────

For each unresolved decision that CANNOT be answered from the codebase:

1. Formulate ONE focused question with your recommended answer
2. Use ask_user_question to present it
3. Wait for user response (approve / modify / reject your recommendation)
4. Mark the decision as resolved
5. Move to the next decision in dependency order

Do NOT write the plan until all critical decisions reach "resolved" status.

═══════════════════════════════════════════════════════════════════════════════
                           TOOL AUTHORIZATION
═══════════════════════════════════════════════════════════════════════════════

Plan mode is an approval ceremony, not a permission boundary. It neither grants
nor removes tool access. Normal workspace, task, credential, permission, and
safety controls apply exactly as they do outside plan mode.

Use ask_user_question for decisions that genuinely require user judgment. For
UI, layout, design, or architectural trade-offs, prefer type="visual_choice".

═══════════════════════════════════════════════════════════════════════════════

Now proceed with your research, keeping this breakdown framework in mind.
Document your decomposition before executing. This is the foundation of
systematic problem solving.`
}

// PlanModeFirstToolHook implements the Hook interface for registration with the hook system.
// It delegates to CheckPlanModeFirstTool for the actual logic.
type PlanModeFirstToolHook struct{}

// NewPlanModeFirstToolHook creates a new plan mode first tool hook.
func NewPlanModeFirstToolHook() *PlanModeFirstToolHook {
	return &PlanModeFirstToolHook{}
}

// Name returns the hook name.
func (h *PlanModeFirstToolHook) Name() string {
	return "plan-mode-first-tool-hook"
}

// Priority returns the hook priority (runs before task enforcement).
func (h *PlanModeFirstToolHook) Priority() int {
	return PlanModeFirstToolPriority
}

// Filter returns true for tool execution events.
func (h *PlanModeFirstToolHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute
}

// OnEvent is the main hook logic - injects problem breakdown on first tool in plan mode.
func (h *PlanModeFirstToolHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		return hooks.Continue(), nil
	}

	if msg, shouldInject := CheckPlanModeFirstTool(toolName); shouldInject {
		return hooks.ContinueWithMessage(msg), nil
	}

	return hooks.Continue(), nil
}
