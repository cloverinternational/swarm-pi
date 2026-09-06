// Package tools provides parallel tool execution runtime.
package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ToolCallRuntime manages concurrent tool execution with safety guarantees.
// It coordinates parallel execution of multiple tools while ensuring that:
// 1. Only one parallel batch runs at a time (prevents resource exhaustion)
// 2. Parallel-safe tools execute concurrently within a batch
// 3. Non-parallel tools execute exclusively (sequential)
//
// Based on Codex's ToolCallRuntime architecture.
type ToolCallRuntime struct {
	// router provides tool routing and parallel capability detection
	router *ToolRouter

	// registry is the underlying tool registry for execution
	registry Registry

	// parallelExecution is a RWMutex that controls batch execution
	// - Read lock: multiple parallel-safe tools can execute concurrently
	// - Write lock: exclusive execution for non-parallel tools
	parallelExecution sync.RWMutex

	// logger for observability
	logger observability.Logger

	// tracer for distributed tracing
	tracer observability.Tracer
}

// Global pool manager for tool execution
var (
	globalPoolManager *pool.Manager
	poolManagerMu     sync.RWMutex
)

// SetGlobalPoolManager sets the global pool manager for tool execution
func SetGlobalPoolManager(pm *pool.Manager) {
	poolManagerMu.Lock()
	defer poolManagerMu.Unlock()
	globalPoolManager = pm
}

// getGlobalPoolManager returns the global pool manager
func getGlobalPoolManager() *pool.Manager {
	poolManagerMu.RLock()
	defer poolManagerMu.RUnlock()
	return globalPoolManager
}

// isSmallBatch determines if a batch is too small to benefit from pooling
func isSmallBatch(count int) bool {
	return count <= 2
}

// NewToolCallRuntime creates a new tool call runtime.
func NewToolCallRuntime(registry Registry, logger observability.Logger, tracer observability.Tracer) *ToolCallRuntime {
	// Create router from registry
	router := NewToolRouter(ToolRouterConfig{
		Registry: registry,
	})

	return &ToolCallRuntime{
		router:   router,
		registry: registry,
		logger:   logger,
		tracer:   tracer,
	}
}

// NewToolCallRuntimeWithMCP creates a new tool call runtime with MCP tools.
func NewToolCallRuntimeWithMCP(registry Registry, mcpTools map[string]Tool, logger observability.Logger, tracer observability.Tracer) *ToolCallRuntime {
	router := NewToolRouter(ToolRouterConfig{
		Registry: registry,
		MCPTools: mcpTools,
	})

	return &ToolCallRuntime{
		router:   router,
		registry: registry,
		logger:   logger,
		tracer:   tracer,
	}
}

