package chat

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestAddRequestReturnsIndexAndRequestPtr verifies that AddRequest returns the
// index of the appended request and that requestPtr resolves that index back to
// the same entry even after additional requests are appended. This is the core
// of the JSON/Diff tab fix: response data must attach to the EXACT request
// captured for a call, not blindly to the last request.
func TestAddRequestReturnsIndexAndRequestPtr(t *testing.T) {
	d := NewDebugScreen()
	d.autoScroll = false // keep selection deterministic

	idx0 := d.AddRequest(DebugRequest{Model: "m0", Body: "body0"})
	idx1 := d.AddRequest(DebugRequest{Model: "m1", Body: "body1"})

	if idx0 != 0 || idx1 != 1 {
		t.Fatalf("expected indices 0 and 1, got %d and %d", idx0, idx1)
	}

	// Attach a response to the FIRST request via its index.
	if rp := d.requestPtr(idx0); rp != nil {
		rp.Response = "resp0"
	} else {
		t.Fatalf("requestPtr(%d) returned nil", idx0)
	}

	// Append a third request AFTER attaching — simulates nested calls appending
	// requests between capture and response in a real agentic loop.
	idx2 := d.AddRequest(DebugRequest{Model: "m2"})
	if idx2 != 2 {
		t.Fatalf("expected index 2, got %d", idx2)
	}

	// The response must still be on request 0, not the last request.
	if got := d.requests[0].Response; got != "resp0" {
		t.Fatalf("expected request 0 to keep Response=resp0, got %q", got)
	}
	if d.requests[2].Response != "" {
		t.Fatalf("expected request 2 to have empty Response, got %q", d.requests[2].Response)
	}
}

// TestRequestPtrBounds verifies requestPtr is safe for out-of-range indices.
func TestRequestPtrBounds(t *testing.T) {
	d := NewDebugScreen()
	if d.requestPtr(-1) != nil {
		t.Fatal("expected nil for negative index")
	}
	if d.requestPtr(0) != nil {
		t.Fatal("expected nil for index into empty requests")
	}
	d.AddRequest(DebugRequest{Model: "m"})
	if d.requestPtr(0) == nil {
		t.Fatal("expected non-nil for valid index")
	}
	if d.requestPtr(1) != nil {
		t.Fatal("expected nil for index past end")
	}
}

// rawEventRecordingProvider is a minimal concrete provider that records whether
// SetRawEventCallback reached it. It stands in for *anthropic.Provider.
type rawEventRecordingProvider struct {
	cb provider.RawEventCallback
}

func (p *rawEventRecordingProvider) Name() string { return "recording" }
func (p *rawEventRecordingProvider) Capabilities() provider.Capabilities {
	return provider.Capabilities{}
}
func (p *rawEventRecordingProvider) Chat(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
	return nil, nil
}
func (p *rawEventRecordingProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return nil, nil
}
func (p *rawEventRecordingProvider) LastProviderJSON() json.RawMessage { return nil }
func (p *rawEventRecordingProvider) SetRawEventCallback(cb provider.RawEventCallback) {
	p.cb = cb
}

type noopRawEventCallback struct{}

func (noopRawEventCallback) OnRawEvent(eventType string, rawData string) {}

// TestWrappersForwardRawEventCallback verifies the Raw Events tab fix: each TUI
// provider wrapper forwards SetRawEventCallback to its base so the callback
// reaches the concrete provider through any depth of wrapping.
func TestWrappersForwardRawEventCallback(t *testing.T) {
	cb := noopRawEventCallback{}

	cases := []struct {
		name string
		wrap func(base provider.Provider) provider.Provider
	}{
		{"modelPinned", func(base provider.Provider) provider.Provider {
			return newModelPinnedProvider(base, "some-model")
		}},
		{"rateLimited", func(base provider.Provider) provider.Provider {
			return newRateLimitedProvider(base, newModelRateLimiter(nil, time.Now), "p", "m")
		}},
		{"generationSettings", func(base provider.Provider) provider.Provider {
			return newGenerationSettingsProvider(base, newModelGenerationSettings("some-model"))
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &rawEventRecordingProvider{}
			wrapped := tc.wrap(rec)

			capable, ok := wrapped.(rawEventCapable)
			if !ok {
				t.Fatalf("%s wrapper does not implement rawEventCapable", tc.name)
			}
			capable.SetRawEventCallback(cb)

			if rec.cb == nil {
				t.Fatalf("%s wrapper did not forward callback to base provider", tc.name)
			}
			if _, ok := wrapped.(provider.DebugProvider); !ok {
				t.Fatalf("%s wrapper does not implement provider.DebugProvider", tc.name)
			}
		})
	}
}
