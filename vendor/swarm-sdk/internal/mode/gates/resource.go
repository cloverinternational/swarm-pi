package gates

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// ResourceGate enforces resource limits (cost, tokens, time)
type ResourceGate struct {
	*BaseGate
}

// NewResourceGate creates a new resource gate
func NewResourceGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error) {
	base := NewBaseGate(config, logger, tracer)

	return &ResourceGate{
		BaseGate: base,
	}, nil
}

// Evaluate evaluates the resource gate
func (g *ResourceGate) Evaluate(ctx context.Context, evalCtx *EvaluationContext) (*GateDecision, error) {
	ctx, span := g.tracer.StartSpan(ctx, "gate.resource.evaluate")
	defer span.End()

	decision := &GateDecision{
		Allow:      true,
		Confidence: 1.0,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]any),
	}

	reasons := []string{}

	// Check max cost
	if maxCost := g.GetPolicyFloat("max_cost", 0); maxCost > 0 {
		currentCost := evalCtx.TotalCost
		decision.Metadata["current_cost"] = currentCost
		decision.Metadata["max_cost"] = maxCost

		if currentCost >= maxCost {
			decision.Allow = false
			reasons = append(reasons, fmt.Sprintf("cost limit exceeded: $%.2f >= $%.2f", currentCost, maxCost))
		} else {
			percentage := (currentCost / maxCost) * 100
			reasons = append(reasons, fmt.Sprintf("cost: $%.2f / $%.2f (%.0f%%)", currentCost, maxCost, percentage))
		}
	}

	// Check max tokens
	if maxTokens := g.GetPolicyInt("max_tokens", 0); maxTokens > 0 {
		currentTokens := evalCtx.TotalTokens
		decision.Metadata["current_tokens"] = currentTokens
		decision.Metadata["max_tokens"] = maxTokens

		if currentTokens >= maxTokens {
			decision.Allow = false
			reasons = append(reasons, fmt.Sprintf("token limit exceeded: %d >= %d", currentTokens, maxTokens))
		} else {
			percentage := (float64(currentTokens) / float64(maxTokens)) * 100
			reasons = append(reasons, fmt.Sprintf("tokens: %d / %d (%.0f%%)", currentTokens, maxTokens, percentage))
		}
	}

	// Check max duration
	if maxDuration := g.policy["max_duration"]; maxDuration != nil {
		var maxDur time.Duration

		switch v := maxDuration.(type) {
		case time.Duration:
			maxDur = v
		case int:
			maxDur = time.Duration(v) * time.Second
		case float64:
			maxDur = time.Duration(v) * time.Second
		case string:
			var err error
			maxDur, err = time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("invalid max_duration: %v", err)
			}
		}

		if maxDur > 0 {
			currentDuration := evalCtx.ElapsedTime
			decision.Metadata["current_duration"] = currentDuration.String()
			decision.Metadata["max_duration"] = maxDur.String()

			if currentDuration >= maxDur {
				decision.Allow = false
				reasons = append(reasons, fmt.Sprintf("time limit exceeded: %s >= %s", currentDuration, maxDur))
			} else {
				percentage := (float64(currentDuration) / float64(maxDur)) * 100
				reasons = append(reasons, fmt.Sprintf("time: %s / %s (%.0f%%)", currentDuration.Round(time.Second), maxDur, percentage))
			}
		}
	}

	// Check if we should evaluate before execution
	checkBefore := g.GetPolicyBool("check_before_execution", false)
	if checkBefore {
		decision.Metadata["check_before_execution"] = true
	}

	// Build reason
	if len(reasons) > 0 {
		decision.Reason = fmt.Sprintf("resource check: %s", reasons[0])
		if len(reasons) > 1 {
			for _, r := range reasons[1:] {
				decision.Reason += "; " + r
			}
		}
	} else {
		decision.Reason = "no resource limits configured"
	}

	// Determine action
	actionConfig := g.DetermineAction(decision.Allow, false)
	decision.Action = actionConfig.Action

	// Calculate confidence based on resource usage
	if decision.Allow {
		// Confidence decreases as we approach limits
		minConfidence := 1.0

		if maxCost := g.GetPolicyFloat("max_cost", 0); maxCost > 0 {
			costRatio := evalCtx.TotalCost / maxCost
			if costRatio > 0.8 { // Warning at 80%
				minConfidence = min(minConfidence, 1.0-((costRatio-0.8)/0.2)*0.5)
			}
		}

		if maxTokens := g.GetPolicyInt("max_tokens", 0); maxTokens > 0 {
			tokenRatio := float64(evalCtx.TotalTokens) / float64(maxTokens)
			if tokenRatio > 0.8 {
				minConfidence = min(minConfidence, 1.0-((tokenRatio-0.8)/0.2)*0.5)
			}
		}

		decision.Confidence = minConfidence
	} else {
		decision.Confidence = 0.0
	}

	return decision, nil
}

