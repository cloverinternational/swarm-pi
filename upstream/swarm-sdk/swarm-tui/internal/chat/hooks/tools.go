package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// HookTools provides tools for the LLM to manage hooks.
type HookTools struct {
	config        *HooksConfig
	hooksManager  *HooksManager
	workspaceRoot string
	toolRegistry  tools.Registry // For validating tool_matcher patterns
}

// NewHookTools creates a new HookTools instance.
func NewHookTools(config *HooksConfig, manager *HooksManager, workspaceRoot string, toolRegistry tools.Registry) *HookTools {
	return &HookTools{
		config:        config,
		hooksManager:  manager,
		workspaceRoot: workspaceRoot,
		toolRegistry:  toolRegistry,
	}
}

// GetTools returns all hook management tools.
func (ht *HookTools) GetTools() []tools.Tool {
	return []tools.Tool{
		&CreateHookTool{ht: ht},
		&UpdateHookTool{ht: ht},
		&ListHooksTool{ht: ht},
		&EnableHookTool{ht: ht},
		&DisableHookTool{ht: ht},
		&DeleteHookTool{ht: ht},
		&TestHookTool{ht: ht},
		&GetHookTool{ht: ht},
		&ListEventsTool{ht: ht},
		&ValidateHookTool{ht: ht},
		&ListAvailableToolsTool{ht: ht},
	}
}

// ---- Create Hook Tool ----

type CreateHookTool struct {
	ht *HookTools
}

func (t *CreateHookTool) Name() string { return "create_hook" }

func (t *CreateHookTool) Description() string {
	return `Create a new custom shell hook that executes when specific events occur.

Hooks can:
- Log events to files
- Send notifications
- Block dangerous operations (like Claude Code hooks)
- Validate tool inputs
- Intercept model requests/responses

EVENT TYPES (Unified naming - use either Claude Code or Gemini CLI names):

Session Events:
- session.start / SessionStart: When session begins
- session.end / SessionEnd: When session ends

Agent Events:
- user.prompt_submit / UserPromptSubmit / BeforeAgent: Before processing user prompt
- agent.stop / Stop / AfterAgent: When agent completes
- subagent.stop / SubagentStop: When a subagent stops (Claude Code only)

Tool Events:
- tool.before_execute / PreToolUse / BeforeTool: Before tool runs
- tool.after_execute / PostToolUse / AfterTool: After tool completes

Model Events (Gemini CLI):
- BeforeModel: Before LLM request (modify request)
- AfterModel: After LLM response (modify response)
- BeforeToolSelection: Before tool selection

Other Events:
- compact.before / PreCompact / PreCompress: Before context compaction
- notification / Notification: For notifications

Tool Matcher (Claude Code compatible):
- Use "tool_matcher" to filter by tool name
- Supports regex: "Bash", "Edit|Write", "mcp__.*"
- Empty or "*" matches all tools

Path Constraints:
- Use "path_allowlist" to allow only matching paths
- Use "path_denylist" to block matching paths
- Patterns support globbing (e.g., "src/*.go") or prefixes ("src/")

Environment variables available in command:
- CLAUDE_PROJECT_DIR: Project directory (Claude Code compatible)
- SWARMOS_TOOL_NAME: Name of the tool being executed
- SWARMOS_TOOL_PARAMS: Tool parameters as JSON
- SWARMOS_TOOL_FILE_PATH: File path (if applicable)
- SWARMOS_HOOK_EVENT: Event type that triggered

JSON input via stdin (Claude Code format):
- session_id, hook_event_name, tool_name, tool_input

Action types:
- "continue": Always continue (for logging/observability)
- "block": Block if ANY non-zero exit code
- "block_exit2": Block ONLY if exit code is 2 (Claude Code semantics - recommended)
- "block_on_output": Block if command produces stdout output`
}

func (t *CreateHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Unique name for the hook (kebab-case, e.g., 'log-file-ops')",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Human-readable description of what the hook does",
			},
			"event_patterns": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Event types to trigger on. Use SwarmOS names (tool.before_execute) or Claude Code names (PreToolUse)",
			},
			"tool_matcher": map[string]any{
				"type":        "string",
				"description": "Regex pattern to match tool names (e.g., 'Bash', 'Edit|Write', 'mcp__.*'). Empty matches all tools.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command to execute. Receives JSON via stdin and env vars (CLAUDE_PROJECT_DIR, TOOL_NAME, etc.)",
			},
			"priority": map[string]any{
				"type":        "integer",
				"description": "Execution priority 0-100 (higher runs first). Use 99 for security, 50 for general.",
				"default":     50,
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"continue", "block", "block_exit2", "block_on_output"},
				"description": "Action on command result. 'block_exit2' (recommended) blocks only on exit code 2 (Claude Code semantics)",
				"default":     "block_exit2",
			},
			"timeout": map[string]any{
				"type":        "string",
				"description": "Maximum execution time (e.g., '60s', '1m'). Claude Code default is 60s.",
				"default":     "60s",
			},
			"permission_policy": map[string]any{
				"type":        "string",
				"enum":        []string{"allow", "deny"},
				"description": "Whether this hook is allowed to execute.",
				"default":     "allow",
			},
			"path_allowlist": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of path patterns to allow (glob or prefix). If set, paths must match to trigger.",
			},
			"path_denylist": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of path patterns to deny (glob or prefix). Denied paths block trigger.",
			},
		},
		"required": []string{"name", "description", "event_patterns", "command"},
	}
}

