package bgprocess

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// BackgroundBashTool wraps the standard Bash tool and adds background execution support.
type BackgroundBashTool struct {
	baseTool                tools.Tool
	manager                 *BackgroundProcessManager
	defaultTimeout          time.Duration
	autoBackgroundSec       int  // Auto-background after this many seconds (0 = disabled)
	allowExplicitBackground bool // When false, the agent cannot set background=true (default: false)

	// Mid-execution backgrounding support (Ctrl+B)
	mu                sync.Mutex
	currentHandle     ProcessHandle      // Currently executing process handle
	currentCancel     context.CancelFunc // Cancel function for the foreground wait
	currentCommand    string             // Currently executing command (for UI display)
	backgroundTrigger chan struct{}      // Signal channel for user-triggered backgrounding
}

// BackgroundBashToolConfig contains configuration for the background bash tool.
type BackgroundBashToolConfig struct {
	BashConfig              builtin.BashToolConfig
	Manager                 *BackgroundProcessManager
	DefaultTimeout          time.Duration
	AutoBackgroundSec       int
	AllowExplicitBackground bool // When true, agent can set background=true to immediately background commands
}

// NewBackgroundBashTool creates a new BackgroundBashTool.
func NewBackgroundBashTool(config BackgroundBashToolConfig) *BackgroundBashTool {
	return &BackgroundBashTool{
		baseTool:                builtin.NewBashToolWithConfig(config.BashConfig),
		manager:                 config.Manager,
		defaultTimeout:          config.DefaultTimeout,
		autoBackgroundSec:       config.AutoBackgroundSec,
		allowExplicitBackground: config.AllowExplicitBackground,
		backgroundTrigger:       make(chan struct{}, 1), // Buffered to prevent blocking
	}
}

// Background reasons recorded in tool-result metadata under "background_reason".
// The TUI renderer uses these to explain WHY a command was backgrounded.
const (
	backgroundReasonExplicit = "explicit" // agent set background=true
	backgroundReasonIdle     = "idle"     // auto-backgrounded after idle timeout
	backgroundReasonUser     = "user"     // user pressed Ctrl+B
)

// backgroundedResult builds a ToolResult for a backgrounded command with the
// unified metadata signal the TUI renderer relies on. All three backgrounding
// paths (explicit, idle auto-background, Ctrl+B) funnel through here so the
// renderer can detect backgrounding via metadata instead of sniffing the JSON
// body. output is the JSON payload shown to the agent.
func backgroundedResult(output, taskID, reason string) *tools.ToolResult {
	return tools.NewToolResult(output).
		WithMetadata("task_id", taskID).
		WithMetadata("background", true).
		WithMetadata("background_reason", reason)
}

func completedProcessResult(result *ProcessResult) *tools.ToolResult {
	// Subprocesses run under the PTY-forcing `script` buffering fallback
	// (used when stdbuf is unavailable) auto-detect a terminal and emit SGR
	// color / cursor-control escape codes even though the caller never asked
	// for color, corrupting model-facing output (issue #303) — e.g.
	// ripgrep's match highlighting wraps the matched substring in escape
	// bytes that read as noise or get misparsed as if the text were missing.
	// Reuse the SDK Bash tool's own sanitizer rather than duplicating it.
	output := builtin.StripANSI(string(result.Output))
	if output == "" {
		if result.ExitCode == 0 {
			output = "Command completed successfully (no output)"
		} else {
			output = "Command failed with no output"
		}
	}
	if result.ExitCode != 0 {
		output = fmt.Sprintf("Exit code: %d\n%s", result.ExitCode, output)
	}
	return &tools.ToolResult{
		Output:  output,
		IsError: result.ExitCode != 0,
	}
}

// Name returns the tool name.
func (t *BackgroundBashTool) Name() string {
	return "Bash"
}

