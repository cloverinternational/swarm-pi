package bgprocess

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
	"time"
)

// MemoryBuffer implements OutputBuffer with in-memory storage.
type MemoryBuffer struct {
	lines          []OutputLine
	maxSize        int64
	currSize       int64
	mu             sync.RWMutex
	closed         bool
	listeners      []chan OutputLine
	closedChannels map[chan OutputLine]bool
	listenerMu     sync.Mutex
}

// NewMemoryBuffer creates a new memory-backed output buffer.
func NewMemoryBuffer(maxSize int64) *MemoryBuffer {
	if maxSize <= 0 {
		maxSize = 10 * 1024 * 1024 // 10MB default
	}
	return &MemoryBuffer{
		lines:          make([]OutputLine, 0, 1000),
		maxSize:        maxSize,
		listeners:      make([]chan OutputLine, 0),
		closedChannels: make(map[chan OutputLine]bool),
	}
}

// Write implements io.Writer. Treats input as stdout.
func (b *MemoryBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, ErrBufferClosed
	}

	// Split into lines without using bufio.Scanner to avoid buffer size limits
	data := p
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		var line []byte
		if idx >= 0 {
			line = data[:idx]
			data = data[idx+1:]
		} else {
			line = data
			data = nil
		}
		// A newline-terminated line is real output even when it is empty.
		// Dropping empty lines here silently rewrote every command's output:
		// `cat file` delivered content the file did not contain, and patches
		// written from that view could never match the bytes on disk. Only a
		// trailing fragment with no newline may be skipped when empty, since
		// that is the absence of a line rather than an empty one.
		if idx >= 0 || len(line) > 0 {
			if err := b.writeLineLocked("stdout", string(line)); err != nil {
				return 0, err
			}
		}
	}

	return len(p), nil
}

// WriteLine writes a line with metadata.
func (b *MemoryBuffer) WriteLine(stream string, content string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBufferClosed
	}

	return b.writeLineLocked(stream, content)
}

// writeLineLocked writes a line (must hold lock).
func (b *MemoryBuffer) writeLineLocked(stream string, content string) error {
	lineSize := int64(len(content))

	// Check if we would exceed max size
	if b.currSize+lineSize > b.maxSize {
		// Remove oldest lines until we have space
		for b.currSize+lineSize > b.maxSize && len(b.lines) > 0 {
			removed := b.lines[0]
			b.lines = b.lines[1:]
			b.currSize -= int64(len(removed.Content))
		}
	}

	line := OutputLine{
		LineNumber: len(b.lines) + 1,
		Timestamp:  time.Now(),
		Stream:     stream,
		Content:    content,
	}

	b.lines = append(b.lines, line)
	b.currSize += lineSize

	// Notify listeners
	b.notifyListeners(line)

	return nil
}

// notifyListeners sends the line to all active listeners.
func (b *MemoryBuffer) notifyListeners(line OutputLine) {
	b.listenerMu.Lock()
	defer b.listenerMu.Unlock()

	for _, ch := range b.listeners {
		select {
		case ch <- line:
		default:
			// Listener is slow, skip
		}
	}
}

// Lines returns output lines matching the query options.
func (b *MemoryBuffer) Lines(opts LineQueryOpts) ([]OutputLine, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []OutputLine
	var pattern *regexp.Regexp
	var err error

	if opts.Pattern != "" {
		pattern, err = regexp.Compile(opts.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}
	}

	startIdx := 0
	if opts.FromLine > 0 {
		startIdx = opts.FromLine - 1 // Convert to 0-indexed
	}

	count := 0
	for i := startIdx; i < len(b.lines); i++ {
		line := b.lines[i]

		// Filter by stream
		if opts.Stream != "" && line.Stream != opts.Stream {
			continue
		}

		// Filter by pattern
		if pattern != nil && !pattern.MatchString(line.Content) {
			continue
		}

		result = append(result, line)
		count++

		// Limit results
		if opts.MaxLines > 0 && count >= opts.MaxLines {
			break
		}
	}

	return result, nil
}

// Tail returns the last n lines.
func (b *MemoryBuffer) Tail(n int) ([]OutputLine, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n <= 0 {
		return nil, nil
	}

	start := max(len(b.lines)-n, 0)

	result := make([]OutputLine, len(b.lines)-start)
	copy(result, b.lines[start:])
	return result, nil
}

// Since returns all output after a given timestamp.
func (b *MemoryBuffer) Since(t time.Time) ([]OutputLine, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []OutputLine
	for _, line := range b.lines {
		if line.Timestamp.After(t) {
			result = append(result, line)
		}
	}
	return result, nil
}

