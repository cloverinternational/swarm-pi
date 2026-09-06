package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/filetracker"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// BuildCompactionContext assembles the full compaction context from the agent's runtime state.
// This includes file access records, todos (from request context), mode state, and MCP servers.
// The returned context is used by the compaction service to create intelligent handoff summaries.
//
// This method also automatically syncs tasks to disk before building the context,
// ensuring no task state is lost during compaction.
func (a *Agent) BuildCompactionContext() (*compaction.CompactionContext, error) {
	// Auto-sync tasks to disk before compaction
	if store := a.TaskStore(); store != nil {
		if err := store.Save(); err != nil {
			// FAIL FAST: Don't proceed with compaction if we can't save tasks
			a.logger.Error(context.Background(), "agent.compaction.sync_tasks_failed",
				observability.F("error", err.Error()),
				observability.F("impact", "compaction aborted to prevent task loss"))
			return nil, fmt.Errorf("task store sync failed, compaction aborted: %w", err)
		}
	}
	ctx := &compaction.CompactionContext{}
	// Get file access records from the file tracker
	if tracker := a.FileTracker(); tracker != nil {
		records := tracker.GetRecords()
		for _, r := range records {
			if !r.WriteAt.IsZero() {
				ctx.ModifiedFiles = append(ctx.ModifiedFiles, r.Path)
			} else {
				ctx.ReadFiles = append(ctx.ReadFiles, r.Path)
			}
		}
	}
	// Get tasks from persistent task store (primary source)
	if store := a.TaskStore(); store != nil {
		for _, task := range store.GetActiveTasks() {
			ctx.ActiveTodos = append(ctx.ActiveTodos, compaction.Todo{
				Content:    task.Subject,
				Status:     string(task.Status),
				ActiveForm: task.ActiveForm,
				DependsOn:  task.DependsOn,
			})
		}
		for _, task := range store.GetCompletedTasks() {
			ctx.CompletedTodos = append(ctx.CompletedTodos, compaction.Todo{
				Content:    task.Subject,
				Status:     string(task.Status),
				ActiveForm: task.ActiveForm,
				DependsOn:  task.DependsOn,
			})
		}
	}
	// Extract mode and MCP state from request context
	a.mu.RLock()
	reqCtx := a.requestContext
	a.mu.RUnlock()
	if reqCtx != nil {
		// Get current mode
		if mode, ok := reqCtx["mode"].(string); ok {
			ctx.CurrentMode = mode
		}
		// Get active MCP servers
		if servers, ok := reqCtx["mcp_servers"].([]string); ok {
			ctx.ActiveMCPServers = servers
		}
		// Get recent MCP tools
		if tools, ok := reqCtx["mcp_tools"].([]string); ok {
			ctx.RecentMCPTools = tools
		}
	}
	// Get system prompt from definition
	if a.definition != nil && a.definition.SystemPrompt != "" {
		ctx.SystemPrompt = a.definition.SystemPrompt
	}
	return ctx, nil
}

// BuildContextWithTracker wraps the given context with the agent's file tracker.
// This allows tools to record file access via filetracker.RecordAccess.
func (a *Agent) BuildContextWithTracker(ctx context.Context) context.Context {
	if tracker := a.FileTracker(); tracker != nil {
		return filetracker.WithRecorder(ctx, tracker)
	}
	return ctx
}

// getContextWindow returns the context window limit for the current agent's model.
// Priority: 1. user-configured value (always wins), 2. provider capabilities, 3. safe default.
func (a *Agent) getContextWindow() int {
	a.mu.RLock()
	configured := a.configuredContextWindow
	prov := a.provider
	a.mu.RUnlock()
	// User config wins — if explicitly set, use it unconditionally
	if configured > 0 {
		return configured
	}
	// Provider capabilities are the next source
	if prov != nil {
		if limit := prov.Capabilities().MaxContextWindow; limit > 0 {
			return provider.ClampContextWindow(limit)
		}
	}
	return provider.DefaultUnknownContextWindow
}

