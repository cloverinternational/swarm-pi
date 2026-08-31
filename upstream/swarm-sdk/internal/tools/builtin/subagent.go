// Package builtin provides delegate_task tool for sub-agent task delegation.
package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/models"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/inspection"
)

// defaultSubAgentModel is the cheap fallback model used when no model is
// explicitly configured for a sub-agent spawn.
const defaultSubAgentModel = "claude-3-5-haiku-20241022"

// SubagentTool allows the main agent to delegate specialized tasks to sub-agents.
// This enables the agent to autonomously spawn ephemeral sub-agents for focused work.

// SubagentParams are the typed parameters for the Subagent (Task) tool.
type SubagentParams struct {
	Task               string `json:"task"                              description:"The task to delegate to the sub-agent. Be specific and clear about what you want the sub-agent to do."   required:"true"`
	AgentID            string `json:"agent_id,omitempty" description:"Agent to invoke; incompatible with preset."`
	Preset             string `json:"preset,omitempty" description:"DEPRECATED: use agent_id." enum:"code_formatter,text_summarizer,data_validator,error_analyzer,question_answerer"`
	SystemPrompt       string `json:"system_prompt,omitempty"           description:"Custom system prompt that overrides agent configuration. Takes precedence over 'agent_id'."`
	Model              string `json:"model,omitempty"                   description:"Optional model override. Supports fuzzy matching (e.g. 'sonnet'). Only use when explicitly requested."`
	Resume             string `json:"resume,omitempty"                  description:"Optional agent_id of a prior Subagent run to resume."`
	RunInBackground    bool   `json:"run_in_background,omitempty"       description:"If true, launch the sub-agent as a background task (non-blocking). Cannot be used with 'auto_background_seconds'."`
	AutoBackgroundSecs int    `json:"auto_background_seconds,omitempty" description:"Start inline but convert to background after this many seconds. Cannot be used with 'run_in_background'."`
}

type SubagentTool struct {
	tools.BaseTool

	factory                agent.Factory
	providerConfig         provider.Config
	providerConfigResolver func(providerName string) (provider.Config, error)
	contextWindowResolver  func(providerName, model string) int
	parentToolReg          tools.Registry // Parent's tool registry to copy tools from
	logger                 observability.Logger
	tracer                 observability.Tracer
	customAgentDefs        map[string]*agent.Definition  // Custom agent definitions from settings
	customAgentDefGetter   func() []*agent.Definition    // Dynamic getter for custom agents
	customAgentDefsMu      sync.RWMutex                  // Protects getter + definition snapshot during parallel spawns
	roleModelSelector      RoleModelSelector             // Optional: role-based model selection for SADD
	selectorMu             sync.RWMutex                  // Protects roleModelSelector from concurrent access
	credentialStore        *agent.CredentialStore        // Multi-account rotation store; wired into every sub-agent so they rotate on 429/auth errors
	currentProviderGetter  func() string                 // Optional: callback to get current provider from engine state
	bgManager              BackgroundAgentManager        // Optional: used for run_in_background / auto-background path
	repositoryInspector    tools.SafeRepositoryInspector // Mechanically safe inspection for read-only built-ins

	// Agent constructor callbacks — wired at startup by the TUI layer.
	// agentUpsertCallback persists a created/updated custom agent definition.
	// agentDeleteCallback removes a custom agent definition by ID.
	// agentContextProvider returns the live agents JSON + profile IDs + role aliases
	// to inject as structured context into the agent_constructor subagent.
	agentUpsertCallback  func(AgentConstructorEntry) error
	agentDeleteCallback  func(string) error
	agentContextProvider func() (agentsJSON []byte, profileIDs []string, roleAliases []string)
}

// AgentConstructorEntry is the portable agent definition produced by the
// agent_constructor preset subagent and consumed by persistence callbacks.
// It intentionally mirrors the TUI's CustomAgentEntry JSON fields so that the
// TUI can unmarshal it without an SDK import cycle.
type AgentConstructorEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	SystemPrompt string   `json:"system_prompt"`
	ProfileID    string   `json:"profile_id,omitempty"`
	RoleAlias    string   `json:"role_alias,omitempty"`
	Tools        []string `json:"tools,omitempty"`
	Description  string   `json:"description,omitempty"`
}

// constructorOutput is the JSON structure the agent_constructor subagent must emit.
// It is parsed internally in processConstructorOutput and never exposed directly.
type constructorOutput struct {
	Action string                 `json:"action"` // "create" | "update" | "delete"
	Entry  *AgentConstructorEntry `json:"entry"`  // nil only for delete
	Reason string                 `json:"reason"`
	Error  string                 `json:"error,omitempty"` // set when out-of-scope
}

// constructorSystemPrompt is the curated, human-written system prompt for the
// agent_constructor preset. It encodes exactly 2 skills (schema knowledge +
// minimality discipline) per the SkillsBench optimal range.
// IMPORTANT: this is the single source of truth. Do not let agent_constructor
// self-modify or self-extend its own prompt — SkillsBench shows self-generated
// skills yield -1.3pp on average.
const constructorSystemPrompt = `Produce the smallest CustomAgentEntry change requested by the supplied context. Return only JSON: {"action":"create|update|delete","entry":{...},"reason":"one sentence"}, or {"error":"out of scope for agent_constructor","reason":"..."}.

IDs must be unique lowercase kebab-case (max 32 chars), names readable (max 48), and system_prompt under 2000 chars. Include profile_id only when requested and tools only when restriction is needed. For delete, entry contains only id; update rather than duplicate existing agents.`

