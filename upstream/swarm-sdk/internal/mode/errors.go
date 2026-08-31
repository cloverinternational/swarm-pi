package mode

import "fmt"

// ErrorType represents different categories of mode errors.
type ErrorType string

const (
	// ErrorTypeValidation indicates validation failure
	ErrorTypeValidation ErrorType = "validation"

	// ErrorTypeExecution indicates execution failure
	ErrorTypeExecution ErrorType = "execution"

	// ErrorTypeTimeout indicates timeout exceeded
	ErrorTypeTimeout ErrorType = "timeout"

	// ErrorTypeSteering indicates steering decision blocked execution
	ErrorTypeSteering ErrorType = "steering"

	// ErrorTypeDependency indicates dependency resolution failure
	ErrorTypeDependency ErrorType = "dependency"

	// ErrorTypeNotFound indicates mode/group not found
	ErrorTypeNotFound ErrorType = "not_found"

	// ErrorTypeConflict indicates conflicting configuration
	ErrorTypeConflict ErrorType = "conflict"
)

// ModeError represents an error in the mode system.
type ModeError struct {
	// Type of error
	Type ErrorType

	// Error message
	Message string

	// Mode ID where error occurred
	ModeID string

	// Group ID where error occurred (if applicable)
	GroupID string

	// Agent ID where error occurred (if applicable)
	AgentID string

	// Underlying error (if any)
	Err error

	// Additional context
	Context map[string]any
}

// Error implements the error interface.
func (e *ModeError) Error() string {
	msg := fmt.Sprintf("mode error [%s]: %s", e.Type, e.Message)

	if e.ModeID != "" {
		msg += fmt.Sprintf(" (mode: %s)", e.ModeID)
	}

	if e.GroupID != "" {
		msg += fmt.Sprintf(" (group: %s)", e.GroupID)
	}

	if e.AgentID != "" {
		msg += fmt.Sprintf(" (agent: %s)", e.AgentID)
	}

	if e.Err != nil {
		msg += fmt.Sprintf(": %v", e.Err)
	}

	return msg
}

// Unwrap returns the underlying error.
func (e *ModeError) Unwrap() error {
	return e.Err
}

// IsValidationError returns true if error is a validation error.
func IsValidationError(err error) bool {
	if modeErr, ok := err.(*ModeError); ok {
		return modeErr.Type == ErrorTypeValidation
	}
	return false
}

// IsExecutionError returns true if error is an execution error.
func IsExecutionError(err error) bool {
	if modeErr, ok := err.(*ModeError); ok {
		return modeErr.Type == ErrorTypeExecution
	}
	return false
}

// IsTimeoutError returns true if error is a timeout error.
func IsTimeoutError(err error) bool {
	if modeErr, ok := err.(*ModeError); ok {
		return modeErr.Type == ErrorTypeTimeout
	}
	return false
}

// IsSteeringError returns true if error is a steering error.
func IsSteeringError(err error) bool {
	if modeErr, ok := err.(*ModeError); ok {
		return modeErr.Type == ErrorTypeSteering
	}
	return false
}

// IsDependencyError returns true if error is a dependency error.
func IsDependencyError(err error) bool {
	if modeErr, ok := err.(*ModeError); ok {
		return modeErr.Type == ErrorTypeDependency
	}
	return false
}

// IsNotFoundError returns true if error is a not found error.
func IsNotFoundError(err error) bool {
	if modeErr, ok := err.(*ModeError); ok {
		return modeErr.Type == ErrorTypeNotFound
	}
	return false
}
