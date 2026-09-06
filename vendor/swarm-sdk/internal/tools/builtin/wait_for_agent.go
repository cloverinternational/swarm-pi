// Package builtin provides wait_for_agent tool for event-driven agent waiting.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// WaitForAgentParams are the typed parameters for the wait_for_agent tool.
type WaitForAgentParams struct {
	AgentID        string `json:"agent_id" description:"Agent ID to wait for completion." required:"true"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty" description:"Maximum time to wait in seconds (default: 600, 0 for no timeout)." default:"600"`
}

// WaitForAgentTool allows the main agent to wait for background agents to complete
// using event-driven notifications instead of polling.
type WaitForAgentTool struct {
	tools.BaseTool

	bgManager BackgroundAgentManager
	logger    observability.Logger
	tracer    observability.Tracer
}

// WaitForAgentConfig configures the wait_for_agent tool.
type WaitForAgentConfig struct {
	BGManager BackgroundAgentManager
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewWaitForAgentTool creates a new wait_for_agent tool.
func NewWaitForAgentTool(config WaitForAgentConfig) (tools.Tool, error) {
	if config.BGManager == nil {
		return nil, sdkerr.Permanent("wait_for_agent.missing_manager", "background manager is required")
	}
	if config.Logger == nil {
		config.Logger = noop.NewLogger()
	}
	if config.Tracer == nil {
		config.Tracer = noop.NewTracer()
	}

	t := &WaitForAgentTool{
		bgManager: config.BGManager,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}
	return tools.Typed[WaitForAgentParams](t), nil
}

// Name returns the tool name.
func (t *WaitForAgentTool) Name() string {
	return "wait_for_agent"
}

// Description returns the tool description.
func (t *WaitForAgentTool) Description() string {
	return "Wait for a background agent to complete using event-driven notifications. " +
		"Blocks execution until the agent finishes (completed, failed, or cancelled) or timeout occurs. " +
		"More efficient than polling - uses internal event channels for immediate completion detection."
}

// IsIdempotent returns false because waiting has temporal side effects.
func (t *WaitForAgentTool) IsIdempotent() bool {
	return false
}

// OptimizationHints provides guidance for efficient tool use.
func (t *WaitForAgentTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

// RequiresPermission returns the required permissions for this tool.
func (t *WaitForAgentTool) RequiresPermission() []tools.Permission {
	return nil
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *WaitForAgentTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns false because waiting blocks execution.
func (t *WaitForAgentTool) SupportsParallel() bool {
	return false
}

// Parameters returns the JSON schema for tool parameters.
func (t *WaitForAgentTool) Parameters() any {
	return tools.SchemaFor[WaitForAgentParams]()
}

// Execute executes the wait_for_agent tool by decoding rawParams into WaitForAgentParams and calling Run.
func (t *WaitForAgentTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, fmt.Errorf("wait_for_agent: marshal params: %w", err)
	}
	var p WaitForAgentParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("wait_for_agent: unmarshal params: %w", err)
	}
	return t.Run(ctx, p)
}

// Run executes the wait_for_agent tool with typed parameters.
func (t *WaitForAgentTool) Run(ctx context.Context, p WaitForAgentParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "wait_for_agent.execute")
	defer span.End()

	if p.AgentID == "" {
		err := sdkerr.Permanent("wait_for_agent.missing_agent_id", "agent_id parameter is required")
		span.RecordError(err)
		return nil, err
	}

	timeoutSeconds := 600
	if p.TimeoutSeconds != nil {
		if *p.TimeoutSeconds < 0 {
			err := sdkerr.Permanent("wait_for_agent.invalid_timeout", "timeout_seconds must be zero or greater")
			span.RecordError(err)
			return nil, err
		}
		timeoutSeconds = *p.TimeoutSeconds
	}

	span.SetAttribute("agent_id", p.AgentID)
	span.SetAttribute("timeout_seconds", timeoutSeconds)

	t.logger.Info(ctx, "wait_for_agent.starting",
		observability.F("agent_id", p.AgentID),
		observability.F("timeout_seconds", timeoutSeconds))

	// Get the background agent
	bgAgent, err := t.bgManager.Get(p.AgentID)
	if err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "wait_for_agent.agent_not_found",
			observability.F("agent_id", p.AgentID),
			observability.F("error", err.Error()))
		return nil, sdkerr.Permanent("wait_for_agent.agent_not_found",
			fmt.Sprintf("agent '%s' not found: %v", p.AgentID, err))
	}

	// Check the synchronized terminal signal for the fast path. Reading a
	// running result's mutable fields races with terminal state publication.
	select {
	case <-bgAgent.Done():
		result := bgAgent.Result()
		t.logger.Info(ctx, "wait_for_agent.already_complete",
			observability.F("agent_id", p.AgentID),
			observability.F("status", result.Status))
		return t.buildResult(result), nil
	default:
	}

	// This tool owns its documented timeout budget. Framework deadlines may
	// already be expired when a long wait starts and must not consume it.
	var timeout <-chan time.Time
	if timeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
		defer timer.Stop()
		timeout = timer.C
	}
	cancelled := explicitContextCancellation(ctx)

	// Done is the reliable lifecycle signal. Events is bounded observability
	// data and must not be drained for completion control.
	select {
	case <-bgAgent.Done():
		result := bgAgent.Result()
		t.logger.Info(ctx, "wait_for_agent.completed",
			observability.F("agent_id", p.AgentID),
			observability.F("status", result.Status),
			observability.F("duration", result.Duration))
		return t.buildResult(result), nil
	case <-timeout:
		t.logger.Warn(ctx, "wait_for_agent.timeout",
			observability.F("agent_id", p.AgentID),
			observability.F("timeout_seconds", timeoutSeconds))
		return t.buildWaitOutcome(bgAgent, "timeout", timeoutSeconds), nil
	case <-cancelled:
		return t.buildWaitOutcome(bgAgent, "cancelled", timeoutSeconds), nil
	}
}

