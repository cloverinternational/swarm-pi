// Package agent provides background agent pattern for long-running async tasks.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"encoding/json"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

var ErrInterjectQueueFull = errors.New("interjection queue is full")
var ErrAgentNotRunning = errors.New("agent is not running")

const interjectQueueCapacity = 8

// BackgroundAgentStatus represents the current state of a background agent.
type BackgroundAgentStatus string

const (
	// StatusPending means the agent hasn't started yet
	StatusPending BackgroundAgentStatus = "pending"

	// StatusRunning means the agent is currently executing
	StatusRunning BackgroundAgentStatus = "running"

	// StatusCompleted means the agent finished successfully
	StatusCompleted BackgroundAgentStatus = "completed"

	// StatusFailed means the agent encountered an error
	StatusFailed BackgroundAgentStatus = "failed"

	// StatusCancelled means the agent was cancelled
	StatusCancelled BackgroundAgentStatus = "cancelled"
)

// BackgroundAgentEvent represents an event emitted during background execution.
type BackgroundAgentEvent struct {
	// AgentID identifies the agent
	AgentID string

	// EventType is the type of event
	EventType BackgroundEventType

	// Status is the current status
	Status BackgroundAgentStatus

	// Message is a human-readable message
	Message string

	// Progress is a percentage (0-100)
	Progress int

	// Timestamp when the event occurred
	Timestamp time.Time

	// Metadata contains additional event data
	Metadata map[string]any
}

// BackgroundEventType classifies the event.
type BackgroundEventType string

const (
	// EventStarted is emitted when execution begins
	EventStarted BackgroundEventType = "started"

	// EventProgress is emitted during execution with updates
	EventProgress BackgroundEventType = "progress"

	// EventCompleted is emitted on successful completion
	EventCompleted BackgroundEventType = "completed"

	// EventFailed is emitted on failure
	EventFailed BackgroundEventType = "failed"

	// EventCancelled is emitted on cancellation
	EventCancelled BackgroundEventType = "cancelled"
)

// BackgroundAgentResult stores the final result of background execution.
type BackgroundAgentResult struct {
	// AgentID identifies the agent
	AgentID string

	// Status is the final status
	Status BackgroundAgentStatus

	// Result is the agent's response (if successful)
	Result string

	// Error is the error (if failed)
	Error error

	// OutputFile is the path to the persisted NDJSON output file.
	// Empty if the output store could not be initialised.
	OutputFile string

	// StartTime when execution started
	StartTime time.Time

	// EndTime when execution finished
	EndTime time.Time

	// Duration of execution
	Duration time.Duration

	// TurnCount is the number of turns executed
	TurnCount int

	// TokensUsed is the total tokens consumed
	TokensUsed int

	// CostUSD is the estimated cost
	CostUSD float64

	// Metadata contains additional result data
	Metadata map[string]any
}

// BackgroundAgent wraps an agent for async execution.
type BackgroundAgent struct {
	agent    *Agent
	parentID string
	logger   observability.Logger
	tracer   observability.Tracer

	// State
	mu         sync.RWMutex
	status     BackgroundAgentStatus
	result     *BackgroundAgentResult
	cancelFunc context.CancelFunc

	// closeOnce ensures events and done channels are closed exactly once
	// across all terminal paths (success, failure, cancellation).
	closeOnce sync.Once

	// eventsMu pairs every send on events with the close of events.
	// emitEvent holds the read lock across its closed-check and send;
	// closeChannels holds the write lock while setting closed and closing.
	// The atomic closed flag alone is NOT sufficient: a sender can observe
	// closed=false, lose the CPU, and send after the channel is closed
	// (TOCTOU) — panicking the embedding process. With the lock, close
	// cannot begin while any send is in flight, and closed=true is visible
	// to every sender that acquires the read lock afterwards.
	eventsMu sync.RWMutex

	// closed is set to true (under eventsMu) BEFORE the events and done
	// channels are closed, so emitEvent can check it and return silently
	// instead of sending on a closed channel.
	closed atomic.Bool

	// Event channel (buffered) — for observability only. emitEvent is
	// non-blocking, so events can be dropped when the buffer is full.
	// Do NOT rely on events for control flow or completion detection.
	events chan BackgroundAgentEvent

	// done is closed exactly once when the agent reaches any terminal
	// state (completed, failed, cancelled). Unlike events, this signal
	// is guaranteed — closing a channel is atomic and cannot be dropped.
	// Use Done() for reliable completion detection.
	done chan struct{}

	// outputStore persists every IntermediateUpdate to disk as NDJSON.
	// Nil if the store could not be created (non-fatal).
	outputStore *OutputStore

	interjectMu          sync.Mutex
	interjectQueue       []string
	priorMessageInjector MessageInjector
}