// Description returns the tool description.
func (t *BackgroundBashTool) Description() string {
	desc := `Execute shell commands and return the output. Supports pipes, redirections, and shell features.
Both stdout and stderr are captured.

IMPORTANT: You MUST specify timeout_seconds for every command to prevent hanging.

Command runs and blocks until completion or timeout.
- If completes within timeout: Returns output immediately
- If exceeds timeout: Auto-backgrounds and returns task_id for later retrieval

Parameters:
- command (required): Shell command to execute
- cwd: Working directory (default: current directory)
- env: Environment variables as key-value pairs
- timeout_seconds (REQUIRED): Maximum seconds to wait before auto-backgrounding

Example:
{
  "command": "npm test",
  "timeout_seconds": 30
}
Returns: Output if completes in 30s, or {"backgrounded": true, "task_id": "..."} if exceeds timeout`

	if t.allowExplicitBackground {
		desc += `

You may also set "background": true to immediately background a command without waiting.
Use ReadBackgroundCommand tool to check status later.`
	}

	return desc
}

// Parameters returns the JSON schema for tool parameters.
func (t *BackgroundBashTool) Parameters() any {
	// Get base parameters
	baseParams := t.baseTool.Parameters().(map[string]any)
	properties := baseParams["properties"].(map[string]any)

	// Modify timeout_seconds to be REQUIRED
	properties["timeout_seconds"] = map[string]any{
		"type":        "integer",
		"description": "REQUIRED: Maximum seconds to wait for command completion. If exceeded, command auto-backgrounds. Minimum: 1 second.",
		"minimum":     1,
	}

	// Only expose the background parameter when explicit backgrounding is enabled
	if t.allowExplicitBackground {
		properties["background"] = map[string]any{
			"type":        "boolean",
			"description": "Optional: Set to true to immediately run in background without waiting. Use for known long-running commands.",
			"default":     false,
		}
	}

	// Make timeout_seconds required
	if required, ok := baseParams["required"].([]string); ok {
		// Add timeout_seconds to required fields if not already there
		hasTimeout := slices.Contains(required, "timeout_seconds")
		if !hasTimeout {
			baseParams["required"] = append(required, "timeout_seconds")
		}
	} else {
		baseParams["required"] = []string{"command", "timeout_seconds"}
	}

	return baseParams
}

// Execute runs the command with timeout and auto-background support.
func (t *BackgroundBashTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Extract timeout (REQUIRED parameter)
	timeoutSec := 0.0
	if ts, ok := params["timeout_seconds"].(float64); ok {
		timeoutSec = ts
	}
	if timeoutSec <= 0 {
		return tools.NewErrorResult(fmt.Errorf("timeout_seconds is required and must be > 0")), nil
	}

	// Check if explicit background mode is requested (only when flag is enabled)
	if t.allowExplicitBackground {
		if bg, ok := params["background"].(bool); ok && bg {
			return t.executeBackground(ctx, params)
		}
	}

	// Foreground with timeout: Run command, wait up to timeout, auto-background if exceeded
	return t.executeForegroundWithTimeout(ctx, params, time.Duration(timeoutSec*float64(time.Second)))
}

