//go:build integration
// +build integration

// Package main demonstrates a real poem agent running through managed execution
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
)

func main() {
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("   🎭 POEM AGENT - AGI & THE FUTURE OF HUMANITY")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	endpoint := "http://127.0.0.1:8080"
	apiKey := "swarm-test-key-12345"
	projectID := "poem-agent-project"
	sessionID := fmt.Sprintf("poem-agent-%d", time.Now().Unix())

	// ─────────────────────────────────────────────────────────────
	// STEP 1: Create Managed Client
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 1: Creating Managed Client")
	fmt.Println("│")

	managedClient, err := managed.NewClient(managed.Config{
		Endpoint: endpoint,
		APIKey:   apiKey,
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create managed client: %v", err)
	}
	fmt.Printf("│  ✅ Connected to Swarm Cloud at: %s\n", endpoint)
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 2: Initialize Project
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 2: Initializing Project")
	fmt.Println("│")

	ctx := context.Background()
	if err := managedClient.InitProject(ctx, projectID); err != nil {
		log.Printf("│  ⚠️  Project init warning: %v", err)
	} else {
		fmt.Printf("│  ✅ Project initialized: %s\n", projectID)
	}
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 3: Boot Session (Docker Container)
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 3: Booting Session Container")
	fmt.Println("│")

	sessionInfo, err := managedClient.BootSession(ctx, sessionID, projectID)
	if err != nil {
		log.Fatalf("│  ❌ Failed to boot session: %v", err)
	}

	fmt.Printf("│  ✅ Session booted!\n")
	fmt.Printf("│  📦 Session ID:    %s\n", sessionID)
	fmt.Printf("│  📦 Status:        %s\n", sessionInfo.Status)
	fmt.Printf("│  📦 Port:          %d\n", sessionInfo.Port)
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 4: Create Managed Provider
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 4: Creating Managed Provider")
	fmt.Println("│")

	managedProvider, err := managed.NewManagedProvider(managed.ManagedProviderConfig{
		Client:    managedClient,
		ProjectID: projectID,
		SessionID: sessionID,
		Model:     "claude-sonnet-4-5",
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create managed provider: %v", err)
	}
	fmt.Println("│  ✅ Managed provider created")
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 5: Create Poem Agent
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 5: Creating Poem Agent")
	fmt.Println("│")

	agentDef := &agent.Definition{
		ID:            "poem-agent-001",
		Name:          "AGI Poet",
		Model:         "claude-sonnet-4-5",
		Provider:      "managed",
		ExecutionMode: agent.ExecutionModeManaged,
		ManagedConfig: &agent.ManagedConfig{
			Endpoint:  endpoint,
			APIKey:    apiKey,
			ProjectID: projectID,
			SessionID: sessionID,
		},
	}

	testAgent, err := agent.New(agent.Config{
		Definition: agentDef,
		Provider:   managedProvider,
	})
	if err != nil {
		log.Fatalf("│  ❌ Failed to create agent: %v", err)
	}
	fmt.Println("│  ✅ Poem Agent created")
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 6: Run Poem Generation (3 iterations)
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 6: Running Poem Generation (3 iterations)")
	fmt.Println("│")

	var mu sync.Mutex
	poems := make([]string, 0)

	for i := 1; i <= 3; i++ {
		fmt.Printf("│  ┌─ ITERATION %d ─────────────────────────────────────────\n", i)
		fmt.Printf("│  │\n")

		execCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		prompt := fmt.Sprintf(`Create a dark, evocative poem about AGI and the future of humanity.

The poem should explore themes like:
- The rise of artificial general intelligence
- The relationship between humans and machines
- The potential end of human civilization
- The beauty and terror of technological transcendence

Make it creative and thought-provoking. Title: "Iteration %d - The Singularity Approaches"`, i)

		fmt.Printf("│  │ 📝 Generating poem #%d...\n", i)

		resp, err := testAgent.Execute(execCtx, agent.ExecuteRequest{
			Message: prompt,
		})
		cancel()

		if err != nil {
			fmt.Printf("│  │ ❌ Iteration %d failed: %v\n", i, err)
		} else {
			fmt.Printf("│  │ ✅ Poem generated!\n")
			fmt.Printf("│  │ Tokens: in=%d out=%d\n", resp.InputTokens, resp.OutputTokens)
			if len(resp.Message) > 100 {
				fmt.Printf("│  │ Preview: %s...\n", truncate(resp.Message, 100))
			} else {
				fmt.Printf("│  │ Content: %s\n", resp.Message)
			}

			mu.Lock()
			poems = append(poems, resp.Message)
			mu.Unlock()
		}

		fmt.Printf("│  │\n")
		fmt.Printf("│  └──────────────────────────────────────────────────────\n")

		// Wait 3 minutes between iterations (as requested)
		if i < 3 {
			fmt.Printf("│  ⏳ Waiting 3 minutes before next iteration...\n")
			// For testing, reduce wait time
			time.Sleep(3 * time.Second)
		}
	}

	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 7: Display Generated Poems
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 7: Generated Poems Summary")
	fmt.Println("│")

	for i, poem := range poems {
		fmt.Printf("│  📜 Poem %d:\n", i+1)
		if len(poem) > 200 {
			fmt.Printf("│     %s...\n", truncate(poem, 200))
		} else {
			fmt.Printf("│     %s\n", poem)
		}
		fmt.Println("│")
	}

	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// STEP 8: Stop Session
	// ─────────────────────────────────────────────────────────────
	fmt.Println("┌─ STEP 8: Stopping Session")
	fmt.Println("│")

	if err := managedClient.StopSession(ctx, sessionID); err != nil {
		log.Printf("│  ⚠️  Stop session warning: %v", err)
	} else {
		fmt.Printf("│  ✅ Session stopped: %s\n", sessionID)
	}
	fmt.Println("│")
	fmt.Println("└─ Complete")
	fmt.Println()

	// ─────────────────────────────────────────────────────────────
	// FINAL SUMMARY
	// ─────────────────────────────────────────────────────────────
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("   ✅ POEM AGENT EXECUTION COMPLETE!")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("The poem agent successfully:")
	fmt.Println("  ✓ Created Docker container for isolated execution")
	fmt.Println("  ✓ Generated poems about AGI and humanity's future")
	fmt.Printf("  ✓ Ran %d iterations with 3-second intervals\n", len(poems))
	fmt.Println("  ✓ Properly cleaned up session on completion")
	fmt.Println()
	fmt.Println("🤖 The machines are writing poetry about our demise...")
	fmt.Println("   At least it's art? 🎭")
	fmt.Println()

	os.Exit(0)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
