package observability

import (
	"context"
	stderrors "errors"
	"path/filepath"
	"testing"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func TestLocalTracerRecordsErrorLineageAndLookup(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace-events.jsonl")
	tracer, store, sink, err := NewLocalJSONLTracer(tracePath)
	if err != nil {
		t.Fatalf("NewLocalJSONLTracer() error = %v", err)
	}
	defer sink.Close()

	ctx := context.Background()
	ctx, rootSpan := tracer.StartSpan(ctx, "agent.execute")
	ctx, providerSpan := tracer.StartSpan(ctx, "provider.openai.chat")

	rootErr := sdkerr.Wrap(stderrors.New("HTTP 429"), "provider.http.rate_limited")
	wrappedErr := sdkerr.Wrap(
		rootErr,
		"provider.openai.request_failed",
		sdkerr.WithOperation("provider.openai.chat"),
		sdkerr.WithComponent("provider.openai"),
	)

	providerSpan.RecordError(wrappedErr)
	providerSpan.End()
	rootSpan.End()

	errorID := sdkerr.GetErrorID(wrappedErr)
	if errorID == "" {
		t.Fatal("expected non-empty ErrorID")
	}
	if sdkerr.GetTraceID(wrappedErr) == "" {
		t.Fatal("expected non-empty TraceID after RecordError")
	}

	report, lookupErr := LookupByErrorID(store, errorID)
	if lookupErr != nil {
		t.Fatalf("LookupByErrorID() error = %v", lookupErr)
	}
	if report.TraceID != rootSpan.TraceID() {
		t.Fatalf("expected report trace %q, got %q", rootSpan.TraceID(), report.TraceID)
	}
	if len(report.OperationSequence) == 0 {
		t.Fatal("expected non-empty operation sequence")
	}
	if report.CausalityTree == nil {
		t.Fatal("expected non-nil causality tree")
	}
}

func TestLocalTracerInjectExtractPropagation(t *testing.T) {
	store := NewInMemoryLookupStore()
	tracer := NewLocalTracer(LocalTracerConfig{LookupStore: store, Redactor: NewDefaultRedactor()})

	ctx := context.Background()
	ctx, rootSpan := tracer.StartSpan(ctx, "agent.execute")
	defer rootSpan.End()

	carrier := map[string]string{}
	if err := tracer.InjectContext(ctx, carrier); err != nil {
		t.Fatalf("InjectContext() error = %v", err)
	}
	if carrier["x-trace-id"] == "" {
		t.Fatal("expected x-trace-id in carrier")
	}
	if carrier["x-span-id"] == "" {
		t.Fatal("expected x-span-id in carrier")
	}

	extractedCtx, err := tracer.ExtractContext(carrier)
	if err != nil {
		t.Fatalf("ExtractContext() error = %v", err)
	}

	_, childSpan := tracer.StartSpan(extractedCtx, "provider.http.request")
	defer childSpan.End()

	if childSpan.TraceID() != rootSpan.TraceID() {
		t.Fatalf("expected propagated trace %q, got %q", rootSpan.TraceID(), childSpan.TraceID())
	}
}