func (t *CreateHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	description, _ := params["description"].(string)
	command, _ := params["command"].(string)
	action, _ := params["action"].(string)
	timeout, _ := params["timeout"].(string)
	permissionPolicy, _ := params["permission_policy"].(string)
	toolMatcher, _ := params["tool_matcher"].(string)
	pathAllowlist := readStringSlice(params["path_allowlist"])
	pathDenylist := readStringSlice(params["path_denylist"])
	priority := 50
	if p, ok := params["priority"].(float64); ok {
		priority = int(p)
	}

	var eventPatterns []string
	if patterns, ok := params["event_patterns"].([]any); ok {
		for _, p := range patterns {
			if s, ok := p.(string); ok {
				// Convert Claude Code event names to SwarmOS names
				converted := convertClaudeCodeEventName(s)
				eventPatterns = append(eventPatterns, converted)
			}
		}
	}

	// Validate
	if name == "" {
		return tools.NewErrorResult(fmt.Errorf("name is required")), nil
	}
	if command == "" {
		return tools.NewErrorResult(fmt.Errorf("command is required")), nil
	}
	if len(eventPatterns) == 0 {
		return tools.NewErrorResult(fmt.Errorf("at least one event_pattern is required")), nil
	}

	// VALIDATE TOOL_MATCHER BEFORE SAVING - Prevents creating hooks that never fire
	if toolMatcher != "" && toolMatcher != "*" {
		if err := t.ht.validateToolMatcher(toolMatcher, eventPatterns); err != nil {
			return tools.NewErrorResult(fmt.Errorf("VALIDATION FAILED: %v", err)), nil
		}
	}

	// Set Claude Code compatible defaults
	if action == "" {
		action = "block_exit2" // Claude Code semantics
	}
	if timeout == "" {
		timeout = "60s" // Claude Code default
	}
	if permissionPolicy == "" {
		permissionPolicy = string(hooks.HookPermissionAllow)
	}

	workingDir, err := SanitizeHookWorkingDir("", t.ht.workspaceRoot)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("invalid workspace root: %v", err)), nil
	}

	config := &hooks.ShellHookConfig{
		Name:             name,
		Description:      description,
		EventPatterns:    eventPatterns,
		ToolMatcher:      toolMatcher,
		Command:          command,
		Priority:         priority,
		Action:           action,
		Timeout:          timeout,
		Enabled:          false,
		PermissionPolicy: hooks.NormalizeHookPermissionPolicy(hooks.HookPermissionPolicy(permissionPolicy)),
		PathAllowlist:    pathAllowlist,
		PathDenylist:     pathDenylist,
		PassEventAsJSON:  true,
		WorkingDir:       workingDir,
		CreatedAt:        time.Now().Format(time.RFC3339),
	}

	// Add to config
	if err := t.ht.config.AddHook(config); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to create hook: %v", err)), nil
	}

	// Save config
	if err := t.ht.config.Save(); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to save config: %v", err)), nil
	}

	// Register with manager if available
	if t.ht.hooksManager != nil {
		hook, err := config.ToShellHook()
		if err != nil {
			return tools.NewErrorResult(fmt.Errorf("failed to build hook: %v", err)), nil
		}
		if err := t.ht.hooksManager.RegisterCustomHook(hook); err != nil {
			return tools.NewErrorResult(fmt.Errorf("failed to register hook: %v", err)), nil
		}
	}

	var resultBuilder strings.Builder
	resultBuilder.WriteString(fmt.Sprintf("Created hook '%s'\n", name))
	resultBuilder.WriteString(fmt.Sprintf("Events: %s\n", strings.Join(eventPatterns, ", ")))
	if toolMatcher != "" {
		resultBuilder.WriteString(fmt.Sprintf("Tool Matcher: %s\n", toolMatcher))
	}
	if len(pathAllowlist) > 0 {
		resultBuilder.WriteString(fmt.Sprintf("Path Allowlist: %s\n", strings.Join(pathAllowlist, ", ")))
	}
	if len(pathDenylist) > 0 {
		resultBuilder.WriteString(fmt.Sprintf("Path Denylist: %s\n", strings.Join(pathDenylist, ", ")))
	}
	resultBuilder.WriteString(fmt.Sprintf("Priority: %d\n", priority))
	resultBuilder.WriteString(fmt.Sprintf("Action: %s\n", action))
	resultBuilder.WriteString(fmt.Sprintf("Timeout: %s\n", timeout))
	resultBuilder.WriteString("Enabled: false (use enable_hook to activate)")

	return tools.NewToolResult(resultBuilder.String()), nil
}

