package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ShellHook executes shell commands in response to events.
// This allows users to create custom hooks without writing Go code.
type ShellHook struct {
	// HookName is the unique identifier for this hook.
	HookName string

	// Description explains what this hook does.
	Description string

	// EventPatterns are the event types this hook responds to.
	// Supports wildcards: "tool.*", "*.before_*", etc.
	EventPatterns []string

	// ToolMatcher is a pattern to match tool names (for PreToolUse/PostToolUse hooks).
	// Supports regex patterns like "Bash", "Edit|Write", "mcp__.*".
	// Empty string or "*" matches all tools.
	ToolMatcher string

	// ToolMatcherRegex is the compiled regex for tool matching.
	ToolMatcherRegex *regexp.Regexp

	// Command is the shell command to execute.
	// Event data is passed via environment variables.
	Command string

	// HookPriority determines execution order (0-100, higher first).
	HookPriority int

	// Timeout is the maximum time the command can run.
	Timeout time.Duration

	// HookAction determines what happens based on exit code:
	// - "continue": Always continue (exit code ignored)
	// - "block": Block if exit code != 0 (any non-zero)
	// - "block_exit2": Block only if exit code == 2 (Claude Code semantics)
	// - "block_on_output": Block if command outputs anything to stdout
	HookAction string

	// PermissionPolicy controls whether the hook is allowed to execute.
	PermissionPolicy HookPermissionPolicy

	// PathAllowlist restricts execution to matching paths (tool events only).
	PathAllowlist []string

	// PathDenylist blocks execution when matching paths (tool events only).
	PathDenylist []string

	// PassEventAsJSON passes the full event as JSON to stdin.
	PassEventAsJSON bool

	// WorkingDir sets the working directory for the command.
	WorkingDir string

	// Stdout contains the last stdout output (separated from stderr)
	Stdout string

	// Stderr contains the last stderr output
	Stderr string
}

// NewShellHook creates a new shell hook with defaults.
func NewShellHook(name, command string, patterns []string) *ShellHook {
	return &ShellHook{
		HookName:         name,
		Command:          command,
		EventPatterns:    patterns,
		HookPriority:     50,
		Timeout:          60 * time.Second, // Claude Code default is 60 seconds
		HookAction:       "block_exit2",    // Claude Code semantics: exit 2 = block
		PermissionPolicy: HookPermissionAllow,
		PathAllowlist:    nil,
		PathDenylist:     nil,
		PassEventAsJSON:  true,
	}
}

// NewShellHookWithMatcher creates a new shell hook with tool matcher.
func NewShellHookWithMatcher(name, command string, patterns []string, toolMatcher string) *ShellHook {
	h := NewShellHook(name, command, patterns)
	h.SetToolMatcher(toolMatcher)
	return h
}

// SetToolMatcher sets the tool matcher pattern and compiles it.
func (h *ShellHook) SetToolMatcher(pattern string) {
	h.ToolMatcher = pattern
	if pattern != "" && pattern != "*" {
		// Compile as regex, anchored to match full string
		if re, err := regexp.Compile("^(" + pattern + ")$"); err == nil {
			h.ToolMatcherRegex = re
		}
	}
}

// Name returns the hook's unique identifier.
func (h *ShellHook) Name() string {
	return h.HookName
}

// PermissionPolicy returns the hook's permission policy.
func (h *ShellHook) HookPermissionPolicy() HookPermissionPolicy {
	return NormalizeHookPermissionPolicy(h.PermissionPolicy)
}

// Priority returns the execution priority.
func (h *ShellHook) Priority() int {
	return h.HookPriority
}

// Filter returns true if this hook should process the event.
func (h *ShellHook) Filter(event Event) bool {
	// First check event type pattern
	eventMatches := false
	for _, pattern := range h.EventPatterns {
		if matchEventPattern(pattern, event.Type) {
			eventMatches = true
			break
		}
	}
	if !eventMatches {
		return false
	}

	// For tool-related events, check tool matcher
	if h.ToolMatcher != "" && h.ToolMatcher != "*" {
		toolName, ok := event.Data["tool_name"].(string)
		if !ok {
			// No tool name in event, can't match
			return true // Allow if no tool context
		}

		// Use compiled regex if available
		if h.ToolMatcherRegex != nil {
			if !h.ToolMatcherRegex.MatchString(toolName) {
				return false
			}
			return h.matchesPathConstraints(event)
		}

		// Fallback to exact match
		if h.ToolMatcher != toolName {
			return false
		}
	}

	return h.matchesPathConstraints(event)
}

