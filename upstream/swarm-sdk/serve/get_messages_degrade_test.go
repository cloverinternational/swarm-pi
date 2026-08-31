package serve

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// TestHandleGetMessages_ImplicitEmptyDegradesGracefully locks in the fix for a
// real "rpc errors from the mobile app" symptom: attaching to a freshly-started
// or newly-attached session and immediately seeing
//
//	getMessages failed: conversation not found
//
// The mobile PWA (and any attach client) requests the transcript with an EMPTY
// convID, meaning "the active conversation". When that conversation has no
// stored transcript yet — or there is no active conversation at all — the
// serve bridge must treat it as an empty transcript (a normal "no messages
// yet" state), NOT surface a raw RPC error to the UI.
//
// A bare *client.Client has no conversation manager, so GetConversationMessages
// always errors — which lets us exercise both branches of the fix precisely:
//   - implicit (empty convID)  → degrade to an empty transcript, no error
//   - explicit (convID given)  → a genuine lookup miss, error still surfaced
func TestHandleGetMessages_ImplicitEmptyDegradesGracefully(t *testing.T) {
	m := NewMux(&client.Client{})

	t.Run("implicit_empty_convID_returns_empty_not_error", func(t *testing.T) {
		res, err := m.Dispatch(context.Background(), "client.getMessages", json.RawMessage(`{"convID":""}`))
		if err != nil {
			t.Fatalf("implicit getMessages (empty convID) must degrade to an empty transcript, got error: %v", err)
		}
		out, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("expected map result, got %T", res)
		}
		msgs, ok := out["messages"].([]any)
		if !ok {
			t.Fatalf("expected messages to be []any, got %T (%v)", out["messages"], out["messages"])
		}
		if len(msgs) != 0 {
			t.Fatalf("expected empty transcript, got %d message(s)", len(msgs))
		}
	})

	t.Run("implicit_missing_params_returns_empty_not_error", func(t *testing.T) {
		// The PWA sometimes calls with no params at all; that is still an
		// implicit active-conversation request and must degrade the same way.
		res, err := m.Dispatch(context.Background(), "client.getMessages", nil)
		if err != nil {
			t.Fatalf("implicit getMessages (nil params) must degrade to an empty transcript, got error: %v", err)
		}
		if _, ok := res.(map[string]any); !ok {
			t.Fatalf("expected map result, got %T", res)
		}
	})

	t.Run("explicit_missing_convID_still_errors", func(t *testing.T) {
		_, err := m.Dispatch(context.Background(), "client.getMessages", json.RawMessage(`{"convID":"nope-does-not-exist"}`))
		if err == nil {
			t.Fatal("an EXPLICIT convID that does not resolve must still surface an error, got nil")
		}
	})
}
