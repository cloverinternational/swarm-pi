package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

// TestFetchModelsCachedAgainstRealCatalog replays the verbatim bundled
// catalog from the Codex CLI (codex-rs/models-manager/models.json, vendored
// under testdata) through the full fetch→parse→persist→cache-hit pipeline.
// This pins swarm's parser to the exact production payload shape, not a
// hand-trimmed fixture.
func TestFetchModelsCachedAgainstRealCatalog(t *testing.T) {
	catalog, err := os.ReadFile("testdata/codex_bundled_models.json")
	if err != nil {
		t.Fatalf("read real catalog fixture: %v", err)
	}

	withTempHome(t)

	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.URL.Query().Get("client_version"); got != codexClientVersion {
			t.Errorf("client_version = %q, want %q", got, codexClientVersion)
		}
		w.Header().Set("ETag", `W/"real-catalog"`)
		w.Write(catalog)
	}))
	defer server.Close()

	models, err := FetchModelsCached(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "tok",
		AccountID:   "acct",
	})
	if err != nil {
		t.Fatalf("FetchModelsCached against real catalog: %v", err)
	}

	if len(models) != 8 {
		t.Fatalf("parsed %d models from real catalog, want 8", len(models))
	}

	bySlug := map[string]CatalogModel{}
	for _, m := range models {
		bySlug[m.Slug] = m
	}

	sol, ok := bySlug["gpt-5.6-sol"]
	if !ok {
		t.Fatal("gpt-5.6-sol missing from parsed catalog")
	}
	if sol.ResolvedContextWindow() != 372000 {
		t.Errorf("gpt-5.6-sol context = %d, want 372000", sol.ResolvedContextWindow())
	}
	if !sol.UseResponsesLite {
		t.Error("gpt-5.6-sol must be responses-lite")
	}
	if sol.DefaultReasoningLevel != "low" {
		t.Errorf("gpt-5.6-sol default effort = %q, want low", sol.DefaultReasoningLevel)
	}
	if len(sol.SupportedReasoningLevels) != 6 {
		t.Errorf("gpt-5.6-sol has %d efforts, want 6 (low..ultra)", len(sol.SupportedReasoningLevels))
	}
	if sol.BaseInstructions == "" {
		t.Error("gpt-5.6-sol base_instructions must be non-empty")
	}
	if !sol.Listed() {
		t.Error("gpt-5.6-sol must be listed")
	}

	if v55, ok := bySlug["gpt-5.5"]; !ok || v55.UseResponsesLite {
		t.Errorf("gpt-5.5 parse wrong: ok=%v lite=%v", ok, v55.UseResponsesLite)
	}
	if hidden, ok := bySlug["gpt-5.4"]; !ok || hidden.Listed() {
		t.Errorf("gpt-5.4 must parse as hidden: ok=%v", ok)
	}
	if upgraded := bySlug["gpt-5.4"].Upgrade; upgraded == nil || upgraded.Model != "gpt-5.6-terra" {
		t.Errorf("gpt-5.4 upgrade = %+v, want →gpt-5.6-terra", bySlug["gpt-5.4"].Upgrade)
	}

	// Priority ordering: sol(1) first, codex-auto-review(43) last.
	if models[0].Slug != "gpt-5.6-sol" || models[len(models)-1].Slug != "codex-auto-review" {
		t.Errorf("priority order wrong: first=%s last=%s", models[0].Slug, models[len(models)-1].Slug)
	}

	// Wire options from the persisted cache.
	opts := ModelWireOptionsFromCache()
	if !opts["gpt-5.6-terra"].ResponsesLite || opts["gpt-5.5"].ResponsesLite {
		t.Errorf("lite flags from cache wrong: %v", opts)
	}
	if !opts["gpt-5.5"].ParallelToolCalls {
		t.Error("gpt-5.5 must carry supports_parallel_tool_calls from the catalog")
	}
	if !opts["gpt-5.6-sol"].SupportsMaxEffort || opts["gpt-5.5"].SupportsMaxEffort {
		t.Errorf("max-effort flags from cache wrong: sol=%v 5.5=%v",
			opts["gpt-5.6-sol"].SupportsMaxEffort, opts["gpt-5.5"].SupportsMaxEffort)
	}

	// Second fetch must be served from the fresh cache without a network hit.
	if _, err := FetchModelsCached(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "tok",
	}); err != nil {
		t.Fatalf("cached refetch: %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("network calls = %d, want 1 (second fetch must hit cache)", calls.Load())
	}
}
