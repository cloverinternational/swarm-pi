// Package chat provides background agent management for concurrent conversations.
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// RunningAgentStatus represents the current state of a background agent.
type RunningAgentStatus string

const (
	AgentStatusRunning   RunningAgentStatus = "running"
	AgentStatusCompleted RunningAgentStatus = "completed"
	AgentStatusFailed    RunningAgentStatus = "failed"
	AgentStatusCancelled RunningAgentStatus = "cancelled"
)

// PersistedAgentState is saved to JSON for restart capability.
type PersistedAgentState struct {
	ConvID        string             `json:"conv_id"`
	Status        RunningAgentStatus `json:"status"`
	StartedAt     time.Time          `json:"started_at"`
	EndedAt       time.Time          `json:"ended_at"`
	LastUpdate    time.Time          `json:"last_update"`
	ErrorMessage  string             `json:"error_message,omitempty"`
	FinalResponse string             `json:"final_response,omitempty"`
	Model         string             `json:"model,omitempty"`
	LastUserMsg   string             `json:"last_user_msg,omitempty"`
}

// RunningAgent tracks a single agent running in the background.
type RunningAgent struct {
	ConvID      string
	Status      RunningAgentStatus
	StartedAt   time.Time
	EndedAt     time.Time
	LastUpdate  time.Time
	Error       error
	Model       string
	LastUserMsg string

	// Cancellation
	ctx        context.Context
	cancelFunc context.CancelFunc

	// Update fan-out: source channel and subscribers
	sourceUpdate chan agent.IntermediateUpdate
	sourceClosed bool
	subscribers  map[string]chan agent.IntermediateUpdate
	updateBuffer []agent.IntermediateUpdate // All updates for reconnection

	// Final response (set on completion)
	FinalResponse string
	AllMessages   []*Message

	mu sync.RWMutex
}

func (ra *RunningAgent) closeSourceUpdateLocked() {
	if ra == nil || ra.sourceUpdate == nil || ra.sourceClosed {
		return
	}
	close(ra.sourceUpdate)
	ra.sourceClosed = true
}

// BackgroundAgentManager manages all running background agents.
type BackgroundAgentManager struct {
	agents      map[string]*RunningAgent
	storagePath string
	mu          sync.RWMutex
}

// NewBackgroundAgentManager creates a new background agent manager.
func NewBackgroundAgentManager() *BackgroundAgentManager {
	home, _ := os.UserHomeDir()
	storagePath := filepath.Join(home, ".swarmos", "running_agents.json")

	mgr := &BackgroundAgentManager{
		agents:      make(map[string]*RunningAgent),
		storagePath: storagePath,
	}

	logDebug("[BGManager] Initializing background agent manager, storage: %s", storagePath)

	// Load persisted state on startup
	mgr.loadPersistedState()

	logDebug("[BGManager] Initialized with %d agents loaded from storage", len(mgr.agents))

	return mgr
}

// loadPersistedState loads agent state from JSON file.
// IMPORTANT: Agents that were "running" when app quit are now stale and cannot be resumed.
// We clear the persisted file on startup since we can't resume running agents anyway.
func (m *BackgroundAgentManager) loadPersistedState() {
	data, err := os.ReadFile(m.storagePath)
	if err != nil {
		return // File doesn't exist or can't read - that's fine
	}

	var states []PersistedAgentState
	if err := json.Unmarshal(data, &states); err != nil {
		logDebug("[BGManager] Failed to parse persisted state: %v", err)
		return
	}

	// Count stale agents for logging
	staleCount := 0
	for _, state := range states {
		if state.Status == AgentStatusRunning {
			staleCount++
			logDebug("[BGManager] Found stale running agent for conversation %s - will not restore", state.ConvID)
		}
	}

	// FIX: Don't restore "running" agents - they're stale and can't be resumed
	// The goroutine that was executing is gone, the context is gone, etc.
	// Users need to manually restart if they want to continue.
	if staleCount > 0 {
		logDebug("[BGManager] Clearing %d stale running agents from persisted state", staleCount)
		// Clear the file to prevent confusion on next startup
		_ = os.Remove(m.storagePath)
	}

	// Note: We intentionally don't load any agents on startup
	// This ensures a clean slate and prevents "ghost" running indicators
}

