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

// maxTaskOutputBytes is the maximum bytes returned in a single TaskOutput call.
// Mirrors Claude Code's 8 MB default (TASK_MAX_OUTPUT_LENGTH).
const maxTaskOutputBytes = 8 * 1024 * 1024

// SubagentOutputParams are the typed parameters for the SubagentOutput tool.
// (Same shape as CheckBgAgentParams — reuses the same LLM-facing schema.)
type SubagentOutputParams = CheckBgAgentParams

// SubagentOutputTool allows the main agent to check status of background agents.
type SubagentOutputTool struct {
	tools.BaseTool

	bgManager BackgroundAgentManager
	logger    observability.Logger
	tracer    observability.Tracer
}

// SubagentOutputConfig configures the check_background_agent tool.
type SubagentOutputConfig struct {
	BGManager BackgroundAgentManager
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewSubagentOutputTool creates a new SubagentOutput tool wrapped as a typed Tool.
// Logger and Tracer default to noops when nil.
func NewSubagentOutputTool(config SubagentOutputConfig) (tools.Tool, error) {
	if config.BGManager == nil {
		return nil, sdkerr.Permanent("check_background_agent.missing_manager", "background manager is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &SubagentOutputTool{
		bgManager: config.BGManager,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[SubagentOutputParams](t), nil
}

// Name returns the tool name.
func (t *SubagentOutputTool) Name() string {
	return "SubagentOutput"
}

// Description returns the tool description.
func (t *SubagentOutputTool) Description() string {
	return "Check the status or retrieve output of background agents. " +
		"Actions: 'status' (default) — current status; " +
		"'result' — full output with optional byte offset for incremental reads (output_file is read directly when available); " +
		"'cancel' — stop a running agent. " +
		"Pass offset from a previous result call to read only new content. " +
		"Do NOT poll in a loop to watch progress — you are notified automatically when a background agent completes. " +
		"Call this once after that notification to collect the result, or only when the user explicitly asks for a progress check."
}

// IsIdempotent returns false (cancel changes state).
func (t *SubagentOutputTool) IsIdempotent() bool {
	return false
}

// OptimizationHints provides guidance for efficient tool use.
func (t *SubagentOutputTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *SubagentOutputTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *SubagentOutputTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true — status/result reads are safe to execute concurrently.
func (t *SubagentOutputTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
func (t *SubagentOutputTool) Parameters() any {
	return tools.SchemaFor[SubagentOutputParams]()
}

// Run executes the SubagentOutput tool with typed parameters.
func (t *SubagentOutputTool) Run(ctx context.Context, params SubagentOutputParams) (*tools.ToolResult, error) {
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
func (t *SubagentOutputTool) getStatus(ctx context.Context, agentID string) (*tools.ToolResult, error) {
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
func (t *SubagentOutputTool) cancelAgent(ctx context.Context, agentID string) (*tools.ToolResult, error) {
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
func (t *SubagentOutputTool) getResult(ctx context.Context, agentID string, offset int64) (*tools.ToolResult, error) {
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
		// readErr != nil (or the store reported zero bytes) does NOT mean the
		// agent produced nothing: issue #284 was an agent that wrote 172KB of
		// real NDJSON output and then failed during finalization, after which
		// ReadOutputStore's structured read tripped some transient error (a
		// concurrent Close/rename, a stat race, etc.) and this function fell
		// straight through to buildMemoryResult, which for StatusFailed prints
		// ONLY the error line -- discarding every byte the agent actually
		// produced, with no pointer to recover it. Before giving up, attempt a
		// raw best-effort read of the same file directly: this bypasses
		// ReadOutputStore's stricter open/seek/ReadFull error handling and, if
		// it succeeds, still yields the real NDJSON bytes to parse into a
		// transcript. It only needs to beat "nothing, silently".
		if readErr != nil {
			if raw, rawErr := agent.ReadOutputStoreBestEffort(agentID); rawErr == nil && len(raw) > 0 {
				fallback := &agent.ReadOutputStoreResult{
					Content:    raw,
					BytesRead:  int64(len(raw)),
					TotalBytes: int64(len(raw)),
					NewOffset:  int64(len(raw)),
				}
				return t.buildFileResult(result, fallback, 0)
			}
		}
	}

	// Fallback: in-memory result (for agents without an output file, or file not yet created)
	return t.buildMemoryResult(result)
}

// buildFileResult formats a result sourced from the output file.
func (t *SubagentOutputTool) buildFileResult(result *agent.BackgroundAgentResult, read *agent.ReadOutputStoreResult, requestedOffset int64) (*tools.ToolResult, error) {
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
		// The output file is NDJSON (one record per streaming chunk). Returning it
		// verbatim floods the parent with hundreds of KB of {"type":"content",...}
		// noise. Parse and reconstruct a readable view instead. The byte offsets
		// above (new_offset/total_bytes) remain valid for incremental reads because
		// they index the raw file, not this rendered view.
		records := agent.ParseOutputStoreRecords(read.Content)
		// For a full read from the start (offset 0), prefer the agent's FINAL
		// message only (Claude Code parity) — the parent wants the answer, not the
		// play-by-play. For incremental reads (offset > 0, typically a running
		// agent), a partial chunk rarely contains the final record, so show the
		// coalesced transcript of whatever new content arrived.
		var view string
		if requestedOffset == 0 {
			view = agent.FormatFinalOrTranscript(records, false)
		} else {
			view = agent.FormatRecordsAsTranscript(records, false)
		}
		if view != "" {
			out.WriteString(view)
		} else {
			// Content wasn't parseable NDJSON (e.g. a partial line at an offset split).
			// Fall back to raw content so nothing is lost.
			out.WriteString(read.Content)
		}
	}

	return &tools.ToolResult{Output: out.String()}, nil
}

// buildMemoryResult formats a result from the in-memory BackgroundAgentResult.
func (t *SubagentOutputTool) buildMemoryResult(result *agent.BackgroundAgentResult) (*tools.ToolResult, error) {
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
		// Even when neither the structured nor the best-effort file read in
		// getResult produced usable content, still point at the output file
		// when one is known (issue #284's "near-miss" boundary: an agent that
		// truly failed before producing any substantive output may return
		// only its failure marker and error metadata -- but if the file
		// exists, naming it here keeps this a readable pointer rather than a
		// dead end).
		if result.OutputFile != "" {
			out.WriteString(fmt.Sprintf("output_file: %s (raw output may still be recoverable from this path)\n", result.OutputFile))
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
