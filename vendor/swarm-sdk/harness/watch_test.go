package harness

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// validManifest returns a valid, self-contained harness manifest whose digest
// varies with the prompt marker, so distinct markers produce distinct digests.
func validManifest(prompt string) string {
	return `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: watch-test
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "` + prompt + `"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
}

// invalidManifest parses cleanly but fails semantic validation (empty provider
// id + model), so CompileBytes returns structured Diagnostics.
const invalidManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: watch-test
provider:
  id: ""
  model: ""
agent:
  systemPrompt:
    inline: "X"
  tools: []
permissions:
  approvalMode: interactive
`

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func digestOf(t *testing.T, path, manifest string) string {
	t.Helper()
	p, err := CompileBytes([]byte(manifest), path)
	if err != nil {
		t.Fatalf("CompileBytes(%q): %v", manifest, err)
	}
	return p.Digest()
}

func recvEvent(t *testing.T, ch <-chan WatchEvent) WatchEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("event channel closed unexpectedly")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for a watch event")
		return WatchEvent{}
	}
}

func assertNoEvent(t *testing.T, ch <-chan WatchEvent) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("unexpected extra watch event: %+v", ev)
		}
	case <-time.After(150 * time.Millisecond):
	}
}

// TestWatchDebounceStableWrite: a rapid burst of writes (A,B,C,D) BEFORE any
// poll coalesces into exactly ONE event, emitted once D has been stable for
// StablePolls consecutive polls, with NewDigest == digest(D).
func TestWatchDebounceStableWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")

	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := Watch(ctx, path, WatchOptions{StablePolls: 2, tick: tick})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Rapid burst BEFORE any tick: only the settled content (D) is ever observed.
	mustWrite(t, path, validManifest("A"))
	mustWrite(t, path, validManifest("B"))
	mustWrite(t, path, validManifest("C"))
	mustWrite(t, path, validManifest("D"))

	tick <- time.Now() // poll 1: candidate=D, count 1, no emit
	tick <- time.Now() // poll 2: candidate=D, count 2, EMIT (send returns after receive)

	ev := recvEvent(t, events)
	if ev.Err != nil {
		t.Fatalf("unexpected error event: %v (diags=%v)", ev.Err, ev.Diagnostics)
	}
	if want := digestOf(t, path, validManifest("D")); ev.NewDigest != want {
		t.Fatalf("NewDigest = %q, want digest(D) %q", ev.NewDigest, want)
	}
	if ev.Plan == nil || ev.Plan.RevealSystemPrompt() != "D" {
		t.Fatalf("event plan = %v, want prompt D", ev.Plan)
	}
	// The prior baseline was empty (first emit): OldDigest is empty.
	if ev.OldDigest != "" {
		t.Fatalf("OldDigest = %q, want empty on first emit", ev.OldDigest)
	}

	// No spurious re-emit for the same stable content (no more ticks driven).
	assertNoEvent(t, events)
}

// TestWatchInvalidDoesNotAdvanceBaseline: an invalid manifest emits an event
// carrying Err/Diagnostics and does NOT advance the baseline, so a following
// valid write still emits a change whose OldDigest is the last VALID baseline.
func TestWatchInvalidDoesNotAdvanceBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")

	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := Watch(ctx, path, WatchOptions{StablePolls: 2, tick: tick})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	// Phase 1: valid V1 stabilizes and emits.
	mustWrite(t, path, validManifest("V1"))
	tick <- time.Now()
	tick <- time.Now()
	e1 := recvEvent(t, events)
	if e1.Err != nil || e1.Plan == nil {
		t.Fatalf("phase1 expected a valid event, got err=%v", e1.Err)
	}
	v1Digest := digestOf(t, path, validManifest("V1"))
	if e1.NewDigest != v1Digest {
		t.Fatalf("phase1 NewDigest = %q, want %q", e1.NewDigest, v1Digest)
	}

	// Phase 2: an INVALID manifest stabilizes -> Err + structured Diagnostics.
	mustWrite(t, path, invalidManifest)
	tick <- time.Now()
	tick <- time.Now()
	e2 := recvEvent(t, events)
	if e2.Err == nil {
		t.Fatalf("phase2 expected an error event")
	}
	if e2.Plan != nil {
		t.Fatalf("phase2 expected a nil plan, got %v", e2.Plan)
	}
	if !e2.Diagnostics.HasErrors() {
		t.Fatalf("phase2 expected structured diagnostics, got %v", e2.Diagnostics)
	}

	// Phase 3: a following VALID V2 still emits, and OldDigest is the last VALID
	// baseline (V1) — proving the invalid edit did NOT advance the baseline.
	mustWrite(t, path, validManifest("V2"))
	tick <- time.Now()
	tick <- time.Now()
	e3 := recvEvent(t, events)
	if e3.Err != nil || e3.Plan == nil {
		t.Fatalf("phase3 expected a valid event, got err=%v", e3.Err)
	}
	if e3.OldDigest != v1Digest {
		t.Fatalf("phase3 OldDigest = %q, want V1 baseline %q (invalid must not advance baseline)", e3.OldDigest, v1Digest)
	}
	if want := digestOf(t, path, validManifest("V2")); e3.NewDigest != want {
		t.Fatalf("phase3 NewDigest = %q, want %q", e3.NewDigest, want)
	}
}

// TestWatchCtxCancelClosesChannel: cancelling ctx stops polling and CLOSES the
// channel (no leaked goroutine). Uses a real fast ticker and runs under -race.
func TestWatchCtxCancelClosesChannel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")
	mustWrite(t, path, validManifest("A"))

	ctx, cancel := context.WithCancel(context.Background())
	events, err := Watch(ctx, path, WatchOptions{PollInterval: 5 * time.Millisecond, StablePolls: 2})
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	time.Sleep(25 * time.Millisecond) // let it genuinely poll a few times
	cancel()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return // channel closed => goroutine returned, no leak
			}
			// drain any in-flight event and keep waiting for close
		case <-deadline:
			t.Fatalf("watch channel not closed within 2s after ctx cancel")
		}
	}
}
