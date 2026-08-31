package tools

import "time"

// OptimizationHints provides guidance for efficient tool use.
// Based on Anthropic's efficient tool use patterns.
type OptimizationHints struct {
	// PreferSequential suggests this tool should not run in parallel with others.
	// Set to true for tools that have side effects or dependencies.
	PreferSequential bool

	// EstimatedDuration helps the LLM decide batching strategy.
	// Tools with similar durations can be batched together.
	EstimatedDuration time.Duration

	// CanBatch indicates this tool can be batched with others of the same type.
	// Useful for read operations that can be grouped.
	CanBatch bool

	// BatchSize is the optimal batch size for this tool.
	// 0 means no batching, >0 means batch up to this many calls.
	BatchSize int

	// Priority for execution ordering (0-100, higher = execute sooner).
	// High priority tools run first in sequential execution.
	Priority int

	// MinimalLatency indicates this tool should be executed with minimal delay.
	// Useful for interactive tools that users are waiting for.
	MinimalLatency bool

	// Cacheable indicates if results can be cached for identical inputs.
	// Different from IsIdempotent - this is a hint for LLM-level caching.
	Cacheable bool

	// CacheTTL is the time-to-live for cached results.
	CacheTTL time.Duration
}

// DefaultOptimizationHints returns sensible defaults for a tool
func DefaultOptimizationHints() *OptimizationHints {
	return &OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 100 * time.Millisecond,
		CanBatch:          false,
		BatchSize:         0,
		Priority:          50,
		MinimalLatency:    false,
		Cacheable:         false,
		CacheTTL:          5 * time.Minute,
	}
}

// FastReadHints returns optimization hints for fast read operations
func FastReadHints() *OptimizationHints {
	return &OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 10 * time.Millisecond,
		CanBatch:          true,
		BatchSize:         10,
		Priority:          80,
		MinimalLatency:    true,
		Cacheable:         true,
		CacheTTL:          1 * time.Minute,
	}
}

// WriteOperationHints returns optimization hints for write operations
func WriteOperationHints() *OptimizationHints {
	return &OptimizationHints{
		PreferSequential:  true,
		EstimatedDuration: 50 * time.Millisecond,
		CanBatch:          false,
		BatchSize:         0,
		Priority:          100,
		MinimalLatency:    false,
		Cacheable:         false,
		CacheTTL:          0,
	}
}

// ExpensiveOperationHints returns optimization hints for expensive operations
func ExpensiveOperationHints() *OptimizationHints {
	return &OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 1 * time.Second,
		CanBatch:          false,
		BatchSize:         0,
		Priority:          30,
		MinimalLatency:    false,
		Cacheable:         true,
		CacheTTL:          10 * time.Minute,
	}
}