// OnEvent executes the shell command when an event occurs.
// Output is written to temp files to avoid buffer issues and enable recovery.
func (h *ShellHook) OnEvent(ctx context.Context, event Event) (HookResult, error) {
	// Create context with timeout
	execCtx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()

	// Create temp files for stdout and stderr
	stdoutFile, err := os.CreateTemp("/tmp", "hook-stdout-*.txt")
	if err != nil {
		return Continue(), fmt.Errorf("failed to create stdout temp file: %w", err)
	}
	stdoutPath := stdoutFile.Name()
	defer os.Remove(stdoutPath)

	stderrFile, err := os.CreateTemp("/tmp", "hook-stderr-*.txt")
	if err != nil {
		stdoutFile.Close()
		os.Remove(stdoutPath)
		return Continue(), fmt.Errorf("failed to create stderr temp file: %w", err)
	}
	stderrPath := stderrFile.Name()
	defer os.Remove(stderrPath)

	// Create command
	cmd := exec.CommandContext(execCtx, "sh", "-c", h.Command)
	cmd.WaitDelay = 2 * time.Second

	// Set working directory if specified
	if h.WorkingDir != "" {
		cmd.Dir = h.WorkingDir
	}

	// Set environment variables with event data (includes CLAUDE_PROJECT_DIR)
	cmd.Env = h.buildEnvironment(event)

	// Pass event as JSON to stdin if enabled (Claude Code format)
	if h.PassEventAsJSON {
		eventJSON := h.buildClaudeCodeInput(event)
		cmd.Stdin = strings.NewReader(string(eventJSON))
	}

	// Redirect output to temp files
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	// Execute command
	cmdErr := cmd.Run()

	// Close files before reading
	stdoutFile.Close()
	stderrFile.Close()

	// Read output from temp files
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)

	// Store outputs for later access
	h.Stdout = strings.TrimSpace(string(stdoutBytes))
	h.Stderr = strings.TrimSpace(string(stderrBytes))

	// Check for timeout
	if execCtx.Err() == context.DeadlineExceeded {
		return ContinueWithMessage(fmt.Sprintf("hook %s timed out after %v", h.HookName, h.Timeout)), nil
	}

	// Get exit code
	exitCode := 0
	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1 // Generic error
		}
	}

	// Honor stdout JSON control payloads (Claude Code official hooks and
	// marketplace plugin hooks). Such hooks legitimately exit 0 and signal
	// their verdict via a JSON object on stdout; without this, only exit codes
	// could block, so those hooks silently allowed every tool. A control block
	// wins regardless of HookAction; the control payload is stripped from
	// stdout (replaced by hookSpecificOutput.additionalContext when present) so
	// it never leaks into model context. Exit-code semantics still apply below;
	// a control payload can only ADD a block, never undo an exit-code block.
	ctrl := interpretHookControlPayload(h.Stdout)
	if ctrl.HasControl {
		h.Stdout = ctrl.Stdout
	}
	if ctrl.Block {
		reason := ctrl.Reason
		if reason == "" {
			reason = h.Stderr
		}
		if reason == "" {
			reason = fmt.Sprintf("hook %s blocked execution", h.HookName)
		}
		return Block(reason), nil
	}

	// Handle based on action type
	switch h.HookAction {
	case "block_exit2":
		// Claude Code semantics: Exit code 2 = blocking error
		// Exit code 0 = success, stdout added to context
		// Other exit codes = non-blocking error
		if exitCode == 2 {
			reason := h.Stderr
			if reason == "" {
				reason = h.Stdout
			}
			if reason == "" {
				reason = fmt.Sprintf("hook %s blocked execution", h.HookName)
			}
			return Block(reason), nil
		}
		if exitCode != 0 {
			// Non-blocking error, show stderr to user
			return ContinueWithMessage(fmt.Sprintf("hook %s failed (exit %d): %s", h.HookName, exitCode, h.Stderr)), nil
		}
		// Success - return stdout
		return ContinueWithMessage(h.Stdout), nil

	case "block":
		// Block if command failed (any non-zero exit)
		if exitCode != 0 {
			reason := h.Stderr
			if reason == "" {
				reason = h.Stdout
			}
			if reason == "" {
				reason = fmt.Sprintf("hook %s blocked event", h.HookName)
			}
			return Block(reason), nil
		}
		return ContinueWithMessage(h.Stdout), nil

	case "block_on_output":
		// Block if command produced output (stdout)
		if h.Stdout != "" {
			return Block(h.Stdout), nil
		}
		return Continue(), nil

	default: // "continue"
		// Always continue, just log output
		if h.Stdout != "" {
			return ContinueWithMessage(h.Stdout), nil
		}
		return Continue(), nil
	}
}

