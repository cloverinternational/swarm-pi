// Package builtin provides multi_agent_wait tool for coordinating multiple background agents.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// MultiAgentWaitTool provides a specialized tool for waiting on multiple agents simultaneously.
// This is a convenience wrapper around check_background_agent's wait action with clearer
// semantics for multi-agent coordination patterns.

// MultiAgentWaitParams are the typed parameters for the multi_agent_wait tool.
type MultiAgentWaitParams struct {
	AgentIDs       []string `json:"agent_ids"                  description:"Array of agent IDs to wait for. All agents must complete before this tool returns."  required:"true"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty"  description:"Maximum time to wait in seconds (default: 600, 0 for no timeout)"                         default:"600"`
	PollIntervalMs int      `json:"poll_interval_ms,omitempty" description:"Deprecated and ignored: waiting is now event-driven via each agent's completion signal, not polling." default:"1000"`
}

type MultiAgentWaitTool struct {
	tools.BaseTool

	bgManager BackgroundAgentManager
	logger    observability.Logger
	tracer    observability.Tracer
}

// MultiAgentWaitConfig configures the multi_agent_wait tool.
type MultiAgentWaitConfig struct {
	BGManager BackgroundAgentManager
	Logger    observability.Logger
	Tracer    observability.Tracer
}

// NewMultiAgentWaitTool creates a new multi_agent_wait tool.
func NewMultiAgentWaitTool(config MultiAgentWaitConfig) (*MultiAgentWaitTool, error) {
	if config.BGManager == nil {
		return nil, sdkerr.Permanent("multi_agent_wait.missing_manager", "background manager is required")
	}
	if config.Logger == nil {
		return nil, sdkerr.Permanent("multi_agent_wait.missing_logger", "logger is required")
	}
	if config.Tracer == nil {
		return nil, sdkerr.Permanent("multi_agent_wait.missing_tracer", "tracer is required")
	}

	return &MultiAgentWaitTool{
		bgManager: config.BGManager,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}, nil
}

// Name returns the tool name.
func (t *MultiAgentWaitTool) Name() string {
	return "multi_agent_wait"
}

// Description returns the tool description.
func (t *MultiAgentWaitTool) Description() string {
	return `Wait for multiple background or task agents to complete before continuing. Blocks execution until all specified agents finish (completed, failed, or cancelled). 

This tool is designed for coordinating parallel agent workflows where the orchestrator needs to:
- Spawn multiple background agents for independent tasks
- Wait for all agents to complete before proceeding
- Collect results from all agents in one operation

Use this when you need to parallelize work across multiple agents and synchronize on their completion.`
}

// IsIdempotent returns false because waiting has temporal side effects.
func (t *MultiAgentWaitTool) IsIdempotent() bool {
	return false
}

// Validate checks if the given parameters are valid for this tool.
func (t *MultiAgentWaitTool) Validate(params map[string]any) error {
	// agent_ids is required
	agentIDsRaw, ok := params["agent_ids"]
	if !ok {
		return sdkerr.Permanent("multi_agent_wait.missing_agent_ids", "agent_ids parameter is required")
	}

	// Validate agent_ids is an array
	agentIDsArray, ok := agentIDsRaw.([]any)
	if !ok {
		return sdkerr.Permanent("multi_agent_wait.invalid_agent_ids", "agent_ids must be an array")
	}

	if len(agentIDsArray) == 0 {
		return sdkerr.Permanent("multi_agent_wait.empty_agent_ids", "agent_ids array cannot be empty")
	}

	// Validate all IDs are strings
	for i, idRaw := range agentIDsArray {
		if _, ok := idRaw.(string); !ok {
			return sdkerr.Permanent("multi_agent_wait.invalid_agent_id",
				fmt.Sprintf("agent_ids[%d] must be a string", i))
		}
	}

	return nil
}

// OptimizationHints provides guidance for efficient tool use.
func (t *MultiAgentWaitTool) OptimizationHints() *tools.OptimizationHints {
	return nil // Use defaults
}

// RequiresPermission returns the required permissions for this tool.
func (t *MultiAgentWaitTool) RequiresPermission() []tools.Permission {
	return nil // No special permissions required
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *MultiAgentWaitTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns false because waiting blocks execution.
// This tool is explicitly designed for synchronization and should not run in parallel.
func (t *MultiAgentWaitTool) SupportsParallel() bool {
	return false
}

// Parameters returns the JSON schema for tool parameters.
func (t *MultiAgentWaitTool) Parameters() any {
	return tools.SchemaFor[MultiAgentWaitParams]()
}

// Execute executes the multi_agent_wait tool by delegating to check_background_agent's wait action.
// Execute implements tools.Tool by decoding rawParams into MultiAgentWaitParams and calling Run.
func (t *MultiAgentWaitTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, sdkerr.Permanent("multi_agent_wait.param_encode", fmt.Sprintf("marshal params: %v", err))
	}
	var p MultiAgentWaitParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, sdkerr.Permanent("multi_agent_wait.param_decode", fmt.Sprintf("unmarshal params: %v", err))
	}
	return t.Run(ctx, p)
}

