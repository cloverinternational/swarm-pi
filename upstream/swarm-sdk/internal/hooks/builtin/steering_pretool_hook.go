package builtin

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// logDebugToFile writes debug info to file for real-time steering observation
func logDebugToFile(format string, v ...any) {
	f, err := os.OpenFile("/tmp/hooks-debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "["+time.Now().Format("15:04:05.000")+"] "+format+"\n", v...)
}

// SteeringPreToolHook evaluates tool calls for task relevance BEFORE execution.
// It can block off-topic tools or inject focus guidance, closing the steering
// feedback loop that the old SwarmCode system had.
//
// Gating strategy:
//   - Task management tools are always allowed without LLM evaluation.
//   - Every tool call is now evaluated (sampling removed) for maximum guidance.
type SteeringPreToolHook struct {
	steeringAgent   *agent.SteeringAgent
	logger          observability.Logger
	contextProvider SteeringContextProvider
	eventEmitter    SteeringEventEmitter // emits decision events to the hook pipeline (e.g. for bronze capture)

	mu sync.Mutex
	// Phase tracking: anchored on the top-tier in-progress task. Resets when
	// the active task changes.
	currentPhaseTask string
	toolsInPhase     int
	artifactsInPhase []string
	artifactsSeen    map[string]bool

	// Steering context accumulator: tracks recent guidance so the evaluator
	// knows what it already told the agent and avoids redundant suggestions.
	priorGuidance        []string // last N suggestions (ring buffer)
	consecutiveGuides    int      // count of consecutive "guide" actions
	maxPriorGuidance     int      // ring buffer capacity
	maxConsecutiveGuides int      // after this many consecutive guides, auto-continue

	enabled bool
}

// SteeringEventEmitter allows the steering hook to emit secondary events
// (e.g. steering.decision_made) back into the hook pipeline so that other
// hooks (notably bronze capture) can observe and record steering decisions.
type SteeringEventEmitter interface {
	Emit(ctx context.Context, event hooks.Event) (*hooks.Event, error)
}

// SteeringContextProvider supplies rich session context for pre-tool relevance
// evaluation. It is a superset of AnalysisContextProvider so the TUI can share
// one implementation between the two steering hooks.
type SteeringContextProvider interface {
	AnalysisContextProvider
}

// alwaysAllowTools are task-management and user-interaction tools that must
// never be blocked or evaluated -- evaluating them would risk infinite
// recursion (task tools) or strip the question context needed to render
// the visual companion correctly (ask_user_question).
var alwaysAllowTools = map[string]bool{
	"TaskManage": true, "task_manage": true,
	"TaskCreate": true, "TaskUpdate": true, "TaskRead": true, "TaskList": true,
	"task_create": true, "task_update": true, "task_read": true, "task_list": true,
	"TaskGet": true, "task_get": true,
	"AskUserQuestion": true, "ask_user_question": true,
}

// alwaysEvaluateTools have high enough blast radius that we evaluate every
// call regardless of sampling cadence.
var alwaysEvaluateTools = map[string]bool{
	"Write": true, "Edit": true, "Bash": true, "Shell": true,
	"MultiEdit":  true,
	"file_write": true, "str_replace": true, "multi_edit": true,
	"shell": true, "bash": true,
}

// fileTouchingTools are the ones we record in the phase artifact set.
var fileTouchingTools = map[string]bool{
	"Read": true, "Write": true, "Edit": true, "MultiEdit": true,
	"file_read": true, "file_write": true, "str_replace": true, "multi_edit": true,
}

const (
	// Hard ceiling on how long the evaluator is allowed to block the agent.
	// 30s to accommodate LLM latency under parallel SAC agent load.
	steeringTimeout = 30 * time.Second
)

// NewSteeringPreToolHook creates a new pre-tool steering hook.
func NewSteeringPreToolHook(sa *agent.SteeringAgent, logger observability.Logger) *SteeringPreToolHook {
	return &SteeringPreToolHook{
		steeringAgent: sa,
		logger:        logger,
		artifactsSeen: make(map[string]bool),
		// Lower than the old 5/4 thresholds after the 2026-04-27 survey
		// observed 5 identical "decompose-first" banners stacked on a
		// single user prompt. At 3 prior guides we remember enough to
		// avoid repetition; after 2 consecutive guides we auto-continue
		//	rather than dragging the agent through a third copy.
		maxPriorGuidance:     3,
		maxConsecutiveGuides: 2,
		enabled:              true,
	}
}

// Name returns the hook name.
func (h *SteeringPreToolHook) Name() string { return "steering-pretool" }

// Priority runs this hook very early in the pre-tool chain.
func (h *SteeringPreToolHook) Priority() int { return 95 }

// Filter selects pre-tool events only.
func (h *SteeringPreToolHook) Filter(event hooks.Event) bool {
	if !h.enabled {
		return false
	}
	return event.Type == hooks.EventToolBeforeExecute
}

// SetEnabled toggles the hook at runtime.
func (h *SteeringPreToolHook) SetEnabled(enabled bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.enabled = enabled
}

// IsEnabled reports whether the hook is active.
func (h *SteeringPreToolHook) IsEnabled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.enabled
}

