package tools

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// ChangeTracker accumulates tool changes and generates system messages
// to notify the LLM about available tool modifications.
type ChangeTracker struct {
	mu      sync.Mutex
	changes []TrackedChange

	// Snapshot of tool names at last reset for comparison
	lastKnownTools map[string]bool
}

// TrackedChange represents a recorded tool change with timestamp.
type TrackedChange struct {
	Change    ToolChange
	Timestamp time.Time
	Trigger   string
}

// NewChangeTracker creates a new change tracker.
func NewChangeTracker() *ChangeTracker {
	return &ChangeTracker{
		changes:        make([]TrackedChange, 0),
		lastKnownTools: make(map[string]bool),
	}
}

// RecordChange records a single tool change.
func (ct *ChangeTracker) RecordChange(change ToolChange, trigger string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.changes = append(ct.changes, TrackedChange{
		Change:    change,
		Timestamp: time.Now(),
		Trigger:   trigger,
	})
}

// RecordAdded records a tool being added.
func (ct *ChangeTracker) RecordAdded(toolName string, tool Tool, trigger string, metadata map[string]any) {
	ct.RecordChange(ToolChange{
		Type:     ToolChangeAdded,
		ToolName: toolName,
		Tool:     tool,
		Metadata: metadata,
	}, trigger)
}

// RecordRemoved records a tool being removed.
func (ct *ChangeTracker) RecordRemoved(toolName string, trigger string, metadata map[string]any) {
	ct.RecordChange(ToolChange{
		Type:     ToolChangeRemoved,
		ToolName: toolName,
		Tool:     nil,
		Metadata: metadata,
	}, trigger)
}

// RecordEnabled records a tool being enabled.
func (ct *ChangeTracker) RecordEnabled(toolName string, tool Tool, trigger string, metadata map[string]any) {
	ct.RecordChange(ToolChange{
		Type:     ToolChangeEnabled,
		ToolName: toolName,
		Tool:     tool,
		Metadata: metadata,
	}, trigger)
}

// RecordDisabled records a tool being disabled.
func (ct *ChangeTracker) RecordDisabled(toolName string, tool Tool, trigger string, metadata map[string]any) {
	ct.RecordChange(ToolChange{
		Type:     ToolChangeDisabled,
		ToolName: toolName,
		Tool:     tool,
		Metadata: metadata,
	}, trigger)
}

// HasChanges returns true if there are pending changes.
func (ct *ChangeTracker) HasChanges() bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return len(ct.changes) > 0
}

// GetChanges returns all pending changes without clearing them.
func (ct *ChangeTracker) Changes() []TrackedChange {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	result := make([]TrackedChange, len(ct.changes))
	copy(result, ct.changes)
	return result
}

// GetChangesAndReset returns all pending changes and clears the tracker.
func (ct *ChangeTracker) ChangesAndReset() []TrackedChange {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	result := ct.changes
	ct.changes = make([]TrackedChange, 0)
	return result
}

// Reset clears all pending changes.
func (ct *ChangeTracker) Reset() {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.changes = make([]TrackedChange, 0)
}

