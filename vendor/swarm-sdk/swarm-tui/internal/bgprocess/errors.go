package bgprocess

import (
	"errors"
	"fmt"
)

// Sentinel errors for error checking with errors.Is().
var (
	ErrProcessNotFound      = errors.New("process not found")
	ErrProcessAlreadyExists = errors.New("process already exists")
	ErrInvalidState         = errors.New("invalid state transition")
	ErrPermissionDenied     = errors.New("permission denied")
	ErrCommandFailed        = errors.New("command execution failed")
	ErrTimeout              = errors.New("process execution timeout")
	ErrBufferFull           = errors.New("output buffer full")
	ErrBufferClosed         = errors.New("output buffer closed")
	ErrInvalidHandle        = errors.New("invalid process handle")
	ErrManagerShutdown      = errors.New("manager is shutting down")
)

// ProcessError wraps errors with process context.
type ProcessError struct {
	Handle  ProcessHandle
	Op      string // Operation that failed
	Err     error  // Underlying error
	Message string // Human-readable message
}

// Error implements the error interface.
func (e *ProcessError) Error() string {
	if e.Handle.IsZero() {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Message, e.Err)
	}
	return fmt.Sprintf("%s [%s]: %s: %v", e.Op, e.Handle.ID(), e.Message, e.Err)
}

// Unwrap returns the underlying error for errors.Is/As.
func (e *ProcessError) Unwrap() error {
	return e.Err
}

// NewProcessError creates a new ProcessError.
func NewProcessError(handle ProcessHandle, op string, err error, msg string) *ProcessError {
	return &ProcessError{
		Handle:  handle,
		Op:      op,
		Err:     err,
		Message: msg,
	}
}

// WrapSpawnError wraps an error that occurred during process spawning.
func WrapSpawnError(err error, msg string) error {
	return &ProcessError{
		Op:      "spawn",
		Err:     err,
		Message: msg,
	}
}

// WrapExecutionError wraps an error that occurred during process execution.
func WrapExecutionError(handle ProcessHandle, err error, msg string) error {
	return &ProcessError{
		Handle:  handle,
		Op:      "execute",
		Err:     err,
		Message: msg,
	}
}

// WrapStateError wraps an error related to state transitions.
func WrapStateError(handle ProcessHandle, from, to ProcessState) error {
	return &ProcessError{
		Handle:  handle,
		Op:      "state_transition",
		Err:     ErrInvalidState,
		Message: fmt.Sprintf("cannot transition from %s to %s", from, to),
	}
}

// WrapVisibilityError wraps an error related to visibility/permission checks.
func WrapVisibilityError(handle ProcessHandle, action ControlAction, subject OwnerInfo) error {
	return &ProcessError{
		Handle:  handle,
		Op:      "visibility_check",
		Err:     ErrPermissionDenied,
		Message: fmt.Sprintf("subject %s/%s denied action %s", subject.UserID, subject.AgentID, action),
	}
}

// WrapOutputError wraps an error related to output buffer operations.
func WrapOutputError(handle ProcessHandle, err error, msg string) error {
	return &ProcessError{
		Handle:  handle,
		Op:      "output",
		Err:     err,
		Message: msg,
	}
}

// IsNotFound returns true if the error indicates a process was not found.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrProcessNotFound)
}

// IsPermissionDenied returns true if the error indicates permission was denied.
func IsPermissionDenied(err error) bool {
	return errors.Is(err, ErrPermissionDenied)
}

// IsInvalidState returns true if the error indicates an invalid state transition.
func IsInvalidState(err error) bool {
	return errors.Is(err, ErrInvalidState)
}

// IsTimeout returns true if the error indicates a timeout.
func IsTimeout(err error) bool {
	return errors.Is(err, ErrTimeout)
}

// ValidationError represents a validation failure.
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// NewValidationError creates a new ValidationError.
func NewValidationError(field, message string) *ValidationError {
	return &ValidationError{
		Field:   field,
		Message: message,
	}
}

// ValidateSpawnRequest validates a SpawnRequest.
func ValidateSpawnRequest(req SpawnRequest) error {
	if req.Command == "" {
		return NewValidationError("command", "command is required")
	}
	if req.Owner.UserID == "" && req.Owner.AgentID == "" {
		return &SpawnValidationError{
			Field:   "owner",
			Message: "either user_id or agent_id is required",
			Command: req.Command,
			Tags:    req.Tags,
		}
	}
	if req.Timeout < 0 {
		return NewValidationError("timeout", "timeout cannot be negative")
	}
	if req.OutputConfig.MaxSize < 0 {
		return NewValidationError("output_config.max_size", "max_size cannot be negative")
	}
	return nil
}

// SpawnValidationError is a structured error for owner validation failures.
// It includes command and tag context to help with Sentry tracing and debugging.
type SpawnValidationError struct {
	Field   string
	Message string
	Command string            // The command that was being spawned
	Tags    map[string]string // Tags from the spawn request (e.g., source, mode)
}

// truncateCommand truncates a command for logging.
func truncateCommand(cmd string) string {
	const maxLen = 100
	if len(cmd) <= maxLen {
		return cmd
	}
	return cmd[:maxLen] + "..."
}

// Error implements the error interface with full context for observability.
func (e *SpawnValidationError) Error() string {
	source := e.Tags["source"]
	mode := e.Tags["mode"]
	return fmt.Sprintf("spawn validation error: %s: %s (command=%q, source=%s, mode=%s)",
		e.Field, e.Message, truncateCommand(e.Command), source, mode)
}
