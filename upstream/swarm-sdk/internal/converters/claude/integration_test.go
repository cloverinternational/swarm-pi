//go:build integration

package claude_test

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/converters/claude"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// TestClaudeCodeIntegration tests the full import/export flow with the manager
func TestClaudeCodeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create temporary directories
	tempDir, err := ioutil.TempDir("", "claude-integration-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	claudeDir := filepath.Join(tempDir, ".claude")
	swarmDir := filepath.Join(tempDir, ".swarm")

	// Create test Claude conversation
	claudeConv := &claude.ClaudeConversation{
		ID:         "test-integration-conv",
		Title:      "Integration Test Conversation",
		Status:     "active",
		Mode:       "plan",
		TokenCount: 500,
		CreatedAt:  time.Now().Unix() * 1000,
		UpdatedAt:  time.Now().Unix() * 1000,
		ProjectID:  "-test-integration-project",
		Messages: []claude.ClaudeMessage{
			{
				Role:       "user",
				Content:    "This is an integration test",
				TokenCount: 10,
				Timestamp:  time.Now().Unix() * 1000,
			},
			{
				Role:       "assistant",
				Content:    "I understand this is a test",
				TokenCount: 15,
				Timestamp:  time.Now().Add(1*time.Minute).Unix() * 1000,
			},
		},
	}

	// Create Claude project directory structure
	projectDir := filepath.Join(claudeDir, "projects", claudeConv.ProjectID)
	convDir := filepath.Join(projectDir, "memory", "conversations")
	err = os.MkdirAll(convDir, 0755)
	if err != nil {
		t.Fatalf("failed to create Claude directory structure: %v", err)
	}

	// Save Claude conversation
	convPath := filepath.Join(convDir, claudeConv.ID+".json")
	data, err := json.MarshalIndent(claudeConv, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal Claude conversation: %v", err)
	}
	err = ioutil.WriteFile(convPath, data, 0644)
	if err != nil {
		t.Fatalf("failed to write Claude conversation: %v", err)
	}

	// Create Swarm storage and manager
	store, err := storage.NewFileStorage(storage.FileStorageConfig{
		BaseDir:     filepath.Join(swarmDir, "conversations"),
		CacheSize:   100,
		AutoCompact: false,
	})
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	logger := noop.NewLogger()
	tracer := noop.NewTracer()

	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()

	t.Run("import from Claude format", func(t *testing.T) {
		// Read the Claude file
		data, err := ioutil.ReadFile(convPath)
		if err != nil {
			t.Fatalf("failed to read Claude file: %v", err)
		}

		// Import using manager
		conv, err := mgr.Import(ctx, data, manager.ExportFormatClaudeCode)
		if err != nil {
			t.Fatalf("failed to import Claude conversation: %v", err)
		}

		// Verify imported data
		if conv.ID != claudeConv.ID {
			t.Errorf("ID mismatch: got %s, want %s", conv.ID, claudeConv.ID)
		}

		if title, ok := conv.Metadata.Custom["title"].(string); !ok || title != claudeConv.Title {
			t.Errorf("Title mismatch: got %v, want %s", conv.Metadata.Custom["title"], claudeConv.Title)
		}

		if len(conv.Messages) != len(claudeConv.Messages) {
			t.Errorf("Message count mismatch: got %d, want %d", len(conv.Messages), len(claudeConv.Messages))
		}
	})

	t.Run("export to Claude format", func(t *testing.T) {
		// Export the conversation
		exported, err := mgr.Export(ctx, claudeConv.ID, manager.ExportFormatClaudeCode)
		if err != nil {
			t.Fatalf("failed to export to Claude format: %v", err)
		}

		// Parse exported data
		var exportedConv claude.ClaudeConversation
		err = json.Unmarshal(exported, &exportedConv)
		if err != nil {
			t.Fatalf("failed to parse exported Claude conversation: %v", err)
		}

		// Verify exported data
		if exportedConv.ID != claudeConv.ID {
			t.Errorf("Exported ID mismatch: got %s, want %s", exportedConv.ID, claudeConv.ID)
		}

		if exportedConv.Title != claudeConv.Title {
			t.Errorf("Exported title mismatch: got %s, want %s", exportedConv.Title, claudeConv.Title)
		}

		if len(exportedConv.Messages) != len(claudeConv.Messages) {
			t.Errorf("Exported message count mismatch: got %d, want %d",
				len(exportedConv.Messages), len(claudeConv.Messages))
		}
	})

	t.Run("batch project operations", func(t *testing.T) {
		// Create a second conversation
		conv2 := &conversation.Conversation{
			ID:            "test-conv-2",
			Mode:          "act",
			Status:        conversation.StatusActive,
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			WorkspacePath: "/test/integration/project",
			Messages:      []*conversation.Message{},
			Metadata: conversation.ConversationMetadata{
				Custom: map[string]any{
					"title": "Second Test Conversation",
				},
			},
		}

		// Save to storage
		err = store.Save(ctx, conv2)
		if err != nil {
			t.Fatalf("failed to save second conversation: %v", err)
		}

		// Create converter with custom base directory
		converter := claude.NewClaudeCodeConverterWithBaseDir(claudeDir)

		// Load both conversations
		convs, err := store.List(ctx)
		if err != nil {
			t.Fatalf("failed to list conversations: %v", err)
		}

		// Export to Claude project
		err = converter.ExportToProject(convs, "/test/batch/export")
		if err != nil {
			t.Fatalf("failed to export to Claude project: %v", err)
		}

		// Verify files were created
		exportProjectDir := filepath.Join(claudeDir, "projects", "-test-batch-export")
		exportConvDir := filepath.Join(exportProjectDir, "memory", "conversations")

		// Check first conversation file
		conv1Path := filepath.Join(exportConvDir, claudeConv.ID+".json")
		if _, err := os.Stat(conv1Path); os.IsNotExist(err) {
			t.Error("first conversation file not created in batch export")
		}

		// Check second conversation file
		conv2Path := filepath.Join(exportConvDir, conv2.ID+".json")
		if _, err := os.Stat(conv2Path); os.IsNotExist(err) {
			t.Error("second conversation file not created in batch export")
		}

		// Check sessions index
		indexPath := filepath.Join(exportProjectDir, "sessions-index.json")
		if _, err := os.Stat(indexPath); os.IsNotExist(err) {
			t.Error("sessions-index.json not created in batch export")
		}

		// Import back from the exported project
		imported, err := converter.ImportProjectConversations("/test/batch/export")
		if err != nil {
			t.Fatalf("failed to import from Claude project: %v", err)
		}

		if len(imported) != 2 {
			t.Errorf("imported conversation count mismatch: got %d, want 2", len(imported))
		}
	})
}

