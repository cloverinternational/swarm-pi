// Package managed provides the ManagedBackgroundAgent for remote async execution.
package managed

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// AgentDefinition is a minimal interface for agent definition data.
// This interface breaks the import cycle between managed and agent packages.
type AgentDefinition interface {
	// ID returns the agent's unique identifier.
	ID() string
	// GetModel returns the model name.
	GetModel() string
	// GetManagedConfig returns the managed execution config.
	GetManagedConfig() *ManagedExecutionConfig
}

// ManagedExecutionConfig contains configuration for managed execution.
// This is a minimal version that the agent package can convert from agent.ManagedConfig.
type ManagedExecutionConfig struct {
	Endpoint   string
	APIKey     string
	ProjectID  string
	SessionID  string
	Timeout    time.Duration
	MaxRetries int
	EnableSSE  bool
}

// BackgroundAgentStatus represents the current state of a background agent.
type BackgroundAgentStatus string

const (
	// StatusPending means the agent hasn't started yet.
	StatusPending BackgroundAgentStatus = "pending"
	// StatusRunning means the agent is currently executing.
	StatusRunning BackgroundAgentStatus = "running"
	// StatusCompleted means the agent finished successfully.
	StatusCompleted BackgroundAgentStatus = "completed"
	// StatusFailed means the agent encountered an error.
	StatusFailed BackgroundAgentStatus = "failed"
	// StatusCancelled means the agent was cancelled.
	StatusCancelled BackgroundAgentStatus = "cancelled"
)

// BackgroundEventType classifies the event.
type BackgroundEventType string

const (
	// EventStarted is emitted when execution begins.
	EventStarted BackgroundEventType = "started"
	// EventProgress is emitted during execution with updates.
	EventProgress BackgroundEventType = "progress"
	// EventCompleted is emitted on successful completion.
	EventCompleted BackgroundEventType = "completed"
	// EventFailed is emitted on failure.
	EventFailed BackgroundEventType = "failed"
	// EventCancelled is emitted on cancellation.
	EventCancelled BackgroundEventType = "cancelled"
)

// BackgroundAgentEvent represents an event emitted during background execution.
type BackgroundAgentEvent struct {
	// AgentID identifies the agent.
	AgentID string
	// EventType is the type of event.
	EventType BackgroundEventType
	// Status is the current status.
	Status BackgroundAgentStatus
	// Message is a human-readable message.
	Message string
	// Progress is a percentage (0-100).
	Progress int
	// Timestamp when the event occurred.
	Timestamp time.Time
	// Metadata contains additional event data.
	Metadata map[string]any
}

// BackgroundAgentResult stores the final result of background execution.
type BackgroundAgentResult struct {
	// AgentID identifies the agent.
	AgentID string
	// Status is the final status.
	Status BackgroundAgentStatus
	// Result is the agent's response (if successful).
	Result string
	// Error is the error (if failed).
	Error error
	// StartTime when execution started.
	StartTime time.Time
	// EndTime when execution finished.
	EndTime time.Time
	// Duration of execution.
	Duration time.Duration
	// TurnCount is the number of turns executed.
	TurnCount int
	// TokensUsed is the total tokens consumed.
	TokensUsed int
	// CostUSD is the estimated cost.
	CostUSD float64
	// Metadata contains additional result data.
	Metadata map[string]any
}

// ManagedBackgroundAgent executes an agent remotely via Swarm Cloud.
// It implements the same event-based interface as agent.BackgroundAgent
// but routes execution through the managed service.
type ManagedBackgroundAgent struct {
	definition AgentDefinition
	client     *Client
	provider   *ManagedProvider

	mu         sync.RWMutex
	status     BackgroundAgentStatus
	result     *BackgroundAgentResult
	cancelFunc context.CancelFunc

	// Event channel (buffered)
	events chan BackgroundAgentEvent

	logger *slog.Logger
}

// ManagedBackgroundAgentConfig configures the managed background agent.
type ManagedBackgroundAgentConfig struct {
	// Definition is the agent definition.
	Definition AgentDefinition

	// Client is the managed API client.
	Client *Client

	// EventBufferSize is the size of the event channel (default: 100).
	EventBufferSize int

	// Logger for structured logging.
	Logger *slog.Logger
}