// BackgroundAgentConfig configures background agent execution.
type BackgroundAgentConfig struct {
	// Agent to run in background
	Agent *Agent

	// ParentID identifies the managed agent or session that spawned this work.
	ParentID string

	// EventBufferSize is the size of the event channel (default: 100)
	EventBufferSize int

	// Logger for observability
	Logger observability.Logger

	// Tracer for distributed tracing
	Tracer observability.Tracer
}

// NewBackgroundAgent creates a new background agent.
//
// Deprecated: use factory.Create(ctx, AgentConfig{Kind: KindBackground, ...}) instead.
func NewBackgroundAgent(config BackgroundAgentConfig) (*BackgroundAgent, error) {
	if config.Agent == nil {
		return nil, sdkerr.Permanent("background_agent.nil_agent", "agent cannot be nil")
	}

	if config.Logger == nil {
		return nil, sdkerr.Permanent("background_agent.nil_logger", "logger cannot be nil")
	}

	if config.Tracer == nil {
		return nil, sdkerr.Permanent("background_agent.nil_tracer", "tracer cannot be nil")
	}

	bufferSize := config.EventBufferSize
	if bufferSize <= 0 {
		bufferSize = 100
	}

	return &BackgroundAgent{
		agent:    config.Agent,
		parentID: config.ParentID,
		logger:   config.Logger,
		tracer:   config.Tracer,
		status:   StatusPending,
		events:   make(chan BackgroundAgentEvent, bufferSize),
		done:     make(chan struct{}),
	}, nil
}

// ParentID returns the stable parent identity, if one was provided.
func (b *BackgroundAgent) ParentID() string {
	return b.parentID
}

// Start begins async execution of the agent.
// Returns immediately - use Events() and Wait() to monitor progress.
func (b *BackgroundAgent) Start(ctx context.Context, task string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.status == StatusRunning {
		return sdkerr.Permanent("background_agent.already_running",
			"agent is already running")
	}

	if b.status == StatusCompleted || b.status == StatusFailed || b.status == StatusCancelled {
		return sdkerr.Permanent("background_agent.already_finished",
			"agent has already finished (create new instance to run again)")
	}

	// Create cancellable context
	ctx, cancel := context.WithCancel(ctx)
	b.cancelFunc = cancel

	// Update status
	b.status = StatusRunning

	b.agent.mu.Lock()
	b.priorMessageInjector = b.agent.messageInjector
	b.agent.messageInjector = b.messageInjector
	b.agent.mu.Unlock()

	// Initialize result
	b.result = &BackgroundAgentResult{
		AgentID:   b.agent.ID(),
		Status:    StatusRunning,
		StartTime: time.Now(),
		Metadata:  make(map[string]any),
	}

	// Create the output store — every IntermediateUpdate will be persisted here
	// so the parent agent can read partial output while we run, and so output
	// survives process crashes.
	if store, err := NewOutputStore(b.agent.ID()); err == nil {
		b.outputStore = store
		b.result.OutputFile = store.Path()
		b.installOutputStoreCallback()
	} else {
		b.logger.Warn(context.Background(), "background_agent.output_store_failed",
			observability.F("agent_id", b.agent.ID()),
			observability.F("error", err.Error()))
	}

	// Emit started event
	b.emitEvent(BackgroundAgentEvent{
		AgentID:   b.agent.ID(),
		EventType: EventStarted,
		Status:    StatusRunning,
		Message:   "Background agent started",
		Progress:  0,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"task": task,
		},
	})

	// Launch goroutine
	go b.execute(ctx, task)

	return nil
}