const (
	outputBudgetCap         = 20_000 // cap on output token reservation when computing effective input window
	compactionMargin        = 13_000 // safety margin subtracted from effective window for compaction threshold
	blockingLimitMargin     = 3_000  // hard stop: refuse API calls when input is within this many tokens of max
	minAutoCompactThreshold = 1_000  // floor for a user-configured absolute auto-compact threshold
)

// getEffectiveInputWindow returns the usable input budget after reserving space for output tokens.
// Mirrors Claude Code: effectiveWindow = contextWindow - min(maxOutputTokens, 20_000)
func (a *Agent) getEffectiveInputWindow() int {
	contextWindow := a.getContextWindow()
	maxOutput := 0
	if a.provider != nil {
		maxOutput = a.provider.Capabilities().MaxOutputTokens
	}
	reservation := min(maxOutput, outputBudgetCap)
	return contextWindow - reservation
}

// getAutoCompactThreshold returns the input-token count at which proactive
// compaction fires. The configured descriptor is deliberately resolved here,
// against the active provider/model, instead of being prematurely converted by
// a client that may not yet know the real context window.
func (a *Agent) getAutoCompactThreshold() int {
	def := a.getEffectiveInputWindow() - compactionMargin
	a.mu.RLock()
	cfg := a.autoCompactionConfig
	proactiveThreshold := a.proactiveSummarizeThreshold
	a.mu.RUnlock()

	userTokens := 0
	switch cfg.Threshold.Mode {
	case AutoCompactionThresholdPercent:
		if cfg.Threshold.Value > 0 && cfg.Threshold.Value <= 1 {
			userTokens = int(float64(a.getContextWindow()) * cfg.Threshold.Value)
		}
	case AutoCompactionThresholdFixedTokens:
		if cfg.Threshold.Value > 0 {
			userTokens = int(cfg.Threshold.Value)
		}
	}

	// Compatibility precedence after the canonical descriptor: the partial
	// absolute-token migration, then the original percentage field.
	if userTokens <= 0 && cfg.AutoCompactThresholdTokens > 0 {
		userTokens = cfg.AutoCompactThresholdTokens
	}
	if userTokens <= 0 && cfg.AutoCompactionThresholdPercent > 0 &&
		cfg.AutoCompactionThresholdPercent <= 1 {
		userTokens = int(float64(a.getContextWindow()) * cfg.AutoCompactionThresholdPercent)
	}
	if userTokens <= 0 {
		userTokens = max(def, minAutoCompactThreshold)
	} else if userTokens > def {
		userTokens = max(def, minAutoCompactThreshold)
	} else if userTokens < minAutoCompactThreshold {
		userTokens = minAutoCompactThreshold
	}

	// The proactive threshold is opt-in and can only make compaction happen
	// earlier. Invalid values and values at/above the normal threshold leave the
	// existing behavior unchanged.
	if proactiveThreshold > 0 && proactiveThreshold < 1 {
		proactiveTokens := max(
			int(float64(a.getContextWindow())*proactiveThreshold),
			minAutoCompactThreshold,
		)
		if proactiveTokens < userTokens {
			return proactiveTokens
		}
	}
	return userTokens
}

// getBlockingLimit returns the token count above which API calls must not be made.
// blockingLimit = contextWindow - blockingLimitMargin
func (a *Agent) getBlockingLimit() int {
	return max(a.getEffectiveInputWindow()-blockingLimitMargin, minAutoCompactThreshold)
}

// seedTokenCountFromHistory initializes a.inputTokens from the last API-reported
// usage in the message history. Mirrors Claude Code's kV(messages) backward scan.
// Called once when a conversation is resumed so the compaction check has a valid
// baseline before the first API call completes.
func (a *Agent) seedTokenCountFromHistory(messages []*conversation.Message) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		// Never scan past a compaction summary/handoff boundary. Any assistant
		// usage recorded before the most recent compaction reflects the
		// PRE-compaction context size; seeding from it re-trips the blocking
		// guard forever after a successful compaction (269030 >= 269000). The
		// post-boundary slice legitimately carries no usage yet, so we stop and
		// leave a.inputTokens at zero until the next API call populates it.
		if isCompactionBoundaryMessage(msg) {
			return
		}
		if msg.Role == conversation.RoleAssistant && msg.Tokens != nil {
			if size := msg.Tokens.InputContextSize(); size > 0 {
				a.inputTokens = size
				a.outputTokens = msg.Tokens.Output
				return
			}
		}
	}
	// No API-reported usage found — leave at zero; first turn will populate.
}

