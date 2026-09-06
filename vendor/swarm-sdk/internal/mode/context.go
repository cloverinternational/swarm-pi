package mode

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxContextChars is the maximum character length for merged group context.
// ~100k chars ≈ ~25-30k tokens, leaving room for the agent's system prompt
// and response within typical context windows (128k-200k tokens).
const maxContextChars = 100000

// maxPerAgentChars is the maximum character length per agent output when merging.
const maxPerAgentChars = 40000

// ContextBuilder builds input context for a group from dependent group outputs.
// It supports multiple context sources and merging strategies.
type ContextBuilder struct {
	workflowInput string
	groupResults  map[string]*GroupResult
}

// NewContextBuilder creates a new context builder
func NewContextBuilder(workflowInput string, groupResults map[string]*GroupResult) *ContextBuilder {
	return &ContextBuilder{
		workflowInput: workflowInput,
		groupResults:  groupResults,
	}
}

// BuildForGroup builds the input context for a group based on its dependencies
func (cb *ContextBuilder) BuildForGroup(group *AgentGroup) string {
	// If no dependencies, use workflow input
	if len(group.DependsOn) == 0 {
		return cb.workflowInput
	}

	// Build context from dependencies
	return cb.mergeContextFromDependencies(group.DependsOn)
}

// BuildForAgent builds context for an agent based on its context_sources
func (cb *ContextBuilder) BuildForAgent(agentDef any, groupContext string) string {
	// TODO: Implement context_sources parsing from agent definition
	// For now, just use group context
	return groupContext
}

// mergeContextFromDependencies merges outputs from multiple dependent groups
func (cb *ContextBuilder) mergeContextFromDependencies(dependencies []string) string {
	if len(dependencies) == 0 {
		return cb.workflowInput
	}

	// Single dependency - use its output directly
	if len(dependencies) == 1 {
		return cb.getSingleGroupOutput(dependencies[0])
	}

	// Multiple dependencies - merge them
	return cb.mergeMultipleGroupOutputs(dependencies)
}

// getSingleGroupOutput gets the output from a single group
func (cb *ContextBuilder) getSingleGroupOutput(groupID string) string {
	result, exists := cb.groupResults[groupID]
	if !exists {
		return cb.workflowInput
	}

	// 1. Try explicit group output first (set by coordinator or synthesis)
	if result.Output != "" {
		return truncateContext(result.Output, maxContextChars)
	}

	// 2. Fallback: Use first successful agent output
	for _, agentResult := range result.Results {
		if agentResult.Error == nil && agentResult.Output != "" {
			return truncateContext(agentResult.Output, maxContextChars)
		}
	}

	// Fallback to workflow input if no successful output
	return cb.workflowInput
}

// truncateContext truncates text to maxRunes, adding a marker if truncated.
func truncateContext(text string, maxRunes int) string {
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	return string([]rune(text)[:maxRunes]) +
		"\n\n[... output truncated to fit within model context window ...]"
}

// mergeMultipleGroupOutputs merges outputs from multiple groups
func (cb *ContextBuilder) mergeMultipleGroupOutputs(groupIDs []string) string {
	var sections []string

	// Add original input as context
	if cb.workflowInput != "" {
		sections = append(sections, fmt.Sprintf("# Original Request\n\n%s", cb.workflowInput))
	}

	// Add each group's output as a section
	for _, groupID := range groupIDs {
		result, exists := cb.groupResults[groupID]
		if !exists {
			continue
		}

		groupSection := cb.formatGroupOutput(groupID, result)
		if groupSection != "" {
			sections = append(sections, groupSection)
		}
	}

	// Join all sections
	merged := strings.Join(sections, "\n\n---\n\n")

	// Truncate total merged context to prevent context window overflow
	if utf8.RuneCountInString(merged) > maxContextChars {
		merged = string([]rune(merged)[:maxContextChars]) +
			"\n\n[... context truncated to fit within model context window ...]"
	}

	return merged
}

