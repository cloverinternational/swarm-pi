package gates

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode/consensus"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// ConsensusGate evaluates agent consensus
type ConsensusGate struct {
	*BaseGate
	registry *consensus.ConsensusEvaluatorRegistry
}

// NewConsensusGate creates a new consensus gate
func NewConsensusGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error) {
	base := NewBaseGate(config, logger, tracer)

	// Initialize consensus registry if not already done
	if consensus.GlobalConsensusRegistry == nil {
		consensus.InitGlobalConsensusRegistry(logger, tracer)
	}

	return &ConsensusGate{
		BaseGate: base,
		registry: consensus.GlobalConsensusRegistry,
	}, nil
}

// Evaluate evaluates the consensus gate
func (g *ConsensusGate) Evaluate(ctx context.Context, evalCtx *EvaluationContext) (*GateDecision, error) {
	ctx, span := g.tracer.StartSpan(ctx, "gate.consensus.evaluate")
	defer span.End()

	if evalCtx.GroupResult == nil {
		return &GateDecision{
			Allow:      false,
			Confidence: 0.0,
			Reason:     "no group result to evaluate",
			Action:     ActionBlock,
			Timestamp:  time.Now(),
		}, nil
	}

	// Type assert the group result
	// Since we're using any to avoid import cycles, we need reflection or type switches
	// For now, we'll extract the data we need using reflection-like approach

	// Create GroupResultData from any
	groupResultData := &consensus.GroupResultData{
		Results: make(map[string]*consensus.AgentResultData),
	}

	// Try to extract fields using type assertion to map
	// This is a workaround - in production, you'd use proper type conversion
	if resultMap, ok := evalCtx.GroupResult.(map[string]any); ok {
		if id, ok := resultMap["group_id"].(string); ok {
			groupResultData.GroupID = id
		}
		if name, ok := resultMap["group_name"].(string); ok {
			groupResultData.GroupName = name
		}
		if status, ok := resultMap["status"].(string); ok {
			groupResultData.Status = status
		}
		// Extract results...
	}

	// For now, return a placeholder since we can't properly access the interface
	// This will be fixed when we properly structure the type conversion

	// Build consensus config from policy
	consensusConfig := g.buildConsensusConfig()

	// Evaluate consensus
	result, err := g.registry.Evaluate(ctx, groupResultData, consensusConfig)
	if err != nil {
		return nil, fmt.Errorf("consensus evaluation failed: %w", err)
	}

	// Determine if gate allows progression
	threshold := g.GetPolicyFloat("threshold", 0.7)
	allow := result.Reached && result.Confidence >= threshold

	// Determine action
	actionConfig := g.DetermineAction(allow, false)

	// Build gate decision
	decision := &GateDecision{
		Allow:      allow,
		Confidence: result.Confidence,
		Action:     actionConfig.Action,
		Timestamp:  time.Now(),
		Metadata: map[string]any{
			"consensus_result": result,
			"method":           result.Method,
			"agreements":       result.Agreements,
			"conflicts":        result.Conflicts,
		},
	}

	if allow {
		decision.Reason = fmt.Sprintf("consensus reached: %s (confidence: %.2f)", result.Summary, result.Confidence)
	} else if !result.Reached {
		decision.Reason = fmt.Sprintf("consensus not reached: %s", result.Summary)
	} else {
		decision.Reason = fmt.Sprintf("consensus confidence %.2f below threshold %.2f", result.Confidence, threshold)
	}

	// Add synthesis to metadata if available
	if result.Synthesis != "" {
		decision.Metadata["synthesis"] = result.Synthesis
	}

	return decision, nil
}

// buildConsensusConfig builds consensus config from gate policy
func (g *ConsensusGate) buildConsensusConfig() consensus.ConsensusConfig {
	config := consensus.ConsensusConfig{
		Method:            g.GetPolicyString("method", "voting"),
		Threshold:         g.GetPolicyFloat("threshold", 0.7),
		MinAgreementRatio: g.GetPolicyFloat("min_agreement_ratio", 0.66),
	}

	// Extract evaluation criteria
	if criteria, ok := g.policy["evaluation"].([]any); ok {
		config.EvaluationCriteria = make([]string, 0, len(criteria))
		for _, c := range criteria {
			if criterion, ok := c.(string); ok {
				config.EvaluationCriteria = append(config.EvaluationCriteria, criterion)
			}
		}
	}

	// Extract meta agent config
	if metaAgent, ok := g.policy["meta_agent"].(map[string]any); ok {
		config.MetaAgent = metaAgent
	}

	// Extract custom config
	if customConfig, ok := g.policy["custom_config"].(map[string]any); ok {
		config.CustomConfig = customConfig
	}

	return config
}

// Register consensus gate factory
func init() {
	GlobalGateRegistry.RegisterFactory(GateTypeConsensus, NewConsensusGate)
}
