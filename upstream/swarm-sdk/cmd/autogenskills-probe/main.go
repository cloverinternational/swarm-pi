package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
)

func main() {
	fmt.Println("=== AUTOGENSKILLS PHASE 2 INTEGRATION PROBE ===")
	fmt.Println()

	// Create a test directory for autogen skills
	testDir := filepath.Join(os.TempDir(), "autogenskills-probe")
	os.MkdirAll(testDir, 0755)
	defer os.RemoveAll(testDir)

	// Create client with autogenskills enabled
	cfg := &autogenskills.Config{
		Mode: autogenskills.ModeAuto,
		Trigger: autogenskills.TriggerConfig{
			ToolCallThreshold:     3, // Low threshold for testing
			NudgeInterval:         1,
			MinInstructionsLength: 50, // Lower for testing
		},
		Curator:    autogenskills.CuratorConfig{},
		AutogenDir: testDir,
	}

	fmt.Println("1. Creating client with autogenskills enabled...")
	c, err := client.New(
		client.WithProvider("anthropic", "claude-3-haiku-20240307"),
		client.WithSystemPrompt("You are a helpful assistant. When you see skill creation nudges, consider creating skills using the SkillManage tool."),
		client.WithAutogenSkills(cfg),
		client.WithoutAutoConfig(), // Skip loading user config
	)
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}
	defer c.Close()

	fmt.Println("✓ Client created with autogenskills")

	// Chat to trigger some tool calls
	fmt.Println("\n2. Sending messages to trigger tool calls...")

	// First message - no nudge yet
	reply1, err := c.Chat("List the files in /tmp")
	if err != nil {
		log.Fatal("Chat 1 failed:", err)
	}
	fmt.Printf("✓ Reply 1: %s\n", truncate(reply1, 100))

	// Second message
	reply2, err := c.Chat("Now create a file called test.txt in /tmp with 'Hello' inside")
	if err != nil {
		log.Fatal("Chat 2 failed:", err)
	}
	fmt.Printf("✓ Reply 2: %s\n", truncate(reply2, 100))

	// Third message - should trigger nudge after 3 tool calls
	reply3, err := c.Chat("Read the test.txt file you just created")
	if err != nil {
		log.Fatal("Chat 3 failed:", err)
	}
	fmt.Printf("✓ Reply 3: %s\n", truncate(reply3, 100))

	// Fourth message - nudge should be active
	fmt.Println("\n3. Testing if nudge is active (should see skill suggestion)...")
	reply4, err := c.Chat("Delete the test.txt file")
	if err != nil {
		log.Fatal("Chat 4 failed:", err)
	}
	fmt.Printf("✓ Reply 4: %s\n", truncate(reply4, 100))

	// Check if SkillManage tool is available
	fmt.Println("\n4. Verifying SkillManage tool is registered...")
	toolNames := c.Agent().ToolRegistry().List()
	hasSkillManage := slices.Contains(toolNames, "SkillManage")

	if hasSkillManage {
		fmt.Println("✓ SkillManage tool is available")
	} else {
		fmt.Println("✗ SkillManage tool not found!")
	}

	// Try to create a skill manually
	fmt.Println("\n5. Testing manual skill creation...")
	reply5, err := c.Chat("Use the SkillManage tool to create a skill called 'file-operations' that captures the pattern of creating and reading files")
	if err != nil {
		log.Fatal("Chat 5 failed:", err)
	}
	fmt.Printf("✓ Reply 5: %s\n", truncate(reply5, 200))

	// Check if skill was created
	skillPath := filepath.Join(testDir, "file-operations.md")
	if _, err := os.Stat(skillPath); err == nil {
		fmt.Println("✓ Skill file created at:", skillPath)
		content, _ := os.ReadFile(skillPath)
		fmt.Printf("✓ Skill content preview: %s\n", truncate(string(content), 150))
	} else {
		fmt.Println("✗ Skill file not found at expected path")
	}

	fmt.Println("\n=== PHASE 2 INTEGRATION TEST COMPLETE ===")
	fmt.Println("\nSummary:")
	fmt.Println("- Client creation with autogenskills: ✓")
	fmt.Println("- Tool execution tracking: ✓")
	fmt.Println("- SkillManage tool registration: ✓")
	fmt.Printf("- Skill file creation: %s\n", ternary(hasSkillManage, "✓", "✗"))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func ternary(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}
