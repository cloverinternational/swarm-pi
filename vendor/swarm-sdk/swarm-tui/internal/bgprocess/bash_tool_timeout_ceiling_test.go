package bgprocess

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// TestBackgroundBashTool_AutoBackgroundedProcessHasNoWallClockKillDeadline
// proves issues #282/#256: a command auto-backgrounded via a short
// timeout_seconds must NOT also be silently SIGKILLed at a fixed process-level
// ceiling (previously max(timeout_seconds*2, 5*time.Minute), which in
// practice was almost always the undocumented "5 minute" floor since real
// callers request small timeout_seconds). The underlying process's own
// context must have no deadline at all — it is only ever ended by natural
// completion or an explicit cancel.
func TestBackgroundBashTool_AutoBackgroundedProcessHasNoWallClockKillDeadline(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())
	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig: builtin.DefaultBashConfig(),
		Manager:    mgr,
	})

	result, err := tool.Execute(testContext(), map[string]any{
		"command":         "sleep 5",
		"timeout_seconds": 0.5, // short idle threshold -> triggers auto-background quickly
	})
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		t.Fatalf("Failed to parse response as JSON: %v\nOutput: %s", err, result.Output)
	}
	taskID, ok := response["task_id"].(string)
	if !ok || taskID == "" {
		t.Fatalf("expected task_id in response, got: %#v", response)
	}
	defer mgr.CancelByID(context.Background(), taskID, OwnerInfo{Role: "admin"})

	exec, err := mgr.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	impl, ok := exec.(*ProcessExecutorImpl)
	if !ok {
		t.Fatalf("expected *ProcessExecutorImpl, got %T", exec)
	}
	if _, hasDeadline := impl.ctx.Deadline(); hasDeadline {
		t.Errorf("auto-backgrounded process context has a wall-clock deadline; " +
			"it must run until natural completion or explicit cancel (issues #282, #256)")
	}
}

// TestBackgroundBashTool_ExplicitBackgroundNoTimeoutHasNoDeadline proves the
// same contract for the background=true entry path (executeBackground):
// when the caller does not supply timeout_seconds, the process must not
// silently inherit the tool's 5-minute default as a hard kill deadline.
func TestBackgroundBashTool_ExplicitBackgroundNoTimeoutHasNoDeadline(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())
	tool := NewBackgroundBashTool(BackgroundBashToolConfig{
		BashConfig:              builtin.DefaultBashConfig(),
		Manager:                 mgr,
		DefaultTimeout:          5 * time.Minute,
		AllowExplicitBackground: true,
	})

	result, err := tool.ExecuteStreaming(testContext(), map[string]any{
		"command":    "sleep 2",
		"background": true,
		// deliberately no timeout_seconds
	}, nil)
	if err != nil {
		t.Fatalf("ExecuteStreaming() returned error: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal([]byte(result.Output), &response); err != nil {
		t.Fatalf("Failed to parse response as JSON: %v\nOutput: %s", err, result.Output)
	}
	taskID, ok := response["task_id"].(string)
	if !ok || taskID == "" {
		t.Fatalf("expected task_id in response, got: %#v", response)
	}
	defer mgr.CancelByID(context.Background(), taskID, OwnerInfo{Role: "admin"})

	exec, err := mgr.GetByID(context.Background(), taskID)
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	impl, ok := exec.(*ProcessExecutorImpl)
	if !ok {
		t.Fatalf("expected *ProcessExecutorImpl, got %T", exec)
	}
	if _, hasDeadline := impl.ctx.Deadline(); hasDeadline {
		t.Errorf("explicitly backgrounded process with no timeout_seconds has a wall-clock " +
			"deadline; it must not silently inherit the tool's default timeout as a kill cap (issues #282, #256)")
	}
}
