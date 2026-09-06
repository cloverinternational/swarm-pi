package steering

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// LLMEvaluatorConfig configures the LLM evaluator
type LLMEvaluatorConfig struct {
	Provider  provider.Provider
	Model     string
	MaxTokens int
}

// LLMEvaluator uses an LLM to evaluate steering decisions
type LLMEvaluator struct {
	config *LLMEvaluatorConfig
	rules  []SteeringRule
}

// SteeringRule defines a rule-based steering condition
type SteeringRule struct {
	ID          string
	Name        string
	Description string
	Condition   func(toolCall conversation.ToolCall, conv *conversation.Conversation) (bool, string)
	Action      DecisionType
	Priority    int
}

// NewLLMEvaluator creates a new LLM-based evaluator
func NewLLMEvaluator(cfg *LLMEvaluatorConfig) *LLMEvaluator {
	return &LLMEvaluator{
		config: cfg,
		rules:  DefaultSteeringRules(),
	}
}

// Evaluate evaluates a tool call using LLM
func (e *LLMEvaluator) Evaluate(ctx context.Context, toolCall conversation.ToolCall, conv *conversation.Conversation) (*SteeringDecision, error) {
	// 1. Check rules first (fast path, no LLM call)
	for _, rule := range e.rules {
		if triggered, reason := rule.Condition(toolCall, conv); triggered {
			return &SteeringDecision{
				Type:      rule.Action,
				Reasoning: fmt.Sprintf("Rule '%s': %s", rule.Name, reason),
				Timestamp: time.Now(),
			}, nil
		}
	}

	// 2. Skip LLM evaluation if not configured
	if e.config == nil || e.config.Provider == nil {
		return &SteeringDecision{Type: DecisionApprove}, nil
	}

	// 3. Build evaluation prompt
	prompt := e.buildPrompt(toolCall, conv)

	// 4. Call LLM
	req := &provider.ChatRequest{
		Model: e.config.Model,
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: prompt},
		},
		MaxTokens: &[]int{e.config.MaxTokens}[0],
	}

	resp, err := e.config.Provider.Chat(ctx, *req)
	if err != nil {
		return nil, fmt.Errorf("LLM evaluation failed: %w", err)
	}

	// 5. Parse response
	decision := e.parseResponse(resp.Message.Content)
	decision.Timestamp = time.Now()

	return decision, nil
}

func (e *LLMEvaluator) buildPrompt(toolCall conversation.ToolCall, conv *conversation.Conversation) string {
	return fmt.Sprintf(`Evaluate this tool call for steering:

Tool: %s
Input: %v

Decide: APPROVE, BLOCK, or MODIFY
Respond in JSON: {"action": "APPROVE|BLOCK|MODIFY", "reason": "..."}`, toolCall.Name, toolCall.Parameters)
}

func (e *LLMEvaluator) parseResponse(response string) *SteeringDecision {
	decision := &SteeringDecision{Reasoning: response}

	var parsed struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}

	if err := json.Unmarshal([]byte(response), &parsed); err == nil {
		decision.Reasoning = parsed.Reason
		switch strings.ToUpper(parsed.Action) {
		case "BLOCK":
			decision.Type = DecisionBlock
		case "MODIFY":
			decision.Type = DecisionModify
		default:
			decision.Type = DecisionApprove
		}
		return decision
	}

	// Fallback to keyword detection
	upper := strings.ToUpper(response)
	if strings.Contains(upper, "BLOCK") {
		decision.Type = DecisionBlock
	} else {
		decision.Type = DecisionApprove
	}

	return decision
}

// DefaultSteeringRules returns default steering rules
func DefaultSteeringRules() []SteeringRule {
	return []SteeringRule{
		{
			ID:          "plan-mode-writes",
			Name:        "Block writes in PLAN mode",
			Description: "Prevent write operations during planning phase",
			Priority:    100,
			Action:      DecisionBlock,
			Condition: func(toolCall conversation.ToolCall, conv *conversation.Conversation) (bool, string) {
				if conv.Mode == "PLAN" {
					writeTools := map[string]bool{
						"write_file": true, "create_file": true, "delete_file": true,
						"str_replace": true, "execute_shell": true,
					}
					if writeTools[toolCall.Name] {
						return true, "Write operations blocked in PLAN mode"
					}
				}
				return false, ""
			},
		},
	}
}
