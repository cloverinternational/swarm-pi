package bgprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BackgroundProcessManager implements Manager interface.
type BackgroundProcessManager struct {
	processes       map[string]*ProcessExecutorImpl
	backgroundedIDs map[string]bool // Process IDs that were explicitly backgrounded
	mu              sync.RWMutex
	visibility      VisibilityPolicy

	// Statistics
	totalSpawned   int64
	completedCount int64
	failedCount    int64
	cancelledCount int64

	// Lifecycle
	ctx        context.Context
	cancel     context.CancelFunc
	shutdownWg sync.WaitGroup
	shutdownMu sync.Mutex
	isShutdown bool

	// Configuration
	cleanupInterval time.Duration
	maxProcessAge   time.Duration

	// Observability hooks
	onSpawn    func(handle ProcessHandle, req SpawnRequest)
	onComplete func(handle ProcessHandle, info ProcessInfo, result *ProcessResult)
	onError    func(handle ProcessHandle, info ProcessInfo, err error)
}

// ManagerConfig contains configuration for the manager.
type ManagerConfig struct {
	Visibility      VisibilityPolicy
	CleanupInterval time.Duration
	MaxProcessAge   time.Duration
	OnSpawn         func(handle ProcessHandle, req SpawnRequest)
	OnComplete      func(handle ProcessHandle, info ProcessInfo, result *ProcessResult)
	OnError         func(handle ProcessHandle, info ProcessInfo, err error)
}

// DefaultManagerConfig returns sensible defaults.
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		Visibility:      NewSimpleVisibilityPolicy(),
		CleanupInterval: 5 * time.Minute,
		MaxProcessAge:   30 * time.Minute,
	}
}

// NewManager creates a new background process manager.
func NewManager(config ManagerConfig) *BackgroundProcessManager {
	if config.Visibility == nil {
		config.Visibility = NewSimpleVisibilityPolicy()
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 5 * time.Minute
	}
	if config.MaxProcessAge <= 0 {
		config.MaxProcessAge = 30 * time.Minute
	}

	ctx, cancel := context.WithCancel(context.Background())

	m := &BackgroundProcessManager{
		processes:       make(map[string]*ProcessExecutorImpl),
		backgroundedIDs: make(map[string]bool),
		visibility:      config.Visibility,
		ctx:             ctx,
		cancel:          cancel,
		cleanupInterval: config.CleanupInterval,
		maxProcessAge:   config.MaxProcessAge,
		onSpawn:         config.OnSpawn,
		onComplete:      config.OnComplete,
		onError:         config.OnError,
	}

	// Start cleanup goroutine
	m.shutdownWg.Add(1)
	go m.cleanupLoop()

	return m
}

// Spawn creates and starts a new background process.
func (m *BackgroundProcessManager) Spawn(ctx context.Context, req SpawnRequest) (ProcessHandle, error) {
	m.shutdownMu.Lock()
	if m.isShutdown {
		m.shutdownMu.Unlock()
		return ProcessHandle{}, ErrManagerShutdown
	}
	m.shutdownMu.Unlock()

	// Create executor
	executor, err := NewProcessExecutor(ExecutorConfig{
		Request: req,
	})
	if err != nil {
		return ProcessHandle{}, err
	}

	handle := executor.Handle()

	// Set state change callback now that we have the handle
	executor.onStateChange = func(from, to ProcessState) {
		m.handleStateChange(handle, from, to)
	}

	// Register in map
	m.mu.Lock()
	m.processes[handle.ID()] = executor
	m.mu.Unlock()

	atomic.AddInt64(&m.totalSpawned, 1)

	// Notify spawn hook
	if m.onSpawn != nil {
		m.onSpawn(handle, req)
	}

	// Start the process
	if err := executor.Start(ctx); err != nil {
		m.mu.Lock()
		delete(m.processes, handle.ID())
		m.mu.Unlock()

		if m.onError != nil {
			m.onError(handle, executor.Info(), err)
		}
		return ProcessHandle{}, err
	}

	return handle, nil
}