// persistState saves current agent state to JSON file.
func (m *BackgroundAgentManager) persistState() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var states []PersistedAgentState
	for _, ra := range m.agents {
		ra.mu.RLock()
		state := PersistedAgentState{
			ConvID:        ra.ConvID,
			Status:        ra.Status,
			StartedAt:     ra.StartedAt,
			EndedAt:       ra.EndedAt,
			LastUpdate:    ra.LastUpdate,
			FinalResponse: ra.FinalResponse,
			Model:         ra.Model,
			LastUserMsg:   ra.LastUserMsg,
		}
		if ra.Error != nil {
			state.ErrorMessage = ra.Error.Error()
		}
		ra.mu.RUnlock()
		states = append(states, state)
	}

	data, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		logDebug("[BGManager] Failed to marshal state: %v", err)
		return
	}

	// Ensure directory exists
	dir := filepath.Dir(m.storagePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logDebug("[BGManager] Failed to create directory: %v", err)
		return
	}

	if err := os.WriteFile(m.storagePath, data, 0644); err != nil {
		logDebug("[BGManager] Failed to write state: %v", err)
	}
}

// Register registers a new running agent for a conversation.
// Returns a source channel that agent execution should write to.
func (m *BackgroundAgentManager) Register(parentCtx context.Context, convID string, model string, userMsg string) (chan<- agent.IntermediateUpdate, context.Context, context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Cancel any existing agent for this conversation
	if existing, ok := m.agents[convID]; ok {
		existing.mu.Lock()
		if existing.cancelFunc != nil && existing.Status == AgentStatusRunning {
			existing.cancelFunc()
		}
		existing.mu.Unlock()
	}

	// Create cancellable context from parent context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, cancel := context.WithCancel(parentCtx)

	// Create source channel (unbuffered to not lose updates)
	sourceChan := make(chan agent.IntermediateUpdate, 1000)

	ra := &RunningAgent{
		ConvID:       convID,
		Status:       AgentStatusRunning,
		StartedAt:    time.Now(),
		LastUpdate:   time.Now(),
		Model:        model,
		LastUserMsg:  userMsg,
		ctx:          ctx,
		cancelFunc:   cancel,
		sourceUpdate: sourceChan,
		subscribers:  make(map[string]chan agent.IntermediateUpdate),
		updateBuffer: make([]agent.IntermediateUpdate, 0),
	}

	m.agents[convID] = ra

	// Start fan-out goroutine
	go m.fanOutUpdates(ra)

	// Persist state
	go m.persistState()

	logDebug("[BGManager] Registered agent for conversation %s", convID)
	return sourceChan, ctx, cancel
}

// fanOutUpdates distributes updates from source to all subscribers.
func (m *BackgroundAgentManager) fanOutUpdates(ra *RunningAgent) {

	updateCount := 0
	for update := range ra.sourceUpdate {
		updateCount++
		ra.mu.Lock()

		// Store all updates (no limit)
		ra.updateBuffer = append(ra.updateBuffer, update)
		ra.LastUpdate = time.Now()

		// Fan out to all subscribers
		successCount := 0
		droppedCount := 0
		for subID, subChan := range ra.subscribers {
			select {
			case subChan <- update:
				// Sent successfully
				successCount++
			default:
				// Subscriber channel full, skip this update
				droppedCount++
				logDebug("[BGManager] Subscriber %s channel full, dropping update", subID)
			}
		}

		ra.mu.Unlock()
	}

	logDebug("[BGManager] Source channel closed for conversation %s", ra.ConvID)
	ra.mu.Lock()
	for subID, subChan := range ra.subscribers {
		close(subChan)
		delete(ra.subscribers, subID)
	}
	ra.mu.Unlock()
}

// Subscribe creates a new subscription to a running agent's updates.
// Returns a channel that receives updates and the buffered updates that were missed.
// Returns nil, nil if no agent exists for the conversation.
func (m *BackgroundAgentManager) Subscribe(convID string, subscriberID string) (<-chan agent.IntermediateUpdate, []agent.IntermediateUpdate) {

	if m == nil || convID == "" {
		logDebug("[BGManager] Subscribe called with nil manager or empty convID")
		return nil, nil
	}

	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		logDebug("[BGManager] Subscribe(%s, %s): no agent found for conversation", convID, subscriberID)
		return nil, nil
	}

	ra.mu.Lock()
	defer ra.mu.Unlock()

	// Don't subscribe to completed/failed/cancelled agents
	if ra.Status != AgentStatusRunning {
		logDebug("[BGManager] Subscribe(%s, %s): agent not running (status=%s)", convID, subscriberID, ra.Status)
		return nil, nil
	}

	// Create subscriber channel with large buffer for high-throughput streaming
	subChan := make(chan agent.IntermediateUpdate, 10000)
	ra.subscribers[subscriberID] = subChan

	// Return copy of all buffered updates
	buffered := make([]agent.IntermediateUpdate, len(ra.updateBuffer))
	copy(buffered, ra.updateBuffer)

	logDebug("[BGManager] Subscriber %s subscribed to conversation %s (buffered: %d)", subscriberID, convID, len(buffered))

	return subChan, buffered
}

