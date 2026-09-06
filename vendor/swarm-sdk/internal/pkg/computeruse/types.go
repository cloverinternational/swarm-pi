// Package computeruse provides computer control capabilities for AI agents.
// It implements the Model Context Protocol (MCP) tools for screen capture,
// mouse/keyboard input, and application management.
//
// This package is inspired by and compatible with Claude Code's Computer Use
// functionality, providing a Linux-native implementation.
package computeruse

import "context"

// MouseButton represents a mouse button.
type MouseButton string

const (
	MouseButtonLeft   MouseButton = "left"
	MouseButtonRight  MouseButton = "right"
	MouseButtonMiddle MouseButton = "middle"
)

// ClickCount represents the number of clicks.
type ClickCount int

const (
	ClickCountSingle ClickCount = 1
	ClickCountDouble ClickCount = 2
	ClickCountTriple ClickCount = 3
)

// CoordinateMode represents the coordinate system used.
type CoordinateMode string

const (
	// CoordinateModePixels uses raw screen coordinates.
	CoordinateModePixels CoordinateMode = "pixels"
	// CoordinateModeNormalized uses 0-1000 scale relative to display size.
	CoordinateModeNormalized CoordinateMode = "normalized"
)

// Point represents a 2D coordinate.
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Rect represents a rectangular region.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// DisplayGeometry represents display information.
type DisplayGeometry struct {
	ID          int     `json:"id"`
	Name        string  `json:"name,omitempty"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	ScaleFactor float64 `json:"scaleFactor"`
	Primary     bool    `json:"primary,omitempty"`
	X           int     `json:"x,omitempty"` // Offset in virtual screen
	Y           int     `json:"y,omitempty"` // Offset in virtual screen
}

// ScreenshotResult represents a screenshot capture result.
type ScreenshotResult struct {
	Base64    string `json:"base64"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	DisplayID int    `json:"displayId,omitempty"`
	OriginX   int    `json:"originX,omitempty"`
	OriginY   int    `json:"originY,omitempty"`
}

// InstalledApp represents an installed application.
type InstalledApp struct {
	ID          string `json:"bundleId"` // Desktop entry ID (e.g., "org.gnome.Nautilus.desktop")
	DisplayName string `json:"displayName"`
	Path        string `json:"path,omitempty"`
	IconDataURL string `json:"iconDataUrl,omitempty"`
}

// RunningApp represents a running application.
type RunningApp struct {
	ID          string `json:"bundleId"`
	DisplayName string `json:"displayName"`
	PID         int    `json:"pid,omitempty"`
}

// FrontmostApp represents the active application.
type FrontmostApp struct {
	ID          string `json:"bundleId"`
	DisplayName string `json:"displayName"`
}

// GrantFlags represents permission grants.
type GrantFlags struct {
	ClipboardRead   bool `json:"clipboardRead"`
	ClipboardWrite  bool `json:"clipboardWrite"`
	SystemKeyCombos bool `json:"systemKeyCombos"`
}

// PermissionRequest represents a permission request.
type PermissionRequest struct {
	Apps        []InstalledApp `json:"apps,omitempty"`
	Permissions []string       `json:"permissions,omitempty"`
	Reason      string         `json:"reason,omitempty"`
}

// PermissionResponse represents a permission response.
type PermissionResponse struct {
	Granted     bool        `json:"granted"`
	AllowedApps []string    `json:"allowedApps,omitempty"`
	GrantFlags  *GrantFlags `json:"grantFlags,omitempty"`
}

// PrepareCaptureResult represents the result of preparing for capture.
type PrepareCaptureResult struct {
	Screenshot ScreenshotResult `json:"screenshot"`
	DisplayID  int              `json:"displayId"`
	HiddenApps []string         `json:"hiddenApps,omitempty"`
}

// ExecutorCapabilities describes what the executor supports.
type ExecutorCapabilities struct {
	Platform            string         `json:"platform"`
	ScreenshotFiltering string         `json:"screenshotFiltering,omitempty"`
	HostBundleID        string         `json:"hostBundleId,omitempty"`
	CoordinateMode      CoordinateMode `json:"coordinateMode,omitempty"`
}

// InputBackend defines the interface for platform-specific input handling.
// Implementations provide X11, Wayland, or other display server support.
type InputBackend interface {
	// Display operations
	GetDisplaySize(displayID int) (*DisplayGeometry, error)
	ListDisplays() ([]DisplayGeometry, error)
	Screenshot(displayID int, allowedApps []string) (*ScreenshotResult, error)
	Zoom(region Rect, displayID int) (*ScreenshotResult, error)

	// Mouse operations
	MoveMouse(x, y int) error
	Click(x, y int, button MouseButton, count ClickCount, modifiers []string) error
	MouseDown() error
	MouseUp() error
	GetCursorPosition() (*Point, error)
	Drag(from, to *Point) error
	Scroll(x, y int, dx, dy int) error

	// Keyboard operations
	Type(text string, viaClipboard bool) error
	Key(keySequence string, repeat int) error
	HoldKey(keys []string, durationMs int) error

	// Clipboard operations
	ReadClipboard() (string, error)
	WriteClipboard(text string) error

	// Application operations
	GetFrontmostApp() (*FrontmostApp, error)
	AppUnderPoint(x, y int) (*FrontmostApp, error)
	ListInstalledApps() ([]InstalledApp, error)
	ListRunningApps() ([]RunningApp, error)
	OpenApp(appID string) error

	// Lifecycle
	Close() error
}

