package serve

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// TestMuxDispatch_RecoversFromHandlerPanic guards the fix that keeps a single
// panicking serve handler from taking down the transport: over /ws it would
// drop the connection, over /rpc it would hand the mobile PWA a bare HTTP 500.
// Dispatch now recovers and returns a clean JSON-RPC internal error, and normal
// dispatch keeps working afterwards.
func TestMuxDispatch_RecoversFromHandlerPanic(t *testing.T) {
	m := NewMux(&client.Client{})
	m.Register(Method{
		Name: "test.panic",
		Handler: func(context.Context, *client.Client, json.RawMessage) (any, error) {
			panic("boom")
		},
	})

	result, err := m.Dispatch(context.Background(), "test.panic", nil)
	if err == nil {
		t.Fatal("expected an error from a panicking handler, got nil")
	}
	if result != nil {
		t.Fatalf("expected nil result, got %v", result)
	}

	// A normal method still dispatches after a recovered panic.
	if _, err := m.Dispatch(context.Background(), "client.snapshot", nil); err != nil {
		t.Fatalf("normal dispatch after a recovered panic errored: %v", err)
	}
}
