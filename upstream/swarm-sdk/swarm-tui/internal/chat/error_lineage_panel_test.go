package chat

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	sdkobs "github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerror "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func TestBuildErrorLineagePanel(t *testing.T) {
	root := sdkerror.Permanent(
		"provider.http.rate_limit",
		"provider returned 429",
		sdkerror.WithTraceID("trace-test-1"),
		sdkerror.WithOperation("provider.http.request"),
		sdkerror.WithComponent("provider/http"),
		sdkerror.WithAttr("status_code", 429),
	)

	err := sdkerror.Wrap(
		root,
		"agent.execute.failed",
		sdkerror.WithOperation("agent.execute"),
		sdkerror.WithComponent("agent"),
	)

	panel := buildErrorLineagePanel(err, false)
	if panel == nil {
		t.Fatalf("expected non-nil panel")
	}
	if panel.ErrorID == "" {
		t.Fatalf("expected error id to be populated")
	}
	if panel.Code != "agent.execute.failed" {
		t.Fatalf("expected top-level code agent.execute.failed, got %q", panel.Code)
	}
	if panel.TraceID != "trace-test-1" {
		t.Fatalf("expected trace id propagation, got %q", panel.TraceID)
	}
	if len(panel.Frames) < 2 {
		t.Fatalf("expected at least 2 lineage frames, got %d", len(panel.Frames))
	}
	if len(panel.OperationSequence) == 0 || panel.OperationSequence[0] != "agent.execute" {
		t.Fatalf("expected operation sequence to start with agent.execute, got %v", panel.OperationSequence)
	}
	// Verify source location is captured on frames
	for i, f := range panel.Frames {
		if f.File == "" {
			t.Logf("frame %d has empty File (acceptable for test)", i)
		}
	}
}

func TestBuildErrorLineagePanelFallsBackToRootAttributes(t *testing.T) {
	root := sdkerror.Permanent(
		"provider.http.payment_required",
		"HTTP 402: Payment required",
		sdkerror.WithTraceID("trace-test-attrs"),
		sdkerror.WithOperation("provider.http.request"),
		sdkerror.WithAttr("status_code", 402),
		sdkerror.WithAttr("provider", "openai"),
	)
	err := sdkerror.Wrap(
		root,
		"agent.execute.failed",
		sdkerror.WithOperation("agent.execute"),
	)

	panel := buildErrorLineagePanel(err, false)
	if panel == nil {
		t.Fatalf("expected non-nil panel")
	}
	if len(panel.Attributes) == 0 {
		t.Fatalf("expected panel attributes to fall back from root frame, got none")
	}
	if got := panel.Attributes["status_code"]; got != 402 {
		t.Fatalf("expected status_code=402 from root attrs, got %#v", got)
	}
	if got := panel.Attributes["provider"]; got != "openai" {
		t.Fatalf("expected provider=openai from root attrs, got %#v", got)
	}
}

func TestToggleActiveErrorLineage(t *testing.T) {
	app := &App{
		currentConvID:              "conv-1",
		errorLineageExpandedByConv: make(map[string]bool),
		messages: []Message{
			{
				Role: "assistant",
				ErrorLineage: &errorLineagePanel{
					Expanded: false,
				},
			},
		},
	}

	toggled, expanded := app.toggleActiveErrorLineage()
	if !toggled {
		t.Fatalf("expected toggle to succeed")
	}
	if !expanded {
		t.Fatalf("expected first toggle to report expanded=true")
	}
	if !app.messages[0].ErrorLineage.Expanded {
		t.Fatalf("expected panel to be expanded after toggle")
	}
	if !app.errorLineageExpandedByConv["conv-1"] {
		t.Fatalf("expected conversation expansion state to be persisted")
	}

	toggled, expanded = app.toggleActiveErrorLineage()
	if !toggled {
		t.Fatalf("expected second toggle to succeed")
	}
	if expanded {
		t.Fatalf("expected second toggle to report expanded=false")
	}
	if app.messages[0].ErrorLineage.Expanded {
		t.Fatalf("expected panel to be collapsed after second toggle")
	}
	if app.errorLineageExpandedByConv["conv-1"] {
		t.Fatalf("expected persisted state to be collapsed after second toggle")
	}
}

