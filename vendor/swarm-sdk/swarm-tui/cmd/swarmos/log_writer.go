package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// ANSI color codes for log categories.
const (
	colorReset    = "\033[0m"
	colorRed      = "\033[31m"
	colorGreen    = "\033[32m"
	colorYellow   = "\033[33m"
	colorBlue     = "\033[34m"
	colorMagenta  = "\033[35m"
	colorCyan     = "\033[36m"
	colorDim      = "\033[2m"
	colorDimWhite = "\033[2;37m"
	colorBold     = "\033[1m"
)

// LogCategory defines a named log category with a consistent color.
type LogCategory struct {
	Name  string
	Color string
}

var (
	catHook       = LogCategory{"hook", colorMagenta}
	catEngine     = LogCategory{"engine", colorBlue}
	catTokens     = LogCategory{"tokens", colorCyan}
	catTool       = LogCategory{"tool", colorYellow}
	catError      = LogCategory{"error", colorRed}
	catDebug      = LogCategory{"dbg", colorDim}
	catProxy      = LogCategory{"proxy", colorBlue}
	catMetrics    = LogCategory{"metrics", colorCyan}
	catContext    = LogCategory{"ctx", colorBlue}
	catHooks      = LogCategory{"hooks", colorMagenta}
	catSystem     = LogCategory{"sys", colorDim}
	catCompaction = LogCategory{"compact", colorCyan}
)

// logWriter provides timestamped, color-coded, category-prefixed log output.
// All methods are safe for concurrent use. Colors are automatically disabled
// when the underlying writer is not a terminal.
type logWriter struct {
	w         io.Writer
	mu        sync.Mutex
	useColor  bool
	verbose   bool
	debug     bool // --debug-to-stderr
	startTime time.Time
}

// newLogWriter creates a logWriter that writes to w.
// If w is an *os.File, it checks whether it's a terminal for color support.
func newLogWriter(w io.Writer, verbose, debugToStderr bool) *logWriter {
	useColor := false
	if f, ok := w.(*os.File); ok {
		useColor = isTerminal(f)
	}
	// Respect NO_COLOR (https://no-color.org/) and TERM=dumb
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		useColor = false
	}
	return &logWriter{
		w:         w,
		useColor:  useColor,
		verbose:   verbose,
		debug:     debugToStderr,
		startTime: time.Now(),
	}
}

// isTerminal returns true if f is a character device (i.e., a terminal).
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// timestamp returns the current time as HH:MM:SS.mmm.
func timestamp() string {
	return time.Now().Format("15:04:05.000")
}

// formatLine builds a complete log line with optional color.
func (l *logWriter) formatLine(cat LogCategory, level, format string, args ...interface{}) string {
	msg := fmt.Sprintf(format, args...)
	ts := timestamp()

	if l.useColor {
		return fmt.Sprintf("%s[%s]%s %s%s%s %s",
			colorDimWhite, ts, colorReset,
			cat.Color, cat.Name, colorReset,
			msg,
		)
	}
	return fmt.Sprintf("[%s] [%s] %s", ts, cat.Name, msg)
}

// Info writes an informational log line. Always shown.
func (l *logWriter) Info(cat LogCategory, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, l.formatLine(cat, "INFO", format, args...))
}

// Debug writes a debug log line. Only shown when verbose is enabled.
func (l *logWriter) Debug(cat LogCategory, format string, args ...interface{}) {
	if !l.verbose && !l.debug {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, l.formatLine(cat, "DBG", format, args...))
}

// Warn writes a warning log line. Always shown.
func (l *logWriter) Warn(cat LogCategory, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, l.formatLine(cat, "WARN", format, args...))
}

// Error writes an error log line. Always shown.
func (l *logWriter) Error(cat LogCategory, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, l.formatLine(cat, "ERR", format, args...))
}

// Engine writes an engine tracing line. Only shown with --debug-to-stderr.
func (l *logWriter) Engine(format string, args ...interface{}) {
	if !l.debug {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintln(l.w, l.formatLine(catEngine, "ENG", format, args...))
}

// Banner writes a multi-line banner box (like the startup info).
// Uses plain output with optional color on the title line only.
func (l *logWriter) Banner(lines []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range lines {
		if l.useColor && strings.Contains(line, "SwarmOS") {
			fmt.Fprintf(l.w, "%s%s%s\n", colorBold, line, colorReset)
		} else {
			fmt.Fprintln(l.w, line)
		}
	}
}

// Raw writes a raw string (no timestamp/category prefix).
// Used for things like tool output chunks that should not be decorated.
func (l *logWriter) Raw(format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, format, args...)
}
