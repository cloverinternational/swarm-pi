// Package builtin provides delegate_task tool for sub-agent task delegation.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// RoleType represents the role of a sub-agent in the SADD workflow.
type RoleType string

// uniqueSubAgentID returns a globally unique ID for a sub-agent invocation.
// It prefers the tool call ID from context (which is set by the LLM during
// parallel tool execution) but falls back to a UUID when the context has
// no tool call ID. This guarantees uniqueness even when two parallel Task
// calls use the same preset or agent_id parameter.
//
// Without the UUID fallback, parallel Task calls that share a preset or
// agent_id would produce identical sub-agent IDs, causing the TUI to merge
// their streaming updates into a single display block.
func uniqueSubAgentID(ctx context.Context, prefix string) string {
	if callID := tools.ToolCallID(ctx); callID != "" {
		return fmt.Sprintf("%s-%s", prefix, callID)
	}
	return fmt.Sprintf("%s-%s", prefix, uuid.New().String()[:8])
}

const (
	// RoleSupervisor is for planning and coordination tasks (e.g., Plan preset).
	RoleSupervisor RoleType = "supervisor"
	// RoleImplementer is for code execution tasks (e.g., Explore, general-purpose presets).
	RoleImplementer RoleType = "implementer"
	// RoleSpecReviewer is for specification review tasks.
	RoleSpecReviewer RoleType = "specReviewer"
	// RoleQualityReviewer is for code quality review tasks (e.g., code-review, bug-hunter presets).
	RoleQualityReviewer RoleType = "qualityReviewer"
)

// RoleModelConfig holds model configuration for a specific role.
type RoleModelConfig struct {
	Provider         string          // Provider name (e.g., "anthropic", "openai", "cerebras")
	Model            string          // Model ID
	ReasoningLevel   string          // "low", "med", "high", "xhigh" for models that support it
	DisableReasoning bool            // For Cerebras GLM - disables reasoning
	Chain            *fallback.Chain // Optional: full model pool chain (Feature 026)
}

// RoleModelSelector is a function that returns model configuration for a given role.
// If nil is returned, the default model configuration is used.
type RoleModelSelector func(role RoleType) *RoleModelConfig

// DelegateTaskTool allows the main agent to delegate specialized tasks to sub-agents.
// This enables the agent to autonomously spawn ephemeral sub-agents for focused work.

// DelegateTaskParams are the typed parameters for the delegate_task (Task) tool.
type DelegateTaskParams struct {
	Task               string `json:"task"                              description:"The task to delegate to the sub-agent. Be specific and clear about what you want the sub-agent to do."   required:"true"`
	Preset             string `json:"preset,omitempty" description:"Preset sub-agent type." enum:"code_formatter,text_summarizer,data_validator,error_analyzer,question_answerer"`
	SystemPrompt       string `json:"system_prompt,omitempty"           description:"Optional custom system prompt to configure the sub-agent. Overrides preset and agent_id if specified."`
	Model              string `json:"model,omitempty"                   description:"Optional model to use for the sub-agent. If omitted, uses a cheap/fast model suitable for sub-agents."`
	AgentID            string `json:"agent_id,omitempty"                description:"Optional custom agent ID to use for this task. Overrides preset if both are specified."`
	Resume             string `json:"resume,omitempty"                  description:"Optional agent_id of a prior BackgroundTask run to resume."`
	RunInBackground    bool   `json:"run_in_background,omitempty"       description:"If true, launch the sub-agent as a background task (non-blocking). Returns immediately with an agent_id."`
	AutoBackgroundSecs int    `json:"auto_background_seconds,omitempty" description:"Start inline but automatically promote to background after this many seconds if not yet complete."`
}

type DelegateTaskTool struct {
	tools.BaseTool

	factory                agent.Factory
	providerConfig         provider.Config
	providerConfigResolver func(providerName string) (provider.Config, error)
	contextWindowResolver  func(providerName, model string) int
	parentToolReg          tools.Registry // Parent's tool registry to copy tools from
	logger                 observability.Logger
	tracer                 observability.Tracer
	customAgentDefs        map[string]*agent.Definition // Custom agent definitions from settings
	customAgentDefGetter   func() []*agent.Definition   // Dynamic getter for custom agents
	roleModelSelector      RoleModelSelector            // Optional: role-based model selection for SADD
	selectorMu             sync.RWMutex                 // Protects roleModelSelector from concurrent access
	credentialStore        *agent.CredentialStore       // Multi-account rotation store; wired into every sub-agent so they rotate on 429/auth errors
	currentProviderGetter  func() string                // Optional: callback to get current provider from engine state
	bgManager              BackgroundAgentManager       // Optional: used for run_in_background / auto-background path
}

// DelegateTaskConfig configures the delegate_task tool.
type DelegateTaskConfig struct {
	Factory                agent.Factory
	ProviderConfig         provider.Config                                    // Default provider config for sub-agents
	ProviderConfigResolver func(providerName string) (provider.Config, error) // Optional: Function to resolve provider configs
	ContextWindowResolver  func(providerName, model string) int               // Optional: resolves final model context after overrides
	ParentToolReg          tools.Registry                                     // Parent's tool registry to copy tools from
	Logger                 observability.Logger
	Tracer                 observability.Tracer
	CustomAgentDefGetter   func() []*agent.Definition // Optional: Dynamic getter for custom agent definitions
	RoleModelSelector      RoleModelSelector          // Optional: role-based model selection for SADD workflows
	CurrentProviderGetter  func() string              // Optional: callback to get current provider from engine state
	BGManager              BackgroundAgentManager     // Optional: required for run_in_background / auto-background support
	WorkspaceRoot          string                     // Workspace root for sandboxing sub-agent file operations
}