// ExecuteBatch executes multiple tool calls with intelligent parallelization.
// It groups tools by parallel capability and executes them accordingly:
// - Parallel-safe tools run concurrently within the batch
// - Non-parallel tools run sequentially
//
// Returns all results in the same order as the input invocations.
// Partial failures are included in the results; use IsSuccess() to check.
//
// DUPLICATE-ID TOLERANCE: Some providers (and especially nested agents that
// proxy through other tool dispatchers) occasionally emit two tool_use blocks
// with the same `ID` in a single assistant turn — e.g. `functions.grep:N`
// where the suffix is a sequence counter that wraps. The original (pre-2026-05)
// implementation indexed by ID into a `map[string]int`, so the second
// duplicate overwrote the first slot and one of the two calls' results was
// permanently lost ("tool result missing for call X — possible duplicate
// tool call ID"). The model then retried the same parallel pattern next turn,
// burned tokens, and never recovered.
//
// We now defend against this in two layers:
//  1. **Internal positional indexing.** Each call is tracked by its position
//     in the input slice (`internalIdx`), not by `call.ID`. Result merging
//     uses this index, so collisions in the model-supplied ID can no longer
//     lose results.
//  2. **Stable per-batch ID rewriting (when needed).** If the parent context
//     requires unique IDs downstream (Anthropic wire protocol), we rewrite
//     duplicates to `<original>#dup<n>` so the executor and any callbacks
//     see a unique ID. The original ID is preserved on `Invocation.ID` for
//     audit/logging; the rewritten ID is on `internalID`.
//
// Net effect: an 8-way parallel batch with 3 colliding IDs now returns 8
// successful results instead of 5 successes + 3 "missing result" failures.
func (r *ToolCallRuntime) ExecuteBatch(ctx context.Context, calls []*ToolInvocation) ([]*ToolInvocationResult, error) {
	if len(calls) == 0 {
		return []*ToolInvocationResult{}, nil
	}

	// Start batch span
	ctx, batchSpan := r.tracer.StartSpan(ctx, "tool_batch.execute")
	defer batchSpan.End()

	batchSpan.SetAttribute("tool_count", len(calls))
	batchStart := time.Now()

	r.logger.Info(ctx, "tool_batch.starting",
		observability.F("tool_count", len(calls)))

	// ── Layer 1: Detect ID collisions up-front and log them for diagnosis. ──
	// We do NOT mutate call.ID here — downstream code (callbacks, traces, the
	// model's tool_result pairing) all rely on the original ID. We only need
	// unique POSITIONAL tracking, which we get for free from the input slice
	// order. Logging the collision makes it grep-able instead of mysteriously
	// dropping a result.
	seen := make(map[string]int, len(calls))
	collisionCount := 0
	for _, call := range calls {
		if call == nil {
			continue
		}
		if prev, ok := seen[call.ID]; ok {
			collisionCount++
			r.logger.Warn(ctx, "tool_batch.duplicate_id_detected",
				observability.F("call_id", call.ID),
				observability.F("first_index", prev),
				observability.F("duplicate_name", call.Name),
				observability.F("total_calls", len(calls)),
				observability.F("note", "tolerated_via_positional_indexing"),
			)
		} else {
			seen[call.ID] = 0
		}
	}
	if collisionCount > 0 {
		batchSpan.SetAttribute("duplicate_id_count", collisionCount)
	}

	// Group tools by parallel capability
	parallelCalls, exclusiveCalls := r.groupByParallelCapability(calls)

	batchSpan.SetAttribute("parallel_count", len(parallelCalls))
	batchSpan.SetAttribute("exclusive_count", len(exclusiveCalls))

	r.logger.Debug(ctx, "tool_batch.grouped",
		observability.F("parallel_count", len(parallelCalls)),
		observability.F("exclusive_count", len(exclusiveCalls)))

	// Create results slice with proper capacity
	results := make([]*ToolInvocationResult, len(calls))

	// ── Layer 2: Positional pointer index (collision-proof). ────────────
	// Map pointer→position so we can route each result to the right slot
	// regardless of whether the model's `ID` field is unique. ToolInvocation
	// pointers are unique by construction (each call is allocated in
	// agent_tools.go::buildToolInvocations as a separate value), so this is
	// the only fully-safe key. The old map[string]int lookup is gone.
	callPosByPtr := make(map[*ToolInvocation]int, len(calls))
	for i, call := range calls {
		callPosByPtr[call] = i
	}

	// Execute parallel-safe tools concurrently
	var parallelResults []*ToolInvocationResult
	if len(parallelCalls) > 0 {
		parallelResults = r.executeParallelGroup(ctx, parallelCalls)
	}

	// Execute exclusive tools sequentially
	var exclusiveResults []*ToolInvocationResult
	if len(exclusiveCalls) > 0 {
		exclusiveResults = r.executeExclusiveGroup(ctx, exclusiveCalls)
	}

	// Merge results back into original order via pointer identity.
	for _, result := range parallelResults {
		if result != nil && result.Invocation != nil {
			if idx, ok := callPosByPtr[result.Invocation]; ok {
				results[idx] = result
			}
		}
	}
	for _, result := range exclusiveResults {
		if result != nil && result.Invocation != nil {
			if idx, ok := callPosByPtr[result.Invocation]; ok {
				results[idx] = result
			}
		}
	}

	// Fill any nil slots. With positional indexing this should now be
	// genuinely impossible unless a tool returned nil from its execute path
	// without an error (a real bug, not an ID collision).
	for i, result := range results {
		if result == nil {
			err := sdkerr.Permanent(
				"tools.runtime.missing_result",
				fmt.Sprintf("tool '%s' (call %s, index %d) returned no result and no error — this is a tool-implementation bug, not an ID collision",
					calls[i].Name, calls[i].ID, i),
				sdkerr.WithOperation("tools.execute_batch"),
				sdkerr.WithComponent("tools.runtime"),
				sdkerr.WithTraceFromContext(ctx),
			)
			r.logger.Error(ctx, "tool_batch.missing_result_no_collision",
				observability.F("call_id", calls[i].ID),
				observability.F("tool_name", calls[i].Name),
				observability.F("index", i),
			)
			results[i] = &ToolInvocationResult{
				Invocation: calls[i],
				Result:     nil,
				Error:      err,
				Duration:   time.Since(batchStart),
				ExecutedAt: batchStart,
			}
		}
	}

	// Log batch completion
	batchDuration := time.Since(batchStart)
	successCount := 0
	failureCount := 0
	for _, result := range results {
		if result != nil && result.IsSuccess() {
			successCount++
		} else {
			failureCount++
		}
	}

	batchSpan.SetAttribute("duration_ms", batchDuration.Milliseconds())
	batchSpan.SetAttribute("success_count", successCount)
	batchSpan.SetAttribute("failure_count", failureCount)

	r.logger.Info(ctx, "tool_batch.completed",
		observability.F("duration_ms", batchDuration.Milliseconds()),
		observability.F("success_count", successCount),
		observability.F("failure_count", failureCount))

	return results, nil
}

