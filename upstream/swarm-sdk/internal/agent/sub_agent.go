// Package agent provides specialized sub-agent patterns for task delegation.
package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-core/pkg/pool"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// SubAgentExecutor wraps an Agent for delegation patterns.
// Sub-agents are just agents that are called by other agents - no artificial constraints.
type SubAgentExecutor struct {
	agent *Agent
}

// Global pool manager for agent execution
var (
	agentPoolManager *pool.Manager
	agentPoolMu      sync.RWMutex
)

// SetAgentPoolManager sets the global pool manager for agent execution
func SetAgentPoolManager(pm *pool.Manager) {
	agentPoolMu.Lock()
	defer agentPoolMu.Unlock()
	agentPoolManager = pm
}

// getAgentPoolManager returns the global pool manager
func getAgentPoolManager() *pool.Manager {
	agentPoolMu.RLock()
	defer agentPoolMu.RUnlock()
	return agentPoolManager
}

// NewSubAgentExecutor wraps an existing agent as a sub-agent executor.
func NewSubAgentExecutor(agent *Agent) (*SubAgentExecutor, error) {
	if agent == nil {
		return nil, sdkerr.Permanent("sub_agent.nil_agent", "agent cannot be nil")
	}

	return &SubAgentExecutor{
		agent: agent,
	}, nil
}

// Execute runs the sub-agent with the given task.
// Each execution is isolated (new conversation).
func (s *SubAgentExecutor) Execute(ctx context.Context, task string) (string, error) {
	return s.ExecuteWithContext(ctx, task, nil)
}

// ExecuteWithContext runs the sub-agent with additional context.
func (s *SubAgentExecutor) ExecuteWithContext(ctx context.Context, task string, additionalContext map[string]any) (string, error) {
	// Build execution request
	req := ExecuteRequest{
		Message: s.buildTaskMessage(task, additionalContext),

		// Each sub-agent execution is isolated (new conversation)
		ConversationID: "",

		// Use agent's configured limits
		MaxTurns: 0, // Use agent default
		Timeout:  0, // Use agent default

		Context: additionalContext,
	}

	// Execute
	resp, err := s.agent.Execute(ctx, req)
	if err != nil {
		return "", s.categorizeSubAgentError(err)
	}

	return resp.Message, nil
}

// buildTaskMessage constructs the task message with optional context.
func (s *SubAgentExecutor) buildTaskMessage(task string, ctx map[string]any) string {
	if len(ctx) == 0 {
		return task
	}

	// Include context as structured information
	msg := task + "\n\nAdditional Context:\n"
	for key, value := range ctx {
		msg += fmt.Sprintf("- %s: %v\n", key, value)
	}

	return msg
}

// categorizeSubAgentError categorizes errors from sub-agent execution.
func (s *SubAgentExecutor) categorizeSubAgentError(err error) error {
	// If already an SDK error with a category, return as-is
	if sdkerr.GetType(err) != "" {
		return err
	}

	// Default to permanent for unknown errors
	return sdkerr.Permanent("sub_agent.execution_failed", err.Error())
}

// Agent returns the underlying agent.
func (s *SubAgentExecutor) Agent() *Agent {
	return s.agent
}

// Stats returns execution statistics for the sub-agent.
func (s *SubAgentExecutor) Stats() AgentStats {
	return s.agent.Stats()
}

// SubAgentPool manages a pool of reusable sub-agent executors.
// This enables efficient sub-agent reuse for common tasks.
type SubAgentPool struct {
	executors map[string]*SubAgentExecutor
	factory   Factory
	logger    observability.Logger
	tracer    observability.Tracer
	mu        sync.RWMutex
}

// PoolConfig configures a sub-agent pool.
type PoolConfig struct {
	// Factory creates new sub-agents when needed.
	Factory Factory

	// Logger for observability.
	Logger observability.Logger

	// Tracer for distributed tracing.
	Tracer observability.Tracer
}

// NewSubAgentPool creates a new sub-agent pool.
func NewSubAgentPool(config PoolConfig) (*SubAgentPool, error) {
	if config.Factory == nil {
		return nil, sdkerr.Permanent("sub_agent_pool.missing_factory",
			"factory is required")
	}

	if config.Logger == nil {
		return nil, sdkerr.Permanent("sub_agent_pool.missing_logger",
			"logger is required")
	}

	if config.Tracer == nil {
		return nil, sdkerr.Permanent("sub_agent_pool.missing_tracer",
			"tracer is required")
	}

	return &SubAgentPool{
		executors: make(map[string]*SubAgentExecutor),
		factory:   config.Factory,
		logger:    config.Logger,
		tracer:    config.Tracer,
	}, nil
}