// NewDelegateTaskTool creates a new delegate_task tool.
func NewDelegateTaskTool(config DelegateTaskConfig) (*DelegateTaskTool, error) {
	if config.Factory == nil {
		return nil, sdkerr.Permanent("delegate_task.missing_factory", "factory is required")
	}
	if config.Logger == nil {
		return nil, sdkerr.Permanent("delegate_task.missing_logger", "logger is required")
	}
	if config.Tracer == nil {
		return nil, sdkerr.Permanent("delegate_task.missing_tracer", "tracer is required")
	}

	tool := &DelegateTaskTool{
		factory:                config.Factory,
		providerConfig:         config.ProviderConfig,
		providerConfigResolver: config.ProviderConfigResolver,
		contextWindowResolver:  config.ContextWindowResolver,
		parentToolReg:          config.ParentToolReg,
		logger:                 config.Logger,
		tracer:                 config.Tracer,
		customAgentDefs:        make(map[string]*agent.Definition),
		customAgentDefGetter:   config.CustomAgentDefGetter,
		roleModelSelector:      config.RoleModelSelector,
		currentProviderGetter:  config.CurrentProviderGetter,
		bgManager:              config.BGManager,
	}

	// Load initial custom agent definitions if getter provided
	if tool.customAgentDefGetter != nil {
		tool.refreshCustomAgentDefs()
	}

	// Pre-load multi-account credential store so all sub-agents can rotate on 429/auth errors.
	// This mirrors what sdk_integration.go does for the main agent.
	if credStore, csErr := agent.NewCredentialStoreFromTuiAccounts(nil); csErr == nil && credStore.CredentialCount() > 1 {
		tool.credentialStore = credStore
	}

	return tool, nil
}

// refreshCustomAgentDefs updates the internal map of custom agent definitions.
func (t *DelegateTaskTool) refreshCustomAgentDefs() {
	if t.customAgentDefGetter == nil {
		return
	}

	defs := t.customAgentDefGetter()
	t.customAgentDefs = make(map[string]*agent.Definition)
	for _, def := range defs {
		if def != nil && def.ID != "" {
			t.customAgentDefs[def.ID] = def
		}
	}
}

// SetCustomAgentDefGetter sets the custom agent definitions getter function.
// This allows dynamically updating the available custom agents.
func (t *DelegateTaskTool) SetCustomAgentDefGetter(getter func() []*agent.Definition) {
	t.customAgentDefGetter = getter
	if getter != nil {
		t.refreshCustomAgentDefs()
	}
}

// SetRoleModelSelector sets the role-based model selector function.
// This allows dynamically configuring models per SADD role.
// Thread-safe: can be called concurrently with tool execution.
func (t *DelegateTaskTool) SetRoleModelSelector(selector RoleModelSelector) {
	t.selectorMu.Lock()
	defer t.selectorMu.Unlock()
	t.roleModelSelector = selector
}

// presetToRole maps a preset name to a SADD role type.
// Returns RoleImplementer as the default for unknown presets.
func presetToRole(preset string) RoleType {
	switch strings.ToLower(preset) {
	case "plan":
		return RoleSupervisor
	case "explore", "general-purpose", "code_formatter", "text_summarizer", "data_validator", "error_analyzer", "question_answerer":
		return RoleImplementer
	case "code-review", "bug-hunter":
		return RoleQualityReviewer
	case "spec-review":
		return RoleSpecReviewer
	default:
		return RoleImplementer
	}
}

// Name returns the tool name.
func (t *DelegateTaskTool) Name() string {
	return "Task"
}

// Description returns the tool description.
func (t *DelegateTaskTool) Description() string {
	return "Delegate a focused task to a sub-agent. Invoke independent tasks together to run them in parallel."
}

// IsIdempotent returns false since delegating tasks creates new sub-agents each time.
func (t *DelegateTaskTool) IsIdempotent() bool {
	return false
}

// Validate checks if the given parameters are valid for this tool.
func (t *DelegateTaskTool) Validate(params map[string]any) error {
	task, ok := params["task"].(string)
	if !ok || task == "" {
		return sdkerr.Permanent("delegate_task.missing_task", "task parameter is required")
	}
	return nil
}

// OptimizationHints provides guidance for efficient tool use.
func (t *DelegateTaskTool) OptimizationHints() *tools.OptimizationHints {
	return nil // Use defaults
}

// RequiresPermission returns the required permissions for this tool.
func (t *DelegateTaskTool) RequiresPermission() []tools.Permission {
	return nil // No special permissions required
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *DelegateTaskTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Parameters returns the JSON schema for tool parameters.
func (t *DelegateTaskTool) Parameters() any {
	// Build list of available custom agent IDs
	agentIDs := make([]string, 0, len(t.customAgentDefs))
	for id := range t.customAgentDefs {
		agentIDs = append(agentIDs, id)
	}

	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "A clear, self-contained task for the sub-agent.",
			},
			"preset": map[string]any{
				"type": "string",
				"enum": []string{
					"code_formatter",
					"text_summarizer",
					"data_validator",
					"error_analyzer",
					"question_answerer",
				},
				"description": "Specialized preset; defaults to a general-purpose sub-agent.",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "Custom system prompt; overrides preset and agent_id.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Model override for the sub-agent.",
			},
			"resume": map[string]any{
				"type":        "string",
				"description": "Agent ID of a prior BackgroundTask run to resume.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Launch in the background and retrieve the result with TaskOutput.",
			},
			"auto_background_seconds": map[string]any{
				"type":        "integer",
				"description": "Promote an unfinished inline task to the background after this many seconds.",
			},
		},
		"required": []string{"task"},
	}

	// Add agent_id parameter if custom agents are available
	if len(agentIDs) > 0 {
		params["properties"].(map[string]any)["agent_id"] = map[string]any{
			"type":        "string",
			"enum":        agentIDs,
			"description": "Custom agent ID; overrides preset.",
		}
	}

	return params
}

// Execute executes the delegate_task tool.
// Execute implements tools.Tool by decoding rawParams into DelegateTaskParams and calling Run.
func (t *DelegateTaskTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, sdkerr.Permanent("delegate_task.param_encode", fmt.Sprintf("marshal params: %v", err))
	}
	var p DelegateTaskParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, sdkerr.Permanent("delegate_task.param_decode", fmt.Sprintf("unmarshal params: %v", err))
	}
	return t.Run(ctx, p)
}

