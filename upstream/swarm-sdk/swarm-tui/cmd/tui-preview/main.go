// Package main implements a terminal viewport previewer for the TUI automation system.
// It connects to a running automation server and displays real-time frame updates.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/headless/automation/client"
)

func main() {
	var (
		address  = flag.String("addr", "/tmp/tui-automation.sock", "Server address (host:port or /path/to/socket)")
		width    = flag.Int("width", 120, "Viewport width")
		height   = flag.Int("height", 40, "Viewport height")
		follow   = flag.Bool("follow", false, "Follow frame updates in real-time")
		once     = flag.Bool("once", false, "Get single frame and exit")
		raw      = flag.Bool("raw", false, "Output raw content (no border)")
		stats    = flag.Bool("stats", false, "Show frame statistics")
		border   = flag.Bool("border", true, "Show viewport border")
		clear    = flag.Bool("clear", true, "Clear screen before each frame (in follow mode)")
		interval = flag.Duration("interval", 100*time.Millisecond, "Poll interval (non-follow mode)")
		timeout  = flag.Duration("timeout", 5*time.Second, "Connection timeout")
		export   = flag.String("export", "", "Export frame to file (supports .txt, .html)")
	)
	flag.Parse()

	// Create client
	c := client.New(*address)

	// Connect with timeout using context
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Try to connect with timeout
	connectDone := make(chan error, 1)
	go func() {
		connectDone <- c.Connect()
	}()

	select {
	case err := <-connectDone:
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to connect to %s: %v\n", *address, err)
			fmt.Fprintf(os.Stderr, "\nMake sure the TUI automation server is running.\n")
			fmt.Fprintf(os.Stderr, "Start it with: tui-automation -server -addr %s\n", *address)
			os.Exit(1)
		}
	case <-ctx.Done():
		fmt.Fprintf(os.Stderr, "Connection timeout after %v\n", *timeout)
		os.Exit(1)
	}
	defer c.Close()

	fmt.Fprintf(os.Stderr, "Connected to %s\n", *address)

	// Resize viewport if needed
	if *width > 0 && *height > 0 {
		if err := c.Resize(*width, *height); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: resize failed: %v\n", err)
		}
	}

	// Handle single frame mode
	if *once {
		frame, err := c.GetFrame()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get frame: %v\n", err)
			os.Exit(1)
		}
		outputFrame(frame, *raw, *stats, *border)
		exportFrameIfNeeded(frame, *export)
		return
	}

	// Handle follow mode
	if *follow {
		followFrames(c, *raw, *stats, *border, *clear)
		return
	}

	// Default: poll mode
	pollFrames(c, *interval, *raw, *stats, *border, *clear)
}

func followFrames(c *client.Client, raw, stats, border, clear bool) {
	// Set up signal handler
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Subscribe to frame updates
	if err := c.Subscribe("frame"); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to subscribe: %v\n", err)
		os.Exit(1)
	}

	// Set up frame handler
	frameCh := make(chan *client.Frame, 10)
	c.OnFrameUpdate(func(f *client.Frame) {
		select {
		case frameCh <- f:
		default:
			// Drop frame if channel is full
		}
	})

	// Start listening in background
	go c.Listen()

	fmt.Fprintf(os.Stderr, "Following frame updates (Ctrl+C to stop)...\n\n")

	for {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\nStopping...\n")
			c.Unsubscribe("frame")
			return
		case frame := <-frameCh:
			if clear {
				clearScreen()
			}
			outputFrame(frame, raw, stats, border)
		}
	}
}

func pollFrames(c *client.Client, interval time.Duration, raw, stats, border, clear bool) {
	// Set up signal handler
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	fmt.Fprintf(os.Stderr, "Polling frames every %v (Ctrl+C to stop)...\n\n", interval)

	var lastContent string
	for {
		select {
		case <-sigCh:
			fmt.Fprintf(os.Stderr, "\nStopping...\n")
			return
		case <-ticker.C:
			frame, err := c.GetFrame()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error getting frame: %v\n", err)
				continue
			}

			// Only redraw if content changed
			if frame.Content != lastContent {
				if clear {
					clearScreen()
				}
				outputFrame(frame, raw, stats, border)
				lastContent = frame.Content
			}
		}
	}
}

