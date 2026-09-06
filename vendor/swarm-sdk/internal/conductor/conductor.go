package conductor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
)

// Conductor is the top-level entry point for Symphony-compatible issue
// orchestration.  It wires together:
//
//   - A WORKFLOW.md definition (config + prompt template)
//   - An IssueTracker (Forgejo, GitHub, or Linear)
//   - A PeerPool (A2A peer discovery + spawn)
//   - The unified client.Event bus (all lifecycle events flow through it)
//   - A PeerDiscoveryPoller (join/leave/status events)
//   - An Orchestrator (poll/dispatch/retry state machine)
//
// Typical usage:
//
//	c, err := conductor.New(
//	    conductor.WithWorkflow("./WORKFLOW.md"),
//	    conductor.WithClient(myClient),
//	    conductor.WithPool(myPool),
//	)
//	if err != nil { ... }
//	if err := c.Start(ctx); err != nil { ... }
//	defer c.Stop()
type Conductor struct {
	workflow *WorkflowDef
	tracker  IssueTracker
	pool     PeerPool
	bus      *client.Client

	poller       *a2a.PeerDiscoveryPoller
	orchestrator *Orchestrator
}

// Option is a functional option for configuring a Conductor.
type Option func(*Conductor) error

// WithWorkflow loads and sets the WORKFLOW.md from path.
func WithWorkflow(path string) Option {
	return func(c *Conductor) error {
		wf, err := LoadWorkflow(path)
		if err != nil {
			return err
		}
		c.workflow = wf
		return nil
	}
}

// WithWorkflowDef sets a pre-parsed WorkflowDef directly (useful for tests).
func WithWorkflowDef(wf *WorkflowDef) Option {
	return func(c *Conductor) error {
		c.workflow = wf
		return nil
	}
}

// WithTracker sets the issue tracker explicitly.
// If not provided, New() constructs one from the WORKFLOW.md tracker config.
func WithTracker(t IssueTracker) Option {
	return func(c *Conductor) error {
		c.tracker = t
		return nil
	}
}

// WithPool sets the peer pool.
func WithPool(p PeerPool) Option {
	return func(c *Conductor) error {
		c.pool = p
		return nil
	}
}

// WithClient sets the client.Client used as the event bus.
func WithClient(cl *client.Client) Option {
	return func(c *Conductor) error {
		c.bus = cl
		return nil
	}
}

// New creates a Conductor from the given options.
// At minimum, WithWorkflow (or WithWorkflowDef) and WithClient are required.
// WithPool is required if the orchestrator will dispatch work.
func New(opts ...Option) (*Conductor, error) {
	c := &Conductor{}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if c.workflow == nil {
		return nil, fmt.Errorf("conductor: no workflow configured (use WithWorkflow)")
	}
	if c.bus == nil {
		return nil, fmt.Errorf("conductor: no client configured (use WithClient)")
	}
	// Auto-build tracker from WORKFLOW.md if not set explicitly.
	if c.tracker == nil {
		t, err := buildTracker(c.workflow.Config.Tracker)
		if err != nil {
			return nil, fmt.Errorf("conductor: build tracker: %w", err)
		}
		c.tracker = t
	}
	// Pool is optional (conductor can run in observe-only mode without spawning).
	if c.pool == nil {
		c.pool = newListOnlyPool()
	}
	return c, nil
}

// Start launches the peer discovery poller and the orchestrator poll loop.
// Returns immediately; both run in background goroutines.
func (c *Conductor) Start(ctx context.Context) error {
	// Wire peer discovery → event bus.
	pollInterval := time.Duration(c.workflow.Config.Polling.IntervalMs) * time.Millisecond
	if pollInterval < time.Second {
		pollInterval = 2 * time.Second
	}
	c.poller = a2a.NewPeerDiscoveryPoller(a2a.DefaultSwarmName, pollInterval, func(change a2a.PeerChange) {
		c.handlePeerChange(change)
	})
	c.poller.Start()

	// Build and start orchestrator.
	c.orchestrator = NewOrchestrator(c.workflow, c.tracker, c.pool, c.bus)
	c.orchestrator.Start(ctx)
	return nil
}