// DependencyGate evaluates conditions and dependencies
type DependencyGate struct {
	*BaseGate
}

// NewDependencyGate creates a new dependency gate
func NewDependencyGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error) {
	base := NewBaseGate(config, logger, tracer)

	return &DependencyGate{
		BaseGate: base,
	}, nil
}

// Evaluate evaluates the dependency gate
func (g *DependencyGate) Evaluate(ctx context.Context, evalCtx *EvaluationContext) (*GateDecision, error) {
	ctx, span := g.tracer.StartSpan(ctx, "gate.dependency.evaluate")
	defer span.End()

	decision := &GateDecision{
		Allow:      true,
		Confidence: 1.0,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]any),
	}

	// Get conditions from policy
	conditions, ok := g.policy["conditions"].([]any)
	if !ok || len(conditions) == 0 {
		decision.Reason = "no conditions specified"
		return decision, nil
	}

	// Check if all conditions are required or just any
	allRequired := g.GetPolicyBool("all_required", true)
	anyRequired := g.GetPolicyBool("any_required", false)

	if allRequired && anyRequired {
		return nil, fmt.Errorf("cannot specify both all_required and any_required")
	}

	// Evaluate conditions
	passedConditions := 0
	failedConditions := []string{}

	for i, cond := range conditions {
		condStr, ok := cond.(string)
		if !ok {
			continue
		}

		// Evaluate condition
		passed := g.evaluateCondition(condStr, evalCtx)

		if passed {
			passedConditions++
		} else {
			failedConditions = append(failedConditions, condStr)
		}

		decision.Metadata[fmt.Sprintf("condition_%d", i)] = map[string]any{
			"condition": condStr,
			"passed":    passed,
		}
	}

	// Determine if gate passes
	totalConditions := len(conditions)

	if allRequired {
		// All conditions must pass
		decision.Allow = passedConditions == totalConditions
		decision.Confidence = float64(passedConditions) / float64(totalConditions)

		if decision.Allow {
			decision.Reason = fmt.Sprintf("all %d conditions passed", totalConditions)
		} else {
			decision.Reason = fmt.Sprintf("%d of %d conditions failed: %v", len(failedConditions), totalConditions, failedConditions)
		}
	} else if anyRequired {
		// At least one condition must pass
		decision.Allow = passedConditions > 0
		decision.Confidence = float64(passedConditions) / float64(totalConditions)

		if decision.Allow {
			decision.Reason = fmt.Sprintf("%d of %d conditions passed (any required)", passedConditions, totalConditions)
		} else {
			decision.Reason = "no conditions passed (at least one required)"
		}
	} else {
		// Default to all required
		decision.Allow = passedConditions == totalConditions
		decision.Confidence = float64(passedConditions) / float64(totalConditions)
		decision.Reason = fmt.Sprintf("%d of %d conditions passed", passedConditions, totalConditions)
	}

	// Determine action
	actionConfig := g.DetermineAction(decision.Allow, false)
	decision.Action = actionConfig.Action

	return decision, nil
}

// evaluateCondition evaluates a single condition string
func (g *DependencyGate) evaluateCondition(condition string, evalCtx *EvaluationContext) bool {
	// This is a simplified implementation
	// In a real system, you'd use an expression evaluator like:
	// - github.com/antonmedv/expr
	// - github.com/PaesslerAG/gval
	// - Custom DSL parser

	// For now, support simple string matching
	// Format: "path.to.value operator value"
	// Examples:
	// - "groups.group1.status == 'completed'"
	// - "groups.group1.output contains 'security'"
	// - "context.has_required_files == true"

	// Simple implementation: just check for "contains" keyword
	if len(condition) > 0 {
		// For demo purposes, if condition contains "true", return true
		// In real implementation, parse and evaluate the expression

		// Check for some common patterns
		if evalCtx.Group != nil {
			// Group-level checks - can't access fields due to any
		}

		if evalCtx.GroupResult != nil {
			// Can check result status, consensus, etc. - can't access fields due to any
		}

		// For now, return true for any condition (placeholder)
		// This should be replaced with actual expression evaluation
		return true
	}

	return false
}