// Stream returns a channel that emits output lines as they arrive.
func (b *MemoryBuffer) Stream(ctx context.Context) (<-chan OutputLine, error) {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()

	if closed {
		return nil, ErrBufferClosed
	}

	ch := make(chan OutputLine, 100)

	b.listenerMu.Lock()
	b.listeners = append(b.listeners, ch)
	b.listenerMu.Unlock()

	// Cleanup when context is done
	go func() {
		<-ctx.Done()
		b.listenerMu.Lock()
		if !b.closedChannels[ch] {
			close(ch)
			b.closedChannels[ch] = true
		}
		b.listenerMu.Unlock()
		b.removeListener(ch)
	}()

	return ch, nil
}

// removeListener removes a listener channel and marks it as closed.
func (b *MemoryBuffer) removeListener(ch chan OutputLine) {
	b.listenerMu.Lock()
	defer b.listenerMu.Unlock()

	b.closedChannels[ch] = true
	for i, listener := range b.listeners {
		if listener == ch {
			// Remove from slice without closing (caller closes)
			b.listeners = append(b.listeners[:i], b.listeners[i+1:]...)
			return
		}
	}
}

// Size returns the total bytes stored.
func (b *MemoryBuffer) Size() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currSize
}

// LineCount returns the total number of lines.
func (b *MemoryBuffer) LineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.lines)
}

// Flush is a no-op for memory buffers.
func (b *MemoryBuffer) Flush() error {
	return nil
}

// Close closes the buffer and notifies all listeners.
func (b *MemoryBuffer) Close() error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	b.listenerMu.Lock()
	// Close all listener channels that haven't been closed yet
	for _, ch := range b.listeners {
		if !b.closedChannels[ch] {
			close(ch)
			b.closedChannels[ch] = true
		}
	}
	b.listeners = nil
	b.listenerMu.Unlock()

	return nil
}

// FileBuffer implements OutputBuffer with file-backed storage.
type FileBuffer struct {
	file           *os.File
	path           string
	lines          []OutputLine // Also keep in memory for fast queries
	maxSize        int64
	currSize       int64
	mu             sync.RWMutex
	closed         bool
	listeners      []chan OutputLine
	closedChannels map[chan OutputLine]bool
	listenerMu     sync.Mutex
}

// NewFileBuffer creates a new file-backed output buffer.
func NewFileBuffer(path string, maxSize int64) (*FileBuffer, error) {
	if maxSize <= 0 {
		maxSize = 100 * 1024 * 1024 // 100MB default for file
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}

	return &FileBuffer{
		file:           file,
		path:           path,
		lines:          make([]OutputLine, 0, 1000),
		maxSize:        maxSize,
		listeners:      make([]chan OutputLine, 0),
		closedChannels: make(map[chan OutputLine]bool),
	}, nil
}

// Write implements io.Writer.
func (b *FileBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, ErrBufferClosed
	}

	// Write to file
	n, err = b.file.Write(p)
	if err != nil {
		return n, err
	}

	// Also parse into lines without using bufio.Scanner to avoid buffer size limits
	data := p
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		var lineContent []byte
		if idx >= 0 {
			lineContent = data[:idx]
			data = data[idx+1:]
		} else {
			lineContent = data
			data = nil
		}
		// See MemoryBuffer.Write: an empty newline-terminated line is content.
		if idx >= 0 || len(lineContent) > 0 {
			line := OutputLine{
				LineNumber: len(b.lines) + 1,
				Timestamp:  time.Now(),
				Stream:     "stdout",
				Content:    string(lineContent),
			}
			b.lines = append(b.lines, line)
			b.currSize += int64(len(line.Content))
			b.notifyListeners(line)
		}
	}

	return n, nil
}

// WriteLine writes a line with metadata.
func (b *FileBuffer) WriteLine(stream string, content string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return ErrBufferClosed
	}

	// Write to file with metadata prefix
	_, err := fmt.Fprintf(b.file, "[%s][%s] %s\n", time.Now().Format(time.RFC3339Nano), stream, content)
	if err != nil {
		return fmt.Errorf("failed to write to file: %w", err)
	}

	line := OutputLine{
		LineNumber: len(b.lines) + 1,
		Timestamp:  time.Now(),
		Stream:     stream,
		Content:    content,
	}
	b.lines = append(b.lines, line)
	b.currSize += int64(len(content))

	b.notifyListeners(line)

	return nil
}

// notifyListeners sends the line to all active listeners.
func (b *FileBuffer) notifyListeners(line OutputLine) {
	b.listenerMu.Lock()
	defer b.listenerMu.Unlock()

	for _, ch := range b.listeners {
		select {
		case ch <- line:
		default:
			// Listener is slow, skip
		}
	}
}

// Lines returns output lines matching the query options.
func (b *FileBuffer) Lines(opts LineQueryOpts) ([]OutputLine, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []OutputLine
	var pattern *regexp.Regexp
	var err error

	if opts.Pattern != "" {
		pattern, err = regexp.Compile(opts.Pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}
	}

	startIdx := 0
	if opts.FromLine > 0 {
		startIdx = opts.FromLine - 1
	}

	count := 0
	for i := startIdx; i < len(b.lines); i++ {
		line := b.lines[i]

		if opts.Stream != "" && line.Stream != opts.Stream {
			continue
		}

		if pattern != nil && !pattern.MatchString(line.Content) {
			continue
		}

		result = append(result, line)
		count++

		if opts.MaxLines > 0 && count >= opts.MaxLines {
			break
		}
	}

	return result, nil
}

