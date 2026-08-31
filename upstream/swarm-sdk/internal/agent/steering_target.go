// Package agent — steering_target.go.
//
// Phase 2 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// SteeringTarget is the subject-side effect surface invoked by the seven
// steering tools (swarm-sdk/tools/steeringtools/...) when the long-lived
// observer agent decides to intervene.
//
// Each method records a PENDING effect; effects are consumed at the next
// pre-tool boundary by SteeringStreamHook (Phase 2.6). The target itself
// performs no I/O — it is a thread-safe in-memory state machine.
//
// Tools receive the target via context.Value(steeringTargetKey{}). The
// streaming driver places it there before invoking the observer agent.
// When no target is present (e.g. during Phase 1 stub tests or when the
// driver is not wired up), tools fall back to their stub response,
// preserving backward compatibility.
package agent

import (
	"context"
	"sync"
	"time"
)

// SteeringTarget is the subject-side effect API. Implementations must be
// safe for concurrent use — the observer agent runs in a separate
// goroutine from the subject's pre-tool hook chain.
type SteeringTarget interface {
	// ArmBlockNext arms a one-shot block on the subject's next tool
	// call. If toolName is non-empty, the block fires only on the next
	// call to that named tool; otherwise it fires on the very next tool
	// regardless of name. The block clears itself after consumption.
	ArmBlockNext(toolName, reason string)

	// QueueSystemNote prepends a short instruction to the subject's
	// next system prompt. Multiple notes accumulate in priority order
	// (higher first).
	QueueSystemNote(text string, priority int)

	// QueueRefocus is a stronger variant of QueueSystemNote that
	// emphasises returning attention to a specific anchor task.
	QueueRefocus(anchor, reminder string)

	// HaltPeerLoop asks the A2A runtime to drop inbound DMs from a
	// specific peer for a bounded TTL. Phase 2 records the intent;
	// wiring to the live A2A runtime lands in Phase 3.
	HaltPeerLoop(peer, reason string, ttl time.Duration) error

	// LogConcern records a non-blocking observation that the subject's
	// trajectory looks off. Surfaces to observability without altering
	// behavior.
	LogConcern(text string, severity string)

	// RecordAskUser records that the observer wants to ask the user a
	// question. The actual UI invocation is delegated by the streaming
	// driver to the SDK's interaction adapter; the target itself only
	// records intent so the call returns immediately and the observer
	// goroutine doesn't block.
	RecordAskUser(question string, urgency string)
}

// steeringTargetKey is the context key used to convey the target into
// tool Run() invocations. Unexported so external packages must go
// through the exported helpers below.
type steeringTargetKey struct{}

// WithSteeringTarget returns a derived context carrying target. The
// streaming driver wraps the observer's ctx with this before calling
// observerAgent.Execute(...).
func WithSteeringTarget(ctx context.Context, target SteeringTarget) context.Context {
	return context.WithValue(ctx, steeringTargetKey{}, target)
}

// SteeringTargetFromContext returns the target carried on ctx, if any.
// Tool Run() bodies use the ok value to decide between real effect and
// the Phase-1 stub fallback.
func SteeringTargetFromContext(ctx context.Context) (SteeringTarget, bool) {
	t, ok := ctx.Value(steeringTargetKey{}).(SteeringTarget)
	return t, ok
}

// ---------------------------------------------------------------------------
// Default implementation.
// ---------------------------------------------------------------------------

// PendingBlock is a one-shot armed block on a future subject tool call.
type PendingBlock struct {
	ToolName string // empty means "any next tool"
	Reason   string
	ArmedAt  time.Time
}

// PendingNote is a queued system-prompt insertion for the subject's
// next turn.
type PendingNote struct {
	Text     string
	Priority int
	QueuedAt time.Time
}

// PendingRefocus is a stronger refocus directive that supersedes
// queued notes when consumed.
type PendingRefocus struct {
	Anchor   string
	Reminder string
	QueuedAt time.Time
}

// PendingHalt is a recorded request to mute a peer for TTL.
type PendingHalt struct {
	Peer       string
	Reason     string
	TTLSeconds int
	ArmedAt    time.Time
}

// LoggedConcern is a non-blocking observation the steering layer wants
// to surface for audit.
type LoggedConcern struct {
	Text     string
	Severity string
	LoggedAt time.Time
}

// RecordedAskUser is a deferred ask_user request awaiting adapter
// wiring (Phase 3).
type RecordedAskUser struct {
	Question   string
	Urgency    string
	RecordedAt time.Time
}