// Run executes the multi_agent_wait tool with fully typed parameters.
//
// It waits for every named agent to reach a terminal state (completed, failed,
// or cancelled) by selecting on each agent's guaranteed Done() channel. Done()
// is closed atomically in all three terminal handlers, so it cannot be missed
// even when the event buffer is full (unlike draining the Events() channel).
func (t *MultiAgentWaitTool) Run(ctx context.Context, p MultiAgentWaitParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "multi_agent_wait.execute")
	defer span.End()
	if len(p.AgentIDs) == 0 {
		err := sdkerr.Permanent("multi_agent_wait.empty_agent_ids", "agent_ids array cannot be empty")
		span.RecordError(err)
		return nil, err
	}
	timeoutSeconds := 600
	if p.TimeoutSeconds != nil {
		if *p.TimeoutSeconds < 0 {
			err := sdkerr.Permanent("multi_agent_wait.invalid_timeout", "timeout_seconds must be zero or greater")
			span.RecordError(err)
			return nil, err
		}
		timeoutSeconds = *p.TimeoutSeconds
	}
	span.SetAttribute("agent_count", len(p.AgentIDs))
	t.logger.Info(ctx, "multi_agent_wait.starting",
		observability.F("agent_ids", p.AgentIDs),
		observability.F("agent_count", len(p.AgentIDs)))

	// Resolve every agent up front so a bad ID fails fast with a clear error
	// rather than silently waiting forever on an agent that does not exist.
	bgAgents := make([]*agent.BackgroundAgent, 0, len(p.AgentIDs))
	for _, id := range p.AgentIDs {
		bg, err := t.bgManager.Get(id)
		if err != nil {
			e := sdkerr.Permanent("multi_agent_wait.agent_not_found",
				fmt.Sprintf("agent '%s' not found: %v", id, err))
			span.RecordError(e)
			return nil, e
		}
		bgAgents = append(bgAgents, bg)
	}

	// Shared deadline across all agents. 0 (or negative) means "no timeout".
	var timeoutCh <-chan time.Time
	if timeoutSeconds > 0 {
		timer := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	// Wait for each agent's terminal signal. Done() is guaranteed to close on
	// completion/failure/cancellation; if it is already closed, the receive
	// returns immediately (fast path for agents that finished before we got here).
	cancelled := explicitContextCancellation(ctx)
	for _, bg := range bgAgents {
		select {
		case <-bg.Done():
		case <-timeoutCh:
			t.logger.Warn(ctx, "multi_agent_wait.timeout",
				observability.F("agent_ids", p.AgentIDs),
				observability.F("completed", countTerminalAgents(bgAgents)))
			return t.buildAggregateResult(bgAgents, "timeout", timeoutSeconds), nil
		case <-cancelled:
			return t.buildAggregateResult(bgAgents, "cancelled", timeoutSeconds), nil
		}
	}

	t.logger.Info(ctx, "multi_agent_wait.completed",
		observability.F("agent_ids", p.AgentIDs),
		observability.F("agent_count", len(p.AgentIDs)))

	return t.buildAggregateResult(bgAgents, "completed", timeoutSeconds), nil
}

func countTerminalAgents(bgAgents []*agent.BackgroundAgent) int {
	count := 0
	for _, bg := range bgAgents {
		if isTerminalStatus(bg.Status()) {
			count++
		}
	}
	return count
}

func (t *MultiAgentWaitTool) buildAggregateResult(bgAgents []*agent.BackgroundAgent, waitStatus string, timeoutSeconds int) *tools.ToolResult {
	states := make([]map[string]any, 0, len(bgAgents))
	for _, bg := range bgAgents {
		states = append(states, t.summarize(bg))
	}
	completed := countTerminalAgents(bgAgents)
	out := map[string]any{
		"wait_status":      waitStatus,
		"timeout_seconds":  timeoutSeconds,
		"agent_count":      len(states),
		"completed_count":  completed,
		"agents":           states,
		"agents_cancelled": false,
		"message": fmt.Sprintf("Wait %s with %d of %d agent(s) in a terminal state; "+
			"the wait did not cancel agent execution.", waitStatus, completed, len(states)),
	}
	resultJSON, _ := json.MarshalIndent(out, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}
}

// summarize extracts a compact terminal-state summary for a single agent.
func (t *MultiAgentWaitTool) summarize(bg *agent.BackgroundAgent) map[string]any {
	status := bg.Status()
	if !isTerminalStatus(status) {
		return map[string]any{"agent_id": bg.AgentID(), "status": string(status)}
	}
	res := bg.Result()
	if res == nil {
		return map[string]any{"agent_id": bg.AgentID(), "status": string(status)}
	}
	summary := map[string]any{
		"agent_id": res.AgentID,
		"status":   string(res.Status),
		"duration": res.Duration.String(),
	}
	switch res.Status {
	case agent.StatusCompleted:
		summary["tokens_used"] = res.TokensUsed
		summary["cost_usd"] = res.CostUSD
		summary["turns"] = res.TurnCount
	case agent.StatusFailed:
		if res.Error != nil {
			summary["error"] = res.Error.Error()
		}
	}
	if res.OutputFile != "" {
		summary["output_file"] = res.OutputFile
	}
	return summary
}
