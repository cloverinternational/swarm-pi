// Package codemode provides code-mode execution for the swarm-sdk.
package codemode

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode/sandbox"
)

// TraceCollector captures nested tool call metadata for observability.
// It records each call and its result, making them available for the
// run_code ToolResult's metadata field.
type TraceCollector struct {
	mu sync.RWMutex

	// calls maps call_id to call metadata
	calls map[string]*CallTrace

	// callOrder preserves the order of calls
	callOrder []string

	// counter generates unique call IDs
	counter int64

	// parentID is the outer run_code call ID for nesting
	parentID string
}

// CallTrace records the details of a single tool call.
type CallTrace struct {
	ID         string
	ToolName   string
	Args       map[string]any
	StartTime  time.Time
	EndTime    time.Time
	Duration   time.Duration
	Result     *tools.ToolResult
	Error      error
	IsComplete bool
}

// NewTraceCollector creates a new trace collector.
func NewTraceCollector(parentID string) *TraceCollector {
	return &TraceCollector{
		calls:     make(map[string]*CallTrace),
		callOrder: make([]string, 0),
		parentID:  parentID,
	}
}

// StartCall records the start of a tool call and returns its ID.
func (t *TraceCollector) StartCall(toolName string, args map[string]any) string {
	id := t.nextID()

	trace := &CallTrace{
		ID:        id,
		ToolName:  toolName,
		Args:      args,
		StartTime: time.Now(),
	}

	t.mu.Lock()
	t.calls[id] = trace
	t.callOrder = append(t.callOrder, id)
	t.mu.Unlock()

	return id
}

// EndCall records the completion of a tool call.
func (t *TraceCollector) EndCall(id string, result *tools.ToolResult, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	trace, ok := t.calls[id]
	if !ok {
		return
	}

	trace.EndTime = time.Now()
	trace.Duration = trace.EndTime.Sub(trace.StartTime)
	trace.Result = result
	trace.Error = err
	trace.IsComplete = true
}

// nextID generates a unique call ID.
func (t *TraceCollector) nextID() string {
	n := atomic.AddInt64(&t.counter, 1)
	if t.parentID != "" {
		return t.parentID + "__" + string(rune(n))
	}
	return "cm_" + string(rune(n))
}

// GetCalls returns all recorded calls in order.
func (t *TraceCollector) GetCalls() []*CallTrace {
	t.mu.RLock()
	defer t.mu.RUnlock()

	calls := make([]*CallTrace, 0, len(t.callOrder))
	for _, id := range t.callOrder {
		if trace, ok := t.calls[id]; ok {
			calls = append(calls, trace)
		}
	}
	return calls
}

// ToToolCalls converts the collected traces to sandbox.ToolCallMeta map.
func (t *TraceCollector) ToToolCalls() map[string]sandbox.ToolCallMeta {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]sandbox.ToolCallMeta, len(t.calls))
	for id, trace := range t.calls {
		result[id] = sandbox.ToolCallMeta{
			ToolName: trace.ToolName,
			Args:     trace.Args,
			CallID:   id,
		}
	}
	return result
}

// ToToolReturns converts the collected traces to sandbox.ToolReturnMeta map.
func (t *TraceCollector) ToToolReturns() map[string]sandbox.ToolReturnMeta {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]sandbox.ToolReturnMeta, len(t.calls))
	for id, trace := range t.calls {
		if !trace.IsComplete {
			continue
		}
		result[id] = sandbox.ToolReturnMeta{
			ToolName:   trace.ToolName,
			CallID:     id,
			IsError:    trace.Error != nil || (trace.Result != nil && trace.Result.IsError),
			DurationMS: trace.Duration.Milliseconds(),
		}
	}
	return result
}

// ToToolResultMetadata creates the metadata map for a ToolResult.
// This includes tool_calls and tool_returns for observability.
func (t *TraceCollector) ToToolResultMetadata() map[string]any {
	return map[string]any{
		"code_mode":    true,
		"tool_calls":   t.ToToolCalls(),
		"tool_returns": t.ToToolReturns(),
	}
}

// Merge combines another trace collector into this one.
// Used when a sandbox has internal traces to merge into the main trace.
func (t *TraceCollector) Merge(other *TraceCollector) {
	t.mu.Lock()
	defer t.mu.Unlock()

	other.mu.RLock()
	defer other.mu.RUnlock()

	for id, trace := range other.calls {
		// Avoid overwriting existing calls
		if _, exists := t.calls[id]; !exists {
			t.calls[id] = trace
			t.callOrder = append(t.callOrder, id)
		}
	}
}

// Stats returns summary statistics about the collected traces.
func (t *TraceCollector) Stats() TraceStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var stats TraceStats
	for _, trace := range t.calls {
		stats.TotalCalls++
		if trace.IsComplete {
			stats.CompletedCalls++
			stats.TotalDuration += trace.Duration
			if trace.Error != nil {
				stats.FailedCalls++
			}
		}
	}
	return stats
}

// TraceStats holds summary statistics about tool calls.
type TraceStats struct {
	TotalCalls      int
	CompletedCalls  int
	FailedCalls     int
	TotalDuration   time.Duration
	AverageDuration time.Duration
}

// Reset clears all collected traces.
func (t *TraceCollector) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.calls = make(map[string]*CallTrace)
	t.callOrder = make([]string, 0)
	atomic.StoreInt64(&t.counter, 0)
}
