package tools

import (
	"context"
)

// ToolChangeType represents the type of change that occurred.
type ToolChangeType string

const (
	// ToolChangeAdded indicates a new tool was registered.
	ToolChangeAdded ToolChangeType = "added"

	// ToolChangeRemoved indicates a tool was unregistered.
	ToolChangeRemoved ToolChangeType = "removed"

	// ToolChangeEnabled indicates a tool was enabled.
	ToolChangeEnabled ToolChangeType = "enabled"

	// ToolChangeDisabled indicates a tool was disabled.
	ToolChangeDisabled ToolChangeType = "disabled"
)

// ToolChange represents a single tool state change.
type ToolChange struct {
	// Type is the type of change.
	Type ToolChangeType

	// ToolName is the name of the tool that changed.
	ToolName string

	// Tool is the tool instance (nil for removal).
	Tool Tool

	// Metadata provides additional context.
	Metadata map[string]any
}

// ToolChangeEvent represents a notification about tool changes.
type ToolChangeEvent struct {
	// Changes lists all changes in this event.
	Changes []ToolChange

	// Trigger describes what triggered the change.
	// Examples: "user_toggle", "mcp_connect", "mcp_disconnect", "mode_change", "programmatic"
	Trigger string

	// Context provides additional information about the trigger.
	Context map[string]any
}

// ToolChangeListener is the observer interface for tool changes.
// Implement this interface to receive notifications when tools change.
type ToolChangeListener interface {
	// OnToolsChanged is called when tools are added, removed, enabled, or disabled.
	// This method should be non-blocking and thread-safe.
	OnToolsChanged(ctx context.Context, event ToolChangeEvent)
}

// ObservableRegistry extends Registry with observer pattern support.
type ObservableRegistry interface {
	Registry

	// AddListener registers a listener for tool change notifications.
	AddListener(listener ToolChangeListener)

	// RemoveListener unregisters a listener.
	RemoveListener(listener ToolChangeListener)

	// NotifyBatchChange emits a batch change event for multiple tool changes.
	// Used for MCP connect/disconnect or mode changes.
	NotifyBatchChange(ctx context.Context, trigger string, context map[string]any, changes []ToolChange)
}
