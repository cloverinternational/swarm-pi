// Package builtin provides built-in hooks for common SDK functionality.
package builtin

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// WriteValidationHook tracks Write tool validation failures and blocks repeated misuse.
//
// This hook fires on ToolAfterExecute for Write tool failures.
// It tracks consecutive failures with missing required parameters (file_path, content).
//
// Behavior:
//   - 1st failure: Normal error returned (enriched by FSWrite.Validate)
//   - 2nd failure: Warning message added to context
//   - 3rd+ failure: BLOCKS execution and requires agent acknowledgment
//
// Important: This hook ONLY triggers for missing required parameter failures.
// It does NOT trigger for:
//   - Whitespace issues in edits (that's an editing problem, not tool misuse)
//   - Content mismatches in patches
//   - File not found errors
//   - Permission denied errors
//
// The goal is to teach agents the correct Write tool pattern when they're
// repeatedly calling Write({}) without required parameters.
type WriteValidationHook struct {
	// failureCounts tracks Write validation failures per conversation
	failureCounts map[string]*writeFailureState
	mu            sync.RWMutex
}

// writeFailureState tracks the failure state for a conversation
type writeFailureState struct {
	count         int
	lastErrorType string
	blocked       bool
	acknowledged  bool
}

// NewWriteValidationHook creates a new Write validation hook.
func NewWriteValidationHook() *WriteValidationHook {
	return &WriteValidationHook{
		failureCounts: make(map[string]*writeFailureState),
	}
}

// Name returns the hook name.
func (h *WriteValidationHook) Name() string {
	return "write-validation-hook"
}

// Priority returns the hook priority (runs early to catch failures).
func (h *WriteValidationHook) Priority() int {
	return 90 // High priority, runs before most other hooks
}

// Filter returns true for tool after execute events involving Write tool.
func (h *WriteValidationHook) Filter(event hooks.Event) bool {
	if event.Type != hooks.EventToolAfterExecute {
		return false
	}
	toolName, _ := event.Data["tool_name"].(string)
	return strings.EqualFold(toolName, "Write")
}

// OnEvent processes Write tool execution results.
func (h *WriteValidationHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Extract conversation ID
	conversationID := event.ConversationID
	if conversationID == "" {
		conversationID = "default"
	}

	// Extract tool result information
	toolName, _ := event.Data["tool_name"].(string)
	if !strings.EqualFold(toolName, "Write") {
		return hooks.Continue(), nil
	}

	// Check if there was an error
	errorType, hasErrorType := event.Data["error_type"].(string)
	errorMsg, hasErrorMsg := event.Data["error_message"].(string)
	success, _ := event.Data["success"].(bool)

	// If success, reset failure count for this conversation
	if success || (!hasErrorType && !hasErrorMsg) {
		h.resetFailureCount(conversationID)
		return hooks.Continue(), nil
	}

	// Check if this is a missing required parameter error
	// ONLY track these types of failures, not whitespace/content mismatches
	if !h.isMissingRequiredParamError(errorType, errorMsg) {
		// This is a different type of error (whitespace, permission, etc.)
		// Don't count it toward the failure threshold
		return hooks.Continue(), nil
	}

	// Increment failure count
	state := h.incrementFailureCount(conversationID, errorType)

	// Determine response based on failure count
	switch state.count {
	case 1:
		// First failure: just return normally (error message is already enriched by FSWrite.Validate)
		return hooks.Continue(), nil

	case 2:
		// Second failure: add warning to context
		warningMsg := `[WRITE TOOL WARNING]

You have failed to provide required parameters to the Write tool twice.

REQUIRED PARAMETERS:
  - file_path: Absolute path to the file (e.g., "/home/user/project/file.txt")
  - content: The full text content to write

EXAMPLE:
  Write({
    "file_path": "/absolute/path/to/file.txt",
    "content": "your content here"
  })

Next failure will require acknowledgment before continuing.`

		return hooks.ContinueWithMessage(warningMsg), nil

	default:
		// 3rd+ failure: Block and require acknowledgment
		if !state.acknowledged {
			state.blocked = true
			blockMsg := h.buildBlockingMessage(state.count, errorMsg)
			return hooks.Block(blockMsg), nil
		}
		// Already acknowledged, allow to proceed
		return hooks.Continue(), nil
	}
}

