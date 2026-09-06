// Package agent provides the Ring 2 agent implementation.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/systemprompt"
)

// InterventionPoint defines where the steering agent can intervene
type InterventionPoint string

const (
	// InterventionBeforeTool - Before a tool is executed
	InterventionBeforeTool InterventionPoint = "before_tool"
	// InterventionAfterTool - After a tool is executed
	InterventionAfterTool InterventionPoint = "after_tool"
	// InterventionBeforeResponse - Before final response is returned
	InterventionBeforeResponse InterventionPoint = "before_response"
	// InterventionAfterTurn - After a complete turn
	InterventionAfterTurn InterventionPoint = "after_turn"
)

// DecisionType defines what action the steering agent decided
type DecisionType string

const (
	// DecisionApprove - Approve and proceed
	DecisionApprove DecisionType = "approve"
	// DecisionBlock - Block execution
	DecisionBlock DecisionType = "block"
	// DecisionRetry - Retry with feedback
	DecisionRetry DecisionType = "retry"
	// DecisionModify - Modify the content
	DecisionModify DecisionType = "modify"
)

// SteeringDecision represents a decision made by the steering agent
type SteeringDecision struct {
	Decision  DecisionType
	Reasoning string
	Feedback  string // Used for retry scenarios
	Modified  string // Used for modify scenarios
	Timestamp time.Time
}

// InterventionLog represents a log entry for an intervention
type InterventionLog struct {
	Point       InterventionPoint
	Decision    SteeringDecision
	Context     map[string]any
	Duration    time.Duration
	EvaluatorID string
}

// SteeringConfig configures the steering agent behavior
type SteeringConfig struct {
	// InterventionPoints defines which points to evaluate
	InterventionPoints []InterventionPoint

	// MaxRetries defines maximum retry attempts (0 = no limit, user decides)
	MaxRetries int

	// EvaluationPrompt is the system prompt for evaluation
	EvaluationPrompt string

	// AutoApproveSimple automatically approves simple operations
	AutoApproveSimple bool

	// LogInterventions enables intervention logging
	LogInterventions bool
}

// DefaultSteeringConfig returns a default configuration
func DefaultSteeringConfig() SteeringConfig {
	return SteeringConfig{
		InterventionPoints: []InterventionPoint{
			InterventionBeforeTool,
			InterventionBeforeResponse,
		},
		MaxRetries:        3,
		EvaluationPrompt:  defaultEvaluationPrompt,
		AutoApproveSimple: true,
		LogInterventions:  true,
	}
}

const defaultEvaluationPrompt = `You are a quality control and safety evaluator. Your role is to analyze agent actions and outputs.

Evaluate the following and respond with one of:
- APPROVE: Action/output is good, proceed
- BLOCK: Action/output has critical issues, stop
- RETRY: Action/output needs improvement, provide specific feedback
- MODIFY: Action/output mostly good but needs small changes, provide modified version

Always provide clear reasoning for your decision.

DEPENDENCY AWARENESS: When evaluating an action, consider whether the agent has
resolved upstream decisions before acting on downstream ones. Nitpick when:
- The agent makes a choice that depends on an unresolved upstream decision
- The agent skips a dependency (e.g., modifying code before understanding its consumers)
- The agent assumes a constraint that hasn't been validated
- The agent acts on a downstream task before its prerequisite is complete
If you spot this, RETRY with feedback identifying the unresolved upstream dependency.

PLAN MODE AWARENESS: If the agent is in plan mode and attempts to write code or make edits
before resolving the decision tree with the user, BLOCK and suggest:
"Resolve requirement decisions with the user first. Ask one question at a time with your
recommended answer. Only write the plan after shared understanding is reached."`

// SteeringAgent wraps an agent with oversight and quality control
type SteeringAgent struct {
	evaluator       *Agent // Expensive model for evaluation (GPT-4, Claude Opus)
	config          SteeringConfig
	interventionLog []InterventionLog
	mu              sync.RWMutex
	logger          observability.Logger
	tracer          observability.Tracer
	debugWriter     io.Writer // Optional: per-batch debug file writer
}

// NewSteeringAgent creates a new steering agent
func NewSteeringAgent(evaluator *Agent, config SteeringConfig, logger observability.Logger, tracer observability.Tracer) (*SteeringAgent, error) {
	if evaluator == nil {
		return nil, fmt.Errorf("evaluator agent is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if tracer == nil {
		return nil, fmt.Errorf("tracer is required")
	}

	// Set default config if empty
	if len(config.InterventionPoints) == 0 {
		config = DefaultSteeringConfig()
	}

	return &SteeringAgent{
		evaluator:       evaluator,
		config:          config,
		interventionLog: make([]InterventionLog, 0),
		logger:          logger,
		tracer:          tracer,
	}, nil
}

// SetDebugWriter sets a writer for detailed debug output during EvaluateFindings.
// The caller is responsible for closing the writer after the evaluation completes.
func (s *SteeringAgent) SetDebugWriter(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debugWriter = w
}

// ClearDebugWriter removes the debug writer.
func (s *SteeringAgent) ClearDebugWriter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.debugWriter = nil
}

// GetProvider returns the underlying LLM provider used by the evaluator agent.
// Allows callers to reuse the same provider for auxiliary LLM tasks such as
// Gold tier analysis, without requiring a separate provider reference.
func (s *SteeringAgent) GetProvider() provider.Provider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.evaluator == nil {
		return nil
	}
	return s.evaluator.provider
}

// EvaluateToolUse evaluates whether a tool should be executed
func (s *SteeringAgent) EvaluateToolUse(ctx context.Context, toolName string, params map[string]any) (SteeringDecision, error) {
	ctx, span := s.tracer.StartSpan(ctx, "SteeringAgent.EvaluateToolUse")
	defer span.End()

	s.logger.Info(ctx, "Evaluating tool use",
		observability.F("tool", toolName),
		observability.F("params", params))

	// Auto-approve if configured and tool is simple
	if s.config.AutoApproveSimple && s.isSimpleTool(toolName) {
		s.logger.Debug(ctx, "Auto-approving simple tool", observability.F("tool", toolName))
		return SteeringDecision{
			Decision:  DecisionApprove,
			Reasoning: "Auto-approved: Simple tool operation",
			Timestamp: time.Now(),
		}, nil
	}

	// Build evaluation prompt
	evaluationContext := fmt.Sprintf(`Tool Use Evaluation:
Tool: %s
Parameters: %v

Should this tool be executed? Evaluate for safety, correctness, and appropriateness.`, toolName, params)

	decision, err := s.evaluate(ctx, InterventionBeforeTool, evaluationContext, map[string]any{
		"tool":   toolName,
		"params": params,
	})
	if err != nil {
		return SteeringDecision{}, fmt.Errorf("evaluation failed: %w", err)
	}

	return decision, nil
}