// convertClaudeCodeEventName converts Claude Code event names to SwarmOS event names.
func convertClaudeCodeEventName(name string) string {
	claudeToSwarm := map[string]string{
		"PreToolUse":        hooks.EventToolBeforeExecute,
		"PostToolUse":       hooks.EventToolAfterExecute,
		"PermissionRequest": hooks.EventToolBeforeExecute,
		"UserPromptSubmit":  "user.prompt_submit",
		"Stop":              "agent.stop",
		"SubagentStop":      "subagent.stop",
		"SessionStart":      "session.start",
		"SessionEnd":        "session.end",
		"Notification":      "notification",
		"PreCompact":        "compact.before",
	}
	if converted, ok := claudeToSwarm[name]; ok {
		return converted
	}
	return name // Return as-is if not a Claude Code name
}

// validateToolMatcher validates a tool_matcher pattern against registered tools.
// Returns error with suggestions if pattern won't match any tools.
func (ht *HookTools) validateToolMatcher(toolMatcher string, eventPatterns []string) error {
	// Empty or "*" matches all tools - valid
	if toolMatcher == "" || toolMatcher == "*" {
		return nil
	}

	// Check if this is for a tool event
	isToolEvent := false
	for _, event := range eventPatterns {
		if strings.Contains(event, "tool.before_execute") ||
			strings.Contains(event, "tool.after_execute") ||
			strings.Contains(event, "PreToolUse") ||
			strings.Contains(event, "PostToolUse") ||
			strings.Contains(event, "BeforeTool") ||
			strings.Contains(event, "AfterTool") {
			isToolEvent = true
			break
		}
	}

	// If not a tool event, tool_matcher is ignored anyway
	if !isToolEvent {
		return nil
	}

	// Get list of registered tools
	if ht.toolRegistry == nil {
		// Can't validate without registry, but warn
		logDebug("[HookTools] WARNING: Cannot validate tool_matcher '%s' - no tool registry available", toolMatcher)
		return nil
	}

	registeredTools := ht.toolRegistry.List()
	if len(registeredTools) == 0 {
		logDebug("[HookTools] WARNING: Tool registry is empty, cannot validate tool_matcher")
		return nil
	}

	// Compile the pattern as regex (same logic as ShellHook)
	re, err := regexp.Compile("^(" + toolMatcher + ")$")
	if err != nil {
		return fmt.Errorf("invalid tool_matcher regex '%s': %v", toolMatcher, err)
	}

	// Check if pattern matches ANY registered tool
	var matches []string
	for _, toolName := range registeredTools {
		if re.MatchString(toolName) {
			matches = append(matches, toolName)
		}
	}

	if len(matches) == 0 {
		// NO MATCHES - this hook will NEVER fire!
		// Show first 10 tools as examples
		exampleTools := registeredTools
		if len(exampleTools) > 10 {
			exampleTools = exampleTools[:10]
		}

		return fmt.Errorf(
			"tool_matcher '%s' does not match any registered tools.\n\n"+
				"Registered tools include: %s\n\n"+
				"Common tool names:\n"+
				"  - file_write (NOT 'Write')\n"+
				"  - apply_patch (NOT 'Edit')\n"+
				"  - file_read (NOT 'Read')\n"+
				"  - Bash\n"+
				"  - Grep\n"+
				"  - list_dir\n"+
				"  - mcp_* (for MCP tools)\n\n"+
				"Use 'list_available_tools' to see all registered tools.\n"+
				"Use '*' to match all tools.",
			toolMatcher,
			strings.Join(exampleTools, ", "),
		)
	}

	logDebug("[HookTools] tool_matcher '%s' matches %d tools: %v", toolMatcher, len(matches), matches)
	return nil
}

func readStringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return normalizeStringSlice(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return normalizeStringSlice(out)
	default:
		return nil
	}
}

func normalizeStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (t *CreateHookTool) Validate(params map[string]any) error { return nil }
func (t *CreateHookTool) IsIdempotent() bool                   { return false }
func (t *CreateHookTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *CreateHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *CreateHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- List Hooks Tool ----

type ListHooksTool struct {
	ht *HookTools
}

func (t *ListHooksTool) Name() string { return "list_hooks" }

func (t *ListHooksTool) Description() string {
	return "List all hooks (both built-in and custom). Shows name, status, priority, and event patterns."
}

func (t *ListHooksTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListHooksTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	var sb strings.Builder

	// Built-in hooks
	sb.WriteString("== Built-in Hooks ==\n")
	builtinHooks := []struct {
		name     string
		priority int
		category string
	}{
		{"logging", 90, "observability"},
		{"metrics", 85, "observability"},
		{"audit", 80, "compliance"},
		{"tracing", 95, "observability"},
	}

	for _, h := range builtinHooks {
		enabled := "OFF"
		if t.ht.hooksManager != nil && t.ht.hooksManager.IsEnabled(h.name) {
			enabled = "ON"
		}
		sb.WriteString(fmt.Sprintf("  %s [%s] P:%d (%s) Perm: allow\n", h.name, enabled, h.priority, h.category))
	}

	// Custom hooks
	sb.WriteString("\n== Custom Hooks ==\n")
	customHooks := t.ht.config.ListHooks()
	if len(customHooks) == 0 {
		sb.WriteString("  (no custom hooks)\n")
	} else {
		for _, h := range customHooks {
			enabled := "OFF"
			if h.Enabled {
				enabled = "ON"
			}
			permission := hooks.NormalizeHookPermissionPolicy(h.PermissionPolicy)
			sb.WriteString(fmt.Sprintf("  %s [%s] P:%d\n", h.Name, enabled, h.Priority))
			sb.WriteString(fmt.Sprintf("    Events: %s\n", strings.Join(h.EventPatterns, ", ")))
			if h.ToolMatcher != "" {
				sb.WriteString(fmt.Sprintf("    Tool Matcher: %s\n", h.ToolMatcher))
			}
			if len(h.PathAllowlist) > 0 {
				sb.WriteString(fmt.Sprintf("    Path Allowlist: %s\n", strings.Join(h.PathAllowlist, ", ")))
			}
			if len(h.PathDenylist) > 0 {
				sb.WriteString(fmt.Sprintf("    Path Denylist: %s\n", strings.Join(h.PathDenylist, ", ")))
			}
			sb.WriteString(fmt.Sprintf("    Action: %s, Timeout: %s, Perm: %s\n", h.Action, h.Timeout, permission))
		}
	}

	return tools.NewToolResult(sb.String()), nil
}

func (t *ListHooksTool) Validate(params map[string]any) error { return nil }
func (t *ListHooksTool) IsIdempotent() bool                   { return true }
func (t *ListHooksTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *ListHooksTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ListHooksTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Enable Hook Tool ----

type EnableHookTool struct {
	ht *HookTools
}

func (t *EnableHookTool) Name() string { return "enable_hook" }

func (t *EnableHookTool) Description() string {
	return "Enable a hook by name. Works for both built-in and custom hooks."
}

func (t *EnableHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the hook to enable",
			},
		},
		"required": []string{"name"},
	}
}

func (t *EnableHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(fmt.Errorf("name is required")), nil
	}

	// Try built-in first
	builtinHooks := []string{"logging", "metrics", "audit", "tracing"}
	for _, h := range builtinHooks {
		if h == name {
			if t.ht.hooksManager != nil {
				if err := t.ht.hooksManager.EnableHook(name); err != nil {
					return tools.NewErrorResult(fmt.Errorf("failed to enable hook: %v", err)), nil
				}
			}
			return tools.NewToolResult(fmt.Sprintf("Enabled built-in hook: %s", name)), nil
		}
	}

	// Try custom hook
	if err := t.ht.config.SetEnabled(name, true); err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook not found: %s", name)), nil
	}

	if err := t.ht.config.Save(); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to save: %v", err)), nil
	}

	// Re-register with manager
	if t.ht.hooksManager != nil {
		config, _ := t.ht.config.GetHook(name)
		if config != nil {
			workingDir, err := SanitizeHookWorkingDir(config.WorkingDir, t.ht.workspaceRoot)
			if err != nil {
				return tools.NewErrorResult(fmt.Errorf("invalid hook working_dir: %v", err)), nil
			}
			config.WorkingDir = workingDir
			hook, err := config.ToShellHook()
			if err != nil {
				return tools.NewErrorResult(fmt.Errorf("invalid hook config: %v", err)), nil
			}
			if err := t.ht.hooksManager.RegisterCustomHook(hook); err != nil {
				return tools.NewErrorResult(fmt.Errorf("failed to register hook: %v", err)), nil
			}
		}
	}

	return tools.NewToolResult(fmt.Sprintf("Enabled custom hook: %s", name)), nil
}

func (t *EnableHookTool) Validate(params map[string]any) error { return nil }
func (t *EnableHookTool) IsIdempotent() bool                   { return false }
func (t *EnableHookTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *EnableHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *EnableHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Disable Hook Tool ----

type DisableHookTool struct {
	ht *HookTools
}

func (t *DisableHookTool) Name() string { return "disable_hook" }

func (t *DisableHookTool) Description() string {
	return "Disable a hook by name. The hook remains configured but won't execute."
}

func (t *DisableHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the hook to disable",
			},
		},
		"required": []string{"name"},
	}
}

