package codex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The catalog cache now lives under the SwarmOS root (paths.In → SWARM_HOME).
	// Isolate it so tests never read or clobber the developer's real ~/.swarm.
	t.Setenv("SWARM_HOME", home)
	return home
}

func writeCacheFile(t *testing.T, home string, cache modelsCache) {
	t.Helper()
	_ = home // cache path derives from ModelsCachePath()/SWARM_HOME
	path, err := ModelsCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func testModels() []CatalogModel {
	return []CatalogModel{{Slug: "gpt-5.6-terra", DisplayName: "GPT-5.6-Terra", Visibility: "list", Priority: 2}}
}

func TestPersistAndLoadFreshModels(t *testing.T) {
	withTempHome(t)

	if err := PersistModels(testModels(), `W/"abc"`); err != nil {
		t.Fatalf("PersistModels: %v", err)
	}
	models, ok := LoadFreshModels(DefaultModelsCacheTTL)
	if !ok {
		t.Fatal("expected fresh cache hit")
	}
	if len(models) != 1 || models[0].Slug != "gpt-5.6-terra" {
		t.Errorf("unexpected models: %+v", models)
	}

	path, _ := ModelsCachePath()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache perms = %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadFreshModelsTTLExpiry(t *testing.T) {
	home := withTempHome(t)
	writeCacheFile(t, home, modelsCache{
		FetchedAt:     time.Now().Add(-10 * time.Minute),
		ClientVersion: codexClientVersion,
		Models:        testModels(),
	})
	if _, ok := LoadFreshModels(DefaultModelsCacheTTL); ok {
		t.Fatal("expected stale cache miss")
	}
}

func TestLoadFreshModelsClientVersionInvalidation(t *testing.T) {
	home := withTempHome(t)
	writeCacheFile(t, home, modelsCache{
		FetchedAt:     time.Now(),
		ClientVersion: "0.0.1",
		Models:        testModels(),
	})
	if _, ok := LoadFreshModels(DefaultModelsCacheTTL); ok {
		t.Fatal("expected client-version invalidation")
	}
}

func TestLoadFreshModelsCorruptFile(t *testing.T) {
	withTempHome(t)
	path, err := ModelsCachePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadFreshModels(DefaultModelsCacheTTL); ok {
		t.Fatal("expected corrupt cache miss")
	}
}

func TestFetchModelsCachedUsesFreshCache(t *testing.T) {
	withTempHome(t)
	if err := PersistModels(testModels(), ""); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Write([]byte(catalogFixture))
	}))
	defer server.Close()

	models, err := FetchModelsCached(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "tok",
	})
	if err != nil {
		t.Fatalf("FetchModelsCached: %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("network hit despite fresh cache (%d calls)", calls.Load())
	}
	if len(models) != 1 || models[0].Slug != "gpt-5.6-terra" {
		t.Errorf("unexpected models: %+v", models)
	}
}

func TestFetchModelsCachedFetchesAndPersists(t *testing.T) {
	withTempHome(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `W/"v2"`)
		w.Write([]byte(catalogFixture))
	}))
	defer server.Close()

	models, err := FetchModelsCached(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "tok",
	})
	if err != nil {
		t.Fatalf("FetchModelsCached: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("len(models) = %d, want 3", len(models))
	}

	cached, ok := LoadFreshModels(DefaultModelsCacheTTL)
	if !ok {
		t.Fatal("fetch should have persisted the cache")
	}
	if len(cached) != 3 {
		t.Errorf("cached len = %d, want 3", len(cached))
	}
}

func TestFetchModelsCachedStaleFallbackOnNetworkError(t *testing.T) {
	home := withTempHome(t)
	writeCacheFile(t, home, modelsCache{
		FetchedAt:     time.Now().Add(-24 * time.Hour),
		ClientVersion: codexClientVersion,
		Models:        testModels(),
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	models, err := FetchModelsCached(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "tok",
	})
	if err != nil {
		t.Fatalf("expected stale fallback, got error: %v", err)
	}
	if len(models) != 1 || models[0].Slug != "gpt-5.6-terra" {
		t.Errorf("unexpected fallback models: %+v", models)
	}
}

func TestFetchModelsCachedErrorWithoutFallback(t *testing.T) {
	withTempHome(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, err := FetchModelsCached(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "tok",
	}); err == nil {
		t.Fatal("expected error when no cache fallback exists")
	}
}
