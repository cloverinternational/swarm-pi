// Browser input tools for entering text into form fields.
// Ported from ii-agent's browser/enter_text.py and enter_text_multiple_fields.py
package ii

import (
	"context"
	"fmt"
	"strings"
	"time"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserEnterTextTool enters text using the keyboard.
// It can optionally click on coordinates before typing and press Enter after.
type BrowserEnterTextTool struct {
	browser *BrowserManager
}

// NewBrowserEnterTextTool creates a new browser enter text tool.
func NewBrowserEnterTextTool(browser *BrowserManager) *BrowserEnterTextTool {
	return &BrowserEnterTextTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserEnterTextTool) Name() string {
	return "browser_enter_text"
}

// DisplayName returns the human-readable display name.
func (t *BrowserEnterTextTool) DisplayName() string {
	return "Browser Enter Text"
}

// Description returns the tool description.
func (t *BrowserEnterTextTool) Description() string {
	return `Enter text with a keyboard in the browser.

If coordinates are provided, the tool will click on that position first before entering text.
This is useful for focusing on input fields before typing.

Usage:
- text: The text to type
- coordinate_x/coordinate_y: Optional coordinates to click before typing
- press_enter: Set to true to press Enter after typing (useful for form submission)
- override: Set to true to clear existing text before typing (Ctrl+A, Backspace)

Examples:
- Type in focused field: {"text": "Hello world"}
- Click and type: {"text": "search query", "coordinate_x": 300, "coordinate_y": 100}
- Type and submit: {"text": "search query", "press_enter": true}
- Replace existing text: {"text": "new text", "coordinate_x": 300, "coordinate_y": 100, "override": true}

Note: Always verify the text was entered correctly by checking the screenshot.`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserEnterTextTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "Text to enter with the keyboard",
			},
			"coordinate_x": map[string]any{
				"type":        "number",
				"description": "Optional X coordinate to click before entering text",
			},
			"coordinate_y": map[string]any{
				"type":        "number",
				"description": "Optional Y coordinate to click before entering text",
			},
			"press_enter": map[string]any{
				"type":        "boolean",
				"description": "If true, presses Enter after entering text. Useful for form submission.",
				"default":     false,
			},
			"override": map[string]any{
				"type":        "boolean",
				"description": "If true, clears existing text before entering new text. If false, appends.",
				"default":     false,
			},
		},
		"required": []string{"text"},
	}
}

// Execute enters text in the browser.
func (t *BrowserEnterTextTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_enter_text.context_cancelled")
	default:
	}

	// Extract text parameter
	text, ok := params["text"].(string)
	if !ok {
		return tools.NewToolResult("ERROR: text parameter is required"), nil
	}

	// Extract optional parameters
	coordinateX, hasX := extractFloat64(params, "coordinate_x")
	coordinateY, hasY := extractFloat64(params, "coordinate_y")
	pressEnter := false
	if pe, ok := params["press_enter"].(bool); ok {
		pressEnter = pe
	}
	override := false
	if ov, ok := params["override"].(bool); ok {
		override = ov
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Click on coordinates if provided
	if hasX && hasY {
		if err := t.browser.Click(ctx, coordinateX, coordinateY, false); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to click at coordinates: %s", err.Error())), nil
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Clear existing text if override is true
	if override {
		if err := t.browser.ClearInput(ctx); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to clear existing text: %s", err.Error())), nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Type the text
	if err := t.browser.TypeText(ctx, text); err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to type text: %s", err.Error())), nil
	}

	// Press Enter if requested
	if pressEnter {
		if err := t.browser.PressKey(ctx, "Enter"); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to press Enter: %s", err.Error())), nil
		}
		time.Sleep(2 * time.Second)
	}

	// Build result message
	var clickMsg string
	if hasX && hasY {
		clickMsg = fmt.Sprintf(" at coordinates (%.0f, %.0f)", coordinateX, coordinateY)
	}
	msg := fmt.Sprintf(`Entered "%s" on the keyboard%s. Make sure to double check that the text was entered to where you intended.`, text, clickMsg)

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserEnterTextTool) Validate(params map[string]any) error {
	_, ok := params["text"].(string)
	if !ok {
		return fmt.Errorf("text is required")
	}
	return nil
}