func (t *DisableHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(fmt.Errorf("name is required")), nil
	}

	// Try built-in first
	builtinHooks := []string{"logging", "metrics", "audit", "tracing"}
	for _, h := range builtinHooks {
		if h == name {
			if t.ht.hooksManager != nil {
				if err := t.ht.hooksManager.DisableHook(name); err != nil {
					return tools.NewErrorResult(fmt.Errorf("failed to disable hook: %v", err)), nil
				}
			}
			return tools.NewToolResult(fmt.Sprintf("Disabled built-in hook: %s", name)), nil
		}
	}

	// Try custom hook
	if err := t.ht.config.SetEnabled(name, false); err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook not found: %s", name)), nil
	}

	if err := t.ht.config.Save(); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to save: %v", err)), nil
	}

	// Unregister from manager
	if t.ht.hooksManager != nil {
		if err := t.ht.hooksManager.UnregisterCustomHook(name); err != nil {
			return tools.NewErrorResult(fmt.Errorf("failed to unregister hook: %v", err)), nil
		}
	}

	return tools.NewToolResult(fmt.Sprintf("Disabled custom hook: %s", name)), nil
}

func (t *DisableHookTool) Validate(params map[string]any) error { return nil }
func (t *DisableHookTool) IsIdempotent() bool                   { return false }
func (t *DisableHookTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *DisableHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *DisableHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Delete Hook Tool ----

type DeleteHookTool struct {
	ht *HookTools
}

func (t *DeleteHookTool) Name() string { return "delete_hook" }

func (t *DeleteHookTool) Description() string {
	return "Permanently delete a custom hook. Built-in hooks cannot be deleted."
}

func (t *DeleteHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the custom hook to delete",
			},
		},
		"required": []string{"name"},
	}
}

func (t *DeleteHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(fmt.Errorf("name is required")), nil
	}

	// Check if built-in
	builtinHooks := []string{"logging", "metrics", "audit", "tracing"}
	if slices.Contains(builtinHooks, name) {
		return tools.NewErrorResult(fmt.Errorf("cannot delete built-in hooks")), nil
	}

	// Delete custom hook
	if err := t.ht.config.DeleteHook(name); err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook not found: %s", name)), nil
	}

	if err := t.ht.config.Save(); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to save: %v", err)), nil
	}

	// Unregister from manager
	if t.ht.hooksManager != nil {
		if err := t.ht.hooksManager.UnregisterCustomHook(name); err != nil {
			return tools.NewErrorResult(fmt.Errorf("failed to unregister hook: %v", err)), nil
		}
	}

	return tools.NewToolResult(fmt.Sprintf("Deleted hook: %s", name)), nil
}

func (t *DeleteHookTool) Validate(params map[string]any) error { return nil }
func (t *DeleteHookTool) IsIdempotent() bool                   { return false }
func (t *DeleteHookTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *DeleteHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *DeleteHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Test Hook Tool ----

type TestHookTool struct {
	ht *HookTools
}

func (t *TestHookTool) Name() string { return "test_hook" }

func (t *TestHookTool) Description() string {
	return "Test a hook by simulating an event. Useful for verifying hook behavior before enabling."
}

func (t *TestHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"hook_name": map[string]any{
				"type":        "string",
				"description": "Name of the hook to test",
			},
			"event_type": map[string]any{
				"type":        "string",
				"description": "Event type to simulate (e.g., 'tool.before_execute')",
			},
			"test_data": map[string]any{
				"type":        "object",
				"description": "Test data to pass to the hook (tool_name, file_path, etc.)",
			},
		},
		"required": []string{"hook_name", "event_type"},
	}
}

func (t *TestHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	hookName, _ := params["hook_name"].(string)
	eventType, _ := params["event_type"].(string)
	testData, _ := params["test_data"].(map[string]any)

	if hookName == "" {
		return tools.NewErrorResult(fmt.Errorf("hook_name is required")), nil
	}
	if eventType == "" {
		return tools.NewErrorResult(fmt.Errorf("event_type is required")), nil
	}

	// Get hook config
	config, err := t.ht.config.GetHook(hookName)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook not found: %s", hookName)), nil
	}
	if hooks.NormalizeHookPermissionPolicy(config.PermissionPolicy) == hooks.HookPermissionDeny {
		return tools.NewToolResult(fmt.Sprintf("Hook '%s' is denied by permission policy", hookName)), nil
	}

	workingDir, err := SanitizeHookWorkingDir(config.WorkingDir, t.ht.workspaceRoot)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("invalid hook working_dir: %v", err)), nil
	}
	config.WorkingDir = workingDir

	// Create shell hook
	hook, err := config.ToShellHook()
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("invalid hook config: %v", err)), nil
	}

	// Create test event
	event := hooks.Event{
		ID:        "test-event-001",
		Type:      eventType,
		Timestamp: time.Now(),
		Data:      testData,
	}

	// Check if hook would filter this event
	if !hook.Filter(event) {
		return tools.NewToolResult(fmt.Sprintf("Hook '%s' would NOT process event type '%s'\nConfigured patterns: %s",
			hookName, eventType, strings.Join(config.EventPatterns, ", "))), nil
	}

	// Execute hook
	result, err := hook.OnEvent(ctx, event)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook execution error: %v", err)), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Test result for hook '%s':\n", hookName))
	sb.WriteString(fmt.Sprintf("  Event: %s\n", eventType))
	sb.WriteString(fmt.Sprintf("  Action: %s\n", result.Action.String()))
	if result.Message != "" {
		sb.WriteString(fmt.Sprintf("  Output: %s\n", result.Message))
	}

	return tools.NewToolResult(sb.String()), nil
}

