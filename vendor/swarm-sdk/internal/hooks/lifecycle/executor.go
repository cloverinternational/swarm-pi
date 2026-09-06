package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Executor runs lifecycle hooks.
type Executor struct {
	config LifecycleHooksConfig

	// PromptEvaluator is called for prompt-type hooks.
	// If nil, prompt hooks return "allow" by default.
	PromptEvaluator func(ctx context.Context, prompt string, hookCtx *HookContext) (*HookDecision, error)

	// DefaultTimeout for command execution.
	DefaultTimeout time.Duration
}

// NewExecutor creates a new lifecycle hook executor.
func NewExecutor(config LifecycleHooksConfig) *Executor {
	// Compile all matchers
	compileMatchers := func(matchers []HookMatcher) {
		for i := range matchers {
			// Compile is best-effort here; invalid patterns produce no matches
			// rather than halting executor startup.
			_ = matchers[i].Compile()
		}
	}

	compileMatchers(config.PreToolUse)
	compileMatchers(config.PostToolUse)
	compileMatchers(config.Stop)
	compileMatchers(config.SessionStart)
	compileMatchers(config.Notification)
	compileMatchers(config.PreMessage)
	compileMatchers(config.PostMessage)

	return &Executor{
		config:         config,
		DefaultTimeout: 60 * time.Second,
	}
}

// Execute runs hooks for the given event.
func (e *Executor) Execute(ctx context.Context, hookCtx *HookContext) ([]*HookDecision, error) {
	matchers := e.getMatchersForEvent(hookCtx.Event)
	if len(matchers) == 0 {
		return nil, nil
	}

	// Get match target based on event type
	matchTarget := e.getMatchTarget(hookCtx)

	var decisions []*HookDecision

	for _, matcher := range matchers {
		if !matcher.Matches(matchTarget) {
			continue
		}

		for _, hook := range matcher.Hooks {
			decision, err := e.executeHook(ctx, hook, hookCtx)
			if err != nil {
				return decisions, fmt.Errorf("hook execution failed: %w", err)
			}

			decisions = append(decisions, decision)

			// Stop on blocking decision
			if decision.IsBlocked() {
				return decisions, nil
			}
		}
	}

	return decisions, nil
}

