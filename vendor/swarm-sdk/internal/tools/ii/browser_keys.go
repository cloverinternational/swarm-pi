// Browser keyboard tools for simulating key presses.
// Ported from ii-agent's browser/press_key.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserPressKeyTool simulates key presses in the browser.
// It supports single keys and key combinations.
type BrowserPressKeyTool struct {
	browser *BrowserManager
}

// NewBrowserPressKeyTool creates a new browser press key tool.
func NewBrowserPressKeyTool(browser *BrowserManager) *BrowserPressKeyTool {
	return &BrowserPressKeyTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserPressKeyTool) Name() string {
	return "browser_press_key"
}

// DisplayName returns the human-readable display name.
func (t *BrowserPressKeyTool) DisplayName() string {
	return "Browser Press Key"
}

// Description returns the tool description.
func (t *BrowserPressKeyTool) Description() string {
	return `Simulate a key press in the current browser page.

This tool sends keyboard events to the browser, useful for navigation,
form submission, and keyboard shortcuts.

Supported keys:
- Navigation: Enter, Tab, Escape, Backspace, Delete
- Arrow keys: ArrowUp, ArrowDown, ArrowLeft, ArrowRight
- Page navigation: PageUp, PageDown, Home, End
- Modifiers: Control (Ctrl), Alt, Shift, Meta (Command on Mac)

Key combinations:
- Use + to combine keys: "Control+Enter", "Control+a", "Shift+Tab"
- Multiple modifiers: "Control+Shift+s"

Examples:
- Submit form: {"key": "Enter"}
- Navigate backwards: {"key": "Backspace"}
- Select all: {"key": "Control+a"}
- Save: {"key": "Control+s"}
- Next element: {"key": "Tab"}
- Previous element: {"key": "Shift+Tab"}

Note: For Mac, use "Control" - it will be mapped appropriately.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserPressKeyTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key": map[string]any{
				"type":        "string",
				"description": "Key name to press (e.g., Enter, Tab, ArrowUp) or key combination (e.g., Control+Enter)",
			},
		},
		"required": []string{"key"},
	}
}

// Execute presses the specified key.
func (t *BrowserPressKeyTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_press_key.context_cancelled")
	default:
	}

	// Extract key parameter
	key, ok := params["key"].(string)
	if !ok || key == "" {
		return tools.NewToolResult("ERROR: key parameter is required"), nil
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Press the key
	if err := t.browser.PressKey(ctx, key); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to press key '%s': %s", key, err.Error())), nil
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := fmt.Sprintf(`Pressed "%s" on the keyboard.`, key)
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserPressKeyTool) Validate(params map[string]any) error {
	key, ok := params["key"].(string)
	if !ok || key == "" {
		return fmt.Errorf("key is required")
	}
	return nil
}

// IsIdempotent returns false as key presses can change state.
func (t *BrowserPressKeyTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool can modify browser/page state.
func (t *BrowserPressKeyTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserPressKeyTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserPressKeyTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserPressKeyTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as key presses typically don't need confirmation.
func (t *BrowserPressKeyTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserPressKeyTool) Metadata() map[string]any {
	return nil
}

// CommonKeys provides constants for commonly used key names.
// These match the key names expected by chromedp.
var CommonKeys = struct {
	// Navigation keys
	Enter     string
	Tab       string
	Escape    string
	Backspace string
	Delete    string

	// Arrow keys
	ArrowUp    string
	ArrowDown  string
	ArrowLeft  string
	ArrowRight string

	// Page navigation
	PageUp   string
	PageDown string
	Home     string
	End      string

	// Modifier keys
	Control string
	Alt     string
	Shift   string
	Meta    string
}{
	Enter:      "Enter",
	Tab:        "Tab",
	Escape:     "Escape",
	Backspace:  "Backspace",
	Delete:     "Delete",
	ArrowUp:    "ArrowUp",
	ArrowDown:  "ArrowDown",
	ArrowLeft:  "ArrowLeft",
	ArrowRight: "ArrowRight",
	PageUp:     "PageUp",
	PageDown:   "PageDown",
	Home:       "Home",
	End:        "End",
	Control:    "Control",
	Alt:        "Alt",
	Shift:      "Shift",
	Meta:       "Meta",
}