func (t *TestHookTool) Validate(params map[string]any) error { return nil }
func (t *TestHookTool) IsIdempotent() bool                   { return true }
func (t *TestHookTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *TestHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *TestHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Get Hook Tool ----

type GetHookTool struct {
	ht *HookTools
}

func (t *GetHookTool) Name() string { return "get_hook" }

func (t *GetHookTool) Description() string {
	return "Get detailed information about a specific hook."
}

func (t *GetHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the hook to get details for",
			},
		},
		"required": []string{"name"},
	}
}

func (t *GetHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(fmt.Errorf("name is required")), nil
	}

	// Check built-in
	builtinInfo := map[string]string{
		"logging": "Logs all events to ~/.swarmos/swarmos.log",
		"metrics": "Collects execution metrics and performance data",
		"audit":   "Audits sensitive operations for compliance",
		"tracing": "Distributed tracing with span creation",
	}
	if desc, ok := builtinInfo[name]; ok {
		enabled := "OFF"
		if t.ht.hooksManager != nil && t.ht.hooksManager.IsEnabled(name) {
			enabled = "ON"
		}
		return tools.NewToolResult(fmt.Sprintf("Built-in Hook: %s\nStatus: %s\nDescription: %s", name, enabled, desc)), nil
	}

	// Get custom hook
	config, err := t.ht.config.GetHook(name)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook not found: %s", name)), nil
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return tools.NewToolResult(string(data)), nil
}

func (t *GetHookTool) Validate(params map[string]any) error { return nil }
func (t *GetHookTool) IsIdempotent() bool                   { return true }
func (t *GetHookTool) RequiresPermission() []tools.Permission {
	return nil // Hooks are always allowed - individual hooks can be enabled/disabled
}
func (t *GetHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *GetHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Update Hook Tool ----

type UpdateHookTool struct {
	ht *HookTools
}

func (t *UpdateHookTool) Name() string { return "update_hook" }

func (t *UpdateHookTool) Description() string {
	return `Update an existing custom hook. Only the fields you specify will be updated.
Built-in hooks cannot be updated. Use enable_hook/disable_hook to toggle them.`
}

func (t *UpdateHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the hook to update (required)",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "New description (optional)",
			},
			"event_patterns": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "New event patterns (optional)",
			},
			"tool_matcher": map[string]any{
				"type":        "string",
				"description": "New tool matcher regex (optional)",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "New shell command (optional)",
			},
			"priority": map[string]any{
				"type":        "integer",
				"description": "New priority 0-100 (optional)",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"continue", "block", "block_exit2", "block_on_output"},
				"description": "New action type (optional)",
			},
			"timeout": map[string]any{
				"type":        "string",
				"description": "New timeout (optional)",
			},
			"path_allowlist": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "New path allowlist (optional)",
			},
			"path_denylist": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "New path denylist (optional)",
			},
		},
		"required": []string{"name"},
	}
}

func (t *UpdateHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	name, _ := params["name"].(string)
	if name == "" {
		return tools.NewErrorResult(fmt.Errorf("name is required")), nil
	}

	// Check if built-in
	builtinHooks := []string{"logging", "metrics", "audit", "tracing"}
	if slices.Contains(builtinHooks, name) {
		return tools.NewErrorResult(fmt.Errorf("cannot update built-in hooks")), nil
	}

	// Get existing hook
	config, err := t.ht.config.GetHook(name)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("hook not found: %s", name)), nil
	}

	// Update fields if provided
	if desc, ok := params["description"].(string); ok {
		config.Description = desc
	}
	if patterns, ok := params["event_patterns"].([]any); ok {
		var eventPatterns []string
		for _, p := range patterns {
			if s, ok := p.(string); ok {
				eventPatterns = append(eventPatterns, convertClaudeCodeEventName(s))
			}
		}
		if len(eventPatterns) > 0 {
			config.EventPatterns = eventPatterns
		}
	}
	if matcher, ok := params["tool_matcher"].(string); ok {
		config.ToolMatcher = matcher
	}
	if cmd, ok := params["command"].(string); ok {
		config.Command = cmd
	}
	if priority, ok := params["priority"].(float64); ok {
		config.Priority = int(priority)
	}
	if action, ok := params["action"].(string); ok {
		config.Action = action
	}
	if timeout, ok := params["timeout"].(string); ok {
		config.Timeout = timeout
	}
	if allowlist := readStringSlice(params["path_allowlist"]); allowlist != nil {
		config.PathAllowlist = allowlist
	}
	if denylist := readStringSlice(params["path_denylist"]); denylist != nil {
		config.PathDenylist = denylist
	}

	// Save config
	if err := t.ht.config.UpdateHook(name, config); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to update hook: %v", err)), nil
	}
	if err := t.ht.config.Save(); err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to save config: %v", err)), nil
	}

	// Re-register if enabled and manager available
	if config.Enabled && t.ht.hooksManager != nil {
		// Unregister old
		_ = t.ht.hooksManager.UnregisterCustomHook(name)
		// Register updated
		hook, err := config.ToShellHook()
		if err == nil {
			_ = t.ht.hooksManager.RegisterCustomHook(hook)
		}
	}

	return tools.NewToolResult(fmt.Sprintf("Updated hook '%s' successfully", name)), nil
}

