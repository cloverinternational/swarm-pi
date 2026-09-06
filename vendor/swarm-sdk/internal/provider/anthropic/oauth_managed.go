package anthropic

import (
	"context"
	"os"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// ManagedModeEnvVar is the environment variable that switches the Anthropic
// OAuth provider into "managed" (centrally-refreshed consumer) mode.
//
// When set to a truthy value ("1", "true", "yes", "on"; case-insensitive),
// the provider:
//
//   - Reloads the OAuth access token from the same oauth.json it uses today,
//     re-reading it from disk before each request. The reload is cheap: the
//     file mtime is cached, so the JSON is only re-parsed when the file
//     actually changes (i.e. when the central refresher rotates the token).
//   - NEVER self-refreshes. RefreshAndStoreToken / refreshOAuthToken are never
//     invoked, and the provider never writes oauth.json. It is a read-only
//     consumer of a token owned and rotated by a single central daemon.
//   - On a 401 / auth-expired response, reloads the token from disk ONCE and
//     retries; if it still 401s, the error is returned (the turn fails and the
//     worker requeues — by the next attempt the central daemon will have
//     rotated the token on disk).
//
// When the variable is unset/empty/falsey, behavior is byte-for-byte identical
// to the historical self-refresh path; managed mode is purely additive.
const ManagedModeEnvVar = "SWARMOS_OAUTH_MANAGED"

var (
	managedModeOnce sync.Once
	managedModeVal  bool
)

// managedModeEnabled reports whether managed (no-self-refresh) mode is active.
// The env var is read exactly once per process and cached.
func managedModeEnabled() bool {
	managedModeOnce.Do(func() {
		managedModeVal = parseManagedEnv(os.Getenv(ManagedModeEnvVar))
	})
	return managedModeVal
}

// parseManagedEnv decodes a SWARMOS_OAUTH_MANAGED value to a bool.
func parseManagedEnv(v string) bool {
	switch v {
	case "1", "true", "TRUE", "True", "yes", "YES", "on", "ON":
		return true
	default:
		return false
	}
}

// setManagedModeForTest overrides the cached managed-mode flag. Test-only.
func setManagedModeForTest(enabled bool) {
	managedModeOnce.Do(func() {}) // consume the Once so production read is skipped
	managedModeVal = enabled
}

// isManaged reports whether this provider instance should behave as a managed,
// no-self-refresh OAuth consumer. Requires both the env flag and an OAuth
// credential; non-OAuth providers (x-api-key) are never managed.
func (p *Provider) isManaged() bool {
	return p.config.IsOAuth && managedModeEnabled()
}

// reloadManagedToken reloads the OAuth access token from oauth.json and, if it
// changed, rebuilds the HTTP client so the new bearer token is used. It is a
// no-op unless managed mode is active.
//
//   - When force is false, the file's mtime is checked first; the JSON is only
//     re-parsed (and the client only rebuilt) when oauth.json has changed since
//     the last successful reload. This makes the per-request call cheap.
//   - When force is true (used on a 401), the mtime cache is bypassed and the
//     file is always re-read — the central refresher may have rotated the token
//     in-place such that the access token changed even within the same second.
//
// reloadManagedToken NEVER refreshes and NEVER writes oauth.json.
func (p *Provider) reloadManagedToken(ctx context.Context, force bool) {
	if !p.isManaged() {
		return
	}

	configPath, err := GetOAuthConfigPath()
	if err != nil {
		p.logger.Warn(ctx, "anthropic.oauth.managed.path_error",
			observability.F("error", err.Error()),
		)
		return
	}

	p.managedMu.Lock()
	defer p.managedMu.Unlock()

	// Fast path: skip re-parse when the file is unchanged (mtime-cached).
	if !force {
		fi, statErr := os.Stat(configPath)
		if statErr == nil {
			mtime := fi.ModTime().UnixNano()
			if mtime == p.managedTokenMtime && p.managedAccessToken != "" {
				return // unchanged since last reload — nothing to do
			}
		}
		// stat error falls through to a full read attempt below.
	}

	token, err := GetStoredOAuthToken()
	if err != nil || token == nil || token.AccessToken == "" {
		// On a transient read error, keep using the currently-loaded token.
		if err != nil {
			p.logger.Warn(ctx, "anthropic.oauth.managed.reload_failed",
				observability.F("error", err.Error()),
				observability.F("force", force),
			)
		}
		return
	}

	// Record the mtime we just consumed so the next non-forced reload can skip.
	if fi, statErr := os.Stat(configPath); statErr == nil {
		p.managedTokenMtime = fi.ModTime().UnixNano()
	}

	// Only rebuild the client when the access token actually changed.
	if token.AccessToken == p.managedAccessToken && p.config.APIKey == token.AccessToken {
		return
	}

	if rebuildErr := p.rebuildClientWithToken(token.AccessToken); rebuildErr != nil {
		p.logger.Error(ctx, "anthropic.oauth.managed.rebuild_failed",
			observability.F("error", rebuildErr.Error()),
		)
		return
	}
	p.managedAccessToken = token.AccessToken

	p.logger.Info(ctx, "anthropic.oauth.managed.token_reloaded",
		observability.F("force", force),
		observability.F("new_expiry", token.Expiry),
	)
}
