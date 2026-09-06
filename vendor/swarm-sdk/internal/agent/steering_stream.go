// Package agent — steering_stream.go.
//
// Phase 2 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// This file implements the long-lived streaming steering driver. The
// driver subscribes to subject-agent events (delivered via SteeringStreamHook,
// Phase 2.6) and forwards them, in batches, to a long-lived observer
// agent equipped with the seven tools in swarm-sdk/tools/steeringtools.
// When the observer chooses to intervene, its tool calls mutate the
// driver's SteeringTarget; SteeringStreamHook then consumes those
// pending effects at the subject's next pre-tool boundary.
//
// Phase 2.3 (this commit) wires the event pump and Enqueue plumbing.
// The flush() body is a placeholder that drains the buffer without
// invoking the observer; Phase 2.4 fills it with the real LLM call.
//
// Design contract:
//
//   - Driver MUST be safe to construct when Mode != stream. Start() is
//     a no-op in that case; Enqueue() silently drops events.
//   - Driver MUST NOT call any provider Chat() when no events are pending.
//   - Driver MUST drop OLDEST events on backpressure (not newest), so
//     fresh signal isn't starved by stale tool-result spam.
//   - Driver MUST be safe for concurrent Enqueue from many goroutines.
//   - Stop() MUST wait for the pump goroutine to fully exit before
//     returning.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// Observer is the abstraction the streaming driver needs to consult an
// observer agent. The production implementation wraps *Agent.Execute
// (see agentObserver). Tests inject fakes that record the transcript
// and (optionally) apply effects via the SteeringTarget on ctx.
type Observer interface {
	// Observe receives a transcript of recent subject events and may
	// invoke steering tools to mutate the SteeringTarget on ctx. The
	// returned error is logged but does not stop the pump.
	Observe(ctx context.Context, transcript string) error
}

// agentObserver adapts *Agent to the Observer interface.
type agentObserver struct {
	a *Agent
}

// Observe calls a.Execute with the transcript as the user message. The
// observer's tools (registered as steeringtools.All()) are responsible
// for reaching back into ctx for the SteeringTarget and applying effects.
func (o *agentObserver) Observe(ctx context.Context, transcript string) error {
	if o == nil || o.a == nil {
		return nil
	}
	_, err := o.a.Execute(ctx, ExecuteRequest{Message: transcript})
	return err
}

// SteeringDriverConfig configures the streaming steering driver.
type SteeringDriverConfig struct {
	// Mode is the effective steering mode. When this is not SteeringModeStream
	// the driver is created but inert: Start() is a no-op and Stop() is safe.
	Mode SteeringMode

	// EventBufferSize is the bounded channel capacity for queued events.
	// A reasonable default of 64 is used when zero.
	EventBufferSize int

	// MaxToolCallsPerMinute caps the steering agent's action rate so it
	// cannot become the new noisy neighbor. Zero disables the cap (not
	// recommended outside tests).
	MaxToolCallsPerMinute int

	// SteeringAgent is the long-lived observer agent. When non-nil and
	// cfg.Observer is nil, the driver wraps this in an agentObserver
	// adapter at Start().
	SteeringAgent *Agent

	// Observer overrides the default agent-backed observer. Tests
	// supply a fake here; production typically leaves it nil and sets
	// SteeringAgent instead.
	Observer Observer

	// Target is the subject-side effect surface. Defaults to
	// NewDefaultSteeringTarget() when nil.
	Target SteeringTarget

	// FlushCount triggers a flush after this many events. Default 5.
	FlushCount int

	// FlushAge triggers a flush after the oldest buffered event reaches
	// this age. Default 2s.
	FlushAge time.Duration

	// DriftEnabled gates the drift tracker. When false, no drift metadata
	// is appended to the transcript. Default false.
	DriftEnabled bool

	// Drift configures the drift detector thresholds. Fields default when
	// zero. Only consulted when DriftEnabled is true.
	Drift DriftConfig

	// A2ARuntime pushes halt_peer_loop effects to the A2A runtime.
	// Nil = halts are recorded but not pushed.
	A2ARuntime PeerMuter

	// AskAdapter pushes ask_user requests to the interaction layer.
	// Nil = asks are recorded but not surfaced.
	AskAdapter AskUserPusher

	// AuditLogPath is the path to append JSONL steering events.
	// Empty = no disk-persisted audit trail.
	AuditLogPath string

	// Logger is used for audit snapshots and pushEffects logging.
	// Nil = no audit logging.
	Logger observability.Logger
}

