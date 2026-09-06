package conductor

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
)

// PeerPool abstracts peer lifecycle operations so the orchestrator is not
// coupled to the TUI's spawn implementation.
type PeerPool interface {
	// List returns all currently live peers in the swarm.
	List() ([]a2a.PeerPresence, error)

	// SendTask sends a prompt to the peer identified by handle and returns once
	// the task has been accepted for execution (not when it completes).
	SendTask(ctx context.Context, handle, prompt string) error

	// Spawn creates a new headless agent peer for the given workspace path.
	// Returns the A2A handle of the spawned peer.
	Spawn(ctx context.Context, cfg SpawnConfig) (string, error)
}

// SpawnConfig configures a new headless peer spawned by the orchestrator.
type SpawnConfig struct {
	Handle        string // desired A2A handle
	WorkspacePath string // directory to use as cwd
	SystemPrompt  string // optional system prompt override
	Model         string // optional model override
}

// ── orchestrator state ────────────────────────────────────────────────────────

// runEntry tracks a single in-flight issue assignment.
type runEntry struct {
	issue      Issue
	peerHandle string
	startedAt  time.Time
	attempt    int // 0 = first run
}

// retryEntry holds a scheduled retry for a failed issue.
type retryEntry struct {
	issue     Issue
	attempt   int
	dueAt     time.Time
	lastError string
}

// Orchestrator implements the Symphony §7-8 poll/dispatch/reconcile loop.
// All state mutations are serialised under mu.  Every lifecycle change is
// published to the client event bus so any Subscribe listener receives it.
type Orchestrator struct {
	workflow *WorkflowDef
	tracker  IssueTracker
	pool     PeerPool
	bus      *client.Client // event bus only; Chat() is never called here

	// ── mutable state (under mu) ─────────────────────────────────────────────
	mu      sync.Mutex
	claimed map[string]bool        // issueID → claimed (running or retrying)
	running map[string]*runEntry   // issueID → run entry
	retries map[string]*retryEntry // issueID → retry entry

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewOrchestrator creates an orchestrator.  Call Start() to begin polling.
func NewOrchestrator(wf *WorkflowDef, tracker IssueTracker, pool PeerPool, bus *client.Client) *Orchestrator {
	return &Orchestrator{
		workflow: wf,
		tracker:  tracker,
		pool:     pool,
		bus:      bus,
		claimed:  make(map[string]bool),
		running:  make(map[string]*runEntry),
		retries:  make(map[string]*retryEntry),
		stopCh:   make(chan struct{}),
	}
}

// Start begins the poll loop.  Non-blocking; the loop runs in a goroutine.
func (o *Orchestrator) Start(ctx context.Context) {
	o.wg.Add(1)
	go o.loop(ctx)
}

// WorkflowConfig returns the WorkflowConfig in use by this orchestrator.
// Useful for status displays (e.g. "polling every Nms").
func (o *Orchestrator) WorkflowConfig() WorkflowConfig {
	return o.workflow.Config
}

// Stop signals the poll loop to halt and waits for it to exit.
func (o *Orchestrator) Stop() {
	close(o.stopCh)
	o.wg.Wait()
}

// OnPeerCompleted must be called when a peer finishes its assigned task.
// The conductor screen / TUI calls this after detecting a peer going idle
// following a task dispatch.
func (o *Orchestrator) OnPeerCompleted(peerHandle string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for issueID, entry := range o.running {
		if entry.peerHandle == peerHandle {
			delete(o.running, issueID)
			delete(o.claimed, issueID)
			o.bus.InjectEvent(client.Event{
				Kind: client.EventTaskCompleted,
				Source: client.EventSource{
					Kind:       client.SourcePeer,
					PeerHandle: peerHandle,
				},
				Payload: client.TaskCompletedPayload{
					IssueID:    issueID,
					Identifier: entry.issue.Identifier,
					PeerHandle: peerHandle,
				},
			})
			return
		}
	}
}

// OnPeerFailed must be called when a peer fails its assigned task.
func (o *Orchestrator) OnPeerFailed(peerHandle, errMsg string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for issueID, entry := range o.running {
		if entry.peerHandle == peerHandle {
			delete(o.running, issueID)
			nextAttempt := entry.attempt + 1
			delay := o.backoffDelay(nextAttempt)
			o.retries[issueID] = &retryEntry{
				issue:     entry.issue,
				attempt:   nextAttempt,
				dueAt:     time.Now().Add(delay),
				lastError: errMsg,
			}
			o.bus.InjectEvent(client.Event{
				Kind: client.EventTaskRetrying,
				Source: client.EventSource{
					Kind:       client.SourcePeer,
					PeerHandle: peerHandle,
				},
				Payload: client.TaskRetryingPayload{
					IssueID:    issueID,
					Identifier: entry.issue.Identifier,
					Attempt:    nextAttempt,
					DelayMs:    delay.Milliseconds(),
					Error:      errMsg,
				},
			})
			return
		}
	}
}

// ── poll loop ─────────────────────────────────────────────────────────────────

func (o *Orchestrator) loop(ctx context.Context) {
	defer o.wg.Done()
	interval := time.Duration(o.workflow.Config.Polling.IntervalMs) * time.Millisecond
	// Tick immediately.
	o.tick(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-o.stopCh:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			o.tick(ctx)
		}
	}
}

