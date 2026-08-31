package computeruse

import (
	"context"
	"encoding/json"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ComputerUseTool wraps a computer use operation as a tools.Tool.
type ComputerUseTool struct {
	name        string
	description string
	params      map[string]any
	executor    ComputerExecutor
	logger      observability.Logger
}

// NewToolsFromExecutor creates tool instances from a Linux executor.
func NewToolsFromExecutor(executor ComputerExecutor, logger observability.Logger) []tools.Tool {
	defs := GetToolDefinitions()
	toolList := make([]tools.Tool, 0, len(defs))

	for _, def := range defs {
		tool := &ComputerUseTool{
			name:        def.Name,
			description: def.Description,
			params:      def.InputSchema,
			executor:    executor,
			logger:      logger,
		}
		toolList = append(toolList, tool)
	}

	return toolList
}

// Name returns the tool name.
func (t *ComputerUseTool) Name() string {
	return t.name
}

// Description returns the tool description.
func (t *ComputerUseTool) Description() string {
	return t.description
}

// Parameters returns the tool parameters schema.
func (t *ComputerUseTool) Parameters() any {
	return t.params
}

// Execute runs the tool.
func (t *ComputerUseTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	var result any
	var err error

	switch t.name {
	case ToolNameScreenshot:
		result, err = t.handleScreenshot(ctx, params)
	case ToolNameZoom:
		result, err = t.handleZoom(ctx, params)
	case ToolNameMouseMove:
		result, err = t.handleMouseMove(ctx, params)
	case ToolNameLeftClick:
		result, err = t.handleClick(ctx, params, MouseButtonLeft, ClickCountSingle)
	case ToolNameRightClick:
		result, err = t.handleClick(ctx, params, MouseButtonRight, ClickCountSingle)
	case ToolNameMiddleClick:
		result, err = t.handleClick(ctx, params, MouseButtonMiddle, ClickCountSingle)
	case ToolNameDoubleClick:
		result, err = t.handleClick(ctx, params, MouseButtonLeft, ClickCountDouble)
	case ToolNameTripleClick:
		result, err = t.handleClick(ctx, params, MouseButtonLeft, ClickCountTriple)
	case ToolNameScroll:
		result, err = t.handleScroll(ctx, params)
	case ToolNameLeftClickDrag:
		result, err = t.handleDrag(ctx, params)
	case ToolNameType:
		result, err = t.handleType(ctx, params)
	case ToolNameKey:
		result, err = t.handleKey(ctx, params)
	case ToolNameHoldKey:
		result, err = t.handleHoldKey(ctx, params)
	case ToolNameReadClipboard:
		result, err = t.handleReadClipboard(ctx)
	case ToolNameWriteClipboard:
		result, err = t.handleWriteClipboard(ctx, params)
	case ToolNameOpenApplication:
		result, err = t.handleOpenApplication(ctx, params)
	case ToolNameLeftMouseDown:
		err = t.executor.MouseDown(ctx)
		result = map[string]any{"success": true}
	case ToolNameLeftMouseUp:
		err = t.executor.MouseUp(ctx)
		result = map[string]any{"success": true}
	case ToolNameCursorPosition:
		result, err = t.handleCursorPosition(ctx)
	default:
		return tools.NewErrorResult(ErrUnsupportedPlatform), nil
	}

	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	// Convert result to appropriate content
	return t.resultToToolResult(result), nil
}

func (t *ComputerUseTool) resultToToolResult(result any) *tools.ToolResult {
	// Handle screenshot results with base64 image
	if m, ok := result.(map[string]any); ok {
		if base64, hasBase64 := m["base64"].(string); hasBase64 {
			// This is a screenshot result - return as image content
			return tools.NewContentResult(tools.ImageContentBase64(base64, "image/jpeg"))
		}
		// Convert to JSON for other map results
		jsonBytes, err := json.Marshal(m)
		if err != nil {
			return tools.NewToolResult("{\"success\": true}")
		}
		return tools.NewToolResult(string(jsonBytes))
	}
	return tools.NewToolResult("{\"success\": true}")
}

func (t *ComputerUseTool) handleScreenshot(ctx context.Context, params map[string]any) (any, error) {
	displayID := 0
	if id, ok := params["displayId"].(float64); ok {
		displayID = int(id)
	}

	opts := ScreenshotOptions{DisplayID: displayID}
	result, err := t.executor.Screenshot(ctx, opts)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"base64":    result.Base64,
		"width":     result.Width,
		"height":    result.Height,
		"displayId": result.DisplayID,
	}, nil
}

