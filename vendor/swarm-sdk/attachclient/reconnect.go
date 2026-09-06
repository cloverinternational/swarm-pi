package attachclient

import (
	"context"
	"fmt"
	"math"
	"time"
)

// BackoffPolicy bounds attachclient's reconnect timing. It is a hard
// invariant (package-boundaries.md's "Focused tests: ... bounded retry")
// that retry is bounded, not infinite: MaxRetries caps the number of
// consecutive failed connect attempts before the event stream terminates
// with an error on EventStream.Errs instead of retrying forever.
type BackoffPolicy struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
	// MaxRetries is the ceiling on consecutive failed OpenEvents attempts
	// before giving up. MaxRetries <= 0 in a policy built by
	// DefaultBackoffPolicy is never produced; a caller-constructed zero
	// BackoffPolicy is replaced by DefaultBackoffPolicy in New (see
	// client.go), so production code always retries a bounded number of
	// times.
	MaxRetries int
}

// DefaultBackoffPolicy is the production default: exponential backoff from
// 250ms up to a 10s ceiling, capped at 8 consecutive failed attempts.
func DefaultBackoffPolicy() BackoffPolicy {
	return BackoffPolicy{
		Initial:    250 * time.Millisecond,
		Max:        10 * time.Second,
		Multiplier: 2,
		MaxRetries: 8,
	}
}

// next returns the backoff delay before retry number attempt (1-based).
func (b BackoffPolicy) next(attempt int) time.Duration {
	if attempt <= 1 {
		return b.Initial
	}
	mult := b.Multiplier
	if mult <= 0 {
		mult = 2
	}
	d := float64(b.Initial) * math.Pow(mult, float64(attempt-1))
	if b.Max > 0 && (d > float64(b.Max) || d <= 0) {
		d = float64(b.Max)
	}
	return time.Duration(d)
}

// newEventStream constructs the caller-facing EventStream and starts the
// reconnect state machine in its own goroutine. parent is the caller's
// context (e.g. the Client.Events(ctx, ...) argument); Close cancels a
// child of parent so cancelling parent OR calling Close both terminate the
// loop, and Detach (client.go) calls Close on every outstanding stream it
// created without ever touching the network — the loop's own defer close(out)
// and ctx.Done() checks guarantee prompt, resource-only termination.
func newEventStream(parent context.Context, transport Transport, base string, start Cursor, backoff BackoffPolicy, token string, onAck func(Cursor)) EventStream {
	ctx := parent
	if token != "" {
		ctx = withCredential(ctx, token)
	}
	loopCtx, cancel := context.WithCancel(ctx)

	out := make(chan Event, 64)
	errs := make(chan error, 1)

	go reconnectLoop(loopCtx, transport, base, start, backoff, out, errs, onAck)

	return EventStream{
		Events: out,
		Errs:   errs,
		Close:  cancel,
	}
}

// reconnectLoop is attachclient's reconnect/backoff/gap state machine. It:
//
//   - opens transport.OpenEvents(ctx, base, cursor) starting from the given
//     (possibly previously-acknowledged) cursor;
//   - on a failed connect, retries with bounded exponential backoff
//     (BackoffPolicy) and reports a terminal error on errs once MaxRetries
//     consecutive failures are reached — it never retries forever;
//   - on a successful connect, resets the attempt counter (a working
//     connection means the peer is reachable again) and streams decoded
//     events out, advancing and acknowledging cursor as each event is
//     delivered (onAck, wired to the client's CursorStore in client.go);
//   - on an explicit gap (decodeEvent returning a *GapError), forwards a
//     Kind == "gap" Event carrying it instead of silently skipping the
//     missing history, then resumes from the daemon-reported oldest
//     retained position;
//   - exits immediately and closes out when ctx is cancelled (Detach or the
//     caller's own context), making no further Transport calls at all.
func reconnectLoop(ctx context.Context, transport Transport, base string, cursor Cursor, backoff BackoffPolicy, out chan<- Event, errs chan<- error, onAck func(Cursor)) {
	defer close(out)

	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}

		stream, err := transport.OpenEvents(ctx, base, cursor)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			attempt++
			if backoff.MaxRetries > 0 && attempt > backoff.MaxRetries {
				select {
				case errs <- fmt.Errorf("attachclient: reconnect exhausted after %d attempt(s): %w", attempt-1, err):
				default:
				}
				return
			}
			wait := backoff.next(attempt)
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
				continue
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}

		// A successful connect resets the failure streak.
		attempt = 0
		cursor = drainStream(ctx, stream, cursor, out, onAck)
		if ctx.Err() != nil {
			return
		}
		// drainStream returned because the connection ended (EOF/read
		// error) but ctx is still live: loop back around and reconnect
		// from the last acknowledged cursor.
	}
}

// drainStream reads events off one open stream until it errors/ends or ctx
// is cancelled, forwarding decoded events (or an explicit gap) to out and
// returning the last acknowledged cursor so the caller can resume from it.
func drainStream(ctx context.Context, stream RawEventStream, cursor Cursor, out chan<- Event, onAck func(Cursor)) Cursor {
	defer stream.Close()
	for {
		raw, err := stream.Next(ctx)
		if err != nil {
			return cursor
		}

		ev, gap := decodeEvent(raw)
		if gap != nil {
			gapEvent := Event{Kind: "gap", Gap: gap}
			select {
			case out <- gapEvent:
			case <-ctx.Done():
				return cursor
			}
			// Never silently skip the missing history: surface the gap
			// (above), then resume immediately after the daemon's oldest
			// retained sequence rather than guessing forward from the
			// stale cursor.
			if gap.OldestRetained > 0 {
				cursor = Cursor{StreamID: gap.StreamID, Sequence: gap.OldestRetained - 1}
			}
			continue
		}

		select {
		case out <- ev:
			// Only advance the acknowledged cursor when the event actually
			// carried resume-position information; legacy/no-sequence
			// events are still delivered but do not move the bookmark
			// (ADR-004 Compatibility: "unversioned events may be
			// displayed without claiming replay completeness").
			if ev.Cursor.Sequence != 0 {
				cursor = ev.Cursor
			}
			if onAck != nil {
				onAck(cursor)
			}
		case <-ctx.Done():
			return cursor
		}
	}
}
