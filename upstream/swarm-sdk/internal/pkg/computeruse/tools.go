package computeruse

import (
	"encoding/json"
)

// ToolNames defines the MCP tool names for computer use.
// These match Claude Code's computer-use tool names exactly.
const (
	ToolNameScreenshot      = "screenshot"
	ToolNameZoom            = "zoom"
	ToolNameMouseMove       = "mouse_move"
	ToolNameLeftClick       = "left_click"
	ToolNameRightClick      = "right_click"
	ToolNameMiddleClick     = "middle_click"
	ToolNameDoubleClick     = "double_click"
	ToolNameTripleClick     = "triple_click"
	ToolNameScroll          = "scroll"
	ToolNameLeftClickDrag   = "left_click_drag"
	ToolNameType            = "type"
	ToolNameKey             = "key"
	ToolNameHoldKey         = "hold_key"
	ToolNameReadClipboard   = "read_clipboard"
	ToolNameWriteClipboard  = "write_clipboard"
	ToolNameOpenApplication = "open_application"
	ToolNameRequestAccess   = "request_access"
	ToolNameListGrantedApps = "list_granted_applications"
	ToolNameLeftMouseDown   = "left_mouse_down"
	ToolNameLeftMouseUp     = "left_mouse_up"
	ToolNameCursorPosition  = "cursor_position"
	ToolNameComputerBatch   = "computer_batch"
)

// ToolDefinition represents an MCP tool definition.
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// GetToolDefinitions returns all computer use tool definitions.
func GetToolDefinitions() []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        ToolNameScreenshot,
			Description: "Take a screenshot of the current screen. Returns a base64-encoded JPEG image.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"displayId": map[string]any{
						"type":        "integer",
						"description": "Display ID to capture (0 for primary)",
					},
				},
			},
		},
		{
			Name:        ToolNameZoom,
			Description: "Zoom into a specific region of the screen.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"region": map[string]any{
						"type":        "array",
						"description": "Region to capture [x, y, width, height]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    4,
						"maxItems":    4,
					},
				},
				"required": []string{"region"},
			},
		},
		{
			Name:        ToolNameMouseMove,
			Description: "Move the mouse cursor to the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Target coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameLeftClick,
			Description: "Perform a left mouse click at the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Click coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameRightClick,
			Description: "Perform a right mouse click at the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Click coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameMiddleClick,
			Description: "Perform a middle mouse click at the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Click coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameDoubleClick,
			Description: "Perform a double left click at the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Click coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameTripleClick,
			Description: "Perform a triple left click at the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Click coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameScroll,
			Description: "Scroll at the specified coordinates.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"coordinate": map[string]any{
						"type":        "array",
						"description": "Scroll position [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
					"direction": map[string]any{
						"type":        "string",
						"description": "Scroll direction: 'up', 'down', 'left', or 'right'",
						"enum":        []string{"up", "down", "left", "right"},
					},
					"amount": map[string]any{
						"type":        "integer",
						"description": "Number of scroll steps (default: 1)",
						"default":     1,
					},
				},
				"required": []string{"coordinate", "direction"},
			},
		},
		{
			Name:        ToolNameLeftClickDrag,
			Description: "Perform a drag operation from one point to another.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"start_coordinate": map[string]any{
						"type":        "array",
						"description": "Start coordinates [x, y] (optional, uses current position if omitted)",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
					"coordinate": map[string]any{
						"type":        "array",
						"description": "End coordinates [x, y]",
						"items":       map[string]any{"type": "integer"},
						"minItems":    2,
						"maxItems":    2,
					},
				},
				"required": []string{"coordinate"},
			},
		},
		{
			Name:        ToolNameType,
			Description: "Type text at the current cursor position.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "Text to type",
					},
					"viaClipboard": map[string]any{
						"type":        "boolean",
						"description": "Use clipboard paste for complex text",
						"default":     false,
					},
				},
				"required": []string{"text"},
			},
		},
		{
			Name:        ToolNameKey,
			Description: "Press a key or key combination (e.g., 'ctrl+c', 'enter', 'escape').",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "Key sequence (e.g., 'ctrl+c', 'enter', 'escape')",
					},
					"repeat": map[string]any{
						"type":        "integer",
						"description": "Number of times to repeat the key press",
						"default":     1,
					},
				},
				"required": []string{"text"},
			},
		},
		{
			Name:        ToolNameHoldKey,
			Description: "Hold one or more keys for a specified duration.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "Key(s) to hold",
					},
					"duration": map[string]any{
						"type":        "integer",
						"description": "Duration in seconds to hold the key(s)",
						"minimum":     0,
					},
				},
				"required": []string{"text", "duration"},
			},
		},
		{
			Name:        ToolNameReadClipboard,
			Description: "Read the current clipboard content.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        ToolNameWriteClipboard,
			Description: "Write text to the clipboard.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "Text to write to clipboard",
					},
				},
				"required": []string{"text"},
			},
		},
		{
			Name:        ToolNameOpenApplication,
			Description: "Open an application by its ID (desktop entry name).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"bundle_id": map[string]any{
						"type":        "string",
						"description": "Application ID (desktop entry name, e.g., 'org.gnome.Nautilus')",
					},
				},
				"required": []string{"bundle_id"},
			},
		},
		{
			Name:        ToolNameRequestAccess,
			Description: "Request permission to interact with specific applications.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"apps": map[string]any{
						"type":        "array",
						"description": "Applications to request access for",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"displayName": map[string]any{
									"type": "string",
								},
							},
						},
					},
				},
			},
		},
		{
			Name:        ToolNameListGrantedApps,
			Description: "List applications that have been granted access.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        ToolNameLeftMouseDown,
			Description: "Press the left mouse button down (without releasing).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        ToolNameLeftMouseUp,
			Description: "Release the left mouse button.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        ToolNameCursorPosition,
			Description: "Get the current mouse cursor position.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        ToolNameComputerBatch,
			Description: "Execute multiple computer actions in a batch.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"actions": map[string]any{
						"type":        "array",
						"description": "List of actions to execute",
						"items": map[string]any{
							"type": "object",
						},
					},
				},
				"required": []string{"actions"},
			},
		},
	}
}

// ToMCPTools converts tool definitions to MCP tool format.
func (td ToolDefinition) ToMCPTools() map[string]any {
	return map[string]any{
		"name":        td.Name,
		"description": td.Description,
		"inputSchema": td.InputSchema,
	}
}

// ToolDefinitionsJSON returns the tool definitions as JSON.
func ToolDefinitionsJSON() ([]byte, error) {
	defs := GetToolDefinitions()
	tools := make([]map[string]any, len(defs))
	for i, def := range defs {
		tools[i] = def.ToMCPTools()
	}

	return json.Marshal(map[string]any{
		"tools": tools,
	})
}