// NewSubagentTool creates a new delegate_task tool.
func NewSubagentTool(config DelegateTaskConfig) (*SubagentTool, error) {
	if config.Factory == nil {
		return nil, sdkerr.Permanent("delegate_task.missing_factory", "factory is required")
	}
	if config.Logger == nil {
		return nil, sdkerr.Permanent("delegate_task.missing_logger", "logger is required")
	}
	if config.Tracer == nil {
		return nil, sdkerr.Permanent("delegate_task.missing_tracer", "tracer is required")
	}

	tool := &SubagentTool{
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
	if inspector, inspectErr := inspection.New(config.WorkspaceRoot); inspectErr == nil {
		tool.repositoryInspector = inspector
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
func (t *SubagentTool) refreshCustomAgentDefs() {
	t.customAgentDefsMu.RLock()
	getter := t.customAgentDefGetter
	t.customAgentDefsMu.RUnlock()
	if getter == nil {
		return
	}

	defs := getter()
	next := make(map[string]*agent.Definition, len(defs))
	for _, def := range defs {
		if def != nil && def.ID != "" {
			next[def.ID] = def
		}
	}
	t.customAgentDefsMu.Lock()
	t.customAgentDefs = next
	t.customAgentDefsMu.Unlock()
}

// SetCustomAgentDefGetter sets the custom agent definitions getter function.
// This allows dynamically updating the available custom agents.
func (t *SubagentTool) SetCustomAgentDefGetter(getter func() []*agent.Definition) {
	t.customAgentDefsMu.Lock()
	t.customAgentDefGetter = getter
	t.customAgentDefsMu.Unlock()
	if getter != nil {
		t.refreshCustomAgentDefs()
	}
}

// SetRoleModelSelector sets the role-based model selector function.
// This allows dynamically configuring models per SADD role.
// Thread-safe: can be called concurrently with tool execution.
func (t *SubagentTool) SetRoleModelSelector(selector RoleModelSelector) {
	t.selectorMu.Lock()
	defer t.selectorMu.Unlock()
	t.roleModelSelector = selector
}

// SetAgentWriteCallback wires the persistence callbacks used exclusively by the
// agent_constructor preset. Call this at startup alongside SetCustomAgentDefGetter.
//
//	upsert — called when action is "create" or "update". Receives the parsed entry.
//	delete — called when action is "delete". Receives the target agent ID.
//
// If callbacks are nil the constructor still runs but changes are not persisted.
// In that case processConstructorOutput logs a warning and returns the raw JSON.
func (t *SubagentTool) SetAgentWriteCallback(
	upsert func(AgentConstructorEntry) error,
	delete func(string) error,
) {
	t.agentUpsertCallback = upsert
	t.agentDeleteCallback = delete
}

// SetAgentContextProvider wires a live-read function that provides structured
// context injected into the agent_constructor subagent before it sees the user's
// intent. This ensures the constructor always knows about existing agents and
// available profiles without having to discover them itself (P2: context engineering).
//
// Returns:
//
//	agentsJSON  — current agents.json content (marshalled []CustomAgentEntry slice)
//	profileIDs  — slice of available profile ID strings
//	roleAliases — slice of valid role alias strings
func (t *SubagentTool) SetAgentContextProvider(
	fn func() (agentsJSON []byte, profileIDs []string, roleAliases []string),
) {
	t.agentContextProvider = fn
}

// getAllAvailableAgents returns a sorted list of all available agent IDs
func (t *SubagentTool) getAllAvailableAgents() []string {
	// Start with custom agents
	t.customAgentDefsMu.RLock()
	agents := make([]string, 0, len(t.customAgentDefs)+10)
	for id := range t.customAgentDefs {
		agents = append(agents, id)
	}
	t.customAgentDefsMu.RUnlock()

	// Add built-in agents
	builtinIDs := []string{
		"general-assistant",
		"code-reviewer",
		"research-agent",
		"explore",
		"background-worker",
		"agent_constructor",
		"code_formatter",
		"text_summarizer",
		"data_validator",
		"error_analyzer",
		"question_answerer",
	}
	agents = append(agents, builtinIDs...)

	// Sort for consistent ordering
	sort.Strings(agents)
	return agents
}

func (t *SubagentTool) customAgentDefinition(id string) (*agent.Definition, bool) {
	t.customAgentDefsMu.RLock()
	defer t.customAgentDefsMu.RUnlock()
	def, ok := t.customAgentDefs[id]
	return def, ok
}

// presetToRole maps a preset name to a SADD role type.
// Returns RoleImplementer as the default for unknown presets.
// Name returns the tool name.
func (t *SubagentTool) Name() string {
	return "Subagent"
}

// Description returns the tool description.
func (t *SubagentTool) Description() string {
	return `Run a specialized subagent for autonomous, multi-step work. It has separate context, so provide a self-contained task and request a concise final report.

Use direct tools for small lookups. Launch independent subagents together, without duplicating their work. Use run_in_background for long work; completion is reported automatically, and SubagentOutput retrieves the result. Synchronous output is capped at 8 MB.`
}

// IsIdempotent returns false since delegating tasks creates new sub-agents each time.
func (t *SubagentTool) IsIdempotent() bool {
	return false
}

// Validate checks if the given parameters are valid for this tool.
func (t *SubagentTool) Validate(params map[string]any) error {
	task, ok := params["task"].(string)
	if !ok || task == "" {
		return sdkerr.Permanent("delegate_task.missing_task", "task parameter is required")
	}

	// Check for conflicting parameters
	agentID, hasAgentID := params["agent_id"].(string)
	preset, hasPreset := params["preset"].(string)
	systemPrompt, hasSystemPrompt := params["system_prompt"].(string)

	// Validate conflicting agent configuration
	if hasAgentID && agentID != "" && hasPreset && preset != "" {
		return sdkerr.Permanent("delegate_task.conflicting_params",
			"Cannot specify both 'agent_id' and 'preset'. The 'preset' parameter is deprecated - use 'agent_id' instead.")
	}

	// Warn about system prompt override (not an error, but log it)
	if hasSystemPrompt && systemPrompt != "" && hasAgentID && agentID != "" {
		// This is allowed but potentially confusing - we'll log a warning in Execute
	}

	// Check for conflicting background modes
	runInBackground, _ := params["run_in_background"].(bool)
	autoBackgroundSecs := 0
	switch v := params["auto_background_seconds"].(type) {
	case float64:
		autoBackgroundSecs = int(v)
	case int:
		autoBackgroundSecs = v
	}

	if runInBackground && autoBackgroundSecs > 0 {
		return sdkerr.Permanent("delegate_task.conflicting_background",
			"Cannot specify both 'run_in_background' and 'auto_background_seconds'. Choose one background mode.")
	}

	if autoBackgroundSecs < 0 {
		return sdkerr.Permanent("delegate_task.invalid_timeout",
			"auto_background_seconds must be positive")
	}

	// agent_constructor must run inline so its output can be parsed and persisted
	// immediately. Background mode would defer the JSON post-processing step.
	if hasAgentID && agentID == "agent_constructor" && (runInBackground || autoBackgroundSecs > 0) {
		return sdkerr.Permanent("delegate_task.constructor_no_background",
			"agent_constructor cannot run in background mode: its output must be parsed and "+
				"persisted synchronously. Remove 'run_in_background' or 'auto_background_seconds'.")
	}

	return nil
}

// OptimizationHints provides guidance for efficient tool use.
func (t *SubagentTool) OptimizationHints() *tools.OptimizationHints {
	return nil // Use defaults
}

// RequiresPermission returns the required permissions for this tool.
func (t *SubagentTool) RequiresPermission() []tools.Permission {
	return nil // No special permissions required
}

// SupportedContentTypes returns the content types this tool can produce.
func (t *SubagentTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// Parameters returns the JSON schema for tool parameters.
func (t *SubagentTool) Parameters() any {
	// Get all available agent IDs
	allAgentIDs := t.getAllAvailableAgents()

	params := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "A clear, self-contained task for the sub-agent.",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"enum":        allAgentIDs,
				"description": "Agent to invoke; cannot be used with preset.",
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
				"description": "DEPRECATED: Use 'agent_id' instead. Legacy preset sub-agent types.",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "Custom system prompt that overrides agent_id configuration.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional fuzzy-matched model override; use only when explicitly requested.",
			},
			"resume": map[string]any{
				"type":        "string",
				"description": "Agent ID of a prior Subagent run to resume.",
			},
			"run_in_background": map[string]any{
				"type":        "boolean",
				"description": "Launch in the background; cannot be used with auto_background_seconds.",
			},
			"auto_background_seconds": map[string]any{
				"type":        "integer",
				"description": "Move an unfinished task to the background after this many seconds; incompatible with run_in_background.",
			},
		},
		"required": []string{"task"},
	}

	return params
}

// Execute executes the delegate_task tool.
// Execute implements tools.Tool by decoding rawParams into SubagentParams and calling Run.
func (t *SubagentTool) Execute(ctx context.Context, rawParams map[string]any) (*tools.ToolResult, error) {
	b, err := json.Marshal(rawParams)
	if err != nil {
		return nil, sdkerr.Permanent("delegate_task.param_encode", fmt.Sprintf("marshal params: %v", err))
	}
	var p SubagentParams
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, sdkerr.Permanent("delegate_task.param_decode", fmt.Sprintf("unmarshal params: %v", err))
	}
	return t.Run(ctx, p)
}

