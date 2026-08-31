package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// TestWriteValidationHook_MissingParamDetection tests that the hook correctly
// identifies missing required parameter errors vs other error types.
func TestWriteValidationHook_MissingParamDetection(t *testing.T) {
	h := NewWriteValidationHook()

	tests := []struct {
		name     string
		errType  string
		errMsg   string
		expected bool
	}{
		{
			name:     "file_path.missing error type",
			errType:  "file_path.missing",
			errMsg:   "file_path is required",
			expected: true,
		},
		{
			name:     "content.missing error type",
			errType:  "content.missing",
			errMsg:   "content is required",
			expected: true,
		},
		{
			name:     "empty params error message",
			errType:  "",
			errMsg:   "Write tool call missing required parameters",
			expected: true,
		},
		{
			name:     "empty params object",
			errType:  "",
			errMsg:   "Write({})",
			expected: true,
		},
		// These should NOT trigger the hook (editing issues, not tool misuse)
		{
			name:     "whitespace error",
			errType:  "edit.whitespace_mismatch",
			errMsg:   "whitespace does not match",
			expected: false,
		},
		{
			name:     "old_string mismatch",
			errType:  "patch.mismatch",
			errMsg:   "old_string does not match",
			expected: false,
		},
		{
			name:     "content mismatch",
			errType:  "content.mismatch",
			errMsg:   "content mismatch in file",
			expected: false,
		},
		{
			name:     "permission denied",
			errType:  "fs_write.permission_denied",
			errMsg:   "permission denied",
			expected: false,
		},
		{
			name:     "file already exists",
			errType:  "fs_write.exists",
			errMsg:   "file already exists",
			expected: false,
		},
		{
			name:     "path outside workspace",
			errType:  "fs_write.path_outside_workspace",
			errMsg:   "path must be within workspace",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.isMissingRequiredParamError(tt.errType, tt.errMsg)
			if result != tt.expected {
				t.Errorf("isMissingRequiredParamError(%q, %q) = %v, want %v",
					tt.errType, tt.errMsg, result, tt.expected)
			}
		})
	}
}

// TestWriteValidationHook_FailureCounting tests the failure counting logic.
func TestWriteValidationHook_FailureCounting(t *testing.T) {
	h := NewWriteValidationHook()
	convID := "test-conversation"

	// Initially should be 0
	if count := h.GetFailureCount(convID); count != 0 {
		t.Errorf("Initial count = %d, want 0", count)
	}

	// Simulate first failure
	state := h.incrementFailureCount(convID, "file_path.missing")
	if state.count != 1 {
		t.Errorf("After 1st increment, count = %d, want 1", state.count)
	}

	// Simulate second failure
	state = h.incrementFailureCount(convID, "content.missing")
	if state.count != 2 {
		t.Errorf("After 2nd increment, count = %d, want 2", state.count)
	}

	// Check GetFailureCount
	if count := h.GetFailureCount(convID); count != 2 {
		t.Errorf("GetFailureCount = %d, want 2", count)
	}

	// Reset and verify
	h.resetFailureCount(convID)
	if count := h.GetFailureCount(convID); count != 0 {
		t.Errorf("After reset, count = %d, want 0", count)
	}
}

// TestWriteValidationHook_SuccessResetsCount tests that successful Write calls
// reset the failure counter.
func TestWriteValidationHook_SuccessResetsCount(t *testing.T) {
	h := NewWriteValidationHook()
	convID := "test-conversation"

	// Simulate two failures
	h.incrementFailureCount(convID, "file_path.missing")
	h.incrementFailureCount(convID, "content.missing")

	if count := h.GetFailureCount(convID); count != 2 {
		t.Errorf("After 2 failures, count = %d, want 2", count)
	}

	// Simulate successful Write event
	ctx := context.Background()
	event := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: convID,
		Data: map[string]any{
			"tool_name": "Write",
			"success":   true,
		},
	}

	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent failed: %v", err)
	}

	if result.Action != hooks.ActionContinue {
		t.Errorf("OnEvent with success should continue, got %v", result.Action)
	}

	// Failure count should be reset
	if count := h.GetFailureCount(convID); count != 0 {
		t.Errorf("After success, count = %d, want 0", count)
	}
}

// TestWriteValidationHook_SecondFailureWarning tests that the 2nd failure
// returns a warning message but doesn't block.
func TestWriteValidationHook_SecondFailureWarning(t *testing.T) {
	h := NewWriteValidationHook()
	convID := "test-conversation"

	// First failure
	ctx := context.Background()
	event := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: convID,
		Data: map[string]any{
			"tool_name":     "Write",
			"error_type":    "file_path.missing",
			"error_message": "file_path is required",
			"success":       false,
		},
	}

	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("First OnEvent failed: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("First failure should continue, got %v", result.Action)
	}

	// Second failure should have warning but still continue
	result, err = h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("Second OnEvent failed: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("Second failure should continue, got %v", result.Action)
	}
	if !strings.Contains(result.Message, "WRITE TOOL WARNING") {
		t.Errorf("Second failure should include warning message, got: %s", result.Message)
	}
}

