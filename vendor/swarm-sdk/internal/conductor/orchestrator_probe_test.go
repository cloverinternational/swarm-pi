// Package conductor — orchestrator_probe_test.go
//
// Integration probes for the Orchestrator that exercise the full
// poll→claim→dispatch→complete/fail/retry state machine using in-process
// mock tracker and pool implementations.  All events are captured from the
// unified client.Event bus and asserted.
package conductor_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conductor"
)

// ─── Mock tracker ─────────────────────────────────────────────────────────────

type mockTracker struct {
	mu     sync.Mutex
	issues []conductor.Issue
	errOn  string // if non-empty, FetchCandidateIssues returns this error
}

func (m *mockTracker) FetchCandidateIssues(_ context.Context) ([]conductor.Issue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.errOn != "" {
		return nil, errors.New(m.errOn)
	}
	out := make([]conductor.Issue, len(m.issues))
	copy(out, m.issues)
	return out, nil
}

func (m *mockTracker) FetchIssuesByIDs(_ context.Context, _ []string) ([]conductor.Issue, error) {
	return nil, nil
}

func (m *mockTracker) FetchTerminalIssueIDs(_ context.Context) ([]string, error) {
	return nil, nil
}

func (m *mockTracker) TransitionIssue(_ context.Context, _, _ string) error { return nil }
func (m *mockTracker) PostComment(_ context.Context, _, _ string) error     { return nil }

// ─── Mock pool ────────────────────────────────────────────────────────────────

type mockPool struct {
	mu     sync.Mutex
	peers  []a2a.PeerPresence
	sent   []sendRecord
	sendFn func(handle, prompt string) error // optional override
}

type sendRecord struct {
	handle string
	prompt string
}

func (p *mockPool) List() ([]a2a.PeerPresence, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]a2a.PeerPresence, len(p.peers))
	copy(out, p.peers)
	return out, nil
}

func (p *mockPool) SendTask(_ context.Context, handle, prompt string) error {
	if p.sendFn != nil {
		if err := p.sendFn(handle, prompt); err != nil {
			return err
		}
	}
	p.mu.Lock()
	p.sent = append(p.sent, sendRecord{handle: handle, prompt: prompt})
	p.mu.Unlock()
	return nil
}

func (p *mockPool) Spawn(_ context.Context, _ conductor.SpawnConfig) (string, error) {
	return "spawned-1", nil
}

func (p *mockPool) sentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// newTestBus returns a bare *client.Client suitable for use as an event bus
// in tests: no provider configured, sessStopped == false (zero value).
func newTestBus(t *testing.T) *client.Client {
	t.Helper()
	c := &client.Client{}
	c.SetSessionState("act", "", "")
	return c
}

