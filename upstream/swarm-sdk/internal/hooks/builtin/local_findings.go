package builtin

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/google/uuid"
)

// LocalFindingsHook captures tool execution results to local findings storage.
// This hook runs at priority 85, after ToolResultAnalysisHook (90) which decides
// whether to capture, and before guidance hooks (80) that might need access.
//
// The hook implements the AAR-style findings database pattern, enabling agents
// to search prior tool executions via standard file tools.
type LocalFindingsHook struct {
	cache       findings.Cache
	index       findings.SemanticIndex
	logger      observability.Logger
	priority    int
	autoCapture []string // List of tools to auto-capture (empty = all)
	emitter     SteeringEventEmitter

	// pendingIndex tracks findings that need to be indexed
	// This provides a recovery mechanism if async indexing fails
	pendingIndex []findings.Finding
	pendingMu    sync.Mutex
}

// NewLocalFindingsHook creates a new local findings hook with the given dependencies.
// If cache is nil, it creates a new file-based cache using default config.
// By default, all tools are captured. Use SetAutoCapture() to limit to specific tools.
func NewLocalFindingsHook(cache findings.Cache, index findings.SemanticIndex, logger observability.Logger) (*LocalFindingsHook, error) {
	// Create cache if not provided
	config := findings.DefaultConfig()
	if cache == nil {
		var err error
		cache, err = findings.NewFileCache(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create findings cache: %w", err)
		}
	}

	// Use no-op index if not provided or if disabled
	if index == nil {
		index = findings.NewNoOpIndex()
	}

	hook := &LocalFindingsHook{
		cache:       cache,
		index:       index,
		logger:      logger,
		priority:    85,
		autoCapture: config.AutoCaptureTools, // Empty slice means capture all
	}

	return hook, nil
}

// Name returns the hook name
func (h *LocalFindingsHook) Name() string {
	return "local-findings"
}

// Priority returns the hook priority (higher = runs first)
func (h *LocalFindingsHook) Priority() int {
	return h.priority
}

// SetPriority allows adjusting the hook priority
func (h *LocalFindingsHook) SetPriority(p int) {
	h.priority = p
}

// SetAutoCapture configures which tools should be automatically captured.
// Pass an empty slice to capture all tools, or a list of specific tool names.
func (h *LocalFindingsHook) SetAutoCapture(tools []string) {
	h.autoCapture = tools
}

// SetEventEmitter wires local finding capture events back into the hook stream
// for bronze capture and remote artifact mirroring.
func (h *LocalFindingsHook) SetEventEmitter(emitter SteeringEventEmitter) {
	h.emitter = emitter
}

// Filter returns true for tool after-execute events that should be captured.
// Capture behavior:
// - If event Metadata has "capture_finding" set, use that value (allows override)
// - If autoCapture list is empty, capture all tools
// - If autoCapture list is non-empty, only capture tools in the list
func (h *LocalFindingsHook) Filter(event hooks.Event) bool {
	// Only process after-tool events
	if event.Type != hooks.EventToolAfterExecute {
		return false
	}

	// Extract tool name
	toolName, _ := event.Data["tool_name"].(string)

	// Check if ToolResultAnalysisHook or other hooks flagged this explicitly
	if event.Metadata != nil {
		// If explicitly flagged to capture, always capture
		if val, ok := event.Metadata["capture_finding"].(bool); ok && val {
			return true
		}
		// If explicitly flagged to skip, honor that
		if val, ok := event.Metadata["capture_finding"].(bool); ok && !val {
			return false
		}
	}

	// Apply auto-capture rules
	if len(h.autoCapture) == 0 {
		// Empty list means capture all tools
		return true
	}

	// Only capture tools in the autoCapture list
	return slices.Contains(h.autoCapture, toolName)
}