// defaultSteeringTarget is the canonical thread-safe implementation
// returned by NewDefaultSteeringTarget. It stores armed effects in
// memory under a single mutex.
type defaultSteeringTarget struct {
	mu       sync.Mutex
	block    *PendingBlock
	refocus  *PendingRefocus
	notes    []PendingNote
	halts    []PendingHalt
	concerns []LoggedConcern
	asks     []RecordedAskUser
}

// NewDefaultSteeringTarget constructs the canonical in-memory target.
// Streaming driver instances each get their own target.
func NewDefaultSteeringTarget() SteeringTarget {
	return &defaultSteeringTarget{}
}

// ArmBlockNext records a one-shot block. A second call before
// ConsumeBlockFor overwrites the previous block (last-write-wins).
func (t *defaultSteeringTarget) ArmBlockNext(toolName, reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.block = &PendingBlock{
		ToolName: toolName,
		Reason:   reason,
		ArmedAt:  time.Now(),
	}
}

// QueueSystemNote appends a note to the queue. Notes accumulate; the
// hook drains them on the next pre-tool boundary.
func (t *defaultSteeringTarget) QueueSystemNote(text string, priority int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.notes = append(t.notes, PendingNote{
		Text:     text,
		Priority: priority,
		QueuedAt: time.Now(),
	})
}

// QueueRefocus overwrites any prior refocus (last-write-wins).
func (t *defaultSteeringTarget) QueueRefocus(anchor, reminder string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.refocus = &PendingRefocus{
		Anchor:   anchor,
		Reminder: reminder,
		QueuedAt: time.Now(),
	}
}

// HaltPeerLoop records a halt with clamped TTL. The A2A runtime wiring
// arrives in Phase 3; for now we only persist the intent. Returns nil
// to satisfy the interface.
func (t *defaultSteeringTarget) HaltPeerLoop(peer, reason string, ttl time.Duration) error {
	secs := int(ttl.Seconds())
	if secs <= 0 {
		secs = 60
	}
	if secs > 600 {
		secs = 600
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.halts = append(t.halts, PendingHalt{
		Peer:       peer,
		Reason:     reason,
		TTLSeconds: secs,
		ArmedAt:    time.Now(),
	})
	return nil
}

// LogConcern appends to the concern log.
func (t *defaultSteeringTarget) LogConcern(text, severity string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.concerns = append(t.concerns, LoggedConcern{
		Text:     text,
		Severity: severity,
		LoggedAt: time.Now(),
	})
}

// RecordAskUser appends to the deferred ask queue.
func (t *defaultSteeringTarget) RecordAskUser(question, urgency string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.asks = append(t.asks, RecordedAskUser{
		Question:   question,
		Urgency:    urgency,
		RecordedAt: time.Now(),
	})
}

// ---------------------------------------------------------------------------
// Inspection helpers (used by SteeringStreamHook in Phase 2.6).
// ---------------------------------------------------------------------------

// ConsumeBlockFor returns and clears any armed block matching toolName.
// A block with empty ToolName matches every call. Returns (nil, false)
// when no block is armed or when the armed block names a different
// tool.
func (t *defaultSteeringTarget) ConsumeBlockFor(toolName string) (*PendingBlock, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.block == nil {
		return nil, false
	}
	if t.block.ToolName != "" && t.block.ToolName != toolName {
		return nil, false
	}
	out := t.block
	t.block = nil
	return out, true
}

// DrainNotes returns and clears all queued notes, sorted by priority
// (descending) and then by queue order.
func (t *defaultSteeringTarget) DrainNotes() []PendingNote {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.notes) == 0 {
		return nil
	}
	out := make([]PendingNote, len(t.notes))
	copy(out, t.notes)
	t.notes = t.notes[:0]
	// Simple insertion-sort by priority desc; n is tiny in practice.
	for i := 1; i < len(out); i++ {
		j := i
		for j > 0 && out[j].Priority > out[j-1].Priority {
			out[j], out[j-1] = out[j-1], out[j]
			j--
		}
	}
	return out
}

// ConsumeRefocus returns and clears any queued refocus directive.
func (t *defaultSteeringTarget) ConsumeRefocus() (*PendingRefocus, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.refocus == nil {
		return nil, false
	}
	out := t.refocus
	t.refocus = nil
	return out, true
}