// executeForegroundWithTimeout runs command in foreground, auto-backgrounds if timeout exceeded.
func (t *BackgroundBashTool) executeForegroundWithTimeout(ctx context.Context, params map[string]any, timeout time.Duration) (*tools.ToolResult, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return tools.NewErrorResult(fmt.Errorf("command is required")), nil
	}

	cwd, _ := params["cwd"].(string)

	// Build environment
	env := make(map[string]string)
	if envMap, ok := params["env"].(map[string]any); ok {
		for k, v := range envMap {
			if vs, ok := v.(string); ok {
				env[k] = vs
			}
		}
	}

	// Extract owner info from context (injected by SDK agent at tool execution time)
	owner := ownerFromContext(ctx)
	// `timeout` here is the FOREGROUND WAIT / idle threshold (see
	// startIdleTimeoutWatcher below) — it decides when we stop blocking and
	// auto-background the command. It must NOT also bound the process's own
	// lifetime: a previous version derived a process-level SpawnRequest.Timeout
	// as max(timeout*2, 5*time.Minute), which silently SIGKILLed the process
	// itself once that deadline passed, regardless of whether it had already
	// been auto-backgrounded and was still healthy. Because most callers
	// request small timeout_seconds (30-120s), timeout*2 almost always lost
	// to the "5 minute minimum" floor, so in practice nearly every
	// backgrounded command was killed at a silent, undocumented ~300s
	// ceiling — exactly the failure reported in issues #282 and #256 (two
	// independent long-running, actively-producing-output processes killed
	// at 300.03-300.04s with exit_code -1, unrelated to their requested
	// timeout_seconds of 30/60/120). A background process must run until
	// natural completion or an explicit ReadBackgroundCommand
	// (action="cancel") — leaving SpawnRequest.Timeout unset (0) gives it an
	// uncancelled context (see executor.go: Timeout<=0 skips
	// context.WithTimeout entirely) instead of an implicit wall-clock cap.
	req := SpawnRequest{
		Command: command,
		WorkDir: cwd,
		Env:     env,
		Owner:   owner,
		Tags:    map[string]string{"source": "bash_tool", "mode": "foreground_with_timeout"},
	}

	// Spawn the background process
	handle, err := t.manager.Spawn(ctx, req)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to spawn process: %w", err)), nil
	}

	// Idle-timeout wait context. The old implementation used context.WithTimeout
	//	with a hard wall-clock deadline, which killed healthy long-running
	//	commands (IMAP search, `go build` during toolchain download) that were
	//	producing steady output but hadn't yet completed. Survey 2026-04-27
	//	flagged this as a systemic problem.
	//
	//	The new behavior: the timeout is an IDLE timeout. Whenever the process
	//	emits output (measured by exec.Output().Size() advancing) we extend the
	//	deadline by the original timeout window. A process that is silent for
	//	`timeout` seconds still gets auto-backgrounded just like before; a
	//	process that prints something every second can run indefinitely. The
	//	underlying process itself now has NO separate wall-clock kill deadline
	//	(see the SpawnRequest built above) — once auto-backgrounded it keeps
	//	running until it exits on its own or is explicitly cancelled via
	//	ReadBackgroundCommand(action="cancel"), per issues #282/#256.
	waitCtx, cancel := context.WithCancel(ctx)
	idleFired := new(atomicBool)
	idleStop := t.startIdleTimeoutWatcher(handle, timeout, idleFired, cancel)
	defer idleStop()

	// Track current execution for Ctrl+B backgrounding
	t.mu.Lock()
	t.currentHandle = handle
	t.currentCancel = cancel
	t.currentCommand = command
	// Drain any stale signal
	select {
	case <-t.backgroundTrigger:
	default:
	}
	t.mu.Unlock()

	// Cleanup tracking when done
	defer func() {
		t.mu.Lock()
		t.currentHandle = ProcessHandle{}
		t.currentCancel = nil
		t.currentCommand = ""
		t.mu.Unlock()
		cancel()
	}()

	// Wait for completion, timeout, or user-triggered background (Ctrl+B)
	resultCh := make(chan *ProcessResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := t.manager.Wait(waitCtx, handle)
		if err != nil {
			errCh <- err
		} else {
			resultCh <- result
		}
	}()

	var userTriggered bool
	select {
	case result := <-resultCh:
		// Command completed within timeout - return output normally
		return completedProcessResult(result), nil

	case err := <-errCh:
		// Discriminate: idle-timeout watcher fires idleFired BEFORE calling
		//	cancel(), so we can tell "the idle watcher killed the wait" apart
		//	from "the user hit Ctrl+B" (both present as context.Canceled).
		if idleFired.Load() {
			// Idle timeout exceeded - command auto-backgrounded. "Idle" here
			//	means no output in `timeout` seconds; a noisy process can run
			//	well beyond the timeout without triggering this branch.
			t.manager.MarkBackgrounded(handle.ID())
			response := map[string]any{
				"backgrounded":    true,
				"task_id":         handle.ID(),
				"status":          "running",
				"message":         fmt.Sprintf("Command idle for %v (no output) and was auto-backgrounded. Use ReadBackgroundCommand to check status — look at metadata.seconds_since_last_output to decide if it is still healthy.", timeout),
				"timeout_seconds": timeout.Seconds(),
			}
			responseJSON, _ := json.Marshal(response)
			return backgroundedResult(string(responseJSON), handle.ID(), backgroundReasonIdle), nil
		}

		// Check if user triggered backgrounding via Ctrl+B
		if waitCtx.Err() == context.Canceled {
			userTriggered = true
		} else {
			// Other error
			return tools.NewErrorResult(fmt.Errorf("command execution error: %w", err)), nil
		}

	case <-t.backgroundTrigger:
		// User pressed Ctrl+B to background this command
		userTriggered = true
		cancel() // Cancel the wait context
	}

	// User-triggered backgrounding (Ctrl+B)
	if userTriggered {
		t.manager.MarkBackgrounded(handle.ID())
		response := map[string]any{
			"backgrounded":   true,
			"task_id":        handle.ID(),
			"status":         "running",
			"message":        "Command was backgrounded by user (Ctrl+B). Use ReadBackgroundCommand to check status.",
			"user_triggered": true,
		}
		responseJSON, _ := json.Marshal(response)
		return backgroundedResult(string(responseJSON), handle.ID(), backgroundReasonUser), nil
	}

	// Should not reach here
	return tools.NewErrorResult(fmt.Errorf("unexpected execution path")), nil
}

