package bgprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// testContext creates a context with required owner info for testing
func testContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, "user_id", "test-user")
	ctx = context.WithValue(ctx, "agent_id", "test-agent")
	ctx = context.WithValue(ctx, "conversation_id", "test-conv")
	return ctx
}

func TestBackgroundBashTool_RequiresTimeout(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Test without timeout - should error
	result, err := tool.Execute(testContext(), map[string]any{
		"command": "echo 'test'",
	})

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if !result.IsError {
		t.Error("Expected error result when timeout_seconds not provided")
	}

	if !strings.Contains(result.Output, "timeout_seconds") {
		t.Errorf("Error message should mention timeout_seconds, got: %s", result.Output)
	}
}

func TestBackgroundBashTool_ForegroundCompletesWithinTimeout(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Create context with owner info
	ctx := context.Background()
	ctx = context.WithValue(ctx, "user_id", "test-user")

	// Quick command that completes within timeout
	result, err := tool.Execute(ctx, map[string]any{
		"command":         "echo 'hello world'",
		"timeout_seconds": 5.0,
	})

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.Output)
	}

	if !strings.Contains(result.Output, "hello world") {
		t.Errorf("Expected output to contain 'hello world', got: %s", result.Output)
	}
}

func TestBackgroundBashTool_ForegroundAutoBackgroundsOnTimeout(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Long-running command with short timeout
	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "sleep 5",
		"timeout_seconds": 0.5, // Very short timeout
	})

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Should return backgrounded response
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		t.Fatalf("Failed to parse response as JSON: %v\nOutput: %s", err, result.Output)
	}

	// Check for backgrounded fields
	if backgrounded, ok := response["backgrounded"].(bool); !ok || !backgrounded {
		t.Error("Expected backgrounded=true in response")
	}

	if taskID, ok := response["task_id"].(string); !ok || taskID == "" {
		t.Error("Expected task_id in response")
	} else {
		// Verify the background process exists
		ctx := context.Background()
		info, err := mgr.GetInfo(ctx, NewProcessHandle(taskID))
		if err != nil {
			t.Errorf("Background process should exist: %v", err)
		} else {
			if info.State != StateRunning && info.State != StateCompleted {
				t.Errorf("Background process should be running or completed, got: %v", info.State)
			}
		}

		// Cancel it to avoid hanging test
		owner := OwnerInfo{UserID: "test"}
		mgr.Cancel(ctx, NewProcessHandle(taskID), owner)
	}

	if status, ok := response["status"].(string); !ok || status != "running" {
		t.Errorf("Expected status=running in response, got: %v", response["status"])
	}

	if message, ok := response["message"].(string); !ok || !strings.Contains(message, "auto-backgrounded") {
		t.Errorf("Expected message about auto-backgrounding, got: %v", response["message"])
	}
}

// TestBackgroundBashTool_ExplicitBackgroundSetsMetadata verifies that the
// explicit background=true path attaches the unified metadata signal the TUI
// renderer relies on: background=true, task_id, background_reason=explicit.
func TestBackgroundBashTool_ExplicitBackgroundSetsMetadata(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig:              builtin.DefaultBashConfig(),
		Manager:                 mgr,
		AllowExplicitBackground: true,
	})

	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "sleep 2",
		"timeout_seconds": 10.0,
		"background":      true,
	})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}
	if result.Metadata == nil {
		t.Fatal("expected result metadata to be set for backgrounded command")
	}
	if bg, ok := result.Metadata["background"].(bool); !ok || !bg {
		t.Errorf("expected metadata background=true, got %v", result.Metadata["background"])
	}
	if reason, _ := result.Metadata["background_reason"].(string); reason != "explicit" {
		t.Errorf("expected background_reason=explicit, got %q", reason)
	}
	taskID, _ := result.Metadata["task_id"].(string)
	if taskID == "" {
		t.Error("expected non-empty task_id in metadata")
	} else {
		_ = mgr.CancelByID(context.Background(), taskID, OwnerInfo{Role: "admin"})
	}
}

// TestBackgroundedResultHelper verifies the helper sets all three metadata keys.
func TestBackgroundedResultHelper(t *testing.T) {
	r := backgroundedResult(`{"task_id":"x"}`, "x", backgroundReasonIdle)
	if r.Metadata["background"] != true {
		t.Error("expected background=true")
	}
	if r.Metadata["task_id"] != "x" {
		t.Errorf("expected task_id=x, got %v", r.Metadata["task_id"])
	}
	if r.Metadata["background_reason"] != "idle" {
		t.Errorf("expected background_reason=idle, got %v", r.Metadata["background_reason"])
	}
}

