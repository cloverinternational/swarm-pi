// Package mode provides multi-agent orchestration through mode definitions.
package mode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// generateInvocationID creates a unique ID for this specific agent invocation.
// This is purely internal and cannot be set by external code.
func generateInvocationID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is extremely rare; fall back to a time-based ID
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// GroupCoordinator manages the execution of an agent group
type GroupCoordinator struct {
	group                *AgentGroup
	logger               observability.Logger
	tracer               observability.Tracer
	intermediateCallback agent.IntermediateCallback
	eventCallback        WorkflowEventCallback // Optional callback for lifecycle events
}

// GroupResult contains the results from a group execution
type GroupResult struct {
	GroupID   string                  `json:"group_id"`
	GroupName string                  `json:"group_name"`
	Status    GroupStatus             `json:"status"`
	Results   map[string]*AgentResult `json:"results"` // agent_id -> result
	Consensus *ConsensusResult        `json:"consensus,omitempty"`
	Output    string                  `json:"output,omitempty"` // Synthesis of results or output from coordinator
	StartTime time.Time               `json:"start_time"`
	EndTime   time.Time               `json:"end_time"`
	Duration  time.Duration           `json:"duration"`
	Error     error                   `json:"error,omitempty"`
}

// AgentResult contains the result from a single agent execution
type AgentResult struct {
	AgentID   string        `json:"agent_id"`
	AgentName string        `json:"agent_name"`
	Output    string        `json:"output"`
	Error     error         `json:"error,omitempty"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Duration  time.Duration `json:"duration"`
	Tokens    int           `json:"tokens,omitempty"`
	Cost      float64       `json:"cost,omitempty"`
}

// ConsensusResult contains the consensus analysis of agent outputs
type ConsensusResult struct {
	Reached    bool            `json:"reached"`
	Confidence float64         `json:"confidence"` // 0.0 to 1.0
	Summary    string          `json:"summary"`
	Agreements []string        `json:"agreements"`
	Conflicts  []string        `json:"conflicts"`
	Method     ConsensusMethod `json:"method"`
}

// ConsensusMethod defines how consensus is determined
type ConsensusMethod string

const (
	ConsensusMethodVoting      ConsensusMethod = "voting"
	ConsensusMethodSynthesis   ConsensusMethod = "synthesis"
	ConsensusMethodFirstWins   ConsensusMethod = "first_wins"
	ConsensusMethodBestOfN     ConsensusMethod = "best_of_n"
	ConsensusMethodAdversarial ConsensusMethod = "adversarial"
)

// GroupStatus represents the execution status of a group
type GroupStatus string

const (
	GroupStatusPending   GroupStatus = "pending"
	GroupStatusRunning   GroupStatus = "running"
	GroupStatusCompleted GroupStatus = "completed"
	GroupStatusFailed    GroupStatus = "failed"
	GroupStatusCancelled GroupStatus = "cancelled"
	GroupStatusTimeout   GroupStatus = "timeout"
)

// NewGroupCoordinator creates a new group coordinator
func NewGroupCoordinator(group *AgentGroup, logger observability.Logger, tracer observability.Tracer) *GroupCoordinator {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	return &GroupCoordinator{
		group:  group,
		logger: logger,
		tracer: tracer,
	}
}

// SetIntermediateCallback sets a callback for real-time agent activity updates.
// When set, each agent executed by this coordinator will emit intermediate updates
// (tool calls, tool results, content, thinking, hooks) through this callback.
func (gc *GroupCoordinator) SetIntermediateCallback(cb agent.IntermediateCallback) {
	gc.intermediateCallback = cb
}

// SetEventCallback sets a callback for agent lifecycle events (started, completed, failed).
func (gc *GroupCoordinator) SetEventCallback(cb WorkflowEventCallback) {
	gc.eventCallback = cb
}

// emitAgentEvent emits an agent lifecycle event if a callback is registered.
func (gc *GroupCoordinator) emitAgentEvent(eventType WorkflowEventType, agentID, agentName string, result *AgentResult) {
	if gc.eventCallback == nil {
		return
	}
	evt := WorkflowEvent{
		Type:      eventType,
		Timestamp: time.Now(),
		GroupID:   gc.group.ID,
		GroupName: gc.group.Name,
		AgentID:   agentID,
		AgentName: agentName,
	}
	if result != nil {
		evt.Duration = result.Duration
		evt.Tokens = result.Tokens
		evt.Cost = result.Cost
		evt.Error = result.Error
		evt.Output = result.Output
	}
	gc.eventCallback(evt)
}

// Execute runs the agent group according to its execution strategy
func (gc *GroupCoordinator) Execute(ctx context.Context, input string, agentFactory agent.Factory) (*GroupResult, error) {
	// Start tracing
	ctx, span := gc.tracer.StartSpan(ctx, "group.execute")
	defer span.End()

	// Add span attributes
	observability.AddGroupAttributes(span, gc.group.ID, gc.group.Name)
	span.SetAttribute("execution", string(gc.group.Execution))

	// Initialize result
	result := &GroupResult{
		GroupID:   gc.group.ID,
		GroupName: gc.group.Name,
		Status:    GroupStatusRunning,
		Results:   make(map[string]*AgentResult),
		StartTime: time.Now(),
	}

	// Create timeout context if specified
	var cancel context.CancelFunc
	if gc.group.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, gc.group.Timeout)
		defer cancel()
	}

	// Execute based on strategy
	var err error
	switch gc.group.Execution {
	case ExecutionParallel:
		err = gc.executeParallel(ctx, input, agentFactory, result)
	case ExecutionSequential:
		err = gc.executeSequential(ctx, input, agentFactory, result)
	case ExecutionAdversarial:
		err = gc.executeAdversarial(ctx, input, agentFactory, result)
	default:
		err = fmt.Errorf("unsupported execution strategy: %s", gc.group.Execution)
	}

	// Finalize result
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	if err != nil {
		result.Error = err
		// Check for timeout - either from context or the error itself
		if ctx.Err() == context.DeadlineExceeded || err == context.DeadlineExceeded {
			result.Status = GroupStatusTimeout
		} else {
			result.Status = GroupStatusFailed
		}
		observability.RecordError(ctx, gc.logger, span, err, "group.execution_failed")
	} else {
		// Check if we timed out but err was nil (e.g. parallel execution where agents failed due to timeout)
		if ctx.Err() == context.DeadlineExceeded {
			result.Status = GroupStatusTimeout
			result.Error = ctx.Err()
		} else {
			// Check completion criteria
			if gc.isComplete(result) {
				// Execute coordinator if primary work is complete
				if err := gc.executeCoordinator(ctx, agentFactory, result); err != nil {
					result.Status = GroupStatusFailed
					result.Error = err
				} else {
					result.Status = GroupStatusCompleted
					// Analyze consensus if required
					if gc.group.Completion.Type == "consensus" {
						result.Consensus = gc.analyzeConsensus(ctx, result)
					}
				}
			} else {
				result.Status = GroupStatusFailed
				result.Error = fmt.Errorf("completion criteria not met")
			}
		}
	}

	// Log completion
	gc.logger.Info(ctx, "group.completed",
		observability.F("group", gc.group.Name),
		observability.F("status", string(result.Status)),
		observability.F("duration", result.Duration.Seconds()),
		observability.F("agent_count", len(result.Results)))

	return result, result.Error
}

// executeParallel runs all agents concurrently
func (gc *GroupCoordinator) executeParallel(ctx context.Context, input string, factory agent.Factory, result *GroupResult) error {
	ctx, span := gc.tracer.StartSpan(ctx, "group.execute.parallel")
	defer span.End()

	var wg sync.WaitGroup
	var mu sync.Mutex
	results := make(chan *AgentResult, len(gc.group.Agents))
	errors := make(chan error, len(gc.group.Agents))

	// Start all agents
	for _, agentDef := range gc.group.Agents {
		wg.Add(1)
		go func(def *agent.Definition) {
			defer wg.Done()

			// Create agent instance
			agentInstance, err := gc.createAgent(ctx, def, factory)
			if err != nil {
				// CRITICAL FIX: Store a failed result instead of just returning
				// This ensures the result map is complete and matches agent count
				failedResult := &AgentResult{
					AgentID:   def.ID,
					AgentName: def.Name,
					Error:     err,
					StartTime: time.Now(),
					EndTime:   time.Now(),
				}
				mu.Lock()
				result.Results[def.ID] = failedResult
				mu.Unlock()
				errors <- fmt.Errorf("failed to create agent %s: %w", def.Name, err)
				return
			}

			// Execute agent
			agentResult := gc.executeAgent(ctx, agentInstance, input)

			// Store result
			mu.Lock()
			result.Results[def.ID] = agentResult
			mu.Unlock()

			results <- agentResult
		}(agentDef)
	}

	// Wait for all agents to complete
	wg.Wait()
	close(results)
	close(errors)

	// Check for errors
	var firstError error
	for err := range errors {
		if firstError == nil {
			firstError = err
		}
		gc.logger.Error(ctx, "group.parallel.agent.error",
			observability.F("error", err.Error()))
	}

	// Don't short-circuit here — let isComplete() evaluate the actual results.
	// Even if some agents had creation errors, others may have succeeded.
	if firstError != nil {
		gc.logger.Warn(ctx, "group.parallel.had_errors",
			observability.F("group", gc.group.Name),
			observability.F("error", firstError.Error()))
	}

	return nil
}

// executeSequential runs agents one after another
func (gc *GroupCoordinator) executeSequential(ctx context.Context, input string, factory agent.Factory, result *GroupResult) error {
	ctx, span := gc.tracer.StartSpan(ctx, "group.execute.sequential")
	defer span.End()

	currentInput := input

	for _, agentDef := range gc.group.Agents {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Create agent instance
		agentInstance, err := gc.createAgent(ctx, agentDef, factory)
		if err != nil {
			return fmt.Errorf("failed to create agent %s: %w", agentDef.Name, err)
		}

		// Execute agent
		agentResult := gc.executeAgent(ctx, agentInstance, currentInput)
		result.Results[agentDef.ID] = agentResult

		// Check for error
		if agentResult.Error != nil {
			if !gc.allowsPartialFailure() {
				return agentResult.Error
			}
			gc.logger.Warn(ctx, "group.sequential.agent.error",
				observability.F("agent", agentDef.Name),
				observability.F("error", agentResult.Error.Error()))
			continue
		}

		// Use output as input for next agent
		currentInput = agentResult.Output
	}

	return nil
}

// executeAdversarial runs agents in debate/adversarial mode
func (gc *GroupCoordinator) executeAdversarial(ctx context.Context, input string, factory agent.Factory, result *GroupResult) error {
	ctx, span := gc.tracer.StartSpan(ctx, "group.execute.adversarial")
	defer span.End()

	// For adversarial execution, we run agents in rounds
	// Each agent sees the outputs of previous agents and can respond/critique

	const maxRounds = 3 // Configurable
	var debateHistory []string

	for round := range maxRounds {
		gc.logger.Info(ctx, "group.adversarial.round",
			observability.F("round", round+1),
			observability.F("max_rounds", maxRounds))

		roundComplete := true

		for _, agentDef := range gc.group.Agents {
			// Check context cancellation
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// Create agent instance
			agentInstance, err := gc.createAgent(ctx, agentDef, factory)
			if err != nil {
				return fmt.Errorf("failed to create agent %s: %w", agentDef.Name, err)
			}

			// Build context with debate history
			debateContext := fmt.Sprintf("Original question: %s\n\nDebate history:\n%s\n\nYour turn to respond:",
				input, formatDebateHistory(debateHistory))

			// Execute agent
			agentResult := gc.executeAgent(ctx, agentInstance, debateContext)

			// Store result with round information
			resultKey := fmt.Sprintf("%s_round%d", agentDef.ID, round+1)
			result.Results[resultKey] = agentResult

			if agentResult.Error != nil {
				gc.logger.Warn(ctx, "group.adversarial.agent.error",
					observability.F("agent", agentDef.Name),
					observability.F("round", round+1),
					observability.F("error", agentResult.Error.Error()))
				continue
			}

			// Add to debate history
			debateHistory = append(debateHistory, fmt.Sprintf("[%s]: %s", agentDef.Name, agentResult.Output))

			// Check if consensus reached (simplified check)
			if gc.hasConsensusInDebate(debateHistory) {
				roundComplete = false
				break
			}
		}

		if !roundComplete {
			break
		}
	}

	return nil
}

// createAgent creates an agent instance from a definition
func (gc *GroupCoordinator) createAgent(ctx context.Context, def *agent.Definition, factory agent.Factory) (*agent.Agent, error) {
	// Create provider config from definition
	// Note: Don't set Name/Model here - let the factory handle resolution from the definition
	// This is important for '@current' provider/model support in WorkflowAgentFactory
	providerConfig := provider.Config{
		// Name and Model will be set by the factory based on def.Provider and def.Model
		// which allows special values like '@current' to be resolved properly
	}
	if def.Provider != "" && def.Provider != "@current" {
		providerConfig.Name = def.Provider
	}
	if def.Model != "" && def.Model != "@current" {
		providerConfig.Model = def.Model
	}

	// Create agent using factory
	return factory.CreateFromDefinition(ctx, def, providerConfig)
}

// executeAgent runs a single agent and captures its result
func (gc *GroupCoordinator) executeAgent(ctx context.Context, agentInstance *agent.Agent, input string) *AgentResult {
	startTime := time.Now()

	result := &AgentResult{
		AgentID:   agentInstance.ID(),
		AgentName: agentInstance.Name(),
		StartTime: startTime,
	}

	// Emit agent_started event
	gc.emitAgentEvent(WorkflowEventAgentStarted, agentInstance.ID(), agentInstance.Name(), nil)

	// Wire intermediate callback for real-time activity updates.
	// Wrap the callback to tag each update with this agent's identity,
	// so the TUI can track activity per-agent (not per-tool-call).
	if gc.intermediateCallback != nil {
		agentID := agentInstance.ID()
		// Make AgentID unique per invocation for parallel safety
		// Generate internal unique ID - cannot be set or confused by external code
		invocationID := generateInvocationID()
		agentID = fmt.Sprintf("%s-%s", agentID, invocationID)
		agentName := agentInstance.Name()
		agentInstance.SetIntermediateCallback(func(ctx context.Context, update agent.IntermediateUpdate) error {
			// Wrap in SubAgentUpdate to carry agent identity through the event pipeline
			wrapped := agent.SubAgentUpdate{
				AgentID:   agentID,
				AgentName: agentName,
				Update:    update,
			}
			return gc.intermediateCallback(ctx, wrapped)
		})
	}

	// Create execute request
	req := agent.ExecuteRequest{
		Message: input,
	}

	// Execute the agent
	resp, err := agentInstance.Execute(ctx, req)

	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	if err != nil {
		result.Error = err
		result.Output = ""
		gc.logger.Error(ctx, "agent.execution.FAILED",
			observability.F("agent_id", agentInstance.ID()),
			observability.F("agent_name", agentInstance.Name()),
			observability.F("error", err.Error()),
			observability.F("error_type", fmt.Sprintf("%T", err)),
			observability.F("duration_s", result.Duration.Seconds()))
	} else if resp == nil {
		result.Error = fmt.Errorf("agent returned nil response without error")
		result.Output = ""
		gc.logger.Error(ctx, "agent.execution.NIL_RESPONSE",
			observability.F("agent_id", agentInstance.ID()),
			observability.F("agent_name", agentInstance.Name()))
	} else {
		result.Output = resp.Message
		result.Tokens = resp.TokensUsed
		result.Cost = resp.CostUSD

		gc.logger.Info(ctx, "agent.execution.completed",
			observability.F("agent_id", agentInstance.ID()),
			observability.F("agent_name", agentInstance.Name()),
			observability.F("has_output", result.Output != ""),
			observability.F("output_length", len(result.Output)),
			observability.F("tokens_used", resp.TokensUsed),
			observability.F("finish_reason", string(resp.FinishReason)))
	}

	// Emit agent_completed or agent_failed event
	if result.Error != nil {
		gc.emitAgentEvent(WorkflowEventAgentFailed, agentInstance.ID(), agentInstance.Name(), result)
	} else {
		gc.emitAgentEvent(WorkflowEventAgentCompleted, agentInstance.ID(), agentInstance.Name(), result)
	}

	return result
}

// isComplete checks if the group execution meets completion criteria.
//
// Whether an agent "succeeded" depends on two things:
//  1. No execution error (r.Error == nil).
//  2. Produced non-empty output text — ONLY when criteria.RequireOutput is true.
//     The default (false) means tool-only agents that write files / call APIs
//     and return a brief or empty final message are counted as successful.
//
// Special-case: for "first" completion type, an agent that produced output
// even alongside a non-fatal error (e.g. token-limit truncation) still counts,
// because some useful content was returned.
func (gc *GroupCoordinator) isComplete(result *GroupResult) bool {
	criteria := gc.group.Completion

	gc.logger.Info(context.Background(), "group.isComplete.evaluation",
		observability.F("group_name", gc.group.Name),
		observability.F("completion_type", criteria.Type),
		observability.F("require_output", criteria.RequireOutput),
		observability.F("total_agents", len(gc.group.Agents)),
		observability.F("results_count", len(result.Results)))

	// agentSucceeded returns true when the agent completed without error.
	// When RequireOutput is set, the agent must also have produced non-empty text.
	agentSucceeded := func(r *AgentResult) bool {
		if r.Error != nil {
			return false
		}
		if criteria.RequireOutput && strings.TrimSpace(r.Output) == "" {
			return false
		}
		return true
	}

	// Log per-agent status for post-mortem debugging.
	for agentID, r := range result.Results {
		errStr := ""
		if r.Error != nil {
			errStr = r.Error.Error()
		}
		gc.logger.Info(context.Background(), "group.isComplete.agent_status",
			observability.F("group_name", gc.group.Name),
			observability.F("agent_id", agentID),
			observability.F("agent_name", r.AgentName),
			observability.F("succeeded", agentSucceeded(r)),
			observability.F("has_error", r.Error != nil),
			observability.F("error", errStr),
			observability.F("output_length", len(r.Output)),
			observability.F("duration_s", r.Duration.Seconds()))
	}

	switch criteria.Type {
	case "all":
		// For adversarial execution the result map has keys like "agent_round1",
		// "agent_round2", etc. — count unique agent IDs rather than raw entries.
		expectedCount := len(gc.group.Agents)
		if gc.group.Execution == ExecutionAdversarial {
			// Count how many distinct agent IDs (prefix before "_round") succeeded
			// in at least one round.
			agentSeen := make(map[string]bool, expectedCount)
			agentFailed := make(map[string]bool, expectedCount)
			for key, r := range result.Results {
				// Strip the "_roundN" suffix to get the base agent ID.
				baseID := key
				if idx := strings.LastIndex(key, "_round"); idx != -1 {
					baseID = key[:idx]
				}
				if agentSucceeded(r) {
					agentSeen[baseID] = true
				} else {
					// Only mark as definitively failed if it never succeeded.
					if !agentSeen[baseID] {
						agentFailed[baseID] = true
					}
				}
			}
			failureCount := len(agentFailed)
			gc.logger.Info(context.Background(), "group.isComplete.adversarial_result",
				observability.F("group_name", gc.group.Name),
				observability.F("unique_succeeded", len(agentSeen)),
				observability.F("unique_failed", failureCount),
				observability.F("max_failures", criteria.MaxFailures))
			if failureCount > criteria.MaxFailures {
				return false
			}
			return len(agentSeen) >= expectedCount-criteria.MaxFailures
		}

		// Non-adversarial: every agent must have a result entry (creation errors
		// are stored as failed results by executeParallel).
		if len(result.Results) != expectedCount {
			gc.logger.Warn(context.Background(), "group.isComplete.agent_count_mismatch",
				observability.F("group_name", gc.group.Name),
				observability.F("expected", expectedCount),
				observability.F("actual", len(result.Results)))
			return false
		}
		failureCount := 0
		for agentID, agentResult := range result.Results {
			if !agentSucceeded(agentResult) {
				failureCount++
				gc.logger.Warn(context.Background(), "group.isComplete.agent_failed",
					observability.F("group_name", gc.group.Name),
					observability.F("agent_id", agentID),
					observability.F("has_error", agentResult.Error != nil),
					observability.F("has_output", agentResult.Output != ""))
			}
		}
		if failureCount > criteria.MaxFailures {
			gc.logger.Warn(context.Background(), "group.isComplete.too_many_failures",
				observability.F("group_name", gc.group.Name),
				observability.F("failure_count", failureCount),
				observability.F("max_failures", criteria.MaxFailures))
			return false
		}
		return true

	case "first":
		// Any agent with output (even alongside a non-fatal error) is sufficient.
		for agentID, agentResult := range result.Results {
			if agentSucceeded(agentResult) {
				gc.logger.Info(context.Background(), "group.isComplete.first_success",
					observability.F("group_name", gc.group.Name),
					observability.F("agent_id", agentID))
				return true
			}
			// Accept agents that produced output before a non-fatal error
			// (e.g. token-limit truncation with partial response).
			if agentResult.Output != "" {
				gc.logger.Info(context.Background(), "group.isComplete.first_success_with_error",
					observability.F("group_name", gc.group.Name),
					observability.F("agent_id", agentID),
					observability.F("output_length", len(agentResult.Output)))
				return true
			}
		}
		return false

	case "majority":
		// effectiveThreshold is the minimum success ratio. The default of 0.0 in
		// CompletionCriteria is a sentinel meaning "use 0.5".
		effectiveThreshold := criteria.Threshold
		if effectiveThreshold <= 0.0 {
			effectiveThreshold = 0.5
		}
		totalAgents := len(gc.group.Agents)
		if totalAgents == 0 {
			return true
		}
		successCount := 0
		for _, agentResult := range result.Results {
			if agentSucceeded(agentResult) {
				successCount++
			}
		}
		ratio := float64(successCount) / float64(totalAgents)
		passed := ratio >= effectiveThreshold
		gc.logger.Info(context.Background(), "group.isComplete.majority_result",
			observability.F("group_name", gc.group.Name),
			observability.F("success_count", successCount),
			observability.F("total_agents", totalAgents),
			observability.F("ratio", ratio),
			observability.F("threshold", effectiveThreshold),
			observability.F("passed", passed))
		return passed

	case "consensus":
		// Threshold 0.0 sentinel → default to 0.5.
		effectiveThreshold := criteria.Threshold
		if effectiveThreshold <= 0.0 {
			effectiveThreshold = 0.5
		}
		consensus := gc.analyzeConsensus(context.Background(), result)
		return consensus.Reached && consensus.Confidence >= effectiveThreshold

	default:
		// Unknown type: fall back to "all" semantics (fail-safe).
		if len(result.Results) != len(gc.group.Agents) {
			return false
		}
		for _, agentResult := range result.Results {
			if !agentSucceeded(agentResult) {
				return false
			}
		}
		return true
	}
}

// analyzeConsensus analyzes agent outputs for consensus.
// An agent counts toward consensus only when it succeeded (no error, and
// non-empty output when RequireOutput is set on the group).
func (gc *GroupCoordinator) analyzeConsensus(ctx context.Context, result *GroupResult) *ConsensusResult {
	consensus := &ConsensusResult{
		Method:     ConsensusMethodVoting,
		Agreements: []string{},
		Conflicts:  []string{},
	}

	requireOutput := gc.group.Completion.RequireOutput
	successCount := 0
	for _, agentResult := range result.Results {
		if agentResult.Error != nil {
			continue
		}
		if requireOutput && strings.TrimSpace(agentResult.Output) == "" {
			continue
		}
		successCount++
	}

	// For adversarial groups the result map has one entry per round per agent;
	// use the number of defined agents as the denominator, not len(result.Results).
	totalAgents := len(gc.group.Agents)
	if totalAgents == 0 {
		consensus.Reached = true
		consensus.Confidence = 1.0
		consensus.Summary = "No agents defined — trivially complete"
		return consensus
	}

	consensus.Confidence = float64(successCount) / float64(totalAgents)
	consensus.Reached = consensus.Confidence >= 0.5
	if consensus.Reached {
		consensus.Summary = fmt.Sprintf("Consensus reached: %d/%d agents succeeded", successCount, totalAgents)
		consensus.Agreements = append(consensus.Agreements, "Majority of agents provided valid outputs")
	} else {
		consensus.Summary = fmt.Sprintf("No consensus: only %d/%d agents succeeded", successCount, totalAgents)
		consensus.Conflicts = append(consensus.Conflicts, "Insufficient agent agreement")
	}
	return consensus
}

// allowsPartialFailure checks if the group allows partial failures
func (gc *GroupCoordinator) allowsPartialFailure() bool {
	criteria := gc.group.Completion

	// Some completion types inherently allow partial failure
	switch criteria.Type {
	case "first", "consensus", "majority":
		return true
	case "all":
		return criteria.MaxFailures > 0
	default:
		return false
	}
}

// hasConsensusInDebate checks if consensus has been reached in adversarial debate
func (gc *GroupCoordinator) hasConsensusInDebate(history []string) bool {
	// Simplified: check if recent responses show agreement
	// In a real implementation, use NLP to detect agreement patterns

	if len(history) < 2 {
		return false
	}

	// Look for agreement keywords in recent responses
	lastTwo := history[len(history)-2:]
	return slices.ContainsFunc(lastTwo, containsAgreement)
}

// formatDebateHistory formats the debate history for context
func formatDebateHistory(history []string) string {
	if len(history) == 0 {
		return "No previous responses yet."
	}

	var formatted strings.Builder
	for i, entry := range history {
		formatted.WriteString(fmt.Sprintf("%d. %s\n", i+1, entry))
	}
	return formatted.String()
}

// containsAgreement checks if a response contains agreement indicators
func containsAgreement(text string) bool {
	// Very simplified - in production, use proper NLP
	agreements := []string{
		"i agree",
		"you're right",
		"that's correct",
		"consensus reached",
		"we agree",
	}

	lowered := fmt.Sprintf(" %s ", text) // Add spaces for word boundary
	for _, phrase := range agreements {
		if contains(lowered, phrase) {
			return true
		}
	}
	return false
}

// contains is a simple string contains check (case-insensitive)
func contains(text, substr string) bool {
	return strings.Contains(strings.ToLower(text), strings.ToLower(substr))
}

// executeCoordinator produces the group's final output based on the configured OutputStrategy.
// - "raw": concatenates all agent outputs (or uses single agent output directly)
// - "first": uses the first successful agent's output
// - "synthesize": runs a coordinator agent to synthesize, falling back to raw if none defined
func (gc *GroupCoordinator) executeCoordinator(ctx context.Context, agentFactory agent.Factory, result *GroupResult) error {
	strategy := gc.group.OutputStrategy
	if strategy == "" {
		strategy = OutputStrategyRaw
	}

	gc.logger.Info(ctx, "group.output_strategy",
		observability.F("group", gc.group.Name),
		observability.F("strategy", string(strategy)))

	switch strategy {
	case OutputStrategyFirst:
		// Use the first successful agent's output
		for _, agentResult := range result.Results {
			if agentResult.Error == nil && agentResult.Output != "" {
				result.Output = agentResult.Output
				return nil
			}
		}
		return fmt.Errorf("no successful agent output found for first strategy")

	case OutputStrategySynthesize:
		// Use coordinator agent if defined
		if gc.group.Coordinator != nil {
			return gc.runCoordinatorAgent(ctx, agentFactory, result)
		}
		// Fall through to raw if no coordinator defined
		gc.logger.Warn(ctx, "group.synthesize_no_coordinator",
			observability.F("group", gc.group.Name))
		return gc.buildRawOutput(result)

	default: // OutputStrategyRaw
		return gc.buildRawOutput(result)
	}
}

// runCoordinatorAgent executes the coordinator agent to synthesize agent outputs.
func (gc *GroupCoordinator) runCoordinatorAgent(ctx context.Context, agentFactory agent.Factory, result *GroupResult) error {
	var synthesisInput strings.Builder
	synthesisInput.WriteString("Please synthesize the following outputs from multiple agents into a single, cohesive final response.\n\n")

	for _, agentResult := range result.Results {
		if agentResult.Error == nil && agentResult.Output != "" {
			synthesisInput.WriteString(fmt.Sprintf("--- AGENT: %s ---\n%s\n\n", agentResult.AgentName, agentResult.Output))
		}
	}

	coordinator, err := gc.createAgent(ctx, gc.group.Coordinator, agentFactory)
	if err != nil {
		return fmt.Errorf("failed to create coordinator agent: %w", err)
	}

	gc.logger.Info(ctx, "group.executing_coordinator",
		observability.F("group", gc.group.Name),
		observability.F("coordinator", coordinator.Name()))

	coordResult := gc.executeAgent(ctx, coordinator, synthesisInput.String())
	if coordResult.Error != nil {
		return fmt.Errorf("coordinator agent failed: %w", coordResult.Error)
	}

	result.Output = coordResult.Output
	result.Results["coordinator"] = coordResult
	return nil
}

// buildRawOutput concatenates all successful agent outputs into the group output.
func (gc *GroupCoordinator) buildRawOutput(result *GroupResult) error {
	var finalOutput strings.Builder
	for _, agentResult := range result.Results {
		if agentResult.Error == nil && agentResult.Output != "" {
			if finalOutput.Len() > 0 {
				finalOutput.WriteString("\n\n---\n\n")
			}
			finalOutput.WriteString(agentResult.Output)
		}
	}
	result.Output = finalOutput.String()
	return nil
}
