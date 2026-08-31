// Package observability provides structured logging, metrics, and tracing interfaces.
// This is Ring 0 - pure types and interfaces with no external dependencies.
package observability

// Level represents the severity level of a log entry.
type Level int

const (
	// LevelTrace is for very detailed debugging information.
	// Use sparingly as it generates high volume of logs.
	LevelTrace Level = iota

	// LevelDebug is for detailed debugging information.
	// Useful during development and troubleshooting.
	LevelDebug

	// LevelInfo is for informational messages about normal operations.
	// Default level for production.
	LevelInfo

	// LevelWarn is for warning messages about potential issues.
	// System continues to function but attention may be needed.
	LevelWarn

	// LevelError is for error messages about failures.
	// System encountered an error but may continue operating.
	LevelError

	// LevelFatal is for fatal errors that require immediate shutdown.
	// System cannot continue operating.
	LevelFatal
)

// String returns the string representation of the log level.
func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "trace"
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	case LevelFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// ParseLevel converts a string to a Level.
// Returns LevelInfo if the string is not recognized.
func ParseLevel(s string) Level {
	switch s {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn":
		return LevelWarn
	case "error":
		return LevelError
	case "fatal":
		return LevelFatal
	default:
		return LevelInfo
	}
}