func TestBackgroundBashTool_ExplicitBackgroundImmediate(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
		// This test explicitly validates the agent-set `background=true` behavior.
		AllowExplicitBackground: true,
	})

	// Explicit background mode - should return immediately
	start := time.Now()
	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "sleep 2",
		"timeout_seconds": 10.0,
		"background":      true, // Explicit background
	})
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Should return immediately (within 1 second)
	if duration > time.Second {
		t.Errorf("Explicit background should return immediately, took %v", duration)
	}

	// Should return backgrounded response with task_id
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		t.Fatalf("Failed to parse response as JSON: %v", err)
	}

	if taskID, ok := response["task_id"].(string); !ok || taskID == "" {
		t.Error("Expected task_id in response for explicit background")
	} else {
		// Cancel it
		owner := OwnerInfo{UserID: "test"}
		mgr.Cancel(context.Background(), NewProcessHandle(taskID), owner)
	}
}

func TestBackgroundBashTool_ForegroundFailedCommand(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Command that fails quickly
	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "exit 42",
		"timeout_seconds": 5.0,
	})

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	// Should return error result with exit code
	if !result.IsError {
		t.Error("Expected IsError=true for failed command")
	}

	if !strings.Contains(result.Output, "42") {
		t.Errorf("Expected exit code 42 in output, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "Command failed with no output") {
		t.Errorf("Expected empty-output failure diagnostic, got: %s", result.Output)
	}

	if strings.Contains(result.Output, "completed successfully") {
		t.Errorf("Failed command must not claim success, got: %s", result.Output)
	}
}

func TestBackgroundBashTool_SequentialStreamingCommandsReuseTool(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})
	ctx := testContext()

	tests := []struct {
		command    string
		wantOutput string
		wantError  bool
	}{
		{command: "echo first", wantOutput: "first\n"},
		{command: "pwd", wantOutput: "\n"},
		{command: "printf second", wantOutput: "second"},
		{command: "true", wantOutput: "Command completed successfully (no output)"},
		{command: "exit 42", wantOutput: "Exit code: 42\nCommand failed with no output", wantError: true},
		{command: "echo after-failure", wantOutput: "after-failure\n"},
	}

	for i, tt := range tests {
		var streamed strings.Builder
		result, err := tool.ExecuteStreaming(ctx, map[string]any{
			"command":         tt.command,
			"timeout_seconds": 5.0,
		}, func(chunk, _ string) {
			streamed.WriteString(chunk)
		})
		if err != nil {
			t.Fatalf("command %d (%q): ExecuteStreaming() returned error: %v", i+1, tt.command, err)
		}
		if result.IsError != tt.wantError {
			t.Errorf("command %d (%q): IsError = %v, want %v; output: %q", i+1, tt.command, result.IsError, tt.wantError, result.Output)
		}
		if !strings.Contains(result.Output, tt.wantOutput) {
			t.Errorf("command %d (%q): output %q does not contain %q", i+1, tt.command, result.Output, tt.wantOutput)
		}
		if tt.wantOutput != "Command completed successfully (no output)" &&
			tt.wantOutput != "Exit code: 42\nCommand failed with no output" &&
			!strings.Contains(streamed.String(), strings.TrimSpace(tt.wantOutput)) {
			t.Errorf("command %d (%q): streamed output %q does not contain %q", i+1, tt.command, streamed.String(), strings.TrimSpace(tt.wantOutput))
		}
	}
}

