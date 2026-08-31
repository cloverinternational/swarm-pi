package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const catalogFixture = `{
  "models": [
    {
      "slug": "gpt-5.5",
      "display_name": "GPT-5.5",
      "description": "Flagship",
      "default_reasoning_level": "medium",
      "supported_reasoning_levels": [
        {"effort": "low", "description": "Fast"},
        {"effort": "medium", "description": "Balanced"},
        {"effort": "high", "description": "Deep"},
        {"effort": "xhigh", "description": "Deeper"}
      ],
      "visibility": "list",
      "supported_in_api": true,
      "priority": 7,
      "context_window": 272000,
      "max_context_window": 272000,
      "use_responses_lite": false,
      "minimal_client_version": "0.124.0",
      "unknown_future_field": {"nested": true}
    },
    {
      "slug": "gpt-5.6-sol",
      "display_name": "GPT-5.6-Sol",
      "default_reasoning_level": "low",
      "supported_reasoning_levels": [
        {"effort": "low"},
        {"effort": "medium"},
        {"effort": "high"},
        {"effort": "xhigh"},
        {"effort": "max"},
        {"effort": "ultra"}
      ],
      "visibility": "list",
      "supported_in_api": true,
      "priority": 1,
      "context_window": 372000,
      "max_context_window": 372000,
      "use_responses_lite": true,
      "minimal_client_version": "0.144.0"
    },
    {
      "slug": "codex-auto-review",
      "display_name": "Codex Auto Review",
      "visibility": "hide",
      "priority": 43,
      "max_context_window": 1000000,
      "upgrade": {"model": "gpt-5.6-terra", "migration_markdown": "moved"}
    }
  ]
}`

func TestListModels(t *testing.T) {
	var gotPath, gotQuery string
	var gotHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query().Get("client_version")
		gotHeaders = r.Header.Clone()
		w.Header().Set("ETag", `W/"catalog-v1"`)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(catalogFixture))
	}))
	defer server.Close()

	models, etag, err := ListModels(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "test-token",
		AccountID:   "acct-123",
	})
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}

	if gotPath != "/models" {
		t.Errorf("path = %q, want /models", gotPath)
	}
	if gotQuery != codexClientVersion {
		t.Errorf("client_version = %q, want %q", gotQuery, codexClientVersion)
	}
	if got := gotHeaders.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q", got)
	}
	if got := gotHeaders.Get("ChatGPT-Account-ID"); got != "acct-123" {
		t.Errorf("ChatGPT-Account-ID = %q", got)
	}
	if got := gotHeaders.Get("originator"); got != "swarmos" {
		t.Errorf("originator = %q", got)
	}
	if got := gotHeaders.Get("version"); got != codexClientVersion {
		t.Errorf("version = %q", got)
	}
	if etag != `W/"catalog-v1"` {
		t.Errorf("etag = %q", etag)
	}

	if len(models) != 3 {
		t.Fatalf("len(models) = %d, want 3", len(models))
	}
	// Sorted by ascending priority.
	if models[0].Slug != "gpt-5.6-sol" || models[1].Slug != "gpt-5.5" || models[2].Slug != "codex-auto-review" {
		t.Errorf("priority order wrong: %s, %s, %s", models[0].Slug, models[1].Slug, models[2].Slug)
	}

	sol := models[0]
	if !sol.UseResponsesLite {
		t.Error("gpt-5.6-sol should have use_responses_lite")
	}
	if sol.ResolvedContextWindow() != 372000 {
		t.Errorf("gpt-5.6-sol context = %d, want 372000", sol.ResolvedContextWindow())
	}
	if len(sol.SupportedReasoningLevels) != 6 || sol.SupportedReasoningLevels[4].Effort != "max" {
		t.Errorf("gpt-5.6-sol reasoning levels wrong: %+v", sol.SupportedReasoningLevels)
	}
	if !sol.Listed() {
		t.Error("gpt-5.6-sol should be listed")
	}

	if models[2].Listed() {
		t.Error("codex-auto-review must not be listed")
	}
	if models[2].ResolvedContextWindow() != 1000000 {
		t.Errorf("hidden model max_context_window fallback = %d", models[2].ResolvedContextWindow())
	}
	if models[2].Upgrade == nil || models[2].Upgrade.Model != "gpt-5.6-terra" {
		t.Errorf("upgrade parse wrong: %+v", models[2].Upgrade)
	}
}

func TestListModelsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	_, _, err := ListModels(context.Background(), ListModelsOptions{
		BaseURL:     server.URL,
		AccessToken: "bad",
	})
	if err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestListModelsRequiresToken(t *testing.T) {
	if _, _, err := ListModels(context.Background(), ListModelsOptions{}); err == nil {
		t.Fatal("expected error without access token")
	}
}
