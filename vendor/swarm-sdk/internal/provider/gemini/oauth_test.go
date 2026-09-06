package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newTestManager builds an OAuthManager with a temp token file so
// SaveTokens/loadTokens hit disk safely. tokenURL can be set by the caller to
// redirect the OAuth token endpoint at a test server.
func newTestManager(t *testing.T) *OAuthManager {
	t.Helper()
	dir := t.TempDir()
	return &OAuthManager{
		clientID:     "test-client",
		clientSecret: "test-secret",
		scopes:       DefaultScopes,
		tokenPath:    filepath.Join(dir, "oauth_credentials.json"),
		httpClient:   &http.Client{Timeout: 5 * time.Second},
	}
}

// TestTokenSourceReturnsCachedValidToken verifies that TokenSource().Token()
// returns the in-memory token without any network call when it is still valid.
func TestTokenSourceReturnsCachedValidToken(t *testing.T) {
	m := newTestManager(t)
	m.tokens = &OAuthTokens{
		AccessToken: "valid-access",
		ExpiresAt:   time.Now().Unix() + 3600, // 1h out, not expired
	}
	ts := m.TokenSource(context.Background())
	got, err := ts.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "valid-access" {
		t.Fatalf("got %q, want valid-access", got)
	}
}

// TestTokenSourceRefreshesAndPersists verifies the savingTokenSource contract:
// an expired token triggers a refresh, the new token is returned AND persisted.
func TestTokenSourceRefreshesAndPersists(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			t.Errorf("expected refresh_token grant, got %q", r.FormValue("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-access",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()

	m := newTestManager(t)
	m.tokenURL = srv.URL
	m.tokens = &OAuthTokens{
		AccessToken:  "stale-access",
		RefreshToken: "refresh-123",
		ExpiresAt:    time.Now().Unix() - 10, // already expired
	}

	ts := m.TokenSource(context.Background())
	got, err := ts.Token()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "fresh-access" {
		t.Fatalf("got %q, want fresh-access", got)
	}
	if hits != 1 {
		t.Fatalf("expected 1 refresh call, got %d", hits)
	}
	// Refresh token must be preserved across refresh (response omitted it).
	if m.tokens.RefreshToken != "refresh-123" {
		t.Fatalf("refresh token not preserved: %q", m.tokens.RefreshToken)
	}
	// New token must be persisted to disk.
	data, err := os.ReadFile(m.tokenPath)
	if err != nil {
		t.Fatalf("token file not written: %v", err)
	}
	var persisted OAuthTokens
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted token unreadable: %v", err)
	}
	if persisted.AccessToken != "fresh-access" {
		t.Fatalf("persisted access token = %q, want fresh-access", persisted.AccessToken)
	}
}

// TestRefreshInvalidGrantSurfacesTypedError verifies an invalid_grant response
// is surfaced as ErrRefreshTokenInvalid (recognized by IsReauthRequired) and
// does NOT fall through to an interactive auth attempt.
func TestRefreshInvalidGrantSurfacesTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
	}))
	defer srv.Close()

	m := newTestManager(t)
	m.tokenURL = srv.URL
	m.noBrowser = true // ensure no browser attempt even if logic changed
	m.tokens = &OAuthTokens{
		AccessToken:  "stale",
		RefreshToken: "revoked-token",
		ExpiresAt:    time.Now().Unix() - 10,
	}

	_, err := m.AccessToken(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrRefreshTokenInvalid) {
		t.Fatalf("error is not ErrRefreshTokenInvalid: %v", err)
	}
	if !IsReauthRequired(err) {
		t.Fatalf("IsReauthRequired should be true for: %v", err)
	}
}

// TestLoadTokensBackfillsExpiresAt verifies loadTokens computes ExpiresAt when
// only ExpiresIn was persisted, so IsExpired() doesn't treat it as expired.
func TestLoadTokensBackfillsExpiresAt(t *testing.T) {
	m := newTestManager(t)
	// Persist a token with only expires_in (no expires_at).
	raw := `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`
	if err := os.WriteFile(m.tokenPath, []byte(raw), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tok, err := m.loadTokens()
	if err != nil {
		t.Fatalf("loadTokens: %v", err)
	}
	if tok.ExpiresAt <= 0 {
		t.Fatalf("ExpiresAt not backfilled: %d", tok.ExpiresAt)
	}
	if tok.IsExpired() {
		t.Fatalf("token with 1h expires_in should not be expired after backfill")
	}
}

// TestIsInvalidGrant covers the detector's positive and negative cases.
func TestIsInvalidGrant(t *testing.T) {
	if !isInvalidGrant(http.StatusBadRequest, []byte(`{"error":"invalid_grant"}`)) {
		t.Error("should detect invalid_grant JSON")
	}
	if !isInvalidGrant(http.StatusBadRequest, []byte(`garbled invalid_grant text`)) {
		t.Error("should detect invalid_grant via substring fallback")
	}
	if isInvalidGrant(http.StatusOK, []byte(`{"error":"invalid_grant"}`)) {
		t.Error("should not flag non-400 status")
	}
	if isInvalidGrant(http.StatusBadRequest, []byte(`{"error":"invalid_request"}`)) {
		t.Error("should not flag other error codes")
	}
}