// Run executes the Subagent tool with fully typed parameters.
func (t *SubagentTool) Run(ctx context.Context, p SubagentParams) (*tools.ToolResult, error) {
	ctx, span := t.tracer.StartSpan(ctx, "delegate_task.execute")
	defer span.End()
	if p.Task == "" {
		err := sdkerr.Permanent("delegate_task.missing_task", "task parameter is required")
		span.RecordError(err)
		return nil, err
	}
	task := p.Task
	preset := p.Preset
	systemPrompt := p.SystemPrompt
	modelParam := p.Model
	agentID := p.AgentID
	resumeID := p.Resume
	runInBackground := p.RunInBackground
	autoBackgroundSecs := p.AutoBackgroundSecs
	// Log warning if system_prompt overrides agent_id
	if systemPrompt != "" && agentID != "" {
		t.logger.Warn(ctx, "delegate_task.system_prompt_overrides",
			observability.F("agent_id", agentID),
			observability.F("info", "system_prompt specified - will override agent_id configuration"))
	}

	// Apply fuzzy matching to model parameter if provided
	var model string
	if modelParam != "" {
		result := models.FuzzyModelMatchWithDetails(modelParam)
		if result.Model != "" {
			model = result.Model
			t.logger.Info(ctx, "delegate_task.model_fuzzy_matched",
				observability.F("input", modelParam),
				observability.F("matched", model),
				observability.F("confidence", result.Confidence))

			// Validate the match isn't ambiguous
			if err := models.ValidateModelMatch(modelParam, model); err != nil {
				t.logger.Warn(ctx, "delegate_task.model_match_ambiguous",
					observability.F("error", err.Error()))
			}
		} else {
			// No match found - return error with suggestions
			suggestions := "Try: " + strings.Join(result.Suggestions, ", ")
			err := sdkerr.Permanent("delegate_task.invalid_model",
				fmt.Sprintf("Model '%s' not recognized. %s", modelParam, suggestions))
			span.RecordError(err)
			return nil, err
		}
	}

	// Handle preset as agent_id for backward compatibility
	if agentID == "" && preset != "" {
		// Preset functions as a built-in agent ID
		agentID = preset
	}

	if requiresRepositoryInspection(agentID) && t.repositoryInspector == nil {
		err := sdkerr.Permanent("delegate_task.missing_required_capability",
			fmt.Sprintf("agent %q requires the safe repository inspection capability", agentID))
		span.RecordError(err)
		return nil, err
	}
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

	// Refresh custom agent definitions to ensure we have the latest.
	t.refreshCustomAgentDefs()

	// Add built-in agent definitions for presets
	builtinAgents := getBuiltinAgentDefinitions()

	// Build sub-agent config
	var subAgent *agent.Agent
	var err error

	// Determine role from preset/agentID for role-based model selection
	role := presetToRole(agentID)

	// Priority order: system_prompt > agent_id > default
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
			MaxTurns:       0, // Sub-agents: unlimited turns (user disabled limits)
			Timeout:        0, // No timeout — inherit parent context. Claude Code uses no hardcoded deadline on sub-agents.
			Temperature:    0.7,
		}
		subAgent, err = t.factory.CreateSubAgent(ctx, config)
	} else if agentID != "" {
		// 2. Agent ID specified - check custom agents, built-in agents, and presets

		// First check custom agents from settings
		if agentDef, exists := t.customAgentDefinition(agentID); exists {
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

			// Ensure the definition carries the "sub_agent" metadata type so the
			// factory's Step 8.5 configures auto-compaction for this agent.
			// Use a shallow copy to avoid mutating the stored definition.
			patchedDef := withSubAgentID(
				withSubAgentMeta(agentDef),
				uniqueSubAgentID(ctx, agentID),
			)
			_ = chain // chain is on the def already if set
			subAgent, err = t.factory.CreateFromDefinition(ctx, patchedDef, providerCfg)
		} else if builtinDef, exists := builtinAgents[agentID]; exists {
			// Built-in agent (general-assistant, code-reviewer, etc.)
			providerCfg, _ := t.getProviderConfig(builtinDef.Provider, model, role)
			// If agent definition has a specific model, use it (unless overridden by param)
			if model == "" && builtinDef.Model != "" {
				providerCfg.Model = builtinDef.Model
				providerCfg = withResolvedContextWindow(providerCfg, t.providerConfig, t.contextWindowResolver)
			}

			// Use withSubAgentMeta to inject "type": "sub_agent" metadata so
			// the factory's Step 8.5 configures auto-compaction. This also
			// creates a shallow copy so the shared builtin pointer is not
			// mutated (the factory now deep-copies too, but belt-and-suspenders
			// for any future code path that reads the def after CreateFromDefinition).
			patchedDef := withSubAgentID(
				withSubAgentMeta(builtinDef),
				uniqueSubAgentID(ctx, agentID),
			)
			subAgent, err = t.factory.CreateFromDefinition(ctx, patchedDef, providerCfg)
		} else if presetType, isPreset := isPresetAgent(agentID); isPreset {
			// Legacy preset agent (code_formatter, text_summarizer, etc.)
			providerCfg, _ := t.getProviderConfig("", model, role)
			subAgent, err = agent.CreatePresetSubAgentWithID(
				ctx, t.factory, presetType, providerCfg, uniqueSubAgentID(ctx, agentID))
		} else {
			// Build list of available agents for error message
			availableAgents := t.getAllAvailableAgents()

			err := sdkerr.Permanent("delegate_task.unknown_agent_id",
				fmt.Sprintf("Agent '%s' not found. Available agents: %s",
					agentID, strings.Join(availableAgents, ", ")))
			span.RecordError(err)
			return nil, err
		}
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
			MaxTurns:       0, // Sub-agents: unlimited turns (user disabled limits)
			Timeout:        0, // No timeout — inherit parent context. Claude Code uses no hardcoded deadline on sub-agents.
			Temperature:    0.7,
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

	// Wire the parent's intermediate callback so sub-agent activity (tool calls,
	// tool results, thinking, content, heartbeats) becomes visible in the UI as
	// a nested sub-agent box rather than running silently and only delivering a
	// final ToolResult. Each inner update is wrapped in agent.SubAgentUpdate so
	// downstream consumers can route updates per-sub-agent (by AgentID) and
	// distinguish them from the parent's own activity.
	//
	// This matches Claude Code's progress-message model where sub-agents emit a
	// live transcript that collapses to "Done (N tool uses   M tokens   Xs)" on
	// completion.
	//
	// Background agents (run_in_background / auto_background_seconds) take a
	// separate path via bgManager and never reach this branch.
	if parentCallback := agent.GetIntermediateCallback(ctx); parentCallback != nil {
		subAgentID := subAgent.ID()
		subAgentName := subAgent.Name()
		wrappedCallback := func(cbCtx context.Context, update agent.IntermediateUpdate) error {
			return parentCallback(cbCtx, agent.SubAgentUpdate{
				AgentID:   subAgentID,
				AgentName: subAgentName,
				Update:    update,
			})
		}
		subAgent.SetIntermediateCallback(wrappedCallback)
		t.logger.Debug(ctx, "delegate_task.callback_wired_for_sync_subagent",
			observability.F("agent", subAgentName),
			observability.F("agent_id", subAgentID))
	}

	// Do NOT inherit the hooks manager — sub-agents should not be steered.
	// Steering hooks add latency on every tool call and are unnecessary overhead
	// for sub-agents that should execute tools directly without evaluation.
	//
	// That reasoning still stands and is NOT reversed below. What follows adds
	// only an OBSERVATION-ONLY surface, which is a different thing:
	//
	//   - It cannot steer. agent.ObservationalHooks has no return values at all
	//     (internal/agent/observational.go), so there is no channel through
	//     which a hook can block, deny, ask, modify the call, or inject text.
	//   - It cannot add steering latency. hooks.ObservationalView.Observe does a
	//     bounded copy and one non-blocking channel send; every hook body runs
	//     on a separate goroutine after the tool call has already been decided.
	//   - Only hooks that implement hooks.ObservationalHook are reachable, so a
	//     blocking hook is not merely ignored — it is never invoked.
	//
	// Why this exists (PLAN.md G3): Subagent skipped hooks while Task/delegate
	// inherited them (delegate_task.go:558) and background agents were dark
	// entirely, so whether a run was observable at all depended on which
	// delegation tool the model happened to pick. Any ledger built on that is
	// silently missing exactly the sub-agent work it most wants to attribute.
	//
	// This also covers BOTH background paths (run_in_background and
	// auto_background_seconds): they wrap this same *agent.Agent, so attaching
	// here reaches them without touching the bgManager plumbing.
	if hooksMgr := agent.GetHooksManager(ctx); hooksMgr != nil {
		t.logger.Debug(ctx, "delegate_task.hooks_skipped_for_subagent",
			observability.F("agent", subAgent.Name()),
			observability.F("agent_id", subAgent.ID()))

		// Default OFF, explicit opt-in, checked HERE at the wiring site.
		//
		// PLAN.md §9.3 forbids "registered but disabled", which is how
		// --no-hooks came to lie for months: the hooks manager was built and
		// attached before the flag was read, so the flag suppressed only a
		// later cosmetic branch. The gate is therefore consulted before
		// anything is constructed: when it is off, SetObservationalHooks is
		// never called and subAgent.HasObservationalHooks() is false. There is
		// no disabled-but-attached state for this feature to lie about.
		gate := bench.ObservationalHooksStatus()
		if !gate.Enabled {
			t.logger.Debug(ctx, "delegate_task.observational_hooks_disabled",
				observability.F("agent_id", subAgent.ID()),
				observability.F("gate_source", string(gate.Source)),
				observability.F("env", bench.EnvObservationalHooks))
		} else if viewProvider, ok := hooksMgr.(agent.ObservationalHooksProvider); ok {
			// A manager that cannot produce a view yields NO observation. We
			// deliberately do not fall back to wrapping the full hook set and
			// discarding verdicts: that would pay every blocking hook's latency
			// and side effects inside a sub-agent, which is the thing the skip
			// comment above exists to prevent.
			// NOTE: named viewProvider, not provider — `provider` is an
			// imported package name in this file.
			if observer := viewProvider.ObservationalHooks(); observer != nil {
				subAgent.SetObservationalHooks(observer)
				t.logger.Debug(ctx, "delegate_task.observational_hooks_attached",
					observability.F("agent", subAgent.Name()),
					observability.F("agent_id", subAgent.ID()),
					observability.F("gate_source", string(gate.Source)),
					observability.F("attached", subAgent.HasObservationalHooks()))
			}
		} else {
			t.logger.Debug(ctx, "delegate_task.observational_hooks_unavailable",
				observability.F("agent_id", subAgent.ID()),
				observability.F("hint", "parent hooks manager does not implement agent.ObservationalHooksProvider; sub-agent stays dark"))
		}
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

		// Resolve abstract/cross-client aliases (for example file_read vs Read)
		// to the concrete names this parent can actually execute. The resolved
		// names become both the copy set and, for restricted agents, the exact
		// provider-visible allow-list.
		resolvedToolNames := resolveSubagentToolNames(allowedTools, toolNames)
		resolvedToolsSet := make(map[string]bool, len(resolvedToolNames))
		for _, toolName := range resolvedToolNames {
			resolvedToolsSet[toolName] = true
		}

		copiedCount := 0
		copiedToolNames := make([]string, 0, len(resolvedToolNames))
		for _, toolName := range toolNames {
			// Check if this tool is allowed for the agent
			if !resolvedToolsSet[toolName] {
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
				copiedToolNames = append(copiedToolNames, toolName)
			}
		}

		if !copyAll {
			// Static built-in definitions intentionally mention aliases for
			// multiple clients. Advertising those unresolved aliases caused a
			// tool_not_found warning on every turn and could leave specialists
			// tool-starved. Publish only tools the child can execute.
			subAgent.SetToolHints(copiedToolNames)
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

	if requiresRepositoryInspection(agentID) {
		if err := subAgent.ToolRegistry().Register(t.repositoryInspector); err != nil {
			return nil, sdkerr.Permanent("delegate_task.inspection_registration_failed",
				fmt.Sprintf("register safe repository inspection capability: %v", err))
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
	// Inject structured context for agent_constructor. This pre-assembles the
	// existing agents JSON, available profile IDs, and role aliases into a
	// structured block so the constructor knows exactly what already exists
	// and what fields are valid (P2: context engineering).
	if agentID == "agent_constructor" {
		task = t.buildConstructorContext(task)
	}
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

	// Set up context owner for task tracking (if TodoManager is available)
	ownerID := fmt.Sprintf("subagent-%s-%d", preset, time.Now().UnixNano())
	ii.SetOwnerInContext(ctx, ownerID)
	defer ii.ClearOwnerInContext(ctx, ownerID)

	// Execute the task with a detached context so the sub-agent isn't killed
	// by the parent's tool-call deadline. Claude Code sub-agents have no
	// per-call timeout — they run until natural completion. The parent's
	// executeTools wraps its context with a short deadline that would cause
	// the sub-agent's executeLoop to hit ctx.Done() immediately.
	// context.WithoutCancel preserves values (trace IDs, etc.) but strips
	// the parent's deadline, then we add a generous independent timeout.
	subCtx, subCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
	defer subCancel()

	// Mark context as sub-agent so steering hooks skip evaluation.
	subCtx = agent.WithSubAgent(subCtx)

	// Track start time outside the heartbeat block so the completion summary
	// can use the same baseline regardless of whether heartbeats fire.
	subStart := time.Now()

	// Heartbeat goroutine: emit HeartbeatUpdate every ~5s while the sub-agent
	// is in flight so the TUI can distinguish "wired but idle" (e.g. long
	// thinking block, slow provider) from "callback never fired". Without
	// this, the TUI's stall-detection threshold (30s of no events) trips on
	// every healthy sub-agent that happens to be in a long turn.
	if parentCallback := agent.GetIntermediateCallback(ctx); parentCallback != nil {
		subAgentID := subAgent.ID()
		subAgentName := subAgent.Name()
		hbCtx, hbCancel := context.WithCancel(subCtx)
		defer hbCancel()
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-hbCtx.Done():
					return
				case <-ticker.C:
					_ = parentCallback(hbCtx, agent.SubAgentUpdate{
						AgentID:   subAgentID,
						AgentName: subAgentName,
						Update: agent.HeartbeatUpdate{
							ElapsedMs: time.Since(subStart).Milliseconds(),
							Source:    "delegate_task.sync",
						},
					})
				}
			}
		}()
	}

	result, err := executor.Execute(subCtx, enrichedTask)

	// Emit a terminal SubAgentCompleteUpdate so the TUI can replace the live
	// transcript spinner with a "Done (N tool uses   M tokens   Xs)" summary.
	// We emit this for both success and failure so the UI always finalizes.
	if parentCallback := agent.GetIntermediateCallback(ctx); parentCallback != nil {
		stats := executor.Stats()
		completeUpdate := agent.SubAgentCompleteUpdate{
			TurnCount:    stats.TurnCount,
			ToolUseCount: subAgent.ToolCallsTotal(),
			TokensUsed:   stats.TotalTokens,
			Duration:     time.Since(subStart),
			FinalMessage: result,
		}
		if err != nil {
			completeUpdate.Error = err.Error()
		}
		_ = parentCallback(ctx, agent.SubAgentUpdate{
			AgentID:   subAgent.ID(),
			AgentName: subAgent.Name(),
			Update:    completeUpdate,
		})
	}
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
	// Note: This limit is documented in the tool description.
	const maxSyncResultBytes = 8 * 1024 * 1024 // 8 MB
	if len(result) > maxSyncResultBytes {
		head := result[:maxSyncResultBytes]
		// Trim to last newline so we don't cut mid-line.
		if idx := strings.LastIndex(head, "\n"); idx > 0 {
			head = head[:idx]
		}
		omittedKB := (len(result) - len(head)) / 1024
		result = fmt.Sprintf("[%dKB of output truncated — sub-agent produced more than the 8MB result limit]\n\n%s",
			omittedKB, head)
	}

	// Post-process agent_constructor output — parse the JSON contract and call
	// persistence callbacks. This is a separate verification step (P6: cross-component
	// verification: the constructor validates its own schema, and we validate again here).
	if agentID == "agent_constructor" {
		return t.processConstructorOutput(result)
	}
	// Return just the result content directly - no JSON wrapping or metadata.
	// This makes it easier for the parent agent to use the output.
	return &tools.ToolResult{
		Output: result,
	}, nil
}

// buildConstructorContext prepends a structured context block to the raw user intent,
// giving the agent_constructor precisely the information it needs to make a
// minimal, valid change — and nothing more (P2: context engineering).
func (t *SubagentTool) buildConstructorContext(userIntent string) string {
	var agentsJSON []byte
	var profileIDs, roleAliases []string

	if t.agentContextProvider != nil {
		agentsJSON, profileIDs, roleAliases = t.agentContextProvider()
	}

	// Build the structured context block
	var sb strings.Builder
	sb.WriteString("AGENT CONSTRUCTOR CONTEXT — read this before responding:\n\n")

	if len(agentsJSON) > 0 && string(agentsJSON) != "null" {
		sb.WriteString("existing_agents: ")
		sb.Write(agentsJSON)
		sb.WriteString("\n")
	} else {
		sb.WriteString("existing_agents: []\n")
	}

	if len(profileIDs) > 0 {
		sb.WriteString("available_profiles: [")
		for i, p := range profileIDs {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(`"`)
			sb.WriteString(p)
			sb.WriteString(`"`)
		}
		sb.WriteString("]\n")
	} else {
		sb.WriteString("available_profiles: [\"default\"]\n")
	}

	if len(roleAliases) > 0 {
		sb.WriteString("available_role_aliases: [")
		for i, r := range roleAliases {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(`"`)
			sb.WriteString(r)
			sb.WriteString(`"`)
		}
		sb.WriteString("]\n")
	}

	sb.WriteString("\n---\nUSER INTENT:\n")
	sb.WriteString(userIntent)
	return sb.String()
}

// processConstructorOutput parses the constructor's JSON output and calls
// the registered persistence callbacks. It is the cross-component verification
// step (P6): the constructor self-validates, and we validate again independently.
//
// Expected output contract from the constructor:
//
//	{"action":"create"|"update"|"delete","entry":{...},"reason":"..."}
//
// or a refusal:
//
//	{"error":"out of scope for agent_constructor","reason":"..."}
func (t *SubagentTool) processConstructorOutput(rawOutput string) (*tools.ToolResult, error) {
	// Extract JSON from the output — the constructor should return pure JSON,
	// but be defensive: look for the first '{' and last '}' in case there is
	// any surrounding whitespace or occasional preamble text.
	raw := strings.TrimSpace(rawOutput)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return &tools.ToolResult{
			Output: fmt.Sprintf("agent_constructor returned non-JSON output:\n%s", rawOutput),
		}, nil
	}
	jsonStr := raw[start : end+1]

	var out constructorOutput
	if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
		return &tools.ToolResult{
			Output: fmt.Sprintf("agent_constructor returned invalid JSON: %v\nRaw output:\n%s", err, rawOutput),
		}, nil
	}

	// Handle out-of-scope refusal
	if out.Error != "" {
		return &tools.ToolResult{
			Output: fmt.Sprintf("agent_constructor refused: %s", out.Error),
		}, nil
	}

	// Validate and dispatch by action
	switch out.Action {
	case "create", "update":
		if out.Entry == nil {
			return &tools.ToolResult{
				Output: "agent_constructor: 'entry' is required for action '" + out.Action + "'",
			}, nil
		}
		// Cross-component schema validation (P6)
		if out.Entry.ID == "" {
			return &tools.ToolResult{Output: "agent_constructor: entry.id is required"}, nil
		}
		if out.Entry.Name == "" {
			return &tools.ToolResult{Output: "agent_constructor: entry.name is required"}, nil
		}
		if out.Entry.SystemPrompt == "" {
			return &tools.ToolResult{Output: "agent_constructor: entry.system_prompt is required"}, nil
		}
		if len(out.Entry.SystemPrompt) > 2000 {
			return &tools.ToolResult{
				Output: fmt.Sprintf("agent_constructor: entry.system_prompt exceeds 2000 chars (%d)", len(out.Entry.SystemPrompt)),
			}, nil
		}

		// Persist via callback
		if t.agentUpsertCallback != nil {
			if err := t.agentUpsertCallback(*out.Entry); err != nil {
				return &tools.ToolResult{
					Output: fmt.Sprintf("agent_constructor: failed to persist '%s': %v", out.Entry.ID, err),
				}, nil
			}
		}
		action := "created"
		if out.Action == "update" {
			action = "updated"
		}
		reason := out.Reason
		if reason == "" {
			reason = "(no reason provided)"
		}
		return &tools.ToolResult{
			Output: fmt.Sprintf("Agent '%s' %s successfully.\nID: %s\nReason: %s",
				out.Entry.Name, action, out.Entry.ID, reason),
		}, nil

	case "delete":
		targetID := ""
		if out.Entry != nil {
			targetID = out.Entry.ID
		}
		if targetID == "" {
			return &tools.ToolResult{
				Output: "agent_constructor: entry.id is required for delete action",
			}, nil
		}
		if t.agentDeleteCallback != nil {
			if err := t.agentDeleteCallback(targetID); err != nil {
				return &tools.ToolResult{
					Output: fmt.Sprintf("agent_constructor: failed to delete '%s': %v", targetID, err),
				}, nil
			}
		}
		reason := out.Reason
		if reason == "" {
			reason = "(no reason provided)"
		}
		return &tools.ToolResult{
			Output: fmt.Sprintf("Agent '%s' deleted successfully.\nReason: %s", targetID, reason),
		}, nil

	default:
		return &tools.ToolResult{
			Output: fmt.Sprintf("agent_constructor: unknown action '%s' (expected: create, update, delete)", out.Action),
		}, nil
	}
}

