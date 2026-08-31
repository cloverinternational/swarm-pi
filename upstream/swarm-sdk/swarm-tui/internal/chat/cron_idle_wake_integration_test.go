package chat

import (
	"bytes"
	"io"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// This is an END-TO-END proof against a REAL running bubbletea event loop.
//
// Unlike cron_idle_wake_test.go (which uses the wakeNotify seam to observe wake
// requests), this test starts an actual *tea.Program with NO user input source
// (an empty reader) and NO renderer, then proves:
//
//	A) PRE-FIX behavior — enqueuing a cron prompt directly onto updateQueue
//	   while the program is idle does NOT get drained (nothing wakes the loop).
//	   This is exactly the reported bug: "doesn't auto-run until I interact".
//
//	B) FIXED behavior — routing the same prompt through the real App.sendToRuntime
//	   (which now calls wakeRuntimeAsync() on the critical path) DOES drain it,
//	   with zero user interaction, because wakeRuntimeAsync does
//	   program.Send(drainQueueMsg{}) which makes the live loop run Update().
//
// The drainProbeModel below faithfully mimics App's queueDrain: on drainQueueMsg
// it drains updateQueue and records any cronPromptInjectMsg it sees. (App.Update's
// real handling of cronPromptInjectMsg is separately proven by
// routing_bugs_proof_test.go using the real App.Update.)

type drainProbeModel struct {
	queue     chan tea.Msg
	processed *int32 // incremented when a cronPromptInjectMsg is drained
}

func (m drainProbeModel) Init() tea.Cmd { return nil }

func (m drainProbeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case drainQueueMsg:
		// Same shape as App.Update's queueDrain: drain everything available.
		for {
			select {
			case qm := <-m.queue:
				if _, ok := qm.(cronPromptInjectMsg); ok {
					atomic.AddInt32(m.processed, 1)
				}
			default:
				return m, nil
			}
		}
	}
	return m, nil
}

func (m drainProbeModel) View() tea.View { return tea.NewView("") }

// TestCronPrompt_EndToEnd_RealProgram_DrainsWhileIdle proves the fix drives a
// real bubbletea loop with no input, and that WITHOUT the wake an idle loop
// never drains the queue.
func TestCronPrompt_EndToEnd_RealProgram_DrainsWhileIdle(t *testing.T) {
	var processed int32
	a := &App{}
	a.updateQueue = make(chan tea.Msg, 100)
	a.streamingMessage = false // idle: no animation/tick chain running

	model := drainProbeModel{queue: a.updateQueue, processed: &processed}

	// A REAL program with NO user input (empty reader) and NO renderer. Nothing
	// but our own Send can ever make this loop run Update().
	p := tea.NewProgram(
		model,
		tea.WithInput(bytes.NewReader(nil)),
		tea.WithOutput(io.Discard),
		tea.WithoutRenderer(),
	)
	a.program = p // wakeRuntimeAsync targets this live program

	runErr := make(chan error, 1)
	go func() { _, err := p.Run(); runErr <- err }()
	defer func() {
		p.Quit()
		select {
		case <-runErr:
		case <-time.After(2 * time.Second):
		}
	}()

	// Wait for the program's event loop to be live.
	waitUntil(t, 2*time.Second, func() bool { return programIsRunning(p) })

	// ── Phase A: PRE-FIX behavior (direct enqueue, NO wake) ──────────────────
	// This is what the buggy critical path effectively did: put it on the queue
	// and return. An idle loop has no reason to run Update(), so it must NOT
	// drain. We assert it stays UNprocessed for a generous window.
	a.updateQueue <- cronPromptInjectMsg{prompt: "phaseA: should sit undrained while idle"}
	if drainedWithin(200*time.Millisecond, &processed, 1) {
		t.Fatalf("PHASE A FAILED: idle real program drained the queue without any wake — " +
			"the reproduction premise is invalid")
	}
	if got := atomic.LoadInt32(&processed); got != 0 {
		t.Fatalf("PHASE A: expected 0 processed while idle+unwoken, got %d", got)
	}
	t.Logf("PHASE A (pre-fix): cron prompt correctly sat UNdrained in an idle loop (processed=0, queue len=%d)", len(a.updateQueue))

	// ── Phase B: FIXED behavior (real sendToRuntime, which wakes) ────────────
	// sendToRuntime classifies cronPromptInjectMsg as critical and now calls
	// wakeRuntimeAsync() → program.Send(drainQueueMsg{}). The live loop wakes and
	// drains BOTH the phase-A message and this one — with zero user input.
	a.sendToRuntime(cronPromptInjectMsg{prompt: "phaseB: fired by scheduler while idle"})
	if !drainedWithin(2*time.Second, &processed, 2) {
		t.Fatalf("PHASE B FAILED: sendToRuntime did not wake the idle loop; processed=%d, queue len=%d",
			atomic.LoadInt32(&processed), len(a.updateQueue))
	}
	t.Logf("PHASE B (fixed): sendToRuntime woke the idle loop with NO user input; both prompts drained (processed=%d)",
		atomic.LoadInt32(&processed))
}

// waitUntil polls cond until true or the deadline elapses.
func waitUntil(t *testing.T, d time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", d)
	}
}

// programIsRunning returns true once the program accepts a Send without panicking
// by probing with a benign drainQueueMsg (harmless: the queue is empty).
func programIsRunning(p *tea.Program) bool {
	defer func() { _ = recover() }()
	p.Send(drainQueueMsg{})
	return true
}

// drainedWithin waits up to d for *n to reach want.
func drainedWithin(d time.Duration, n *int32, want int32) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(n) >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return atomic.LoadInt32(n) >= want
}