// execute runs in a goroutine and performs the actual execution.
func (b *BackgroundAgent) execute(ctx context.Context, task string) {
	defer func() {
		if r := recover(); r != nil {
			b.handleFailure(fmt.Errorf("panic: %v", r))
		}
	}()

	ctx, span := b.tracer.StartSpan(ctx, "background_agent.execute")
	defer span.End()

	span.SetAttribute("agent.id", b.agent.ID())
	span.SetAttribute("agent.background", true)

	// Execute the agent
	resp, err := b.agent.Execute(ctx, ExecuteRequest{
		Message: task,
	})

	// Handle result
	if err != nil {
		// Check if cancelled
		if ctx.Err() == context.Canceled {
			b.handleCancellation()
		} else {
			b.handleFailure(err)
		}
		return
	}

	// Success
	b.handleSuccess(resp)
}

// handleSuccess updates state on successful completion.
func (b *BackgroundAgent) handleSuccess(resp *ExecuteResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.status = StatusCompleted
	b.result.Status = StatusCompleted
	b.result.Result = resp.Message
	b.result.EndTime = time.Now()
	b.result.Duration = b.result.EndTime.Sub(b.result.StartTime)
	b.result.TurnCount = resp.TurnCount
	b.result.TokensUsed = resp.TokensUsed
	b.result.CostUSD = resp.CostUSD

	// Persist the final assistant message and close the output store.
	if b.outputStore != nil {
		_ = b.outputStore.WriteRecord(OutputRecord{Type: "final", Content: resp.Message})
		_ = b.outputStore.Close()
	}

	// Emit completed event
	b.emitEvent(BackgroundAgentEvent{
		AgentID:   b.agent.ID(),
		EventType: EventCompleted,
		Status:    StatusCompleted,
		Message:   "Background agent completed successfully",
		Progress:  100,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"duration":    b.result.Duration.String(),
			"turn_count":  b.result.TurnCount,
			"tokens_used": b.result.TokensUsed,
			"cost_usd":    b.result.CostUSD,
		},
	})

	b.closeChannels()
	b.restoreMessageInjector()
}

// handleFailure updates state on failure.
func (b *BackgroundAgent) handleFailure(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.status = StatusFailed
	b.result.Status = StatusFailed
	b.result.Error = err
	b.result.EndTime = time.Now()
	b.result.Duration = b.result.EndTime.Sub(b.result.StartTime)

	// Persist failure record and close output store so partial output is readable.
	if b.outputStore != nil {
		_ = b.outputStore.WriteRecord(OutputRecord{Type: "final", Error: err.Error()})
		_ = b.outputStore.Close()
	}

	// Emit failed event
	b.emitEvent(BackgroundAgentEvent{
		AgentID:   b.agent.ID(),
		EventType: EventFailed,
		Status:    StatusFailed,
		Message:   fmt.Sprintf("Background agent failed: %v", err),
		Progress:  -1,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"error":    err.Error(),
			"duration": b.result.Duration.String(),
		},
	})

	b.closeChannels()
	b.restoreMessageInjector()
}