// Unsubscribe removes a subscription.
func (m *BackgroundAgentManager) Unsubscribe(convID string, subscriberID string) {
	if m == nil || convID == "" {
		return
	}

	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		logDebug("[BGManager] Unsubscribe(%s, %s): no agent found", convID, subscriberID)
		return
	}

	ra.mu.Lock()
	defer ra.mu.Unlock()

	if subChan, exists := ra.subscribers[subscriberID]; exists {
		close(subChan)
		delete(ra.subscribers, subscriberID)
		logDebug("[BGManager] Subscriber %s unsubscribed from conversation %s", subscriberID, convID)
	} else {
		logDebug("[BGManager] Unsubscribe(%s, %s): subscriber not found", convID, subscriberID)
	}
}

// MarkComplete marks an agent as completed.
func (m *BackgroundAgentManager) MarkComplete(convID string, response string, allMessages []*Message, err error) {
	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		return
	}

	ra.mu.Lock()

	if err != nil {
		ra.Status = AgentStatusFailed
		ra.Error = err
	} else {
		ra.Status = AgentStatusCompleted
	}
	ra.EndedAt = time.Now()
	ra.FinalResponse = response
	ra.AllMessages = allMessages

	// Close source channel to let fan-out drain queued updates,
	// then fan-out closes subscribers after delivery.
	ra.closeSourceUpdateLocked()

	ra.mu.Unlock()

	// Persist state
	go m.persistState()

	logDebug("[BGManager] Agent marked complete for conversation %s (status: %s)", convID, ra.Status)
}

// Cancel cancels a running agent.
func (m *BackgroundAgentManager) Cancel(convID string) {
	if m == nil || convID == "" {
		logDebug("[BGManager] Cancel: nil manager or empty convID")
		return
	}

	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		logDebug("[BGManager] Cancel(%s): no agent found", convID)
		return
	}

	ra.mu.Lock()
	defer ra.mu.Unlock()

	if ra.Status == AgentStatusRunning && ra.cancelFunc != nil {
		logDebug("[BGManager] Cancel(%s): cancelling running agent", convID)
		ra.cancelFunc()
		ra.Status = AgentStatusCancelled
		ra.EndedAt = time.Now()

		// Persist state
		go m.persistState()

		logDebug("[BGManager] Agent cancelled for conversation %s", convID)
	} else {
		logDebug("[BGManager] Cancel(%s): agent not running or no cancel func (status=%s)", convID, ra.Status)
	}
}

// IsRunning returns true if an agent is running for the conversation.
func (m *BackgroundAgentManager) IsRunning(convID string) bool {
	if m == nil || convID == "" {
		return false
	}

	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		return false
	}

	ra.mu.RLock()
	status := ra.Status
	ra.mu.RUnlock()

	isRunning := status == AgentStatusRunning
	logDebug("[BGManager] IsRunning(%s) = %v (status=%s)", convID, isRunning, status)
	return isRunning
}

// GetStatus returns the running agent info for a conversation.
func (m *BackgroundAgentManager) GetStatus(convID string) *RunningAgent {
	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		return nil
	}

	return ra
}

// GetRunningStatus returns just the status for a conversation.
func (m *BackgroundAgentManager) GetRunningStatus(convID string) RunningAgentStatus {
	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		return ""
	}

	ra.mu.RLock()
	defer ra.mu.RUnlock()
	return ra.Status
}

// ListRunning returns IDs of all conversations with running agents.
func (m *BackgroundAgentManager) ListRunning() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var running []string
	for convID, ra := range m.agents {
		ra.mu.RLock()
		if ra.Status == AgentStatusRunning {
			running = append(running, convID)
		}
		ra.mu.RUnlock()
	}
	return running
}