// Run executes the delegate_task tool with fully typed parameters.
func (t *DelegateTaskTool) Run(ctx context.Context, p DelegateTaskParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "delegate_task.execute")
	defer span.End()
	// Validate required parameter
	if p.Task == "" {
		err := sdkerr.Permanent("delegate_task.missing_task", "task parameter is required")
		span.RecordError(err)
		return nil, err
	}
	task := p.Task
	preset := p.Preset
	systemPrompt := p.SystemPrompt
	model := p.Model
	agentID := p.AgentID
	resumeID := p.Resume
	runInBackground := p.RunInBackground
	autoBackgroundSecs := p.AutoBackgroundSecs
	span.SetAttribute("task_length", len(task))
	span.SetAttribute("preset", preset)
	span.SetAttribute("agent_id", agentID)
	span.SetAttribute("has_custom_prompt", systemPrompt != "")
	span.SetAttribute("has_custom_model", model != "")
	span.SetAttribute("resume", resumeID)

	// If resuming a prior run, load and prepend its output as context.
	if resumeID != "" {
		priorCtx, err := agent.BuildResumeContext(resumeID)
		if err != nil {
			t.logger.Warn(ctx, "delegate_task.resume_failed",
				observability.F("resume_id", resumeID),
				observability.F("error", err.Error()))
			// Non-fatal: continue without prior context
		} else if priorCtx != "" {
			task = priorCtx + task
			t.logger.Info(ctx, "delegate_task.resume_loaded",
				observability.F("resume_id", resumeID),
				observability.F("prior_context_len", len(priorCtx)))
		}
	}

	t.logger.Info(ctx, "delegate_task.starting",
		observability.F("task", task),
		observability.F("preset", preset),
		observability.F("agent_id", agentID))

	// Refresh custom agent definitions to ensure we have the latest
	if t.customAgentDefGetter != nil {
		t.refreshCustomAgentDefs()
	}

	// Build sub-agent config
	var subAgent *agent.Agent
	var err error

	// Determine role from preset for role-based model selection
	role := presetToRole(preset)

	// Priority order: system_prompt > agent_id > preset > default
	if systemPrompt != "" {
		// 1. Custom system prompt (highest priority - explicit override)
		providerCfg, chain := t.getProviderConfig("", model, role)
		config := agent.SubAgentConfig{
			AgentID:        uniqueSubAgentID(ctx, "subagent"),
			AgentName:      "Task Delegator",
			Description:    "Ephemeral sub-agent for task delegation",
			ProviderConfig: providerCfg,
			Chain:          chain,
			Tools:          []string{"*"}, // Sub-agents inherit parent tools
			SystemPrompt:   systemPrompt,
			MaxTurns:       0,           // Sub-agents: unlimited turns (user disabled limits)
			Timeout:        time.Minute, // 1 minute timeout
			// Temperature intentionally left 0 (= provider default / omit). Hardcoding
			// 0.7 made every default sub-agent send a temperature param, which newer
			// models (e.g. claude-opus-4-8) REJECT with a 400 "temperature is deprecated
			// for this model" — silently killing the sub-agent. The parent agent works
			// precisely because it never pins temperature. 0 lets the request omit it.
		}
		subAgent, err = t.factory.CreateSubAgent(ctx, config)
	} else if agentID != "" {
		// 2. Custom agent from settings (second priority)
		agentDef, exists := t.customAgentDefs[agentID]
		if !exists {
			err := sdkerr.Permanent("delegate_task.unknown_agent_id",
				fmt.Sprintf("custom agent with ID '%s' not found", agentID))
			span.RecordError(err)
			return nil, err
		}

		// Use CreateFromDefinition with the custom agent definition
		// Resolve provider config for the agent's specific provider
		providerCfg, chain := t.getProviderConfig(agentDef.Provider, model, role)
		// If agent definition has a specific model, use it (unless overridden by param)
		if model == "" && agentDef.Model != "" {
			providerCfg.Model = agentDef.Model
			providerCfg = withResolvedContextWindow(providerCfg, t.providerConfig, t.contextWindowResolver)
		}

		// If definition has a chain and we don't have a role override, use it
		if chain == nil && agentDef.Chain != nil && model == "" {
			chain = agentDef.Chain
		}

		subAgent, err = t.factory.CreateFromDefinition(ctx, agentDef, providerCfg)
		// Note: CreateFromDefinition doesn't currently take a chain parameter,
		// but since we updated the Definition struct, the factory implementation
		// of CreateFromDefinition needs to be checked.

		// Override the definition-based ID with a unique, call-scoped ID.
		// Without this, parallel Task calls using the same agent_id produce
		// sub-agents with identical IDs, causing the TUI to merge their
		// streaming updates into a single box.
		if err == nil && subAgent != nil {
			subAgent.SetID(uniqueSubAgentID(ctx, agentID))
		}
	} else if preset != "" {
		// 3. Use preset sub-agent (third priority)
		presetType := agent.PresetSubAgentType(preset)
		providerCfg, _ := t.getProviderConfig("", model, role)

		subAgent, err = agent.CreatePresetSubAgent(ctx, t.factory, presetType, providerCfg)

		// Override the static preset ID with a unique, call-scoped ID.
		// getPresetConfig sets AgentID = string(presetType) (e.g. "code_formatter"),
		// which collides when parallel Task calls use the same preset.
		if err == nil && subAgent != nil {
			subAgent.SetID(uniqueSubAgentID(ctx, preset))
		}
		// Note: CreatePresetSubAgent only takes provider.Config.
		// For true pool support, we may need to update the Agent struct to hold the chain
		// and use it during execution.
	} else {
		// 4. Default: Create custom sub-agent with standard prompt
		providerCfg, chain := t.getProviderConfig("", model, role)
		config := agent.SubAgentConfig{
			AgentID:        uniqueSubAgentID(ctx, "subagent"),
			AgentName:      "Task Delegator",
			Description:    "Ephemeral sub-agent for task delegation",
			ProviderConfig: providerCfg,
			Chain:          chain,
			Tools:          []string{"*"}, // Sub-agents inherit parent tools
			SystemPrompt:   t.getSystemPrompt(""),
			MaxTurns:       0,           // Sub-agents: unlimited turns (user disabled limits)
			Timeout:        time.Minute, // 1 minute timeout
			// Temperature intentionally left 0 (= provider default / omit) — see the
			// default-path note above. Newer models 400 on any temperature param.
		}
		subAgent, err = t.factory.CreateSubAgent(ctx, config)
	}

	if err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "delegate_task.creation_failed",
			observability.F("error", err.Error()))
		return nil, sdkerr.Permanent("delegate_task.creation_failed",
			fmt.Sprintf("failed to create sub-agent: %v", err))
	}

	// Inherit capabilities from parent context if available
	// This enables streaming updates from the sub-agent to flow to the UI.
	//
	// We track whether the wiring succeeded — if it didn't, the parent will
	// see a frozen "Using SubAgent" spinner with no updates for the entire
	// run. That used to fail silently at Debug/Warn level; we now surface it
	// loudly and prepend a marker to the final result so the user is told
	// streaming was unavailable instead of staring at a hung-looking spinner.
	var (
		wrappedCallback   agent.IntermediateCallback
		callbackInherited bool
	)
	if callback := agent.GetIntermediateCallback(ctx); callback != nil {
		// Wrap the callback to tag updates as coming from this sub-agent
		// Special handling for ExhaustedUpdate: we need to intercept the Response
		// channel so the parent TUI can send a FallbackDecision back to this sub-agent.
		wrappedCallback = func(ctx context.Context, update agent.IntermediateUpdate) error {
			t.logger.Debug(ctx, "delegate_task.emitting_subagent_update",
				observability.F("agent", subAgent.Name()),
				observability.F("agent_id", subAgent.ID()),
				observability.F("type", update.UpdateType()))

			// Special handling: if this is an ExhaustedUpdate, we need to create
			// a bridge channel that allows the parent to send FallbackDecision back
			// to this sub-agent's response channel.
			if exhausted, ok := update.(agent.ExhaustedUpdate); ok && exhausted.Response != nil {
				// Save the original response channel that the sub-agent is waiting on
				originalResponseCh := exhausted.Response

				// Create a bridge channel that the parent will write to
				bridgeCh := make(chan agent.FallbackDecision, 1)

				// Replace the sub-agent's response channel with our bridge
				// The parent TUI will send decisions here
				exhausted.Response = bridgeCh

				go func() {
					outerTimer := time.NewTimer(30 * time.Second)
					defer outerTimer.Stop()
					select {
					case decision := <-bridgeCh:
						innerTimer := time.NewTimer(5 * time.Second)
						defer innerTimer.Stop()
						select {
						case originalResponseCh <- decision:
							t.logger.Debug(ctx, "delegate_task.exhausted_decision_forwarded",
								observability.F("agent", subAgent.Name()),
								observability.F("cancel", decision.Cancel),
								observability.F("has_new_chain", decision.NewChain != nil))
						case <-innerTimer.C:
							t.logger.Warn(ctx, "delegate_task.exhausted_decision_timeout",
								observability.F("agent", subAgent.Name()),
								observability.F("hint", "parent did not send decision in time, sub-agent will cancel"))
						}
					case <-ctx.Done():
					case <-outerTimer.C:
						t.logger.Warn(ctx, "delegate_task.exhausted_response_timeout",
							observability.F("agent", subAgent.Name()),
							observability.F("timeout_seconds", 30),
							observability.F("hint", "no FallbackDecision received from parent, sub-agent blocking on exhaustion"))
					}
				}()
			}

			return callback(ctx, agent.SubAgentUpdate{
				AgentID:   subAgent.ID(),
				AgentName: subAgent.Name(),
				Update:    update,
			})
		}
		subAgent.SetIntermediateCallback(wrappedCallback)
		callbackInherited = true
		t.logger.Debug(ctx, "delegate_task.callback_inherited_and_wrapped")
	} else {
		t.logger.Error(ctx, "delegate_task.NO_CALLBACK_IN_CONTEXT",
			observability.F("agent", subAgent.Name()),
			observability.F("agent_id", subAgent.ID()),
			observability.F("hint", "parent agent has no intermediate callback in context — sub-agent will run silently with no progress visible to the UI; check that SetIntermediateCallback is wired on the parent before Execute"))
	}

	// Inherit hooks manager for tool execution events
	if hooksMgr := agent.GetHooksManager(ctx); hooksMgr != nil {
		subAgent.SetHooksManager(hooksMgr)
		t.logger.Debug(ctx, "delegate_task.hooks_inherited")
	}

	// Copy tools from parent registry to sub-agent's registry
	// Respect the agent's tool configuration - only copy specified tools
	// IMPORTANT: Use ListAll/GetIncludingDisabled to access ALL tools from the parent,
	// including ones the user disabled on the parent. Sub-agents have their own tool
	// configurations and should NOT inherit the parent's disabled-tool restrictions.
	if t.parentToolReg != nil {
		subToolReg := subAgent.ToolRegistry()

		// Get the agent's definition to check which tools it should have
		agentDef := subAgent.Definition()
		allowedTools := agentDef.ToolHints

		// If agent specifies "*", copy all tools (except self-referential ones)
		copyAll := len(allowedTools) == 1 && allowedTools[0] == "*"

		// Build set of allowed tools for quick lookup
		allowedToolsSet := make(map[string]bool)
		if !copyAll {
			for _, toolName := range allowedTools {
				allowedToolsSet[toolName] = true
			}
		}

		// Try to access ALL tools (including disabled) via SimpleRegistry
		// This prevents parent's disabled tools from being hidden from sub-agents
		// that are configured to use them
		parentSimpleReg, isSimpleReg := t.parentToolReg.(*tools.SimpleRegistry)

		var toolNames []string
		if isSimpleReg {
			toolNames = parentSimpleReg.ListAll()
		} else {
			toolNames = t.parentToolReg.List()
		}

		copiedCount := 0
		for _, toolName := range toolNames {
			// Skip self-referential tools to prevent infinite recursion
			if toolName == "Task" || toolName == "BackgroundTask" || toolName == "TaskOutput" {
				continue
			}

			// Check if this tool is allowed for the agent
			if !copyAll && !allowedToolsSet[toolName] {
				t.logger.Debug(ctx, "delegate_task.tool_skipped_not_allowed",
					observability.F("tool", toolName),
					observability.F("agent", subAgent.ID()))
				continue
			}

			// Get tool - use GetIncludingDisabled if available to bypass parent restrictions
			var tool tools.Tool
			if isSimpleReg {
				var getErr error
				tool, _, getErr = parentSimpleReg.IncludingDisabled(toolName)
				if getErr != nil {
					t.logger.Warn(ctx, "delegate_task.tool_copy_failed",
						observability.F("tool", toolName),
						observability.F("error", getErr.Error()))
					continue
				}
			} else {
				var getErr error
				tool, getErr = t.parentToolReg.Get(toolName)
				if getErr != nil {
					t.logger.Warn(ctx, "delegate_task.tool_copy_failed",
						observability.F("tool", toolName),
						observability.F("error", getErr.Error()))
					continue
				}
			}

			if err := subToolReg.Register(tool); err != nil {
				t.logger.Warn(ctx, "delegate_task.tool_register_failed",
					observability.F("tool", toolName),
					observability.F("error", err.Error()))
			} else {
				t.logger.Debug(ctx, "delegate_task.tool_copied",
					observability.F("tool", toolName))
				copiedCount++
			}
		}
		t.logger.Info(ctx, "delegate_task.tools_copied",
			observability.F("tools_copied", copiedCount),
			observability.F("tools_available", subToolReg.List()))

		// Copy permission checker from parent registry to sub-agent registry
		// This ensures sub-agents inherit the same permission policies as the parent
		if isSimpleReg {
			if subSimpleReg, ok := subToolReg.(*tools.SimpleRegistry); ok {
				if parentChecker := parentSimpleReg.PermissionChecker(); parentChecker != nil {
					subSimpleReg.SetPermissionChecker(parentChecker)
					t.logger.Debug(ctx, "delegate_task.permission_checker_inherited",
						observability.F("agent", subAgent.ID()))
				}
			}
		}
	}

	// Wire multi-account credential rotation into every sub-agent.
	// Without this, sub-agents always fail hard on rate-limit/auth errors instead of
	// rotating to the next stored account (same as the main agent does at creation time).
	if t.credentialStore != nil {
		subAgent.SetCredentialStore(t.credentialStore)
	}

	// Build context-enriched task
	// Determine agent type for context hints
	agentType := preset
	if agentType == "" && agentID != "" {
		agentType = agentID
	}
	contextPrefix := buildContextPrefix(agentType)
	enrichedTask := contextPrefix + task + "\n[/TASK]"

	// Branch: run as background agent or inline (synchronous)
	if runInBackground {
		return t.executeAsBackground(ctx, span, subAgent, enrichedTask, preset)
	}
	if autoBackgroundSecs > 0 {
		return t.executeWithAutoBackground(ctx, span, subAgent, enrichedTask, preset, autoBackgroundSecs)
	}

	// Wrap in executor
	executor, err := agent.NewSubAgentExecutor(subAgent)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	// Heartbeat: drive a HeartbeatUpdate into the wrapped callback every
	// heartbeatInterval while the sub-agent is executing. The Execute call
	// below blocks for the entire run; without a periodic liveness signal,
	// any silent stretch (long thinking blocks, credential rotation, slow
	// providers) is indistinguishable from a hung subagent. The TUI uses the
	// heartbeat to refresh "still working" status and to suppress its
	// "no-progress callback — broken" warning state.
	//
	// Heartbeats are only emitted when the callback was successfully
	// inherited; without one, there is nowhere to send them. The ticker is
	// stopped via the done channel below, regardless of whether Execute
	// returned normally, panicked, or had its context cancelled.
	const heartbeatInterval = 5 * time.Second
	heartbeatDone := make(chan struct{})
	if callbackInherited && wrappedCallback != nil {
		startTime := time.Now()
		go func() {
			ticker := time.NewTicker(heartbeatInterval)
			defer ticker.Stop()
			for {
				select {
				case <-heartbeatDone:
					return
				case <-ctx.Done():
					return
				case now := <-ticker.C:
					_ = wrappedCallback(ctx, agent.HeartbeatUpdate{
						ElapsedMs: now.Sub(startTime).Milliseconds(),
						Source:    "delegate_task.sync",
					})
				}
			}
		}()
	}

	// Execute the task with context
	result, err := executor.Execute(ctx, enrichedTask)
	close(heartbeatDone)
	if err != nil {
		span.RecordError(err)
		t.logger.Error(ctx, "delegate_task.execution_failed",
			observability.F("error", err.Error()))
		return &tools.ToolResult{
			Output: fmt.Sprintf("Sub-agent execution failed: %v", err),
		}, nil // Return as tool result, not error
	}

	// Get stats
	stats := executor.Stats()

	span.SetAttribute("tokens_used", stats.TotalTokens)
	span.SetAttribute("cost_usd", stats.TotalCost)
	span.SetAttribute("turns", stats.TurnCount)

	t.logger.Info(ctx, "delegate_task.completed",
		observability.F("tokens_used", stats.TotalTokens),
		observability.F("cost_usd", stats.TotalCost),
		observability.F("turns", stats.TurnCount))

	// Cap result size to prevent context explosion in the parent agent.
	// A chatty sub-agent can produce megabytes of output; jamming it all
	// into a single tool_result block silently blows up the parent's context.
	const maxSyncResultBytes = 8 * 1024 * 1024 // 8 MB
	truncated := false
	if len(result) > maxSyncResultBytes {
		truncated = true
		head := result[:maxSyncResultBytes]
		// Trim to last newline so we don't cut mid-line.
		if idx := strings.LastIndex(head, "\n"); idx > 0 {
			head = head[:idx]
		}
		omittedKB := (len(result) - len(head)) / 1024
		result = fmt.Sprintf("[%dKB of output truncated — sub-agent produced more than the 8MB result limit]\n\n%s",
			omittedKB, head)
	}

	// If the sub-agent had no progress callback in context, the parent UI
	// stayed silent for the entire run. Surface that to the parent agent
	// (and ultimately the user) so it isn't mistaken for a normal silent
	// completion.
	if !callbackInherited {
		result = "[warning: sub-agent ran without a progress callback in context — no streaming updates were visible to the UI; this is a wiring bug, not a sub-agent failure]\n\n" + result
	}

	// Build structured result
	resultData := map[string]any{
		"result":             result,
		"tokens_used":        stats.TotalTokens,
		"cost_usd":           stats.TotalCost,
		"turns":              stats.TurnCount,
		"preset":             preset,
		"truncated":          truncated,
		"callback_inherited": callbackInherited,
	}

	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")

	return &tools.ToolResult{
		Output: string(resultJSON),
	}, nil
}

