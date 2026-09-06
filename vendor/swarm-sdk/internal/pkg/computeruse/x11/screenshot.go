package x11

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
)

// Screenshot captures the entire screen or a specific display.
func (b *Backend) Screenshot(displayID int, allowedApps []string) (*computeruse.ScreenshotResult, error) {
	// Get display geometry
	geom, err := b.GetDisplaySize(displayID)
	if err != nil {
		return nil, err
	}

	// Calculate target dimensions for API
	targetW, targetH := computeruse.TargetImageSize(geom.Width, geom.Height, 1920)

	// Build import command
	// import -window root -display :0 -crop WxH+X+Y -resize WxH -quality 75 jpeg:-
	args := []string{
		"-window", "root",
		"-display", b.display,
		"-crop", fmt.Sprintf("%dx%d+%d+%d", geom.Width, geom.Height, geom.X, geom.Y),
		"-resize", fmt.Sprintf("%dx%d", targetW, targetH),
		"-quality", fmt.Sprintf("%d", b.opts.ScreenshotQuality),
		"jpeg:-",
	}

	// Run import command
	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "import", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+b.display)

	output, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("screenshot failed: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("screenshot failed: %w", err)
	}

	return &computeruse.ScreenshotResult{
		Base64:    base64.StdEncoding.EncodeToString(output),
		Width:     targetW,
		Height:    targetH,
		DisplayID: displayID,
		OriginX:   geom.X,
		OriginY:   geom.Y,
	}, nil
}

// Zoom captures a specific region of the screen.
func (b *Backend) Zoom(region computeruse.Rect, displayID int) (*computeruse.ScreenshotResult, error) {
	// Get display geometry for offset calculation
	geom, err := b.GetDisplaySize(displayID)
	if err != nil {
		return nil, err
	}

	// Calculate target dimensions
	targetW, targetH := computeruse.TargetImageSize(region.W, region.H, 1920)

	// Build import command with crop region
	// The region coordinates need to be offset by display position
	absX := region.X + geom.X
	absY := region.Y + geom.Y

	args := []string{
		"-window", "root",
		"-display", b.display,
		"-crop", fmt.Sprintf("%dx%d+%d+%d", region.W, region.H, absX, absY),
		"-resize", fmt.Sprintf("%dx%d", targetW, targetH),
		"-quality", fmt.Sprintf("%d", b.opts.ScreenshotQuality),
		"jpeg:-",
	}

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "import", args...)
	cmd.Env = append(os.Environ(), "DISPLAY="+b.display)

	output, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("zoom failed: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("zoom failed: %w", err)
	}

	return &computeruse.ScreenshotResult{
		Base64:    base64.StdEncoding.EncodeToString(output),
		Width:     targetW,
		Height:    targetH,
		DisplayID: displayID,
		OriginX:   absX,
		OriginY:   absY,
	}, nil
}