// StreamingSteeringDriver is the long-lived steering agent driver.
//
// Construction is always safe. Behavior is gated on Mode():
//   - poll: Start/Stop/Enqueue are silent no-ops.
//   - stream: Start spawns the event pump; Enqueue forwards events;
//     Stop cancels and waits.
type StreamingSteeringDriver struct {
	cfg SteeringDriverConfig

	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	events   chan hooks.Event
	target   SteeringTarget
	observer Observer
	drift    *DriftTracker
	auditLog *AuditLog
	logger   observability.Logger
	wg       sync.WaitGroup

	// Diagnostic counters — exported via accessors below.
	droppedTotal atomic.Uint64
	flushedTotal atomic.Uint64
}

// NewStreamingSteeringDriver builds a driver from cfg, applying defaults.
func NewStreamingSteeringDriver(cfg SteeringDriverConfig) *StreamingSteeringDriver {
	if cfg.EventBufferSize <= 0 {
		cfg.EventBufferSize = 64
	}
	if cfg.FlushCount <= 0 {
		cfg.FlushCount = 5
	}
	if cfg.FlushAge <= 0 {
		cfg.FlushAge = 2 * time.Second
	}
	d := &StreamingSteeringDriver{cfg: cfg}
	if cfg.Target != nil {
		d.target = cfg.Target
	} else {
		d.target = NewDefaultSteeringTarget()
	}
	if cfg.DriftEnabled {
		d.drift = NewDriftTracker(cfg.Drift)
	}
	d.logger = cfg.Logger
	return d
}

// Mode returns the effective steering mode the driver was configured with.
func (d *StreamingSteeringDriver) Mode() SteeringMode {
	return ResolveSteeringMode(d.cfg.Mode)
}

// IsStreaming reports whether the driver is in active streaming mode. Used
// by hooks to decide whether to enqueue events.
func (d *StreamingSteeringDriver) IsStreaming() bool {
	return d.Mode() == SteeringModeStream
}

// Target returns the SteeringTarget this driver writes pending effects
// to. Hooks read armed effects from this target at pre-tool boundaries.
func (d *StreamingSteeringDriver) Target() SteeringTarget {
	return d.target
}

// DroppedTotal reports the cumulative number of events that were
// dropped due to backpressure on a full buffer.
func (d *StreamingSteeringDriver) DroppedTotal() uint64 {
	return d.droppedTotal.Load()
}

// FlushedTotal reports the cumulative number of flush invocations.
// Useful for tests asserting that the pump woke up at all.
func (d *StreamingSteeringDriver) FlushedTotal() uint64 {
	return d.flushedTotal.Load()
}

// Start activates the driver. When Mode != stream this is a no-op.
// Idempotent: a second call while running returns nil without effect.
//
// On the first successful call, Start derives the observer:
//   - cfg.Observer wins if non-nil (test path).
//   - else if cfg.SteeringAgent is non-nil, wrap it in agentObserver.
//   - else leave nil — flush() will skip the LLM call but still bump
//     flushedTotal for observability.
func (d *StreamingSteeringDriver) Start(ctx context.Context) error {
	if !d.IsStreaming() {
		return nil
	}
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return nil
	}
	childCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.events = make(chan hooks.Event, d.cfg.EventBufferSize)
	switch {
	case d.cfg.Observer != nil:
		d.observer = d.cfg.Observer
	case d.cfg.SteeringAgent != nil:
		d.observer = &agentObserver{a: d.cfg.SteeringAgent}
	default:
		d.observer = nil
	}

	// Initialize audit log if path is configured
	if d.cfg.AuditLogPath != "" {
		auditLog, err := NewAuditLog(d.cfg.AuditLogPath)
		if err != nil {
			d.running = false
			d.mu.Unlock()
			return fmt.Errorf("failed to create audit log: %w", err)
		}
		d.auditLog = auditLog
	}

	d.running = true
	d.wg.Add(1)
	d.mu.Unlock()

	go d.pump(childCtx)
	return nil
}

// Stop deactivates the driver. Safe to call when never started or in poll
// mode. Waits for the pump goroutine to fully exit before returning.
func (d *StreamingSteeringDriver) Stop() error {
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return nil
	}
	if d.cancel != nil {
		d.cancel()
	}
	d.running = false
	d.mu.Unlock()

	d.wg.Wait()

	// Close audit log if present
	if d.auditLog != nil {
		d.auditLog.Close()
	}

	return nil
}

