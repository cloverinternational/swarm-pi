package sdk

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// =============================================================================
// Spec: 020-hook-execution-realtime
// Tests for real-time hook execution bridge conversion
// =============================================================================

// TestBridge_HookOutputChunk_Conversion verifies the bridge correctly
// converts SDK HookOutputChunk to TUI core HookOutputChunk payload.
func TestBridge_HookOutputChunk_Conversion(t *testing.T) {
	// Create SDK chunk
	sdkChunk := agent.HookOutputChunk{
		HookID:    "hook-exec-abc123",
		HookName:  "secret-scanner",
		Chunk:     "Scanning file: main.go\n",
		IsStderr:  false,
		Timestamp: time.Now().UnixMilli(),
	}

	// Convert through bridge
	stateUpdate := convertIntermediateUpdate(sdkChunk)
	if stateUpdate == nil {
		t.Fatal("convertIntermediateUpdate returned nil for HookOutputChunk")
	}

	// Verify update type
	if stateUpdate.Type != core.UpdateHookOutputChunk {
		t.Errorf("UpdateType = %q, want %q", stateUpdate.Type, core.UpdateHookOutputChunk)
	}

	// Verify the payload is correct type
	payload, ok := stateUpdate.Payload.(core.HookOutputChunk)
	if !ok {
		t.Fatalf("Payload type = %T, want core.HookOutputChunk", stateUpdate.Payload)
	}

	// Verify all fields were mapped
	if payload.HookID != sdkChunk.HookID {
		t.Errorf("HookID = %q, want %q", payload.HookID, sdkChunk.HookID)
	}
	if payload.HookName != sdkChunk.HookName {
		t.Errorf("HookName = %q, want %q", payload.HookName, sdkChunk.HookName)
	}
	if payload.Chunk != sdkChunk.Chunk {
		t.Errorf("Chunk = %q, want %q", payload.Chunk, sdkChunk.Chunk)
	}
	if payload.IsStderr != sdkChunk.IsStderr {
		t.Errorf("IsStderr = %v, want %v", payload.IsStderr, sdkChunk.IsStderr)
	}
	if payload.Timestamp != sdkChunk.Timestamp {
		t.Errorf("Timestamp = %d, want %d", payload.Timestamp, sdkChunk.Timestamp)
	}
}

// TestBridge_HookExecutionUpdate_RealtimeFields verifies the bridge correctly
// maps the new real-time fields (hookId, status, startedAt) from SDK to TUI.
func TestBridge_HookExecutionUpdate_RealtimeFields(t *testing.T) {
	tests := []struct {
		name      string
		sdkUpdate agent.HookExecutionUpdate
		wantType  core.UpdateType
	}{
		{
			name: "started_event",
			sdkUpdate: agent.HookExecutionUpdate{
				HookID:            "hook-exec-start-001",
				Status:            "started",
				StartedAt:         time.Now().UnixMilli(),
				HookName:          "lint-check",
				ToolName:          "Write",
				Phase:             "before",
				TimeoutConfigured: 30 * time.Second,
				WorkingDir:        "/project",
			},
			wantType: core.UpdateHookExecution,
		},
		{
			name: "completed_event",
			sdkUpdate: agent.HookExecutionUpdate{
				HookID:            "hook-exec-start-001",
				Status:            "completed",
				StartedAt:         time.Now().Add(-2 * time.Second).UnixMilli(),
				HookName:          "lint-check",
				ToolName:          "Write",
				Phase:             "before",
				Success:           true,
				Output:            "All checks passed",
				Duration:          2 * time.Second,
				ExitCode:          0,
				MatchedPattern:    "Write|Edit",
				TimeoutConfigured: 30 * time.Second,
				WorkingDir:        "/project",
			},
			wantType: core.UpdateHookExecution,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert through bridge
			stateUpdate := convertIntermediateUpdate(tt.sdkUpdate)
			if stateUpdate == nil {
				t.Fatal("convertIntermediateUpdate returned nil")
			}

			// Verify update type
			if stateUpdate.Type != tt.wantType {
				t.Errorf("UpdateType = %q, want %q", stateUpdate.Type, tt.wantType)
			}

			// Verify payload type
			payload, ok := stateUpdate.Payload.(core.HookExecutionPayload)
			if !ok {
				t.Fatalf("Payload type = %T, want core.HookExecutionPayload", stateUpdate.Payload)
			}

			// Verify new real-time fields
			if payload.HookID != tt.sdkUpdate.HookID {
				t.Errorf("HookID = %q, want %q", payload.HookID, tt.sdkUpdate.HookID)
			}
			if payload.Status != tt.sdkUpdate.Status {
				t.Errorf("Status = %q, want %q", payload.Status, tt.sdkUpdate.Status)
			}
			if payload.StartedAt != tt.sdkUpdate.StartedAt {
				t.Errorf("StartedAt = %d, want %d", payload.StartedAt, tt.sdkUpdate.StartedAt)
			}
		})
	}
}

