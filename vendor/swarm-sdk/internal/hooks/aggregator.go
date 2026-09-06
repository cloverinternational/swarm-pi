// Package hooks implements the event hook system.
// This file provides the HookAggregator for merging hook results.
package hooks

import (
	"sort"
	"strings"
	"time"
)

// HookAggregator merges results from multiple hooks.
type HookAggregator struct{}

// NewHookAggregator creates a new hook aggregator.
func NewHookAggregator() *HookAggregator {
	return &HookAggregator{}
}

// HookExecutionResult represents the result of executing a single hook.
type HookExecutionResult struct {
	HookConfig CommandHookConfig
	EventName  HookEventName
	Success    bool
	Output     *HookResponse
	Stdout     string
	Stderr     string
	ExitCode   int
	Duration   time.Duration
	Error      error
}

// AggregatedHookResult represents the merged result of all hooks.
type AggregatedHookResult struct {
	Success       bool
	FinalOutput   *HookResponse
	AllOutputs    []*HookResponse
	Errors        []error
	TotalDuration time.Duration
}

// AggregateResults merges multiple hook results based on event-specific rules.
func (a *HookAggregator) AggregateResults(results []HookExecutionResult, eventName HookEventName) *AggregatedHookResult {
	if len(results) == 0 {
		return &AggregatedHookResult{
			Success:     true,
			FinalOutput: &HookResponse{Decision: DecisionAllow},
		}
	}

	aggregated := &AggregatedHookResult{
		Success:     true,
		AllOutputs:  make([]*HookResponse, 0, len(results)),
		Errors:      make([]error, 0),
		FinalOutput: &HookResponse{Decision: DecisionAllow},
	}

	// Collect all outputs and errors
	for _, result := range results {
		aggregated.TotalDuration += result.Duration

		if result.Error != nil {
			aggregated.Errors = append(aggregated.Errors, result.Error)
		}

		if !result.Success {
			aggregated.Success = false
		}

		if result.Output != nil {
			aggregated.AllOutputs = append(aggregated.AllOutputs, result.Output)
		}
	}

	// Apply event-specific aggregation strategy
	switch eventName {
	case EventBeforeTool, EventAfterTool, EventBeforeAgent, EventAfterAgent, EventSessionStart:
		a.aggregateWithORLogic(aggregated)
	case EventBeforeModel, EventAfterModel:
		a.aggregateWithFieldReplacement(aggregated)
	case EventBeforeToolSelection:
		a.aggregateWithUnionLogic(aggregated)
	default:
		a.aggregateWithORLogic(aggregated)
	}

	return aggregated
}

// aggregateWithORLogic uses OR logic for decisions.
// Any blocking decision blocks. Messages are concatenated.
func (a *HookAggregator) aggregateWithORLogic(result *AggregatedHookResult) {
	var systemMessages []string
	var additionalContexts []string
	suppressOutput := false

	for _, output := range result.AllOutputs {
		// Check for blocking decisions
		if output.Decision.IsBlocking() {
			result.FinalOutput.Decision = output.Decision
			result.FinalOutput.Reason = output.Reason
			result.Success = false
			return
		}

		// Check for ask decisions
		if output.Decision.NeedsConfirmation() && result.FinalOutput.Decision != DecisionDeny && result.FinalOutput.Decision != DecisionBlock {
			result.FinalOutput.Decision = DecisionAsk
			result.FinalOutput.Reason = output.Reason
		}

		// Collect system messages
		if output.SystemMessage != "" {
			systemMessages = append(systemMessages, output.SystemMessage)
		}

		// Collect additional context
		if ctx := output.GetAdditionalContext(); ctx != "" {
			additionalContexts = append(additionalContexts, ctx)
		}

		// Any suppressOutput=true suppresses all
		if output.SuppressOutput {
			suppressOutput = true
		}

		// Check for stop request
		if !output.ShouldContinue() {
			cont := false
			result.FinalOutput.Continue = &cont
			result.FinalOutput.StopReason = output.StopReason
		}
	}

	// Combine system messages
	if len(systemMessages) > 0 {
		result.FinalOutput.SystemMessage = strings.Join(systemMessages, "\n")
	}

	// Combine additional contexts
	if len(additionalContexts) > 0 {
		if result.FinalOutput.HookSpecificOutput == nil {
			result.FinalOutput.HookSpecificOutput = make(map[string]any)
		}
		result.FinalOutput.HookSpecificOutput["additionalContext"] = strings.Join(additionalContexts, "\n")
	}

	result.FinalOutput.SuppressOutput = suppressOutput
}

