package chat

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// startProbeProgram spins up a REAL bubbletea program driving a drainProbeModel
// with NO input source and NO renderer, so the ONLY thing that can make the
// loop run Update()/drain is an out-of-band Program.Send (i.e. our wake). It
// returns the app (with .program wired) and a cleanup func.
func startProbeProgram(t *testing.T, queueCap int) (*App, *int32, func()) {
	t.Helper()
	processed := new(int32)
	a := &App{}
	a.updateQueue = make(chan tea.Msg, queueCap)
	a.streamingMessage = false // idle: no animation/tick chain running

	model := drainProbeModel{queue: a.updateQueue, processed: processed}
	p := tea.NewProgram(
		model,
		tea.WithInput(bytes.NewReader(nil)),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
	)
	a.program = p

	runErr := make(chan error, 1)
	go func() { _, err := p.Run(); runErr <- err }()
	cleanup := func() {
		p.Quit()
		select {
		case <-runErr:
		case <-time.After(2 * time.Second):
		}
	}
	waitUntil(t, 2*time.Second, func() bool { return programIsRunning(p) })
	return a, processed, cleanup
}

// TestSmoke_RealScheduler_RealProgram_DeliversWithoutFocus is the end-to-end
// "does it actually work" smoke test. It wires the REAL cron scheduler exactly
// like production (app_init.go SetCronPromptHandler → sendToRuntime) into a REAL
// idle bubbletea program, then registers a due task and asserts the prompt is
// delivered and drained with ZERO user interaction (no keypress, no focus).
func TestSmoke_RealScheduler_RealProgram_DeliversWithoutFocus(t *testing.T) {
	a, processed, cleanup := startProbeProgram(t, 100)
	defer cleanup()

	// Production wiring: the sink's handler forwards fired prompts into the
	// runtime via sendToRuntime, which enqueues + wakes the idle loop.
	sink := &tuiCronPromptSink{}
	sink.SetHandler(func(prompt string) {
		a.sendToRuntime(cronPromptInjectMsg{prompt: prompt})
	})

	sched, err := builtin.NewCronScheduler(builtin.CronSchedulerConfig{
		Logger:        noop.NewLogger(),
		PromptSink:    sink,
		CheckInterval: 50 * time.Millisecond, // sub-second poll for a fast test
	})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}

	// A one-shot task whose first (and only) boundary is already in the past —
	// due on the very first poll. One-shot jitter is early (negative), so there
	// is no deferral: it fires immediately via the sink.
	if err := sched.AddTask(&builtin.ScheduledTask{
		ID:        "smoke-oneshot",
		Prompt:    "smoke: run scheduled report",
		Cron:      "* * * * *",
		Recurring: false,
		CreatedAt: time.Now().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	sched.Start()
	defer sched.Stop()

	if !drainedWithin(3*time.Second, processed, 1) {
		t.Fatalf("SMOKE FAILED: scheduled prompt was NOT delivered+drained without user input "+
			"(processed=%d, queue len=%d)", atomic.LoadInt32(processed), len(a.updateQueue))
	}
	t.Logf("SMOKE OK: real scheduler → sink → sendToRuntime → wake → drain delivered the "+
		"scheduled prompt with zero user input (processed=%d)", atomic.LoadInt32(processed))
}

// TestSmoke_RealScheduler_Recurring_NoFlood_NoFocus combines the two fixes: a
// REAL recurring task on a REAL idle program must deliver at most ~once per
// minute (not the pre-fix 1×/second flood) AND deliver without any focus event.
func TestSmoke_RealScheduler_Recurring_NoFlood_NoFocus(t *testing.T) {
	a, processed, cleanup := startProbeProgram(t, 500)
	defer cleanup()

	sink := &tuiCronPromptSink{}
	sink.SetHandler(func(prompt string) {
		a.sendToRuntime(cronPromptInjectMsg{prompt: prompt})
	})
	sched, err := builtin.NewCronScheduler(builtin.CronSchedulerConfig{
		Logger:        noop.NewLogger(),
		PromptSink:    sink,
		CheckInterval: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCronScheduler: %v", err)
	}
	if err := sched.AddTask(&builtin.ScheduledTask{
		ID:        "smoke-recurring",
		Prompt:    "smoke: recurring health check",
		Cron:      "* * * * *",
		Recurring: true,
		CreatedAt: time.Now().Add(-3 * time.Minute),
	}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	sched.Start()
	defer sched.Stop()

	// Cover the recurring jitter window (~6s for an every-minute cron) + margin.
	time.Sleep(8 * time.Second)

	got := atomic.LoadInt32(processed)
	t.Logf("recurring deliveries within ~8s on a real idle program (no focus): %d", got)
	// Pre-fix this was 100+ (one delivery per poll during the jitter window).
	// Correct is 1, or 2 if the 8s window happens to cross a minute boundary.
	if got > 2 {
		t.Fatalf("FLOOD REGRESSION: recurring task delivered %d times in one minute (want <= 2)", got)
	}
	if got < 1 {
		t.Fatalf("DELIVERY REGRESSION: recurring task delivered %d times — the idle wake did not fire", got)
	}
}

// TestSmoke_WakeSignal_NoLostWakeups_Concurrent hammers sendToRuntime from many
// goroutines against a REAL idle program and asserts EVERY enqueued prompt is
// drained. Because the hardened wake coalesces requests, several enqueues share
// one drainQueueMsg — the re-arm logic must still guarantee a final drain after
// the last enqueue so nothing is stranded (no lost wakeups).
func TestSmoke_WakeSignal_NoLostWakeups_Concurrent(t *testing.T) {
	const workers = 8
	const perWorker = 100
	total := int32(workers * perWorker)

	a, processed, cleanup := startProbeProgram(t, int(total)+16)
	defer cleanup()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				a.sendToRuntime(cronPromptInjectMsg{prompt: fmt.Sprintf("w%d-%d", id, i)})
			}
		}(w)
	}
	wg.Wait()

	if !drainedWithin(5*time.Second, processed, total) {
		t.Fatalf("LOST WAKEUP: only %d/%d prompts drained — the coalescing wake stranded messages",
			atomic.LoadInt32(processed), total)
	}
	t.Logf("SMOKE OK: all %d concurrently-enqueued prompts drained under coalesced wakes "+
		"(no lost wakeups, no stranding)", total)
}