// isMissingRequiredParamError checks if the error is about missing required parameters.
// Returns false for whitespace issues, content mismatches, permission errors, etc.
func (h *WriteValidationHook) isMissingRequiredParamError(errorType, errorMsg string) bool {
	// Check error type patterns for missing required params
	missingParamPatterns := []string{
		"file_path.missing",
		"content.missing",
		"file_path is required",
		"content is required",
		"missing required parameters",
		"Write tool call missing required",
	}

	for _, pattern := range missingParamPatterns {
		if strings.Contains(errorType, pattern) || strings.Contains(errorMsg, pattern) {
			return true
		}
	}

	// Check if error mentions empty params object
	if strings.Contains(errorMsg, "Write({})") || strings.Contains(errorMsg, "empty parameter") {
		return true
	}

	// DO NOT trigger for these error types (editing issues, not tool misuse)
	editingErrorPatterns := []string{
		"whitespace",
		"old_string does not match",
		"content mismatch",
		"hash mismatch",
		"line number",
		"undo",
		"snapshot",
		"overwrite",
		"already exists",
		"permission denied",
		"not found",
		"outside workspace",
	}

	for _, pattern := range editingErrorPatterns {
		if strings.Contains(errorType, pattern) || strings.Contains(errorMsg, pattern) {
			return false
		}
	}

	// Default: if we can't determine, don't count it
	// (fail open - only trigger on known missing param patterns)
	return false
}

// incrementFailureCount increments the failure count for a conversation.
func (h *WriteValidationHook) incrementFailureCount(conversationID string, errorType string) *writeFailureState {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, exists := h.failureCounts[conversationID]
	if !exists {
		state = &writeFailureState{}
		h.failureCounts[conversationID] = state
	}

	state.count++
	state.lastErrorType = errorType
	return state
}

// resetFailureCount resets the failure count for a conversation.
func (h *WriteValidationHook) resetFailureCount(conversationID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, exists := h.failureCounts[conversationID]
	if exists {
		state.count = 0
		state.blocked = false
		state.acknowledged = false
	}
}

// Acknowledge marks the hook as acknowledged for a conversation, allowing execution to continue.
func (h *WriteValidationHook) Acknowledge(conversationID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	state, exists := h.failureCounts[conversationID]
	if !exists {
		state = &writeFailureState{}
		h.failureCounts[conversationID] = state
	}
	state.acknowledged = true
	state.blocked = false
}

// IsBlocked checks if the conversation is currently blocked.
func (h *WriteValidationHook) IsBlocked(conversationID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	state, exists := h.failureCounts[conversationID]
	if !exists {
		return false
	}
	return state.blocked && !state.acknowledged
}

// GetFailureCount returns the current failure count for a conversation.
func (h *WriteValidationHook) GetFailureCount(conversationID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	state, exists := h.failureCounts[conversationID]
	if !exists {
		return 0
	}
	return state.count
}

// buildBlockingMessage creates the blocking message for 3rd+ failures.
func (h *WriteValidationHook) buildBlockingMessage(count int, lastError string) string {
	return fmt.Sprintf(`[WRITE TOOL BLOCKED - ACKNOWLEDGMENT REQUIRED]

═══════════════════════════════════════════════════════════════════════════════
            YOU MUST ACKNOWLEDGE CORRECT USAGE TO CONTINUE
═══════════════════════════════════════════════════════════════════════════════

You have failed to provide required parameters to the Write tool %d times.

THIS IS A TOOL USAGE ERROR - NOT AN EDITING ERROR

═══════════════════════════════════════════════════════════════════════════════
                             REQUIRED ACKNOWLEDGMENT
═══════════════════════════════════════════════════════════════════════════════

You MUST acknowledge that you understand the correct Write tool usage:

1. Write tool requires TWO mandatory parameters: file_path and content
2. file_path MUST be an absolute path (starts with /)
3. Both parameters must be provided in EVERY Write call

═══════════════════════════════════════════════════════════════════════════════
                             CORRECT USAGE PATTERN
═══════════════════════════════════════════════════════════════════════════════

Write({
  "file_path": "/absolute/path/to/your/file.txt",
  "content": "your actual content here"
})

═══════════════════════════════════════════════════════════════════════════════
                             HOW TO ACKNOWLEDGE
═══════════════════════════════════════════════════════════════════════════════

Reply with a message that includes the phrase:
  "I acknowledge the correct Write tool usage"

This will unblock the tool and reset the failure counter.

═══════════════════════════════════════════════════════════════════════════════
                             LAST ERROR
═══════════════════════════════════════════════════════════════════════════════

%s

═══════════════════════════════════════════════════════════════════════════════
IMPORTANT: THIS IS DIFFERENT FROM EDIT ERRORS
═══════════════════════════════════════════════════════════════════════════════

This block is for MISSING PARAMETERS only.
If you're seeing whitespace or "old_string does not match" errors,
that's a different issue - use Read to verify file content before editing.`,
		count,
		lastError)
}
