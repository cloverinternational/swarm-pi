// Package bgprocess provides background bash command execution with ABAC visibility control.
// It implements a layered architecture with clear separation between process execution,
// output management, and visibility enforcement.
package bgprocess

import (
	"context"
	"io"
	"slices"
	"time"
)

// ProcessState represents the lifecycle state of a background process.
type ProcessState string

const (
	StateCreated   ProcessState = "created"
	StateRunning   ProcessState = "running"
	StatePaused    ProcessState = "paused"
	StateCompleted ProcessState = "completed"
	StateFailed    ProcessState = "failed"
	StateCancelled ProcessState = "cancelled"
)

// IsTerminal returns true if the state is a terminal state (no further transitions).
func (s ProcessState) IsTerminal() bool {
	return s == StateCompleted || s == StateFailed || s == StateCancelled
}

// ProcessHandle is an opaque, type-safe reference to a background process.
// Using handles instead of raw PIDs prevents use-after-free and race conditions.
type ProcessHandle struct {
	id        string
	createdAt time.Time
}

// NewProcessHandle creates a new process handle with the given ID.
func NewProcessHandle(id string) ProcessHandle {
	return ProcessHandle{
		id:        id,
		createdAt: time.Now(),
	}
}

// ID returns the unique process identifier.
func (h ProcessHandle) ID() string { return h.id }

// CreatedAt returns when the handle was created.
func (h ProcessHandle) CreatedAt() time.Time { return h.createdAt }

// IsZero returns true if the handle is uninitialized.
func (h ProcessHandle) IsZero() bool { return h.id == "" }

// ProcessInfo contains metadata about a background process.
type ProcessInfo struct {
	Handle      ProcessHandle
	Command     string
	WorkDir     string
	Env         map[string]string
	State       ProcessState
	ExitCode    *int
	StartedAt   time.Time
	CompletedAt *time.Time
	Duration    time.Duration
	Owner       OwnerInfo
	Tags        map[string]string
}

// OwnerInfo identifies who spawned the process for ABAC visibility.
type OwnerInfo struct {
	UserID         string
	AgentID        string
	ConversationID string
	Role           string // "user", "agent", "admin"
}

// ProcessResult contains the final outcome of process execution.
type ProcessResult struct {
	ExitCode    int
	Output      []byte
	Error       error
	Duration    time.Duration
	CompletedAt time.Time
}

// ProcessSignal represents OS signals (cross-platform abstraction).
type ProcessSignal int

const (
	SignalTerm ProcessSignal = iota // Graceful termination (SIGTERM)
	SignalKill                      // Forceful kill (SIGKILL)
	SignalInt                       // Interrupt (SIGINT, Ctrl+C)
	SignalStop                      // Pause execution (SIGSTOP)
	SignalCont                      // Resume execution (SIGCONT)
)

// String returns the signal name.
func (s ProcessSignal) String() string {
	switch s {
	case SignalTerm:
		return "SIGTERM"
	case SignalKill:
		return "SIGKILL"
	case SignalInt:
		return "SIGINT"
	case SignalStop:
		return "SIGSTOP"
	case SignalCont:
		return "SIGCONT"
	default:
		return "UNKNOWN"
	}
}

// ControlAction represents actions that require authorization.
type ControlAction string

const (
	ActionView   ControlAction = "view"
	ActionRead   ControlAction = "read"
	ActionCancel ControlAction = "cancel"
	ActionPause  ControlAction = "pause"
	ActionResume ControlAction = "resume"
	ActionSignal ControlAction = "signal"
)

// SpawnRequest contains parameters for spawning a background process.
type SpawnRequest struct {
	Command      string
	WorkDir      string
	Env          map[string]string
	Owner        OwnerInfo
	Tags         map[string]string
	Timeout      time.Duration
	OutputConfig OutputBufferConfig
}

// OutputBufferConfig configures output storage.
type OutputBufferConfig struct {
	Type     BufferType
	MaxSize  int64
	FilePath string // For file-backed buffers
}

// BufferType specifies output buffer implementation.
type BufferType string