// OnEvent captures the tool result to local findings storage
func (h *LocalFindingsHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Extract tool information
	toolName, _ := event.Data["tool_name"].(string)
	toolInput, _ := event.Data["tool_input"].(map[string]any)
	toolOutput, _ := event.Data["tool_output"].(map[string]any)

	// Build context summary
	contextSummary := h.buildContextSummary(toolName, event)

	// Extract analysis metadata
	tags := h.extractTags(event.Metadata)
	priority := h.extractPriority(event.Metadata)

	// Check for success indicator
	success := true
	if s, ok := toolOutput["success"].(bool); ok {
		success = s
	}

	// Calculate duration if available
	var durationMs int64
	if d, ok := toolOutput["duration_ms"].(float64); ok {
		durationMs = int64(d)
	} else if d, ok := toolOutput["duration_ms"].(int64); ok {
		durationMs = d
	}

	// Extract tokens if available
	tokensUsed := 0
	if t, ok := toolOutput["tokens_used"].(float64); ok {
		tokensUsed = int(t)
	} else if t, ok := toolOutput["tokens_used"].(int); ok {
		tokensUsed = t
	}

	// Create the finding
	finding := findings.Finding{
		FindingID:      uuid.New().String(),
		ToolName:       toolName,
		ToolInput:      toolInput,
		ToolOutput:     toolOutput,
		Timestamp:      time.Now(),
		AgentID:        event.AgentID,
		ConversationID: event.ConversationID,
		ContextSummary: contextSummary,
		Tags:           tags,
		Metadata: findings.FindingMetadata{
			DurationMs: durationMs,
			TokensUsed: tokensUsed,
			Success:    success,
			Priority:   priority,
			Source:     "local",
		},
	}

	// Write to cache
	if err := h.cache.Write(ctx, finding); err != nil {
		if h.logger != nil {
			h.logger.Error(ctx, "Failed to write finding to cache",
				observability.F("error", err),
				observability.F("tool", toolName))
		}
		return hooks.ContinueWithMessage(fmt.Sprintf("Failed to capture finding: %v", err)), nil
	}

	// Queue for indexing (async but with retry capability)
	h.queueForIndexing(finding)
	h.emitFindingCaptured(ctx, finding)

	if h.logger != nil {
		h.logger.Info(ctx, "Captured finding",
			observability.F("tool", toolName),
			observability.F("tags", tags),
			observability.F("priority", priority))
	}

	// Note: Dream bridge promotion is handled by FindingsAnalysisHook after LLM scoring.
	// LocalFindingsHook only does raw capture to avoid double-writing unscored findings.
	//
	// Silent success: capture is an observability concern, not a signal the
	// agent should react to. Returning a message here caused a per-tool
	// "Captured finding for X" system-reminder on every call, which buried
	// real findings in noise. Errors still surface above.
	return hooks.Continue(), nil
}

func (h *LocalFindingsHook) emitFindingCaptured(ctx context.Context, finding findings.Finding) {
	if h.emitter == nil {
		return
	}
	evt := hooks.Event{
		Type:           hooks.EventFindingsCaptured,
		Timestamp:      finding.Timestamp,
		ConversationID: finding.ConversationID,
		AgentID:        finding.AgentID,
		Data: map[string]any{
			"artifact_type": "finding",
			"artifact_id":   finding.FindingID,
			"finding_id":    finding.FindingID,
			"tool_name":     finding.ToolName,
			"priority":      finding.Metadata.Priority,
			"tags":          finding.Tags,
			"finding":       finding,
		},
	}
	if _, err := h.emitter.Emit(ctx, evt); err != nil && h.logger != nil {
		h.logger.Warn(ctx, "findings.emit_captured_failed",
			observability.F("error", err.Error()),
			observability.F("finding_id", finding.FindingID))
	}
}

// queueForIndexing adds a finding to the indexing queue and attempts async indexing.
// If indexing fails, the finding remains in the queue for retry on next OnEvent.
func (h *LocalFindingsHook) queueForIndexing(finding findings.Finding) {
	h.pendingMu.Lock()
	h.pendingIndex = append(h.pendingIndex, finding)
	pendingCount := len(h.pendingIndex)
	h.pendingMu.Unlock()

	// Process all pending in background
	go h.processPendingIndexes()

	// Log if there's a backlog
	if h.logger != nil && pendingCount > 1 {
		h.logger.Debug(context.Background(), "Findings index backlog",
			observability.F("pending_count", pendingCount))
	}
}