// buildClaudeCodeInput creates JSON input in Claude Code format.
func (h *ShellHook) buildClaudeCodeInput(event Event) []byte {
	return h.buildClaudeCodeInputImpl(event)
}

// hookControlPayload is the interpreted result of a stdout JSON control payload.
type hookControlPayload struct {
	HasControl bool   // stdout was a recognized JSON control object
	Block      bool   // the payload requested a block/deny
	Reason     string // block reason, if any
	Stdout     string // stdout after stripping the control payload
}

// controlPayloadKeys mark a JSON stdout object as a hook *control* payload
// rather than ordinary output. A hook legitimately printing {"foo":"bar"} is
// untouched because it contains none of these keys.
var controlPayloadKeys = map[string]struct{}{
	"result":             {},
	"decision":           {},
	"reason":             {},
	"hookSpecificOutput": {},
	"continue":           {},
	"stopReason":         {},
}

// interpretHookControlPayload recognizes Claude Code official and marketplace
// plugin stdout JSON verdicts so a command hook that exits 0 can still block.
// Dialects honored:
//   - plugin:   {"result":"block","reason":...}
//   - official: {"decision":"block","reason":...},
//     {"hookSpecificOutput":{"permissionDecision":"deny","permissionDecisionReason":...}},
//     {"continue":false,"stopReason":...}
//
// Plain text, non-control JSON, and invalid JSON are returned untouched
// (HasControl=false, original stdout preserved). When a control payload is
// present, the raw JSON is stripped from stdout and replaced by
// hookSpecificOutput.additionalContext (or empty) so it never leaks into model
// context.
func interpretHookControlPayload(stdout string) hookControlPayload {
	res := hookControlPayload{Stdout: stdout}
	text := strings.TrimSpace(stdout)
	if !strings.HasPrefix(text, "{") {
		return res
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return res
	}
	hasControl := false
	for k := range payload {
		if _, ok := controlPayloadKeys[k]; ok {
			hasControl = true
			break
		}
	}
	if !hasControl {
		return res
	}
	res.HasControl = true

	hookSpecific, _ := payload["hookSpecificOutput"].(map[string]any)

	blockRequested := payload["result"] == "block" || payload["decision"] == "block"
	if hookSpecific != nil && hookSpecific["permissionDecision"] == "deny" {
		blockRequested = true
	}
	if c, ok := payload["continue"].(bool); ok && !c {
		blockRequested = true
	}
	if blockRequested {
		res.Block = true
		reason, _ := payload["reason"].(string)
		if reason == "" && hookSpecific != nil {
			if r, ok := hookSpecific["permissionDecisionReason"].(string); ok {
				reason = r
			}
		}
		if reason == "" {
			if r, ok := payload["stopReason"].(string); ok {
				reason = r
			}
		}
		res.Reason = reason
	}

	// Strip the control payload from stdout; additionalContext, when present,
	// becomes the hook's visible output.
	res.Stdout = ""
	if hookSpecific != nil {
		if ac, ok := hookSpecific["additionalContext"].(string); ok {
			res.Stdout = ac
		}
	}
	return res
}

