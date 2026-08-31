package hooks

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// HookRealtimeUpdate represents a real-time update from hook execution.
// This is used to emit IntermediateUpdates through the agent's callback.
// Spec: 020-hook-execution-realtime
type HookRealtimeUpdate interface {
	UpdateType() string
}

// HookStartedUpdate is emitted when a hook begins execution.
type HookStartedUpdate struct {
	HookID            string        // Unique ID for correlating start/complete events
	HookName          string        // Name of the hook
	ToolName          string        // Tool that triggered the hook (if applicable)
	Phase             string        // "before" or "after"
	StartedAt         int64         // Unix timestamp (milliseconds) when hook started
	TimeoutConfigured time.Duration // Configured timeout
	WorkingDir        string        // Working directory
}

func (u HookStartedUpdate) UpdateType() string { return "hook_started" }

// HookOutputChunkUpdate is emitted when a hook outputs data during execution.
type HookOutputChunkUpdate struct {
	HookID    string // Correlation ID matching HookStartedUpdate.HookID
	HookName  string // Name of the hook (for display with [hook-name] prefix)
	Chunk     string // The output chunk (buffered at 1KB or 100ms)
	IsStderr  bool   // true for stderr, false for stdout
	Timestamp int64  // Unix timestamp (milliseconds) when chunk was emitted
}

func (u HookOutputChunkUpdate) UpdateType() string { return "hook_output_chunk" }

// HookCompletedUpdate is emitted when a hook finishes execution.
type HookCompletedUpdate struct {
	HookID            string        // Same ID as the corresponding HookStartedUpdate
	HookName          string        // Name of the hook
	ToolName          string        // Tool that triggered the hook (if applicable)
	Phase             string        // "before" or "after"
	StartedAt         int64         // Unix timestamp (milliseconds) when hook started
	Success           bool          // Whether hook execution succeeded
	Output            string        // Final output (stdout)
	Blocked           bool          // Whether hook blocked the action
	Error             string        // Error message if any
	Duration          time.Duration // Total execution duration
	ExitCode          int           // Exit code
	MatchedPattern    string        // Event pattern that matched
	TimeoutConfigured time.Duration // Configured timeout
	WorkingDir        string        // Working directory
}

func (u HookCompletedUpdate) UpdateType() string { return "hook_completed" }

// HookRealtimeCallback is called when real-time hook updates are available.
// The callback receives HookStartedUpdate, HookOutputChunkUpdate, or HookCompletedUpdate.
type HookRealtimeCallback func(update HookRealtimeUpdate)

// GenerateHookID creates a unique ID for correlating hook execution events.
func GenerateHookID() string {
	return "hook-" + uuid.New().String()[:8]
}

// realtimeContextKey is the context key for real-time callback data.
type realtimeContextKey struct{}

// realtimeContextData holds callback data passed through context.
type realtimeContextData struct {
	Callback HookRealtimeCallback
	HookID   string
	HookName string
}

// ContextWithRealtimeCallback returns a context with real-time callback data.
// This allows hooks to emit streaming output chunks during execution.
func ContextWithRealtimeCallback(ctx context.Context, callback HookRealtimeCallback, hookID, hookName string) context.Context {
	return context.WithValue(ctx, realtimeContextKey{}, &realtimeContextData{
		Callback: callback,
		HookID:   hookID,
		HookName: hookName,
	})
}

// GetRealtimeCallback retrieves the real-time callback from context.
// Returns nil if no callback is set.
func GetRealtimeCallback(ctx context.Context) HookRealtimeCallback {
	data, ok := ctx.Value(realtimeContextKey{}).(*realtimeContextData)
	if !ok || data == nil {
		return nil
	}
	return data.Callback
}

// GetRealtimeHookID retrieves the hook ID from context.
func GetRealtimeHookID(ctx context.Context) string {
	data, ok := ctx.Value(realtimeContextKey{}).(*realtimeContextData)
	if !ok || data == nil {
		return ""
	}
	return data.HookID
}

// GetRealtimeHookName retrieves the hook name from context.
func GetRealtimeHookName(ctx context.Context) string {
	data, ok := ctx.Value(realtimeContextKey{}).(*realtimeContextData)
	if !ok || data == nil {
		return ""
	}
	return data.HookName
}

// EmitOutputChunk is a helper to emit a HookOutputChunkUpdate from within a hook.
// It retrieves the callback and hook info from context.
func EmitOutputChunk(ctx context.Context, chunk string, isStderr bool) {
	data, ok := ctx.Value(realtimeContextKey{}).(*realtimeContextData)
	if !ok || data == nil || data.Callback == nil {
		return
	}
	data.Callback(HookOutputChunkUpdate{
		HookID:    data.HookID,
		HookName:  data.HookName,
		Chunk:     chunk,
		IsStderr:  isStderr,
		Timestamp: time.Now().UnixMilli(),
	})
}
