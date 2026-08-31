package serve

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
)

// sseLogsMu/sseLogs back eventLogFor's per-Mux lazy EventLog. This lives
// at package scope rather than as a *Mux struct field because mux.go is
// outside this task's FILES ownership (see CONTRACT.md's "# FILES:"
// header and its "YOU MAY EDIT ONLY THE FILES LISTED" rule) — wiring the
// ring buffer into SSEHandler without editing mux.go means the
// association has to live here instead of on Mux itself.
//
// Entries are never evicted, matching the same accepted lifetime
// trade-off swarm-ic/backend/internal/server/serve_mount.go already makes
// for its own `muxes map[*client.Client]*serve.Mux` cache: a *Mux is
// expected to live for the lifetime of the process (or daemon session)
// that created it, not to be created and discarded in a hot loop.
var (
	sseLogsMu sync.Mutex
	sseLogs   = make(map[*Mux]*EventLog)
)

// eventLogFor lazily creates (once per *Mux, on first use) and returns
// the *EventLog backing that Mux's SSEHandler resume/replay support. The
// log auto-attaches to m.client so every event the client's Subscribe bus
// emits is recorded — the same subscription SSEHandler used to make
// directly before this task; that direct subscription (and its own
// unsub-per-connection) is replaced by EventLog.SubscribeFrom's
// per-connection live-tail channel below.
func eventLogFor(m *Mux) *EventLog {
	sseLogsMu.Lock()
	defer sseLogsMu.Unlock()
	if l, ok := sseLogs[m]; ok {
		return l
	}
	l := NewEventLog(0)
	l.Attach(m.client)
	sseLogs[m] = l
	return l
}

// parseResumeCursor extracts a resume cursor from r per CONTRACT.md
// ("support Last-Event-ID HTTP header and/or a ?after=<sequence> query
// parameter for resume"). The ?after= query parameter takes precedence
// when both are present (it is the more deliberate, harder-to-set-by-
// accident of the two — a browser's native EventSource sets Last-Event-ID
// automatically from the last "id:" line it saw, while ?after= only
// appears if the caller built the URL itself).
//
// ok is false when neither is supplied, or when the one supplied does not
// parse as a base-10 uint64 — CONTRACT.md's required backward-compat
// smoke test is "no-resume-cursor connects tail-only as before", and a
// malformed cursor (more likely a proxy/browser quirk than a deliberate,
// well-formed replay request) degrades to that same tail-only behavior
// rather than erroring the whole connection.
func parseResumeCursor(r *http.Request) (after uint64, ok bool) {
	if v := r.URL.Query().Get("after"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	return 0, false
}

// SSEHandler returns an http.Handler that streams Client.Subscribe events
// to one HTTP client per connection as Server-Sent Events.
//
// Wire format (per event):
//
//	id: <sequence>
//	event: <kind>
//	data: <json envelope>
//	\n
//
// Emitting "id: <sequence>" on every event means a browser's native
// EventSource automatically sends "Last-Event-ID: <sequence>" on its next
// reconnect (per the SSE spec) — resume works transparently for plain
// `new EventSource(url)` callers with no client-side change, in addition
// to an explicit `?after=<sequence>` for callers that build the URL
// themselves (e.g. after reading `sequence` out of the last envelope they
// processed). See parseResumeCursor.
//
// Resume behavior (CONTRACT.md "Extend the existing swarm-sdk/serve event
// hub"):
//   - No cursor supplied (fresh connection, both Last-Event-ID and
//     ?after= absent): unchanged tail-only behavior — no replay, live
//     events only, exactly as before this task.
//   - A cursor is supplied and still within EventLog's retained window:
//     every buffered event after that cursor is replayed (each with its
//     own "id:"/"event:"/"data:" lines, identical in shape to a live
//     event) before the connection switches to live tail — so a resuming
//     EventSource-based client's addEventListener(kind, ...) handlers
//     fire for replayed events exactly as they would have for the
//     original live ones.
//   - A cursor predates EventLog's retained window (events in between
//     were evicted): a single typed "event: gap" (EventKindGap) envelope
//     is emitted — see stream.go's GapPayload — reporting the requested/
//     oldest/latest sequence numbers, and the connection then ends. The
//     client must decide how to recover (e.g. reload a snapshot via RPC
//     then reconnect with no cursor) rather than the server silently
//     resuming from the oldest retained event or silently dropping the
//     request.
//
// The connection is held open until the client disconnects (which closes
// r.Context().Done()).  No client → server messages are accepted; this is a
// pure one-way subscription.  Use WebSocketHandler when bidirectional flow
// is needed.
func (m *Mux) SSEHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := m.applyAuth(w, r)
		if !ok {
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			if !setResponseWriteDeadline(w) {
				return
			}
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		// Resolve the resume cursor before writing any response bytes so a
		// malformed request never leaves a half-written stream open.
		after, hasAfter := parseResumeCursor(r)

		if !setResponseWriteDeadline(w) {
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
		w.WriteHeader(http.StatusOK)

		// Initial comment line forces the headers out for proxies that wait
		// for first body bytes before flushing.
		if _, err := fmt.Fprint(w, ": stream-open\n\n"); err != nil {
			return
		}
		flusher.Flush()
		clearResponseWriteDeadline(w)

		elog := eventLogFor(m)
		var cursor *uint64
		if hasAfter {
			cursor = &after
		}
		replay, ch, cancel, oldest, latest, hasGap := elog.SubscribeFrom(cursor)
		defer cancel()

		if hasGap {
			if !setResponseWriteDeadline(w) {
				return
			}
			gapEnv := EventEnvelope{
				SchemaVersion: eventSchemaVersion,
				Kind:          EventKindGap,
				StreamID:      elog.StreamID(),
				Payload: GapPayload{
					Requested: after,
					Oldest:    oldest,
					Latest:    latest,
				},
			}
			body := marshalEnvelope(gapEnv)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", EventKindGap, body)
			flusher.Flush()
			clearResponseWriteDeadline(w)
			// The cursor is unrecoverable: the client must make an explicit
			// recovery decision (e.g. drop ?after= for tail-only, or reload a
			// snapshot first) rather than the server guessing one for it. End
			// the connection instead of silently falling back to a replay
			// window the client never asked for.
			return
		}

		for _, ev := range replay {
			if !setResponseWriteDeadline(w) {
				return
			}
			if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Kind, ev.Data); err != nil {
				return
			}
			flusher.Flush()
			clearResponseWriteDeadline(w)
		}

		done := ctx.Done()
		for {
			select {
			case <-done:
				return
			case entry, ok := <-ch:
				if !ok {
					return
				}
				// Refresh the write deadline immediately before this
				// write/flush (CONTRACT.md: "SSE refreshes the deadline
				// for each write/flush and clears it while idle"). A slow
				// or stalled client gets a fresh bounded window per event
				// rather than either an unbounded write or a deadline
				// installed once at connection-open time that would
				// eventually fire on a perfectly healthy, merely
				// low-traffic stream.
				if !setResponseWriteDeadline(w) {
					return
				}
				if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", entry.Seq, entry.Kind, entry.Data); err != nil {
					return
				}
				flusher.Flush()
				// Idle again until the next event (or shutdown): clear the
				// deadline so waiting on ch/done below is never mistaken
				// for a stalled write and does not need its own repeating
				// keepalive write just to dodge the deadline.
				clearResponseWriteDeadline(w)
			}
		}
	})
}
