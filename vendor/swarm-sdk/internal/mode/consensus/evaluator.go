// Package consensus provides mechanisms for evaluating agent agreement
package consensus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// GroupResultData contains the data needed from a group result for consensus evaluation
// This avoids circular dependencies by not importing the mode package
type GroupResultData struct {
	GroupID   string
	GroupName string
	Status    string
	Results   map[string]*AgentResultData
}

// AgentResultData contains data from a single agent execution
type AgentResultData struct {
	AgentID   string
	AgentName string
	Output    string
	Error     error
	Tokens    int
	Cost      float64
}

// ConsensusEvaluator evaluates consensus among agent outputs
type ConsensusEvaluator interface {
	// Evaluate determines if consensus is reached
	Evaluate(ctx context.Context, groupResult *GroupResultData, config ConsensusConfig) (*ConsensusResult, error)

	// Method returns the consensus method name
	Method() string
}

// ConsensusConfig configures consensus evaluation
type ConsensusConfig struct {
	// Method specifies the consensus evaluation method
	Method string `yaml:"method"`

	// Threshold for consensus (0.0 to 1.0)
	Threshold float64 `yaml:"threshold"`

	// MinAgreementRatio is the minimum fraction of agents that must agree
	MinAgreementRatio float64 `yaml:"min_agreement_ratio"`

	// EvaluationCriteria for structured consensus
	EvaluationCriteria []string `yaml:"evaluation_criteria,omitempty"`

	// MetaAgent configuration for LLM-based consensus
	MetaAgent map[string]any `yaml:"meta_agent,omitempty"`

	// CustomConfig for method-specific configuration
	CustomConfig map[string]any `yaml:"custom_config,omitempty"`
}

// ConsensusResult contains the result of consensus evaluation
type ConsensusResult struct {
	// Reached indicates if consensus was achieved
	Reached bool `json:"reached"`

	// Confidence in the consensus (0.0 to 1.0)
	Confidence float64 `json:"confidence"`

	// Method used for evaluation
	Method string `json:"method"`

	// Summary of the consensus
	Summary string `json:"summary"`

	// Agreements are points where agents agree
	Agreements []string `json:"agreements"`

	// Conflicts are points where agents disagree
	Conflicts []string `json:"conflicts"`

	// Synthesis is the unified output (if applicable)
	Synthesis string `json:"synthesis,omitempty"`

	// Clusters groups similar agent outputs
	Clusters []ConsensusCluster `json:"clusters,omitempty"`

	// Metadata contains method-specific data
	Metadata map[string]any `json:"metadata,omitempty"`

	// Timestamp
	Timestamp time.Time `json:"timestamp"`
}

// ConsensusCluster represents a group of similar agent outputs
type ConsensusCluster struct {
	// AgentIDs in this cluster
	AgentIDs []string `json:"agent_ids"`

	// Centroid output representative of the cluster
	Centroid string `json:"centroid"`

	// Size is the number of agents in the cluster
	Size int `json:"size"`

	// Confidence in this cluster
	Confidence float64 `json:"confidence"`
}

// VotingConsensusEvaluator implements voting-based consensus
type VotingConsensusEvaluator struct {
	logger observability.Logger
	tracer observability.Tracer
}

// NewVotingConsensusEvaluator creates a new voting evaluator
func NewVotingConsensusEvaluator(logger observability.Logger, tracer observability.Tracer) *VotingConsensusEvaluator {
	return &VotingConsensusEvaluator{
		logger: logger,
		tracer: tracer,
	}
}

// Method returns the method name
func (e *VotingConsensusEvaluator) Method() string {
	return "voting"
}

// Evaluate performs voting-based consensus evaluation
func (e *VotingConsensusEvaluator) Evaluate(ctx context.Context, groupResult *GroupResultData, config ConsensusConfig) (*ConsensusResult, error) {
	ctx, span := e.tracer.StartSpan(ctx, "consensus.voting.evaluate")
	defer span.End()

	// Extract successful outputs
	outputs := extractSuccessfulOutputsFromData(groupResult)
	if len(outputs) == 0 {
		return &ConsensusResult{
			Reached:    false,
			Confidence: 0.0,
			Method:     "voting",
			Summary:    "no successful agent outputs to evaluate",
			Timestamp:  time.Now(),
		}, nil
	}

	// Simple voting: cluster by similarity
	clusters := e.clusterOutputs(outputs)

	// Find largest cluster
	largestCluster := clusters[0]
	for _, cluster := range clusters {
		if cluster.Size > largestCluster.Size {
			largestCluster = cluster
		}
	}

	// Calculate agreement ratio
	agreementRatio := float64(largestCluster.Size) / float64(len(outputs))

	// Determine if consensus reached
	minRatio := config.MinAgreementRatio
	if minRatio == 0 {
		minRatio = 0.66 // Default: 2/3 majority
	}

	reached := agreementRatio >= minRatio

	// Build result
	result := &ConsensusResult{
		Reached:    reached,
		Confidence: agreementRatio,
		Method:     "voting",
		Clusters:   clusters,
		Timestamp:  time.Now(),
		Metadata: map[string]any{
			"total_agents":    len(outputs),
			"largest_cluster": largestCluster.Size,
			"agreement_ratio": agreementRatio,
			"required_ratio":  minRatio,
		},
	}

	if reached {
		result.Summary = fmt.Sprintf("Consensus reached: %d/%d agents agree (%.0f%%)",
			largestCluster.Size, len(outputs), agreementRatio*100)
		result.Agreements = []string{
			fmt.Sprintf("%d agents produced similar outputs", largestCluster.Size),
		}
		result.Synthesis = largestCluster.Centroid
	} else {
		result.Summary = fmt.Sprintf("No consensus: largest agreement is %d/%d agents (%.0f%%), need %.0f%%",
			largestCluster.Size, len(outputs), agreementRatio*100, minRatio*100)
		result.Conflicts = []string{
			fmt.Sprintf("Agents split across %d different output clusters", len(clusters)),
		}
	}

	return result, nil
}