// executeBackground runs the command in background mode.
func (t *BackgroundBashTool) executeBackground(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	command, _ := params["command"].(string)
	if command == "" {
		return tools.NewErrorResult(fmt.Errorf("command is required")), nil
	}

	cwd, _ := params["cwd"].(string)

	// Build environment
	env := make(map[string]string)
	if envMap, ok := params["env"].(map[string]any); ok {
		for k, v := range envMap {
			if vs, ok := v.(string); ok {
				env[k] = vs
			}
		}
	}

	// Get timeout. Unlike the foreground/streaming paths, an explicitly
	// backgrounded command (background=true) has no idle-watcher governing
	// it, so IF the caller gave a timeout_seconds it genuinely means "cap
	// this process's lifetime at N seconds" and is honored as-is below.
	// But when the caller did NOT give one, falling back to
	// t.defaultTimeout (5 minutes) silently imposed the same undocumented
	// ~300s kill ceiling this whole fix removes elsewhere (issues #282,
	// #256) — a plain `background: true` with no timeout_seconds must run
	// until natural completion or an explicit cancel, not die quietly at 5
	// minutes. Leave req.Timeout at zero (no cap) in that case.
	var timeout time.Duration
	if ts, ok := params["timeout_seconds"].(float64); ok && ts > 0 {
		timeout = time.Duration(ts) * time.Second
	}

	// Extract owner info from context (injected by SDK agent at tool execution time)
	owner := ownerFromContext(ctx)

	// Build spawn request
	req := SpawnRequest{
		Command: command,
		WorkDir: cwd,
		Env:     env,
		Owner:   owner,
		Timeout: timeout,
		Tags:    map[string]string{"source": "bash_tool"},
	}

	// Spawn the background process
	handle, err := t.manager.Spawn(ctx, req)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to spawn background process: %w", err)), nil
	}

	// Mark as explicitly backgrounded so completion callbacks fire
	t.manager.MarkBackgrounded(handle.ID())

	// Return the task ID
	result := map[string]any{
		"status":  "backgrounded",
		"task_id": handle.ID(),
		"message": fmt.Sprintf("Command is running in background. Use ReadBackgroundCommand with task_id=%q to check status and read output.", handle.ID()),
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to marshal result: %w", err)), nil
	}

	return backgroundedResult(string(jsonBytes), handle.ID(), backgroundReasonExplicit), nil
}

