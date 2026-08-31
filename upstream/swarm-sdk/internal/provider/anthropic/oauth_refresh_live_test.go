package anthropic

import (
	"os"
	"testing"
)

// TestRefreshAccessToken_Live exercises the real Anthropic OAuth refresh
// endpoint using the refresh token stored in the local OAuth config. It is
// skipped unless SWARM_LIVE_OAUTH_REFRESH=1 so it never runs in CI.
//
// Purpose: prove the production refresh path (which omits `scope`) succeeds
// against the live endpoint, fixing the HTTP 400 invalid_scope failure.
func TestRefreshAccessToken_Live(t *testing.T) {
	if os.Getenv("SWARM_LIVE_OAUTH_REFRESH") != "1" {
		t.Skip("set SWARM_LIVE_OAUTH_REFRESH=1 to run the live OAuth refresh test")
	}

	cfg, err := LoadOAuthConfig()
	if err != nil {
		t.Fatalf("LoadOAuthConfig: %v", err)
	}
	if cfg.Token == nil || cfg.Token.RefreshToken == "" {
		t.Fatalf("no stored refresh token to test with")
	}

	tok, err := RefreshAccessToken(cfg.Token.RefreshToken)
	if err != nil {
		t.Fatalf("RefreshAccessToken failed (the bug): %v", err)
	}
	if tok.AccessToken == "" {
		t.Fatalf("refresh returned empty access token")
	}
	t.Logf("refresh OK: token_type=%s expires_in=%d scope=%q new_refresh=%v",
		tok.TokenType, tok.ExpiresIn, tok.Scope, tok.RefreshToken != cfg.Token.RefreshToken)

	// Persist the rotated token so the running session keeps working.
	cfg.Token = tok
	if err := SaveOAuthConfig(cfg); err != nil {
		t.Fatalf("SaveOAuthConfig: %v", err)
	}
}
