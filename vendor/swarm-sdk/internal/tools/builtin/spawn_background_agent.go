// Package builtin provides spawn_background_agent tool for async task execution.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// SpawnBackgroundAgentTool allows the main agent to spawn long-running background tasks.

// SpawnBackgroundAgentParams are the typed parameters for the spawn_background_agent tool.
type SpawnBackgroundAgentParams struct {
	Task         string   `json:"task"                  description:"The long-running task for the background agent to perform."                                                               required:"true"`
	AgentID      string   `json:"agent_id,omitempty"    description:"Optional custom ID for tracking this background agent. If omitted, an ID will be auto-generated."`
	Tools        []string `json:"tools,omitempty"       description:"Optional list of tool names the background agent can use. If omitted, the agent has access to all available tools."`
	Model        string   `json:"model,omitempty"       description:"Optional model to use for the background agent."`
	SystemPrompt string   `json:"system_prompt,omitempty" description:"Optional custom system prompt for the background agent."`
	Resume       string   `json:"resume,omitempty"      description:"Optional agent_id of a prior BackgroundTask run to resume."`
}

type SpawnBackgroundAgentTool struct {
	tools.BaseTool

	factory               agent.Factory
	bgManager             BackgroundAgentManager
	providerConfig        provider.Config
	parentToolReg         tools.Registry // Parent's tool registry to copy tools from
	logger                observability.Logger
	tracer                observability.Tracer
	currentProviderGetter func() string // Returns current provider name from active profile
}

// SpawnBackgroundAgentConfig configures the spawn_background_agent tool.
type SpawnBackgroundAgentConfig struct {
	Factory               agent.Factory
	BGManager             BackgroundAgentManager
	ProviderConfig        provider.Config
	ParentToolReg         tools.Registry // Parent's tool registry to copy tools from
	Logger                observability.Logger
	Tracer                observability.Tracer
	CurrentProviderGetter func() string // Returns current provider name from active profile
}

// NewSpawnBackgroundAgentTool creates a new spawn_background_agent tool.
func NewSpawnBackgroundAgentTool(config SpawnBackgroundAgentConfig) (*SpawnBackgroundAgentTool, error) {
	if config.Factory == nil {
		return nil, sdkerr.Permanent("spawn_background_agent.missing_factory", "factory is required")
	}
	if config.BGManager == nil {
		return nil, sdkerr.Permanent("spawn_background_agent.missing_manager", "background manager is required")
	}
	if config.Logger == nil {
		return nil, sdkerr.Permanent("spawn_background_agent.missing_logger", "logger is required")
	}
	if config.Tracer == nil {
		return nil, sdkerr.Permanent("spawn_background_agent.missing_tracer", "tracer is required")
	}

	return &SpawnBackgroundAgentTool{
		factory:               config.Factory,
		bgManager:             config.BGManager,
		providerConfig:        config.ProviderConfig,
		parentToolReg:         config.ParentToolReg,
		logger:                config.Logger,
		tracer:                config.Tracer,
		currentProviderGetter: config.CurrentProviderGetter,
	}, nil
}

// Name returns the tool name.
func (t *SpawnBackgroundAgentTool) Name() string {
	return "BackgroundTask"
}

// Description returns the tool description.
func (t *SpawnBackgroundAgentTool) Description() string {
	return "Launch a background agent for long-running work that shouldn't block the conversation. " +
		"Use for large-scale analysis, multi-file refactoring, or operations that may take minutes. " +
		"Returns immediately with an agent_id and output_file path. " +
		"Use TaskOutput(agent_id, action='result') to retrieve output, or Read the output_file directly for a live view while running. " +
		"Do not duplicate the agent's work on the same files while it is running."
}

// IsIdempotent returns false since spawning background agents creates new instances each time.
func (t *SpawnBackgroundAgentTool) IsIdempotent() bool {
	return false
}

// Validate checks if the given parameters are valid for this tool.
func (t *SpawnBackgroundAgentTool) Validate(params map[string]any) error {
	task, ok := params["task"].(string)
	if !ok || task == "" {
		return sdkerr.Permanent("spawn_background_agent.missing_task", "task parameter is required")
	}
	return nil
}

