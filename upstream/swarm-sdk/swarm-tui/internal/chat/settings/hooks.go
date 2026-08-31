package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// hooksInitOnce ensures the "Initialized" debug log fires at most once per
// process regardless of how many HooksSettings instances are created (e.g.
// across repeated headless invocations loaded in the same binary run).
var hooksInitOnce sync.Once

// HookEntry represents a configured hook with Claude Code compatible fields
type HookEntry struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Events           []string `json:"event_patterns"` // e.g., ["tool.before_execute", "tool.after_execute"]
	Command          string   `json:"command"`
	ToolMatcher      string   `json:"tool_matcher,omitempty"` // Regex to filter tools, e.g., "Bash", "Edit|Write"
	Action           string   `json:"action"`                 // "continue", "block", "block_exit2", "block_on_output"
	Timeout          string   `json:"timeout"`                // e.g., "60s"
	Priority         int      `json:"priority"`               // 99=security, 90=logging, 50=general
	Enabled          bool     `json:"enabled"`
	PermissionPolicy string   `json:"permission_policy,omitempty"` // "allow" or "deny"
	PathAllowlist    []string `json:"path_allowlist,omitempty"`
	PathDenylist     []string `json:"path_denylist,omitempty"`
	PassEventAsJSON  bool     `json:"pass_event_json,omitempty"`
	WorkingDir       string   `json:"working_dir,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	Builtin          bool     `json:"builtin"` // Cannot be deleted
	Scope            string   `json:"-"`       // "global" or "project" - computed, not stored
	Source           string   `json:"-"`       // "system" (builtin), "user" (~/.swarmos), "project" (.swarmos) - computed
}

// HooksConfig stores all hooks configuration
type HooksConfig struct {
	Hooks []HookEntry `json:"hooks"`
}

// HookExecutionDisplay represents a hook execution for UI display
type HookExecutionDisplay struct {
	HookName string `json:"hook_name"`
	Phase    string `json:"phase"` // "pre" or "post"
	Success  bool   `json:"success"`
	Blocked  bool   `json:"blocked"`
	Output   string `json:"output"` // stdout/stderr from the hook
	Error    string `json:"error,omitempty"`
}

// ToolCallDisplay represents a tool call for UI display
type ToolCallDisplay struct {
	Name      string                 `json:"name"`
	InputJSON string                 `json:"input_json"` // Pretty-printed JSON
	Result    string                 `json:"result"`
	Success   bool                   `json:"success"`
	PreHooks  []HookExecutionDisplay `json:"pre_hooks,omitempty"`  // Hooks that ran before this tool
	PostHooks []HookExecutionDisplay `json:"post_hooks,omitempty"` // Hooks that ran after this tool
}

// ChatMessage represents a message in the hooks chat
type ChatMessage struct {
	Role      string            // "user", "assistant", "system", "tool"
	Content   string            // Main text content
	ToolCalls []ToolCallDisplay // Tool calls (for assistant messages)
	Turns     int               // Number of LLM turns (for assistant messages)
}

// AgentResponseCallback is the callback type for detailed agent responses
type AgentResponseCallback func(message string) (*AgentResponse, error)

// AgentResponse contains the full response from the hooks agent
type AgentResponse struct {
	Content    string
	ToolCalls  []ToolCallDisplay
	TotalTurns int
	Error      string
}

// HooksSettings manages hook configuration UI
type HooksSettings struct {
	globalHooks            []HookEntry
	projectHooks           []HookEntry
	allHooks               []HookEntry               // Combined list for unified view
	builtinStates          map[string]map[string]any // Maps hook name to {enabled, permission_policy}
	globalPath             string
	projectPath            string
	onHookChange           func(name string, enabled bool) error
	onHookPermissionChange func(name string, policy string) error
	onHookSave             func(hook HookEntry) error
	onChatSend             func(message string)  // Legacy callback (simple string response)
	onChatSendDetailed     AgentResponseCallback // Detailed callback with tool calls
	chatMessages           []ChatMessage         // Chat history
}

// Built-in hooks with Claude Code compatible format
var builtinHooks = []HookEntry{
	{
		Name:             "debug-logger",
		Description:      "Logs all tool executions for debugging",
		Events:           []string{"tool.before_execute", "tool.after_execute"},
		Command:          `echo "[$(date +%H:%M:%S)] $SWARMOS_HOOK_EVENT: $SWARMOS_TOOL_NAME" >> ~/.swarmos/hook_debug.log`,
		Action:           "continue",
		Timeout:          "60s",
		Priority:         90,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "bash-audit-logger",
		Description:      "Audit log for all bash command executions",
		Events:           []string{"tool.after_execute"},
		Command:          `echo "[$(date '+%Y-%m-%d %H:%M:%S')] Bash command executed" >> ~/.swarmos/bash_audit.log`,
		ToolMatcher:      "bash",
		Action:           "continue",
		Timeout:          "60s",
		Priority:         95,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "file-write-backup",
		Description:      "Creates backup before file writes",
		Events:           []string{"tool.before_execute"},
		Command:          `if [ -n "$SWARMOS_TOOL_FILE_PATH" ] && [ -f "$SWARMOS_TOOL_FILE_PATH" ]; then cp "$SWARMOS_TOOL_FILE_PATH" "$SWARMOS_TOOL_FILE_PATH.backup.$(date +%s)"; fi`,
		ToolMatcher:      "Write|Edit",
		Action:           "continue",
		Timeout:          "60s",
		Priority:         80,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "dangerous-command-blocker",
		Description:      "Blocks potentially dangerous rm commands",
		Events:           []string{"tool.before_execute"},
		Command:          `if echo "$SWARMOS_TOOL_PARAMS" | grep -qE 'rm.*-rf.*(/|~|\$HOME)'; then echo "BLOCKED: Dangerous rm command detected!" && exit 2; fi`,
		ToolMatcher:      "bash",
		Action:           "block_exit2",
		Timeout:          "60s",
		Priority:         99,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "git-auto-commit",
		Description:      "Auto-commits file changes after writes",
		Events:           []string{"tool.after_execute"},
		Command:          `if [ -n "$SWARMOS_TOOL_FILE_PATH" ]; then cd "$(dirname "$SWARMOS_TOOL_FILE_PATH")" && git add "$SWARMOS_TOOL_FILE_PATH" && git commit -m "Auto-commit: $(basename "$SWARMOS_TOOL_FILE_PATH")" 2>/dev/null; fi`,
		ToolMatcher:      "Write|Edit",
		Action:           "continue",
		Timeout:          "60s",
		Priority:         70,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "security-scan-trigger",
		Description:      "Triggers security scan after code changes",
		Events:           []string{"tool.after_execute"},
		Command:          `if echo "$SWARMOS_TOOL_FILE_PATH" | grep -qE '\.(py|js|ts|go)$'; then echo "🔒 Security scan recommended for: $SWARMOS_TOOL_FILE_PATH"; fi`,
		ToolMatcher:      "Write|Edit",
		Action:           "continue",
		Timeout:          "60s",
		Priority:         75,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "tool-timing-logger",
		Description:      "Logs tool execution duration for performance monitoring",
		Events:           []string{"tool.before_execute", "tool.after_execute"},
		Command:          `if [ "$SWARMOS_HOOK_EVENT" = "tool.before_execute" ]; then echo "$(date +%s%N)" > /tmp/swarmos_tool_start_$SWARMOS_TOOL_NAME; else START=$(cat /tmp/swarmos_tool_start_$SWARMOS_TOOL_NAME 2>/dev/null || echo 0); END=$(date +%s%N); DURATION=$((($END - $START) / 1000000)); echo "[$(date)] $SWARMOS_TOOL_NAME: ${DURATION}ms" >> ~/.swarmos/tool_timing.log; fi`,
		Action:           "continue",
		Timeout:          "60s",
		Priority:         85,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "notification-on-completion",
		Description:      "Sends desktop notification when long-running commands complete",
		Events:           []string{"tool.after_execute"},
		Command:          `if command -v notify-send > /dev/null; then notify-send "SwarmOS" "Tool $SWARMOS_TOOL_NAME completed"; fi`,
		ToolMatcher:      "bash",
		Action:           "continue",
		Timeout:          "60s",
		Priority:         60,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "prompt-context-injector",
		Description:      "Injects project context before each prompt (stdout becomes context)",
		Events:           []string{"user.prompt_submit"},
		Command:          `echo "Current directory: $(pwd)"`,
		Action:           "continue",
		Timeout:          "60s",
		Priority:         50,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "session-start-logger",
		Description:      "Logs session start with timestamp and working directory",
		Events:           []string{"session.start"},
		Command:          `echo "[$(date '+%Y-%m-%d %H:%M:%S')] Session started in $(pwd)" >> ~/.swarmos/sessions.log`,
		Action:           "continue",
		Timeout:          "60s",
		Priority:         90,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "agent-stop-summary",
		Description:      "Logs when agent completes work",
		Events:           []string{"agent.stop"},
		Command:          `echo "[$(date '+%H:%M:%S')] Agent stopped" >> ~/.swarmos/agent.log`,
		Action:           "continue",
		Timeout:          "60s",
		Priority:         85,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
	{
		Name:             "mcp-tool-auditor",
		Description:      "Audits all MCP tool executions",
		Events:           []string{"tool.before_execute", "tool.after_execute"},
		Command:          `echo "[$(date '+%H:%M:%S')] MCP: $SWARMOS_TOOL_NAME" >> ~/.swarmos/mcp_audit.log`,
		ToolMatcher:      "mcp__.*",
		Action:           "continue",
		Timeout:          "60s",
		Priority:         95,
		Enabled:          false,
		PermissionPolicy: "allow",
		PassEventAsJSON:  true,
		Builtin:          true,
		Scope:            "global",
		Source:           "system",
	},
}

// BuiltinHookEntries returns a copy of the built-in shell hook templates.
// These are code-defined and should not be mutated by callers.
func BuiltinHookEntries() []HookEntry {
	out := make([]HookEntry, len(builtinHooks))
	copy(out, builtinHooks)
	return out
}

// NewHooksSettings creates a new hooks settings component
func NewHooksSettings() *HooksSettings {
	home, _ := os.UserHomeDir()
	globalPath := filepath.Join(home, ".swarmos", "hooks.json")

	h := &HooksSettings{
		globalPath:   globalPath,
		chatMessages: []ChatMessage{},
	}

	h.load()

	hooksInitOnce.Do(func() {
		logHooksDebug("[HooksSettings] Initialized with %d global, %d project hooks",
			len(h.globalHooks), len(h.projectHooks))
	})

	return h
}

// SetProjectPath sets the project-specific hooks path
func (h *HooksSettings) SetProjectPath(projectDir string) {
	if projectDir != "" {
		h.projectPath = filepath.Join(projectDir, ".swarmos", "hooks.json")
		h.load() // Reload to include project hooks
	}
}

// Reload reloads hooks from disk
func (h *HooksSettings) Reload() error {
	logHooksDebug("[HooksSettings] Reloading hooks")
	h.load()
	logHooksDebug("[HooksSettings] Reloaded: %d global, %d project hooks",
		len(h.globalHooks), len(h.projectHooks))
	return nil
}

// logHooksDebug logs debug messages
func logHooksDebug(format string, args ...any) {
	home, _ := os.UserHomeDir()
	logDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	logPath := filepath.Join(logDir, "swarmos_debug.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	// Best-effort debug logging.
	_, _ = f.WriteString(msg + "\n")
}

// load loads hooks from both global and project paths
func (h *HooksSettings) load() {
	h.builtinStates = nil
	h.loadBuiltinStates()
	h.globalHooks = []HookEntry{}
	h.projectHooks = []HookEntry{}
	h.allHooks = []HookEntry{}

	// Add built-in hooks first (Source: "system")
	for _, hook := range builtinHooks {
		hook.Source = "system"
		h.globalHooks = append(h.globalHooks, hook)
		h.allHooks = append(h.allHooks, hook)
	}

	// Restore builtin hook states from persisted storage
	for i, hook := range h.globalHooks {
		if hook.Builtin {
			state := h.getBuiltinState(hook.Name)
			if enabled, ok := state["enabled"].(bool); ok {
				h.globalHooks[i].Enabled = enabled
			}
			if policy, ok := state["permission_policy"].(string); ok {
				h.globalHooks[i].PermissionPolicy = normalizeHookPermissionPolicy(policy)
			}
		}
	}
	// Also restore in allHooks
	for i, hook := range h.allHooks {
		if hook.Builtin {
			state := h.getBuiltinState(hook.Name)
			if enabled, ok := state["enabled"].(bool); ok {
				h.allHooks[i].Enabled = enabled
			}
			if policy, ok := state["permission_policy"].(string); ok {
				h.allHooks[i].PermissionPolicy = normalizeHookPermissionPolicy(policy)
			}
		}
	}

	// Load user hooks from ~/.swarmos/hooks.json (Source: "user")
	if data, err := os.ReadFile(h.globalPath); err == nil {
		hooks := h.parseHooksFile(data, "global", "user")
		h.globalHooks = append(h.globalHooks, hooks...)
		h.allHooks = append(h.allHooks, hooks...)
	}

	// Load project hooks if path is set (Source: "project")
	if h.projectPath != "" {
		if data, err := os.ReadFile(h.projectPath); err == nil {
			hooks := h.parseHooksFile(data, "project", "project")
			h.projectHooks = hooks
			h.allHooks = append(h.allHooks, hooks...)
		}
	}
}

// parseHooksFile parses hooks from JSON data
func (h *HooksSettings) parseHooksFile(data []byte, scope string, source string) []HookEntry {
	var hooks []HookEntry

	// Try new format first (custom_hooks)
	var newFormat struct {
		CustomHooks []struct {
			Name             string   `json:"name"`
			Description      string   `json:"description"`
			EventPatterns    []string `json:"event_patterns"`
			Command          string   `json:"command"`
			ToolMatcher      string   `json:"tool_matcher"`
			Action           string   `json:"action"`
			Timeout          string   `json:"timeout"`
			Priority         int      `json:"priority"`
			Enabled          bool     `json:"enabled"`
			PermissionPolicy string   `json:"permission_policy"`
			PathAllowlist    []string `json:"path_allowlist"`
			PathDenylist     []string `json:"path_denylist"`
			PassEventAsJSON  bool     `json:"pass_event_json"`
			WorkingDir       string   `json:"working_dir"`
			CreatedAt        string   `json:"created_at"`
		} `json:"custom_hooks"`
	}

	if err := json.Unmarshal(data, &newFormat); err == nil && len(newFormat.CustomHooks) > 0 {
		for _, ch := range newFormat.CustomHooks {
			passEventJSON := ch.PassEventAsJSON
			if !passEventJSON {
				passEventJSON = true
			}
			hooks = append(hooks, HookEntry{
				Name:             ch.Name,
				Description:      ch.Description,
				Events:           ch.EventPatterns,
				Command:          ch.Command,
				ToolMatcher:      ch.ToolMatcher,
				Action:           ch.Action,
				Timeout:          ch.Timeout,
				Priority:         ch.Priority,
				Enabled:          ch.Enabled,
				PermissionPolicy: normalizeHookPermissionPolicy(ch.PermissionPolicy),
				PathAllowlist:    normalizeHookPathList(ch.PathAllowlist),
				PathDenylist:     normalizeHookPathList(ch.PathDenylist),
				PassEventAsJSON:  passEventJSON,
				WorkingDir:       ch.WorkingDir,
				CreatedAt:        ch.CreatedAt,
				Builtin:          false,
				Scope:            scope,
				Source:           source,
			})
		}
	}

	// Try legacy format
	var legacyFormat struct {
		Hooks []HookEntry `json:"hooks"`
	}
	if err := json.Unmarshal(data, &legacyFormat); err == nil {
		for _, hook := range legacyFormat.Hooks {
			if !hook.Builtin { // Don't duplicate built-ins
				hook.Scope = scope
				hook.Source = source
				hook.PermissionPolicy = normalizeHookPermissionPolicy(hook.PermissionPolicy)
				hook.PathAllowlist = normalizeHookPathList(hook.PathAllowlist)
				hook.PathDenylist = normalizeHookPathList(hook.PathDenylist)
				if hook.PassEventAsJSON == false {
					// Default to true if unset in legacy entries.
					hook.PassEventAsJSON = true
				}
				hooks = append(hooks, hook)
			}
		}
	}

	return hooks
}

func normalizeHookPermissionPolicy(policy string) string {
	if strings.EqualFold(policy, "deny") {
		return "deny"
	}
	return "allow"
}

func normalizeHookPathList(values []string) []string {
	if len(values) == 0 {
		return nil
	}

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

// save saves hooks to the appropriate file
func (h *HooksSettings) save(scope string) error {
	var hooks []HookEntry
	var path string

	if scope == "global" {
		hooks = h.globalHooks
		path = h.globalPath
	} else {
		hooks = h.projectHooks
		path = h.projectPath
	}

	// Filter out built-ins for saving
	var toSave []map[string]any
	for _, hook := range hooks {
		if !hook.Builtin {
			entry := map[string]any{
				"name":           hook.Name,
				"description":    hook.Description,
				"event_patterns": hook.Events,
				"command":        hook.Command,
				"tool_matcher":   hook.ToolMatcher,
				"action":         hook.Action,
				"timeout":        hook.Timeout,
				"priority":       hook.Priority,
				"enabled":        hook.Enabled,
			}
			if normalizeHookPermissionPolicy(hook.PermissionPolicy) != "allow" {
				entry["permission_policy"] = normalizeHookPermissionPolicy(hook.PermissionPolicy)
			}
			if len(hook.PathAllowlist) > 0 {
				entry["path_allowlist"] = hook.PathAllowlist
			}
			if len(hook.PathDenylist) > 0 {
				entry["path_denylist"] = hook.PathDenylist
			}
			if hook.PassEventAsJSON {
				entry["pass_event_json"] = true
			}
			if hook.WorkingDir != "" {
				entry["working_dir"] = hook.WorkingDir
			}
			if hook.CreatedAt != "" {
				entry["created_at"] = hook.CreatedAt
			}
			toSave = append(toSave, entry)
		}
	}

	config := map[string]any{
		"custom_hooks": toSave,
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// SetEnabled sets the enabled state for a hook
func (h *HooksSettings) SetEnabled(name string, enabled bool) {
	// Check global hooks
	for i := range h.globalHooks {
		if h.globalHooks[i].Name == name {
			h.globalHooks[i].Enabled = enabled

			// If it's a builtin hook, save state separately
			if h.globalHooks[i].Builtin {
				state := h.getBuiltinState(name)
				state["enabled"] = enabled
				h.setBuiltinState(name, state)
				if err := h.saveBuiltinStates(); err != nil {
					logHooksDebug("[HooksSettings] Failed to save builtin hook state: %v", err)
				}
			} else {
				// Regular hooks are saved to the hooks.json file
				if err := h.save("global"); err != nil {
					logHooksDebug("[HooksSettings] Failed to save global hooks: %v", err)
				}
			}

			if h.onHookChange != nil {
				if err := h.onHookChange(name, enabled); err != nil {
					logHooksDebug("[HooksSettings] Hook change callback failed: %v", err)
				}
			}
			return
		}
	}

	// Check project hooks
	for i := range h.projectHooks {
		if h.projectHooks[i].Name == name {
			h.projectHooks[i].Enabled = enabled
			if err := h.save("project"); err != nil {
				logHooksDebug("[HooksSettings] Failed to save project hooks: %v", err)
			}
			if h.onHookChange != nil {
				if err := h.onHookChange(name, enabled); err != nil {
					logHooksDebug("[HooksSettings] Hook change callback failed: %v", err)
				}
			}
			return
		}
	}
}

// SetPermissionPolicy updates the permission policy for a hook.
func (h *HooksSettings) SetPermissionPolicy(name string, policy string) {
	normalized := normalizeHookPermissionPolicy(policy)

	// Check global hooks
	for i := range h.globalHooks {
		if h.globalHooks[i].Name == name {
			h.globalHooks[i].PermissionPolicy = normalized

			// If it's a builtin hook, save state separately
			if h.globalHooks[i].Builtin {
				state := h.getBuiltinState(name)
				state["permission_policy"] = normalized
				h.setBuiltinState(name, state)
				if err := h.saveBuiltinStates(); err != nil {
					logHooksDebug("[HooksSettings] Failed to save builtin hook state: %v", err)
				}
			} else {
				// Regular hooks are saved to the hooks.json file
				if err := h.save("global"); err != nil {
					logHooksDebug("[HooksSettings] Failed to save global hooks: %v", err)
				}
			}

			if h.onHookPermissionChange != nil {
				if err := h.onHookPermissionChange(name, normalized); err != nil {
					logHooksDebug("[HooksSettings] Hook permission callback failed: %v", err)
				}
			}
			return
		}
	}

	// Check project hooks
	for i := range h.projectHooks {
		if h.projectHooks[i].Name == name {
			h.projectHooks[i].PermissionPolicy = normalized
			if err := h.save("project"); err != nil {
				logHooksDebug("[HooksSettings] Failed to save project hooks: %v", err)
			}
			if h.onHookPermissionChange != nil {
				if err := h.onHookPermissionChange(name, normalized); err != nil {
					logHooksDebug("[HooksSettings] Hook permission callback failed: %v", err)
				}
			}
			return
		}
	}
}

// GetGlobalHooks returns global hooks
func (h *HooksSettings) GetGlobalHooks() []HookEntry {
	return h.globalHooks
}

// GetProjectHooks returns project hooks
func (h *HooksSettings) GetProjectHooks() []HookEntry {
	return h.projectHooks
}

// GetAllHooks returns all hooks (for compatibility)
func (h *HooksSettings) GetHooks() []HookEntry {
	all := make([]HookEntry, 0, len(h.globalHooks)+len(h.projectHooks))
	all = append(all, h.globalHooks...)
	all = append(all, h.projectHooks...)
	return all
}

// SetOnHookChange sets the callback for hook changes
func (h *HooksSettings) SetOnHookChange(callback func(string, bool) error) {
	h.onHookChange = callback
}

// SetOnHookPermissionChange sets the callback for hook permission changes.
func (h *HooksSettings) SetOnHookPermissionChange(callback func(string, string) error) {
	h.onHookPermissionChange = callback
}

// SetOnHookSave sets the callback for hook create/edit saves.
func (h *HooksSettings) SetOnHookSave(callback func(HookEntry) error) {
	h.onHookSave = callback
}

// SetOnChatSend sets the callback for sending chat messages
func (h *HooksSettings) SetOnChatSend(callback func(string)) {
	h.onChatSend = callback
}

// AddChatMessage adds a message to the chat history
func (h *HooksSettings) AddChatMessage(role, content string) {
	h.chatMessages = append(h.chatMessages, ChatMessage{
		Role:    role,
		Content: content,
	})
}

// AddAgentResponse adds an agent response with tool calls to the chat history
func (h *HooksSettings) AddAgentResponse(resp *AgentResponse) {
	if resp == nil {
		return
	}
	h.chatMessages = append(h.chatMessages, ChatMessage{
		Role:      "assistant",
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
		Turns:     resp.TotalTurns,
	})
}

// GetChatMessages returns the chat history
func (h *HooksSettings) GetChatMessages() []ChatMessage {
	return h.chatMessages
}

// SetChatSendDetailed sets the detailed callback for sending chat messages
func (h *HooksSettings) SetChatSendDetailed(callback AgentResponseCallback) {
	h.onChatSendDetailed = callback
}

// ClearChatHistory clears the chat history
func (h *HooksSettings) ClearChatHistory() {
	h.chatMessages = []ChatMessage{}
}

// Render renders the hooks settings UI
func (h *HooksSettings) HandleKey(key string, state *State) bool {
	switch state.HooksState {
	case "main":
		return h.handleMainKey(key, state)
	case "templates":
		return h.handleTemplatesKey(key, state)
	case "action_menu":
		return h.handleActionMenuKey(key, state)
	case "chat":
		return h.handleChatKey(key, state)
	case "edit":
		return h.handleEditKey(key, state)
	}
	return false
}

// handleMainKey handles keyboard input in main view (unified list)
func (h *HooksSettings) handleMainKey(key string, state *State) bool {
	switch key {
	case "up", "k":
		if state.HooksSelected > 0 {
			state.HooksSelected--
		}
		return true

	case "down", "j":
		if state.HooksSelected < len(h.allHooks)-1 {
			state.HooksSelected++
		}
		return true

	case "home", "g":
		state.HooksSelected = 0
		state.HooksScrollOffsetGlobal = 0
		return true

	case "end", "G":
		if len(h.allHooks) > 0 {
			state.HooksSelected = len(h.allHooks) - 1
		}
		return true

	case "pageup", "ctrl+u":
		state.HooksSelected -= 10
		if state.HooksSelected < 0 {
			state.HooksSelected = 0
		}
		return true

	case "pagedown", "ctrl+d":
		state.HooksSelected += 10
		if state.HooksSelected >= len(h.allHooks) {
			state.HooksSelected = len(h.allHooks) - 1
		}
		if state.HooksSelected < 0 {
			state.HooksSelected = 0
		}
		return true

	case "r", "R":
		// Reload hooks
		if err := h.Reload(); err != nil {
			logHooksDebug("[HooksSettings] Failed to reload hooks: %v", err)
		}
		return true

	case "n", "N":
		// New hook
		h.startCreateHook(state, "user")
		return true

	case "t", "T":
		// Templates
		state.HooksState = "templates"
		state.HooksTemplateIdx = 0
		return true

	case "a", "A":
		// AI Assistant
		state.HooksState = "chat"
		state.HooksCursorPos = 0
		return true

	case "e", "E":
		// Edit selected hook
		if len(h.allHooks) > 0 && state.HooksSelected < len(h.allHooks) {
			hook := h.allHooks[state.HooksSelected]
			if !hook.Builtin {
				h.loadHookToForm(hook, state)
				state.HooksState = "edit"
			}
		}
		return true

	case "d", "D":
		// Delete selected hook
		if len(h.allHooks) > 0 && state.HooksSelected < len(h.allHooks) {
			hook := h.allHooks[state.HooksSelected]
			if !hook.Builtin {
				h.deleteHookFromAll(state.HooksSelected)
				if state.HooksSelected >= len(h.allHooks) && len(h.allHooks) > 0 {
					state.HooksSelected = len(h.allHooks) - 1
				}
			}
		}
		return true

	case "enter":
		// Action menu for selected hook
		if len(h.allHooks) > 0 && state.HooksSelected < len(h.allHooks) {
			state.HooksState = "action_menu"
			state.HooksActionChoice = 0
		}
		return true

	case " ", "space":
		// Toggle enabled/disabled
		if len(h.allHooks) > 0 && state.HooksSelected < len(h.allHooks) {
			hook := h.allHooks[state.HooksSelected]
			h.SetEnabled(hook.Name, !hook.Enabled)
			// Reload to refresh allHooks
			h.load()
		}
		return true
	}

	return false
}

// loadHookToForm loads hook data into form fields for editing
func (h *HooksSettings) loadHookToForm(hook HookEntry, state *State) {
	state.HooksFormName = hook.Name
	state.HooksFormEvent = strings.Join(hook.Events, ", ")
	state.HooksFormCommand = hook.Command
	state.HooksFormToolMatcher = hook.ToolMatcher
	state.HooksFormPathAllowlist = strings.Join(hook.PathAllowlist, ", ")
	state.HooksFormPathDenylist = strings.Join(hook.PathDenylist, ", ")
	state.HooksFormAction = hook.Action
	state.HooksFormTimeout = hook.Timeout
	state.HooksFormDescription = hook.Description
	state.HooksFormEnabled = hook.Enabled
	state.HooksEditingMode = "edit"
	state.HooksEditingScope = hook.Scope
	state.HooksEditingField = 0
	state.HooksCursorPos = len(state.HooksFormName)
}

// deleteHookFromAll deletes a hook from the unified list
func (h *HooksSettings) deleteHookFromAll(idx int) {
	if idx < 0 || idx >= len(h.allHooks) {
		return
	}
	hook := h.allHooks[idx]
	if hook.Builtin {
		return
	}

	// Determine which list to delete from
	if hook.Scope == "project" {
		for i := range h.projectHooks {
			if h.projectHooks[i].Name == hook.Name {
				h.projectHooks = append(h.projectHooks[:i], h.projectHooks[i+1:]...)
				break
			}
		}
		_ = h.save("project")
	} else {
		for i := range h.globalHooks {
			if h.globalHooks[i].Name == hook.Name && !h.globalHooks[i].Builtin {
				h.globalHooks = append(h.globalHooks[:i], h.globalHooks[i+1:]...)
				break
			}
		}
		_ = h.save("global")
	}

	// Reload to refresh allHooks
	h.load()
}

func (h *HooksSettings) startCreateHook(state *State, scope string) {
	state.HooksEditingMode = "create"
	// Default to user scope (global) unless project is specified
	if scope == "project" && h.projectPath != "" {
		state.HooksEditingScope = "project"
	} else {
		state.HooksEditingScope = "global"
	}
	state.HooksFormName = ""
	state.HooksFormEvent = "tool.before_execute"
	state.HooksFormCommand = ""
	state.HooksFormToolMatcher = ""
	state.HooksFormPathAllowlist = ""
	state.HooksFormPathDenylist = ""
	state.HooksFormAction = "block_exit2"
	state.HooksFormTimeout = "60s"
	state.HooksFormDescription = ""
	state.HooksFormEnabled = true // Enable by default for new hooks
	state.HooksEditingField = 0
	state.HooksCursorPos = 0
	state.HooksState = "edit"
}

// handleActionMenuKey handles keyboard input in action menu
// handleTemplatesKey handles keyboard input in templates picker
func (h *HooksSettings) handleTemplatesKey(key string, state *State) bool {
	templates := builtinHooks

	switch key {
	case "up", "k":
		if state.HooksTemplateIdx > 0 {
			state.HooksTemplateIdx--
		}
		return true
	case "down", "j":
		if state.HooksTemplateIdx < len(templates)-1 {
			state.HooksTemplateIdx++
		}
		return true
	case "enter", " ", "space":
		// Create hook from selected template
		if state.HooksTemplateIdx < len(templates) {
			tmpl := templates[state.HooksTemplateIdx]

			// Initialize form with template values
			state.HooksFormName = tmpl.Name + "-copy"
			state.HooksFormEvent = strings.Join(tmpl.Events, ", ")
			state.HooksFormCommand = tmpl.Command
			state.HooksFormToolMatcher = tmpl.ToolMatcher
			state.HooksFormPathAllowlist = strings.Join(tmpl.PathAllowlist, ", ")
			state.HooksFormPathDenylist = strings.Join(tmpl.PathDenylist, ", ")
			state.HooksFormAction = tmpl.Action
			state.HooksFormTimeout = tmpl.Timeout
			state.HooksFormDescription = tmpl.Description
			state.HooksFormEnabled = true // Enable by default

			state.HooksEditingMode = "create"
			state.HooksEditingScope = "global"
			state.HooksEditingField = 0
			state.HooksState = "edit"
		}
		return true
	case "esc", "backspace", "q":
		// Return to main view
		state.HooksState = "main"
		state.HooksTemplateIdx = 0
		return true
	}
	return false
}

// handleActionMenuKey handles keyboard input in action menu
func (h *HooksSettings) handleActionMenuKey(key string, state *State) bool {
	if state.HooksSelected >= len(h.allHooks) {
		state.HooksState = "main"
		return true
	}

	hook := h.allHooks[state.HooksSelected]

	maxActions := 2 // Built-in: Toggle + permission
	if !hook.Builtin {
		maxActions = 5 // Toggle, Permission, Edit, Edit with AI, Delete
	}

	switch key {
	case "up", "k":
		if state.HooksActionChoice > 0 {
			state.HooksActionChoice--
		}
		return true

	case "down", "j":
		if state.HooksActionChoice < maxActions-1 {
			state.HooksActionChoice++
		}
		return true

	case "enter", " ", "space":
		if hook.Builtin {
			switch state.HooksActionChoice {
			case 0: // Toggle
				h.SetEnabled(hook.Name, !hook.Enabled)
				h.load() // Reload to refresh allHooks
				state.HooksState = "main"
			case 1: // Toggle Permission
				nextPolicy := "allow"
				if normalizeHookPermissionPolicy(hook.PermissionPolicy) == "allow" {
					nextPolicy = "deny"
				}
				h.SetPermissionPolicy(hook.Name, nextPolicy)
				h.load() // Reload to refresh allHooks
				state.HooksState = "main"
			}
		} else {
			switch state.HooksActionChoice {
			case 0: // Toggle
				h.SetEnabled(hook.Name, !hook.Enabled)
				h.load() // Reload to refresh allHooks
				state.HooksState = "main"
			case 1: // Toggle Permission
				nextPolicy := "allow"
				if normalizeHookPermissionPolicy(hook.PermissionPolicy) == "allow" {
					nextPolicy = "deny"
				}
				h.SetPermissionPolicy(hook.Name, nextPolicy)
				h.load() // Reload to refresh allHooks
				state.HooksState = "main"
			case 2: // Edit Hook
				h.loadHookToForm(hook, state)
				state.HooksState = "edit"
			case 3: // Edit with AI
				state.HooksState = "chat"
				state.HooksChatInput = fmt.Sprintf("I want to modify the hook named '%s'. ", hook.Name)
				state.HooksCursorPos = len(state.HooksChatInput)
			case 4: // Delete
				h.deleteHookFromAll(state.HooksSelected)
				state.HooksState = "main"
			}
		}
		return true

	case "esc", "backspace", "q":
		state.HooksState = "main"
		return true
	}

	return false
}

// handleEditKey handles keyboard input in edit form
func (h *HooksSettings) handleEditKey(key string, state *State) bool {
	switch key {
	case "esc":
		state.HooksState = "main"
		state.HooksEditingMode = "edit"
		state.HooksEditingScope = ""
		return true

	case "tab", "down":
		if state.HooksEditingField < 7 {
			state.HooksEditingField++
		}
		state.HooksCursorPos = len(hooksFieldValue(state))
		return true

	case "shift+tab", "up":
		if state.HooksEditingField > 0 {
			state.HooksEditingField--
		}
		state.HooksCursorPos = len(hooksFieldValue(state))
		return true

	case "ctrl+s":
		// Save the hook
		h.saveEditedHook(state)
		state.HooksState = "main"
		state.HooksEditingMode = "edit"
		state.HooksEditingScope = ""
		return true

	default:
		switch state.HooksEditingField {
		case 0:
			state.HooksFormName = handleTextInput(state.HooksFormName, &state.HooksCursorPos, key)
		case 1:
			state.HooksFormEvent = handleTextInput(state.HooksFormEvent, &state.HooksCursorPos, key)
		case 2:
			state.HooksFormCommand = handleTextInput(state.HooksFormCommand, &state.HooksCursorPos, key)
		case 3:
			state.HooksFormToolMatcher = handleTextInput(state.HooksFormToolMatcher, &state.HooksCursorPos, key)
		case 4:
			state.HooksFormPathAllowlist = handleTextInput(state.HooksFormPathAllowlist, &state.HooksCursorPos, key)
		case 5:
			state.HooksFormPathDenylist = handleTextInput(state.HooksFormPathDenylist, &state.HooksCursorPos, key)
		case 6:
			state.HooksFormAction = handleTextInput(state.HooksFormAction, &state.HooksCursorPos, key)
		case 7:
			state.HooksFormTimeout = handleTextInput(state.HooksFormTimeout, &state.HooksCursorPos, key)
		}
		return true
	}
}

// handleChatKey handles keyboard input in chat view
func (h *HooksSettings) handleChatKey(key string, state *State) bool {
	if state.HooksChatWaiting {
		if key == "esc" {
			state.HooksState = "main"
			return true
		}
		return false
	}

	switch key {
	case "esc":
		state.HooksState = "main"
		return true

	case "enter":
		if state.HooksChatInput != "" && h.onChatSend != nil {
			h.AddChatMessage("user", state.HooksChatInput)
			h.onChatSend(state.HooksChatInput)
			state.HooksChatInput = ""
			state.HooksCursorPos = 0
			state.HooksChatWaiting = true
		}
		return true

	case "backspace":
		if state.HooksCursorPos > 0 {
			state.HooksChatInput = state.HooksChatInput[:state.HooksCursorPos-1] + state.HooksChatInput[state.HooksCursorPos:]
			state.HooksCursorPos--
		}
		return true

	case "delete":
		if state.HooksCursorPos < len(state.HooksChatInput) {
			state.HooksChatInput = state.HooksChatInput[:state.HooksCursorPos] + state.HooksChatInput[state.HooksCursorPos+1:]
		}
		return true

	case "left":
		if state.HooksCursorPos > 0 {
			state.HooksCursorPos--
		}
		return true

	case "right":
		if state.HooksCursorPos < len(state.HooksChatInput) {
			state.HooksCursorPos++
		}
		return true

	case "home", "ctrl+a":
		state.HooksCursorPos = 0
		return true

	case "end", "ctrl+e":
		state.HooksCursorPos = len(state.HooksChatInput)
		return true

	case "pageup", "ctrl+u":
		if state.HooksChatScrollOffset > 0 {
			state.HooksChatScrollOffset -= 5
			if state.HooksChatScrollOffset < 0 {
				state.HooksChatScrollOffset = 0
			}
		}
		return true

	case "pagedown", "ctrl+d":
		state.HooksChatScrollOffset += 5
		return true

	case "space":
		state.HooksChatInput = state.HooksChatInput[:state.HooksCursorPos] + " " + state.HooksChatInput[state.HooksCursorPos:]
		state.HooksCursorPos++
		return true

	default:
		if len(key) == 1 && key[0] >= 32 && key[0] <= 126 {
			state.HooksChatInput = state.HooksChatInput[:state.HooksCursorPos] + key + state.HooksChatInput[state.HooksCursorPos:]
			state.HooksCursorPos++
			return true
		} else if len(key) > 1 && !strings.HasPrefix(key, "ctrl+") && !strings.HasPrefix(key, "alt+") {
			state.HooksChatInput = state.HooksChatInput[:state.HooksCursorPos] + key + state.HooksChatInput[state.HooksCursorPos:]
			state.HooksCursorPos += len(key)
			return true
		}
	}

	return false
}

// saveEditedHook saves the edited hook
func (h *HooksSettings) saveEditedHook(state *State) {
	var hooks *[]HookEntry
	var idx int

	scope := state.HooksEditingScope
	if scope == "" {
		scope = state.HooksFocusedPanel
	}
	if scope == "global" {
		hooks = &h.globalHooks
		idx = state.HooksSelectedGlobal
	} else {
		hooks = &h.projectHooks
		idx = state.HooksSelectedProject
	}

	// Parse events
	events := strings.Split(state.HooksFormEvent, ",")
	for i := range events {
		events[i] = strings.TrimSpace(events[i])
	}

	if state.HooksEditingMode == "create" {
		if strings.TrimSpace(state.HooksFormName) == "" {
			logHooksDebug("[HooksSettings] Cannot create hook without name")
			return
		}
		newHook := HookEntry{
			Name:             state.HooksFormName,
			Description:      state.HooksFormDescription,
			Events:           events,
			Command:          state.HooksFormCommand,
			ToolMatcher:      state.HooksFormToolMatcher,
			Action:           state.HooksFormAction,
			Timeout:          state.HooksFormTimeout,
			Priority:         50,
			Enabled:          false,
			PermissionPolicy: "allow",
			PathAllowlist:    parseHookPathList(state.HooksFormPathAllowlist),
			PathDenylist:     parseHookPathList(state.HooksFormPathDenylist),
			PassEventAsJSON:  true,
			CreatedAt:        time.Now().Format(time.RFC3339),
			Builtin:          false,
			Scope:            scope,
		}
		*hooks = append(*hooks, newHook)
		if scope == "global" {
			state.HooksSelectedGlobal = len(*hooks) - 1
		} else {
			state.HooksSelectedProject = len(*hooks) - 1
		}
		idx = len(*hooks) - 1
		state.HooksEditingMode = "edit"
		state.HooksEditingScope = scope
	} else if idx >= len(*hooks) {
		return
	}

	(*hooks)[idx].Name = state.HooksFormName
	(*hooks)[idx].Events = events
	(*hooks)[idx].Command = state.HooksFormCommand
	(*hooks)[idx].ToolMatcher = state.HooksFormToolMatcher
	(*hooks)[idx].PathAllowlist = parseHookPathList(state.HooksFormPathAllowlist)
	(*hooks)[idx].PathDenylist = parseHookPathList(state.HooksFormPathDenylist)
	(*hooks)[idx].Action = state.HooksFormAction
	(*hooks)[idx].Timeout = state.HooksFormTimeout
	(*hooks)[idx].Description = state.HooksFormDescription
	(*hooks)[idx].Scope = scope

	// Save to disk
	if scope == "global" {
		if err := h.save("global"); err != nil {
			logHooksDebug("[HooksSettings] Failed to save global hooks: %v", err)
		}
	} else {
		if err := h.save("project"); err != nil {
			logHooksDebug("[HooksSettings] Failed to save project hooks: %v", err)
		}
	}

	if h.onHookSave != nil && idx >= 0 && idx < len(*hooks) {
		if err := h.onHookSave((*hooks)[idx]); err != nil {
			logHooksDebug("[HooksSettings] Hook save callback failed: %v", err)
		}
	}
}

func hooksFieldValue(state *State) string {
	switch state.HooksEditingField {
	case 0:
		return state.HooksFormName
	case 1:
		return state.HooksFormEvent
	case 2:
		return state.HooksFormCommand
	case 3:
		return state.HooksFormToolMatcher
	case 4:
		return state.HooksFormPathAllowlist
	case 5:
		return state.HooksFormPathDenylist
	case 6:
		return state.HooksFormAction
	case 7:
		return state.HooksFormTimeout
	default:
		return ""
	}
}

func parseHookPathList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n'
	})

	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// wordWrapText wraps text to specified width
func wordWrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}

	var lines []string
	var currentLine string

	for _, word := range words {
		if len(currentLine) == 0 {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= width {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}

	if len(currentLine) > 0 {
		lines = append(lines, currentLine)
	}

	return strings.Join(lines, "\n")
}

// truncateString truncates a string to specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}

// Helper functions
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// getBuiltinStatePath returns the path to the builtin hooks state file
func (h *HooksSettings) getBuiltinStatePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".swarmos", "hooks_builtin_state.json")
}

// loadBuiltinStates loads the enabled/permission state of builtin hooks from disk
func (h *HooksSettings) loadBuiltinStates() {
	if h.builtinStates == nil {
		h.builtinStates = make(map[string]map[string]any)
	}

	data, err := os.ReadFile(h.getBuiltinStatePath())
	if err != nil {
		// File doesn't exist yet, that's fine
		return
	}

	var states map[string]map[string]any
	if err := json.Unmarshal(data, &states); err != nil {
		logHooksDebug("[HooksSettings] Failed to parse builtin states: %v", err)
		return
	}

	h.builtinStates = states
}

// saveBuiltinStates saves the enabled/permission state of builtin hooks to disk
func (h *HooksSettings) saveBuiltinStates() error {
	if h.builtinStates == nil || len(h.builtinStates) == 0 {
		return nil
	}

	path := h.getBuiltinStatePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(h.builtinStates, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

// getBuiltinState retrieves the saved state for a builtin hook
func (h *HooksSettings) getBuiltinState(name string) map[string]any {
	if h.builtinStates == nil {
		h.builtinStates = make(map[string]map[string]any)
	}
	if _, exists := h.builtinStates[name]; !exists {
		h.builtinStates[name] = make(map[string]any)
	}
	return h.builtinStates[name]
}

// setBuiltinState saves the state for a builtin hook
func (h *HooksSettings) setBuiltinState(name string, state map[string]any) {
	if h.builtinStates == nil {
		h.builtinStates = make(map[string]map[string]any)
	}
	h.builtinStates[name] = state
}
