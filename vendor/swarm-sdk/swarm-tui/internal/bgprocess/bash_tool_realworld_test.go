package bgprocess

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// TestBackgroundBashTool_RealWorld_Sleep120 simulates what happens when an agent
// runs "sleep 120" with a 5-second timeout, exactly as you requested.
func TestBackgroundBashTool_RealWorld_Sleep120(t *testing.T) {
	t.Log("=== Simulating Agent Running: sleep 120 with timeout_seconds: 5 ===")

	// Initialize manager
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	// Create the BackgroundBashTool (same as in production)
	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig:        builtin.DefaultBashConfig(),
		Manager:           mgr,
		DefaultTimeout:    5 * time.Minute,
		AutoBackgroundSec: 30,
	})

	// Create context with owner info (simulating agent)
	ctx := context.Background()
	ctx = context.WithValue(ctx, "user_id", "test-agent")
	ctx = context.WithValue(ctx, "agent_id", "agent-claude")
	ctx = context.WithValue(ctx, "conversation_id", "conv-123")

	t.Log("Agent sends command: sleep 120")
	t.Log("Agent specifies timeout: 5 seconds")
	t.Log("")

	// Record start time
	startTime := time.Now()

	// Agent executes the command (THIS IS THE ACTUAL IMPLEMENTATION)
	result, err := tool.Execute(ctx, map[string]any{
		"command":         "sleep 120",
		"timeout_seconds": 5.0, // Agent waits max 5 seconds
	})

	// Record end time
	elapsed := time.Since(startTime)

	t.Logf("Execution returned after: %.2f seconds", elapsed.Seconds())
	t.Log("")

	// Verify the behavior
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Should return after ~5 seconds (not 120 seconds!)
	if elapsed > 10*time.Second {
		t.Errorf("Agent waited too long! Expected ~5s, got %.2fs", elapsed.Seconds())
	}

	if elapsed < 4*time.Second {
		t.Errorf("Returned too quickly (before timeout). Got %.2fs", elapsed.Seconds())
	}

	t.Log("✓ Agent did NOT wait 120 seconds")
	t.Log("✓ Agent waited approximately 5 seconds (the timeout)")
	t.Log("")

	// Parse the response
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		t.Fatalf("Failed to parse response: %v\nOutput: %s", err, result.Output)
	}

	t.Log("Agent received response:")
	responseJSON, _ := json.MarshalIndent(response, "  ", "  ")
	t.Logf("  %s", responseJSON)
	t.Log("")

	// Verify the response indicates auto-backgrounding
	if backgrounded, ok := response["backgrounded"].(bool); !ok || !backgrounded {
		t.Error("❌ Response should indicate command was backgrounded")
	} else {
		t.Log("✓ Response indicates command was auto-backgrounded")
	}

	taskID := ""
	if tid, ok := response["task_id"].(string); !ok || tid == "" {
		t.Error("❌ Response should include task_id")
	} else {
		taskID = tid
		t.Logf("✓ Task ID returned: %s", taskID)
	}

	if status, ok := response["status"].(string); !ok || status != "running" {
		t.Error("❌ Status should be 'running'")
	} else {
		t.Log("✓ Status is 'running'")
	}

	if message, ok := response["message"].(string); !ok || !strings.Contains(message, "auto-backgrounded") {
		t.Error("❌ Message should mention auto-backgrounding")
	} else {
		t.Log("✓ Message explains auto-backgrounding")
	}

	t.Log("")
	t.Log("=== Agent can now check status later ===")

	// Agent can check the status while it's still running
	info, err := mgr.GetInfo(ctx, NewProcessHandle(taskID))
	if err != nil {
		t.Fatalf("Failed to get process info: %v", err)
	}

	t.Logf("Process state: %s", info.State)
	t.Logf("Command: %s", info.Command)
	t.Logf("Duration so far: %.2f seconds", info.Duration.Seconds())

	if info.State != StateRunning {
		t.Errorf("Process should still be running, got: %s", info.State)
	}

	t.Log("")
	t.Log("✓ Process is still running in background")
	t.Log("✓ Agent can continue doing other work")
	t.Log("✓ Agent can check status/output later with ReadBackgroundCommand")

	// Clean up - cancel the background process
	owner := OwnerInfo{UserID: "test-agent"}
	if err := mgr.Cancel(ctx, NewProcessHandle(taskID), owner); err != nil {
		t.Logf("Warning: Failed to cancel process: %v", err)
	} else {
		t.Log("")
		t.Log("✓ Cleanup: Background process cancelled successfully")
	}

	// Final verification
	t.Log("")
	t.Log("=== VERIFICATION COMPLETE ===")
	t.Log("✅ Agent runs 'sleep 120' with 5s timeout")
	t.Log("✅ Agent waits ~5 seconds (NOT 120 seconds)")
	t.Log("✅ Command auto-backgrounds after timeout")
	t.Log("✅ Agent receives task_id to check later")
	t.Log("✅ Process continues running in background")
	t.Log("✅ Agent can continue other work immediately")
}

