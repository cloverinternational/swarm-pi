package termimage

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// failingWriter fails the Nth write so a reconcile can be interrupted mid
// frame, which is how a real terminal disconnect or full pipe presents.
type failingWriter struct {
	bytes.Buffer
	fail bool
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.fail {
		return 0, errors.New("write failed")
	}
	return w.Buffer.Write(p)
}

type modelHarness struct {
	t       *testing.T
	manager *Manager
	term    *termModel
	// pool of registered placements the driver can publish.
	pool []Placement
	// registersSinceEvict counts Registers since the last Reconcile that
	// actually ran LRU eviction (only Kitty-capability reconciles do).
	registersSinceEvict int
}

// reconcile runs Manager.Reconcile, feeds whatever bytes were produced into the
// terminal model using the envelope in force for that write, then asserts the
// central invariant: everything the manager reports as applied must be paintable
// by the terminal. A violation is the "space reserved but nothing drawn" bug.
func (h *modelHarness) reconcile(fail bool) error {
	envelope := h.manager.Envelope()
	evicts := h.manager.Capability() == Kitty
	w := &failingWriter{fail: fail}
	err := h.manager.Reconcile(w)
	if evicts {
		h.registersSinceEvict = 0
	}
	if feedErr := h.term.Feed(w.String(), envelope); feedErr != nil {
		h.t.Fatalf("terminal model rejected stream: %v", feedErr)
	}
	if err != nil {
		return err
	}
	h.assertApplied()
	return nil
}

func (h *modelHarness) assertApplied() {
	h.manager.mu.Lock()
	appliedCap := h.manager.appliedCapability
	applied := cloneFrame(h.manager.applied)
	h.manager.mu.Unlock()
	if appliedCap != Kitty {
		return
	}
	for _, p := range applied.Placements {
		if h.manager.SourceError(p.Source.Key) != "" {
			continue // deliberately degraded to a text fallback
		}
		if !h.term.hasPlacement(p) {
			h.t.Fatalf("applied placement image=%d placement=%d is not paintable: transmitted=%v placed=%v",
				p.ImageID, p.PlacementID,
				h.term.images[p.ImageID],
				h.term.placements[placementHandle(p.ImageID, p.PlacementID)])
		}
	}
}

func (h *modelHarness) assertNoDanglingPlacements() {
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	for key, p := range h.manager.placements {
		if h.manager.sources[p.placement.Source.Key] == nil {
			h.t.Fatalf("placement %q references evicted source %q", key, p.placement.Source.Key)
		}
	}
	for imageID := range h.manager.transferFiles {
		found := false
		for _, s := range h.manager.sources {
			// transfers is the authoritative record: Invalidate clears
			// `uploaded` without discarding the temp file that still exists.
			if _, ok := s.transfers[imageID]; ok || s.imageID == imageID || s.isUploaded(imageID) {
				found = true
			}
		}
		if !found {
			h.t.Fatalf("transfer file tracked for unknown image %d", imageID)
		}
	}
}

func TestManagerStateMachineAgainstTerminalModel(t *testing.T) {
	for _, envelope := range []Envelope{Raw, TmuxPassthrough} {
		for seed := int64(1); seed <= 40; seed++ {
			t.Run(fmt.Sprintf("env%d/seed%d", envelope, seed), func(t *testing.T) {
				runStateMachine(t, seed, envelope, 400)
			})
		}
	}
}