// OptimizationHints provides guidance for efficient tool use.
func (t *SpawnBackgroundAgentTool) OptimizationHints() *tools.OptimizationHints {
	return nil // Use defaults
}

// RequiresPermission returns the required permissions for this tool.
func (t *SpawnBackgroundAgentTool) RequiresPermission() []tools.Permission {
	return nil // No special permissions required
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *SpawnBackgroundAgentTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// SupportsParallel returns true because spawning background agents is safe to execute concurrently.
// The background agent registration uses thread-safe data structures and each agent is independent.
func (t *SpawnBackgroundAgentTool) SupportsParallel() bool {
	return true
}

// Parameters returns the JSON schema for tool parameters.
// Parameters returns the JSON schema for tool parameters.
func (t *SpawnBackgroundAgentTool) Parameters() any {
	return tools.SchemaFor[SpawnBackgroundAgentParams]()
}

// Execute executes the spawn_background_agent tool.
// Execute implements tools.Tool by decoding rawParams into SpawnBackgroundAgentParams and calling Run.
func (t *SpawnBackgroundAgentTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, fmt.Errorf("spawn_background_agent: marshal params: %w", err)
	}
	var p SpawnBackgroundAgentParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("spawn_background_agent: unmarshal params: %w", err)
	}
	return t.Run(ctx, p)
}

