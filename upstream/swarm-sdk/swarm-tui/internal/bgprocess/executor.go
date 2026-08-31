package bgprocess

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"time"
)

// ProcessExecutorImpl implements ProcessExecutor.
type ProcessExecutorImpl struct {
	handle  ProcessHandle
	request SpawnRequest

	state       ProcessState
	stateMu     sync.RWMutex
	stateChange chan ProcessState

	cmd         *exec.Cmd
	output      OutputBuffer
	startedAt   time.Time
	completedAt *time.Time
	exitCode    *int
	execErr     error

	ctx    context.Context
	cancel context.CancelFunc

	doneCh   chan struct{}
	doneOnce sync.Once

	// Temp files for output capture (avoids buffer overflow issues)
	stdoutFile *os.File
	stderrFile *os.File

	// Observability hooks
	onStateChange func(from, to ProcessState)
}

// ExecutorConfig contains configuration for creating an executor.
type ExecutorConfig struct {
	Request       SpawnRequest
	OnStateChange func(from, to ProcessState)
}

// NewProcessExecutor creates a new process executor.
func NewProcessExecutor(config ExecutorConfig) (*ProcessExecutorImpl, error) {
	if err := ValidateSpawnRequest(config.Request); err != nil {
		return nil, err
	}

	// Generate handle
	handle := NewProcessHandle(generateProcessID())

	// Create output buffer
	outputConfig := config.Request.OutputConfig
	if outputConfig.Type == "" {
		outputConfig = DefaultOutputBufferConfig()
	}

	output, err := NewOutputBuffer(outputConfig)
	if err != nil {
		return nil, WrapSpawnError(err, "failed to create output buffer")
	}

	// Create context with timeout if specified
	ctx := context.Background()
	var cancel context.CancelFunc
	if config.Request.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, config.Request.Timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	return &ProcessExecutorImpl{
		handle:        handle,
		request:       config.Request,
		state:         StateCreated,
		stateChange:   make(chan ProcessState, 10),
		output:        output,
		ctx:           ctx,
		cancel:        cancel,
		doneCh:        make(chan struct{}),
		onStateChange: config.OnStateChange,
	}, nil
}

// Handle returns the process handle.
func (e *ProcessExecutorImpl) Handle() ProcessHandle {
	return e.handle
}

// State returns the current process state.
func (e *ProcessExecutorImpl) State() ProcessState {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()
	return e.state
}