func outputFrame(frame *client.Frame, raw, stats, border bool) {
	if frame == nil {
		fmt.Println("<no frame>")
		return
	}

	if stats {
		printStats(frame)
	}

	if raw {
		// Output raw content directly
		fmt.Print(frame.Content)
		if !strings.HasSuffix(frame.Content, "\n") {
			fmt.Println()
		}
		return
	}

	if border {
		printBorder(frame.Width, "top")
	}

	// Output lines with border
	for _, line := range frame.Lines {
		if border {
			fmt.Print("│")
		}
		fmt.Print(line)
		// Pad to width
		lineWidth := visibleWidth(line)
		if lineWidth < frame.Width {
			fmt.Print(strings.Repeat(" ", frame.Width-lineWidth))
		}
		if border {
			fmt.Print("│")
		}
		fmt.Println()
	}

	if border {
		printBorder(frame.Width, "bottom")
	}
}

// visibleWidth calculates the visible width of a string (excluding ANSI codes)
func visibleWidth(s string) int {
	width := 0
	inEscape := false

	for _, r := range s {
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		width++
	}

	return width
}

func printStats(frame *client.Frame) {
	fmt.Printf("╭─ Frame Statistics ─────────────────────────╮\n")
	fmt.Printf("│ Size: %3dx%-3d                              │\n", frame.Width, frame.Height)
	fmt.Printf("│ Frame: #%-5d                              │\n", frame.FrameNum)
	fmt.Printf("│ Render time: %-12v                  │\n", frame.RenderTime)
	fmt.Printf("│ Lines: %-3d                                 │\n", len(frame.Lines))
	fmt.Printf("╰────────────────────────────────────────────╯\n\n")
}

func printBorder(width int, position string) {
	if position == "top" {
		fmt.Print("╭")
		fmt.Print(strings.Repeat("─", width))
		fmt.Println("╮")
	} else {
		fmt.Print("╰")
		fmt.Print(strings.Repeat("─", width))
		fmt.Println("╯")
	}
}

func clearScreen() {
	fmt.Print("\x1b[2J\x1b[H")
}

func exportFrameIfNeeded(frame *client.Frame, path string) {
	if path == "" {
		return
	}

	var err error
	if strings.HasSuffix(path, ".html") {
		err = exportHTML(frame, path)
	} else {
		err = exportText(frame, path)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to export: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "Exported to %s\n", path)
	}
}

func exportText(frame *client.Frame, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.WriteString(frame.Content)
	return err
}

func exportHTML(frame *client.Frame, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Simple HTML with ANSI-to-HTML conversion
	fmt.Fprintf(f, `<!DOCTYPE html>
<html>
<head>
<title>TUI Viewport Preview</title>
<style>
body {
  background: #1a1b26;
  color: #c0caf5;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  font-size: 14px;
  line-height: 1.2;
  padding: 20px;
}
.viewport {
  white-space: pre;
  background: #1a1b26;
  border: 1px solid #3b4261;
  padding: 10px;
  border-radius: 8px;
}
.stats {
  margin-bottom: 20px;
  padding: 10px;
  background: #24283b;
  border-radius: 8px;
}
</style>
</head>
<body>
<h1>TUI Viewport Preview</h1>
<div class="stats">
<strong>Size:</strong> %dx%d | <strong>Frame:</strong> #%d | <strong>Render time:</strong> %v
</div>
<div class="viewport">
`, frame.Width, frame.Height, frame.FrameNum, frame.RenderTime)

	// Convert content to HTML (escape HTML entities)
	content := frame.Content
	content = strings.ReplaceAll(content, "&", "&amp;")
	content = strings.ReplaceAll(content, "<", "&lt;")
	content = strings.ReplaceAll(content, ">", "&gt;")

	fmt.Fprintf(f, "%s", content)

	fmt.Fprintf(f, `</div>
</body>
</html>
`)

	return nil
}
