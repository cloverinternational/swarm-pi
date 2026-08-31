package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsEndpoint(t *testing.T) {
	cases := map[string]string{
		"https://api.groq.com/openai/v1":  "https://api.groq.com/openai/v1/models",
		"https://api.groq.com/openai/v1/": "https://api.groq.com/openai/v1/models",
		"https://api.openai.com":          "https://api.openai.com/v1/models",
		"https://x.example/v1/models":     "https://x.example/v1/models",
	}
	for in, want := range cases {
		if got := modelsEndpoint(in); got != want {
			t.Errorf("modelsEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchModelsForProvider_OpenAICompatible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization header = %q, want %q", got, "Bearer secret")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"model-b","context_length":8192},
			{"id":"model-a","display_name":"Model A","context_window":128000},
			{"id":"model-b","context_length":8192}
		]}`))
	}))
	defer srv.Close()

	models, err := FetchModelsForProvider(context.Background(), "openai-compatible", srv.URL+"/v1", "secret")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Deduped (model-b twice) and sorted by ID => [model-a, model-b].
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2: %+v", len(models), models)
	}
	if models[0].ID != "model-a" || models[1].ID != "model-b" {
		t.Errorf("models not sorted/deduped: %+v", models)
	}
	if models[0].DisplayName != "Model A" || models[0].ContextWindow != 128000 {
		t.Errorf("model-a metadata wrong: %+v", models[0])
	}
	// display name falls back to ID when absent.
	if models[1].DisplayName != "model-b" {
		t.Errorf("model-b display name = %q, want fallback to id", models[1].DisplayName)
	}
}

func TestFetchModelsForProvider_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := FetchModelsForProvider(context.Background(), "openai-compatible", srv.URL+"/v1", "bad")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestLookupKnownProvider(t *testing.T) {
	if _, ok := LookupKnownProvider("groq"); !ok {
		t.Error("expected to find preset 'groq' by key")
	}
	if _, ok := LookupKnownProvider("xAI (Grok)"); !ok {
		t.Error("expected to find preset by display name")
	}
	if _, ok := LookupKnownProvider("nope"); ok {
		t.Error("did not expect to find bogus preset")
	}
}
