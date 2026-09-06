package hooks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"time"
)

// debugLogFile path for hook tracing
const debugLogPath = "/tmp/hooks-debug.log"

// logDebug writes a debug message to the log file
func logDebug(format string, args ...any) {
	f, err := os.OpenFile(debugLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "["+time.Now().Format("15:04:05.000")+"] "+format+"\n", args...)
}

// Executor handles the actual hook execution pipeline
type Executor struct {
	manager *Manager
	config  ManagerConfig
}

// NewExecutor creates a new hook executor
func NewExecutor(manager *Manager, config ManagerConfig) *Executor {
	return &Executor{
		manager: manager,
		config:  config,
	}
}

// Execute runs hooks for an event in priority order
func (e *Executor) Execute(ctx context.Context, event Event, hooks []Hook) (*ExecutionResult, error) {
	result := &ExecutionResult{
		FinalEvent:    &event,
		HooksExecuted: 0,
		HookOutputs:   []HookOutput{},
	}

	currentEvent := &event
	startTime := time.Now()
	if event.Type == EventMessageAfterReceive {
		if role, _ := event.Data["role"].(string); role == "user" {
			DefaultMetaNudgeBudget().RecordUserTurn(NudgeSessionID(ctx, event))
		}
	}

	for _, hook := range hooks {
		// Check filter (already filtered in getApplicableHooks, but double-check)
		if !hook.Filter(*currentEvent) {
			continue
		}

		// Execute with timeout and recovery
		hookResult, err := e.executeWithRecovery(ctx, hook, *currentEvent)

		// Update hook stats
		e.updateHookStats(hook.Name(), hookResult, err, time.Since(startTime))

		if err != nil {
			// Session shutdown propagates context.Canceled to every in-flight
			// hook simultaneously; emitting one system-reminder per hook produced
			// a noise cascade at session end. Skip recording these benign cancels
			// — but keep deadline-exceeded timeouts (they're real faults) and all
			// other errors, which remain visible for debugging.
			if errors.Is(err, context.Canceled) && ctx.Err() == context.Canceled {
				e.handleHookError(hook, err)
				result.HooksExecuted++
				continue
			}
			// Log error but continue to next hook
			e.handleHookError(hook, err)
			result.HooksExecuted++
			// BUG FIX (previously: the HookResult returned alongside err was
			// discarded entirely by executeWithRecovery, so a hook that
			// returned (HookResult{Action: ActionBlock}, err) — "block this,
			// and here is why" — silently downgraded to "log the error and
			// continue to the next hook", never actually blocking. A hook
			// timeout/panic/cancel still yields a zero-value HookResult (see
			// executeWithRecovery), whose Action is the zero value of Action,
			// NOT ActionBlock, so this branch only fires for a hook that
			// itself explicitly chose to block.
			if hookResult.Action == ActionBlock {
				result.Blocked = true
				result.BlockedBy = hook.Name()
				reason := hookResult.Message
				if reason == "" {
					reason = err.Error()
				}
				result.BlockReason = reason
				result.ExecutionTimeMS = time.Since(startTime).Milliseconds()
				result.HookOutputs = append(result.HookOutputs, HookOutput{
					HookName: hook.Name(),
					Output:   hookResult.Message,
					Success:  false,
					Error:    reason,
				})
				// Matches the plain ActionBlock case below: blocking is
				// signaled via result.Blocked, not via Execute()'s own
				// returned error — callers (EmitWithResult) treat a non-nil
				// Execute() error as an executor-level failure, not a hook
				// decision, and that contract must not change here.
				return result, nil
			}
			// Capture error output
			result.HookOutputs = append(result.HookOutputs, HookOutput{
				HookName: hook.Name(),
				Output:   err.Error(),
				Success:  false,
				Error:    err.Error(),
			})
			continue
		}

		result.HooksExecuted++

		// Capture hook output (stdout/stderr from shell hooks OR HookResult.Message)
		hookOutput := HookOutput{
			HookName: hook.Name(),
			Output:   hookResult.Message,
			Success:  true,
		}
		// DEBUG: Log the message flow
		logDebug("executor: hook=%s action=%s message_len=%d output=%q",
			hook.Name(), hookResult.Action.String(), len(hookResult.Message), hookResult.Message)

		// Process result
		switch hookResult.Action {
		case ActionContinue:
			// Capture output and continue to next hook
			result.HookOutputs = append(result.HookOutputs, hookOutput)
			continue

		case ActionBlock:
			// Stop processing
			result.Blocked = true
			result.BlockedBy = hook.Name()
			result.BlockReason = hookResult.Message
			result.ExecutionTimeMS = time.Since(startTime).Milliseconds()
			hookOutput.Success = false
			hookOutput.Error = hookResult.Message
			result.HookOutputs = append(result.HookOutputs, hookOutput)
			return result, nil

		case ActionModify:
			// Update event for next hook
			if hookResult.ModifiedEvent == nil {
				e.handleHookError(hook, fmt.Errorf("hook returned ActionModify but ModifiedEvent is nil"))
				continue
			}
			currentEvent = hookResult.ModifiedEvent
			result.Modified = true
			result.FinalEvent = currentEvent
			result.HookOutputs = append(result.HookOutputs, hookOutput)
		}
	}

	result.ExecutionTimeMS = time.Since(startTime).Milliseconds()
	return result, nil
}

