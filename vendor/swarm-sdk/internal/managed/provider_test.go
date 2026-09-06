package managed

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed/mockserver"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestManagedProvider_Name(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	client, err := NewClient(Config{
		Endpoint: srv.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	provider, err := NewManagedProvider(ManagedProviderConfig{
		Client:    client,
		ProjectID: "test-project",
		Model:     "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	if provider.Name() != "managed" {
		t.Errorf("Name() = %s, want managed", provider.Name())
	}
}

func TestManagedProvider_Capabilities(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	client, err := NewClient(Config{
		Endpoint: srv.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Test with custom capabilities
	customCaps := provider.Capabilities{
		Streaming:        true,
		FunctionCalling:  true,
		Vision:           true,
		MaxContextWindow: 100000,
		MaxOutputTokens:  4096,
	}
	provider, err := NewManagedProvider(ManagedProviderConfig{
		Client:       client,
		ProjectID:    "test-project",
		Model:        "claude-sonnet-4-5",
		Capabilities: customCaps,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	caps := provider.Capabilities()
	if !caps.Streaming {
		t.Error("expected Streaming to be true")
	}
	if !caps.FunctionCalling {
		t.Error("expected FunctionCalling to be true")
	}
	if caps.MaxContextWindow != 100000 {
		t.Errorf("MaxContextWindow = %d, want 100000", caps.MaxContextWindow)
	}
}

func TestManagedProvider_Validation(t *testing.T) {
	tests := []struct {
		name      string
		config    ManagedProviderConfig
		wantError bool
	}{
		{
			name: "missing client",
			config: ManagedProviderConfig{
				ProjectID: "test-project",
				Model:     "claude-sonnet-4-5",
			},
			wantError: true,
		},
		{
			name: "missing project ID",
			config: ManagedProviderConfig{
				Model: "claude-sonnet-4-5",
			},
			wantError: true,
		},
		{
			name: "missing model",
			config: ManagedProviderConfig{
				ProjectID: "test-project",
			},
			wantError: true,
		},
		{
			name: "valid config",
			config: ManagedProviderConfig{
				Client:    &Client{},
				ProjectID: "test-project",
				Model:     "claude-sonnet-4-5",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewManagedProvider(tt.config)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestManagedProvider_SessionManagement(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	client, err := NewClient(Config{
		Endpoint: srv.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	provider, err := NewManagedProvider(ManagedProviderConfig{
		Client:    client,
		SessionID: "test-session-provider-001",
		ProjectID: "test-project",
		Model:     "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// Ensure session is started
	if err := provider.ensureSession(ctx); err != nil {
		t.Fatalf("failed to ensure session: %v", err)
	}

	// Verify session was created
	sessions := srv.GetSessions()
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}

	// Close the session
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("failed to close provider: %v", err)
	}

	// Verify session stats
	stats := provider.Stats()
	if stats.SessionActive {
		t.Error("expected session to be inactive after close")
	}
}

func TestManagedProvider_Stream(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	client, err := NewClient(Config{
		Endpoint: srv.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	prov, err := NewManagedProvider(ManagedProviderConfig{
		Client:    client,
		SessionID: "test-session-stream",
		ProjectID: "test-project",
		Model:     "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer prov.Close(ctx)

	// Start stream
	streamCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// First ensure session
	if err := prov.ensureSession(streamCtx); err != nil {
		t.Fatalf("failed to ensure session: %v", err)
	}

	// Stream
	req := provider.ChatRequest{
		Model: "claude-sonnet-4-5",
	}
	streamCh, err := prov.Stream(streamCtx, req)
	if err != nil {
		t.Fatalf("failed to start stream: %v", err)
	}

	// Collect chunks
	var chunks []provider.StreamChunk
	for chunk := range streamCh {
		chunks = append(chunks, chunk)
		if chunk.Done {
			break
		}
	}

	if len(chunks) == 0 {
		t.Error("expected at least one chunk")
	}

	// Check final chunk
	lastChunk := chunks[len(chunks)-1]
	if !lastChunk.Done {
		t.Error("expected final chunk to be done")
	}
}

func TestManagedProvider_Chat(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	client, err := NewClient(Config{
		Endpoint: srv.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	prov, err := NewManagedProvider(ManagedProviderConfig{
		Client:    client,
		SessionID: "test-session-chat",
		ProjectID: "test-project",
		Model:     "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}
	defer prov.Close(ctx)

	// Chat
	chatCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req := provider.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "Hello, managed agent!"},
		},
	}

	resp, err := prov.Chat(chatCtx, req)
	if err != nil {
		t.Fatalf("failed to chat: %v", err)
	}

	if resp.Message == nil {
		t.Fatal("expected non-nil message")
	}

	if resp.Message.Role != conversation.RoleAssistant {
		t.Errorf("expected role assistant, got %s", resp.Message.Role)
	}

	if resp.Message.Content == "" {
		t.Error("expected non-empty content")
	}

	// Check stats
	stats := prov.Stats()
	if stats.MessagesSent == 0 {
		t.Error("expected messages to be sent")
	}
}

func TestManagedProvider_AutoSessionID(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer srv.Stop(ctx)

	client, err := NewClient(Config{
		Endpoint: srv.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// Create provider without session ID
	prov, err := NewManagedProvider(ManagedProviderConfig{
		Client:    client,
		ProjectID: "test-project",
		Model:     "claude-sonnet-4-5",
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// Should have generated a session ID
	if prov.SessionID() == "" {
		t.Error("expected auto-generated session ID")
	}

	// Session ID should start with "session-"
	if prov.SessionID()[:8] != "session-" {
		t.Errorf("expected session ID to start with 'session-', got %s", prov.SessionID()[:8])
	}
}
