// Package main provides CLI commands for Claude Code import/export functionality.
package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/converters/claude"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// cmdImportClaude imports conversations from Claude Code format
func cmdImportClaude(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	converter := claude.NewClaudeCodeConverter()

	// Check if importing a single file or a project
	if config.InputFile != "" {
		// Import single conversation file
		fmt.Printf("Importing Claude conversation from: %s\n", config.InputFile)

		conv, err := converter.ImportSingleConversation(config.InputFile)
		if err != nil {
			return fmt.Errorf("failed to import conversation: %w", err)
		}

		// Save to storage
		err = store.Save(ctx, conv)
		if err != nil {
			return fmt.Errorf("failed to save conversation: %w", err)
		}

		fmt.Printf("Successfully imported conversation: %s\n", conv.ID)
		if title, ok := conv.Metadata.Custom["title"].(string); ok && title != "" {
			fmt.Printf("Title: %s\n", title)
		}
		fmt.Printf("Messages: %d\n", len(conv.Messages))
		fmt.Printf("Total Tokens: %d\n", conv.TotalTokens)

		return nil
	}

	if config.ProjectPath != "" {
		// Import all conversations from a Claude project
		fmt.Printf("Importing Claude project from: %s\n", config.ProjectPath)

		conversations, err := converter.ImportProjectConversations(config.ProjectPath)
		if err != nil {
			return fmt.Errorf("failed to import project: %w", err)
		}

		// Save all conversations
		successCount := 0
		errors := []string{}

		for _, conv := range conversations {
			err = store.Save(ctx, conv)
			if err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", conv.ID, err))
				continue
			}
			successCount++
		}

		fmt.Printf("\nImport Summary:\n")
		fmt.Printf("Total conversations found: %d\n", len(conversations))
		fmt.Printf("Successfully imported: %d\n", successCount)

		if len(errors) > 0 {
			fmt.Printf("\nErrors:\n")
			for _, e := range errors {
				fmt.Printf("  - %s\n", e)
			}
		}

		return nil
	}

	// List available Claude projects if no input specified
	projects, err := converter.ListClaudeProjects()
	if err != nil {
		return fmt.Errorf("failed to list Claude projects: %w", err)
	}

	if len(projects) == 0 {
		fmt.Println("No Claude projects found.")
		fmt.Println("\nUsage:")
		fmt.Println("  Import single file:  import-claude -file ~/.claude/projects/*/conversations/conv-id.json")
		fmt.Println("  Import project:      import-claude -project /path/to/project")
		return nil
	}

	fmt.Println("Available Claude projects:")
	for _, project := range projects {
		fmt.Printf("  %s\n", project)
	}
	fmt.Println("\nUse 'import-claude -project <path>' to import a project")

	return nil
}

// cmdExportClaude exports conversations to Claude Code format
func cmdExportClaude(ctx context.Context, config *Config, logger observability.Logger, tracer observability.Tracer) error {
	// Create storage
	store, err := createStorage(config.StoragePath)
	if err != nil {
		return fmt.Errorf("failed to create storage: %w", err)
	}

	converter := claude.NewClaudeCodeConverter()

	// Export single conversation
	if config.ConversationID != "" {
		// Load conversation
		conv, err := store.Load(ctx, config.ConversationID)
		if err != nil {
			return fmt.Errorf("failed to load conversation: %w", err)
		}

		// Determine output file
		outputFile := config.OutputFile
		if outputFile == "" {
			// Default to stdout-friendly name
			outputFile = fmt.Sprintf("%s-claude.json", conv.ID)
		}

		// Export to Claude format
		err = converter.ExportSingleConversation(conv, outputFile)
		if err != nil {
			return fmt.Errorf("failed to export conversation: %w", err)
		}

		fmt.Printf("Successfully exported conversation to: %s\n", outputFile)
		return nil
	}

	// Export all conversations for a workspace/project
	if config.WorkspacePath != "" || config.ProjectPath != "" {
		targetPath := config.WorkspacePath
		if targetPath == "" {
			targetPath = config.ProjectPath
		}

		// List all conversations
		convs, err := store.List(ctx)
		if err != nil {
			return fmt.Errorf("failed to list conversations: %w", err)
		}

		// Filter by workspace path if specified
		filtered := make([]*conversation.Conversation, 0)
		for _, conv := range convs {
			if targetPath == "" || conv.WorkspacePath == targetPath {
				filtered = append(filtered, conv)
			}
		}

		if len(filtered) == 0 {
			fmt.Printf("No conversations found for workspace: %s\n", targetPath)
			return nil
		}

		fmt.Printf("Found %d conversations to export\n", len(filtered))

		// Export to Claude project format
		err = converter.ExportToProject(filtered, targetPath)
		if err != nil {
			return fmt.Errorf("failed to export to project: %w", err)
		}

		projectID := pathToProjectID(targetPath)
		fmt.Printf("\nSuccessfully exported to Claude project: %s\n", projectID)
		fmt.Printf("Location: ~/.claude/projects/%s/\n", projectID)

		return nil
	}

	// List conversations if no specific ID provided
	convs, err := store.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list conversations: %w", err)
	}

	if len(convs) == 0 {
		fmt.Println("No conversations found.")
		return nil
	}

	fmt.Println("Available conversations:")
	for _, conv := range convs {
		fmt.Printf("  %s", conv.ID)
		if title, ok := conv.Metadata.Custom["title"].(string); ok && title != "" {
			fmt.Printf(" - %s", title)
		}
		fmt.Printf(" (%d messages)\n", len(conv.Messages))
	}
	fmt.Println("\nUse 'export-claude -id <conversation-id>' to export a specific conversation")
	fmt.Println("Use 'export-claude -workspace <path>' to export all conversations for a workspace")

	return nil
}

// Helper function to convert path to project ID (matches converter logic)
func pathToProjectID(path string) string {
	normalized := filepath.Clean(path)
	if normalized == "/" {
		return "-"
	}
	components := strings.Split(normalized, string(filepath.Separator))
	filtered := make([]string, 0)
	for _, comp := range components {
		if comp != "" {
			filtered = append(filtered, comp)
		}
	}
	return "-" + strings.Join(filtered, "-")
}
