package managed

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed/mockserver"
)

// mockAgentDefinition implements AgentDefinition for testing
type mockAgentDefinition struct {
	id         string
	model      string
	managedCfg *ManagedExecutionConfig
}

func (m *mockAgentDefinition) ID() string                                { return m.id }
func (m *mockAgentDefinition) GetModel() string                          { return m.model }
func (m *mockAgentDefinition) GetManagedConfig() *ManagedExecutionConfig { return m.managedCfg }

func TestManagedBackgroundAgent_StartAndWait(t *testing.T) {
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

	def := &mockAgentDefinition{
		id:    "test-managed-bg-agent",
		model: "claude-sonnet-4-5",
		managedCfg: &ManagedExecutionConfig{
			Endpoint:  srv.URL(),
			ProjectID: "test-project",
		},
	}

	bgAgent, err := NewManagedBackgroundAgent(ManagedBackgroundAgentConfig{
		Definition: def,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("failed to create managed background agent: %v", err)
	}

	// Start execution
	if err := bgAgent.Start(ctx, "Hello, managed agent!"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Check status
	if bgAgent.Status() != StatusRunning {
		t.Errorf("expected status %s, got %s", StatusRunning, bgAgent.Status())
	}

	// Wait for completion with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result, err := bgAgent.Wait(waitCtx)
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	if result == nil {
		t.Fatal("expected non-nil result")
	}

	if result.Status != StatusCompleted {
		t.Errorf("expected status %s, got %s", StatusCompleted, result.Status)
	}

	if result.Result == "" {
		t.Error("expected non-empty result")
	}

	// Verify session was created
	sessions := srv.GetSessions()
	if len(sessions) == 0 {
		t.Error("expected at least one session on server")
	}
}

func TestManagedBackgroundAgent_Events(t *testing.T) {
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

	def := &mockAgentDefinition{
		id:    "test-events-agent",
		model: "claude-sonnet-4-5",
		managedCfg: &ManagedExecutionConfig{
			ProjectID: "test-project",
		},
	}

	bgAgent, err := NewManagedBackgroundAgent(ManagedBackgroundAgentConfig{
		Definition: def,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("failed to create managed background agent: %v", err)
	}

	// Start execution
	if err := bgAgent.Start(ctx, "Test events"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Collect events
	var events []BackgroundAgentEvent
	timeout := time.After(5 * time.Second)

Collect:
	for {
		select {
		case event, ok := <-bgAgent.Events():
			if !ok {
				break Collect
			}
			events = append(events, event)
			if event.EventType == EventCompleted || event.EventType == EventFailed {
				break Collect
			}
		case <-timeout:
			t.Fatal("timeout waiting for events")
		}
	}

	// Check we got a started event
	hasStarted := false
	for _, e := range events {
		if e.EventType == EventStarted {
			hasStarted = true
		}
	}
	if !hasStarted {
		t.Error("expected EventStarted event")
	}

	// Check we got a completed event
	hasCompleted := false
	for _, e := range events {
		if e.EventType == EventCompleted {
			hasCompleted = true
		}
	}
	if !hasCompleted {
		t.Error("expected EventCompleted event")
	}
}

func TestManagedBackgroundAgent_Cancel(t *testing.T) {
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

	def := &mockAgentDefinition{
		id:    "test-cancel-agent",
		model: "claude-sonnet-4-5",
		managedCfg: &ManagedExecutionConfig{
			ProjectID: "test-project",
		},
	}

	bgAgent, err := NewManagedBackgroundAgent(ManagedBackgroundAgentConfig{
		Definition: def,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("failed to create managed background agent: %v", err)
	}

	// Start execution
	if err := bgAgent.Start(ctx, "Test cancel"); err != nil {
		t.Fatalf("failed to start: %v", err)
	}

	// Wait a bit for execution to start
	time.Sleep(100 * time.Millisecond)

	// Cancel
	bgAgent.Cancel()

	// Wait for completion
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, _ := bgAgent.Wait(waitCtx)

	// Status should be one of the terminal states
	status := bgAgent.Status()
	if status != StatusCancelled && status != StatusCompleted && status != StatusFailed {
		t.Errorf("expected terminal status, got %s", status)
	}

	// Result should be set
	if result == nil {
		t.Error("expected result to be set after cancel")
	}
}

func TestManagedBackgroundAgent_Validation(t *testing.T) {
	tests := []struct {
		name      string
		config    ManagedBackgroundAgentConfig
		wantError bool
	}{
		{
			name: "missing definition",
			config: ManagedBackgroundAgentConfig{
				Client: &Client{},
			},
			wantError: true,
		},
		{
			name: "missing client",
			config: ManagedBackgroundAgentConfig{
				Definition: &mockAgentDefinition{id: "test"},
			},
			wantError: true,
		},
		{
			name: "valid config",
			config: ManagedBackgroundAgentConfig{
				Definition: &mockAgentDefinition{
					id:         "test",
					model:      "claude-sonnet-4-5",
					managedCfg: &ManagedExecutionConfig{ProjectID: "test-project"},
				},
				Client: &Client{},
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewManagedBackgroundAgent(tt.config)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestManagedBackgroundAgent_DoubleStart(t *testing.T) {
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

	def := &mockAgentDefinition{
		id:    "test-double-start",
		model: "claude-sonnet-4-5",
		managedCfg: &ManagedExecutionConfig{
			ProjectID: "test-project",
		},
	}

	bgAgent, err := NewManagedBackgroundAgent(ManagedBackgroundAgentConfig{
		Definition: def,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("failed to create managed background agent: %v", err)
	}

	// First start should succeed
	if err := bgAgent.Start(ctx, "First start"); err != nil {
		t.Fatalf("first start failed: %v", err)
	}

	// Second start should fail
	err = bgAgent.Start(ctx, "Second start")
	if err == nil {
		t.Error("expected error on double start")
	}
}

func TestManagedBackgroundAgent_Close(t *testing.T) {
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

	def := &mockAgentDefinition{
		id:    "test-close-agent",
		model: "claude-sonnet-4-5",
		managedCfg: &ManagedExecutionConfig{
			ProjectID: "test-project",
		},
	}

	bgAgent, err := NewManagedBackgroundAgent(ManagedBackgroundAgentConfig{
		Definition: def,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("failed to create managed background agent: %v", err)
	}

	// Close should work
	if err := bgAgent.Close(ctx); err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestManagedBackgroundAgent_SessionID(t *testing.T) {
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

	def := &mockAgentDefinition{
		id:    "test-session-id-agent",
		model: "claude-sonnet-4-5",
		managedCfg: &ManagedExecutionConfig{
			ProjectID: "test-project",
			SessionID: "custom-session-123",
		},
	}

	bgAgent, err := NewManagedBackgroundAgent(ManagedBackgroundAgentConfig{
		Definition: def,
		Client:     client,
	})
	if err != nil {
		t.Fatalf("failed to create managed background agent: %v", err)
	}

	// Session ID should match
	if bgAgent.SessionID() != "custom-session-123" {
		t.Errorf("expected session ID 'custom-session-123', got %s", bgAgent.SessionID())
	}
}