// EvaluateToolResult evaluates the result of a tool execution
func (s *SteeringAgent) EvaluateToolResult(ctx context.Context, toolName string, result string, err error) (SteeringDecision, error) {
	ctx, span := s.tracer.StartSpan(ctx, "SteeringAgent.EvaluateToolResult")
	defer span.End()

	s.logger.Info(ctx, "Evaluating tool result",
		observability.F("tool", toolName),
		observability.F("result_length", len(result)),
		observability.F("has_error", err != nil))

	// Build evaluation prompt
	evaluationContext := fmt.Sprintf(`Tool Result Evaluation:
Tool: %s
Result: %s
Error: %v

Is this result acceptable? Evaluate quality, correctness, and safety.`, toolName, result, err)

	decision, err2 := s.evaluate(ctx, InterventionAfterTool, evaluationContext, map[string]any{
		"tool":   toolName,
		"result": result,
		"error":  err,
	})
	if err2 != nil {
		return SteeringDecision{}, fmt.Errorf("evaluation failed: %w", err2)
	}

	return decision, nil
}

// EvaluateResponse evaluates the final response before it's returned
func (s *SteeringAgent) EvaluateResponse(ctx context.Context, response string, conversationHistory []conversation.Message) (SteeringDecision, error) {
	ctx, span := s.tracer.StartSpan(ctx, "SteeringAgent.EvaluateResponse")
	defer span.End()

	s.logger.Info(ctx, "Evaluating final response",
		observability.F("response_length", len(response)),
		observability.F("history_length", len(conversationHistory)))

	// Build evaluation prompt with conversation context
	var historyContext strings.Builder
	if len(conversationHistory) > 0 {
		// Include last few messages for context
		start := 0
		if len(conversationHistory) > 5 {
			start = len(conversationHistory) - 5
		}
		for _, msg := range conversationHistory[start:] {
			historyContext.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content))
		}
	}

	evaluationContext := fmt.Sprintf(`Response Evaluation:

Recent Conversation:
%s

Agent Response:
%s

Evaluate this response for:
1. Accuracy and correctness
2. Completeness (answers the question)
3. Safety (no harmful content)
4. Clarity and helpfulness`, historyContext.String(), response)

	decision, err := s.evaluate(ctx, InterventionBeforeResponse, evaluationContext, map[string]any{
		"response":         response,
		"conversation_len": len(conversationHistory),
	})
	if err != nil {
		return SteeringDecision{}, fmt.Errorf("evaluation failed: %w", err)
	}

	return decision, nil
}

// ExecutionTurn represents a complete turn of agent execution
type ExecutionTurn struct {
	TurnNumber int
	Input      string
	Output     string
	ToolCalls  []ToolCall
	TokensUsed int
	Duration   time.Duration
}

// ToolCall represents a single tool invocation
type ToolCall struct {
	Name   string
	Params map[string]any
	Result string
}

// EvaluateTurn evaluates an entire conversation turn
func (s *SteeringAgent) EvaluateTurn(ctx context.Context, turn ExecutionTurn) (SteeringDecision, error) {
	ctx, span := s.tracer.StartSpan(ctx, "SteeringAgent.EvaluateTurn")
	defer span.End()

	s.logger.Info(ctx, "Evaluating conversation turn",
		observability.F("turn_number", turn.TurnNumber),
		observability.F("tool_calls", len(turn.ToolCalls)))

	// Build evaluation prompt with full turn context
	evaluationContext := fmt.Sprintf(`Turn Evaluation:

Turn Number: %d
Input: %s
Output: %s
Tool Calls: %d
Tokens Used: %d
Duration: %v

Evaluate the overall quality and effectiveness of this turn.`,
		turn.TurnNumber,
		turn.Input,
		turn.Output,
		len(turn.ToolCalls),
		turn.TokensUsed,
		turn.Duration)

	decision, err := s.evaluate(ctx, InterventionAfterTurn, evaluationContext, map[string]any{
		"turn_number": turn.TurnNumber,
		"tool_calls":  len(turn.ToolCalls),
		"tokens_used": turn.TokensUsed,
	})
	if err != nil {
		return SteeringDecision{}, fmt.Errorf("evaluation failed: %w", err)
	}

	return decision, nil
}

// evaluate performs the actual evaluation using the evaluator agent
func (s *SteeringAgent) evaluate(ctx context.Context, point InterventionPoint, evaluationContext string, logContext map[string]any) (SteeringDecision, error) {
	start := time.Now()

	// Check if this intervention point is enabled
	if !s.isInterventionPointEnabled(point) {
		return SteeringDecision{
			Decision:  DecisionApprove,
			Reasoning: fmt.Sprintf("Intervention point %s not enabled", point),
			Timestamp: time.Now(),
		}, nil
	}

	// Create evaluation messages
	messages := []*conversation.Message{
		{
			Role:    conversation.RoleUser,
			Content: evaluationContext,
		},
	}

	// Execute evaluator agent.
	// Explicit MaxTokens prevents provider-default truncation of the JSON
	// decision mid-sentence, which surfaces as cut-off "reason"/"suggestion"
	// fields in downstream hook output.
	maxTok := 2048
	request := provider.ChatRequest{
		Messages:     messages,
		Model:        s.evaluator.Definition().Model,
		SystemPrompt: s.config.EvaluationPrompt,
		Tools:        []provider.Tool{}, // No tools for evaluation
		MaxTokens:    &maxTok,
	}

	response, err := s.evaluator.provider.Chat(ctx, request)
	if err != nil {
		return SteeringDecision{}, fmt.Errorf("evaluator chat failed: %w", err)
	}

	// Parse decision from response
	decision := s.parseDecision(response.Message.Content)
	decision.Timestamp = time.Now()

	// Log intervention if enabled
	if s.config.LogInterventions {
		s.logIntervention(InterventionLog{
			Point:       point,
			Decision:    decision,
			Context:     logContext,
			Duration:    time.Since(start),
			EvaluatorID: s.evaluator.ID(),
		})
	}

	s.logger.Info(ctx, "Steering decision made",
		observability.F("point", point),
		observability.F("decision", decision.Decision),
		observability.F("reasoning", decision.Reasoning))

	return decision, nil
}

