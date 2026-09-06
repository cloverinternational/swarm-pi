package builtin

import (
	"context"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// Task Enforcement Hook - FORCES task creation before ANY tool execution.
//
// This hook enforces task usage by:
//   - BLOCKING ALL tool execution until at least one task exists
//   - Only exempting task tools and plan mode tools
//   - Tracking if user/agent has indicated they have a plan
//   - Smart detection of user intent (avoiding false positives)
//   - Agent CANNOT EXECUTE ANY TOOL without a task
//
// The goal is to force agents to plan and track work from the start.

const (
	// ImmediateBlockThreshold = 0 means block immediately on first non-exempt tool
	ImmediateBlockThreshold = 0
	// EnforcementPriority runs after steering (100) but before other hooks
	EnforcementPriority = 95
)

// ToolCounter tracks tool executions per session (used for nudge messages).
// It is thread-safe and can be shared across goroutines.
type ToolCounter struct {
	count int
	mu    sync.Mutex
}

// Increment adds 1 to the counter and returns the new count.
func (c *ToolCounter) Increment() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	return c.count
}

// Count returns the current counter value.
func (c *ToolCounter) Count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.count
}

// Reset sets the counter back to 0.
func (c *ToolCounter) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count = 0
}

// SessionState tracks planning state for the current session.
// This is used to determine if the user/agent has already planned.
type SessionState struct {
	planModeUsed    bool   // enter_plan_mode was called
	userHasPlan     bool   // user indicated they have a plan
	lastUserMessage string // last user message for context
	mu              sync.Mutex
}

// SetPlanModeUsed marks that plan mode was used
func (s *SessionState) SetPlanModeUsed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planModeUsed = true
}

// HasPlanModeUsed returns if plan mode was used
func (s *SessionState) HasPlanModeUsed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planModeUsed
}

// SetUserHasPlan marks that user indicated they have a plan
func (s *SessionState) SetUserHasPlan() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userHasPlan = true
}

// HasUserPlan returns if user indicated they have a plan
func (s *SessionState) HasUserPlan() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userHasPlan
}

// SetLastUserMessage stores the last user message
func (s *SessionState) SetLastUserMessage(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastUserMessage = msg
}

// GetLastUserMessage returns the last user message
func (s *SessionState) GetLastUserMessage() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUserMessage
}

// Reset clears all session state
func (s *SessionState) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.planModeUsed = false
	s.userHasPlan = false
	s.lastUserMessage = ""
}

type EnforcementMode string

const (
	EnforcementModeAdvise EnforcementMode = "advise"
	EnforcementModeBlock  EnforcementMode = "block"
	EnforcementModeOff    EnforcementMode = "off"
)

// TaskEnforcementConfig controls the no-focused-task gate.
type TaskEnforcementConfig struct {
	EnforcementMode EnforcementMode `json:"enforcement_mode"`
}

func (c TaskEnforcementConfig) withDefaults() TaskEnforcementConfig {
	switch c.EnforcementMode {
	case EnforcementModeAdvise, EnforcementModeBlock, EnforcementModeOff:
	default:
		c.EnforcementMode = EnforcementModeAdvise
	}
	return c
}

// TaskEnforcementHook advises by default and can opt into legacy blocking.
type TaskEnforcementHook struct {
	counter      *ToolCounter
	sessionState *SessionState
	config       TaskEnforcementConfig
}

// NewTaskEnforcementHook creates a new task enforcement hook.
func NewTaskEnforcementHook() *TaskEnforcementHook {
	return NewTaskEnforcementHookWithConfig(TaskEnforcementConfig{})
}

// NewTaskEnforcementHookWithConfig creates a hook with explicit block, advise,
// or off behavior.
func NewTaskEnforcementHookWithConfig(config TaskEnforcementConfig) *TaskEnforcementHook {
	return &TaskEnforcementHook{
		counter:      &ToolCounter{},
		sessionState: &SessionState{},
		config:       config.withDefaults(),
	}
}

