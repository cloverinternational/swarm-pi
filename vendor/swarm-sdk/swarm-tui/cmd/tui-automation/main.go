// Command tui-automation runs the headless TUI automation server.
//
// Usage:
//
//	tui-automation [flags]
//
// Flags:
//
//	-addr string     Listen address (default "unix:///tmp/tui-automation.sock")
//	-width int       Initial terminal width (default 80)
//	-height int      Initial terminal height (default 24)
//	-config string   Screen configuration file
//	-demo            Run with demo panel instead of full app
//
// Example:
//
//	# Start server with Unix socket (full app)
//	tui-automation -addr unix:///tmp/tui.sock
//
//	# Start server with TCP
//	tui-automation -addr tcp://localhost:9999
//
//	# With custom size and config
//	tui-automation -width 120 -height 40 -config screens.yaml
//
//	# Demo mode with sample messages
//	tui-automation -demo
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/server"
	zone "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/zone"
)

func main() {
	addr := flag.String("addr", "unix:///tmp/tui-automation.sock", "Listen address")
	width := flag.Int("width", 80, "Initial terminal width")
	height := flag.Int("height", 24, "Initial terminal height")
	configPath := flag.String("config", "", "Screen configuration file")
	demo := flag.Bool("demo", false, "Run with demo panel instead of full app")
	flag.Parse()

	// Initialize bubblezone for mouse support (required for chat.App)
	zone.NewGlobal()

	var model tea.Model

	if *demo {
		// Create a panel with sample messages
		panel := chatui.NewPanel(*width, *height)
		panel.SetMessages([]chatui.Message{
			{
				Role:    "user",
				Content: "Hello! This is the TUI automation server.",
			},
			{
				Role: "assistant",
				OrderedBlocks: []chatui.MessageBlock{
					{Type: chatui.BlockContent, Content: "Welcome to the headless TUI automation system. You can:\n\n- Send keys and mouse events\n- Query rendered frames\n- Inspect deep model state\n- Navigate between screens"},
				},
			},
		})
		model = panel
		fmt.Println("Mode:       Demo (chatui.Panel)")
	} else {
		// Create the full chat app
		app := chat.NewApp()
		model = app
		fmt.Println("Mode:       Full App (chat.App)")
	}

	// Create server
	srv := server.New(model, server.Config{
		Address: *addr,
		Width:   *width,
		Height:  *height,
	})

	// Load config if provided
	if *configPath != "" {
		driver := srv.GetDriver()
		if err := driver.LoadConfig(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to load config: %v\n", err)
		}
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Print startup info
	fmt.Printf("TUI Automation Server\n")
	fmt.Printf("=====================\n")
	fmt.Printf("Address:    %s\n", *addr)
	fmt.Printf("Dimensions: %dx%d\n", *width, *height)
	if *configPath != "" {
		fmt.Printf("Config:     %s\n", *configPath)
	}
	fmt.Printf("\nPress Ctrl+C to stop.\n\n")

	// Start server
	if err := srv.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}
