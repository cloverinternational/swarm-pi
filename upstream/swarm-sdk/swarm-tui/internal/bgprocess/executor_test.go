package bgprocess

import (
	"context"
	"testing"
	"time"
)

func TestProcessExecutor_BasicExecution(t *testing.T) {
	tests := []struct {
		name    string
		request SpawnRequest
		wantErr bool
	}{
		{
			name: "simple echo command",
			request: SpawnRequest{
				Command: "echo 'hello world'",
				Owner: OwnerInfo{
					UserID: "test-user",
				},
			},
			wantErr: false,
		},
		{
			name: "command with working directory",
			request: SpawnRequest{
				Command: "pwd",
				WorkDir: "/tmp",
				Owner: OwnerInfo{
					UserID: "test-user",
				},
			},
			wantErr: false,
		},
		{
			name: "command with environment",
			request: SpawnRequest{
				Command: "echo $TEST_VAR",
				Env: map[string]string{
					"TEST_VAR": "test_value",
				},
				Owner: OwnerInfo{
					UserID: "test-user",
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := NewProcessExecutor(ExecutorConfig{
				Request: tt.request,
			})
			if err != nil {
				t.Fatalf("NewProcessExecutor() error = %v", err)
			}

			ctx := context.Background()
			if err := executor.Start(ctx); (err != nil) != tt.wantErr {
				t.Errorf("Start() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Wait for completion
			result, err := executor.Wait(ctx)
			if err != nil {
				t.Errorf("Wait() error = %v", err)
				return
			}

			if result.ExitCode != 0 {
				t.Errorf("Expected exit code 0, got %d", result.ExitCode)
			}

			// Verify state is terminal
			state := executor.State()
			if !state.IsTerminal() {
				t.Errorf("Expected terminal state, got %v", state)
			}
		})
	}
}

func TestProcessExecutor_FailingCommand(t *testing.T) {
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: SpawnRequest{
			Command: "exit 42",
			Owner: OwnerInfo{
				UserID: "test-user",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	ctx := context.Background()
	if err := executor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	result, err := executor.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	if result.ExitCode != 42 {
		t.Errorf("Expected exit code 42, got %d", result.ExitCode)
	}

	if executor.State() != StateFailed {
		t.Errorf("Expected StateFailed, got %v", executor.State())
	}
}

func TestProcessExecutor_Timeout(t *testing.T) {
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: SpawnRequest{
			Command: "sleep 10",
			Timeout: 100 * time.Millisecond,
			Owner: OwnerInfo{
				UserID: "test-user",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	ctx := context.Background()
	if err := executor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	result, err := executor.Wait(ctx)
	if err == nil {
		t.Error("Expected timeout error")
	}

	if result != nil && result.ExitCode == 0 {
		t.Error("Expected non-zero exit code for timeout")
	}
}

func TestProcessExecutor_Cancel(t *testing.T) {
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: SpawnRequest{
			Command: "sleep 30",
			Owner: OwnerInfo{
				UserID: "test-user",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	ctx := context.Background()
	if err := executor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Cancel the process
	if err := executor.Cancel(); err != nil {
		t.Errorf("Cancel() error = %v", err)
	}

	// Verify state
	state := executor.State()
	if state != StateCancelled && state != StateFailed {
		t.Errorf("Expected StateCancelled or StateFailed, got %v", state)
	}
}

func TestProcessExecutor_PauseResume(t *testing.T) {
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: SpawnRequest{
			Command: "sleep 2",
			Owner: OwnerInfo{
				UserID: "test-user",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	ctx := context.Background()
	if err := executor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Pause
	if err := executor.Pause(); err != nil {
		t.Errorf("Pause() error = %v", err)
	}

	if executor.State() != StatePaused {
		t.Errorf("Expected StatePaused, got %v", executor.State())
	}

	// Resume
	if err := executor.Resume(); err != nil {
		t.Errorf("Resume() error = %v", err)
	}

	if executor.State() != StateRunning {
		t.Errorf("Expected StateRunning after Resume, got %v", executor.State())
	}

	// Cleanup
	executor.Cancel()
}

func TestProcessExecutor_OutputCapture(t *testing.T) {
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: SpawnRequest{
			Command: "echo 'line1' && echo 'line2' && echo 'error' >&2",
			Owner: OwnerInfo{
				UserID: "test-user",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	ctx := context.Background()
	if err := executor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Wait for completion
	_, err = executor.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	// Check output
	output := executor.Output()
	lines, err := output.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines() error = %v", err)
	}

	if len(lines) < 3 {
		t.Errorf("Expected at least 3 lines, got %d", len(lines))
	}

	// Check stdout filter
	stdoutLines, err := output.Lines(LineQueryOpts{Stream: "stdout"})
	if err != nil {
		t.Fatalf("Lines(stdout) error = %v", err)
	}

	if len(stdoutLines) < 2 {
		t.Errorf("Expected at least 2 stdout lines, got %d", len(stdoutLines))
	}

	// Check stderr filter
	stderrLines, err := output.Lines(LineQueryOpts{Stream: "stderr"})
	if err != nil {
		t.Fatalf("Lines(stderr) error = %v", err)
	}

	if len(stderrLines) < 1 {
		t.Errorf("Expected at least 1 stderr line, got %d", len(stderrLines))
	}
}

func TestProcessExecutor_Info(t *testing.T) {
	request := SpawnRequest{
		Command: "echo 'test'",
		WorkDir: "/tmp",
		Env: map[string]string{
			"TEST": "value",
		},
		Owner: OwnerInfo{
			UserID:  "user-123",
			AgentID: "agent-456",
		},
		Tags: map[string]string{
			"type": "test",
		},
	}

	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: request,
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	info := executor.Info()

	if info.Command != request.Command {
		t.Errorf("Info.Command = %v, want %v", info.Command, request.Command)
	}

	if info.WorkDir != request.WorkDir {
		t.Errorf("Info.WorkDir = %v, want %v", info.WorkDir, request.WorkDir)
	}

	if info.Owner.UserID != request.Owner.UserID {
		t.Errorf("Info.Owner.UserID = %v, want %v", info.Owner.UserID, request.Owner.UserID)
	}

	if info.Tags["type"] != "test" {
		t.Errorf("Info.Tags[type] = %v, want test", info.Tags["type"])
	}
}

func TestProcessExecutor_StateTransitions(t *testing.T) {
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: SpawnRequest{
			Command: "sleep 1",
			Owner: OwnerInfo{
				UserID: "test-user",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewProcessExecutor() error = %v", err)
	}

	// Initial state
	if executor.State() != StateCreated {
		t.Errorf("Initial state = %v, want StateCreated", executor.State())
	}

	// Start
	ctx := context.Background()
	if err := executor.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if executor.State() != StateRunning {
		t.Errorf("After Start state = %v, want StateRunning", executor.State())
	}

	// Wait for completion
	executor.Wait(ctx)

	state := executor.State()
	if !state.IsTerminal() {
		t.Errorf("After Wait state = %v, expected terminal state", state)
	}
}