// SetContextProvider wires the rich session context provider. When nil, the
// hook falls back to title-only evaluation.
func (h *SteeringPreToolHook) SetContextProvider(cp SteeringContextProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.contextProvider = cp
}

// SetEventEmitter wires the event emitter that publishes steering decision
// events back into the hook pipeline. When set, every steering decision
// (approve, block, guide, focus, auto-allow, fail-open) is emitted as a
// hooks.EventSteeringDecisionMade event so bronze and other observers can
// capture it. When nil, decisions are only logged.
func (h *SteeringPreToolHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.eventEmitter = emitter
}

// emitDecision publishes a steering.decision_made event through the emitter.
// It is fire-and-forget: errors are logged but never surface to the caller.
func (h *SteeringPreToolHook) emitDecision(ctx context.Context, toolName, decision, reason, taskTitle, taskCategory string, elapsedMs int64, evaluated bool) {
	h.mu.Lock()
	emitter := h.eventEmitter
	h.mu.Unlock()

	if emitter == nil {
		return
	}

	evt := hooks.Event{
		Type:      hooks.EventSteeringDecisionMade,
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name":     toolName,
			"decision":      decision,
			"reason":        reason,
			"task_title":    taskTitle,
			"task_category": taskCategory,
			"elapsed_ms":    elapsedMs,
			"evaluated":     evaluated, // true = LLM eval, false = rule-based
		},
	}

	// Use a background context to avoid cancellation from the parent
	// (the parent may have a tight steering timeout).
	emitCtx := agent.WithSteeringReentrancy(context.Background())
	if _, err := emitter.Emit(emitCtx, evt); err != nil {
		if h.logger != nil {
			h.logger.Warn(ctx, "steering_pretool.emit_decision_failed",
				observability.F("error", err.Error()),
				observability.F("tool", toolName))
		}
	}
}

