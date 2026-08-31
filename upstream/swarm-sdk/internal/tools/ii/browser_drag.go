// Browser drag tool for performing drag and drop operations.
// Ported from ii-agent's browser/drag.py
package ii

import (
	"context"
	"fmt"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserDragTool performs drag and drop operations between two points.
// It simulates mouse down at the start position, moves to the end position,
// and releases the mouse button.
type BrowserDragTool struct {
	browser *BrowserManager
}

// NewBrowserDragTool creates a new browser drag tool.
func NewBrowserDragTool(browser *BrowserManager) *BrowserDragTool {
	return &BrowserDragTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserDragTool) Name() string {
	return "browser_drag"
}

// DisplayName returns the human-readable display name.
func (t *BrowserDragTool) DisplayName() string {
	return "Browser Drag"
}

// Description returns the tool description.
func (t *BrowserDragTool) Description() string {
	return `Perform a drag and drop operation between two points on the page.

This tool simulates:
1. Moving mouse to start position
2. Pressing mouse button down
3. Moving to end position while holding button
4. Releasing mouse button

Usage:
- Provide start coordinates (coordinate_x_start, coordinate_y_start)
- Provide end coordinates (coordinate_x_end, coordinate_y_end)
- Returns a screenshot after the drag operation completes

Use this tool for:
- Drag and drop interactions
- Slider adjustments
- Reordering elements
- Drawing operations
- Resizing elements

Example:
{
  "coordinate_x_start": 100,
  "coordinate_y_start": 200,
  "coordinate_x_end": 300,
  "coordinate_y_end": 200
}

Note: Always verify the drag operation was successful by checking the screenshot.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserDragTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"coordinate_x_start": map[string]any{
				"type":        "number",
				"description": "X coordinate of drag start position",
			},
			"coordinate_y_start": map[string]any{
				"type":        "number",
				"description": "Y coordinate of drag start position",
			},
			"coordinate_x_end": map[string]any{
				"type":        "number",
				"description": "X coordinate of drag end position",
			},
			"coordinate_y_end": map[string]any{
				"type":        "number",
				"description": "Y coordinate of drag end position",
			},
		},
		"required": []string{"coordinate_x_start", "coordinate_y_start", "coordinate_x_end", "coordinate_y_end"},
	}
}

// Execute performs the drag operation.
func (t *BrowserDragTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_drag.context_cancelled")
	default:
	}

	// Extract parameters
	startX, okStartX := extractFloat64(params, "coordinate_x_start")
	startY, okStartY := extractFloat64(params, "coordinate_y_start")
	endX, okEndX := extractFloat64(params, "coordinate_x_end")
	endY, okEndY := extractFloat64(params, "coordinate_y_end")

	if !okStartX || !okStartY || !okEndX || !okEndY {
		return tools.NewToolResult("ERROR: Must provide coordinate_x_start, coordinate_y_start, coordinate_x_end, and coordinate_y_end to drag an element"), nil
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Perform drag operation
	if err := t.browser.Drag(ctx, startX, startY, endX, endY); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Drag operation failed: %s", err.Error())), nil
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := fmt.Sprintf("Dragged from coordinates (%.0f, %.0f) to (%.0f, %.0f). Make sure to verify the drag operation was successful.",
		startX, startY, endX, endY)
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserDragTool) Validate(params map[string]any) error {
	_, okStartX := extractFloat64(params, "coordinate_x_start")
	_, okStartY := extractFloat64(params, "coordinate_y_start")
	_, okEndX := extractFloat64(params, "coordinate_x_end")
	_, okEndY := extractFloat64(params, "coordinate_y_end")

	if !okStartX || !okStartY || !okEndX || !okEndY {
		return fmt.Errorf("all coordinates (coordinate_x_start, coordinate_y_start, coordinate_x_end, coordinate_y_end) are required")
	}
	return nil
}

// IsIdempotent returns false as dragging changes page state.
func (t *BrowserDragTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserDragTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserDragTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserDragTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserDragTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as dragging typically doesn't need confirmation.
func (t *BrowserDragTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserDragTool) Metadata() map[string]any {
	return nil
}