// GenerateSystemMessage creates a system message describing the tool changes.
// Returns nil if there are no changes.
func (ct *ChangeTracker) GenerateSystemMessage() *conversation.Message {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if len(ct.changes) == 0 {
		return nil
	}

	// Group changes by type for cleaner output
	added := make([]string, 0)
	removed := make([]string, 0)
	enabled := make([]string, 0)
	disabled := make([]string, 0)

	triggers := make(map[string]bool)

	for _, tc := range ct.changes {
		triggers[tc.Trigger] = true

		switch tc.Change.Type {
		case ToolChangeAdded:
			desc := tc.Change.ToolName
			if tc.Change.Tool != nil {
				desc = fmt.Sprintf("%s - %s", tc.Change.ToolName, tc.Change.Tool.Description())
			}
			added = append(added, desc)
		case ToolChangeRemoved:
			removed = append(removed, tc.Change.ToolName)
		case ToolChangeEnabled:
			desc := tc.Change.ToolName
			if tc.Change.Tool != nil {
				desc = fmt.Sprintf("%s - %s", tc.Change.ToolName, tc.Change.Tool.Description())
			}
			enabled = append(enabled, desc)
		case ToolChangeDisabled:
			disabled = append(disabled, tc.Change.ToolName)
		}
	}

	// Build message content
	var parts []string

	parts = append(parts, "[TOOL AVAILABILITY UPDATE]")
	parts = append(parts, "")

	// Describe triggers
	triggerList := make([]string, 0, len(triggers))
	for t := range triggers {
		triggerList = append(triggerList, t)
	}
	parts = append(parts, fmt.Sprintf("Trigger: %s", strings.Join(triggerList, ", ")))
	parts = append(parts, "")

	if len(added) > 0 {
		parts = append(parts, "NEW TOOLS AVAILABLE:")
		for _, t := range added {
			parts = append(parts, fmt.Sprintf("  + %s", t))
		}
		parts = append(parts, "")
	}

	if len(enabled) > 0 {
		parts = append(parts, "TOOLS RE-ENABLED:")
		for _, t := range enabled {
			parts = append(parts, fmt.Sprintf("  ✓ %s", t))
		}
		parts = append(parts, "")
	}

	if len(disabled) > 0 {
		parts = append(parts, "TOOLS DISABLED (do not attempt to use):")
		for _, t := range disabled {
			parts = append(parts, fmt.Sprintf("  ✗ %s", t))
		}
		parts = append(parts, "")
	}

	if len(removed) > 0 {
		parts = append(parts, "TOOLS REMOVED (no longer available):")
		for _, t := range removed {
			parts = append(parts, fmt.Sprintf("  - %s", t))
		}
		parts = append(parts, "")
	}

	parts = append(parts, "Please acknowledge this update and adjust your tool usage accordingly.")

	return &conversation.Message{
		ID:        fmt.Sprintf("tool-update-%d", time.Now().UnixNano()),
		Timestamp: time.Now(),
		Role:      conversation.RoleSystem,
		Content:   strings.Join(parts, "\n"),
		Metadata: map[string]any{
			"type":     "tool_availability_update",
			"triggers": triggerList,
			"counts": map[string]int{
				"added":    len(added),
				"removed":  len(removed),
				"enabled":  len(enabled),
				"disabled": len(disabled),
			},
		},
	}
}

// GenerateSystemMessageAndReset creates a system message and clears the tracker.
// Returns nil if there are no changes.
func (ct *ChangeTracker) GenerateSystemMessageAndReset() *conversation.Message {
	msg := ct.GenerateSystemMessage()
	if msg != nil {
		ct.Reset()
	}
	return msg
}

// UpdateSnapshot updates the internal snapshot of known tools.
// This is used to track what tools were available at the last turn.
func (ct *ChangeTracker) UpdateSnapshot(toolNames []string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.lastKnownTools = make(map[string]bool)
	for _, name := range toolNames {
		ct.lastKnownTools[name] = true
	}
}

// GetLastKnownTools returns the tool names from the last snapshot.
func (ct *ChangeTracker) LastKnownTools() []string {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	result := make([]string, 0, len(ct.lastKnownTools))
	for name := range ct.lastKnownTools {
		result = append(result, name)
	}
	return result
}

// ComputeDiff compares current tools against the last snapshot
// and returns the differences. Does not record changes.
func (ct *ChangeTracker) ComputeDiff(currentTools []string) (added, removed []string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	currentSet := make(map[string]bool)
	for _, name := range currentTools {
		currentSet[name] = true
	}

	// Find added tools (in current but not in last)
	for name := range currentSet {
		if !ct.lastKnownTools[name] {
			added = append(added, name)
		}
	}

	// Find removed tools (in last but not in current)
	for name := range ct.lastKnownTools {
		if !currentSet[name] {
			removed = append(removed, name)
		}
	}

	return added, removed
}
