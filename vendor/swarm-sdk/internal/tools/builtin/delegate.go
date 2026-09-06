package builtin

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// DelegateParams are the typed parameters for the Delegate tool.
type DelegateParams struct {
	Task           string `json:"task"                     description:"The specific task to delegate. Be clear and self-contained — the delegate agent will work autonomously but can ask you questions." required:"true"`
	AgentID        string `json:"agent_id,omitempty"        description:"Agent type to invoke (e.g. 'general-assistant', 'research-agent'). Defaults to general-assistant."`
	SystemPrompt   string `json:"system_prompt,omitempty"   description:"Custom system prompt for the delegate. Overrides agent_id configuration."`
	Model          string `json:"model,omitempty"           description:"Optional model override for the delegate (e.g. 'sonnet', 'haiku')."`
	InheritContext bool   `json:"inherit_context,omitempty" description:"If true, the delegate receives your conversation summary and tool set as additional context."`
}

// DelegateEntry holds the runtime state of a spawned delegate.
type DelegateEntry struct {
	bg        *agent.BackgroundAgent
	qch       *DelegateQuestionChannel
	task      string
	spawnedAt time.Time
}

// DelegateRegistry stores all active delegates keyed by agent_id.
// Shared between DelegateTool (writes) and DelegateOutputTool (reads/answers).
type DelegateRegistry struct {
	mu      sync.RWMutex
	entries map[string]*DelegateEntry
}

// NewDelegateRegistry creates a new delegate registry.
func NewDelegateRegistry() *DelegateRegistry {
	return &DelegateRegistry{
		entries: make(map[string]*DelegateEntry),
	}
}

// Register adds a delegate entry to the registry.
func (r *DelegateRegistry) Register(agentID string, entry *DelegateEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[agentID] = entry
}

// Get retrieves a delegate entry by agent ID.
func (r *DelegateRegistry) Get(agentID string) (*DelegateEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[agentID]
	return e, ok
}

// Remove deletes a delegate entry from the registry.
func (r *DelegateRegistry) Remove(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, agentID)
}

// ───────────────────────────────────────────────────────────────────────────
// DelegateTool
// ───────────────────────────────────────────────────────────────────────────

// DelegateTool spawns a forked task agent that can ask the parent questions.
type DelegateTool struct {
	tools.BaseTool
	factory                agent.Factory
	registry               *DelegateRegistry
	bgManager              BackgroundAgentManager
	parentToolReg          tools.Registry
	providerConfig         provider.Config
	providerConfigResolver func(string) (provider.Config, error)
	contextWindowResolver  func(providerName, model string) int
	logger                 observability.Logger
	tracer                 observability.Tracer

	// contextSummariser optionally produces a short summary of parent conversation
	// to inject when inherit_context=true.
	contextSummariser func() string
}

// DelegateToolConfig configures the Delegate tool.
type DelegateToolConfig struct {
	Factory                agent.Factory
	Registry               *DelegateRegistry
	BGManager              BackgroundAgentManager
	ParentToolReg          tools.Registry
	ProviderConfig         provider.Config
	ProviderConfigResolver func(string) (provider.Config, error)
	ContextWindowResolver  func(providerName, model string) int
	Logger                 observability.Logger
	Tracer                 observability.Tracer
	ContextSummariser      func() string // optional
}

