// Package client — source.go
//
// Context helpers for propagating EventSource through the agent execution
// pipeline, and public methods for injecting external events (A2A peers,
// background agents, conductor) into the unified event bus.
package client

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
)

// sourceContextKey is the unexported key used to store EventSource in a
// context.Context.  Using a named struct prevents collisions with any
// third-party context keys.
type sourceContextKey struct{}

// WithSource returns a derived context that carries src.  Pass this context
// when invoking the agent callback (IntermediateCallback) or when calling
// InjectUpdate so that the event bus can tag every resulting Event with the
// correct provenance.
//
//	ctx = client.WithSource(ctx, client.EventSource{
//	    Kind:       client.SourcePeer,
//	    PeerHandle: "laptop-a1b2c3d4",
//	    ConvID:     conv.ID,
//	})
func WithSource(ctx context.Context, src EventSource) context.Context {
	return context.WithValue(ctx, sourceContextKey{}, src)
}

// sourceFromContext extracts the EventSource stored by WithSource.
// Returns a zero-value EventSource (Kind == "") when none is present;
// callers should default to SourceLocal in that case.
func sourceFromContext(ctx context.Context) EventSource {
	if ctx == nil {
		return EventSource{}
	}
	if s, ok := ctx.Value(sourceContextKey{}).(EventSource); ok {
		return s
	}
	return EventSource{}
}

// ── Public injection surface ──────────────────────────────────────────────────

// InjectUpdate injects an IntermediateUpdate from an external source (a remote
// A2A peer, a background agent, or a conductor-launched headless worker) into
// the unified event bus.  Every Subscribe and SubscribeUpdates listener
// receives the update exactly as if it had been produced by a local agent,
// with Source.Kind set to identify the originating system.
//
// Typical callers:
//   - A2A runtime's inbound event callback (Source.Kind = SourcePeer)
//   - BackgroundAgentManager (Source.Kind = SourceBackground)
//   - Conductor peer stream subscriptions (Source.Kind = SourcePeer)
//
// InjectUpdate is safe to call from any goroutine.
func (c *Client) InjectUpdate(ctx context.Context, src EventSource, u agent.IntermediateUpdate) error {
	return c.fanout(WithSource(ctx, src), u)
}

// InjectEvent publishes a pre-built Event directly to the Subscribe bus.
// Use this for non-agent events that originate outside the client's own agent
// (peer lifecycle changes, conductor task transitions, etc.).
//
// InjectEvent is safe to call from any goroutine.
func (c *Client) InjectEvent(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	c.dispatchEvent(ev)
}
