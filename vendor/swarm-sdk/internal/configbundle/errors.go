// Package configbundle provides a unified configuration system for Swarm.
package configbundle

import (
	"errors"
	"fmt"
)

// Common errors returned by the configbundle package.
var (
	// ErrNoProjectConfig indicates that no project config exists.
	ErrNoProjectConfig = errors.New("no project config found")

	// ErrNoGlobalConfig indicates that no global config exists.
	ErrNoGlobalConfig = errors.New("no global config found")

	// ErrInvalidConfig indicates that a config file is invalid.
	ErrInvalidConfig = errors.New("invalid config file")

	// ErrConfigNotFound indicates that a config file was not found.
	ErrConfigNotFound = errors.New("config file not found")

	// ErrSchemaMismatch indicates that a config file has an incompatible schema version.
	ErrSchemaMismatch = errors.New("config schema version mismatch")

	// ErrMigrationFailed indicates that config migration failed.
	ErrMigrationFailed = errors.New("config migration failed")

	// ErrSaveFailed indicates that saving a config file failed.
	ErrSaveFailed = errors.New("failed to save config")

	// ErrPermissionDenied indicates permission denied when accessing config.
	ErrPermissionDenied = errors.New("permission denied")
)

// ConfigError represents a detailed error with context.
type ConfigError struct {
	Op      string // Operation that failed
	Path    string // File path involved
	Err     error  // Underlying error
	Message string // Human-readable message
}

func (e *ConfigError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("%s: %s (%s): %v", e.Op, e.Message, e.Path, e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", e.Op, e.Message, e.Err)
}

func (e *ConfigError) Unwrap() error {
	return e.Err
}

// NewConfigError creates a new ConfigError.
func NewConfigError(op, path, message string, err error) *ConfigError {
	return &ConfigError{
		Op:      op,
		Path:    path,
		Err:     err,
		Message: message,
	}
}

// IsConfigNotFound returns true if the error indicates config not found.
func IsConfigNotFound(err error) bool {
	return errors.Is(err, ErrConfigNotFound) || errors.Is(err, ErrNoProjectConfig) || errors.Is(err, ErrNoGlobalConfig)
}
