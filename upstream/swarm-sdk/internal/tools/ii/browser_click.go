// Browser click tool for clicking elements on the page.
// Ported from ii-agent's browser/click.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserClickTool performs mouse clicks at specified coordinates on the page.
// It supports single and double clicks and handles new tab creation.
type BrowserClickTool struct {
	browser *BrowserManager
}

// NewBrowserClickTool creates a new browser click tool.
func NewBrowserClickTool(browser *BrowserManager) *BrowserClickTool {
	return &BrowserClickTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserClickTool) Name() string {
	return "browser_click"
}

// DisplayName returns the human-readable display name.
func (t *BrowserClickTool) DisplayName() string {
	return "Browser Click"
}

// Description returns the tool description.
func (t *BrowserClickTool) Description() string {
	return `Click on an element at specified coordinates on the current browser page.

This tool simulates a mouse click at the given X and Y coordinates.
Use browser_view_interactive_elements first to identify element positions.

Usage:
- Provide coordinate_x and coordinate_y for the click position
- Set double_click to true for double-click actions
- The tool automatically handles new tabs if the click opens one

After clicking, the tool returns a screenshot showing the result.

Examples:
- Single click: {"coordinate_x": 300, "coordinate_y": 200}
- Double click: {"coordinate_x": 300, "coordinate_y": 200, "double_click": true}

Note: Coordinates are relative to the viewport, not the entire page.
Use scroll tools to bring elements into view before clicking.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserClickTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"coordinate_x": map[string]any{
				"type":        "number",
				"description": "X coordinate of click position (pixels from left edge)",
			},
			"coordinate_y": map[string]any{
				"type":        "number",
				"description": "Y coordinate of click position (pixels from top edge)",
			},
			"double_click": map[string]any{
				"type":        "boolean",
				"description": "If true, performs a double click instead of single click",
				"default":     false,
			},
		},
		"required": []string{"coordinate_x", "coordinate_y"},
	}
}

// Execute performs a click at the specified coordinates.
func (t *BrowserClickTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_click.context_cancelled")
	default:
	}

	// Extract parameters
	coordinateX, okX := extractFloat64(params, "coordinate_x")
	coordinateY, okY := extractFloat64(params, "coordinate_y")
	if !okX || !okY {
		return tools.NewToolResult("ERROR: Must provide both coordinate_x and coordinate_y to click on an element"), nil
	}

	doubleClick := false
	if dc, ok := params["double_click"].(bool); ok {
		doubleClick = dc
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Get initial tab count to detect new tabs
	initialTabCount, err := t.browser.TabCount(ctx)
	if err != nil {
		initialTabCount = 1 // Default assumption
	}

	// Perform click
	if err := t.browser.Click(ctx, coordinateX, coordinateY, doubleClick); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Click operation failed at (%.0f, %.0f): %s",
			coordinateX, coordinateY, err.Error())), nil
	}

	// Build result message
	var msg string
	if doubleClick {
		msg = fmt.Sprintf("Double clicked at coordinates %.0f, %.0f", coordinateX, coordinateY)
	} else {
		msg = fmt.Sprintf("Clicked at coordinates %.0f, %.0f", coordinateX, coordinateY)
	}

	// Check for new tab
	newTabCount, err := t.browser.TabCount(ctx)
	if err == nil && newTabCount > initialTabCount {
		msg += " - New tab opened - switching to it"
		if err := t.browser.SwitchToTab(ctx, -1); err != nil {
			msg += fmt.Sprintf(" (warning: failed to switch: %s)", err.Error())
		}
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

	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserClickTool) Validate(params map[string]any) error {
	_, okX := extractFloat64(params, "coordinate_x")
	_, okY := extractFloat64(params, "coordinate_y")
	if !okX || !okY {
		return fmt.Errorf("coordinate_x and coordinate_y are required")
	}
	return nil
}

// IsIdempotent returns false as clicking changes page state.
func (t *BrowserClickTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserClickTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserClickTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserClickTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserClickTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as clicking typically doesn't need confirmation.
func (t *BrowserClickTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserClickTool) Metadata() map[string]any {
	return nil
}

// extractFloat64 safely extracts a float64 from params, handling both float64 and int types.
func extractFloat64(params map[string]any, key string) (float64, bool) {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case float64:
			return v, true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		}
	}
	return 0, false
}