// ExecuteStreaming runs the command with streaming output support.
// This method spawns through the manager to enable Ctrl+B backgrounding,
// while streaming output to the caller via onOutput callback.
func (t *BackgroundBashTool) ExecuteStreaming(ctx context.Context, params map[string]any, onOutput func(chunk string, stream string)) (*tools.ToolResult, error) {
	// Check if background mode is requested (only when flag is enabled)
	if t.allowExplicitBackground {
		if bg, ok := params["background"].(bool); ok && bg {
			return t.executeBackground(ctx, params)
		}
	}

	// Extract command and build spawn request (same as executeForegroundWithTimeout)
	command, _ := params["command"].(string)
	if command == "" {
		return tools.NewErrorResult(fmt.Errorf("command is required")), nil
	}

	cwd, _ := params["cwd"].(string)
	env := make(map[string]string)
	if envMap, ok := params["env"].(map[string]any); ok {
		for k, v := range envMap {
			if vs, ok := v.(string); ok {
				env[k] = vs
			}
		}
	}

	// Extract owner info from context (injected by SDK agent at tool execution time)
	owner := ownerFromContext(ctx)

	// Extract timeout
	timeoutSec := 0.0
	if ts, ok := params["timeout_seconds"].(float64); ok {
		timeoutSec = ts
	}
	timeout := time.Duration(timeoutSec * float64(time.Second))
	if timeout <= 0 {
		timeout = t.defaultTimeout
	}

	// See executeForegroundWithTimeout's identical comment: `timeout` here
	// bounds only the foreground wait/idle-background decision, not the
	// process's own lifetime (issues #282, #256). Leave SpawnRequest.Timeout
	// unset so the process runs to natural completion or explicit cancel.
	req := SpawnRequest{
		Command: command,
		WorkDir: cwd,
		Env:     env,
		Owner:   owner,
		Tags:    map[string]string{"source": "bash_tool", "mode": "streaming"},
	}

	// Spawn the process through the manager for proper handle tracking
	handle, err := t.manager.Spawn(ctx, req)
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to spawn process: %w", err)), nil
	}

	// Idle-timeout wait context (see executeForegroundWithTimeout for rationale).
	waitCtx, cancel := context.WithCancel(ctx)
	idleFired := new(atomicBool)
	idleStop := t.startIdleTimeoutWatcher(handle, timeout, idleFired, cancel)
	defer idleStop()

	// Track current execution for Ctrl+B backgrounding
	t.mu.Lock()
	t.currentHandle = handle
	t.currentCancel = cancel
	t.currentCommand = command
	select {
	case <-t.backgroundTrigger:
	default:
	}
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		t.currentHandle = ProcessHandle{}
		t.currentCancel = nil
		t.currentCommand = ""
		t.mu.Unlock()
		cancel()
	}()

	// Stream output from the manager's process to the onOutput callback
	var lastLine int
	streamTicker := time.NewTicker(50 * time.Millisecond)
	defer streamTicker.Stop()

	flushOutput := func() {
		exec, err := t.manager.Get(ctx, handle)
		if err != nil || exec == nil {
			return
		}
		lines, err := exec.Output().Lines(LineQueryOpts{FromLine: lastLine + 1})
		if err != nil || len(lines) == 0 {
			return
		}
		for _, line := range lines {
			if onOutput != nil {
				onOutput(line.Content+"\n", line.Stream)
			}
		}
		lastLine += len(lines)
	}

	// Wait for completion, timeout, or Ctrl+B
	resultCh := make(chan *ProcessResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := t.manager.Wait(waitCtx, handle)
		if err != nil {
			errCh <- err
		} else {
			resultCh <- result
		}
	}()

	var userTriggered bool
	for {
		select {
		case result := <-resultCh:
			// Command completed - flush remaining output
			flushOutput()
			return completedProcessResult(result), nil

		case err := <-errCh:
			flushOutput()
			if idleFired.Load() {
				t.manager.MarkBackgrounded(handle.ID())
				response := map[string]any{
					"backgrounded":    true,
					"task_id":         handle.ID(),
					"status":          "running",
					"message":         fmt.Sprintf("Command idle for %v (no output) and was auto-backgrounded. Use ReadBackgroundCommand to check status — look at metadata.seconds_since_last_output to decide if it is still healthy.", timeout),
					"timeout_seconds": timeout.Seconds(),
				}
				responseJSON, _ := json.Marshal(response)
				return backgroundedResult(string(responseJSON), handle.ID(), backgroundReasonIdle), nil
			}
			if waitCtx.Err() == context.Canceled {
				userTriggered = true
			} else {
				return tools.NewErrorResult(fmt.Errorf("command execution error: %w", err)), nil
			}

		case <-t.backgroundTrigger:
			userTriggered = true
			cancel()

		case <-streamTicker.C:
			// Periodically flush output to the streaming callback
			flushOutput()
			continue
		}

		if userTriggered {
			flushOutput()
			t.manager.MarkBackgrounded(handle.ID())
			response := map[string]any{
				"backgrounded":   true,
				"task_id":        handle.ID(),
				"status":         "running",
				"message":        "Command was backgrounded by user (Ctrl+B). Use ReadBackgroundCommand to check status.",
				"user_triggered": true,
			}
			responseJSON, _ := json.Marshal(response)
			return backgroundedResult(string(responseJSON), handle.ID(), backgroundReasonUser), nil
		}
	}
}

