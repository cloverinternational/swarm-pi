package steering

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/steering/efficiency"
)

// DecisionType defines what action the steering decided
type DecisionType string

const (
	DecisionApprove DecisionType = "approve"
	DecisionBlock   DecisionType = "block"
	DecisionRetry   DecisionType = "retry"
	DecisionModify  DecisionType = "modify"
)

// SteeringDecision represents a steering decision
type SteeringDecision struct {
	Type      DecisionType
	Reasoning string
	Feedback  string
	Modified  any
	Timestamp time.Time
}

// Config configures steering behavior
type Config struct {
	// Enable/disable steering
	Enabled bool

	// Model name for LLM-based evaluation (uses cheap model like haiku)
	ModelName string

	// Provider for LLM calls (optional)
	Provider provider.Provider

	// Mode-based steering (PLAN mode blocks writes)
	ModeBased bool

	// Efficiency analysis settings
	EnableEfficiency bool
	MaxFileReads     int

	// Logging
	LogDecisions bool
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Enabled:          true,
		ModeBased:        true,
		EnableEfficiency: true,
		MaxFileReads:     3,
		LogDecisions:     true,
	}
}

// Steering provides the main steering implementation
type Steering struct {
	config             *Config
	evaluator          *LLMEvaluator
	efficiencyAnalyzer *efficiency.Analyzer
	log                *log.Logger
	mu                 sync.RWMutex

	// Decision history
	decisions []*SteeringDecision
}

// New creates a new steering instance
func New(cfg *Config) (*Steering, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	s := &Steering{
		config:             cfg,
		efficiencyAnalyzer: efficiency.NewAnalyzer(cfg.MaxFileReads),
		log:                log.New(os.Stdout, "[steering] ", log.LstdFlags),
		decisions:          make([]*SteeringDecision, 0),
	}

	// Setup LLM evaluator if model name and provider provided
	if cfg.ModelName != "" && cfg.Provider != nil {
		s.evaluator = NewLLMEvaluator(&LLMEvaluatorConfig{
			Provider:  cfg.Provider,
			Model:     cfg.ModelName,
			MaxTokens: 500,
		})
		s.log.Printf("Using model %s for steering evaluation", cfg.ModelName)
	}

	s.log.Println("Steering initialized")
	return s, nil
}

// EvaluateToolCall evaluates a tool call for steering
func (s *Steering) EvaluateToolCall(ctx context.Context, toolName string, toolInput map[string]any, conv *conversation.Conversation) (*SteeringDecision, error) {
	s.mu.RLock()
	enabled := s.config.Enabled
	modeBased := s.config.ModeBased
	enableEfficiency := s.config.EnableEfficiency
	s.mu.RUnlock()

	if !enabled {
		return &SteeringDecision{Type: DecisionApprove}, nil
	}

	// Build tool call struct
	toolCall := conversation.ToolCall{
		Name:       toolName,
		Parameters: toolInput,
	}

	// 1. Mode-based steering (fast path, no LLM)
	if modeBased && conv != nil {
		if conv.Mode == "PLAN" {
			writeTools := map[string]bool{
				"write_file": true, "create_file": true, "delete_file": true,
				"str_replace": true, "execute_shell": true,
			}
			if writeTools[toolName] {
				decision := &SteeringDecision{
					Type:      DecisionBlock,
					Reasoning: "Write operations blocked in PLAN mode",
					Timestamp: time.Now(),
				}
				s.recordDecision(decision)
				return decision, nil
			}
		}
	}

	// 2. Efficiency analysis
	if enableEfficiency {
		result := s.efficiencyAnalyzer.AnalyzeToolCall(ctx, toolName, toolInput)
		if len(result.Patterns) > 0 {
			s.log.Printf("Efficiency patterns: %+v", result.Patterns)
		}
	}

	// 3. LLM-based evaluation (if configured)
	if s.evaluator != nil {
		decision, err := s.evaluator.Evaluate(ctx, toolCall, conv)
		if err != nil {
			s.log.Printf("LLM evaluation error: %v", err)
			// Fail-open: continue on error
			return &SteeringDecision{Type: DecisionApprove}, nil
		}
		if decision != nil && decision.Type != DecisionApprove {
			s.recordDecision(decision)
			return decision, nil
		}
	}

	// Default: approve
	return &SteeringDecision{Type: DecisionApprove}, nil
}

func (s *Steering) recordDecision(decision *SteeringDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.decisions = append(s.decisions, decision)

	if s.config.LogDecisions {
		s.log.Printf("Decision: %s - %s", decision.Type, decision.Reasoning)
	}
}

// SetEnabled enables or disables steering
func (s *Steering) SetEnabled(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.Enabled = enabled
	s.log.Printf("Steering enabled: %v", enabled)
}

// IsEnabled returns whether steering is enabled
func (s *Steering) IsEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Enabled
}

// GetEfficiencyScore returns the current efficiency score (1-5)
func (s *Steering) GetEfficiencyScore() float64 {
	return s.efficiencyAnalyzer.GetScore()
}

// GetDecisions returns recent steering decisions
func (s *Steering) GetDecisions() []*SteeringDecision {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*SteeringDecision, len(s.decisions))
	copy(result, s.decisions)
	return result
}

// Reset clears the steering state
func (s *Steering) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.efficiencyAnalyzer.Reset()
	s.decisions = make([]*SteeringDecision, 0)
	s.log.Println("Steering state reset")
}
