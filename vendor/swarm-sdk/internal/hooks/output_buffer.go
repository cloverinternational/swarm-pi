package hooks

import (
	"sync"
	"time"
)

// OutputBuffer buffers output chunks and emits them based on hybrid throttling:
// emit at 1KB OR 100ms, whichever comes first.
// Spec: 020-hook-execution-realtime
type OutputBuffer struct {
	callback     func(chunk string, isStderr bool)
	stdoutBuf    []byte
	stderrBuf    []byte
	lastEmitTime time.Time
	mu           sync.Mutex

	// Thresholds
	sizeThreshold int           // 1KB
	timeThreshold time.Duration // 100ms
}

const (
	defaultSizeThreshold = 1024 // 1KB
	defaultTimeThreshold = 100 * time.Millisecond
)

// NewOutputBuffer creates a new output buffer with hybrid throttling.
// The callback is invoked with each chunk and a flag indicating stderr vs stdout.
func NewOutputBuffer(callback func(chunk string, isStderr bool)) *OutputBuffer {
	return &OutputBuffer{
		callback:      callback,
		stdoutBuf:     make([]byte, 0, defaultSizeThreshold),
		stderrBuf:     make([]byte, 0, defaultSizeThreshold),
		lastEmitTime:  time.Now(),
		sizeThreshold: defaultSizeThreshold,
		timeThreshold: defaultTimeThreshold,
	}
}

// Write writes data to the buffer. If the buffer reaches the size threshold (1KB),
// it immediately emits the chunk via the callback.
func (b *OutputBuffer) Write(data []byte, isStderr bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if isStderr {
		b.stderrBuf = append(b.stderrBuf, data...)
		b.checkAndEmitLocked(true)
	} else {
		b.stdoutBuf = append(b.stdoutBuf, data...)
		b.checkAndEmitLocked(false)
	}
}

// checkAndEmitLocked checks if the buffer has reached the size threshold
// and emits if needed. Must be called with mu held.
func (b *OutputBuffer) checkAndEmitLocked(isStderr bool) {
	var buf *[]byte
	if isStderr {
		buf = &b.stderrBuf
	} else {
		buf = &b.stdoutBuf
	}

	// Emit chunks while buffer exceeds threshold
	for len(*buf) >= b.sizeThreshold {
		chunk := (*buf)[:b.sizeThreshold]
		*buf = (*buf)[b.sizeThreshold:]

		// Reset the last emit time
		b.lastEmitTime = time.Now()

		// Invoke callback (note: callback invoked under lock, keep it fast)
		b.callback(string(chunk), isStderr)
	}
}

// Tick checks if the time threshold (100ms) has elapsed since the last emission.
// If so, and there's buffered data, it emits the data.
// Call this periodically (e.g., from a ticker goroutine).
func (b *OutputBuffer) Tick() {
	b.mu.Lock()
	defer b.mu.Unlock()

	elapsed := time.Since(b.lastEmitTime)
	if elapsed >= b.timeThreshold {
		// Emit any buffered stdout
		if len(b.stdoutBuf) > 0 {
			chunk := string(b.stdoutBuf)
			b.stdoutBuf = b.stdoutBuf[:0]
			b.lastEmitTime = time.Now()
			b.callback(chunk, false)
		}

		// Emit any buffered stderr
		if len(b.stderrBuf) > 0 {
			chunk := string(b.stderrBuf)
			b.stderrBuf = b.stderrBuf[:0]
			b.lastEmitTime = time.Now()
			b.callback(chunk, true)
		}
	}
}

// Flush emits any remaining buffered data regardless of thresholds.
// Call this when the hook execution completes.
func (b *OutputBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Emit any remaining stdout
	if len(b.stdoutBuf) > 0 {
		chunk := string(b.stdoutBuf)
		b.stdoutBuf = b.stdoutBuf[:0]
		b.callback(chunk, false)
	}

	// Emit any remaining stderr
	if len(b.stderrBuf) > 0 {
		chunk := string(b.stderrBuf)
		b.stderrBuf = b.stderrBuf[:0]
		b.callback(chunk, true)
	}

	b.lastEmitTime = time.Now()
}