// TestWriteValidationHook_ThirdFailureBlocks tests that the 3rd failure
// blocks execution and requires acknowledgment.
func TestWriteValidationHook_ThirdFailureBlocks(t *testing.T) {
	h := NewWriteValidationHook()
	convID := "test-conversation"

	ctx := context.Background()
	event := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: convID,
		Data: map[string]any{
			"tool_name":     "Write",
			"error_type":    "file_path.missing",
			"error_message": "file_path is required",
			"success":       false,
		},
	}

	// First two failures
	h.OnEvent(ctx, event)
	h.OnEvent(ctx, event)

	// Third failure should block
	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("Third OnEvent failed: %v", err)
	}
	if result.Action != hooks.ActionBlock {
		t.Errorf("Third failure should block, got %v", result.Action)
	}
	if !strings.Contains(result.Message, "ACKNOWLEDGMENT REQUIRED") {
		t.Errorf("Block message should mention acknowledgment, got: %s", result.Message)
	}

	// Should be blocked
	if !h.IsBlocked(convID) {
		t.Error("IsBlocked should return true after 3rd failure")
	}
}

// TestWriteValidationHook_AcknowledgmentUnblocks tests that acknowledgment
// allows execution to continue.
func TestWriteValidationHook_AcknowledgmentUnblocks(t *testing.T) {
	h := NewWriteValidationHook()
	convID := "test-conversation"

	ctx := context.Background()
	event := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: convID,
		Data: map[string]any{
			"tool_name":     "Write",
			"error_type":    "file_path.missing",
			"error_message": "file_path is required",
			"success":       false,
		},
	}

	// Three failures to trigger block
	h.OnEvent(ctx, event)
	h.OnEvent(ctx, event)
	h.OnEvent(ctx, event)

	if !h.IsBlocked(convID) {
		t.Fatal("Should be blocked after 3 failures")
	}

	// Acknowledge
	h.Acknowledge(convID)

	// Should no longer be blocked
	if h.IsBlocked(convID) {
		t.Error("Should not be blocked after acknowledgment")
	}

	// Next failure should continue (acknowledged)
	result, err := h.OnEvent(ctx, event)
	if err != nil {
		t.Fatalf("OnEvent after acknowledgment failed: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("Should continue after acknowledgment, got %v", result.Action)
	}
}

// TestWriteValidationHook_FilterOnlyWriteTool tests that the hook only
// fires for Write tool events.
func TestWriteValidationHook_FilterOnlyWriteTool(t *testing.T) {
	h := NewWriteValidationHook()

	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{"Write tool", "Write", true},
		{"write lowercase", "write", true},
		{"WRITE uppercase", "WRITE", true},
		{"Read tool", "Read", false},
		{"Bash tool", "Bash", false},
		{"Empty tool name", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := hooks.Event{
				Type: hooks.EventToolAfterExecute,
				Data: map[string]any{
					"tool_name": tt.toolName,
				},
			}
			result := h.Filter(event)
			if result != tt.expected {
				t.Errorf("Filter(%q) = %v, want %v", tt.toolName, result, tt.expected)
			}
		})
	}
}

// TestWriteValidationHook_NoConversationID tests behavior with default
// conversation ID.
func TestWriteValidationHook_NoConversationID(t *testing.T) {
	h := NewWriteValidationHook()

	ctx := context.Background()
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		// No ConversationID set
		Data: map[string]any{
			"tool_name":     "Write",
			"error_type":    "file_path.missing",
			"error_message": "file_path is required",
			"success":       false,
		},
	}

	// Should use "default" as conversation ID
	for range 3 {
		h.OnEvent(ctx, event)
	}

	// Should be blocked under "default" conversation
	if !h.IsBlocked("default") {
		t.Error("Should be blocked for default conversation")
	}
}

// TestWriteValidationHook_IgnoresOtherEvents tests that the hook ignores
// non-tool events.
func TestWriteValidationHook_IgnoresOtherEvents(t *testing.T) {
	h := NewWriteValidationHook()

	eventTypes := []string{
		hooks.EventToolBeforeExecute,
		hooks.EventMessageAfterReceive,
		hooks.EventProviderAfterResponse,
		hooks.EventContextWindowExceeded,
	}

	for _, eventType := range eventTypes {
		t.Run(eventType, func(t *testing.T) {
			event := hooks.Event{
				Type: eventType,
				Data: map[string]any{
					"tool_name": "Write",
				},
			}
			if h.Filter(event) {
				t.Errorf("Filter should return false for %v", eventType)
			}
		})
	}
}

// TestWriteValidationHook_MultiConversationIsolation tests that failure
// counts are isolated per conversation.
func TestWriteValidationHook_MultiConversationIsolation(t *testing.T) {
	h := NewWriteValidationHook()
	conv1 := "conversation-1"
	conv2 := "conversation-2"

	ctx := context.Background()
	event := hooks.Event{
		Type:           hooks.EventToolAfterExecute,
		ConversationID: conv1,
		Data: map[string]any{
			"tool_name":     "Write",
			"error_type":    "file_path.missing",
			"error_message": "file_path is required",
			"success":       false,
		},
	}

	// Add 2 failures to conv1
	h.OnEvent(ctx, event)
	h.OnEvent(ctx, event)

	// Conv1 should have 2, conv2 should have 0
	if h.GetFailureCount(conv1) != 2 {
		t.Errorf("conv1 count = %d, want 2", h.GetFailureCount(conv1))
	}
	if h.GetFailureCount(conv2) != 0 {
		t.Errorf("conv2 count = %d, want 0", h.GetFailureCount(conv2))
	}

	// Add event for conv2
	event.ConversationID = conv2
	h.OnEvent(ctx, event)

	// Conv1 should still have 2, conv2 should have 1
	if h.GetFailureCount(conv1) != 2 {
		t.Errorf("conv1 count = %d, want 2", h.GetFailureCount(conv1))
	}
	if h.GetFailureCount(conv2) != 1 {
		t.Errorf("conv2 count = %d, want 1", h.GetFailureCount(conv2))
	}
}