// Validate validates the parameters.
func (t *BackgroundBashTool) Validate(params map[string]any) error {
	if v, ok := t.baseTool.(tools.ValidatableTool); ok {
		return v.Validate(params)
	}
	return nil
}

// IsIdempotent returns false since bash commands have side effects.
func (t *BackgroundBashTool) IsIdempotent() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *BackgroundBashTool) RequiresPermission() []tools.Permission {
	if p, ok := t.baseTool.(tools.PermissionedTool); ok {
		return p.RequiresPermission()
	}
	return nil
}

// SupportedContentTypes returns supported content types.
func (t *BackgroundBashTool) SupportedContentTypes() []tools.ContentType {
	if c, ok := t.baseTool.(tools.ContentTypedTool); ok {
		return c.SupportedContentTypes()
	}
	return nil
}

// OptimizationHints returns optimization hints.
func (t *BackgroundBashTool) OptimizationHints() *tools.OptimizationHints {
	if h, ok := t.baseTool.(tools.HintedTool); ok {
		return h.OptimizationHints()
	}
	return nil
}

// ============================================================================
// Ctrl+B Mid-Execution Backgrounding API
// ============================================================================

// CurrentExecution represents info about the currently running bash command.
type CurrentExecution struct {
	Handle  ProcessHandle
	Command string
	Running bool
}

// GetCurrentExecution returns information about the currently executing bash command.
// Returns nil if no command is currently running in foreground.
func (t *BackgroundBashTool) GetCurrentExecution() *CurrentExecution {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.currentHandle.IsZero() {
		return nil
	}

	return &CurrentExecution{
		Handle:  t.currentHandle,
		Command: t.currentCommand,
		Running: true,
	}
}

// IsExecuting returns true if a bash command is currently executing in foreground.
func (t *BackgroundBashTool) IsExecuting() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return !t.currentHandle.IsZero()
}