// NewManagedBackgroundAgent creates a new managed background agent.
func NewManagedBackgroundAgent(cfg ManagedBackgroundAgentConfig) (*ManagedBackgroundAgent, error) {
	if cfg.Definition == nil {
		return nil, fmt.Errorf("definition is required")
	}
	if cfg.Client == nil {
		return nil, fmt.Errorf("client is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	eventBufferSize := cfg.EventBufferSize
	if eventBufferSize == 0 {
		eventBufferSize = 100
	}

	// Create managed provider from definition
	managedConfig := ManagedProviderConfig{
		Client: cfg.Client,
		Model:  cfg.Definition.GetModel(),
	}
	if managedCfg := cfg.Definition.GetManagedConfig(); managedCfg != nil {
		managedConfig.ProjectID = managedCfg.ProjectID
		managedConfig.SessionID = managedCfg.SessionID
	}

	provider, err := NewManagedProvider(managedConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed provider: %w", err)
	}

	return &ManagedBackgroundAgent{
		definition: cfg.Definition,
		client:     cfg.Client,
		provider:   provider,
		status:     StatusPending,
		events:     make(chan BackgroundAgentEvent, eventBufferSize),
		logger:     logger,
	}, nil
}

// Start begins the managed execution.
// It returns immediately and sends events through the Events() channel.
func (a *ManagedBackgroundAgent) Start(ctx context.Context, input string) error {
	a.mu.Lock()
	if a.status != StatusPending {
		a.mu.Unlock()
		return fmt.Errorf("agent already started with status: %s", a.status)
	}
	a.status = StatusRunning
	a.mu.Unlock()

	// Create cancellable context
	execCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancelFunc = cancel
	a.mu.Unlock()

	// Emit started event
	a.emitEvent(BackgroundAgentEvent{
		EventType: EventStarted,
		Status:    StatusRunning,
		Message:   "Managed execution started",
		Timestamp: time.Now(),
	})

	// Start execution in background
	go a.execute(execCtx, input)

	return nil
}

// execute runs the managed execution loop.
func (a *ManagedBackgroundAgent) execute(ctx context.Context, input string) {
	startTime := time.Now()

	defer func() {
		a.mu.Lock()
		if a.status == StatusRunning {
			a.status = StatusCompleted
		}
		a.mu.Unlock()
	}()

	// Ensure session is started
	if err := a.provider.ensureSession(ctx); err != nil {
		a.handleError(err, startTime)
		return
	}

	// Send initial input
	if err := a.client.SendMessage(ctx, a.provider.SessionID(), "user", input); err != nil {
		a.handleError(err, startTime)
		return
	}

	// Stream events from the managed service
	eventCh, err := a.client.StreamEvents(ctx, a.provider.SessionID())
	if err != nil {
		a.handleError(err, startTime)
		return
	}

	// Process events
	var content string
	var totalInputTokens int
	var totalOutputTokens int
	turnCount := 0

	for {
		select {
		case <-ctx.Done():
			a.handleCancel(startTime)
			return

		case event, ok := <-eventCh:
			if !ok {
				// Stream closed, we're done
				a.handleCompletion(content, startTime, turnCount, totalInputTokens, totalOutputTokens)
				return
			}

			// Convert managed event to background event
			bgEvent := a.convertEvent(event)
			a.emitEvent(bgEvent)

			// Track progress
			if event.Type == "content" || event.Type == "text" {
				if c, ok := event.Data["content"].(string); ok {
					content += c
				} else if event.Content != "" {
					content += event.Content
				}
			}

			// Track tokens
			if input, ok := event.Data["input_tokens"].(int); ok {
				totalInputTokens += input
			} else if input, ok := event.Data["input_tokens"].(float64); ok {
				totalInputTokens += int(input)
			}
			if output, ok := event.Data["output_tokens"].(int); ok {
				totalOutputTokens += output
			} else if output, ok := event.Data["output_tokens"].(float64); ok {
				totalOutputTokens += int(output)
			}

			// Track turns
			if event.Type == "turn_complete" || event.Type == "done" {
				turnCount++
			}

			// Handle errors
			if event.Type == "error" {
				errMsg := "unknown error"
				if e, ok := event.Data["error"].(string); ok {
					errMsg = e
				}
				a.handleError(fmt.Errorf("%s", errMsg), startTime)
				return
			}
		}
	}
}

// convertEvent converts a managed Event to a BackgroundAgentEvent.
func (a *ManagedBackgroundAgent) convertEvent(event Event) BackgroundAgentEvent {
	bgEvent := BackgroundAgentEvent{
		AgentID:   a.definition.ID(),
		Timestamp: time.Now(),
		Metadata:  event.Data,
	}

	switch event.Type {
	case "thinking":
		bgEvent.EventType = EventProgress
		bgEvent.Status = StatusRunning
		bgEvent.Message = "Processing..."
		if content, ok := event.Data["content"].(string); ok {
			bgEvent.Message = content
		}

	case "content", "text":
		bgEvent.EventType = EventProgress
		bgEvent.Status = StatusRunning
		bgEvent.Message = "Generating response..."

	case "tool_call":
		bgEvent.EventType = EventProgress
		bgEvent.Status = StatusRunning
		if name, ok := event.Data["name"].(string); ok {
			bgEvent.Message = fmt.Sprintf("Calling tool: %s", name)
		} else {
			bgEvent.Message = "Executing tool..."
		}

	case "done", "complete":
		bgEvent.EventType = EventCompleted
		bgEvent.Status = StatusCompleted
		bgEvent.Message = "Execution completed"

	case "error":
		bgEvent.EventType = EventFailed
		bgEvent.Status = StatusFailed
		if errMsg, ok := event.Data["error"].(string); ok {
			bgEvent.Message = errMsg
		} else {
			bgEvent.Message = "Execution failed"
		}

	default:
		bgEvent.EventType = EventProgress
		bgEvent.Status = StatusRunning
		bgEvent.Message = fmt.Sprintf("Event: %s", event.Type)
	}

	return bgEvent
}

// emitEvent sends an event through the event channel.
func (a *ManagedBackgroundAgent) emitEvent(event BackgroundAgentEvent) {
	select {
	case a.events <- event:
	default:
		a.logger.Warn("event channel full, dropping event", "event_type", event.EventType)
	}
}

// handleCompletion handles successful completion.
func (a *ManagedBackgroundAgent) handleCompletion(content string, startTime time.Time, turns, inputTokens, outputTokens int) {
	endTime := time.Now()

	a.mu.Lock()
	a.status = StatusCompleted
	a.result = &BackgroundAgentResult{
		AgentID:    a.definition.ID(),
		Status:     StatusCompleted,
		Result:     content,
		StartTime:  startTime,
		EndTime:    endTime,
		Duration:   endTime.Sub(startTime),
		TurnCount:  turns,
		TokensUsed: inputTokens + outputTokens,
	}
	a.mu.Unlock()

	a.emitEvent(BackgroundAgentEvent{
		EventType: EventCompleted,
		Status:    StatusCompleted,
		Message:   "Execution completed successfully",
		Timestamp: endTime,
	})
}

// handleError handles an execution error.
func (a *ManagedBackgroundAgent) handleError(err error, startTime time.Time) {
	endTime := time.Now()

	a.mu.Lock()
	a.status = StatusFailed
	a.result = &BackgroundAgentResult{
		AgentID:   a.definition.ID(),
		Status:    StatusFailed,
		Error:     err,
		StartTime: startTime,
		EndTime:   endTime,
		Duration:  endTime.Sub(startTime),
	}
	a.mu.Unlock()

	a.emitEvent(BackgroundAgentEvent{
		EventType: EventFailed,
		Status:    StatusFailed,
		Message:   err.Error(),
		Timestamp: endTime,
	})
}

// handleCancel handles cancellation.
func (a *ManagedBackgroundAgent) handleCancel(startTime time.Time) {
	endTime := time.Now()

	a.mu.Lock()
	a.status = StatusCancelled
	a.result = &BackgroundAgentResult{
		AgentID:   a.definition.ID(),
		Status:    StatusCancelled,
		StartTime: startTime,
		EndTime:   endTime,
		Duration:  endTime.Sub(startTime),
	}
	a.mu.Unlock()

	a.emitEvent(BackgroundAgentEvent{
		EventType: EventCancelled,
		Status:    StatusCancelled,
		Message:   "Execution cancelled",
		Timestamp: endTime,
	})
}

// Cancel cancels the running execution.
func (a *ManagedBackgroundAgent) Cancel() {
	a.mu.RLock()
	cancelFunc := a.cancelFunc
	a.mu.RUnlock()

	if cancelFunc != nil {
		cancelFunc()
	}
}

// Events returns the event channel.
func (a *ManagedBackgroundAgent) Events() <-chan BackgroundAgentEvent {
	return a.events
}

// Status returns the current execution status.
func (a *ManagedBackgroundAgent) Status() BackgroundAgentStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

// Result returns the final result (nil if not completed).
func (a *ManagedBackgroundAgent) Result() *BackgroundAgentResult {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.result
}

// Wait blocks until execution completes and returns the result.
func (a *ManagedBackgroundAgent) Wait(ctx context.Context) (*BackgroundAgentResult, error) {
	for {
		a.mu.RLock()
		status := a.status
		result := a.result
		a.mu.RUnlock()

		// Check if already completed
		if status == StatusCompleted || status == StatusFailed || status == StatusCancelled {
			return result, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-a.events:
			if !ok {
				// Channel closed
				return a.Result(), nil
			}
			_ = event // Just drain the event
		case <-time.After(100 * time.Millisecond):
			// Poll for status change
		}
	}
}

// Close stops the managed session and releases resources.
func (a *ManagedBackgroundAgent) Close(ctx context.Context) error {
	if a.provider != nil {
		return a.provider.Close(ctx)
	}
	return nil
}

// SessionID returns the managed session ID.
func (a *ManagedBackgroundAgent) SessionID() string {
	if a.provider == nil {
		return ""
	}
	return a.provider.SessionID()
}

// Provider returns the underlying managed provider.
func (a *ManagedBackgroundAgent) Provider() *ManagedProvider {
	return a.provider
}