// buildClaudeCodeInputImpl creates JSON input in Claude Code format.
func (h *ShellHook) buildClaudeCodeInputImpl(event Event) []byte {
	// Build Claude Code compatible JSON input
	//
	// "session_id" here is DELIBERATELY event.ConversationID, matching
	// Claude Code's own hook JSON schema (Claude Code has no separate
	// "conversation" concept — its session_id IS the conversation). Do not
	// "fix" this to use SwarmOS's own process-level session id
	// (conversation.ProcessSessionID(), internal/conversation/joinkey.go) —
	// that would break every existing hook script written against this
	// contract. If you are building a joiner against SwarmOS's OWN process
	// session identity, this external field is the wrong value to key on;
	// use event.ConversationID (see its doc comment in event.go) or, for the
	// bench ledger, bench.Effect.SessionID instead.
	input := map[string]any{
		"session_id":      event.ConversationID,
		"transcript_path": transcriptPathForEvent(event),
		"cwd":             h.WorkingDir,
		"hook_event_name": mapToClaudeCodeEvent(event.Type),
	}

	// Add tool-specific fields for tool events
	if toolName, ok := event.Data["tool_name"].(string); ok {
		input["tool_name"] = toolName
	}
	if params, ok := event.Data["params"].(map[string]any); ok {
		input["tool_input"] = params
	} else if params, ok := event.Data["parameters"].(map[string]any); ok {
		input["tool_input"] = params
	}
	if result, ok := event.Data["result"]; ok {
		input["tool_response"] = result
	}

	// Add prompt for UserPromptSubmit
	if prompt, ok := event.Data["prompt"].(string); ok {
		input["prompt"] = prompt
	}

	// Copy any additional data
	for k, v := range event.Data {
		if _, exists := input[k]; !exists {
			input[k] = v
		}
	}

	jsonData, _ := json.Marshal(input)
	return jsonData
}

// mapToClaudeCodeEvent maps SwarmOS event types back to Claude Code event names.
func mapToClaudeCodeEvent(eventType string) string {
	reverseMap := map[string]string{
		EventToolBeforeExecute: "PreToolUse",
		EventToolAfterExecute:  "PostToolUse",
		"user.prompt_submit":   "UserPromptSubmit",
		"agent.stop":           "Stop",
		"subagent.stop":        "SubagentStop",
		"session.start":        "SessionStart",
		"session.end":          "SessionEnd",
		"notification":         "Notification",
		"compact.before":       "PreCompact",
	}
	if mapped, ok := reverseMap[eventType]; ok {
		return mapped
	}
	return eventType
}

// buildEnvironment creates environment variables from the event.
func (h *ShellHook) buildEnvironment(event Event) []string {
	env := os.Environ()

	// Claude Code compatible environment variables
	projectDir := h.WorkingDir
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}
	env = append(env,
		"CLAUDE_PROJECT_DIR="+projectDir,  // Claude Code compatibility
		"SWARMOS_PROJECT_DIR="+projectDir, // SwarmOS naming
	)

	// Add event metadata
	env = append(env,
		"HOOK_NAME="+h.HookName,
		"HOOK_EVENT_ID="+event.ID,
		"HOOK_EVENT_TYPE="+event.Type,
		"HOOK_TIMESTAMP="+event.Timestamp.Format(time.RFC3339),
		"HOOK_CONVERSATION_ID="+event.ConversationID,
		"HOOK_AGENT_ID="+event.AgentID,
		"HOOK_MODE_ID="+event.ModeID,
		"HOOK_GROUP_ID="+event.GroupID,
		"HOOK_TRACE_ID="+event.TraceID,
	)

	// Add event data as individual variables
	for key, value := range event.Data {
		envKey := "HOOK_DATA_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
		envValue := fmt.Sprintf("%v", value)
		env = append(env, envKey+"="+envValue)
	}

	// Add commonly used data fields with shorter names
	if toolName, ok := event.Data["tool_name"].(string); ok {
		env = append(env, "TOOL_NAME="+toolName)
	}
	if toolParams, ok := event.Data["parameters"].(map[string]any); ok {
		if paramsJSON, err := json.Marshal(toolParams); err == nil {
			env = append(env, "TOOL_PARAMS="+string(paramsJSON))
		}
	}
	if filePath, ok := event.Data["file_path"].(string); ok {
		env = append(env, "FILE_PATH="+filePath)
	}
	if command, ok := event.Data["command"].(string); ok {
		// COMMAND_WORDS is newline-separated so hook authors can match exact
		// executable names without scanning arguments or heredoc bodies.
		env = append(env,
			"COMMAND="+command,
			"COMMAND_WORDS="+strings.Join(ExtractShellCommandWords(command), "\n"),
		)
	}
	if content, ok := event.Data["content"].(string); ok {
		// Truncate very long content
		if len(content) > 1000 {
			content = content[:1000] + "..."
		}
		env = append(env, "CONTENT="+content)
	}
	if result, ok := event.Data["result"].(string); ok {
		// Truncate very long results
		if len(result) > 1000 {
			result = result[:1000] + "..."
		}
		env = append(env, "RESULT="+result)
	}
	if errMsg, ok := event.Data["error"].(string); ok {
		env = append(env, "ERROR="+errMsg)
	}

	return env
}