func (o *Orchestrator) tick(ctx context.Context) {
	// 1. Reconcile: remove run entries whose peer has died.
	peers, ok := o.reconcile()
	if !ok {
		return
	}

	// 2. Promote ready retries.
	o.promoteRetries(ctx)

	// 3. Fetch candidate issues.
	issues, err := o.tracker.FetchCandidateIssues(ctx)
	if err != nil {
		// Log via event bus — consumers can surface it.
		o.bus.InjectEvent(client.Event{
			Kind:    client.EventError,
			Payload: client.ErrorPayload{Message: fmt.Sprintf("conductor: fetch issues: %v", err)},
		})
		return
	}

	// 4. Dispatch eligible issues to available peers.
	idlePeers := idlePeerHandles(peers)
	o.dispatch(ctx, issues, idlePeers)
}

// reconcile removes run entries whose assigned peer is no longer alive.
// Returns the current peer list (reused by tick to avoid a second List call)
// and false if the peer list could not be fetched.
func (o *Orchestrator) reconcile() ([]a2a.PeerPresence, bool) {
	peers, err := o.pool.List()
	if err != nil {
		return nil, false
	}
	alive := map[string]bool{}
	for _, p := range peers {
		alive[p.Handle] = true
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for issueID, entry := range o.running {
		if !alive[entry.peerHandle] {
			// Peer died — schedule retry.
			delete(o.running, issueID)
			nextAttempt := entry.attempt + 1
			delay := o.backoffDelay(nextAttempt)
			o.retries[issueID] = &retryEntry{
				issue:     entry.issue,
				attempt:   nextAttempt,
				dueAt:     time.Now().Add(delay),
				lastError: "peer process exited unexpectedly",
			}
			o.bus.InjectEvent(client.Event{
				Kind: client.EventTaskRetrying,
				Payload: client.TaskRetryingPayload{
					IssueID:    issueID,
					Identifier: entry.issue.Identifier,
					Attempt:    nextAttempt,
					DelayMs:    delay.Milliseconds(),
					Error:      "peer process exited unexpectedly",
				},
			})
		}
	}
	return peers, true
}

// promoteRetries dispatches retry entries that are past their due time.
func (o *Orchestrator) promoteRetries(ctx context.Context) {
	now := time.Now()
	o.mu.Lock()
	var ready []*retryEntry
	for issueID, r := range o.retries {
		if now.After(r.dueAt) {
			ready = append(ready, r)
			delete(o.retries, issueID)
		}
	}
	o.mu.Unlock()

	if len(ready) == 0 {
		return
	}
	peers, _ := o.pool.List()
	idlePeers := idlePeerHandles(peers)

	for _, r := range ready {
		if len(idlePeers) == 0 {
			// Re-queue with no delay if no peers available.
			o.mu.Lock()
			o.retries[r.issue.ID] = &retryEntry{
				issue:     r.issue,
				attempt:   r.attempt,
				dueAt:     time.Now().Add(5 * time.Second),
				lastError: "no idle peers",
			}
			o.mu.Unlock()
			continue
		}
		handle := idlePeers[0]
		idlePeers = idlePeers[1:]
		o.assignToPeer(ctx, r.issue, handle, r.attempt)
	}
}

// dispatch assigns unclaimed issues to idle peers.
func (o *Orchestrator) dispatch(ctx context.Context, issues []Issue, idlePeers []string) {
	maxAgents := o.workflow.Config.Agent.MaxConcurrentAgents

	o.mu.Lock()
	runningCount := len(o.running)
	o.mu.Unlock()

	for _, issue := range sortForDispatch(issues) {
		if len(idlePeers) == 0 {
			break
		}
		if runningCount >= maxAgents {
			break
		}
		o.mu.Lock()
		claimed := o.claimed[issue.ID]
		o.mu.Unlock()
		if claimed {
			continue
		}
		handle := idlePeers[0]
		idlePeers = idlePeers[1:]
		o.assignToPeer(ctx, issue, handle, 0)
		runningCount++
	}
}

// assignToPeer claims an issue, builds the prompt, sends it to the peer, and
// emits EventTaskDispatched.
func (o *Orchestrator) assignToPeer(ctx context.Context, issue Issue, handle string, attempt int) {
	o.mu.Lock()
	o.claimed[issue.ID] = true
	o.running[issue.ID] = &runEntry{
		issue:      issue,
		peerHandle: handle,
		startedAt:  time.Now(),
		attempt:    attempt,
	}
	o.mu.Unlock()

	var attemptPtr *int
	if attempt > 0 {
		a := attempt
		attemptPtr = &a
	}
	prompt, err := o.workflow.BuildPrompt(issue, attemptPtr)
	if err != nil {
		o.bus.InjectEvent(client.Event{
			Kind:    client.EventError,
			Payload: client.ErrorPayload{Message: fmt.Sprintf("conductor: build prompt for %s: %v", issue.Identifier, err)},
		})
		o.mu.Lock()
		delete(o.claimed, issue.ID)
		delete(o.running, issue.ID)
		o.mu.Unlock()
		return
	}

	if err := o.pool.SendTask(ctx, handle, prompt); err != nil {
		o.bus.InjectEvent(client.Event{
			Kind:    client.EventError,
			Payload: client.ErrorPayload{Message: fmt.Sprintf("conductor: send task %s → %s: %v", issue.Identifier, handle, err)},
		})
		o.mu.Lock()
		delete(o.claimed, issue.ID)
		delete(o.running, issue.ID)
		o.mu.Unlock()
		return
	}

	o.bus.InjectEvent(client.Event{
		Kind: client.EventTaskDispatched,
		Source: client.EventSource{
			Kind:       client.SourcePeer,
			PeerHandle: handle,
		},
		Payload: client.TaskDispatchedPayload{
			IssueID:    issue.ID,
			Identifier: issue.Identifier,
			PeerHandle: handle,
			Attempt:    attempt,
		},
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// backoffDelay computes the exponential backoff delay for attempt n (1-based).
// First continuation after clean exit uses 1 s; failures use 10 s × 2^(n-1),
// capped at MaxRetryBackoffMs.
func (o *Orchestrator) backoffDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 1 * time.Second
	}
	maxMs := o.workflow.Config.Agent.MaxRetryBackoffMs
	ms := min(int64(10_000)*int64(math.Pow(2, float64(attempt-1))), int64(maxMs))
	return time.Duration(ms) * time.Millisecond
}

// idlePeerHandles returns handles of peers with status == "idle".
func idlePeerHandles(peers []a2a.PeerPresence) []string {
	var out []string
	for _, p := range peers {
		if p.Status == string(a2a.SwarmStatusIdle) || p.Status == "" {
			out = append(out, p.Handle)
		}
	}
	return out
}

// sortForDispatch sorts issues by priority (ascending) then created_at
// (oldest first) as specified in Symphony SPEC §8.2.
func sortForDispatch(issues []Issue) []Issue {
	sorted := make([]Issue, len(issues))
	copy(sorted, issues)
	// Simple insertion sort — issue counts are typically small (<100).
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && issueIsHigherPriority(sorted[j], sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	return sorted
}

func issueIsHigherPriority(a, b Issue) bool {
	if a.Priority != b.Priority {
		if a.Priority == 0 {
			return false // unset sorts last
		}
		if b.Priority == 0 {
			return true
		}
		return a.Priority < b.Priority
	}
	return a.CreatedAt.Before(b.CreatedAt)
}
