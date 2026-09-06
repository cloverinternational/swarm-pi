// Browser view tool for viewing page content and interactive elements.
// Ported from ii-agent's browser/view.py
package ii

import (
	"context"
	"fmt"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserViewTool returns the visible interactive elements on the current page.
// It identifies clickable elements, input fields, and other interactive components,
// assigns each an index, and returns both a screenshot with highlights and a text list.
type BrowserViewTool struct {
	browser *BrowserManager
}

// NewBrowserViewTool creates a new browser view tool.
func NewBrowserViewTool(browser *BrowserManager) *BrowserViewTool {
	return &BrowserViewTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserViewTool) Name() string {
	return "browser_view_interactive_elements"
}

// DisplayName returns the human-readable display name.
func (t *BrowserViewTool) DisplayName() string {
	return "Browser View Interactive Elements"
}

// Description returns the tool description.
func (t *BrowserViewTool) Description() string {
	return `Return the visible interactive elements on the current browser page.

This tool scans the current page for interactive elements (links, buttons, inputs, etc.)
and returns them with unique indices that can be used for interaction.

Usage:
- Call this tool to see what elements are available for interaction
- Each element is assigned an index that can be used with click, enter_text, etc.
- Returns both a screenshot with highlighted elements and a text list

The returned elements include:
- Links (<a> tags)
- Buttons (<button> tags and role="button")
- Input fields (<input>, <textarea>, <select>)
- Interactive elements with onclick handlers
- Elements with tabindex attribute

Example output format:
[0]<a>Click here</a>
[1]<button>Submit</button>
[2]<input type="text">Search...</input>`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserViewTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// Execute identifies and returns interactive elements on the page.
func (t *BrowserViewTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_view.context_cancelled")
	default:
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Update state with interactive elements
	state, err := t.browser.UpdateStateWithInteractiveElements(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to get interactive elements: %s", err.Error())), nil
	}

	// Build highlighted elements text
	var highlightedElements strings.Builder
	highlightedElements.WriteString("<highlighted_elements>\n")

	if len(state.InteractiveElements) > 0 {
		for i := 0; i < len(state.InteractiveElements); i++ {
			element, exists := state.InteractiveElements[i]
			if !exists {
				continue
			}

			// Build start tag
			startTag := fmt.Sprintf("[%d]<%s", element.Index, element.TagName)
			if element.InputType != "" {
				startTag += fmt.Sprintf(` type="%s"`, element.InputType)
			}
			startTag += ">"

			// Clean element text (remove newlines)
			elementText := strings.ReplaceAll(element.Text, "\n", " ")

			highlightedElements.WriteString(fmt.Sprintf("%s%s</%s>\n",
				startTag, elementText, element.TagName))
		}
	}
	highlightedElements.WriteString("</highlighted_elements>")

	// Build result message
	msg := fmt.Sprintf(`Current URL: %s

Current viewport information:
%s`, state.URL, highlightedElements.String())

	result := tools.NewToolResult(msg)

	// Add screenshot with highlights as image content
	if state.ScreenshotWithHighlights != "" {
		result.AddContent(tools.ImageContentBase64(state.ScreenshotWithHighlights, "image/png"))
	} else if state.Screenshot != "" {
		// Fall back to regular screenshot if highlights not available
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserViewTool) Validate(params map[string]any) error {
	// No required parameters
	return nil
}

// IsIdempotent returns true as viewing doesn't change state.
func (t *BrowserViewTool) IsIdempotent() bool {
	return true
}

// IsReadOnly returns true as this tool only reads data.
func (t *BrowserViewTool) IsReadOnly() bool {
	return true
}

// RequiresPermission returns the required permissions.
func (t *BrowserViewTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserViewTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserViewTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as viewing doesn't need confirmation.
func (t *BrowserViewTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserViewTool) Metadata() map[string]any {
	return nil
}