// IsIdempotent returns false as entering text changes state.
func (t *BrowserEnterTextTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserEnterTextTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserEnterTextTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserEnterTextTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserEnterTextTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as text entry doesn't need confirmation.
func (t *BrowserEnterTextTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserEnterTextTool) Metadata() map[string]any {
	return nil
}

// TextEntry represents a single text entry for multi-field input.
type TextEntry struct {
	Text        string  `json:"text"`
	CoordinateX float64 `json:"coordinate_x"`
	CoordinateY float64 `json:"coordinate_y"`
	PressEnter  bool    `json:"press_enter"`
	Override    bool    `json:"override"`
}

// BrowserEnterMultipleTextsTool enters text in multiple input fields sequentially.
// Useful for filling forms with multiple fields like login or registration forms.
type BrowserEnterMultipleTextsTool struct {
	browser *BrowserManager
}

// NewBrowserEnterMultipleTextsTool creates a new browser enter multiple texts tool.
func NewBrowserEnterMultipleTextsTool(browser *BrowserManager) *BrowserEnterMultipleTextsTool {
	return &BrowserEnterMultipleTextsTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserEnterMultipleTextsTool) Name() string {
	return "browser_enter_multi_texts"
}

// DisplayName returns the human-readable display name.
func (t *BrowserEnterMultipleTextsTool) DisplayName() string {
	return "Browser Enter Multiple Texts"
}

// Description returns the tool description.
func (t *BrowserEnterMultipleTextsTool) Description() string {
	return `Enter text in multiple input fields sequentially with a single call.

Useful for filling forms like:
- Login form: Fill username at (300, 200) and password at (300, 250)
- Registration: Fill name, email, and password fields
- Contact form: Fill name, email, phone, and message fields
- Profile update: Fill multiple profile fields

Each field will be clicked and filled in the order provided.

Usage:
- enter_texts: Array of objects with text and coordinates
  - text: The text to enter
  - coordinate_x: X coordinate of the input field
  - coordinate_y: Y coordinate of the input field
  - press_enter: Optional, press Enter after this field
  - override: Optional, clear existing text before entering

Example:
{
  "enter_texts": [
    {"text": "username", "coordinate_x": 300, "coordinate_y": 200},
    {"text": "password123", "coordinate_x": 300, "coordinate_y": 250, "press_enter": true}
  ]
}`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserEnterMultipleTextsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"enter_texts": map[string]any{
				"type":        "array",
				"description": "List of text entries to input on different fields",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "Text to enter",
						},
						"coordinate_x": map[string]any{
							"type":        "number",
							"description": "X coordinate to click before entering text",
						},
						"coordinate_y": map[string]any{
							"type":        "number",
							"description": "Y coordinate to click before entering text",
						},
						"press_enter": map[string]any{
							"type":        "boolean",
							"description": "If true, press Enter after entering this text",
							"default":     false,
						},
						"override": map[string]any{
							"type":        "boolean",
							"description": "If true, clear existing text before entering new text",
							"default":     false,
						},
					},
					"required": []string{"text", "coordinate_x", "coordinate_y"},
				},
				"minItems": 1,
			},
		},
		"required": []string{"enter_texts"},
	}
}

// Execute enters text in multiple fields.
func (t *BrowserEnterMultipleTextsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_enter_multi_texts.context_cancelled")
	default:
	}

	// Extract enter_texts parameter
	enterTextsRaw, ok := params["enter_texts"].([]any)
	if !ok {
		return tools.NewToolResult("ERROR: enter_texts parameter is required and must be an array"), nil
	}

	if len(enterTextsRaw) == 0 {
		return tools.NewToolResult("ERROR: enter_texts array cannot be empty"), nil
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Process each text entry
	var results []string
	for i, entryRaw := range enterTextsRaw {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Invalid entry at index %d", i)), nil
		}

		// Extract entry fields
		text, ok := entry["text"].(string)
		if !ok {
			return tools.NewToolResult(fmt.Sprintf("ERROR: text is required for entry %d", i)), nil
		}

		coordinateX, okX := extractFloat64(entry, "coordinate_x")
		coordinateY, okY := extractFloat64(entry, "coordinate_y")
		if !okX || !okY {
			return tools.NewToolResult(fmt.Sprintf("ERROR: coordinate_x and coordinate_y are required for entry %d", i)), nil
		}

		pressEnter := false
		if pe, ok := entry["press_enter"].(bool); ok {
			pressEnter = pe
		}
		override := false
		if ov, ok := entry["override"].(bool); ok {
			override = ov
		}

		// Click on coordinates
		if err := t.browser.Click(ctx, coordinateX, coordinateY, false); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to click for field %d: %s", i+1, err.Error())), nil
		}
		time.Sleep(500 * time.Millisecond)

		// Clear existing text if override is true
		if override {
			if err := t.browser.ClearInput(ctx); err != nil {
				return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to clear text for field %d: %s", i+1, err.Error())), nil
			}
			time.Sleep(100 * time.Millisecond)
		}

		// Type the text
		if err := t.browser.TypeText(ctx, text); err != nil {
			return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to type text for field %d: %s", i+1, err.Error())), nil
		}

		// Press Enter if requested
		if pressEnter {
			if err := t.browser.PressKey(ctx, "Enter"); err != nil {
				return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to press Enter for field %d: %s", i+1, err.Error())), nil
			}
			time.Sleep(1 * time.Second)
		}

		results = append(results, fmt.Sprintf(`Field %d: Entered "%s" at (%.0f, %.0f)`, i+1, text, coordinateX, coordinateY))

		// Small delay between fields
		time.Sleep(300 * time.Millisecond)
	}

	// Build result message
	msg := fmt.Sprintf("Successfully entered text in %d fields:\n%s", len(results), strings.Join(results, "\n"))

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserEnterMultipleTextsTool) Validate(params map[string]any) error {
	enterTexts, ok := params["enter_texts"].([]any)
	if !ok || len(enterTexts) == 0 {
		return fmt.Errorf("enter_texts is required and must be a non-empty array")
	}
	return nil
}

// IsIdempotent returns false as entering text changes state.
func (t *BrowserEnterMultipleTextsTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserEnterMultipleTextsTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserEnterMultipleTextsTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserEnterMultipleTextsTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserEnterMultipleTextsTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as text entry doesn't need confirmation.
func (t *BrowserEnterMultipleTextsTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserEnterMultipleTextsTool) Metadata() map[string]any {
	return nil
}
