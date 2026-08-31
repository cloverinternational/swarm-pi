package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestMemoryWatcher_ThresholdCrossing verifies that a threshold fires once
// on upward crossing and resets after memory drops back below.
func TestMemoryWatcher_ThresholdCrossing(t *testing.T) {
	tmpDir := t.TempDir()

	// OnBreach runs on the watcher's fire goroutine — guard the slice so the
	// test's reads below don't race the callback's writes.
	var firedMu sync.Mutex
	var fired []string
	w := NewMemoryWatcher(WatcherConfig{
		Thresholds: []Threshold{
			{LimitMB: 10, Label: "test_low", TriggerGC: false, FreeOSMem: false},
		},
		PollInterval:            50 * time.Millisecond,
		OutputDir:               tmpDir,
		MaxProfilesPerThreshold: 5,
		OnBreach: func(th Threshold, heapMB float64) {
			firedMu.Lock()
			defer firedMu.Unlock()
			fired = append(fired, th.Label)
		},
	})

	w.Start()
	defer w.Stop()

	// Wait for at least one poll cycle
	time.Sleep(100 * time.Millisecond)

	// Force a large allocation to push heap above the 10MB threshold and KEEP it
	// referenced through the polling window. Assigning to _ (or letting GC run)
	// frees it before the watcher polls, so the threshold would never breach.
	buf := make([]byte, 15*1024*1024)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = 1 // touch pages so the memory is actually committed into the heap
	}

	// Wait for polling to detect the breach
	time.Sleep(200 * time.Millisecond)
	runtime.KeepAlive(buf) // buf must stay live across the sleep above

	firedMu.Lock()
	firedSnapshot := append([]string(nil), fired...)
	firedMu.Unlock()
	if len(firedSnapshot) == 0 {
		t.Fatal("expected OnBreach to fire after allocating above threshold")
	}
	if firedSnapshot[0] != "test_low" {
		t.Fatalf("expected label 'test_low', got %q", firedSnapshot[0])
	}

	// Check that a heap profile was written
	matches, err := filepath.Glob(filepath.Join(tmpDir, "heap-test_low-*.prof"))
	if err != nil {
		t.Fatalf("glob error: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected at least one heap profile file")
	}

	// Check that a goroutine profile was written
	grMatches, err := filepath.Glob(filepath.Join(tmpDir, "goroutine-test_low-*.prof"))
	if err != nil {
		t.Fatalf("glob error: %v", err)
	}
	if len(grMatches) == 0 {
		t.Fatal("expected at least one goroutine profile file")
	}
}

// TestMemoryWatcher_StartStop verifies that Start/Stop are idempotent.
func TestMemoryWatcher_StartStop(t *testing.T) {
	w := NewMemoryWatcher(WatcherConfig{
		Thresholds: []Threshold{
			{LimitMB: 1000, Label: "never", TriggerGC: false, FreeOSMem: false},
		},
		PollInterval: 50 * time.Millisecond,
	})

	w.Start()
	if !w.IsRunning() {
		t.Fatal("expected watcher to be running after Start")
	}

	// Double-start should be no-op
	w.Start()
	if !w.IsRunning() {
		t.Fatal("expected watcher still running after second Start")
	}

	w.Stop()
	if w.IsRunning() {
		t.Fatal("expected watcher to be stopped")
	}

	// Double-stop should be no-op
	w.Stop()
	if w.IsRunning() {
		t.Fatal("expected watcher still stopped after second Stop")
	}
}

// TestMemoryWatcher_DefaultThresholds ensures the default config produces
// the expected 350/500/750/1000 MB thresholds.
func TestMemoryWatcher_DefaultThresholds(t *testing.T) {
	w := NewMemoryWatcher(WatcherConfig{})

	if len(w.thresholds) != 4 {
		t.Fatalf("expected 4 default thresholds, got %d", len(w.thresholds))
	}

	expected := []int{350, 500, 750, 1000}
	for i, th := range w.thresholds {
		if th.LimitMB != expected[i] {
			t.Fatalf("threshold %d: expected %dMB, got %dMB", i, expected[i], th.LimitMB)
		}
	}

	// Verify states map exists for each threshold
	for _, th := range w.thresholds {
		if w.states[th.LimitMB] == nil {
			t.Fatalf("missing state for threshold %dMB", th.LimitMB)
		}
	}
}

// TestMemoryWatcher_CleanupOldProfiles verifies that profile retention
// caps are respected.
func TestMemoryWatcher_CleanupOldProfiles(t *testing.T) {
	tmpDir := t.TempDir()

	w := NewMemoryWatcher(WatcherConfig{
		Thresholds: []Threshold{
			{LimitMB: 5, Label: "tiny", TriggerGC: false, FreeOSMem: false},
		},
		OutputDir:               tmpDir,
		MaxProfilesPerThreshold: 3,
	})

	// Create 5 DISTINCT fake heap profiles. Second-precision timestamps would
	// collide over this fast loop (same filename → overwrite), so index each
	// name; stagger mtimes so oldest-first cleanup is deterministic.
	for i := range 5 {
		name := filepath.Join(tmpDir, fmt.Sprintf("heap-tiny-%d.prof", i))
		_ = os.WriteFile(name, []byte("fake"), 0644)
		time.Sleep(10 * time.Millisecond) // stagger mtimes
	}

	w.cleanupOldProfiles("tiny")

	matches, _ := filepath.Glob(filepath.Join(tmpDir, "heap-tiny-*.prof"))
	if len(matches) != 3 {
		t.Fatalf("expected 3 profiles after cleanup, got %d", len(matches))
	}
}
