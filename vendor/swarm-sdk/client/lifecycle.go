// Package client — lifecycle.go
//
// Start/Stop pair for the unified client.Client surface, lifted from
// session.ClientSession.  Together they wire the themed-event bridge,
// drain in-flight turns on shutdown, and let *client.Client satisfy
// the session.Session interface.
package client

import (
	"context"
)

// Start hooks the client into its own IntermediateUpdate stream and
// begins themed-event fan-out.  Idempotent: subsequent calls return nil
// without re-subscribing.
//
// After Start returns, every call that mutates session-state (SetMode,
// SetModel, SwitchProfile, CreateAgent, SetTheme, etc.) emits a
// corresponding themed Event to subscribers registered via Subscribe().
// Subscribe is safe to call before or after Start.
func (c *Client) Start(_ context.Context) error {
	c.startAgentBridge()
	return nil
}

// Stop cancels any in-flight turn, unsubscribes from the themed-event
// bridge, and drains internal goroutines.  Idempotent.  Safe to call
// from any goroutine.
//
// Stop does NOT close the underlying conversation storage; call
// Client.Close() for that.  Once stopped, the client cannot be
// restarted — construct a new Client for a fresh session.
func (c *Client) Stop(_ context.Context) error {
	c.stopAgentBridge()
	return nil
}