// ApprovalGate requires human or agent approval
type ApprovalGate struct {
	*BaseGate
	approvalChan chan bool
}

// NewApprovalGate creates a new approval gate
func NewApprovalGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error) {
	base := NewBaseGate(config, logger, tracer)

	return &ApprovalGate{
		BaseGate:     base,
		approvalChan: make(chan bool, 1),
	}, nil
}

// Evaluate evaluates the approval gate
func (g *ApprovalGate) Evaluate(ctx context.Context, evalCtx *EvaluationContext) (*GateDecision, error) {
	ctx, span := g.tracer.StartSpan(ctx, "gate.approval.evaluate")
	defer span.End()

	decision := &GateDecision{
		Allow:      false, // Default to not allowing until approved
		Confidence: 0.0,
		Timestamp:  time.Now(),
		Metadata:   make(map[string]any),
	}

	// Get timeout
	timeout := g.timeout
	if timeout == 0 {
		timeout = 1 * time.Hour // Default 1 hour
	}

	// Get timeout action
	timeoutAction := g.GetPolicyString("timeout_action", "reject")

	// Get required approvers
	requiredApprovers := []map[string]any{}
	if approvers, ok := g.policy["required_approvers"].([]any); ok {
		for _, a := range approvers {
			if approver, ok := a.(map[string]any); ok {
				requiredApprovers = append(requiredApprovers, approver)
			}
		}
	}

	decision.Metadata["required_approvers"] = requiredApprovers
	decision.Metadata["timeout"] = timeout.String()
	decision.Metadata["timeout_action"] = timeoutAction

	// Log approval request
	g.logger.Info(ctx, "approval_required",
		observability.F("gate_id", g.ID()),
		observability.F("timeout", timeout.String()))

	// In a real implementation, this would:
	// 1. Send notification to required approvers
	// 2. Wait for approval or timeout
	// 3. Handle approval response

	// For now, implement timeout behavior immediately
	// This is a placeholder - real implementation would wait for actual approval

	timedOut := false

	// Simulate waiting with context cancellation support
	select {
	case <-ctx.Done():
		decision.Reason = "approval request cancelled"
		decision.Action = ActionBlock
		return decision, ctx.Err()

	case approved := <-g.approvalChan:
		// Got approval response
		decision.Allow = approved
		if approved {
			decision.Confidence = 1.0
			decision.Reason = "approved by required approvers"
			decision.Action = ActionContinue
		} else {
			decision.Confidence = 0.0
			decision.Reason = "rejected by approvers"
			decision.Action = ActionBlock
		}

	case <-time.After(1 * time.Second):
		// Timeout (using short timeout for demo)
		timedOut = true
	}

	if timedOut {
		// Handle timeout
		switch timeoutAction {
		case "auto_approve", "continue":
			decision.Allow = true
			decision.Confidence = 0.5
			decision.Reason = "auto-approved after timeout"
			decision.Action = ActionContinue

		case "reject", "block":
			decision.Allow = false
			decision.Confidence = 0.0
			decision.Reason = "rejected due to timeout"
			decision.Action = ActionBlock

		case "continue_anyway":
			decision.Allow = true
			decision.Confidence = 0.3
			decision.Reason = "continuing without approval (timeout)"
			decision.Action = ActionContinue

		default:
			decision.Allow = false
			decision.Confidence = 0.0
			decision.Reason = "no approval received (timeout)"
			decision.Action = ActionBlock
		}

		decision.Metadata["timed_out"] = true
	}

	return decision, nil
}

// RequestApproval sends an approval request
// This would be called externally to provide approval
func (g *ApprovalGate) RequestApproval(approved bool) {
	select {
	case g.approvalChan <- approved:
	default:
		// Channel full, ignore
	}
}

// Helper function
func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// Register gate factories
func init() {
	GlobalGateRegistry.RegisterFactory(GateTypeResource, NewResourceGate)
	GlobalGateRegistry.RegisterFactory(GateTypeDependency, NewDependencyGate)
	GlobalGateRegistry.RegisterFactory(GateTypeApproval, NewApprovalGate)
}
