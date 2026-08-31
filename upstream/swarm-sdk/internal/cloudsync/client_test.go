package cloudsync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/cloud"
)

func TestAPIClient_DoRequest_AttachesBearerToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/keys" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&hits, 1)

		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("expected Authorization Bearer access-1, got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"keys":   []any{},
		})
	}))
	t.Cleanup(server.Close)

	tm, err := cloud.NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&cloud.TokenSet{
		AccessToken:  "access-1",
		IDToken:      "id-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &cloud.CloudConfig{
		APIBaseURL: server.URL,
	}

	client := NewAPIClient(cfg, tm, server.Client())
	if _, err := client.GetKeys(context.Background()); err != nil {
		t.Fatalf("GetKeys: %v", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 request, got %d", atomic.LoadInt32(&hits))
	}
}

func TestAPIClient_DoRequest_401RefreshesAndRetries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var keysHits int32
	var tokenHits int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sync/keys":
			atomic.AddInt32(&keysHits, 1)
			auth := r.Header.Get("Authorization")
			if atomic.LoadInt32(&keysHits) == 1 {
				if auth != "Bearer old-access" {
					t.Fatalf("expected old token first, got %q", auth)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"status":"error","message":"unauthorized"}`))
				return
			}
			if auth != "Bearer new-access" {
				t.Fatalf("expected new token after refresh, got %q", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status": "ok",
				"keys":   []any{},
			})
		case "/oauth2/token":
			atomic.AddInt32(&tokenHits, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "new-access",
				"id_token":     "new-id",
				"token_type":   "Bearer",
				"scope":        "openid",
				"expires_in":   int64(3600),
			})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	tm, err := cloud.NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&cloud.TokenSet{
		AccessToken:  "old-access",
		IDToken:      "old-id",
		RefreshToken: "refresh-abc",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &cloud.CloudConfig{
		APIBaseURL:     server.URL,
		APIFallbackURL: "",
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
	}

	client := NewAPIClient(cfg, tm, server.Client())
	if _, err := client.GetKeys(context.Background()); err != nil {
		t.Fatalf("GetKeys: %v", err)
	}
	if atomic.LoadInt32(&keysHits) != 2 {
		t.Fatalf("expected 2 /sync/keys calls, got %d", atomic.LoadInt32(&keysHits))
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("expected 1 /oauth2/token call, got %d", atomic.LoadInt32(&tokenHits))
	}
}

func TestAPIClient_DoRequest_UsesFallbackOn5xx(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var primaryHits int32
	var fallbackHits int32

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/keys" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&primaryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"status":"error","message":"server_error"}`))
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sync/keys" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&fallbackHits, 1)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"keys":   []any{},
		})
	}))
	t.Cleanup(fallback.Close)

	tm, err := cloud.NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&cloud.TokenSet{
		AccessToken:  "access-1",
		IDToken:      "id-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &cloud.CloudConfig{
		APIBaseURL:     primary.URL,
		APIFallbackURL: fallback.URL,
	}

	// Use a plain http client that can talk to both servers.
	httpClient := &http.Client{}
	client := NewAPIClient(cfg, tm, httpClient)

	if _, err := client.GetKeys(context.Background()); err != nil {
		t.Fatalf("GetKeys: %v", err)
	}
	if atomic.LoadInt32(&primaryHits) != 1 {
		t.Fatalf("expected 1 primary call, got %d", atomic.LoadInt32(&primaryHits))
	}
	if atomic.LoadInt32(&fallbackHits) != 1 {
		t.Fatalf("expected 1 fallback call, got %d", atomic.LoadInt32(&fallbackHits))
	}
}
