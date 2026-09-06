package cloud

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func makeJWT(t *testing.T, payload map[string]any) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	body := base64.RawURLEncoding.EncodeToString(rawPayload)
	return header + "." + body + "."
}

func TestResolveIdentity_SignedIn_AdminFromIDTokenGroups(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	idToken := makeJWT(t, map[string]any{
		"email":          "admin@example.com",
		"cognito:groups": []string{"Administrators", "Other"},
	})

	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "access",
		IDToken:      idToken,
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	identity := ResolveIdentity(tm)
	if identity.Status != AuthStatusSignedIn {
		t.Fatalf("expected status %q, got %q", AuthStatusSignedIn, identity.Status)
	}
	if identity.Email != "admin@example.com" {
		t.Fatalf("expected email to be parsed, got %q", identity.Email)
	}
	if !identity.IsAdmin {
		t.Fatalf("expected IsAdmin=true")
	}
	if identity.Plan != "Administrator" {
		t.Fatalf("expected plan Administrator, got %q", identity.Plan)
	}
}

func TestResolveIdentity_AdminFromAccessTokenGroupsFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	idToken := makeJWT(t, map[string]any{
		"email": "fallback@example.com",
	})
	accessToken := makeJWT(t, map[string]any{
		"cognito:groups": []any{"Administrators"},
	})

	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  accessToken,
		IDToken:      idToken,
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	identity := ResolveIdentity(tm)
	if identity.Status != AuthStatusSignedIn {
		t.Fatalf("expected status %q, got %q", AuthStatusSignedIn, identity.Status)
	}
	if identity.Email != "fallback@example.com" {
		t.Fatalf("expected email to be parsed, got %q", identity.Email)
	}
	if !identity.IsAdmin {
		t.Fatalf("expected IsAdmin=true from access token groups fallback")
	}
}

func TestResolveIdentity_NotSignedInWhenNoTokens(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	identity := ResolveIdentity(tm)
	if identity.Status != AuthStatusNotSignedIn {
		t.Fatalf("expected status %q, got %q", AuthStatusNotSignedIn, identity.Status)
	}
}

func TestResolveIdentity_ExpiredWhenTokenExpired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "access",
		IDToken:      "id",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-2 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	identity := ResolveIdentity(tm)
	if identity.Status != AuthStatusExpired {
		t.Fatalf("expected status %q, got %q", AuthStatusExpired, identity.Status)
	}
}
