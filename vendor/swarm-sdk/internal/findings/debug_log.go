package findings

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// AnalysisDebugLog writes a human-readable debug trace for one analysis batch.
// Each batch creates one file: <baseDir>/debug/analysis_<timestamp>.log
// All writes are best-effort -- errors are silently ignored so debug logging
// never disrupts the main pipeline.
type AnalysisDebugLog struct {
	file *os.File
	w    *bufio.Writer
}

// NewAnalysisDebugLog creates a debug log file for one analysis batch.
// Returns nil (not an error) if the directory cannot be created or the file
// cannot be opened -- callers should nil-check before use.
func NewAnalysisDebugLog(baseDir string) *AnalysisDebugLog {
	dir := filepath.Join(ExpandCacheDir(baseDir), "debug")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil
	}

	filename := fmt.Sprintf("analysis_%s.log", time.Now().Format("20060102_150405"))
	path := filepath.Join(dir, filename)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil
	}

	d := &AnalysisDebugLog{
		file: f,
		w:    bufio.NewWriter(f),
	}

	// Write header
	d.writef("=== FINDINGS ANALYSIS DEBUG LOG ===\n")
	d.writef("Timestamp: %s\n", time.Now().Format(time.RFC3339))
	d.writef("File: %s\n\n", path)

	return d
}

// Section writes a visible separator with a title.
func (d *AnalysisDebugLog) Section(title string) {
	if d == nil {
		return
	}
	d.writef("\n=== %s ===\n", strings.ToUpper(title))
}

// Subsection writes a smaller separator.
func (d *AnalysisDebugLog) Subsection(title string) {
	if d == nil {
		return
	}
	d.writef("\n--- %s ---\n", title)
}

// Write writes a labeled text block.
func (d *AnalysisDebugLog) Write(label, content string) {
	if d == nil {
		return
	}
	if content == "" {
		d.writef("%s: (empty)\n", label)
		return
	}
	// Truncate very long content
	display := content
	if len(display) > 2000 {
		display = display[:2000] + fmt.Sprintf("... (truncated, %d total bytes)", len(content))
	}
	d.writef("%s:\n%s\n", label, display)
}

// Writef writes a formatted line.
func (d *AnalysisDebugLog) Writef(format string, args ...any) {
	if d == nil {
		return
	}
	d.writef(format, args...)
}

// WriteJSON pretty-prints a value as indented JSON with a label.
func (d *AnalysisDebugLog) WriteJSON(label string, v any) {
	if d == nil {
		return
	}
	data, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		d.writef("%s: (JSON marshal error: %v)\n", label, err)
		return
	}
	display := string(data)
	if len(display) > 5000 {
		display = display[:5000] + fmt.Sprintf("\n  ... (truncated, %d total bytes)", len(data))
	}
	d.writef("%s:\n  %s\n", label, display)
}

// WriteList writes a numbered list of strings.
func (d *AnalysisDebugLog) WriteList(label string, items []string) {
	if d == nil {
		return
	}
	d.writef("%s (%d items):\n", label, len(items))
	for i, item := range items {
		display := item
		if len(display) > 300 {
			display = display[:300] + "..."
		}
		d.writef("  [%d] %s\n", i, display)
	}
}

// Writer returns the underlying writer so callers can write directly.
// Returns nil if the debug log is nil.
func (d *AnalysisDebugLog) Writer() *bufio.Writer {
	if d == nil {
		return nil
	}
	return d.w
}

// Close flushes and closes the debug log file.
func (d *AnalysisDebugLog) Close() {
	if d == nil {
		return
	}
	d.writef("\n=== END ===\n")
	d.w.Flush()
	d.file.Close()
}

// writef is the internal write helper -- swallows all errors.
func (d *AnalysisDebugLog) writef(format string, args ...any) {
	fmt.Fprintf(d.w, format, args...)
}