// clusterOutputs groups similar outputs together
func (e *VotingConsensusEvaluator) clusterOutputs(outputs map[string]string) []ConsensusCluster {
	// Simple clustering: use exact match or keyword similarity
	// In a real implementation, this would use embeddings

	clusters := []ConsensusCluster{}
	assigned := make(map[string]bool)

	for agentID, output := range outputs {
		if assigned[agentID] {
			continue
		}

		// Start new cluster
		cluster := ConsensusCluster{
			AgentIDs: []string{agentID},
			Centroid: output,
			Size:     1,
		}
		assigned[agentID] = true

		// Find similar outputs
		for otherID, otherOutput := range outputs {
			if assigned[otherID] {
				continue
			}

			// Simple similarity check
			similarity := e.calculateSimilarity(output, otherOutput)
			if similarity > 0.7 {
				cluster.AgentIDs = append(cluster.AgentIDs, otherID)
				cluster.Size++
				assigned[otherID] = true
			}
		}

		cluster.Confidence = float64(cluster.Size) / float64(len(outputs))
		clusters = append(clusters, cluster)
	}

	return clusters
}

// calculateSimilarity computes similarity between two outputs
func (e *VotingConsensusEvaluator) calculateSimilarity(a, b string) float64 {
	// Simple implementation: Jaccard similarity on words
	wordsA := strings.Fields(strings.ToLower(a))
	wordsB := strings.Fields(strings.ToLower(b))

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0.0
	}

	setA := make(map[string]bool)
	for _, word := range wordsA {
		setA[word] = true
	}

	setB := make(map[string]bool)
	for _, word := range wordsB {
		setB[word] = true
	}

	// Calculate intersection and union
	intersection := 0
	for word := range setA {
		if setB[word] {
			intersection++
		}
	}

	union := len(setA) + len(setB) - intersection

	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// SemanticConsensusEvaluator uses semantic similarity (embeddings)
type SemanticConsensusEvaluator struct {
	logger observability.Logger
	tracer observability.Tracer
}

// NewSemanticConsensusEvaluator creates a new semantic evaluator
func NewSemanticConsensusEvaluator(logger observability.Logger, tracer observability.Tracer) *SemanticConsensusEvaluator {
	return &SemanticConsensusEvaluator{
		logger: logger,
		tracer: tracer,
	}
}

// Method returns the method name
func (e *SemanticConsensusEvaluator) Method() string {
	return "semantic_similarity"
}

// Evaluate performs semantic similarity-based consensus evaluation
func (e *SemanticConsensusEvaluator) Evaluate(ctx context.Context, groupResult *GroupResultData, config ConsensusConfig) (*ConsensusResult, error) {
	// TODO: Implement using embeddings API
	// For now, fall back to voting-based approach
	voting := NewVotingConsensusEvaluator(e.logger, e.tracer)
	result, err := voting.Evaluate(ctx, groupResult, config)
	if err != nil {
		return nil, err
	}

	result.Method = "semantic_similarity_fallback"
	result.Metadata["note"] = "using voting fallback, embeddings not yet implemented"

	return result, nil
}

// LLMSynthesisEvaluator uses an LLM to synthesize and evaluate consensus
type LLMSynthesisEvaluator struct {
	logger observability.Logger
	tracer observability.Tracer
}

// NewLLMSynthesisEvaluator creates a new LLM synthesis evaluator
func NewLLMSynthesisEvaluator(logger observability.Logger, tracer observability.Tracer) *LLMSynthesisEvaluator {
	return &LLMSynthesisEvaluator{
		logger: logger,
		tracer: tracer,
	}
}

// Method returns the method name
func (e *LLMSynthesisEvaluator) Method() string {
	return "llm_synthesis"
}