// SnapshotHalts returns a copy of recorded halts. Does NOT clear —
// halts are durable until the streaming driver pushes them to A2A.
func (t *defaultSteeringTarget) SnapshotHalts() []PendingHalt {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.halts) == 0 {
		return nil
	}
	out := make([]PendingHalt, len(t.halts))
	copy(out, t.halts)
	return out
}

// SnapshotConcerns returns a copy of logged concerns. Does NOT clear.
func (t *defaultSteeringTarget) SnapshotConcerns() []LoggedConcern {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.concerns) == 0 {
		return nil
	}
	out := make([]LoggedConcern, len(t.concerns))
	copy(out, t.concerns)
	return out
}

// SnapshotAsks returns a copy of pending ask_user requests. Does NOT
// clear — the interaction adapter (Phase 3) drains them explicitly.
func (t *defaultSteeringTarget) SnapshotAsks() []RecordedAskUser {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.asks) == 0 {
		return nil
	}
	out := make([]RecordedAskUser, len(t.asks))
	copy(out, t.asks)
	return out
}

// DrainHalts returns all pending halts and clears them. Phase 3 contract:
// halts are durable across observer flushes until pushEffects drains them
// into the A2A runtime.
func (t *defaultSteeringTarget) DrainHalts() []PendingHalt {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.halts) == 0 {
		return nil
	}
	out := make([]PendingHalt, len(t.halts))
	copy(out, t.halts)
	t.halts = t.halts[:0]
	return out
}

// DrainAsks returns all pending ask_user requests and clears them. Phase 3
// contract: asks are durable across observer flushes until pushEffects
// drains them into the interaction adapter.
func (t *defaultSteeringTarget) DrainAsks() []RecordedAskUser {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.asks) == 0 {
		return nil
	}
	out := make([]RecordedAskUser, len(t.asks))
	copy(out, t.asks)
	t.asks = t.asks[:0]
	return out
}

// DefaultSteeringTargetInspector exposes the inspection helpers on the
// canonical implementation. Callers (typically SteeringStreamHook) type-
// assert the SteeringTarget they hold to this interface when they need
// to consume armed effects. Tests can also implement this directly.
type DefaultSteeringTargetInspector interface {
	ConsumeBlockFor(toolName string) (*PendingBlock, bool)
	DrainNotes() []PendingNote
	ConsumeRefocus() (*PendingRefocus, bool)
	DrainHalts() []PendingHalt
	DrainAsks() []RecordedAskUser
	SnapshotHalts() []PendingHalt
	SnapshotConcerns() []LoggedConcern
	SnapshotAsks() []RecordedAskUser
}

// Ensure defaultSteeringTarget satisfies the inspector interface at
// compile time.
var _ DefaultSteeringTargetInspector = (*defaultSteeringTarget)(nil)

// PeerMuter is the narrow interface the streaming driver needs to push
// halt_peer_loop effects into the A2A runtime. The a2a.PeerMuteStore
// satisfies this without importing the agent package — clean dependency
// direction (a2a does NOT import agent).
type PeerMuter interface {
	MutePeer(peer, reason string, ttl time.Duration)
}

// AskUserPusher surfaces ask_user requests to the UI / interaction layer.
// Phase 4 contract: synchronous. The observer's ask_user tool call blocks
// until the user replies (or the timeout fires). The returned answer is
// surfaced back to the observer LLM as the tool's result. On timeout or
// error, callers fall back to params.Default.
//
// The subject agent is NOT paused — only the observer goroutine blocks.
type AskUserPusher interface {
	Ask(ctx context.Context, question, urgency string, timeout time.Duration) (string, error)
}

// askUserPusherKey is the context key for AskUserPusher carried alongside
// the SteeringTarget. The streaming driver places the pusher on ctx before
// invoking observer.Observe so the ask_user tool can find it.
type askUserPusherKey struct{}

// WithAskUserPusher returns a derived ctx carrying p. The streaming driver
// wraps the observer's ctx with this when cfg.AskAdapter is non-nil.
func WithAskUserPusher(ctx context.Context, p AskUserPusher) context.Context {
	return context.WithValue(ctx, askUserPusherKey{}, p)
}

// AskUserPusherFromContext returns the pusher carried on ctx, if any.
// The ask_user tool uses ok to choose between the synchronous (Phase 4)
// path and the Phase 3 record-intent fallback.
func AskUserPusherFromContext(ctx context.Context) (AskUserPusher, bool) {
	p, ok := ctx.Value(askUserPusherKey{}).(AskUserPusher)
	return p, ok
}