// executeAsBackground wraps subAgent in a BackgroundAgent, starts it asynchronously,
// registers it with bgManager, and returns an async_launched tool result immediately.
// This is the pure-async path (run_in_background=true).
func (t *DelegateTaskTool) executeAsBackground(
	ctx context.Context,
	span observability.Span,
	subAgent *agent.Agent,
	task string,
	preset string,
) (*tools.ToolResult, error) {
	bgAgent, err := t.startBackgroundAgent(ctx, span, subAgent, task)
	if err != nil {
		return nil, err
	}
	_ = preset // available for future use (e.g., tagging the background entry)
	t.registerBackground(ctx, bgAgent, task)
	return buildAsyncLaunchedResult(bgAgent, task), nil
}

// executeWithAutoBackground starts the agent inline but promotes it to a background
// agent if execution exceeds timeoutSecs seconds — mirroring Claude Code's
// Promise.race(msgPromise, backgroundSignal) pattern from d1q().
//
// If the agent completes within the timeout a synchronous-style result is returned.
// If the timeout fires first the agent keeps running and async_launched is returned.
func (t *DelegateTaskTool) executeWithAutoBackground(
	ctx context.Context,
	span observability.Span,
	subAgent *agent.Agent,
	task string,
	preset string,
	timeoutSecs int,
) (*tools.ToolResult, error) {
	bgAgent, err := t.startBackgroundAgent(ctx, span, subAgent, task)
	if err != nil {
		return nil, err
	}

	_ = preset
	timeout := time.NewTimer(time.Duration(timeoutSecs) * time.Second)
	defer timeout.Stop()

	// Race the guaranteed terminal signal, not the bounded observability event
	// stream. Another consumer may drain Events and progress events may drop.
	select {
	case <-bgAgent.Done():
		return t.buildSyncResultFromBackground(bgAgent)
	case <-timeout.C:
		// Timer fired before completion — promote to background.
		t.logger.Info(ctx, "delegate_task.auto_background_promoted",
			observability.F("agent_id", bgAgent.AgentID()),
			observability.F("timeout_secs", timeoutSecs))
		t.registerBackground(ctx, bgAgent, task)
		return buildAsyncLaunchedResult(bgAgent, task), nil
	}
}

