package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// GoalStoreFileName is the file the active goal is persisted to inside the
// conversation metadata directory. Mirrors taskstore's task.json convention so
// goals survive restarts and conversation switches the same way tasks do.
const GoalStoreFileName = "goal.json"

// Goal state constants
const (
	GoalStateNotYetEvaluated = "not_yet_evaluated"
	GoalStateActive          = "active"
	GoalStateMet             = "met"
	GoalStateFailed          = "failed"
	GoalStateImpossible      = "impossible"
	GoalStateCleared         = "cleared"
)

// GoalHookPriority runs after other hooks but before final processing
const GoalHookPriority = 90

// MaxConditionLength is the maximum allowed length for a goal condition
const MaxConditionLength = 10000

// Goal represents a user-defined condition to be evaluated after each turn
type Goal struct {
	Condition  string     `json:"condition"`
	State      string     `json:"state"`
	LastReason string     `json:"last_reason"`
	Iterations int        `json:"iterations"`
	CreatedAt  time.Time  `json:"created_at"`
	AchievedAt *time.Time `json:"achieved_at,omitempty"`
}

// GoalResult represents the evaluation result
type GoalResult struct {
	Ok         bool   `json:"ok"`
	Reason     string `json:"reason"`
	Impossible bool   `json:"impossible,omitempty"`
}

// Evaluator interface for goal evaluation
type Evaluator interface {
	Evaluate(ctx context.Context, condition string, transcript string) (GoalResult, error)
}

// DefaultEvaluator is a placeholder implementation
type DefaultEvaluator struct{}

func (e *DefaultEvaluator) Evaluate(ctx context.Context, condition string, transcript string) (GoalResult, error) {
	// In real implementation, this would call the LLM with the evaluation prompt
	// For now, return not met
	return GoalResult{
		Ok:     false,
		Reason: "evaluation not implemented",
	}, nil
}

// FuncEvaluator wraps a plain function as a GoalHook Evaluator.
// Use NewFuncEvaluator to create one and wire it via GoalHook.SetEvaluator.
type FuncEvaluator struct {
	fn func(ctx context.Context, condition, transcript string) (GoalResult, error)
}

// NewFuncEvaluator creates an Evaluator backed by the given function.
func NewFuncEvaluator(fn func(ctx context.Context, condition, transcript string) (GoalResult, error)) *FuncEvaluator {
	return &FuncEvaluator{fn: fn}
}

func (e *FuncEvaluator) Evaluate(ctx context.Context, condition, transcript string) (GoalResult, error) {
	return e.fn(ctx, condition, transcript)
}

// GoalHook implements the /goal command as an AfterAgent hook
type GoalHook struct {
	goal               *Goal
	evaluator          Evaluator
	mu                 sync.RWMutex
	isTrustedWorkspace func() bool
	areHooksRestricted func() bool
	logger             observability.Logger

	// storePath is the absolute path to goal.json for the active
	// conversation. Empty disables persistence (in-memory only), which keeps
	// existing callers/tests that never call SetStorePath working unchanged.
	storePath string
}

// persistedGoal is the on-disk envelope for a goal. Kept separate from the
// runtime Goal so we can version the file format independently later.
type persistedGoal struct {
	Version string `json:"version"`
	Goal    *Goal  `json:"goal"`
}

// goalStoreVersion is the current goal.json format version.
const goalStoreVersion = "1.0.0"

// NewGoalHook creates a new goal hook
func NewGoalHook() *GoalHook {
	return &GoalHook{
		evaluator:          &DefaultEvaluator{},
		isTrustedWorkspace: func() bool { return true },  // default to trusted
		areHooksRestricted: func() bool { return false }, // default to unrestricted
		logger:             noop.NewLogger(),
	}
}

// SetLogger overrides the hook's logger (falls back to no-op when nil).
func (h *GoalHook) SetLogger(l observability.Logger) {
	if l == nil {
		l = noop.NewLogger()
	}
	h.logger = l
}