// handleCancellation updates state on cancellation.
func (b *BackgroundAgent) handleCancellation() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.status = StatusCancelled
	b.result.Status = StatusCancelled
	b.result.EndTime = time.Now()
	b.result.Duration = b.result.EndTime.Sub(b.result.StartTime)

	// Flush and close the output store — partial output remains readable.
	if b.outputStore != nil {
		_ = b.outputStore.WriteRecord(OutputRecord{Type: "final", Content: "cancelled"})
		_ = b.outputStore.Close()
	}

	// Emit cancelled event
	b.emitEvent(BackgroundAgentEvent{
		AgentID:   b.agent.ID(),
		EventType: EventCancelled,
		Status:    StatusCancelled,
		Message:   "Background agent cancelled",
		Progress:  -1,
		Timestamp: time.Now(),
		Metadata: map[string]any{
			"duration": b.result.Duration.String(),
		},
	})

	b.closeChannels()
	b.restoreMessageInjector()
}

func (b *BackgroundAgent) messageInjector() []string {
	b.interjectMu.Lock()
	queued := b.interjectQueue
	b.interjectQueue = nil
	b.interjectMu.Unlock()
	if b.priorMessageInjector != nil {
		queued = append(queued, b.priorMessageInjector()...)
	}
	return queued
}

func (b *BackgroundAgent) restoreMessageInjector() {
	b.agent.mu.Lock()
	b.agent.messageInjector = b.priorMessageInjector
	b.agent.mu.Unlock()
}

// Interject queues text for the next agent turn without blocking the caller.
func (b *BackgroundAgent) Interject(text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("interjection text cannot be empty")
	}
	b.mu.RLock()
	running := b.status == StatusRunning
	b.mu.RUnlock()
	if !running {
		return ErrAgentNotRunning
	}
	b.interjectMu.Lock()
	defer b.interjectMu.Unlock()
	if len(b.interjectQueue) >= interjectQueueCapacity {
		return ErrInterjectQueueFull
	}
	b.interjectQueue = append(b.interjectQueue, text)
	return nil
}

// emitEvent sends an event to the channel (non-blocking).
//
// If the events channel has already been closed (or is about to be closed),
// emitEvent returns silently. This prevents a "send on closed channel" panic
// when a concurrent goroutine (e.g., the progress tracker) races with the
// closeOnce.Do that closes the channel in handleSuccess/handleFailure/
// handleCancellation.
func (b *BackgroundAgent) emitEvent(event BackgroundAgentEvent) {
	// The read lock pairs this send with closeChannels' write-locked close:
	// once we hold it with closed=false, the channel cannot be closed until
	// the send completes. Checking the flag alone would leave a TOCTOU
	// window between the check and the send.
	b.eventsMu.RLock()
	defer b.eventsMu.RUnlock()
	if b.closed.Load() {
		return
	}
	select {
	case b.events <- event:
		// Event sent
	default:
		// Channel full, log warning
		b.logger.Warn(context.Background(), "background_agent.event_dropped",
			observability.F("agent_id", b.agent.ID()),
			observability.F("event_type", event.EventType))
	}
}

// closeChannels closes the events and done channels exactly once.
// The write lock excludes any in-flight emitEvent send (see eventsMu).
func (b *BackgroundAgent) closeChannels() {
	b.closeOnce.Do(func() {
		b.eventsMu.Lock()
		b.closed.Store(true)
		close(b.events)
		b.eventsMu.Unlock()
		// Signal done: guaranteed delivery regardless of event buffer state.
		close(b.done)
	})
}

// Cancel stops the background agent execution.
func (b *BackgroundAgent) Cancel() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancelFunc != nil && b.status == StatusRunning {
		b.cancelFunc()
	}
}

// Status returns the current status.
func (b *BackgroundAgent) Status() BackgroundAgentStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.status
}

// Events returns the event channel for monitoring progress.
// The channel is closed when execution completes.
func (b *BackgroundAgent) Events() <-chan BackgroundAgentEvent {
	return b.events
}

