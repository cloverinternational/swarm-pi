package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// makeEvent is a tiny helper to construct a minimal hooks.Event for pump tests.
func makeEvent(typ string, i int) hooks.Event {
	return hooks.Event{
		Type:      typ,
		Timestamp: time.Now(),
		Data:      map[string]any{"i": i},
	}
}

// TestDriverEnqueueOnPollModeIsSilentDrop verifies that hook authors can
// call Enqueue unconditionally without checking IsStreaming first.
func TestDriverEnqueueOnPollModeIsSilentDrop(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{Mode: SteeringModePoll})
	// No Start needed for poll mode.
	d.Enqueue(makeEvent("tool.before_execute", 1))
	if got := d.DroppedTotal(); got != 0 {
		t.Errorf("poll-mode Enqueue should not count as dropped, got %d", got)
	}
}

// TestDriverEnqueueBeforeStartIsSilentDrop verifies that an early
// Enqueue call (e.g. during construction race) does not panic or
// block.
func TestDriverEnqueueBeforeStartIsSilentDrop(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{Mode: SteeringModeStream})
	d.Enqueue(makeEvent("tool.before_execute", 1))
	// Not counted as dropped — the channel was never created.
	if got := d.DroppedTotal(); got != 0 {
		t.Errorf("pre-Start Enqueue should not count as dropped, got %d", got)
	}
}

// TestDriverFlushOnCountThreshold verifies that the pump fires a flush
// as soon as buffered events reach FlushCount.
func TestDriverFlushOnCountThreshold(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      3,
		FlushAge:        10 * time.Second, // long, so count threshold dominates
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	for i := range 3 {
		d.Enqueue(makeEvent("tool.before_execute", i))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.FlushedTotal() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := d.FlushedTotal(); got < 1 {
		t.Fatalf("expected at least 1 flush after count threshold, got %d", got)
	}
}

// TestDriverFlushOnAgeThreshold verifies that the pump fires a flush
// for buffered events older than FlushAge even if count is below
// FlushCount.
func TestDriverFlushOnAgeThreshold(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      100, // unreachable
		FlushAge:        80 * time.Millisecond,
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	d.Enqueue(makeEvent("tool.before_execute", 1))

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if d.FlushedTotal() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := d.FlushedTotal(); got < 1 {
		t.Fatalf("expected at least 1 flush after age threshold, got %d", got)
	}
}

// TestDriverDropOldestUnderBackpressure constructs a pathological case
// where the producer outruns the pump and verifies that droppedTotal
// increases (i.e., events are not silently lost without accounting).
// We block the pump by setting an enormous FlushAge and a tiny channel,
// then fire a burst much larger than the buffer.
func TestDriverDropOldestUnderBackpressure(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 2,
		FlushCount:      1000,           // never trigger by count
		FlushAge:        24 * time.Hour, // never trigger by age
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	// Burst 50 events into a 2-slot channel. Pump can pull at most a
	// handful before we fill it; the rest must be drops.
	for i := range 50 {
		d.Enqueue(makeEvent("tool.before_execute", i))
	}
	// Give the pump a brief moment to drain anything it could.
	time.Sleep(50 * time.Millisecond)
	if got := d.DroppedTotal(); got == 0 {
		t.Fatalf("expected droppedTotal > 0 under backpressure, got %d", got)
	}
}

// TestDriverStopWaitsForPump verifies that Stop() blocks until the pump
// goroutine has actually exited (no leaked goroutines).
func TestDriverStopWaitsForPump(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:     SteeringModeStream,
		FlushAge: 100 * time.Millisecond,
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = d.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s — pump goroutine leaked?")
	}
}

// TestDriverConcurrentEnqueueIsSafe pounds Enqueue from many goroutines
// with -race to confirm no data race on internal state.
func TestDriverConcurrentEnqueueIsSafe(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 8,
		FlushCount:      4,
		FlushAge:        50 * time.Millisecond,
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(gid int) {
			defer wg.Done()
			for i := range 50 {
				d.Enqueue(makeEvent("tool.before_execute", gid*1000+i))
			}
		}(g)
	}
	wg.Wait()
	// Pump should fire at least some flushes during this storm.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.FlushedTotal() >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected at least 1 flush during concurrent burst, got %d", d.FlushedTotal())
}

// TestDriverTargetDefaultsToNewWhenNil verifies that the driver
// auto-constructs a SteeringTarget when the caller does not supply one.
func TestDriverTargetDefaultsToNewWhenNil(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{Mode: SteeringModeStream})
	if d.Target() == nil {
		t.Fatal("Target should auto-default when cfg.Target is nil")
	}
}

// TestDriverTargetIsConfiguredOneWhenSupplied verifies pass-through.
func TestDriverTargetIsConfiguredOneWhenSupplied(t *testing.T) {
	supplied := NewDefaultSteeringTarget()
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:   SteeringModeStream,
		Target: supplied,
	})
	if d.Target() != supplied {
		t.Fatal("Target should pass-through cfg.Target")
	}
}
