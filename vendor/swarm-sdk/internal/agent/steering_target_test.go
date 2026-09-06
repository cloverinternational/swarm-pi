package agent

import (
	"context"
	"testing"
	"time"
)

func TestDefaultSteeringTarget_ArmAndConsumeBlock(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	// No block armed yet.
	if _, ok := tgt.ConsumeBlockFor("Bash"); ok {
		t.Fatal("expected no block when none armed")
	}

	// Arm a named block.
	tgt.ArmBlockNext("Bash", "off-task")
	got, ok := tgt.ConsumeBlockFor("Read")
	if ok {
		t.Fatalf("named block fired for wrong tool: got=%+v", got)
	}
	got, ok = tgt.ConsumeBlockFor("Bash")
	if !ok {
		t.Fatal("expected named block to fire for matching tool")
	}
	if got.ToolName != "Bash" || got.Reason != "off-task" {
		t.Fatalf("block fields wrong: %+v", got)
	}
	// One-shot: should be cleared.
	if _, ok := tgt.ConsumeBlockFor("Bash"); ok {
		t.Fatal("block was not cleared after consumption")
	}

	// Any-tool block.
	tgt.ArmBlockNext("", "wasteful")
	if _, ok := tgt.ConsumeBlockFor("Anything"); !ok {
		t.Fatal("empty-name block should match any tool")
	}
}

func TestDefaultSteeringTarget_NotesSortByPriorityDesc(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	tgt.QueueSystemNote("low", 1)
	tgt.QueueSystemNote("high", 9)
	tgt.QueueSystemNote("mid", 5)

	notes := tgt.DrainNotes()
	if len(notes) != 3 {
		t.Fatalf("expected 3 notes, got %d", len(notes))
	}
	if notes[0].Priority != 9 || notes[1].Priority != 5 || notes[2].Priority != 1 {
		t.Fatalf("notes not sorted desc by priority: %+v", notes)
	}
	// Drained — second drain returns empty.
	if got := tgt.DrainNotes(); got != nil {
		t.Fatalf("expected nil after drain, got %+v", got)
	}
}

func TestDefaultSteeringTarget_Refocus(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	if _, ok := tgt.ConsumeRefocus(); ok {
		t.Fatal("refocus consumed when none queued")
	}
	tgt.QueueRefocus("phase-2", "wire the pump")
	tgt.QueueRefocus("phase-2-final", "after wire-up") // last-write-wins
	got, ok := tgt.ConsumeRefocus()
	if !ok {
		t.Fatal("expected refocus")
	}
	if got.Anchor != "phase-2-final" {
		t.Fatalf("expected last-write-wins, got %+v", got)
	}
	if _, ok := tgt.ConsumeRefocus(); ok {
		t.Fatal("refocus not cleared after consume")
	}
}

func TestDefaultSteeringTarget_HaltClampsTTL(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	if err := tgt.HaltPeerLoop("peer-a", "loop", 0); err != nil {
		t.Fatalf("HaltPeerLoop: %v", err)
	}
	if err := tgt.HaltPeerLoop("peer-b", "long", 9999*time.Second); err != nil {
		t.Fatalf("HaltPeerLoop: %v", err)
	}
	halts := tgt.SnapshotHalts()
	if len(halts) != 2 {
		t.Fatalf("expected 2 halts, got %d", len(halts))
	}
	if halts[0].TTLSeconds != 60 {
		t.Errorf("zero TTL not defaulted to 60, got %d", halts[0].TTLSeconds)
	}
	if halts[1].TTLSeconds != 600 {
		t.Errorf("excessive TTL not capped at 600, got %d", halts[1].TTLSeconds)
	}
}

func TestDefaultSteeringTarget_ConcernAndAsk(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	tgt.LogConcern("c1", "warn")
	tgt.LogConcern("c2", "error")
	tgt.RecordAskUser("q1", "high")

	if got := tgt.SnapshotConcerns(); len(got) != 2 {
		t.Errorf("expected 2 concerns, got %d", len(got))
	}
	// Snapshot does NOT clear.
	if got := tgt.SnapshotConcerns(); len(got) != 2 {
		t.Errorf("snapshot mutated state: got %d on second read", len(got))
	}
	if got := tgt.SnapshotAsks(); len(got) != 1 {
		t.Errorf("expected 1 ask, got %d", len(got))
	}
}

func TestSteeringTargetContext_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := SteeringTargetFromContext(ctx); ok {
		t.Fatal("empty ctx should not carry a target")
	}
	tgt := NewDefaultSteeringTarget()
	ctx2 := WithSteeringTarget(ctx, tgt)
	got, ok := SteeringTargetFromContext(ctx2)
	if !ok {
		t.Fatal("target not found in derived ctx")
	}
	if got != tgt {
		t.Fatal("target round-trip returned different instance")
	}
	// Original ctx still empty.
	if _, ok := SteeringTargetFromContext(ctx); ok {
		t.Fatal("ctx.WithValue should not mutate parent")
	}
}

func TestDefaultSteeringTarget_ConcurrentSafe(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)
	done := make(chan struct{})

	// Concurrent arm/consume from many goroutines.
	for i := range 32 {
		go func(id int) {
			tgt.ArmBlockNext("Bash", "reason")
			tgt.QueueSystemNote("note", id)
			tgt.LogConcern("c", "info")
			tgt.RecordAskUser("q", "low")
			done <- struct{}{}
		}(i)
	}
	for range 32 {
		go func() {
			_, _ = tgt.ConsumeBlockFor("Bash")
			_ = tgt.DrainNotes()
			_ = tgt.SnapshotConcerns()
			_ = tgt.SnapshotAsks()
			done <- struct{}{}
		}()
	}
	for range 64 {
		<-done
	}
	// If we got here without -race firing, the mutex is doing its job.
}

func TestDefaultSteeringTarget_DrainHalts(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	// No halts → drain returns nil.
	if got := tgt.DrainHalts(); got != nil {
		t.Fatalf("expected nil drain, got %+v", got)
	}

	// Add two halts.
	_ = tgt.HaltPeerLoop("peer-a", "loop", 0)
	_ = tgt.HaltPeerLoop("peer-b", "more", 0)

	halts := tgt.DrainHalts()
	if len(halts) != 2 {
		t.Fatalf("expected 2 halts, got %d", len(halts))
	}

	// Second drain returns nil (clear-on-read semantics).
	if got := tgt.DrainHalts(); got != nil {
		t.Fatalf("expected nil on second drain, got %+v", got)
	}

	// Snapshot is also empty now (halts were cleared).
	if got := tgt.SnapshotHalts(); got != nil {
		t.Fatalf("expected nil snapshot after drain, got %+v", got)
	}
}

func TestDefaultSteeringTarget_DrainAsks(t *testing.T) {
	tgt := NewDefaultSteeringTarget().(*defaultSteeringTarget)

	// No asks → drain returns nil.
	if got := tgt.DrainAsks(); got != nil {
		t.Fatalf("expected nil drain, got %+v", got)
	}

	tgt.RecordAskUser("q1", "high")
	tgt.RecordAskUser("q2", "low")

	asks := tgt.DrainAsks()
	if len(asks) != 2 {
		t.Fatalf("expected 2 asks, got %d", len(asks))
	}

	// Second drain returns nil.
	if got := tgt.DrainAsks(); got != nil {
		t.Fatalf("expected nil on second drain, got %+v", got)
	}
}
