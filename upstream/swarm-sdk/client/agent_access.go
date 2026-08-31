// Package client — agent_access.go
//
// Low-level accessors for internal client fields.  These give structured
// access to immutable or safely-shared internals rather than raw mutation
// paths.
package client

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// Agent returns the underlying *agent.Agent so callers can attach injectors
// (SetMessageInjector, SetRichMessageInjector) or configure per-request
// behaviour (SetToolFilter, SetupTaskPersistence).  May be nil before the
// client is fully initialised.
//
// Prefer higher-level Client methods where possible — direct access bypasses
// lifecycle management.
func (c *Client) Agent() *agent.Agent {
	return c.agent
}

// Logger returns the observability.Logger used internally by this client.
// Useful for adapters that need to emit structured log events through the
// same logger without constructing a new one.
func (c *Client) Logger() observability.Logger {
	return c.logger
}

// Tracer returns the observability.Tracer used internally by this client.
func (c *Client) Tracer() observability.Tracer {
	return c.tracer
}

// Storage returns the underlying conversation storage backend.  Callers that
// need raw Load/Save/Query access (e.g. building complex conversation queries
// not exposed by ConversationManager) can use this.  May be nil when the
// client was constructed without a storage directory.
func (c *Client) Storage() storage.Storage {
	return c.convStorage
}

// UpdateSystemPrompt updates the live system prompt for subsequent agent
// executions without going through the configbundle persistence layer.
// It is safe to call at any time between turns.
//
// Use SetSystemPrompt(ctx, name, content) from crud.go for persisted
// named system prompt management.
func (c *Client) UpdateSystemPrompt(prompt string) {
	c.opts.systemPrompt = prompt
	if c.agent != nil {
		c.agent.SetSystemPrompt(prompt)
	}
}