// TestBridge_HookOutputChunk_StderrFlag verifies stderr flag is correctly mapped.
func TestBridge_HookOutputChunk_StderrFlag(t *testing.T) {
	// Test stderr chunk
	stderrChunk := agent.HookOutputChunk{
		HookID:    "hook-stderr-test",
		HookName:  "build-check",
		Chunk:     "error: compilation failed\n",
		IsStderr:  true,
		Timestamp: time.Now().UnixMilli(),
	}

	stateUpdate := convertIntermediateUpdate(stderrChunk)
	if stateUpdate == nil {
		t.Fatal("convertIntermediateUpdate returned nil")
	}

	payload, ok := stateUpdate.Payload.(core.HookOutputChunk)
	if !ok {
		t.Fatalf("Payload type = %T, want core.HookOutputChunk", stateUpdate.Payload)
	}

	if !payload.IsStderr {
		t.Error("IsStderr should be true for stderr chunk")
	}

	// Test stdout chunk
	stdoutChunk := agent.HookOutputChunk{
		HookID:    "hook-stdout-test",
		HookName:  "build-check",
		Chunk:     "Building...\n",
		IsStderr:  false,
		Timestamp: time.Now().UnixMilli(),
	}

	stateUpdate = convertIntermediateUpdate(stdoutChunk)
	payload, _ = stateUpdate.Payload.(core.HookOutputChunk)

	if payload.IsStderr {
		t.Error("IsStderr should be false for stdout chunk")
	}
}

// TestHookExecutionPayload_StatusValues verifies valid status values.
func TestHookExecutionPayload_StatusValues(t *testing.T) {
	validStatuses := []string{"started", "completed"}

	for _, status := range validStatuses {
		payload := core.HookExecutionPayload{
			HookID:   "test-hook-id",
			Status:   status,
			HookName: "test-hook",
		}

		if payload.Status != status {
			t.Errorf("Status = %q, want %q", payload.Status, status)
		}
	}
}

// TestHookOutputChunk_EmptyChunk verifies handling of empty output chunks.
func TestHookOutputChunk_EmptyChunk(t *testing.T) {
	// Empty chunk should still be valid (might happen with just newlines)
	emptyChunk := agent.HookOutputChunk{
		HookID:    "hook-empty-test",
		HookName:  "test-hook",
		Chunk:     "",
		IsStderr:  false,
		Timestamp: time.Now().UnixMilli(),
	}

	stateUpdate := convertIntermediateUpdate(emptyChunk)
	if stateUpdate == nil {
		t.Fatal("convertIntermediateUpdate returned nil for empty chunk")
	}

	payload, ok := stateUpdate.Payload.(core.HookOutputChunk)
	if !ok {
		t.Fatalf("Payload type = %T, want core.HookOutputChunk", stateUpdate.Payload)
	}

	// Empty chunk is valid
	if payload.Chunk != "" {
		t.Errorf("Chunk = %q, want empty string", payload.Chunk)
	}
}
