package safego

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestGoRecoversPanic proves that a panic inside the launched function does not
// crash the test binary (the process) — if the panic escaped, `go test` would
// abort with a fatal error instead of this test passing — and that the panic is
// reported through the supplied logger with the goroutine name and value.
func TestGoRecoversPanic(t *testing.T) {
	var mu sync.Mutex
	var logged string

	logf := func(format string, args ...any) {
		mu.Lock()
		logged = fmt.Sprintf(format, args...)
		mu.Unlock()
	}

	GoWithLogger("test.panic", logf, func() {
		panic("boom")
	})

	// The deferred recover+log runs during panic unwinding after fn returns, so
	// poll briefly for the log line to appear rather than racing on it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		got := logged
		mu.Unlock()
		if got != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("panic was not logged within timeout")
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(logged, "test.panic") {
		t.Errorf("log missing goroutine name; got %q", logged)
	}
	if !strings.Contains(logged, "boom") {
		t.Errorf("log missing panic value; got %q", logged)
	}
	if !strings.Contains(logged, "safego") {
		t.Errorf("log missing safego prefix; got %q", logged)
	}
}

// TestGoRunsNormally proves the happy path: fn executes and completes.
func TestGoRunsNormally(t *testing.T) {
	done := make(chan int, 1)
	Go("test.normal", func() {
		done <- 42
	})
	if got := <-done; got != 42 {
		t.Errorf("fn did not run correctly; got %d, want 42", got)
	}
}

// TestGoNilLoggerFallback proves a nil logf does not itself panic and that the
// process survives a panic routed through the standard-logger fallback.
func TestGoNilLoggerFallback(t *testing.T) {
	done := make(chan struct{})
	GoWithLogger("test.nillog", nil, func() {
		defer close(done)
		panic("nil-logger boom")
	})
	<-done
	// Reaching here means the process survived the panic with a nil logger.
	// Yield so the deferred recover completes before the test ends.
	time.Sleep(10 * time.Millisecond)
}
