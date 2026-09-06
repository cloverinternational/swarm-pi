package bgprocess

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ReadBackgroundCommandTool allows agents to read output from background bash commands.
type ReadBackgroundCommandTool struct {
	manager *BackgroundProcessManager
}

// NewReadBackgroundCommandTool creates a new ReadBackgroundCommandTool.
func NewReadBackgroundCommandTool(manager *BackgroundProcessManager) *ReadBackgroundCommandTool {
	return &ReadBackgroundCommandTool{
		manager: manager,
	}
}

// Name returns the tool name.
func (t *ReadBackgroundCommandTool) Name() string {
	return "ReadBackgroundCommand"
}

// Description returns the tool description.
func (t *ReadBackgroundCommandTool) Description() string {
	return `Reads output and status from a background bash command.

Use this tool to:
- Check the status of a background process (running, completed, failed, cancelled)
- Read the stdout/stderr output from the process
- Get the exit code when the process has completed
- Query specific lines or search output with a pattern

The output is returned as structured JSON with metadata including timestamps,
line numbers, and stream type (stdout/stderr) for each line.

Heartbeat fields on metadata tell you whether a long-running process is still
making progress without needing to diff polls yourself:
- "last_output_at": RFC3339 timestamp of the most recent line written.
- "seconds_since_last_output": how long ago that was (0 if never output).
- "bytes_written": cumulative output size in bytes (strictly non-decreasing).
A process that is "running" but has a high seconds_since_last_output may still
be healthy (network I/O, LLM calls) — prefer another poll over a cancel unless
the gap exceeds several minutes OR bytes_written has not changed between polls.

IMPORTANT: By default only the last 100 lines (tail) are returned in the "output"
array to keep responses concise. When the output is truncated, "metadata.output_file"
contains the path to a temp file with the complete output — read that file with the
Read or Bash tool if you need earlier lines.

Parameters:
- task_id (required): The ID of the background process to read from
- action: "output" (default), "status", "cancel", or "list"
- from_line: Start reading from this line number (1-indexed, default: 1)
- max_lines: Maximum number of lines to return (default: 100, capped at 1000)
- pattern: Regex pattern to filter output lines (optional)
- stream: Filter by stream type - "stdout", "stderr", or empty for all (optional)`
}

// Parameters returns the JSON schema for tool parameters.
func (t *ReadBackgroundCommandTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task_id": map[string]any{
				"type":        "string",
				"description": "The ID of the background process (returned when command was backgrounded)",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"status", "output", "cancel", "list"},
				"default":     "output",
				"description": "Action to perform: output (get output lines, default), status (get process state only), cancel (terminate process), list (list all background processes)",
			},
			"from_line": map[string]any{
				"type":        "integer",
				"default":     1,
				"description": "Start reading from this line number (1-indexed)",
			},
			"max_lines": map[string]any{
				"type":        "integer",
				"default":     100,
				"description": "Maximum number of lines to return (default: 100 tail lines; capped at 1000). When truncated, see metadata.output_file for the full output.",
			},
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to filter output lines",
			},
			"stream": map[string]any{
				"type":        "string",
				"enum":        []string{"stdout", "stderr", ""},
				"description": "Filter by stream type (stdout, stderr, or empty for all)",
			},
		},
		"required": []string{},
	}
}

// Execute runs the tool.
func (t *ReadBackgroundCommandTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Extract parameters
	action := "output"
	if a, ok := params["action"].(string); ok && a != "" {
		action = a
	}

	taskID, _ := params["task_id"].(string)

	// For list action, task_id is not required
	if action == "list" {
		return t.executeList(ctx)
	}

	// For other actions, task_id is required
	if taskID == "" {
		return tools.NewErrorResult(fmt.Errorf("task_id is required for action %q", action)), nil
	}

	switch action {
	case "status":
		return t.executeStatus(ctx, taskID)
	case "output":
		return t.executeOutput(ctx, taskID, params)
	case "cancel":
		return t.executeCancel(ctx, taskID)
	default:
		return tools.NewErrorResult(fmt.Errorf("unknown action: %s", action)), nil
	}
}

// executeStatus returns the status of a background process.
func (t *ReadBackgroundCommandTool) executeStatus(ctx context.Context, taskID string) (*tools.ToolResult, error) {
	subject := t.extractSubject(ctx)

	output, err := t.manager.FormatOutputByID(ctx, taskID, subject, LineQueryOpts{
		MaxLines: 0, // No output lines for status
	})
	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	// Remove output lines for status-only response
	output.Output = nil

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to marshal status: %w", err)), nil
	}

	return tools.NewToolResult(string(jsonBytes)), nil
}