// isCompactionBoundaryMessage reports whether msg is a compaction summary /
// handoff message that marks the start of a post-compaction active context.
// Everything positioned before such a message is pre-compaction and must not
// seed the live input-token counter. Detection is conservative: an explicit
// metadata flag, or the canonical summary content prefix emitted by
// compaction.Service when it builds the post-compaction handoff.
func isCompactionBoundaryMessage(msg *conversation.Message) bool {
	if msg == nil {
		return false
	}
	if msg.Metadata != nil {
		for _, key := range []string{"compaction_boundary", "compaction_summary", "is_compaction_handoff"} {
			if b, ok := msg.Metadata[key].(bool); ok && b {
				return true
			}
		}
	}
	if msg.Content != "" && strings.HasPrefix(msg.Content, compaction.SummaryPrefix) {
		return true
	}
	return false
}

// ResetContextTokens authoritatively sets the live input-token counter after
// a compaction commit, so the blocking guard/threshold never evaluate a stale
// pre-compaction total. Mirrors Crush resetting PromptTokens=0 post-summary.
func (a *Agent) ResetContextTokens(estimate int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if estimate < 0 {
		estimate = 0
	}
	a.inputTokens = estimate
}

// State returns the agent's current state.
func (a *Agent) State() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// Definition returns a copy of the agent's definition.
//
// The clone is taken under a.mu.RLock because definition fields are no longer
// write-once: SetToolHints swaps ToolHints at runtime under a.mu.Lock (the
// client's closed harness path widens hints as MCP servers register tools), so
// an unlocked Clone would race that write. Cloning under the read lock makes
// every caller observe either the complete old definition or the complete new
// one, never a half-applied state.
func (a *Agent) Definition() *Definition {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.definition.Clone()
}

// Stats returns current agent runtime statistics.
func (a *Agent) Stats() AgentStats {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return AgentStats{
		State:        a.state,
		TurnCount:    a.turnCount,
		InputTokens:  a.inputTokens,
		OutputTokens: a.outputTokens,
		// TotalTokens is derived, not accumulated: current context size + cumulative output.
		// This avoids the double-count that arose from summing resp.Usage.Total across turns.
		TotalTokens:    a.inputTokens + a.outputTokens,
		TotalCost:      a.totalCost,
		StartedAt:      a.startedAt,
		LastActivityAt: a.lastActivityAt,
	}
}

// AgentStats contains agent runtime statistics.
type AgentStats struct {
	State          State
	TurnCount      int
	InputTokens    int
	OutputTokens   int
	TotalTokens    int
	TotalCost      float64
	StartedAt      time.Time
	LastActivityAt time.Time
}

// Context keys for passing agent capabilities to tools.
type contextKey string

const (
	ctxKeyIntermediateCallback contextKey = "agent.intermediate_callback"
	ctxKeyHooksManager         contextKey = "agent.hooks_manager"
	ctxKeySubAgent             contextKey = "agent.sub_agent" // marks sub-agent execution (skips steering)
)

// WithIntermediateCallback adds the intermediate callback to the context.
func WithIntermediateCallback(ctx context.Context, cb IntermediateCallback) context.Context {
	return context.WithValue(ctx, ctxKeyIntermediateCallback, cb)
}

// GetIntermediateCallback retrieves the intermediate callback from the context.
func GetIntermediateCallback(ctx context.Context) IntermediateCallback {
	if cb, ok := ctx.Value(ctxKeyIntermediateCallback).(IntermediateCallback); ok {
		return cb
	}
	return nil
}

// WithHooksManager adds the hooks manager to the context.
func WithHooksManager(ctx context.Context, hm HooksManager) context.Context {
	return context.WithValue(ctx, ctxKeyHooksManager, hm)
}

