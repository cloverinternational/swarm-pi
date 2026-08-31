package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
)

// maxOpenBronzeFiles controls how many hour-partitioned file handles stay open.
// Handles older than the most recent maxOpenBronzeFiles hours are closed.
const maxOpenBronzeFiles = 3

// bronzeFlushInterval is the number of tool call events between proactive
// file syncs (MemPalace pattern: periodic checkpoint to prevent data loss).
const bronzeFlushInterval = 15

// BronzeEventHook captures ALL agent activity as raw events without filtering.
// This implements the "Bronze tier" of the bronze-silver-gold strategy:
// - Bronze: Capture everything (this hook)
// - Silver: Aggregate and index (future)
// - Gold: ML insights and predictions (future)
//
// Storage format: Append-only JSONL files, partitioned by date
// ~/.swarm/projects/<hash>/bronze/YYYY/MM/DD/events_<hour>.jsonl
//
// Each line: {"ts": "...", "type": "...", "agent_id": "...", "conv_id": "...", "payload": {...}}
type BronzeEventHook struct {
	baseDir   string
	logger    observability.Logger
	priority  int
	mu        sync.Mutex
	writers   map[string]*json.Encoder // hour -> encoder (lazy init)
	files     map[string]*os.File      // hour -> file handle
	writeChan chan bronzeEvent         // async write channel
	done      chan struct{}
	emitter   SteeringEventEmitter

	// toolStartTimes tracks the timestamp of tool.before_execute events
	// so that duration_ms can be computed when the matching after_execute arrives.
	// Key: conversationID + ":" + toolName (best-effort correlation).
	toolStartTimes map[string]time.Time

	// toolCallCount tracks tool events for proactive flush scheduling.
	toolCallCount int
}

// bronzeEvent is the internal structure for queueing
type bronzeEvent struct {
	Timestamp      time.Time      `json:"ts"`
	Type           string         `json:"type"`
	AgentID        string         `json:"agent_id,omitempty"`
	ConversationID string         `json:"conv_id,omitempty"`
	TaskID         string         `json:"task_id,omitempty"` // Active task ID at time of event
	ModeID         string         `json:"mode_id,omitempty"`
	GroupID        string         `json:"group_id,omitempty"`
	TraceID        string         `json:"trace_id,omitempty"`
	EventID        string         `json:"event_id"`
	Payload        map[string]any `json:"payload"`
	RawData        map[string]any `json:"raw_data,omitempty"` // Full event.Data for tool events
}

// NewBronzeEventHook creates a new bronze tier event capture hook.
// If baseDir is empty, uses default: ~/.swarm/projects/<workspace-hash>/bronze
func NewBronzeEventHook(baseDir string, logger observability.Logger) (*BronzeEventHook, error) {
	if baseDir == "" {
		baseDir = findings.DefaultConfig().LocalCacheDir
		baseDir = filepath.Join(filepath.Dir(baseDir), "bronze")
	}

	// Ensure directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bronze directory: %w", err)
	}

	hook := &BronzeEventHook{
		baseDir:        baseDir,
		logger:         logger,
		priority:       5, // Low priority - runs after other hooks have processed
		writers:        make(map[string]*json.Encoder),
		files:          make(map[string]*os.File),
		writeChan:      make(chan bronzeEvent, 1000),
		done:           make(chan struct{}),
		toolStartTimes: make(map[string]time.Time),
	}

	// Start background writer
	go hook.backgroundWriter()

	return hook, nil
}

// Name returns the hook name
func (h *BronzeEventHook) Name() string {
	return "bronze-event-capture"
}

// Priority returns the hook priority (lower = runs later)
func (h *BronzeEventHook) Priority() int {
	return h.priority
}

// SetPriority allows adjusting the hook priority
func (h *BronzeEventHook) SetPriority(p int) {
	h.priority = p
}

