// Browser dropdown tools for interacting with select elements.
// Ported from ii-agent's browser/dropdown.py
package ii

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BrowserGetSelectOptionsTool retrieves all options from a select element.
// Use this to see available options before selecting one.
type BrowserGetSelectOptionsTool struct {
	browser *BrowserManager
}

// NewBrowserGetSelectOptionsTool creates a new browser get select options tool.
func NewBrowserGetSelectOptionsTool(browser *BrowserManager) *BrowserGetSelectOptionsTool {
	return &BrowserGetSelectOptionsTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserGetSelectOptionsTool) Name() string {
	return "browser_get_select_options"
}

// DisplayName returns the human-readable display name.
func (t *BrowserGetSelectOptionsTool) DisplayName() string {
	return "Browser Get Select Options"
}

// Description returns the tool description.
func (t *BrowserGetSelectOptionsTool) Description() string {
	return `Get all options from a <select> dropdown element.

Use this tool when you need to see what options are available in a dropdown
before selecting one. First use browser_view_interactive_elements to find
the select element's index.

Usage:
- index: The index of the select element (from browser_view_interactive_elements)
- Returns a list of all options with their text and index
- Use the exact option text with browser_select_dropdown_option to select

Example workflow:
1. Call browser_view_interactive_elements to find the select element (e.g., [3]<select>)
2. Call browser_get_select_options with {"index": 3}
3. See options like: 0: option="Red", 1: option="Blue", 2: option="Green"
4. Call browser_select_dropdown_option with {"index": 3, "option": "Blue"}`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserGetSelectOptionsTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index": map[string]any{
				"type":        "integer",
				"description": "Index of the <select> element to get options from (from browser_view_interactive_elements)",
			},
		},
		"required": []string{"index"},
	}
}

// Execute retrieves options from the select element.
func (t *BrowserGetSelectOptionsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_get_select_options.context_cancelled")
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

	// Get current state to find the element
	state := t.browser.State()
	element, exists := state.InteractiveElements[index]
	if !exists {
		return tools.NewToolResult(fmt.Sprintf("ERROR: No element found with index %d", index)), nil
	}

	// Verify it's a select element
	if strings.ToLower(element.TagName) != "select" {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Element %d is not a select element, it's a %s", index, element.TagName)), nil
	}

	// Get options from the select element
	options, err := t.browser.SelectOptions(ctx, element.BrowserAgentID)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to get select options: %s", err.Error())), nil
	}

	// Format options for output
	var formattedOptions []string
	for _, opt := range options {
		encodedText, _ := json.Marshal(opt.Text)
		formattedOptions = append(formattedOptions, fmt.Sprintf("%d: option=%s", opt.Index, string(encodedText)))
	}

	msg := strings.Join(formattedOptions, "\n")
	msg += "\nIf you decide to use this select element, use the exact option name in select_dropdown_option"

	// Update state
	state, err = t.browser.UpdateState(ctx)
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
func (t *BrowserGetSelectOptionsTool) Validate(params map[string]any) error {
	_, ok := extractFloat64(params, "index")
	if !ok {
		return fmt.Errorf("index is required")
	}
	return nil
}

// IsIdempotent returns true as getting options doesn't change state.
func (t *BrowserGetSelectOptionsTool) IsIdempotent() bool {
	return true
}

// IsReadOnly returns true as this tool only reads data.
func (t *BrowserGetSelectOptionsTool) IsReadOnly() bool {
	return true
}

// RequiresPermission returns the required permissions.
func (t *BrowserGetSelectOptionsTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserGetSelectOptionsTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserGetSelectOptionsTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as getting options doesn't need confirmation.
func (t *BrowserGetSelectOptionsTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserGetSelectOptionsTool) Metadata() map[string]any {
	return nil
}

// BrowserSelectDropdownOptionTool selects an option from a select element.
// Use browser_get_select_options first to see available options.
type BrowserSelectDropdownOptionTool struct {
	browser *BrowserManager
}

// NewBrowserSelectDropdownOptionTool creates a new browser select dropdown option tool.
func NewBrowserSelectDropdownOptionTool(browser *BrowserManager) *BrowserSelectDropdownOptionTool {
	return &BrowserSelectDropdownOptionTool{browser: browser}
}

// Name returns the tool name.
func (t *BrowserSelectDropdownOptionTool) Name() string {
	return "browser_select_dropdown_option"
}

// DisplayName returns the human-readable display name.
func (t *BrowserSelectDropdownOptionTool) DisplayName() string {
	return "Browser Select Dropdown Option"
}

// Description returns the tool description.
func (t *BrowserSelectDropdownOptionTool) Description() string {
	return `Select an option from a <select> dropdown element by its text.

Use this tool after browser_get_select_options to select a specific option.
The option text must match exactly as shown in get_select_options.

Usage:
- index: The index of the select element (from browser_view_interactive_elements)
- option: The exact text of the option to select

Example workflow:
1. Call browser_view_interactive_elements to find the select element (e.g., [3]<select>)
2. Call browser_get_select_options with {"index": 3}
3. See options like: 0: option="Red", 1: option="Blue", 2: option="Green"
4. Call browser_select_dropdown_option with {"index": 3, "option": "Blue"}

Note: The option text must match exactly (case-sensitive).`
}

// Parameters returns the JSON schema for tool parameters.
func (t *BrowserSelectDropdownOptionTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index": map[string]any{
				"type":        "integer",
				"description": "Index of the <select> element (from browser_view_interactive_elements)",
			},
			"option": map[string]any{
				"type":        "string",
				"description": "Exact text of the option to select (case-sensitive)",
			},
		},
		"required": []string{"index", "option"},
	}
}