func (t *UpdateHookTool) Validate(params map[string]any) error        { return nil }
func (t *UpdateHookTool) IsIdempotent() bool                          { return false }
func (t *UpdateHookTool) RequiresPermission() []tools.Permission      { return nil }
func (t *UpdateHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *UpdateHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- List Events Tool ----

type ListEventsTool struct {
	ht *HookTools
}

func (t *ListEventsTool) Name() string { return "list_hook_events" }

func (t *ListEventsTool) Description() string {
	return `List all available hook event types with their aliases.
Shows both Claude Code and Gemini CLI naming conventions.`
}

func (t *ListEventsTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListEventsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	var sb strings.Builder

	sb.WriteString("HOOK EVENT TYPES\n")
	sb.WriteString("================\n\n")

	sb.WriteString("SESSION EVENTS:\n")
	sb.WriteString("  session.start | SessionStart\n")
	sb.WriteString("    Fires when a session begins\n")
	sb.WriteString("  session.end | SessionEnd\n")
	sb.WriteString("    Fires when a session ends\n\n")

	sb.WriteString("AGENT EVENTS:\n")
	sb.WriteString("  user.prompt_submit | UserPromptSubmit | BeforeAgent\n")
	sb.WriteString("    Before processing user prompt (can inject context)\n")
	sb.WriteString("  agent.stop | Stop | AfterAgent\n")
	sb.WriteString("    When agent completes (main agent)\n")
	sb.WriteString("  subagent.stop | SubagentStop\n")
	sb.WriteString("    When a subagent stops (Claude Code only)\n\n")

	sb.WriteString("TOOL EVENTS:\n")
	sb.WriteString("  tool.before_execute | PreToolUse | BeforeTool\n")
	sb.WriteString("    Before tool execution (can block)\n")
	sb.WriteString("  tool.after_execute | PostToolUse | AfterTool\n")
	sb.WriteString("    After tool execution\n\n")

	sb.WriteString("MODEL EVENTS (Gemini CLI):\n")
	sb.WriteString("  BeforeModel\n")
	sb.WriteString("    Before LLM request (can modify request)\n")
	sb.WriteString("  AfterModel\n")
	sb.WriteString("    After LLM response (can modify response)\n")
	sb.WriteString("  BeforeToolSelection\n")
	sb.WriteString("    Before tool selection phase\n\n")

	sb.WriteString("OTHER EVENTS:\n")
	sb.WriteString("  compact.before | PreCompact | PreCompress\n")
	sb.WriteString("    Before context compaction\n")
	sb.WriteString("  notification | Notification\n")
	sb.WriteString("    For notifications/logging\n\n")

	sb.WriteString("EXIT CODE SEMANTICS:\n")
	sb.WriteString("  0 = Success (continue)\n")
	sb.WriteString("  1 = Non-blocking error (log but continue)\n")
	sb.WriteString("  2 = Blocking error (stop execution)\n")

	return tools.NewToolResult(sb.String()), nil
}

func (t *ListEventsTool) Validate(params map[string]any) error        { return nil }
func (t *ListEventsTool) IsIdempotent() bool                          { return true }
func (t *ListEventsTool) RequiresPermission() []tools.Permission      { return nil }
func (t *ListEventsTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ListEventsTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- Validate Hook Tool ----

type ValidateHookTool struct {
	ht *HookTools
}

func (t *ValidateHookTool) Name() string { return "validate_hook" }

func (t *ValidateHookTool) Description() string {
	return "Validate a hook configuration before creating or enabling it. Checks if tool_matcher patterns will actually match registered tools. Use this BEFORE create_hook or enable_hook to avoid creating hooks that never fire."
}

func (t *ValidateHookTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_matcher": map[string]any{
				"type":        "string",
				"description": "Tool matcher pattern to validate (e.g., 'file_write|apply_patch')",
			},
			"event_patterns": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Event patterns for context (e.g., ['tool.after_execute'])",
			},
		},
		"required": []string{"tool_matcher", "event_patterns"},
	}
}

