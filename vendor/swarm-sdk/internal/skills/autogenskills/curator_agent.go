package autogenskills

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// CuratorAgent spawns an LLM-powered sub-agent for semantic skill maintenance.
//
// It creates a restricted sub-agent with only SkillManage tools, gives it a
// curated system prompt describing the current skill library, and asks it to:
//   - Consolidate overlapping skills (merge into a unified skill)
//   - Patch outdated skills (update content that has drifted)
//   - Archive unused skills
//
// This matches Hermes's agent/curator.py forked AIAgent pattern.
//
// CONTRACT:
//   - One-shot execution: create, run, return results.
//   - Uses agent.Factory to create the sub-agent.
//   - Restricted toolset: SkillManage only (no terminal, no file I/O).
//   - Thread-safe: can be called from any goroutine.
//   - Does NOT auto-trigger; the caller decides when to run it.
type CuratorAgent struct {
	factory agent.Factory
	provCfg provider.Config
	service *Service
	logger  observability.Logger
}

// NewCuratorAgent creates a new LLM-powered curator sub-agent.
//
// CONTRACT:
//   - factory, provCfg (Name), and service must be non-nil/non-zero.
//   - Returns an error if any required dependency is missing.
func NewCuratorAgent(factory agent.Factory, provCfg provider.Config, service *Service, logger observability.Logger) (*CuratorAgent, error) {
	if factory == nil {
		return nil, fmt.Errorf("autogenskills: CuratorAgent requires non-nil factory")
	}
	if provCfg.Name == "" {
		return nil, fmt.Errorf("autogenskills: CuratorAgent requires provider config with non-empty Name")
	}
	if service == nil {
		return nil, fmt.Errorf("autogenskills: CuratorAgent requires non-nil service")
	}
	if logger == nil {
		return nil, fmt.Errorf("autogenskills: CuratorAgent requires non-nil logger")
	}

	return &CuratorAgent{
		factory: factory,
		provCfg: provCfg,
		service: service,
		logger:  logger,
	}, nil
}

// CuratorAgentResult holds the outcome of a curator sub-agent run.
type CuratorAgentResult struct {
	// Summary is the agent's final text summary of what it did.
	Summary string

	// TurnCount is the number of turns the sub-agent used.
	TurnCount int

	// TokensUsed is the total tokens consumed by the sub-agent.
	TokensUsed int

	// Duration is the wall-clock time for the curator run.
	Duration time.Duration

	// Error is non-nil if execution failed.
	Error error
}

// CuratorRunOptions controls one curator sub-agent pass.
type CuratorRunOptions struct {
	Results     []ReviewResult
	Consolidate bool
	// Preview permits only read-only SkillManage actions and suppresses usage
	// bumps, while asking the agent to report the actions it would take.
	Preview bool
}

// Run executes the curator sub-agent as a one-shot operation.
//
// It builds a system prompt with the current skill inventory, creates a
// sub-agent with the SkillManage tool as its only tool, and instructs it
// to review and maintain the skill library.
//
// CONTRACT:
//   - Creates a new sub-agent each call (no reuse).
//   - Marks the context as sub-agent so steering hooks skip it.
//   - Returns both CuratorAgentResult.Error and a non-nil Go error on failure,
//     so orchestration callers cannot mistake a failed maintenance pass for
//     success and consume the cadence window.
func (ca *CuratorAgent) Run(ctx context.Context) (*CuratorAgentResult, error) {
	return ca.RunWithOptions(ctx, CuratorRunOptions{})
}

// RunWithReview executes the curator sub-agent with pre-computed review results.
// When results are provided, the system prompt includes the rule-based curator's
// recommendations (archive, consolidate, patch), allowing the LLM to focus on
// execution rather than discovery.
func (ca *CuratorAgent) RunWithReview(ctx context.Context, results []ReviewResult, consolidate bool) (*CuratorAgentResult, error) {
	return ca.RunWithOptions(ctx, CuratorRunOptions{Results: results, Consolidate: consolidate})
}

// RunPreview executes a non-mutating review pass.
func (ca *CuratorAgent) RunPreview(ctx context.Context, results []ReviewResult, consolidate bool) (*CuratorAgentResult, error) {
	return ca.RunWithOptions(ctx, CuratorRunOptions{Results: results, Consolidate: consolidate, Preview: true})
}

