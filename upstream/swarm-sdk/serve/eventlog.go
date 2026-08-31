// Package serve — eventlog.go
//
// EventLog captures client.Subscribe events in a ring buffer for
// late-joining clients to replay. Attach the log to a running client
// via Attach, then call Events(after) to retrieve anything since a
// given sequence number.
//
// EventLog is also SSEHandler's resume backend (see sse.go): every event
// it records is assigned a durable stream_id (this EventLog's identity)
// and a monotonic sequence number, and SubscribeFrom atomically combines
// a replay snapshot with live-tail registration so a late-joining SSE
// connection can never miss or duplicate an event across that handoff.
// docs/architecture/swarm-attach/adr-004-versioned-contracts.md's "Event
// contract" governs the shape this produces; see stream.go's
// eventSchemaVersion doc comment for why that shape is implemented
// directly here rather than imported from swarm-sdk/internal/
// attachcontract (which did not exist in this working tree while this
// file was written).
package serve

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// sseListenerBuffer bounds each SubscribeFrom live-tail channel, matching
// the historical per-connection SSE buffer size (sse.go used 64 directly
// before this file owned the channel). A slow reader's channel fills and
// further sends are dropped (see appendLocked) rather than blocking the
// event-dispatch goroutine that produced them — the same "drop rather
// than block" policy SSEHandler already documented for its previous
// direct-Subscribe implementation.
const sseListenerBuffer = 64

// loggedEvent pairs a marshalled EventEnvelope with its monotonic
// sequence number, the wall-clock time it was recorded, and the Kind it
// was marshalled from (so SSEHandler can emit the correct SSE `event:
// <kind>` line for both replayed and live-tailed entries without
// re-parsing Data).
type loggedEvent struct {
	Seq  uint64
	At   time.Time
	Kind string
	Data []byte // pre-marshalled EventEnvelope JSON
}

// EventLog is a goroutine-safe ring buffer that records every Event
// emitted by a *client.Client. Call Attach to start recording; call
// Events to replay from a given sequence.
//
// The zero value is ready to use; just call Attach.
type EventLog struct {
	mu       sync.RWMutex
	buf      []loggedEvent
	cap      int
	next     uint64 // next sequence number to assign
	streamID string
	unsub    client.Unsubscribe

	// listeners holds every live-tail subscriber registered via
	// SubscribeFrom, keyed by an internal id so removeListener can target
	// exactly one without scanning by channel identity. appendLocked fans
	// each recorded entry out to all of them under the same lock that
	// protects buf, so registration (SubscribeFrom) and eviction
	// (appendLocked) can never race a connection's replay-then-tail
	// handoff.
	listeners map[uint64]chan loggedEvent
	nextSubID uint64
}

// NewEventLog creates an EventLog with the given ring-buffer capacity.
// cap must be > 0; if not, a default of 1024 is used.
func NewEventLog(capacity int) *EventLog {
	if capacity <= 0 {
		capacity = 1024
	}
	return &EventLog{
		buf:      make([]loggedEvent, 0, capacity),
		cap:      capacity,
		streamID: uuid.NewString(),
	}
}

// StreamID returns this EventLog's Event v1 stream_id (ADR-004 Event
// contract). It is generated once at NewEventLog and never changes for
// the lifetime of the log, so every event it ever records — and any gap
// event it triggers — carries the same stream identity.
func (l *EventLog) StreamID() string {
	return l.streamID
}

// Attach subscribes to c and begins recording events. Returns the
// current sequence number so callers know where "now" starts.
// Call Detach to stop recording.
func (l *EventLog) Attach(c *client.Client) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.unsub = c.Subscribe(func(ev client.Event) error {
		env := EncodeEvent(ev)
		l.mu.Lock()
		l.appendLocked(env)
		l.mu.Unlock()
		return nil
	})
	return l.next
}

// Detach stops the event log subscription. Idempotent.
func (l *EventLog) Detach() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unsub != nil {
		l.unsub()
		l.unsub = nil
	}
}

// appendLocked assigns env its Event v1 stream_id/sequence/event_id,
// marshals it, stores it in the ring buffer (evicting the oldest entry
// once cap is reached), and fans the recorded entry out to every live
// listener registered via SubscribeFrom. Callers must hold l.mu for
// writing.
//
// Every call — including one for an EventEnvelope whose Kind
// payloadForEvent (stream.go) did not recognize — advances l.next. This
// is ADR-004 Compatibility's "an unknown event kind under a compatible
// major can be ignored or preserved while still advancing the sequence
// cursor": payloadForEvent already preserves an unrecognized Kind's raw
// payload rather than dropping it (see stream.go), and this method never
// special-cases Kind at all, so an unknown kind advances the cursor
// exactly like any other event.
func (l *EventLog) appendLocked(env EventEnvelope) loggedEvent {
	seq := l.next
	l.next++

	env.StreamID = l.streamID
	env.Sequence = &seq
	env.EventID = fmt.Sprintf("%s-%d", l.streamID, seq)

	body := marshalEnvelope(env)
	entry := loggedEvent{Seq: seq, At: time.Now(), Kind: env.Kind, Data: body}

	if len(l.buf) >= l.cap {
		// Ring buffer full — overwrite oldest by shifting.
		copy(l.buf, l.buf[1:])
		l.buf[len(l.buf)-1] = entry
	} else {
		l.buf = append(l.buf, entry)
	}

	for _, ch := range l.listeners {
		select {
		case ch <- entry:
		default:
			// Slow listener — drop rather than block the producer (see
			// sseListenerBuffer's doc comment).
		}
	}
	return entry
}

