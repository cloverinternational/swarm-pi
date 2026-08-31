package hosted

import "testing"

type mockUpdate struct {
	Value string `json:"value"`
}

func (m mockUpdate) UpdateType() string {
	return "tool_result"
}

func TestNewUpdateEnvelopeUsesExistingUpdateType(t *testing.T) {
	envelope := NewUpdateEnvelope(42, mockUpdate{Value: "ok"})

	if envelope.ProtocolVersion != CurrentProtocolVersion {
		t.Fatalf("ProtocolVersion = %s, want %s", envelope.ProtocolVersion, CurrentProtocolVersion)
	}
	if envelope.Sequence != 42 {
		t.Fatalf("Sequence = %d, want 42", envelope.Sequence)
	}
	if envelope.Event.Type != EventType("tool_result") {
		t.Fatalf("Event.Type = %s, want tool_result", envelope.Event.Type)
	}
	if envelope.Cursor == nil {
		t.Fatal("expected replay cursor")
	}
	if envelope.Cursor.Sequence != 42 {
		t.Fatalf("Cursor.Sequence = %d, want 42", envelope.Cursor.Sequence)
	}
}

func TestEventEnvelopeDecodePayload(t *testing.T) {
	envelope := NewEnvelope(EventTypeQuestionRequest, map[string]string{"answer": "yes"})

	var payload map[string]string
	if err := envelope.DecodePayload(&payload); err != nil {
		t.Fatalf("DecodePayload() error = %v", err)
	}

	if payload["answer"] != "yes" {
		t.Fatalf("payload[answer] = %q, want yes", payload["answer"])
	}
}

func TestExecutionMetadataFromMap(t *testing.T) {
	metadata := ExecutionMetadataFromMap(map[string]any{
		"session_id":    "sess-1",
		"projectID":     "proj-1",
		"trace_id":      "trace-1",
		"parentTraceID": "trace-0",
		"policy_context": map[string]any{
			"approval_mode": "strict",
		},
		"labels": map[string]string{
			"tenant": "acme",
		},
	})

	if metadata == nil {
		t.Fatal("ExecutionMetadataFromMap() returned nil")
	}
	if metadata.SessionID != "sess-1" {
		t.Fatalf("SessionID = %q, want sess-1", metadata.SessionID)
	}
	if metadata.ProjectID != "proj-1" {
		t.Fatalf("ProjectID = %q, want proj-1", metadata.ProjectID)
	}
	if metadata.TraceID != "trace-1" {
		t.Fatalf("TraceID = %q, want trace-1", metadata.TraceID)
	}
	if metadata.ParentTraceID != "trace-0" {
		t.Fatalf("ParentTraceID = %q, want trace-0", metadata.ParentTraceID)
	}
	if metadata.PolicyContext["approval_mode"] != "strict" {
		t.Fatalf("PolicyContext[approval_mode] = %q, want strict", metadata.PolicyContext["approval_mode"])
	}
	if metadata.Labels["tenant"] != "acme" {
		t.Fatalf("Labels[tenant] = %q, want acme", metadata.Labels["tenant"])
	}
}