// SetStorePath sets the goal.json path (inside a conversation metadata dir) and
// loads any persisted goal from it, replacing the in-memory goal. Passing an
// empty path disables persistence and clears the in-memory goal so switching to
// a conversation that never had a goal does not leak the previous one.
//
// This mirrors taskstore.SetupTaskPersistence: call it whenever a conversation
// is opened, created, or switched.
func (h *GoalHook) SetStorePath(path string) {
	h.mu.Lock()
	h.storePath = path
	h.mu.Unlock()

	if path == "" {
		h.mu.Lock()
		h.goal = nil
		h.mu.Unlock()
		return
	}

	loaded, err := loadGoalFromDisk(path)
	if err != nil {
		if h.logger != nil {
			h.logger.Error(context.Background(), "goal.load_failed",
				observability.F("path", path), observability.F("error", err.Error()))
		}
		return
	}

	h.mu.Lock()
	h.goal = loaded // may be nil when the file is absent — that clears stale state
	h.mu.Unlock()
}

// loadGoalFromDisk reads and decodes a goal.json file. A missing file is not an
// error: it returns (nil, nil) so the caller clears any stale in-memory goal.
func loadGoalFromDisk(path string) (*Goal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("goal: failed to read %s: %w", path, err)
	}

	var env persistedGoal
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("goal: failed to parse %s: %w", path, err)
	}
	return env.Goal, nil
}

// persistLocked writes the current goal to disk. Must be called with h.mu held.
// It is a no-op when no store path is configured. Persistence errors are logged
// but never surfaced to callers so goal mutation never fails on IO problems.
func (h *GoalHook) persistLocked() {
	if h.storePath == "" {
		return
	}

	env := persistedGoal{Version: goalStoreVersion, Goal: h.goal}
	data, err := json.MarshalIndent(&env, "", "  ")
	if err != nil {
		if h.logger != nil {
			h.logger.Error(context.Background(), "goal.marshal_failed", observability.F("error", err.Error()))
		}
		return
	}

	dir := filepath.Dir(h.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		if h.logger != nil {
			h.logger.Error(context.Background(), "goal.mkdir_failed", observability.F("error", err.Error()))
		}
		return
	}

	// Atomic write: temp file then rename.
	tmp := h.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		if h.logger != nil {
			h.logger.Error(context.Background(), "goal.write_failed", observability.F("error", err.Error()))
		}
		return
	}
	if err := os.Rename(tmp, h.storePath); err != nil {
		os.Remove(tmp)
		if h.logger != nil {
			h.logger.Error(context.Background(), "goal.rename_failed", observability.F("error", err.Error()))
		}
	}
}

// Name returns the hook identifier
func (h *GoalHook) Name() string {
	return "goal"
}

// Priority returns the hook execution priority
func (h *GoalHook) Priority() int {
	return GoalHookPriority
}

// Filter returns true for AfterAgent / AgentStopped events.
// EmitAgentStopped fires EventAgentStopped ("agent.stopped"), so we match
// that rather than the SDK-level EventAfterAgent constant.
func (h *GoalHook) Filter(event hooks.Event) bool {
	return event.Type == string(hooks.EventAgentStopped) ||
		event.Type == string(hooks.EventAfterAgent)
}

// OnEvent evaluates the goal condition after each agent turn
func (h *GoalHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	goal := h.GetGoal()

	// Skip if no active goal
	if goal == nil || goal.State == GoalStateMet || goal.State == GoalStateCleared || goal.State == GoalStateImpossible {
		return hooks.Continue(), nil
	}

	// Prefer the complete turn transcript supplied by the runtime. A turn can
	// contain assistant tool calls and tool-result messages whose Content field is
	// empty; reconstructing from only prompt + final response hides the strongest
	// completion evidence from the evaluator.
	transcript, _ := event.Data["transcript"].(string)
	if strings.TrimSpace(transcript) == "" {
		if data, ok := event.Data["input"].(hooks.AfterAgentInput); ok {
			transcript = fmt.Sprintf("User: %s\n\nAssistant: %s", data.Prompt, data.PromptResponse)
		} else {
			prompt, _ := event.Data["prompt"].(string)
			response, _ := event.Data["prompt_response"].(string)
			if prompt == "" && response == "" {
				return hooks.Continue(), nil
			}
			transcript = fmt.Sprintf("User: %s\n\nAssistant: %s", prompt, response)
		}
	}

	// A runtime may explicitly mark a transcript as incomplete. Do not infer
	// this from transcript text: user input, source files, and tool output are
	// untrusted and may contain the same words.
	transcriptTruncated, _ := event.Data["transcript_truncated"].(bool)
	if transcriptTruncated {
		result := GoalResult{
			Ok:     false,
			Reason: "insufficient evidence in transcript",
		}
		h.updateGoal(result)
		return hooks.Continue(), nil
	}

	// Evaluate the goal
	result, err := h.evaluator.Evaluate(ctx, goal.Condition, transcript)
	if err != nil {
		// Error: goal evaluation failed
		return hooks.Continue(), nil
	}

	// Update goal state
	h.updateGoal(result)

	// If goal is met, stop execution
	if result.Ok {
		// Info: goal achieved
		return hooks.Block(fmt.Sprintf("Goal achieved: %s", result.Reason)), nil
	}

	return hooks.Continue(), nil
}