// Enqueue posts a hook event to the pump. Non-blocking: when the buffer
// is full, the OLDEST event is dropped and droppedTotal is incremented.
// Safe to call before Start() — the event is silently discarded.
//
// Calling on a poll-mode driver is also a silent drop, so hook authors
// can call Enqueue unconditionally.
func (d *StreamingSteeringDriver) Enqueue(ev hooks.Event) {
	if !d.IsStreaming() {
		return
	}
	// Update drift tracker (O(1), under mutex).
	if d.drift != nil {
		d.drift.RecordEvent(ev)
	}
	d.mu.Lock()
	ch := d.events
	d.mu.Unlock()
	if ch == nil {
		// Start() has not run, or Stop() has cleared it. Silently drop.
		return
	}

	// Fast path: room available, send wins immediately.
	select {
	case ch <- ev:
		return
	default:
	}

	// Slow path: buffer full. Drop the oldest entry and try again.
	// We may race with the pump consuming an entry between the two
	// operations; that's fine — at worst we don't have to drop, in
	// which case the event still lands and droppedTotal stays put.
	select {
	case <-ch:
		d.droppedTotal.Add(1)
	default:
	}
	select {
	case ch <- ev:
	default:
		// Still no room (pump is slow AND another producer beat us to
		// the slot). Drop this event too — count it.
		d.droppedTotal.Add(1)
	}
}

// pump is the long-running goroutine that batches events and triggers
// flushes by count or by age threshold.
func (d *StreamingSteeringDriver) pump(ctx context.Context) {
	defer d.wg.Done()

	// Wake up frequently enough to detect age-threshold flushes without
	// excessive churn. min(flushAge/4, 250ms) is responsive without
	// busy-looping.
	tickInterval := max(min(d.cfg.FlushAge/4, 250*time.Millisecond), 10*time.Millisecond)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	var buf []hooks.Event
	var bufStart time.Time

	for {
		select {
		case <-ctx.Done():
			// Final drain: best-effort flush of any pending events
			// using a fresh background ctx so cancellation doesn't
			// abort the flush itself.
			if len(buf) > 0 {
				d.flush(context.Background(), buf)
			}
			return

		case ev, ok := <-d.events:
			if !ok {
				return
			}
			if len(buf) == 0 {
				bufStart = time.Now()
			}
			buf = append(buf, ev)
			if len(buf) >= d.cfg.FlushCount {
				d.flush(ctx, buf)
				buf = buf[:0]
			}

		case <-ticker.C:
			if len(buf) > 0 && time.Since(bufStart) >= d.cfg.FlushAge {
				d.flush(ctx, buf)
				buf = buf[:0]
			}
		}
	}
}

// flush hands a batch of events to the observer for evaluation.
//
// Flow:
//  1. Render `batch` into a compact transcript fragment.
//  2. Mark ctx as steering-reentrant (so the observer's own LLM call
//     and any tool dispatches won't recursively fire steering hooks)
//     and attach the SteeringTarget so tools in steeringtools/* can
//     reach back via SteeringTargetFromContext.
//  3. Call observer.Observe(ctx, fragment). Errors are swallowed (the
//     pump must keep running for the lifetime of the subject agent).
//
// When d.observer is nil (e.g. tests that exercise only the pump), the
// transcript is rendered and counted but no LLM call is made.
func (d *StreamingSteeringDriver) flush(ctx context.Context, batch []hooks.Event) {
	if len(batch) == 0 {
		return
	}
	d.flushedTotal.Add(1)
	if d.observer == nil {
		return
	}

	fragment := renderEventTranscript(batch)
	// Append drift metadata (read-only — does not mutate the target).
	if d.drift != nil {
		fragment += "\n" + d.drift.Summary()
	}
	obsCtx := WithSteeringReentrancy(WithSteeringTarget(ctx, d.target))
	// Phase 4: place the ask adapter on ctx so the ask_user tool can call
	// it synchronously inside Observe().
	if d.cfg.AskAdapter != nil {
		obsCtx = WithAskUserPusher(obsCtx, d.cfg.AskAdapter)
	}
	_ = d.observer.Observe(obsCtx, fragment)

	// Phase 3: push pending effects (halts → A2A, asks → UI).
	d.pushEffects(ctx)

	// Phase 3: log audit snapshot.
	d.auditSnapshot(ctx)

	// Phase 5: write audit log entry
	d.writeAuditEntry(ctx, batch)
}

