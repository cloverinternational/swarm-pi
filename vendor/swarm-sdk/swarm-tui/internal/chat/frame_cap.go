package chat

import (
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// frameBudget is the minimum spacing between composed frames.
//
// Bubble Tea flushes the terminal on a ticker at its configured FPS (default
// 60, tea.go:1394) but calls Model.View() once per message (tea.go:880). Any
// frame composed between two flushes is thrown away, so building them only
// burns CPU and — because the program's message channel is unbuffered — delays
// the next input event behind the render.
//
// 16ms ≈ 62.5fps: just under the flush ticker so we never starve it.
const frameBudget = 16 * time.Millisecond

// frameFlushMsg wakes Update() once after a skipped frame so the last frame of
// an input burst is composed even if no further input arrives.
type frameFlushMsg struct{}

// scheduleTrailingFrame returns a command that re-renders after the frame
// budget, or nil if a flush is already pending or nothing was deferred.
//
// The CAS is the whole safety story: at most one trailing tick can be
// outstanding, so this cannot reproduce the tea.Batch goroutine pileup that
// an unguarded per-event tick previously caused.
func (a *App) scheduleTrailingFrame() tea.Cmd {
	if !a.frameDeferred {
		return nil
	}
	if !atomic.CompareAndSwapInt32(&a.frameFlushPending, 0, 1) {
		return nil
	}
	return tea.Tick(frameBudget, func(time.Time) tea.Msg { return frameFlushMsg{} })
}

// clearTrailingFrame releases the guard so a later skip can schedule again.
func (a *App) clearTrailingFrame() {
	atomic.StoreInt32(&a.frameFlushPending, 0)
}
