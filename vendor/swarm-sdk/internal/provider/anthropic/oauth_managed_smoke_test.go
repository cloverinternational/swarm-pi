package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestManagedSmokeTokenSwapPickup is an end-to-end smoke test of managed mode:
// a fake Anthropic /v1/messages server rejects (401) any bearer token that is
// not the "current" token, simulating Anthropic invalidating the prior access
// token the instant the central daemon rotates it. We start with OLD on disk,
// fire a chat (the server is configured to only accept NEW), and prove the
// provider 401s, reloads the swapped-in token from disk, and the retry
// succeeds with NEW — without ever self-refreshing.
//
// Gated behind SWARMOS_SMOKE=1 so it does not run in the normal suite.
func TestManagedSmokeTokenSwapPickup(t *testing.T) {
	if os.Getenv("SWARMOS_SMOKE") != "1" {
		t.Skip("set SWARMOS_SMOKE=1 to run the managed-mode live smoke test")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	setManagedModeForTest(true)
	defer setManagedModeForTest(false)

	// The token the fake server will accept. Starts unset so OLD is rejected.
	accepted := "sk-ant-oat-NEW"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+accepted {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid bearer"}}`))
			return
		}
		// Minimal non-streaming Anthropic message response.
		resp := map[string]any{
			"id":          "msg_smoke",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-6",
			"stop_reason": "end_turn",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	// Disk starts with OLD (which the server will reject).
	writeOAuthFile(t, home, "sk-ant-oat-OLD")

	p := newManagedProvider(t, "sk-ant-oat-OLD")
	p.config.BaseURL = srv.URL

	// Simulate the central daemon rotating the token on disk to NEW *before*
	// the request is made (the provider should pick it up on the pre-request
	// reload). We delay slightly so the mtime differs from the initial write.
	time.Sleep(10 * time.Millisecond)
	writeOAuthFile(t, home, "sk-ant-oat-NEW")

	resp, err := p.chat(context.Background(), provider.ChatRequest{
		Model: "claude-sonnet-4-6",
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat failed after token swap: %v", err)
	}
	if resp == nil || !strings.Contains(strings.ToLower(contentText(resp)), "ok") {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Prove the provider is now using NEW (picked up from disk, not refreshed).
	if p.config.APIKey != "sk-ant-oat-NEW" {
		t.Fatalf("provider APIKey=%q, want NEW (reloaded from disk)", p.config.APIKey)
	}
	t.Logf("smoke OK: provider picked up swapped token from disk; final bearer=%s", p.config.APIKey)
}

func contentText(resp *provider.ChatResponse) string {
	if resp == nil || resp.Message == nil {
		return ""
	}
	return resp.Message.Content
}
