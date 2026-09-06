package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/mock"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// newTestAgent creates a minimal *Agent for probe tests without
// importing agenttest (which would cause an import cycle).
func newTestAgent(id string) *Agent {
	prov := mock.NewProvider()
	ag, err := New(Config{
		Definition: &Definition{
			ID:       id,
			Provider: "mock",
			Model:    "mock-model",
		},
		Provider:     prov,
		ToolRegistry: tools.NewSimpleRegistry(noop.NewLogger(), noop.NewTracer()),
	})
	if err != nil {
		panic("newTestAgent: " + err.Error())
	}
	return ag
}

// TestProbe_EmitEventAfterClose directly tests that emitEvent panics
// when called after the events channel has been closed.
//
// We call emitEvent directly (not through ReportProgress) because
// ReportProgress has a status guard that returns early when
// status != StatusRunning. The actual race occurs when a concurrent
// goroutine reads status as Running BEFORE handleSuccess changes it
// to Completed — then the RUnlock happens, handleSuccess closes the
// channel, and the concurrent goroutine calls emitEvent on the closed
// channel → PANIC.
//
// This test simulates the post-close scenario directly.
func TestProbe_EmitEventAfterClose(t *testing.T) {
	t.Parallel()

	ag := newTestAgent("probe-emit-after-close")
	bg, err := NewBackgroundAgent(BackgroundAgentConfig{
		Agent:           ag,
		EventBufferSize: 16,
		Logger:          noop.NewLogger(),
		Tracer:          noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewBackgroundAgent: %v", err)
	}

	if err := bg.Start(context.Background(), "test task"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the agent to fully complete (events channel closed).
	<-bg.Done()

	// Now call emitEvent directly on the closed channel.
	// Before fix: this panics because sending on a closed channel
	// panics before select/default can evaluate.
	// After fix: emitEvent checks the closed flag and returns silently.
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				t.Logf("BUG REPRODUCED: emitEvent panicked after close: %v", r)
			}
		}()
		bg.emitEvent(BackgroundAgentEvent{
			AgentID:   ag.ID(),
			EventType: EventProgress,
			Status:    StatusRunning,
			Message:   "should not panic",
			Progress:  50,
			Timestamp: time.Now(),
		})
	}()

	if didPanic {
		t.Errorf("BUG REPRODUCED: emitEvent panicked when called after channel close — " +
			"send on closed channel before select/default could evaluate")
	}
}

// TestProbe_ReportProgressRacesCompletion stress-tests the race between
// a concurrent ReportProgress caller and the completion path.
//
// The race window:
//  1. Progress goroutine: RLock → read status=Running → RUnlock
//  2. Completion goroutine: Lock → set status=Completed → closeOnce.Do(close) → Unlock
//  3. Progress goroutine: calls emitEvent → PANIC (channel closed)
//
// To trigger this, we need the progress goroutine to read status=Running
// JUST BEFORE the completion goroutine changes it. We achieve this by
// having many concurrent ReportProgress goroutines while the agent runs.
func TestProbe_ReportProgressRacesCompletion(t *testing.T) {
	t.Parallel()

	const iterations = 100
	var panicCount atomic.Int32

	for i := range iterations {
		ag := newTestAgent("probe-race")
		bg, err := NewBackgroundAgent(BackgroundAgentConfig{
			Agent:           ag,
			EventBufferSize: 1, // Tiny buffer to maximize collision chance
			Logger:          noop.NewLogger(),
			Tracer:          noop.NewTracer(),
		})
		if err != nil {
			t.Fatalf("iter %d: NewBackgroundAgent: %v", i, err)
		}

		if err := bg.Start(context.Background(), "test task"); err != nil {
			t.Fatalf("iter %d: Start: %v", i, err)
		}

		// Hammer ReportProgress from multiple goroutines while the agent executes.
		// This creates the race condition: status read as Running, then channel closed,
		// then emitEvent called on closed channel.
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for range 4 {
			wg.Go(func() {
				for {
					select {
					case <-stop:
						return
					default:
						// ReportProgress checks status internally, but there's a
						// TOCTOU race: status can change between the check and emitEvent.
						bg.ReportProgress(50, "hammer", nil)
					}
				}
			})
		}

		// Wait for the agent to complete.
		<-bg.Done()
		close(stop)
		wg.Wait()

		// If the agent ended up as Failed due to a recovered panic,
		// the race was hit.
		if bg.Status() == StatusFailed {
			result := bg.Result()
			if result != nil && result.Error != nil {
				panicCount.Add(1)
				t.Logf("iter %d: status=Failed, error: %s", i, result.Error.Error())
			}
		}
	}

	if panics := panicCount.Load(); panics > 0 {
		t.Errorf("BUG REPRODUCED: %d/%d iterations had panic-induced status corruption — "+
			"ReportProgress raced with channel close", panics, iterations)
	}
}

// TestProbe_StatusCorruptionAfterPanic tests that the status remains
// Completed after the agent finishes, even if emitEvent is called
// after the channel is closed.
func TestProbe_StatusCorruptionAfterPanic(t *testing.T) {
	t.Parallel()

	ag := newTestAgent("probe-status-corruption")
	bg, err := NewBackgroundAgent(BackgroundAgentConfig{
		Agent:           ag,
		EventBufferSize: 16,
		Logger:          noop.NewLogger(),
		Tracer:          noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("NewBackgroundAgent: %v", err)
	}

	if err := bg.Start(context.Background(), "test task"); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for completion
	<-bg.Done()

	// Status should be Completed
	if bg.Status() != StatusCompleted {
		t.Fatalf("pre-check: expected StatusCompleted, got %s", bg.Status())
	}

	// Call emitEvent directly — should not panic or corrupt status.
	didPanic := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
			}
		}()
		bg.emitEvent(BackgroundAgentEvent{
			AgentID:   ag.ID(),
			EventType: EventProgress,
			Status:    StatusRunning,
			Message:   "test after close",
			Progress:  50,
			Timestamp: time.Now(),
		})
	}()

	if didPanic {
		t.Errorf("BUG REPRODUCED: emitEvent panicked after channel close")
	}

	if bg.Status() != StatusCompleted {
		t.Errorf("BUG REPRODUCED: status was corrupted from Completed to %s", bg.Status())
	}
}