// handleStateChange is called when a process changes state.
// IMPORTANT: This runs inside transitionState which holds e.stateMu (write lock).
// Callbacks that call exec.Wait() or exec.Info() must run in a separate goroutine
// to avoid deadlocking on the non-reentrant mutex.  The goroutine waits on
// exec.doneCh (via Wait) which closes AFTER transitionState returns.
func (m *BackgroundProcessManager) handleStateChange(handle ProcessHandle, from, to ProcessState) {
	m.mu.RLock()
	exec := m.processes[handle.ID()]
	m.mu.RUnlock()

	// Only fire callbacks for processes that were explicitly backgrounded.
	// Foreground processes deliver results through the normal tool result flow.
	isBackgrounded := m.IsBackgrounded(handle.ID())

	switch to {
	case StateCompleted:
		atomic.AddInt64(&m.completedCount, 1)
		if isBackgrounded && exec != nil && m.onComplete != nil {
			cb := m.onComplete
			go func() {
				// Wait for doneCh to close (happens after transitionState releases stateMu)
				result, _ := exec.Wait(context.Background())
				cb(handle, exec.Info(), result)
			}()
		}
	case StateFailed:
		atomic.AddInt64(&m.failedCount, 1)
		if isBackgrounded && exec != nil && m.onError != nil {
			cb := m.onError
			go func() {
				// Wait for doneCh then safely read state
				result, _ := exec.Wait(context.Background())
				var execErr error
				if result != nil {
					execErr = result.Error
				}
				cb(handle, exec.Info(), execErr)
			}()
		}
	case StateCancelled:
		atomic.AddInt64(&m.cancelledCount, 1)
		if isBackgrounded && exec != nil && m.onComplete != nil {
			cb := m.onComplete
			go func() {
				result, _ := exec.Wait(context.Background())
				cb(handle, exec.Info(), result)
			}()
		}
	}
}

// Get retrieves a process executor by handle.
func (m *BackgroundProcessManager) Get(ctx context.Context, handle ProcessHandle) (ProcessExecutor, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "get", ErrProcessNotFound, "process not found")
	}

	return exec, nil
}

// GetInfo retrieves process info by handle.
func (m *BackgroundProcessManager) GetInfo(ctx context.Context, handle ProcessHandle) (*ProcessInfo, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "get_info", ErrProcessNotFound, "process not found")
	}

	info := exec.Info()
	return &info, nil
}

// List returns all processes visible to the subject.
func (m *BackgroundProcessManager) List(ctx context.Context, subject OwnerInfo, filters ...Filter) ([]ProcessInfo, error) {
	m.mu.RLock()
	allProcesses := make([]ProcessInfo, 0, len(m.processes))
	for _, exec := range m.processes {
		info := exec.Info()

		// Apply filters
		match := true
		for _, f := range filters {
			if !f.Match(info) {
				match = false
				break
			}
		}
		if match {
			allProcesses = append(allProcesses, info)
		}
	}
	m.mu.RUnlock()

	// Apply visibility filtering
	return m.visibility.FilterVisible(ctx, subject, allProcesses)
}

// Cancel terminates a process.
func (m *BackgroundProcessManager) Cancel(ctx context.Context, handle ProcessHandle, subject OwnerInfo) error {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return NewProcessError(handle, "cancel", ErrProcessNotFound, "process not found")
	}

	// Check permission
	info := exec.Info()
	allowed, err := m.visibility.CanControl(ctx, subject, info, ActionCancel)
	if err != nil {
		return err
	}
	if !allowed {
		return WrapVisibilityError(handle, ActionCancel, subject)
	}

	return exec.Cancel()
}

// Wait blocks until the process completes.
func (m *BackgroundProcessManager) Wait(ctx context.Context, handle ProcessHandle) (*ProcessResult, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "wait", ErrProcessNotFound, "process not found")
	}

	return exec.Wait(ctx)
}

// GetOutput retrieves output from a process.
func (m *BackgroundProcessManager) GetOutput(ctx context.Context, handle ProcessHandle, subject OwnerInfo, opts LineQueryOpts) ([]OutputLine, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "get_output", ErrProcessNotFound, "process not found")
	}

	// Check permission
	info := exec.Info()
	allowed, err := m.visibility.CanControl(ctx, subject, info, ActionRead)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, WrapVisibilityError(handle, ActionRead, subject)
	}

	return exec.Output().Lines(opts)
}