// OnEvent evaluates a proposed tool call for task relevance.
func (h *SteeringPreToolHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	if agent.IsSteeringReentrant(ctx) {
		logDebugToFile("steering-pretool: SKIP reentrancy guard")
		return hooks.Continue(), nil
	}

	// Sub-agents bypass steering entirely — they execute tools directly
	// without task-relevance evaluation to reduce latency and token cost.
	if agent.IsSubAgent(ctx) {
		return hooks.Continue(), nil
	}

	toolName, _ := event.Data["tool_name"].(string)
	if toolName == "" {
		if tn, ok := event.Data["name"].(string); ok {
			toolName = tn
		}
	}
	params, _ := event.Data["params"].(map[string]any)
	if params == nil {
		params, _ = event.Data["tool_input"].(map[string]any)
	}

	if alwaysAllowTools[toolName] {
		logDebugToFile("steering-pretool: SKIP always-allow tool=%s", toolName)
		h.emitDecision(ctx, toolName, "approve", "always-allow tool", "", "", 0, false)
		return hooks.Continue(), nil
	}
	if h.steeringAgent == nil {
		logDebugToFile("steering-pretool: SKIP steeringAgent=nil tool=%s", toolName)
		return hooks.Continue(), nil
	}

	mgr := ii.GetTodoManager()
	if mgr == nil {
		logDebugToFile("steering-pretool: SKIP todoManager=nil tool=%s", toolName)
		return hooks.Continue(), nil
	}
	var activeTask *ii.TodoItem
	var pendingTask *ii.TodoItem
	var taskHistory []string
	for _, task := range mgr.Todos() {
		t := task
		taskHistory = append(taskHistory, fmt.Sprintf("[%s] %s", t.Status, t.Content))
		if activeTask == nil && t.Status == ii.TodoStatusInProgress {
			activeTask = &t
		}
		if pendingTask == nil && t.Status == ii.TodoStatusPending {
			pendingTask = &t
		}
	}
	// Use in-progress task first; fall back to first pending task so planning
	// tasks that haven't been started yet still anchor steering.
	if activeTask == nil {
		activeTask = pendingTask
	}
	if activeTask == nil {
		// When there are zero tasks at all, the task-enforcement hook will
		// handle blocking with a detailed message. Injecting a second
		// "no active task" nudge here is pure noise — the agent can't act
		// on steering guidance when enforcement will block the tool anyway.
		// Stay silent and let enforcement own this signal.
		if len(taskHistory) == 0 {
			logDebugToFile("steering-pretool: SKIP no tasks exist, deferring to enforcement hook tool=%s", toolName)
			return hooks.Continue(), nil
		}
		// Tasks exist but none are in-progress or pending — all completed.
		// A gentle nudge to pick up new work is appropriate here.
		if h.logger != nil {
			h.logger.Debug(ctx, "steering_pretool.no_active_task",
				observability.F("tool", toolName))
		}
		msg := fmt.Sprintf("[Steering] No active task — all tracked tasks are completed. Create a new task if this tool call starts fresh work.")
		return hooks.ContinueWithMessage(msg), nil
	}

	// Debugging category = diagnosis ONLY. Reading/searching is the work.
	// Implementation (Write/Edit/Bash) requires an Acting task.
	if activeTask.Category == "debugging" || activeTask.Category == "D" {
		// Allow read-only diagnostic tools during debugging
		diagnosticTools := map[string]bool{
			"Read": true, "grep": true, "find": true, "list_files": true,
			"file_read": true, "semantic_grep": true, "search": true,
		}
		if !diagnosticTools[toolName] {
			msg := fmt.Sprintf("[Steering] %s requires an ACTING task. Current task is DEBUGGING (diagnosis only).\nCreate an Acting task to implement fixes, or switch the current task category to 'acting'.", toolName)
			h.emitDecision(ctx, toolName, "block", "debugging category restricts to read-only", activeTask.Content, "debugging", 0, false)
			return hooks.Block(msg), nil
		}
		return hooks.Continue(), nil
	}

	// Planning category = Plan Mode: hard-block destructive tools without
	// LLM evaluation. The agent should only read/research during planning.
	if activeTask.Category == ii.TaskCategoryPlanning && alwaysEvaluateTools[toolName] {
		if h.logger != nil {
			h.logger.Info(ctx, "steering_pretool.plan_mode_block",
				observability.F("tool", toolName),
				observability.F("task", activeTask.Content))
		}
		msg := fmt.Sprintf("[Steering] Blocked %s: task \"%s\" is in PLANNING mode — only reading and research allowed. Change the task category to 'acting' before making changes.",
			toolName, activeTask.Content)
		h.emitDecision(ctx, toolName, "block", "planning mode restricts destructive tools", activeTask.Content, "planning", 0, false)
		return hooks.Block(msg), nil
	}

	// Phase tracking + sampling gate under the lock so counters stay consistent.
	h.mu.Lock()
	if h.currentPhaseTask != activeTask.Content {
		h.currentPhaseTask = activeTask.Content
		h.toolsInPhase = 0
		h.artifactsInPhase = h.artifactsInPhase[:0]
		for k := range h.artifactsSeen {
			delete(h.artifactsSeen, k)
		}
		// Reset accumulator on task change — prior guidance is stale.
		h.priorGuidance = h.priorGuidance[:0]
		h.consecutiveGuides = 0
	}
	h.toolsInPhase++

	if fileTouchingTools[toolName] {
		if fp, ok := params["file_path"].(string); ok && fp != "" {
			if !h.artifactsSeen[fp] {
				h.artifactsSeen[fp] = true
				h.artifactsInPhase = append(h.artifactsInPhase, fp)
			}
		}
	}

	// Snapshot state under the lock for the evaluation call.
	toolsInPhase := h.toolsInPhase
	artifactsInPhase := make([]string, len(h.artifactsInPhase))
	copy(artifactsInPhase, h.artifactsInPhase)
	priorGuidance := make([]string, len(h.priorGuidance))
	copy(priorGuidance, h.priorGuidance)
	consecutiveGuides := h.consecutiveGuides
	cp := h.contextProvider
	h.mu.Unlock()

	taskCat := string(activeTask.Category)
	if taskCat == "" {
		taskCat = "acting"
	}

	// If we've had too many consecutive guides, skip the LLM call to avoid
	// drowning the agent in repetitive advice. Reset the counter so the hook
	// re-engages after the cooldown rather than being permanently disabled.
	if consecutiveGuides >= h.maxConsecutiveGuides {
		logDebugToFile("steering-pretool: AUTO-CONTINUE after %d consecutive guides tool=%s", consecutiveGuides, toolName)
		h.mu.Lock()
		h.consecutiveGuides = 0 // reset so hook re-engages after cooldown
		h.mu.Unlock()
		h.emitDecision(ctx, toolName, "approve", fmt.Sprintf("auto-continue after %d consecutive guides", consecutiveGuides), activeTask.Content, taskCat, 0, false)
		return hooks.Continue(), nil
	}

	// Build a richer task description from notes if available.
	taskDesc := activeTask.Content
	if len(activeTask.Notes) > 0 {
		taskDesc = activeTask.Content + "\nNotes:\n- " + strings.Join(activeTask.Notes, "\n- ")
	}

	rc := agent.RelevanceContext{
		ToolName:         toolName,
		ToolParams:       paramsForSteeringEval(toolName, params),
		TaskTitle:        activeTask.Content,
		TaskCategory:     taskCat,
		TaskDescription:  taskDesc,
		TaskHistory:      taskHistory,
		ToolsInPhase:     toolsInPhase,
		ArtifactsInPhase: artifactsInPhase,
		PriorGuidance:    priorGuidance,
	}

	if cp != nil {
		rc.RecentMessages = cp.GetRecentMessages(0)
		rc.ActivePlan = cp.GetActivePlan()
		// Prefer the provider's task history if present -- it's richer.
		if th := cp.GetTaskHistory(); len(th) > 0 {
			rc.TaskHistory = th
		}
	}
	logDebugToFile("steering-pretool-context: tool=%s cp_nil=%v msgs=%d plan_len=%d tasks=%d artifacts=%d",
		toolName, cp == nil, len(rc.RecentMessages),
		len(rc.ActivePlan), len(rc.TaskHistory), len(rc.ArtifactsInPhase))

	evalCtx := agent.WithSteeringReentrancy(ctx)
	evalCtx, cancel := context.WithTimeout(evalCtx, steeringTimeout)
	defer cancel()

	start := time.Now()
	action, reason, err := h.steeringAgent.EvaluateRelevance(evalCtx, rc)
	elapsed := time.Since(start)

	if err != nil {
		if h.logger != nil {
			h.logger.Warn(ctx, "steering_pretool.evaluation_failed",
				observability.F("error", err.Error()),
				observability.F("tool", toolName),
				observability.F("elapsed_ms", elapsed.Milliseconds()))
		}
		h.emitDecision(ctx, toolName, "approve", "fail-open: evaluation error: "+err.Error(), activeTask.Content, taskCat, elapsed.Milliseconds(), true)
		return hooks.Continue(), nil // fail open
	}

	// Log every evaluation outcome, including continue, so operators can see
	// what the evaluator is actually deciding.
	if h.logger != nil {
		h.logger.Info(ctx, "steering_pretool.evaluated",
			observability.F("tool", toolName),
			observability.F("action", action),
			observability.F("reason", reason),
			observability.F("task", activeTask.Content),
			observability.F("tools_in_phase", toolsInPhase),
			observability.F("artifacts_in_phase", len(artifactsInPhase)),
			observability.F("elapsed_ms", elapsed.Milliseconds()))
	}
	// DEBUG: Also log to file for real-time observation
	logDebugToFile("steering-pretool: tool=%s action=%s reason=%q task=%s",
		toolName, action, reason, activeTask.Content)

	// Emit structured decision event for bronze capture and observability.
	h.emitDecision(ctx, toolName, action, reason, activeTask.Content, taskCat, elapsed.Milliseconds(), true)

	// Record guidance in the accumulator so future evaluations know what
	// was already suggested and can build on it instead of repeating.
	h.mu.Lock()
	switch action {
	case "guide", "focus":
		// Suppress NEAR-IDENTICAL repeats by comparing the first 64 chars of
		// the reason. If the evaluator is about to emit the same banner it
		// just emitted, downgrade to a silent continue rather than spam the
		// agent context. Survey 2026-04-27 observed 5 copies of the same
		// "decompose-first" banner stacked on one prompt.
		reasonKey := reason
		if len(reasonKey) > 64 {
			reasonKey = reasonKey[:64]
		}
		repeated := false
		for _, prior := range h.priorGuidance {
			priorKey := prior
			if len(priorKey) > 64 {
				priorKey = priorKey[:64]
			}
			if priorKey == reasonKey {
				repeated = true
				break
			}
		}
		if repeated {
			// Treat the repeat as an approve: counter resets, no banner emitted.
			h.consecutiveGuides = 0
			h.mu.Unlock()
			logDebugToFile("steering-pretool: SUPPRESS repeat guidance tool=%s prefix=%q", toolName, reasonKey)
			return hooks.Continue(), nil
		}
		h.consecutiveGuides++
		// Append to ring buffer, evicting oldest if at capacity.
		if len(h.priorGuidance) >= h.maxPriorGuidance {
			h.priorGuidance = append(h.priorGuidance[1:], reason)
		} else {
			h.priorGuidance = append(h.priorGuidance, reason)
		}
	default:
		h.consecutiveGuides = 0
	}
	h.mu.Unlock()

	switch action {
	case "block":
		msg := fmt.Sprintf("[Steering] Blocked %s: %s\nCurrent task: %s", toolName, reason, activeTask.Content)
		return hooks.Block(msg), nil
	case "focus":
		msg := fmt.Sprintf("[Steering] Focus reminder: %s\nCurrent task: %s", reason, activeTask.Content)
		return hooks.ContinueWithMessage(msg), nil
	case "guide":
		msg := fmt.Sprintf("[Steering] Project context: %s\nCurrent task: %s", reason, activeTask.Content)
		return hooks.ContinueWithMessage(msg), nil
	default:
		// Continue silently — don't inject into agent context.
		return hooks.Continue(), nil
	}
}

// paramsForSteeringEval returns a copy of the tool's params with fields that
// would bias the steering evaluator stripped.
//
// Specifically, `ask_user_question` with `type=visual_choice` carries a
// `visual` payload (a primitive with a full description of the options being
// presented — cards, mockups, etc.). The evaluator is an LLM and reads that
// payload as "the agent is committing to aesthetic work", which can push an
// otherwise-equivalent text question into the `block` action.
//
// Asking the user is never off-topic by definition; *when* to ask is what
// steering governs. The detailed primitive payload biases the evaluator
// toward "aesthetic premature work" judgments. So we strip the `visual`
// payload from the evaluator's view while leaving the question text and
// type visible (the evaluator needs to know the agent is asking a
// visual-style question so it can give topically-appropriate guidance).
// The actual tool call uses unmodified params.
func paramsForSteeringEval(toolName string, params map[string]any) map[string]any {
	if toolName != "ask_user_question" || params == nil {
		return params
	}
	if _, hasVisual := params["visual"]; !hasVisual {
		return params // nothing to sanitize
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if k == "visual" {
			continue
		}
		out[k] = v
	}
	return out
}