// GetCompletionResult returns the final result if agent completed while user was away.
// Returns completed=false if no agent exists or agent is still running.
func (m *BackgroundAgentManager) GetCompletionResult(convID string) (response string, allMessages []*Message, err error, completed bool) {
	if m == nil || convID == "" {
		logDebug("[BGManager] GetCompletionResult: nil manager or empty convID")
		return "", nil, nil, false
	}

	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok {
		logDebug("[BGManager] GetCompletionResult(%s): no agent found", convID)
		return "", nil, nil, false
	}

	ra.mu.RLock()
	defer ra.mu.RUnlock()

	if ra.Status == AgentStatusRunning {
		logDebug("[BGManager] GetCompletionResult(%s): agent still running", convID)
		return "", nil, nil, false
	}

	logDebug("[BGManager] GetCompletionResult(%s): returning result (status=%s, response=%d chars, messages=%d)",
		convID, ra.Status, len(ra.FinalResponse), len(ra.AllMessages))
	return ra.FinalResponse, ra.AllMessages, ra.Error, true
}

// NeedsRestart returns agents that need to be restarted after app restart.
func (m *BackgroundAgentManager) NeedsRestart() []PersistedAgentState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var needsRestart []PersistedAgentState
	for _, ra := range m.agents {
		ra.mu.RLock()
		// Agent was running but has no active context (app restarted)
		if ra.Status == AgentStatusRunning && ra.ctx == nil {
			needsRestart = append(needsRestart, PersistedAgentState{
				ConvID:      ra.ConvID,
				Status:      ra.Status,
				StartedAt:   ra.StartedAt,
				Model:       ra.Model,
				LastUserMsg: ra.LastUserMsg,
			})
		}
		ra.mu.RUnlock()
	}
	return needsRestart
}

// Remove removes an agent from tracking.
func (m *BackgroundAgentManager) Remove(convID string) {
	if m == nil || convID == "" {
		logDebug("[BGManager] Remove: nil manager or empty convID")
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if ra, ok := m.agents[convID]; ok {
		logDebug("[BGManager] Remove(%s): removing agent", convID)
		ra.mu.Lock()
		if ra.Status == AgentStatusRunning && ra.cancelFunc != nil {
			ra.cancelFunc()
		}
		ra.closeSourceUpdateLocked()
		for subID, subChan := range ra.subscribers {
			close(subChan)
			delete(ra.subscribers, subID)
		}
		ra.mu.Unlock()

		delete(m.agents, convID)

		// Persist state
		go m.persistState()

		logDebug("[BGManager] Removed agent for conversation %s", convID)
	} else {
		logDebug("[BGManager] Remove(%s): no agent found", convID)
	}
}

// Cleanup removes completed/failed agents older than maxAge.
func (m *BackgroundAgentManager) Cleanup(maxAge time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var toRemove []string

	for convID, ra := range m.agents {
		ra.mu.RLock()
		shouldRemove := ra.Status != AgentStatusRunning && now.Sub(ra.EndedAt) > maxAge
		ra.mu.RUnlock()

		if shouldRemove {
			toRemove = append(toRemove, convID)
		}
	}

	for _, convID := range toRemove {
		delete(m.agents, convID)
		logDebug("[BGManager] Cleaned up agent for conversation %s", convID)
	}

	if len(toRemove) > 0 {
		go m.persistState()
	}
}

// Context returns the context for a running agent.
func (m *BackgroundAgentManager) Context(convID string) context.Context {
	m.mu.RLock()
	ra, ok := m.agents[convID]
	m.mu.RUnlock()

	if !ok || ra.ctx == nil {
		return context.Background()
	}

	return ra.ctx
}

// Count returns the number of running agents.
func (m *BackgroundAgentManager) Count() int {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, ra := range m.agents {
		ra.mu.RLock()
		if ra.Status == AgentStatusRunning {
			count++
		}
		ra.mu.RUnlock()
	}
	return count
}

// ===============================================================================
// SDK Background Agent Manager (for tools)
// ===============================================================================

// SDKBackgroundAgentManager manages SDK BackgroundAgent instances spawned by tools.
// This is separate from the conversation-level BackgroundAgentManager above.
type SDKBackgroundAgentManager struct {
	agents     map[string]*agent.BackgroundAgent
	tasks      map[string]string // agent_id -> task description
	events     chan SDKBackgroundAgentEvent
	mu         sync.RWMutex
	stopChan   chan struct{}
	wg         sync.WaitGroup
	onComplete func(agentID, task, status, outputFile, errMsg, resultText string) // Optional callback for terminal states
}

// SDKBackgroundAgentEvent represents an event from an SDK background agent.
type SDKBackgroundAgentEvent struct {
	AgentID   string
	EventType string
	Status    agent.BackgroundAgentStatus
	Message   string
	Progress  int
	Timestamp time.Time
	Metadata  map[string]any
}

// NewSDKBackgroundAgentManager creates a new SDK background agent manager.
func NewSDKBackgroundAgentManager() *SDKBackgroundAgentManager {
	return &SDKBackgroundAgentManager{
		agents:   make(map[string]*agent.BackgroundAgent),
		tasks:    make(map[string]string),
		events:   make(chan SDKBackgroundAgentEvent, 100),
		stopChan: make(chan struct{}),
	}
}

// Add registers a new SDK background agent and starts monitoring its events.
func (m *SDKBackgroundAgentManager) Add(bg *agent.BackgroundAgent, task string) error {
	if bg == nil {
		return fmt.Errorf("background agent cannot be nil")
	}

	agentID := bg.AgentID()

	m.mu.Lock()
	if _, exists := m.agents[agentID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("agent with ID '%s' already exists", agentID)
	}

	m.agents[agentID] = bg
	m.tasks[agentID] = task
	m.mu.Unlock()

	// Start monitoring events from this agent
	m.wg.Add(1)
	go m.monitorAgent(bg)

	logDebug("[SDKBGManager] Added agent %s for task: %s", agentID, task)
	return nil
}

// Get retrieves an SDK background agent by ID.
func (m *SDKBackgroundAgentManager) Get(agentID string) (*agent.BackgroundAgent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	bg, exists := m.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent '%s' not found", agentID)
	}

	return bg, nil
}