func TestBackgroundBashTool_SessionTransitionsDoNotPoisonLaterCommands(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer func() { _ = mgr.Shutdown(context.Background()) }()

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig:              builtin.DefaultBashConfig(),
		Manager:                 mgr,
		AllowExplicitBackground: true,
	})
	ctx := testContext()

	runSuccess := func(command, want string) {
		t.Helper()
		result, err := tool.ExecuteStreaming(ctx, map[string]any{
			"command":         command,
			"timeout_seconds": 5.0,
		}, nil)
		if err != nil {
			t.Fatalf("%q: ExecuteStreaming() returned error: %v", command, err)
		}
		if result.IsError || !strings.Contains(result.Output, want) {
			t.Fatalf("%q: got IsError=%v output=%q, want successful output containing %q", command, result.IsError, result.Output, want)
		}
	}

	for i := range 32 {
		want := fmt.Sprintf("sequential-%d", i)
		runSuccess("echo "+want, want)
	}

	backgrounded, err := tool.Execute(ctx, map[string]any{
		"command":         "sleep 5",
		"timeout_seconds": 10.0,
		"background":      true,
	})
	if err != nil {
		t.Fatalf("explicit background: Execute() returned error: %v", err)
	}
	taskID, _ := backgrounded.Metadata["task_id"].(string)
	if taskID == "" {
		t.Fatalf("explicit background: missing task_id metadata: %#v", backgrounded.Metadata)
	}
	if err := mgr.CancelByID(context.Background(), taskID, OwnerInfo{Role: "admin"}); err != nil {
		t.Fatalf("explicit background: cleanup failed: %v", err)
	}
	runSuccess("echo after-explicit-background", "after-explicit-background")

	idleBackgrounded, err := tool.ExecuteStreaming(ctx, map[string]any{
		"command":         "sleep 5",
		"timeout_seconds": 0.05,
	}, nil)
	if err != nil {
		t.Fatalf("idle background: ExecuteStreaming() returned error: %v", err)
	}
	idleTaskID, _ := idleBackgrounded.Metadata["task_id"].(string)
	if idleTaskID == "" {
		t.Fatalf("idle background: missing task_id metadata: %#v", idleBackgrounded.Metadata)
	}
	if err := mgr.CancelByID(context.Background(), idleTaskID, OwnerInfo{Role: "admin"}); err != nil {
		t.Fatalf("idle background: cleanup failed: %v", err)
	}
	runSuccess("echo after-idle-background", "after-idle-background")

	cancelHandle, err := mgr.Spawn(ctx, SpawnRequest{
		Command: "sleep 5",
		Owner:   OwnerInfo{UserID: "test-user"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("cancel transition: Spawn() returned error: %v", err)
	}
	if err := mgr.Cancel(context.Background(), cancelHandle, OwnerInfo{Role: "admin"}); err != nil {
		t.Fatalf("cancel transition: Cancel() returned error: %v", err)
	}
	runSuccess("echo after-cancel", "after-cancel")

	timeoutHandle, err := mgr.Spawn(ctx, SpawnRequest{
		Command: "sleep 5",
		Owner:   OwnerInfo{UserID: "test-user"},
		Timeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("process timeout transition: Spawn() returned error: %v", err)
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), time.Second)
	defer timeoutCancel()
	if _, err := mgr.Wait(timeoutCtx, timeoutHandle); err == nil {
		t.Fatal("process timeout transition: Wait() unexpectedly succeeded")
	}
	runSuccess("echo after-process-timeout", "after-process-timeout")
}

func TestBackgroundBashTool_ForegroundWithEnvAndCwd(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Command with environment and cwd
	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "echo $TEST_VAR && pwd",
		"timeout_seconds": 5.0,
		"cwd":             "/tmp",
		"env": map[string]any{
			"TEST_VAR": "hello_from_test",
		},
	})

	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.Output)
	}

	if !strings.Contains(result.Output, "hello_from_test") {
		t.Errorf("Expected output to contain env var value, got: %s", result.Output)
	}

	if !strings.Contains(result.Output, "/tmp") {
		t.Errorf("Expected output to contain /tmp (cwd), got: %s", result.Output)
	}
}

func TestBackgroundBashTool_ParametersRequireTimeout(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	params := tool.Parameters().(map[string]any)

	// Check that timeout_seconds is in required fields
	required, ok := params["required"].([]string)
	if !ok {
		t.Fatal("Parameters should have 'required' field")
	}

	hasTimeout := slices.Contains(required, "timeout_seconds")

	if !hasTimeout {
		t.Error("timeout_seconds should be in required parameters")
	}

	// Check that timeout_seconds has minimum value
	properties, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters should have 'properties' field")
	}

	timeoutDef, ok := properties["timeout_seconds"].(map[string]any)
	if !ok {
		t.Fatal("Properties should have 'timeout_seconds' definition")
	}

	if minimum, ok := timeoutDef["minimum"].(int); !ok || minimum < 1 {
		t.Error("timeout_seconds should have minimum value >= 1")
	}
}