// processPendingIndexes processes all pending findings in the index queue.
// Successfully indexed findings are removed from the queue.
func (h *LocalFindingsHook) processPendingIndexes() {
	h.pendingMu.Lock()
	if len(h.pendingIndex) == 0 {
		h.pendingMu.Unlock()
		return
	}
	// Copy pending list and clear it
	pending := make([]findings.Finding, len(h.pendingIndex))
	copy(pending, h.pendingIndex)
	h.pendingIndex = h.pendingIndex[:0]
	h.pendingMu.Unlock()

	// Process each pending finding
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var failed []findings.Finding
	for _, finding := range pending {
		if err := h.index.Index(ctx, finding); err != nil {
			// Queue for retry next time
			failed = append(failed, finding)
			if h.logger != nil {
				h.logger.Debug(ctx, "Failed to index finding (will retry)",
					observability.F("error", err),
					observability.F("finding_id", finding.FindingID))
			}
		}
	}

	// Re-queue failed items (with limit to prevent unbounded growth)
	if len(failed) > 0 {
		h.pendingMu.Lock()
		// Keep only last 100 failed to prevent memory growth
		if len(failed) > 100 {
			failed = failed[len(failed)-100:]
		}
		h.pendingIndex = append(h.pendingIndex, failed...)
		h.pendingMu.Unlock()
	}
}

// buildContextSummary creates a human-readable summary of the execution context
func (h *LocalFindingsHook) buildContextSummary(toolName string, event hooks.Event) string {
	summary := fmt.Sprintf("Executed %s", toolName)

	// Add context from event data if available
	if toolInput, ok := event.Data["tool_input"].(map[string]any); ok {
		// Try to extract meaningful context
		if cmd, ok := toolInput["command"].(string); ok && cmd != "" {
			summary = fmt.Sprintf("Executed %s: %s", toolName, truncate(cmd, 50))
		} else if file, ok := toolInput["file_path"].(string); ok && file != "" {
			summary = fmt.Sprintf("Executed %s on %s", toolName, truncate(file, 40))
		} else if query, ok := toolInput["query"].(string); ok && query != "" {
			summary = fmt.Sprintf("Executed %s: %s", toolName, truncate(query, 50))
		}
	}

	return summary
}

// extractTags extracts tags from analysis metadata
func (h *LocalFindingsHook) extractTags(metadata map[string]any) []string {
	var tags []string

	if metadata != nil {
		// Extract tags from metadata
		if tagList, ok := metadata["finding_tags"].([]string); ok {
			tags = append(tags, tagList...)
		}
	}

	// Always add auto-captured tag
	tags = append(tags, "auto-captured")

	return tags
}

// extractPriority extracts priority from analysis metadata
func (h *LocalFindingsHook) extractPriority(metadata map[string]any) float64 {
	if metadata == nil {
		return 50.0 // Default medium priority
	}

	if p, ok := metadata["finding_priority"].(float64); ok {
		return p
	}

	// Handle int case (from older data or direct assignment)
	if p, ok := metadata["finding_priority"].(int); ok {
		return float64(p)
	}

	return 50.0 // Default
}

// Close releases resources held by the hook
func (h *LocalFindingsHook) Close() error {
	var errs []error

	if h.cache != nil {
		if err := h.cache.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if h.index != nil {
		if err := h.index.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors closing LocalFindingsHook: %v", errs)
	}

	return nil
}

// GetCache returns the underlying cache for testing or advanced use
func (h *LocalFindingsHook) GetCache() findings.Cache {
	return h.cache
}

// GetIndex returns the underlying semantic index
func (h *LocalFindingsHook) GetIndex() findings.SemanticIndex {
	return h.index
}

// GetPendingIndexCount returns the number of findings waiting to be indexed.
// This can be used by agents to check if they need to wait before querying.
func (h *LocalFindingsHook) GetPendingIndexCount() int {
	h.pendingMu.Lock()
	defer h.pendingMu.Unlock()
	return len(h.pendingIndex)
}

// FlushPendingIndexes synchronously indexes all pending findings.
// Call this before querying if you need to ensure all findings are searchable.
// Returns the number of findings successfully indexed and any errors encountered.
func (h *LocalFindingsHook) FlushPendingIndexes(ctx context.Context) (int, error) {
	h.pendingMu.Lock()
	if len(h.pendingIndex) == 0 {
		h.pendingMu.Unlock()
		return 0, nil
	}
	// Copy and clear
	pending := make([]findings.Finding, len(h.pendingIndex))
	copy(pending, h.pendingIndex)
	h.pendingIndex = h.pendingIndex[:0]
	h.pendingMu.Unlock()

	// Index all pending with provided context
	indexedCount := 0
	var lastErr error
	for _, finding := range pending {
		if err := h.index.Index(ctx, finding); err != nil {
			lastErr = err
			// Re-queue for retry
			h.pendingMu.Lock()
			h.pendingIndex = append(h.pendingIndex, finding)
			h.pendingMu.Unlock()
		} else {
			indexedCount++
		}
	}

	return indexedCount, lastErr
}

// truncate limits string length
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