const (
	BufferMemory BufferType = "memory"
	BufferFile   BufferType = "file"
	BufferHybrid BufferType = "hybrid" // Memory with file spillover
)

// DefaultOutputBufferConfig returns sensible defaults for output buffering.
func DefaultOutputBufferConfig() OutputBufferConfig {
	return OutputBufferConfig{
		Type:    BufferMemory,
		MaxSize: 10 * 1024 * 1024, // 10MB
	}
}

// OutputLine represents a single line of output with metadata.
type OutputLine struct {
	LineNumber int       `json:"line_number"`
	Timestamp  time.Time `json:"timestamp"`
	Stream     string    `json:"stream"` // "stdout" or "stderr"
	Content    string    `json:"content"`
}

// OutputBuffer abstracts output storage and retrieval.
type OutputBuffer interface {
	io.Writer
	io.Closer

	// WriteLine writes a line with metadata.
	WriteLine(stream string, content string) error

	// Lines returns output lines, optionally filtered.
	Lines(opts LineQueryOpts) ([]OutputLine, error)

	// Tail returns the last n lines.
	Tail(n int) ([]OutputLine, error)

	// Since returns all output after a given timestamp.
	Since(t time.Time) ([]OutputLine, error)

	// Stream returns a channel that emits output lines as they arrive.
	Stream(ctx context.Context) (<-chan OutputLine, error)

	// Size returns the total bytes stored.
	Size() int64

	// LineCount returns the total number of lines.
	LineCount() int

	// Flush ensures all buffered data is written.
	Flush() error
}

// LineQueryOpts configures line queries.
type LineQueryOpts struct {
	FromLine int    // Start from this line number (1-indexed, 0 = beginning)
	MaxLines int    // Maximum lines to return (0 = unlimited)
	Stream   string // Filter by stream ("stdout", "stderr", "" = all)
	Pattern  string // Filter by regex pattern (optional)
}

// ProcessExecutor manages the lifecycle of a single background process.
type ProcessExecutor interface {
	// Start begins execution of the command.
	Start(ctx context.Context) error

	// State returns the current process state.
	State() ProcessState

	// Wait blocks until the process completes or context is cancelled.
	Wait(ctx context.Context) (*ProcessResult, error)

	// Signal sends a signal to the process.
	Signal(sig ProcessSignal) error

	// Pause suspends the process (SIGSTOP on Unix).
	Pause() error

	// Resume resumes a paused process (SIGCONT on Unix).
	Resume() error

	// Cancel terminates the process gracefully (SIGTERM -> SIGKILL).
	Cancel() error

	// Output returns the output buffer.
	Output() OutputBuffer

	// Info returns process metadata.
	Info() ProcessInfo

	// Handle returns the process handle.
	Handle() ProcessHandle
}

// VisibilityPolicy determines who can view/control a background process.
type VisibilityPolicy interface {
	// CanView checks if the subject can view process information.
	CanView(ctx context.Context, subject OwnerInfo, process ProcessInfo) (bool, error)

	// CanControl checks if the subject can control the process.
	CanControl(ctx context.Context, subject OwnerInfo, process ProcessInfo, action ControlAction) (bool, error)

	// FilterVisible filters a list of processes to only those visible to the subject.
	FilterVisible(ctx context.Context, subject OwnerInfo, processes []ProcessInfo) ([]ProcessInfo, error)
}

