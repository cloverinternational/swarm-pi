package profiling

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

// LoggingProfiler wraps another profiler and logs events to a JSONL file.
// It writes to ~/.swarm/logs/cache_profiling.log
type LoggingProfiler struct {
	delegate CacheProfiler
	file     *os.File
	writer   *bufio.Writer
	mu       sync.Mutex
}

// NewLoggingProfiler creates a profiler that logs events to a file.
// delegate is the underlying profiler implementation (e.g., Validator).
func NewLoggingProfiler(delegate CacheProfiler) (*LoggingProfiler, error) {
	// Resolve the canonical log path (~/.swarm/logs/cache_profiling.log); the
	// parent directory is ensured by paths.In. This is an append-only JSONL log
	// so it is opened directly (atomicfile is for atomic full-file replaces).
	logPath := paths.In("logs", "cache_profiling.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		// Fallback: no logging
		return &LoggingProfiler{
			delegate: delegate,
			file:     nil,
			writer:   nil,
		}, nil
	}

	return &LoggingProfiler{
		delegate: delegate,
		file:     f,
		writer:   bufio.NewWriter(f),
	}, nil
}

// log writes a structured event to the log file.
func (lp *LoggingProfiler) log(event any) {
	if lp.writer == nil {
		// Logging not available
		return
	}

	lp.mu.Lock()
	defer lp.mu.Unlock()

	// Marshal to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	// Write as JSONL (one line per entry)
	if _, err := lp.writer.Write(data); err != nil {
		return
	}
	if _, err := lp.writer.WriteString("\n"); err != nil {
		return
	}

	// Flush every write to ensure data is written
	_ = lp.writer.Flush() // profiling flush; non-fatal if it fails
}

// BeforeTranslation delegates to underlying profiler and logs.
func (lp *LoggingProfiler) BeforeTranslation(ctx context.Context, messages any) (string, error) {
	hash, err := lp.delegate.BeforeTranslation(ctx, messages)

	lp.log(map[string]any{
		"ts":         time.Now().Format(time.RFC3339Nano),
		"event_type": "BEFORE_TRANSLATION",
		"hash":       hash,
		"error":      errToString(err),
	})

	return hash, err
}

// AfterTranslation delegates to underlying profiler and logs.
func (lp *LoggingProfiler) AfterTranslation(ctx context.Context, preHash string, translatedJSON any) error {
	err := lp.delegate.AfterTranslation(ctx, preHash, translatedJSON)

	lp.log(map[string]any{
		"ts":         time.Now().Format(time.RFC3339Nano),
		"event_type": "AFTER_TRANSLATION",
		"pre_hash":   preHash,
		"error":      errToString(err),
	})

	return err
}

// TrackMutation delegates and logs.
func (lp *LoggingProfiler) TrackMutation(ctx context.Context, event *MutationEvent) error {
	err := lp.delegate.TrackMutation(ctx, event)

	lp.log(map[string]any{
		"ts":         time.Now().Format(time.RFC3339Nano),
		"event_type": "MUTATION_TRACKED",
		"message_id": event.MessageID,
		"type":       event.Type,
		"location":   event.Location,
		"field":      event.FieldModified,
		"reason":     event.ReasonModified,
		"error":      errToString(err),
	})

	return err
}

// RecordAPIResponse delegates and logs.
func (lp *LoggingProfiler) RecordAPIResponse(ctx context.Context, metrics *APIMetrics) error {
	err := lp.delegate.RecordAPIResponse(ctx, metrics)

	lp.log(map[string]any{
		"ts":                    time.Now().Format(time.RFC3339Nano),
		"event_type":            "API_RESPONSE",
		"provider":              metrics.Provider,
		"model":                 metrics.Model,
		"request_tokens":        metrics.RequestTokens,
		"output_tokens":         metrics.OutputTokens,
		"cache_creation_tokens": metrics.CacheCreationTokens,
		"cache_read_tokens":     metrics.CacheReadTokens,
		"cache_hit":             metrics.CacheHitDetected,
		"message_count":         metrics.MessageCount,
		"response_time_ms":      metrics.ResponseTimeMS,
		"error":                 errToString(err),
	})

	return err
}

// GetMetrics delegates to underlying profiler.
func (lp *LoggingProfiler) Metrics(conversationID string) *CacheMetrics {
	return lp.delegate.Metrics(conversationID)
}

// GenerateReport delegates to underlying profiler and logs.
func (lp *LoggingProfiler) GenerateReport(conversationID string) *CacheBreakReport {
	report := lp.delegate.GenerateReport(conversationID)

	lp.log(map[string]any{
		"ts":              time.Now().Format(time.RFC3339Nano),
		"event_type":      "REPORT_GENERATED",
		"conversation_id": conversationID,
		"total_breaks":    report.Summary.CacheBreaks,
		"cache_hit_rate":  report.Summary.CacheHitPercent,
		"break_impact":    report.Efficiency.BreakImpact,
		"recommendations": len(report.Recommendations),
	})

	return report
}

// ExportMetrics delegates to underlying profiler and logs.
func (lp *LoggingProfiler) ExportMetrics(ctx context.Context, conversationID string, format string) (any, error) {
	data, err := lp.delegate.ExportMetrics(ctx, conversationID, format)

	lp.log(map[string]any{
		"ts":              time.Now().Format(time.RFC3339Nano),
		"event_type":      "EXPORT_METRICS",
		"conversation_id": conversationID,
		"format":          format,
		"error":           errToString(err),
	})

	return data, err
}

// Reset delegates to underlying profiler and logs.
func (lp *LoggingProfiler) Reset(conversationID string) error {
	err := lp.delegate.Reset(conversationID)

	lp.log(map[string]any{
		"ts":              time.Now().Format(time.RFC3339Nano),
		"event_type":      "RESET",
		"conversation_id": conversationID,
		"error":           errToString(err),
	})

	return err
}

// Close closes the log file.
func (lp *LoggingProfiler) Close() error {
	lp.mu.Lock()
	defer lp.mu.Unlock()

	if lp.writer != nil {
		_ = lp.writer.Flush() // profiling flush; non-fatal if it fails
	}

	if lp.file != nil {
		return lp.file.Close()
	}

	return nil
}

// Helper

func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