// executeAsBackground wraps subAgent in a BackgroundAgent, starts it asynchronously,
// registers it with bgManager, and returns an async_launched tool result immediately.
// This is the pure-async path (run_in_background=true).
func (t *SubagentTool) executeAsBackground(
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
	if err := t.registerBackground(ctx, bgAgent, task); err != nil {
		return nil, err
	}
	return buildAsyncLaunchedResult(bgAgent, task), nil
}

// executeWithAutoBackground starts the agent inline but promotes it to a background
// agent if execution exceeds timeoutSecs seconds — mirroring Claude Code's
// Promise.race(msgPromise, backgroundSignal) pattern from d1q().
//
// If the agent completes within the timeout a synchronous-style result is returned.
// If the timeout fires first the agent keeps running and async_launched is returned.
func (t *SubagentTool) executeWithAutoBackground(
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

	// Race: agent completion vs. auto-background timer.
	// Watch THREE signals:
	//  1. bgAgent.Done() — guaranteed atomic terminal signal (never drops)
	//  2. bgAgent.Events() — fast-path early detection via event channel
	//  3. timeout.C — promote to background if taking too long
	for {
		select {
		case <-bgAgent.Done():
			// Authoritative completion signal — fires even if event buffer
			// was full and the terminal event was dropped. Drain remaining
			// events to prevent goroutine leak, then return result.
			for range bgAgent.Events() {
			}
			return t.buildSyncResultFromBackground(bgAgent)

		case event, ok := <-bgAgent.Events():
			if !ok {
				// Channel closed — agent finished, drain result.
				return t.buildSyncResultFromBackground(bgAgent)
			}
			switch event.Status {
			case agent.StatusCompleted, agent.StatusFailed, agent.StatusCancelled:
				return t.buildSyncResultFromBackground(bgAgent)
			}
			// StatusRunning / EventProgress — keep waiting.

		case <-timeout.C:
			// Timer fired before completion — promote to background.
			t.logger.Info(ctx, "delegate_task.auto_background_promoted",
				observability.F("agent_id", bgAgent.AgentID()),
				observability.F("timeout_secs", timeoutSecs))
			if err := t.registerBackground(ctx, bgAgent, task); err != nil {
				return nil, err
			}
			return buildAsyncLaunchedResult(bgAgent, task), nil
		}
	}
}

