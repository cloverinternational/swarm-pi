// Package chat provides comprehensive rendering validation tests.
// This test loads real SDK conversations and validates background color continuity
// across all tool renderers by examining the raw ANSI escape sequences.
package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkconv "github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/bash"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/edit"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/generic"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/grep"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/patch"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/read"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/todo"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/websearch"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/write"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
	"github.com/charmbracelet/x/ansi"
)

// TestRenderingBackgroundContinuity is the main test that loads real conversations,
// renders them through the actual TUI pipeline, and outputs ANSI for inspection.
//
// Usage:
//  1. Run test: go test -v ./internal/chat -run TestRenderingBackgroundContinuity
//  2. Check output: ls -la testdata/output/
//  3. Inspect annotated files to find background breaks
//
// The test loads conversations from ~/.swarmos/conversations/ or testdata/conversations/
func TestRenderingBackgroundContinuity(t *testing.T) {
	if os.Getenv("RUN_CHAT_INTEGRATION_TESTS") != "1" {
		t.Skip("Skipping chat integration test (reads local conversations, writes testdata/output); set RUN_CHAT_INTEGRATION_TESTS=1 to run")
	}

	// Create output directory
	outputDir := "testdata/output"
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output directory: %v", err)
	}

	// Find conversation files to test
	convFiles := findConversationFiles(t)
	if len(convFiles) == 0 {
		t.Skip("No conversation files found. Place JSON files in testdata/conversations/ or use real conversations from ~/.swarmos/conversations/")
	}

	t.Logf("Found %d conversation files to test", len(convFiles))

	// Create rendering harness
	harness := NewRenderingTestHarness(100) // 100 char width

	// Test each conversation
	for _, convFile := range convFiles {
		convName := strings.TrimSuffix(filepath.Base(convFile), ".json")
		t.Run(convName, func(t *testing.T) {
			testConversationRendering(t, harness, convFile, convName, outputDir)
		})
	}
}

// testConversationRendering tests a single conversation in both modes
func testConversationRendering(t *testing.T, harness *RenderingTestHarness, convFile, convName, outputDir string) {
	// Load SDK conversation
	sdkConv, err := loadSDKConversation(convFile)
	if err != nil {
		t.Fatalf("Failed to load conversation %s: %v", convFile, err)
	}

	// Convert to TUI format
	tuiMessages := convertSDKToTUIMessages(sdkConv.Messages)
	t.Logf("Loaded conversation with %d messages", len(tuiMessages))

	// Test both modes
	for _, mode := range []struct {
		name    string
		verbose bool
	}{
		{"normal", true},
		{"compact", false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			// Render conversation
			lines := harness.RenderConversation(tuiMessages, mode.verbose)
			t.Logf("Rendered %d lines in %s mode", len(lines), mode.name)

			// Create output
			output := &TestRenderingOutput{
				ConversationName: convName,
				Mode:             mode.name,
				Lines:            lines,
				RawANSI:          strings.Join(lines, "\n"),
			}

			// Save raw ANSI
			if err := output.SaveToFile(outputDir); err != nil {
				t.Errorf("Failed to save raw ANSI: %v", err)
			}

			// Save annotated for debugging
			if err := output.SaveAnnotated(outputDir); err != nil {
				t.Errorf("Failed to save annotated output: %v", err)
			}

			t.Logf("✓ Output saved to %s/%s_%s.*", outputDir, convName, mode.name)
		})
	}
}

// findConversationFiles finds conversation JSON files to test
func findConversationFiles(t *testing.T) []string {
	var files []string

	// Check testdata/conversations/ first
	testdataDir := "testdata/conversations"
	if entries, err := os.ReadDir(testdataDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				files = append(files, filepath.Join(testdataDir, entry.Name()))
			}
		}
	}

	// If no test fixtures, check real conversations directory
	if len(files) == 0 {
		home, err := os.UserHomeDir()
		if err == nil {
			convDir := filepath.Join(home, ".swarmos", "conversations")
			if entries, err := os.ReadDir(convDir); err == nil {
				// Limit to first 5 conversations to keep test reasonable
				count := 0
				for _, entry := range entries {
					if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") && count < 5 {
						files = append(files, filepath.Join(convDir, entry.Name()))
						count++
					}
				}
			}
		}
	}

	return files
}