// Stop halts the poller and orchestrator and waits for them to exit.
func (c *Conductor) Stop() {
	if c.poller != nil {
		c.poller.Stop()
	}
	if c.orchestrator != nil {
		c.orchestrator.Stop()
	}
}

// Orchestrator returns the underlying Orchestrator for direct state inspection
// (e.g. from the TUI conductor screen).
func (c *Conductor) Orchestrator() *Orchestrator {
	return c.orchestrator
}

// handlePeerChange translates a2a.PeerChange into client.Event and injects it
// into the unified event bus.
func (c *Conductor) handlePeerChange(change a2a.PeerChange) {
	switch change.Event {
	case a2a.PeerEventJoined:
		c.bus.InjectEvent(client.Event{
			Kind: client.EventPeerJoined,
			Source: client.EventSource{
				Kind:        client.SourcePeer,
				PeerHandle:  change.Current.Handle,
				WorkspaceID: change.Current.Workspace,
			},
			Payload: client.PeerJoinedPayload{
				Handle:      change.Current.Handle,
				EndpointURL: change.Current.EndpointURL,
				Workspace:   change.Current.Workspace,
				Model:       change.Current.Model,
				PID:         change.Current.PID,
			},
		})

	case a2a.PeerEventLeft:
		c.bus.InjectEvent(client.Event{
			Kind: client.EventPeerLeft,
			Source: client.EventSource{
				Kind:       client.SourcePeer,
				PeerHandle: change.Previous.Handle,
			},
			Payload: client.PeerLeftPayload{
				Handle: change.Previous.Handle,
			},
		})
		// Notify orchestrator so it can retry any assigned issue.
		if c.orchestrator != nil {
			c.orchestrator.OnPeerFailed(change.Previous.Handle, "peer process exited")
		}

	case a2a.PeerEventStatus:
		c.bus.InjectEvent(client.Event{
			Kind: client.EventPeerStatus,
			Source: client.EventSource{
				Kind:       client.SourcePeer,
				PeerHandle: change.Current.Handle,
			},
			Payload: client.PeerStatusPayload{
				Handle:      change.Current.Handle,
				Status:      change.Current.Status,
				CurrentTask: change.Current.CurrentTask,
				Workspace:   change.Current.Workspace,
			},
		})
		// Detect peer going idle after being dispatched a task → mark completed.
		if c.orchestrator != nil && change.Current.Status == string(a2a.SwarmStatusIdle) && change.Previous.Status == string(a2a.SwarmStatusWorking) {
			c.orchestrator.OnPeerCompleted(change.Current.Handle)
		}
	}
}

// ── tracker factory ───────────────────────────────────────────────────────────

func buildTracker(cfg TrackerConfig) (IssueTracker, error) {
	kind := strings.ToLower(strings.TrimSpace(cfg.Kind))
	if kind == "" {
		return nil, fmt.Errorf("tracker.kind is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("tracker.api_key is required (or set the corresponding env var)")
	}
	owner, repo, err := splitOwnerRepo(cfg.Repo)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "github":
		return NewGitHubTracker(cfg.APIKey, owner, repo,
			cfg.ActiveLabels, cfg.TerminalLabels), nil
	default:
		return nil, fmt.Errorf("unsupported tracker.kind %q (supported: github)", kind)
	}
}

func splitOwnerRepo(repo string) (string, string, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("tracker.repo must be in owner/repo format, got %q", repo)
	}
	return parts[0], parts[1], nil
}

// ── list-only pool (observe mode) ────────────────────────────────────────────

// listOnlyPool is a no-op PeerPool used when no pool is configured.
// It can list peers but cannot send tasks or spawn new workers.
type listOnlyPool struct{}

func newListOnlyPool() PeerPool { return &listOnlyPool{} }

func (p *listOnlyPool) List() ([]a2a.PeerPresence, error) {
	return a2a.ListPeers(a2a.DefaultSwarmName)
}

func (p *listOnlyPool) SendTask(_ context.Context, handle, _ string) error {
	return fmt.Errorf("conductor: pool not configured; cannot send task to peer %q", handle)
}

func (p *listOnlyPool) Spawn(_ context.Context, cfg SpawnConfig) (string, error) {
	return "", fmt.Errorf("conductor: pool not configured; cannot spawn peer %q", cfg.Handle)
}
