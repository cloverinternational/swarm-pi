package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
)

// GetBackgroundProcessOutput must return a live snapshot (state + tail output)
// for a known process, and (nil,false) for an unknown task_id.
func TestGetBackgroundProcessOutput(t *testing.T) {
	mgr := bgprocess.NewManager(bgprocess.DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	sdk := &SDKIntegration{bgProcessManager: mgr}

	// Unknown id → not found.
	if _, ok := sdk.GetBackgroundProcessOutput("does-not-exist", 50); ok {
		t.Fatal("expected ok=false for unknown task_id")
	}

	// Spawn a short-lived process that prints a couple of lines.
	handle, err := mgr.Spawn(context.Background(), bgprocess.SpawnRequest{
		Command: "printf 'alpha\\nbravo\\n'; sleep 0.3",
		Owner:   bgprocess.OwnerInfo{AgentID: "test"},
		Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	taskID := handle.ID()

	// Poll briefly for output to appear.
	var view *BackgroundProcessLiveView
	var ok bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, ok = sdk.GetBackgroundProcessOutput(taskID, 100)
		if ok && len(view.Lines) >= 2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !ok || view == nil {
		t.Fatal("expected a live view for the spawned process")
	}
	if view.TaskID != taskID {
		t.Errorf("TaskID = %q, want %q", view.TaskID, taskID)
	}

	var joined strings.Builder
	for _, ln := range view.Lines {
		joined.WriteString(ln.Content)
		joined.WriteString("\n")
	}
	got := joined.String()
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "bravo") {
		t.Errorf("expected alpha+bravo in live output, got:\n%s", got)
	}

	// Wait for completion, then the view should be terminal (not running).
	_, _ = mgr.Wait(context.Background(), handle)
	// Give the state a moment to settle.
	time.Sleep(50 * time.Millisecond)
	view, ok = sdk.GetBackgroundProcessOutput(taskID, 100)
	if !ok {
		t.Fatal("expected view after completion")
	}
	if view.Running {
		t.Errorf("expected Running=false after completion, state=%q", view.State)
	}
}