// startBackgroundAgent creates and starts a BackgroundAgent wrapping subAgent.
// Returns the running BackgroundAgent or an error.
func (t *DelegateTaskTool) startBackgroundAgent(
	ctx context.Context,
	span observability.Span,
	subAgent *agent.Agent,
	task string,
) (*agent.BackgroundAgent, error) {
	agentID := subAgent.ID()
	if agentID == "" {
		agentID = fmt.Sprintf("bg-delegate-%s", uuid.New().String()[:8])
	}

	bgAgent, err := agent.NewBackgroundAgent(agent.BackgroundAgentConfig{
		Agent:           subAgent,
		ParentID:        tools.OwnerAgentID(ctx),
		EventBufferSize: 100,
		Logger:          t.logger,
		Tracer:          t.tracer,
	})
	if err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("delegate_task.background_create_failed",
			fmt.Sprintf("failed to create background agent: %v", err))
	}

	// IMPORTANT: use context.Background() so the agent outlives the request context.
	bgCtx := context.Background()
	if err := bgAgent.Start(bgCtx, task); err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("delegate_task.background_start_failed",
			fmt.Sprintf("failed to start background agent: %v", err))
	}

	t.logger.Info(ctx, "delegate_task.background_started",
		observability.F("agent_id", agentID))
	return bgAgent, nil
}

