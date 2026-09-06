package bgprocess

import (
	"context"
	"testing"
	"time"
)

func TestManager_Spawn(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	tests := []struct {
		name    string
		request SpawnRequest
		wantErr bool
	}{
		{
			name: "valid spawn",
			request: SpawnRequest{
				Command: "echo 'test'",
				Owner: OwnerInfo{
					UserID: "user-1",
				},
			},
			wantErr: false,
		},
		{
			name: "spawn with tags",
			request: SpawnRequest{
				Command: "pwd",
				Owner: OwnerInfo{
					UserID: "user-1",
				},
				Tags: map[string]string{
					"type":    "test",
					"project": "bgprocess",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handle, err := mgr.Spawn(context.Background(), tt.request)
			if (err != nil) != tt.wantErr {
				t.Errorf("Spawn() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				if handle.IsZero() {
					t.Error("Expected non-zero handle")
				}

				// Verify we can get the process
				exec, err := mgr.Get(context.Background(), handle)
				if err != nil {
					t.Errorf("Get() error = %v", err)
				}
				if exec == nil {
					t.Error("Expected non-nil executor")
				}
			}
		})
	}
}

func TestManager_GetInfo(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	request := SpawnRequest{
		Command: "sleep 0.1",
		Owner: OwnerInfo{
			UserID:  "user-1",
			AgentID: "agent-1",
		},
		Tags: map[string]string{
			"test": "value",
		},
	}

	handle, err := mgr.Spawn(context.Background(), request)
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	info, err := mgr.GetInfo(context.Background(), handle)
	if err != nil {
		t.Fatalf("GetInfo() error = %v", err)
	}

	if info.Command != request.Command {
		t.Errorf("Info.Command = %v, want %v", info.Command, request.Command)
	}

	if info.Owner.UserID != request.Owner.UserID {
		t.Errorf("Info.Owner.UserID = %v, want %v", info.Owner.UserID, request.Owner.UserID)
	}

	if info.Tags["test"] != "value" {
		t.Errorf("Info.Tags[test] = %v, want value", info.Tags["test"])
	}
}

func TestManager_List(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	// Spawn multiple processes
	owner1 := OwnerInfo{UserID: "user-1", ConversationID: "conv-1"}
	owner2 := OwnerInfo{UserID: "user-2", ConversationID: "conv-2"}

	handle1, _ := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "sleep 1",
		Owner:   owner1,
		Tags:    map[string]string{"env": "dev"},
	})

	handle2, _ := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "sleep 1",
		Owner:   owner1,
		Tags:    map[string]string{"env": "prod"},
	})

	handle3, _ := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "sleep 1",
		Owner:   owner2,
		Tags:    map[string]string{"env": "dev"},
	})

	// Test listing all processes for user-1
	processes, err := mgr.List(context.Background(), owner1)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	// Should see processes from same user and conversation
	if len(processes) < 2 {
		t.Errorf("Expected at least 2 processes for user-1, got %d", len(processes))
	}

	// Test with tag filter
	devProcesses, err := mgr.List(context.Background(), owner1, TagFilter{
		Tags: map[string]string{"env": "dev"},
	})
	if err != nil {
		t.Fatalf("List() with filter error = %v", err)
	}

	if len(devProcesses) == 0 {
		t.Error("Expected at least 1 dev process")
	}

	// Test with state filter
	runningProcesses, err := mgr.List(context.Background(), owner1, StateFilter{
		States: []ProcessState{StateRunning},
	})
	if err != nil {
		t.Fatalf("List() with state filter error = %v", err)
	}

	if len(runningProcesses) == 0 {
		t.Error("Expected at least 1 running process")
	}

	// Cleanup
	mgr.Cancel(context.Background(), handle1, owner1)
	mgr.Cancel(context.Background(), handle2, owner1)
	mgr.Cancel(context.Background(), handle3, owner2)
}

func TestManager_Cancel(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	owner := OwnerInfo{UserID: "user-1"}

	handle, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "sleep 30",
		Owner:   owner,
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the process
	if err := mgr.Cancel(context.Background(), handle, owner); err != nil {
		t.Errorf("Cancel() error = %v", err)
	}

	// Verify state
	info, err := mgr.GetInfo(context.Background(), handle)
	if err != nil {
		t.Fatalf("GetInfo() error = %v", err)
	}

	if !info.State.IsTerminal() {
		t.Errorf("Expected terminal state after cancel, got %v", info.State)
	}
}

func TestManager_Wait(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	owner := OwnerInfo{UserID: "user-1"}

	handle, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "echo 'test' && sleep 0.1",
		Owner:   owner,
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	result, err := mgr.Wait(context.Background(), handle)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}
}

func TestManager_GetOutput(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	owner := OwnerInfo{UserID: "user-1"}

	handle, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "echo 'line1' && echo 'line2'",
		Owner:   owner,
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	// Wait for completion
	mgr.Wait(context.Background(), handle)

	// Get output
	lines, err := mgr.GetOutput(context.Background(), handle, owner, LineQueryOpts{})
	if err != nil {
		t.Fatalf("GetOutput() error = %v", err)
	}

	if len(lines) < 2 {
		t.Errorf("Expected at least 2 lines, got %d", len(lines))
	}

	// Test with max lines limit
	limitedLines, err := mgr.GetOutput(context.Background(), handle, owner, LineQueryOpts{
		MaxLines: 1,
	})
	if err != nil {
		t.Fatalf("GetOutput() with limit error = %v", err)
	}

	if len(limitedLines) > 1 {
		t.Errorf("Expected at most 1 line, got %d", len(limitedLines))
	}
}

