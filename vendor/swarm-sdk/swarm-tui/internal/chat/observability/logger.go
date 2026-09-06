// Package observability provides TUI-friendly implementations of SDK observability interfaces
package observability

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	tuianalytics "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/analytics"
)

// LogCallback is called for each log message (for debug screen integration)
type LogCallback func(message string)

var globalLogCallback LogCallback

// SetLogCallback sets the global log callback for sending logs to debug screen
func SetLogCallback(callback LogCallback) {
	globalLogCallback = callback
}

// TUILogger implements observability.Logger by writing to debug log
type TUILogger struct {
	mu     sync.Mutex
	logger *log.Logger
	level  observability.Level
	fields []observability.Field
}

// NewTUILogger creates a logger that writes to ~/.swarmos/swarmos.log
func NewTUILogger() *TUILogger {
	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "."
	}

	// Ensure ~/.swarmos directory exists
	swarmosDir := fmt.Sprintf("%s/.swarmos", homeDir)
	if err := os.MkdirAll(swarmosDir, 0755); err != nil {
		// Fallback to stderr if directory can't be created
		return &TUILogger{
			logger: log.New(os.Stderr, "[SDK] ", log.Ltime|log.Lmicroseconds),
			level:  observability.LevelInfo,
			fields: make([]observability.Field, 0),
		}
	}

	// Open log file in ~/.swarmos/
	logPath := fmt.Sprintf("%s/swarmos.log", swarmosDir)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Fallback to stderr if file can't be opened
		return &TUILogger{
			logger: log.New(os.Stderr, "[SDK] ", log.Ltime|log.Lmicroseconds),
			level:  observability.LevelInfo,
			fields: make([]observability.Field, 0),
		}
	}

	return &TUILogger{
		logger: log.New(f, "[SDK] ", log.Ltime|log.Lmicroseconds),
		level:  observability.LevelInfo,
		fields: make([]observability.Field, 0),
	}
}

// Log emits a structured log entry
func (l *TUILogger) Log(ctx context.Context, level observability.Level, event string, fields ...observability.Field) {
	// Skip if below configured level
	if level < l.level {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Build log message
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("[%s] %s", level.String(), event))

	// Add fields
	allFields := append(l.fields, fields...)
	if len(allFields) > 0 {
		msg.WriteString(" |")
		for _, field := range allFields {
			msg.WriteString(fmt.Sprintf(" %s=%v", field.Key, field.Value))
		}
	}

	// Write to file
	l.logger.Println(msg.String())

	// ALSO send to debug screen via callback if available
	if globalLogCallback != nil {
		globalLogCallback(fmt.Sprintf("[SDK] %s", msg.String()))
	}
	if manager := tuianalytics.DefaultManager(); manager != nil {
		manager.CaptureLog(level, event, allFields)
	}
}

// Trace logs at trace level
func (l *TUILogger) Trace(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelTrace, event, fields...)
}

// Debug logs at debug level
func (l *TUILogger) Debug(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelDebug, event, fields...)
}

// Info logs at info level
func (l *TUILogger) Info(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelInfo, event, fields...)
}

// Warn logs at warn level
func (l *TUILogger) Warn(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelWarn, event, fields...)
}

// Error logs at error level
func (l *TUILogger) Error(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelError, event, fields...)
}

// Fatal logs at fatal level
func (l *TUILogger) Fatal(ctx context.Context, event string, fields ...observability.Field) {
	l.Log(ctx, observability.LevelFatal, event, fields...)
}

// WithFields returns a new logger with the given fields attached
func (l *TUILogger) WithFields(fields ...observability.Field) observability.Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	newFields := make([]observability.Field, len(l.fields)+len(fields))
	copy(newFields, l.fields)
	copy(newFields[len(l.fields):], fields)

	return &TUILogger{
		logger: l.logger,
		level:  l.level,
		fields: newFields,
	}
}

// SetLevel sets the minimum log level
func (l *TUILogger) SetLevel(level observability.Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}
