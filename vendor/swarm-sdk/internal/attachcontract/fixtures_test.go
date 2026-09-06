package attachcontract

import (
	"encoding/json"
	"testing"
	"time"
)

// goldenEventV1Fixture is the canonical Event v1 JSON fixture: the wire
// shape every producer and consumer of this contract must agree on. It
// stands in, at this phase's scope, for the "golden schema fingerprint
// checked by every generated or separately implemented client" requirement
// in ADR-004's Verification list — a future generated/cross-language client
// can be checked against this exact byte shape.
const goldenEventV1Fixture = `{` +
	`"schema_version":"1.0",` +
	`"event_id":"evt-golden-1",` +
	`"stream_id":"stream-golden-1",` +
	`"sequence":100,` +
	`"kind":"sequence_gap",` +
	`"at":"2026-08-08T12:00:00Z",` +
	`"source":{"kind":"peer","peer_handle":"handle-golden","conversation_id":"conv-golden"},` +
	`"payload":{"stream_id":"stream-golden-1","requested_cursor":50,"oldest_available_cursor":90}` +
	`}`

// TestGoldenEventV1FixtureDecodes proves the canonical fixture decodes to
// the exact semantic values it represents.
func TestGoldenEventV1FixtureDecodes(t *testing.T) {
	var env Envelope
	if err := json.Unmarshal([]byte(goldenEventV1Fixture), &env); err != nil {
		t.Fatalf("decoding golden fixture: %v", err)
	}
	if env.SchemaVersion != "1.0" {
		t.Errorf("SchemaVersion = %q, want %q", env.SchemaVersion, "1.0")
	}
	if env.EventID != "evt-golden-1" || env.StreamID != "stream-golden-1" || env.Sequence != 100 {
		t.Errorf("identity mismatch: %+v", env)
	}
	if env.Kind != KindSequenceGap {
		t.Errorf("Kind = %q, want %q", env.Kind, KindSequenceGap)
	}
	wantAt, _ := time.Parse(time.RFC3339, "2026-08-08T12:00:00Z")
	if !env.At.Equal(wantAt) {
		t.Errorf("At = %v, want %v", env.At, wantAt)
	}
	if env.Source.PeerHandle != "handle-golden" || env.Source.ConversationID != "conv-golden" {
		t.Errorf("Source mismatch: %+v", env.Source)
	}
	gp, ok := env.Payload.(GapPayload)
	if !ok {
		t.Fatalf("Payload type = %T, want GapPayload", env.Payload)
	}
	want := GapPayload{StreamID: "stream-golden-1", RequestedCursor: 50, OldestAvailableCursor: 90}
	if gp != want {
		t.Errorf("Payload = %+v, want %+v", gp, want)
	}
}

// TestProducerFixtureConsumerFixtureRoundTrip encodes an Envelope with the
// package's producer helper (NewEnvelope) and decodes it with the package's
// decoder (DecodeEnvelope), asserting semantic equality end to end — the
// producer-fixture/consumer-fixture round trip ADR-004's Verification list
// requires "for every supported version."
func TestProducerFixtureConsumerFixtureRoundTrip(t *testing.T) {
	at := time.Date(2026, 8, 8, 12, 30, 0, 0, time.UTC)
	src := Source{Kind: "subagent", AgentID: "agent-rt", ConversationID: "conv-rt", WorkspaceID: "ws-rt"}
	payload := NewGapPayload("stream-rt", 5, 42)
	produced := NewEnvelope("stream-rt", "evt-rt-1", 7, at, src, payload)

	encoded, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("producer encode: %v", err)
	}

	consumed, err := DecodeEnvelope(encoded, []int{1})
	if err != nil {
		t.Fatalf("consumer decode: %v", err)
	}

	if consumed.SchemaVersion != produced.SchemaVersion ||
		consumed.EventID != produced.EventID ||
		consumed.StreamID != produced.StreamID ||
		consumed.Sequence != produced.Sequence ||
		consumed.Kind != produced.Kind {
		t.Errorf("consumed envelope identity mismatch:\n produced=%+v\n consumed=%+v", produced, consumed)
	}
	if !consumed.At.Equal(produced.At) {
		t.Errorf("consumed.At = %v, want %v", consumed.At, produced.At)
	}
	if consumed.Source != produced.Source {
		t.Errorf("consumed.Source = %+v, want %+v", consumed.Source, produced.Source)
	}
	consumedPayload, ok := consumed.Payload.(GapPayload)
	if !ok {
		t.Fatalf("consumed.Payload type = %T, want GapPayload", consumed.Payload)
	}
	if consumedPayload != payload {
		t.Errorf("consumed.Payload = %+v, want %+v", consumedPayload, payload)
	}
}

// FuzzDecodeEnvelope proves malformed and oversized input to DecodeEnvelope
// is bounded and never panics — ADR-004 Verification item 7: "fuzz tests
// proving malformed and oversized input is bounded and does not panic."
func FuzzDecodeEnvelope(f *testing.F) {
	seeds := []string{
		goldenEventV1Fixture,
		`{}`,
		`null`,
		`[]`,
		`{"schema_version":"1.0"}`,
		`{"schema_version":"1.10","event_id":"e","stream_id":"s","sequence":0,"kind":"k","at":"2026-08-08T12:00:00Z"}`,
		`{"schema_version":"9.9","event_id":"e","stream_id":"s","sequence":1,"kind":"sequence_gap","at":"2026-08-08T12:00:00Z","payload":{"not":"a gap"}}`,
		`not even json`,
		`{"schema_version":`,
		`{"schema_version":"1.0","event_id":"e","stream_id":"s","sequence":1,"kind":"sequence_gap","at":"2026-08-08T12:00:00Z","payload":{"stream_id":1,"requested_cursor":"x"}}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("DecodeEnvelope panicked on input %q: %v", data, r)
			}
		}()
		// Both entry points must be panic-free and bounded: the strict
		// major-gated decoder, and Envelope's own json.Unmarshaler used
		// directly by any caller that doesn't need major gating.
		_, _ = DecodeEnvelope(data, []int{1})

		var env Envelope
		_ = json.Unmarshal(data, &env)
	})
}
