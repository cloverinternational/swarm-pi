package claude_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/converters/claude"
)

// Test data
var (
	testClaudeConv = &claude.ClaudeConversation{
		ID:         "test-conv-123",
		Title:      "Test Conversation",
		Status:     "active",
		Mode:       "plan",
		TokenCount: 1500,
		CreatedAt:  1710693600000, // 2024-03-17 18:00:00 UTC in milliseconds
		UpdatedAt:  1710697200000, // 2024-03-17 19:00:00 UTC in milliseconds
		ProjectID:  "-home-user-project",
		Messages: []claude.ClaudeMessage{
			{
				Role:       "user",
				Content:    "Hello, how are you?",
				TokenCount: 5,
				Timestamp:  1710693600000,
			},
			{
				Role:       "assistant",
				Content:    "I'm doing well, thank you!",
				TokenCount: 7,
				Timestamp:  1710693660000,
				ToolUse: []claude.ClaudeToolUse{
					{
						Name: "test_tool",
						Input: map[string]any{
							"param": "value",
						},
						Result: "Tool executed successfully",
						Status: "completed",
					},
				},
			},
		},
	}

	testSwarmConv = &conversation.Conversation{
		ID:                 "swarm-conv-456",
		Mode:               "act",
		Status:             conversation.StatusActive,
		CreatedAt:          time.Now().Add(-1 * time.Hour),
		UpdatedAt:          time.Now(),
		WorkspacePath:      "/home/user/workspace",
		TotalTokens:        2000,
		CurrentContextSize: 1800,
		Metadata: conversation.ConversationMetadata{
			UserID:    "user123",
			ProjectID: "-home-user-workspace",
			Tags:      []string{"test", "example"},
			Custom: map[string]any{
				"title": "Swarm Test Conversation",
			},
		},
		Messages: []*conversation.Message{
			{
				ID:        "msg_1",
				Timestamp: time.Now().Add(-30 * time.Minute),
				Role:      conversation.RoleUser,
				Content:   "What's the weather like?",
				Tokens: &conversation.TokenUsage{
					Input:  10,
					Output: 0,
					Total:  10,
				},
			},
			{
				ID:        "msg_2",
				Timestamp: time.Now().Add(-29 * time.Minute),
				Role:      conversation.RoleAssistant,
				Content:   "I'll check the weather for you.",
				Tokens: &conversation.TokenUsage{
					Input:  15,
					Output: 8,
					Total:  23,
				},
				ToolCalls: []conversation.ToolCall{
					{
						ID:   "call_1",
						Name: "weather_check",
						Parameters: map[string]any{
							"location": "New York",
						},
					},
				},
				ToolResults: []conversation.ToolResult{
					{
						CallID: "call_1",
						Name:   "weather_check",
						Output: "Sunny, 72°F",
					},
				},
			},
		},
	}
)

