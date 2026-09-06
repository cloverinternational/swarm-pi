package builtin

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// ToolResultAnalysisHook analyzes tool execution results and can trigger
// follow-up actions such as task creation or findings capture.
// This hook runs at priority 90, after enforcement hooks but before
// guidance hooks, enabling it to make decisions based on execution outcomes.
type ToolResultAnalysisHook struct {
	analyzers map[string]findings.ResultAnalyzer
	priority  int
}

// NewToolResultAnalysisHook creates a new tool result analysis hook
// with default analyzers.
func NewToolResultAnalysisHook() *ToolResultAnalysisHook {
	hook := &ToolResultAnalysisHook{
		analyzers: make(map[string]findings.ResultAnalyzer),
		priority:  90,
	}

	// Register default analyzers
	hook.RegisterAnalyzer(NewDefaultAnalyzer())
	hook.RegisterAnalyzer(NewErrorAnalyzer())
	hook.RegisterAnalyzer(NewInsightAnalyzer())
	hook.RegisterAnalyzer(NewDreamAnalyzer())
	hook.RegisterAnalyzer(NewSteeringAnalyzer())

	return hook
}

// Name returns the hook name
func (h *ToolResultAnalysisHook) Name() string {
	return "tool-result-analysis"
}

// Priority returns the hook priority (higher = runs first)
func (h *ToolResultAnalysisHook) Priority() int {
	return h.priority
}

// SetPriority allows adjusting the hook priority
func (h *ToolResultAnalysisHook) SetPriority(p int) {
	h.priority = p
}

// Filter returns true for tool after-execute events
func (h *ToolResultAnalysisHook) Filter(event hooks.Event) bool {
	return event.Type == hooks.EventToolAfterExecute
}

// OnEvent analyzes the tool result and determines follow-up actions
func (h *ToolResultAnalysisHook) OnEvent(ctx context.Context, event hooks.Event) (hooks.HookResult, error) {
	// Extract tool information from event
	toolName, ok := event.Data["tool_name"].(string)
	if !ok || toolName == "" {
		return hooks.Continue(), nil
	}

	toolInput, _ := event.Data["tool_input"].(map[string]any)
	toolOutput, _ := event.Data["tool_output"].(map[string]any)

	// Run all registered analyzers and aggregate results
	analysis := h.analyze(ctx, toolName, toolInput, toolOutput)

	// Store analysis results in event metadata for downstream hooks
	if event.Metadata == nil {
		event.Metadata = make(map[string]any)
	}

	// Flag for LocalFindingsHook to capture this finding
	if analysis.ShouldCapture {
		event.Metadata["capture_finding"] = true
		event.Metadata["finding_tags"] = analysis.Tags
		event.Metadata["finding_priority"] = analysis.Priority
		event.Metadata["finding_insights"] = analysis.Insights
	} else {
		// Explicitly record the negative decision so downstream hooks (e.g.
		// LocalFindingsHook.Filter) honor it instead of falling back to their
		// own auto-capture policy. Without this, "boring" tool results that the
		// analysis deliberately skipped would still be captured under a
		// capture-all default.
		event.Metadata["capture_finding"] = false
	}

	// Flag for task creation (can be picked up by PostActingHook or similar)
	if analysis.ShouldCreateTask {
		event.Metadata["create_task"] = true
		event.Metadata["task_description"] = analysis.TaskDescription
	}

	// If we have insights, include them in the response message
	if len(analysis.Insights) > 0 {
		message := fmt.Sprintf("Analysis: %s", strings.Join(analysis.Insights, "; "))
		return hooks.ContinueWithMessage(message), nil
	}

	return hooks.Continue(), nil
}

// RegisterAnalyzer adds a custom analyzer to the hook
func (h *ToolResultAnalysisHook) RegisterAnalyzer(analyzer findings.ResultAnalyzer) {
	if analyzer != nil {
		h.analyzers[analyzer.Name()] = analyzer
	}
}

// analyze runs all registered analyzers and aggregates their results
func (h *ToolResultAnalysisHook) analyze(ctx context.Context, toolName string, input, output map[string]any) findings.AnalysisResult {
	// Start with default result (don't capture)
	result := findings.AnalysisResult{
		ShouldCapture:    false,
		ShouldCreateTask: false,
		Priority:         50, // Medium priority
	}

	// Run each analyzer and merge results
	for _, analyzer := range h.analyzers {
		a, err := analyzer.Analyze(ctx, toolName, input, output)
		if err != nil {
			continue // Skip failed analyzers
		}

		// Merge: if any analyzer says capture, we capture
		if a.ShouldCapture {
			result.ShouldCapture = true
		}

		// Merge: if any analyzer says create task, we create task
		if a.ShouldCreateTask {
			result.ShouldCreateTask = true
			if result.TaskDescription == "" {
				result.TaskDescription = a.TaskDescription
			}
		}

		// Merge tags (union)
		for _, tag := range a.Tags {
			if !contains(result.Tags, tag) {
				result.Tags = append(result.Tags, tag)
			}
		}

		// Take max priority
		if a.Priority > result.Priority {
			result.Priority = a.Priority
		}

		// Merge insights
		result.Insights = append(result.Insights, a.Insights...)

		// Merge related finding IDs
		for _, id := range a.RelatedFindingIDs {
			if !contains(result.RelatedFindingIDs, id) {
				result.RelatedFindingIDs = append(result.RelatedFindingIDs, id)
			}
		}
	}

	return result
}