// groupByParallelCapability separates calls into parallel-safe and exclusive groups.
func (r *ToolCallRuntime) groupByParallelCapability(calls []*ToolInvocation) ([]*ToolInvocation, []*ToolInvocation) {
	parallelCalls := make([]*ToolInvocation, 0)
	exclusiveCalls := make([]*ToolInvocation, 0)

	for _, call := range calls {
		if r.router.ToolSupportsParallel(call.Name) {
			parallelCalls = append(parallelCalls, call)
		} else {
			exclusiveCalls = append(exclusiveCalls, call)
		}
	}

	return parallelCalls, exclusiveCalls
}

// executeParallelGroup executes multiple tools concurrently with read lock.
// All tools in this group are parallel-safe and can run simultaneously.
func (r *ToolCallRuntime) executeParallelGroup(ctx context.Context, calls []*ToolInvocation) []*ToolInvocationResult {
	if len(calls) == 0 {
		return []*ToolInvocationResult{}
	}

	// Acquire read lock (allows multiple goroutines)
	r.parallelExecution.RLock()
	defer r.parallelExecution.RUnlock()

	ctx, span := r.tracer.StartSpan(ctx, "tool_batch.parallel_group")
	defer span.End()
	span.SetAttribute("tool_count", len(calls))

	r.logger.Debug(ctx, "tool_batch.parallel_group_starting",
		observability.F("tool_count", len(calls)))

	// Execute all tools concurrently using pool if available
	var wg sync.WaitGroup
	results := make([]*ToolInvocationResult, len(calls))

	// Check if we have a pool manager
	poolManager := getGlobalPoolManager()
	usePool := poolManager != nil && !isSmallBatch(len(calls))

	for i, call := range calls {
		wg.Add(1)
		idx, invocation := i, call // Capture loop variables

		if usePool {
			// Submit to pool
			err := poolManager.Submit(ctx, pool.PoolTypeTools, func() {
				defer wg.Done()
				results[idx] = r.executeSingle(ctx, invocation)
			})

			if err != nil {
				// Pool is full, fall back to goroutine
				r.logger.Warn(ctx, "tool_pool_full",
					observability.F("tool", invocation.Name))
				go func() {
					defer wg.Done()
					results[idx] = r.executeSingle(ctx, invocation)
				}()
			}
		} else {
			// Direct goroutine execution for small batches
			go func() {
				defer wg.Done()
				results[idx] = r.executeSingle(ctx, invocation)
			}()
		}
	}

	// Wait for all to complete, but never block the batch forever. A wedged
	// tool (e.g. a sub-agent whose update callback froze, or a tool blocked on
	// a stalled downstream) must not turn the whole parallel batch into an
	// infinite spinner with no surfaced error. We wait on the WaitGroup via a
	// done channel and apply a generous watchdog deadline; on timeout we
	// synthesize timeout results for the tools that never returned and log
	// exactly which ones, so the failure is observable instead of silent.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Normal completion.
	case <-ctx.Done():
		// Parent context cancelled/deadlined. Surface whatever finished and
		// mark the rest as cancelled so callers never see a nil result.
		r.fillUnfinishedResults(ctx, calls, results, ctx.Err())
		r.logger.Warn(ctx, "tool_batch.parallel_group_ctx_done",
			observability.F("tool_count", len(calls)),
			observability.F("error", ctx.Err().Error()))
	case <-time.After(parallelGroupWatchdog):
		stuck := r.fillUnfinishedResults(ctx, calls, results,
			fmt.Errorf("tool did not complete within %s (parallel batch watchdog)", parallelGroupWatchdog))
		r.logger.Error(ctx, "tool_batch.parallel_group_watchdog_timeout",
			observability.F("tool_count", len(calls)),
			observability.F("stuck_count", len(stuck)),
			observability.F("stuck_tools", strings.Join(stuck, ",")),
			observability.F("watchdog", parallelGroupWatchdog.String()))
	}

	r.logger.Debug(ctx, "tool_batch.parallel_group_completed",
		observability.F("tool_count", len(calls)))

	return results
}

