package observability

import "testing"

func TestLookupBuildsCausalityTree(t *testing.T) {
	store := NewInMemoryLookupStore()

	_ = store.StoreEvent(TraceEvent{
		EventType:     EventTypeSpanError,
		TraceID:       "trace-1",
		SpanID:        "span-1",
		Operation:     "agent.execute",
		Component:     "agent",
		ErrorID:       "err-root",
		ParentErrorID: "",
		Message:       "root failure",
		Attributes: map[string]any{
			"error_code": "agent.failure",
		},
	})
	_ = store.StoreEvent(TraceEvent{
		EventType:     EventTypeSpanError,
		TraceID:       "trace-1",
		SpanID:        "span-2",
		Operation:     "provider.openai.chat",
		Component:     "provider.openai",
		ErrorID:       "err-child",
		ParentErrorID: "err-root",
		Message:       "child failure",
		Attributes: map[string]any{
			"error_code": "provider.openai.request_failed",
		},
	})

	report, err := store.LookupError("err-child")
	if err != nil {
		t.Fatalf("LookupError() error = %v", err)
	}
	if report.TraceID != "trace-1" {
		t.Fatalf("expected trace-1, got %q", report.TraceID)
	}
	if len(report.OperationSequence) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(report.OperationSequence))
	}
	if report.CausalityTree == nil {
		t.Fatal("expected non-nil causality tree")
	}
	if report.CausalityTree.ErrorID != "err-root" {
		t.Fatalf("expected root error err-root, got %q", report.CausalityTree.ErrorID)
	}
	if len(report.CausalityTree.Children) != 1 {
		t.Fatalf("expected one child, got %d", len(report.CausalityTree.Children))
	}
	if report.CausalityTree.Children[0].ErrorID != "err-child" {
		t.Fatalf("expected child err-child, got %q", report.CausalityTree.Children[0].ErrorID)
	}
}
