package anthropic

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// Beta tool type constants based on Anthropic's API versioning.
const (
	ToolTypeComputerUse = "computer_20250124"
	ToolTypeTextEditor  = "text_editor_20250728"
	ToolTypeBash        = "bash_20250124"
	ToolTypeWebSearch   = "web_search_20250305"
)

// CreateComputerUseTool creates an Anthropic computer use beta tool.
// The computer use tool allows Claude to interact with desktop environments through:
// - Screenshot capture
// - Mouse control (click, drag, move)
// - Keyboard input
// - Desktop automation
//
// Parameters:
//   - displayWidthPx: Screen width in pixels (e.g., 1024, 1920)
//   - displayHeightPx: Screen height in pixels (e.g., 768, 1080)
//   - cacheControl: Optional cache control configuration (for prompt caching)
//
// Returns a provider.Tool that can be used in ChatRequest.Tools.
func CreateComputerUseTool(displayWidthPx int, displayHeightPx int, cacheControl *CacheControl) provider.Tool {
	// Computer use tool schema
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{
					"key",
					"type",
					"mouse_move",
					"left_click",
					"left_click_drag",
					"right_click",
					"middle_click",
					"double_click",
					"screenshot",
					"cursor_position",
				},
				"description": "The action to perform on the computer",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Text to type (for 'type' action)",
			},
			"coordinate": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "integer",
				},
				"description": "[x, y] coordinates for mouse actions",
			},
		},
		"required": []string{"action"},
	}

	// Build the tool with Anthropic-specific beta tool structure
	tool := provider.Tool{
		Name:        "computer",
		Type:        ToolTypeComputerUse,
		Description: "Use a mouse and keyboard to interact with a computer, and take screenshots.",
		Parameters:  inputSchema,
	}

	// Add display dimensions as metadata
	if tool.Metadata == nil {
		tool.Metadata = make(map[string]any)
	}
	tool.Metadata["display_width_px"] = displayWidthPx
	tool.Metadata["display_height_px"] = displayHeightPx
	tool.Metadata["display_number"] = 1 // Default display number

	// Add cache control if provided
	if cacheControl != nil {
		tool.Metadata["cache_control"] = cacheControl
	}

	return tool
}

// CreateTextEditorTool creates an Anthropic text editor beta tool.
// The text editor tool allows Claude to perform string replacement operations
// for editing files.
//
// Parameters:
//   - cacheControl: Optional cache control configuration (for prompt caching)
//
// Returns a provider.Tool that can be used in ChatRequest.Tools.
func CreateTextEditorTool(cacheControl *CacheControl) provider.Tool {
	// Text editor tool schema
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type": "string",
				"enum": []string{
					"view",
					"create",
					"str_replace",
					"insert",
					"undo_edit",
				},
				"description": "The editing command to execute",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the file",
			},
			"file_text": map[string]any{
				"type":        "string",
				"description": "File content (for 'create' command)",
			},
			"old_str": map[string]any{
				"type":        "string",
				"description": "String to replace (for 'str_replace' command)",
			},
			"new_str": map[string]any{
				"type":        "string",
				"description": "Replacement string (for 'str_replace' command)",
			},
			"insert_line": map[string]any{
				"type":        "integer",
				"description": "Line number to insert at (for 'insert' command)",
			},
			"view_range": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "integer",
				},
				"description": "[start_line, end_line] for 'view' command",
			},
		},
		"required": []string{"command", "path"},
	}

	// Build the tool with Anthropic-specific beta tool structure
	tool := provider.Tool{
		Name:        "str_replace_based_edit_tool",
		Type:        ToolTypeTextEditor,
		Description: "A text editor tool for viewing, creating, and editing files using string replacement.",
		Parameters:  inputSchema,
	}

	// Add cache control if provided
	if cacheControl != nil {
		if tool.Metadata == nil {
			tool.Metadata = make(map[string]any)
		}
		tool.Metadata["cache_control"] = cacheControl
	}

	return tool
}

// CreateBashTool creates an Anthropic bash beta tool.
// The bash tool allows Claude to execute shell commands in a bash environment.
//
// Parameters:
//   - cacheControl: Optional cache control configuration (for prompt caching)
//
// Returns a provider.Tool that can be used in ChatRequest.Tools.
func CreateBashTool(cacheControl *CacheControl) provider.Tool {
	// Bash tool schema
	inputSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The bash command to execute",
			},
			"restart": map[string]any{
				"type":        "boolean",
				"description": "Whether to restart the shell before executing (default: false)",
			},
		},
		"required": []string{"command"},
	}

	// Build the tool with Anthropic-specific beta tool structure
	tool := provider.Tool{
		Name:        "bash",
		Type:        ToolTypeBash,
		Description: "Execute bash commands in a persistent shell session.",
		Parameters:  inputSchema,
	}

	// Add cache control if provided
	if cacheControl != nil {
		if tool.Metadata == nil {
			tool.Metadata = make(map[string]any)
		}
		tool.Metadata["cache_control"] = cacheControl
	}

	return tool
}

// ParseBetaToolResult parses the result from a beta tool execution.
// Beta tools return results in specific formats that may need special handling.
//
// Parameters:
//   - toolName: The name of the beta tool
//   - result: The raw result from tool execution
//
// Returns the parsed result as a string.
func ParseBetaToolResult(toolName string, result any) string {
	// For now, beta tools return results as strings or structured data
	// that can be directly serialized
	switch v := result.(type) {
	case string:
		return v
	case map[string]any:
		// Handle structured results (e.g., screenshot data)
		if data, ok := v["output"].(string); ok {
			return data
		}
		if base64Data, ok := v["base64_image"].(string); ok {
			return "Screenshot captured: " + base64Data[:50] + "..."
		}
		// Fallback: return as formatted string
		return formatMapAsString(v)
	default:
		return ""
	}
}

// formatMapAsString formats a map as a readable string.
func formatMapAsString(m map[string]any) string {
	result := ""
	for key, value := range m {
		if result != "" {
			result += ", "
		}
		result += key + ": "
		switch v := value.(type) {
		case string:
			result += v
		case int, int64, float64, bool:
			result += toString(v)
		default:
			result += "..."
		}
	}
	return result
}

// toString converts various types to string.
func toString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return string(rune(val))
	case int64:
		return string(rune(val))
	case float64:
		return string(rune(int(val)))
	case bool:
		if val {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}
