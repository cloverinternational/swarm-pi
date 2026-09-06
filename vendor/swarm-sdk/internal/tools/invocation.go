// Package tools provides tool invocation types for parallel execution.
package tools

import (
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
)

// ToolInvocation represents a pending tool execution with full context.
// This is used by the ToolCallRuntime to manage parallel tool execution.
type ToolInvocation struct {
	// ID is the unique identifier for this tool call (from provider).
	// Note: providers (and some proxy layers) occasionally emit duplicate
	// IDs within a single batch. The dispatcher tolerates collisions by
	// routing results via pointer identity, not via this string.
	ID string

	// Name is the tool name to execute
	Name string

	// Parameters are the tool input parameters
	Parameters map[string]any

	// Context provides execution context for the tool
	Context ToolInvocationContext

	// StartTime tracks when this invocation was created
	StartTime time.Time

	// SourceIndex is the position of the originating ToolCall in the agent's
	// message.ToolCalls slice. Set by buildToolInvocations and used during
	// result aggregation to pair each invocation with the right source call
	// even when multiple calls share the same ID (rare but real model bug).
	// Zero-value (0) is a valid index for the first call; consumers should
	// treat it as authoritative.
	SourceIndex int
}

// ToolInvocationContext provides execution context for a tool call.
// This includes information about the agent, conversation, and mode.
type ToolInvocationContext struct {
	// AgentID identifies the agent making the tool call
	AgentID string

	// ConversationID identifies the conversation
	ConversationID string

	// Mode is the current execution mode (if any)
	Mode string

	// UserID identifies the user (for permission checks)
	UserID string

	// WorkingDirectory is the current working directory
	WorkingDirectory string

	// Hosted carries additive hosted execution metadata.
	Hosted *hosted.ExecutionMetadata
}

// ToolInvocationResult wraps a tool result with invocation metadata.
type ToolInvocationResult struct {
	// Invocation is the original invocation
	Invocation *ToolInvocation

	// Result is the tool execution result
	Result *ToolResult

	// Error is any error that occurred during execution
	Error error

	// Duration is how long the tool took to execute
	Duration time.Duration

	// ExecutedAt is when the tool was executed
	ExecutedAt time.Time
}

// IsSuccess returns true if the tool executed without error.
func (r *ToolInvocationResult) IsSuccess() bool {
	return r.Error == nil && r.Result != nil
}

// IsFailed returns true if the tool execution failed.
func (r *ToolInvocationResult) IsFailed() bool {
	return r.Error != nil
}