// TestManagerWithClaudeFormat tests that the manager correctly handles Claude format
func TestManagerWithClaudeFormat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Create temporary directory
	tempDir, err := ioutil.TempDir("", "claude-manager-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create storage and manager
	store, err := storage.NewFileStorage(storage.FileStorageConfig{
		BaseDir: filepath.Join(tempDir, "conversations"),
	})
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	logger := noop.NewLogger()
	tracer := noop.NewTracer()

	mgr, err := manager.NewManager(manager.Config{
		Storage: store,
		Logger:  logger,
		Tracer:  tracer,
	})
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	ctx := context.Background()

	// Create a test conversation
	conv := &conversation.Conversation{
		ID:        "mgr-test-conv",
		Mode:      "plan",
		Status:    conversation.StatusActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Messages: []*conversation.Message{
			{
				ID:        "msg1",
				Timestamp: time.Now(),
				Role:      conversation.RoleUser,
				Content:   "Test message for manager",
				Tokens: &conversation.TokenUsage{
					Input:  10,
					Output: 0,
					Total:  10,
				},
			},
		},
		Metadata: conversation.ConversationMetadata{
			UserID:    "test-user",
			ProjectID: "test-project",
			Custom: map[string]any{
				"title": "Manager Test Conversation",
			},
		},
	}

	// Save conversation
	err = store.Save(ctx, conv)
	if err != nil {
		t.Fatalf("failed to save conversation: %v", err)
	}

	// Export as Claude format through manager
	claudeData, err := mgr.Export(ctx, conv.ID, manager.ExportFormatClaudeCode)
	if err != nil {
		t.Fatalf("failed to export as Claude format: %v", err)
	}

	// Verify it's valid Claude format
	var claudeConv claude.ClaudeConversation
	err = json.Unmarshal(claudeData, &claudeConv)
	if err != nil {
		t.Fatalf("failed to parse Claude format: %v", err)
	}

	if claudeConv.ID != conv.ID {
		t.Errorf("Claude format ID mismatch: got %s, want %s", claudeConv.ID, conv.ID)
	}

	// Import Claude format back through manager
	imported, err := mgr.Import(ctx, claudeData, manager.ExportFormatClaudeCode)
	if err != nil {
		t.Fatalf("failed to import Claude format: %v", err)
	}

	if imported.ID != conv.ID {
		t.Errorf("Imported ID mismatch: got %s, want %s", imported.ID, conv.ID)
	}

	// Verify title preservation
	if title, ok := imported.Metadata.Custom["title"].(string); !ok || title != "Manager Test Conversation" {
		t.Errorf("Title not preserved after round trip: got %v", imported.Metadata.Custom["title"])
	}
}