// matchEventPattern checks if an event type matches a pattern.
// Supports wildcards: "tool.*" matches "tool.before_execute"
func matchEventPattern(pattern, eventType string) bool {
	// Exact match
	if pattern == eventType {
		return true
	}

	// Match all
	if pattern == "*" {
		return true
	}

	// Wildcard matching
	patternParts := strings.Split(pattern, ".")
	eventParts := strings.Split(eventType, ".")

	for i, patternPart := range patternParts {
		if patternPart == "*" {
			// If this is the last part, match everything remaining
			if i == len(patternParts)-1 {
				return true
			}
			continue
		}

		// Check if we've run out of event parts
		if i >= len(eventParts) {
			return false
		}

		// Exact part match
		if patternPart != eventParts[i] {
			return false
		}
	}

	// All pattern parts matched
	return len(patternParts) == len(eventParts)
}

func (h *ShellHook) matchesPathConstraints(event Event) bool {
	if len(h.PathAllowlist) == 0 && len(h.PathDenylist) == 0 {
		return true
	}

	candidates := extractEventPaths(event)
	if len(candidates) == 0 {
		return len(h.PathAllowlist) == 0
	}

	if matchesAnyPattern(candidates, h.PathDenylist) {
		return false
	}

	if len(h.PathAllowlist) == 0 {
		return true
	}

	return matchesAnyPattern(candidates, h.PathAllowlist)
}

func extractEventPaths(event Event) []string {
	params, ok := event.Data["params"].(map[string]any)
	if !ok {
		params, _ = event.Data["parameters"].(map[string]any)
	}
	if params == nil {
		return nil
	}

	keys := []string{
		"path",
		"file_path",
		"dir_path",
		"source",
		"destination",
		"old_path",
		"new_path",
		"target",
		"root",
	}

	var paths []string
	for _, key := range keys {
		values := extractStringValues(params[key])
		for _, value := range values {
			if value != "" {
				paths = append(paths, normalizePathCandidate(value)...)
			}
		}
	}

	if patch, ok := params["patch"].(string); ok && patch != "" {
		for _, p := range extractPatchPaths(patch) {
			paths = append(paths, normalizePathCandidate(p)...)
		}
	}

	return uniqueStrings(paths)
}

