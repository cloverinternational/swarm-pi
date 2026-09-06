package mode

import (
	"context"
	"fmt"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode/gates"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
)

// GateActionHandler handles gate action execution (retry, route, etc.)
type GateActionHandler struct {
	engine  *WorkflowEngine
	logger  observability.Logger
	tracer  observability.Tracer
	retries map[string]int // Track retry counts per group
}

// NewGateActionHandler creates a new gate action handler
func NewGateActionHandler(engine *WorkflowEngine, logger observability.Logger, tracer observability.Tracer) *GateActionHandler {
	if logger == nil {
		logger = noop.NewLogger()
	}
	if tracer == nil {
		tracer = noop.NewTracer()
	}
	return &GateActionHandler{
		engine:  engine,
		logger:  logger,
		tracer:  tracer,
		retries: make(map[string]int),
	}
}

// HandleGateResult processes a gate evaluation result and returns the modified group result
func (h *GateActionHandler) HandleGateResult(
	ctx context.Context,
	gateResult *gates.GateEvaluationResult,
	group *AgentGroup,
	groupInput string,
	factory agent.Factory,
) (*GroupResult, error) {

	if gateResult.Allow {
		// Gate passed - no action needed
		return nil, nil
	}

	// Gate did not allow - handle the action
	switch gateResult.Action {
	case gates.ActionRetry, gates.ActionRetryWithFeedback:
		return h.handleRetry(ctx, gateResult, group, groupInput, factory)

	case gates.ActionRouteToGroup:
		return h.handleRoute(ctx, gateResult, group, groupInput, factory)

	case gates.ActionBlock:
		return h.handleBlock(gateResult, group)

	case gates.ActionSkipGroup:
		return h.handleSkip(group)

	default:
		return h.handleBlock(gateResult, group)
	}
}

// handleRetry handles retry and retry_with_feedback actions
func (h *GateActionHandler) handleRetry(
	ctx context.Context,
	gateResult *gates.GateEvaluationResult,
	group *AgentGroup,
	groupInput string,
	factory agent.Factory,
) (*GroupResult, error) {

	currentRetries := h.retries[group.ID]
	maxRetries := 3 // Default
	if h.engine.mode.Config.MaxRetries > 0 {
		maxRetries = h.engine.mode.Config.MaxRetries
	}

	if currentRetries >= maxRetries {
		h.logger.Warn(ctx, "group max retries exceeded",
			observability.F("group_id", group.ID),
			observability.F("max_retries", maxRetries))

		return &GroupResult{
			GroupID:   group.ID,
			GroupName: group.Name,
			Status:    GroupStatusFailed,
			Error:     fmt.Errorf("max retries exceeded (%d): %s", maxRetries, gateResult.Reason),
			Results:   make(map[string]*AgentResult),
		}, nil
	}

	// Increment retry count
	h.retries[group.ID] = currentRetries + 1

	h.logger.Info(ctx, "group retry requested by gate",
		observability.F("group_id", group.ID),
		observability.F("attempt", currentRetries+1),
		observability.F("max_retries", maxRetries),
		observability.F("reason", gateResult.Reason))

	// Build input with feedback if requested
	retryInput := groupInput
	if gateResult.Action == gates.ActionRetryWithFeedback {
		// Extract feedback from metadata
		feedback := ""
		if feedbackVal, ok := gateResult.Metadata["feedback"]; ok {
			if feedbackStr, ok := feedbackVal.(string); ok {
				feedback = feedbackStr
			}
		}
		// Fall back to reason if no specific feedback
		if feedback == "" {
			feedback = gateResult.Reason
		}

		retryInput = fmt.Sprintf(`%s

# Feedback from Quality Gate

%s

# Instructions

Please address the feedback above and improve your response.`,
			groupInput, feedback)
	}

	// Retry the group execution
	coordinator := NewGroupCoordinator(group, h.logger, h.tracer)
	result, err := coordinator.Execute(ctx, retryInput, factory)
	if err != nil {
		return nil, fmt.Errorf("group %s retry failed: %w", group.Name, err)
	}

	return result, nil
}

// handleRoute handles routing to a different group
func (h *GateActionHandler) handleRoute(
	ctx context.Context,
	gateResult *gates.GateEvaluationResult,
	group *AgentGroup,
	groupInput string,
	factory agent.Factory,
) (*GroupResult, error) {

	// Extract target group from metadata
	targetGroupID := ""
	if targetVal, ok := gateResult.Metadata["target_group"]; ok {
		if targetStr, ok := targetVal.(string); ok {
			targetGroupID = targetStr
		}
	}

	h.logger.Info(ctx, "group routing requested by gate",
		observability.F("group_id", group.ID),
		observability.F("target_group", targetGroupID),
		observability.F("reason", gateResult.Reason))

	// Find target group
	targetGroup := h.engine.mode.GetGroup(targetGroupID)
	if targetGroup == nil {
		h.logger.Error(ctx, "routing target group not found",
			observability.F("target_group", targetGroupID))

		return &GroupResult{
			GroupID:   group.ID,
			GroupName: group.Name,
			Status:    GroupStatusFailed,
			Error:     fmt.Errorf("routing target group not found: %s", targetGroupID),
			Results:   make(map[string]*AgentResult),
		}, nil
	}

	// Execute target group
	coordinator := NewGroupCoordinator(targetGroup, h.logger, h.tracer)
	routedResult, err := coordinator.Execute(ctx, groupInput, factory)
	if err != nil {
		h.logger.Error(ctx, "routed group execution failed",
			observability.F("target_group", targetGroupID),
			observability.F("error", err.Error()))

		return &GroupResult{
			GroupID:   group.ID,
			GroupName: group.Name,
			Status:    GroupStatusFailed,
			Error:     fmt.Errorf("routed group failed: %w", err),
			Results:   make(map[string]*AgentResult),
		}, nil
	}

	// Modify result to reflect routing (keep original group ID for workflow tracking)
	routedResult.GroupID = group.ID
	routedResult.GroupName = fmt.Sprintf("%s (routed to %s)", group.Name, targetGroup.Name)

	return routedResult, nil
}

// handleBlock handles blocking action
func (h *GateActionHandler) handleBlock(gateResult *gates.GateEvaluationResult, group *AgentGroup) (*GroupResult, error) {
	return &GroupResult{
		GroupID:   group.ID,
		GroupName: group.Name,
		Status:    GroupStatusFailed,
		Error:     fmt.Errorf("blocked by gate: %s", gateResult.Reason),
		Results:   make(map[string]*AgentResult),
	}, nil
}

// handleSkip handles skip_group action
func (h *GateActionHandler) handleSkip(group *AgentGroup) (*GroupResult, error) {
	return &GroupResult{
		GroupID:   group.ID,
		GroupName: group.Name,
		Status:    GroupStatusCompleted,
		Results:   make(map[string]*AgentResult),
	}, nil
}
