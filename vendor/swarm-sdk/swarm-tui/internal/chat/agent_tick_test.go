package chat

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// While a tick is pending, further requests must return nil so callers do not
// wrap them in a tea.Batch and start a parallel chain.
func TestScheduleAgentTickSuppressesWhilePending(t *testing.T) {
	a := &App{}

	cmd := a.scheduleAgentTick(50 * time.Millisecond)
	if cmd == nil {
		t.Fatal("first schedule returned nil, want a command")
	}

	// Not pending until Bubble Tea actually executes the command.
	if got := atomic.LoadInt32(&a.agentTickPending); got != 0 {
		t.Fatalf("agentTickPending = %d before execution, want 0", got)
	}
	if second := a.scheduleAgentTick(50 * time.Millisecond); second == nil {
		t.Fatal("second schedule returned nil before the first ran — the flag was set too early")
	}

	// Execute: this blocks for the duration and then delivers the tick.
	msg := cmd()
	if _, ok := msg.(agentTickMsg); !ok {
		t.Fatalf("executed command produced %T, want agentTickMsg", msg)
	}
	// The delivering tick clears the flag so the chain can continue.
	if got := atomic.LoadInt32(&a.agentTickPending); got != 0 {
		t.Fatalf("agentTickPending = %d after delivery, want 0", got)
	}
}

// The whole point: concurrent schedules must collapse to ONE live chain.
func TestScheduleAgentTickOnlyOneChainWins(t *testing.T) {
	a := &App{}

	const racers = 64
	var delivered int32
	var nilled int32
	var wg sync.WaitGroup
	start := make(chan struct{})

	for i := 0; i < racers; i++ {
		cmd := a.scheduleAgentTick(20 * time.Millisecond)
		if cmd == nil {
			// Already suppressed at creation time — also a win.
			atomic.AddInt32(&nilled, 1)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if msg := cmd(); msg != nil {
				atomic.AddInt32(&delivered, 1)
			} else {
				atomic.AddInt32(&nilled, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if delivered != 1 {
		t.Fatalf("%d tick chains survived, want exactly 1 (nilled=%d)", delivered, nilled)
	}
	if got := atomic.LoadInt32(&a.agentTickPending); got != 0 {
		t.Fatalf("agentTickPending = %d after the chain delivered, want 0", got)
	}
}

// A dropped command must NOT leave the flag stuck, or the drain loop would
// stall for the rest of the session.
func TestScheduleAgentTickDroppedCommandDoesNotPoison(t *testing.T) {
	a := &App{}

	for i := 0; i < 100; i++ {
		_ = a.scheduleAgentTick(10 * time.Millisecond) // created and discarded
	}
	if got := atomic.LoadInt32(&a.agentTickPending); got != 0 {
		t.Fatalf("agentTickPending = %d after dropping commands, want 0", got)
	}

	cmd := a.scheduleAgentTick(10 * time.Millisecond)
	if cmd == nil {
		t.Fatal("scheduler refused to schedule after dropped commands — flag was poisoned")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("command produced no message after dropped commands")
	}
}
