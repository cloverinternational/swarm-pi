package chat

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// scheduleAgentTick returns the command that keeps the agent drain-tick loop
// alive, or nil when a tick is already pending.
//
// Why the guard exists. agentTickMsg is a self-perpetuating tick: its handler
// schedules the next one while streaming. It was ALSO minted fresh by the
// drainQueueMsg handler and by sendMessage. drainQueueMsg is a hot message —
// wakeRuntimeAsync sends one per queued item, and Update's defer re-emits it
// whenever the queue is non-empty — so every one of those started an
// additional, independent, immortal tick chain. Nothing ever merged them.
//
// The cost is not the timer. Every tick delivers a message, every message runs
// Update and View, and while streaming that re-renders the transcript. Each
// in-flight chain also parks a goroutine, and the tea.Batch wrappers park a
// waiter on wg.Wait() plus workers on the unbuffered program channel. A live
// dump showed 53 pending ticks and 11 batch waiters — 74 of 142 goroutines
// sitting in tick machinery, driving a viewport rebuild several hundred times
// a second.
//
// The dedupe CAS deliberately lives INSIDE the returned closure, mirroring
// AnimationClock.scheduleTick. Marking pending at creation time would poison
// the flag whenever a caller drops the returned command: pending would stay 1
// with no real timer behind it, every later schedule would return nil, and the
// drain loop would stall for the rest of the session.
func (a *App) scheduleAgentTick(d time.Duration) tea.Cmd {
	// Fast path: a chain is already running, so callers get nil and skip the
	// tea.Batch they would otherwise build around it.
	if atomic.LoadInt32(&a.agentTickPending) != 0 {
		return nil
	}
	return func() tea.Msg {
		if !atomic.CompareAndSwapInt32(&a.agentTickPending, 0, 1) {
			return nil // another chain won the race
		}
		msg := tea.Tick(d, func(t time.Time) tea.Msg {
			atomic.StoreInt32(&a.agentTickPending, 0)
			return agentTickMsg{}
		})()
		if msg == nil {
			atomic.StoreInt32(&a.agentTickPending, 0)
		}
		return msg
	}
}
