package conductor

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	a2apb "github.com/Swarm-Code/mono/swarm-sdk/internal/a2a/pb"
)

// LivePeerPool is the production PeerPool implementation.
// It discovers peers from the filesystem (~/.swarm/swarms/<name>/peers/)
// and sends tasks via the A2A JSON-RPC HTTP protocol to each peer's
// endpoint_url.  Spawn execs swarmos in headless+A2A mode.
//
// Usage:
//
//	pool := conductor.NewLivePeerPool("default", "swarmos")
type LivePeerPool struct {
	swarmName  string
	swarmosBin string // path to swarmos binary; auto-detected if empty
	httpClient *http.Client
	a2aClient  *a2a.Client
}

// NewLivePeerPool creates a LivePeerPool for the given swarm.
// swarmName defaults to "default". swarmosBin is the path to the swarmos
// binary; if empty it is resolved via PATH.
func NewLivePeerPool(swarmName, swarmosBin string) *LivePeerPool {
	if swarmName == "" {
		swarmName = a2a.DefaultSwarmName
	}
	if swarmosBin == "" {
		if path, err := exec.LookPath("swarmos"); err == nil {
			swarmosBin = path
		} else {
			swarmosBin = "swarmos" // best-effort fallback
		}
	}
	hc := &http.Client{}
	return &LivePeerPool{
		swarmName:  swarmName,
		swarmosBin: swarmosBin,
		httpClient: hc,
		a2aClient:  a2a.NewClient(hc),
	}
}

// List returns all live peers in the swarm.
func (p *LivePeerPool) List() ([]a2a.PeerPresence, error) {
	return a2a.ListPeers(p.swarmName)
}

// SendTask sends prompt to the peer identified by handle via the A2A
// JSON-RPC SendMessage protocol.  Returns once the request has been
// accepted (not when the agent turn completes).
func (p *LivePeerPool) SendTask(ctx context.Context, handle, prompt string) error {
	peer, err := a2a.GetPeer(p.swarmName, handle)
	if err != nil {
		return fmt.Errorf("livepeer: get peer %q: %w", handle, err)
	}
	if peer == nil {
		return fmt.Errorf("livepeer: peer %q not found in swarm %q", handle, p.swarmName)
	}
	if peer.EndpointURL == "" {
		return fmt.Errorf("livepeer: peer %q has no endpoint_url — was it started with --a2a?", handle)
	}

	msg := &a2apb.Message{
		MessageId: fmt.Sprintf("conductor-%d", time.Now().UnixNano()),
		Role:      a2apb.Role_ROLE_USER,
		Parts: []*a2apb.Part{
			{Content: &a2apb.Part_Text{Text: prompt}},
		},
	}
	// ReturnImmediately=true: server spawns the agent task in a goroutine
	// and returns a Task ID immediately instead of blocking the HTTP
	// connection for the full agent turn (which can take minutes).
	req := &a2apb.SendMessageRequest{
		Message: msg,
		Configuration: &a2apb.SendMessageConfiguration{
			ReturnImmediately: true,
		},
	}

	_, err = p.a2aClient.SendMessage(ctx, peer.EndpointURL, req, nil)
	if err != nil {
		return fmt.Errorf("livepeer: send task to %q (%s): %w", handle, peer.EndpointURL, err)
	}
	return nil
}

// Spawn launches a new headless swarmos agent and registers it as an A2A
// peer.  Returns the peer handle once the presence file appears.
func (p *LivePeerPool) Spawn(ctx context.Context, cfg SpawnConfig) (string, error) {
	handle := cfg.Handle
	if handle == "" {
		handle = fmt.Sprintf("conductor-worker-%d", time.Now().UnixNano()%1_000_000)
	}

	args := []string{
		"daemon",
		"--a2a-handle", handle,
	}
	if cfg.WorkspacePath != "" {
		args = append(args, "--workspace", cfg.WorkspacePath)
	}
	if cfg.Model != "" {
		args = append(args, "-m", cfg.Model)
	}

	cmd := exec.CommandContext(ctx, p.swarmosBin, args...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("livepeer: spawn %q: %w", handle, err)
	}

	// Wait up to 5 s for the presence file to appear.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		peer, _ := a2a.GetPeer(p.swarmName, handle)
		if peer != nil && peer.EndpointURL != "" {
			return handle, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "", fmt.Errorf("livepeer: spawned %q but peer never appeared in swarm (process may still be starting)", handle)
}
