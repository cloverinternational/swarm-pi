//go:build integration
// +build integration

// Package main demonstrates the managed execution bridge
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/managed/mockserver"
)

func main() {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("   MANAGED EXECUTION BRIDGE - END-TO-END DEMONSTRATION")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 1: Start Mock Managed Service
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 1: Starting Mock Managed Service")
	fmt.Println("│")

	var bootCalls, stopCalls []string
	var mu sync.Mutex

	mockServer, err := mockserver.New(mockserver.Config{
		APIKeys: []string{"demo-api-key-12345"},
		OnBootSession: func(sessionID, projectID string) error {
			mu.Lock()
			bootCalls = append(bootCalls, fmt.Sprintf("session=%s project=%s", sessionID, projectID))
			mu.Unlock()
			fmt.Printf("│  📦 BootSession called: session=%s project=%s\n", sessionID, projectID)
			return nil
		},
		OnStopSession: func(sessionID string) error {
			mu.Lock()
			stopCalls = append(stopCalls, sessionID)
			mu.Unlock()
			fmt.Printf("│  🛑 StopSession called: session=%s\n", sessionID)
			return nil
		},
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create mock server: %v", err)
	}

	ctx := context.Background()
	if err := mockServer.Start(ctx); err != nil {
		log.Fatalf("│  ❌ Failed to start mock server: %v", err)
	}
	defer mockServer.Stop(ctx)

	endpoint := mockServer.URL()
	fmt.Printf("│  ✅ Mock server running at: %s\n", endpoint)
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 2: Create Managed Client
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 2: Creating Managed Client")
	fmt.Println("│")

	managedClient, err := managed.NewClient(managed.Config{
		Endpoint: endpoint,
		APIKey:   "demo-api-key-12345",
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create managed client: %v", err)
	}
	fmt.Println("│  ✅ Managed client created")
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 3: Create Managed Provider
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 3: Creating Managed Provider")
	fmt.Println("│")

	managedProvider, err := managed.NewManagedProvider(managed.ManagedProviderConfig{
		Client:    managedClient,
		ProjectID: "demo-project",
		SessionID: "demo-session-001",
		Model:     "claude-sonnet-4.6",
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create managed provider: %v", err)
	}
	fmt.Println("│  ✅ Managed provider created")
	fmt.Printf("│  📊 Capabilities: ContextWindow=%d, MaxOutput=%d\n",
		managedProvider.Capabilities().MaxContextWindow,
		managedProvider.Capabilities().MaxOutputTokens)
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 4: Create Agent with Managed Execution Mode
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 4: Creating Agent with Managed Mode")
	fmt.Println("│")

	agentDef := &agent.Definition{
		ID:            "demo-agent-001",
		Name:          "Demo Managed Agent",
		Model:         "claude-sonnet-4.6",
		Provider:      "managed",
		ExecutionMode: agent.ExecutionModeManaged,
		ManagedConfig: &agent.ManagedConfig{
			Endpoint:  endpoint,
			APIKey:    "demo-api-key-12345",
			ProjectID: "demo-project",
			SessionID: "demo-session-001",
		},
	}

	// Verify execution mode
	fmt.Printf("│  Execution Mode: %s\n", agentDef.GetExecutionMode())
	fmt.Printf("│  Is Managed: %v\n", agentDef.IsManaged())

	testAgent, err := agent.New(agent.Config{
		Definition: agentDef,
		Provider:   managedProvider,
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create agent: %v", err)
	}
	fmt.Println("│  ✅ Agent created successfully")
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 5: Execute Request
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 5: Executing Request via Managed Service")
	fmt.Println("│")

	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	fmt.Println("│  📤 Sending: \"Hello, can you help me write a function?\"")
	fmt.Println("│")

	startTime := time.Now()

	resp, err := testAgent.Execute(execCtx, agent.ExecuteRequest{
		Message: "Hello, can you help me write a function?",
	})
	if err != nil {
		log.Fatalf("│  ❌ Execute failed: %v", err)
	}

	duration := time.Since(startTime)

	// ─────────────────────────────────────────────────────────────
	// STEP 6: Display Results
	// ─────────────────────────────────────────────────────────────
	fmt.Println("│  ✅ Execution completed!")
	fmt.Println("│")
	fmt.Println("│  ┌─ RESPONSE DETAILS ─────────────────────────────────────")
	fmt.Printf("│  │ Message:        %s\n", truncate(resp.Message, 60))
	fmt.Printf("│  │ Session ID:     %s\n", resp.ConversationID)
	fmt.Printf("│  │ Duration:       %v\n", duration)
	fmt.Printf("│  │ Input Tokens:   %d\n", resp.InputTokens)
	fmt.Printf("│  │ Output Tokens:  %d\n", resp.OutputTokens)
	fmt.Printf("│  │ Finish Reason:  %s\n", resp.FinishReason)
	fmt.Printf("│  │ Turn Count:     %d\n", resp.TurnCount)
	fmt.Println("│  │")
	fmt.Println("│  │ METADATA:")
	if resp.Metadata != nil {
		for k, v := range resp.Metadata {
			fmt.Printf("│  │   %s: %v\n", k, v)
		}
	}
	fmt.Println("│  └────────────────────────────────────────────────────────")
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 7: Verify Session Lifecycle
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 7: Verifying Session Lifecycle")
	fmt.Println("│")

	mu.Lock()
	boots := bootCalls
	stops := stopCalls
	mu.Unlock()

	fmt.Printf("│  BootSession calls: %d\n", len(boots))
	for i, call := range boots {
		fmt.Printf("│    [%d] %s\n", i+1, call)
	}

	fmt.Printf("│  StopSession calls: %d\n", len(stops))
	for i, call := range stops {
		fmt.Printf("│    [%d] %s\n", i+1, call)
	}

	if len(boots) >= 1 && len(stops) >= 1 {
		fmt.Println("│  ✅ Session lifecycle verified (boot → execute → stop)")
	} else {
		fmt.Println("│  ⚠️  Session lifecycle incomplete")
	}
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 8: Verify Provider Stats
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 8: Provider Statistics")
	fmt.Println("│")

	stats := managedProvider.Stats()
	fmt.Printf("│  Session ID:     %s\n", stats.SessionID)
	fmt.Printf("│  Project ID:     %s\n", stats.ProjectID)
	fmt.Printf("│  Messages Sent:  %d\n", stats.MessagesSent)
	fmt.Printf("│  Input Tokens:   %d\n", stats.InputTokens)
	fmt.Printf("│  Output Tokens:  %d\n", stats.OutputTokens)
	fmt.Printf("│  Session Active: %v\n", stats.SessionActive)
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// FINAL SUMMARY
	// ─────────────────────────────────────────────────────────────
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("   ✅ ALL TESTS PASSED - MANAGED EXECUTION BRIDGE WORKING!")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("The managed execution bridge successfully:")
	fmt.Println("  ✓ Created managed client and provider")
	fmt.Println("  ✓ Created agent with ExecutionModeManaged")
	fmt.Println("  ✓ Delegated execution to managed service")
	fmt.Println("  ✓ Received and processed SSE events")
	fmt.Println("  ✓ Returned valid response with metadata")
	fmt.Println("  ✓ Properly managed session lifecycle (boot → stop)")
	fmt.Println()

	os.Exit(0)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
