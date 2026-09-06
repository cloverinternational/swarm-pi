package builtin

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// newBashForTest creates a BashTool inner type for testing streaming directly.
// Use tools.Typed[BashParams](newBashForTest()) to get a full tools.Tool.
func newBashForTest() *BashTool {
	if DefaultBashConfig().Shell == "" {
		return &BashTool{shell: "/bin/bash"}
	}
	return &BashTool{shell: "/bin/bash"}
}

// ── Zero-latency delivery ─────────────────────────────────────────────────────

// TestExecuteStreaming_FirstLineArrivesBeforeCommandFinishes proves that the
// pipe-based implementation delivers output mid-execution, not only at the end.
// It runs a command that emits one line, sleeps briefly, then emits another.
// The first chunk must arrive well before the command finishes.
func TestExecuteStreaming_FirstLineArrivesBeforeCommandFinishes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	var mu sync.Mutex
	var firstChunkAt time.Time
	var chunks []string

	start := time.Now()

	params := BashParams{
		Command:        "echo first && sleep 0.2 && echo second",
		TimeoutSeconds: 60,
	}

	_, err := tool.RunStreaming(ctx, params, func(chunk, stream string) {
		mu.Lock()
		defer mu.Unlock()
		if firstChunkAt.IsZero() {
			firstChunkAt = time.Now()
		}
		chunks = append(chunks, chunk)
	})
	if err != nil {
		t.Fatalf("RunStreaming error: %v", err)
	}

	totalDuration := time.Since(start)

	mu.Lock()
	defer mu.Unlock()

	if firstChunkAt.IsZero() {
		t.Fatal("onOutput was never called")
	}

	firstChunkDelay := firstChunkAt.Sub(start)

	// The first line ("first\n") must arrive well before the 200ms sleep finishes.
	// With pipes it arrives in <10ms on any modern system.
	// We allow up to 150ms to leave headroom for CI.
	if firstChunkDelay > 150*time.Millisecond {
		t.Errorf("first chunk arrived after %v — expected <150ms (pipe should be near-instant, not 200ms command duration)", firstChunkDelay)
	}

	t.Logf("✓ first chunk in %v, total in %v, %d chunks", firstChunkDelay, totalDuration, len(chunks))

	// Both lines must be present in the combined output
	combined := strings.Join(chunks, "")
	if !strings.Contains(combined, "first") {
		t.Errorf("missing 'first' in output: %q", combined)
	}
	if !strings.Contains(combined, "second") {
		t.Errorf("missing 'second' in output: %q", combined)
	}
}

// ── No trailing newline ───────────────────────────────────────────────────────

// TestExecuteStreaming_NoTrailingNewline verifies that the final line of output
// is delivered even when the command produces no trailing newline character.
// bufio.Reader.ReadString returns partial content on io.EOF; we must not drop it.
func TestExecuteStreaming_NoTrailingNewline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	var received []string
	params := BashParams{
		Command:        "printf 'no-newline-here'",
		TimeoutSeconds: 60,
	}

	result, err := tool.RunStreaming(ctx, params, func(chunk, stream string) {
		received = append(received, chunk)
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}

	combined := strings.Join(received, "")
	if !strings.Contains(combined, "no-newline-here") {
		t.Errorf("expected 'no-newline-here' in streamed chunks, got: %q", combined)
	}
	if !strings.Contains(result.Output, "no-newline-here") {
		t.Errorf("expected 'no-newline-here' in final result, got: %q", result.Output)
	}

	t.Logf("✓ final line without newline delivered: %d chunks", len(received))
}

// ── Line ordering ─────────────────────────────────────────────────────────────