// Tail returns the last n lines.
func (b *FileBuffer) Tail(n int) ([]OutputLine, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n <= 0 {
		return nil, nil
	}

	start := max(len(b.lines)-n, 0)

	result := make([]OutputLine, len(b.lines)-start)
	copy(result, b.lines[start:])
	return result, nil
}

// Since returns all output after a given timestamp.
func (b *FileBuffer) Since(t time.Time) ([]OutputLine, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []OutputLine
	for _, line := range b.lines {
		if line.Timestamp.After(t) {
			result = append(result, line)
		}
	}
	return result, nil
}

// Stream returns a channel that emits output lines as they arrive.
func (b *FileBuffer) Stream(ctx context.Context) (<-chan OutputLine, error) {
	b.mu.RLock()
	closed := b.closed
	b.mu.RUnlock()

	if closed {
		return nil, ErrBufferClosed
	}

	ch := make(chan OutputLine, 100)

	b.listenerMu.Lock()
	b.listeners = append(b.listeners, ch)
	b.listenerMu.Unlock()

	go func() {
		<-ctx.Done()
		b.listenerMu.Lock()
		if !b.closedChannels[ch] {
			close(ch)
			b.closedChannels[ch] = true
		}
		b.listenerMu.Unlock()
		b.removeListener(ch)
	}()

	return ch, nil
}

// removeListener removes a listener channel and marks it as closed.
func (b *FileBuffer) removeListener(ch chan OutputLine) {
	b.listenerMu.Lock()
	defer b.listenerMu.Unlock()

	b.closedChannels[ch] = true
	for i, listener := range b.listeners {
		if listener == ch {
			b.listeners = append(b.listeners[:i], b.listeners[i+1:]...)
			return
		}
	}
}

// Size returns the total bytes stored.
func (b *FileBuffer) Size() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.currSize
}

// LineCount returns the total number of lines.
func (b *FileBuffer) LineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.lines)
}

// Flush syncs the file to disk.
func (b *FileBuffer) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.file.Sync()
}

// Close closes the file and cleans up.
func (b *FileBuffer) Close() error {
	b.mu.Lock()
	b.closed = true
	err := b.file.Close()
	b.mu.Unlock()

	b.listenerMu.Lock()
	// Close all listener channels that haven't been closed yet
	for _, ch := range b.listeners {
		if !b.closedChannels[ch] {
			close(ch)
			b.closedChannels[ch] = true
		}
	}
	b.listeners = nil
	b.listenerMu.Unlock()

	return err
}

// Path returns the file path.
func (b *FileBuffer) Path() string {
	return b.path
}

// NewOutputBuffer creates an output buffer based on the configuration.
func NewOutputBuffer(config OutputBufferConfig) (OutputBuffer, error) {
	switch config.Type {
	case BufferFile:
		if config.FilePath == "" {
			return nil, fmt.Errorf("file_path required for file buffer")
		}
		return NewFileBuffer(config.FilePath, config.MaxSize)
	case BufferMemory, "":
		return NewMemoryBuffer(config.MaxSize), nil
	case BufferHybrid:
		// For now, hybrid falls back to memory
		// TODO: Implement hybrid buffer with file spillover
		return NewMemoryBuffer(config.MaxSize), nil
	default:
		return nil, fmt.Errorf("unknown buffer type: %s", config.Type)
	}
}

// MultiWriter wraps an OutputBuffer to capture both stdout and stderr.
type MultiWriter struct {
	buffer OutputBuffer
	stream string
}

// NewStdoutWriter creates a writer that tags output as stdout.
func NewStdoutWriter(buf OutputBuffer) io.Writer {
	return &MultiWriter{buffer: buf, stream: "stdout"}
}

// NewStderrWriter creates a writer that tags output as stderr.
func NewStderrWriter(buf OutputBuffer) io.Writer {
	return &MultiWriter{buffer: buf, stream: "stderr"}
}

// Write implements io.Writer.
func (w *MultiWriter) Write(p []byte) (n int, err error) {
	// Split into lines without using bufio.Scanner to avoid buffer size limits
	data := p
	for len(data) > 0 {
		idx := bytes.IndexByte(data, '\n')
		var line []byte
		if idx >= 0 {
			line = data[:idx]
			data = data[idx+1:]
		} else {
			line = data
			data = nil
		}
		// See MemoryBuffer.Write: an empty newline-terminated line is content.
		if idx >= 0 || len(line) > 0 {
			if err := w.buffer.WriteLine(w.stream, string(line)); err != nil {
				return 0, err
			}
		}
	}
	return len(p), nil
}