// GetHooksManager retrieves the hooks manager from the context.
func GetHooksManager(ctx context.Context) HooksManager {
	if hm, ok := ctx.Value(ctxKeyHooksManager).(HooksManager); ok {
		return hm
	}
	return nil
}

// WithSubAgent marks the context as running inside a sub-agent.
// Steering hooks check this to skip evaluation for sub-agent tool calls.
func WithSubAgent(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySubAgent, true)
}

// IsSubAgent reports whether the context belongs to a sub-agent.
func IsSubAgent(ctx context.Context) bool {
	_, ok := ctx.Value(ctxKeySubAgent).(bool)
	return ok
}

// ── Context pressure ──────────────────────────────────────────────────────────

// checkContextPressure runs the blocking limit guard and auto-compact threshold
// check before every API call. It may mutate *messages in-place via CompactFunc.
// compactionNotified is a per-Execute() flag; pass &false at the start of each
// execution so the threshold notification fires at most once per user turn.
// At the hard limit it always returns a typed error unless in-agent compaction
// succeeds. This prevents a known compaction failure from being obscured by a
// second oversized provider request.
func (a *Agent) checkContextPressure(ctx context.Context, messages *[]*conversation.Message, currentTokens int, compactionNotified *bool) (bool, error) {
	a.mu.RLock()
	cfg := a.autoCompactionConfig
	a.mu.RUnlock()

	blockingLimit := a.getBlockingLimit()
	contextLimit := a.getContextWindow()
	emitNeeded := func(threshold int) {
		if *compactionNotified {
			return
		}
		*compactionNotified = true
		percentUsed := 0.0
		if contextLimit > 0 {
			percentUsed = float64(currentTokens) / float64(contextLimit)
		}
		a.callIntermediateCallback(ctx, CompactionNeededUpdate{
			CurrentTokens: currentTokens,
			Threshold:     threshold,
			ContextLimit:  contextLimit,
			PercentUsed:   percentUsed,
		})
	}

	// --- Hard blocking limit ---
	if currentTokens > 0 && currentTokens >= blockingLimit {
		if cfg.EnableAutoCompaction {
			emitNeeded(a.getAutoCompactThreshold())
		}
		if cfg.CompactFunc == nil {
			return false, &ContextPressureError{
				Stage:         "context_limit_guard",
				CurrentTokens: currentTokens,
				Threshold:     blockingLimit,
				ContextLimit:  contextLimit,
			}
		}

		if _, err := a.runInLoopCompaction(ctx, messages, currentTokens, blockingLimit, cfg.CompactFunc); err != nil {
			a.callIntermediateCallback(ctx, CompactionFailedUpdate{
				TokensBefore: currentTokens,
				Error:        err.Error(),
				Fatal:        true,
				Turn:         a.turnCount,
			})
			return false, &ContextPressureError{
				Stage:         "compaction_failed",
				CurrentTokens: currentTokens,
				Threshold:     blockingLimit,
				ContextLimit:  contextLimit,
				Cause:         err,
			}
		}
		*compactionNotified = false
		return true, nil
	}

	// --- Proactive threshold ---
	if !cfg.EnableAutoCompaction {
		return false, nil
	}
	threshold := a.getAutoCompactThreshold()
	if currentTokens <= 0 || currentTokens < threshold || *compactionNotified {
		return false, nil
	}
	a.logger.Info(ctx, "agent.compaction_needed",
		observability.F("current_tokens", currentTokens),
		observability.F("threshold", threshold),
		observability.F("context_limit", contextLimit),
	)
	if cfg.CompactFunc == nil {
		emitNeeded(threshold)
		return false, nil
	}

	emitNeeded(threshold)
	if _, err := a.runInLoopCompaction(ctx, messages, currentTokens, blockingLimit, cfg.CompactFunc); err != nil {
		a.logger.Warn(ctx, "agent.compaction_failed",
			observability.F("error", err.Error()),
			observability.F("fatal", false),
		)
		a.callIntermediateCallback(ctx, CompactionFailedUpdate{
			TokensBefore: currentTokens,
			Error:        err.Error(),
			Fatal:        false,
			Turn:         a.turnCount,
		})
		*compactionNotified = false
		return false, nil
	}
	*compactionNotified = false
	return true, nil
}