// TriggerBackground sends a signal to background the currently executing command.
// Returns the ProcessHandle of the backgrounded command, or an error if no command is running.
func (t *BackgroundBashTool) TriggerBackground() (ProcessHandle, error) {
	t.mu.Lock()
	handle := t.currentHandle
	cancel := t.currentCancel
	t.mu.Unlock()

	if handle.IsZero() {
		return ProcessHandle{}, fmt.Errorf("no bash command is currently executing")
	}

	// Send signal to background (non-blocking due to buffered channel)
	select {
	case t.backgroundTrigger <- struct{}{}:
		// Signal sent successfully
	default:
		// Channel already has a signal, cancel directly
		if cancel != nil {
			cancel()
		}
	}

	return handle, nil
}

// GetManager returns the background process manager.
func (t *BackgroundBashTool) GetManager() *BackgroundProcessManager {
	return t.manager
}

// ownerFromContext extracts owner identity from the context.
// The SDK agent injects these via tools.WithOwnerInfo before tool execution.
// Falls back to a default "tui" user if no identity is found.
func ownerFromContext(ctx context.Context) OwnerInfo {
	owner := OwnerInfo{
		UserID:         tools.OwnerUserID(ctx),
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
	}
	// Ensure at least one identity is set (required by validation)
	if owner.UserID == "" && owner.AgentID == "" {
		owner.AgentID = "default"
	}
	return owner
}

// Ensure BackgroundBashTool implements both Tool and StreamingTool.
var _ tools.Tool = (*BackgroundBashTool)(nil)
var _ tools.StreamingTool = (*BackgroundBashTool)(nil)

// atomicBool is a tiny sync.atomic.Bool shim to avoid a hard dependency on
// Go 1.19+ sync/atomic Bool in case downstream forks target older toolchains.
// It serializes a single flag via atomic CompareAndSwap on an int32.
type atomicBool struct {
	v atomic.Int32
}

func (b *atomicBool) Load() bool { return b.v.Load() != 0 }
func (b *atomicBool) Set()       { b.v.Store(1) }

// startIdleTimeoutWatcher spawns a goroutine that polls exec.Output().Size()
// and invokes cancel() only when the output byte count has been unchanged for
// `timeout` seconds. idleFired is flipped BEFORE cancel so the caller can
// discriminate between idle-timeout cancellation and user-triggered
// cancellation (both present as context.Canceled on waitCtx).
//
// Returned stop function terminates the watcher; callers should defer it.
func (t *BackgroundBashTool) startIdleTimeoutWatcher(handle ProcessHandle, timeout time.Duration, idleFired *atomicBool, cancel context.CancelFunc) func() {
	if timeout <= 0 {
		// Negative or zero timeout is a misuse; degrade to "never idle-cancel".
		return func() {}
	}

	done := make(chan struct{})

	go func() {
		// Poll at 1Hz OR timeout/4, whichever is shorter. This gives a
		//	balance between responsiveness and overhead; for a 30s timeout
		//	we poll every 1s, for a 5s timeout every 1.25s.
		interval := time.Second
		if step := timeout / 4; step < interval && step > 0 {
			interval = step
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var lastSize int64 = -1
		lastChangeAt := time.Now()

		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				// Look up the executor each tick — the process may complete or
				//	be removed from the manager while we watch.
				info, err := t.manager.GetInfo(context.Background(), handle)
				if err != nil || info == nil || info.State.IsTerminal() {
					return
				}
				size := t.outputSizeFor(handle)
				if lastSize < 0 || size > lastSize {
					lastSize = size
					lastChangeAt = time.Now()
					continue
				}
				if time.Since(lastChangeAt) >= timeout {
					idleFired.Set()
					cancel()
					return
				}
			}
		}
	}()

	return func() {
		select {
		case <-done:
		default:
			close(done)
		}
	}
}

// outputSizeFor returns the current byte count of the process output buffer,
// or -1 when the process is not tracked (e.g. already reaped).
func (t *BackgroundBashTool) outputSizeFor(handle ProcessHandle) int64 {
	exec, err := t.manager.Get(context.Background(), handle)
	if err != nil || exec == nil || exec.Output() == nil {
		return -1
	}
	return exec.Output().Size()
}