// parseDecision parses the evaluator's response into a decision
func (s *SteeringAgent) parseDecision(response string) SteeringDecision {
	decision := SteeringDecision{
		Reasoning: response,
		Timestamp: time.Now(),
	}

	// Simple keyword-based parsing (in production, could use structured output)
	if containsKeyword(response, "APPROVE") {
		decision.Decision = DecisionApprove
	} else if containsKeyword(response, "BLOCK") {
		decision.Decision = DecisionBlock
	} else if containsKeyword(response, "RETRY") {
		decision.Decision = DecisionRetry
		// Extract feedback from response
		decision.Feedback = extractFeedback(response)
	} else if containsKeyword(response, "MODIFY") {
		decision.Decision = DecisionModify
		// Extract modified content
		decision.Modified = extractModified(response)
	} else {
		// Default to approve if unclear
		decision.Decision = DecisionApprove
		decision.Reasoning = "Unclear evaluation, defaulting to approve: " + response
	}

	return decision
}

// isSimpleTool checks if a tool is considered simple for auto-approval
func (s *SteeringAgent) isSimpleTool(toolName string) bool {
	simplTools := map[string]bool{
		"read_file":  true,
		"list_files": true,
		"get_time":   true,
		"calculate":  true,
		"search":     true,
	}
	return simplTools[toolName]
}

// isInterventionPointEnabled checks if an intervention point is configured
func (s *SteeringAgent) isInterventionPointEnabled(point InterventionPoint) bool {
	return slices.Contains(s.config.InterventionPoints, point)
}

// logIntervention adds an entry to the intervention log
func (s *SteeringAgent) logIntervention(log InterventionLog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interventionLog = append(s.interventionLog, log)
}

// GetInterventionLog returns a copy of the intervention log
func (s *SteeringAgent) InterventionLog() []InterventionLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	logCopy := make([]InterventionLog, len(s.interventionLog))
	copy(logCopy, s.interventionLog)
	return logCopy
}

// GetInterventionStats returns statistics about interventions
func (s *SteeringAgent) InterventionStats() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := map[string]any{
		"total_interventions": len(s.interventionLog),
		"by_decision":         make(map[DecisionType]int),
		"by_point":            make(map[InterventionPoint]int),
		"total_duration":      time.Duration(0),
	}

	var totalDuration time.Duration
	byDecision := make(map[DecisionType]int)
	byPoint := make(map[InterventionPoint]int)

	for _, log := range s.interventionLog {
		byDecision[log.Decision.Decision]++
		byPoint[log.Point]++
		totalDuration += log.Duration
	}

	stats["by_decision"] = byDecision
	stats["by_point"] = byPoint
	stats["total_duration"] = totalDuration
	if len(s.interventionLog) > 0 {
		stats["avg_duration"] = totalDuration / time.Duration(len(s.interventionLog))
	}

	return stats
}

// ClearInterventionLog clears the intervention log
func (s *SteeringAgent) ClearInterventionLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interventionLog = make([]InterventionLog, 0)
}

// Helper functions

func containsKeyword(text string, keyword string) bool {
	// Case-insensitive check
	text = toLower(text)
	keyword = toLower(keyword)
	return contains(text, keyword)
}

func toLower(s string) string {
	result := make([]rune, len(s))
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			result[i] = r + 32
		} else {
			result[i] = r
		}
	}
	return string(result)
}

func extractFeedback(response string) string {
	// Simple extraction - look for "Feedback:" or similar
	if idx := findSubstringIndex(response, "Feedback:"); idx >= 0 {
		return response[idx+9:] // Skip "Feedback:"
	}
	if idx := findSubstringIndex(response, "feedback:"); idx >= 0 {
		return response[idx+9:]
	}
	// Return full response as feedback if no explicit feedback section
	return response
}

func extractModified(response string) string {
	// Simple extraction - look for "Modified:" or similar
	if idx := findSubstringIndex(response, "Modified:"); idx >= 0 {
		return response[idx+9:] // Skip "Modified:"
	}
	if idx := findSubstringIndex(response, "modified:"); idx >= 0 {
		return response[idx+9:]
	}
	// Return full response as modified content if no explicit section
	return response
}

