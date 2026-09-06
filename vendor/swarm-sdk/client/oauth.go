package client

import (
	"fmt"

	provanthropic "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
)

// RefreshAnthropicOAuthIfNeeded ensures the stored Anthropic / Claude Code
// OAuth token (~/.swarm/config/oauth/anthropic.json) is valid, refreshing it via the stored
// refresh token when it has expired (or is within the 5-minute expiry buffer)
// and writing the rotated token back to disk. It is a safe no-op when no OAuth
// token is configured.
//
// Call this before constructing a client for an OAuth (ClaudeCode) provider so
// requests don't fail on a stale access token — the SDK's request-path
// auto-refresh does not reliably cover streaming turns.
//
// Returns whether a refresh was performed.
func RefreshAnthropicOAuthIfNeeded() (bool, error) {
	cfg, err := provanthropic.LoadOAuthConfig()
	if err != nil {
		// No / unreadable OAuth config — nothing to refresh, not an error here.
		return false, nil
	}
	if cfg == nil || cfg.Token == nil || cfg.Token.AccessToken == "" {
		return false, nil
	}
	if !provanthropic.IsTokenExpired(cfg.Token) {
		return false, nil
	}
	if cfg.Token.RefreshToken == "" {
		return false, fmt.Errorf("anthropic OAuth token expired and no refresh token available")
	}
	if _, err := provanthropic.RefreshAndStoreToken(); err != nil {
		return false, fmt.Errorf("refresh anthropic OAuth token: %w", err)
	}
	return true, nil
}