// registerBackground starts the progress tracker and registers bgAgent with bgManager.
// Safe to call exactly once per agent.
func (t *DelegateTaskTool) registerBackground(ctx context.Context, bgAgent *agent.BackgroundAgent, task string) {
	bgAgent.StartProgressTracker(15 * time.Second)
	if t.bgManager != nil {
		if regErr := t.bgManager.Add(bgAgent, task); regErr != nil {
			t.logger.Warn(ctx, "delegate_task.background_registration_failed",
				observability.F("agent_id", bgAgent.AgentID()),
				observability.F("error", regErr.Error()))
		}
	}
	t.logger.Info(ctx, "delegate_task.background_spawned",
		observability.F("agent_id", bgAgent.AgentID()))
}

// buildAsyncLaunchedResult returns the canonical async_launched tool result.
// Callers must have already called registerBackground before this.
func buildAsyncLaunchedResult(bgAgent *agent.BackgroundAgent, task string) *tools.ToolResult {
	agentID := bgAgent.AgentID()
	outputFile := bgAgent.OutputFilePath()
	resultData := map[string]any{
		"status":      "async_launched",
		"agent_id":    agentID,
		"description": task,
		"output_file": outputFile,
		"message": fmt.Sprintf(
			"Background agent launched (agent_id: %s). "+
				"Do not duplicate this agent's work. "+
				"Use TaskOutput with agent_id=%q to retrieve results.",
			agentID, agentID),
	}
	if outputFile != "" {
		resultData["can_read_output"] = true
	}
	bgResultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(bgResultJSON)}
}