// renderEventTranscript produces a small, deterministic human-readable
// summary of the buffered events for the observer's prompt. Format:
//
//	[tool.before_execute] name=Bash params={...}
//	[tool.after_execute]  name=Bash output="...truncated..."
//	[tool.before_execute] name=Read params={file_path=...}
//
// Truncation keeps the transcript bounded; the observer doesn't need
// the full payload to judge drift.
func renderEventTranscript(batch []hooks.Event) string {
	const maxValChars = 240
	var b strings.Builder
	for i, ev := range batch {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteByte('[')
		b.WriteString(string(ev.Type))
		b.WriteByte(']')
		if ev.Data == nil {
			continue
		}
		// Stable order over a small, well-known set of keys so the
		// transcript is deterministic across map iterations.
		for _, k := range []string{"tool_name", "name", "params", "tool_input", "output", "result", "error"} {
			v, ok := ev.Data[k]
			if !ok {
				continue
			}
			b.WriteByte(' ')
			b.WriteString(k)
			b.WriteByte('=')
			s := fmt.Sprintf("%v", v)
			if len(s) > maxValChars {
				s = s[:maxValChars] + "…"
			}
			b.WriteString(s)
		}
	}
	return b.String()
}

// pushEffects drains pending halts and asks from the SteeringTarget and
// pushes them to the configured A2A runtime and ask adapter.
// Uses Drain (not Snapshot) so entries are consumed exactly once.
//
// Phase 4 note on DrainAsks: this branch is now vestigial. The synchronous
// ask_user path (Phase 4B) handles the question inside Observe() and never
// touches target.RecordAskUser. This loop is kept so that any pre-Phase-4
// caller code that still calls RecordAskUser via the legacy stub path
// still has its asks surfaced to the adapter. Each recorded ask is
// surfaced fire-and-forget with a 30s timeout; the answer is discarded
// because there is no observer waiting for it.
func (d *StreamingSteeringDriver) pushEffects(ctx context.Context) {
	insp, ok := d.target.(DefaultSteeringTargetInspector)
	if !ok {
		return
	}

	if d.cfg.A2ARuntime != nil {
		for _, h := range insp.DrainHalts() {
			d.cfg.A2ARuntime.MutePeer(h.Peer, h.Reason,
				time.Duration(h.TTLSeconds)*time.Second)
		}
	}

	if d.cfg.AskAdapter != nil {
		for _, a := range insp.DrainAsks() {
			// Fire-and-forget; answer discarded because the originating
			// tool call already returned. Use background ctx so a flush
			// cancellation doesn't abort an in-flight question.
			_, _ = d.cfg.AskAdapter.Ask(context.Background(), a.Question, a.Urgency, 30*time.Second)
		}
	}
}

// auditSnapshot logs a one-line audit of accumulated steering state.
// Uses Snapshot (read-only, no drain) because audit is an observer, not a
// consumer.
func (d *StreamingSteeringDriver) auditSnapshot(ctx context.Context) {
	if d.logger == nil {
		return
	}
	insp, ok := d.target.(DefaultSteeringTargetInspector)
	if !ok {
		return
	}
	d.logger.Info(ctx, "steering.audit",
		observability.F("concerns", len(insp.SnapshotConcerns())),
		observability.F("flushed", d.FlushedTotal()),
		observability.F("dropped", d.DroppedTotal()),
	)
}

// writeAuditEntry writes a JSONL audit entry to disk for forensic analysis.
// Captures the event batch, observer fragment, and any effects that were armed.
func (d *StreamingSteeringDriver) writeAuditEntry(ctx context.Context, batch []hooks.Event) {
	if d.auditLog == nil {
		return
	}

	// Create the base audit record
	rec := AuditRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Kind:      "flush",
		Flushed:   d.FlushedTotal(),
		Dropped:   d.DroppedTotal(),
	}

	// Add effects snapshot if available
	if insp, ok := d.target.(DefaultSteeringTargetInspector); ok {
		concerns := insp.SnapshotConcerns()
		if len(concerns) > 0 {
			rec.Concerns = len(concerns)
			// For detailed concern tracking, we'd need to write multiple records
			// or extend AuditRecord to include concern details
		}

		// Write separate records for halts and asks
		for _, h := range insp.SnapshotHalts() {
			d.auditLog.Write(AuditRecord{
				Kind:       "halt",
				Peer:       h.Peer,
				Reason:     h.Reason,
				TTLSeconds: int(h.TTLSeconds),
			})
		}

		for _, a := range insp.SnapshotAsks() {
			d.auditLog.Write(AuditRecord{
				Kind:     "ask",
				Question: a.Question,
				Urgency:  a.Urgency,
			})
		}
	}

	// Write the main flush record
	d.auditLog.Write(rec)
}

// ErrSteeringDriverNotReady is returned by driver methods when an API is
// invoked before Start(). Currently unused externally; reserved for
// later phases that may surface a hard error rather than a silent drop.
var ErrSteeringDriverNotReady = errors.New("agent: streaming steering driver not started")
