package openai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestPlexusStyleConfigDoesNotRepeatGatewayFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"message":"all alias targets exhausted"}}`))
	}))
	defer server.Close()

	zero := 0
	client, err := New(Config{
		APIKey:         "test-plexus-client-key",
		BaseURL:        server.URL,
		Name:           "plexus",
		HTTPMaxRetries: &zero,
		Logger:         noop.NewLogger(),
		Tracer:         noop.NewTracer(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = client.Chat(context.Background(), provider.ChatRequest{Model: "swarm-main"})
	if got := calls.Load(); got != 1 {
		t.Fatalf("gateway request count = %d, want exactly 1", got)
	}
}