func TestImportConversation(t *testing.T) {
	converter := claude.NewClaudeCodeConverter()

	t.Run("successful import", func(t *testing.T) {
		conv, err := converter.ImportConversation(testClaudeConv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify basic fields
		if conv.ID != testClaudeConv.ID {
			t.Errorf("ID mismatch: got %s, want %s", conv.ID, testClaudeConv.ID)
		}
		if conv.Mode != testClaudeConv.Mode {
			t.Errorf("Mode mismatch: got %s, want %s", conv.Mode, testClaudeConv.Mode)
		}
		if conv.Status != conversation.StatusActive {
			t.Errorf("Status mismatch: got %s, want %s", conv.Status, conversation.StatusActive)
		}

		// Verify title in custom metadata
		if title, ok := conv.Metadata.Custom["title"].(string); !ok || title != testClaudeConv.Title {
			t.Errorf("Title mismatch: got %v, want %s", conv.Metadata.Custom["title"], testClaudeConv.Title)
		}

		// Verify workspace path conversion
		expectedPath := "/home/user/project"
		if conv.WorkspacePath != expectedPath {
			t.Errorf("WorkspacePath mismatch: got %s, want %s", conv.WorkspacePath, expectedPath)
		}

		// Verify message count
		if len(conv.Messages) != len(testClaudeConv.Messages) {
			t.Fatalf("Message count mismatch: got %d, want %d", len(conv.Messages), len(testClaudeConv.Messages))
		}

		// Verify first message
		msg := conv.Messages[0]
		if msg.Role != conversation.RoleUser {
			t.Errorf("First message role mismatch: got %s, want %s", msg.Role, conversation.RoleUser)
		}
		if msg.Content != testClaudeConv.Messages[0].Content {
			t.Errorf("First message content mismatch: got %s, want %s", msg.Content, testClaudeConv.Messages[0].Content)
		}

		// Verify tool conversion
		assistantMsg := conv.Messages[1]
		if len(assistantMsg.ToolCalls) != 1 {
			t.Errorf("ToolCalls count mismatch: got %d, want 1", len(assistantMsg.ToolCalls))
		}
		if len(assistantMsg.ToolResults) != 1 {
			t.Errorf("ToolResults count mismatch: got %d, want 1", len(assistantMsg.ToolResults))
		}
	})

	t.Run("nil input", func(t *testing.T) {
		_, err := converter.ImportConversation(nil)
		if err == nil {
			t.Error("expected error for nil input")
		}
	})
}

func TestExportConversation(t *testing.T) {
	converter := claude.NewClaudeCodeConverter()

	t.Run("successful export", func(t *testing.T) {
		claudeConv, err := converter.ExportConversation(testSwarmConv)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Verify basic fields
		if claudeConv.ID != testSwarmConv.ID {
			t.Errorf("ID mismatch: got %s, want %s", claudeConv.ID, testSwarmConv.ID)
		}
		if claudeConv.Mode != testSwarmConv.Mode {
			t.Errorf("Mode mismatch: got %s, want %s", claudeConv.Mode, testSwarmConv.Mode)
		}

		// Verify title extraction
		expectedTitle := "Swarm Test Conversation"
		if claudeConv.Title != expectedTitle {
			t.Errorf("Title mismatch: got %s, want %s", claudeConv.Title, expectedTitle)
		}

		// Verify project ID conversion
		expectedProjectID := "-home-user-workspace"
		if claudeConv.ProjectID != expectedProjectID {
			t.Errorf("ProjectID mismatch: got %s, want %s", claudeConv.ProjectID, expectedProjectID)
		}

		// Verify message count
		if len(claudeConv.Messages) != len(testSwarmConv.Messages) {
			t.Fatalf("Message count mismatch: got %d, want %d", len(claudeConv.Messages), len(testSwarmConv.Messages))
		}

		// Verify tool conversion
		assistantMsg := claudeConv.Messages[1]
		if len(assistantMsg.ToolUse) != 1 {
			t.Errorf("ToolUse count mismatch: got %d, want 1", len(assistantMsg.ToolUse))
		}
		if assistantMsg.ToolUse[0].Name != "weather_check" {
			t.Errorf("Tool name mismatch: got %s, want weather_check", assistantMsg.ToolUse[0].Name)
		}
	})

	t.Run("nil input", func(t *testing.T) {
		_, err := converter.ExportConversation(nil)
		if err == nil {
			t.Error("expected error for nil input")
		}
	})
}

func TestRoundTripConversion(t *testing.T) {
	converter := claude.NewClaudeCodeConverter()

	t.Run("Claude -> Swarm -> Claude", func(t *testing.T) {
		// Import from Claude format
		swarmConv, err := converter.ImportConversation(testClaudeConv)
		if err != nil {
			t.Fatalf("import failed: %v", err)
		}

		// Export back to Claude format
		claudeConv2, err := converter.ExportConversation(swarmConv)
		if err != nil {
			t.Fatalf("export failed: %v", err)
		}

		// Verify key fields are preserved
		if claudeConv2.ID != testClaudeConv.ID {
			t.Errorf("ID not preserved: got %s, want %s", claudeConv2.ID, testClaudeConv.ID)
		}
		if claudeConv2.Title != testClaudeConv.Title {
			t.Errorf("Title not preserved: got %s, want %s", claudeConv2.Title, testClaudeConv.Title)
		}
		if len(claudeConv2.Messages) != len(testClaudeConv.Messages) {
			t.Errorf("Message count not preserved: got %d, want %d",
				len(claudeConv2.Messages), len(testClaudeConv.Messages))
		}
	})

	t.Run("Swarm -> Claude -> Swarm", func(t *testing.T) {
		// Export to Claude format
		claudeConv, err := converter.ExportConversation(testSwarmConv)
		if err != nil {
			t.Fatalf("export failed: %v", err)
		}

		// Import back to Swarm format
		swarmConv2, err := converter.ImportConversation(claudeConv)
		if err != nil {
			t.Fatalf("import failed: %v", err)
		}

		// Verify key fields are preserved
		if swarmConv2.ID != testSwarmConv.ID {
			t.Errorf("ID not preserved: got %s, want %s", swarmConv2.ID, testSwarmConv.ID)
		}
		if swarmConv2.Mode != testSwarmConv.Mode {
			t.Errorf("Mode not preserved: got %s, want %s", swarmConv2.Mode, testSwarmConv.Mode)
		}
		if len(swarmConv2.Messages) != len(testSwarmConv.Messages) {
			t.Errorf("Message count not preserved: got %d, want %d",
				len(swarmConv2.Messages), len(testSwarmConv.Messages))
		}

		// Verify title is preserved
		if title, ok := swarmConv2.Metadata.Custom["title"].(string); !ok || title != "Swarm Test Conversation" {
			t.Errorf("Title not preserved: got %v", swarmConv2.Metadata.Custom["title"])
		}
	})
}

func TestPathConversion(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		projectID string
	}{
		{
			name:      "simple path",
			path:      "/home/user/project",
			projectID: "-home-user-project",
		},
		{
			name:      "deep path",
			path:      "/home/user/go/src/myproject",
			projectID: "-home-user-go-src-myproject",
		},
		{
			name:      "root path",
			path:      "/",
			projectID: "-",
		},
		{
			name:      "path with trailing slash",
			path:      "/home/user/project/",
			projectID: "-home-user-project",
		},
	}

	// Use reflection to access private methods
	// In a real implementation, these would be exported or tested through public methods

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test path to project ID conversion
			conv := &claude.ClaudeCodeConverter{}

			// Create a Swarm conversation with workspace path
			swarmConv := &conversation.Conversation{
				ID:            "test",
				WorkspacePath: tt.path,
				Mode:          "plan",
				Status:        conversation.StatusActive,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
				Messages:      []*conversation.Message{},
				Metadata: conversation.ConversationMetadata{
					Custom: make(map[string]any),
				},
			}

			claudeConv, err := conv.ExportConversation(swarmConv)
			if err != nil {
				t.Fatalf("export failed: %v", err)
			}

			if claudeConv.ProjectID != tt.projectID {
				t.Errorf("ProjectID mismatch: got %s, want %s", claudeConv.ProjectID, tt.projectID)
			}

			// Test project ID to path conversion
			claudeConv.ProjectID = tt.projectID
			swarmConv2, err := conv.ImportConversation(claudeConv)
			if err != nil {
				t.Fatalf("import failed: %v", err)
			}

			// Normalize expected path
			expectedPath := filepath.Clean(tt.path)
			if swarmConv2.WorkspacePath != expectedPath {
				t.Errorf("WorkspacePath mismatch: got %s, want %s", swarmConv2.WorkspacePath, expectedPath)
			}
		})
	}
}

