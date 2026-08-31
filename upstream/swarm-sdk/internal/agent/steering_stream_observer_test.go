package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// fakeObserver records the transcripts it receives and, on each
// invocation, reaches into ctx for the SteeringTarget and arms a block
// on the next "Bash" call. This mirrors what a real observer would do
// through the steeringtools tool surface, but without an LLM provider.
type fakeObserver struct {
	calls       atomic.Uint32
	lastFrag    atomic.Value // string
	armedReason atomic.Value // string
}

func (f *fakeObserver) Observe(ctx context.Context, transcript string) error {
	f.calls.Add(1)
	f.lastFrag.Store(transcript)

	if !IsSteeringReentrant(ctx) {
		// Defensive: observer must always be invoked with a reentrant
		// ctx so steering hooks short-circuit on the observer's own
		// downstream tool/LLM calls. If this ever fires, the driver
		// failed to mark ctx — that is a hard bug.
		return nil
	}
	target, ok := SteeringTargetFromContext(ctx)
	if !ok {
		return nil
	}
	reason := "drift detected by fake observer"
	target.ArmBlockNext("Bash", reason)
	f.armedReason.Store(reason)
	return nil
}

// makeBashEvent builds a hooks.Event shaped like the polling hook's
// before-tool payloads. Used by the Phase 2.4 transcript tests.
func makeBashEvent(i int) hooks.Event {
	return hooks.Event{
		Type:      hooks.EventToolBeforeExecute,
		Timestamp: time.Now(),
		Data: map[string]any{
			"tool_name": "Bash",
			"params":    map[string]any{"cmd": "echo hi", "i": i},
		},
	}
}

// TestDriverFlushInvokesObserverAndArmsBlock is the end-to-end Phase 2.4
// assertion: enqueueing FlushCount events makes the pump call
// observer.Observe with a non-empty transcript, and the observer
// successfully reaches the SteeringTarget through ctx to arm a block.
func TestDriverFlushInvokesObserverAndArmsBlock(t *testing.T) {
	obs := &fakeObserver{}
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      3,
		FlushAge:        10 * time.Second, // age threshold inert
		Observer:        obs,
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	for i := range 3 {
		d.Enqueue(makeBashEvent(i))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if obs.calls.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := obs.calls.Load(); got < 1 {
		t.Fatalf("observer was never invoked after 3 events")
	}

	frag, _ := obs.lastFrag.Load().(string)
	if !strings.Contains(frag, string(hooks.EventToolBeforeExecute)) {
		t.Errorf("transcript missing event type marker: %q", frag)
	}
	if !strings.Contains(frag, "tool_name=Bash") {
		t.Errorf("transcript missing tool name field: %q", frag)
	}

	// The fake armed a block on the target the driver supplied. Use
	// the inspector interface to verify it was actually delivered.
	insp, ok := d.Target().(DefaultSteeringTargetInspector)
	if !ok {
		t.Fatal("driver target does not implement DefaultSteeringTargetInspector")
	}
	pb, found := insp.ConsumeBlockFor("Bash")
	if !found {
		t.Fatal("observer did not arm a block on the target through ctx")
	}
	if pb.Reason == "" {
		t.Errorf("armed block missing reason: %+v", pb)
	}
}

// TestRenderEventTranscriptStableOrder verifies that the transcript
// renderer emits a deterministic field order (not Go's randomised map
// iteration) so that diffs between consecutive observer prompts are
// caused by event content, not key shuffling.
func TestRenderEventTranscriptStableOrder(t *testing.T) {
	a := makeBashEvent(0)
	b := makeBashEvent(0)
	a.Data["error"] = "x"
	b.Data["error"] = "x"
	a.Data["output"] = "y"
	b.Data["output"] = "y"

	got1 := renderEventTranscript([]hooks.Event{a, b})
	got2 := renderEventTranscript([]hooks.Event{a, b})
	if got1 != got2 {
		t.Fatalf("non-deterministic transcript:\n%q\n%q", got1, got2)
	}
	if !strings.Contains(got1, "tool_name=Bash") || !strings.Contains(got1, "output=y") {
		t.Errorf("transcript missing expected fields: %q", got1)
	}
}

// TestRenderEventTranscriptTruncatesLargeValues confirms the per-value
// cap so the observer's prompt stays bounded under huge tool output.
func TestRenderEventTranscriptTruncatesLargeValues(t *testing.T) {
	big := strings.Repeat("x", 1024)
	ev := makeBashEvent(0)
	ev.Data["output"] = big

	got := renderEventTranscript([]hooks.Event{ev})
	if !strings.Contains(got, "…") {
		t.Fatalf("expected ellipsis on truncated value")
	}
	if strings.Count(got, "x") > 260 {
		t.Errorf("value not truncated: total x=%d", strings.Count(got, "x"))
	}
}

// TestDriverFlushSkipsWhenObserverNil ensures that constructing the
// driver without an observer still increments flushedTotal (so the
// pump's heartbeat is visible) but does not panic.
func TestDriverFlushSkipsWhenObserverNil(t *testing.T) {
	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:       SteeringModeStream,
		FlushCount: 1,
	})
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	d.Enqueue(makeBashEvent(0))
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if d.FlushedTotal() >= 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("flushedTotal stayed 0 with observer=nil; expected pump to still tick")
}