// startBackgroundAgent creates and starts a BackgroundAgent wrapping subAgent.
// Returns the running BackgroundAgent or an error.
func (t *SubagentTool) startBackgroundAgent(
	ctx context.Context,
	span observability.Span,
	subAgent *agent.Agent,
	task string,
) (*agent.BackgroundAgent, error) {
	if t.bgManager == nil {
		return nil, sdkerr.Permanent("delegate_task.background_unavailable",
			"background execution requires a configured background agent manager")
	}

	agentID := subAgent.ID()
	if agentID == "" {
		agentID = fmt.Sprintf("bg-delegate-%d", time.Now().UnixNano())
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

	// Detach cancellation/deadlines so the agent outlives the request while
	// preserving workspace, trace, permission, and runtime context values.
	bgCtx := detachedSubagentContext(ctx)
	if err := bgAgent.Start(bgCtx, task); err != nil {
		span.RecordError(err)
		return nil, sdkerr.Permanent("delegate_task.background_start_failed",
			fmt.Sprintf("failed to start background agent: %v", err))
	}

	t.logger.Info(ctx, "delegate_task.background_started",
		observability.F("agent_id", agentID))
	return bgAgent, nil
}

// registerBackground registers bgAgent before returning a retrievable ID, then
// starts progress tracking. Registration failure cancels the orphaned run.
// Safe to call exactly once per agent.
func (t *SubagentTool) registerBackground(ctx context.Context, bgAgent *agent.BackgroundAgent, task string) error {
	if regErr := t.bgManager.Add(bgAgent, task); regErr != nil {
		bgAgent.Cancel()
		t.logger.Error(ctx, "delegate_task.background_registration_failed",
			observability.F("agent_id", bgAgent.AgentID()),
			observability.F("error", regErr.Error()))
		return sdkerr.Permanent("delegate_task.background_registration_failed",
			fmt.Sprintf("background agent started but could not be registered: %v", regErr))
	}
	bgAgent.StartProgressTracker(15 * time.Second)
	t.logger.Info(ctx, "delegate_task.background_spawned",
		observability.F("agent_id", bgAgent.AgentID()))
	return nil
}

// buildAsyncLaunchedResult returns the canonical async_launched tool result.
// Callers must have already called registerBackground before this.
// buildSyncResultFromBackground extracts the final result from a completed
// BackgroundAgent and formats it as a synchronous tool result.
func (t *SubagentTool) buildSyncResultFromBackground(bgAgent *agent.BackgroundAgent) (*tools.ToolResult, error) {
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
	if len(output) > maxSyncResultBytes {
		head := output[:maxSyncResultBytes]
		if idx := strings.LastIndex(head, "\n"); idx > 0 {
			head = head[:idx]
		}
		omittedKB := (len(output) - len(head)) / 1024
		output = fmt.Sprintf("[%dKB of output truncated — sub-agent produced more than the 8MB result limit]\n\n%s",
			omittedKB, head)
	}

	// Return just the result content directly - no JSON wrapping or metadata.
	return &tools.ToolResult{Output: output}, nil
}

// getProviderConfig returns provider config for sub-agent.
// If providerName is specified and resolver is available, tries to resolve credentials for that provider.
// If a RoleModelSelector is configured and role is specified, uses role-based model selection.
// Otherwise returns the default provider config.
// Thread-safe: uses RWMutex to protect roleModelSelector access.
func (t *SubagentTool) getProviderConfig(providerName, model string, role RoleType) (provider.Config, *fallback.Chain) {

	config := t.providerConfig

	// Deep-copy the Custom map to prevent data races between concurrent sub-agent
	// spawns that mutate config.Custom (e.g. setting "reasoning_level",
	// "disable_reasoning"). provider.Config is a value type, so the struct
	// fields are copied by value — but Custom is a map, and maps are reference
	// types in Go. Without this copy, parallel Subagent calls share the same map
	// and concurrent writes are a race.
	if config.Custom != nil {
		customCopy := make(map[string]any, len(config.Custom))
		for k, v := range config.Custom {
			customCopy[k] = v
		}
		config.Custom = customCopy
	}

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
						t.logger.Warn(context.Background(), "delegate_task.role_provider_resolution_failed",
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
				t.logger.Warn(context.Background(), "delegate_task.provider_resolution_failed",
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
					t.logger.Warn(context.Background(), "delegate_task.current_provider_resolution_failed",
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
		config.Model = defaultSubAgentModel // defaultSubAgentModel: fast, cheap fallback (see const above)
	}

	return withResolvedContextWindow(config, t.providerConfig, t.contextWindowResolver), nil
}

// getSystemPrompt returns the system prompt for sub-agent.
// For OAuth providers, returns empty string to allow the provider to use only the OAuth prefix.
func (t *SubagentTool) getSystemPrompt(customPrompt string) string {
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

	// For non-OAuth providers, use the standard sub-agent prompt. The detailed
	// reporting contract lives in the task prefix ([REPORTING DIRECTIVE] in
	// buildContextPrefix) so it applies uniformly to OAuth sub-agents too (whose
	// system prompt is intentionally empty). Keep this aligned with that contract.
	return "You are a focused worker sub-agent. Execute the task directly with your tools; do not converse or narrate between tool calls. Report once at the end with a concise, factual, structured result — your final message is your only deliverable."
}

// buildContextPrefix builds a context prefix for sub-agent tasks.
// This provides essential information about the working directory and environment.
// SupportsParallel implements the ParallelCapable interface.
// Task tools can be executed in parallel since each spawns an independent sub-agent.
func (t *SubagentTool) SupportsParallel() bool {
	return true
}

// getBuiltinAgentDefinitions returns built-in agent definitions
func getBuiltinAgentDefinitions() map[string]*agent.Definition {
	return map[string]*agent.Definition{
		"general-assistant": {
			ID:           "general-assistant",
			Name:         "General Assistant",
			Description:  "General-purpose subagent for contained multi-step research and execution.",
			SystemPrompt: "You are a helpful AI assistant. You help users with a variety of tasks including answering questions, writing code, analyzing data, and solving problems.",
			ToolHints:    []string{"*"},
			Capabilities: &agent.Capabilities{
				MaxTokens:     8192,
				Temperature:   0.7,
				MaxTurns:      0, // Unlimited
				Timeout:       0, // Inherit parent context
				SupportsTools: true,
			},
			// type=sub_agent ensures factory Step 8.5 configures auto-compaction.
			Metadata: map[string]any{"type": "sub_agent"},
		},
		"code-reviewer": {
			ID:           "code-reviewer",
			Name:         "Code Reviewer",
			Description:  "Read-only code reviewer for focused bug, security, performance, and style analysis.",
			SystemPrompt: "You are an expert code reviewer. Use repository_inspect to list, search, and read only the repository text needed for the review. Analyze code for bugs, security issues, performance problems, and style violations. Provide constructive feedback with specific suggestions for improvement.",
			ToolHints:    []string{"repository_inspect"},
			Capabilities: &agent.Capabilities{
				MaxTokens:     8192,
				Temperature:   0.3,
				MaxTurns:      0, // Unlimited
				Timeout:       0, // Inherit parent context
				SupportsTools: true,
			},
			Metadata: map[string]any{"type": "sub_agent"},
		},
		"research-agent": {
			ID:           "research-agent",
			Name:         "Research Agent",
			Description:  "Subagent for open-ended codebase research requiring multiple searches and reads.",
			SystemPrompt: "You are a research agent. Your job is to explore codebases, search for information, and gather context. Be thorough in your exploration and provide comprehensive summaries of what you find.",
			ToolHints:    []string{"file_read", "grep", "list_dir", "bash"},
			Capabilities: &agent.Capabilities{
				MaxTokens:     16384,
				Temperature:   0.5,
				MaxTurns:      0, // Unlimited
				Timeout:       0, // Inherit parent context
				SupportsTools: true,
			},
			Metadata: map[string]any{"type": "sub_agent"},
		},
		"explore": {
			ID:           "explore",
			Name:         "Explore",
			Description:  "Read-only subagent for locating code and tracing flows without shell or network access.",
			SystemPrompt: "You are a read-only exploration agent. Your sole job is to navigate and understand the codebase with repository_inspect. You CANNOT modify files, run shell commands, or use the network. Work efficiently: search to locate symbols, read only the spans you need, and narrow requests when output is truncated. Report once at the end with a concise, factual summary including precise file:line pointers.",
			// This single mechanically non-mutating capability replaces prompt-
			// based trust in separately registered filesystem/search tools.
			ToolHints: []string{"repository_inspect"},
			Capabilities: &agent.Capabilities{
				MaxTokens:     16384,
				Temperature:   0.3,
				MaxTurns:      0, // Unlimited
				Timeout:       0, // Inherit parent context
				SupportsTools: true,
			},
			Metadata: map[string]any{"type": "sub_agent"},
		},
		"background-worker": {
			ID:           "background-worker",
			Name:         "Background Worker",
			Description:  "Background subagent for long-running work such as large refactors or slow builds.",
			SystemPrompt: "You are a background worker agent. Execute long-running tasks efficiently. Report progress periodically and handle errors gracefully.",
			ToolHints:    []string{"*"},
			Capabilities: &agent.Capabilities{
				MaxTokens:     8192,
				Temperature:   0.5,
				MaxTurns:      0, // Unlimited
				Timeout:       0, // Inherit parent context
				SupportsTools: true,
			},
			Metadata: map[string]any{"type": "sub_agent"},
		},
		// agent_constructor is a special preset handled with post-processing in Execute().
		// Its system prompt is the curated constructorSystemPrompt const.
		// It uses a fast/cheap model because the task is structured JSON generation, not
		// complex reasoning (P4: use the right model per step).
		"agent_constructor": {
			ID:           "agent_constructor",
			Name:         "Agent Constructor",
			Description:  "Surgical agent definition writer. Creates, edits, or deletes custom agent definitions. Emits minimal JSON diffs persisted to .swarm/agents.json.",
			SystemPrompt: constructorSystemPrompt,
			ToolHints:    []string{}, // no tools — reads context from injected block only
			Capabilities: &agent.Capabilities{
				MaxTokens:     1024, // tight budget enforces minimality
				Temperature:   0.1,  // near-deterministic for structured output
				MaxTurns:      1,    // single-turn: read context, emit JSON, done
				Timeout:       30 * time.Second,
				SupportsTools: false,
			},
			Metadata: map[string]any{"type": "sub_agent"},
		},
	}
}

// resolveSubagentToolNames maps a child definition's cross-client tool hints
// onto the concrete names present in the parent registry. It preserves parent
// registry order and never exposes recursive agent-control tools.
func resolveSubagentToolNames(requested, available []string) []string {
	if len(requested) == 0 || len(available) == 0 {
		return nil
	}

	copyAll := len(requested) == 1 && requested[0] == "*"
	requestedNames := make(map[string]struct{}, len(requested))
	requestedCapabilities := make(map[string]struct{}, len(requested))
	if !copyAll {
		for _, name := range requested {
			requestedNames[name] = struct{}{}
			if capability := subagentToolCapability(name); capability != "" {
				requestedCapabilities[capability] = struct{}{}
			}
		}
	}

	resolved := make([]string, 0, len(available))
	for _, name := range available {
		if isRecursiveSubagentTool(name) {
			continue
		}
		if copyAll {
			resolved = append(resolved, name)
			continue
		}
		if _, ok := requestedNames[name]; ok {
			resolved = append(resolved, name)
			continue
		}
		if capability := subagentToolCapability(name); capability != "" {
			if _, ok := requestedCapabilities[capability]; ok {
				resolved = append(resolved, name)
			}
		}
	}
	if len(resolved) == 0 {
		return nil
	}
	return resolved
}

func subagentToolCapability(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read", "file_read":
		return "filesystem-read"
	case "grep":
		return "text-search"
	case "list_dir":
		return "filesystem-list"
	case "bash", "shell":
		return "shell"
	default:
		return ""
	}
}

func isRecursiveSubagentTool(name string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", " ", "").
		Replace(strings.ToLower(strings.TrimSpace(name)))
	switch normalized {
	case "subagent",
		"subagentoutput",
		"delegatetask",
		"delegate",
		"delegateoutput",
		"spawnbackgroundagent",
		"checkbackgroundagent",
		"backgroundtask",
		"taskoutput",
		"waitforagent",
		"multiagentwait":
		return true
	default:
		return false
	}
}

func detachedSubagentContext(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return agent.WithSubAgent(context.WithoutCancel(parent))
}

func requiresRepositoryInspection(agentID string) bool {
	return agentID == "explore" || agentID == "code-reviewer"
}

// isPresetAgent checks if an agent ID is a legacy preset type
func isPresetAgent(agentID string) (agent.PresetSubAgentType, bool) {
	presets := map[string]agent.PresetSubAgentType{
		"code_formatter":    agent.PresetCodeFormatter,
		"text_summarizer":   agent.PresetTextSummarizer,
		"data_validator":    agent.PresetDataValidator,
		"error_analyzer":    agent.PresetErrorAnalyzer,
		"question_answerer": agent.PresetQuestionAnswerer,
	}

	if preset, ok := presets[agentID]; ok {
		return preset, true
	}
	return "", false
}

// withSubAgentMeta returns a shallow copy of def with "type": "sub_agent" injected
// into its Metadata map. This ensures the factory's Step 8.5 (auto-compaction
// configuration) fires for agents created via CreateFromDefinition — which skips
// compaction when Metadata is nil or doesn't carry the sentinel key.
//
// A shallow copy is used to avoid mutating the caller's stored definition (which
// may be reused across parallel tool invocations).
func withSubAgentMeta(def *agent.Definition) *agent.Definition {
	if def == nil {
		return def
	}
	// Already marked — no copy needed.
	if def.Metadata != nil {
		if t, ok := def.Metadata["type"].(string); ok && t == "sub_agent" {
			return def
		}
	}
	// Shallow copy + patch metadata.
	copied := *def
	newMeta := make(map[string]any, len(def.Metadata)+1)
	for k, v := range def.Metadata {
		newMeta[k] = v
	}
	newMeta["type"] = "sub_agent"
	copied.Metadata = newMeta
	return &copied
}

func withSubAgentID(def *agent.Definition, id string) *agent.Definition {
	if def == nil {
		return nil
	}
	copied := def.Clone()
	copied.ID = id
	return copied
}