// SetEventEmitter wires an emitter for mirroring local bronze writes into the
// hook event stream. This lets remote analytics capture the exact local bronze
// record without scraping the JSONL files.
func (h *BronzeEventHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emitter = emitter
}

// Filter returns true for ALL events - we capture everything
func (h *BronzeEventHook) Filter(event hooks.Event) bool {
	if event.Type == hooks.EventBronzeLocalEventCaptured {
		return false
	}
	return true // Capture every single event
}

// OnEvent captures the raw event to bronze storage
func (h *BronzeEventHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Resolve active task ID from the global TodoManager so bronze events
	// can be correlated back to the task that was in progress when they fired.
	var activeTaskID string
	if tm := ii.GetTodoManager(); tm != nil {
		if inProgress := tm.ByStatus(ii.TodoStatusInProgress); len(inProgress) > 0 {
			activeTaskID = inProgress[0].ID
		}
	}

	// Build bronze event
	bronze := bronzeEvent{
		Timestamp:      event.Timestamp,
		Type:           event.Type,
		AgentID:        event.AgentID,
		ConversationID: event.ConversationID,
		TaskID:         activeTaskID,
		ModeID:         event.ModeID,
		GroupID:        event.GroupID,
		TraceID:        event.TraceID,
		EventID:        event.ID,
		Payload:        make(map[string]any),
		RawData:        event.Data, // Full raw data
	}

	// Extract key fields based on event type for easier querying
	switch event.Type {
	// Tool events
	case hooks.EventToolBeforeExecute, hooks.EventToolAfterExecute, hooks.EventToolExecutionFailed:
		if toolName, ok := event.Data["tool_name"].(string); ok {
			bronze.Payload["tool_name"] = toolName
		}
		if toolInput, ok := event.Data["tool_input"].(map[string]any); ok {
			bronze.Payload["tool_input"] = summarizeToolInput(toolInput)
		}
		if toolOutput, ok := event.Data["tool_output"].(map[string]any); ok {
			bronze.Payload["tool_output_summary"] = summarizeToolOutput(toolOutput)
		}

		// Duration tracking: record start time on before_execute,
		// compute duration_ms on after_execute/failed.
		toolName, _ := event.Data["tool_name"].(string)
		durationKey := event.ConversationID + ":" + toolName
		if event.Type == hooks.EventToolBeforeExecute {
			h.mu.Lock()
			h.toolStartTimes[durationKey] = event.Timestamp
			h.mu.Unlock()
		} else {
			h.mu.Lock()
			if startTime, ok := h.toolStartTimes[durationKey]; ok {
				bronze.Payload["duration_ms"] = event.Timestamp.Sub(startTime).Milliseconds()
				delete(h.toolStartTimes, durationKey)
			}
			h.mu.Unlock()
		}

	// Message events
	case hooks.EventMessageAdded, hooks.EventMessageEdited:
		if role, ok := event.Data["role"].(string); ok {
			bronze.Payload["role"] = role
		}
		if content, ok := event.Data["content"].(string); ok {
			bronze.Payload["content_length"] = len(content)
		}

	// Provider events
	case hooks.EventProviderBeforeRequest, hooks.EventProviderAfterResponse:
		if model, ok := event.Data["model"].(string); ok {
			bronze.Payload["model"] = model
		}
		if tokens, ok := event.Data["tokens_used"].(int); ok {
			bronze.Payload["tokens_used"] = tokens
		}
		// Capture input/output token breakdown from usage map
		if usage, ok := event.Data["usage"].(map[string]any); ok {
			if input, ok := usage["input"].(int); ok {
				bronze.Payload["input_tokens"] = input
			}
			if output, ok := usage["output"].(int); ok {
				bronze.Payload["output_tokens"] = output
			}
		}
		if provider, ok := event.Data["provider"].(string); ok {
			bronze.Payload["provider"] = provider
		}

	// Context events
	case hooks.EventContextWindowExceeded, hooks.EventContextTrimmed:
		if tokens, ok := event.Data["token_count"].(int); ok {
			bronze.Payload["token_count"] = tokens
		}
		if limit, ok := event.Data["context_limit"].(int); ok {
			bronze.Payload["context_limit"] = limit
		}

	// Mode events
	case hooks.EventModeEntered, hooks.EventModeExited:
		if modeID, ok := event.Data["mode_id"].(string); ok {
			bronze.Payload["mode_id"] = modeID
		}

	// Group events
	case hooks.EventGroupStarted, hooks.EventGroupCompleted:
		if groupID, ok := event.Data["group_id"].(string); ok {
			bronze.Payload["group_id"] = groupID
		}

	// Steering decision events
	case hooks.EventSteeringDecisionMade:
		for _, key := range []string{"tool_name", "decision", "reason", "task_title", "task_category"} {
			if v, ok := event.Data[key].(string); ok {
				bronze.Payload[key] = v
			}
		}
		if v, ok := event.Data["elapsed_ms"].(int64); ok {
			bronze.Payload["elapsed_ms"] = v
		}
		if v, ok := event.Data["evaluated"].(bool); ok {
			bronze.Payload["evaluated"] = v
		}

	// Dream consolidation events
	case hooks.EventDreamConsolidationStarted, hooks.EventDreamConsolidationComplete, hooks.EventDreamConsolidationFailed:
		for _, key := range []string{"session_count", "memory_count", "duration_ms"} {
			if v, ok := event.Data[key]; ok {
				bronze.Payload[key] = v
			}
		}
		if v, ok := event.Data["error"].(string); ok && v != "" {
			bronze.Payload["error"] = v
		}

	// Findings evaluation events
	case hooks.EventFindingsEvaluated, hooks.EventFindingsPromoted:
		for _, key := range []string{"finding_id", "tool_name", "memory_type"} {
			if v, ok := event.Data[key].(string); ok {
				bronze.Payload[key] = v
			}
		}
		if v, ok := event.Data["priority"].(int); ok {
			bronze.Payload["priority"] = v
		}
		if v, ok := event.Data["promoted"].(bool); ok {
			bronze.Payload["promoted"] = v
		}

	// Gold analysis events
	case hooks.EventSystemMetrics:
		// Extract all metric fields emitted by SystemMetricsHook directly into payload
		// so they're queryable without JSON parsing from bronze_events.
		for _, k := range []string{
			"trigger", "heap_alloc_mb", "heap_sys_mb", "rss_mb",
			"goroutines", "gc_count", "num_cpu", "go_version",
			"os", "arch", "uptime_s",
		} {
			if v, ok := event.Data[k]; ok {
				bronze.Payload[k] = v
			}
		}

	case hooks.EventGoldAnalysisStarted, hooks.EventGoldAnalysisComplete, hooks.EventGoldInsightCreated:
		for _, key := range []string{"analysis_level", "project", "topic", "category"} {
			if v, ok := event.Data[key].(string); ok {
				bronze.Payload[key] = v
			}
		}
		if v, ok := event.Data["duration_ms"]; ok {
			bronze.Payload["duration_ms"] = v
		}
	}

	// Queue for async write (non-blocking)
	select {
	case h.writeChan <- bronze:
	case <-ctx.Done():
		return hooks.Continue(), ctx.Err()
	default:
		// Channel full, write synchronously
		if err := h.writeSync(bronze); err != nil {
			if h.logger != nil {
				h.logger.Error(ctx, "Bronze sync write failed",
					observability.F("error", err),
					observability.F("event_type", event.Type))
			}
		}
	}
	h.emitBronzeEvent(ctx, bronze)

	// Proactive flush: every bronzeFlushInterval tool calls, sync open files
	// to disk to prevent data loss during long sessions (MemPalace pattern).
	if event.Type == hooks.EventToolAfterExecute || event.Type == hooks.EventToolExecutionFailed {
		h.mu.Lock()
		h.toolCallCount++
		shouldFlush := h.toolCallCount >= bronzeFlushInterval
		if shouldFlush {
			h.toolCallCount = 0
		}
		h.mu.Unlock()
		if shouldFlush {
			h.syncOpenFiles()
		}
	}

	return hooks.Continue(), nil
}

