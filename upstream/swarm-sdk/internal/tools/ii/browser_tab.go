// Browser tab management tools for switching and creating tabs.
// Ported from ii-agent's browser/tab.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserSwitchTabTool switches to a specific browser tab by index.
type BrowserSwitchTabTool struct {
	browser *BrowserManager
}

// NewBrowserSwitchTabTool creates a new browser switch tab tool.
func NewBrowserSwitchTabTool(browser *BrowserManager) *BrowserSwitchTabTool {
	return &BrowserSwitchTabTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserSwitchTabTool) Name() string {
	return "browser_switch_tab"
}

// DisplayName returns the human-readable display name.
func (t *BrowserSwitchTabTool) DisplayName() string {
	return "Browser Switch Tab"
}

// Description returns the tool description.
func (t *BrowserSwitchTabTool) Description() string {
	return `Switch to a specific browser tab by its index.

Tabs are numbered starting from 0. Use negative indices to count from the end
(e.g., -1 for the last tab).

Usage:
- index: The index of the tab to switch to (0-based)
- Returns a screenshot of the tab after switching

Examples:
- Switch to first tab: {"index": 0}
- Switch to second tab: {"index": 1}
- Switch to last tab: {"index": -1}

Use this tool when:
- A link opened in a new tab and you want to switch to it
- You need to return to a previous tab
- Managing multiple tabs during a browsing session`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserSwitchTabTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index": map[string]any{
				"type":        "integer",
				"description": "Index of the tab to switch to (0-based, negative indices count from end)",
			},
		},
		"required": []string{"index"},
	}
}

// Execute switches to the specified tab.
func (t *BrowserSwitchTabTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_switch_tab.context_cancelled")
	default:
	}

	// Extract index parameter
	indexFloat, ok := extractFloat64(params, "index")
	if !ok {
		return tools.NewToolResult("ERROR: index parameter is required"), nil
	}
	index := int(indexFloat)

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Switch to tab
	if err := t.browser.SwitchToTab(ctx, index); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Switch tab operation failed for tab %d: %s", index, err.Error())), nil
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := fmt.Sprintf("Switched to tab %d", index)
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserSwitchTabTool) Validate(params map[string]any) error {
	_, ok := extractFloat64(params, "index")
	if !ok {
		return fmt.Errorf("index is required")
	}
	return nil
}

// IsIdempotent returns false as switching tabs changes browser focus.
func (t *BrowserSwitchTabTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserSwitchTabTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserSwitchTabTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserSwitchTabTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserSwitchTabTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as switching tabs doesn't need confirmation.
func (t *BrowserSwitchTabTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserSwitchTabTool) Metadata() map[string]any {
	return nil
}

// BrowserOpenNewTabTool opens a new browser tab.
type BrowserOpenNewTabTool struct {
	browser *BrowserManager
}

// NewBrowserOpenNewTabTool creates a new browser open new tab tool.
func NewBrowserOpenNewTabTool(browser *BrowserManager) *BrowserOpenNewTabTool {
	return &BrowserOpenNewTabTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserOpenNewTabTool) Name() string {
	return "browser_open_new_tab"
}

// DisplayName returns the human-readable display name.
func (t *BrowserOpenNewTabTool) DisplayName() string {
	return "Browser Open New Tab"
}

// Description returns the tool description.
func (t *BrowserOpenNewTabTool) Description() string {
	return `Open a new browser tab.

This tool creates a new empty tab (about:blank) and switches to it.
Use browser_navigation afterwards to navigate the new tab to a URL.

Usage:
- No parameters required
- Returns a screenshot of the new tab (will be blank)

Example workflow:
1. Call browser_open_new_tab to create new tab
2. Call browser_navigation with the URL you want to visit
3. Continue interacting with the page

Use this tool when you need to:
- Open a URL in a new tab while keeping the current tab
- Manage multiple pages simultaneously
- Compare content across different pages`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserOpenNewTabTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Execute opens a new tab.
func (t *BrowserOpenNewTabTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_open_new_tab.context_cancelled")
	default:
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Create new tab
	if err := t.browser.CreateNewTab(ctx); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Open new tab operation failed: %s", err.Error())), nil
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := "Opened a new tab"
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserOpenNewTabTool) Validate(params map[string]any) error {
	// No required parameters
	return nil
}

// IsIdempotent returns false as opening a tab creates new state.
func (t *BrowserOpenNewTabTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserOpenNewTabTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserOpenNewTabTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserOpenNewTabTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserOpenNewTabTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as opening a tab doesn't need confirmation.
func (t *BrowserOpenNewTabTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserOpenNewTabTool) Metadata() map[string]any {
	return nil
}