// loadSDKConversation loads a conversation from SDK JSON format
func loadSDKConversation(path string) (*sdkconv.Conversation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var conv sdkconv.Conversation
	if err := json.Unmarshal(data, &conv); err != nil {
		return nil, fmt.Errorf("unmarshal JSON: %w", err)
	}

	return &conv, nil
}

// convertSDKToTUIMessages converts SDK Message format to TUI Message format
func convertSDKToTUIMessages(sdkMessages []*sdkconv.Message) []Message {
	tuiMessages := make([]Message, 0, len(sdkMessages))

	for _, sdkMsg := range sdkMessages {
		tuiMsg := Message{
			Role:      string(sdkMsg.Role),
			Content:   sdkMsg.Content,
			Timestamp: sdkMsg.Timestamp,
			Thinking:  sdkMsg.Thinking,
		}

		// Build OrderedBlocks from SDK message structure
		blocks := make([]MessageBlock, 0)
		seq := 1

		// Add thinking block if present
		if sdkMsg.Thinking != "" {
			blocks = append(blocks, MessageBlock{
				Type:     "thinking",
				Content:  sdkMsg.Thinking,
				Sequence: seq,
			})
			seq++
		}

		// Add content block if present
		if sdkMsg.Content != "" {
			blocks = append(blocks, MessageBlock{
				Type:     "content",
				Content:  sdkMsg.Content,
				Sequence: seq,
			})
			seq++
		}

		// Add tool calls
		for _, tc := range sdkMsg.ToolCalls {
			blocks = append(blocks, MessageBlock{
				Type: "tool_call",
				ToolCall: &ToolCallDisplay{
					ID:         tc.ID,
					Name:       tc.Name,
					Parameters: tc.Parameters,
				},
				Sequence: seq,
			})
			seq++
		}

		// Add tool results
		for _, tr := range sdkMsg.ToolResults {
			errMsg := ""
			if tr.Error != nil {
				errMsg = tr.Error.Message
			}
			blocks = append(blocks, MessageBlock{
				Type: "tool_result",
				ToolResult: &ToolResultDisplay{
					CallID:   tr.CallID,
					Output:   tr.Output,
					Error:    errMsg,
					ToolName: tr.Name,
				},
				Sequence: seq,
			})
			seq++
		}

		tuiMsg.OrderedBlocks = blocks
		tuiMessages = append(tuiMessages, tuiMsg)
	}

	return tuiMessages
}

// RenderingTestHarness sets up a minimal App for rendering tests
type RenderingTestHarness struct {
	app          *App
	toolRegistry *toolrender.Registry
	theme        Theme
	width        int
}

// NewRenderingTestHarness creates a test harness with full rendering pipeline
func NewRenderingTestHarness(width int) *RenderingTestHarness {
	// Create theme
	theme := Theme{
		Primary:    palette.Accent,
		PrimaryDim: palette.AccentDim,
		Secondary:  palette.AccentSoft,
		Accent:     palette.Teal,
		Success:    palette.Success,
		Error:      palette.Error,
		Warning:    palette.Warning,
		Info:       palette.Info,
		Text:       palette.Text,
		TextMuted:  palette.TextMuted,
		TextDim:    palette.TextDim,
		BG:         palette.Surface,
		BGLight:    palette.Panel,
		BGLighter:  palette.PanelAlt,
		Border:     palette.Border,
	}

	// Create tool registry with all renderers
	registry := toolrender.NewRegistry()

	// Register all tool renderers (order matters - first match wins)
	registry.Register(read.New())
	registry.Register(write.New())
	registry.Register(edit.New())
	registry.Register(bash.New())
	registry.Register(grep.New())
	registry.Register(patch.New())
	registry.Register(todo.New())
	registry.Register(websearch.New())
	registry.Register(generic.New()) // Fallback

	// Create minimal App instance
	app := &App{
		theme:        theme,
		toolRegistry: registry,
		width:        width,
		height:       1000, // Large enough for full rendering
		renderSettings: &RenderSettings{
			ShowFullToolOutput: true, // Normal mode by default
			Colors: struct {
				ToolCall   string `json:"tool_call"`
				ToolOutput string `json:"tool_output"`
				ToolError  string `json:"tool_error"`
				Connector  string `json:"connector"`
			}{
				ToolCall:   palette.Accent,
				ToolOutput: palette.Success,
				ToolError:  palette.Error,
				Connector:  palette.TextMuted,
			},
		},
		showThinking:       false, // Don't show thinking by default
		showFullToolOutput: true,
		collapseManager:    NewCollapseManager(),
		collapseWidget:     NewDefaultCollapseWidget(),
		toolNameResolver:   NewMCPToolNameResolver(),
		animationClock:     NewAnimationClock(),
	}

	return &RenderingTestHarness{
		app:          app,
		toolRegistry: registry,
		theme:        theme,
		width:        width,
	}
}