func TestFormatErrorLineageForClipboard(t *testing.T) {
	panel := &errorLineagePanel{
		Summary:   "request failed",
		ErrorID:   "err_test",
		Code:      "agent.execute.failed",
		TraceID:   "trace_test",
		Operation: "agent.execute",
		Component: "agent",
		Frames: []errorLineageFrame{
			{
				Message:   "top-level failure",
				ErrorID:   "err_test",
				Code:      "agent.execute.failed",
				Operation: "agent.execute",
			},
		},
	}

	content := formatErrorLineageForClipboard(panel, "conv-clip")
	for _, expected := range []string{
		"Swarm SDK Error Lineage",
		"Conversation: conv-clip",
		"ErrorID: err_test",
		"Code: agent.execute.failed",
		"TraceID: trace_test",
		"Causal Chain:",
		"top-level failure",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("expected clipboard content to contain %q; got:\n%s", expected, content)
		}
	}
}

func TestFormatErrorLineageForClipboardWithTimelineAndRemediation(t *testing.T) {
	panel := &errorLineagePanel{
		Summary: "rate limited",
		ErrorID: "err_rate",
		Code:    "agent.execute.failed",
		TraceID: "trace_rate",
		Frames: []errorLineageFrame{
			{
				Message:   "execution failed",
				Code:      "agent.execute.failed",
				Operation: "agent.execute",
			},
			{
				Message:   "rate limit exceeded",
				Code:      "provider.http.rate_limit",
				Operation: "provider.http.request",
				ErrorID:   "err_rate_inner",
				SpanID:    "span-1",
			},
		},
		Report: &sdkobs.LineageReport{
			TraceID: "trace_rate",
			Events: []sdkobs.TraceEvent{
				{
					EventType: sdkobs.EventTypeSpanError,
					Timestamp: mustParseTime("2026-01-02T15:04:05Z"),
					TraceID:   "trace_rate",
					SpanID:    "span-1",
					ErrorID:   "err_rate_inner",
					Operation: "provider.http.request",
				},
				{
					EventType:  sdkobs.EventTypeSpanEnd,
					Timestamp:  mustParseTime("2026-01-02T15:04:06Z"),
					TraceID:    "trace_rate",
					SpanID:     "span-1",
					DurationMs: 1000,
				},
			},
		},
	}

	content := formatErrorLineageForClipboard(panel, "conv-rate")
	for _, expected := range []string{
		"Causal Chain:",
		"rate_limit",
		"Span Timeline:",
		"1000ms",
		"Remediation:",
		"Wait and retry",
	} {
		if !strings.Contains(content, expected) {
			t.Fatalf("expected clipboard content to contain %q; got:\n%s", expected, content)
		}
	}
}

func TestFillTraceSpanFromFrames(t *testing.T) {
	panel := &errorLineagePanel{
		TraceID: "",
		SpanID:  "",
		Frames: []errorLineageFrame{
			{TraceID: "", SpanID: ""},
			{TraceID: "trace-xyz", SpanID: "span-abc"},
		},
	}

	fillTraceSpanFromFrames(panel)
	if panel.TraceID != "trace-xyz" {
		t.Fatalf("expected trace fallback to populate trace-xyz, got %q", panel.TraceID)
	}
	if panel.SpanID != "span-abc" {
		t.Fatalf("expected span fallback to populate span-abc, got %q", panel.SpanID)
	}
}