func TestManager_Visibility(t *testing.T) {
	policy := NewSimpleVisibilityPolicy()
	mgr := NewManager(ManagerConfig{
		Visibility: policy,
	})
	defer mgr.Shutdown(context.Background())

	// User 1 spawns a process
	user1 := OwnerInfo{UserID: "user-1", ConversationID: "conv-1"}
	user2 := OwnerInfo{UserID: "user-2", ConversationID: "conv-2"}

	handle, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "sleep 1",
		Owner:   user1,
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	// User 1 should be able to access their own process
	_, err = mgr.GetOutput(context.Background(), handle, user1, LineQueryOpts{})
	if err != nil {
		t.Errorf("User 1 should access their own process: %v", err)
	}

	// User 2 should NOT be able to access user 1's process
	_, err = mgr.GetOutput(context.Background(), handle, user2, LineQueryOpts{})
	if !IsPermissionDenied(err) {
		t.Errorf("User 2 should be denied access, got error: %v", err)
	}

	// User 2 should NOT be able to cancel user 1's process
	err = mgr.Cancel(context.Background(), handle, user2)
	if !IsPermissionDenied(err) {
		t.Errorf("User 2 should be denied cancel, got error: %v", err)
	}

	// Cleanup
	mgr.Cancel(context.Background(), handle, user1)
}

func TestManager_Cleanup(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Visibility:      NewSimpleVisibilityPolicy(),
		CleanupInterval: 100 * time.Millisecond,
		MaxProcessAge:   200 * time.Millisecond,
	})
	defer mgr.Shutdown(context.Background())

	owner := OwnerInfo{UserID: "user-1"}

	// Spawn a quick process
	handle, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "echo 'test'",
		Owner:   owner,
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	// Wait for completion
	mgr.Wait(context.Background(), handle)

	// Verify it exists
	_, err = mgr.GetInfo(context.Background(), handle)
	if err != nil {
		t.Fatalf("Process should exist: %v", err)
	}

	// Wait for cleanup
	time.Sleep(350 * time.Millisecond)

	// Manual cleanup
	removed, err := mgr.Cleanup(100 * time.Millisecond)
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	if removed == 0 {
		t.Log("No processes removed by manual cleanup (may have been cleaned by automatic cleanup)")
	}
}

func TestManager_Shutdown(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())

	owner := OwnerInfo{UserID: "user-1"}

	// Spawn multiple processes
	handles := make([]ProcessHandle, 3)
	for i := range 3 {
		h, err := mgr.Spawn(context.Background(), SpawnRequest{
			Command: "sleep 10",
			Owner:   owner,
		})
		if err != nil {
			t.Fatalf("Spawn() error = %v", err)
		}
		handles[i] = h
	}

	// Give them time to start
	time.Sleep(100 * time.Millisecond)

	// Shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := mgr.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown() error = %v", err)
	}

	// Verify all processes are in terminal state
	for _, handle := range handles {
		info, err := mgr.GetInfo(context.Background(), handle)
		if err != nil {
			t.Errorf("GetInfo() after shutdown error = %v", err)
			continue
		}

		if !info.State.IsTerminal() {
			t.Errorf("Process %s not in terminal state after shutdown: %v", handle.ID(), info.State)
		}
	}

	// Try to spawn after shutdown
	_, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "echo 'test'",
		Owner:   owner,
	})
	if err != ErrManagerShutdown {
		t.Errorf("Expected ErrManagerShutdown, got %v", err)
	}
}

func TestManager_Stats(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	owner := OwnerInfo{UserID: "user-1"}

	// Spawn successful process
	handle1, _ := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "echo 'test'",
		Owner:   owner,
	})

	// Spawn failing process
	handle2, _ := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "exit 1",
		Owner:   owner,
	})

	// Wait for completion
	mgr.Wait(context.Background(), handle1)
	mgr.Wait(context.Background(), handle2)

	stats := mgr.Stats()

	if stats.TotalSpawned < 2 {
		t.Errorf("Expected TotalSpawned >= 2, got %d", stats.TotalSpawned)
	}

	if stats.CompletedCount < 1 {
		t.Errorf("Expected CompletedCount >= 1, got %d", stats.CompletedCount)
	}

	if stats.FailedCount < 1 {
		t.Errorf("Expected FailedCount >= 1, got %d", stats.FailedCount)
	}
}

func TestManager_FormatOutput(t *testing.T) {
	mgr := NewManager(DefaultManagerConfig())
	defer mgr.Shutdown(context.Background())

	owner := OwnerInfo{UserID: "user-1"}

	handle, err := mgr.Spawn(context.Background(), SpawnRequest{
		Command: "echo 'test output'",
		Owner:   owner,
		WorkDir: "/tmp",
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}

	// Wait for completion
	mgr.Wait(context.Background(), handle)

	// Format output
	output, err := mgr.FormatOutput(context.Background(), handle, owner, LineQueryOpts{})
	if err != nil {
		t.Fatalf("FormatOutput() error = %v", err)
	}

	if output.TaskID != handle.ID() {
		t.Errorf("TaskID = %v, want %v", output.TaskID, handle.ID())
	}

	if output.Command != "echo 'test output'" {
		t.Errorf("Command = %v, want 'echo 'test output''", output.Command)
	}

	if output.Metadata.WorkDir != "/tmp" {
		t.Errorf("WorkDir = %v, want /tmp", output.Metadata.WorkDir)
	}

	if len(output.Output) == 0 {
		t.Error("Expected at least one output line")
	}
}