// executeOutput returns the output of a background process.
func (t *ReadBackgroundCommandTool) executeOutput(ctx context.Context, taskID string, params map[string]any) (*tools.ToolResult, error) {
	subject := t.extractSubject(ctx)

	const defaultTail = 100
	const maxAllowed = 1000

	// Determine the window size. Default is the last 100 lines (tail).
	// The caller may override max_lines (capped at 1000).
	maxLines := defaultTail
	if ml, ok := params["max_lines"].(float64); ok && int(ml) > 0 {
		maxLines = min(int(ml), maxAllowed)
	}

	// If the caller specified from_line explicitly, honour it; otherwise we
	// compute fromLine so that we get the last maxLines lines (tail behaviour).
	fromLine := 0 // 0 = let FormatOutput/Lines decide after we know total
	callerSpecifiedFrom := false
	if fl, ok := params["from_line"].(float64); ok && int(fl) >= 1 {
		fromLine = int(fl)
		callerSpecifiedFrom = true
	}

	// First get total line count so we can compute the tail start.
	if !callerSpecifiedFrom {
		totalLines, err := t.manager.getTotalLines(ctx, taskID, subject)
		if err == nil && totalLines > maxLines {
			fromLine = totalLines - maxLines + 1 // 1-indexed start of tail
		} else {
			fromLine = 1
		}
	}

	opts := LineQueryOpts{
		FromLine: fromLine,
		MaxLines: maxLines,
	}
	if pattern, ok := params["pattern"].(string); ok {
		opts.Pattern = pattern
	}
	if stream, ok := params["stream"].(string); ok {
		opts.Stream = stream
	}

	output, err := t.manager.FormatOutputByID(ctx, taskID, subject, opts)
	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	jsonBytes, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to marshal output: %w", err)), nil
	}

	return tools.NewToolResult(string(jsonBytes)), nil
}

// executeCancel cancels a background process.
func (t *ReadBackgroundCommandTool) executeCancel(ctx context.Context, taskID string) (*tools.ToolResult, error) {
	subject := t.extractSubject(ctx)

	err := t.manager.CancelByID(ctx, taskID, subject)
	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	return tools.NewToolResult(fmt.Sprintf("Process %s has been cancelled", taskID)), nil
}

// executeList lists all background processes visible to the subject.
func (t *ReadBackgroundCommandTool) executeList(ctx context.Context) (*tools.ToolResult, error) {
	subject := t.extractSubject(ctx)

	processes, err := t.manager.List(ctx, subject)
	if err != nil {
		return tools.NewErrorResult(err), nil
	}

	// Build summary list
	type ProcessSummary struct {
		TaskID    string `json:"task_id"`
		Command   string `json:"command"`
		Status    string `json:"status"`
		StartedAt string `json:"started_at"`
		Duration  string `json:"duration"`
		ExitCode  *int   `json:"exit_code,omitempty"`
	}

	summaries := make([]ProcessSummary, 0, len(processes))
	for _, p := range processes {
		cmd := p.Command
		if len(cmd) > 50 {
			cmd = cmd[:50] + "..."
		}

		summaries = append(summaries, ProcessSummary{
			TaskID:    p.Handle.ID(),
			Command:   cmd,
			Status:    string(p.State),
			StartedAt: p.StartedAt.Format("2006-01-02 15:04:05"),
			Duration:  p.Duration.Round(1e9).String(), // Round to seconds
			ExitCode:  p.ExitCode,
		})
	}

	result := map[string]any{
		"count":     len(summaries),
		"processes": summaries,
	}

	jsonBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to marshal list: %w", err)), nil
	}

	return tools.NewToolResult(string(jsonBytes)), nil
}

// extractSubject extracts the owner info from context for ABAC checks.
func (t *ReadBackgroundCommandTool) extractSubject(ctx context.Context) OwnerInfo {
	// Use the same SDK context extraction as the bash tool for consistency.
	subject := OwnerInfo{
		UserID:         tools.OwnerUserID(ctx),
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
	}

	// Also check raw context keys as fallback (some callers may set these directly)
	if subject.UserID == "" {
		if uid, ok := ctx.Value("user_id").(string); ok {
			subject.UserID = uid
		}
	}
	if role, ok := ctx.Value("role").(string); ok {
		subject.Role = role
	}

	// The primary agent executing this tool is the admin — it should be able
	// to read all background processes it (or its sub-agents) spawned.
	// Context values are rarely populated in the TUI execution path, so
	// default to admin when no identity is available.
	if subject.UserID == "" && subject.AgentID == "" && subject.Role == "" {
		subject.Role = "admin"
	}

	return subject
}

// Validate validates the parameters.
func (t *ReadBackgroundCommandTool) Validate(params map[string]any) error {
	action := "output"
	if a, ok := params["action"].(string); ok && a != "" {
		action = a
	}

	// Validate action
	validActions := map[string]bool{
		"status": true,
		"output": true,
		"cancel": true,
		"list":   true,
	}
	if !validActions[action] {
		return fmt.Errorf("invalid action: %s (must be status, output, cancel, or list)", action)
	}

	// For non-list actions, task_id is required
	if action != "list" {
		if taskID, ok := params["task_id"].(string); !ok || taskID == "" {
			return fmt.Errorf("task_id is required for action %q", action)
		}
	}

	return nil
}

// IsIdempotent returns false since status can change.
func (t *ReadBackgroundCommandTool) IsIdempotent() bool {
	return false
}

// RequiresPermission returns the required permissions.
func (t *ReadBackgroundCommandTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionBashExecute}
}

// SupportedContentTypes returns supported content types.
func (t *ReadBackgroundCommandTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints returns optimization hints.
func (t *ReadBackgroundCommandTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// Ensure ReadBackgroundCommandTool implements tools.Tool.
var _ tools.Tool = (*ReadBackgroundCommandTool)(nil)