func runStateMachine(t *testing.T, seed int64, envelope Envelope, steps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	manager := NewManager()
	manager.SetCapability(Kitty)
	manager.mu.Lock()
	manager.envelope = envelope
	manager.mu.Unlock()
	h := &modelHarness{t: t, manager: manager, term: newTermModel()}
	defer func() {
		owned := trackedTempFiles(manager)
		if err := manager.Release(&bytes.Buffer{}); err != nil {
			t.Fatalf("release: %v", err)
		}
		assertPathsRemoved(t, owned)
	}()

	// Enough distinct sources to force LRU eviction several times over.
	const distinctSources = maxRetainedImages * 3
	for step := 0; step < steps; step++ {
		op := rng.Intn(10)
		if op <= 2 {
			h.registersSinceEvict++
		}
		switch op {
		case 0, 1, 2: // Register (new source, or resize of an existing one)
			key := fmt.Sprintf("src-%d", rng.Intn(distinctSources))
			source := syntheticSource(key)
			cols, rows := 1+rng.Intn(80), 1+rng.Intn(40)
			occ := fmt.Sprintf("occ-%d", rng.Intn(4))
			p, err := manager.Register(source, occ, cols, rows)
			if err != nil {
				t.Fatalf("register: %v", err)
			}
			h.pool = append(h.pool, p)
			if len(h.pool) > 256 {
				h.pool = h.pool[len(h.pool)-256:]
			}
		case 3, 4, 5: // Publish a random subset (sometimes the empty frame)
			manager.Publish(Frame{Placements: randomSubset(rng, h.pool)})
		case 6: // Reconcile, occasionally with a failing writer
			fail := rng.Intn(8) == 0
			err := h.reconcile(fail)
			if fail {
				if err == nil && h.hadPendingWork() {
					t.Fatal("failing writer did not report an error")
				}
				// After a failed write the next reconcile must retransmit
				// everything: a silently lost upload paints nothing.
				if err != nil {
					if err2 := h.reconcile(false); err2 != nil {
						t.Fatalf("retry reconcile: %v", err2)
					}
				}
			} else if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
		case 7: // Invalidate then reconcile must always retransmit
			manager.Invalidate()
			if err := h.reconcile(false); err != nil {
				t.Fatalf("reconcile after invalidate: %v", err)
			}
		case 8: // Capability churn
			manager.SetCapability([]Capability{Unknown, Kitty, Unsupported}[rng.Intn(3)])
			if err := h.reconcile(false); err != nil {
				t.Fatalf("reconcile after capability: %v", err)
			}
			manager.SetCapability(Kitty)
		case 9: // Probe / upload responses
			h.randomResponse(rng)
		}
		h.assertNoDanglingPlacements()
		assertBounded(t, manager, h.registersSinceEvict)
	}
	// Settle: a final clean reconcile must leave every applied placement live.
	manager.SetCapability(Kitty)
	manager.Invalidate()
	if err := h.reconcile(false); err != nil {
		t.Fatalf("final reconcile: %v", err)
	}
}

// hadPendingWork reports whether a Reconcile would have produced bytes. A
// clean (non-dirty) manager writes nothing, so a failing writer is never
// consulted and correctly returns no error.
func (h *modelHarness) hadPendingWork() bool {
	h.manager.mu.Lock()
	defer h.manager.mu.Unlock()
	if !h.manager.dirty || h.manager.capability != Kitty {
		return false
	}
	for _, p := range h.manager.desired.Placements {
		state := h.manager.sources[p.Source.Key]
		if state == nil && len(p.Source.PNG) > 0 {
			return true
		}
		if state == nil || state.err != "" {
			continue
		}
		id := p.ImageID
		if id == 0 {
			id = state.imageID
		}
		if !state.isUploaded(id) {
			return true
		}
		key := placementKey(p.Source.Key, p.Occurrence, p.Columns, p.Rows)
		if ps := h.manager.placements[key]; ps == nil || !ps.created {
			return true
		}
	}
	return false
}

func (h *modelHarness) randomResponse(rng *rand.Rand) {
	ids := []uint32{queryDirectID, queryTmuxID, queryTempID, queryTmuxTempID}
	var id uint32
	if rng.Intn(2) == 0 {
		id = ids[rng.Intn(len(ids))]
	} else {
		h.manager.mu.Lock()
		for _, s := range h.manager.sources {
			id = s.imageID
			break
		}
		h.manager.mu.Unlock()
	}
	if id == 0 {
		return
	}
	ok := rng.Intn(2) == 0
	message := "OK"
	if !ok {
		message = "EINVAL: bad"
	}
	h.manager.ObserveResponse(Response{ID: id, Message: message, OK: ok})
}