// GetOrCreate retrieves an existing sub-agent or creates a new one.
func (p *SubAgentPool) FindOrCreate(ctx context.Context, id string, config SubAgentConfig) (*SubAgentExecutor, error) {
	ctx, span := p.tracer.StartSpan(ctx, "sub_agent_pool.get_or_create")
	defer span.End()

	span.SetAttribute("agent_id", id)

	// Hold the write lock through creation so concurrent requests for the same
	// ID are single-flight and cannot create distinct executors or race on the
	// map. Sub-agent construction is infrequent compared with pool reads.
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if executor already exists
	if exec, exists := p.executors[id]; exists {
		p.logger.Debug(ctx, "sub_agent_pool.cache_hit",
			observability.F("agent_id", id))
		return exec, nil
	}

	// Create new sub-agent
	p.logger.Info(ctx, "sub_agent_pool.creating_agent",
		observability.F("agent_id", id))

	config.AgentID = id // Ensure ID matches
	agent, err := p.factory.CreateSubAgent(ctx, config)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Wrap as executor
	executor, err := NewSubAgentExecutor(agent)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Store in pool
	p.executors[id] = executor

	p.logger.Info(ctx, "sub_agent_pool.agent_created",
		observability.F("agent_id", id))

	return executor, nil
}

// Execute runs a task using a named sub-agent from the pool.
func (p *SubAgentPool) Execute(ctx context.Context, agentID string, task string) (string, error) {
	ctx, span := p.tracer.StartSpan(ctx, "sub_agent_pool.execute")
	defer span.End()

	span.SetAttribute("agent_id", agentID)
	span.SetAttribute("task_length", len(task))

	// Get executor
	p.mu.RLock()
	executor, exists := p.executors[agentID]
	p.mu.RUnlock()
	if !exists {
		err := sdkerr.Permanent("sub_agent_pool.not_found",
			fmt.Sprintf("sub-agent '%s' not found in pool", agentID))
		span.RecordError(err)
		return "", err
	}

	// Execute
	result, err := executor.Execute(ctx, task)
	if err != nil {
		span.RecordError(err)
		return "", err
	}

	return result, nil
}

// Size returns the number of sub-agents in the pool.
func (p *SubAgentPool) Size() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.executors)
}

// List returns all sub-agent IDs in the pool.
func (p *SubAgentPool) List() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	ids := make([]string, 0, len(p.executors))
	for id := range p.executors {
		ids = append(ids, id)
	}
	return ids
}

// Remove removes a sub-agent from the pool.
func (p *SubAgentPool) Remove(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.executors[id]; !exists {
		return sdkerr.Permanent("sub_agent_pool.not_found",
			fmt.Sprintf("sub-agent '%s' not found in pool", id))
	}

	delete(p.executors, id)
	return nil
}

// Clear removes all sub-agents from the pool.
func (p *SubAgentPool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.executors = make(map[string]*SubAgentExecutor)
}

// PresetSubAgentType defines common sub-agent types.
type PresetSubAgentType string

const (
	// PresetCodeFormatter formats code snippets.
	PresetCodeFormatter PresetSubAgentType = "code_formatter"

	// PresetTextSummarizer summarizes text.
	PresetTextSummarizer PresetSubAgentType = "text_summarizer"

	// PresetDataValidator validates data structures.
	PresetDataValidator PresetSubAgentType = "data_validator"

	// PresetErrorAnalyzer analyzes error messages.
	PresetErrorAnalyzer PresetSubAgentType = "error_analyzer"

	// PresetQuestionAnswerer answers specific questions.
	PresetQuestionAnswerer PresetSubAgentType = "question_answerer"
)

// CreatePresetSubAgent creates a sub-agent from a preset type.
// This provides common sub-agent configurations out of the box.
func CreatePresetSubAgent(ctx context.Context, factory Factory, presetType PresetSubAgentType, providerConfig provider.Config) (*Agent, error) {
	return CreatePresetSubAgentWithID(ctx, factory, presetType, providerConfig, "")
}