// ComputerExecutor defines the high-level interface for computer control.
// This interface matches Claude Code's @ant/computer-use-mcp interface.
type ComputerExecutor interface {
	// Capabilities returns the executor's capabilities.
	Capabilities() ExecutorCapabilities

	// Pre-action operations (Linux may not need hide functionality)
	PrepareForAction(ctx context.Context, allowlistApps []string, displayID int) ([]string, error)
	PreviewHideSet(ctx context.Context, allowlistApps []string, displayID int) ([]InstalledApp, error)

	// Display operations
	GetDisplaySize(ctx context.Context, displayID int) (*DisplayGeometry, error)
	ListDisplays(ctx context.Context) ([]DisplayGeometry, error)
	FindWindowDisplays(ctx context.Context, appIDs []string) (map[string][]int, error)
	ResolvePrepareCapture(ctx context.Context, opts PrepareCaptureOptions) (*PrepareCaptureResult, error)
	Screenshot(ctx context.Context, opts ScreenshotOptions) (*ScreenshotResult, error)
	Zoom(ctx context.Context, region Rect, allowedApps []string, displayID int) (*ScreenshotResult, error)

	// Keyboard operations
	Key(ctx context.Context, keySequence string, repeat int) error
	HoldKey(ctx context.Context, keys []string, durationMs int) error
	Type(ctx context.Context, text string, opts TypeOptions) error
	ReadClipboard(ctx context.Context) (string, error)
	WriteClipboard(ctx context.Context, text string) error

	// Mouse operations
	MoveMouse(ctx context.Context, x, y int) error
	Click(ctx context.Context, x, y int, button MouseButton, count ClickCount, modifiers []string) error
	MouseDown(ctx context.Context) error
	MouseUp(ctx context.Context) error
	GetCursorPosition(ctx context.Context) (*Point, error)
	Drag(ctx context.Context, from, to *Point) error
	Scroll(ctx context.Context, x, y int, dx, dy int) error

	// Application operations
	GetFrontmostApp(ctx context.Context) (*FrontmostApp, error)
	AppUnderPoint(ctx context.Context, x, y int) (*FrontmostApp, error)
	ListInstalledApps(ctx context.Context) ([]InstalledApp, error)
	GetAppIcon(ctx context.Context, path string) (string, error)
	ListRunningApps(ctx context.Context) ([]RunningApp, error)
	OpenApp(ctx context.Context, appID string) error

	// Lifecycle
	Close() error
}

// ScreenshotOptions represents options for taking a screenshot.
type ScreenshotOptions struct {
	AllowedApps []string `json:"allowedBundleIds,omitempty"`
	DisplayID   int      `json:"displayId,omitempty"`
}

// PrepareCaptureOptions represents options for preparing a capture.
type PrepareCaptureOptions struct {
	AllowedApps        []string `json:"allowedBundleIds"`
	PreferredDisplayID int      `json:"preferredDisplayId,omitempty"`
	AutoResolve        bool     `json:"autoResolve"`
	DoHide             bool     `json:"doHide,omitempty"`
}

// TypeOptions represents options for typing text.
type TypeOptions struct {
	ViaClipboard bool `json:"viaClipboard"`
}

// HostAdapter provides the interface between the MCP server and the executor.
// This matches Claude Code's ComputerUseHostAdapter interface.
type HostAdapter interface {
	// ServerName returns the MCP server name.
	ServerName() string

	// Executor returns the computer executor.
	Executor() ComputerExecutor

	// EnsureOSPermissions checks and requests OS-level permissions.
	EnsureOSPermissions(ctx context.Context) (*PermissionResult, error)

	// IsDisabled returns true if computer use is disabled.
	IsDisabled() bool

	// GetSubGates returns feature flags for sub-gates.
	GetSubGates() SubGates

	// GetAutoUnhideEnabled returns whether auto-unhide is enabled.
	GetAutoUnhideEnabled() bool

	// CropRawPatch crops a raw image patch (for pixel validation).
	CropRawPatch(data []byte, x, y, w, h int) []byte
}

// SubGates represents feature flags for computer use sub-features.
type SubGates struct {
	PixelValidation         bool `json:"pixelValidation"`
	ClipboardPasteMultiline bool `json:"clipboardPasteMultiline"`
	MouseAnimation          bool `json:"mouseAnimation"`
	HideBeforeAction        bool `json:"hideBeforeAction"`
	AutoTargetDisplay       bool `json:"autoTargetDisplay"`
	ClipboardGuard          bool `json:"clipboardGuard"`
}

// PermissionResult represents the result of permission checking.
type PermissionResult struct {
	Granted         bool `json:"granted"`
	Accessibility   bool `json:"accessibility,omitempty"`
	ScreenRecording bool `json:"screenRecording,omitempty"`
}

// DefaultSubGates returns the default sub-gates configuration.
func DefaultSubGates() SubGates {
	return SubGates{
		PixelValidation:         false,
		ClipboardPasteMultiline: true,
		MouseAnimation:          true,
		HideBeforeAction:        false, // Linux doesn't need hide
		AutoTargetDisplay:       true,
		ClipboardGuard:          true,
	}
}