// Events returns all recorded events with sequence numbers strictly
// greater than after. The returned slice is ordered by sequence
// number. Returns the current high-water-mark sequence number as well
// so callers know where to start their next poll.
//
// Callers should use this to implement ?after=seq on SSE endpoints.
func (l *EventLog) Events(after uint64) (events []loggedEvent, latest uint64) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	latest = l.next
	if latest == 0 {
		return nil, 0
	}

	return eventsAfterLocked(l.buf, after), latest
}

// Latest returns the current high-water-mark sequence number.
func (l *EventLog) Latest() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.next
}

// Retention returns the current retained sequence window: oldest is the
// smallest sequence number still held in the ring buffer, latest is the
// high-water mark (the sequence the next recorded event will receive),
// and ok is false when nothing has ever been recorded (oldest/latest are
// both meaningless in that case). Exported mainly for tests/diagnostics;
// SSEHandler's gap detection uses SubscribeFrom directly so the retention
// check and the live-tail registration happen atomically under one lock.
func (l *EventLog) Retention() (oldest, latest uint64, ok bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	latest = l.next
	if len(l.buf) == 0 {
		return 0, latest, false
	}
	return l.buf[0].Seq, latest, true
}

// SubscribeFrom registers a live-tail listener and, when after != nil,
// atomically returns the buffered replay for everything after *after —
// combining the two under one lock acquisition so no event recorded
// between "read the buffer" and "start receiving new ones" can be missed
// or double-delivered.
//
// after == nil means "no resume cursor": the historical SSEHandler
// behavior before this task (CONTRACT.md's required backward-compat
// smoke test — "no-resume-cursor connects tail-only as before"). replay
// is always nil, no gap check is performed, and the listener starts
// receiving events recorded from this call onward.
//
// after != nil means "resume after this sequence" (from Last-Event-ID or
// ?after=). If the ring buffer has evicted every event in (*after,
// oldest) — i.e. *after is older than what's retained — hasGap is true,
// oldest/latest are populated so the caller can build a GapPayload, and
// NO listener is registered (replay/ch/cancel are nil/no-op): the caller
// must surface the gap explicitly rather than silently resuming from the
// oldest retained event (ADR-004 Event contract). Otherwise replay
// contains every buffered event with Seq > *after (possibly empty if the
// caller was already caught up) and the listener is registered for
// everything after that.
//
// The returned cancel func is idempotent and safe to call from any
// goroutine; it deregisters the listener but deliberately does not close
// ch (a concurrent appendLocked could otherwise send-on-a-closed-channel)
// — callers stop reading once they call cancel, they must not rely on ch
// being closed to detect that.
func (l *EventLog) SubscribeFrom(after *uint64) (replay []loggedEvent, ch <-chan loggedEvent, cancel func(), oldest, latest uint64, hasGap bool) {
	l.mu.Lock()

	latest = l.next

	if after != nil && len(l.buf) > 0 {
		oldest = l.buf[0].Seq
		if oldest > 0 && *after < oldest-1 {
			hasGap = true
		}
	}
	if hasGap {
		l.mu.Unlock()
		return nil, nil, func() {}, oldest, latest, true
	}

	if after != nil {
		replay = eventsAfterLocked(l.buf, *after)
	}

	id := l.nextSubID
	l.nextSubID++
	lch := make(chan loggedEvent, sseListenerBuffer)
	if l.listeners == nil {
		l.listeners = make(map[uint64]chan loggedEvent)
	}
	l.listeners[id] = lch
	l.mu.Unlock()

	return replay, lch, func() { l.removeListener(id) }, oldest, latest, false
}

// removeListener deregisters a SubscribeFrom listener by id. Idempotent.
func (l *EventLog) removeListener(id uint64) {
	l.mu.Lock()
	delete(l.listeners, id)
	l.mu.Unlock()
}

// eventsAfterLocked returns every buffered event with Seq > after, in
// order, as a fresh slice (so callers may retain it after releasing the
// lock without aliasing buf's backing array). Callers must hold l.mu (for
// reading or writing).
func eventsAfterLocked(buf []loggedEvent, after uint64) []loggedEvent {
	// Binary search for the first event with Seq > after.
	lo, hi := 0, len(buf)
	for lo < hi {
		mid := (lo + hi) / 2
		if buf[mid].Seq <= after {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(buf) {
		return nil
	}
	out := make([]loggedEvent, len(buf)-lo)
	copy(out, buf[lo:])
	return out
}