// buildSyncResultFromBackground extracts the final result from a completed
// BackgroundAgent and formats it as a synchronous tool result.
func (t *DelegateTaskTool) buildSyncResultFromBackground(bgAgent *agent.BackgroundAgent) (*tools.ToolResult, error) {
	result := bgAgent.Result()
	if result == nil {
		return &tools.ToolResult{Output: "Sub-agent finished with no result."}, nil
	}

	if result.Status == agent.StatusFailed {
		errMsg := "unknown error"
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		return &tools.ToolResult{
			Output: fmt.Sprintf("Sub-agent execution failed: %s", errMsg),
		}, nil
	}

	output := result.Result
	// Cap at 8 MB to prevent context explosion.
	const maxSyncResultBytes = 8 * 1024 * 1024
	truncated := false
	if len(output) > maxSyncResultBytes {
		truncated = true
		head := output[:maxSyncResultBytes]
		if idx := strings.LastIndex(head, "\n"); idx > 0 {
			head = head[:idx]
		}
		omittedKB := (len(output) - len(head)) / 1024
		output = fmt.Sprintf("[%dKB of output truncated — sub-agent produced more than the 8MB result limit]\n\n%s",
			omittedKB, head)
	}

	resultData := map[string]any{
		"result":      output,
		"tokens_used": result.TokensUsed,
		"cost_usd":    result.CostUSD,
		"turns":       result.TurnCount,
		"duration":    result.Duration.String(),
		"truncated":   truncated,
	}
	resultJSON, _ := json.MarshalIndent(resultData, "", "  ")
	return &tools.ToolResult{Output: string(resultJSON)}, nil
}

// getProviderConfig returns provider config for sub-agent.
// If providerName is specified and resolver is available, tries to resolve credentials for that provider.
// If a RoleModelSelector is configured and role is specified, uses role-based model selection.
// Otherwise returns the default provider config.
// Thread-safe: uses RWMutex to protect roleModelSelector access.
// sanitizeProviderName strips fallback-chain / orchestrator-endpoint decoration
// from a provider identity so it is a BARE provider name the factory can match.
//
// The current-provider getter and some resolvers can hand back a decorated
// endpoint label like "anthropic/claude-opus-4-8[0]" (provider/model[index]).
// When that lands in provider.Config.Name, the factory's Step-2
// ProvidersMatch(def.Provider="claudecode", config.Name="anthropic/claude-opus-4-8[0]")
// fails with "definition requires provider 'claudecode' but got
// 'anthropic/claude-opus-4-8[0]'", killing every builtin/custom sub-agent spawn.
// We keep only the segment before the first '/' (and before any '[').
func sanitizeProviderName(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return n
	}
	if i := strings.IndexAny(n, "/["); i >= 0 {
		n = n[:i]
	}
	return strings.TrimSpace(n)
}

func withResolvedContextWindow(config, base provider.Config, resolver func(providerName, model string) int) provider.Config {
	if resolver == nil {
		if config.Name != base.Name || config.Model != base.Model {
			config.ContextWindow = 0
		}
		return config
	}
	// Provider/model selection may have replaced a config that carried the
	// previous model's window. Clear it before resolving the final identity.
	config.ContextWindow = 0
	if window := resolver(config.Name, config.Model); window > 0 {
		config.ContextWindow = window
	}
	return config
}

func (t *DelegateTaskTool) getProviderConfig(providerName, model string, role RoleType) (provider.Config, *fallback.Chain) {

	config := t.providerConfig
	// Get selector under read lock to avoid race with SetRoleModelSelector
	t.selectorMu.RLock()
	selector := t.roleModelSelector
	t.selectorMu.RUnlock()

	// Check if we have role-based model selection
	if role != "" && selector != nil {
		roleConfig := selector(role)
		if roleConfig != nil {
			// Use role-based provider/model
			if roleConfig.Provider != "" && t.providerConfigResolver != nil {
				if resolvedConfig, err := t.providerConfigResolver(roleConfig.Provider); err == nil {
					config = resolvedConfig
				} else {
					if t.logger != nil {
						t.logger.Warn(nil, "delegate_task.role_provider_resolution_failed",
							observability.F("role", string(role)),
							observability.F("provider", roleConfig.Provider),
							observability.F("error", err.Error()))
					}
					config.Name = roleConfig.Provider
				}
			} else if roleConfig.Provider != "" {
				config.Name = roleConfig.Provider
			}

			// Set model from role config
			if roleConfig.Model != "" {
				config.Model = roleConfig.Model
			}

			// Set reasoning level in Custom map
			if roleConfig.ReasoningLevel != "" {
				if config.Custom == nil {
					config.Custom = make(map[string]any)
				}
				config.Custom["reasoning_level"] = roleConfig.ReasoningLevel
			}

			// Set disable_reasoning for Cerebras GLM
			if roleConfig.DisableReasoning {
				if config.Custom == nil {
					config.Custom = make(map[string]any)
				}
				config.Custom["disable_reasoning"] = true
			}

			config.Name = sanitizeProviderName(config.Name)
			return withResolvedContextWindow(config, t.providerConfig, t.contextWindowResolver), roleConfig.Chain
		}
	}

	// If a different provider is specified and we have a resolver, use it
	if providerName != "" && providerName != t.providerConfig.Name && t.providerConfigResolver != nil {
		if resolvedConfig, err := t.providerConfigResolver(providerName); err == nil {
			config = resolvedConfig
		} else {
			// Log warning but continue with default config
			if t.logger != nil {
				t.logger.Warn(nil, "delegate_task.provider_resolution_failed",
					observability.F("provider", providerName),
					observability.F("error", err.Error()))
			}
			// Fall back to changing just the name
			config.Name = providerName
		}
	} else if providerName == "" && t.currentProviderGetter != nil {
		// If no explicit provider specified, resolve the current provider's credentials.
		// Always go through providerConfigResolver when available — the stored providerConfig
		// may have been created without an API key (e.g. ipc-server creates it with Name+Model
		// only), so we must resolve fresh credentials regardless of whether the provider name
		// matches the stored config.
		currentProvider := strings.TrimSpace(t.currentProviderGetter())
		if currentProvider != "" && t.providerConfigResolver != nil {
			if resolvedConfig, err := t.providerConfigResolver(currentProvider); err == nil {
				config = resolvedConfig
			} else {
				if t.logger != nil {
					t.logger.Warn(nil, "delegate_task.current_provider_resolution_failed",
						observability.F("provider", currentProvider),
						observability.F("error", err.Error()))
				}
				// Resolver failed — at minimum carry the name so the provider
				// factory knows which provider to use.
				config.Name = currentProvider
			}
		} else if currentProvider != "" {
			// No resolver available; carry the name forward.
			config.Name = currentProvider
		}
	}

	// Override model if specified, otherwise use cheap default
	if model != "" {
		config.Model = model
	} else if config.Model == "" {
		// Default to cheap model for sub-agents
		config.Model = "claude-haiku-4-5-20251001" // Fast and cheap
	}

	config.Name = sanitizeProviderName(config.Name)
	return withResolvedContextWindow(config, t.providerConfig, t.contextWindowResolver), nil
}