// Start begins execution of the command.
// Output is captured to temp files to avoid buffer overflow issues with large outputs.
func (e *ProcessExecutorImpl) Start(ctx context.Context) error {
	if err := e.transitionState(StateCreated, StateRunning); err != nil {
		return err
	}

	// Build command
	shell := detectShell()

	// Use platform-appropriate buffering control to force line buffering.
	// Falls back gracefully on macOS where stdbuf is not available.
	e.cmd = buildBashCommand(e.ctx, shell, e.request.Command)
	// WaitDelay ensures pipes are closed after process exit, preventing hangs
	// when child processes inherit stdout/stderr file descriptors (Go issue #21922)
	e.cmd.WaitDelay = 2 * time.Second

	// Isolate process in its own session on Unix to prevent terminal capture.
	// This creates a new session with no controlling terminal, so interactive
	// commands (vim, htop, sudo) cannot steal input from the TUI.
	// On Windows, CREATE_NEW_PROCESS_GROUP has similar effect.
	setSysProcAttr(e.cmd)

	// Set working directory
	if e.request.WorkDir != "" {
		e.cmd.Dir = e.request.WorkDir
	}

	// Set environment
	if len(e.request.Env) > 0 {
		e.cmd.Env = os.Environ()
		for k, v := range e.request.Env {
			e.cmd.Env = append(e.cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Create temp files for stdout/stderr to avoid buffer overflow issues
	var err error
	e.stdoutFile, err = os.CreateTemp(os.TempDir(), "bgprocess-stdout-*.txt")
	if err != nil {
		e.transitionState(StateRunning, StateFailed)
		return WrapExecutionError(e.handle, err, "failed to create stdout temp file")
	}

	e.stderrFile, err = os.CreateTemp(os.TempDir(), "bgprocess-stderr-*.txt")
	if err != nil {
		e.stdoutFile.Close()
		os.Remove(e.stdoutFile.Name())
		e.transitionState(StateRunning, StateFailed)
		return WrapExecutionError(e.handle, err, "failed to create stderr temp file")
	}

	// Redirect command output to temp files
	e.cmd.Stdout = e.stdoutFile
	e.cmd.Stderr = e.stderrFile

	// Start the process
	e.startedAt = time.Now()
	if err := e.cmd.Start(); err != nil {
		e.cleanupTempFiles()
		e.transitionState(StateRunning, StateFailed)
		return WrapExecutionError(e.handle, err, "failed to start command")
	}

	// Channel to signal tailers to stop
	stopTailing := make(chan struct{})

	// Start goroutines to tail output files
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		e.tailOutputFile(e.stdoutFile.Name(), "stdout", stopTailing)
	}()

	go func() {
		defer wg.Done()
		e.tailOutputFile(e.stderrFile.Name(), "stderr", stopTailing)
	}()

	// Wait for process completion in background
	go func() {
		// Wait for process to exit
		err := e.cmd.Wait()

		// Close temp files so final content is flushed
		e.stdoutFile.Close()
		e.stderrFile.Close()

		// Signal tailers to do final read and stop
		close(stopTailing)

		// Wait for output capture to complete
		wg.Wait()

		// Clean up temp files
		e.cleanupTempFiles()

		now := time.Now()
		e.stateMu.Lock()
		e.completedAt = &now
		if err != nil {
			e.execErr = err
			if exitErr, ok := err.(*exec.ExitError); ok {
				code := exitErr.ExitCode()
				e.exitCode = &code
			}
		} else {
			code := 0
			e.exitCode = &code
		}
		e.stateMu.Unlock()

		// Close output buffer
		e.output.Close()

		// Determine final state
		e.stateMu.RLock()
		currentState := e.state
		e.stateMu.RUnlock()

		if currentState == StateCancelled {
			// Already cancelled, don't change state
		} else if e.exitCode != nil && *e.exitCode == 0 {
			e.transitionState(currentState, StateCompleted)
		} else {
			e.transitionState(currentState, StateFailed)
		}

		// Signal completion
		e.doneOnce.Do(func() {
			close(e.doneCh)
		})
	}()

	return nil
}

// tailOutputFile reads from a temp file and writes to the output buffer.
// This avoids buffer overflow issues by reading chunks instead of scanning lines.
func (e *ProcessExecutorImpl) tailOutputFile(path string, stream string, stop <-chan struct{}) {
	var offset int64
	buf := make([]byte, 32*1024) // 32KB read buffer

	for {
		// Check if we should stop
		select {
		case <-stop:
			// Process finished, do final read and return
			e.readFileChunk(path, &offset, buf, stream)
			return
		case <-e.ctx.Done():
			return
		default:
			e.readFileChunk(path, &offset, buf, stream)
			time.Sleep(50 * time.Millisecond) // Poll interval
		}
	}
}

// readFileChunk reads new content from a file and writes to output buffer.
func (e *ProcessExecutorImpl) readFileChunk(path string, offset *int64, buf []byte, stream string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	// Seek to current offset
	_, err = file.Seek(*offset, io.SeekStart)
	if err != nil {
		return
	}

	// Read available data
	n, err := file.Read(buf)
	if n > 0 {
		chunk := string(buf[:n])
		*offset += int64(n)

		// Write to output buffer (splitting by newlines for line-oriented storage)
		start := 0
		for i := 0; i < len(chunk); i++ {
			if chunk[i] == '\n' {
				line := chunk[start:i]
				// This line is bounded by a newline, so it is a complete,
				// real line even when empty — an empty line is content
				// (a genuinely blank line in the process's output), not the
				// absence of one. Dropping it here silently rewrote every
				// command's output before it ever reached the buffer that
				// issue #297/#264's patch-context failures were traced to.
				e.output.WriteLine(stream, line)
				start = i + 1
			}
		}
		// Handle remaining content without newline
		if start < len(chunk) {
			e.output.WriteLine(stream, chunk[start:])
		}
	}
}

// cleanupTempFiles removes the temporary output files.
func (e *ProcessExecutorImpl) cleanupTempFiles() {
	if e.stdoutFile != nil {
		os.Remove(e.stdoutFile.Name())
	}
	if e.stderrFile != nil {
		os.Remove(e.stderrFile.Name())
	}
}

// Wait blocks until the process completes or context is cancelled.
func (e *ProcessExecutorImpl) Wait(ctx context.Context) (*ProcessResult, error) {
	select {
	case <-e.doneCh:
		// Process completed
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.ctx.Done():
		// Our internal context was cancelled
		return nil, e.ctx.Err()
	}

	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	var duration time.Duration
	if e.completedAt != nil {
		duration = e.completedAt.Sub(e.startedAt)
	}

	exitCode := -1
	if e.exitCode != nil {
		exitCode = *e.exitCode
	}

	// Get all output
	lines, _ := e.output.Lines(LineQueryOpts{})
	var outputBytes []byte
	for _, line := range lines {
		outputBytes = append(outputBytes, []byte(line.Content+"\n")...)
	}

	completedAt := time.Now()
	if e.completedAt != nil {
		completedAt = *e.completedAt
	}

	return &ProcessResult{
		ExitCode:    exitCode,
		Output:      outputBytes,
		Error:       e.execErr,
		Duration:    duration,
		CompletedAt: completedAt,
	}, nil
}

// Signal sends a signal to the process.
func (e *ProcessExecutorImpl) Signal(sig ProcessSignal) error {
	e.stateMu.RLock()
	state := e.state
	e.stateMu.RUnlock()

	if state != StateRunning && state != StatePaused {
		return WrapStateError(e.handle, state, StateRunning)
	}

	if e.cmd == nil || e.cmd.Process == nil {
		return WrapExecutionError(e.handle, ErrInvalidState, "process not started")
	}

	var osSignal os.Signal
	switch sig {
	case SignalTerm:
		osSignal = signalTerm()
	case SignalKill:
		osSignal = signalKill()
	case SignalInt:
		osSignal = syscall.SIGINT // SIGINT works on both Unix and Windows
	case SignalStop:
		osSignal = signalStop()
	case SignalCont:
		osSignal = signalCont()
	default:
		return fmt.Errorf("unknown signal: %v", sig)
	}

	return e.cmd.Process.Signal(osSignal)
}

// Pause suspends the process (SIGSTOP on Unix).
// Returns ErrNotSupported on Windows where pause/resume is not available.
func (e *ProcessExecutorImpl) Pause() error {
	if !canPauseResume() {
		return ErrNotSupported
	}

	if err := e.transitionState(StateRunning, StatePaused); err != nil {
		return err
	}

	if err := e.Signal(SignalStop); err != nil {
		// Rollback state
		e.transitionState(StatePaused, StateRunning)
		return err
	}

	return nil
}

// Resume resumes a paused process (SIGCONT on Unix).
// Returns ErrNotSupported on Windows where pause/resume is not available.
func (e *ProcessExecutorImpl) Resume() error {
	if !canPauseResume() {
		return ErrNotSupported
	}

	if err := e.transitionState(StatePaused, StateRunning); err != nil {
		return err
	}

	if err := e.Signal(SignalCont); err != nil {
		// Rollback state
		e.transitionState(StateRunning, StatePaused)
		return err
	}

	return nil
}

// Cancel terminates the process gracefully (SIGTERM -> SIGKILL).
func (e *ProcessExecutorImpl) Cancel() error {
	e.stateMu.RLock()
	state := e.state
	e.stateMu.RUnlock()

	if state.IsTerminal() {
		return nil // Already done
	}

	// Try graceful termination first
	if e.cmd != nil && e.cmd.Process != nil {
		e.Signal(SignalTerm)

		// Wait briefly for graceful shutdown
		select {
		case <-e.doneCh:
			// Process exited
		case <-time.After(5 * time.Second):
			// Force kill
			e.Signal(SignalKill)
		}
	}

	// Cancel context
	e.cancel()

	// Wait for completion
	<-e.doneCh

	// Update state if not already terminal
	e.stateMu.Lock()
	if !e.state.IsTerminal() {
		e.state = StateCancelled
		if e.onStateChange != nil {
			e.onStateChange(state, StateCancelled)
		}
	}
	e.stateMu.Unlock()

	return nil
}

// Output returns the output buffer.
func (e *ProcessExecutorImpl) Output() OutputBuffer {
	return e.output
}

// Info returns process metadata.
func (e *ProcessExecutorImpl) Info() ProcessInfo {
	e.stateMu.RLock()
	defer e.stateMu.RUnlock()

	var duration time.Duration
	if e.completedAt != nil {
		duration = e.completedAt.Sub(e.startedAt)
	} else if !e.startedAt.IsZero() {
		duration = time.Since(e.startedAt)
	}

	return ProcessInfo{
		Handle:      e.handle,
		Command:     e.request.Command,
		WorkDir:     e.request.WorkDir,
		Env:         e.request.Env,
		State:       e.state,
		ExitCode:    e.exitCode,
		StartedAt:   e.startedAt,
		CompletedAt: e.completedAt,
		Duration:    duration,
		Owner:       e.request.Owner,
		Tags:        e.request.Tags,
	}
}

// PID returns the process ID, or 0 if not started.
func (e *ProcessExecutorImpl) PID() int {
	if e.cmd != nil && e.cmd.Process != nil {
		return e.cmd.Process.Pid
	}
	return 0
}

// transitionState atomically transitions the process state.
func (e *ProcessExecutorImpl) transitionState(from, to ProcessState) error {
	e.stateMu.Lock()
	defer e.stateMu.Unlock()

	if e.state != from {
		return WrapStateError(e.handle, e.state, to)
	}

	// Validate transition
	if !isValidTransition(from, to) {
		return WrapStateError(e.handle, from, to)
	}

	e.state = to

	// Notify state change
	select {
	case e.stateChange <- to:
	default:
		// Channel full, skip
	}

	if e.onStateChange != nil {
		e.onStateChange(from, to)
	}

	return nil
}

// isValidTransition checks if a state transition is allowed.
func isValidTransition(from, to ProcessState) bool {
	validTransitions := map[ProcessState][]ProcessState{
		StateCreated:   {StateRunning, StateFailed},
		StateRunning:   {StateCompleted, StateFailed, StateCancelled, StatePaused},
		StatePaused:    {StateRunning, StateCancelled},
		StateCompleted: {}, // Terminal
		StateFailed:    {}, // Terminal
		StateCancelled: {}, // Terminal
	}

	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}

	return slices.Contains(allowed, to)
}

// generateProcessID generates a unique process ID.
func generateProcessID() string {
	return fmt.Sprintf("bg-%d-%d", time.Now().UnixNano(), os.Getpid())
}

// detectShell returns the appropriate shell for the current platform.
// On Windows, prefers PowerShell if available, falls back to cmd.exe.
// On Unix, prefers bash, falls back to sh or $SHELL.
func detectShell() string {
	if runtime.GOOS == "windows" {
		// Try PowerShell first (better command support)
		if _, err := exec.LookPath("pwsh"); err == nil {
			return "pwsh"
		}
		if _, err := exec.LookPath("powershell"); err == nil {
			return "powershell"
		}
		// Fallback to cmd.exe
		return "cmd.exe"
	}
	// Unix: prefer $SHELL if set
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	// Fall back to bash or sh
	if _, err := exec.LookPath("bash"); err == nil {
		return "/bin/bash"
	}
	return "/bin/sh"
}
