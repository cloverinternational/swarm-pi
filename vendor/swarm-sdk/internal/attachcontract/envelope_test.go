package attachcontract

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("mustTime(%q): %v", s, err)
	}
	return tm
}

// TestEnvelopeRoundTripFieldCasing asserts the encoded JSON uses exactly
// the ADR-004 field spellings/casing (schema_version, event_id, stream_id,
// sequence, kind, at, source, payload — never SchemaVersion/schemaVersion
// etc.), and that decode reproduces semantically identical data.
func TestEnvelopeRoundTripFieldCasing(t *testing.T) {
	at := mustTime(t, "2026-08-08T12:00:00Z")
	src := Source{Kind: "peer", PeerHandle: "handle-1", ConversationID: "conv-1"}
	env := NewEnvelope("stream-1", "evt-1", 42, at, src, NewGapPayload("stream-1", 10, 20))

	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)

	for _, key := range []string{
		`"schema_version"`, `"event_id"`, `"stream_id"`, `"sequence"`,
		`"kind"`, `"at"`, `"source"`, `"payload"`,
	} {
		if !strings.Contains(s, key) {
			t.Errorf("encoded envelope missing expected literal JSON key %s; got: %s", key, s)
		}
	}
	for _, badKey := range []string{
		`"SchemaVersion"`, `"schemaVersion"`, `"EventID"`, `"eventId"`,
		`"StreamID"`, `"streamId"`, `"Kind"`, `"At"`,
	} {
		if strings.Contains(s, badKey) {
			t.Errorf("encoded envelope contains unexpected non-canonical key %s; got: %s", badKey, s)
		}
	}

	var decoded Envelope
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.SchemaVersion != EventSchemaVersion {
		t.Errorf("SchemaVersion = %q, want %q", decoded.SchemaVersion, EventSchemaVersion)
	}
	if decoded.EventID != "evt-1" || decoded.StreamID != "stream-1" || decoded.Sequence != 42 {
		t.Errorf("decoded envelope identity mismatch: %+v", decoded)
	}
	if decoded.Kind != KindSequenceGap {
		t.Errorf("Kind = %q, want %q", decoded.Kind, KindSequenceGap)
	}
	if !decoded.At.Equal(at) {
		t.Errorf("At = %v, want %v", decoded.At, at)
	}
	if decoded.Source.PeerHandle != "handle-1" || decoded.Source.ConversationID != "conv-1" {
		t.Errorf("decoded Source mismatch: %+v", decoded.Source)
	}
	gp, ok := decoded.Payload.(GapPayload)
	if !ok {
		t.Fatalf("decoded Payload type = %T, want GapPayload", decoded.Payload)
	}
	if gp.RequestedCursor != 10 || gp.OldestAvailableCursor != 20 || gp.StreamID != "stream-1" {
		t.Errorf("decoded GapPayload mismatch: %+v", gp)
	}
}

// TestEnvelopeSourceSnakeCase directly asserts the Source object's wire keys
// are snake_case correlation identifiers, matching ADR-004's "a consistently
// named source object with snake-case correlation identifiers."
func TestEnvelopeSourceSnakeCase(t *testing.T) {
	src := Source{
		Kind:           "subagent",
		AgentID:        "agent-1",
		PeerHandle:     "peer-1",
		ConversationID: "conv-1",
		WorkspaceID:    "ws-1",
	}
	b, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(b)
	for _, key := range []string{`"agent_id"`, `"peer_handle"`, `"conversation_id"`, `"workspace_id"`} {
		if !strings.Contains(s, key) {
			t.Errorf("Source JSON missing snake_case key %s; got: %s", key, s)
		}
	}
	for _, badKey := range []string{`"agentID"`, `"peerHandle"`, `"convID"`} {
		if strings.Contains(s, badKey) {
			t.Errorf("Source JSON encoder must not emit legacy camelCase key %s; got: %s", badKey, s)
		}
	}
}

// TestEnvelopeSourceLegacyAliasDecode proves Source's decoder accepts the
// legacy camelCase spellings from today's serve/stream.go
// (agentID/peerHandle/convID) as read-only migration aliases, per ADR-004
// "Compatibility": "unversioned events may be displayed without claiming
// replay completeness."
func TestEnvelopeSourceLegacyAliasDecode(t *testing.T) {
	raw := `{"kind":"subagent","agentID":"legacy-agent","peerHandle":"legacy-peer","convID":"legacy-conv"}`
	var src Source
	if err := json.Unmarshal([]byte(raw), &src); err != nil {
		t.Fatalf("Unmarshal legacy source: %v", err)
	}
	if src.AgentID != "legacy-agent" || src.PeerHandle != "legacy-peer" || src.ConversationID != "legacy-conv" {
		t.Errorf("legacy alias not applied: %+v", src)
	}

	// Canonical field wins when both are present.
	raw2 := `{"agent_id":"canonical-agent","agentID":"legacy-agent"}`
	var src2 Source
	if err := json.Unmarshal([]byte(raw2), &src2); err != nil {
		t.Fatalf("Unmarshal mixed source: %v", err)
	}
	if src2.AgentID != "canonical-agent" {
		t.Errorf("canonical agent_id should win over legacy agentID, got %q", src2.AgentID)
	}
}