// Name returns the hook name for identification.
func (h *TaskEnforcementHook) Name() string {
	return "task-enforcement-hook"
}

// Priority returns the hook priority.
func (h *TaskEnforcementHook) Priority() int {
	return EnforcementPriority
}

// Filter returns true for tool execution events and message events.
func (h *TaskEnforcementHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolBeforeExecute || event.Type == hooks.EventMessageAfterReceive
}

// OnEvent is the main hook logic - BLOCKS all tools until tasks exist.
//
// Logic Flow:
//  1. Handle message events to detect plan-following intent
//  2. For tool events:
//     a. Track plan mode usage
//     b. Extract tool name from event data
//     c. Check if tool is exempt (task tools, plan mode tools)
//     d. Get global TodoManager
//     e. Check if any pending or in-progress tasks exist
//     f. If tasks exist: reset counter, allow tool
//     g. If no tasks: BLOCK IMMEDIATELY with enforcement message
func (h *TaskEnforcementHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	if h.config.EnforcementMode == EnforcementModeOff {
		return hooks.Continue(), nil
	}

	// Sub-agents bypass all task enforcement — they execute tools directly
	// on behalf of a parent session that already has tasks. Blocking them
	// causes delegation to fail and wastes LLM turns on enforcement messages.
	if agent.IsSubAgent(ctx) {
		return hooks.Continue(), nil
	}

	// Handle message events to detect plan-following intent
	if event.Type == hooks.EventMessageAfterReceive {
		// Check if this is a user message
		role, _ := event.Data["role"].(string)
		if role == "user" {
			content, _ := event.Data["content"].(string)
			if content != "" {
				h.sessionState.SetLastUserMessage(content)
				// Check if user indicates they have an existing plan
				if userIndicatesExistingPlan(content) {
					h.sessionState.SetUserHasPlan()
				}
			}
		}
		return hooks.Continue(), nil
	}

	// Handle tool execution events
	// Extract tool name from event data
	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		// No tool name - can't enforce, allow
		return hooks.Continue(), nil
	}

	// Track if plan mode was used
	if isPlanModeTool(toolName) {
		h.sessionState.SetPlanModeUsed()
	}

	// Exempt task tools and plan mode tools from blocking
	// This allows the agent to create tasks and use plan mode even when blocked
	if isExemptTool(toolName) {
		return hooks.Continue(), nil
	}

	// Read-only exploration tools are ALWAYS exempt, task or no task.
	// Forcing task creation before `Read`/`Grep`/`Glob`/`LS` turned simple
	// exploration ("what's in this directory?") into a mandatory 2-step
	// ceremony (TaskCreate → TaskUpdate → actual tool), which the 2026-04-23
	// session survey repeatedly called out as the top friction point.
	// Plan mode used to be the only place these were allowed; we generalize
	// here because exploration without state-change is not the kind of
	// impactful work this hook exists to gate.
	if isPlanModeReadOnlyTool(toolName) {
		return hooks.Continue(), nil
	}
	if isBashTool(toolName) {
		if params, ok := event.Data["params"].(map[string]any); ok {
			if cmd, ok := params["command"].(string); ok && isBashReadOnly(cmd) {
				return hooks.Continue(), nil
			}
		}
	}

	// Get the global TodoManager
	tm := ii.GetTodoManager()
	if tm == nil {
		// Fail-open: if no manager, can't enforce
		return hooks.Continue(), nil
	}

	// Require an ACTIVE focus, not merely a pending task. A vague pending
	// task must not unlock unrelated side effects; the agent has to focus a
	// task (start it, or refocus an in-progress one) before acting.
	active := tm.ByStatus(ii.TodoStatusInProgress)
	hasFocus := false
	for i := range active {
		if active[i].Active {
			hasFocus = true
			break
		}
	}

	if hasFocus {
		// A task is focused! Reset counter and allow.
		h.counter.Reset()
		return hooks.Continue(), nil
	}

	// Increment counter for tracking purposes
	_ = h.counter.Increment()

	if h.config.EnforcementMode == EnforcementModeBlock {
		return hooks.Block(hooks.WrapReminder(
			h.Name(), "block", hooks.NextReminderSeq(h.Name()), h.enforcementMessageContextual())), nil
	}

	seq, ok := hooks.DefaultMetaNudgeBudget().TryClaim(
		hooks.NudgeSessionID(ctx, event), hooks.MetaNudgeBlock)
	if !ok {
		return hooks.Continue(), nil
	}
	const advisory = "No active task is focused; consider a TaskManage create/update before multi-step work."
	return hooks.ContinueWithMessage(hooks.WrapReminder(h.Name(), "nudge", seq, advisory)), nil
}

