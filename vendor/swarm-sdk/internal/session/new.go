// Package session — new.go
//
// session.New legacy shim.  Translates the old session.Config struct into
// the equivalent client.Option calls so existing call sites keep working
// while we migrate them to client.New() directly.
package session

import (
	"errors"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
)

// Config configures a session.  Either Client must be supplied (in which
// case the existing client is reused) or the caller should switch to
// client.New() directly.
//
// Deprecated: use client.New() with the equivalent client.Option values
// (WithConfigManager, WithActiveMode, WithActiveAgent, WithActiveProfile).
type Config struct {
	// Client is the SDK client used to drive agent execution and
	// conversation management.  Required.
	Client *client.Client

	// ConfigManager is the unified configuration manager used by the
	// CRUD-style methods (CreateAgent, CreateProfile, CreateHook, etc.).
	// Optional — when nil, those methods return ErrNoConfigManager.
	ConfigManager *configbundle.Manager

	// InitialMode sets State.OperatingMode at construction time
	// (defaults to "act" when empty).
	InitialMode string

	// InitialAgent sets State.ActiveAgent at construction time.
	InitialAgent string

	// InitialProfile sets State.ActiveProfile at construction time.
	InitialProfile string

	// EventBuffer sizes the internal fan-out queue.  Currently unused —
	// preserved on the struct for source compat.
	EventBuffer int
}

// New constructs a Session from cfg.  Returns the wrapped *client.Client
// directly; the returned value satisfies the Session interface and is
// usable with every method in this package.
//
// When cfg.Client is non-nil the existing client is reused — its
// session-state surface is reset to honour InitialMode / InitialAgent /
// InitialProfile.  When cfg.Client is nil, returns an error: callers
// should use client.New() directly.
//
// Deprecated: use client.New() with the equivalent client.Option values
// (WithConfigManager, WithActiveMode, WithActiveAgent, WithActiveProfile).
func New(cfg Config) (*client.Client, error) {
	if cfg.Client == nil {
		return nil, errors.New("session.New: Config.Client is required")
	}
	c := cfg.Client

	// Attach ConfigManager directly so CRUD methods work even when the
	// client was constructed without a provider (matching the legacy
	// session.ClientSession behaviour where cfgMgr was a plain field).
	if cfg.ConfigManager != nil {
		c.SetConfigManager(cfg.ConfigManager)
	}

	// Re-seed the session-state surface with the requested initial
	// values so existing callers see the same behaviour.
	c.SetSessionState(cfg.InitialMode, cfg.InitialAgent, cfg.InitialProfile)

	return c, nil
}