func findSubstringIndex(s string, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// ---------------------------------------------------------------------------
// Findings Evaluation (Bronze -> Silver promotion via LLM judgment)
// ---------------------------------------------------------------------------

// FindingsEvaluation represents an LLM-scored evaluation of a single finding.
// The Priority is a precise float64 (e.g. 72.45) representing true semantic
// judgment of the finding's value, not a deterministic score from Go math.
type FindingsEvaluation struct {
	// FindingID links back to the evaluated finding
	FindingID string `json:"finding_id"`

	// Priority is the LLM-assigned score 0-100 with 2 decimal precision
	Priority float64 `json:"priority"`

	// ShouldPromote indicates whether this finding should be promoted to Silver
	ShouldPromote bool `json:"should_promote"`

	// Reasoning explains the LLM's score
	Reasoning string `json:"reasoning"`

	// Tags suggested by the LLM for categorization
	Tags []string `json:"tags,omitempty"`

	// Source records whether the score came from the first LLM pass, the repair
	// pass, or deterministic fallback scoring.
	Source string `json:"source,omitempty"`
}

// FindingSummary is a lightweight representation of a finding for LLM evaluation.
// We don't send the full finding to save tokens.
type FindingSummary struct {
	ID              string   `json:"id"`
	ToolName        string   `json:"tool"`
	Success         bool     `json:"success"`
	ContextSummary  string   `json:"context"`
	Tags            []string `json:"tags,omitempty"`
	InitialPriority float64  `json:"initial_priority,omitempty"`
	DurationMs      int64    `json:"duration_ms,omitempty"`
	ErrorMessage    string   `json:"error,omitempty"`
	InputKeys       []string `json:"input_keys,omitempty"`
	OutputPreview   string   `json:"output_preview,omitempty"`
	Phase           string   `json:"phase,omitempty"`        // Current workflow phase
	TaskContext     string   `json:"task_context,omitempty"` // Active task subject
	ToolIndex       int      `json:"tool_index,omitempty"`   // Nth tool call in this phase
}

// AnalysisSessionContext provides rich context for a batch of findings.
// This is prepended to the evaluation prompt so the LLM understands WHY
// tools were called, not just WHAT was called.
type AnalysisSessionContext struct {
	RecentMessages []string `json:"recent_messages,omitempty"` // Last N conversation turns
	ActivePlan     string   `json:"active_plan,omitempty"`     // Plan content if in plan mode
	DreamMemories  []string `json:"memories,omitempty"`        // Relevant cross-session memories
	TaskHistory    []string `json:"task_history,omitempty"`    // Completed + in-progress tasks
}

const findingsEvaluationPrompt = `You are a findings evaluator for an AI agent system. Your job is to score tool execution results (findings) based on their long-term value as agent memories.

Each finding includes a workflow phase (researching/planning/acting/verifying/debugging/documenting) and the active task context. Use this to judge value relative to what the agent is doing.

Score each finding from 0.00 to 100.00 using precise decimal values that reflect true semantic judgment.

Scoring guide:
- 90.00-100.00: Critical insight - changes how the agent should work (permission errors, security patterns, architectural discoveries)
- 70.00-89.99: Valuable pattern - worth remembering across sessions (successful recipes, anti-patterns, file relationships)
- 50.00-69.99: Mild interest - useful context but not essential (routine operations, expected results)
- 0.00-49.99: Noise - not worth persisting (trivial reads, routine listing, standard output)

Phase-specific scoring:
- RESEARCHING: File reads/searches are expected noise UNLESS they reveal architecture or non-obvious relationships. Score high only for structural discoveries.
- PLANNING: Exploratory tool use is expected. Score high only for design-changing insights.
- ACTING: Edits and writes are the main event. Score errors/failures HIGH as anti-patterns. Score successful complex multi-step operations as recipes.
- VERIFYING: Test results and build outputs are CRITICAL. Pass/fail outcomes deserve high scores. Build errors are valuable anti-patterns.
- DEBUGGING: Error traces, stack dumps, root cause discoveries are EXTREMELY valuable. Score fix attempts as recipes.
- DOCUMENTING: Mostly noise unless it reveals undocumented behavior or missing docs.

Factors to consider:
1. Would this help the agent avoid mistakes in future sessions?
2. Does this reveal non-obvious relationships between files or systems?
3. Is this a reusable recipe or anti-pattern?
4. How unique is this information vs common knowledge?
5. How relevant is this finding to the current workflow phase?

Respond with a JSON array of evaluations. Each element:
{"finding_id": "...", "priority": 72.45, "should_promote": true, "reasoning": "detailed explanation", "tags": ["tag1"]}

Set should_promote=true when priority >= 70.00.
Return exactly one object for every input finding ID. Do not omit findings, duplicate IDs, or invent IDs.

IMPORTANT for reasoning: When priority >= 70, the reasoning becomes a permanent memory that future agents will read. Write it as ACTIONABLE knowledge:
- Name specific files, paths, functions, or patterns involved
- Explain WHAT happened and WHY it matters
- Include the FIX or RECIPE if applicable (exact commands, code patterns)
- Say what to WATCH OUT FOR if this situation recurs
- Bad: "Makefile has hardcoded paths"
- Good: "Makefile at swarm-tui/Makefile hardcodes /home/swarm/swarm-sdk in replace directives (lines 12,15,23). Fix: replace with relative ../swarm-sdk. This breaks any clone outside the original machine."

Use precise decimal scores (e.g. 73.28, 45.91, 88.14). Never use round numbers like 50.00 or 70.00.`

// EvaluateFindings uses the evaluator LLM to score a batch of findings.
// Returns evaluations with precise priority scores based on semantic judgment.
// sessionCtx provides rich context (conversation, plan, dreams) for better scoring.
func (s *SteeringAgent) EvaluateFindings(ctx context.Context, summaries []FindingSummary, sessionCtx *AnalysisSessionContext) ([]FindingsEvaluation, error) {
	ctx, span := s.tracer.StartSpan(ctx, "SteeringAgent.EvaluateFindings")
	defer span.End()

	if len(summaries) == 0 {
		return nil, nil
	}

	// Grab debug writer (nil-safe -- all debug writes check for nil)
	s.mu.RLock()
	dw := s.debugWriter
	s.mu.RUnlock()

	s.logger.Info(ctx, "Evaluating findings batch",
		observability.F("count", len(summaries)))

	// Marshal summaries to JSON for the prompt
	summariesJSON, err := json.Marshal(summaries)
	if err != nil {
		return nil, fmt.Errorf("marshal findings summaries: %w", err)
	}

	// Build phase context preamble
	var preamble strings.Builder
	phaseBreakdown := map[string]int{}
	var activePhase, activeTask string
	for _, s := range summaries {
		if s.Phase != "" {
			phaseBreakdown[s.Phase]++
			if activePhase == "" {
				activePhase = s.Phase
				activeTask = s.TaskContext
			}
		}
	}

	// Session context: what is the agent doing and why?
	if sessionCtx != nil {
		if len(sessionCtx.RecentMessages) > 0 {
			preamble.WriteString("Recent conversation:\n")
			for _, msg := range sessionCtx.RecentMessages {
				preamble.WriteString("  " + msg + "\n")
			}
			preamble.WriteString("\n")
		}
		if sessionCtx.ActivePlan != "" {
			preamble.WriteString("Active plan:\n" + sessionCtx.ActivePlan + "\n\n")
		}
		if len(sessionCtx.DreamMemories) > 0 {
			preamble.WriteString("Relevant memories from prior sessions:\n")
			for _, mem := range sessionCtx.DreamMemories {
				preamble.WriteString("  - " + mem + "\n")
			}
			preamble.WriteString("\n")
		}
		if len(sessionCtx.TaskHistory) > 0 {
			preamble.WriteString("Task history:\n")
			for _, task := range sessionCtx.TaskHistory {
				preamble.WriteString("  " + task + "\n")
			}
			preamble.WriteString("\n")
		}
	}

	if activePhase != "" {
		preamble.WriteString(fmt.Sprintf("Active workflow phase: %s\nActive task: %s\nPhase breakdown: %v\n\n",
			activePhase, activeTask, phaseBreakdown))
	}

	evaluationContext := preamble.String() + fmt.Sprintf("Evaluate these %d findings:\n\n%s", len(summaries), string(summariesJSON))

	// Log the full context for debugging
	s.logger.Info(ctx, "findings_evaluation.context",
		observability.F("session_ctx_present", sessionCtx != nil),
		observability.F("has_messages", sessionCtx != nil && len(sessionCtx.RecentMessages) > 0),
		observability.F("has_plan", sessionCtx != nil && sessionCtx.ActivePlan != ""),
		observability.F("has_dreams", sessionCtx != nil && len(sessionCtx.DreamMemories) > 0),
		observability.F("has_tasks", sessionCtx != nil && len(sessionCtx.TaskHistory) > 0),
		observability.F("active_phase", activePhase),
		observability.F("preamble_length", preamble.Len()),
		observability.F("total_context_length", len(evaluationContext)))

	// Log the actual preamble content for deep debugging
	preambleStr := preamble.String()
	if len(preambleStr) > 2000 {
		preambleStr = preambleStr[:2000] + "...(truncated for log)"
	}
	s.logger.Debug(ctx, "findings_evaluation.full_preamble",
		observability.F("preamble", preambleStr))

	// === FILE DEBUG: Write full input to debug log ===
	if dw != nil {
		fmt.Fprintf(dw, "\n=== STEERING AGENT INPUT ===\n")
		fmt.Fprintf(dw, "Model: %s\n", s.evaluator.Definition().Model)
		fmt.Fprintf(dw, "Summaries count: %d\n\n", len(summaries))

		fmt.Fprintf(dw, "--- System Prompt ---\n%s\n", findingsEvaluationPrompt)

		fmt.Fprintf(dw, "\n--- Preamble (session context -> LLM) ---\n")
		fmt.Fprintf(dw, "%s\n", preamble.String())

		fmt.Fprintf(dw, "\n--- Summaries JSON ---\n")
		prettySum, _ := json.MarshalIndent(summaries, "", "  ")
		fmt.Fprintf(dw, "%s\n", string(prettySum))

		fmt.Fprintf(dw, "\n--- Full Evaluation Context (sent as user message) ---\n")
		fmt.Fprintf(dw, "---BEGIN---\n%s\n---END---\n", evaluationContext)
	}

	// Create evaluation messages
	messages := []*conversation.Message{
		{
			Role:    conversation.RoleUser,
			Content: evaluationContext,
		},
	}

	// Execute evaluator agent.
	// Findings evaluation may emit a scored list — cap generously so large
	// batches don't get cut off, but still bounded.
	maxTok := 4096
	request := provider.ChatRequest{
		Messages:     messages,
		Model:        s.evaluator.Definition().Model,
		SystemPrompt: findingsEvaluationPrompt,
		Tools:        []provider.Tool{}, // No tools for evaluation
		MaxTokens:    &maxTok,
	}

	response, err := s.evaluator.provider.Chat(ctx, request)
	if err != nil {
		if dw != nil {
			fmt.Fprintf(dw, "\n=== LLM ERROR ===\n%v\n", err)
		}
		return nil, fmt.Errorf("evaluator chat failed: %w", err)
	}

	// === FILE DEBUG: Write raw LLM response ===
	if dw != nil {
		fmt.Fprintf(dw, "\n=== RAW LLM RESPONSE ===\n")
		fmt.Fprintf(dw, "Response length: %d bytes\n", len(response.Message.Content))
		fmt.Fprintf(dw, "---BEGIN---\n%s\n---END---\n", response.Message.Content)
	}

	// Parse the JSON response.
	evaluations, err := parseFindingsEvaluations(response.Message.Content, summaries)
	if err != nil {
		s.logger.Warn(ctx, "Failed to parse findings evaluations, retrying repair",
			observability.F("error", err.Error()),
			observability.F("response_length", len(response.Message.Content)))
		if dw != nil {
			fmt.Fprintf(dw, "\n=== PARSE ERROR ===\n%v\n", err)
		}

		repaired, repairErr := s.repairFindingsEvaluation(ctx, evaluationContext, response.Message.Content, summaries, err, dw)
		if repairErr != nil {
			s.logger.Warn(ctx, "Findings evaluation repair failed, using deterministic fallback",
				observability.F("error", repairErr.Error()),
				observability.F("count", len(summaries)))
			if dw != nil {
				fmt.Fprintf(dw, "\n=== REPAIR ERROR ===\n%v\n", repairErr)
				fmt.Fprintf(dw, "\n=== FALLBACK EVALUATIONS ===\n")
			}
			evaluations = fallbackFindingsEvaluations(summaries, repairErr)
		} else {
			evaluations = repaired
			for i := range evaluations {
				evaluations[i].Source = "repaired"
			}
		}
	} else {
		for i := range evaluations {
			evaluations[i].Source = "llm"
		}
	}

	// === FILE DEBUG: Write parsed evaluations ===
	if dw != nil {
		fmt.Fprintf(dw, "\n=== PARSED EVALUATIONS ===\n")
		for i, eval := range evaluations {
			fmt.Fprintf(dw, "  [%d] finding=%s priority=%.2f promote=%v tags=%v reasoning=%q\n",
				i, eval.FindingID, eval.Priority, eval.ShouldPromote, eval.Tags, eval.Reasoning)
		}
	}

	// Log results
	promoted := 0
	for _, eval := range evaluations {
		if eval.ShouldPromote {
			promoted++
		}
	}

	s.logger.Info(ctx, "Findings evaluation complete",
		observability.F("evaluated", len(evaluations)),
		observability.F("promoted", promoted))

	return evaluations, nil
}

func (s *SteeringAgent) repairFindingsEvaluation(ctx context.Context, originalContext, previousResponse string, summaries []FindingSummary, parseErr error, dw io.Writer) ([]FindingsEvaluation, error) {
	requiredIDs := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		requiredIDs = append(requiredIDs, summary.ID)
	}
	repairPrompt := fmt.Sprintf(`Your previous findings evaluation response was invalid:
%v

Required finding IDs, exactly once each:
%s

Return ONLY a JSON array. No markdown, no commentary. Use this exact shape:
[{"finding_id":"...","priority":72.45,"should_promote":true,"reasoning":"...","tags":["..."]}]

Original findings context:
%s`, parseErr, strings.Join(requiredIDs, "\n"), originalContext)

	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: originalContext},
		{Role: conversation.RoleAssistant, Content: previousResponse},
		{Role: conversation.RoleUser, Content: repairPrompt},
	}
	maxTok := 4096
	request := provider.ChatRequest{
		Messages:     messages,
		Model:        s.evaluator.Definition().Model,
		SystemPrompt: findingsEvaluationPrompt,
		Tools:        []provider.Tool{},
		MaxTokens:    &maxTok,
	}
	response, err := s.evaluator.provider.Chat(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("repair evaluator chat failed: %w", err)
	}
	if dw != nil {
		fmt.Fprintf(dw, "\n=== REPAIR RAW LLM RESPONSE ===\n")
		fmt.Fprintf(dw, "Response length: %d bytes\n", len(response.Message.Content))
		fmt.Fprintf(dw, "---BEGIN---\n%s\n---END---\n", response.Message.Content)
	}
	evaluations, err := parseFindingsEvaluations(response.Message.Content, summaries)
	if err != nil {
		return nil, fmt.Errorf("repair parse failed: %w", err)
	}
	return evaluations, nil
}