// executeWithRecovery runs a hook with panic recovery and timeout
func (e *Executor) executeWithRecovery(ctx context.Context, hook Hook, event Event) (result HookResult, err error) {
	// Generate unique hook ID for correlation
	// Spec: 020-hook-execution-realtime
	hookID := GenerateHookID()
	startTime := time.Now()
	startedAtMs := startTime.UnixMilli()

	// Extract tool name from event if available
	toolName := ""
	if tn, ok := event.Data["tool_name"].(string); ok {
		toolName = tn
	}

	// Extract phase from event type
	phase := "before"
	if event.Type == EventToolAfterExecute {
		phase = "after"
	}

	// Emit "started" event via real-time callback
	// Spec: 020-hook-execution-realtime
	if e.config.RealtimeCallback != nil {
		e.config.RealtimeCallback(HookStartedUpdate{
			HookID:            hookID,
			HookName:          hook.Name(),
			ToolName:          toolName,
			Phase:             phase,
			StartedAt:         startedAtMs,
			TimeoutConfigured: e.config.MaxExecutionTime,
			WorkingDir:        "", // Will be set by shell hook if available
		})
	}

	// Panic recovery
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("hook %s panicked: %v\nstack:\n%s", hook.Name(), r, string(stack))
			// Emit "completed" event on panic
			if e.config.RealtimeCallback != nil {
				e.config.RealtimeCallback(HookCompletedUpdate{
					HookID:            hookID,
					HookName:          hook.Name(),
					ToolName:          toolName,
					Phase:             phase,
					StartedAt:         startedAtMs,
					Success:           false,
					Error:             err.Error(),
					Duration:          time.Since(startTime),
					TimeoutConfigured: e.config.MaxExecutionTime,
				})
			}
		}
	}()

	// Apply timeout from config
	ctx, cancel := context.WithTimeout(ctx, e.config.MaxExecutionTime)
	defer cancel()

	// Inject real-time callback into context for hooks that support streaming
	// Spec: 020-hook-execution-realtime
	if e.config.RealtimeCallback != nil {
		ctx = ContextWithRealtimeCallback(ctx, e.config.RealtimeCallback, hookID, hook.Name())
	}

	// Execute hook in goroutine
	// hookOutcome carries BOTH the HookResult and the error a hook returned,
	// instead of the mutually-exclusive resultChan/errChan pair this used to
	// be. A hook is free to return (HookResult{Action: ActionBlock, ...}, err)
	// to block AND explain why via a Go error in the same call — e.g. "block
	// this, validation failed: <err>". The old two-channel design silently
	// discarded the HookResult the instant err was non-nil, so a hook that
	// legitimately intended to block never actually blocked; the error alone
	// reached Execute(), which has no Action to act on. See the caller
	// (Execute, below) for where the preserved Action is now honored.
	type hookOutcome struct {
		result HookResult
		err    error
	}
	outcomeChan := make(chan hookOutcome, 1)

	go func() {
		// Another panic recovery inside goroutine
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				outcomeChan <- hookOutcome{err: fmt.Errorf("hook %s panicked in goroutine: %v\nstack:\n%s",
					hook.Name(), r, string(stack))}
			}
		}()

		res, e := hook.OnEvent(ctx, event)
		outcomeChan <- hookOutcome{result: res, err: e}
	}()

	// Wait for result or timeout
	select {
	case outcome := <-outcomeChan:
		result = outcome.result
		err = outcome.err
		if err == nil {
			// Emit "completed" event on success
			// Spec: 020-hook-execution-realtime
			if e.config.RealtimeCallback != nil {
				e.config.RealtimeCallback(HookCompletedUpdate{
					HookID:            hookID,
					HookName:          hook.Name(),
					ToolName:          toolName,
					Phase:             phase,
					StartedAt:         startedAtMs,
					Success:           result.Action != ActionBlock,
					Output:            result.Message,
					Blocked:           result.Action == ActionBlock,
					Duration:          time.Since(startTime),
					TimeoutConfigured: e.config.MaxExecutionTime,
				})
			}
			return result, nil
		}
		// Emit "completed" event on error. result may still carry a real
		// Action (see hookOutcome doc above) — Execute() is responsible for
		// honoring ActionBlock even though err is also set; this callback is
		// purely observational telemetry and does not decide that.
		if e.config.RealtimeCallback != nil {
			e.config.RealtimeCallback(HookCompletedUpdate{
				HookID:            hookID,
				HookName:          hook.Name(),
				ToolName:          toolName,
				Phase:             phase,
				StartedAt:         startedAtMs,
				Success:           false,
				Error:             err.Error(),
				Blocked:           result.Action == ActionBlock,
				Duration:          time.Since(startTime),
				TimeoutConfigured: e.config.MaxExecutionTime,
			})
		}
		return result, err
	case <-ctx.Done():
		// Distinguish real hook timeouts from benign session-shutdown cancels.
		// Session shutdown causes every in-flight hook to receive context.Canceled
		// simultaneously, producing a cascade of identical "context cancelled"
		// system-reminders that buried real failures. A timeout is a real fault
		// and must still surface as an error; a plain Canceled is reported at
		// debug level with a neutral sentinel so callers can suppress emission.
		if ctx.Err() == context.DeadlineExceeded {
			errMsg := fmt.Sprintf("hook %s timed out after %v", hook.Name(), e.config.MaxExecutionTime)
			if e.config.RealtimeCallback != nil {
				e.config.RealtimeCallback(HookCompletedUpdate{
					HookID:            hookID,
					HookName:          hook.Name(),
					ToolName:          toolName,
					Phase:             phase,
					StartedAt:         startedAtMs,
					Success:           false,
					Error:             errMsg,
					Duration:          time.Since(startTime),
					TimeoutConfigured: e.config.MaxExecutionTime,
				})
			}
			return HookResult{}, fmt.Errorf("%s", errMsg)
		}
		// Plain cancellation: session is winding down. Return the canonical
		// context error so callers can .Is(err, context.Canceled) and skip
		// emitting a per-hook reminder.
		return HookResult{}, ctx.Err()
	}
}

