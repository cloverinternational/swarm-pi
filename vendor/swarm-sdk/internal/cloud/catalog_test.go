package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_FetchCatalog_UsesFreshCacheWithoutNetwork(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}

	cacheManager, err := NewCatalogCacheManager()
	if err != nil {
		t.Fatalf("NewCatalogCacheManager: %v", err)
	}
	cache := &CatalogCache{
		ETag:      "etag",
		UpdatedAt: time.Now().Unix(),
		Body:      json.RawMessage(`{"cached":true}`),
	}
	if err := cacheManager.SaveCache(cache); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}
	loadedCache, err := cacheManager.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if loadedCache == nil {
		t.Fatalf("expected cache after SaveCache")
	}

	cfg := &CloudConfig{APIBaseURL: "http://example.invalid"}
	client := NewClient(cfg, tm, nil)

	got, err := client.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if got == nil || !got.FromCache {
		t.Fatalf("expected cache hit, got %+v", got)
	}
	if got.Bytes != len(loadedCache.Body) {
		t.Fatalf("expected bytes %d, got %d", len(loadedCache.Body), got.Bytes)
	}
}

func TestClient_FetchCatalog_ETag304UpdatesCacheTimestamp(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var catalogHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&catalogHits, 1)

		if got := r.Header.Get("If-None-Match"); got != "etag-abc" {
			t.Fatalf("expected If-None-Match etag-abc, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("expected Authorization Bearer access-1, got %q", got)
		}

		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(server.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "access-1",
		IDToken:      "id-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cacheManager, err := NewCatalogCacheManager()
	if err != nil {
		t.Fatalf("NewCatalogCacheManager: %v", err)
	}
	oldUpdatedAt := time.Now().Add(-2 * defaultCatalogTTL).Unix()
	cache := &CatalogCache{
		ETag:      "etag-abc",
		UpdatedAt: oldUpdatedAt,
		Body:      json.RawMessage(`{"cached":true}`),
	}
	if err := cacheManager.SaveCache(cache); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	cfg := &CloudConfig{APIBaseURL: server.URL}
	client := NewClient(cfg, tm, server.Client())

	got, err := client.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if got == nil || !got.FromCache {
		t.Fatalf("expected FromCache=true for 304, got %+v", got)
	}
	if atomic.LoadInt32(&catalogHits) != 1 {
		t.Fatalf("expected 1 /catalog request, got %d", atomic.LoadInt32(&catalogHits))
	}

	updated, err := cacheManager.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if updated == nil {
		t.Fatalf("expected cache to exist")
	}
	if updated.UpdatedAt <= oldUpdatedAt {
		t.Fatalf("expected UpdatedAt to be bumped; was %d now %d", oldUpdatedAt, updated.UpdatedAt)
	}
	var gotBody map[string]any
	if err := json.Unmarshal(updated.Body, &gotBody); err != nil {
		t.Fatalf("unmarshal cache body: %v", err)
	}
	if v, ok := gotBody["cached"]; !ok || v != true {
		t.Fatalf("expected cached body to remain semantically identical, got %v", gotBody)
	}
}

func TestClient_FetchCatalog_401RefreshesAndRetries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var catalogHits int32
	var tokenHits int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog":
			atomic.AddInt32(&catalogHits, 1)
			auth := r.Header.Get("Authorization")
			if atomic.LoadInt32(&catalogHits) == 1 {
				if auth != "Bearer old-access" {
					t.Fatalf("expected first request token old-access, got %q", auth)
				}
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("unauthorized"))
				return
			}
			if auth != "Bearer new-access" {
				t.Fatalf("expected second request token new-access, got %q", auth)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", "etag-new")
			_, _ = w.Write([]byte(`{"ok":true}`))
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
		APIBaseURL:     server.URL,
		HostedUIURL:    server.URL,
		PublicClientID: "client-123",
		APIFallbackURL: "",
		AuthIssuerURL:  "issuer",
		Region:         "region",
		DeviceID:       "device",
	}
	client := NewClient(cfg, tm, server.Client())

	got, err := client.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if got == nil || got.FromCache {
		t.Fatalf("expected FromCache=false for network fetch, got %+v", got)
	}
	if atomic.LoadInt32(&catalogHits) != 2 {
		t.Fatalf("expected 2 /catalog requests, got %d", atomic.LoadInt32(&catalogHits))
	}
	if atomic.LoadInt32(&tokenHits) != 1 {
		t.Fatalf("expected 1 /oauth2/token request, got %d", atomic.LoadInt32(&tokenHits))
	}
}

func TestClient_FetchCatalog_UsesFallbackOn5xx(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	var primaryHits int32
	var fallbackHits int32

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&primaryHits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	t.Cleanup(primary.Close)

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/catalog" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		atomic.AddInt32(&fallbackHits, 1)

		if got := r.Header.Get("Authorization"); got != "Bearer access-1" {
			t.Fatalf("expected Authorization Bearer access-1, got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", "etag-fallback")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(fallback.Close)

	tm, err := NewTokenManager()
	if err != nil {
		t.Fatalf("NewTokenManager: %v", err)
	}
	if err := tm.SaveTokens(&TokenSet{
		AccessToken:  "access-1",
		IDToken:      "id-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("SaveTokens: %v", err)
	}

	cfg := &CloudConfig{
		APIBaseURL:     primary.URL,
		APIFallbackURL: fallback.URL,
	}
	client := NewClient(cfg, tm, primary.Client())

	got, err := client.FetchCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchCatalog: %v", err)
	}
	if got == nil || got.FromCache {
		t.Fatalf("expected network fetch, got %+v", got)
	}
	if got.SourceURL != fallback.URL {
		t.Fatalf("expected SourceURL %q, got %q", fallback.URL, got.SourceURL)
	}
	if atomic.LoadInt32(&primaryHits) != 1 {
		t.Fatalf("expected 1 primary attempt, got %d", atomic.LoadInt32(&primaryHits))
	}
	if atomic.LoadInt32(&fallbackHits) != 1 {
		t.Fatalf("expected 1 fallback attempt, got %d", atomic.LoadInt32(&fallbackHits))
	}
}