func (h *BronzeEventHook) emitBronzeEvent(ctx context.Context, bronze bronzeEvent) {
	h.mu.Lock()
	emitter := h.emitter
	h.mu.Unlock()
	if emitter == nil {
		return
	}
	evt := hooks.Event{
		Type:           hooks.EventBronzeLocalEventCaptured,
		Timestamp:      bronze.Timestamp,
		TraceID:        bronze.TraceID,
		ConversationID: bronze.ConversationID,
		AgentID:        bronze.AgentID,
		ModeID:         bronze.ModeID,
		GroupID:        bronze.GroupID,
		Data: map[string]any{
			"artifact_type": "bronze_event",
			"artifact_id":   bronze.EventID,
			"bronze_event":  bronze,
		},
	}
	if _, err := emitter.Emit(ctx, evt); err != nil && h.logger != nil {
		h.logger.Warn(ctx, "bronze.emit_event_failed",
			observability.F("error", err.Error()),
			observability.F("event_type", bronze.Type))
	}
}

// backgroundWriter handles async writes
func (h *BronzeEventHook) backgroundWriter() {
	for {
		select {
		case event := <-h.writeChan:
			if err := h.writeSync(event); err != nil {
				if h.logger != nil {
					h.logger.Error(context.Background(), "Bronze async write failed",
						observability.F("error", err),
						observability.F("event_type", event.Type))
				}
			}
		case <-h.done:
			return
		}
	}
}