// checkHardContextLimit validates a fully assembled provider request without
// attempting another compaction. Callers use it after rebuilding a request from
// an already-compacted generation so request-dependent system content cannot
// bypass the blocking guard.
func (a *Agent) checkHardContextLimit(currentTokens int, stage string) error {
	blockingLimit := a.getBlockingLimit()
	if currentTokens <= 0 || currentTokens < blockingLimit {
		return nil
	}
	return &ContextPressureError{
		Stage:         stage,
		CurrentTokens: currentTokens,
		Threshold:     blockingLimit,
		ContextLimit:  a.getContextWindow(),
	}
}

func (a *Agent) runInLoopCompaction(
	ctx context.Context,
	messages *[]*conversation.Message,
	tokensBefore int,
	blockingLimit int,
	compactFunc func(context.Context, []*conversation.Message, *compaction.CompactionContext) ([]*conversation.Message, error),
) (int, error) {
	compCtx, err := a.BuildCompactionContext()
	if err != nil {
		return 0, err
	}
	original, err := cloneConversationMessages(*messages)
	if err != nil {
		return 0, err
	}
	candidate, err := cloneConversationMessages(original)
	if err != nil {
		return 0, err
	}
	compacted, err := compactFunc(ctx, candidate, compCtx)
	if err != nil {
		return 0, err
	}
	estimated, err := validateCompactedMessages(compacted, original, tokensBefore, blockingLimit)
	if err != nil {
		return 0, err
	}
	if err := a.persistInLoopCompaction(ctx, compacted, estimated); err != nil {
		return 0, err
	}
	*messages = compacted
	a.mu.Lock()
	a.inputTokens = estimated
	a.lastRequestEstimate = 0
	a.mu.Unlock()
	a.callIntermediateCallback(ctx, CompactionDoneUpdate{
		TokensBefore: tokensBefore,
		TokensAfter:  estimated,
		Turn:         a.turnCount,
	})
	return estimated, nil
}

func cloneConversationMessages(messages []*conversation.Message) ([]*conversation.Message, error) {
	if messages == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return nil, fmt.Errorf("clone compaction messages: %w", err)
	}
	var cloned []*conversation.Message
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, fmt.Errorf("clone compaction messages: %w", err)
	}
	return cloned, nil
}

func (a *Agent) persistInLoopCompaction(ctx context.Context, compacted []*conversation.Message, contextSize int) error {
	a.mu.RLock()
	callback := a.compactionPersistCallback
	convID := a.conversationID
	a.mu.RUnlock()
	if callback == nil {
		return nil
	}
	summary := ""
	if len(compacted) > 0 && compacted[0] != nil {
		summary = compacted[0].Content
	}
	if err := callback(ctx, convID, compacted, summary, contextSize); err != nil {
		a.logger.Warn(ctx, "agent.compaction_persist_failed",
			observability.F("conversation_id", convID),
			observability.F("error", err.Error()),
		)
		return fmt.Errorf("persist compacted generation: %w", err)
	}
	return nil
}

func validateCompactedMessages(messages, original []*conversation.Message, before, blockingLimit int) (int, error) {
	if len(messages) == 0 {
		return 0, fmt.Errorf("compactor returned no messages")
	}
	if reflect.DeepEqual(messages, original) {
		return 0, fmt.Errorf("compactor returned the unchanged context")
	}
	estimatedTranscript := compaction.EstimateMessagesTokens(messages)
	if estimatedTranscript <= 0 {
		return 0, fmt.Errorf("compactor returned an empty context")
	}
	originalTranscript := compaction.EstimateMessagesTokens(original)
	fixedOverhead := max(0, before-originalTranscript)
	estimatedTotal := fixedOverhead + estimatedTranscript
	if before > 0 && estimatedTotal >= before {
		return 0, fmt.Errorf("compactor did not reduce context: %d >= %d tokens", estimatedTotal, before)
	}
	if estimatedTotal >= blockingLimit {
		return 0, fmt.Errorf("compacted context remains unsafe: %d >= %d tokens", estimatedTotal, blockingLimit)
	}
	return estimatedTotal, nil
}