// TailOutput returns the last n lines of a process's output.
//
// Callers that only want a tail must NOT do this:
//
//	all, _ := GetOutput(ctx, h, subj, LineQueryOpts{})   // copies EVERY line
//	from  := len(all) - n
//	tail, _ := GetOutput(ctx, h, subj, LineQueryOpts{FromLine: from, MaxLines: n})
//
// Lines() allocates a fresh slice containing every line it matches, so the
// first call copies the entire buffer (up to the 10 MB MemoryBuffer cap —
// hundreds of thousands of OutputLine structs) purely to learn its length,
// and then the tail is fetched a second time. On a live "terminal window"
// block that refreshes at 10 Hz, that is two full-buffer copies per frame.
//
// OutputBuffer already exposes LineCount() (O(1)) and Tail(n), so the tail
// costs O(n) instead of O(total).
func (m *BackgroundProcessManager) TailOutput(ctx context.Context, handle ProcessHandle, subject OwnerInfo, n int) ([]OutputLine, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "tail_output", ErrProcessNotFound, "process not found")
	}

	// Check permission — identical to GetOutput.
	info := exec.Info()
	allowed, err := m.visibility.CanControl(ctx, subject, info, ActionRead)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, WrapVisibilityError(handle, ActionRead, subject)
	}

	return exec.Output().Tail(n)
}

// StreamOutput streams output from a process.
func (m *BackgroundProcessManager) StreamOutput(ctx context.Context, handle ProcessHandle, subject OwnerInfo) (<-chan OutputLine, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "stream_output", ErrProcessNotFound, "process not found")
	}

	// Check permission
	info := exec.Info()
	allowed, err := m.visibility.CanControl(ctx, subject, info, ActionRead)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, WrapVisibilityError(handle, ActionRead, subject)
	}

	return exec.Output().Stream(ctx)
}

// Cleanup removes completed/failed processes older than maxAge.
func (m *BackgroundProcessManager) Cleanup(maxAge time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	now := time.Now()

	for id, exec := range m.processes {
		info := exec.Info()

		// Only cleanup terminal processes
		if !info.State.IsTerminal() {
			continue
		}

		// Check age
		var age time.Duration
		if info.CompletedAt != nil {
			age = now.Sub(*info.CompletedAt)
		} else {
			age = now.Sub(info.StartedAt)
		}

		if age > maxAge {
			delete(m.processes, id)
			removed++
		}
	}

	return removed, nil
}

// cleanupLoop periodically cleans up old processes.
func (m *BackgroundProcessManager) cleanupLoop() {
	defer m.shutdownWg.Done()

	ticker := time.NewTicker(m.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.Cleanup(m.maxProcessAge)
		case <-m.ctx.Done():
			return
		}
	}
}

// Shutdown gracefully terminates all running processes.
func (m *BackgroundProcessManager) Shutdown(ctx context.Context) error {
	m.shutdownMu.Lock()
	if m.isShutdown {
		m.shutdownMu.Unlock()
		return nil
	}
	m.isShutdown = true
	m.shutdownMu.Unlock()

	// Cancel all running processes
	m.mu.RLock()
	var wg sync.WaitGroup
	for _, exec := range m.processes {
		if !exec.State().IsTerminal() {
			wg.Add(1)
			go func(e *ProcessExecutorImpl) {
				defer wg.Done()
				e.Cancel()
			}(exec)
		}
	}
	m.mu.RUnlock()

	// Wait for all cancellations with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All processes cancelled
	case <-ctx.Done():
		// Timeout, force continue
	}

	// Stop cleanup goroutine
	m.cancel()
	m.shutdownWg.Wait()

	return nil
}

// Stats returns manager statistics.
func (m *BackgroundProcessManager) Stats() ManagerStats {
	m.mu.RLock()
	activeCount := 0
	for _, exec := range m.processes {
		if !exec.State().IsTerminal() {
			activeCount++
		}
	}
	m.mu.RUnlock()

	return ManagerStats{
		TotalSpawned:   atomic.LoadInt64(&m.totalSpawned),
		ActiveCount:    activeCount,
		CompletedCount: atomic.LoadInt64(&m.completedCount),
		FailedCount:    atomic.LoadInt64(&m.failedCount),
		CancelledCount: atomic.LoadInt64(&m.cancelledCount),
	}
}

// GetByID retrieves a process executor by ID string.
func (m *BackgroundProcessManager) GetByID(ctx context.Context, id string) (ProcessExecutor, error) {
	m.mu.RLock()
	exec, ok := m.processes[id]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(NewProcessHandle(id), "get", ErrProcessNotFound, "process not found")
	}

	return exec, nil
}