// RelevanceContext is the rich input to EvaluateRelevance. Fields are optional --
// the prompt builder omits sections that are empty, so callers can populate as
// much or as little as they have available.
type RelevanceContext struct {
	// The proposed action (required)
	ToolName   string
	ToolParams map[string]any

	// Current task being worked on (required)
	TaskTitle       string
	TaskCategory    string
	TaskDescription string // fuller than title if available

	// Broader task context (optional)
	TaskHistory []string // completed + in-progress tasks from the todo manager
	ActivePlan  string   // plan content when in plan mode

	// Session context (optional)
	RecentMessages []string // conversation slice (role: content) -- typically phase-scoped
	DreamMemories  []string // relevant cross-session memories

	// Phase-scoped session telemetry (optional)
	ToolsInPhase     int      // tool calls since the current phase started
	ArtifactsInPhase []string // file paths touched during this phase

	// Steering accumulator (optional) — what the evaluator already told the
	// agent so it can avoid repeating itself and build on prior guidance.
	PriorGuidance []string // recent suggestions already injected
}

// EvaluateRelevance asks the LLM whether a proposed tool call is relevant to the current task.
// Returns action ("continue", "focus", "block") and a reason string.
// Failure modes (LLM error, missing JSON, parse failure) return "continue" to fail open --
// the caller is responsible for deciding how to treat that.
//
// If the LLM hits MaxTokens on the first pass, we retry once with a "be more
// concise" nudge rather than discarding the truncated guidance. The retry is
// cheap because it only fires on the rare length-capped case, and preserves
// useful steering instead of failing open silently.
func (s *SteeringAgent) EvaluateRelevance(ctx context.Context, rc RelevanceContext) (action string, reason string, err error) {
	ctx, span := s.tracer.StartSpan(ctx, "SteeringAgent.EvaluateRelevance")
	defer span.End()

	prompt := buildRelevancePrompt(rc)
	systemPrompt := buildSteeringSystemPrompt(rc.DreamMemories)

	result, finish, ok := s.chatForRelevance(ctx, prompt, systemPrompt, 2048)
	if !ok {
		return "continue", "evaluation failed, allowing by default", nil
	}

	// Retry-on-truncation: the LLM has guidance but ran out of tokens. Ask it
	// to produce the same evaluation more concisely rather than losing the
	// guidance to a half-sentence.
	if finish == provider.FinishReasonLength {
		concisePrompt := prompt + "\n\nYour previous response was too long and was truncated. " +
			"Keep \"reason\" to at most 20 words and \"suggestion\" to at most 80 words. " +
			"Emit ONLY the JSON object — no preamble, no markdown fences."
		retryResult, retryFinish, retryOK := s.chatForRelevance(ctx, concisePrompt, systemPrompt, 3072)
		if retryOK && retryFinish != provider.FinishReasonLength {
			result = retryResult
		} else {
			// Still truncated or failed: fail open so the agent doesn't see
			// a half-sentence guide.
			return "continue", "evaluator response truncated, allowing by default", nil
		}
	}

	switch result.Action {
	case "continue":
		return result.Action, result.Reason, nil
	case "focus", "block", "guide":
		return result.Action, result.Reason + "; " + result.Suggestion, nil
	default:
		return "continue", result.Reason, nil
	}
}

