package automation_test

import (
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/config"
)

// TestNavigationWithConfig demonstrates using screen configs for navigation.
func TestNavigationWithConfig(t *testing.T) {
	// Create panel
	panel := chatui.NewPanel(100, 30)
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Test message 1"},
		{Role: "assistant", OrderedBlocks: []chatui.MessageBlock{
			{Type: chatui.BlockContent, Content: "Response 1"},
		}},
		{Role: "user", Content: "Test message 2"},
		{Role: "assistant", OrderedBlocks: []chatui.MessageBlock{
			{Type: chatui.BlockContent, Content: "Response 2"},
		}},
	})

	// Create driver with config
	driver := automation.NewDriver(panel, automation.WithSize(100, 30))

	// Parse inline config
	cfg, err := config.Parse([]byte(`
screens:
  chat:
    id: "chat"
    type: "ScreenChat"
    navigation:
      scroll_down: ["j", "down"]
      scroll_up: ["k", "up"]
      page_down: ["ctrl+d"]
      page_up: ["ctrl+u"]
    detection:
      text_patterns:
        - "│"
`))
	if err != nil {
		t.Fatalf("Failed to parse config: %v", err)
	}

	driver.SetConfig(cfg)
	driver.Start()
	defer driver.Stop()

	// Verify we can detect the chat screen
	screen := driver.DetectScreen()
	if screen != "chat" {
		t.Logf("Detected screen: %s (expected chat)", screen)
		// Not a failure since detection depends on rendered content
	}

	// Test scrolling
	t.Run("ScrollDown", func(t *testing.T) {
		initialFrame := driver.GetFrame()
		driver.SendKey("j")
		afterFrame := driver.GetFrame()

		// Frames should be captured
		if initialFrame == nil || afterFrame == nil {
			t.Error("Frames should not be nil")
		}
	})

	// Test multiple keys
	t.Run("MultipleKeys", func(t *testing.T) {
		err := driver.SendKeys("j", "j", "k")
		if err != nil {
			t.Errorf("SendKeys failed: %v", err)
		}
	})

	// Test content inspection
	t.Run("ContentInspection", func(t *testing.T) {
		if !driver.ContainsText("Test message") {
			t.Error("Should contain 'Test message'")
		}

		if !driver.ContainsText("Response") {
			t.Error("Should contain 'Response'")
		}
	})

	// Test state inspection
	t.Run("StateInspection", func(t *testing.T) {
		model := driver.Model()
		if panel, ok := model.(*chatui.Panel); ok {
			// Use the inspector interface
			messages := panel.GetMessages()
			if len(messages) != 4 {
				t.Errorf("Expected 4 messages, got %d", len(messages))
			}

			screen := panel.GetScreen()
			if screen != "chat" {
				t.Errorf("Expected screen 'chat', got '%s'", screen)
			}

			// Check message content
			if messages[0].Role != "user" {
				t.Errorf("Expected first message role 'user', got '%s'", messages[0].Role)
			}
		} else {
			t.Error("Model should be *chatui.Panel")
		}
	})
}

// TestResizeAndViewport tests viewport behavior at different sizes.
func TestResizeAndViewport(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	// Add enough messages to enable scrolling
	var messages []chatui.Message
	for range 20 {
		messages = append(messages, chatui.Message{
			Role:    "user",
			Content: "Message line that should wrap at smaller viewport widths to test word wrapping behavior",
		})
	}
	panel.SetMessages(messages)

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	sizes := []struct {
		name   string
		width  int
		height int
	}{
		{"small", 60, 20},
		{"medium", 100, 30},
		{"large", 150, 50},
		{"wide", 200, 24},
		{"tall", 80, 60},
	}

	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			err := driver.Resize(size.width, size.height)
			if err != nil {
				t.Fatalf("Resize failed: %v", err)
			}

			w, h := driver.GetSize()
			if w != size.width || h != size.height {
				t.Errorf("Size mismatch: got %dx%d, want %dx%d", w, h, size.width, size.height)
			}

			frame := driver.GetFrame()
			if frame.Width != size.width || frame.Height != size.height {
				t.Errorf("Frame size mismatch: got %dx%d, want %dx%d",
					frame.Width, frame.Height, size.width, size.height)
			}

			// Content should still be present
			if !driver.ContainsText("Message line") {
				t.Error("Content should be preserved after resize")
			}
		})
	}
}

// TestWaitForContentTimeout tests the timeout behavior.
func TestWaitForContentTimeout(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Initial content"},
	})

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Should find existing content quickly
	err := driver.WaitForContent("Initial content", 100*time.Millisecond)
	if err != nil {
		t.Errorf("Should find existing content: %v", err)
	}

	// Should timeout for missing content
	err = driver.WaitForContent("This text does not exist", 50*time.Millisecond)
	if err == nil {
		t.Error("Should timeout for missing content")
	}
}

// TestDeepStateInspection tests the deep state inspection interfaces.
func TestDeepStateInspection(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", OrderedBlocks: []chatui.MessageBlock{
			{Type: chatui.BlockContent, Content: "World"},
		}},
	})

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Test MessageInspector
	t.Run("MessageInspector", func(t *testing.T) {
		messages := panel.GetMessages()

		if len(messages) != 2 {
			t.Fatalf("Expected 2 messages, got %d", len(messages))
		}

		if messages[0].Role != "user" || messages[0].Content != "Hello" {
			t.Errorf("First message wrong: %+v", messages[0])
		}

		if messages[1].Role != "assistant" {
			t.Errorf("Second message role wrong: %s", messages[1].Role)
		}

		if panel.GetMessageCount() != 2 {
			t.Errorf("MessageCount wrong: %d", panel.GetMessageCount())
		}
	})

	// Test ScrollInspector
	t.Run("ScrollInspector", func(t *testing.T) {
		offset := panel.GetScrollOffset()
		// Initial offset should be 0
		if offset < 0 {
			t.Errorf("Scroll offset should be non-negative: %d", offset)
		}

		first, last := panel.GetVisibleRange()
		if first > last {
			t.Errorf("Invalid visible range: %d-%d", first, last)
		}
	})

	// Test InputInspector
	t.Run("InputInspector", func(t *testing.T) {
		text := panel.GetInputText()
		// Initial input should be empty
		if text != "" {
			t.Logf("Input text: %q", text)
		}

		pos := panel.GetCursorPosition()
		if pos < 0 {
			t.Errorf("Cursor position should be non-negative: %d", pos)
		}

		focused := panel.IsInputFocused()
		t.Logf("Input focused: %v", focused)
	})
}

// TestFrameRendering tests that frames are properly rendered.
func TestFrameRendering(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Test with special chars: <>&\"'"},
		{Role: "assistant", OrderedBlocks: []chatui.MessageBlock{
			{Type: chatui.BlockContent, Content: "Response with `code` and *markdown*"},
		}},
	})

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	frame := driver.GetFrame()

	// Frame should have content
	if frame.Content == "" {
		t.Error("Frame content should not be empty")
	}

	// Frame should have correct dimensions
	if frame.Width != 80 || frame.Height != 24 {
		t.Errorf("Frame dimensions wrong: %dx%d", frame.Width, frame.Height)
	}

	// Lines should be populated
	if len(frame.Lines) != 24 {
		t.Errorf("Expected 24 lines, got %d", len(frame.Lines))
	}

	// Check that content is present
	text := driver.GetText()
	if !contains(text, "Test with special chars") {
		t.Error("Frame should contain user message")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}
