package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
)

// ServerConfig configures the computer use server.
type ServerConfig struct {
	// ServerName is the MCP server name (default: "computer-use")
	ServerName string

	// Executor is the computer executor to use
	Executor ComputerExecutor

	// Logger is the logger for debug output
	Logger Logger
}

// Logger defines the logging interface.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Server represents the computer use MCP server.
type Server struct {
	config   ServerConfig
	executor ComputerExecutor
}

// NewServer creates a new computer use MCP server.
func NewServer(config ServerConfig) (*Server, error) {
	if config.Executor == nil {
		return nil, fmt.Errorf("executor is required")
	}

	if config.ServerName == "" {
		config.ServerName = "computer-use"
	}

	return &Server{
		config:   config,
		executor: config.Executor,
	}, nil
}

// Name returns the server name.
func (s *Server) Name() string {
	return s.config.ServerName
}

// ListTools returns the list of available tools.
func (s *Server) ListTools() ([]map[string]any, error) {
	defs := GetToolDefinitions()
	tools := make([]map[string]any, len(defs))
	for i, def := range defs {
		tools[i] = def.ToMCPTools()
	}
	return tools, nil
}

// HandleToolCall handles a tool call.
func (s *Server) HandleToolCall(ctx context.Context, toolName string, params map[string]any) (any, error) {
	if s.config.Logger != nil {
		s.config.Logger.Debug("Handling tool call: %s", toolName)
	}

	switch toolName {
	case ToolNameScreenshot:
		return s.handleScreenshot(ctx, params)
	case ToolNameZoom:
		return s.handleZoom(ctx, params)
	case ToolNameMouseMove:
		return s.handleMouseMove(ctx, params)
	case ToolNameLeftClick:
		return s.handleClick(ctx, params, MouseButtonLeft, ClickCountSingle)
	case ToolNameRightClick:
		return s.handleClick(ctx, params, MouseButtonRight, ClickCountSingle)
	case ToolNameMiddleClick:
		return s.handleClick(ctx, params, MouseButtonMiddle, ClickCountSingle)
	case ToolNameDoubleClick:
		return s.handleClick(ctx, params, MouseButtonLeft, ClickCountDouble)
	case ToolNameTripleClick:
		return s.handleClick(ctx, params, MouseButtonLeft, ClickCountTriple)
	case ToolNameScroll:
		return s.handleScroll(ctx, params)
	case ToolNameLeftClickDrag:
		return s.handleDrag(ctx, params)
	case ToolNameType:
		return s.handleType(ctx, params)
	case ToolNameKey:
		return s.handleKey(ctx, params)
	case ToolNameHoldKey:
		return s.handleHoldKey(ctx, params)
	case ToolNameReadClipboard:
		return s.handleReadClipboard(ctx)
	case ToolNameWriteClipboard:
		return s.handleWriteClipboard(ctx, params)
	case ToolNameOpenApplication:
		return s.handleOpenApplication(ctx, params)
	case ToolNameLeftMouseDown:
		return s.handleMouseDown(ctx)
	case ToolNameLeftMouseUp:
		return s.handleMouseUp(ctx)
	case ToolNameCursorPosition:
		return s.handleCursorPosition(ctx)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// handleScreenshot handles the screenshot tool.
func (s *Server) handleScreenshot(ctx context.Context, params map[string]any) (any, error) {
	displayID := 0
	if id, ok := params["displayId"].(float64); ok {
		displayID = int(id)
	}

	opts := ScreenshotOptions{DisplayID: displayID}
	result, err := s.executor.Screenshot(ctx, opts)
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

// handleZoom handles the zoom tool.
func (s *Server) handleZoom(ctx context.Context, params map[string]any) (any, error) {
	regionRaw, ok := params["region"].([]any)
	if !ok || len(regionRaw) != 4 {
		return nil, fmt.Errorf("region must be [x, y, width, height]")
	}

	region := Rect{
		X: int(regionRaw[0].(float64)),
		Y: int(regionRaw[1].(float64)),
		W: int(regionRaw[2].(float64)),
		H: int(regionRaw[3].(float64)),
	}

	result, err := s.executor.Zoom(ctx, region, nil, 0)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"base64": result.Base64,
		"width":  result.Width,
		"height": result.Height,
	}, nil
}

// handleMouseMove handles the mouse_move tool.
func (s *Server) handleMouseMove(ctx context.Context, params map[string]any) (any, error) {
	coord, err := parseCoordinate(params["coordinate"])
	if err != nil {
		return nil, err
	}

	if err := s.executor.MoveMouse(ctx, coord.X, coord.Y); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleClick handles click tools.
func (s *Server) handleClick(ctx context.Context, params map[string]any, button MouseButton, count ClickCount) (any, error) {
	coord, err := parseCoordinate(params["coordinate"])
	if err != nil {
		return nil, err
	}

	if err := s.executor.Click(ctx, coord.X, coord.Y, button, count, nil); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleScroll handles the scroll tool.
func (s *Server) handleScroll(ctx context.Context, params map[string]any) (any, error) {
	coord, err := parseCoordinate(params["coordinate"])
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

	if err := s.executor.Scroll(ctx, coord.X, coord.Y, dx, dy); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleDrag handles the left_click_drag tool.
func (s *Server) handleDrag(ctx context.Context, params map[string]any) (any, error) {
	to, err := parseCoordinate(params["coordinate"])
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

	if err := s.executor.Drag(ctx, from, to); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleType handles the type tool.
func (s *Server) handleType(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)
	viaClipboard := false
	if v, ok := params["viaClipboard"].(bool); ok {
		viaClipboard = v
	}

	if err := s.executor.Type(ctx, text, TypeOptions{ViaClipboard: viaClipboard}); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleKey handles the key tool.
func (s *Server) handleKey(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)
	repeat := 1
	if r, ok := params["repeat"].(float64); ok {
		repeat = int(r)
	}

	if err := s.executor.Key(ctx, text, repeat); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleHoldKey handles the hold_key tool.
func (s *Server) handleHoldKey(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)
	duration, _ := params["duration"].(float64)

	// Parse key sequence into keys
	keys := parseKeySequence(text)

	if err := s.executor.HoldKey(ctx, keys, int(duration*1000)); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleReadClipboard handles the read_clipboard tool.
func (s *Server) handleReadClipboard(ctx context.Context) (any, error) {
	text, err := s.executor.ReadClipboard(ctx)
	if err != nil {
		return nil, err
	}

	return map[string]any{"text": text}, nil
}

// handleWriteClipboard handles the write_clipboard tool.
func (s *Server) handleWriteClipboard(ctx context.Context, params map[string]any) (any, error) {
	text, _ := params["text"].(string)

	if err := s.executor.WriteClipboard(ctx, text); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleOpenApplication handles the open_application tool.
func (s *Server) handleOpenApplication(ctx context.Context, params map[string]any) (any, error) {
	appID, _ := params["bundle_id"].(string)

	if err := s.executor.OpenApp(ctx, appID); err != nil {
		return nil, err
	}

	return map[string]any{"success": true}, nil
}

// handleMouseDown handles the left_mouse_down tool.
func (s *Server) handleMouseDown(ctx context.Context) (any, error) {
	if err := s.executor.MouseDown(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"success": true}, nil
}

// handleMouseUp handles the left_mouse_up tool.
func (s *Server) handleMouseUp(ctx context.Context) (any, error) {
	if err := s.executor.MouseUp(ctx); err != nil {
		return nil, err
	}
	return map[string]any{"success": true}, nil
}

// handleCursorPosition handles the cursor_position tool.
func (s *Server) handleCursorPosition(ctx context.Context) (any, error) {
	pos, err := s.executor.GetCursorPosition(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"x": pos.X,
		"y": pos.Y,
	}, nil
}

// parseCoordinate parses a coordinate from params.
func parseCoordinate(raw any) (*Point, error) {
	arr, ok := raw.([]any)
	if !ok || len(arr) != 2 {
		return nil, fmt.Errorf("coordinate must be [x, y]")
	}

	x, ok1 := arr[0].(float64)
	y, ok2 := arr[1].(float64)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("coordinate values must be numbers")
	}

	return &Point{X: int(x), Y: int(y)}, nil
}

// parseKeySequence parses a key sequence like "ctrl+c" into keys.
func parseKeySequence(seq string) []string {
	// Split on + and handle special cases
	parts := []string{}
	for _, p := range splitKeySequence(seq) {
		parts = append(parts, p)
	}
	return parts
}

// splitKeySequence splits a key sequence on + but handles escaped plus signs.
func splitKeySequence(seq string) []string {
	// Simple split for now
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

// Close closes the server.
func (s *Server) Close() error {
	return s.executor.Close()
}

// MarshalJSON marshals the server info for MCP.
func (s *Server) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any{
		"name":    s.config.ServerName,
		"version": "1.0.0",
	})
}