// Run executes the spawn_background_agent tool with fully typed parameters.
func (t *SpawnBackgroundAgentTool) Run(ctx context.Context, p SpawnBackgroundAgentParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "spawn_background_agent.execute")
	defer span.End()
	if p.Task == "" {
		err := sdkerr.Permanent("spawn_background_agent.missing_task", "task parameter is required")
		span.RecordError(err)
		return nil, err
	}
	task := p.Task
	agentID := p.AgentID
	if agentID == "" {
		agentID = fmt.Sprintf("bg-%d", time.Now().UnixNano())
	}
	model := p.Model
	systemPrompt := p.SystemPrompt
	resumeID := p.Resume
	toolsList := p.Tools
	// If resuming a prior run, load and prepend its output as context.
	if resumeID != "" {
		priorCtx, err := agent.BuildResumeContext(resumeID)
		if err != nil {
			t.logger.Warn(ctx, "spawn_background_agent.resume_failed",
				observability.F("resume_id", resumeID),
				observability.F("error", err.Error()))
		} else if priorCtx != "" {
			task = priorCtx + task
			t.logger.Info(ctx, "spawn_background_agent.resume_loaded",
				observability.F("resume_id", resumeID),
				observability.F("prior_context_len", len(priorCtx)))
		}
	}

	// Default to all tools if none specified
	if len(toolsList) == 0 {
		toolsList = []string{"*"}
	}

	span.SetAttribute("agent_id", agentID)
	span.SetAttribute("task_length", len(task))
	span.SetAttribute("tools_count", len(toolsList))

	t.logger.Info(ctx, "spawn_background_agent.starting",
		observability.F("agent_id", agentID),
		observability.F("task", task),
		observability.F("tools", toolsList))

	// Create background agent config
	bgConfig := agent.BackgroundConfig{
		AgentID:        agentID,
		AgentName:      "Background Task Agent",
		Description:    "Background agent for async task execution",
		ProviderConfig: t.getProviderConfig(model),
		Tools:          toolsList,
		SystemPrompt:   t.getSystemPrompt(systemPrompt),
		MaxTurns:       0,                // Background agents: unlimited turns (user disabled limits)
		Timeout:        30 * time.Minute, // 30 minutes timeout
		Temperature:    0.7,
	}

	// Create background agent
	baseAgent, err := t.factory.CreateBackground(ctx, bgConfig)
	if err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "spawn_background_agent.creation_failed",
			observability.F("error", err.Error()))
		return nil, sdkerr.Permanent("spawn_background_agent.creation_failed",
			fmt.Sprintf("failed to create background agent: %v", err))
	}

	// Copy tools from parent registry to background agent's registry
	// IMPORTANT: Use ListAll/GetIncludingDisabled to access ALL tools from the parent,
	// including ones the user disabled on the parent. Background agents have their own
	// tool configurations and should NOT inherit the parent's disabled-tool restrictions.
	if t.parentToolReg != nil {
		bgToolReg := baseAgent.ToolRegistry()

		// Try to access ALL tools (including disabled) via SimpleRegistry
		parentSimpleReg, isSimpleReg := t.parentToolReg.(*tools.SimpleRegistry)

		var toolNames []string
		if isSimpleReg {
			toolNames = parentSimpleReg.ListAll()
		} else {
			toolNames = t.parentToolReg.List()
		}

		// Build set of allowed tools from background agent config
		bgDef := baseAgent.Definition()
		bgAllowedTools := bgDef.ToolHints
		copyAll := len(bgAllowedTools) == 0 || (len(bgAllowedTools) == 1 && bgAllowedTools[0] == "*")

		allowedToolsSet := make(map[string]bool)
		if !copyAll {
			for _, tn := range bgAllowedTools {
				allowedToolsSet[tn] = true
			}
		}

		for _, toolName := range toolNames {
			// Skip self-referential tools to prevent infinite recursion
			if toolName == "BackgroundTask" || toolName == "TaskOutput" {
				continue
			}

			// Check if this tool is allowed for the background agent
			if !copyAll && !allowedToolsSet[toolName] {
				t.logger.Debug(ctx, "spawn_background_agent.tool_skipped_not_allowed",
					observability.F("tool", toolName),
					observability.F("agent_id", agentID))
				continue
			}

			// Get tool - use GetIncludingDisabled if available to bypass parent restrictions
			var tool tools.Tool
			if isSimpleReg {
				var getErr error
				tool, _, getErr = parentSimpleReg.IncludingDisabled(toolName)
				if getErr != nil {
					t.logger.Warn(ctx, "spawn_background_agent.tool_copy_failed",
						observability.F("tool", toolName),
						observability.F("error", getErr.Error()))
					continue
				}
			} else {
				var getErr error
				tool, getErr = t.parentToolReg.Get(toolName)
				if getErr != nil {
					t.logger.Warn(ctx, "spawn_background_agent.tool_copy_failed",
						observability.F("tool", toolName),
						observability.F("error", getErr.Error()))
					continue
				}
			}

			if err := bgToolReg.Register(tool); err != nil {
				t.logger.Warn(ctx, "spawn_background_agent.tool_register_failed",
					observability.F("tool", toolName),
					observability.F("error", err.Error()))
			} else {
				t.logger.Debug(ctx, "spawn_background_agent.tool_copied",
					observability.F("tool", toolName))
			}
		}
		t.logger.Info(ctx, "spawn_background_agent.tools_copied",
			observability.F("agent_id", agentID),
			observability.F("tools_available", bgToolReg.List()))

		// Copy permission checker from parent registry to background agent registry
		if isSimpleReg {
			if bgSimpleReg, ok := bgToolReg.(*tools.SimpleRegistry); ok {
				if parentChecker := parentSimpleReg.PermissionChecker(); parentChecker != nil {
					bgSimpleReg.SetPermissionChecker(parentChecker)
					t.logger.Debug(ctx, "spawn_background_agent.permission_checker_inherited",
						observability.F("agent_id", agentID))
				}
			}
		}
	}

	// Wrap in BackgroundAgent for async execution
	bgAgent, err := agent.NewBackgroundAgent(agent.BackgroundAgentConfig{
		Agent:           baseAgent,
		ParentID:        tools.OwnerAgentID(ctx),
		EventBufferSize: 100,
		Logger:          t.logger,
		Tracer:          t.tracer,
	})
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Build context-enriched task for background agent
	enrichedTask := buildContextPrefix("background") + task + "\n[/TASK]"

	// Start the background agent (async).
	//
	// IMPORTANT (issue #232): this must NOT be a bare context.Background().
	// The request context (ctx) is cancelled/deadline-bound to the tool call
	// that spawned it, so deriving directly from ctx would kill the
	// background agent within microseconds of Start() returning (the
	// "worker fails immediately with a nested context-deadline error"
	// symptom) -- that half of the fix is real and load-bearing. But a bare
	// context.Background() throws out the parent's context VALUES too:
	// workspace/trace/permission-checker/owner values that hooks and
	// downstream tool checks read via ctx.Value, and the agent.IsSubAgent
	// marker that steering hooks use to skip evaluation for background
	// work. detachedSubagentContext (subagent.go) is the sibling helper used
	// by Subagent(run_in_background=true) for exactly this same problem:
	// context.WithoutCancel(ctx) strips cancellation AND any inherited
	// deadline while preserving every context value, then agent.WithSubAgent
	// marks it. Reuse it here so BackgroundTask and
	// Subagent(run_in_background=true) detach identically instead of this
	// tool silently dropping context state the other one preserves.
	bgCtx := detachedSubagentContext(ctx)
	if err := bgAgent.Start(bgCtx, enrichedTask); err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "spawn_background_agent.start_failed",
			observability.F("error", err.Error()))
		return nil, sdkerr.Permanent("spawn_background_agent.start_failed",
			fmt.Sprintf("failed to start background agent: %v", err))
	}

	// Launch progress tracker — updates result.Metadata["summary"] every 15s
	// so the TUI side panel can show live activity without polling the LLM.
	bgAgent.StartProgressTracker(15 * time.Second)

	// Register with manager
	if err := t.bgManager.Add(bgAgent, task); err != nil {
		span.RecordError(err)
		t.logger.Warn(ctx, "spawn_background_agent.registration_failed",
			observability.F("error", err.Error()))
		// Continue anyway - agent is running
	}

	t.logger.Info(ctx, "spawn_background_agent.spawned",
		observability.F("agent_id", agentID),
		observability.F("status", string(bgAgent.Status())))

	// Build result — async_launched mirrors Claude Code's canonical response format.
	// The message is baked into the tool result so the LLM knows exactly what to do next:
	// it should tell the user what was launched and then NOT duplicate the agent's work.
	outputFile := bgAgent.OutputFilePath()
	resultData := map[string]any{
		"status":      "async_launched",
		"agent_id":    agentID,
		"description": task,
		"output_file": outputFile,
		"message": fmt.Sprintf(
			"Background agent launched (agent_id: %s). "+
				"Do not duplicate this agent's work — avoid the same files or topics. "+
				"Work on non-overlapping tasks, or tell the user what you launched and end your response. "+
				"Use TaskOutput with agent_id=%q and action=\"result\" to check progress or retrieve output. "+
				"You can also Read the output_file directly for a live tail while the agent is running.",
			agentID, agentID),
	}
	if outputFile != "" {
		resultData["can_read_output"] = true
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")

	return &tools.ToolResult{
		Output: string(resultJSON),
	}, nil
}