// explicitContextCancellation reports explicit context cancellation while
// ignoring deadline expiry. Wait tools enforce their own user-visible budget.
func explicitContextCancellation(ctx context.Context) <-chan struct{} {
	cancelled := make(chan struct{})
	if ctx.Err() == context.Canceled {
		close(cancelled)
		return cancelled
	}
	if ctx.Done() == nil {
		return cancelled
	}
	go func() {
		<-ctx.Done()
		if ctx.Err() == context.Canceled {
			close(cancelled)
		}
	}()
	return cancelled
}

// isTerminalStatus returns true if the status indicates the agent has finished.
func isTerminalStatus(status agent.BackgroundAgentStatus) bool {
	switch status {
	case agent.StatusCompleted, agent.StatusFailed, agent.StatusCancelled:
		return true
	default:
		return false
	}
}

// buildResult formats the BackgroundAgentResult as a tool result.
func (t *WaitForAgentTool) buildResult(result *agent.BackgroundAgentResult) *tools.ToolResult {
	if result == nil {
		return &tools.ToolResult{Output: "Agent finished with no result."}
	}

	resultData := map[string]any{
		"agent_id": result.AgentID,
		"status":   string(result.Status),
		"duration": result.Duration.String(),
	}

	switch result.Status {
	case agent.StatusCompleted:
		resultData["result"] = result.Result
		resultData["tokens_used"] = result.TokensUsed
		resultData["cost_usd"] = result.CostUSD
		resultData["turns"] = result.TurnCount

	case agent.StatusFailed:
		if result.Error != nil {
			resultData["error"] = result.Error.Error()
		} else {
			resultData["error"] = "unknown error"
		}
		// Include partial result if available
		if result.Result != "" {
			resultData["partial_result"] = result.Result
		}

	case agent.StatusCancelled:
		resultData["message"] = "Agent was cancelled before completion."
		if result.Result != "" {
			resultData["partial_result"] = result.Result
		}
	}

	if result.OutputFile != "" {
		resultData["output_file"] = result.OutputFile
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}
}

func (t *WaitForAgentTool) buildWaitOutcome(bg *agent.BackgroundAgent, waitStatus string, timeoutSeconds int) *tools.ToolResult {
	state := map[string]any{
		"agent_id": bg.AgentID(),
		"status":   string(bg.Status()),
	}
	if result := bg.Result(); isTerminalStatus(bg.Status()) && result != nil {
		state["duration"] = result.Duration.String()
		if result.OutputFile != "" {
			state["output_file"] = result.OutputFile
		}
	}
	out := map[string]any{
		"wait_status":     waitStatus,
		"timeout_seconds": timeoutSeconds,
		"agent":           state,
		"message":         fmt.Sprintf("Wait %s; agent execution was not cancelled.", waitStatus),
	}
	resultJSON, _ := json.MarshalIndent(out, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}
}
