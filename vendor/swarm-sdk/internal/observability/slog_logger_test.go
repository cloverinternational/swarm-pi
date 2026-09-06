package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

func TestNewSlogLogger_Basics(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug - 8, // allow trace
	})
	l := observability.NewSlogLogger(slog.New(handler))

	ctx := context.Background()
	l.Info(ctx, "agent.started", observability.F("id", "test-agent"), observability.F("model", "claude-3"))

	line := buf.String()
	if !strings.Contains(line, "agent.started") {
		t.Errorf("expected event name in log output, got: %s", line)
	}

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v\nraw: %s", err, line)
	}
	if entry["msg"] != "agent.started" {
		t.Errorf("msg = %q, want %q", entry["msg"], "agent.started")
	}
	if entry["id"] != "test-agent" {
		t.Errorf("id = %q, want %q", entry["id"], "test-agent")
	}
	if entry["model"] != "claude-3" {
		t.Errorf("model = %q, want %q", entry["model"], "claude-3")
	}
}

func TestNewSlogLogger_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	// Only log Warn and above
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	l := observability.NewSlogLogger(slog.New(handler))

	ctx := context.Background()
	l.Debug(ctx, "filtered.out")
	l.Info(ctx, "also.filtered")
	l.Warn(ctx, "passes.through")
	l.Error(ctx, "also.passes")

	output := buf.String()
	if strings.Contains(output, "filtered.out") {
		t.Error("Debug message should have been filtered by slog handler")
	}
	if strings.Contains(output, "also.filtered") {
		t.Error("Info message should have been filtered by slog handler")
	}
	if !strings.Contains(output, "passes.through") {
		t.Error("Warn message should have passed through")
	}
	if !strings.Contains(output, "also.passes") {
		t.Error("Error message should have passed through")
	}
}

func TestNewSlogLogger_NilFallsToDefault(t *testing.T) {
	// A nil *slog.Logger must fall back to a usable default, not panic.
	l := observability.NewSlogLogger(nil)
	if l == nil {
		t.Fatal("NewSlogLogger(nil) returned nil; expected a default-backed logger")
	}
	// And it must be usable without panicking.
	l.Info(context.Background(), "no.panic")
}

func TestNewSlogLogger_AllLevels(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.Level(-8), // allow all including trace
	})
	l := observability.NewSlogLogger(slog.New(handler))
	ctx := context.Background()

	l.Trace(ctx, "trace.event")
	l.Debug(ctx, "debug.event")
	l.Info(ctx, "info.event")
	l.Warn(ctx, "warn.event")
	l.Error(ctx, "error.event")

	output := buf.String()
	for _, event := range []string{"trace.event", "debug.event", "info.event", "warn.event", "error.event"} {
		if !strings.Contains(output, event) {
			t.Errorf("expected %q in output, got: %s", event, output)
		}
	}
}

// Compile-time guarantee that *SlogLogger implements observability.Logger.
var _ observability.Logger = observability.NewSlogLogger(slog.Default())

func TestNewSlogLogger_SatisfiesInterface(t *testing.T) {
	var l any = observability.NewSlogLogger(slog.Default())
	if _, ok := l.(observability.Logger); !ok {
		t.Fatal("NewSlogLogger does not satisfy observability.Logger")
	}
}