// relevanceResult is the JSON shape the steering evaluator emits.
type relevanceResult struct {
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion,omitempty"` // Only for "guide" action
}

// chatForRelevance issues one relevance-evaluator turn and parses the response.
// Returns the parsed result, the LLM's finish reason (so callers can detect
// length-truncation), and ok=false if the provider call or JSON parse failed.
func (s *SteeringAgent) chatForRelevance(ctx context.Context, prompt, systemPrompt string, maxTokens int) (relevanceResult, provider.FinishReason, bool) {
	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: prompt},
	}

	request := provider.ChatRequest{
		Messages:     messages,
		Model:        s.evaluator.Definition().Model,
		SystemPrompt: systemPrompt,
		Tools:        []provider.Tool{},
		MaxTokens:    &maxTokens,
	}

	response, err := s.evaluator.provider.Chat(ctx, request)
	if err != nil {
		return relevanceResult{}, "", false
	}

	jsonStr := extractJSON(response.Message.Content)
	if jsonStr == "" {
		return relevanceResult{}, response.FinishReason, false
	}

	var result relevanceResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return relevanceResult{}, response.FinishReason, false
	}

	return result, response.FinishReason, true
}

// buildSteeringSystemPrompt constructs the system prompt for the steering evaluator.
// It layers project knowledge from dream memories on top of the base evaluator role,
// giving the evaluator accumulated session knowledge about architecture, anti-patterns,
// and recipes — similar to how AGENTS.md grounds a coding agent.
//
// The prompt is resolved via the systemprompt catalog (role "steering-evaluator")
// so users can override it. When no catalog override exists, we auto-generate from
// dream memories with a sensible fallback.
func buildSteeringSystemPrompt(dreamMemories []string) string {
	base := systemprompt.Resolve(systemprompt.Request{
		Role:     systemprompt.RoleSteeringEvaluator,
		Fallback: "", // handled below
	})

	// If user has a custom steering prompt in the catalog, use it directly.
	if base != "" {
		return base
	}

	var b strings.Builder
	b.WriteString("You are a task focus evaluator. Respond only with valid JSON.")

	// Layer in dream memories as project knowledge, categorized by type.
	if len(dreamMemories) > 0 {
		b.WriteString("\n\nPROJECT KNOWLEDGE (accumulated from prior sessions):\n")

		// Partition memories by type for structured presentation
		var antiPatterns, recipes, architecture, other []string
		for _, m := range dreamMemories {
			lower := strings.ToLower(m)
			switch {
			case strings.Contains(lower, "[anti-pattern") || strings.Contains(lower, "[feedback") || strings.Contains(lower, "avoid"):
				antiPatterns = append(antiPatterns, m)
			case strings.Contains(lower, "[recipe") || strings.Contains(lower, "recipe") || strings.Contains(lower, "pattern"):
				recipes = append(recipes, m)
			case strings.Contains(lower, "[project") || strings.Contains(lower, "architecture") || strings.Contains(lower, "layout"):
				architecture = append(architecture, m)
			default:
				other = append(other, m)
			}
		}

		if len(antiPatterns) > 0 {
			b.WriteString("\n## Known Anti-Patterns (WARN the agent if they're about to repeat these)\n")
			for _, m := range antiPatterns {
				b.WriteString("- " + m + "\n")
			}
		}
		if len(recipes) > 0 {
			b.WriteString("\n## Proven Recipes (suggest these approaches when relevant)\n")
			for _, m := range recipes {
				b.WriteString("- " + m + "\n")
			}
		}
		if len(architecture) > 0 {
			b.WriteString("\n## Architecture & Layout\n")
			for _, m := range architecture {
				b.WriteString("- " + m + "\n")
			}
		}
		if len(other) > 0 {
			b.WriteString("\n## Other Context\n")
			for _, m := range other {
				b.WriteString("- " + m + "\n")
			}
		}
	}

	return b.String()
}

