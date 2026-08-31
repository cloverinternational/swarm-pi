package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/headless/ipc"
)

func TestWallDeadlineFrom_Zero(t *testing.T) {
	now := time.Now()

	if _, ok := wallDeadlineFrom(0, now); ok {
		t.Fatal("wallDeadlineFrom(0, now) returned ok=true, want false")
	}
	if _, ok := wallDeadlineFrom(-1, now); ok {
		t.Fatal("wallDeadlineFrom(-1, now) returned ok=true, want false")
	}
}

func TestWallDeadlineFrom_Positive(t *testing.T) {
	start := time.Unix(1000, 0)
	dl, ok := wallDeadlineFrom(60, start)
	if !ok {
		t.Fatal("wallDeadlineFrom(60, start) returned ok=false, want true")
	}
	want := start.Add(60 * time.Second)
	if !dl.Equal(want) {
		t.Fatalf("wallDeadlineFrom(60, start) = %v, want %v", dl, want)
	}
}

func TestPrintResultEventWallDeadlineOmitempty(t *testing.T) {
	unset, err := json.Marshal(ipc.PrintResultEvent{Type: "result"})
	if err != nil {
		t.Fatalf("marshal result event without wall deadline: %v", err)
	}
	if strings.Contains(string(unset), "wall_deadline_seconds") {
		t.Fatalf("unset wall deadline was not omitted: %s", unset)
	}

	set, err := json.Marshal(ipc.PrintResultEvent{
		Type:                "result",
		WallDeadlineSeconds: 120,
	})
	if err != nil {
		t.Fatalf("marshal result event with wall deadline: %v", err)
	}
	if !strings.Contains(string(set), `"wall_deadline_seconds":120`) {
		t.Fatalf("set wall deadline missing from JSON: %s", set)
	}
}