// NewDelegateTool creates a new Delegate tool.
func NewDelegateTool(cfg DelegateToolConfig) (tools.Tool, error) {
	if cfg.Factory == nil {
		return nil, sdkerr.Permanent("delegate.missing_factory", "agent factory is required")
	}
	if cfg.Registry == nil {
		return nil, sdkerr.Permanent("delegate.missing_registry", "delegate registry is required")
	}
	if cfg.BGManager == nil {
		return nil, sdkerr.Permanent("delegate.missing_bgmanager", "background manager is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = noop.NewLogger()
	}
	if cfg.Tracer == nil {
		cfg.Tracer = noop.NewTracer()
	}
	t := &DelegateTool{
		factory:                cfg.Factory,
		registry:               cfg.Registry,
		bgManager:              cfg.BGManager,
		parentToolReg:          cfg.ParentToolReg,
		providerConfig:         cfg.ProviderConfig,
		providerConfigResolver: cfg.ProviderConfigResolver,
		contextWindowResolver:  cfg.ContextWindowResolver,
		logger:                 cfg.Logger,
		tracer:                 cfg.Tracer,
		contextSummariser:      cfg.ContextSummariser,
	}
	return tools.Typed[DelegateParams](t), nil
}

func (t *DelegateTool) Name() string { return "Delegate" }

func (t *DelegateTool) Description() string {
	return `Spawn a focused agent that can ask the parent clarifying questions. Use DelegateOutput to poll, answer questions, check status, and retrieve the result; use Subagent instead for fully autonomous work.`
}

func (t *DelegateTool) Parameters() any                        { return tools.SchemaFor[DelegateParams]() }
func (t *DelegateTool) IsIdempotent() bool                     { return false }
func (t *DelegateTool) RequiresPermission() []tools.Permission { return nil }
func (t *DelegateTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Run spawns the delegate agent asynchronously and returns immediately.
func (t *DelegateTool) Run(ctx context.Context, p DelegateParams) (*tools.ToolResult, error) {
	if p.Task == "" {
		return nil, sdkerr.Permanent("delegate.empty_task", "task cannot be empty")
	}

	agentTypeID := p.AgentID
	if agentTypeID == "" {
		agentTypeID = "general-assistant"
	}

	// Pick provider config — use parent's config as base.
	// Value-copy the struct so we don't mutate the shared t.providerConfig.
	provCfg := t.providerConfig
	// Defensive copy of the Custom map: the struct copy above is shallow, so
	// the Custom map pointer is still shared across concurrent Delegate calls.
	// Copy it now (before any mutation) to prevent data races.
	if len(provCfg.Custom) > 0 {
		custom := make(map[string]any, len(provCfg.Custom))
		for k, v := range provCfg.Custom {
			custom[k] = v
		}
		provCfg.Custom = custom
	}
	if p.Model != "" {
		provCfg.Model = p.Model
	}
	provCfg = withResolvedContextWindow(provCfg, t.providerConfig, t.contextWindowResolver)

	// Build system prompt
	sysPrompt := p.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = fmt.Sprintf("You are a delegate agent (%s). Focus on the task and ask the parent using ask_parent when you need clarification.", agentTypeID)
	}

	// Create the agent via factory
	agentSubID := fmt.Sprintf("delegate-%s-%d", agentTypeID, time.Now().UnixNano())
	cfg := agent.SubAgentConfig{
		AgentID:        agentSubID,
		AgentName:      fmt.Sprintf("Delegate (%s)", agentTypeID),
		Description:    "Delegate agent — can ask parent questions via ask_parent tool",
		ProviderConfig: provCfg,
		SystemPrompt:   sysPrompt,
		MaxTurns:       0, // unlimited
		Timeout:        0, // no timeout
		// Temperature is intentionally NOT set here (stays 0).
		// Convention: 0 means "use provider default", matching CreateSubAgent paths.
		// provCfg.Temperature already carries any role/provider-configured value.
	}

	delegateAgent, err := t.factory.CreateSubAgent(ctx, cfg)
	if err != nil {
		return nil, sdkerr.Permanent("delegate.build_failed",
			fmt.Sprintf("failed to build delegate agent: %v", err))
	}

	// Build the enriched task with context prefix
	enrichedTask := t.buildEnrichedTask(p)

	// inherit_context includes the parent's executable tool surface, not merely
	// a textual conversation summary. Resolve through the same alias and
	// recursive-control filter used by Subagent.
	if p.InheritContext && t.parentToolReg != nil {
		parentSimpleReg, isSimpleReg := t.parentToolReg.(*tools.SimpleRegistry)
		var toolNames []string
		if isSimpleReg {
			toolNames = parentSimpleReg.ListAll()
		} else {
			toolNames = t.parentToolReg.List()
		}

		resolvedNames := resolveSubagentToolNames([]string{"*"}, toolNames)
		copiedNames := make([]string, 0, len(resolvedNames))
		for _, toolName := range resolvedNames {
			var inheritedTool tools.Tool
			var getErr error
			if isSimpleReg {
				inheritedTool, _, getErr = parentSimpleReg.IncludingDisabled(toolName)
			} else {
				inheritedTool, getErr = t.parentToolReg.Get(toolName)
			}
			if getErr != nil {
				t.logger.Warn(ctx, "delegate.tool_copy_failed",
					observability.F("tool", toolName),
					observability.F("error", getErr.Error()))
				continue
			}
			if regErr := delegateAgent.ToolRegistry().Register(inheritedTool); regErr != nil {
				t.logger.Warn(ctx, "delegate.tool_register_failed",
					observability.F("tool", toolName),
					observability.F("error", regErr.Error()))
				continue
			}
			copiedNames = append(copiedNames, toolName)
		}

		if isSimpleReg {
			if childSimpleReg, ok := delegateAgent.ToolRegistry().(*tools.SimpleRegistry); ok {
				if checker := parentSimpleReg.PermissionChecker(); checker != nil {
					childSimpleReg.SetPermissionChecker(checker)
				}
			}
		}
		t.logger.Info(ctx, "delegate.tools_copied",
			observability.F("agent_id", agentSubID),
			observability.F("tools", copiedNames))
	}

	// Create question channel for parent↔delegate Q&A
	qch := &DelegateQuestionChannel{}

	// Inject ask_parent tool into delegate's tool registry
	agentID := agentSubID
	askTool := newAskParentTool(agentID, qch)
	if reg := delegateAgent.ToolRegistry(); reg != nil {
		if regErr := reg.Register(askTool); regErr != nil {
			t.logger.Warn(ctx, "delegate.ask_parent_register_failed",
				observability.F("agent_id", agentID),
				observability.F("error", regErr.Error()))
		}
	}

	// Wrap in BackgroundAgent and start
	bgAgent, err := agent.NewBackgroundAgent(agent.BackgroundAgentConfig{
		Agent:           delegateAgent,
		ParentID:        tools.OwnerAgentID(ctx),
		EventBufferSize: 100,
		Logger:          t.logger,
		Tracer:          t.tracer,
	})
	if err != nil {
		return nil, sdkerr.Permanent("delegate.background_create_failed",
			fmt.Sprintf("failed to create background agent: %v", err))
	}

	bgCtx := detachedSubagentContext(ctx)
	if err := bgAgent.Start(bgCtx, enrichedTask); err != nil {
		return nil, sdkerr.Permanent("delegate.start_failed",
			fmt.Sprintf("failed to start delegate: %v", err))
	}

	// Register with the shared background manager before returning an ID. If
	// registration fails, cancel the orphaned run so callers never receive an
	// agent ID that DelegateOutput/notifications cannot track consistently.
	if err := t.bgManager.Add(bgAgent, p.Task); err != nil {
		bgAgent.Cancel()
		t.logger.Error(ctx, "delegate.bgmanager_register_failed",
			observability.F("agent_id", agentID),
			observability.F("error", err.Error()))
		return nil, sdkerr.Permanent("delegate.background_registration_failed",
			fmt.Sprintf("delegate started but could not be registered: %v", err))
	}

	// Register in the delegate-specific Q&A registry only after shared
	// background registration succeeds.
	entry := &DelegateEntry{
		bg:        bgAgent,
		qch:       qch,
		task:      p.Task,
		spawnedAt: time.Now(),
	}
	t.registry.Register(agentID, entry)

	bgAgent.StartProgressTracker(15 * time.Second)

	t.logger.Info(ctx, "delegate.spawned",
		observability.F("agent_id", agentID),
		observability.F("task_len", len(p.Task)),
		observability.F("inherit_context", p.InheritContext))

	outputFile := bgAgent.OutputFilePath()

	return &tools.ToolResult{
		Output: fmt.Sprintf(`Delegate launched: agent_id=%s
status=async_launched
output_file=%s
task=%s

Use DelegateOutput with this agent_id to poll, answer questions, check status, or retrieve the result.`,
			agentID,
			outputFile,
			p.Task,
		),
	}, nil
}

// buildEnrichedTask constructs the full task string with context prefix.
func (t *DelegateTool) buildEnrichedTask(p DelegateParams) string {
	prefix := "[DELEGATE TASK]\n"

	if p.InheritContext && t.contextSummariser != nil {
		summary := t.contextSummariser()
		if summary != "" {
			prefix += fmt.Sprintf("[PARENT CONTEXT SUMMARY]\n%s\n[/PARENT CONTEXT SUMMARY]\n\n", summary)
		}
	}

	prefix += fmt.Sprintf(`Complete the parent's task. Use ask_parent only when genuine ambiguity would make guessing unsafe.

[TASK]
%s
[/TASK]`, p.Task)

	return prefix
}