// formatGroupOutput formats a group's output for inclusion in merged context
func (cb *ContextBuilder) formatGroupOutput(groupID string, result *GroupResult) string {
	if result == nil {
		return ""
	}

	var parts []string

	// Add group header
	header := fmt.Sprintf("# Output from %s", result.GroupName)
	if result.GroupID != "" {
		header += fmt.Sprintf(" (%s)", result.GroupID)
	}
	parts = append(parts, header)

	// Include consensus summary if available
	if result.Consensus != nil && result.Consensus.Reached {
		parts = append(parts, fmt.Sprintf("\n**Consensus**: %s (Confidence: %.0f%%)",
			result.Consensus.Summary, result.Consensus.Confidence*100))
	}

	// 1. Use explicit group output if available
	if result.Output != "" {
		parts = append(parts, "", result.Output)
		return strings.Join(parts, "\n")
	}

	// 2. Fallback: Strategy depends on number of agents
	agentCount := len(result.Results)

	if agentCount == 0 {
		return ""
	} else if agentCount == 1 {
		// Single agent - use output directly
		for _, agentResult := range result.Results {
			if agentResult.Error == nil && agentResult.Output != "" {
				parts = append(parts, "", agentResult.Output)
			}
		}
	} else {
		// Multiple agents - show each agent's contribution (truncated to avoid context overflow)
		parts = append(parts, "")
		for agentID, agentResult := range result.Results {
			if agentResult.Error != nil {
				continue
			}

			if agentResult.Output != "" {
				agentHeader := fmt.Sprintf("## %s", agentResult.AgentName)
				if agentID != "" {
					agentHeader += fmt.Sprintf(" (%s)", agentID)
				}
				output := agentResult.Output
				if utf8.RuneCountInString(output) > maxPerAgentChars {
					output = string([]rune(output)[:maxPerAgentChars]) +
						"\n\n[... output truncated for context limit ...]"
				}
				parts = append(parts, agentHeader, "", output, "")
			}
		}
	}

	return strings.Join(parts, "\n")
}

// MergeStrategy defines how to merge outputs
type MergeStrategy string

const (
	// MergeStrategyFirst uses the first successful output
	MergeStrategyFirst MergeStrategy = "first"

	// MergeStrategyConcat concatenates all outputs
	MergeStrategyConcat MergeStrategy = "concat"

	// MergeStrategyStructured creates structured sections for each output
	MergeStrategyStructured MergeStrategy = "structured"

	// MergeStrategySynthesis would use LLM to synthesize outputs (future)
	MergeStrategySynthesis MergeStrategy = "synthesis"
)

// ExtractContextSources parses context_sources from agent metadata
// Example: ["groups.planning.output", "groups.execution.agents.executor.output"]
func ExtractContextSources(metadata map[string]any) []string {
	if metadata == nil {
		return nil
	}

	sources, ok := metadata["context_sources"]
	if !ok {
		return nil
	}

	// Handle different types
	switch v := sources.(type) {
	case []string:
		return v
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	default:
		return nil
	}
}

// ResolveContextSource resolves a context source reference to actual content
// Supports:
// - "groups.GROUP_ID.output" - entire group output
// - "groups.GROUP_ID.agents.AGENT_ID.output" - specific agent output
// - "groups.GROUP_ID.consensus" - consensus summary
func ResolveContextSource(source string, groupResults map[string]*GroupResult) string {
	parts := strings.Split(source, ".")

	if len(parts) < 3 || parts[0] != "groups" {
		return ""
	}

	groupID := parts[1]
	result, exists := groupResults[groupID]
	if !exists {
		return ""
	}

	// Handle different reference types
	if len(parts) == 3 && parts[2] == "output" {
		// groups.GROUP_ID.output - return full group output
		builder := &ContextBuilder{groupResults: groupResults}
		return builder.formatGroupOutput(groupID, result)
	}

	if len(parts) == 3 && parts[2] == "consensus" {
		// groups.GROUP_ID.consensus - return consensus summary
		if result.Consensus != nil {
			return result.Consensus.Summary
		}
		return ""
	}

	if len(parts) >= 5 && parts[2] == "agents" {
		// groups.GROUP_ID.agents.AGENT_ID.output
		agentID := parts[3]
		if agentResult, exists := result.Results[agentID]; exists {
			if parts[4] == "output" {
				return agentResult.Output
			}
		}
	}

	return ""
}