// Evaluate performs LLM-based consensus synthesis and evaluation
func (e *LLMSynthesisEvaluator) Evaluate(ctx context.Context, groupResult *GroupResultData, config ConsensusConfig) (*ConsensusResult, error) {
	ctx, span := e.tracer.StartSpan(ctx, "consensus.llm_synthesis.evaluate")
	defer span.End()

	outputs := extractSuccessfulOutputsFromData(groupResult)
	if len(outputs) == 0 {
		return &ConsensusResult{
			Reached:    false,
			Confidence: 0.0,
			Method:     "llm_synthesis",
			Summary:    "no successful agent outputs to evaluate",
			Timestamp:  time.Now(),
		}, nil
	}

	// Build synthesis prompt
	prompt := e.buildSynthesisPrompt(outputs, config)

	// TODO: Execute meta agent to synthesize outputs
	// For now, return a placeholder result

	return &ConsensusResult{
		Reached:    false,
		Confidence: 0.0,
		Method:     "llm_synthesis",
		Summary:    "LLM synthesis not yet implemented - requires meta agent integration",
		Timestamp:  time.Now(),
		Metadata: map[string]any{
			"note":         "pending meta agent integration",
			"prompt":       prompt,
			"output_count": len(outputs),
		},
	}, nil
}

// buildSynthesisPrompt builds the prompt for LLM synthesis
func (e *LLMSynthesisEvaluator) buildSynthesisPrompt(outputs map[string]string, config ConsensusConfig) string {
	var sb strings.Builder

	sb.WriteString("You are a consensus evaluator analyzing outputs from multiple agents.\n\n")
	sb.WriteString(fmt.Sprintf("You have %d agent outputs to evaluate:\n\n", len(outputs)))

	i := 1
	for agentID, output := range outputs {
		sb.WriteString(fmt.Sprintf("=== Agent %d (%s) ===\n", i, agentID))
		sb.WriteString(output)
		sb.WriteString("\n\n")
		i++
	}

	sb.WriteString("Analyze these outputs and provide:\n")
	sb.WriteString("1. Areas of agreement (what do they all say?)\n")
	sb.WriteString("2. Areas of conflict (where do they disagree?)\n")
	sb.WriteString("3. Confidence in overall consensus (0.0-1.0)\n")
	sb.WriteString("4. A synthesized unified output that captures the consensus\n\n")

	sb.WriteString("Return JSON:\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"consensus_reached\": true/false,\n")
	sb.WriteString("  \"confidence\": 0.0-1.0,\n")
	sb.WriteString("  \"agreements\": [\"point1\", \"point2\"],\n")
	sb.WriteString("  \"conflicts\": [\"conflict1\", \"conflict2\"],\n")
	sb.WriteString("  \"synthesis\": \"unified output text\"\n")
	sb.WriteString("}\n")

	return sb.String()
}

// Helper function to extract successful outputs from GroupResultData
func extractSuccessfulOutputsFromData(groupResult *GroupResultData) map[string]string {
	outputs := make(map[string]string)
	for agentID, result := range groupResult.Results {
		if result.Error == nil && result.Output != "" {
			outputs[agentID] = result.Output
		}
	}
	return outputs
}

// ConsensusEvaluatorRegistry manages consensus evaluators
type ConsensusEvaluatorRegistry struct {
	evaluators map[string]ConsensusEvaluator
}

// NewConsensusEvaluatorRegistry creates a new registry
func NewConsensusEvaluatorRegistry(logger observability.Logger, tracer observability.Tracer) *ConsensusEvaluatorRegistry {
	registry := &ConsensusEvaluatorRegistry{
		evaluators: make(map[string]ConsensusEvaluator),
	}

	// Register built-in evaluators
	registry.Register("voting", NewVotingConsensusEvaluator(logger, tracer))
	registry.Register("semantic_similarity", NewSemanticConsensusEvaluator(logger, tracer))
	registry.Register("llm_synthesis", NewLLMSynthesisEvaluator(logger, tracer))

	return registry
}

// Register registers a consensus evaluator
func (r *ConsensusEvaluatorRegistry) Register(method string, evaluator ConsensusEvaluator) {
	r.evaluators[method] = evaluator
}

// Get retrieves a consensus evaluator
func (r *ConsensusEvaluatorRegistry) Get(method string) (ConsensusEvaluator, error) {
	evaluator, ok := r.evaluators[method]
	if !ok {
		return nil, fmt.Errorf("unknown consensus method: %s", method)
	}
	return evaluator, nil
}

// Evaluate evaluates consensus using the specified method
func (r *ConsensusEvaluatorRegistry) Evaluate(ctx context.Context, groupResult *GroupResultData, config ConsensusConfig) (*ConsensusResult, error) {
	method := config.Method
	if method == "" {
		method = "voting" // Default
	}

	evaluator, err := r.Get(method)
	if err != nil {
		return nil, err
	}

	return evaluator.Evaluate(ctx, groupResult, config)
}

// GlobalConsensusRegistry is the global consensus evaluator registry
var GlobalConsensusRegistry *ConsensusEvaluatorRegistry

// InitGlobalConsensusRegistry initializes the global registry
func InitGlobalConsensusRegistry(logger observability.Logger, tracer observability.Tracer) {
	GlobalConsensusRegistry = NewConsensusEvaluatorRegistry(logger, tracer)
}