// GetInfoByID retrieves process info by ID string.
func (m *BackgroundProcessManager) GetInfoByID(ctx context.Context, id string) (*ProcessInfo, error) {
	return m.GetInfo(ctx, NewProcessHandle(id))
}

// GetOutputByID retrieves output by ID string.
func (m *BackgroundProcessManager) GetOutputByID(ctx context.Context, id string, subject OwnerInfo, opts LineQueryOpts) ([]OutputLine, error) {
	return m.GetOutput(ctx, NewProcessHandle(id), subject, opts)
}

// LineCountByID returns the current output line count for a process. This is
// O(1) on the buffer and is the correct way to detect "has this process
// produced new output?" without copying any lines.
func (m *BackgroundProcessManager) LineCountByID(ctx context.Context, id string, subject OwnerInfo) (int, error) {
	return m.getTotalLines(ctx, id, subject)
}

// TailOutputByID returns the last n output lines by ID string.
func (m *BackgroundProcessManager) TailOutputByID(ctx context.Context, id string, subject OwnerInfo, n int) ([]OutputLine, error) {
	return m.TailOutput(ctx, NewProcessHandle(id), subject, n)
}

// CancelByID cancels a process by ID string.
func (m *BackgroundProcessManager) CancelByID(ctx context.Context, id string, subject OwnerInfo) error {
	return m.Cancel(ctx, NewProcessHandle(id), subject)
}

// FormatOutput formats output for the ReadBackgroundCommand tool.
func (m *BackgroundProcessManager) FormatOutput(ctx context.Context, handle ProcessHandle, subject OwnerInfo, opts LineQueryOpts) (*BackgroundBashOutput, error) {
	m.mu.RLock()
	exec, ok := m.processes[handle.ID()]
	m.mu.RUnlock()

	if !ok {
		return nil, NewProcessError(handle, "format_output", ErrProcessNotFound, "process not found")
	}

	// Check permission
	info := exec.Info()
	allowed, err := m.visibility.CanControl(ctx, subject, info, ActionRead)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, WrapVisibilityError(handle, ActionRead, subject)
	}

	totalLines := exec.Output().LineCount()

	// Write the full output to a deterministic tmp file whenever the output is
	// non-empty and will be (or already has been) truncated.  The file persists
	// as long as the process is alive in the manager so the agent can read it
	// with ReadLegacy/cat at any time.
	outputFile := ""
	if totalLines > 0 && opts.MaxLines > 0 && totalLines > opts.MaxLines {
		outputFile = m.writeTmpOutputFile(exec, handle.ID())
	}

	// Get the requested window of lines.
	lines, err := exec.Output().Lines(opts)
	if err != nil {
		return nil, WrapOutputError(handle, err, "failed to get output lines")
	}

	truncated := opts.MaxLines > 0 && totalLines > opts.MaxLines

	// Compute heartbeat signals so the polling agent has a basis to decide
	//	"running but healthy" vs "genuinely stuck" rather than cancelling on
	//	wall-clock alone. Cheap because we only peek the last line.
	var lastOutputAtStr string
	var secondsSinceLast float64
	if tail, tailErr := exec.Output().Tail(1); tailErr == nil && len(tail) > 0 {
		last := tail[0].Timestamp
		if !last.IsZero() {
			lastOutputAtStr = last.Format(time.RFC3339)
			secondsSinceLast = time.Since(last).Seconds()
			if secondsSinceLast < 0 {
				secondsSinceLast = 0
			}
		}
	}
	bytesWritten := exec.Output().Size()

	// Build response
	output := &BackgroundBashOutput{
		TaskID:          handle.ID(),
		Status:          string(info.State),
		Command:         info.Command,
		PID:             exec.PID(),
		StartedAt:       info.StartedAt.Format(time.RFC3339),
		DurationSeconds: info.Duration.Seconds(),
		ExitCode:        info.ExitCode,
		Output:          lines,
		Metadata: OutputMetadata{
			WorkDir:                info.WorkDir,
			Env:                    info.Env,
			TotalLines:             totalLines,
			OutputTruncated:        truncated,
			OutputFile:             outputFile,
			LastOutputAt:           lastOutputAtStr,
			SecondsSinceLastOutput: secondsSinceLast,
			BytesWritten:           bytesWritten,
		},
	}

	if info.CompletedAt != nil {
		output.EndedAt = info.CompletedAt.Format(time.RFC3339)
	}

	// Add file path if using file buffer (separate from the output dump file).
	if fb, ok := exec.Output().(*FileBuffer); ok {
		output.Metadata.FilePath = fb.Path()
	}

	return output, nil
}

