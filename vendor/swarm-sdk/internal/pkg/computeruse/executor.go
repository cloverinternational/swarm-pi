package computeruse

import (
	"context"
	"fmt"
	"os"
	"runtime"
)

// DisplayServer represents the type of display server.
type DisplayServer string

const (
	DisplayServerX11     DisplayServer = "x11"
	DisplayServerWayland DisplayServer = "wayland"
	DisplayServerNone    DisplayServer = "none"
)

// LinuxExecutor implements ComputerExecutor for Linux systems.
// It wraps an InputBackend and provides the high-level ComputerExecutor interface.
type LinuxExecutor struct {
	backend       InputBackend
	displayServer DisplayServer
	capabilities  ExecutorCapabilities
	opts          Options
}

// Options configures the Linux executor.
type Options struct {
	// Coordinate mode (pixels or normalized)
	CoordinateMode CoordinateMode

	// Enable mouse animation
	MouseAnimation bool

	// Enable hide before action (Linux typically doesn't need this)
	HideBeforeAction bool
}

// DefaultOptions returns the default options for Linux.
func DefaultOptions() Options {
	return Options{
		CoordinateMode:   CoordinateModePixels,
		MouseAnimation:   true,
		HideBeforeAction: false,
	}
}

// NewLinuxExecutor creates a new Linux executor with the given backend.
// The backend should be created by the appropriate platform-specific package
// (e.g., x11.NewBackend for X11 systems).
func NewLinuxExecutor(backend InputBackend, opts Options) (*LinuxExecutor, error) {
	// Verify we're on Linux
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedPlatform
	}

	if backend == nil {
		return nil, ErrNoDisplay
	}

	return &LinuxExecutor{
		backend:       backend,
		displayServer: detectDisplayServer(),
		capabilities: ExecutorCapabilities{
			Platform:            "linux",
			ScreenshotFiltering: "native",
			CoordinateMode:      opts.CoordinateMode,
		},
		opts: opts,
	}, nil
}

// NewLinuxExecutorWithAutoDetect creates a new Linux executor with auto-detected backend.
// This requires importing the platform-specific backend packages.
// For more control, use NewLinuxExecutor with a specific backend.
//
// Note: This function will be implemented by each backend package's init hook.
// The default implementation returns ErrUnsupportedPlatform.
var AutoDetectBackend = func() (InputBackend, error) {
	return nil, ErrUnsupportedPlatform
}

// NewLinuxExecutorAuto creates a new Linux executor with auto-detected backend.
func NewLinuxExecutorAuto(opts Options) (*LinuxExecutor, error) {
	backend, err := AutoDetectBackend()
	if err != nil {
		return nil, fmt.Errorf("failed to auto-detect backend: %w", err)
	}
	return NewLinuxExecutor(backend, opts)
}

// detectDisplayServer detects the current display server.
func detectDisplayServer() DisplayServer {
	// Check XDG_SESSION_TYPE
	sessionType := os.Getenv("XDG_SESSION_TYPE")
	switch sessionType {
	case "x11":
		return DisplayServerX11
	case "wayland":
		return DisplayServerWayland
	}

	// Check for Wayland display
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		return DisplayServerWayland
	}

	// Check for X11 display
	if os.Getenv("DISPLAY") != "" {
		return DisplayServerX11
	}

	return DisplayServerNone
}

// Capabilities returns the executor's capabilities.
func (e *LinuxExecutor) Capabilities() ExecutorCapabilities {
	return e.capabilities
}

// Close releases resources.
func (e *LinuxExecutor) Close() error {
	if e.backend != nil {
		return e.backend.Close()
	}
	return nil
}

// PrepareForAction prepares for an action (Linux typically doesn't need hide).
func (e *LinuxExecutor) PrepareForAction(ctx context.Context, allowlistApps []string, displayID int) ([]string, error) {
	// Linux doesn't have the same window hide mechanism as macOS
	return nil, nil
}

// PreviewHideSet previews which apps would be hidden.
func (e *LinuxExecutor) PreviewHideSet(ctx context.Context, allowlistApps []string, displayID int) ([]InstalledApp, error) {
	// Linux doesn't hide apps before actions
	return nil, nil
}

// GetDisplaySize returns the geometry of a specific display.
func (e *LinuxExecutor) GetDisplaySize(ctx context.Context, displayID int) (*DisplayGeometry, error) {
	return e.backend.GetDisplaySize(displayID)
}

// ListDisplays returns all connected displays.
func (e *LinuxExecutor) ListDisplays(ctx context.Context) ([]DisplayGeometry, error) {
	return e.backend.ListDisplays()
}