func (t *ValidateHookTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	toolMatcher, _ := params["tool_matcher"].(string)

	var eventPatterns []string
	if patterns, ok := params["event_patterns"].([]any); ok {
		for _, p := range patterns {
			if s, ok := p.(string); ok {
				eventPatterns = append(eventPatterns, s)
			}
		}
	}

	if err := t.ht.validateToolMatcher(toolMatcher, eventPatterns); err != nil {
		return tools.NewToolResult(fmt.Sprintf("❌ VALIDATION FAILED:\n%v", err)), nil
	}

	// Get matches to show user
	re, _ := regexp.Compile("^(" + toolMatcher + ")$")
	var matches []string
	if t.ht.toolRegistry != nil {
		for _, toolName := range t.ht.toolRegistry.List() {
			if re.MatchString(toolName) {
				matches = append(matches, toolName)
			}
		}
	}

	return tools.NewToolResult(fmt.Sprintf(
		"✅ VALIDATION PASSED\n\nPattern '%s' will match %d tools:\n  - %s",
		toolMatcher,
		len(matches),
		strings.Join(matches, "\n  - "),
	)), nil
}

func (t *ValidateHookTool) Validate(params map[string]any) error        { return nil }
func (t *ValidateHookTool) IsIdempotent() bool                          { return true }
func (t *ValidateHookTool) RequiresPermission() []tools.Permission      { return nil }
func (t *ValidateHookTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ValidateHookTool) OptimizationHints() *tools.OptimizationHints { return nil }

// ---- List Available Tools Tool ----

type ListAvailableToolsTool struct {
	ht *HookTools
}

func (t *ListAvailableToolsTool) Name() string { return "list_available_tools" }

func (t *ListAvailableToolsTool) Description() string {
	return "List all available/registered tools in the system. Use this to find the correct tool names for tool_matcher patterns. Tools are listed with their actual names (e.g., 'file_write' not 'Write')."
}

func (t *ListAvailableToolsTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ListAvailableToolsTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if t.ht.toolRegistry == nil {
		return tools.NewToolResult("Tool registry not available"), nil
	}

	toolNames := t.ht.toolRegistry.List()
	if len(toolNames) == 0 {
		return tools.NewToolResult("No tools registered"), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 Available Tools (%d total):\n\n", len(toolNames)))

	// Group by category
	fileTools := []string{}
	mcpTools := []string{}
	agentTools := []string{}
	otherTools := []string{}

	for _, name := range toolNames {
		if strings.HasPrefix(name, "file_") || name == "apply_patch" || strings.HasPrefix(name, "Grep") || name == "list_dir" {
			fileTools = append(fileTools, name)
		} else if strings.HasPrefix(name, "mcp_") {
			mcpTools = append(mcpTools, name)
		} else if strings.Contains(name, "agent") || name == "delegate_task" {
			agentTools = append(agentTools, name)
		} else {
			otherTools = append(otherTools, name)
		}
	}

	if len(fileTools) > 0 {
		sb.WriteString("📁 File Operations:\n  - ")
		sb.WriteString(strings.Join(fileTools, "\n  - "))
		sb.WriteString("\n\n")
	}

	if len(mcpTools) > 0 {
		sb.WriteString("🔌 MCP Tools:\n  - ")
		sb.WriteString(strings.Join(mcpTools, "\n  - "))
		sb.WriteString("\n\n")
	}

	if len(agentTools) > 0 {
		sb.WriteString("🤖 Agent Tools:\n  - ")
		sb.WriteString(strings.Join(agentTools, "\n  - "))
		sb.WriteString("\n\n")
	}

	if len(otherTools) > 0 {
		sb.WriteString("🛠️  Other Tools:\n  - ")
		sb.WriteString(strings.Join(otherTools, "\n  - "))
		sb.WriteString("\n\n")
	}

	sb.WriteString("\n💡 Common Patterns:\n")
	sb.WriteString("  - file_write|apply_patch  (file modifications)\n")
	sb.WriteString("  - file_.*                  (all file tools)\n")
	sb.WriteString("  - Bash                     (shell commands)\n")
	sb.WriteString("  - mcp_.*                   (all MCP tools)\n")
	sb.WriteString("  - *                        (all tools)\n")

	return tools.NewToolResult(sb.String()), nil
}

func (t *ListAvailableToolsTool) Validate(params map[string]any) error        { return nil }
func (t *ListAvailableToolsTool) IsIdempotent() bool                          { return true }
func (t *ListAvailableToolsTool) RequiresPermission() []tools.Permission      { return nil }
func (t *ListAvailableToolsTool) SupportedContentTypes() []tools.ContentType  { return nil }
func (t *ListAvailableToolsTool) OptimizationHints() *tools.OptimizationHints { return nil }