// getProviderConfig returns provider config for background agent.
func (t *SpawnBackgroundAgentTool) getProviderConfig(model string) provider.Config {
	config := t.providerConfig

	// If provider name is empty, try to get it from currentProviderGetter
	if config.Name == "" && t.currentProviderGetter != nil {
		config.Name = t.currentProviderGetter()
	}

	if model != "" {
		config.Model = model
	}
	// Use existing model if not specified

	return config
}

// getSystemPrompt returns the system prompt for background agent.
// For OAuth providers, returns empty string to allow the provider to use only the OAuth prefix.
func (t *SpawnBackgroundAgentTool) getSystemPrompt(customPrompt string) string {
	// If custom prompt provided, use it
	if customPrompt != "" {
		return customPrompt
	}

	// For OAuth providers, return empty string so the provider uses ONLY the OAuth prefix
	// The OAuth system prompt "You are Claude Code, Anthropic's official CLI for Claude."
	// will be added by the provider's chat handler
	// Additional instructions can be passed in the task message itself
	if t.providerConfig.Custom != nil {
		if isOAuth, ok := t.providerConfig.Custom["is_oauth"].(bool); ok && isOAuth {
			return ""
		}
	}

	// For non-OAuth providers, use the standard background agent prompt
	return "You are a background agent handling long-running tasks. Work methodically and thoroughly. Provide detailed progress updates and comprehensive results."
}
