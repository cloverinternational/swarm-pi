// Package builtin provides check_background_agent tool for monitoring background tasks.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// CheckBgAgentParams are the typed parameters for the check_background_agent tool.
type CheckBgAgentParams struct {
	AgentID string `json:"agent_id" description:"Agent ID to check. Omit with action='status' to list all agents."`
	Action  string `json:"action"   description:"Action to perform: status (default), result, cancel" enum:"status,result,cancel"`
	Offset  int64  `json:"offset"   description:"Byte offset for incremental reads with action='result'. Use new_offset from a prior call."`
}

// CheckBackgroundAgentTool allows the main agent to check status of background agents.
type CheckBackgroundAgentTool struct {
	tools.BaseTool

	bgManager BackgroundAgentManager
	logger    observability.Logger
	tracer    observability.Tracer
}

// CheckBackgroundAgentConfig configures the check_background_agent tool.
type CheckBackgroundAgentConfig struct {
	BGManager BackgroundAgentManager
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewCheckBackgroundAgentTool creates a new check_background_agent tool wrapped
// as a typed Tool. Logger and Tracer default to noops when nil.
func NewCheckBackgroundAgentTool(config CheckBackgroundAgentConfig) (tools.Tool, error) {
	if config.BGManager == nil {
		return nil, sdkerr.Permanent("check_background_agent.missing_manager", "background manager is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &CheckBackgroundAgentTool{
		bgManager: config.BGManager,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[CheckBgAgentParams](t), nil
}

// Name returns the tool name.
func (t *CheckBackgroundAgentTool) Name() string {
	return "TaskOutput"
}

// Description returns the tool description.
func (t *CheckBackgroundAgentTool) Description() string {
	return "Check the status or retrieve output of background agents. " +
		"Actions: 'status' (default) — current status; " +
		"'result' — full output with optional byte offset for incremental reads (output_file is read directly when available); " +
		"'cancel' — stop a running agent. " +
		"Pass offset from a previous result call to read only new content."
}

// IsIdempotent returns false (cancel changes state).
func (t *CheckBackgroundAgentTool) IsIdempotent() bool {
	return false
}

// OptimizationHints provides guidance for efficient tool use.
func (t *CheckBackgroundAgentTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *CheckBackgroundAgentTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *CheckBackgroundAgentTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true — status/result reads are safe to execute concurrently.
func (t *CheckBackgroundAgentTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
func (t *CheckBackgroundAgentTool) Parameters() any {
	return tools.SchemaFor[CheckBgAgentParams]()
}

// Run executes the check_background_agent tool with typed parameters.
func (t *CheckBackgroundAgentTool) Run(ctx context.Context, params CheckBgAgentParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "check_background_agent.execute")
	defer span.End()

	action := params.Action
	if action == "" {
		action = "status"
	}

	span.SetAttribute("agent_id", params.AgentID)
	span.SetAttribute("action", action)
	span.SetAttribute("offset", params.Offset)

	t.logger.Info(ctx, "check_background_agent.executing",
		observability.F("agent_id", params.AgentID),
		observability.F("action", action),
		observability.F("offset", params.Offset))

	switch action {
	case "status":
		return t.getStatus(ctx, params.AgentID)
	case "cancel":
		return t.cancelAgent(ctx, params.AgentID)
	case "result":
		return t.getResult(ctx, params.AgentID, params.Offset)
	default:
		err := sdkerr.Permanent("check_background_agent.invalid_action",
			fmt.Sprintf("invalid action: %s (valid: status, result, cancel)", action))
		span.RecordError(err)
		return nil, err
	}
}

// getStatus returns status information for agent(s).
func (t *CheckBackgroundAgentTool) getStatus(ctx context.Context, agentID string) (*tools.ToolResult, error) {
	if agentID != "" {
		bgAgent, err := t.bgManager.Get(agentID)
		if err != nil {
			return &tools.ToolResult{Output: fmt.Sprintf("Agent not found: %s", agentID)}, nil
		}

		result := bgAgent.Result()
		statusData := map[string]any{
			"agent_id":    agentID,
			"status":      string(result.Status),
			"start_time":  result.StartTime,
			"duration":    result.Duration.String(),
			"output_file": result.OutputFile,
		}

		switch result.Status {
		case "running":
			statusData["message"] = fmt.Sprintf("Agent is running (elapsed: %s). Call action='result' to read partial output.", time.Since(result.StartTime).Round(time.Second))
		case "completed":
			statusData["message"] = "Agent completed. Call action='result' to retrieve output."
			statusData["tokens_used"] = result.TokensUsed
			statusData["cost_usd"] = result.CostUSD
		case "failed":
			statusData["message"] = "Agent failed. Call action='result' to retrieve partial output."
			if result.Error != nil {
				statusData["error"] = result.Error.Error()
			}
		case "cancelled":
			statusData["message"] = "Agent was cancelled. Call action='result' to retrieve partial output."
		}

		resultJSON, _ := json.MarshalIndent(statusData, "", "  ")
		return &tools.ToolResult{Output: string(resultJSON)}, nil
	}

	// List all agents
	allAgents := t.bgManager.List()
	if len(allAgents) == 0 {
		return &tools.ToolResult{Output: "No background agents currently tracked."}, nil
	}

	agentsList := make([]map[string]any, 0, len(allAgents))
	for _, info := range allAgents {
		agentData := map[string]any{
			"agent_id": info.AgentID,
			"task":     info.Task,
			"status":   string(info.Status),
			"duration": info.Duration.String(),
		}
		if info.Result != nil && info.Result.OutputFile != "" {
			agentData["output_file"] = info.Result.OutputFile
		}
		if info.Result != nil {
			agentData["tokens_used"] = info.Result.TokensUsed
			agentData["cost_usd"] = info.Result.CostUSD
		}
		agentsList = append(agentsList, agentData)
	}

	resultData := map[string]any{
		"total_agents": len(allAgents),
		"agents":       agentsList,
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}, nil
}

// cancelAgent cancels a running background agent.
func (t *CheckBackgroundAgentTool) cancelAgent(ctx context.Context, agentID string) (*tools.ToolResult, error) {
	if agentID == "" {
		return &tools.ToolResult{Output: "Error: agent_id is required for cancel action"}, nil
	}

	err := t.bgManager.Cancel(agentID)
	if err != nil {
		t.logger.Warn(ctx, "check_background_agent.cancel_failed",
			observability.F("agent_id", agentID),
			observability.F("error", err.Error()))
		return &tools.ToolResult{Output: fmt.Sprintf("Failed to cancel agent '%s': %v", agentID, err)}, nil
	}

	t.logger.Info(ctx, "check_background_agent.cancelled",
		observability.F("agent_id", agentID))

	resultData := map[string]any{
		"agent_id": agentID,
		"message":  "Agent cancellation requested. Partial output remains readable via action='result'.",
		"status":   "cancelled",
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}, nil
}

// getResult retrieves output for a background agent using the persisted output file when
// available, falling back to the in-memory result string for completed agents.
//
// offset is a byte offset into the output file. Pass 0 to read from the start,
// or the new_offset from a previous call to read only new content. This enables
// incremental reads while the agent is still running.
func (t *CheckBackgroundAgentTool) getResult(ctx context.Context, agentID string, offset int64) (*tools.ToolResult, error) {
	if agentID == "" {
		return &tools.ToolResult{Output: "Error: agent_id is required for result action"}, nil
	}

	bgAgent, err := t.bgManager.Get(agentID)
	if err != nil {
		return &tools.ToolResult{Output: fmt.Sprintf("Agent not found: %s", agentID)}, nil
	}

	result := bgAgent.Result()

	// Prefer file-based reading — works while running AND after completion,
	// supports offsets, and is bounded by maxTaskOutputBytes.
	if result.OutputFile != "" || result.AgentID != "" {
		// Try file-based read (works for both running and finished agents)
		readResult, readErr := agent.ReadOutputStore(agentID, offset, maxTaskOutputBytes)
		if readErr == nil && (readResult.BytesRead > 0 || readResult.TotalBytes > 0) {
			return t.buildFileResult(result, readResult, offset)
		}
	}

	// Fallback: in-memory result (for agents without an output file, or file not yet created)
	return t.buildMemoryResult(result)
}

// buildFileResult formats a result sourced from the output file.
func (t *CheckBackgroundAgentTool) buildFileResult(result *agent.BackgroundAgentResult, read *agent.ReadOutputStoreResult, requestedOffset int64) (*tools.ToolResult, error) {
	var out strings.Builder

	out.WriteString(fmt.Sprintf("agent_id:    %s\n", result.AgentID))
	out.WriteString(fmt.Sprintf("status:      %s\n", result.Status))
	out.WriteString(fmt.Sprintf("output_file: %s\n", result.OutputFile))
	out.WriteString(fmt.Sprintf("offset:      %d\n", requestedOffset))
	out.WriteString(fmt.Sprintf("new_offset:  %d\n", read.NewOffset))
	out.WriteString(fmt.Sprintf("total_bytes: %d\n", read.TotalBytes))
	if result.Status == agent.StatusCompleted || result.Status == agent.StatusFailed {
		out.WriteString(fmt.Sprintf("duration:    %s\n", result.Duration))
		if result.TokensUsed > 0 {
			out.WriteString(fmt.Sprintf("tokens_used: %d\n", result.TokensUsed))
			out.WriteString(fmt.Sprintf("cost_usd:    $%.4f\n", result.CostUSD))
		}
		if result.Status == agent.StatusFailed && result.Error != nil {
			out.WriteString(fmt.Sprintf("error:       %s\n", result.Error.Error()))
		}
	} else {
		out.WriteString(fmt.Sprintf("elapsed:     %s\n", time.Since(result.StartTime).Round(time.Second)))
	}
	out.WriteString("───────────────────────────────────────────────────────────────\n")

	// Truncation indicator (Claude Code pattern)
	if read.Truncated {
		omittedBytes := read.TotalBytes - (requestedOffset + read.BytesRead)
		if omittedBytes < 0 {
			omittedBytes = 0
		}
		out.WriteString(fmt.Sprintf("[%dKB of earlier output omitted — use offset=%d to read more]\n\n",
			omittedBytes/1024, read.NewOffset))
	}

	if read.BytesRead == 0 {
		if result.Status == agent.StatusRunning {
			out.WriteString("(agent is running — no output yet at this offset)\n")
		} else {
			out.WriteString("(no output at this offset)\n")
		}
	} else {
		// Reconstruct a readable view from the NDJSON output file rather than
		// dumping raw {"type":...} records (which balloon under token streaming).
		// On a full read (offset 0) prefer the agent's FINAL message only; on
		// incremental reads show the coalesced transcript of new content.
		records := agent.ParseOutputStoreRecords(read.Content)
		var view string
		if requestedOffset == 0 {
			view = agent.FormatFinalOrTranscript(records, false)
		} else {
			view = agent.FormatRecordsAsTranscript(records, false)
		}
		if view != "" {
			out.WriteString(view)
		} else {
			out.WriteString(read.Content)
		}
	}

	return &tools.ToolResult{Output: out.String()}, nil
}

// buildMemoryResult formats a result from the in-memory BackgroundAgentResult.
func (t *CheckBackgroundAgentTool) buildMemoryResult(result *agent.BackgroundAgentResult) (*tools.ToolResult, error) {
	var out strings.Builder

	out.WriteString(fmt.Sprintf("agent_id: %s\n", result.AgentID))
	out.WriteString(fmt.Sprintf("status:   %s\n", result.Status))

	switch result.Status {
	case agent.StatusCompleted:
		out.WriteString(fmt.Sprintf("duration:    %s\n", result.Duration))
		out.WriteString(fmt.Sprintf("turns:       %d\n", result.TurnCount))
		out.WriteString(fmt.Sprintf("tokens_used: %d\n", result.TokensUsed))
		out.WriteString(fmt.Sprintf("cost_usd:    $%.4f\n", result.CostUSD))
		out.WriteString("───────────────────────────────────────────────────────────────\n")
		agentResult := result.Result
		if agentResult == "" {
			agentResult = "(no output produced)"
		}
		out.WriteString(agentResult)

	case agent.StatusFailed:
		out.WriteString(fmt.Sprintf("duration: %s\n", result.Duration))
		out.WriteString("───────────────────────────────────────────────────────────────\n")
		if result.Error != nil {
			out.WriteString(fmt.Sprintf("error: %v\n", result.Error))
		} else {
			out.WriteString("error: unknown\n")
		}

	case agent.StatusRunning:
		out.WriteString(fmt.Sprintf("elapsed: %s\n", time.Since(result.StartTime).Round(time.Second)))
		out.WriteString("Agent is still running. Check back later or use action='status' to monitor.")

	case agent.StatusCancelled:
		out.WriteString(fmt.Sprintf("duration: %s\n", result.Duration))
		out.WriteString("Agent was cancelled before completion.")
	}

	return &tools.ToolResult{Output: out.String()}, nil
}
