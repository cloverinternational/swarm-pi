package tools

import (
	"context"
	"time"
)

// ExecutionContext contains context for tool execution
type ExecutionContext struct {
	// Context is the Go context
	Context context.Context

	// Tool being executed
	Tool Tool

	// Parameters for execution
	Parameters map[string]any

	// Metadata from hooks or orchestrator
	Metadata map[string]any

	// StartTime when execution started
	StartTime time.Time

	// ConversationID if executing within a conversation
	ConversationID string

	// AgentID if executing for a specific agent
	AgentID string

	// TraceID for distributed tracing
	TraceID string

	// SpanID for distributed tracing
	SpanID string
}

// ExecutionMode determines how tools are executed
type ExecutionMode string

const (
	// ExecutionModeSequential executes tools one at a time
	ExecutionModeSequential ExecutionMode = "sequential"

	// ExecutionModeParallel executes all tools concurrently
	ExecutionModeParallel ExecutionMode = "parallel"

	// ExecutionModeOptimized uses tool hints to optimize execution
	ExecutionModeOptimized ExecutionMode = "optimized"

	// ExecutionModeBatch groups similar tools for batching
	ExecutionModeBatch ExecutionMode = "batch"
)

// ExecutionStrategy determines execution order and parallelism
type ExecutionStrategy interface {
	// Plan creates an execution plan for the given tool calls
	Plan(calls []ToolCall, tools map[string]Tool) *ExecutionPlan

	// Mode returns the execution mode
	Mode() ExecutionMode
}

// ExecutionPlan describes how to execute a set of tool calls
type ExecutionPlan struct {
	// Stages contains execution stages
	// Each stage can run in parallel, stages run sequentially
	Stages [][]*ToolCall

	// TotalCalls is the number of calls in the plan
	TotalCalls int

	// EstimatedDuration is the estimated total duration
	EstimatedDuration time.Duration

	// MaxParallelism is the maximum parallelism across all stages
	MaxParallelism int

	// Metadata for the plan
	Metadata map[string]any
}

// SequentialStrategy executes all tools one at a time
type SequentialStrategy struct{}

func (s *SequentialStrategy) Plan(calls []ToolCall, tools map[string]Tool) *ExecutionPlan {
	stages := make([][]*ToolCall, len(calls))
	for i := range calls {
		stages[i] = []*ToolCall{&calls[i]}
	}

	return &ExecutionPlan{
		Stages:         stages,
		TotalCalls:     len(calls),
		MaxParallelism: 1,
	}
}

func (s *SequentialStrategy) Mode() ExecutionMode {
	return ExecutionModeSequential
}

// ParallelStrategy executes all tools concurrently
type ParallelStrategy struct {
	MaxParallel int
}

func (s *ParallelStrategy) Plan(calls []ToolCall, tools map[string]Tool) *ExecutionPlan {
	// Single stage with all calls
	stage := make([]*ToolCall, len(calls))
	for i := range calls {
		stage[i] = &calls[i]
	}

	maxParallel := s.MaxParallel
	if maxParallel == 0 {
		maxParallel = len(calls)
	}

	return &ExecutionPlan{
		Stages:         [][]*ToolCall{stage},
		TotalCalls:     len(calls),
		MaxParallelism: maxParallel,
	}
}

func (s *ParallelStrategy) Mode() ExecutionMode {
	return ExecutionModeParallel
}

// OptimizedStrategy uses tool hints to optimize execution
type OptimizedStrategy struct {
	MaxParallel int
}

func (s *OptimizedStrategy) Plan(calls []ToolCall, tools map[string]Tool) *ExecutionPlan {
	// Group calls by priority and sequential requirements
	var stages [][]*ToolCall

	// First pass: separate sequential and parallel tools
	var sequentialCalls []*ToolCall
	var parallelCalls []*ToolCall

	for i := range calls {
		call := &calls[i]
		tool, exists := tools[call.Name]
		if !exists {
			// Unknown tool, treat as sequential
			sequentialCalls = append(sequentialCalls, call)
			continue
		}

		var hints *OptimizationHints
		if ht, ok := tool.(HintedTool); ok {
			hints = ht.OptimizationHints()
		}
		if hints == nil {
			hints = DefaultOptimizationHints()
		}

		if hints.PreferSequential {
			sequentialCalls = append(sequentialCalls, call)
		} else {
			parallelCalls = append(parallelCalls, call)
		}
	}

	// Sequential tools each get their own stage
	for _, call := range sequentialCalls {
		stages = append(stages, []*ToolCall{call})
	}

	// Parallel tools go in a single stage
	if len(parallelCalls) > 0 {
		stages = append(stages, parallelCalls)
	}

	maxParallel := s.MaxParallel
	if maxParallel == 0 {
		maxParallel = len(parallelCalls)
	}

	return &ExecutionPlan{
		Stages:         stages,
		TotalCalls:     len(calls),
		MaxParallelism: maxParallel,
	}
}

func (s *OptimizedStrategy) Mode() ExecutionMode {
	return ExecutionModeOptimized
}