// parallelGroupWatchdog bounds how long a parallel tool batch may wait for all
// its tools to finish before the batch gives up on the stragglers and surfaces
// a timeout. It is intentionally generous: legitimately long-running tools
// (sub-agents can run for many minutes) must complete normally. The watchdog
// exists only to convert a true deadlock — a goroutine that will NEVER call
// wg.Done() — from an infinite, error-less spinner into an observable failure.
const parallelGroupWatchdog = 15 * time.Minute

// fillUnfinishedResults populates any nil result slots (tools whose goroutine
// never returned) with a synthesized error result, so callers never observe a
// nil *ToolInvocationResult and the offending tools are named in the logs. It
// returns the IDs of the tools that were still unfinished.
func (r *ToolCallRuntime) fillUnfinishedResults(ctx context.Context, calls []*ToolInvocation, results []*ToolInvocationResult, cause error) []string {
	var stuck []string
	for i := range results {
		if results[i] != nil {
			continue
		}
		inv := calls[i]
		stuck = append(stuck, fmt.Sprintf("%s(%s)", inv.Name, inv.ID))
		err := sdkerr.Wrap(
			cause,
			"tools.runtime.tool_unfinished",
			sdkerr.WithOperation("tools.execute_parallel_group"),
			sdkerr.WithComponent("tools.runtime"),
			sdkerr.WithTraceFromContext(ctx),
			sdkerr.WithAttr("tool_name", inv.Name),
			sdkerr.WithAttr("tool_call_id", inv.ID),
		)
		results[i] = &ToolInvocationResult{
			Invocation: inv,
			Result:     nil,
			Error:      err,
			Duration:   0,
			ExecutedAt: time.Now(),
		}
	}
	return stuck
}

// executeExclusiveGroup executes tools sequentially with write lock.
// Tools in this group are not parallel-safe and must run one at a time.
func (r *ToolCallRuntime) executeExclusiveGroup(ctx context.Context, calls []*ToolInvocation) []*ToolInvocationResult {
	if len(calls) == 0 {
		return []*ToolInvocationResult{}
	}

	ctx, span := r.tracer.StartSpan(ctx, "tool_batch.exclusive_group")
	defer span.End()
	span.SetAttribute("tool_count", len(calls))

	r.logger.Debug(ctx, "tool_batch.exclusive_group_starting",
		observability.F("tool_count", len(calls)))

	results := make([]*ToolInvocationResult, len(calls))

	// Execute each tool exclusively (write lock per tool)
	for i, call := range calls {
		r.parallelExecution.Lock()
		results[i] = r.executeSingle(ctx, call)
		r.parallelExecution.Unlock()
	}

	r.logger.Debug(ctx, "tool_batch.exclusive_group_completed",
		observability.F("tool_count", len(calls)))

	return results
}