func randomSubset(rng *rand.Rand, pool []Placement) []Placement {
	if len(pool) == 0 || rng.Intn(6) == 0 {
		return nil
	}
	n := rng.Intn(min(len(pool), 12) + 1)
	out := make([]Placement, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, pool[rng.Intn(len(pool))])
	}
	return out
}

// assertBounded proves the manager's maps do not grow without bound. Eviction
// only runs inside Reconcile, so sources may legitimately exceed the cap by the
// number of Registers since the last Reconcile; slack accounts for that.
func assertBounded(t *testing.T, m *Manager, slack int) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sources) > maxRetainedImages+slack {
		t.Fatalf("sources = %d, unbounded past %d (+%d slack)", len(m.sources), maxRetainedImages, slack)
	}
	// Each retained source may hold up to maxUploadsPerSource live image IDs
	// (an evicted-then-re-registered image keeps its old ID alive while
	// on-screen placeholders still reference it), so this is the true bound.
	if maxTransfers := (maxRetainedImages+slack)*maxUploadsPerSource + slack; len(m.transferFiles) > maxTransfers {
		t.Fatalf("transferFiles = %d, unbounded past %d", len(m.transferFiles), maxTransfers)
	}
	// Placements are keyed by source+occurrence+size; bounded sources with a
	// bounded number of occurrences and sizes must keep this bounded too.
	if len(m.placements) > 200*maxRetainedImages {
		t.Fatalf("placements = %d, unbounded", len(m.placements))
	}
}

// TestManagerLongSequenceStaysBounded runs thousands of operations to prove no
// manager map grows without bound and no temp file is left behind. Unbounded
// growth here is a slow leak in a long-lived TUI session.
func TestManagerLongSequenceStaysBounded(t *testing.T) {
	runStateMachine(t, 4242, Raw, 6000)
}

// TestReconcileWriteFailureRetransmits proves a failed write never silently
// loses an upload: the terminal missed the bytes, so the next Reconcile must
// send them again or the placeholder cells would sit over an image the terminal
// does not have.
func TestReconcileWriteFailureRetransmits(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	source := syntheticSource("retransmit")
	placement, err := manager.Register(source, "occ", 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	manager.Publish(Frame{Placements: []Placement{placement}})
	if err := manager.Reconcile(&failingWriter{fail: true}); err == nil {
		t.Fatal("expected a write failure")
	}
	var out bytes.Buffer
	if err := manager.Reconcile(&out); err != nil {
		t.Fatal(err)
	}
	term := newTermModel()
	if err := term.Feed(out.String(), Raw); err != nil {
		t.Fatal(err)
	}
	if !term.hasPlacement(placement) {
		t.Fatal("upload was lost after a failed write: the placement would paint nothing")
	}
}

// TestEvictedSourceTempFilesAreRemoved proves temp-file transfers do not
// outlive the source that owns them.
func TestEvictedSourceTempFilesAreRemoved(t *testing.T) {
	manager := NewManager()
	manager.SetCapability(Kitty)
	manager.mu.Lock()
	manager.transport = TemporaryFile
	manager.mu.Unlock()
	var paths []string
	for i := 0; i < maxRetainedImages*2; i++ {
		p, err := manager.Register(syntheticSource(fmt.Sprintf("evict-%d", i)), "occ", 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		manager.Publish(Frame{Placements: []Placement{p}})
		if err := manager.Reconcile(&bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, trackedTempFiles(manager)...)
	}
	owned := trackedTempFiles(manager)
	if err := manager.Release(&bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	assertPathsRemoved(t, owned)
	// Everything ever handed out must be gone, not just the final generation.
	assertPathsRemoved(t, paths)
}
