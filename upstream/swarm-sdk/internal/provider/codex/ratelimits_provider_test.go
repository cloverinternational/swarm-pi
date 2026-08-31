package codex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

func newTestCodexProvider(t *testing.T, url string) *Provider {
	t.Helper()
	p, err := New(Config{AccessToken: "tok", AccountID: "acct_test", BaseURL: url, Timeout: 5})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestStream429ReturnsTypedRateLimitError: a codex 429 must surface as a
// synchronous, sdkerr-typed rate-limit error from Stream — not as an
// in-channel chunk the orchestrator can't see.
func TestStream429ReturnsTypedRateLimitError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("x-codex-plan-type", "pro")
		h.Set("x-codex-primary-used-percent", "100")
		h.Set("x-codex-primary-window-minutes", "10080")
		h.Set("x-codex-primary-reset-after-seconds", "302400")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"detail":"usage_limit_reached"}`))
	}))
	defer srv.Close()

	p := newTestCodexProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), provider.ChatRequest{
		Model:    "gpt-5.5",
		Messages: nil,
	})
	if err == nil {
		t.Fatal("expected error from Stream on 429")
	}
	if !sdkerr.IsRateLimitError(err) {
		t.Fatalf("429 error not typed as rate limit: %v", err)
	}
	// Retry-after must be capped (weekly reset is days away; an uncapped
	// value would stall the orchestrator's retry sleep).
	if ra := sdkerr.GetRetryAfter(err); ra <= 0 || ra > 60*time.Second {
		t.Errorf("retry-after = %v, want (0, 60s]", ra)
	}
	// The 429's headers must still have been captured as a usage snapshot.
	snap := p.LatestRateLimits()
	if snap == nil || snap.Primary == nil || snap.Primary.UsedPercent != 100 {
		t.Errorf("snapshot not captured from 429 response: %+v", snap)
	}
	// And persisted per account.
	all, err := LoadRateLimitSnapshots()
	if err != nil || all["acct_test"].Primary == nil {
		t.Errorf("snapshot not persisted: %+v err=%v", all, err)
	}
}

// TestStreamNon429ErrorStaysUntyped: a plain 500 is not a rate limit.
func TestStreamNon429ErrorStaysUntyped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":"boom"}`))
	}))
	defer srv.Close()

	p := newTestCodexProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.5"})
	if err == nil {
		t.Fatal("expected error")
	}
	if sdkerr.IsRateLimitError(err) {
		t.Errorf("500 wrongly typed as rate limit: %v", err)
	}
}

// TestStreamSuccessCapturesSnapshot: usage headers on a 200 are captured and
// persisted without disturbing the stream.
func TestStreamSuccessCapturesSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("x-codex-plan-type", "pro")
		h.Set("x-codex-primary-used-percent", "42")
		h.Set("x-codex-primary-window-minutes", "10080")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
	}))
	defer srv.Close()

	p := newTestCodexProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), provider.ChatRequest{Model: "gpt-5.5"})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch {
	}
	snap := p.LatestRateLimits()
	if snap == nil || snap.Primary == nil || snap.Primary.UsedPercent != 42 {
		t.Errorf("snapshot = %+v, want primary 42%%", snap)
	}
	all, _ := LoadRateLimitSnapshots()
	if all["acct_test"].PlanType != "pro" {
		t.Errorf("persisted snapshot = %+v", all["acct_test"])
	}
}
