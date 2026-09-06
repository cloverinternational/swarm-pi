package builtin

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// SessionStartHook injects plan mode guidance at the start of each conversation.
//
// This hook fires on EventSessionStart and:
// 1. Checks if complexity detection indicates complex work
// 2. If complex: surfaces planning as the FIRST action (not a warning, just logical)
// 3. If simple: allows agent to proceed directly
//
// Key principle: Make planning the path of least resistance for complex work,
// not a punishment or friction point.

const (
	// SessionStartPriority runs early to inject guidance before other hooks
	SessionStartPriority = 10
)

// SessionStartHook injects plan mode guidance at conversation start
type SessionStartHook struct{}

// NewSessionStartHook creates a new session start hook
func NewSessionStartHook() *SessionStartHook {
	return &SessionStartHook{}
}

// Name returns the hook name
func (h *SessionStartHook) Name() string {
	return "session-start-hook"
}

// Priority returns the hook priority (runs early)
func (h *SessionStartHook) Priority() int {
	return SessionStartPriority
}

// Filter returns true for SessionStart events
func (h *SessionStartHook) Filter(event hooks.Event) bool {
	return event.Type == string(hooks.EventAgentStarted)
}

// OnEvent injects the plan mode guidance system message
func (h *SessionStartHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	return hooks.ContinueWithMessage(planModeGuidanceMessage), nil
}

// planModeGuidanceMessage is the system message injected at session start
const planModeGuidanceMessage = `[SESSION START - WORKFLOW GUIDANCE]

═══════════════════════════════════════════════════════════════════════════════
                        RECOMMENDED WORKFLOW
═══════════════════════════════════════════════════════════════════════════════

For complex tasks, follow this order:

   1. enter_plan_mode()        →  Design your approach FIRST
   2. EXPLORE + INTERROGATE    →  Walk the decision tree with the user (one question at a time, with recommended answers)
   3. Write plan file          →  Only after shared understanding is reached
   4. exit_plan_mode()         →  Submit plan for approval
   5. TaskManage(create)       →  Break down plan into tracked tasks
   6. TaskManage(update)       →  Mark task in_progress before working
   7. Execute tools            →  Now you can use other tools
   8. TaskManage(update)       →  Mark task completed when finished

═══════════════════════════════════════════════════════════════════════════════
                              WHEN TO PLAN:
═══════════════════════════════════════════════════════════════════════════════

Use enter_plan_mode when:
  • Task involves multiple files or components
  • Architecture changes are needed
  • Complex refactoring required
  • New feature implementation
  • User explicitly asks to plan
  • There are unresolved decisions that require user judgment

Skip planning for:
  • Simple single-file edits
  • Quick questions or lookups
  • Minor bug fixes
  • User says "continue" or "proceed" (plan already exists)

═══════════════════════════════════════════════════════════════════════════════

This guidance is advisory. Hard enforcement happens via task enforcement hook.
Remember: Plan → Interrogate → Track → Execute → Verify → Document.`
