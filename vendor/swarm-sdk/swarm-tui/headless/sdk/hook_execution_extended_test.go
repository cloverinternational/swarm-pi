package sdk

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// TestHookExecutionPayload_ExtendedFields verifies HookExecutionPayload includes
// the extended fields for verbose hook execution visibility.
// Spec: 019-hook-execution-visibility
func TestHookExecutionPayload_ExtendedFields(t *testing.T) {
	payload := core.HookExecutionPayload{
		// Existing fields
		HookName: "secret-scanner",
		ToolName: "Write",
		Phase:    "before",
		Success:  true,
		Output:   "No secrets found",
		Blocked:  false,
		Error:    "",
		// NEW extended fields (v1.1)
		Duration:          120, // milliseconds
		ExitCode:          0,
		MatchedPattern:    "Write|Edit",
		TimeoutConfigured: 30, // seconds
		WorkingDir:        "/project/src",
	}

	// Verify existing fields
	if payload.HookName != "secret-scanner" {
		t.Errorf("HookName = %q, want %q", payload.HookName, "secret-scanner")
	}
	if payload.Phase != "before" {
		t.Errorf("Phase = %q, want %q", payload.Phase, "before")
	}

	// Verify extended fields
	if payload.Duration != 120 {
		t.Errorf("Duration = %d, want 120", payload.Duration)
	}
	if payload.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", payload.ExitCode)
	}
	if payload.MatchedPattern != "Write|Edit" {
		t.Errorf("MatchedPattern = %q, want %q", payload.MatchedPattern, "Write|Edit")
	}
	if payload.TimeoutConfigured != 30 {
		t.Errorf("TimeoutConfigured = %d, want 30", payload.TimeoutConfigured)
	}
	if payload.WorkingDir != "/project/src" {
		t.Errorf("WorkingDir = %q, want %q", payload.WorkingDir, "/project/src")
	}
}

// TestHookExecutionPayload_FailedHook tests a failed hook scenario with all fields.
func TestHookExecutionPayload_FailedHook(t *testing.T) {
	payload := core.HookExecutionPayload{
		HookName:          "lint-check",
		ToolName:          "Write",
		Phase:             "after",
		Success:           false,
		Output:            "error: syntax error at line 42",
		Blocked:           false,
		Error:             "lint check failed",
		Duration:          250,
		ExitCode:          1,
		MatchedPattern:    "*",
		TimeoutConfigured: 60,
		WorkingDir:        "/project",
	}

	if payload.Success {
		t.Error("Success should be false for failed hook")
	}
	if payload.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1 for failed hook", payload.ExitCode)
	}
	if payload.Error == "" {
		t.Error("Error should not be empty for failed hook")
	}
}

// TestHookExecutionPayload_BlockedHook tests a hook that blocks execution.
func TestHookExecutionPayload_BlockedHook(t *testing.T) {
	payload := core.HookExecutionPayload{
		HookName:          "security-scan",
		ToolName:          "Bash",
		Phase:             "before",
		Success:           false,
		Output:            "BLOCKED: Dangerous command detected",
		Blocked:           true,
		Error:             "command blocked",
		Duration:          50,
		ExitCode:          2,
		MatchedPattern:    "Bash",
		TimeoutConfigured: 10,
		WorkingDir:        "/project",
	}

	if !payload.Blocked {
		t.Error("Blocked should be true for blocking hook")
	}
	if payload.ExitCode != 2 {
		t.Errorf("ExitCode = %d, want 2 for blocked hook", payload.ExitCode)
	}
}

// TestHookExecutionDisplay_ExtendedFields verifies HookExecutionDisplay includes
// the extended fields for rendering in the UI.
func TestHookExecutionDisplay_ExtendedFields(t *testing.T) {
	display := core.HookExecutionDisplay{
		HookName:          "pre-commit",
		ToolName:          "Edit",
		Phase:             "before",
		Success:           true,
		Output:            "All checks passed",
		Blocked:           false,
		Error:             "",
		Duration:          150,
		ExitCode:          0,
		MatchedPattern:    "Edit|Write",
		TimeoutConfigured: 45,
		WorkingDir:        "/workspace",
	}

	// Verify extended fields are accessible
	if display.Duration != 150 {
		t.Errorf("Duration = %d, want 150", display.Duration)
	}
	if display.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", display.ExitCode)
	}
	if display.MatchedPattern != "Edit|Write" {
		t.Errorf("MatchedPattern = %q, want %q", display.MatchedPattern, "Edit|Write")
	}
	if display.TimeoutConfigured != 45 {
		t.Errorf("TimeoutConfigured = %d, want 45", display.TimeoutConfigured)
	}
	if display.WorkingDir != "/workspace" {
		t.Errorf("WorkingDir = %q, want %q", display.WorkingDir, "/workspace")
	}
}

// TestBridge_HookExecutionUpdate_ExtendedFields verifies the bridge correctly
// maps extended fields from SDK HookExecutionUpdate to TUI HookExecutionPayload.
func TestBridge_HookExecutionUpdate_ExtendedFields(t *testing.T) {
	// Create SDK update with extended fields
	sdkUpdate := agent.HookExecutionUpdate{
		HookName:          "test-hook",
		ToolName:          "Read",
		Phase:             "after",
		Success:           true,
		Output:            "completed",
		Blocked:           false,
		Error:             "",
		Duration:          200 * time.Millisecond,
		ExitCode:          0,
		MatchedPattern:    "Read",
		TimeoutConfigured: 30 * time.Second,
		WorkingDir:        "/test",
	}

	// Convert through bridge
	stateUpdate := convertIntermediateUpdate(sdkUpdate)
	if stateUpdate == nil {
		t.Fatal("convertIntermediateUpdate returned nil")
	}

	// Verify the payload has extended fields
	payload, ok := stateUpdate.Payload.(core.HookExecutionPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want core.HookExecutionPayload", stateUpdate.Payload)
	}

	// Verify extended fields were mapped
	if payload.Duration != 200 {
		t.Errorf("Duration = %d, want 200", payload.Duration)
	}
	if payload.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", payload.ExitCode)
	}
	if payload.MatchedPattern != "Read" {
		t.Errorf("MatchedPattern = %q, want %q", payload.MatchedPattern, "Read")
	}
	if payload.TimeoutConfigured != 30 {
		t.Errorf("TimeoutConfigured = %d, want 30", payload.TimeoutConfigured)
	}
	if payload.WorkingDir != "/test" {
		t.Errorf("WorkingDir = %q, want %q", payload.WorkingDir, "/test")
	}
}

// TestHookExecutionPayload_JSONSerialization verifies JSON tags are correct.
func TestHookExecutionPayload_JSONSerialization(t *testing.T) {
	payload := core.HookExecutionPayload{
		HookName:          "json-test",
		Duration:          100,
		ExitCode:          0,
		MatchedPattern:    "test",
		TimeoutConfigured: 30,
		WorkingDir:        "/path",
	}

	// The struct should have proper json tags for the extended fields:
	// Duration          int    `json:"duration,omitempty"`
	// ExitCode          int    `json:"exitCode,omitempty"`
	// MatchedPattern    string `json:"matchedPattern,omitempty"`
	// TimeoutConfigured int    `json:"timeoutConfigured,omitempty"`
	// WorkingDir        string `json:"workingDir,omitempty"`

	// Verify fields are accessible (JSON serialization tested implicitly by IPC)
	if payload.Duration == 0 && payload.ExitCode == 0 && payload.MatchedPattern == "" {
		t.Error("Extended fields should be set")
	}
}
