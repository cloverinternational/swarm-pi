package managed

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed/mockserver"
)

func TestClient_HealthCheck(t *testing.T) {
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

	if err := client.HealthCheck(ctx); err != nil {
		t.Errorf("health check failed: %v", err)
	}
}

func TestClient_InitProject(t *testing.T) {
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

	if err := client.InitProject(ctx, "test-project-001"); err != nil {
		t.Errorf("init project failed: %v", err)
	}

	// Verify project was created
	projects := srv.GetProjects()
	if len(projects) != 1 {
		t.Errorf("expected 1 project, got %d", len(projects))
	}
}

func TestClient_BootSession(t *testing.T) {
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

	// Initialize project first
	if err := client.InitProject(ctx, "test-project"); err != nil {
		t.Fatalf("init project failed: %v", err)
	}

	// Boot session
	info, err := client.BootSession(ctx, "test-session-001", "test-project")
	if err != nil {
		t.Fatalf("boot session failed: %v", err)
	}

	if info.SessionID != "test-session-001" {
		t.Errorf("session_id = %s, want test-session-001", info.SessionID)
	}

	if info.Status != "running" {
		t.Errorf("status = %s, want running", info.Status)
	}

	if info.Port == 0 {
		t.Error("expected non-zero port")
	}
}

func TestClient_StopSession(t *testing.T) {
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

	// Initialize project and boot session
	if err := client.InitProject(ctx, "test-project"); err != nil {
		t.Fatalf("init project failed: %v", err)
	}

	if _, err := client.BootSession(ctx, "test-session-002", "test-project"); err != nil {
		t.Fatalf("boot session failed: %v", err)
	}

	// Stop session
	if err := client.StopSession(ctx, "test-session-002"); err != nil {
		t.Errorf("stop session failed: %v", err)
	}
}

func TestClient_GetSession(t *testing.T) {
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

	// Initialize project and boot session
	if err := client.InitProject(ctx, "test-project"); err != nil {
		t.Fatalf("init project failed: %v", err)
	}

	if _, err := client.BootSession(ctx, "test-session-003", "test-project"); err != nil {
		t.Fatalf("boot session failed: %v", err)
	}

	// Get session
	info, err := client.GetSession(ctx, "test-session-003")
	if err != nil {
		t.Fatalf("get session failed: %v", err)
	}

	if info.SessionID != "test-session-003" {
		t.Errorf("session_id = %s, want test-session-003", info.SessionID)
	}

	// Get from cache (second call)
	info2, err := client.GetSession(ctx, "test-session-003")
	if err != nil {
		t.Fatalf("get session (cached) failed: %v", err)
	}

	if info2.SessionID != info.SessionID {
		t.Error("cached session info doesn't match")
	}
}

func TestClient_SendMessage(t *testing.T) {
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

	// Initialize project and boot session
	if err := client.InitProject(ctx, "test-project"); err != nil {
		t.Fatalf("init project failed: %v", err)
	}

	if _, err := client.BootSession(ctx, "test-session-004", "test-project"); err != nil {
		t.Fatalf("boot session failed: %v", err)
	}

	// Send message
	if err := client.SendMessage(ctx, "test-session-004", "user", "Hello, managed agent!"); err != nil {
		t.Errorf("send message failed: %v", err)
	}
}

func TestClient_StreamEvents(t *testing.T) {
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

	// Initialize project and boot session
	if err := client.InitProject(ctx, "test-project"); err != nil {
		t.Fatalf("init project failed: %v", err)
	}

	if _, err := client.BootSession(ctx, "test-session-005", "test-project"); err != nil {
		t.Fatalf("boot session failed: %v", err)
	}

	// Stream events
	streamCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	eventCh, err := client.StreamEvents(streamCtx, "test-session-005")
	if err != nil {
		t.Fatalf("stream events failed: %v", err)
	}

	// Collect events
	var events []Event
	for event := range eventCh {
		events = append(events, event)
		if len(events) >= 3 {
			cancel()
			break
		}
	}

	if len(events) == 0 {
		t.Error("expected at least one event")
	}

	// Check for expected event types
	hasThinking := false
	hasContent := false
	for _, e := range events {
		if e.Type == "thinking" {
			hasThinking = true
		}
		if e.Type == "content" {
			hasContent = true
		}
	}

	if !hasThinking && !hasContent {
		t.Log("warning: no thinking or content events received")
	}
}

func TestClient_AuthError(t *testing.T) {
	srv, err := mockserver.New(mockserver.Config{
		APIKeys: []string{"correct-key"},
	})
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
		APIKey:   "wrong-key",
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// This should fail with auth error
	err = client.InitProject(ctx, "test-project")
	if err == nil {
		t.Error("expected auth error, got nil")
	}
}

func TestNewClient_Validation(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		apiKey    string
		wantError bool
	}{
		{
			name:      "valid config",
			endpoint:  "http://localhost:8080",
			apiKey:    "test-key",
			wantError: false,
		},
		{
			name:      "missing endpoint",
			endpoint:  "",
			apiKey:    "test-key",
			wantError: true,
		},
		{
			name:      "missing api key",
			endpoint:  "http://localhost:8080",
			apiKey:    "",
			wantError: true,
		},
		{
			name:      "missing both",
			endpoint:  "",
			apiKey:    "",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewClient(Config{
				Endpoint: tt.endpoint,
				APIKey:   tt.apiKey,
			})

			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
