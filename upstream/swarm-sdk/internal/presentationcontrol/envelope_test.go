package presentationcontrol

import (
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/attachcontract"
)

// TestSchemaVersionMarshalsAsJSONString proves that Envelope.SchemaVersion
// (attachcontract.Version) round-trips through JSON as a STRING (e.g.
// "1.0"), never a bare/decimal JSON number, per ADR-004's version model
// ("The JSON spelling is a string so 1.10 cannot be confused with a
// decimal number").
func TestSchemaVersionMarshalsAsJSONString(t *testing.T) {
	env := Envelope{
		SchemaVersion: SchemaVersion,
		Plane:         Plane,
		Op:            OpPing,
		CorrelationID: "test-correlation-id",
	}

	data, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal(env) failed: %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal(data, &raw) failed: %v", err)
	}

	schemaField, ok := raw["schema_version"]
	if !ok {
		t.Fatalf("marshaled envelope missing %q field: %s", "schema_version", data)
	}

	// A JSON string always begins and ends with a double quote once
	// leading/trailing JSON whitespace is stripped (json.Marshal produces
	// no whitespace here); a bare/decimal number would not.
	if len(schemaField) < 2 || schemaField[0] != '"' || schemaField[len(schemaField)-1] != '"' {
		t.Fatalf("schema_version %s is not encoded as a JSON string", schemaField)
	}

	var schemaStr string
	if err := json.Unmarshal(schemaField, &schemaStr); err != nil {
		t.Fatalf("schema_version %s did not decode as a JSON string: %v", schemaField, err)
	}
	if want := "1.0"; schemaStr != want {
		t.Fatalf("schema_version = %q, want %q", schemaStr, want)
	}

	// Cross-check against attachcontract.Version's own String() form to
	// prove we are reusing its exact encoding, not a parallel one.
	if want := (attachcontract.Version{Major: 1, Minor: 0}).String(); schemaStr != want {
		t.Fatalf("schema_version = %q, want attachcontract.Version.String() = %q", schemaStr, want)
	}
}

// TestNewRequestRoundTrip proves NewRequest produces an Envelope that
// marshals to valid JSON and unmarshals back into an equivalent Envelope,
// with the payload preserved.
func TestNewRequestRoundTrip(t *testing.T) {
	type pingPayload struct {
		Nonce string `json:"nonce"`
	}

	req, err := NewRequest(OpPing, pingPayload{Nonce: "abc123"})
	if err != nil {
		t.Fatalf("NewRequest failed: %v", err)
	}
	if req.Plane != Plane {
		t.Fatalf("req.Plane = %q, want %q", req.Plane, Plane)
	}
	if req.Op != OpPing {
		t.Fatalf("req.Op = %q, want %q", req.Op, OpPing)
	}
	if req.SchemaVersion != SchemaVersion {
		t.Fatalf("req.SchemaVersion = %+v, want %+v", req.SchemaVersion, SchemaVersion)
	}
	if req.CorrelationID == "" {
		t.Fatalf("req.CorrelationID is empty")
	}
	if req.Error != nil {
		t.Fatalf("req.Error = %+v, want nil", req.Error)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(req) failed: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(data, &decoded) failed: %v", err)
	}
	if decoded.Op != req.Op || decoded.Plane != req.Plane || decoded.CorrelationID != req.CorrelationID {
		t.Fatalf("round-tripped envelope mismatch: got %+v, want %+v", decoded, req)
	}

	var decodedPayload pingPayload
	if err := json.Unmarshal(decoded.Payload, &decodedPayload); err != nil {
		t.Fatalf("json.Unmarshal(decoded.Payload, &decodedPayload) failed: %v", err)
	}
	if decodedPayload.Nonce != "abc123" {
		t.Fatalf("decodedPayload.Nonce = %q, want %q", decodedPayload.Nonce, "abc123")
	}
}

// TestNewRequestNilPayload proves NewRequest tolerates a nil payload
// (no request body) without error and without emitting a "payload":null
// key thanks to omitempty plus a genuinely empty json.RawMessage.
func TestNewRequestNilPayload(t *testing.T) {
	req, err := NewRequest(OpCapabilities, nil)
	if err != nil {
		t.Fatalf("NewRequest(op, nil) failed: %v", err)
	}
	if len(req.Payload) != 0 {
		t.Fatalf("req.Payload = %q, want empty", req.Payload)
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("json.Marshal(req) failed: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if _, ok := raw["payload"]; ok {
		t.Fatalf("marshaled envelope unexpectedly contains a %q key for a nil payload: %s", "payload", data)
	}
}

// TestNewErrorResponseRoundTrip proves NewErrorResponse produces valid
// JSON that round-trips with its Error populated and Payload absent.
func TestNewErrorResponseRoundTrip(t *testing.T) {
	resp := NewErrorResponse("corr-42", "malformed_envelope", "could not parse envelope")

	if resp.Error == nil {
		t.Fatalf("resp.Error is nil, want non-nil")
	}
	if resp.Payload != nil {
		t.Fatalf("resp.Payload = %q, want nil", resp.Payload)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("json.Marshal(resp) failed: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal(data, &decoded) failed: %v", err)
	}
	if decoded.CorrelationID != "corr-42" {
		t.Fatalf("decoded.CorrelationID = %q, want %q", decoded.CorrelationID, "corr-42")
	}
	if decoded.Error == nil {
		t.Fatalf("decoded.Error is nil, want non-nil")
	}
	if decoded.Error.Category != "malformed_envelope" {
		t.Fatalf("decoded.Error.Category = %q, want %q", decoded.Error.Category, "malformed_envelope")
	}
	if decoded.Error.Message != "could not parse envelope" {
		t.Fatalf("decoded.Error.Message = %q, want %q", decoded.Error.Message, "could not parse envelope")
	}
	if decoded.Plane != Plane {
		t.Fatalf("decoded.Plane = %q, want %q", decoded.Plane, Plane)
	}
}

// TestCorrelationIDUniqueness is a real collision smoke test (not
// tautological): it generates a large number of correlation IDs via
// NewRequest and asserts every single one is unique and non-empty. A
// counter- or PID-based generator would either collide immediately under
// concurrent-looking reuse or produce trivially predictable/sequential
// values; crypto/rand-backed 128-bit IDs should not collide at this scale.
func TestCorrelationIDUniqueness(t *testing.T) {
	const n = 20000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		req, err := NewRequest(OpPing, nil)
		if err != nil {
			t.Fatalf("NewRequest failed on iteration %d: %v", i, err)
		}
		if req.CorrelationID == "" {
			t.Fatalf("empty CorrelationID on iteration %d", i)
		}
		if _, dup := seen[req.CorrelationID]; dup {
			t.Fatalf("duplicate CorrelationID %q detected on iteration %d out of %d", req.CorrelationID, i, n)
		}
		seen[req.CorrelationID] = struct{}{}
	}
	if len(seen) != n {
		t.Fatalf("expected %d unique correlation IDs, got %d", n, len(seen))
	}
}

// TestNewCorrelationIDLength proves the raw generator produces a
// consistent, sufficiently long hex-encoded identifier (32 hex chars for
// 16 random bytes), guarding against an accidental regression to a
// shorter/weaker ID space.
func TestNewCorrelationIDLength(t *testing.T) {
	id, err := newCorrelationID()
	if err != nil {
		t.Fatalf("newCorrelationID failed: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("len(id) = %d, want 32 (16 random bytes hex-encoded)", len(id))
	}
}
