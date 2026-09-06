// Browser navigation tools for navigating to URLs and restarting the browser.
// Ported from ii-agent's browser/navigate.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserNavigationTool implements navigation to URLs in the browser.
// It navigates to the specified URL and returns a screenshot of the page.
type BrowserNavigationTool struct {
	browser *BrowserManager
}

// NewBrowserNavigationTool creates a new browser navigation tool.
func NewBrowserNavigationTool(browser *BrowserManager) *BrowserNavigationTool {
	return &BrowserNavigationTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserNavigationTool) Name() string {
	return "browser_navigation"
}

// DisplayName returns the human-readable display name.
func (t *BrowserNavigationTool) DisplayName() string {
	return "Browser Navigation"
}

// Description returns the tool description.
func (t *BrowserNavigationTool) Description() string {
	return `Navigate the browser to a specified URL.

This tool opens the given URL in the browser and waits for the page to load.
After navigation, it returns a screenshot of the resulting page.

Usage:
- The URL must be complete with protocol prefix (e.g., "https://example.com")
- The tool waits for the DOM content to be loaded before returning
- Returns a screenshot showing the resulting page state

Examples:
- Navigate to a website: {"url": "https://www.google.com"}
- Open a specific page: {"url": "https://github.com/user/repo"}
- Access a local server: {"url": "http://localhost:3000"}`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserNavigationTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "Complete URL to visit. Must include protocol prefix (e.g., https://).",
			},
		},
		"required": []string{"url"},
	}
}

// Execute navigates to the specified URL.
func (t *BrowserNavigationTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_navigation.context_cancelled")
	default:
	}

	// Extract URL parameter
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return tools.NewToolResult("ERROR: url parameter is required"), nil
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		if err := t.browser.Start(ctx); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to start browser: %s", err.Error())), nil
		}
	}

	// Navigate to URL
	if err := t.browser.Navigate(ctx, url); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Timeout error navigating to %s: %s", url, err.Error())), nil
	}

	// Update state and handle PDF navigation
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	state, err = t.browser.HandlePDFNavigation(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to handle PDF navigation: %s", err.Error())), nil
	}

	// Build result with text and image
	msg := fmt.Sprintf("Navigated to %s", url)
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserNavigationTool) Validate(params map[string]any) error {
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return fmt.Errorf("url is required")
	}
	return nil
}

// IsIdempotent returns false as navigation changes browser state.
func (t *BrowserNavigationTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserNavigationTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserNavigationTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{} // Browser tools don't have a specific permission yet
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserNavigationTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserNavigationTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as navigation typically doesn't need confirmation.
func (t *BrowserNavigationTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserNavigationTool) Metadata() map[string]any {
	return nil
}

// BrowserRestartTool restarts the browser and navigates to a URL.
// Useful when the browser gets into a bad state.
type BrowserRestartTool struct {
	browser *BrowserManager
}

// NewBrowserRestartTool creates a new browser restart tool.
func NewBrowserRestartTool(browser *BrowserManager) *BrowserRestartTool {
	return &BrowserRestartTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserRestartTool) Name() string {
	return "browser_restart"
}

// DisplayName returns the human-readable display name.
func (t *BrowserRestartTool) DisplayName() string {
	return "Browser Restart"
}

// Description returns the tool description.
func (t *BrowserRestartTool) Description() string {
	return `Restart the browser and navigate to a specified URL.

Use this tool when the browser becomes unresponsive or gets into a bad state.
It completely closes and restarts the browser session, then navigates to the given URL.

Usage:
- The URL must be complete with protocol prefix (e.g., "https://example.com")
- All previous browser state (tabs, history, etc.) will be lost
- Returns a screenshot of the resulting page after navigation

Examples:
- Restart and navigate: {"url": "https://www.google.com"}
- Reset browser state: {"url": "about:blank"}`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserRestartTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "Complete URL to visit after restart. Must include protocol prefix.",
			},
		},
		"required": []string{"url"},
	}
}

// Execute restarts the browser and navigates to the specified URL.
func (t *BrowserRestartTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_restart.context_cancelled")
	default:
	}

	// Extract URL parameter
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return tools.NewToolResult("ERROR: url parameter is required"), nil
	}

	// Restart browser
	if err := t.browser.Restart(ctx); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to restart browser: %s", err.Error())), nil
	}

	// Navigate to URL
	if err := t.browser.Navigate(ctx, url); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Timeout error navigating to %s after restart: %s", url, err.Error())), nil
	}

	// Update state and handle PDF navigation
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	state, err = t.browser.HandlePDFNavigation(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to handle PDF navigation: %s", err.Error())), nil
	}

	// Build result with text and image
	msg := fmt.Sprintf("Browser restarted and navigated to %s", url)
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserRestartTool) Validate(params map[string]any) error {
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return fmt.Errorf("url is required")
	}
	return nil
}

// IsIdempotent returns false as restart changes browser state.
func (t *BrowserRestartTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserRestartTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserRestartTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserRestartTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserRestartTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as restart doesn't need confirmation.
func (t *BrowserRestartTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserRestartTool) Metadata() map[string]any {
	return nil
}