// executeSingle executes a single tool call and returns the result.
// This is the core execution method used by both parallel and exclusive groups.
func (r *ToolCallRuntime) executeSingle(ctx context.Context, invocation *ToolInvocation) *ToolInvocationResult {
	startTime := time.Now()

	// Create span for this tool call
	ctx, span := r.tracer.StartSpan(ctx, fmt.Sprintf("tool.%s", invocation.Name))
	defer span.End()

	span.SetAttribute("tool_name", invocation.Name)
	span.SetAttribute("tool_id", invocation.ID)
	span.SetAttribute("agent_id", invocation.Context.AgentID)

	r.logger.Debug(ctx, "tool.executing",
		observability.F("tool", invocation.Name),
		observability.F("tool_id", invocation.ID))

	// Get tool from router (checking exists)
	_, found := r.router.Tool(invocation.Name)
	if !found {
		err := sdkerr.Permanent(
			"tools.runtime.tool_not_found",
			fmt.Sprintf("tool not found: %s", invocation.Name),
			sdkerr.WithOperation("tools.execute_single"),
			sdkerr.WithComponent("tools.runtime"),
			sdkerr.WithTraceFromContext(ctx),
			sdkerr.WithAttr("tool_name", invocation.Name),
		)
		span.RecordError(err)
		r.logger.Error(ctx, "tool.not_found",
			observability.F("tool", invocation.Name))

		return &ToolInvocationResult{
			Invocation: invocation,
			Result:     nil,
			Error:      err,
			Duration:   time.Since(startTime),
			ExecutedAt: startTime,
		}
	}

	// Inject tool call ID into context so tools can access it for unique identification
	ctx = WithToolCallID(ctx, invocation.ID)

	// Execute tool through Executor (includes permissions + validation).
	exec := NewExecutor(r.registry, nil)
	result, err := exec.Execute(ctx, invocation.Name, invocation.Parameters)
	if err != nil {
		err = sdkerr.Wrap(
			err,
			"tools.runtime.execution_failed",
			sdkerr.WithOperation("tools.execute_single"),
			sdkerr.WithComponent("tools.runtime"),
			sdkerr.WithTraceFromContext(ctx),
			sdkerr.WithAttr("tool_name", invocation.Name),
			sdkerr.WithAttr("tool_call_id", invocation.ID),
		)
	}

	duration := time.Since(startTime)
	span.SetAttribute("duration_ms", duration.Milliseconds())

	if err != nil {
		span.RecordError(err)
		r.logger.Error(ctx, "tool.execution_failed",
			observability.F("tool", invocation.Name),
			observability.F("duration_ms", duration.Milliseconds()),
			observability.F("error", err.Error()))
	} else {
		r.logger.Debug(ctx, "tool.execution_succeeded",
			observability.F("tool", invocation.Name),
			observability.F("duration_ms", duration.Milliseconds()))
	}

	return &ToolInvocationResult{
		Invocation: invocation,
		Result:     result,
		Error:      err,
		Duration:   duration,
		ExecutedAt: startTime,
	}
}

// ExecuteSingle is a convenience method to execute a single tool call.
// For single tool execution, this is more efficient than ExecuteBatch.
func (r *ToolCallRuntime) ExecuteSingle(ctx context.Context, call *ToolInvocation) (*ToolInvocationResult, error) {
	results, err := r.ExecuteBatch(ctx, []*ToolInvocation{call})
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return nil, sdkerr.Permanent(
			"tools.runtime.no_batch_result",
			"no result returned from batch execution",
			sdkerr.WithOperation("tools.execute_single"),
			sdkerr.WithComponent("tools.runtime"),
			sdkerr.WithTraceFromContext(ctx),
		)
	}

	return results[0], nil
}

// RefreshRouter refreshes the tool router's specifications.
// Call this if tools are added/removed from the registry after runtime creation.
func (r *ToolCallRuntime) RefreshRouter() {
	r.router.Refresh()
}