// Wait blocks until execution completes and returns the result.
func (b *BackgroundAgent) Wait() *BackgroundAgentResult {
	// Wait for events channel to close
	for range b.events {
		// Drain events
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneBackgroundAgentResult(b.result)
}

// Result returns the current result (may be incomplete if still running).
func (b *BackgroundAgent) Result() *BackgroundAgentResult {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return cloneBackgroundAgentResult(b.result)
}

func cloneBackgroundAgentResult(result *BackgroundAgentResult) *BackgroundAgentResult {
	if result == nil {
		return nil
	}
	cloned := *result
	if result.Metadata != nil {
		cloned.Metadata = make(map[string]any, len(result.Metadata))
		for key, value := range result.Metadata {
			cloned.Metadata[key] = value
		}
	}
	return &cloned
}

// Done returns a channel that is closed when the agent reaches any terminal
// state (completed, failed, or cancelled). Unlike the Events() channel, this
// signal is guaranteed — closing a channel is atomic and cannot be dropped
// even when the event buffer is full.
//
// Use this for reliable completion detection:
//
//	select {
//	case <-bgAgent.Done():
//	    // agent finished — check Status() for the terminal state
//	}
func (b *BackgroundAgent) Done() <-chan struct{} {
	return b.done
}

// AgentID returns the underlying agent's ID.
func (b *BackgroundAgent) AgentID() string {
	return b.agent.ID()
}

// installOutputStoreCallback chains an output-store writer onto the agent's
// existing intermediate callback so every streaming update is persisted to disk.
// Must be called while b.mu is held (called from Start).
func (b *BackgroundAgent) installOutputStoreCallback() {
	if b.outputStore == nil {
		return
	}
	store := b.outputStore
	existing := b.agent.IntermediateCallback() // may be nil

	b.agent.SetIntermediateCallback(func(ctx context.Context, update IntermediateUpdate) error {
		// Write update to the output file.
		var rec OutputRecord
		switch u := update.(type) {
		case ThinkingUpdate:
			rec = OutputRecord{Type: "thinking", Content: u.Content, Append: u.Append}
		case ContentUpdate:
			rec = OutputRecord{Type: "content", Content: u.Content, Append: u.Append}
		case ToolCallUpdate:
			params, _ := json.Marshal(u.Parameters)
			rec = OutputRecord{Type: "tool_call", ID: u.ID, Name: u.Name, Params: params}
		case ToolResultUpdate:
			errStr := ""
			if u.Error != nil {
				errStr = u.Error.Error()
			}
			rec = OutputRecord{Type: "tool_result", ID: u.ID, Output: u.Output, Error: errStr}
		case ToolOutputChunk:
			rec = OutputRecord{Type: "tool_chunk", ID: u.ID, Content: u.Chunk}
		default:
			// SubAgentUpdate, FallbackUpdate, etc. — skip, not meaningful in transcript
		}
		if rec.Type != "" {
			if err := store.WriteRecord(rec); err != nil {
				// Non-fatal: log but don't fail the callback chain
				_ = err
			}
			// Flush after each record for low-latency reads by concurrent TaskOutput calls.
			_ = store.Flush()
		}

		// Chain to the original callback (e.g. TUI streaming).
		if existing != nil {
			return existing(ctx, update)
		}
		return nil
	})
}

// OutputFilePath returns the path of the persisted output file, or "" if
// the output store was not initialised.
func (b *BackgroundAgent) OutputFilePath() string {
	if b.outputStore == nil {
		return ""
	}
	return b.outputStore.Path()
}

// StartProgressTracker launches a goroutine that reads the output file every
// interval and updates result.Metadata with the latest activity summary.
// This gives users real-time visibility into what the background agent is doing
// without requiring a separate LLM call.
//
// Call after Start(). The tracker stops automatically when the agent finishes.
func (b *BackgroundAgent) StartProgressTracker(interval time.Duration) {
	if b.outputStore == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	go b.runProgressTracker(interval)
}

// progressScanState carries the incremental scan position between ticks. It is
// owned solely by the runProgressTracker goroutine, so it needs no locking.
type progressScanState struct {
	// offset is the number of bytes already consumed. It always lands on a
	// record boundary (just past a newline) so no record is ever split.
	offset int64
	// toolCount accumulates tool_call records across every chunk consumed so
	// far, preserving the cumulative total that a whole-file scan produced.
	toolCount int
}

// runProgressTracker polls the output file and updates progress metadata.
func (b *BackgroundAgent) runProgressTracker(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	agentID := b.agent.ID()

	// Progress is derived incrementally: each tick consumes only the bytes
	// appended since the previous one. This previously re-read the whole
	// output store from offset 0 (up to 8 MB) and re-parsed every NDJSON
	// record on every tick, for every running agent, for the agent's entire
	// lifetime — O(file size) work forever, where O(new bytes) suffices.
	var st progressScanState

	for {
		ticker.Reset(interval)
		<-ticker.C
		if b.Status() != StatusRunning {
			return
		}
		b.updateProgressFromFile(agentID, &st)
	}
}

// updateProgressFromFile reads recent records from the output file and
// updates result.Metadata["summary"], ["last_tool"], ["tool_count"].
// Only the bytes appended since the last call are read and parsed; st carries
// the scan position and the cumulative tool count across calls.
func (b *BackgroundAgent) updateProgressFromFile(agentID string, st *progressScanState) {
	r, err := ReadOutputStore(agentID, st.offset, 0)
	if err != nil {
		return
	}
	if r.TotalBytes < st.offset {
		// The store was truncated or replaced. Restart the scan rather than
		// sitting at an offset past EOF and never reading again.
		st.offset = 0
		st.toolCount = 0
		return
	}
	if r.BytesRead == 0 {
		return
	}

	// Consume only up to the last complete record. A chunk can end mid-line;
	// advancing past a partial record would drop it permanently.
	content := r.Content
	lastNL := strings.LastIndexByte(content, '\n')
	if lastNL < 0 {
		return // no complete record yet — retry on the next tick
	}
	content = content[:lastNL+1]
	st.offset += int64(len(content))

	records := ParseOutputStoreRecords(content)
	if len(records) == 0 {
		return
	}

	// Walk backwards to find latest content, latest tool call, and count tools.
	var latestContent string
	var latestTool string
	toolCount := 0
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		switch rec.Type {
		case "content", "final":
			if latestContent == "" && rec.Content != "" {
				// Take the first 200 chars as a summary snippet.
				snip := rec.Content
				if len(snip) > 200 {
					snip = snip[:197] + "..."
				}
				latestContent = snip
			}
		case "tool_call":
			if latestTool == "" && rec.Name != "" {
				latestTool = rec.Name
			}
			toolCount++
		}
	}
	st.toolCount += toolCount

	b.mu.Lock()
	if b.result != nil {
		if b.result.Metadata == nil {
			b.result.Metadata = make(map[string]any)
		}
		if latestContent != "" {
			b.result.Metadata["summary"] = latestContent
		}
		if latestTool != "" {
			b.result.Metadata["last_tool"] = latestTool
		}
		if st.toolCount > 0 {
			b.result.Metadata["tool_count"] = st.toolCount
		}
		b.result.Metadata["summary_at"] = time.Now()
	}
	b.mu.Unlock()
}

// ReportProgress allows the agent to report custom progress updates.
// This is typically called from within tool implementations.
func (b *BackgroundAgent) ReportProgress(progress int, message string, metadata map[string]any) {
	b.mu.RLock()
	status := b.status
	agentID := b.agent.ID()
	b.mu.RUnlock()

	if status != StatusRunning {
		return
	}

	b.emitEvent(BackgroundAgentEvent{
		AgentID:   agentID,
		EventType: EventProgress,
		Status:    StatusRunning,
		Message:   message,
		Progress:  progress,
		Timestamp: time.Now(),
		Metadata:  metadata,
	})
}