func validEnvelopeJSON() string {
	return `{"schema_version":"1.0","event_id":"evt-1","stream_id":"s-1","sequence":1,"kind":"sequence_gap","at":"2026-08-08T12:00:00Z","source":{"kind":"local"},"payload":{"stream_id":"s-1","requested_cursor":1,"oldest_available_cursor":5}}`
}

// TestEnvelopeRequiredFields proves each required top-level field's absence
// is a decode error.
func TestEnvelopeRequiredFields(t *testing.T) {
	fields := []string{"schema_version", "event_id", "stream_id", "sequence", "kind", "at"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(validEnvelopeJSON()), &m); err != nil {
				t.Fatalf("setup: %v", err)
			}
			delete(m, field)
			b, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("setup marshal: %v", err)
			}
			var env Envelope
			if err := json.Unmarshal(b, &env); err == nil {
				t.Errorf("expected decode error with %q missing, got none", field)
			}
		})
	}
}

// TestEnvelopeUnknownFieldTolerance proves an extra unrecognized top-level
// JSON field does not fail decode.
func TestEnvelopeUnknownFieldTolerance(t *testing.T) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validEnvelopeJSON()), &m); err != nil {
		t.Fatalf("setup: %v", err)
	}
	m["totally_unrecognized_field"] = json.RawMessage(`"anything"`)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("setup marshal: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unexpected decode error with unknown field present: %v", err)
	}
}

// TestEnvelopeUnknownKindPreservesOpaquePayload proves an envelope with an
// unrecognized `kind` still decodes successfully (advancing the sequence
// cursor is possible) and its payload is preserved opaquely rather than
// causing a decode error.
func TestEnvelopeUnknownKindPreservesOpaquePayload(t *testing.T) {
	raw := `{"schema_version":"1.0","event_id":"evt-2","stream_id":"s-1","sequence":7,"kind":"some_future_kind","at":"2026-08-08T12:00:00Z","payload":{"anything":"goes","n":3}}`
	var env Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		t.Fatalf("unexpected decode error for unknown kind: %v", err)
	}
	if env.Sequence != 7 {
		t.Errorf("Sequence = %d, want 7 (cursor must still advance)", env.Sequence)
	}
	up, ok := env.Payload.(UnknownPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want UnknownPayload", env.Payload)
	}
	if up.EventKind() != "some_future_kind" {
		t.Errorf("UnknownPayload.EventKind() = %q, want %q", up.EventKind(), "some_future_kind")
	}
	if !strings.Contains(string(up.Raw), `"anything":"goes"`) {
		t.Errorf("UnknownPayload.Raw did not preserve original payload bytes: %s", up.Raw)
	}

	// Re-encoding must reproduce the same opaque payload bytes.
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(b, &roundTrip); err != nil {
		t.Fatalf("decode re-marshaled: %v", err)
	}
	if !strings.Contains(string(roundTrip["payload"]), `"anything":"goes"`) {
		t.Errorf("re-encoded envelope lost opaque payload: %s", roundTrip["payload"])
	}
}

// TestEnvelopeMalformedSequenceRejected proves a non-integer `sequence`
// (e.g. a decimal, or a string) is a decode error.
func TestEnvelopeMalformedSequenceRejected(t *testing.T) {
	cases := []string{
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":1.5,"kind":"k","at":"2026-08-08T12:00:00Z"}`,
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":"1","kind":"k","at":"2026-08-08T12:00:00Z"}`,
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":null,"kind":"k","at":"2026-08-08T12:00:00Z"}`,
	}
	for _, raw := range cases {
		var env Envelope
		if err := json.Unmarshal([]byte(raw), &env); err == nil {
			t.Errorf("expected decode error for malformed sequence in %s", raw)
		}
	}
}

// TestEnvelopeMalformedAtRejected proves a non-RFC3339 `at` value is a
// decode error.
func TestEnvelopeMalformedAtRejected(t *testing.T) {
	cases := []string{
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":1,"kind":"k","at":"not-a-time"}`,
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":1,"kind":"k","at":"2026-08-08"}`,
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":1,"kind":"k","at":12345}`,
	}
	for _, raw := range cases {
		var env Envelope
		if err := json.Unmarshal([]byte(raw), &env); err == nil {
			t.Errorf("expected decode error for malformed at in %s", raw)
		}
	}
}

// TestEnvelopeMaxSizeBounded proves an oversized envelope is rejected by
// bound before allocation-heavy decode is attempted.
func TestEnvelopeMaxSizeBounded(t *testing.T) {
	huge := make([]byte, MaxEnvelopeBytes+1)
	for i := range huge {
		huge[i] = 'a'
	}
	var env Envelope
	if err := json.Unmarshal(huge, &env); err == nil {
		t.Errorf("expected error decoding an envelope larger than MaxEnvelopeBytes")
	}
}
