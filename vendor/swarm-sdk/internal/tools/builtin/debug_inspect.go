// Package builtin provides debug_inspect tool for agent-based introspection.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DebugInspectProvider is the interface that the app must implement
// to provide debug inspection capabilities to the agent.
type DebugInspectProvider interface {
	// InspectLogs returns log entries matching the criteria
	InspectLogs(pattern string, categories []string, level string, limit, offset int) (any, error)

	// InspectMessages returns message info, optionally for a specific index
	InspectMessages(messageID *int, limit, offset int) (any, error)

	// InspectRender returns detailed render info for a message
	InspectRender(messageID int) (any, error)

	// InspectRequests returns API request info matching the pattern
	InspectRequests(pattern string, limit, offset int) (any, error)

	// InspectTools returns tool execution traces
	InspectTools(pattern string, limit, offset int) (any, error)

	// InspectState returns current application state
	InspectState() (any, error)
}

// DebugInspectTool provides agent-based introspection into the runtime environment.
// It allows agents to debug themselves by inspecting logs, messages, requests,
// tool executions, and application state.

// DebugInspectParams are the typed parameters for the debug_inspect tool.
type DebugInspectParams struct {
	Target    string   `json:"target"              description:"What to inspect: logs, messages, render, requests, tools, state"  enum:"logs,messages,render,requests,tools,state" required:"true"`
	Pattern   string   `json:"pattern,omitempty"   description:"Regex pattern to search/filter results (case-insensitive)"`
	Category  []string `json:"category,omitempty"  description:"Filter logs by categories (SDK, MCP, TOOL, API, STATE, APP, THINKING, MODE)"`
	Level     string   `json:"level,omitempty"     description:"Minimum log level to include"                                          enum:"trace,debug,info,warn,error,fatal"`
	MessageID *int     `json:"message_id,omitempty" description:"Specific message index to inspect (0-based). Required for 'render' target."`
	Limit     int      `json:"limit,omitempty"     description:"Maximum number of results to return (default: 50, max: 200)"                default:"50"`
	Offset    int      `json:"offset,omitempty"    description:"Skip first N results for pagination (default: 0)"`
}

type DebugInspectTool struct {
	tools.BaseTool

	provider DebugInspectProvider
	logger   observability.Logger
	tracer   observability.Tracer
}

// DebugInspectConfig configures the debug_inspect tool.
type DebugInspectConfig struct {
	Provider DebugInspectProvider
	Logger   observability.Logger
	Tracer   observability.Tracer
}

// NewDebugInspectTool creates a new debug_inspect tool.
func NewDebugInspectTool(config DebugInspectConfig) (*DebugInspectTool, error) {
	if config.Provider == nil {
		return nil, sdkerr.Permanent("debug_inspect.missing_provider", "debug inspect provider is required")
	}
	if config.Logger == nil {
		return nil, sdkerr.Permanent("debug_inspect.missing_logger", "logger is required")
	}
	if config.Tracer == nil {
		return nil, sdkerr.Permanent("debug_inspect.missing_tracer", "tracer is required")
	}

	return &DebugInspectTool{
		provider: config.Provider,
		logger:   config.Logger,
		tracer:   config.Tracer,
	}, nil
}

// Name returns the tool name.
func (t *DebugInspectTool) Name() string {
	return "debug_inspect"
}

// Description returns the tool description.
func (t *DebugInspectTool) Description() string {
	return `Inspect the runtime environment for debugging. This tool provides introspection into:

- **logs**: Search through session logs with regex patterns and level/category filters
- **messages**: Inspect chat messages including content, tool calls, render info, and block structure
- **render**: Get detailed rendering information for a specific message (line positions, styles, blocks)
- **requests**: Inspect API requests/responses with timing, tokens, and error info
- **tools**: View tool execution traces with parameters, outputs, and hook executions
- **state**: Get current application state (provider, model, mode, viewport, settings)

Use this to understand how the conversation is rendered, debug tool executions, search for errors in logs, or inspect the current runtime configuration.`
}

