package serve

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// daemonLogBuf is an in-process ring buffer for daemon log lines.  Callers
// that have access to a *log.Logger (e.g. swarm-ic backend) wire their logger
// output here via SetDaemonLogWriter so daemon.getLogs has lines to return
// immediately without hitting disk.
var daemonLogBuf = newServeLogBuffer(500)

// SetDaemonLogWriter returns a writer that fans out every write to dst AND to
// the in-process log ring buffer.  Wire it as your logger's output:
//
//	logger.SetOutput(serve.SetDaemonLogWriter(logger.Writer()))
func SetDaemonLogWriter(dst io.Writer) io.Writer {
	return io.MultiWriter(dst, daemonLogBuf)
}

// serveLogBuffer is a goroutine-safe ring buffer for log lines.
type serveLogBuffer struct {
	mu   sync.RWMutex
	buf  []string
	cap  int
	head int
	n    int
}

func newServeLogBuffer(capacity int) *serveLogBuffer {
	return &serveLogBuffer{buf: make([]string, capacity), cap: capacity}
}

func (b *serveLogBuffer) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\n")
	parts := strings.Split(text, "\n")
	b.mu.Lock()
	for _, l := range parts {
		if l == "" {
			continue
		}
		b.buf[b.head] = l
		b.head = (b.head + 1) % b.cap
		if b.n < b.cap {
			b.n++
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

func (b *serveLogBuffer) tail(n int) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if n > b.n {
		n = b.n
	}
	if n == 0 {
		return []string{}
	}
	out := make([]string, n)
	start := (b.head - n + b.cap) % b.cap
	for i := 0; i < n; i++ {
		out[i] = b.buf[(start+i)%b.cap]
	}
	return out
}

func (b *serveLogBuffer) len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.n
}

// daemonLogFileCandidates returns paths to check for daemon log output,
// in priority order.  The global daemon spawned by ensureGlobalDaemon writes
// to /tmp/swarmos-global-daemon.log; system-service installations may write
// to the XDG state dir.
func daemonLogFileCandidates() []string {
	candidates := []string{
		filepath.Join(os.TempDir(), "swarmos-global-daemon.log"),
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "swarmos", "daemon.log"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "state", "swarmos", "daemon.log"),
		)
	}
	return candidates
}

// tailLogFile reads the last n lines from the first existing log file in the
// candidate list.  Returns nil when no log file is found.
func tailLogFile(n int) []string {
	for _, path := range daemonLogFileCandidates() {
		lines, err := tailFile(path, n)
		if err == nil {
			return lines
		}
	}
	return nil
}

// tailFile reads the last n lines of a file efficiently using a sliding window.
func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read all lines into a ring buffer of size n.
	ring := make([]string, n)
	head, count := 0, 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		ring[head] = scanner.Text()
		head = (head + 1) % n
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if count == 0 {
		return []string{}, nil
	}
	size := count
	if size > n {
		size = n
	}
	out := make([]string, size)
	start := (head - size + n) % n
	for i := 0; i < size; i++ {
		out[i] = ring[(start+i)%n]
	}
	return out, nil
}

// handleDaemonGetLogs serves the daemon.getLogs RPC method.
//
// Strategy:
//  1. Use the in-process ring buffer when it has been populated (callers that
//     wired serve.SetDaemonLogWriter into their logger).
//  2. Fall back to tailing the daemon log file on disk for standalone daemons
//     that write to /tmp/swarmos-global-daemon.log via stderr redirection.
//
// Params (all optional):
//
//	{ "n": 200 }   — number of tail lines to return (default 200, max 2000)
//
// Returns:
//
//	{ "lines": ["...", ...], "count": N, "source": "buffer"|"file"|"empty" }
func handleDaemonGetLogs(_ context.Context, _ *client.Client, raw json.RawMessage) (any, error) {
	var p struct {
		N int `json:"n"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		_ = json.Unmarshal(raw, &p)
	}
	n := p.N
	if n <= 0 {
		n = 200
	}
	if n > 2000 {
		n = 2000
	}

	// Prefer in-process buffer (fastest, no I/O).
	if daemonLogBuf.len() > 0 {
		lines := daemonLogBuf.tail(n)
		return map[string]any{"lines": lines, "count": len(lines), "source": "buffer"}, nil
	}

	// Fall back to disk — the standalone daemon writes stderr to a log file.
	if lines := tailLogFile(n); lines != nil {
		return map[string]any{"lines": lines, "count": len(lines), "source": "file"}, nil
	}

	return map[string]any{"lines": []string{}, "count": 0, "source": "empty"}, nil
}
