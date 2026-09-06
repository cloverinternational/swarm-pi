package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent/agenttest"
)

// TestGenerate_ReturnsText verifies Generate returns the provider's reply
// without touching the agent loop, storage, or hooks.
func TestGenerate_ReturnsText(t *testing.T) {
	mock := agenttest.NewMockProvider().RespondWith("hello world")
	c := &Client{provider: mock}
	c.opts.model = "mock-model"
	c.opts.systemPrompt = "You are helpful."
	c.opts.maxTokens = 1024

	got, err := c.Generate(context.Background(), "say hi")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "hello world" {
		t.Errorf("reply: got %q want %q", got, "hello world")
	}
	if mock.TotalCalls() != 1 {
		t.Errorf("provider call count: got %d want 1", mock.TotalCalls())
	}
}

// TestGenerate_NoProvider_Errors confirms Generate surfaces a helpful error
// when the client has no provider wired (e.g., a test constructed Client{}).
func TestGenerate_NoProvider_Errors(t *testing.T) {
	c := &Client{}
	_, err := c.Generate(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestGenerate_RespectsContextCancel confirms a cancelled context aborts
// before the provider is called (the mock is synchronous so we verify the
// precondition rather than mid-flight cancellation).
func TestGenerate_RespectsContextCancel(t *testing.T) {
	// A provider that sleeps long enough that ctx.Done will fire first.
	mock := &slowProvider{delay: 500 * time.Millisecond, reply: "should not arrive"}
	c := &Client{provider: mock}
	c.opts.model = "mock-model"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := c.Generate(ctx, "hi")
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Errorf("expected ctx error, got %v", err)
	}
}

// TestGenerate_EmptyResponseErrors confirms nil message → error rather than
// returning a misleading empty string.
func TestGenerate_EmptyResponseErrors(t *testing.T) {
	mock := &nilResponseProvider{}
	c := &Client{provider: mock}
	_, err := c.Generate(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error on nil response, got nil")
	}
}