// IsIdempotent returns true since inspecting doesn't change state.
func (t *DebugInspectTool) IsIdempotent() bool {
	return true
}

// Validate checks if the given parameters are valid for this tool.
func (t *DebugInspectTool) Validate(params map[string]any) error {
	target, ok := params["target"].(string)
	if !ok || target == "" {
		return sdkerr.Permanent("debug_inspect.missing_target", "target is required")
	}

	validTargets := map[string]bool{
		"logs":     true,
		"messages": true,
		"render":   true,
		"requests": true,
		"tools":    true,
		"state":    true,
	}

	if !validTargets[target] {
		return sdkerr.Permanent("debug_inspect.invalid_target",
			fmt.Sprintf("invalid target '%s', must be one of: logs, messages, render, requests, tools, state", target))
	}

	// render target requires message_id
	if target == "render" {
		if _, ok := params["message_id"]; !ok {
			return sdkerr.Permanent("debug_inspect.render_requires_message_id",
				"message_id is required for render target")
		}
	}

	return nil
}

// OptimizationHints provides guidance for efficient tool use.
func (t *DebugInspectTool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{
		PreferSequential:  false,
		EstimatedDuration: 50 * time.Millisecond,
		CanBatch:          false,
		BatchSize:         0,
		Priority:          50,
		MinimalLatency:    true,
		Cacheable:         false, // Don't cache - state changes
		CacheTTL:          0,
	}
}

// RequiresPermission returns the required permissions for this tool.
func (t *DebugInspectTool) RequiresPermission() []tools.Permission {
	return nil // No special permissions - debug mode already gates access
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *DebugInspectTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Parameters returns the JSON schema for tool parameters.
// Parameters returns the JSON schema for tool parameters.
func (t *DebugInspectTool) Parameters() any {
	return tools.SchemaFor[DebugInspectParams]()
}

// Execute executes the debug_inspect tool.
// Execute implements tools.Tool by decoding rawParams into DebugInspectParams and calling Run.
func (t *DebugInspectTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, fmt.Errorf("debug_inspect: marshal params: %w", err)
	}
	var p DebugInspectParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("debug_inspect: unmarshal params: %w", err)
	}
	return t.Run(ctx, p)
}

// Run executes the debug_inspect tool with fully typed parameters.
func (t *DebugInspectTool) Run(ctx context.Context, p DebugInspectParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "debug_inspect.execute")
	defer span.End()
	target := p.Target
	pattern := p.Pattern
	level := p.Level
	categories := p.Category
	messageID := p.MessageID
	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := max(p.Offset, 0)
	span.SetAttribute("target", target)
	span.SetAttribute("pattern", pattern)
	span.SetAttribute("limit", limit)

	t.logger.Debug(ctx, "debug_inspect.executing",
		observability.F("target", target),
		observability.F("pattern", pattern),
		observability.F("limit", limit))

	var result any
	var err error

	switch target {
	case "logs":
		result, err = t.provider.InspectLogs(pattern, categories, level, limit, offset)

	case "messages":
		result, err = t.provider.InspectMessages(messageID, limit, offset)

	case "render":
		if messageID == nil {
			return nil, sdkerr.Permanent("debug_inspect.render_requires_message_id",
				"message_id is required for render target")
		}
		result, err = t.provider.InspectRender(*messageID)

	case "requests":
		result, err = t.provider.InspectRequests(pattern, limit, offset)

	case "tools":
		result, err = t.provider.InspectTools(pattern, limit, offset)

	case "state":
		result, err = t.provider.InspectState()

	default:
		err = sdkerr.Permanent("debug_inspect.invalid_target",
			fmt.Sprintf("invalid target: %s", target))
	}

	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Marshal result to JSON
	output, marshalErr := json.MarshalIndent(result, "", "  ")
	if marshalErr != nil {
		span.RecordError(marshalErr)
		return nil, sdkerr.Permanent("debug_inspect.marshal_error",
			fmt.Sprintf("failed to marshal result: %v", marshalErr))
	}

	return &tools.ToolResult{
		Output: string(output),
	}, nil
}