func TestBatchOperations(t *testing.T) {
	// Create temporary test directory
	tempDir, err := os.MkdirTemp("", "claude-converter-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	converter := claude.NewClaudeCodeConverterWithBaseDir(tempDir)

	t.Run("export and import project", func(t *testing.T) {
		// Create test conversations
		conversations := []*conversation.Conversation{
			{
				ID:            "conv1",
				Mode:          "plan",
				Status:        conversation.StatusActive,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
				WorkspacePath: "/test/project",
				Messages:      []*conversation.Message{},
				Metadata: conversation.ConversationMetadata{
					Custom: map[string]any{
						"title": "First Conversation",
					},
				},
			},
			{
				ID:            "conv2",
				Mode:          "act",
				Status:        conversation.StatusCompleted,
				CreatedAt:     time.Now(),
				UpdatedAt:     time.Now(),
				WorkspacePath: "/test/project",
				Messages:      []*conversation.Message{},
				Metadata: conversation.ConversationMetadata{
					Custom: map[string]any{
						"title": "Second Conversation",
					},
				},
			},
		}

		// Export to project
		err := converter.ExportToProject(conversations, "/test/project")
		if err != nil {
			t.Fatalf("export to project failed: %v", err)
		}

		// Verify files were created
		projectDir := filepath.Join(tempDir, "projects", "-test-project")
		convDir := filepath.Join(projectDir, "memory", "conversations")

		// Check conversation files
		for _, conv := range conversations {
			filePath := filepath.Join(convDir, conv.ID+".json")
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				t.Errorf("conversation file not created: %s", filePath)
			}
		}

		// Check sessions index
		indexPath := filepath.Join(projectDir, "sessions-index.json")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			t.Error("sessions-index.json not created")
		}

		// Import from project
		imported, err := converter.ImportProjectConversations("/test/project")
		if err != nil {
			t.Fatalf("import from project failed: %v", err)
		}

		if len(imported) != len(conversations) {
			t.Errorf("imported conversation count mismatch: got %d, want %d",
				len(imported), len(conversations))
		}

		// Verify imported data
		for i, conv := range imported {
			if conv.ID != conversations[i].ID {
				t.Errorf("imported ID mismatch: got %s, want %s",
					conv.ID, conversations[i].ID)
			}
			if title, ok := conv.Metadata.Custom["title"].(string); ok {
				expectedTitle := conversations[i].Metadata.Custom["title"].(string)
				if title != expectedTitle {
					t.Errorf("imported title mismatch: got %s, want %s",
						title, expectedTitle)
				}
			}
		}
	})

	t.Run("import from non-existent project", func(t *testing.T) {
		_, err := converter.ImportProjectConversations("/non/existent/project")
		if err == nil {
			t.Error("expected error for non-existent project")
		}
	})

	t.Run("export empty conversations", func(t *testing.T) {
		err := converter.ExportToProject([]*conversation.Conversation{}, "/test/empty")
		if err == nil {
			t.Error("expected error for empty conversations")
		}
	})
}