// TestBackgroundBashTool_TriggerBackground tests the Ctrl+B mid-execution backgrounding
func TestBackgroundBashTool_TriggerBackground(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Initially, no command is executing
	if tool.IsExecuting() {
		t.Error("Expected IsExecuting() to be false initially")
	}

	if exec := tool.GetCurrentExecution(); exec != nil {
		t.Error("Expected GetCurrentExecution() to return nil initially")
	}

	// TriggerBackground should fail when nothing is executing
	if _, err := tool.TriggerBackground(); err == nil {
		t.Error("Expected TriggerBackground() to return error when nothing is executing")
	}

	// Start a long-running command in a goroutine
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := tool.Execute(testContext(), map[string]any{
			"command":         "sleep 10",
			"timeout_seconds": 30.0, // Long timeout so we can trigger background
		})
		if err != nil {
			errCh <- err
		} else {
			resultCh <- result.Output
		}
	}()

	// Wait for command to start executing
	time.Sleep(100 * time.Millisecond)

	// Now a command should be executing
	if !tool.IsExecuting() {
		t.Error("Expected IsExecuting() to be true while command runs")
	}

	exec := tool.GetCurrentExecution()
	if exec == nil {
		t.Fatal("Expected GetCurrentExecution() to return execution info")
	}

	if exec.Command != "sleep 10" {
		t.Errorf("Expected Command='sleep 10', got: %s", exec.Command)
	}

	if exec.Handle.IsZero() {
		t.Error("Expected non-zero Handle")
	}

	// Trigger backgrounding (simulating Ctrl+B)
	handle, err := tool.TriggerBackground()
	if err != nil {
		t.Fatalf("TriggerBackground() returned error: %v", err)
	}

	if handle.IsZero() {
		t.Error("Expected non-zero handle from TriggerBackground")
	}

	// Wait for the result
	select {
	case output := <-resultCh:
		// Should have backgrounded response
		var response map[string]any
		if err := json.Unmarshal([]byte(output), &response); err != nil {
			t.Fatalf("Failed to parse response as JSON: %v\nOutput: %s", err, output)
		}

		if backgrounded, ok := response["backgrounded"].(bool); !ok || !backgrounded {
			t.Error("Expected backgrounded=true in response")
		}

		if userTriggered, ok := response["user_triggered"].(bool); !ok || !userTriggered {
			t.Error("Expected user_triggered=true in response (Ctrl+B)")
		}

		if msg, ok := response["message"].(string); !ok || !strings.Contains(msg, "Ctrl+B") {
			t.Errorf("Expected message to mention Ctrl+B, got: %v", response["message"])
		}

	case err := <-errCh:
		t.Fatalf("Execute() returned error: %v", err)

	case <-time.After(5 * time.Second):
		t.Fatal("Execute() did not return after TriggerBackground()")
	}

	// After execution completes, should no longer be executing
	time.Sleep(50 * time.Millisecond)
	if tool.IsExecuting() {
		t.Error("Expected IsExecuting() to be false after execution completes")
	}

	// Cleanup
	owner := OwnerInfo{UserID: "test-user"}
	mgr.Cancel(context.Background(), handle, owner)
}

// TestBackgroundBashTool_TriggerBackgroundQuickCommand tests that TriggerBackground works
// even if called just before command completes (race condition handling)
func TestBackgroundBashTool_TriggerBackgroundQuickCommand(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	// Start a command that completes quickly
	resultCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := tool.Execute(testContext(), map[string]any{
			"command":         "printf 'done'; sleep 0.2",
			"timeout_seconds": 10.0,
		})
		if err != nil {
			errCh <- err
		} else {
			resultCh <- result.Output
		}
	}()

	// Wait a bit for command to start
	time.Sleep(50 * time.Millisecond)

	// Try to trigger background - might succeed or command might complete first
	tool.TriggerBackground() // Ignore error - command might have already completed

	// Wait for result
	select {
	case output := <-resultCh:
		// Either got backgrounded result or normal completion - both are valid
		// Just verify we got some output
		if output == "" {
			t.Error("Expected non-empty output")
		}

	case err := <-errCh:
		t.Fatalf("Execute() returned error: %v", err)

	case <-time.After(5 * time.Second):
		t.Fatal("Execute() did not return in time")
	}
}

// ---------------------------------------------------------------------------
// Tail + tmp-file output tests
// ---------------------------------------------------------------------------