// Manager coordinates multiple background processes.
type Manager interface {
	// Spawn creates and starts a new background process.
	Spawn(ctx context.Context, req SpawnRequest) (ProcessHandle, error)

	// Get retrieves a process executor by handle.
	Get(ctx context.Context, handle ProcessHandle) (ProcessExecutor, error)

	// GetInfo retrieves process info by handle (lighter weight than Get).
	GetInfo(ctx context.Context, handle ProcessHandle) (*ProcessInfo, error)

	// List returns all processes visible to the subject.
	List(ctx context.Context, subject OwnerInfo, filters ...Filter) ([]ProcessInfo, error)

	// Cancel terminates a process.
	Cancel(ctx context.Context, handle ProcessHandle, subject OwnerInfo) error

	// Wait blocks until the process completes.
	Wait(ctx context.Context, handle ProcessHandle) (*ProcessResult, error)

	// GetOutput retrieves output from a process.
	GetOutput(ctx context.Context, handle ProcessHandle, subject OwnerInfo, opts LineQueryOpts) ([]OutputLine, error)

	// StreamOutput streams output from a process.
	StreamOutput(ctx context.Context, handle ProcessHandle, subject OwnerInfo) (<-chan OutputLine, error)

	// Cleanup removes completed/failed processes older than maxAge.
	Cleanup(maxAge time.Duration) (int, error)

	// Shutdown gracefully terminates all running processes.
	Shutdown(ctx context.Context) error

	// Stats returns manager statistics.
	Stats() ManagerStats
}

// ManagerStats contains statistics about the manager.
type ManagerStats struct {
	TotalSpawned   int64
	ActiveCount    int
	CompletedCount int64
	FailedCount    int64
	CancelledCount int64
}

// Filter for querying processes.
type Filter interface {
	Match(info ProcessInfo) bool
}

// StateFilter filters by process state.
type StateFilter struct {
	States []ProcessState
}

// Match returns true if the process is in one of the filter states.
func (f StateFilter) Match(info ProcessInfo) bool {
	return slices.Contains(f.States, info.State)
}

// TagFilter filters by process tags.
type TagFilter struct {
	Tags map[string]string
}

// Match returns true if the process has all the specified tags.
func (f TagFilter) Match(info ProcessInfo) bool {
	for k, v := range f.Tags {
		if info.Tags[k] != v {
			return false
		}
	}
	return true
}

// OwnerFilter filters by owner attributes.
type OwnerFilter struct {
	UserID         string
	AgentID        string
	ConversationID string
}

// Match returns true if the process matches the owner filter.
func (f OwnerFilter) Match(info ProcessInfo) bool {
	if f.UserID != "" && info.Owner.UserID != f.UserID {
		return false
	}
	if f.AgentID != "" && info.Owner.AgentID != f.AgentID {
		return false
	}
	if f.ConversationID != "" && info.Owner.ConversationID != f.ConversationID {
		return false
	}
	return true
}

// BackgroundBashOutput is the structured JSON output format for the ReadBackgroundCommand tool.
type BackgroundBashOutput struct {
	TaskID          string         `json:"task_id"`
	Status          string         `json:"status"`
	Command         string         `json:"command"`
	PID             int            `json:"pid,omitempty"`
	StartedAt       string         `json:"started_at"`
	EndedAt         string         `json:"ended_at,omitempty"`
	DurationSeconds float64        `json:"duration_seconds"`
	ExitCode        *int           `json:"exit_code,omitempty"`
	Output          []OutputLine   `json:"output,omitempty"`
	Metadata        OutputMetadata `json:"metadata"`
}

// OutputMetadata contains additional output information.
type OutputMetadata struct {
	WorkDir         string            `json:"cwd"`
	Env             map[string]string `json:"env,omitempty"`
	TotalLines      int               `json:"total_lines"`
	OutputTruncated bool              `json:"output_truncated"`
	// OutputFile is set whenever the full output has been written to a temp file.
	// When present, the "output" array contains only the tail; read this file for
	// the complete output. FilePath is the FileBuffer backing store (different use).
	OutputFile string `json:"output_file,omitempty"`
	FilePath   string `json:"file_path,omitempty"`

	// Heartbeat signals added 2026-04-27 after survey observed agents cancelling
	// healthy long-running network commands because polling returned no progress
	// indicator. LastOutputAt is the timestamp of the most recent line written.
	// SecondsSinceLastOutput gives the agent an "is this hung?" hint without
	// needing to diff timestamps itself. BytesWritten is the cumulative output
	//	size in bytes (non-decreasing).
	LastOutputAt           string  `json:"last_output_at,omitempty"`
	SecondsSinceLastOutput float64 `json:"seconds_since_last_output,omitempty"`
	BytesWritten           int64   `json:"bytes_written"`
}
