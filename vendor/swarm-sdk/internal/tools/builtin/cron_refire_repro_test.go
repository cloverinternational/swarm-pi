package builtin

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// countingSink records every EnqueuePrompt call with timestamps.
type countingSink struct {
	mu    sync.Mutex
	calls int
	times []time.Time
}

func (c *countingSink) EnqueuePrompt(ctx context.Context, prompt string) error {
	c.mu.Lock()
	c.calls++
	c.times = append(c.times, time.Now())
	c.mu.Unlock()
	return nil
}

func (c *countingSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TestRecurring_SingleOccurrence_FiresOnce is a regression guard for the
// "cron fires every second" bug (same task_id logged ~1/s in production).
//
// A recurring "* * * * *" (every-minute) task has exactly ONE due occurrence
// per minute. With a sub-second check interval the scheduler polls many times
// within that minute, and the async jitter window (up to ~6s here) defers the
// actual delivery. BEFORE the fix, every intervening poll re-armed another
// delivery because LastFiredAt was only updated inside the jittered fire — this
// produced 100+ enqueues per minute. AFTER the fix the occurrence is CLAIMED
// synchronously up front, so at most one delivery happens per minute boundary.
//
// The wait spans ~8s to cover the jitter window; if it happens to cross a
// single wall-clock minute boundary the count may legitimately reach 2. The
// pre-fix flood (100+) is far outside that, so `<= 2` is a safe, deterministic
// guard.
func TestRecurring_SingleOccurrence_FiresOnce(t *testing.T) {
	sink := &countingSink{}
	s, err := NewCronScheduler(CronSchedulerConfig{
		Logger:        noop.NewLogger(),
		PromptSink:    sink,
		CheckInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}

	// CreatedAt a few minutes in the past so the first minute boundary is
	// already behind us and the task is "due" on the very first poll.
	now := time.Now()
	task := &ScheduledTask{
		ID:        "repro-every-minute",
		Prompt:    "health check",
		Cron:      "* * * * *",
		Recurring: true,
		CreatedAt: now.Add(-3 * time.Minute),
	}
	if err := s.AddTask(task); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	s.Start()
	defer s.Stop()

	// Wait long enough to cover the recurring jitter window (up to ~6s for an
	// every-minute cron) plus margin, but stay within the SAME cron minute →
	// at most ONE new occurrence should fire.
	time.Sleep(8 * time.Second)

	got := sink.count()
	t.Logf("EnqueuePrompt calls within ~8s (single cron minute): %d", got)
	if got > 2 {
		t.Fatalf("recurring task fired %d times within one cron minute (want <= 2). "+
			"This is the every-second jitter-window refire bug.", got)
	}
}