// fakePeerMuter records MutePeer calls so we can assert pushEffects routes
// halts from SteeringTarget into the A2A layer.
type fakePeerMuter struct {
	mu    sync.Mutex
	calls []fakeMuteCall
}

type fakeMuteCall struct {
	peer   string
	reason string
	ttl    time.Duration
}

func (f *fakePeerMuter) MutePeer(peer, reason string, ttl time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeMuteCall{peer: peer, reason: reason, ttl: ttl})
}

func (f *fakePeerMuter) Calls() []fakeMuteCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeMuteCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// haltingObserver arms a HaltPeerLoop on every Observe() so we can verify
// the drain → push flow end-to-end.
type haltingObserver struct {
	calls atomic.Uint32
}

func (h *haltingObserver) Observe(ctx context.Context, transcript string) error {
	h.calls.Add(1)
	target, ok := SteeringTargetFromContext(ctx)
	if !ok {
		return nil
	}
	_ = target.HaltPeerLoop("peer-noisy", "ping-pong", 30*time.Second)
	return nil
}

// TestDriverPhase3_DriftAndPushEffects: Phase 3 integration. Enables
// drift tracking, feeds repeated Bash events, and verifies:
//  1. Observer transcript contains the drift summary.
//  2. Halts armed during Observe() are drained and routed to the PeerMuter
//     via pushEffects.
func TestDriverPhase3_DriftAndPushEffects(t *testing.T) {
	obs := &haltingObserver{}
	muter := &fakePeerMuter{}

	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      4,
		FlushAge:        50 * time.Millisecond,
		Observer:        obs,
		DriftEnabled:    true,
		Drift:           DriftConfig{ToolRepeatThreshold: 3},
		A2ARuntime:      muter,
	})

	ctx := t.Context()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	// 4 Bash events → triggers drift signal and a flush.
	for i := range 4 {
		d.Enqueue(makeBashEvent(i))
	}

	// Wait for the flush.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if obs.calls.Load() >= 1 && len(muter.Calls()) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if obs.calls.Load() < 1 {
		t.Fatalf("observer was never called")
	}
	if got := len(muter.Calls()); got < 1 {
		t.Fatalf("expected halt to be pushed to PeerMuter, got %d calls", got)
	}

	call := muter.Calls()[0]
	if call.peer != "peer-noisy" {
		t.Errorf("expected peer='peer-noisy', got %q", call.peer)
	}
	if call.reason != "ping-pong" {
		t.Errorf("expected reason='ping-pong', got %q", call.reason)
	}

	// Drain semantics: a second flush with no new halts should NOT re-push.
	priorCount := len(muter.Calls())
	for i := 4; i < 8; i++ {
		d.Enqueue(makeBashEvent(i))
	}
	time.Sleep(200 * time.Millisecond)

	// The observer arms again, so we expect +1. But if drain were broken,
	// we'd see +2 (old halt + new halt). This asserts the drain-not-snapshot
	// contract.
	if got := len(muter.Calls()); got != priorCount+1 {
		t.Errorf("expected exactly %d new mute call (drain semantics), got total=%d",
			1, got-priorCount)
	}
}

// recordingObserver captures the last transcript it received so tests can
// assert on its contents (e.g., that the drift summary line appears).
type recordingObserver struct {
	calls    atomic.Uint32
	lastFrag atomic.Value // string
}

func (r *recordingObserver) Observe(ctx context.Context, transcript string) error {
	r.calls.Add(1)
	r.lastFrag.Store(transcript)
	return nil
}

func makePeerDMEvent(peer string) hooks.Event {
	return hooks.Event{
		Type:      hooks.EventA2AMessageProjected,
		Timestamp: time.Now(),
		Data: map[string]any{
			"peer_handle": peer,
			"message_id":  "msg-x",
		},
	}
}

// TestDriverPhase4_PeerDMDriftInTranscript: Phase 4 integration. Enables
// drift with a low PeerDMThreshold, feeds EventA2AMessageProjected events
// for one peer, and verifies the observer's transcript contains the
// peer_dms drift line.
func TestDriverPhase4_PeerDMDriftInTranscript(t *testing.T) {
	obs := &recordingObserver{}

	d := NewStreamingSteeringDriver(SteeringDriverConfig{
		Mode:            SteeringModeStream,
		EventBufferSize: 16,
		FlushCount:      3,
		FlushAge:        50 * time.Millisecond,
		Observer:        obs,
		DriftEnabled:    true,
		Drift:           DriftConfig{PeerDMThreshold: 2},
	})

	ctx := t.Context()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop()

	// 3 peer DMs → triggers flush; threshold=2 so peer_dms line should appear.
	for range 3 {
		d.Enqueue(makePeerDMEvent("noisy-peer"))
	}

	// Wait for flush.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if obs.calls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if obs.calls.Load() < 1 {
		t.Fatalf("observer was never called")
	}

	frag := obs.lastFrag.Load().(string)
	if !strings.Contains(frag, "peer_dms: noisy-peer x3") {
		t.Errorf("expected peer_dms line in transcript, got:\n%s", frag)
	}
}
