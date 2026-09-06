package agent

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ToolChangeHandler implements tools.ToolChangeListener for agents.
// It receives tool change notifications and generates system messages
// to inform the LLM about available tool modifications.
type ToolChangeHandler struct {
	mu            sync.Mutex
	pendingEvents []tools.ToolChangeEvent
	agentID       string

	// Optional callback for immediate notification
	onChangeCallback func(ctx context.Context, event tools.ToolChangeEvent)
}

// NewToolChangeHandler creates a new tool change handler for an agent.
func NewToolChangeHandler(agentID string) *ToolChangeHandler {
	return &ToolChangeHandler{
		pendingEvents: make([]tools.ToolChangeEvent, 0),
		agentID:       agentID,
	}
}

// OnToolsChanged implements tools.ToolChangeListener.
// This method should be non-blocking and thread-safe.
func (h *ToolChangeHandler) OnToolsChanged(ctx context.Context, event tools.ToolChangeEvent) {
	h.mu.Lock()
	h.pendingEvents = append(h.pendingEvents, event)
	callback := h.onChangeCallback
	h.mu.Unlock()

	// Call optional callback outside of lock
	if callback != nil {
		go callback(ctx, event)
	}
}

// SetOnChangeCallback sets an optional callback for immediate notification.
func (h *ToolChangeHandler) SetOnChangeCallback(callback func(ctx context.Context, event tools.ToolChangeEvent)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onChangeCallback = callback
}

// HasPendingChanges checks if there are pending tool changes.
func (h *ToolChangeHandler) HasPendingChanges() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.pendingEvents) > 0
}

// GetPendingChanges returns all pending changes without clearing them.
func (h *ToolChangeHandler) PendingChanges() []tools.ToolChangeEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	result := make([]tools.ToolChangeEvent, len(h.pendingEvents))
	copy(result, h.pendingEvents)
	return result
}

// GetPendingChangesAndReset returns all pending changes and clears the handler.
func (h *ToolChangeHandler) PendingChangesAndReset() []tools.ToolChangeEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	result := h.pendingEvents
	h.pendingEvents = make([]tools.ToolChangeEvent, 0)
	return result
}

// GenerateSystemMessage creates a system message describing all pending tool changes.
// Returns nil if there are no pending changes.
func (h *ToolChangeHandler) GenerateSystemMessage() *conversation.Message {
	events := h.PendingChangesAndReset()
	if len(events) == 0 {
		return nil
	}

	// Aggregate all changes from all events
	var allChanges []tools.ToolChange
	triggers := make(map[string]bool)

	for _, event := range events {
		allChanges = append(allChanges, event.Changes...)
		triggers[event.Trigger] = true
	}

	if len(allChanges) == 0 {
		return nil
	}

	// Build message content
	content := buildToolChangeMessage(allChanges, triggers)

	// Get trigger list
	triggerList := make([]string, 0, len(triggers))
	for t := range triggers {
		triggerList = append(triggerList, t)
	}

	return &conversation.Message{
		ID:        generateMessageID("tool-update"),
		Timestamp: time.Now(),
		Role:      conversation.RoleSystem,
		Content:   content,
		Metadata: map[string]any{
			"type":     "tool_availability_update",
			"agent_id": h.agentID,
			"triggers": triggerList,
			"changes":  len(allChanges),
		},
	}
}

// buildToolChangeMessage formats the tool changes into a readable message.
func buildToolChangeMessage(changes []tools.ToolChange, triggers map[string]bool) string {
	// Group changes by type
	added := make([]string, 0)
	removed := make([]string, 0)
	enabled := make([]string, 0)
	disabled := make([]string, 0)

	for _, change := range changes {
		switch change.Type {
		case tools.ToolChangeAdded:
			desc := change.ToolName
			if change.Tool != nil {
				desc = change.ToolName + " - " + change.Tool.Description()
			}
			added = append(added, desc)
		case tools.ToolChangeRemoved:
			removed = append(removed, change.ToolName)
		case tools.ToolChangeEnabled:
			desc := change.ToolName
			if change.Tool != nil {
				desc = change.ToolName + " - " + change.Tool.Description()
			}
			enabled = append(enabled, desc)
		case tools.ToolChangeDisabled:
			disabled = append(disabled, change.ToolName)
		}
	}

	// Build message
	var content strings.Builder
	content.WriteString("[TOOL AVAILABILITY UPDATE]\n\n")

	// Describe triggers
	triggerList := make([]string, 0, len(triggers))
	for t := range triggers {
		triggerList = append(triggerList, t)
	}
	content.WriteString("Trigger: ")
	for i, t := range triggerList {
		if i > 0 {
			content.WriteString(", ")
		}
		content.WriteString(t)
	}
	content.WriteString("\n\n")

	if len(added) > 0 {
		content.WriteString("NEW TOOLS AVAILABLE:\n")
		for _, t := range added {
			content.WriteString("  + " + t + "\n")
		}
		content.WriteString("\n")
	}

	if len(enabled) > 0 {
		content.WriteString("TOOLS RE-ENABLED:\n")
		for _, t := range enabled {
			content.WriteString("  * " + t + "\n")
		}
		content.WriteString("\n")
	}

	if len(disabled) > 0 {
		content.WriteString("TOOLS DISABLED (do not attempt to use):\n")
		for _, t := range disabled {
			content.WriteString("  x " + t + "\n")
		}
		content.WriteString("\n")
	}

	if len(removed) > 0 {
		content.WriteString("TOOLS REMOVED (no longer available):\n")
		for _, t := range removed {
			content.WriteString("  - " + t + "\n")
		}
		content.WriteString("\n")
	}

	content.WriteString("Please acknowledge this update and adjust your tool usage accordingly.")

	return content.String()
}

// generateMessageID creates a unique message ID.
func generateMessageID(prefix string) string {
	return prefix + "-" + time.Now().Format("20060102-150405") + "-" + randomSuffix()
}

// randomSuffix generates a random suffix for IDs.
func randomSuffix() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, 8)
	now := time.Now().UnixNano()
	for i := range result {
		result[i] = chars[(now+int64(i))%int64(len(chars))]
	}
	return string(result)
}
