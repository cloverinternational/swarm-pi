package automation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation"
)

func TestDriverLifecycle(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	driver := automation.NewDriver(panel, automation.WithSize(80, 24))

	// Should not be started yet
	if driver.IsStarted() {
		t.Error("Driver should not be started before Start()")
	}

	// Start
	if err := driver.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if !driver.IsStarted() {
		t.Error("Driver should be started after Start()")
	}

	// Double start should fail
	if err := driver.Start(); err == nil {
		t.Error("Double Start should fail")
	}

	// Stop
	if err := driver.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	if driver.IsStarted() {
		t.Error("Driver should not be started after Stop()")
	}

	// Double stop should fail
	if err := driver.Stop(); err == nil {
		t.Error("Double Stop should fail")
	}
}

func TestDriverSendKey(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Send a key
	if err := driver.SendKey("down"); err != nil {
		t.Fatalf("SendKey failed: %v", err)
	}

	// Should have captured a frame
	frame := driver.GetFrame()
	if frame == nil {
		t.Fatal("GetFrame returned nil")
	}

	if frame.Width != 80 || frame.Height != 24 {
		t.Errorf("Frame dimensions wrong: %dx%d", frame.Width, frame.Height)
	}
}

func TestDriverSendKeys(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Send multiple keys
	if err := driver.SendKeys("down", "down", "up"); err != nil {
		t.Fatalf("SendKeys failed: %v", err)
	}

	// Should have made updates
	if driver.GetUpdateCount() < 3 {
		t.Errorf("Expected at least 3 updates, got %d", driver.GetUpdateCount())
	}
}

func TestDriverResize(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Resize
	if err := driver.Resize(120, 40); err != nil {
		t.Fatalf("Resize failed: %v", err)
	}

	w, h := driver.GetSize()
	if w != 120 || h != 40 {
		t.Errorf("Size wrong after resize: %dx%d", w, h)
	}

	frame := driver.GetFrame()
	if frame.Width != 120 || frame.Height != 40 {
		t.Errorf("Frame dimensions wrong after resize: %dx%d", frame.Width, frame.Height)
	}
}

func TestDriverWithMessages(t *testing.T) {
	// Use larger viewport to ensure all messages fit
	panel := chatui.NewPanel(80, 40)

	// Add some messages
	// Note: User messages use msg.Content, assistant messages use OrderedBlocks
	panel.SetMessages([]chatui.Message{
		{
			Role:    "user",
			Content: "Hello, world!",
		},
		{
			Role: "assistant",
			OrderedBlocks: []chatui.MessageBlock{
				{Type: chatui.BlockContent, Content: "Hi there!"},
			},
		},
	})

	driver := automation.NewDriver(panel, automation.WithSize(80, 40))
	driver.Start()
	defer driver.Stop()

	// Debug: Check what's being captured
	frame := driver.GetFrame()
	t.Logf("Frame content length: %d", len(frame.Content))
	t.Logf("Frame lines: %d", len(frame.Lines))

	// Print all lines for debugging
	for i, line := range frame.Lines {
		if line != "" {
			t.Logf("Line %d: %q", i, line)
		}
	}

	// Check model type
	model := driver.Model()
	t.Logf("Model type: %T", model)

	// Frame should contain message content
	// Note: User messages may render differently than assistant messages
	if !driver.ContainsText("Hi there") {
		t.Error("Frame should contain 'Hi there'")
	}

	// Check if Hello appears (user messages have different styling)
	if !driver.ContainsText("Hello") {
		// Log full content for debugging
		t.Logf("Full content:\n%s", frame.Content)
		t.Error("Frame should contain 'Hello'")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func TestDriverGetText(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{
			Role:    "user",
			Content: "Test message",
		},
	})

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	text := driver.GetText()
	if text == "" {
		t.Error("GetText returned empty")
	}

	if !strings.Contains(text, "Test message") {
		t.Errorf("Text should contain 'Test message', got: %s", text)
	}
}

func TestDriverWaitForContent(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{
			Role:    "user",
			Content: "Expected text",
		},
	})

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Should find immediately
	err := driver.WaitForContent("Expected text", 100*time.Millisecond)
	if err != nil {
		t.Errorf("WaitForContent failed: %v", err)
	}

	// Should timeout for missing text
	err = driver.WaitForContent("Missing text", 50*time.Millisecond)
	if err == nil {
		t.Error("WaitForContent should timeout for missing text")
	}
}

func TestDriverFrameCapture(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Initial frame should exist
	frame := driver.GetFrame()
	if frame == nil {
		t.Fatal("Initial frame is nil")
	}

	initialCount := driver.GetFrameCount()
	if initialCount < 1 {
		t.Error("Should have at least 1 frame after start")
	}

	// Send key should capture new frame
	driver.SendKey("j")

	if driver.GetFrameCount() <= initialCount {
		t.Error("Frame count should increase after SendKey")
	}
}

func TestDriverDifferentSizes(t *testing.T) {
	sizes := []struct {
		w, h int
	}{
		{80, 24},
		{120, 40},
		{40, 10},
		{200, 60},
	}

	for _, size := range sizes {
		t.Run(
			strings.ReplaceAll(
				strings.TrimSpace(
					strings.Replace(
						strings.Replace(
							"size_"+string(rune(size.w))+"x"+string(rune(size.h)),
							string(rune(size.w)), "", 1),
						string(rune(size.h)), "", 1),
				), " ", "_"),
			func(t *testing.T) {
				panel := chatui.NewPanel(size.w, size.h)
				driver := automation.NewDriver(panel, automation.WithSize(size.w, size.h))
				driver.Start()
				defer driver.Stop()

				w, h := driver.GetSize()
				if w != size.w || h != size.h {
					t.Errorf("Size mismatch: got %dx%d, want %dx%d", w, h, size.w, size.h)
				}

				frame := driver.GetFrame()
				if frame.Width != size.w || frame.Height != size.h {
					t.Errorf("Frame size mismatch: got %dx%d, want %dx%d",
						frame.Width, frame.Height, size.w, size.h)
				}
			})
	}
}

func TestDriverScrollKeys(t *testing.T) {
	panel := chatui.NewPanel(80, 24)

	// Add many messages to enable scrolling
	messages := make([]chatui.Message, 20)
	for i := range messages {
		messages[i] = chatui.Message{
			Role:    "user",
			Content: strings.Repeat("Line content ", 5),
		}
	}
	panel.SetMessages(messages)

	driver := automation.NewDriver(panel, automation.WithSize(80, 24))
	driver.Start()
	defer driver.Stop()

	// Scroll down
	initialFrame := driver.GetFrame()
	driver.SendKey("j")
	afterScroll := driver.GetFrame()

	// Content should potentially change (depends on viewport implementation)
	_ = initialFrame
	_ = afterScroll
}

func TestDriverNotStartedErrors(t *testing.T) {
	panel := chatui.NewPanel(80, 24)
	driver := automation.NewDriver(panel)

	// All operations should fail before Start
	if err := driver.SendKey("a"); err == nil {
		t.Error("SendKey should fail before Start")
	}

	if err := driver.SendKeys("a", "b"); err == nil {
		t.Error("SendKeys should fail before Start")
	}

	if err := driver.SendText("hello"); err == nil {
		t.Error("SendText should fail before Start")
	}

	if err := driver.Resize(100, 50); err == nil {
		t.Error("Resize should fail before Start")
	}

	if err := driver.Click(10, 10); err == nil {
		t.Error("Click should fail before Start")
	}

	if err := driver.Scroll(10, 10, true); err == nil {
		t.Error("Scroll should fail before Start")
	}
}