// aggregateWithFieldReplacement uses last-write-wins for specific fields.
// Used for BeforeModel/AfterModel where later hooks override earlier ones.
func (a *HookAggregator) aggregateWithFieldReplacement(result *AggregatedHookResult) {
	var systemMessages []string

	for _, output := range result.AllOutputs {
		// Check for blocking decisions
		if output.Decision.IsBlocking() {
			result.FinalOutput.Decision = output.Decision
			result.FinalOutput.Reason = output.Reason
			result.Success = false
			return
		}

		// Collect system messages
		if output.SystemMessage != "" {
			systemMessages = append(systemMessages, output.SystemMessage)
		}

		// Last-write-wins for hook-specific output
		if output.HookSpecificOutput != nil {
			// Merge llm_request modifications
			if llmReq, ok := output.HookSpecificOutput["llm_request"]; ok {
				if result.FinalOutput.HookSpecificOutput == nil {
					result.FinalOutput.HookSpecificOutput = make(map[string]any)
				}
				result.FinalOutput.HookSpecificOutput["llm_request"] = llmReq
			}

			// Merge llm_response (synthetic or modified)
			if llmResp, ok := output.HookSpecificOutput["llm_response"]; ok {
				if result.FinalOutput.HookSpecificOutput == nil {
					result.FinalOutput.HookSpecificOutput = make(map[string]any)
				}
				result.FinalOutput.HookSpecificOutput["llm_response"] = llmResp
			}
		}
	}

	if len(systemMessages) > 0 {
		result.FinalOutput.SystemMessage = strings.Join(systemMessages, "\n")
	}
}

// aggregateWithUnionLogic uses union for tool selection.
// Collects all tool names. NONE mode is most restrictive.
func (a *HookAggregator) aggregateWithUnionLogic(result *AggregatedHookResult) {
	var systemMessages []string
	var allToolNames []string
	mode := "AUTO" // Default mode

	for _, output := range result.AllOutputs {
		// Check for blocking decisions
		if output.Decision.IsBlocking() {
			result.FinalOutput.Decision = output.Decision
			result.FinalOutput.Reason = output.Reason
			result.Success = false
			return
		}

		// Collect system messages
		if output.SystemMessage != "" {
			systemMessages = append(systemMessages, output.SystemMessage)
		}

		// Collect tool configs
		if output.HookSpecificOutput != nil {
			if toolConfig, ok := output.HookSpecificOutput["toolConfig"].(map[string]any); ok {
				// Mode priority: NONE > ANY > AUTO
				if m, ok := toolConfig["mode"].(string); ok {
					if m == "NONE" || (m == "ANY" && mode != "NONE") {
						mode = m
					}
				}

				// Collect tool names
				if names, ok := toolConfig["allowedFunctionNames"].([]any); ok {
					for _, name := range names {
						if s, ok := name.(string); ok {
							allToolNames = append(allToolNames, s)
						}
					}
				}
			}
		}
	}

	if len(systemMessages) > 0 {
		result.FinalOutput.SystemMessage = strings.Join(systemMessages, "\n")
	}

	// Build final tool config
	if mode != "AUTO" || len(allToolNames) > 0 {
		// Dedupe and sort tool names
		toolNamesSet := make(map[string]bool)
		for _, name := range allToolNames {
			toolNamesSet[name] = true
		}
		uniqueNames := make([]string, 0, len(toolNamesSet))
		for name := range toolNamesSet {
			uniqueNames = append(uniqueNames, name)
		}
		sort.Strings(uniqueNames)

		if result.FinalOutput.HookSpecificOutput == nil {
			result.FinalOutput.HookSpecificOutput = make(map[string]any)
		}
		result.FinalOutput.HookSpecificOutput["toolConfig"] = map[string]any{
			"mode":                 mode,
			"allowedFunctionNames": uniqueNames,
		}
	}
}
