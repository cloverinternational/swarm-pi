package hooks

import (
	"log"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/debuglog"
)

// SetDebugLogger sets the debug logger for the hooks package. It delegates to
// the shared debuglog package so all TUI debug logging routes through a single
// sink.
func SetDebugLogger(logger *log.Logger) {
	debuglog.SetLogger(logger)
}

// logDebug logs a debug message if a debug logger is configured, via the shared
// debuglog helper.
func logDebug(format string, args ...any) {
	debuglog.Logf(format, args...)
}