// getSystemPrompt returns the system prompt for sub-agent.
// For OAuth providers, returns empty string to allow the provider to use only the OAuth prefix.
func (t *DelegateTaskTool) getSystemPrompt(customPrompt string) string {
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

	// For non-OAuth providers, use the standard sub-agent prompt
	return "You are a focused sub-agent. Complete the requested task efficiently and accurately. Provide direct, concise responses."
}

// buildContextPrefix builds a context prefix for sub-agent tasks.
// This provides essential information about the working directory and environment.
func buildContextPrefix(agentType string) string {
	var sb strings.Builder

	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	// Get project name from directory
	projectName := filepath.Base(cwd)

	// Check for common project indicators
	var projectType string
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		projectType = "Go"
	} else if _, err := os.Stat(filepath.Join(cwd, "package.json")); err == nil {
		projectType = "Node.js/JavaScript"
	} else if _, err := os.Stat(filepath.Join(cwd, "Cargo.toml")); err == nil {
		projectType = "Rust"
	} else if _, err := os.Stat(filepath.Join(cwd, "pyproject.toml")); err == nil {
		projectType = "Python"
	} else if _, err := os.Stat(filepath.Join(cwd, "requirements.txt")); err == nil {
		projectType = "Python"
	}

	sb.WriteString("[CONTEXT]\n")
	sb.WriteString(fmt.Sprintf("Working Directory: %s\n", cwd))
	sb.WriteString(fmt.Sprintf("Project: %s\n", projectName))
	if projectType != "" {
		sb.WriteString(fmt.Sprintf("Project Type: %s\n", projectType))
	}

	// Add agent-type specific hints
	switch agentType {
	case "explore":
		sb.WriteString("Agent Role: Explorer - focus on reading, searching, and understanding code\n")
	case "code_formatter":
		sb.WriteString("Agent Role: Code Formatter - focus on formatting and style fixes\n")
	case "text_summarizer":
		sb.WriteString("Agent Role: Summarizer - provide concise summaries\n")
	case "error_analyzer":
		sb.WriteString("Agent Role: Error Analyzer - diagnose and explain errors\n")
	case "research-agent":
		sb.WriteString("Agent Role: Researcher - gather and synthesize information\n")
	case "git-commit-writer":
		sb.WriteString("Agent Role: Git Commit Writer - analyze changes and write commit messages\n")
	case "code-reviewer":
		sb.WriteString("Agent Role: Code Reviewer - review code for issues and improvements\n")
	}

	sb.WriteString("[/CONTEXT]\n\n")

	// Tool-call hygiene reminder — kept short so it does not crowd the task.
	// This was added after a real production incident (see
	// docs/specs/subagent-testing-report-2026-05-16.md) where a research-agent
	// emitted 8 parallel `grep` calls per turn, the upstream proxy assigned
	// duplicate IDs to some of them, and the resulting "tool result missing"
	// error sent the agent into an unrecoverable retry loop. The dispatcher
	// is now collision-proof (parallel.go), but we still nudge sub-agents to
	// keep batches modest so the human-visible spinner stays useful and so
	// the model's own planning has room to react to each tool's output.
	sb.WriteString("[TOOL CALL HYGIENE]\n")
	sb.WriteString("- Batch at most 5 parallel tool calls per turn. If a batch returns errors, switch to sequential calls for the rest of this turn.\n")
	sb.WriteString("- If you see the SAME error twice in a row, change your approach instead of retrying the same pattern.\n")
	sb.WriteString("- Prefer one well-targeted call over many speculative ones; you are billed per tool result and the parent reads everything.\n")
	sb.WriteString("[/TOOL CALL HYGIENE]\n\n")

	// Reporting directive — modeled on Claude Code's forked-worker contract
	// (buildChildMessage). A sub-agent's ONLY deliverable is its final message;
	// the parent never sees intermediate tool output. Without this directive,
	// sub-agents ramble, narrate between tool calls, and return verbose dumps that
	// bloat the parent's context. Keep it tight and structured.
	sb.WriteString("[REPORTING DIRECTIVE]\n")
	sb.WriteString("You are a focused worker. Execute the task directly with your tools — do NOT converse, ask questions, or suggest next steps.\n")
	sb.WriteString("- Do NOT emit text between tool calls. Use tools silently, then report ONCE at the end.\n")
	sb.WriteString("- Your final message is your only deliverable — the parent sees it and nothing else (no intermediate output, no tool logs).\n")
	sb.WriteString("- Stay strictly within the task's scope. If you notice related work outside scope, mention it in one sentence at most.\n")
	sb.WriteString("- Be factual and concise. Keep the report under 500 words unless the task explicitly asks for more. No preamble, no meta-commentary.\n")
	sb.WriteString("- Structure the report with plain-text labels: Result (the answer or key findings), Key files (relevant paths with line numbers for research), Files changed (only if you modified files), Issues (only if any).\n")
	sb.WriteString("[/REPORTING DIRECTIVE]\n\n")

	sb.WriteString("[TASK]\n")

	return sb.String()
}

// SupportsParallel implements the ParallelCapable interface.
// Task tools can be executed in parallel since each spawns an independent sub-agent.
func (t *DelegateTaskTool) SupportsParallel() bool {
	return true
}
