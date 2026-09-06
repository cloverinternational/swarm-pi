package computeruse

import (
	"errors"
	"fmt"
)

// Common errors for the computeruse package.
var (
	// ErrUnsupportedPlatform indicates the current platform is not supported.
	ErrUnsupportedPlatform = errors.New("unsupported platform")

	// ErrNoDisplay indicates no display server is available.
	ErrNoDisplay = errors.New("no display server available")

	// ErrDisplayNotFound indicates the requested display was not found.
	ErrDisplayNotFound = errors.New("display not found")

	// ErrScreenshotFailed indicates screenshot capture failed.
	ErrScreenshotFailed = errors.New("screenshot failed")

	// ErrInputFailed indicates input operation failed.
	ErrInputFailed = errors.New("input operation failed")

	// ErrAppNotFound indicates the application was not found.
	ErrAppNotFound = errors.New("application not found")

	// ErrClipboardFailed indicates clipboard operation failed.
	ErrClipboardFailed = errors.New("clipboard operation failed")

	// ErrPermissionDenied indicates permission was denied.
	ErrPermissionDenied = errors.New("permission denied")

	// ErrToolNotAvailable indicates a required tool is not available.
	ErrToolNotAvailable = errors.New("required tool not available")

	// ErrInvalidCoordinate indicates coordinates are out of bounds.
	ErrInvalidCoordinate = errors.New("invalid coordinate")

	// ErrTimeout indicates an operation timed out.
	ErrTimeout = errors.New("operation timed out")

	// ErrCancelled indicates the operation was cancelled.
	ErrCancelled = errors.New("operation cancelled")
)

// ToolNotAvailableError indicates a specific required tool is not available.
type ToolNotAvailableError struct {
	Tool string
}

func (e *ToolNotAvailableError) Error() string {
	return fmt.Sprintf("required tool not available: %s", e.Tool)
}

func (e *ToolNotAvailableError) Unwrap() error {
	return ErrToolNotAvailable
}

// NewToolNotAvailableError creates a new ToolNotAvailableError.
func NewToolNotAvailableError(tool string) *ToolNotAvailableError {
	return &ToolNotAvailableError{Tool: tool}
}

// DisplayError indicates an issue with display operations.
type DisplayError struct {
	DisplayID int
	Err       error
}

func (e *DisplayError) Error() string {
	if e.DisplayID != 0 {
		return fmt.Sprintf("display %d error: %v", e.DisplayID, e.Err)
	}
	return fmt.Sprintf("display error: %v", e.Err)
}

func (e *DisplayError) Unwrap() error {
	return e.Err
}

// InputError indicates an issue with input operations.
type InputError struct {
	Operation string
	Err       error
}

func (e *InputError) Error() string {
	return fmt.Sprintf("input operation %q failed: %v", e.Operation, e.Err)
}

func (e *InputError) Unwrap() error {
	return e.Err
}

// CoordinateError indicates an issue with coordinates.
type CoordinateError struct {
	X, Y int
	Err  error
}

func (e *CoordinateError) Error() string {
	return fmt.Sprintf("coordinate (%d, %d) error: %v", e.X, e.Y, e.Err)
}

func (e *CoordinateError) Unwrap() error {
	return e.Err
}

// AppError indicates an issue with application operations.
type AppError struct {
	AppID string
	Err   error
}

func (e *AppError) Error() string {
	return fmt.Sprintf("app %q error: %v", e.AppID, e.Err)
}

func (e *AppError) Unwrap() error {
	return e.Err
}