// handleHookError processes hook errors
func (e *Executor) handleHookError(hook Hook, err error) {
	if e.config.ErrorHandler != nil {
		e.config.ErrorHandler(hook.Name(), err)
	}

	// Update error stats
	e.manager.mu.Lock()
	e.manager.stats.TotalErrors++

	// Find and update hook entry stats
	entry := e.manager.findEntry(hook.Name())
	if entry != nil {
		entry.Stats.ErrorCount++
	}
	e.manager.mu.Unlock()
}

// updateHookStats updates statistics for a hook
func (e *Executor) updateHookStats(hookName string, result HookResult, err error, duration time.Duration) {
	e.manager.mu.Lock()
	defer e.manager.mu.Unlock()

	entry := e.manager.findEntry(hookName)
	if entry == nil {
		return
	}

	entry.Stats.ExecutionCount++
	entry.Stats.TotalTimeNS += duration.Nanoseconds()
	entry.Stats.LastExecutedAt = time.Now()

	if err != nil {
		entry.Stats.ErrorCount++
		return
	}

	switch result.Action {
	case ActionBlock:
		entry.Stats.BlockedCount++
	case ActionModify:
		entry.Stats.ModifiedCount++
	}
}

// ExecuteAsync executes hooks asynchronously (fire-and-forget)
// Useful for non-critical hooks like logging/metrics
func (e *Executor) ExecuteAsync(ctx context.Context, event Event, hooks []Hook) {
	go func() {
		// Use background context to avoid cancellation
		bgCtx := context.Background()

		// Copy trace ID if available
		if traceID := event.TraceID; traceID != "" {
			// Could inject trace context here if needed
			_ = traceID
		}

		_, _ = e.Execute(bgCtx, event, hooks)
	}()
}

// ExecuteWithCallback executes hooks and calls callback with result
func (e *Executor) ExecuteWithCallback(ctx context.Context, event Event, hooks []Hook,
	callback func(*ExecutionResult, error)) {
	go func() {
		result, err := e.Execute(ctx, event, hooks)
		callback(result, err)
	}()
}

// GetAverageExecutionTime returns average execution time for a hook
func (e *Executor) GetAverageExecutionTime(hookName string) time.Duration {
	e.manager.mu.RLock()
	defer e.manager.mu.RUnlock()

	entry := e.manager.findEntry(hookName)
	if entry == nil || entry.Stats.ExecutionCount == 0 {
		return 0
	}

	avgNS := entry.Stats.TotalTimeNS / entry.Stats.ExecutionCount
	return time.Duration(avgNS)
}

// GetHookStats returns statistics for a specific hook
func (e *Executor) GetHookStats(hookName string) (*HookStats, error) {
	e.manager.mu.RLock()
	defer e.manager.mu.RUnlock()

	entry := e.manager.findEntry(hookName)
	if entry == nil {
		return nil, fmt.Errorf("hook %s not found", hookName)
	}

	// Return copy of stats
	statsCopy := *entry.Stats
	return &statsCopy, nil
}