// SetGoal sets a new goal condition
func (h *GoalHook) SetGoal(condition string) error {
	// Validate trusted workspace
	if !h.isTrustedWorkspace() {
		return fmt.Errorf("/goal is only available in trusted workspaces")
	}

	// Validate hooks not restricted
	if h.areHooksRestricted() {
		return fmt.Errorf("/goal can't run while hooks are restricted")
	}

	// Validate condition length
	if len(condition) > MaxConditionLength {
		return fmt.Errorf("Goal condition is limited to %d characters (got %d)", MaxConditionLength, len(condition))
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.goal = &Goal{
		Condition:  condition,
		State:      GoalStateNotYetEvaluated,
		CreatedAt:  time.Now(),
		Iterations: 0,
	}

	// Persist the new goal so it survives restarts and conversation switches.
	h.persistLocked()

	// Emit telemetry: goal.set

	return nil
}

// Clear clears the current goal
func (h *GoalHook) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.goal != nil {
		h.goal.State = GoalStateCleared
		h.persistLocked()
		h.logger.Info(context.Background(), "goal.cleared", observability.F("condition", h.goal.Condition))
	}
}

// GetGoal returns the current goal
func (h *GoalHook) GetGoal() *Goal {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if h.goal == nil {
		return nil
	}

	// Return a copy to prevent external modification
	goalCopy := *h.goal
	return &goalCopy
}

// SetEvaluator sets a custom evaluator (for testing)
func (h *GoalHook) SetEvaluator(evaluator Evaluator) {
	h.evaluator = evaluator
}

// SetTrustedWorkspacePredicate sets the trusted workspace check (for testing)
func (h *GoalHook) SetTrustedWorkspacePredicate(pred func() bool) {
	h.isTrustedWorkspace = pred
}

// SetHooksRestrictedPredicate sets the hooks restricted check (for testing)
func (h *GoalHook) SetHooksRestrictedPredicate(pred func() bool) {
	h.areHooksRestricted = pred
}

// updateGoal updates the goal state based on evaluation result
func (h *GoalHook) updateGoal(result GoalResult) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.goal == nil {
		return
	}

	// Transition from not_yet_evaluated to active
	if h.goal.State == GoalStateNotYetEvaluated {
		h.goal.State = GoalStateActive
	}

	h.goal.LastReason = result.Reason

	if result.Ok {
		h.goal.State = GoalStateMet
		now := time.Now()
		h.goal.AchievedAt = &now
		h.logger.Info(context.Background(), "goal.achieved", observability.F("condition", h.goal.Condition), observability.F("iterations", h.goal.Iterations))
	} else if result.Impossible {
		h.goal.State = GoalStateImpossible
		h.logger.Info(context.Background(), "goal.failed", observability.F("condition", h.goal.Condition), observability.F("reason", "impossible"))
	} else {
		h.goal.State = GoalStateActive
		h.goal.Iterations++
	}

	// Persist the evaluated state so progress (iterations, met/failed) survives.
	h.persistLocked()
}

// Evaluate implements the Goal.Evaluate method for state transitions
func (g *Goal) Evaluate(result GoalResult) {
	// Transition from not_yet_evaluated to active
	if g.State == GoalStateNotYetEvaluated {
		g.State = GoalStateActive
	}

	g.LastReason = result.Reason

	if result.Ok {
		g.State = GoalStateMet
		now := time.Now()
		g.AchievedAt = &now
	} else if result.Impossible {
		g.State = GoalStateImpossible
	} else {
		g.State = GoalStateActive
		g.Iterations++
	}
}

// Clear clears the goal
func (g *Goal) Clear() {
	g.State = GoalStateCleared
}
