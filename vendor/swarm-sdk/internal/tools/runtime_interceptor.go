// Package tools provides runtime interception for dynamic behavior changes.
package tools

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/runtime"
)

// RuntimeInterceptor wraps a tool registry and checks runtime config before execution
type RuntimeInterceptor struct {
	registry Registry
	watcher  *runtime.RuntimeWatcher
	logger   observability.Logger
}

// NewRuntimeInterceptor creates a new runtime interceptor
func NewRuntimeInterceptor(registry Registry, watcher *runtime.RuntimeWatcher, logger observability.Logger) *RuntimeInterceptor {
	return &RuntimeInterceptor{
		registry: registry,
		watcher:  watcher,
		logger:   logger,
	}
}

// Execute wraps tool execution with runtime config checks
func (ri *RuntimeInterceptor) Execute(ctx context.Context, toolName string, params map[string]any) (*ToolResult, error) {
	// Get current runtime config
	config := ri.watcher.GetConfig()

	// Check tool permissions from runtime config
	if perm, exists := config.GetToolPermission(toolName); exists {
		if !perm.Allowed {
			return nil, fmt.Errorf("tool %s is blocked by runtime config", toolName)
		}

		// Check path restrictions (if applicable)
		if path, hasPath := params["path"].(string); hasPath {
			if !ri.isPathAllowed(path, perm) {
				return nil, fmt.Errorf("path %s is not allowed by runtime config for tool %s", path, toolName)
			}
		}
	}

	// Execute pre-tool hooks from runtime config
	for _, hook := range config.GetHooks() {
		if hook.Type == "pre_tool" && ri.matchesFilter(toolName, params, hook.Filter) {
			if shouldBlock, err := ri.executeHookAction(ctx, hook, toolName, params); err != nil {
				return nil, fmt.Errorf("pre-tool hook %s failed: %w", hook.Name, err)
			} else if shouldBlock {
				return nil, fmt.Errorf("tool execution blocked by hook %s", hook.Name)
			}
		}
	}

	// Execute the actual tool via Executor (registry type-assertion or direct).
	exec := NewExecutor(ri.registry, nil)
	result, err := exec.Execute(ctx, toolName, params)

	// Execute post-tool hooks from runtime config
	for _, hook := range config.GetHooks() {
		if hook.Type == "post_tool" && ri.matchesFilter(toolName, params, hook.Filter) {
			if _, hookErr := ri.executeHookAction(ctx, hook, toolName, map[string]any{
				"result": result,
				"error":  err,
			}); hookErr != nil {
				ri.logger.Warn(ctx, "runtime.post_tool_hook_failed",
					observability.F("hook", hook.Name),
					observability.F("error", hookErr.Error()))
			}
		}
	}

	return result, err
}

// GetPermissionChecker returns the underlying registry's permission checker when available.
func (ri *RuntimeInterceptor) PermissionChecker() PermissionChecker {
	if provider, ok := ri.registry.(PermissionContextProvider); ok {
		return provider.PermissionChecker()
	}
	return nil
}

// GetRegistration returns the underlying registry's tool registration metadata when available.
func (ri *RuntimeInterceptor) Registration(name string) (*ToolRegistration, bool) {
	if provider, ok := ri.registry.(PermissionContextProvider); ok {
		return provider.Registration(name)
	}
	return nil, false
}

// Register delegates to wrapped registry
func (ri *RuntimeInterceptor) Register(tool Tool) error {
	return ri.registry.Register(tool)
}

// Unregister delegates to wrapped registry
func (ri *RuntimeInterceptor) Unregister(name string) error {
	return ri.registry.Unregister(name)
}

// Get delegates to wrapped registry
func (ri *RuntimeInterceptor) Get(name string) (Tool, error) {
	return ri.registry.Get(name)
}

// List delegates to wrapped registry
func (ri *RuntimeInterceptor) List() []string {
	return ri.registry.List()
}

// IsRegistered delegates to wrapped registry
func (ri *RuntimeInterceptor) IsRegistered(name string) bool {
	return ri.registry.IsRegistered(name)
}

// Helper: Check if path is allowed by permission rules
func (ri *RuntimeInterceptor) isPathAllowed(path string, perm runtime.ToolPermission) bool {
	// Check denied paths first (deny takes precedence)
	for _, denied := range perm.DeniedPaths {
		if matchesPattern(path, denied) {
			return false
		}
	}

	// If allowed paths specified, check if path matches any
	if len(perm.AllowedPaths) > 0 {
		for _, allowed := range perm.AllowedPaths {
			if matchesPattern(path, allowed) {
				return true
			}
		}
		return false // Path not in allowed list
	}

	// No restrictions
	return true
}

// Helper: Match filter conditions
func (ri *RuntimeInterceptor) matchesFilter(toolName string, params map[string]any, filter map[string]any) bool {
	if len(filter) == 0 {
		return true // No filter = match all
	}

	// Check tool_name filter
	if filterTool, ok := filter["tool_name"].(string); ok {
		if filterTool != toolName && filterTool != "*" {
			return false
		}
	}

	// Check param filters
	if filterParams, ok := filter["params"].(map[string]any); ok {
		for key, expectedValue := range filterParams {
			actualValue, exists := params[key]
			if !exists || actualValue != expectedValue {
				return false
			}
		}
	}

	return true
}

// Helper: Execute hook action
func (ri *RuntimeInterceptor) executeHookAction(ctx context.Context, hook runtime.DynamicHook, toolName string, data map[string]any) (bool, error) {
	action := hook.Action

	// Log action
	if logMsg, ok := action["log"].(string); ok {
		ri.logger.Info(ctx, "runtime.hook_action",
			observability.F("hook", hook.Name),
			observability.F("tool", toolName),
			observability.F("message", logMsg))
	}

	// Block action
	if block, ok := action["block"].(bool); ok && block {
		return true, nil
	}

	// Modify params action (future enhancement)
	// if modify, ok := action["modify_params"].(map[string]any); ok {
	//     for key, value := range modify {
	//         data[key] = value
	//     }
	// }

	return false, nil
}

// Helper: Simple glob pattern matching
func matchesPattern(path, pattern string) bool {
	// Simple implementation - could be enhanced with proper glob matching
	if pattern == "*" {
		return true
	}

	// Exact match
	if path == pattern {
		return true
	}

	// Prefix match (pattern ends with *)
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(path) >= len(prefix) && path[:len(prefix)] == prefix
	}

	return false
}