// FormatOutputByID is a convenience method for FormatOutput with ID string.
func (m *BackgroundProcessManager) FormatOutputByID(ctx context.Context, id string, subject OwnerInfo, opts LineQueryOpts) (*BackgroundBashOutput, error) {
	return m.FormatOutput(ctx, NewProcessHandle(id), subject, opts)
}

// writeTmpOutputFile writes every output line from exec to a deterministic
// temp file in the OS temp directory (bgprocess-output-<id>.txt, plain text, one line per line).
// Returns the file path on success, or an empty string on failure.
// getTotalLines returns the current total line count for a process.
// Used by the tool to compute the tail start before fetching a windowed slice.
func (m *BackgroundProcessManager) getTotalLines(ctx context.Context, id string, subject OwnerInfo) (int, error) {
	m.mu.RLock()
	exec, ok := m.processes[id]
	m.mu.RUnlock()
	if !ok {
		return 0, NewProcessError(NewProcessHandle(id), "get_total_lines", ErrProcessNotFound, "process not found")
	}
	info := exec.Info()
	allowed, err := m.visibility.CanControl(ctx, subject, info, ActionRead)
	if err != nil {
		return 0, err
	}
	if !allowed {
		return 0, WrapVisibilityError(NewProcessHandle(id), ActionRead, subject)
	}
	return exec.Output().LineCount(), nil
}

// writeTmpOutputFile writes every output line from exec to a deterministic
// temp file in the OS temp directory (bgprocess-output-<id>.txt, plain text, one line per line).
// Returns the file path on success, or an empty string on failure.
func (m *BackgroundProcessManager) writeTmpOutputFile(exec *ProcessExecutorImpl, id string) string {
	// Sanitise the ID so it is safe to use as a filename.
	safeID := strings.NewReplacer("/", "-", ":", "-", " ", "-").Replace(id)
	path := filepath.Join(os.TempDir(), "bgprocess-output-"+safeID+".txt")

	// Fetch all lines without any limit.
	allLines, err := exec.Output().Lines(LineQueryOpts{})
	if err != nil || len(allLines) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, line := range allLines {
		sb.WriteString(line.Content)
		sb.WriteByte('\n')
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return ""
	}
	return path
}

// MarkBackgrounded records that a process has been explicitly backgrounded
// (via background=true, auto-timeout, or Ctrl+B).  Only backgrounded
// processes trigger the onComplete callback.
func (m *BackgroundProcessManager) MarkBackgrounded(id string) {
	m.mu.Lock()
	m.backgroundedIDs[id] = true
	m.mu.Unlock()
}

// IsBackgrounded returns true if the process was explicitly backgrounded.
func (m *BackgroundProcessManager) IsBackgrounded(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backgroundedIDs[id]
}

// SetCompletionCallback sets or replaces the onComplete hook.
// This is safe to call after NewManager — the callback fires when any
// managed process reaches a terminal state (completed or cancelled).
// The callback runs on the executor goroutine, so it must be
// non-blocking (e.g. send on a buffered channel).
func (m *BackgroundProcessManager) SetCompletionCallback(cb func(handle ProcessHandle, info ProcessInfo, result *ProcessResult)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onComplete = cb
}

// SetErrorCallback sets or replaces the onError hook.
// Fires when a managed process reaches StateFailed.
func (m *BackgroundProcessManager) SetErrorCallback(cb func(handle ProcessHandle, info ProcessInfo, err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onError = cb
}

// String returns a string representation of the manager state.
func (m *BackgroundProcessManager) String() string {
	stats := m.Stats()
	return fmt.Sprintf("BackgroundProcessManager{active=%d, spawned=%d, completed=%d, failed=%d, cancelled=%d}",
		stats.ActiveCount, stats.TotalSpawned, stats.CompletedCount, stats.FailedCount, stats.CancelledCount)
}
