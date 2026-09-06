// Package agent_test tests the managed execution bridge
package agent_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed/mockserver"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestManagedExecutionBridge tests that Execute() delegates to managed client
// when ExecutionMode is set to "managed".
// This mirrors Anthropic's Managed Agents API flow:
// 1. Create agent with managed config
// 2. Call Execute()
// 3. Verify it boots session, sends message, streams events
func TestManagedExecutionBridge(t *testing.T) {
	// ─────────────────────────────────────────────────────────────
	// Setup: Create mock managed service server
	// ─────────────────────────────────────────────────────────────
	mockServer, err := mockserver.New(mockserver.Config{
		APIKeys: []string{"test-api-key"},
	})
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := mockServer.Start(ctx); err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer mockServer.Stop(ctx)

	endpoint := mockServer.URL()
	t.Logf("Mock server endpoint: %s", endpoint)

	// ─────────────────────────────────────────────────────────────
	// Create managed client and provider
	// ─────────────────────────────────────────────────────────────
	managedClient, err := managed.NewClient(managed.Config{
		Endpoint: endpoint,
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("Failed to create managed client: %v", err)
	}

	managedProvider, err := managed.NewManagedProvider(managed.ManagedProviderConfig{
		Client:    managedClient,
		ProjectID: "test-project",
		SessionID: "test-session-001",
		Model:     "claude-sonnet-4.6",
	})
	if err != nil {
		t.Fatalf("Failed to create managed provider: %v", err)
	}

	// ─────────────────────────────────────────────────────────────
	// Create agent with managed provider
	// ─────────────────────────────────────────────────────────────
	agentDef := &agent.Definition{
		ID:            "test-agent-001",
		Name:          "Test Managed Agent",
		Model:         "claude-sonnet-4.6",
		Provider:      "managed",
		ExecutionMode: agent.ExecutionModeManaged,
		ManagedConfig: &agent.ManagedConfig{
			Endpoint:  endpoint,
			APIKey:    "test-api-key",
			ProjectID: "test-project",
			SessionID: "test-session-001",
		},
	}

	// Verify execution mode is managed
	if !agentDef.IsManaged() {
		t.Error("Expected IsManaged() to return true for managed mode agent")
	}

	// Create agent instance
	testAgent, err := agent.New(agent.Config{
		Definition: agentDef,
		Provider:   managedProvider, // Use the managed provider
	})
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	// ─────────────────────────────────────────────────────────────
	// Execute with managed mode
	// ─────────────────────────────────────────────────────────────
	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := testAgent.Execute(execCtx, agent.ExecuteRequest{
		Message: "Hello, can you help me test?",
	})

	// ─────────────────────────────────────────────────────────────
	// Verify results
	// ─────────────────────────────────────────────────────────────
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Verify response is populated
	if resp.Message == "" {
		t.Error("Expected non-empty response message")
	}
	if resp.ConversationID == "" {
		t.Error("Expected non-empty conversation ID (session ID)")
	}
	if resp.FinishReason == "" {
		t.Error("Expected non-empty finish reason")
	}

	// Log results
	t.Logf("Response: %s", resp.Message)
	t.Logf("Duration: %v", resp.Duration)
	t.Logf("Tokens: in=%d out=%d", resp.InputTokens, resp.OutputTokens)
}

// TestManagedExecutionWithHybridMode tests that hybrid mode works correctly
func TestManagedExecutionWithHybridMode(t *testing.T) {
	agentDef := &agent.Definition{
		ID:            "hybrid-agent-001",
		Name:          "Hybrid Agent",
		Model:         "claude-sonnet-4.6",
		Provider:      "anthropic",
		ExecutionMode: agent.ExecutionModeHybrid,
		ManagedConfig: &agent.ManagedConfig{
			Endpoint: "http://test.local",
			APIKey:   "test-key",
		},
	}

	// Verify hybrid mode is detected as managed
	if !agentDef.IsManaged() {
		t.Error("Expected IsManaged() to return true for hybrid mode")
	}

	t.Log("Hybrid mode correctly detected as managed")
}

// TestLocalExecutionWhenNotManaged tests that local execution still works
func TestLocalExecutionWhenNotManaged(t *testing.T) {
	agentDef := &agent.Definition{
		ID:            "local-agent-001",
		Name:          "Local Agent",
		Model:         "claude-sonnet-4.6",
		Provider:      "anthropic",
		ExecutionMode: agent.ExecutionModeLocal,
	}

	// Verify local mode
	if agentDef.IsManaged() {
		t.Error("Expected IsManaged() to return false for local mode agent")
	}

	// Execution mode should be local
	if agentDef.GetExecutionMode() != agent.ExecutionModeLocal {
		t.Errorf("Expected ExecutionModeLocal, got %s", agentDef.GetExecutionMode())
	}

	t.Log("Local mode correctly detected")
}

// TestManagedExecutionWithMissingConfig tests error handling
func TestManagedExecutionWithMissingConfig(t *testing.T) {
	agentDef := &agent.Definition{
		ID:            "bad-agent-001",
		Name:          "Bad Agent",
		Model:         "claude-sonnet-4.6",
		Provider:      "managed", // Use managed provider
		ExecutionMode: agent.ExecutionModeManaged,
		ManagedConfig: &agent.ManagedConfig{
			// Missing endpoint and API key
		},
	}

	// Create a mock provider for testing
	mockServer, err := mockserver.New(mockserver.Config{
		APIKeys: []string{"test-key"},
	})
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := mockServer.Start(ctx); err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer mockServer.Stop(ctx)

	managedClient, err := managed.NewClient(managed.Config{
		Endpoint: mockServer.URL(),
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("Failed to create managed client: %v", err)
	}

	managedProvider, err := managed.NewManagedProvider(managed.ManagedProviderConfig{
		Client:    managedClient,
		ProjectID: "test-project",
		Model:     "claude-sonnet-4.6",
	})
	if err != nil {
		t.Fatalf("Failed to create managed provider: %v", err)
	}

	testAgent, err := agent.New(agent.Config{
		Definition: agentDef,
		Provider:   managedProvider,
	})
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	// Should fail with missing endpoint
	_, err = testAgent.Execute(ctx, agent.ExecuteRequest{
		Message: "This should fail",
	})

	if err == nil {
		t.Fatal("Expected error for missing managed config")
	}

	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("Expected error about missing endpoint, got: %v", err)
	}

	t.Logf("Correctly returned error: %v", err)
}

// TestManagedSessionLifecycle tests the full session lifecycle
func TestManagedSessionLifecycle(t *testing.T) {
	var sessionStates []string
	var mu sync.Mutex

	mockServer, err := mockserver.New(mockserver.Config{
		APIKeys: []string{"test-api-key"},
		OnBootSession: func(sessionID, projectID string) error {
			mu.Lock()
			sessionStates = append(sessionStates, fmt.Sprintf("boot:%s", sessionID))
			mu.Unlock()
			return nil
		},
		OnStopSession: func(sessionID string) error {
			mu.Lock()
			sessionStates = append(sessionStates, fmt.Sprintf("stop:%s", sessionID))
			mu.Unlock()
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := mockServer.Start(ctx); err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer mockServer.Stop(ctx)

	managedClient, err := managed.NewClient(managed.Config{
		Endpoint: mockServer.URL(),
		APIKey:   "test-api-key",
	})
	if err != nil {
		t.Fatalf("Failed to create managed client: %v", err)
	}

	managedProvider, err := managed.NewManagedProvider(managed.ManagedProviderConfig{
		Client:    managedClient,
		ProjectID: "lifecycle-test",
		SessionID: "lifecycle-session-001",
		Model:     "claude-sonnet-4.6",
	})
	if err != nil {
		t.Fatalf("Failed to create managed provider: %v", err)
	}

	agentDef := &agent.Definition{
		ID:            "lifecycle-agent-001",
		Name:          "Lifecycle Agent",
		Model:         "claude-sonnet-4.6",
		Provider:      "managed",
		ExecutionMode: agent.ExecutionModeManaged,
		ManagedConfig: &agent.ManagedConfig{
			Endpoint:  mockServer.URL(),
			APIKey:    "test-api-key",
			ProjectID: "lifecycle-test",
			SessionID: "lifecycle-session-001",
		},
	}

	testAgent, err := agent.New(agent.Config{
		Definition: agentDef,
		Provider:   managedProvider,
	})
	if err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	_, err = testAgent.Execute(ctx, agent.ExecuteRequest{
		Message: "Test lifecycle",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Close triggers managed session stop. Execute intentionally leaves the
	// session running so callers can keep conversing; the full lifecycle is
	// exercised here via Close.
	if err := testAgent.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify session was booted and stopped
	mu.Lock()
	states := sessionStates
	mu.Unlock()

	if len(states) < 2 {
		t.Fatalf("Expected at least 2 session states, got %d: %v", len(states), states)
	}

	// First should be boot
	if !strings.HasPrefix(states[0], "boot:") {
		t.Errorf("Expected first state to be 'boot:', got %s", states[0])
	}

	// Last should be stop
	if !strings.HasPrefix(states[len(states)-1], "stop:") {
		t.Errorf("Expected last state to be 'stop:', got %s", states[len(states)-1])
	}

	t.Logf("Session lifecycle states: %v", states)
}

// Compile-time guarantee that ManagedProvider implements provider.Provider.
var _ provider.Provider = (*managed.ManagedProvider)(nil)

// TestManagedProviderImplementsInterface verifies ManagedProvider implements
// provider.Provider, both at compile time (var _ above) and at runtime via a
// dynamic type assertion that fails loudly if the contract is ever broken.
func TestManagedProviderImplementsInterface(t *testing.T) {
	var p any = (*managed.ManagedProvider)(nil)
	if _, ok := p.(provider.Provider); !ok {
		t.Fatal("ManagedProvider does not implement provider.Provider")
	}
}

// TestManagedExecutionProviderCapabilities tests that managed provider reports capabilities
func TestManagedExecutionProviderCapabilities(t *testing.T) {
	mockServer, err := mockserver.New(mockserver.Config{})
	if err != nil {
		t.Fatalf("Failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := mockServer.Start(ctx); err != nil {
		t.Fatalf("Failed to start mock server: %v", err)
	}
	defer mockServer.Stop(ctx)

	managedClient, err := managed.NewClient(managed.Config{
		Endpoint: mockServer.URL(),
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("Failed to create managed client: %v", err)
	}

	managedProvider, err := managed.NewManagedProvider(managed.ManagedProviderConfig{
		Client:    managedClient,
		ProjectID: "test-project",
		Model:     "claude-sonnet-4.6",
	})
	if err != nil {
		t.Fatalf("Failed to create managed provider: %v", err)
	}

	caps := managedProvider.Capabilities()

	// Default capabilities should be set
	if caps.MaxContextWindow == 0 {
		t.Error("Expected MaxContextWindow to be set")
	}
	if caps.MaxOutputTokens == 0 {
		t.Error("Expected MaxOutputTokens to be set")
	}

	t.Logf("Capabilities: MaxContextWindow=%d, MaxOutputTokens=%d",
		caps.MaxContextWindow, caps.MaxOutputTokens)
}
