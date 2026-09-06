package attachcontract

// KindSequenceGap is the Event v1 `kind` discriminator for GapPayload — the
// typed, explicit event reported when a requested resume cursor is older
// than the oldest cursor retained by the producer's history. ADR-004
// "Event contract": "a typed gap event for a cursor older than retained
// history." This is never a silent skip: a consumer that would otherwise
// miss events between the requested cursor and the oldest retained cursor
// instead receives one of these, still able to advance its own cursor
// afterward.
const KindSequenceGap = "sequence_gap"

// GapPayload is the concrete Payload DTO carried by an Envelope whose Kind
// is KindSequenceGap. It reports, for one StreamID, the cursor a consumer
// asked to resume from (RequestedCursor) and the oldest cursor the producer
// can still serve (OldestAvailableCursor) — the gap is every sequence
// number in [RequestedCursor, OldestAvailableCursor) that the consumer will
// never receive from this producer.
type GapPayload struct {
	// StreamID is the stream this gap applies to. It is carried on the
	// payload (in addition to the enclosing Envelope's StreamID) so a gap
	// event remains self-describing if ever inspected outside its
	// envelope (e.g. logged, or re-emitted across a bridge).
	StreamID string `json:"stream_id"`
	// RequestedCursor is the sequence cursor the consumer asked to resume
	// from (e.g. from a Last-Event-ID/?after= resume request).
	RequestedCursor int64 `json:"requested_cursor"`
	// OldestAvailableCursor is the oldest sequence cursor the producer can
	// still serve for StreamID. RequestedCursor < OldestAvailableCursor is
	// exactly the condition that produces this payload.
	OldestAvailableCursor int64 `json:"oldest_available_cursor"`
}

// EventKind implements Payload.
func (GapPayload) EventKind() string { return KindSequenceGap }

// NewGapPayload constructs a GapPayload for streamID reporting that
// requestedCursor was older than oldestAvailableCursor.
func NewGapPayload(streamID string, requestedCursor, oldestAvailableCursor int64) GapPayload {
	return GapPayload{
		StreamID:              streamID,
		RequestedCursor:       requestedCursor,
		OldestAvailableCursor: oldestAvailableCursor,
	}
}
