package chat

import (
	"context"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// This file reproduces and locks the bug the user reported:
//
//	"the cron job doesn't auto-run until I interact with the TUI"
//
// WHY IT HAPPENED
// ----------------
// A fired cron/wakeup prompt reaches the TUI as a cronPromptInjectMsg via
// SDKIntegration.SetCronPromptHandler -> App.sendToRuntime (see app_init.go
// SetCronPromptHandler and app_messaging.go sendToRuntime).
//
// The Bubble Tea event loop only runs App.Update() — and therefore only drains
// App.updateQueue in the queueDrain loop at the top of Update() — when Bubble
// Tea itself delivers a message (a keypress, a tick, a resize, ...). Putting a
// message onto the plain Go channel App.updateQueue does NOT by itself cause
// Update() to run.
//
// The ONLY thing that nudges the runtime to drain the queue out-of-band is
// App.wakeRuntimeAsync(), which does program.Send(drainQueueMsg{}).
//
// sendToRuntime classifies cronPromptInjectMsg as "critical" (it must never be
// dropped). BEFORE THE FIX, the critical branch enqueued the message and then
// returned WITHOUT calling wakeRuntimeAsync(); only the non-critical branch
// woke the runtime. When the TUI is idle no animation/tick chain is running
// (the animation clock only ticks while it has subscribers), so the fired cron
// prompt sat in updateQueue until the next Bubble Tea event — i.e. until the
// user interacted with the TUI. Exactly the reported symptom.
//
// THE FIX
// -------
// sendToRuntime's critical branch now also calls wakeRuntimeAsync(), mirroring
// the non-critical branch. These tests assert that behavior.
//
// TEST SEAM
// ---------
// wakeRuntimeAsync() normally needs a live *tea.Program to observe. The App has
// a production-nil wakeNotify seam that, when set, is invoked instead of
// program.Send — letting us count wake requests deterministically without a
// running Bubble Tea program.

// newIdleWakeProbeApp builds a minimal, IDLE App wired with a wake counter.
// streamingMessage=false and program=nil model an app sitting at the prompt
// with nothing animating — the exact state in which the bug manifested.
func newIdleWakeProbeApp(wakeCount *int32) *App {
	a := &App{}
	a.updateQueue = make(chan tea.Msg, 100)
	a.streamingMessage = false
	a.wakeNotify = func() { atomic.AddInt32(wakeCount, 1) }
	return a
}

// TestCronPrompt_WakesIdleRuntime is the primary reproduction/regression test.
//
// It feeds a fired cron prompt through the SAME path the scheduler uses
// (sendToRuntime with a cronPromptInjectMsg) while the TUI is idle, and asserts
// that the runtime was nudged to drain the queue.
//
// BEFORE the fix this FAILS: wakeCount stays 0 (message enqueued, never drained
// until the user interacts). AFTER the fix it PASSES: wakeCount == 1.
func TestCronPrompt_WakesIdleRuntime(t *testing.T) {
	var wakeCount int32
	a := newIdleWakeProbeApp(&wakeCount)

	// This is exactly what SetCronPromptHandler does when a cron task fires:
	//   app.sendToRuntime(cronPromptInjectMsg{prompt: prompt})
	a.sendToRuntime(cronPromptInjectMsg{prompt: "run the nightly report"})

	// 1) The prompt must be safely queued (critical => never dropped).
	if got := len(a.updateQueue); got != 1 {
		t.Fatalf("expected fired cron prompt to be queued exactly once, got queue len %d", got)
	}
	select {
	case m := <-a.updateQueue:
		cm, ok := m.(cronPromptInjectMsg)
		if !ok {
			t.Fatalf("queued message has wrong type %T, want cronPromptInjectMsg", m)
		}
		if cm.prompt != "run the nightly report" {
			t.Fatalf("queued prompt mangled: %q", cm.prompt)
		}
	default:
		t.Fatal("expected a message in updateQueue, found none")
	}

	// 2) THE BUG: while idle, the runtime must be woken so the queue is drained
	//    WITHOUT waiting for user interaction. Pre-fix this count is 0.
	if got := atomic.LoadInt32(&wakeCount); got != 1 {
		t.Fatalf("cron prompt did NOT wake the idle runtime (wakeCount=%d, want 1): "+
			"the fired prompt would sit in updateQueue until the user interacts "+
			"with the TUI — this is the reported bug", got)
	}
}

// TestCriticalMessages_AllWakeIdleRuntime documents that EVERY message
// sendToRuntime treats as critical must wake an idle runtime — dropping the
// wake for any of them reproduces a "stuck until interaction" class of bug
// (cron the most visible, but also plan approvals, agent responses, etc.).
func TestCriticalMessages_AllWakeIdleRuntime(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.Msg
	}{
		{"cronPromptInject", cronPromptInjectMsg{prompt: "x"}},
		{"agentResponse", agentResponseMsg{content: "done"}},
		{"planEnter", planEnterMsg{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var wakeCount int32
			a := newIdleWakeProbeApp(&wakeCount)
			a.sendToRuntime(tc.msg)
			if got := atomic.LoadInt32(&wakeCount); got != 1 {
				t.Fatalf("critical %s did not wake idle runtime (wakeCount=%d, want 1)", tc.name, got)
			}
		})
	}
}

// TestNonCriticalMessage_WakesIdleRuntime is the control: the non-critical path
// already woke the runtime before the fix. It proves the test seam is sound and
// that the pre-fix asymmetry was specifically in the critical branch.
func TestNonCriticalMessage_WakesIdleRuntime(t *testing.T) {
	var wakeCount int32
	a := newIdleWakeProbeApp(&wakeCount)

	a.sendToRuntime(notificationMsg{level: "info", message: "hello"})

	if got := atomic.LoadInt32(&wakeCount); got != 1 {
		t.Fatalf("non-critical message did not wake idle runtime (wakeCount=%d, want 1)", got)
	}
}

// TestCronPromptSink_ToRuntime_EndToEndWake exercises the full delivery chain a
// real cron firing uses: the scheduler's PromptSink -> the handler installed by
// SetCronPromptHandler -> sendToRuntime -> wake. It proves that a prompt fired
// from the scheduler goroutine, while the app is idle, results in a runtime
// wake (and thus a drain) rather than silently waiting for user input.
func TestCronPromptSink_ToRuntime_EndToEndWake(t *testing.T) {
	var wakeCount int32
	a := newIdleWakeProbeApp(&wakeCount)

	// Reproduce app_init.go's wiring: the sink's handler forwards into the
	// runtime exactly like SDKIntegration.SetCronPromptHandler does.
	sink := &tuiCronPromptSink{}
	sink.SetHandler(func(prompt string) {
		a.sendToRuntime(cronPromptInjectMsg{prompt: prompt})
	})

	// Simulate the scheduler goroutine firing a task.
	if err := sink.EnqueuePrompt(context.Background(), "scheduled: check disk usage"); err != nil {
		t.Fatalf("EnqueuePrompt: %v", err)
	}

	if got := len(a.updateQueue); got != 1 {
		t.Fatalf("fired prompt not queued (queue len %d, want 1)", got)
	}
	if got := atomic.LoadInt32(&wakeCount); got != 1 {
		t.Fatalf("scheduler-fired prompt did not wake idle runtime (wakeCount=%d, want 1)", got)
	}
}
