package cloud

import (
	"testing"
	"time"
)

func TestTokenManager_SaveLoadClear_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	expiresAt := time.Now().Add(1 * time.Hour).Unix()
	orig := &TokenSet{
		AccessToken:  "access",
		IDToken:      "id",
		RefreshToken: "refresh",
		TokenType:    "Bearer",
		Scope:        "openid email",
		ExpiresAt:    expiresAt,
	}

	if err := tm.SaveTokens(orig); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	loaded, err := tm.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if loaded == nil {
		t.Fatalf("expected tokens after SaveTokens")
	}
	if loaded.AccessToken != orig.AccessToken ||
		loaded.IDToken != orig.IDToken ||
		loaded.RefreshToken != orig.RefreshToken ||
		loaded.TokenType != orig.TokenType ||
		loaded.Scope != orig.Scope ||
		loaded.ExpiresAt != orig.ExpiresAt {
		t.Fatalf("loaded tokens did not match saved tokens")
	}

	if err := tm.ClearTokens(); err != nil {
		t.Fatalf("ClearTokens: %v", err)
	}

	cleared, err := tm.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens after ClearTokens: %v", err)
	}
	if cleared != nil {
		t.Fatalf("expected nil tokens after ClearTokens")
	}
}

func TestTokenSet_IsExpiredWithSkew(t *testing.T) {
	if (*TokenSet)(nil).IsExpiredWithSkew(0) != true {
		t.Fatalf("nil tokens should be expired")
	}

	zero := &TokenSet{ExpiresAt: 0}
	if zero.IsExpiredWithSkew(0) {
		t.Fatalf("ExpiresAt=0 should not be considered expired")
	}

	now := time.Now()

	future := &TokenSet{ExpiresAt: now.Add(2 * time.Minute).Unix()}
	if future.IsExpiredWithSkew(60 * time.Second) {
		t.Fatalf("token expiring in 2m should not be expired with 60s skew")
	}

	soon := &TokenSet{ExpiresAt: now.Add(30 * time.Second).Unix()}
	if !soon.IsExpiredWithSkew(60 * time.Second) {
		t.Fatalf("token expiring in 30s should be expired with 60s skew")
	}
	if soon.IsExpiredWithSkew(0) {
		t.Fatalf("token expiring in 30s should not be expired with 0 skew")
	}
}