// TestExecuteStreaming_MultiLineOrderPreserved verifies that lines arrive in the
// correct sequential order.
func TestExecuteStreaming_MultiLineOrderPreserved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	var mu sync.Mutex
	var chunks []string

	params := BashParams{
		Command:        `for i in 1 2 3 4 5; do echo "line$i"; done`,
		TimeoutSeconds: 60,
	}

	_, err := tool.RunStreaming(ctx, params, func(chunk, stream string) {
		mu.Lock()
		chunks = append(chunks, chunk)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}

	combined := strings.Join(chunks, "")
	for i := 1; i <= 5; i++ {
		expected := "line" + string(rune('0'+i))
		if !strings.Contains(combined, expected) {
			t.Errorf("missing %q in output: %q", expected, combined)
		}
	}

	// Verify ordering — each "lineN" must appear before "lineN+1"
	for i := 1; i < 5; i++ {
		a := "line" + string(rune('0'+i))
		b := "line" + string(rune('0'+i+1))
		posA := strings.Index(combined, a)
		posB := strings.Index(combined, b)
		if posA >= posB {
			t.Errorf("order violation: %q appears at %d, %q at %d", a, posA, b, posB)
		}
	}

	t.Logf("✓ 5 lines delivered in order: %d chunks", len(chunks))
}

// ── Context cancellation ─────────────────────────────────────────────────────

// TestExecuteStreaming_ContextCancellation verifies that cancelling the context
// stops the streaming command promptly.
func TestExecuteStreaming_ContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx, cancel := context.WithCancel(context.Background())

	var chunkCount int
	var mu sync.Mutex

	done := make(chan struct{})
	go func() {
		defer close(done)
		params := BashParams{
			Command:        "for i in $(seq 100); do echo line$i; sleep 0.05; done",
			TimeoutSeconds: 60,
		}
		tool.RunStreaming(ctx, params, func(chunk, stream string) { //nolint:errcheck
			mu.Lock()
			chunkCount++
			mu.Unlock()
		})
	}()

	// Let a few lines arrive, then cancel
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Should terminate well before the 100 * 50ms = 5s full duration
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("RunStreaming did not terminate within 3s after context cancellation")
	}

	mu.Lock()
	count := chunkCount
	mu.Unlock()

	// Should have received at least 1 chunk (we waited 200ms, lines every 50ms)
	// but far fewer than 100
	if count == 0 {
		t.Error("expected at least one chunk before cancellation")
	}
	if count >= 100 {
		t.Errorf("expected cancellation to stop early, but got all %d chunks", count)
	}

	t.Logf("✓ received %d chunks before cancellation", count)
}

// ── Stderr delivery ───────────────────────────────────────────────────────────

// TestExecuteStreaming_StderrDelivered verifies that stderr output is also
// streamed via the onOutput callback.
func TestExecuteStreaming_StderrDelivered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	var stderrChunks []string
	var mu sync.Mutex

	params := BashParams{
		Command:        "echo stdout-line; echo stderr-line >&2",
		TimeoutSeconds: 60,
	}

	_, _ = tool.RunStreaming(ctx, params, func(chunk, stream string) {
		if stream == "stderr" {
			mu.Lock()
			stderrChunks = append(stderrChunks, chunk)
			mu.Unlock()
		}
	})

	mu.Lock()
	combined := strings.Join(stderrChunks, "")
	mu.Unlock()

	if !strings.Contains(combined, "stderr-line") {
		t.Errorf("stderr not delivered via onOutput callback; got: %q", combined)
	}

	t.Logf("✓ stderr delivered: %d chunks", len(stderrChunks))
}

// ── Final result accumulation ─────────────────────────────────────────────────

// TestExecuteStreaming_FinalResultMatchesStreamedChunks verifies that the
// ToolResult returned after streaming contains the same content as the
// accumulated streamed chunks.
func TestExecuteStreaming_FinalResultMatchesStreamedChunks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only test")
	}

	tool := newBashForTest()
	ctx := context.Background()

	var mu sync.Mutex
	var chunks []string

	params := BashParams{
		Command:        "echo hello && echo world",
		TimeoutSeconds: 60,
	}

	result, err := tool.RunStreaming(ctx, params, func(chunk, stream string) {
		mu.Lock()
		chunks = append(chunks, chunk)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}

	combined := strings.TrimSpace(strings.Join(chunks, ""))

	// With XML output, result.Output is structured XML containing the stdout/stderr.
	// Verify the streamed content appears within the XML result.
	finalOutput := result.Output
	if !strings.Contains(finalOutput, combined) {
		t.Errorf("streamed content %q ≠ final result output %q", combined, finalOutput)
	}

	t.Logf("✓ streamed content matches final result: %q", finalOutput)
}