func TestSingleFileOperations(t *testing.T) {
	// Create temporary test directory
	tempDir, err := os.MkdirTemp("", "claude-single-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	converter := claude.NewClaudeCodeConverter()

	t.Run("export and import single file", func(t *testing.T) {
		// Export single conversation
		filePath := filepath.Join(tempDir, "test-conv.json")
		err := converter.ExportSingleConversation(testSwarmConv, filePath)
		if err != nil {
			t.Fatalf("export single conversation failed: %v", err)
		}

		// Verify file exists
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			t.Error("exported file not created")
		}

		// Import single conversation
		imported, err := converter.ImportSingleConversation(filePath)
		if err != nil {
			t.Fatalf("import single conversation failed: %v", err)
		}

		// Verify imported data
		if imported.ID != testSwarmConv.ID {
			t.Errorf("imported ID mismatch: got %s, want %s", imported.ID, testSwarmConv.ID)
		}
	})

	t.Run("import non-existent file", func(t *testing.T) {
		_, err := converter.ImportSingleConversation("/non/existent/file.json")
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})
}

func TestListClaudeProjects(t *testing.T) {
	// Create temporary test directory
	tempDir, err := os.MkdirTemp("", "claude-list-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	converter := claude.NewClaudeCodeConverterWithBaseDir(tempDir)

	// Create some test projects
	projectsDir := filepath.Join(tempDir, "projects")
	testProjects := []string{
		"-home-user-project1",
		"-home-user-project2",
		"-var-data-app",
	}

	for _, proj := range testProjects {
		projDir := filepath.Join(projectsDir, proj)
		if err := os.MkdirAll(projDir, 0755); err != nil {
			t.Fatalf("failed to create test project: %v", err)
		}
	}

	// List projects
	projects, err := converter.ListClaudeProjects()
	if err != nil {
		t.Fatalf("list projects failed: %v", err)
	}

	if len(projects) != len(testProjects) {
		t.Errorf("project count mismatch: got %d, want %d", len(projects), len(testProjects))
	}

	// Verify project paths
	expectedPaths := []string{
		"/home/user/project1",
		"/home/user/project2",
		"/var/data/app",
	}

	for i, path := range projects {
		found := slices.Contains(expectedPaths, path)
		if !found {
			t.Errorf("unexpected project path: %s", projects[i])
		}
	}
}

func TestEdgeCases(t *testing.T) {
	converter := claude.NewClaudeCodeConverter()

	t.Run("conversation without messages", func(t *testing.T) {
		claudeConv := &claude.ClaudeConversation{
			ID:        "empty-conv",
			Status:    "active",
			Mode:      "plan",
			CreatedAt: time.Now().Unix() * 1000,
			UpdatedAt: time.Now().Unix() * 1000,
			Messages:  []claude.ClaudeMessage{},
		}

		conv, err := converter.ImportConversation(claudeConv)
		if err != nil {
			t.Fatalf("import failed: %v", err)
		}

		if len(conv.Messages) != 0 {
			t.Errorf("expected no messages, got %d", len(conv.Messages))
		}
	})

	t.Run("message with empty tool results", func(t *testing.T) {
		claudeMsg := claude.ClaudeMessage{
			Role:      "assistant",
			Content:   "Testing tools",
			Timestamp: time.Now().Unix() * 1000,
			ToolUse: []claude.ClaudeToolUse{
				{
					Name:  "test_tool",
					Input: map[string]any{},
					// No result
				},
			},
		}

		claudeConv := &claude.ClaudeConversation{
			ID:        "tool-test",
			Status:    "active",
			Mode:      "plan",
			CreatedAt: time.Now().Unix() * 1000,
			UpdatedAt: time.Now().Unix() * 1000,
			Messages:  []claude.ClaudeMessage{claudeMsg},
		}

		conv, err := converter.ImportConversation(claudeConv)
		if err != nil {
			t.Fatalf("import failed: %v", err)
		}

		if len(conv.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(conv.Messages))
		}

		msg := conv.Messages[0]
		if len(msg.ToolCalls) != 1 {
			t.Errorf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
		if len(msg.ToolResults) != 0 {
			t.Errorf("expected no tool results, got %d", len(msg.ToolResults))
		}
	})

	t.Run("failed tool execution", func(t *testing.T) {
		swarmMsg := &conversation.Message{
			ID:        "msg_fail",
			Timestamp: time.Now(),
			Role:      conversation.RoleAssistant,
			Content:   "Testing failed tool",
			ToolCalls: []conversation.ToolCall{
				{
					ID:         "call_fail",
					Name:       "failing_tool",
					Parameters: map[string]any{},
				},
			},
			ToolResults: []conversation.ToolResult{
				{
					CallID: "call_fail",
					Name:   "failing_tool",
					Error: &conversation.ToolError{
						Type:    "tool.error",
						Message: "Tool execution failed",
					},
				},
			},
		}

		swarmConv := &conversation.Conversation{
			ID:        "fail-conv",
			Mode:      "plan",
			Status:    conversation.StatusActive,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Messages:  []*conversation.Message{swarmMsg},
			Metadata: conversation.ConversationMetadata{
				Custom: make(map[string]any),
			},
		}

		claudeConv, err := converter.ExportConversation(swarmConv)
		if err != nil {
			t.Fatalf("export failed: %v", err)
		}

		if len(claudeConv.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(claudeConv.Messages))
		}

		claudeMsg := claudeConv.Messages[0]
		if len(claudeMsg.ToolUse) != 1 {
			t.Fatalf("expected 1 tool use, got %d", len(claudeMsg.ToolUse))
		}

		tool := claudeMsg.ToolUse[0]
		if tool.Status != "failed" {
			t.Errorf("expected failed status, got %s", tool.Status)
		}
		if tool.Result != "Error: Tool execution failed" {
			t.Errorf("unexpected error result: %v", tool.Result)
		}
	})
}
