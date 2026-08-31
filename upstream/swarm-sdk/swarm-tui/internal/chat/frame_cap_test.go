package chat

import (
	"sync/atomic"
	"testing"
	"time"
)

// A skipped frame must always leave exactly one trailing tick outstanding,
// otherwise the last frame of an input burst is never composed and the screen
// is left stale.
func TestSkippedFrameSchedulesExactlyOneTrailingTick(t *testing.T) {
	a := &App{frameDeferred: true}

	if cmd := a.scheduleTrailingFrame(); cmd == nil {
		t.Fatal("a deferred frame must schedule a trailing tick")
	}
	// A burst of further skips must NOT pile up more ticks — this is the
	// guard that keeps us from recreating the tea.Batch goroutine pileup.
	for i := 0; i < 1000; i++ {
		if cmd := a.scheduleTrailingFrame(); cmd != nil {
			t.Fatalf("trailing tick %d scheduled while one was already pending", i)
		}
	}
	if got := atomic.LoadInt32(&a.frameFlushPending); got != 1 {
		t.Fatalf("frameFlushPending = %d, want exactly 1", got)
	}
}

// Once the flush fires, the guard must release so later bursts can schedule.
func TestTrailingTickReschedulesAfterFlush(t *testing.T) {
	a := &App{frameDeferred: true}
	if cmd := a.scheduleTrailingFrame(); cmd == nil {
		t.Fatal("first tick should schedule")
	}
	a.clearTrailingFrame()
	if cmd := a.scheduleTrailingFrame(); cmd == nil {
		t.Fatal("after a flush, a new deferred frame must schedule again")
	}
}

// No deferred frame means no tick: an idle UI must stay fully idle rather
// than spin a timer forever.
func TestNoTickWhenNothingDeferred(t *testing.T) {
	a := &App{frameDeferred: false}
	if cmd := a.scheduleTrailingFrame(); cmd != nil {
		t.Fatal("scheduled a trailing tick with no deferred frame")
	}
	if got := atomic.LoadInt32(&a.frameFlushPending); got != 0 {
		t.Fatalf("frameFlushPending = %d, want 0", got)
	}
}

// The budget must sit at or under the terminal flush interval, or the cap
// would throttle below what the renderer can actually display.
func TestFrameBudgetNotSlowerThan60fps(t *testing.T) {
	if frameBudget > time.Second/60 {
		t.Fatalf("frameBudget %v is slower than the 60fps flush interval %v",
			frameBudget, time.Second/60)
	}
}
