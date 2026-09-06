package cloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_RefreshTokens_UpdatesTokenFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var tokenHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		atomic.AddInt32(&tokenHits, 1)

		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected grant_type: %q", r.PostForm.Get("grant_type"))
		}
		if r.PostForm.Get("client_id") != "client-123" {
			t.Fatalf("unexpected client_id: %q", r.PostForm.Get("client_id"))
		}
		if r.PostForm.Get("refresh_token") != "refresh-abc" {
			t.Fatalf("unexpected refresh_token: %q", r.PostForm.Get("refresh_token"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "new-access",
			"id_token":     "new-id",
			"token_type":   "Bearer",
			"scope":        "openid",
			"expires_in":   int64(3600),
		})
	}))
	t.Cleanup(server.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "old-access",
		IDToken:      "old-id",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
	}
	client := NewClient(cfg, tm, server.Client())

	got, err := client.RefreshTokens(context.Background())
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if got == nil || got.AccessToken != "new-access" || got.IDToken != "new-id" {
		t.Fatalf("unexpected refreshed tokens: %+v", got)
	}
	if got.RefreshToken != "refresh-abc" {
		t.Fatalf("expected refresh token preserved, got %q", got.RefreshToken)
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("expected 1 token request, got %d", atomic.LoadInt32(&tokenHits))
	}

	loaded, err := tm.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if loaded == nil || loaded.AccessToken != "new-access" || loaded.IDToken != "new-id" || loaded.RefreshToken != "refresh-abc" {
		t.Fatalf("expected token file to be updated, got %+v", loaded)
	}
}

func TestClient_RefreshTokens_PersistsRotatedRefreshToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if r.PostForm.Get("refresh_token") != "refresh-abc" {
			t.Fatalf("unexpected refresh_token: %q", r.PostForm.Get("refresh_token"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"id_token":      "new-id",
			"refresh_token": "refresh-rotated",
			"token_type":    "Bearer",
			"scope":         "openid",
			"expires_in":    int64(3600),
		})
	}))
	t.Cleanup(server.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "old-access",
		IDToken:      "old-id",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
	}
	client := NewClient(cfg, tm, server.Client())

	got, err := client.RefreshTokens(context.Background())
	if err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
	if got == nil || got.RefreshToken != "refresh-rotated" {
		t.Fatalf("expected rotated refresh token, got %+v", got)
	}

	loaded, err := tm.LoadTokens()
	if err != nil {
		t.Fatalf("LoadTokens: %v", err)
	}
	if loaded == nil || loaded.RefreshToken != "refresh-rotated" {
		t.Fatalf("expected persisted rotated refresh token, got %+v", loaded)
	}
}

func TestClient_GetValidTokens_RefreshesWhenExpired(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var tokenHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&tokenHits, 1)
		_ = r.ParseForm()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "refreshed-access",
			"id_token":     "refreshed-id",
			"token_type":   "Bearer",
			"scope":        "openid",
			"expires_in":   int64(3600),
		})
	}))
	t.Cleanup(server.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	// Expires in 30s: considered expired due to the default skew (60s).
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "old-access",
		IDToken:      "old-id",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(30 * time.Second).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
	}
	client := NewClient(cfg, tm, server.Client())

	got, err := client.GetValidTokens(context.Background())
	if err != nil {
		t.Fatalf("GetValidTokens: %v", err)
	}
	if got == nil || got.AccessToken != "refreshed-access" {
		t.Fatalf("expected refreshed tokens, got %+v", got)
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("expected 1 refresh call, got %d", atomic.LoadInt32(&tokenHits))
	}
}

func TestClient_RefreshTokens_SendsFormEncodedBody(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := ioReadAll(r)
		values, err := url.ParseQuery(string(bodyBytes))
		if err != nil {
			t.Fatalf("ParseQuery: %v", err)
		}
		if values.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected grant_type: %q", values.Get("grant_type"))
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ok",
			"id_token":     "ok",
			"token_type":   "Bearer",
			"scope":        "openid",
			"expires_in":   int64(3600),
		})
	}))
	t.Cleanup(server.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "old-access",
		IDToken:      "old-id",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
	}
	client := NewClient(cfg, tm, server.Client())

	if _, err := client.RefreshTokens(context.Background()); err != nil {
		t.Fatalf("RefreshTokens: %v", err)
	}
}

func ioReadAll(r *http.Request) ([]byte, error) {
	if r == nil || r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}