// collectBusEvents subscribes to bus and returns all events received within
// timeout.  The subscriber is removed after timeout.
func collectBusEvents(bus *client.Client, timeout time.Duration) []client.Event {
	ch := make(chan client.Event, 64)
	unsub := bus.Subscribe(func(ev client.Event) error {
		ch <- ev
		return nil
	})
	time.Sleep(timeout)
	unsub()
	close(ch)
	var out []client.Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func eventsOfKind(evs []client.Event, kind client.EventKind) []client.Event {
	var out []client.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

// minimalWorkflow returns a WorkflowDef with sensible test defaults.
func minimalWorkflow(t *testing.T, body string) *conductor.WorkflowDef {
	t.Helper()
	src := "---\ntracker:\n  kind: github\nagent:\n  max_concurrent_agents: 10\n  max_retry_backoff_ms: 300000\npolling:\n  interval_ms: 60000\n---\n" + body
	wf, err := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")
	if err != nil {
		t.Fatalf("ParseWorkflow: %v", err)
	}
	return wf
}

// ─── tests ────────────────────────────────────────────────────────────────────

// TestOrchestrator_DispatchCycle verifies that a single issue is dispatched to
// the one available idle peer and EventTaskDispatched is emitted on the bus.
func TestOrchestrator_DispatchCycle(t *testing.T) {
	wf := minimalWorkflow(t, "Work on {{.Issue.Identifier}}: {{.Issue.Title}}")

	tracker := &mockTracker{
		issues: []conductor.Issue{
			{ID: "1", Identifier: "#1", Title: "First issue", Priority: 1, CreatedAt: time.Now()},
		},
	}
	pool := &mockPool{
		peers: []a2a.PeerPresence{{Handle: "worker-a", Status: "idle"}},
	}
	bus := newTestBus(t)

	var mu sync.Mutex
	var received []client.Event
	bus.Subscribe(func(ev client.Event) error {
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
		return nil
	})

	orch := conductor.NewOrchestrator(wf, tracker, pool, bus)
	ctx := t.Context()
	orch.Start(ctx)
	time.Sleep(100 * time.Millisecond) // first tick fires immediately
	orch.Stop()

	if pool.sentCount() != 1 {
		t.Errorf("SendTask call count: got %d, want 1", pool.sentCount())
	}

	mu.Lock()
	dispatched := eventsOfKind(received, client.EventTaskDispatched)
	mu.Unlock()

	if len(dispatched) != 1 {
		t.Fatalf("EventTaskDispatched count: got %d, want 1", len(dispatched))
	}
	p := dispatched[0].Payload.(client.TaskDispatchedPayload)
	if p.IssueID != "1" {
		t.Errorf("IssueID = %q, want 1", p.IssueID)
	}
	if p.PeerHandle != "worker-a" {
		t.Errorf("PeerHandle = %q, want worker-a", p.PeerHandle)
	}
	if p.Attempt != 0 {
		t.Errorf("Attempt = %d, want 0 (first run)", p.Attempt)
	}
	if dispatched[0].Source.Kind != client.SourcePeer {
		t.Errorf("Source.Kind = %q, want %q", dispatched[0].Source.Kind, client.SourcePeer)
	}
}

// TestOrchestrator_NoDuplicateDispatch verifies that an issue claimed in one
// tick is not re-dispatched in subsequent ticks even when multiple idle peers
// are available.
func TestOrchestrator_NoDuplicateDispatch(t *testing.T) {
	wf := minimalWorkflow(t, "{{.Issue.Identifier}}")

	tracker := &mockTracker{
		issues: []conductor.Issue{
			{ID: "X", Identifier: "PROBE-X", Title: "unique", Priority: 1},
		},
	}
	pool := &mockPool{
		peers: []a2a.PeerPresence{
			{Handle: "idle-1", Status: "idle"},
			{Handle: "idle-2", Status: "idle"},
		},
	}
	bus := newTestBus(t)
	orch := conductor.NewOrchestrator(wf, tracker, pool, bus)

	ctx := t.Context()
	orch.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	orch.Stop()

	if got := pool.sentCount(); got != 1 {
		t.Errorf("issue PROBE-X dispatched %d times, want exactly 1", got)
	}
}

// TestOrchestrator_OnPeerCompleted verifies that calling OnPeerCompleted emits
// EventTaskCompleted on the bus and removes the run entry.
func TestOrchestrator_OnPeerCompleted(t *testing.T) {
	wf := minimalWorkflow(t, "")
	tracker := &mockTracker{
		issues: []conductor.Issue{
			{ID: "42", Identifier: "PROBE-42", Title: "Complete me"},
		},
	}
	pool := &mockPool{
		peers: []a2a.PeerPresence{{Handle: "w1", Status: "idle"}},
	}
	bus := newTestBus(t)

	var mu sync.Mutex
	var completed []client.Event
	bus.Subscribe(func(ev client.Event) error {
		if ev.Kind == client.EventTaskCompleted {
			mu.Lock()
			completed = append(completed, ev)
			mu.Unlock()
		}
		return nil
	})

	orch := conductor.NewOrchestrator(wf, tracker, pool, bus)
	ctx := t.Context()
	orch.Start(ctx)
	time.Sleep(100 * time.Millisecond) // dispatch runs
	orch.Stop()

	// Signal completion for the peer that received the task.
	orch.OnPeerCompleted("w1")

	mu.Lock()
	n := len(completed)
	mu.Unlock()
	if n != 1 {
		t.Fatalf("EventTaskCompleted count: got %d, want 1", n)
	}
	p := completed[0].Payload.(client.TaskCompletedPayload)
	if p.IssueID != "42" {
		t.Errorf("completed IssueID = %q, want 42", p.IssueID)
	}
	if p.PeerHandle != "w1" {
		t.Errorf("completed PeerHandle = %q, want w1", p.PeerHandle)
	}
}

// TestOrchestrator_OnPeerFailed_SchedulesRetry verifies that a failed peer
// emits EventTaskRetrying and re-queues the issue.
func TestOrchestrator_OnPeerFailed_SchedulesRetry(t *testing.T) {
	wf := minimalWorkflow(t, "")
	tracker := &mockTracker{
		issues: []conductor.Issue{
			{ID: "5", Identifier: "PROBE-5", Title: "Retryable"},
		},
	}
	pool := &mockPool{
		peers: []a2a.PeerPresence{{Handle: "w2", Status: "idle"}},
	}
	bus := newTestBus(t)

	var mu sync.Mutex
	var retrying []client.Event
	bus.Subscribe(func(ev client.Event) error {
		if ev.Kind == client.EventTaskRetrying {
			mu.Lock()
			retrying = append(retrying, ev)
			mu.Unlock()
		}
		return nil
	})

	orch := conductor.NewOrchestrator(wf, tracker, pool, bus)
	ctx := t.Context()
	orch.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	orch.Stop()

	orch.OnPeerFailed("w2", "simulated crash")

	mu.Lock()
	got := len(retrying)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("EventTaskRetrying count: got %d, want 1", got)
	}
	rp := retrying[0].Payload.(client.TaskRetryingPayload)
	if rp.IssueID != "5" {
		t.Errorf("retry IssueID = %q, want 5", rp.IssueID)
	}
	if rp.Attempt != 1 {
		t.Errorf("retry Attempt = %d, want 1", rp.Attempt)
	}
	if rp.Error != "simulated crash" {
		t.Errorf("retry Error = %q, want 'simulated crash'", rp.Error)
	}
}

// TestOrchestrator_TrackerError verifies that a tracker error emits
// EventError on the bus (no panic, no dispatch).
func TestOrchestrator_TrackerError(t *testing.T) {
	wf := minimalWorkflow(t, "")
	tracker := &mockTracker{errOn: "tracker down"}
	pool := &mockPool{
		peers: []a2a.PeerPresence{{Handle: "idle", Status: "idle"}},
	}
	bus := newTestBus(t)

	var mu sync.Mutex
	var errEvents []client.Event
	bus.Subscribe(func(ev client.Event) error {
		if ev.Kind == client.EventError {
			mu.Lock()
			errEvents = append(errEvents, ev)
			mu.Unlock()
		}
		return nil
	})

	orch := conductor.NewOrchestrator(wf, tracker, pool, bus)
	ctx := t.Context()
	orch.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	orch.Stop()

	mu.Lock()
	n := len(errEvents)
	mu.Unlock()
	if n < 1 {
		t.Errorf("expected ≥1 EventError on tracker failure, got %d", n)
	}
	if pool.sentCount() != 0 {
		t.Errorf("no tasks should be dispatched when tracker errors, got %d", pool.sentCount())
	}
}

// TestOrchestrator_MaxConcurrentAgents verifies that the orchestrator never
// dispatches more tasks simultaneously than max_concurrent_agents.
func TestOrchestrator_MaxConcurrentAgents(t *testing.T) {
	src := "---\ntracker:\n  kind: github\nagent:\n  max_concurrent_agents: 2\n  max_retry_backoff_ms: 300000\npolling:\n  interval_ms: 60000\n---\n"
	wf, _ := conductor.ParseWorkflowForTest([]byte(src), "/tmp/WORKFLOW.md")

	var issues []conductor.Issue
	for i := range 5 {
		issues = append(issues, conductor.Issue{
			ID:         fmt.Sprintf("%d", i),
			Identifier: fmt.Sprintf("PROBE-%d", i),
			Title:      "issue",
			Priority:   1,
		})
	}
	tracker := &mockTracker{issues: issues}
	pool := &mockPool{}
	for i := range 5 {
		pool.peers = append(pool.peers, a2a.PeerPresence{
			Handle: fmt.Sprintf("w%d", i),
			Status: "idle",
		})
	}
	bus := newTestBus(t)

	orch := conductor.NewOrchestrator(wf, tracker, pool, bus)
	ctx := t.Context()
	orch.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	orch.Stop()

	if got := pool.sentCount(); got > 2 {
		t.Errorf("dispatched %d tasks with max_concurrent_agents=2 — limit not honoured", got)
	}
}