// RenderConversation renders messages using the actual TUI pipeline
func (h *RenderingTestHarness) RenderConversation(messages []Message, verbose bool) []string {
	// Set verbose mode
	h.app.showFullToolOutput = verbose
	h.app.renderSettings.ShowFullToolOutput = verbose
	h.app.messages = messages

	// Create render context (using simplified structure for testing)
	ctx := MessageRenderContext{
		StartIndex:          0,
		FocusedMessageIndex: -1, // No focus
		MessageNavMode:      false,
		IsActiveMessage:     false, // No streaming
		IsPreview:           false,
		Width:               h.width,
		EditMessageMode:     false,
		EditMessageIdx:      -1,
	}

	// Call actual rendering pipeline
	return h.app.renderMessageListWithContext(messages, ctx)
}

// TestRenderingOutput captures rendered output for inspection
type TestRenderingOutput struct {
	ConversationName string
	Mode             string
	Lines            []string
	RawANSI          string
}

// SaveToFile saves raw ANSI output
func (o *TestRenderingOutput) SaveToFile(dir string) error {
	filename := filepath.Join(dir, fmt.Sprintf("%s_%s.ansi", o.ConversationName, o.Mode))
	return os.WriteFile(filename, []byte(o.RawANSI), 0644)
}

// SaveAnnotated saves annotated output with visual + ANSI side-by-side
func (o *TestRenderingOutput) SaveAnnotated(dir string) error {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("=== %s (mode: %s) ===\n", o.ConversationName, o.Mode))
	buf.WriteString(fmt.Sprintf("Total lines: %d\n\n", len(o.Lines)))

	for i, line := range o.Lines {
		buf.WriteString(fmt.Sprintf("Line %d:\n", i+1))
		buf.WriteString("  Visual: ")
		buf.WriteString(ansi.Strip(line))
		buf.WriteString("\n")
		buf.WriteString("  ANSI:   ")
		buf.WriteString(showVisibleANSI(line))
		buf.WriteString("\n\n")
	}

	filename := filepath.Join(dir, fmt.Sprintf("%s_%s_annotated.txt", o.ConversationName, o.Mode))
	return os.WriteFile(filename, []byte(buf.String()), 0644)
}

// showVisibleANSI makes ANSI escape codes visible for debugging
func showVisibleANSI(s string) string {
	// Replace ESC with visible character
	s = strings.ReplaceAll(s, "\x1b", "␛")
	return s
}

// Mock implementations for test harness

type mockWorkspaceManager struct {
	root string
}

func (m *mockWorkspaceManager) IsWithinWorkspace(path string) bool {
	return strings.HasPrefix(path, m.root)
}

func (m *mockWorkspaceManager) GetWorkspaceRoot() string {
	return m.root
}

func (m *mockWorkspaceManager) ResolvePath(path string) (string, error) {
	return filepath.Join(m.root, path), nil
}

type mockToolNameResolver struct{}

func (m *mockToolNameResolver) Resolve(name string) string {
	return name
}

type mockAnimationClock struct{}

func (m *mockAnimationClock) Frame() int {
	return 0
}

func (m *mockAnimationClock) ViewCompact(frame int) string {
	return ""
}
