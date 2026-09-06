package sdkerr

import (
	"context"
	stderrors "errors"
	"runtime"
	"strings"
	"testing"
)

func TestWrapPreservesLineage(t *testing.T) {
	root := Wrap(
		stderrors.New("HTTP 429"),
		"provider.http.rate_limited",
		WithTraceID("trace-1"),
		WithOperation("provider.http.check_error_response"),
		WithComponent("provider.http"),
	)

	wrapped := Wrap(
		root,
		"provider.openai.request_failed",
		WithOperation("openai.chat"),
		WithComponent("provider.openai"),
	)

	if wrapped == nil {
		t.Fatal("expected wrapped error")
	}
	if wrapped.ErrorID == "" {
		t.Fatal("expected wrapped error ID")
	}
	if wrapped.ParentErrorID != root.ErrorID {
		t.Fatalf("expected parent error id %q, got %q", root.ErrorID, wrapped.ParentErrorID)
	}
	if wrapped.TraceID != root.TraceID {
		t.Fatalf("expected trace id %q, got %q", root.TraceID, wrapped.TraceID)
	}
	if wrapped.Code != "provider.openai.request_failed" {
		t.Fatalf("expected code provider.openai.request_failed, got %q", wrapped.Code)
	}
	if !stderrors.Is(wrapped, root) {
		t.Fatal("expected wrapped error to match root via errors.Is")
	}
}

func TestWithTraceFromContext(t *testing.T) {
	ctx := ContextWithTrace(context.Background(), "trace-abc", "span-123")
	err := Permanent(
		"agent.invalid_state",
		"agent is busy",
		WithTraceFromContext(ctx),
	)

	if err.TraceID != "trace-abc" {
		t.Fatalf("expected trace-abc, got %q", err.TraceID)
	}
	if err.SpanID != "span-123" {
		t.Fatalf("expected span-123, got %q", err.SpanID)
	}
}

func TestErrorFormattingIncludesErrorID(t *testing.T) {
	err := Permanent("agent.failed", "execution failed")
	msg := err.Error()

	if !strings.Contains(msg, "error_id=") {
		t.Fatalf("expected error_id in message, got %q", msg)
	}
	if strings.Contains(msg, "wrapped:") {
		t.Fatalf("expected concise format, got %q", msg)
	}
}

func TestCaptureCreatesCanonicalError(t *testing.T) {
	err := Capture(stderrors.New("boom"), WithOperation("unit.test"), WithComponent("tests"))
	if err == nil {
		t.Fatal("expected captured error")
	}
	if err.ErrorID == "" {
		t.Fatal("expected error id")
	}
	if err.Code == "" {
		t.Fatal("expected code")
	}
	if err.Operation != "unit.test" {
		t.Fatalf("expected operation unit.test, got %q", err.Operation)
	}
	if err.Component != "tests" {
		t.Fatalf("expected component tests, got %q", err.Component)
	}
}

func TestSourceLocationCapturedAtConstruction(t *testing.T) {
	// The line below is the call site we want to capture.
	err := Permanent("test.source_location", "source location test") // line 94
	if err.File == "" {
		t.Fatal("expected File to be populated")
	}
	if err.Line == 0 {
		t.Fatal("expected Line to be non-zero")
	}
	// The captured line should be the line where Permanent was called,
	// which should be near line 94 in this file.
	_, thisFile, _, _ := runtime.Caller(0)
	if err.File != thisFile {
		t.Fatalf("expected File %q, got %q", thisFile, err.File)
	}
	if err.Line < 90 || err.Line > 100 {
		t.Fatalf("expected Line near 94, got %d", err.Line)
	}
}

func TestGetFileGetLineHelpers(t *testing.T) {
	err := Wrap(
		stderrors.New("inner"),
		"test.helpers",
		WithOperation("test.op"),
	)

	file := GetFile(err)
	if file == "" {
		t.Fatal("expected GetFile to return non-empty string")
	}
	line := GetLine(err)
	if line == 0 {
		t.Fatal("expected GetLine to return non-zero")
	}

	// Non-SDK errors should return zero values.
	plainErr := stderrors.New("plain")
	if GetFile(plainErr) != "" {
		t.Fatal("expected empty File for non-SDK error")
	}
	if GetLine(plainErr) != 0 {
		t.Fatal("expected zero Line for non-SDK error")
	}
}

func TestClonePreservesSourceLocation(t *testing.T) {
	original := Permanent("test.clone", "clone test")
	if original.File == "" {
		t.Skip("source location not captured, skipping clone test")
	}
	cloned := original.Clone()
	if cloned.File != original.File {
		t.Fatalf("expected cloned File %q, got %q", original.File, cloned.File)
	}
	if cloned.Line != original.Line {
		t.Fatalf("expected cloned Line %d, got %d", original.Line, cloned.Line)
	}
}

func TestGetCategoryInfersTransientForContextErrors(t *testing.T) {
	if got := GetCategory(context.Canceled); got != CategoryTransient {
		t.Fatalf("expected context.Canceled to be Transient, got %v", got)
	}
	if got := GetCategory(context.DeadlineExceeded); got != CategoryTransient {
		t.Fatalf("expected context.DeadlineExceeded to be Transient, got %v", got)
	}
	// Wrapping context.Canceled should inherit transient category
	wrapped := Wrap(context.Canceled, "provider.stream.interrupted",
		WithOperation("provider.stream"),
	)
	if wrapped.Category != CategoryTransient {
		t.Fatalf("expected wrapped context.Canceled to be Transient, got %v", wrapped.Category)
	}
}
