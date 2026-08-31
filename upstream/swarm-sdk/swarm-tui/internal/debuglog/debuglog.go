// Package debuglog provides a single, opt-in debug logging helper shared by the
// TUI packages.
//
// It centralizes the previously-duplicated private logDebug helpers (chat and
// hooks). By default no sink is configured, so Logf is a no-op and nothing is
// written to disk — this preserves the invariant that the TUI never
// unconditionally writes a debug log to the current working directory. A caller
// may install a sink via SetLogger (e.g. a file-backed *log.Logger) to route
// debug output somewhere; passing nil disables it again.
//
// The behavior mirrors the former hooks-package logger exactly: writes go
// through (*log.Logger).Printf when a sink is set, and are dropped otherwise.
package debuglog

import "log"

// logger is the optional sink. When nil, Logf is a no-op.
var logger *log.Logger

// SetLogger installs the shared debug logger sink. Passing nil disables logging.
func SetLogger(l *log.Logger) { logger = l }

// Logf formats and writes a debug message to the configured sink, if any.
func Logf(format string, args ...any) {
	if logger != nil {
		logger.Printf(format, args...)
	}
}
