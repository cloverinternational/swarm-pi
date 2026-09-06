// Package x11 provides X11-specific implementation of computer control.
// It uses xdotool for input, ImageMagick for screenshots, and xclip for clipboard.
package x11

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// Backend implements InputBackend for X11 display servers.
type Backend struct {
	display string

	// Cached display info
	displays    []computeruse.DisplayGeometry
	displaysMux sync.RWMutex

	// Tool availability flags
	hasXdtool   bool
	hasImport   bool
	hasXclip    bool
	hasXsel     bool
	hasXrandr   bool
	hasXwininfo bool
	hasXprop    bool

	// Options
	opts Options
}

// Options configures the X11 backend.
type Options struct {
	// Display is the X11 display to use (e.g., ":0").
	// If empty, uses $DISPLAY environment variable.
	Display string

	// ScreenshotQuality is the JPEG quality for screenshots (0-100).
	ScreenshotQuality int

	// MouseAnimation enables smooth mouse movement.
	MouseAnimation bool

	// AnimationFPS is the frames per second for mouse animation.
	AnimationFPS int

	// MoveSettleMs is the delay after mouse movement before action.
	MoveSettleMs int
}

// DefaultOptions returns the default options.
func DefaultOptions() Options {
	return Options{
		Display:           "",
		ScreenshotQuality: 75,
		MouseAnimation:    true,
		AnimationFPS:      60,
		MoveSettleMs:      50,
	}
}

// NewBackend creates a new X11 backend.
// It checks for required tools and initializes the display connection.
func NewBackend(opts Options) (*Backend, error) {
	b := &Backend{
		opts: opts,
	}

	// Get display from options or environment
	if opts.Display != "" {
		b.display = opts.Display
	} else {
		b.display = os.Getenv("DISPLAY")
	}

	if b.display == "" {
		return nil, computeruse.ErrNoDisplay
	}

	// Check for required tools
	b.hasXdtool = isToolAvailable("xdotool")
	b.hasImport = isToolAvailable("import")
	b.hasXclip = isToolAvailable("xclip")
	b.hasXsel = isToolAvailable("xsel")
	b.hasXrandr = isToolAvailable("xrandr")
	b.hasXwininfo = isToolAvailable("xwininfo")
	b.hasXprop = isToolAvailable("xprop")

	// Verify minimum requirements
	if !b.hasXdtool {
		return nil, computeruse.NewToolNotAvailableError("xdotool")
	}
	if !b.hasImport {
		return nil, computeruse.NewToolNotAvailableError("import (ImageMagick)")
	}
	if !b.hasXclip && !b.hasXsel {
		return nil, computeruse.NewToolNotAvailableError("xclip or xsel")
	}

	// Initialize display info
	displays, err := b.listDisplaysInternal()
	if err != nil {
		return nil, fmt.Errorf("failed to get display info: %w", err)
	}
	b.displays = displays

	return b, nil
}

// Close releases resources.
func (b *Backend) Close() error {
	return nil
}

// isToolAvailable checks if a command-line tool is available.
func isToolAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// runCommand executes a command and returns its output.
func (b *Backend) runCommand(name string, args ...string) (string, error) {
	ctx := context.Background()
	return b.runCommandWithContext(ctx, name, args...)
}

// runCommandWithContext executes a command with context.
func (b *Backend) runCommandWithContext(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	// Set DISPLAY environment
	cmd.Env = os.Environ()
	if b.display != "" {
		cmd.Env = append(cmd.Env, "DISPLAY="+b.display)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("command %s failed: %w: %s", name, err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}

// runCommandWithInput executes a command with stdin input.
func (b *Backend) runCommandWithInput(name string, input string, args ...string) error {
	ctx := context.Background()
	return b.runCommandWithInputContext(ctx, name, input, args...)
}

// runCommandWithInputContext executes a command with stdin input and context.
func (b *Backend) runCommandWithInputContext(ctx context.Context, name string, input string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)

	// Set DISPLAY environment
	cmd.Env = os.Environ()
	if b.display != "" {
		cmd.Env = append(cmd.Env, "DISPLAY="+b.display)
	}

	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command %s failed: %w: %s", name, err, string(output))
	}

	return nil
}