// CreatePresetSubAgentWithID creates a preset sub-agent with a caller-provided
// invocation ID. Supplying the unique ID before factory creation ensures any
// agent registry observes the same identity used by execution and streaming.
func CreatePresetSubAgentWithID(
	ctx context.Context,
	factory Factory,
	presetType PresetSubAgentType,
	providerConfig provider.Config,
	agentID string,
) (*Agent, error) {
	config := getPresetConfig(presetType, providerConfig)
	if agentID != "" {
		config.AgentID = agentID
	}
	return factory.CreateSubAgent(ctx, config)
}

// getPresetConfig returns the configuration for a preset sub-agent type.
func getPresetConfig(presetType PresetSubAgentType, providerConfig provider.Config) SubAgentConfig {
	// Check if this is an OAuth provider
	isOAuth := false
	if providerConfig.Custom != nil {
		if oauth, ok := providerConfig.Custom["is_oauth"].(bool); ok {
			isOAuth = oauth
		}
	}

	// For OAuth providers, use empty system prompts to allow only the OAuth prefix
	// For non-OAuth providers, use specialized system prompts
	codeFormatterPrompt := "You are a code formatting specialist. Format code clearly and follow language best practices. Return only the formatted code."
	textSummarizerPrompt := "You are a text summarization specialist. Extract key points and create concise summaries. Be direct and factual."
	dataValidatorPrompt := "You are a data validation specialist. Check data for correctness, completeness, and format compliance. Report issues clearly."
	errorAnalyzerPrompt := "You are an error analysis specialist. Explain errors clearly, identify root causes, and suggest concrete fixes."
	questionAnswererPrompt := "You are a question answering specialist. Provide direct, factual answers. If uncertain, say so."

	if isOAuth {
		// For OAuth, use empty prompts - the provider will add the OAuth prefix
		codeFormatterPrompt = ""
		textSummarizerPrompt = ""
		dataValidatorPrompt = ""
		errorAnalyzerPrompt = ""
		questionAnswererPrompt = ""
	}

	// readOnlyTools is the allow-list for presets that need to inspect the
	// filesystem (answer questions, validate files, analyze errors that
	// reference source). It lists BOTH registry naming conventions so the
	// preset receives working tools regardless of whether the parent uses the
	// forge registry (TUI: "Read"/"Grep") or the builtin registry
	// ("file_read"/"grep"/"list_dir"). An empty allow-list (the previous value)
	// matched NO tools — copyAll is true only for ["*"] — which left these
	// presets tool-starved, causing the model to hallucinate tool names
	// (shell/bash/read_file) and loop until timeout.
	readOnlyTools := []string{
		"Read", "Grep", // forge (TUI)
		"file_read", "grep", "list_dir", // builtin
		"semantic_grep", // both
	}

	configs := map[PresetSubAgentType]SubAgentConfig{
		PresetCodeFormatter: {
			AgentName:    "Code Formatter",
			Description:  "Formats code snippets according to language conventions",
			SystemPrompt: codeFormatterPrompt,
			// Operates on code passed in the task text; no filesystem access needed.
			Tools: []string{},
		},
		PresetTextSummarizer: {
			AgentName:    "Text Summarizer",
			Description:  "Summarizes text into concise key points",
			SystemPrompt: textSummarizerPrompt,
			// Operates on text passed in the task; no filesystem access needed.
			Tools: []string{},
		},
		PresetDataValidator: {
			AgentName:    "Data Validator",
			Description:  "Validates data structures and formats",
			SystemPrompt: dataValidatorPrompt,
			// Often asked to verify files/data on disk → needs read access.
			Tools: readOnlyTools,
		},
		PresetErrorAnalyzer: {
			AgentName:    "Error Analyzer",
			Description:  "Analyzes error messages and suggests fixes",
			SystemPrompt: errorAnalyzerPrompt,
			// Error analysis usually requires reading the referenced source.
			Tools: readOnlyTools,
		},
		PresetQuestionAnswerer: {
			AgentName:    "Question Answerer",
			Description:  "Answers specific factual questions",
			SystemPrompt: questionAnswererPrompt,
			// Answering codebase questions requires reading/searching files.
			Tools: readOnlyTools,
		},
	}

	config := configs[presetType]
	config.AgentID = string(presetType)
	config.ProviderConfig = providerConfig

	return config
}

