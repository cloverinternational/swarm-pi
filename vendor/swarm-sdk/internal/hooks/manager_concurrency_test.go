package hooks

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// TestGetApplicableHooks_ConcurrentCacheMisses_NoDataRace is the regression
// test for the pre-existing bug tracked in PLAN.md: getApplicableHooks used
// to take only m.mu.RLock() while writing to m.sortedCache/m.cacheValid on a
// cache miss — a concurrent map write under a read lock. It fires whenever
// two goroutines call EmitWithResult on the SAME *Manager at the same time,
// a realistic shape whenever a manager is shared across goroutines (an IPC
// server, or several sub-agents sharing one manager via delegate
// inheritance).
//
// This test drives many goroutines through EmitWithResult concurrently with
// DISTINCT ConversationIDs, so every one of them is a guaranteed cache miss
// (see buildCacheKey — conversation id is part of the key) and therefore a
// guaranteed concurrent write attempt into the same maps. Run with -race;
// before the fix this reliably reports "fatal error: concurrent map writes"
// or a WARNING: DATA RACE. It does not assert on the returned hook list
// content — the whole point is that the run completes cleanly under -race.
func TestGetApplicableHooks_ConcurrentCacheMisses_NoDataRace(t *testing.T) {
	mgr := NewManager(ManagerConfig{})
	hook := &recordingHook{name: "concurrency-probe", priority: 10, verdict: Continue()}
	if err := mgr.Register(hook, ScopeGlobal, ""); err != nil {
		t.Fatalf("Register: %v", err)
	}

	const goroutines = 64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			event := toolEvent("bash", map[string]any{"command": "echo hello"})
			event.ConversationID = fmt.Sprintf("conv-%d", i) // forces a distinct, guaranteed-miss cache key per goroutine
			if _, err := mgr.EmitWithResult(context.Background(), event); err != nil {
				t.Errorf("EmitWithResult: %v", err)
			}
		}(i)
	}
	wg.Wait()

	if got := hook.calls.Load(); got != goroutines {
		t.Fatalf("hook.calls = %d, want %d — some goroutines' events never reached the hook", got, goroutines)
	}
}