// TestBackgroundBashTool_ExplicitBackground_Sleep120 tests the explicit background mode
func TestBackgroundBashTool_ExplicitBackground_Sleep120(t *testing.T) {
	t.Log("=== Testing EXPLICIT background mode: sleep 120 ===")

	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
		// This test explicitly validates the agent-set `background=true` behavior.
		AllowExplicitBackground: true,
	})

	ctx := testContext()

	t.Log("Agent sends command with background=true")

	startTime := time.Now()
	result, err := tool.Execute(ctx, map[string]any{
		"command":         "sleep 120",
		"timeout_seconds": 300.0,
		"background":      true, // EXPLICIT background
	})
	elapsed := time.Since(startTime)

	t.Logf("Returned after: %.2f seconds", elapsed.Seconds())

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Should return IMMEDIATELY (< 1 second)
	if elapsed > 2*time.Second {
		t.Errorf("Explicit background should return immediately, took %.2fs", elapsed.Seconds())
	}

	t.Log("✓ Returned immediately (explicit background)")

	var response map[string]any
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if taskID, ok := response["task_id"].(string); !ok || taskID == "" {
		t.Error("Should have task_id")
	} else {
		t.Logf("✓ Got task_id: %s", taskID)
		// Cancel it
		mgr.Cancel(ctx, NewProcessHandle(taskID), OwnerInfo{UserID: "test-user"})
	}

	t.Log("✅ Explicit background mode works correctly")
}

// TestBackgroundBashTool_QuickCommand tests command that completes within timeout
func TestBackgroundBashTool_QuickCommand_ReturnsImmediately(t *testing.T) {
	t.Log("=== Testing quick command that completes within timeout ===")

	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	ctx := testContext()

	t.Log("Agent sends: echo 'hello' with 5s timeout")

	startTime := time.Now()
	result, err := tool.Execute(ctx, map[string]any{
		"command":         "echo 'hello world'",
		"timeout_seconds": 5.0,
	})
	elapsed := time.Since(startTime)

	t.Logf("Returned after: %.2f seconds", elapsed.Seconds())

	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}

	// Should return quickly (command completes fast)
	if elapsed > 3*time.Second {
		t.Errorf("Quick command took too long: %.2fs", elapsed.Seconds())
	}

	t.Log("✓ Returned quickly")

	// Should NOT be backgrounded - should have output
	if strings.Contains(result.Output, "backgrounded") {
		t.Error("Quick command should NOT be backgrounded")
		t.Logf("Output: %s", result.Output)
	}

	if !strings.Contains(result.Output, "hello world") {
		t.Errorf("Output should contain 'hello world', got: %s", result.Output)
	}

	t.Log("✓ Got output directly (not backgrounded)")
	t.Log("✅ Quick commands work correctly")
}