// buildRelevancePrompt assembles the evaluator prompt from the populated fields
// of rc. Empty sections are omitted so the LLM focuses on what's present.
func buildRelevancePrompt(rc RelevanceContext) string {
	paramsJSON, _ := json.Marshal(rc.ToolParams)
	paramsStr := string(paramsJSON)
	if len(paramsStr) > 500 {
		paramsStr = paramsStr[:500] + "..."
	}

	var b strings.Builder
	b.WriteString(`You are a steering evaluator for an AI coding agent. You have two jobs:
1. Evaluate whether the proposed tool call is on-task.
2. Proactively guide the agent to DECOMPOSE problems before solving them.

Respond with ONLY a JSON object:
{"action": "continue"|"focus"|"guide"|"block", "reason": "one sentence", "suggestion": "optional guidance"}

Actions:
- "continue": The tool call clearly fits the active task. No guidance needed. Use this ONLY when the tool call is obviously correct and you have nothing useful to add.
- "guide": The tool call is on-task, AND you have useful guidance. Use this to help the agent break down what they're looking at — what do they NEED, what should they EXPECT, and how does the LOGIC work at the lowest level? Push the agent to decompose, not just read. Include your guidance in the "suggestion" field.
- "focus": The tool call is drifting — related but tangential. Allow but redirect. Include the redirection in "suggestion".
- "block": The tool call is off-topic, contradicts the plan, or repeats a completed step. Block and explain why.

DECOMPOSITION PHILOSOPHY (embed this in your guidance):
Before working on the problem, break it down almost as if it was a beginning to CS intro course where you learn how to break down problems based on what you need, what you expect and how logic works at the lowest level. Understand and plan the logic and things needed at the most low level for things to work.

For each component guide the agent to document:
- WHAT I NEED: every input, dependency, precondition required. What must exist before this can work? What state must be initialized?
- WHAT I EXPECT: the expected output for each step. What does success look like at each level? What are the failure modes and how to detect them?
- HOW LOGIC WORKS (LOWEST LEVEL): trace the data flow step-by-step. Identify every transformation and its invariants. What assumptions does each step make? Where could the chain break?

Don't just say "also check X" — say WHY they should check X, WHAT they need from it, WHAT they should expect to find, and HOW it connects to the logic at the lowest level.

IMPORTANT RULES:
- Prefer "guide" over "continue" whenever you can add decomposition value.
- DO NOT repeat guidance you already gave. Check PRIOR GUIDANCE below — if you already suggested something, BUILD ON IT or say something new. Never echo the same advice.
- When context is thin (no messages, no artifacts), use "continue".
- When context is rich, USE IT — but always push toward decomposition, not just surface-level "looks good" confirmations.

PLANNING HEURISTICS (check these before responding):
- If the agent has made 15+ tool calls in this phase WITHOUT a plan artifact → suggest enter_plan_mode to organize before continuing.
- If the task category is "acting" but no prior "planning" task exists in the history → suggest decomposing first: "Consider creating a planning task to map out dependencies before implementing."
- If the agent is about to execute a write/edit tool and hasn't read the target file yet → suggest reading first to understand current state.
- If RELEVANT MEMORIES contain an anti-pattern that matches the current tool → warn the agent explicitly in your suggestion.

`)

	// Planning/researching categories get an extra emphasis on decomposition.
	if rc.TaskCategory == "planning" || rc.TaskCategory == "researching" {
		b.WriteString(`PLAN/RESEARCH MODE ACTIVE: The agent is in a research-first phase. Break it down almost as if it was a beginning to CS intro course where you learn how to break down problems based on what you need, what you expect and how logic works at the lowest level. Understand and plan the logic and things needed at the most low level for things to work.
- Push the agent to document what they need, what they expect, and how the logic works BEFORE reading more files
- Have they identified all the inputs, dependencies, preconditions?
- Have they mapped out how the pieces connect at the lowest level?
- Don't let them just read file after file without synthesizing — they need to trace data flow step-by-step, identify every transformation, and find where the chain could break
- Guide them to build a mental model at the lowest logical level, not just collect information

`)
	}

	b.WriteString("PROPOSED TOOL CALL:\n")
	b.WriteString(fmt.Sprintf("Tool: %s\n", rc.ToolName))
	b.WriteString(fmt.Sprintf("Parameters: %s\n\n", paramsStr))

	b.WriteString("CURRENT TASK:\n")
	b.WriteString(fmt.Sprintf("Title: %s\n", rc.TaskTitle))
	if rc.TaskCategory != "" {
		b.WriteString(fmt.Sprintf("Category: %s\n", rc.TaskCategory))
	}
	if rc.TaskDescription != "" && rc.TaskDescription != rc.TaskTitle {
		b.WriteString(fmt.Sprintf("Description: %s\n", rc.TaskDescription))
	}
	b.WriteString("\n")

	if rc.ToolsInPhase > 0 || len(rc.ArtifactsInPhase) > 0 {
		b.WriteString("PHASE PROGRESS:\n")
		if rc.ToolsInPhase > 0 {
			b.WriteString(fmt.Sprintf("Tool calls since phase started: %d\n", rc.ToolsInPhase))
		}
		if len(rc.ArtifactsInPhase) > 0 {
			artifacts := rc.ArtifactsInPhase
			if len(artifacts) > 20 {
				artifacts = artifacts[len(artifacts)-20:]
			}
			b.WriteString(fmt.Sprintf("Files touched this phase (last %d): %s\n", len(artifacts), strings.Join(artifacts, ", ")))
		}
		b.WriteString("\n")
	}

	if len(rc.TaskHistory) > 0 {
		b.WriteString("TASK PLAN (full):\n")
		for _, t := range rc.TaskHistory {
			b.WriteString("  " + t + "\n")
		}
		b.WriteString("\n")
	} else if rc.ActivePlan != "" {
		b.WriteString("ACTIVE PLAN:\n")
		b.WriteString(rc.ActivePlan)
		b.WriteString("\n\n")
	}

	if len(rc.RecentMessages) > 0 {
		b.WriteString("CONVERSATION (this phase):\n")
		for _, m := range rc.RecentMessages {
			b.WriteString("  " + m + "\n")
		}
		b.WriteString("\n")
	}

	if len(rc.DreamMemories) > 0 {
		b.WriteString("RELEVANT MEMORIES:\n")
		for _, m := range rc.DreamMemories {
			b.WriteString("  - " + m + "\n")
		}
		b.WriteString("\n")
	}

	if len(rc.PriorGuidance) > 0 {
		b.WriteString("PRIOR GUIDANCE (already given — DO NOT repeat these, build on them or say something new):\n")
		for i, g := range rc.PriorGuidance {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, g))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// parseFindingsEvaluations extracts structured evaluations from the LLM response.
// Handles JSON embedded in markdown code blocks or raw JSON.
func parseFindingsEvaluations(response string, summaries []FindingSummary) ([]FindingsEvaluation, error) {
	// Try to extract JSON from the response
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in response")
	}

	var evaluations []FindingsEvaluation
	if err := json.Unmarshal([]byte(jsonStr), &evaluations); err != nil {
		// Try parsing as a single object wrapped in array
		var single FindingsEvaluation
		if err2 := json.Unmarshal([]byte(jsonStr), &single); err2 != nil {
			return nil, fmt.Errorf("parse evaluations JSON: %w (original: %w)", err2, err)
		}
		if len(summaries) != 1 {
			return nil, fmt.Errorf("single evaluation object returned for %d findings", len(summaries))
		}
		evaluations = []FindingsEvaluation{single}
	}

	// Validate and clamp scores
	for i := range evaluations {
		evaluations[i].Priority = math.Max(0, math.Min(100, evaluations[i].Priority))
		// Round to 2 decimal places
		evaluations[i].Priority = math.Round(evaluations[i].Priority*100) / 100
		// Enforce should_promote consistency
		evaluations[i].ShouldPromote = evaluations[i].Priority >= 70.0
	}

	if err := validateFindingsEvaluations(evaluations, summaries); err != nil {
		return nil, err
	}

	return evaluations, nil
}