// List returns information about all SDK background agents.
func (m *SDKBackgroundAgentManager) List() []builtin.BackgroundAgentInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]builtin.BackgroundAgentInfo, 0, len(m.agents))

	for agentID, bg := range m.agents {
		// Result() is nil while the agent is still running (result is only set
		// on a terminal state), so every field access must be guarded — reading
		// result.StartTime/Duration unconditionally panics mid-run.
		result := bg.Result()
		info := builtin.BackgroundAgentInfo{
			AgentID:  agentID,
			ParentID: bg.ParentID(),
			Task:     m.tasks[agentID],
			Status:   bg.Status(),
			Progress: 0, // Progress not directly available
			Result:   result,
		}
		if result != nil {
			info.StartTime = result.StartTime
			info.Duration = result.Duration
			// Populate progress metadata written by StartProgressTracker.
			if result.Metadata != nil {
				if s, ok := result.Metadata["summary"].(string); ok {
					info.Summary = s
				}
				if t, ok := result.Metadata["last_tool"].(string); ok {
					info.LastTool = t
				}
				if tc, ok := result.Metadata["tool_count"].(int); ok {
					info.ToolCount = tc
				}
			}
		}
		infos = append(infos, info)
	}

	sort.SliceStable(infos, func(i, j int) bool {
		if !infos[i].StartTime.Equal(infos[j].StartTime) {
			return infos[i].StartTime.Before(infos[j].StartTime)
		}
		return infos[i].AgentID < infos[j].AgentID
	})

	return infos
}

// Cancel cancels a running SDK background agent.
func (m *SDKBackgroundAgentManager) Cancel(agentID string) error {
	m.mu.RLock()
	bg, exists := m.agents[agentID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("agent '%s' not found", agentID)
	}

	bg.Cancel()
	logDebug("[SDKBGManager] Cancelled agent %s", agentID)
	return nil
}

// Remove removes an SDK background agent from tracking.
func (m *SDKBackgroundAgentManager) Remove(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.agents[agentID]; !exists {
		return fmt.Errorf("agent '%s' not found", agentID)
	}

	delete(m.agents, agentID)
	delete(m.tasks, agentID)
	logDebug("[SDKBGManager] Removed agent %s", agentID)
	return nil
}

// Events returns a channel that receives events from all SDK background agents.
func (m *SDKBackgroundAgentManager) Events() <-chan SDKBackgroundAgentEvent {
	return m.events
}

