package a2a

// registry_ops_test.go — race-clean proof that the field-scoped registry
// operations defined in registry_ops.go (UpdateHeartbeat, UpdateStatus,
// UpdateEndpoints, UpdateCapabilities, Shutdown) never lose an update or
// produce a torn write when called concurrently against the SAME handle,
// and that Shutdown racing UpdateHeartbeat resolves deterministically.
//
// Run with: go test -race -run TestRegistryOps -count=1 ./internal/a2a

import (
	"fmt"
	"os"
	"sync"
	"testing"
)

// TestRegistryOpsConcurrentFieldScopedUpdatesNoLostUpdateNoTornWrite spawns
// registryOpsGoroutinesPerOp goroutines for each of four DIFFERENT ops
// (UpdateStatus, UpdateHeartbeat, UpdateEndpoints, UpdateCapabilities) —
// registryOpsGoroutinesPerOp*4 writer goroutines total, plus one continuous
// reader goroutine — all racing against the SAME peer handle, each writer
// performing registryOpsIterationsPerGoroutine calls. It proves:
//
//  1. No lost update: every field an op owns (Status, EndpointURL, Model)
//     ends up, after every goroutine finishes, holding a value that SOME
//     call to that field's owning op actually wrote — never a stale value
//     from before the race (that would mean an update was silently lost)
//     and never the zero value (that would mean a DIFFERENT op's write
//     clobbered a field it doesn't own).
//  2. No torn write: the concurrent reader's GetPeer calls, running for the
//     entire duration of the writer race, never observe a read/unmarshal
//     error — the on-disk record is always one complete, cleanly-decodable
//     JSON document, never a partially written blob.
//  3. Unrelated fields (PID, seeded at JoinSwarm time and never touched by
//     any op exercised here) are never clobbered by an op that doesn't own
//     them — reinforcing (1) for a field no op in this test ever writes.
func TestRegistryOpsConcurrentFieldScopedUpdatesNoLostUpdateNoTornWrite(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())

	const (
		swarm                             = "registry-ops-stress"
		handle                            = "registry-ops-stress-peer"
		registryOpsGoroutinesPerOp        = 10
		registryOpsIterationsPerGoroutine = 25
	)
	seedPID := os.Getpid()
	if err := JoinSwarm(swarm, PeerPresence{Handle: handle, PID: seedPID, Status: "seed-status"}); err != nil {
		t.Fatalf("seed JoinSwarm: %v", err)
	}

	var mu sync.Mutex
	statusValues := make(map[string]bool, registryOpsGoroutinesPerOp*registryOpsIterationsPerGoroutine)
	endpointValues := make(map[string]bool, registryOpsGoroutinesPerOp*registryOpsIterationsPerGoroutine)
	modelValues := make(map[string]bool, registryOpsGoroutinesPerOp*registryOpsIterationsPerGoroutine)
	record := func(set map[string]bool, v string) {
		mu.Lock()
		set[v] = true
		mu.Unlock()
	}

	// Continuous reader: proves no torn write is ever observable while the
	// writer race is in flight, not merely after it settles.
	stopReader := make(chan struct{})
	var readerWG sync.WaitGroup
	readerWG.Add(1)
	go func() {
		defer readerWG.Done()
		for {
			select {
			case <-stopReader:
				return
			default:
			}
			if _, err := GetPeer(swarm, handle); err != nil {
				t.Errorf("concurrent reader observed a torn/corrupt write: %v", err)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	errs := make(chan error, registryOpsGoroutinesPerOp*4*registryOpsIterationsPerGoroutine)

	for g := 0; g < registryOpsGoroutinesPerOp; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < registryOpsIterationsPerGoroutine; i++ {
				v := fmt.Sprintf("status-%d-%d", g, i)
				record(statusValues, v)
				if err := UpdateStatus(swarm, handle, v, "task"); err != nil {
					errs <- fmt.Errorf("UpdateStatus: %w", err)
				}
			}
		}(g)
	}
	for g := 0; g < registryOpsGoroutinesPerOp; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < registryOpsIterationsPerGoroutine; i++ {
				if err := UpdateHeartbeat(swarm, handle); err != nil {
					errs <- fmt.Errorf("UpdateHeartbeat: %w", err)
				}
			}
		}(g)
	}
	for g := 0; g < registryOpsGoroutinesPerOp; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < registryOpsIterationsPerGoroutine; i++ {
				v := fmt.Sprintf("https://endpoint.invalid/%d-%d", g, i)
				record(endpointValues, v)
				if err := UpdateEndpoints(swarm, handle, v, "", "", ""); err != nil {
					errs <- fmt.Errorf("UpdateEndpoints: %w", err)
				}
			}
		}(g)
	}
	for g := 0; g < registryOpsGoroutinesPerOp; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < registryOpsIterationsPerGoroutine; i++ {
				v := fmt.Sprintf("model-%d-%d", g, i)
				record(modelValues, v)
				if err := UpdateCapabilities(swarm, handle, "", v, "", ""); err != nil {
					errs <- fmt.Errorf("UpdateCapabilities: %w", err)
				}
			}
		}(g)
	}

	wg.Wait()
	close(stopReader)
	readerWG.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("registry op error during concurrent stress: %v", err)
	}

	got, err := GetPeer(swarm, handle)
	if err != nil || got == nil {
		t.Fatalf("final GetPeer: peer=%+v err=%v", got, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !statusValues[got.Status] {
		t.Fatalf("lost update: final Status %q is not any UpdateStatus call's value", got.Status)
	}
	if !endpointValues[got.EndpointURL] {
		t.Fatalf("lost update: final EndpointURL %q is not any UpdateEndpoints call's value", got.EndpointURL)
	}
	if !modelValues[got.Model] {
		t.Fatalf("lost update: final Model %q is not any UpdateCapabilities call's value", got.Model)
	}
	if got.PID != seedPID {
		t.Fatalf("unrelated field PID clobbered by an unrelated op: got %d, want seeded %d", got.PID, seedPID)
	}

	t.Logf(
		"concurrent stress: %d goroutines (%d per op x 4 ops) + 1 reader, %d iterations/goroutine, %d total op calls",
		registryOpsGoroutinesPerOp*4,
		registryOpsGoroutinesPerOp,
		registryOpsIterationsPerGoroutine,
		registryOpsGoroutinesPerOp*4*registryOpsIterationsPerGoroutine,
	)
}

// TestRegistryOpsShutdownRacingHeartbeatResolvesDeterministically races
// registryOpsShutdownHeartbeatGoroutines concurrent UpdateHeartbeat calls
// against a single Shutdown call on the SAME handle and proves the
// resolution is deterministic:
//
// Resolution rule: SHUTDOWN WINS AND IS IRREVERSIBLE. UpdateHeartbeat never
// fabricates a presence record — tx.Get() returning nil makes it fail
// closed with "peer not found" (see registry_ops.go) — so it can never
// resurrect a peer Shutdown has already removed. Every racing heartbeat call
// therefore has exactly two legal outcomes: it is serialized (by
// WithPeerHandleTransaction's per-handle lock, shared with LeaveSwarm's
// withRegistryHandle call) strictly BEFORE the removal and succeeds as a
// harmless liveness refresh on a record about to be deleted, or it is
// serialized strictly AFTER the removal and fails closed with
// "peer not found". No third outcome (corrupt state, a resurrected file, or
// any other error) is legal, and the final state after every goroutine
// completes is always fully removed.
func TestRegistryOpsShutdownRacingHeartbeatResolvesDeterministically(t *testing.T) {
	t.Setenv("SWARM_HOME", t.TempDir())

	const (
		swarm                                  = "registry-ops-shutdown-race"
		handle                                 = "registry-ops-shutdown-race-peer"
		registryOpsShutdownHeartbeatGoroutines = 20
	)
	if err := JoinSwarm(swarm, PeerPresence{Handle: handle, PID: os.Getpid(), Status: "alive"}); err != nil {
		t.Fatalf("seed JoinSwarm: %v", err)
	}

	start := make(chan struct{})
	heartbeatErrs := make([]error, registryOpsShutdownHeartbeatGoroutines)
	var wg sync.WaitGroup

	for i := 0; i < registryOpsShutdownHeartbeatGoroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			heartbeatErrs[i] = UpdateHeartbeat(swarm, handle)
		}(i)
	}

	var shutdownErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		shutdownErr = Shutdown(swarm, handle, "")
	}()

	close(start)
	wg.Wait()

	if shutdownErr != nil {
		t.Fatalf("Shutdown failed: %v", shutdownErr)
	}

	for i, err := range heartbeatErrs {
		if err != nil && !containsError(err, "peer not found") {
			t.Fatalf("heartbeat[%d]: want nil or \"peer not found\", got: %v", i, err)
		}
	}

	got, err := GetPeer(swarm, handle)
	if err != nil {
		t.Fatalf("GetPeer after race: %v", err)
	}
	if got != nil {
		t.Fatalf("shutdown-wins invariant violated: peer resurrected after Shutdown: %+v", got)
	}
}