// isPlanModeTool checks if the tool is a plan mode tool.
func isPlanModeTool(toolName string) bool {
	return hooks.IsPlanModeTool(toolName)
}

// isTaskCreationTool checks if the tool is a task creation/update tool
func isTaskCreationTool(toolName string) bool {
	lowerTool := hooks.NormalizeToolName(toolName)
	return lowerTool == "taskcreate" || lowerTool == "taskupdate"
}

func isTodoMutationTool(toolName string) bool {
	lowerTool := strings.ToLower(strings.ReplaceAll(toolName, "_", ""))
	return lowerTool == "todowrite"
}

// isExemptTool checks if the tool is exempt from blocking.
// Exempt tools: task tools, plan mode tools, skill tools, and
// user-interaction tools — these are ALWAYS allowed regardless of task
// state. This now delegates to the shared classifiers in the base hooks
// package (hooks/toolclass.go) so budget-enforcement (in
// internal/skills/autogenskills) applies the identical exemption set —
// see the "join task-enforcement + skill-budget-enforcement" change.
//
// Task tools: always allowed so the agent can create tasks.
// Plan mode tools: always allowed for planning.
// Skill tools: always allowed. The autogenskills budget hook can HARD
// BLOCK every other tool until the agent creates or invokes a skill; if
// this hook then blocked SkillManage/Skill for lack of a task, the two
// hooks would deadlock each other (budget demands a skill call, task gate
// refuses it). Skill tools change no workspace state, so exempting them
// is safe and keeps both gates mutually satisfiable.
// User-interaction tools: no local state change, and the plan-mode system
// prompts explicitly instruct the agent to call these to resolve the
// decision tree before any task exists. Blocking them at the task gate
// created an unsatisfiable loop in plan mode (TaskCreate is forbidden
// during planning, but ask_user_question was being routed through the
// "needs a task" branch).
func isExemptTool(toolName string) bool {
	return hooks.IsTaskManagementTool(toolName) ||
		hooks.IsPlanModeTool(toolName) ||
		hooks.IsSkillTool(toolName) ||
		hooks.IsUserInteractionTool(toolName)
}

// isPlanModeReadOnlyTool checks if a tool is a read-only/research tool that
// should be allowed during plan mode without requiring tasks.
// The whole point of plan mode is to research and understand before creating
// tasks — blocking research tools defeats that purpose.
func isPlanModeReadOnlyTool(toolName string) bool {
	return hooks.IsReadOnlyExplorationTool(toolName)
}

