package toolmetrics

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRecordAggregates(t *testing.T) {
	Reset()
	Record("alpha", 5*time.Millisecond, 100, false)
	Record("alpha", 15*time.Millisecond, 200, true)
	Record("beta", 1*time.Millisecond, 10, false)

	snaps := SnapshotAll()
	if len(snaps) != 2 {
		t.Fatalf("want 2 tools, got %d", len(snaps))
	}
	// Ranked by total time desc: alpha (20ms) before beta (1ms).
	if snaps[0].Tool != "alpha" {
		t.Fatalf("want alpha ranked first, got %q", snaps[0].Tool)
	}
	a := snaps[0]
	if a.Calls != 2 || a.Errors != 1 {
		t.Fatalf("calls/errors = %d/%d, want 2/1", a.Calls, a.Errors)
	}
	if a.OutputBytes != 300 {
		t.Fatalf("output bytes = %d, want 300", a.OutputBytes)
	}
	if a.MaxMS < 14.9 || a.MaxMS > 15.1 {
		t.Fatalf("max = %v, want ~15ms", a.MaxMS)
	}
	if a.MeanMS < 9.9 || a.MeanMS > 10.1 {
		t.Fatalf("mean = %v, want ~10ms", a.MeanMS)
	}
	if a.Buckets["<10ms"] != 1 || a.Buckets["<100ms"] != 1 {
		t.Fatalf("buckets = %v", a.Buckets)
	}
}

// A tool reporting failure via IsError with a nil error must still count as an
// error, otherwise failure rates silently undercount.
func TestErrorCountingBothChannels(t *testing.T) {
	Reset()
	Record("x", time.Millisecond, 0, true)
	Record("x", time.Millisecond, 0, false)
	s := SnapshotAll()[0]
	if s.Errors != 1 {
		t.Fatalf("errors = %d, want 1", s.Errors)
	}
	_ = errors.New("unused")
}

func TestConcurrentRecordIsRaceFree(t *testing.T) {
	Reset()
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Record("hot", time.Microsecond, 1, false)
			}
		}()
	}
	wg.Wait()
	s := SnapshotAll()[0]
	if s.Calls != 6400 {
		t.Fatalf("calls = %d, want 6400", s.Calls)
	}
	if s.OutputBytes != 6400 {
		t.Fatalf("bytes = %d, want 6400", s.OutputBytes)
	}
}

// Begin must be inert when deep profiling is off, and must not panic.
func TestBeginInertWhenDisabled(t *testing.T) {
	if DeepProfileEnabled() {
		t.Skip("deep profile on")
	}
	ctx := context.Background()
	got, end := Begin(ctx, "t")
	if got != ctx {
		t.Fatal("Begin must return ctx unchanged when disabled")
	}
	end()
	if n := ReadAlloc(); n != 0 {
		t.Fatalf("ReadAlloc must be 0 when disabled, got %d", n)
	}
}

func BenchmarkRecord(b *testing.B) {
	Reset()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Record("bench", time.Microsecond, 128, false)
	}
}

func BenchmarkRecordParallel(b *testing.B) {
	Reset()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			Record("bench", time.Microsecond, 128, false)
		}
	})
}

func BenchmarkBeginDisabled(b *testing.B) {
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, end := Begin(ctx, "bench")
		end()
	}
}
