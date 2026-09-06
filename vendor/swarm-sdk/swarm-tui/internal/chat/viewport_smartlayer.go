package chat

import (
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
)

// smartLayer implements [tea.Layer] with true zero-cost unchanged frames.
//
// # How ultraviolet's early-exit works
//
// TerminalRenderer.Render() checks:
//
//	touchedLines := s.Touched(newbuf)
//	if !s.clear && touchedLines == 0 { return }  // O(1) exit
//
// Touched() returns 0 only when newbuf.Touched is a non-nil slice with ALL
// nil entries. After each Render() call, ultraviolet resets newbuf.Touched to
// a slice of {-1,-1} sentinels (non-nil), so the next call to Touched()
// returns Height() instead of 0 — defeating the early exit.
//
// # The fix
//
// On idle frames (dirty=0), our Draw() zeroes the Touched slice back to all-nil
// via a type-assert to uv.ScreenBuffer (which embeds *uv.Buffer with public
// Touched field). This makes Touched() return 0 on the next Render() call,
// giving a true O(1) no-op. Only dirty frames (new content) pay the full cost.
//
// This lets us run tea.WithFPS(60) for ≤16ms keystroke latency while keeping
// idle CPU near zero (no scrollOptimize, no updateHashmap, no transformLine).
type smartLayer struct {
	mu      sync.Mutex
	content string
	dirty   int32 // atomic: 1 = must redraw, 0 = screen already shows this content
}

func newSmartLayer() *smartLayer {
	return &smartLayer{dirty: 1}
}

// Set updates the content and marks the layer as dirty.
// Called from the UI goroutine after View() computes a new frame.
func (l *smartLayer) Set(s string) {
	l.mu.Lock()
	l.content = s
	l.mu.Unlock()
	atomic.StoreInt32(&l.dirty, 1)
}

// Draw implements [tea.Layer].
// Called from the renderer goroutine at the configured FPS rate (60fps).
//
//   - dirty=1: full ANSI parse + cell write → Render() diffs + writes to terminal
//   - dirty=0: zero cells written; Touched slice is cleared to all-nil so
//     Touched() returns 0 → Render() exits in O(1), no terminal I/O
func (l *smartLayer) Draw(scr uv.Screen, r uv.Rectangle) {
	if !atomic.CompareAndSwapInt32(&l.dirty, 1, 0) {
		// Idle frame: clear the Touched sentinels ultraviolet left from the
		// previous Render() call so that Touched() returns 0 → O(1) early exit.
		if sb, ok := scr.(uv.ScreenBuffer); ok && sb.Buffer != nil {
			clear(sb.Touched) // zero all entries: {-1,-1} → nil
		}
		return
	}
	l.mu.Lock()
	content := l.content
	l.mu.Unlock()
	uv.NewStyledString(content).Draw(scr, r)
}
