// Browser wait tool for waiting for page events.
// Ported from ii-agent's browser/wait.py
package ii

import (
	"context"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DefaultWaitDuration is the default time to wait when no duration is specified.
const DefaultWaitDuration = 1 * time.Second

// BrowserWaitTool waits for the page to load or for a specified duration.
// Useful when content needs time to load after an action.
type BrowserWaitTool struct {
	browser *BrowserManager
}

// NewBrowserWaitTool creates a new browser wait tool.
func NewBrowserWaitTool(browser *BrowserManager) *BrowserWaitTool {
	return &BrowserWaitTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserWaitTool) Name() string {
	return "browser_wait"
}

// DisplayName returns the human-readable display name.
func (t *BrowserWaitTool) DisplayName() string {
	return "Browser Wait"
}

// Description returns the tool description.
func (t *BrowserWaitTool) Description() string {
	return `Wait for the page to load or for a specified duration.

Use this tool when you need to:
- Wait for dynamic content to load after an action
- Allow animations or transitions to complete
- Give the page time to respond to user input
- Wait for AJAX requests to complete

By default, waits for 1 second and returns a fresh screenshot.

Usage:
- No parameters required for default 1-second wait
- Optionally specify duration_ms for custom wait time

Examples:
- Default wait: {}
- Wait 2 seconds: {"duration_ms": 2000}
- Wait 500ms: {"duration_ms": 500}

After waiting, the tool returns an updated screenshot showing the current page state.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserWaitTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"duration_ms": map[string]any{
				"type":        "integer",
				"description": "Optional duration to wait in milliseconds (default: 1000ms)",
				"default":     1000,
				"minimum":     0,
				"maximum":     30000,
			},
		},
		"required": []string{},
	}
}

// Execute waits for the specified duration.
func (t *BrowserWaitTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_wait.context_cancelled")
	default:
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Extract optional duration parameter
	duration := DefaultWaitDuration
	if durationMS, ok := extractFloat64(params, "duration_ms"); ok {
		duration = min(
			// Cap at 30 seconds
			time.Duration(durationMS)*time.Millisecond, 30*time.Second)
	}

	// Wait for the specified duration
	t.browser.Wait(duration)

	// Update state and handle PDF navigation
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult("ERROR: Failed to update browser state: " + err.Error()), nil
	}

	state, err = t.browser.HandlePDFNavigation(ctx)
	if err != nil {
		return tools.NewToolResult("ERROR: Failed to handle PDF navigation: " + err.Error()), nil
	}

	msg := "Waited for page"
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserWaitTool) Validate(params map[string]any) error {
	// Check optional duration_ms is within bounds
	if durationMS, ok := extractFloat64(params, "duration_ms"); ok {
		if durationMS < 0 || durationMS > 30000 {
			return nil // Will be capped in Execute
		}
	}
	return nil
}

// IsIdempotent returns true as waiting doesn't change state directly.
func (t *BrowserWaitTool) IsIdempotent() bool {
	return true
}

// IsReadOnly returns true as this tool only waits and reads state.
func (t *BrowserWaitTool) IsReadOnly() bool {
	return true
}

// RequiresPermission returns the required permissions.
func (t *BrowserWaitTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserWaitTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserWaitTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as waiting doesn't need confirmation.
func (t *BrowserWaitTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserWaitTool) Metadata() map[string]any {
	return nil
}