// monitorAgent monitors events from a single SDK background agent and forwards them.
//
// Two completion signals are watched:
//  1. bg.Done() — the authoritative guaranteed signal (channel close is atomic,
//     never dropped unlike emitEvent which is non-blocking).
//  2. Terminal status events on eventChan — fast-path early detection when
//     the event buffer is healthy.
//
// sync.Once ensures the completion callback fires exactly once regardless
// of which signal arrives first.
func (m *SDKBackgroundAgentManager) monitorAgent(bg *agent.BackgroundAgent) {
	defer m.wg.Done()

	agentID := bg.AgentID()
	eventChan := bg.Events()
	doneChan := bg.Done()

	// fireCompletion reads the final result and invokes the onComplete callback.
	// Wrapped in sync.Once so it fires exactly once even if both paths trigger.
	var cbOnce sync.Once
	fireCompletion := func() {
		finalStatus := bg.Status()
		logDebug("[SDKBGManager] Agent %s completed (status: %s)", agentID, finalStatus)
		m.mu.RLock()
		cb := m.onComplete
		task := m.tasks[agentID]
		m.mu.RUnlock()
		if cb != nil {
			result := bg.Result()
			outputFile := ""
			errMsg := ""
			resultText := ""
			if result != nil {
				outputFile = result.OutputFile
				resultText = result.Result
				if result.Error != nil {
					errMsg = result.Error.Error()
				}
			}
			cb(agentID, task, string(finalStatus), outputFile, errMsg, resultText)
		}
		go m.autoRemove(agentID, 24*time.Hour)
	}

	for {
		select {
		case <-m.stopChan:
			return

		case <-doneChan:
			// Authoritative guaranteed completion signal. The done channel is
			// closed atomically after all state is set in handleSuccess/Failure/
			// Cancellation. This fires regardless of whether the event buffer
			// was full and the terminal event was dropped.
			logDebug("[SDKBGManager] Agent %s done channel fired", agentID)
			cbOnce.Do(fireCompletion)
			// Drain any remaining buffered events to prevent goroutine leak.
			for range eventChan {
			}
			return

		case event, ok := <-eventChan:
			if !ok {
				// Events channel closed — done channel should also fire, or
				// did already fire. Either way, nothing to do here.
				return
			}

			// Forward for observability (non-blocking — this channel is also
			// best-effort)
			select {
			case m.events <- SDKBackgroundAgentEvent{
				AgentID:   agentID,
				EventType: string(event.EventType),
				Status:    event.Status,
				Message:   event.Message,
				Progress:  event.Progress,
				Timestamp: event.Timestamp,
				Metadata:  event.Metadata,
			}:
			default:
				logDebug("[SDKBGManager] Event buffer full, dropping event for %s", agentID)
			}

			// Fast-path: terminal event received — fire callback immediately
			// rather than waiting for done channel to be selected.
			// cbOnce ensures this is a no-op if done channel fires first.
			if event.Status == agent.StatusCompleted ||
				event.Status == agent.StatusFailed ||
				event.Status == agent.StatusCancelled {
				cbOnce.Do(fireCompletion)
			}
		}
	}
}

// autoRemove removes an agent after a delay.
func (m *SDKBackgroundAgentManager) autoRemove(agentID string, delay time.Duration) {
	time.Sleep(delay)
	_ = m.Remove(agentID) // Ignore error
}

// Shutdown stops the manager and cleans up resources.
func (m *SDKBackgroundAgentManager) Shutdown() {
	close(m.stopChan)
	m.wg.Wait()
	close(m.events)

	// Cancel all running agents
	m.mu.Lock()
	for _, bg := range m.agents {
		bg.Cancel()
	}
	m.mu.Unlock()

	logDebug("[SDKBGManager] Shutdown complete")
}

// SetCompletionCallback registers a callback that fires when any tracked
// background agent reaches a terminal state (completed, failed, cancelled).
// The callback runs on the monitor goroutine, so it must be non-blocking.
//
// resultText is the agent's final assistant message (BackgroundAgentResult.Result),
// provided inline so callers can surface it without a follow-up NDJSON read.
// Empty when the agent failed before producing a result.
func (m *SDKBackgroundAgentManager) SetCompletionCallback(cb func(agentID, task, status, outputFile, errMsg, resultText string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onComplete = cb
}

// Size returns the number of tracked SDK background agents.
func (m *SDKBackgroundAgentManager) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.agents)
}
