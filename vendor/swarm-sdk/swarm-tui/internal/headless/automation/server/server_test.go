package server_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
)

func TestServerClientIntegration(t *testing.T) {
	// Create a unique socket path for this test
	sockPath := filepath.Join(os.TempDir(), "tui_test_"+time.Now().Format("20060102150405")+".sock")
	defer os.Remove(sockPath)

	// Create panel with messages
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Hello!"},
		{Role: "assistant", OrderedBlocks: []chatui.MessageBlock{
			{Type: chatui.BlockContent, Content: "Hi there!"},
		}},
	})

	// Start server
	srv := server.New(panel, server.Config{
		Address: "unix://" + sockPath,
		Width:   80,
		Height:  24,
	})

	ctx := t.Context()

	serverReady := make(chan struct{})
	serverErr := make(chan error, 1)

	go func() {
		close(serverReady)
		if err := srv.Start(ctx); err != nil {
			serverErr <- err
		}
	}()

	<-serverReady
	time.Sleep(100 * time.Millisecond) // Wait for server to be listening

	// Create and connect client
	cli := client.New("unix://" + sockPath)
	if err := cli.Connect(); err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer cli.Close()

	// Test GetFrame
	t.Run("GetFrame", func(t *testing.T) {
		frame, err := cli.GetFrame()
		if err != nil {
			t.Fatalf("GetFrame failed: %v", err)
		}

		if frame.Width != 80 || frame.Height != 24 {
			t.Errorf("Frame dimensions wrong: %dx%d", frame.Width, frame.Height)
		}

		if frame.Content == "" {
			t.Error("Frame content is empty")
		}

		t.Logf("Frame content length: %d", len(frame.Content))
	})

	// Test GetState (deep introspection)
	t.Run("GetState", func(t *testing.T) {
		state, err := cli.GetState()
		if err != nil {
			t.Fatalf("GetState failed: %v", err)
		}

		if state.Screen != "chat" {
			t.Errorf("Expected screen 'chat', got '%s'", state.Screen)
		}

		if len(state.Messages) != 2 {
			t.Errorf("Expected 2 messages, got %d", len(state.Messages))
		}

		if state.Messages[0].Role != "user" {
			t.Errorf("Expected first message role 'user', got '%s'", state.Messages[0].Role)
		}

		if state.Messages[0].Content != "Hello!" {
			t.Errorf("Expected first message content 'Hello!', got '%s'", state.Messages[0].Content)
		}

		t.Logf("State: screen=%s, messages=%d", state.Screen, len(state.Messages))
	})

	// Test SendKey
	t.Run("SendKey", func(t *testing.T) {
		if err := cli.SendKey("j"); err != nil {
			t.Fatalf("SendKey failed: %v", err)
		}
	})

	// Test Resize
	t.Run("Resize", func(t *testing.T) {
		if err := cli.Resize(120, 40); err != nil {
			t.Fatalf("Resize failed: %v", err)
		}

		frame, err := cli.GetFrame()
		if err != nil {
			t.Fatalf("GetFrame after resize failed: %v", err)
		}

		if frame.Width != 120 || frame.Height != 40 {
			t.Errorf("Frame dimensions after resize wrong: %dx%d", frame.Width, frame.Height)
		}
	})

	// Test ContainsText
	t.Run("ContainsText", func(t *testing.T) {
		contains, err := cli.ContainsText("Hi there")
		if err != nil {
			t.Fatalf("ContainsText failed: %v", err)
		}

		if !contains {
			t.Error("Expected frame to contain 'Hi there'")
		}
	})

	// Test GetMessages (convenience method)
	t.Run("GetMessages", func(t *testing.T) {
		messages, err := cli.GetMessages()
		if err != nil {
			t.Fatalf("GetMessages failed: %v", err)
		}

		if len(messages) != 2 {
			t.Errorf("Expected 2 messages, got %d", len(messages))
		}
	})
}

func TestServerDirectAccess(t *testing.T) {
	// Test that we can use the server's driver directly for testing
	panel := chatui.NewPanel(80, 24)
	panel.SetMessages([]chatui.Message{
		{Role: "user", Content: "Test"},
	})

	srv := server.New(panel, server.Config{
		Width:  80,
		Height: 24,
	})

	driver := srv.GetDriver()

	// Start the driver directly (without network)
	if err := driver.Start(); err != nil {
		t.Fatalf("Driver start failed: %v", err)
	}
	defer driver.Stop()

	// Verify frame capture works
	frame := driver.GetFrame()
	if frame == nil {
		t.Fatal("Frame is nil")
	}

	if !driver.ContainsText("Test") {
		t.Error("Expected frame to contain 'Test'")
	}
}
