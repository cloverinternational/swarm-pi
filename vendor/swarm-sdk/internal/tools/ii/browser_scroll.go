// Browser scroll tools for scrolling the page up and down.
// Ported from ii-agent's browser/scroll.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserScrollDownTool scrolls the current browser page down.
// It scrolls by approximately 80% of the viewport height.
type BrowserScrollDownTool struct {
	browser *BrowserManager
}

// NewBrowserScrollDownTool creates a new browser scroll down tool.
func NewBrowserScrollDownTool(browser *BrowserManager) *BrowserScrollDownTool {
	return &BrowserScrollDownTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserScrollDownTool) Name() string {
	return "browser_scroll_down"
}

// DisplayName returns the human-readable display name.
func (t *BrowserScrollDownTool) DisplayName() string {
	return "Browser Scroll Down"
}

// Description returns the tool description.
func (t *BrowserScrollDownTool) Description() string {
	return `Scroll down the current browser page.

This tool scrolls the page down by approximately 80% of the viewport height,
allowing you to view content that is below the current viewport.

Usage:
- No parameters required
- For PDF documents, uses PageDown key instead of mouse wheel
- Returns a screenshot showing the new viewport position

Use this tool to:
- View content below the fold
- Navigate through long pages
- Find elements that are not currently visible

Example: {} (no parameters needed)`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserScrollDownTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Execute scrolls the page down.
func (t *BrowserScrollDownTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_scroll_down.context_cancelled")
	default:
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Get current state to check for PDF
	state := t.browser.State()
	isPDF := IsPDFURL(state.URL)

	// Scroll down
	if isPDF {
		// For PDFs, use PageDown key
		if err := t.browser.PressKey(ctx, "PageDown"); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to scroll PDF: %s", err.Error())), nil
		}
	} else {
		// For regular pages, use mouse wheel
		if err := t.browser.ScrollDown(ctx); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to scroll down: %s", err.Error())), nil
		}
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := "Scrolled page down"
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserScrollDownTool) Validate(params map[string]any) error {
	// No required parameters
	return nil
}

// IsIdempotent returns false as scrolling changes viewport state.
func (t *BrowserScrollDownTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserScrollDownTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserScrollDownTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserScrollDownTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserScrollDownTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as scrolling doesn't need confirmation.
func (t *BrowserScrollDownTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserScrollDownTool) Metadata() map[string]any {
	return nil
}

// BrowserScrollUpTool scrolls the current browser page up.
// It scrolls by approximately 80% of the viewport height.
type BrowserScrollUpTool struct {
	browser *BrowserManager
}

// NewBrowserScrollUpTool creates a new browser scroll up tool.
func NewBrowserScrollUpTool(browser *BrowserManager) *BrowserScrollUpTool {
	return &BrowserScrollUpTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserScrollUpTool) Name() string {
	return "browser_scroll_up"
}

// DisplayName returns the human-readable display name.
func (t *BrowserScrollUpTool) DisplayName() string {
	return "Browser Scroll Up"
}

// Description returns the tool description.
func (t *BrowserScrollUpTool) Description() string {
	return `Scroll up the current browser page.

This tool scrolls the page up by approximately 80% of the viewport height,
allowing you to view content that is above the current viewport.

Usage:
- No parameters required
- For PDF documents, uses PageUp key instead of mouse wheel
- Returns a screenshot showing the new viewport position

Use this tool to:
- Return to previously viewed content
- Navigate through long pages
- Go back to the top of the page

Example: {} (no parameters needed)`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserScrollUpTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Execute scrolls the page up.
func (t *BrowserScrollUpTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_scroll_up.context_cancelled")
	default:
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Get current state to check for PDF
	state := t.browser.State()
	isPDF := IsPDFURL(state.URL)

	// Scroll up
	if isPDF {
		// For PDFs, use PageUp key
		if err := t.browser.PressKey(ctx, "PageUp"); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to scroll PDF: %s", err.Error())), nil
		}
	} else {
		// For regular pages, use mouse wheel
		if err := t.browser.ScrollUp(ctx); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to scroll up: %s", err.Error())), nil
		}
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := "Scrolled page up"
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserScrollUpTool) Validate(params map[string]any) error {
	// No required parameters
	return nil
}

// IsIdempotent returns false as scrolling changes viewport state.
func (t *BrowserScrollUpTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserScrollUpTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserScrollUpTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserScrollUpTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserScrollUpTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as scrolling doesn't need confirmation.
func (t *BrowserScrollUpTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserScrollUpTool) Metadata() map[string]any {
	return nil
}
