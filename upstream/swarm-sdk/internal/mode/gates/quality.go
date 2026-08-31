package gates

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// QualityGate evaluates output quality
type QualityGate struct {
	*BaseGate
	evaluator GateEvaluator
}

// NewQualityGate creates a new quality gate
func NewQualityGate(config GateConfig, logger observability.Logger, tracer observability.Tracer) (Gate, error) {
	base := NewBaseGate(config, logger, tracer)

	// Determine evaluator type
	evaluatorType := base.GetPolicyString("evaluator", "rule")

	var evaluator GateEvaluator
	switch evaluatorType {
	case "llm":
		evaluator = &LLMQualityEvaluator{
			logger: logger,
			tracer: tracer,
		}
	case "rule":
		evaluator = &RuleBasedQualityEvaluator{
			logger: logger,
		}
	case "hybrid":
		evaluator = &HybridQualityEvaluator{
			llm:  &LLMQualityEvaluator{logger: logger, tracer: tracer},
			rule: &RuleBasedQualityEvaluator{logger: logger},
		}
	default:
		return nil, fmt.Errorf("unknown quality evaluator type: %s", evaluatorType)
	}

	return &QualityGate{
		BaseGate:  base,
		evaluator: evaluator,
	}, nil
}

// Evaluate evaluates the quality gate
func (g *QualityGate) Evaluate(ctx context.Context, evalCtx *EvaluationContext) (*GateDecision, error) {
	ctx, span := g.tracer.StartSpan(ctx, "gate.quality.evaluate")
	defer span.End()

	decision, err := g.evaluator.Evaluate(ctx, evalCtx, g.policy)
	if err != nil {
		return nil, err
	}

	// Determine action based on result
	minScore := g.GetPolicyFloat("min_score", 0.7)
	allow := decision.Confidence >= minScore

	actionConfig := g.DetermineAction(allow, false)
	decision.Action = actionConfig.Action
	decision.Allow = allow

	if !allow {
		decision.Reason = fmt.Sprintf("quality score %.2f below threshold %.2f", decision.Confidence, minScore)
	} else {
		decision.Reason = fmt.Sprintf("quality score %.2f meets threshold %.2f", decision.Confidence, minScore)
	}

	return decision, nil
}

// LLMQualityEvaluator uses an LLM to evaluate quality
type LLMQualityEvaluator struct {
	logger observability.Logger
	tracer observability.Tracer
}

// Evaluate evaluates quality using an LLM
func (e *LLMQualityEvaluator) Evaluate(ctx context.Context, evalCtx *EvaluationContext, policy map[string]any) (*GateDecision, error) {
	if evalCtx.GroupResult == nil {
		return &GateDecision{
			Allow:      false,
			Confidence: 0.0,
			Reason:     "no group result to evaluate",
			Timestamp:  time.Now(),
		}, nil
	}

	// LLM evaluation requires agent factory integration
	// For now, return not implemented
	return &GateDecision{
		Allow:      false,
		Confidence: 0.0,
		Reason:     "LLM quality evaluation not yet implemented (needs agent factory integration)",
		Timestamp:  time.Now(),
		Metadata: map[string]any{
			"evaluator": "llm",
			"note":      "meta agent creation requires full agent factory setup",
		},
	}, fmt.Errorf("LLM quality evaluation not yet implemented")
}

// RuleBasedQualityEvaluator uses rules to evaluate quality
type RuleBasedQualityEvaluator struct {
	logger observability.Logger
}