// Helper function
func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

// =============================================================================
// Default Analyzers
// =============================================================================

// DefaultAnalyzer provides general-purpose analysis
type DefaultAnalyzer struct{}

// NewDefaultAnalyzer creates a new default analyzer
func NewDefaultAnalyzer() *DefaultAnalyzer {
	return &DefaultAnalyzer{}
}

// Name returns the analyzer name
func (a *DefaultAnalyzer) Name() string {
	return "default"
}

// Analyze provides basic analysis for any tool result
func (a *DefaultAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
	result := findings.AnalysisResult{
		ShouldCapture: false, // By default, don't capture everything
		Priority:      50,
	}

	// Capture certain interesting tools by default
	interestingTools := map[string]bool{
		"evaluate":   true,
		"run_test":   true,
		"build":      true,
		"deploy":     true,
		"benchmark":  true,
		"experiment": true,
	}

	if interestingTools[toolName] {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, toolName, "automated")
	}

	// Check for success/failure indicators in output
	if success, ok := output["success"].(bool); ok {
		if success {
			result.Tags = append(result.Tags, "success")
		} else {
			result.Tags = append(result.Tags, "failure")
			result.Priority = 70 // Higher priority for failures
		}
	}

	return result, nil
}

// ErrorAnalyzer detects and flags errors
type ErrorAnalyzer struct{}

// NewErrorAnalyzer creates a new error analyzer
func NewErrorAnalyzer() *ErrorAnalyzer {
	return &ErrorAnalyzer{}
}

// Name returns the analyzer name
func (a *ErrorAnalyzer) Name() string {
	return "error"
}

// Analyze detects errors in tool output
func (a *ErrorAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
	result := findings.AnalysisResult{
		ShouldCapture: false,
		Priority:      50,
	}

	// Check for error indicators
	hasError := false
	errorMsg := ""

	// Check explicit error field
	if err, ok := output["error"].(string); ok && err != "" {
		hasError = true
		errorMsg = err
	}

	// Check error message field
	if errMsg, ok := output["error_message"].(string); ok && errMsg != "" {
		hasError = true
		errorMsg = errMsg
	}

	// Check stderr
	if stderr, ok := output["stderr"].(string); ok && stderr != "" {
		hasError = true
		errorMsg = stderr
	}

	// Check success flag
	if success, ok := output["success"].(bool); ok && !success {
		hasError = true
	}

	if hasError {
		result.ShouldCapture = true
		result.ShouldCreateTask = true
		result.Tags = append(result.Tags, "error", "needs-attention")
		result.Priority = 90 // High priority for errors
		result.TaskDescription = fmt.Sprintf("Investigate %s failure: %s", toolName, truncate(errorMsg, 100))
		result.Insights = append(result.Insights, fmt.Sprintf("Tool %s failed with error", toolName))
	}

	return result, nil
}

// InsightAnalyzer extracts insights from successful results
type InsightAnalyzer struct{}

// NewInsightAnalyzer creates a new insight analyzer
func NewInsightAnalyzer() *InsightAnalyzer {
	return &InsightAnalyzer{}
}

// Name returns the analyzer name
func (a *InsightAnalyzer) Name() string {
	return "insight"
}

// Analyze extracts interesting insights from tool output.
//
// "Interesting" means signals the agent could act on — evaluation scores,
// coverage changes, anomalous latency. Routine observability fields like
// duration_ms and tokens_used are intentionally excluded: they appear on
// every single tool call and turned the analysis system-reminder into
// per-call noise that buried real findings.
func (a *InsightAnalyzer) Analyze(ctx context.Context, toolName string, input, output map[string]any) (findings.AnalysisResult, error) {
	result := findings.AnalysisResult{
		ShouldCapture: false,
		Priority:      50,
	}

	// Only emit insights for metrics that carry actionable signal — not for
	// routine instrumentation that fires on every call.
	interestingMetrics := []string{
		"accuracy",
		"score",
		"pgr", // Performance Gap Recovery
		"coverage",
	}

	for _, metric := range interestingMetrics {
		if val, ok := output[metric]; ok {
			result.ShouldCapture = true
			result.Tags = append(result.Tags, "has-metrics")
			result.Insights = append(result.Insights, fmt.Sprintf("%s: %v", metric, val))
		}
	}

	// Check for evaluation results (from AAR pattern)
	if pgr, ok := output["pgr"].(float64); ok {
		result.ShouldCapture = true
		result.Tags = append(result.Tags, "evaluation", "pgr")
		result.Priority = 75
		result.Insights = append(result.Insights, fmt.Sprintf("PGR: %.2f", pgr))
	}

	return result, nil
}
