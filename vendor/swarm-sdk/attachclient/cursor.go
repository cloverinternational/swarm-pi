package attachclient

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

// Cursor identifies a client-acknowledged position in a daemon's event
// stream. It is client-owned per package-boundaries.md's attachclient
// section ("Owned state: ... event cursor") — the daemon's own EventLog
// sequence numbers remain authoritative; this Cursor only records what THIS
// client has acknowledged having consumed.
type Cursor struct {
	// StreamID identifies the logical event stream (ADR-004 Event v1's
	// stream_id). Empty means "the daemon's default/only stream".
	StreamID string
	// Sequence is the last acknowledged sequence number on that stream.
	// Zero means "nothing acknowledged yet" (resume from the start / most
	// recent daemon-chosen replay point).
	Sequence int64
}

// Zero reports whether this cursor has not acknowledged any event yet.
func (c Cursor) Zero() bool { return c.StreamID == "" && c.Sequence == 0 }

// GapError is attachclient's interim typed gap signal, reported by the
// reconnect state machine (reconnect.go) whenever a resume attempt lands on
// a cursor older than the daemon's retained history — the hard invariant
// "Reconnect resumes from an acknowledged cursor or reports a gap" forbids
// silently skipping the missing events.
//
// This is attachclient's OWN type, not swarm-sdk/internal/attachcontract's
// canonical typed gap payload: that neutral-contract package (owned by
// P04.A this round, see CONTRACT.md) did not exist/compile in this
// worktree when this file was written, and this package must not edit or
// depend on an incomplete attachcontract. GapError is deliberately shaped
// to carry the same information ADR-004's Event contract specifies for a
// gap ("a typed gap event for a cursor older than retained history") so a
// future swap to attachcontract's type is a mechanical rename. The wire
// signal it decodes (RawEvent.Kind == "gap", with snake_case
// stream_id/requested_after/oldest_retained JSON fields in the payload) is
// documented in decodeEvent below and is forward-compatible with whatever
// serve/stream.go (owned by P04.C/D) eventually emits for a real gap.
type GapError struct {
	StreamID       string
	RequestedAfter int64
	OldestRetained int64
}

func (e *GapError) Error() string {
	return fmt.Sprintf(
		"attachclient: event gap on stream %q: requested resume after sequence %d but oldest retained is %d",
		e.StreamID, e.RequestedAfter, e.OldestRetained)
}

// CursorStore is an injected, OPTIONAL persistence boundary for the
// client's acknowledged cursor. Per package-boundaries.md's
// Persistence/protocol ownership subsection: "Client cursor persistence, if
// enabled, is namespaced and non-authoritative." Implementations MUST:
//
//   - namespace stored cursors by the caller-supplied namespace string (this
//     package always passes "attachclient/<session-id>", so cursors from
//     different daemon sessions never collide even if a store is shared);
//   - treat stored values as advisory bookmarks only, never as authoritative
//     over the daemon's actual retained history — a stale/incorrect stored
//     cursor must surface as an explicit GapError on resume, never a silent
//     skip or a fabricated "caught up" state.
//
// New(Deps{}) uses an in-memory, process-local default (newMemCursorStore)
// that satisfies this contract without touching disk.
type CursorStore interface {
	Load(ctx context.Context, namespace string) (Cursor, bool, error)
	Save(ctx context.Context, namespace string, cursor Cursor) error
}

// memCursorStore is the default, non-persistent CursorStore: a process-local
// map keyed by namespace. It is never shared across process restarts and is
// explicitly non-authoritative (package-boundaries.md), matching the
// production default described on CursorStore.
type memCursorStore struct {
	mu    sync.Mutex
	byKey map[string]Cursor
}

func newMemCursorStore() *memCursorStore {
	return &memCursorStore{byKey: make(map[string]Cursor)}
}

func (m *memCursorStore) Load(_ context.Context, namespace string) (Cursor, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.byKey[namespace]
	return c, ok, nil
}

func (m *memCursorStore) Save(_ context.Context, namespace string, c Cursor) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byKey[namespace] = c
	return nil
}

// eventEnvelope mirrors today's daemon SSE payload shape (see
// cmd/swarmos/attach_cli.go's former sseEnvelope and
// swarm-tui/internal/chat/attach_sse.go's former daemonSSEEnvelope, both of
// which this package's decodeEvent now replaces with one implementation).
type eventEnvelope struct {
	Kind  string `json:"kind"`
	Agent struct {
		Type   string          `json:"type"`
		Update json.RawMessage `json:"update"`
	} `json:"agent"`
	Payload json.RawMessage `json:"payload"`
}

// gapPayload is the documented interim wire shape for an explicit gap
// signal: RawEvent.Kind == "gap" with a JSON payload carrying these
// snake_case fields (matching ADR-004 Event v1's field-naming convention
// even though the envelope itself is attachclient's interim type, not
// attachcontract's).
type gapPayload struct {
	StreamID       string `json:"stream_id"`
	RequestedAfter int64  `json:"requested_after"`
	OldestRetained int64  `json:"oldest_retained"`
}

// decodeEvent turns one Transport-level RawEvent into either an
// application-visible Event or a *GapError, never both. A malformed JSON
// payload decodes to a zero-value envelope (Kind "") rather than an error:
// per ADR-004 Compatibility, "an unknown event kind under a compatible
// major can be ignored or preserved while still advancing the sequence
// cursor" — decodeEvent never panics on malformed/unknown input and always
// still returns raw.Cursor so the reconnect loop can advance past it.
func decodeEvent(raw RawEvent) (Event, *GapError) {
	if raw.Kind == "gap" {
		var g gapPayload
		_ = json.Unmarshal(raw.Payload, &g)
		if g.StreamID == "" {
			g.StreamID = raw.Cursor.StreamID
		}
		return Event{}, &GapError{
			StreamID:       g.StreamID,
			RequestedAfter: g.RequestedAfter,
			OldestRetained: g.OldestRetained,
		}
	}

	var env eventEnvelope
	_ = json.Unmarshal(raw.Payload, &env) // malformed payload -> zero-value envelope, not an error

	kind := env.Kind
	if kind == "" {
		kind = raw.Kind
	}

	return Event{
		Cursor:  raw.Cursor,
		Kind:    kind,
		Agent:   AgentUpdate{Type: env.Agent.Type, Update: env.Agent.Update},
		Payload: env.Payload,
	}, nil
}

// DecodeEnvelope parses one already-extracted JSON event payload using the
// same "kind" / "agent.type" / "agent.update" / "payload" shape as
// decodeEvent above. It is exported for callers that receive a single SSE
// "data:" payload string outside attachclient's own Events()/reconnect loop
// (for example swarm-tui/internal/chat/attach_sse.go's applyDaemonEvent,
// whose `func(data string) bool` signature is fixed by an existing,
// out-of-scope-this-phase test file) and would otherwise have to hand-copy
// the envelope struct a second time. Malformed JSON decodes to a zero-value
// Event (Kind == "") rather than an error, matching decodeEvent.
func DecodeEnvelope(payload []byte) Event {
	ev, _ := decodeEvent(RawEvent{Payload: payload})
	return ev
}