// userIndicatesExistingPlan detects if user message indicates they already have a plan.
// This is SMART detection - avoids false positives like:
//   - "continue with the plan" 	 has plan
//   - "follow the plan" 	 has plan
//   - "as planned" 	 has plan
//   - "according to plan" 	 has plan
//   - "like we planned" 	 has plan
//
// But NOT:
//   - "let's plan this" 	 needs planning
//   - "we should plan" 	 needs planning
//   - "make a plan" 	 needs planning
func userIndicatesExistingPlan(message string) bool {
	lower := strings.ToLower(message)

	// Phrases that indicate user ALREADY HAS a plan
	hasPlanPhrases := []string{
		"continue with the plan",
		"continue with plan",
		"follow the plan",
		"follow your plan",
		"stick to the plan",
		"as planned",
		"according to plan",
		"according to the plan",
		"like we planned",
		"like you planned",
		"per the plan",
		"per plan",
		"from the plan",
		"from your plan",
		"in the plan",
		"in your plan",
		"the plan is",
		"my plan is",
		"our plan is",
		"go ahead with the plan",
		"proceed with the plan",
		"execute the plan",
		"implement the plan",
		"do what you planned",
		"do what you said",
		"do what we discussed",
		"continue doing",
		"keep going",
		"carry on",
		"proceed",
		"resume",
	}

	for _, phrase := range hasPlanPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

// isBashTool checks if the tool is a Bash/shell tool
func isBashTool(toolName string) bool {
	return hooks.IsBashTool(toolName)
}

// isBashReadOnly checks if a bash command is read-only (no mutation).
// Errs on the side of caution — only allows well-known read commands.
func isBashReadOnly(cmd string) bool {
	return hooks.IsBashReadOnly(cmd)
}

// enforcementMessageContextual returns a context-aware block message.
func (h *TaskEnforcementHook) enforcementMessageContextual() string {
	// If user indicated they have a plan, use a softer message
	if h.sessionState.HasUserPlan() {
		return h.enforcementMessageUserHasPlan()
	}
	// Otherwise use the standard enforcement message
	return h.enforcementMessage()
}

// enforcementMessageUserHasPlan is used when user indicated they have a plan.
func (h *TaskEnforcementHook) enforcementMessageUserHasPlan() string {
	return `[TASK ENFORCEMENT - BLOCKED]

═══════════════════════════════════════════════════════════════════════════════
              YOU CANNOT EXECUTE ANY TOOL WITHOUT A TASK
═══════════════════════════════════════════════════════════════════════════════

The user indicated they have a plan, but you need to CREATE TASKS to track it.

                              NO TASK = NO EXECUTION

═══════════════════════════════════════════════════════════════════════════════
                        CREATE TASKS FROM THE PLAN:
═══════════════════════════════════════════════════════════════════════════════

   ╔═══════════════════════════════════════════════════════════════════════╗
   ║  Use TaskManage create operations to break the plan into tasks       ║
   ╚═══════════════════════════════════════════════════════════════════════╝

═══════════════════════════════════════════════════════════════════════════════
                              EXAMPLE:
═══════════════════════════════════════════════════════════════════════════════

  TaskManage create operation:
    subject: "Implement step 1 of the plan"
    category: "acting"
    description: "From the plan: ..."

After creating your task(s), ALL tools will be unlocked.`
}

// enforcementMessage returns the block message when no tasks exist.
// Terse-and-strict: the previous version dumped a 70-line ASCII-art box on
// every block, which the 2026-04-27 survey identified as one of the largest
// sources of context-window noise. The block is the signal — repeating the
// task ontology on every blocked tool added zero information after the first
// fire. Keep the gate hard, drop the wallpaper.
func (h *TaskEnforcementHook) enforcementMessage() string {
	return `[TASK ENFORCEMENT - BLOCKED] YOU CANNOT EXECUTE ANY TOOL WITHOUT A TASK

This is a HARD REQUIREMENT. RECOMMENDED WORKFLOW:
  1. enter_plan_mode()  — for complex work
  2. TaskManage create operation
  3. TaskManage update operation with status="in_progress"
  4. Execute your tools
  5. TaskManage update operation with status="completed"

Categories: researching | planning | acting | verifying | debugging | documenting`
}

// GetCounter returns the internal counter for testing purposes.
func (h *TaskEnforcementHook) GetCounter() *ToolCounter {
	return h.counter
}

// GetSessionState returns the session state for testing purposes.
func (h *TaskEnforcementHook) GetSessionState() *SessionState {
	return h.sessionState
}