// writeSync performs synchronous file write
func (h *BronzeEventHook) writeSync(event bronzeEvent) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Determine file path: base/YYYY/MM/DD/events_HH.jsonl
	hourKey := event.Timestamp.Format("2006-01-02-15")
	dateDir := filepath.Join(h.baseDir,
		event.Timestamp.Format("2006"),
		event.Timestamp.Format("01"),
		event.Timestamp.Format("02"))

	if err := os.MkdirAll(dateDir, 0755); err != nil {
		return fmt.Errorf("failed to create date directory: %w", err)
	}

	// Get or create encoder for this hour
	enc, ok := h.writers[hourKey]
	if !ok {
		filename := fmt.Sprintf("events_%s.jsonl", event.Timestamp.Format("15"))
		filePath := filepath.Join(dateDir, filename)

		f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("failed to open bronze file: %w", err)
		}

		h.files[hourKey] = f
		enc = json.NewEncoder(f)
		h.writers[hourKey] = enc
	}

	// Write event
	if err := enc.Encode(event); err != nil {
		return fmt.Errorf("failed to encode bronze event: %w", err)
	}

	// Reap stale file handles — keep only the most recent maxOpenBronzeFiles hours.
	if len(h.files) > maxOpenBronzeFiles {
		h.reapStaleHandles(hourKey)
	}

	return nil
}

// reapStaleHandles closes file handles for hours other than the current one,
// keeping the total open handles bounded.
func (h *BronzeEventHook) reapStaleHandles(currentHourKey string) {
	for key, f := range h.files {
		if key == currentHourKey {
			continue
		}
		_ = f.Close()
		delete(h.files, key)
		delete(h.writers, key)
	}
}

// syncOpenFiles calls Sync() on all open file handles to force OS-level flush.
// Called under no lock — acquires its own.
func (h *BronzeEventHook) syncOpenFiles() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.files {
		_ = f.Sync()
	}
}

