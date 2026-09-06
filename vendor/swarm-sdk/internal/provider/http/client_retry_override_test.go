package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

func TestHTTPMaxRetriesZeroDisablesRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	zero := 0
	client, err := NewClient(ClientConfig{
		Logger:         noop.NewLogger(),
		Tracer:         noop.NewTracer(),
		HTTPMaxRetries: &zero,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.retryPolicy != nil {
		t.Fatal("HTTPMaxRetries=0 must disable the retry policy")
	}

	response, err := client.Do(context.Background(), client.BuildRequest(context.Background()).
		Method(http.MethodPost).
		URL(server.URL).
		Body(map[string]string{"model": "swarm-main"}))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer response.Close()
	if got := calls.Load(); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestHTTPMaxRetriesNilPreservesDefaultPolicy(t *testing.T) {
	t.Setenv("SWARM_HTTP_MAX_RETRIES", "1")
	client, err := NewClient(ClientConfig{Logger: noop.NewLogger(), Tracer: noop.NewTracer()})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if client.retryPolicy == nil {
		t.Fatal("nil override must preserve the default retry policy")
	}
	if got := client.retryPolicy.MaxAttempts(); got != 2 {
		t.Fatalf("MaxAttempts = %d, want 2", got)
	}
}