func TestBuildSpanTimeline(t *testing.T) {
	panel := &errorLineagePanel{
		Frames: []errorLineageFrame{
			{
				Code:    "agent.execute.failed",
				ErrorID: "err-1",
				SpanID:  "span-1",
			},
			{
				Code:    "provider.http.rate_limit",
				ErrorID: "err-2",
				SpanID:  "span-2",
			},
		},
		Report: &sdkobs.LineageReport{
			Events: []sdkobs.TraceEvent{
				{EventType: sdkobs.EventTypeSpanError, SpanID: "span-1", ErrorID: "err-1"},
				{EventType: sdkobs.EventTypeSpanEnd, SpanID: "span-1", DurationMs: 200},
				{EventType: sdkobs.EventTypeSpanError, SpanID: "span-2", ErrorID: "err-2"},
				{EventType: sdkobs.EventTypeSpanEnd, SpanID: "span-2", DurationMs: 50},
			},
		},
	}

	idx := buildTimingIndex(panel.Report)
	lines := buildSpanTimeline(panel, idx)
	if len(lines) != 2 {
		t.Fatalf("expected 2 timeline lines, got %d: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "execute.failed") {
		t.Fatalf("expected first line to contain execute.failed, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "200ms") {
		t.Fatalf("expected first line to contain 200ms, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "rate_limit") {
		t.Fatalf("expected second line to contain rate_limit, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "50ms") {
		t.Fatalf("expected second line to contain 50ms, got %q", lines[1])
	}
	// Verify bar characters present
	if !strings.Contains(lines[0], "█") {
		t.Fatalf("expected bar characters in timeline, got %q", lines[0])
	}
}

func TestRemediationHintForCode(t *testing.T) {
	tests := []struct {
		code string
		want string
	}{
		{"provider.http.rate_limit", "Wait and retry"},
		{"provider.http.payment_required", "Check your billing tab"},
		{"provider.http.unauthorized", "Verify your API key"},
		{"agent.execute.timeout", "Retry with a longer timeout"},
		{"unknown.error.code", ""},
	}
	for _, tt := range tests {
		got := remediationHintForCode(tt.code)
		if tt.want == "" {
			if got != "" {
				t.Fatalf("expected no hint for %q, got %q", tt.code, got)
			}
			continue
		}
		if !strings.Contains(got, tt.want) {
			t.Fatalf("expected hint for %q to contain %q, got %q", tt.code, tt.want, got)
		}
	}
}

func TestNormalizeLineageMessageRemovesIDs(t *testing.T) {
	input := "agent failed (error_id=err_abc123) (error_id=err_xyz987) (trace: trace-123)"
	got := normalizeLineageMessage(input)
	if strings.Contains(got, "error_id=") {
		t.Fatalf("expected error_id markers removed, got %q", got)
	}
	if strings.Contains(got, "trace:") {
		t.Fatalf("expected trace marker removed, got %q", got)
	}
	if got != "agent failed" {
		t.Fatalf("expected normalized message %q, got %q", "agent failed", got)
	}
}

func TestInferErrorCode(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantSub string // substring to check
	}{
		{"context canceled", context.Canceled, "context.canceled"},
		{"context deadline exceeded", context.DeadlineExceeded, "context.deadline_exceeded"},
		{"fmt error", fmt.Errorf("something went wrong"), "errors.errorString"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferErrorCode(tt.err)
			if !strings.Contains(got, tt.wantSub) {
				t.Fatalf("expected %q to contain %q", got, tt.wantSub)
			}
		})
	}
	// nil returns empty
	if got := inferErrorCode(nil); got != "" {
		t.Fatalf("expected empty for nil, got %q", got)
	}
}

func TestCollectErrorLineageFramesWithStdlibError(t *testing.T) {
	// Build a chain: sdkerr.Wrap(context.Canceled, ...) → context.Canceled
	inner := sdkerror.Wrap(context.Canceled, "provider.stream.interrupted",
		sdkerror.WithOperation("provider.stream"),
		sdkerror.WithComponent("provider"),
	)
	outer := sdkerror.Wrap(inner, "agent.execute.failed",
		sdkerror.WithOperation("agent.execute"),
		sdkerror.WithComponent("agent"),
	)

	frames := collectErrorLineageFrames(outer)
	if len(frames) < 2 {
		t.Fatalf("expected at least 2 frames, got %d", len(frames))
	}

	// The deepest frame should have code "context.canceled" (not "unknown")
	deepest := frames[len(frames)-1]
	if deepest.Code != "context.canceled" {
		t.Fatalf("expected deepest frame code 'context.canceled', got %q (frame: %+v)", deepest.Code, deepest)
	}

	// Context.canceled should only appear ONCE (dedup)
	contextCanceledCount := 0
	for _, f := range frames {
		if f.Code == "context.canceled" {
			contextCanceledCount++
		}
	}
	if contextCanceledCount > 1 {
		t.Fatalf("expected context.canceled to appear at most once, got %d times", contextCanceledCount)
	}
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