func validateFindingsEvaluations(evaluations []FindingsEvaluation, summaries []FindingSummary) error {
	if len(evaluations) != len(summaries) {
		return fmt.Errorf("expected %d evaluations, got %d", len(summaries), len(evaluations))
	}

	expected := make(map[string]struct{}, len(summaries))
	for _, summary := range summaries {
		if strings.TrimSpace(summary.ID) == "" {
			return fmt.Errorf("finding summary has empty id")
		}
		expected[summary.ID] = struct{}{}
	}

	seen := make(map[string]struct{}, len(evaluations))
	for _, eval := range evaluations {
		id := strings.TrimSpace(eval.FindingID)
		if id == "" {
			return fmt.Errorf("evaluation has empty finding_id")
		}
		if _, ok := expected[id]; !ok {
			return fmt.Errorf("evaluation references unknown finding_id %q", id)
		}
		if _, ok := seen[id]; ok {
			return fmt.Errorf("evaluation duplicates finding_id %q", id)
		}
		seen[id] = struct{}{}
	}

	for id := range expected {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("missing evaluation for finding_id %q", id)
		}
	}
	return nil
}

func fallbackFindingsEvaluations(summaries []FindingSummary, cause error) []FindingsEvaluation {
	evaluations := make([]FindingsEvaluation, 0, len(summaries))
	for _, summary := range summaries {
		priority := summary.InitialPriority
		if priority <= 0 {
			priority = 50
		}
		if !summary.Success || summary.ErrorMessage != "" {
			priority = math.Max(priority, 80)
		}
		if containsAnyTag(summary.Tags, "error", "needs-attention") {
			priority = math.Max(priority, 80)
		}
		if containsAnyTag(summary.Tags, "evaluation", "pgr", "has-metrics", "insight") {
			priority = math.Max(priority, 75)
		}
		if summary.ToolName == "conversation-summary" && len(summary.OutputPreview) > 0 {
			priority = math.Max(priority, 72)
		}
		priority = math.Round(math.Max(0, math.Min(100, priority))*100) / 100

		tags := append([]string{}, summary.Tags...)
		if priority >= 70 && !containsAnyTag(tags, "anti-pattern", "recipe", "relationship", "insight", "memory") {
			tags = append(tags, "insight")
		}
		if !summary.Success || summary.ErrorMessage != "" {
			tags = appendUniqueString(tags, "anti-pattern")
		}

		reason := "Deterministic fallback evaluation used because the LLM findings evaluator returned invalid or incomplete output."
		if cause != nil {
			reason += " Cause: " + cause.Error()
		}
		if summary.ContextSummary != "" {
			reason += " Finding context: " + summary.ContextSummary
		}

		evaluations = append(evaluations, FindingsEvaluation{
			FindingID:     summary.ID,
			Priority:      priority,
			ShouldPromote: priority >= 70,
			Reasoning:     reason,
			Tags:          tags,
			Source:        "fallback",
		})
	}
	return evaluations
}

func containsAnyTag(tags []string, wanted ...string) bool {
	wantedSet := make(map[string]struct{}, len(wanted))
	for _, tag := range wanted {
		wantedSet[tag] = struct{}{}
	}
	for _, tag := range tags {
		if _, ok := wantedSet[tag]; ok {
			return true
		}
	}
	return false
}

func appendUniqueString(values []string, next string) []string {
	if slices.Contains(values, next) {
		return values
	}
	return append(values, next)
}

// extractJSON finds and returns the first JSON array or object in a string.
// Handles markdown code blocks (```json ... ```) and raw JSON.
func extractJSON(s string) string {
	// Try markdown code block first
	if idx := strings.Index(s, "```json"); idx >= 0 {
		start := idx + 7
		end := strings.Index(s[start:], "```")
		if end >= 0 {
			return strings.TrimSpace(s[start : start+end])
		}
	}
	if idx := strings.Index(s, "```"); idx >= 0 {
		start := idx + 3
		// Skip optional language identifier on same line
		if nl := strings.Index(s[start:], "\n"); nl >= 0 {
			start += nl + 1
		}
		end := strings.Index(s[start:], "```")
		if end >= 0 {
			candidate := strings.TrimSpace(s[start : start+end])
			if len(candidate) > 0 && (candidate[0] == '[' || candidate[0] == '{') {
				return candidate
			}
		}
	}

	// Try to find raw JSON array
	if idx := strings.Index(s, "["); idx >= 0 {
		// Find matching closing bracket
		depth := 0
		for i := idx; i < len(s); i++ {
			switch s[i] {
			case '[':
				depth++
			case ']':
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}

	// Try raw JSON object
	if idx := strings.Index(s, "{"); idx >= 0 {
		depth := 0
		for i := idx; i < len(s); i++ {
			switch s[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return s[idx : i+1]
				}
			}
		}
	}

	return ""
}

// suppress unused import warnings - strconv used by future callers
var _ = strconv.FormatFloat