func (t *ComputerUseTool) handleZoom(ctx context.Context, params map[string]any) (any, error) {
	regionRaw, ok := params["region"].([]any)
	if !ok || len(regionRaw) != 4 {
		return nil, ErrInvalidCoordinate
	}

	region := Rect{
		X: int(regionRaw[0].(float64)),
		Y: int(regionRaw[1].(float64)),
		W: int(regionRaw[2].(float64)),
		H: int(regionRaw[3].(float64)),
	}

	result, err := t.executor.Zoom(ctx, region, nil, 0)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"base64": result.Base64,
		"width":  result.Width,
		"height": result.Height,
	}, nil
}

func (t *ComputerUseTool) handleMouseMove(ctx context.Context, params map[string]any) (any, error) {
	coord, err := parseCoord(params["coordinate"])
	if err != nil {
		return nil, err
	}

	if err := t.executor.MoveMouse(ctx, coord.X, coord.Y); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleClick(ctx context.Context, params map[string]any, button MouseButton, count ClickCount) (any, error) {
	coord, err := parseCoord(params["coordinate"])
	if err != nil {
		return nil, err
	}

	if err := t.executor.Click(ctx, coord.X, coord.Y, button, count, nil); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleScroll(ctx context.Context, params map[string]any) (any, error) {
	coord, err := parseCoord(params["coordinate"])
	if err != nil {
		return nil, err
	}

	direction, _ := params["direction"].(string)
	amount := 1
	if a, ok := params["amount"].(float64); ok {
		amount = int(a)
	}

	dx, dy := 0, 0
	switch direction {
	case "up":
		dy = amount
	case "down":
		dy = -amount
	case "left":
		dx = -amount
	case "right":
		dx = amount
	}

	if err := t.executor.Scroll(ctx, coord.X, coord.Y, dx, dy); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleDrag(ctx context.Context, params map[string]any) (any, error) {
	to, err := parseCoord(params["coordinate"])
	if err != nil {
		return nil, err
	}

	var from *Point
	if startRaw, ok := params["start_coordinate"].([]any); ok && len(startRaw) == 2 {
		from = &Point{
			X: int(startRaw[0].(float64)),
			Y: int(startRaw[1].(float64)),
		}
	}

	if err := t.executor.Drag(ctx, from, to); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleType(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)
	viaClipboard := false
	if v, ok := params["viaClipboard"].(bool); ok {
		viaClipboard = v
	}

	if err := t.executor.Type(ctx, text, TypeOptions{ViaClipboard: viaClipboard}); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleKey(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)
	repeat := 1
	if r, ok := params["repeat"].(float64); ok {
		repeat = int(r)
	}

	if err := t.executor.Key(ctx, text, repeat); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleHoldKey(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)
	duration, _ := params["duration"].(float64)

	keys := splitKeySeq(text)
	if err := t.executor.HoldKey(ctx, keys, int(duration*1000)); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleReadClipboard(ctx context.Context) (any, error) {
	text, err := t.executor.ReadClipboard(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{"text": text}, nil
}

func (t *ComputerUseTool) handleWriteClipboard(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)

	if err := t.executor.WriteClipboard(ctx, text); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleOpenApplication(ctx context.Context, params map[string]any) (any, error) {
	appID, _ := params["bundle_id"].(string)

	if err := t.executor.OpenApp(ctx, appID); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

func (t *ComputerUseTool) handleCursorPosition(ctx context.Context) (any, error) {
	pos, err := t.executor.GetCursorPosition(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"x": pos.X,
		"y": pos.Y,
	}, nil
}

func parseCoord(raw any) (*Point, error) {
	arr, ok := raw.([]any)
	if !ok || len(arr) != 2 {
		return nil, ErrInvalidCoordinate
	}

	x, ok1 := arr[0].(float64)
	y, ok2 := arr[1].(float64)
	if !ok1 || !ok2 {
		return nil, ErrInvalidCoordinate
	}

	return &Point{X: int(x), Y: int(y)}, nil
}

func splitKeySeq(seq string) []string {
	result := []string{}
	current := ""
	for _, c := range seq {
		if c == '+' && current != "" {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}