// SubAgentResult wraps a sub-agent execution result with metadata.
type SubAgentResult struct {
	// Result is the sub-agent's response.
	Result string

	// AgentID identifies which sub-agent produced this result.
	AgentID string

	// Duration is how long execution took.
	Duration time.Duration

	// TurnCount is the number of turns used.
	TurnCount int

	// TokensUsed is the total tokens consumed.
	TokensUsed int

	// CostUSD is the estimated cost.
	CostUSD float64

	// Metadata contains additional information.
	Metadata map[string]any
}

// ExecuteMultiple executes the same task across multiple sub-agents concurrently.
// Returns results from all sub-agents. Useful for comparing approaches or redundancy.
func ExecuteMultiple(ctx context.Context, executors []*SubAgentExecutor, task string) ([]SubAgentResult, error) {
	if len(executors) == 0 {
		return nil, sdkerr.Permanent("sub_agent.no_executors", "no executors provided")
	}

	// Execute all concurrently
	type result struct {
		idx   int
		res   *ExecuteResponse
		err   error
		agent string
	}

	results := make(chan result, len(executors))

	// Check if we have a pool manager
	poolManager := getAgentPoolManager()
	usePool := poolManager != nil && len(executors) > 2

	for i, executor := range executors {
		idx, exec := i, executor // Capture loop variables

		executeFunc := func() {
			resp, err := exec.agent.Execute(ctx, ExecuteRequest{
				Message: task,
			})

			results <- result{
				idx:   idx,
				res:   resp,
				err:   err,
				agent: exec.agent.ID(),
			}
		}

		if usePool {
			// Submit to pool
			err := poolManager.Submit(ctx, pool.PoolTypeAgents, executeFunc)
			if err != nil {
				// Pool is full, fall back to goroutine
				go executeFunc()
			}
		} else {
			// Direct goroutine execution
			go executeFunc()
		}
	}

	// Collect results
	subResults := make([]SubAgentResult, len(executors))
	var firstErr error

	for i := 0; i < len(executors); i++ {
		r := <-results

		if r.err != nil && firstErr == nil {
			firstErr = r.err
		}

		if r.res != nil {
			subResults[r.idx] = SubAgentResult{
				Result:     r.res.Message,
				AgentID:    r.agent,
				Duration:   r.res.Duration,
				TurnCount:  r.res.TurnCount,
				TokensUsed: r.res.TokensUsed,
				CostUSD:    r.res.CostUSD,
				Metadata:   r.res.Metadata,
			}
		}
	}

	// If all failed, return first error
	if firstErr != nil {
		allFailed := true
		for _, sr := range subResults {
			if sr.Result != "" {
				allFailed = false
				break
			}
		}
		if allFailed {
			return nil, firstErr
		}
	}

	return subResults, nil
}

// SelectBestResult chooses the best result from multiple sub-agent executions.
// Uses a simple heuristic: shortest non-empty response (assumes focused answer).
// For production, consider using a steering agent for evaluation.
func SelectBestResult(results []SubAgentResult) (*SubAgentResult, error) {
	if len(results) == 0 {
		return nil, sdkerr.Permanent("sub_agent.no_results", "no results to select from")
	}

	var best *SubAgentResult
	bestScore := -1

	for i := range results {
		if results[i].Result == "" {
			continue // Skip empty results
		}

		// Simple heuristic: prefer shorter, faster responses
		score := len(results[i].Result)
		if results[i].Duration < 5*time.Second {
			score -= 100 // Bonus for fast execution
		}

		if best == nil || score < bestScore {
			best = &results[i]
			bestScore = score
		}
	}

	if best == nil {
		return nil, sdkerr.Permanent("sub_agent.all_empty", "all results were empty")
	}

	return best, nil
}

// DelegateTask is a helper function to quickly delegate a task to a sub-agent.
// This is the simplest way to use sub-agents without managing pools or executors.
func DelegateTask(ctx context.Context, factory Factory, task string, providerConfig provider.Config) (string, error) {
	// Create ephemeral sub-agent
	config := SubAgentConfig{
		AgentID:        fmt.Sprintf("ephemeral-%d", time.Now().UnixNano()),
		AgentName:      "Task Delegator",
		Description:    "Ephemeral sub-agent for task delegation",
		SystemPrompt:   "Complete the requested task efficiently and accurately.",
		Tools:          []string{},
		ProviderConfig: providerConfig,
	}

	agent, err := factory.CreateSubAgent(ctx, config)
	if err != nil {
		return "", err
	}

	executor, err := NewSubAgentExecutor(agent)
	if err != nil {
		return "", err
	}

	return executor.Execute(ctx, task)
}