// FindWindowDisplays finds which displays have windows for the given apps.
// This is a best-effort implementation that may not work on all backends.
func (e *LinuxExecutor) FindWindowDisplays(ctx context.Context, appIDs []string) (map[string][]int, error) {
	// Not all backends support this
	return make(map[string][]int), nil
}

// ResolvePrepareCapture prepares for a capture.
func (e *LinuxExecutor) ResolvePrepareCapture(ctx context.Context, opts PrepareCaptureOptions) (*PrepareCaptureResult, error) {
	// Take screenshot
	result, err := e.backend.Screenshot(opts.PreferredDisplayID, opts.AllowedApps)
	if err != nil {
		return nil, err
	}

	return &PrepareCaptureResult{
		Screenshot: *result,
		DisplayID:  opts.PreferredDisplayID,
	}, nil
}

// Screenshot takes a screenshot.
func (e *LinuxExecutor) Screenshot(ctx context.Context, opts ScreenshotOptions) (*ScreenshotResult, error) {
	return e.backend.Screenshot(opts.DisplayID, opts.AllowedApps)
}

// Zoom captures a specific region.
func (e *LinuxExecutor) Zoom(ctx context.Context, region Rect, allowedApps []string, displayID int) (*ScreenshotResult, error) {
	return e.backend.Zoom(region, displayID)
}

// Key presses a key combination.
func (e *LinuxExecutor) Key(ctx context.Context, keySequence string, repeat int) error {
	return e.backend.Key(keySequence, repeat)
}

// HoldKey holds keys for a duration.
func (e *LinuxExecutor) HoldKey(ctx context.Context, keys []string, durationMs int) error {
	return e.backend.HoldKey(keys, durationMs)
}

// Type types text.
func (e *LinuxExecutor) Type(ctx context.Context, text string, opts TypeOptions) error {
	return e.backend.Type(text, opts.ViaClipboard)
}

// ReadClipboard reads the clipboard.
func (e *LinuxExecutor) ReadClipboard(ctx context.Context) (string, error) {
	return e.backend.ReadClipboard()
}

// WriteClipboard writes to the clipboard.
func (e *LinuxExecutor) WriteClipboard(ctx context.Context, text string) error {
	return e.backend.WriteClipboard(text)
}

// MoveMouse moves the cursor.
func (e *LinuxExecutor) MoveMouse(ctx context.Context, x, y int) error {
	return e.backend.MoveMouse(x, y)
}

// Click performs a click.
func (e *LinuxExecutor) Click(ctx context.Context, x, y int, button MouseButton, count ClickCount, modifiers []string) error {
	return e.backend.Click(x, y, button, count, modifiers)
}

// MouseDown presses the mouse button.
func (e *LinuxExecutor) MouseDown(ctx context.Context) error {
	return e.backend.MouseDown()
}

// MouseUp releases the mouse button.
func (e *LinuxExecutor) MouseUp(ctx context.Context) error {
	return e.backend.MouseUp()
}

// GetCursorPosition returns the current cursor position.
func (e *LinuxExecutor) GetCursorPosition(ctx context.Context) (*Point, error) {
	return e.backend.GetCursorPosition()
}

// Drag performs a drag operation.
func (e *LinuxExecutor) Drag(ctx context.Context, from, to *Point) error {
	return e.backend.Drag(from, to)
}

// Scroll performs a scroll.
func (e *LinuxExecutor) Scroll(ctx context.Context, x, y int, dx, dy int) error {
	return e.backend.Scroll(x, y, dx, dy)
}

// GetFrontmostApp returns the active application.
func (e *LinuxExecutor) GetFrontmostApp(ctx context.Context) (*FrontmostApp, error) {
	return e.backend.GetFrontmostApp()
}

// AppUnderPoint returns the app at coordinates.
func (e *LinuxExecutor) AppUnderPoint(ctx context.Context, x, y int) (*FrontmostApp, error) {
	return e.backend.AppUnderPoint(x, y)
}

// ListInstalledApps returns installed applications.
func (e *LinuxExecutor) ListInstalledApps(ctx context.Context) ([]InstalledApp, error) {
	return e.backend.ListInstalledApps()
}

// GetAppIcon returns an app's icon (not implemented for Linux).
func (e *LinuxExecutor) GetAppIcon(ctx context.Context, path string) (string, error) {
	// TODO: Implement icon extraction from .desktop files
	return "", nil
}

// ListRunningApps returns running applications.
func (e *LinuxExecutor) ListRunningApps(ctx context.Context) ([]RunningApp, error) {
	return e.backend.ListRunningApps()
}

// OpenApp opens an application.
func (e *LinuxExecutor) OpenApp(ctx context.Context, appID string) error {
	return e.backend.OpenApp(appID)
}