// Execute selects an option from the dropdown.
func (t *BrowserSelectDropdownOptionTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Check context
	select {
	case <-ctx.Done():
		return nil, sdkerr.Wrap(ctx.Err(), "browser_select_dropdown_option.context_cancelled")
	default:
	}

	// Extract parameters
	indexFloat, ok := extractFloat64(params, "index")
	if !ok {
		return tools.NewToolResult("ERROR: index parameter is required"), nil
	}
	index := int(indexFloat)

	option, ok := params["option"].(string)
	if !ok {
		return tools.NewToolResult("ERROR: option parameter is required"), nil
	}

	// Ensure browser is running
	if !t.browser.IsRunning() {
		return tools.NewToolResult("ERROR: Browser is not running. Use browser_navigation to start and navigate first."), nil
	}

	// Get current state to find the element
	state := t.browser.State()
	element, exists := state.InteractiveElements[index]
	if !exists {
		return tools.NewToolResult(fmt.Sprintf("ERROR: No element found with index %d", index)), nil
	}

	// Verify it's a select element
	if strings.ToLower(element.TagName) != "select" {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Element %d is not a select element, it's a %s", index, element.TagName)), nil
	}

	// Select the option
	if err := t.browser.SelectDropdownOption(ctx, element.BrowserAgentID, option); err != nil {
		// Get available options for error message
		options, _ := t.browser.SelectOptions(ctx, element.BrowserAgentID)
		var availableOptions []string
		for _, opt := range options {
			availableOptions = append(availableOptions, opt.Text)
		}

		errorMsg := fmt.Sprintf("ERROR: Failed to select option '%s': %s", option, err.Error())
		if len(availableOptions) > 0 {
			errorMsg += fmt.Sprintf(". Available options: %s", strings.Join(availableOptions, ", "))
		}
		return tools.NewToolResult(errorMsg), nil
	}

	// Update state
	state, err := t.browser.UpdateState(ctx)
	if err != nil {
		return tools.NewToolResult(fmt.Sprintf("ERROR: Failed to update browser state: %s", err.Error())), nil
	}

	msg := fmt.Sprintf("Selected option '%s' from dropdown at index %d", option, index)
	result := tools.NewToolResult(msg)

	// Add screenshot as image content
	if state.Screenshot != "" {
		result.AddContent(tools.ImageContentBase64(state.Screenshot, "image/png"))
	}

	return result, nil
}

// Validate checks if the given parameters are valid.
func (t *BrowserSelectDropdownOptionTool) Validate(params map[string]any) error {
	_, okIndex := extractFloat64(params, "index")
	_, okOption := params["option"].(string)
	if !okIndex || !okOption {
		return fmt.Errorf("index and option are required")
	}
	return nil
}

// IsIdempotent returns false as selecting an option changes state.
func (t *BrowserSelectDropdownOptionTool) IsIdempotent() bool {
	return false
}

// IsReadOnly returns false as this tool modifies browser state.
func (t *BrowserSelectDropdownOptionTool) IsReadOnly() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BrowserSelectDropdownOptionTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *BrowserSelectDropdownOptionTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText, tools.ContentTypeImage}
}

// OptimizationHints provides guidance for efficient tool use.
func (t *BrowserSelectDropdownOptionTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// ShouldConfirmExecute returns nil as selecting an option doesn't need confirmation.
func (t *BrowserSelectDropdownOptionTool) ShouldConfirmExecute(params map[string]any) *ConfirmationDetails {
	return nil
}

// Metadata returns nil as this tool has no special metadata.
func (t *BrowserSelectDropdownOptionTool) Metadata() map[string]any {
	return nil
}