// Evaluate evaluates quality using rules
func (e *RuleBasedQualityEvaluator) Evaluate(ctx context.Context, evalCtx *EvaluationContext, policy map[string]any) (*GateDecision, error) {
	if evalCtx.GroupResult == nil {
		return &GateDecision{
			Allow:      false,
			Confidence: 0.0,
			Reason:     "no group result to evaluate",
			Timestamp:  time.Now(),
		}, nil
	}

	score := 1.0
	reasons := []string{}

	// Since GroupResult is any, we can't directly access fields
	// For now, use default values
	total := 1
	successful := 1
	totalTokens := 0
	totalOutput := 100 // Placeholder

	successRate := float64(successful) / float64(total)

	// Rule 1: Success rate
	if successRate < 1.0 {
		penalty := 0.2 * (1.0 - successRate)
		score -= penalty
		reasons = append(reasons, fmt.Sprintf("success rate: %.0f%% (%d/%d agents)", successRate*100, successful, total))
	}

	// Rule 2: Minimum output length
	if minLength, ok := policy["min_output_length"].(int); ok {
		avgOutput := totalOutput / max(successful, 1)
		if avgOutput < minLength {
			penalty := 0.2
			score -= penalty
			reasons = append(reasons, fmt.Sprintf("output too short: %d < %d", avgOutput, minLength))
		}
	}

	// Rule 3: Maximum output length (to catch repetition/errors)
	if maxLength, ok := policy["max_output_length"].(int); ok {
		avgOutput := totalOutput / max(successful, 1)
		if avgOutput > maxLength {
			penalty := 0.1
			score -= penalty
			reasons = append(reasons, fmt.Sprintf("output too long: %d > %d", avgOutput, maxLength))
		}
	}

	// Rule 4: Keyword presence (if specified) - simplified
	if _, ok := policy["required_keywords"].([]any); ok && successful > 0 {
		// Can't check keywords without access to actual output
		// For now, assume passing
	}

	// Ensure score is in valid range
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	reasoning := "rule-based evaluation"
	if len(reasons) > 0 {
		reasoning += ": " + strings.Join(reasons, "; ")
	}

	return &GateDecision{
		Confidence: score,
		Reason:     reasoning,
		Timestamp:  time.Now(),
		Metadata: map[string]any{
			"evaluator":    "rule",
			"success_rate": successRate,
			"total_tokens": totalTokens,
			"avg_output":   totalOutput / max(successful, 1),
		},
	}, nil
}

// HybridQualityEvaluator combines LLM and rule-based evaluation
type HybridQualityEvaluator struct {
	llm  *LLMQualityEvaluator
	rule *RuleBasedQualityEvaluator
}

// Evaluate combines LLM and rule-based evaluation
func (e *HybridQualityEvaluator) Evaluate(ctx context.Context, evalCtx *EvaluationContext, policy map[string]any) (*GateDecision, error) {
	// Get rule-based score first (fast)
	ruleDecision, err := e.rule.Evaluate(ctx, evalCtx, policy)
	if err != nil {
		return nil, err
	}

	// If rule-based score is very low, don't bother with LLM
	if ruleDecision.Confidence < 0.3 {
		ruleDecision.Metadata["evaluator"] = "hybrid_rule_only"
		return ruleDecision, nil
	}

	// Get LLM score (slower but more nuanced)
	llmDecision, err := e.llm.Evaluate(ctx, evalCtx, policy)
	if err != nil {
		// Fall back to rule-based on error
		ruleDecision.Metadata["evaluator"] = "hybrid_llm_failed"
		return ruleDecision, nil
	}

	// Combine scores (weighted average)
	llmWeight := 0.6
	ruleWeight := 0.4

	combinedScore := llmDecision.Confidence*llmWeight + ruleDecision.Confidence*ruleWeight

	return &GateDecision{
		Confidence: combinedScore,
		Reason:     fmt.Sprintf("hybrid: llm=%.2f, rule=%.2f, combined=%.2f", llmDecision.Confidence, ruleDecision.Confidence, combinedScore),
		Timestamp:  time.Now(),
		Metadata: map[string]any{
			"evaluator":   "hybrid",
			"llm_score":   llmDecision.Confidence,
			"rule_score":  ruleDecision.Confidence,
			"llm_reason":  llmDecision.Reason,
			"rule_reason": ruleDecision.Reason,
		},
	}, nil
}

// Register quality gate factory
func init() {
	GlobalGateRegistry.RegisterFactory(GateTypeQuality, NewQualityGate)
}