// RunWithOptions executes a curator pass with explicit live/preview semantics.
func (ca *CuratorAgent) RunWithOptions(ctx context.Context, opts CuratorRunOptions) (*CuratorAgentResult, error) {
	start := time.Now()

	// Mark context as sub-agent so steering hooks skip it.
	ctx = agent.WithSubAgent(ctx)

	// Build the system prompt with current skill inventory and review results.
	systemPrompt := ca.buildCuratorPromptWithPreview(opts.Results, opts.Consolidate, opts.Preview)

	// Create the SkillManage tool.
	skillManageTool, err := NewSkillManageTool(ca.service)
	if err != nil {
		return nil, fmt.Errorf("autogenskills: curator agent: create SkillManage tool: %w", err)
	}
	skillManageTool.RequireReadBeforeWrite()
	if opts.Preview {
		skillManageTool.RestrictToPreview()
	}

	curatorCfg := ca.service.GetConfig().Curator.WithDefaults()
	timeout, err := time.ParseDuration(curatorCfg.Timeout)
	if err != nil || timeout <= 0 {
		return nil, fmt.Errorf("autogenskills: curator agent: invalid timeout %q", curatorCfg.Timeout)
	}

	// Create sub-agent with restricted toolset.
	agentID := fmt.Sprintf("curator-%d", time.Now().UnixNano())
	subAgentConfig := agent.SubAgentConfig{
		AgentID:        agentID,
		AgentName:      "Skill Curator",
		Description:    "Reviews and maintains the autogenerated skill library",
		SystemPrompt:   systemPrompt,
		Tools:          []string{"SkillManage"},
		ProviderConfig: ca.provCfg,
		MaxTurns:       curatorCfg.MaxTurns,
		Timeout:        timeout,
	}

	subAgent, err := ca.factory.CreateSubAgent(ctx, subAgentConfig)
	if err != nil {
		return nil, fmt.Errorf("autogenskills: curator agent: create sub-agent: %w", err)
	}

	// Register the SkillManage tool on the sub-agent's tool registry.
	if err := subAgent.ToolRegistry().Register(skillManageTool); err != nil {
		return nil, fmt.Errorf("autogenskills: curator agent: register SkillManage tool: %w", err)
	}

	ca.logger.Info(ctx, "curator_agent.sub_agent_created",
		observability.F("agent_id", agentID),
		observability.F("review_results", len(opts.Results)),
		observability.F("preview", opts.Preview),
	)

	// Wrap in SubAgentExecutor and execute.
	executor, err := agent.NewSubAgentExecutor(subAgent)
	if err != nil {
		return nil, fmt.Errorf("autogenskills: curator agent: create executor: %w", err)
	}

	task := curatorExecutionTask(opts.Consolidate, opts.Preview)
	if len(opts.Results) > 0 {
		task += " The rule-based curator has already identified specific recommendations above — focus on executing those."
	}

	result, err := executor.Execute(ctx, task)
	if err != nil {
		runErr := fmt.Errorf("curator sub-agent execution failed: %w", err)
		return &CuratorAgentResult{
			Summary:  "",
			Duration: time.Since(start),
			Error:    runErr,
		}, runErr
	}

	// Collect execution stats.
	stats := subAgent.Stats()

	ca.logger.Info(ctx, "curator_agent.completed",
		observability.F("agent_id", agentID),
		observability.F("duration", time.Since(start).String()),
		observability.F("turns", stats.TurnCount),
		observability.F("tokens", stats.TotalTokens),
	)

	return &CuratorAgentResult{
		Summary:    result,
		TurnCount:  stats.TurnCount,
		TokensUsed: stats.TotalTokens,
		Duration:   time.Since(start),
		Error:      nil,
	}, nil
}

// buildCuratorPrompt constructs the system prompt for the curator sub-agent.
// It includes the current skill inventory so the LLM can make informed decisions.
func (ca *CuratorAgent) buildCuratorPrompt(results []ReviewResult, consolidate bool) string {
	return ca.buildCuratorPromptWithPreview(results, consolidate, false)
}

// versionOrUnknown returns the version string or "?." if empty.
func versionOrUnknown(v string) string {
	if v == "" {
		return "?.?.?"
	}
	return v
}
