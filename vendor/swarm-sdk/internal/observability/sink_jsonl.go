package observability

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JSONLSink persists trace events as JSONL.
type JSONLSink struct {
	mu            sync.Mutex
	file          *os.File
	writer        *bufio.Writer
	traceSequence map[string]int64
}

// NewJSONLSink creates a concurrency-safe JSONL sink at the requested path.
func NewJSONLSink(path string) (*JSONLSink, error) {
	if path == "" {
		return nil, fmt.Errorf("jsonl sink path is required")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create diagnostics directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open diagnostics sink: %w", err)
	}

	return &JSONLSink{
		file:          f,
		writer:        bufio.NewWriter(f),
		traceSequence: make(map[string]int64),
	}, nil
}

// WriteEvent writes one JSON object per line.
func (s *JSONLSink) WriteEvent(_ context.Context, event TraceEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	if event.TraceID != "" {
		s.traceSequence[event.TraceID]++
		event.Sequence = s.traceSequence[event.TraceID]
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal trace event: %w", err)
	}

	if _, err := s.writer.Write(encoded); err != nil {
		return fmt.Errorf("failed to write trace event: %w", err)
	}
	if err := s.writer.WriteByte('\n'); err != nil {
		return fmt.Errorf("failed to write trace separator: %w", err)
	}
	if err := s.writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush trace sink: %w", err)
	}

	return nil
}

// Close flushes and closes the underlying file.
func (s *JSONLSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.writer != nil {
		if err := s.writer.Flush(); err != nil {
			return err
		}
	}
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}
