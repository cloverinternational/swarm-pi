package builtin

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/steering"
)

// SteeringHook intercepts tool calls and evaluates them for steering
type SteeringHook struct {
	steering *steering.Steering
	priority int
}

// NewSteeringHook creates a new steering hook
func NewSteeringHook(s *steering.Steering) *SteeringHook {
	return &SteeringHook{
		steering: s,
		priority: 100, // High priority - run early in hook chain
	}
}

// Name returns the hook name
func (h *SteeringHook) Name() string {
	return "steering-hook"
}

// Priority returns the hook priority (higher = runs first)
func (h *SteeringHook) Priority() int {
	return h.priority
}

// Filter returns true for tool execution events
func (h *SteeringHook) Filter(event hooks.Event) bool {
	// Only process tool.before_execute events
	return event.Type == hooks.EventToolBeforeExecute
}

// OnEvent handles the tool execution event
func (h *SteeringHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	if h.steering == nil || !h.steering.IsEnabled() {
		return hooks.Continue(), nil
	}

	// Extract tool name and input from event data
	toolName, _ := event.Data["tool_name"].(string)
	toolInput, _ := event.Data["tool_input"].(map[string]any)

	if toolName == "" {
		return hooks.Continue(), nil
	}

	// Create evaluation context
	// Note: conversation is passed via event metadata or data

	// Evaluate the tool call
	decision, err := h.steering.EvaluateToolCall(ctx, toolName, toolInput, nil)
	if err != nil {
		// Fail-open: log error but continue
		return hooks.ContinueWithMessage(fmt.Sprintf("Steering error: %v", err)), nil
	}

	// Convert decision to hook result
	switch decision.Type {
	case steering.DecisionBlock:
		return hooks.Block(decision.Reasoning), nil

	case steering.DecisionModify:
		// Create modified event with updated tool input
		modified := event.Clone()
		if decision.Modified != nil {
			if modMap, ok := decision.Modified.(map[string]any); ok {
				maps.Copy(modified.Data, modMap)
			}
		}
		return hooks.ModifyWithMessage(modified, decision.Reasoning), nil

	default:
		return hooks.Continue(), nil
	}
}

// SetPriority allows adjusting the hook priority
func (h *SteeringHook) SetPriority(p int) {
	h.priority = p
}

// SteeringHookOption configures the steering hook
type SteeringHookOption func(*SteeringHook)

// WithSteering sets the steering instance
func WithSteering(s *steering.Steering) SteeringHookOption {
	return func(h *SteeringHook) {
		h.steering = s
	}
}

// WithPriority sets the hook priority
func WithPriority(p int) SteeringHookOption {
	return func(h *SteeringHook) {
		h.priority = p
	}
}

// IsWriteTool checks if a tool is a write operation
func IsWriteTool(toolName string) bool {
	writeTools := map[string]bool{
		"write_file":    true,
		"create_file":   true,
		"delete_file":   true,
		"str_replace":   true,
		"execute_shell": true,
		"file_write":    true,
		"bash":          true,
	}

	// Also check for common patterns
	if strings.Contains(toolName, "write") ||
		strings.Contains(toolName, "create") ||
		strings.Contains(toolName, "delete") {
		return true
	}

	return writeTools[toolName]
}
