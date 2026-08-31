// Package tools provides optimized parallel tool execution with ants pooling
package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// OptimizedToolCallRuntime replaces the standard ToolCallRuntime with pool-based execution
type OptimizedToolCallRuntime struct {
	// Original fields
	router   *ToolRouter
	registry Registry
	logger   observability.Logger
	tracer   observability.Tracer

	// Pool manager for efficient goroutine usage
	poolManager *pool.Manager

	// Optimized execution strategy
	optimizedStrategy OptimizedExecutionStrategy
}

// OptimizedExecutionStrategy defines how tools are executed with pooling
type OptimizedExecutionStrategy interface {
	Execute(ctx context.Context, calls []*ToolInvocation, runtime *OptimizedToolCallRuntime) ([]*ToolInvocationResult, error)
}

// NewOptimizedToolCallRuntime creates an optimized tool runtime
func NewOptimizedToolCallRuntime(registry Registry, logger observability.Logger, tracer observability.Tracer, poolManager *pool.Manager) *OptimizedToolCallRuntime {
	router := NewToolRouter(ToolRouterConfig{
		Registry: registry,
	})

	runtime := &OptimizedToolCallRuntime{
		router:      router,
		registry:    registry,
		logger:      logger,
		tracer:      tracer,
		poolManager: poolManager,
	}

	// Choose strategy based on configuration
	runtime.optimizedStrategy = &PooledExecutionStrategy{}

	return runtime
}

// Execute executes multiple tools with optimization
func (r *OptimizedToolCallRuntime) Execute(ctx context.Context, calls []*ToolInvocation) ([]*ToolInvocationResult, error) {
	if len(calls) == 0 {
		return []*ToolInvocationResult{}, nil
	}

	// Trace execution
	ctx, span := r.tracer.StartSpan(ctx, "tool_runtime.execute_parallel")
	defer span.End()

	// Execute using strategy
	return r.optimizedStrategy.Execute(ctx, calls, r)
}

// PooledExecutionStrategy uses ants pools for execution
type PooledExecutionStrategy struct{}

// Execute implements OptimizedExecutionStrategy using goroutine pools
func (e *PooledExecutionStrategy) Execute(ctx context.Context, calls []*ToolInvocation, runtime *OptimizedToolCallRuntime) ([]*ToolInvocationResult, error) {
	responses := make([]*ToolInvocationResult, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	// Error channel for first error
	errCh := make(chan error, 1)

	// Execute all tools in parallel using the pool
	for i, call := range calls {
		idx := i
		callCopy := call

		// Submit to pool
		err := runtime.poolManager.SubmitWithMetrics(ctx, pool.PoolTypeTools, func() {
			defer wg.Done()

			// Execute tool
			resp := runtime.executeSingle(ctx, callCopy)
			responses[idx] = resp

			// Report first critical error
			if resp.Error != nil && sdkerr.GetType(resp.Error) == "permanent" {
				select {
				case errCh <- fmt.Errorf("tool %s failed: %w", callCopy.Name, resp.Error):
				default:
				}
			}
		}, func(duration time.Duration) {
			// Record execution time metric
			runtime.logger.Debug(ctx, "tool.execution_time",
				observability.F("tool", callCopy.Name),
				observability.F("duration_ms", duration.Milliseconds()))
		})

		if err != nil {
			// Pool is full, fall back to direct execution
			runtime.logger.Warn(ctx, "tool_runtime.pool_full",
				observability.F("tool", callCopy.Name))

			go func(c *ToolInvocation, index int) {
				defer wg.Done()
				resp := runtime.executeSingle(ctx, c)
				responses[index] = resp
			}(callCopy, idx)
		}
	}

	// Wait for all tools to complete
	wg.Wait()

	// Check for critical errors
	select {
	case err := <-errCh:
		return responses, err
	default:
		return responses, nil
	}
}

// BatchedExecutionStrategy groups tools into batches
type BatchedExecutionStrategy struct {
	batchSize int
}

// Execute implements OptimizedExecutionStrategy with batching
func (e *BatchedExecutionStrategy) Execute(ctx context.Context, calls []*ToolInvocation, runtime *OptimizedToolCallRuntime) ([]*ToolInvocationResult, error) {
	responses := make([]*ToolInvocationResult, len(calls))

	// Process in batches
	for i := 0; i < len(calls); i += e.batchSize {
		end := min(i+e.batchSize, len(calls))

		batch := calls[i:end]
		batchResponses := make([]*ToolInvocationResult, len(batch))

		var wg sync.WaitGroup
		wg.Add(len(batch))

		for j, call := range batch {
			idx := i + j
			callCopy := call
			batchIdx := j

			err := runtime.poolManager.Submit(ctx, pool.PoolTypeTools, func() {
				defer wg.Done()
				resp := runtime.executeSingle(ctx, callCopy)
				batchResponses[batchIdx] = resp
				responses[idx] = resp
			})

			if err != nil {
				// Fallback to direct execution
				go func(c *ToolInvocation, respIdx, globalIdx int) {
					defer wg.Done()
					resp := runtime.executeSingle(ctx, c)
					batchResponses[respIdx] = resp
					responses[globalIdx] = resp
				}(callCopy, batchIdx, idx)
			}
		}

		wg.Wait()

		// Check for errors in batch
		for _, resp := range batchResponses {
			if resp.Error != nil && sdkerr.GetType(resp.Error) == "permanent" {
				return responses, resp.Error
			}
		}
	}

	return responses, nil
}

// executeSingle executes a single tool call
func (r *OptimizedToolCallRuntime) executeSingle(ctx context.Context, call *ToolInvocation) *ToolInvocationResult {
	startTime := time.Now()

	// Get tool from registry
	tool, err := r.registry.Get(call.Name)
	if err != nil {
		return &ToolInvocationResult{
			Invocation: call,
			Error:      fmt.Errorf("tool not found: %w", err),
			Duration:   time.Since(startTime),
		}
	}

	// Execute tool
	result, err := measuredExecute(ctx, tool, call.Name, call.Parameters)

	return &ToolInvocationResult{
		Invocation: call,
		Result:     result,
		Error:      err,
		Duration:   time.Since(startTime),
	}
}

// CloudFlareOptimizedStrategy for extreme constraints
type CloudFlareOptimizedStrategy struct{}

// Execute implements OptimizedExecutionStrategy for CloudFlare Workers
func (e *CloudFlareOptimizedStrategy) Execute(ctx context.Context, calls []*ToolInvocation, runtime *OptimizedToolCallRuntime) ([]*ToolInvocationResult, error) {
	responses := make([]*ToolInvocationResult, len(calls))

	// In CloudFlare mode, execute tools sequentially to minimize memory
	for i, call := range calls {
		responses[i] = runtime.executeSingle(ctx, call)

		// Check for critical errors immediately
		if responses[i].Error != nil && sdkerr.GetType(responses[i].Error) == "permanent" {
			return responses, responses[i].Error
		}

		// Yield to prevent blocking
		select {
		case <-ctx.Done():
			return responses, ctx.Err()
		default:
		}
	}

	return responses, nil
}
