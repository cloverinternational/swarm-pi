package agent

import (
	"context"
	"testing"
)

func TestSteeringModeIsValid(t *testing.T) {
	cases := []struct {
		in    SteeringMode
		valid bool
	}{
		{"", true},
		{SteeringModePoll, true},
		{SteeringModeStream, true},
		{"bogus", false},
		{"POLL", false}, // case-sensitive on purpose
	}
	for _, tc := range cases {
		if got := tc.in.IsValid(); got != tc.valid {
			t.Errorf("SteeringMode(%q).IsValid() = %v, want %v", tc.in, got, tc.valid)
		}
	}
}

func TestResolveSteeringModeDefault(t *testing.T) {
	if got := ResolveSteeringMode(""); got != SteeringModePoll {
		t.Errorf("ResolveSteeringMode(\"\") = %q, want %q", got, SteeringModePoll)
	}
	if got := ResolveSteeringMode(SteeringModeStream); got != SteeringModeStream {
		t.Errorf("ResolveSteeringMode(stream) = %q, want stream", got)
	}
}

func TestStreamingDriverInertWhenPoll(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{Mode: SteeringModePoll})
	if d.IsStreaming() {
		t.Fatal("poll-mode driver should not report streaming")
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start() on poll-mode driver: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() on poll-mode driver: %v", err)
	}
}

func TestStreamingDriverEmptyModeDefaultsToPoll(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{})
	if d.Mode() != SteeringModePoll {
		t.Errorf("empty-mode driver should default to poll, got %q", d.Mode())
	}
	if d.IsStreaming() {
		t.Error("empty-mode driver should not be streaming")
	}
}

func TestStreamingDriverStartStopStream(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{Mode: SteeringModeStream})
	if !d.IsStreaming() {
		t.Fatal("stream-mode driver should report streaming")
	}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	// Idempotent start.
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start() second call: %v", err)
	}
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop(): %v", err)
	}
	// Idempotent stop.
	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() second call: %v", err)
	}
}

func TestStreamingDriverEventBufferSizeDefault(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{Mode: SteeringModeStream})
	if d.cfg.EventBufferSize != 64 {
		t.Errorf("default EventBufferSize = %d, want 64", d.cfg.EventBufferSize)
	}
}