// ExecutePreToolUse runs PreToolUse hooks and returns combined decision.
func (e *Executor) ExecutePreToolUse(ctx context.Context, toolName string, input map[string]any, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventPreToolUse
	hookCtx.ToolName = toolName
	hookCtx.ToolInput = input

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

// ExecutePostToolUse runs PostToolUse hooks.
func (e *Executor) ExecutePostToolUse(ctx context.Context, toolName string, output any, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventPostToolUse
	hookCtx.ToolName = toolName
	hookCtx.ToolOutput = output

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

// ExecuteStop runs Stop hooks.
func (e *Executor) ExecuteStop(ctx context.Context, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventStop

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

// ExecuteSessionStart runs SessionStart hooks.
func (e *Executor) ExecuteSessionStart(ctx context.Context, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventSessionStart

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

// ExecuteNotification runs Notification hooks.
func (e *Executor) ExecuteNotification(ctx context.Context, message string, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventNotification
	hookCtx.Message = message

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

// ExecutePostMessage runs PostMessage hooks.
// Fires after the agent responds with a message.
func (e *Executor) ExecutePostMessage(ctx context.Context, message string, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventPostMessage
	hookCtx.Message = message

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

// ExecutePreMessage runs PreMessage hooks.
// Fires before sending a message to the agent.
func (e *Executor) ExecutePreMessage(ctx context.Context, message string, hookCtx *HookContext) (*HookDecision, error) {
	hookCtx.Event = EventPreMessage
	hookCtx.Message = message

	decisions, err := e.Execute(ctx, hookCtx)
	if err != nil {
		return nil, err
	}

	return e.combineDecisions(decisions), nil
}

func (e *Executor) getMatchersForEvent(event HookEvent) []HookMatcher {
	switch event {
	case EventPreToolUse:
		return e.config.PreToolUse
	case EventPostToolUse:
		return e.config.PostToolUse
	case EventStop:
		return e.config.Stop
	case EventSessionStart:
		return e.config.SessionStart
	case EventNotification:
		return e.config.Notification
	case EventPreMessage:
		return e.config.PreMessage
	case EventPostMessage:
		return e.config.PostMessage
	default:
		return nil
	}
}

func (e *Executor) getMatchTarget(hookCtx *HookContext) string {
	switch hookCtx.Event {
	case EventPreToolUse, EventPostToolUse:
		return hookCtx.ToolName
	case EventNotification, EventPreMessage, EventPostMessage:
		return hookCtx.Message
	default:
		return "*"
	}
}

func (e *Executor) executeHook(ctx context.Context, hook HookConfig, hookCtx *HookContext) (*HookDecision, error) {
	start := time.Now()

	var decision *HookDecision
	var err error

	switch hook.Type {
	case HookTypeCommand:
		decision, err = e.executeCommand(ctx, hook, hookCtx)
	case HookTypePrompt:
		decision, err = e.executePrompt(ctx, hook, hookCtx)
	default:
		return nil, fmt.Errorf("unknown hook type: %s", hook.Type)
	}

	if decision != nil {
		decision.Duration = time.Since(start)
	}

	return decision, err
}

// executeCommand runs a shell command hook.
// Output is written to temp files to avoid buffer issues and enable recovery.
func (e *Executor) executeCommand(ctx context.Context, hook HookConfig, hookCtx *HookContext) (*HookDecision, error) {
	// Expand variables in command
	command := e.expandVariables(hook.Command, hookCtx)

	// Set timeout
	timeout := e.DefaultTimeout
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create temp files for stdout and stderr
	stdoutFile, err := os.CreateTemp("/tmp", "lifecycle-hook-stdout-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout temp file: %w", err)
	}
	stdoutPath := stdoutFile.Name()
	defer os.Remove(stdoutPath)

	stderrFile, err := os.CreateTemp("/tmp", "lifecycle-hook-stderr-*.txt")
	if err != nil {
		stdoutFile.Close()
		os.Remove(stdoutPath)
		return nil, fmt.Errorf("failed to create stderr temp file: %w", err)
	}
	stderrPath := stderrFile.Name()
	defer os.Remove(stderrPath)

	// Create command
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.WaitDelay = 2 * time.Second

	// Set working directory
	if hookCtx.WorkingDir != "" {
		cmd.Dir = hookCtx.WorkingDir
	}

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range hook.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, e.expandVariables(v, hookCtx)))
	}

	// Add hook context to environment
	cmd.Env = append(cmd.Env, e.buildEnvFromContext(hookCtx)...)

	// Redirect output to temp files
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	// Run command
	cmdErr := cmd.Run()

	// Close files before reading
	stdoutFile.Close()
	stderrFile.Close()

	// Read output from temp files
	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stderrBytes, _ := os.ReadFile(stderrPath)

	decision := &HookDecision{
		Output:   string(stdoutBytes),
		ExitCode: 0,
	}

	if cmdErr != nil {
		if exitErr, ok := cmdErr.(*exec.ExitError); ok {
			decision.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("command execution failed: %w", cmdErr)
		}
	}

	// Parse decision from exit code
	switch decision.ExitCode {
	case 0:
		decision.Decision = "allow"
	case 1:
		decision.Decision = "deny"
		decision.Reason = string(stderrBytes)
	case 2:
		decision.Decision = "allow"
		decision.SystemMessage = string(stderrBytes)
		decision.Error = fmt.Errorf("%s", string(stderrBytes))
	default:
		decision.Decision = "deny"
		decision.Reason = fmt.Sprintf("exit code %d: %s", decision.ExitCode, string(stderrBytes))
	}

	// Interpret a stdout JSON control payload for structured decisions. Honors
	// the flat SwarmOS form AND the Claude Code official dialects (nested
	// hookSpecificOutput.permissionDecision, continue:false) plus the
	// marketplace plugin form (result:"block"). When a control payload is
	// present the raw JSON is stripped from Output (replaced by
	// hookSpecificOutput.additionalContext when set) so it never leaks into
	// model context. A payload can only ADD a block, never undo an exit-code
	// block, because block-signaling fields are only ever set (non-empty),
	// never cleared.
	if len(stdoutBytes) > 0 {
		applyControlPayload(decision, stdoutBytes)
	}

	return decision, nil
}

// applyControlPayload merges a stdout JSON control payload into decision,
// honoring the flat SwarmOS form, the Claude Code official dialects
// (hookSpecificOutput.permissionDecision, continue:false, stopReason), and the
// marketplace plugin form (result:"block"). It strips the raw control JSON from
// decision.Output so it never reaches model context.
func applyControlPayload(decision *HookDecision, stdoutBytes []byte) {
	text := strings.TrimSpace(string(stdoutBytes))
	if !strings.HasPrefix(text, "{") {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return
	}
	controlKeys := map[string]struct{}{
		"decision": {}, "permissionDecision": {}, "result": {},
		"reason": {}, "systemMessage": {}, "updatedInput": {},
		"hookSpecificOutput": {}, "continue": {}, "stopReason": {}, "output": {},
	}
	hasControl := false
	for k := range payload {
		if _, ok := controlKeys[k]; ok {
			hasControl = true
			break
		}
	}
	if !hasControl {
		return
	}

	hookSpecific, _ := payload["hookSpecificOutput"].(map[string]any)

	if s, ok := payload["decision"].(string); ok && s != "" {
		decision.Decision = s
	}
	// Plugin dialect: {"result":"block"|"deny"} escalates to a block.
	if s, ok := payload["result"].(string); ok && (s == "block" || s == "deny") {
		decision.Decision = "block"
	}
	if s, ok := payload["permissionDecision"].(string); ok && s != "" {
		decision.PermissionDecision = s
	}
	// Official Claude Code nests permissionDecision under hookSpecificOutput.
	if hookSpecific != nil {
		if s, ok := hookSpecific["permissionDecision"].(string); ok && s != "" {
			decision.PermissionDecision = s
		}
	}
	// Official stop signal: continue:false escalates to a block.
	if c, ok := payload["continue"].(bool); ok && !c {
		if decision.Decision == "" || decision.Decision == "allow" {
			decision.Decision = "block"
		}
	}
	if ui, ok := payload["updatedInput"].(map[string]any); ok {
		decision.UpdatedInput = ui
	}
	// Reason precedence: reason -> permissionDecisionReason -> stopReason.
	if s, ok := payload["reason"].(string); ok && s != "" {
		decision.Reason = s
	} else if hookSpecific != nil {
		if s, ok := hookSpecific["permissionDecisionReason"].(string); ok && s != "" {
			decision.Reason = s
		}
	}
	if decision.Reason == "" {
		if s, ok := payload["stopReason"].(string); ok && s != "" {
			decision.Reason = s
		}
	}
	if s, ok := payload["systemMessage"].(string); ok && s != "" {
		decision.SystemMessage = s
	}

	// Strip the raw control JSON from Output; additionalContext, when present,
	// becomes the hook's visible output.
	decision.Output = ""
	if hookSpecific != nil {
		if ac, ok := hookSpecific["additionalContext"].(string); ok {
			decision.Output = ac
		}
	}
}

func (e *Executor) executePrompt(ctx context.Context, hook HookConfig, hookCtx *HookContext) (*HookDecision, error) {
	// Expand variables in prompt
	prompt := e.expandVariables(hook.Prompt, hookCtx)

	// If no prompt evaluator, allow by default
	if e.PromptEvaluator == nil {
		return &HookDecision{
			Decision:      "allow",
			SystemMessage: "No prompt evaluator configured",
		}, nil
	}

	// Set timeout
	timeout := e.DefaultTimeout
	if hook.Timeout > 0 {
		timeout = time.Duration(hook.Timeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return e.PromptEvaluator(ctx, prompt, hookCtx)
}

func (e *Executor) expandVariables(input string, hookCtx *HookContext) string {
	result := input

	// Standard substitutions
	replacements := map[string]string{
		"${CLAUDE_PLUGIN_ROOT}": hookCtx.PluginRoot,
		"${SWARM_PLUGIN_ROOT}":  hookCtx.PluginRoot,
		"${TOOL_NAME}":          hookCtx.ToolName,
		"${SESSION_ID}":         hookCtx.SessionID,
		"${CONVERSATION_ID}":    hookCtx.ConversationID,
		"${WORKING_DIR}":        hookCtx.WorkingDir,
	}

	for k, v := range replacements {
		result = strings.ReplaceAll(result, k, v)
	}

	return result
}

func (e *Executor) buildEnvFromContext(hookCtx *HookContext) []string {
	env := []string{
		fmt.Sprintf("SWARM_EVENT=%s", hookCtx.Event),
		fmt.Sprintf("SWARM_SESSION_ID=%s", hookCtx.SessionID),
		fmt.Sprintf("SWARM_CONVERSATION_ID=%s", hookCtx.ConversationID),
	}

	if hookCtx.ToolName != "" {
		env = append(env, fmt.Sprintf("SWARM_TOOL_NAME=%s", hookCtx.ToolName))
	}

	if hookCtx.ToolInput != nil {
		if inputJSON, err := json.Marshal(hookCtx.ToolInput); err == nil {
			env = append(env, fmt.Sprintf("SWARM_TOOL_INPUT=%s", string(inputJSON)))
		}
	}

	if hookCtx.ToolOutput != nil {
		if outputJSON, err := json.Marshal(hookCtx.ToolOutput); err == nil {
			env = append(env, fmt.Sprintf("SWARM_TOOL_OUTPUT=%s", string(outputJSON)))
		}
	}

	if hookCtx.Message != "" {
		env = append(env, fmt.Sprintf("SWARM_MESSAGE=%s", hookCtx.Message))
	}

	return env
}

func (e *Executor) combineDecisions(decisions []*HookDecision) *HookDecision {
	if len(decisions) == 0 {
		return &HookDecision{Decision: "allow"}
	}

	combined := &HookDecision{
		Decision: "allow",
	}

	var systemMessages []string
	var outputs []string

	for _, d := range decisions {
		if d.IsBlocked() {
			combined.Decision = d.Decision
			combined.PermissionDecision = d.PermissionDecision
			combined.Reason = d.Reason
			return combined
		}

		if d.NeedsConfirmation() {
			combined.Decision = "ask"
			combined.Reason = d.Reason
		}

		if d.UpdatedInput != nil {
			if combined.UpdatedInput == nil {
				combined.UpdatedInput = make(map[string]any)
			}
			maps.Copy(combined.UpdatedInput, d.UpdatedInput)
		}

		if d.SystemMessage != "" {
			systemMessages = append(systemMessages, d.SystemMessage)
		}

		if d.Output != "" {
			outputs = append(outputs, d.Output)
		}

		combined.Duration += d.Duration
	}

	if len(systemMessages) > 0 {
		combined.SystemMessage = strings.Join(systemMessages, "\n")
	}

	if len(outputs) > 0 {
		combined.Output = strings.Join(outputs, "\n")
	}

	return combined
}