func extractStringValues(value any) []string {
	switch v := value.(type) {
	case string:
		return []string{v}
	case []string:
		return v
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func extractPatchPaths(patch string) []string {
	var paths []string
	lines := strings.Split(patch, "\n")
	prefixes := []string{
		"*** Update File: ",
		"*** Add File: ",
		"*** Delete File: ",
		"*** Move to: ",
	}
	for _, line := range lines {
		for _, prefix := range prefixes {
			if after, ok := strings.CutPrefix(line, prefix); ok {
				pathValue := strings.TrimSpace(after)
				if pathValue != "" {
					paths = append(paths, pathValue)
				}
				break
			}
		}
	}
	return paths
}

func normalizePathCandidate(pathValue string) []string {
	cleaned := filepath.Clean(pathValue)
	cleaned = filepath.ToSlash(cleaned)
	raw := filepath.ToSlash(pathValue)

	if cleaned == raw {
		return []string{cleaned}
	}
	return []string{cleaned, raw}
}

func matchesAnyPattern(values []string, patterns []string) bool {
	for _, value := range values {
		for _, patternValue := range patterns {
			if matchPathPattern(patternValue, value) {
				return true
			}
		}
	}
	return false
}

func matchPathPattern(patternValue, candidate string) bool {
	patternValue = strings.TrimSpace(patternValue)
	if patternValue == "" {
		return false
	}

	patternValue = filepath.ToSlash(patternValue)
	candidate = filepath.ToSlash(candidate)

	if strings.ContainsAny(patternValue, "*?[]") {
		if matched, err := path.Match(patternValue, candidate); err == nil && matched {
			return true
		}
		return false
	}

	return strings.HasPrefix(candidate, filepath.ToSlash(patternValue))
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	var out []string
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// ShellHookConfig is the JSON-serializable configuration for a shell hook.
type ShellHookConfig struct {
	Name             string               `json:"name"`
	Description      string               `json:"description"`
	EventPatterns    []string             `json:"event_patterns"`
	ToolMatcher      string               `json:"tool_matcher,omitempty"` // Regex pattern for tool name filtering (Claude Code matcher)
	Command          string               `json:"command"`
	Priority         int                  `json:"priority"`
	Timeout          string               `json:"timeout"` // e.g., "60s", "1m" (Claude Code default is 60s)
	Action           string               `json:"action"`  // "continue", "block", "block_exit2", "block_on_output"
	Enabled          bool                 `json:"enabled"`
	PermissionPolicy HookPermissionPolicy `json:"permission_policy,omitempty"`
	PathAllowlist    []string             `json:"path_allowlist,omitempty"`
	PathDenylist     []string             `json:"path_denylist,omitempty"`
	PassEventAsJSON  bool                 `json:"pass_event_json"`
	WorkingDir       string               `json:"working_dir,omitempty"`
	CreatedAt        string               `json:"created_at,omitempty"`
}

// ToShellHook converts a config to a ShellHook.
func (c *ShellHookConfig) ToShellHook() (*ShellHook, error) {
	// Claude Code default is 60 seconds
	timeout := 60 * time.Second
	if c.Timeout != "" {
		parsed, err := time.ParseDuration(c.Timeout)
		if err == nil {
			timeout = parsed
		}
	}

	// Default to Claude Code semantics (exit 2 = block)
	action := c.Action
	if action == "" {
		action = "block_exit2"
	}

	hook := &ShellHook{
		HookName:         c.Name,
		Description:      c.Description,
		EventPatterns:    c.EventPatterns,
		ToolMatcher:      c.ToolMatcher,
		Command:          c.Command,
		HookPriority:     c.Priority,
		Timeout:          timeout,
		HookAction:       action,
		PermissionPolicy: NormalizeHookPermissionPolicy(c.PermissionPolicy),
		PathAllowlist:    normalizePatternList(c.PathAllowlist),
		PathDenylist:     normalizePatternList(c.PathDenylist),
		PassEventAsJSON:  c.PassEventAsJSON,
		WorkingDir:       c.WorkingDir,
	}

	// Compile tool matcher regex if present
	if c.ToolMatcher != "" && c.ToolMatcher != "*" {
		if re, err := regexp.Compile("^(" + c.ToolMatcher + ")$"); err == nil {
			hook.ToolMatcherRegex = re
		}
	}

	return hook, nil
}

// FromShellHook creates a config from a ShellHook.
func FromShellHook(h *ShellHook, enabled bool) *ShellHookConfig {
	return &ShellHookConfig{
		Name:             h.HookName,
		Description:      h.Description,
		EventPatterns:    h.EventPatterns,
		ToolMatcher:      h.ToolMatcher,
		Command:          h.Command,
		Priority:         h.HookPriority,
		Timeout:          h.Timeout.String(),
		Action:           h.HookAction,
		Enabled:          enabled,
		PermissionPolicy: NormalizeHookPermissionPolicy(h.PermissionPolicy),
		PathAllowlist:    normalizePatternList(h.PathAllowlist),
		PathDenylist:     normalizePatternList(h.PathDenylist),
		PassEventAsJSON:  h.PassEventAsJSON,
		WorkingDir:       h.WorkingDir,
		CreatedAt:        time.Now().Format(time.RFC3339),
	}
}

func normalizePatternList(values []string) []string {
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