// TestSmoke_SafetyNet_BatchLimitContinuation proves the top-of-Update safety-net
// defer: when a single Update() cannot drain the whole queue (batch limit, or an
// early-returning case), it schedules a follow-up drainQueueMsg so the remainder
// — which may include critical broker or cron messages — is never stranded.
// Uses only side-effect-free notificationMsg so it needs no SDK.
func TestSmoke_SafetyNet_BatchLimitContinuation(t *testing.T) {
	a := newRoutingProofApp()

	const n = updateQueueDrainBatchLimit + 12
	for i := 0; i < n; i++ {
		a.updateQueue <- notificationMsg{level: "info", message: fmt.Sprintf("m%d", i)}
	}

	// First Update drains exactly the batch limit and must leave the rest queued.
	_, cmd := a.Update(drainQueueMsg{})
	remaining := len(a.updateQueue)
	if remaining != n-updateQueueDrainBatchLimit {
		t.Fatalf("expected %d messages left after one batch (limit=%d), got %d",
			n-updateQueueDrainBatchLimit, updateQueueDrainBatchLimit, remaining)
	}
	if cmd == nil {
		t.Fatalf("SAFETY NET MISSING: Update left %d queued messages but returned no continuation cmd", remaining)
	}

	// Feeding the scheduled continuation drains the remainder — nothing stranded.
	_, _ = a.Update(drainQueueMsg{})
	if left := len(a.updateQueue); left != 0 {
		t.Fatalf("STRANDING BUG: %d messages left after continuation drain", left)
	}
	t.Logf("SAFETY NET OK: batch-limited Update scheduled a continuation and the full queue drained across passes")
}
