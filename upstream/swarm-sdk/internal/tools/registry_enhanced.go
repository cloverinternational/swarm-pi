package tools

import (
	"context"
	"time"
)

// EnhancedRegistry extends the basic registry with hooks, batching, and optimization.
// This is Ring 0 - interface only, implementation in Ring 1.
type EnhancedRegistry interface {
	Registry // Embed base interface

	// Hook management
	RegisterHook(hook ToolHook) error
	UnregisterHook(name string) error
	ListHooks() []ToolHook
	GetHookRegistry() HookRegistry

	// Batch execution support
	ExecuteBatch(ctx context.Context, calls []ToolCall) ([]*ToolResult, error)

	// Parallel execution support
	ExecuteParallel(ctx context.Context, calls []ToolCall) ([]*ToolResult, error)

	// Sequential execution with optimization
	ExecuteSequential(ctx context.Context, calls []ToolCall, opts *ExecutionOptions) ([]*ToolResult, error)

	// Provider-specific translation
	TranslateToProvider(provider string, tools []Tool) (any, error)
	TranslateFromProvider(provider string, result any) (*ToolResult, error)

	// Metadata management
	SetMetadata(toolName string, metadata *ToolMetadata) error
	GetMetadata(toolName string) (*ToolMetadata, error)

	// Statistics and monitoring
	GetStats() *RegistryStats
	ResetStats()
}

// ToolCall represents a single tool invocation
type ToolCall struct {
	// ID uniquely identifies this tool call
	ID string

	// Name is the tool name
	Name string

	// Parameters for the tool
	Parameters map[string]any

	// Metadata for this specific call
	Metadata map[string]any
}

// ExecutionOptions controls how tools are executed
type ExecutionOptions struct {
	// MaxParallel limits concurrent tool executions (0 = unlimited)
	MaxParallel int

	// Timeout for entire batch
	Timeout time.Duration

	// PerToolTimeout for individual tool execution
	PerToolTimeout time.Duration

	// ContinueOnError determines if batch continues after error
	ContinueOnError bool

	// PreferSequential forces sequential execution
	PreferSequential bool

	// EnableOptimization uses tool optimization hints
	EnableOptimization bool

	// RespectPriority executes tools in priority order
	RespectPriority bool

	// EnableCaching allows cached results for idempotent tools
	EnableCaching bool

	// RetryTransient retries transient errors
	RetryTransient bool

	// MaxRetries for transient errors
	MaxRetries int
}

// DefaultExecutionOptions returns sensible defaults
func DefaultExecutionOptions() *ExecutionOptions {
	return &ExecutionOptions{
		MaxParallel:        10,
		Timeout:            5 * time.Minute,
		PerToolTimeout:     30 * time.Second,
		ContinueOnError:    false,
		PreferSequential:   false,
		EnableOptimization: true,
		RespectPriority:    true,
		EnableCaching:      true,
		RetryTransient:     true,
		MaxRetries:         3,
	}
}

// RegistryStats contains registry statistics
type RegistryStats struct {
	// TotalTools is the number of registered tools
	TotalTools int

	// ToolsBySource breaks down tools by source
	ToolsBySource map[ToolSource]int

	// TotalExecutions is the total number of tool executions
	TotalExecutions int64

	// SuccessfulExecutions is successful executions
	SuccessfulExecutions int64

	// FailedExecutions is failed executions
	FailedExecutions int64

	// CacheHits is the number of cache hits
	CacheHits int64

	// CacheMisses is the number of cache misses
	CacheMisses int64

	// AverageExecutionTimeMS is average execution time
	AverageExecutionTimeMS float64

	// P99ExecutionTimeMS is p99 execution time
	P99ExecutionTimeMS float64

	// TotalHooks is the number of registered hooks
	TotalHooks int

	// HookExecutions is the total number of hook executions
	HookExecutions int64

	// BlockedExecutions is executions blocked by hooks
	BlockedExecutions int64
}

// ToolCallResult combines a call with its result
type ToolCallResult struct {
	Call      *ToolCall
	Result    *ToolResult
	Error     error
	StartTime time.Time
	EndTime   time.Time
}

// BatchResult contains results from batch execution
type BatchResult struct {
	// Results for each call (may include nil results for errors)
	Results []*ToolCallResult

	// TotalCalls is the number of calls in the batch
	TotalCalls int

	// SuccessfulCalls is the number of successful calls
	SuccessfulCalls int

	// FailedCalls is the number of failed calls
	FailedCalls int

	// TotalDuration is total batch execution time
	TotalDuration time.Duration

	// ParallelExecutions is how many ran in parallel
	ParallelExecutions int
}