// TestReadBackgroundCommand_TailDefault verifies that when output exceeds the
// default 100-line tail, only the last 100 lines are returned in the JSON and
// the full output is written to a temp file.
func TestReadBackgroundCommand_TailDefault(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig:              builtin.DefaultBashConfig(),
		Manager:                 mgr,
		AllowExplicitBackground: true,
	})
	ctx := testContext()

	result, err := tool.Execute(ctx, map[string]any{
		"command":         "for i in $(seq 1 200); do echo \"line $i\"; done",
		"timeout_seconds": 30.0,
		"background":      true,
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}
	var spawnResp map[string]any
	json.Unmarshal([]byte(result.Output), &spawnResp)
	taskID, _ := spawnResp["task_id"].(string)

	// Wait for the command to finish.
	time.Sleep(2 * time.Second)

	readTool := NewReadBackgroundCommandTool(mgr)
	// Use background context so extractSubject falls back to role=admin,
	// which has permission to read any process regardless of owner.
	readResult, err := readTool.Execute(context.Background(), map[string]any{
		"task_id": taskID,
		// no action  -> defaults to "output"
		// no max_lines -> defaults to 100-line tail
	})
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(readResult.Output), &out); err != nil {
		t.Fatalf("parse error: %v\nraw: %s", err, readResult.Output)
	}

	// Exactly 100 lines in the response.
	lines, _ := out["output"].([]any)
	if len(lines) != 100 {
		t.Errorf("expected 100 tail lines, got %d", len(lines))
	}

	// The tail should start at line 101 (lines 101-200).
	if len(lines) > 0 {
		first, _ := lines[0].(map[string]any)
		content, _ := first["content"].(string)
		if content != "line 101" {
			t.Errorf("first tail line should be 'line 101', got %q", content)
		}
		last, _ := lines[len(lines)-1].(map[string]any)
		lastContent, _ := last["content"].(string)
		if lastContent != "line 200" {
			t.Errorf("last tail line should be 'line 200', got %q", lastContent)
		}
	}

	// metadata.output_truncated and metadata.output_file must be set.
	meta, _ := out["metadata"].(map[string]any)
	if meta == nil {
		t.Fatal("metadata missing")
	}
	if truncated, _ := meta["output_truncated"].(bool); !truncated {
		t.Error("expected metadata.output_truncated=true")
	}
	outputFile, _ := meta["output_file"].(string)
	if outputFile == "" {
		t.Error("expected metadata.output_file to be set")
	} else {
		t.Logf("output_file: %s", outputFile)
		// The file must exist and contain all 200 lines.
		data, err := os.ReadFile(outputFile)
		if err != nil {
			t.Fatalf("cannot read output_file: %v", err)
		}
		fileLines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		if len(fileLines) != 200 {
			t.Errorf("output_file should have 200 lines, got %d", len(fileLines))
		}
		if fileLines[0] != "line 1" {
			t.Errorf("first line of file should be 'line 1', got %q", fileLines[0])
		}
		if fileLines[199] != "line 200" {
			t.Errorf("last line of file should be 'line 200', got %q", fileLines[199])
		}
	}
}

// TestReadBackgroundCommand_SmallOutput_NoTmpFile verifies that when output fits
// within the tail window, no tmp file is created and output_truncated is false.
func TestReadBackgroundCommand_SmallOutput_NoTmpFile(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig:              builtin.DefaultBashConfig(),
		Manager:                 mgr,
		AllowExplicitBackground: true,
	})
	ctx := testContext()

	result, err := tool.Execute(ctx, map[string]any{
		"command":         "for i in $(seq 1 10); do echo \"line $i\"; done",
		"timeout_seconds": 30.0,
		"background":      true,
	})
	if err != nil {
		t.Fatalf("spawn error: %v", err)
	}
	var spawnResp map[string]any
	json.Unmarshal([]byte(result.Output), &spawnResp)
	taskID, _ := spawnResp["task_id"].(string)

	time.Sleep(1 * time.Second)

	readTool := NewReadBackgroundCommandTool(mgr)
	readResult, err := readTool.Execute(context.Background(), map[string]any{
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	var out map[string]any
	json.Unmarshal([]byte(readResult.Output), &out)

	lines, _ := out["output"].([]any)
	if len(lines) != 10 {
		t.Errorf("expected 10 lines, got %d", len(lines))
	}

	meta, _ := out["metadata"].(map[string]any)
	if truncated, _ := meta["output_truncated"].(bool); truncated {
		t.Error("output_truncated should be false for small output")
	}
	if outputFile, _ := meta["output_file"].(string); outputFile != "" {
		t.Errorf("output_file should be empty for small output, got %q", outputFile)
	}
}
