// Package chat provides chat UI components
package chat

import (
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
)

// AnimationTickMsg is the SINGLE message type for all animation refreshes
// This replaces SpinnerRefreshMsg, LoadingRefreshMsg, settingsPreviewTickMsg
type AnimationTickMsg struct{}

// Animation timing constants
const (
	AnimationFPS          = 10                             // Target frames per second when streaming
	AnimationFPSIdle      = 6                              // Reduced FPS when not streaming
	AnimationTickRate     = time.Second / AnimationFPS     // ~100ms per frame
	AnimationTickRateIdle = time.Second / AnimationFPSIdle // ~167ms per frame (idle)
)

// AnimationClock is the SINGLE source of truth for all animations
// It maintains ONE goroutine that increments the frame counter
// and ONE tea.Cmd chain for UI refreshes
//
// Usage:
//
//	clock.Subscribe()   // Call when an animation becomes visible
//	clock.Unsubscribe() // Call when animation is no longer visible
//	clock.Frame()       // Get current frame for rendering (pure function)
//	clock.SetStreaming(true/false) // Switch between fast/idle tick rates
type AnimationClock struct {
	frame       int64 // atomic: current animation frame
	subscribers int32 // atomic: number of active subscribers
	streaming   int32 // atomic: 1 if streaming, 0 if idle

	// Goroutine control
	mu       sync.Mutex
	running  bool
	stopChan chan struct{}

	// tickPending guards against parallel tea.Tick chains. It is set while a
	// scheduled tick command is pending and cleared when its AnimationTickMsg
	// fires, so redundant Tick()/Subscribe() calls become no-ops instead of
	// spawning duplicate refresh chains.
	tickPending int32
}

// NewAnimationClock creates the single animation clock
// There should only be ONE instance per App
func NewAnimationClock() *AnimationClock {
	return &AnimationClock{
		stopChan: make(chan struct{}),
	}
}

// Frame returns the current animation frame (thread-safe, lock-free)
// This is a PURE READ - no side effects
func (c *AnimationClock) Frame() int {
	return int(atomic.LoadInt64(&c.frame))
}

// Subscribe indicates that something needs animation
// Call this when:
//   - Spinner becomes active
//   - Settings Display section is entered
//   - Any animation becomes visible
//
// Returns a tea.Cmd to start the refresh chain if this is the first subscriber
func (c *AnimationClock) Subscribe() tea.Cmd {
	newCount := atomic.AddInt32(&c.subscribers, 1)

	if newCount == 1 {
		// First subscriber - start the clock
		c.startClock()
		// Return command to start the tea.Tick chain
		return c.scheduleTick()
	}

	// Clock already running, no new command needed
	return nil
}

// Unsubscribe indicates that something no longer needs animation
// Call this when:
//   - Spinner becomes inactive
//   - User leaves Settings Display section
//   - Any animation becomes hidden
func (c *AnimationClock) Unsubscribe() {
	newCount := atomic.AddInt32(&c.subscribers, -1)

	if newCount <= 0 {
		// No more subscribers - stop the clock
		atomic.StoreInt32(&c.subscribers, 0) // Clamp to 0
		c.stopClock()
	}
}

// IsActive returns true if any animations are subscribed
func (c *AnimationClock) IsActive() bool {
	return atomic.LoadInt32(&c.subscribers) > 0
}

// Tick should be called when AnimationTickMsg is received
// It schedules the next tick ONLY if still active
// This prevents orphaned tick chains
func (c *AnimationClock) Tick() tea.Cmd {
	if c.IsActive() {
		return c.scheduleTick()
	}
	return nil
}

// TickIfIdle returns a tick command only when no tick is already pending.
//
// Tick() always returns a non-nil command while IsActive(), because
// scheduleTick's dedupe CAS lives INSIDE the returned closure and therefore
// only runs once Bubble Tea executes it. Callers that use the result to decide
// whether to wrap an existing command in tea.Batch would then Batch on every
// single message — each Batch costs one waiter goroutine blocked on wg.Wait()
// plus N workers blocked on the unbuffered program msg channel. Under load
// those accumulate faster than they drain (observed: 1017 goroutines while
// scrolling). Checking tickPending up front keeps the common case nil.
func (c *AnimationClock) TickIfIdle() tea.Cmd {
	if atomic.LoadInt32(&c.tickPending) != 0 {
		return nil
	}
	return c.Tick()
}

// SetStreaming sets whether we're in streaming mode (faster animation) or idle mode
func (c *AnimationClock) SetStreaming(streaming bool) {
	if streaming {
		atomic.StoreInt32(&c.streaming, 1)
	} else {
		atomic.StoreInt32(&c.streaming, 0)
	}
}

// IsStreaming returns true if in streaming mode
func (c *AnimationClock) IsStreaming() bool {
	return atomic.LoadInt32(&c.streaming) == 1
}

// scheduleTick creates the tea.Cmd that will trigger the next AnimationTickMsg.
// Uses faster tick rate when streaming, slower when idle. Only one pending tick
// may exist at a time; extra requests return nil while a scheduled command is
// actually pending.
func (c *AnimationClock) scheduleTick() tea.Cmd {
	tickRate := AnimationTickRateIdle
	if c.IsStreaming() {
		tickRate = AnimationTickRate
	}
	return func() tea.Msg {
		// Mark pending ONLY when Bubble Tea executes this command. Some Start()
		// callers historically dropped returned commands; setting tickPending at
		// command creation time poisons the clock with tickPending=1 but no real
		// timer, so later safety-net Tick() calls become no-ops and the spinner
		// freezes while the agent is working.
		if !atomic.CompareAndSwapInt32(&c.tickPending, 0, 1) {
			return nil
		}
		msg := tea.Tick(tickRate, func(t time.Time) tea.Msg {
			atomic.StoreInt32(&c.tickPending, 0)
			return AnimationTickMsg{}
		})()
		if msg == nil {
			atomic.StoreInt32(&c.tickPending, 0)
		}
		return msg
	}
}

// startClock begins the frame counter goroutine
func (c *AnimationClock) startClock() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return // Already running
	}

	c.running = true
	c.stopChan = make(chan struct{})

	go func() {
		ticker := time.NewTicker(AnimationTickRate)
		defer ticker.Stop()

		for {
			select {
			case <-c.stopChan:
				return
			case <-ticker.C:
				// Increment frame counter atomically
				// This is the ONLY place frames are incremented
				atomic.AddInt64(&c.frame, 1)
			}
		}
	}()
}

// stopClock stops the frame counter goroutine
func (c *AnimationClock) stopClock() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return // Already stopped
	}

	c.running = false
	close(c.stopChan)
}

// Reset resets the frame counter (useful when starting a new animation)
func (c *AnimationClock) Reset() {
	atomic.StoreInt64(&c.frame, 0)
}