// Flush forces all buffered bronze events to be written and synced to disk.
// Call this before context compaction or any operation that might lose data.
// Safe to call from any goroutine.
func (h *BronzeEventHook) Flush() {
	// Drain the async write channel first.
	for {
		select {
		case event := <-h.writeChan:
			_ = h.writeSync(event)
		default:
			goto drained
		}
	}
drained:
	h.syncOpenFiles()
}

// Close releases all resources. It signals the background writer to stop,
// drains any remaining queued events, then closes all open file handles.
func (h *BronzeEventHook) Close() error {
	close(h.done)

	// Drain remaining queued events so nothing is lost.
	for {
		select {
		case event := <-h.writeChan:
			_ = h.writeSync(event)
		default:
			goto drained
		}
	}
drained:

	h.mu.Lock()
	defer h.mu.Unlock()

	var errs []error
	for hour, f := range h.files {
		if err := f.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", hour, err))
		}
	}
	h.files = nil
	h.writers = nil

	if len(errs) > 0 {
		return fmt.Errorf("bronze close errors: %v", errs)
	}
	return nil
}

// GetStats returns statistics about the bronze store
func (h *BronzeEventHook) GetStats() (Stats, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	stats := Stats{
		OpenFiles: len(h.files),
	}

	// Walk directory to count total files and size
	err := filepath.Walk(h.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if !info.IsDir() && filepath.Ext(path) == ".jsonl" {
			stats.TotalFiles++
			stats.TotalBytes += info.Size()
		}
		return nil
	})

	return stats, err
}

// Stats holds bronze storage statistics
type Stats struct {
	TotalFiles int
	TotalBytes int64
	OpenFiles  int
}

// DefaultBronzeRetentionDays is how long bronze events are kept before cleanup.
const DefaultBronzeRetentionDays = 7

// CleanupOlderThan removes bronze event files older than the given duration.
// Returns the number of files removed and total bytes reclaimed.
func (h *BronzeEventHook) CleanupOlderThan(retention time.Duration) (filesRemoved int, bytesReclaimed int64, err error) {
	cutoff := time.Now().Add(-retention)

	err = filepath.Walk(h.baseDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			size := info.Size()
			if rmErr := os.Remove(path); rmErr == nil {
				filesRemoved++
				bytesReclaimed += size
			}
		}
		return nil
	})

	// Clean up empty date directories
	_ = removeEmptyDirs(h.baseDir)

	return filesRemoved, bytesReclaimed, err
}

// removeEmptyDirs walks bottom-up and removes empty directories.
func removeEmptyDirs(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == root {
			return nil
		}
		entries, _ := os.ReadDir(path)
		if len(entries) == 0 {
			_ = os.Remove(path)
		}
		return nil
	})
}

// Helper functions to summarize data (keep payloads manageable)

func summarizeToolInput(input map[string]any) map[string]any {
	summary := make(map[string]any)
	for k, v := range input {
		switch val := v.(type) {
		case string:
			// Bash/Shell commands get a higher limit for analytics queryability.
			// Other strings are capped at 200 chars to keep payloads manageable.
			limit := 200
			if k == "command" {
				limit = 2000
			}
			if len(val) > limit {
				summary[k] = val[:limit] + "..."
			} else {
				summary[k] = val
			}
		case []byte:
			// Skip binary data
			summary[k] = fmt.Sprintf("<binary:%d bytes>", len(val))
		default:
			summary[k] = v
		}
	}
	return summary
}

func summarizeToolOutput(output map[string]any) map[string]any {
	summary := make(map[string]any)

	// Extract key fields
	if success, ok := output["success"].(bool); ok {
		summary["success"] = success
	}
	if duration, ok := output["duration_ms"].(float64); ok {
		summary["duration_ms"] = duration
	}
	if tokens, ok := output["tokens_used"].(float64); ok {
		summary["tokens_used"] = int(tokens)
	}
	if err, ok := output["error"].(string); ok && err != "" {
		summary["error"] = err[:min(len(err), 100)]
	}

	return summary
}
