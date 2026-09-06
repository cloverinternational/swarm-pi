// Script to fix orphaned conversations that are missing workspace_path metadata.
// This happens when compaction creates a new conversation without preserving metadata.
//
// Usage: go run fix_orphaned_conversations.go [--dry-run] [--workspace /path/to/default]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Conversation represents the conversation structure
type Conversation struct {
	ID                 string               `json:"id"`
	CreatedAt          time.Time            `json:"created_at"`
	UpdatedAt          time.Time            `json:"updated_at"`
	Mode               string               `json:"mode"`
	Status             string               `json:"status"`
	Messages           []Message            `json:"messages"`
	Metadata           ConversationMetadata `json:"metadata"`
	TotalTokens        int                  `json:"total_tokens"`
	CurrentContextSize int                  `json:"current_context_size"`
	TotalCostUSD       float64              `json:"total_cost_usd"`
	// Preserve other fields
	ModeHistory        []string       `json:"mode_history,omitempty"`
	OperatingModeState map[string]any `json:"operating_mode_state,omitempty"`
	AgentMemories      map[string]any `json:"agent_memories,omitempty"`
	GroupState         map[string]any `json:"group_state,omitempty"`
	ModeState          map[string]any `json:"mode_state,omitempty"`
	TraceID            string         `json:"trace_id,omitempty"`
}

// Message represents a conversation message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// Other fields preserved via json.RawMessage would be better, but keeping simple
}

// ConversationMetadata contains metadata
type ConversationMetadata struct {
	UserID    string         `json:"user_id,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Tags      []string       `json:"tags,omitempty"`
	Custom    map[string]any `json:"custom,omitempty"`
}

// Common project paths to look for
var knownWorkspaces = []string{
	"/home/rincon/Swarm/swarmos",
	"/home/rincon/Swarm/finetuning",
	"/home/rincon/Swarm/swarm-ide",
	"/home/rincon/Swarm/claude-code-router-mitm",
	"/home/rincon/Swarm",
	"/home/rincon/Code/DaysLeft",
	"/home/rincon/Code/llxprt-code",
	"/home/rincon/SwarmDJ",
	"/home/rincon",
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Don't actually modify files")
	defaultWorkspace := flag.String("workspace", "", "Default workspace path if cannot infer")
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("Error getting home dir: %v\n", err)
		os.Exit(1)
	}

	conversationsDir := filepath.Join(home, ".swarmos", "conversations")

	entries, err := os.ReadDir(conversationsDir)
	if err != nil {
		fmt.Printf("Error reading conversations dir: %v\n", err)
		os.Exit(1)
	}

	fixed := 0
	skipped := 0
	errors := 0

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}

		filePath := filepath.Join(conversationsDir, entry.Name())

		// Read conversation
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Printf("Error reading %s: %v\n", entry.Name(), err)
			errors++
			continue
		}

		var conv Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			fmt.Printf("Error parsing %s: %v\n", entry.Name(), err)
			errors++
			continue
		}

		// Check if already has workspace_path
		if conv.Metadata.Custom != nil {
			if ws, ok := conv.Metadata.Custom["workspace_path"].(string); ok && ws != "" {
				skipped++
				continue
			}
		}

		// Try to infer workspace from conversation content
		workspace := inferWorkspace(&conv)
		if workspace == "" && *defaultWorkspace != "" {
			workspace = *defaultWorkspace
		}

		if workspace == "" {
			fmt.Printf("SKIP %s: Cannot infer workspace (no file paths found)\n", conv.ID)
			skipped++
			continue
		}

		// Update metadata
		if conv.Metadata.Custom == nil {
			conv.Metadata.Custom = make(map[string]any)
		}
		conv.Metadata.Custom["workspace_path"] = workspace
		conv.Metadata.Custom["fixed_at"] = time.Now().Format(time.RFC3339)
		conv.Metadata.Custom["fixed_by"] = "fix_orphaned_conversations"

		// Add tags if missing
		if conv.Metadata.Tags == nil {
			conv.Metadata.Tags = []string{}
		}
		hasTag := slices.Contains(conv.Metadata.Tags, "tui")
		if !hasTag {
			conv.Metadata.Tags = append(conv.Metadata.Tags, "tui")
		}

		fmt.Printf("FIX %s: Setting workspace to %s\n", conv.ID, workspace)

		if *dryRun {
			fixed++
			continue
		}

		// Write back
		newData, err := json.MarshalIndent(conv, "", "  ")
		if err != nil {
			fmt.Printf("Error marshaling %s: %v\n", entry.Name(), err)
			errors++
			continue
		}

		if err := os.WriteFile(filePath, newData, 0644); err != nil {
			fmt.Printf("Error writing %s: %v\n", entry.Name(), err)
			errors++
			continue
		}

		fixed++
	}

	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("Fixed: %d\n", fixed)
	fmt.Printf("Skipped (already has workspace or cannot infer): %d\n", skipped)
	fmt.Printf("Errors: %d\n", errors)
	if *dryRun {
		fmt.Println("\n(Dry run - no files were modified)")
	}
}

// inferWorkspace tries to determine the workspace from file paths in messages
func inferWorkspace(conv *Conversation) string {
	// Regex to find file paths in content
	pathRegex := regexp.MustCompile(`(/home/[a-zA-Z0-9_]+/[^\s\n\r"'\x60\[\](){}]+)`)

	pathCounts := make(map[string]int)

	for _, msg := range conv.Messages {
		matches := pathRegex.FindAllString(msg.Content, -1)
		for _, match := range matches {
			// Find which known workspace this path belongs to
			for _, ws := range knownWorkspaces {
				if strings.HasPrefix(match, ws+"/") || match == ws {
					pathCounts[ws]++
					break
				}
			}
		}
	}

	// Find the most common workspace
	maxCount := 0
	bestWorkspace := ""
	for ws, count := range pathCounts {
		if count > maxCount {
			maxCount = count
			bestWorkspace = ws
		}
	}

	return bestWorkspace
}
