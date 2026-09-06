package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
)

func (sdk *SDKIntegration) SetDebugScreen(ds *DebugScreen) {
	sdk.debugScreen = ds
}

// SetDebugInspectProvider sets the provider for the debug_inspect tool.
// This allows the agent to introspect logs, messages, requests, tools, and app state.
// Must be called after SDK initialization when debug mode is enabled.
func (sdk *SDKIntegration) SetDebugInspectProvider(provider builtin.DebugInspectProvider) {
	// Harness construction owns the complete tool registry. If debug_inspect was
	// authorized by the plan it was bound during client construction; if it was
	// not authorized, this late TUI hook must not inject it.
	if sdk.harnessGoverned() {
		return
	}
	sdk.debugInspectProvider = provider

	// If debug mode is enabled and we have a provider, register the debug_inspect tool
	if sdk.debugMode && provider != nil && sdk.toolRegistry != nil {
		debugInspectTool, err := builtin.NewDebugInspectTool(builtin.DebugInspectConfig{
			Provider: provider,
			Logger:   sdk.logger,
			Tracer:   sdk.tracer,
		})
		if err != nil {
			sdk.logger.Warn(context.Background(), "failed to create debug_inspect tool",
				observability.F("error", err.Error()))
			return
		}

		if err := sdk.toolRegistry.Register(debugInspectTool); err != nil {
			sdk.logger.Warn(context.Background(), "failed to register debug_inspect tool",
				observability.F("error", err.Error()))
		} else {
			sdk.logger.Info(context.Background(), "debug_inspect tool registered")
			logDebug("DEBUG_INSPECT: ✓ Tool registered for agent introspection")
		}
	}
}

// rawEventCapable is implemented by any provider (or provider wrapper) that can
// receive a raw-event callback. The concrete provider implementations (e.g.
// *anthropic.Provider, *minimax.Provider) implement this directly; the TUI
// wrapper providers implement it by forwarding to their base provider so the
// callback reaches the concrete provider through any depth of wrapping.
type rawEventCapable interface {
	SetRawEventCallback(provider.RawEventCallback)
}

// SetRawEventCallback sets the callback for raw API event logging.
//
// The active provider is almost always wrapped (context injection, rate
// limiting, model pinning, reliability/fallback). Previously this asserted the
// provider directly to *anthropic.Provider, which fails through any wrapper and
// silently dropped the callback — leaving the Debug Inspector "Raw Events" tab
// permanently empty. We now assert the wrapper-forwarding rawEventCapable
// interface so the callback propagates to the concrete base provider.
func (sdk *SDKIntegration) SetRawEventCallback(callback provider.RawEventCallback) {
	sdk.rawEventCallback = callback
	if capable, ok := sdk.provider.(rawEventCapable); ok {
		capable.SetRawEventCallback(callback)
	}
}

// SetOperatingMode sets the current operating mode (PLAN, ACT, AUTO, DEBUG).
// This affects which tools are exposed to the LLM and what system instructions are injected.
func (sdk *SDKIntegration) SetOperatingMode(modeID string) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	if sdk.sdkClient != nil {
		if err := sdk.sdkClient.SetMode(sdk.ctx, modeID); err != nil {
			sdk.logger.Warn(sdk.ctx, "sdk.operating_mode.set_failed",
				observability.F("mode", modeID),
				observability.F("error", err.Error()),
			)
			return
		}
	}
	sdk.operatingMode = modeID
	sdk.logger.Info(sdk.ctx, "sdk.operating_mode.changed",
		observability.F("mode", modeID),
	)
}

// SetOperatingModeForExecution updates mode state and tool filtering while
// preserving the current prompt. ExecuteMessage injects mode guidance for each
// request, so retaining the client's eager prompt mutation would duplicate it.
func (sdk *SDKIntegration) SetOperatingModeForExecution(modeID string) {
	currentPrompt := ""
	if sdk != nil && sdk.activeAgent() != nil {
		currentPrompt = sdk.activeAgent().SystemPrompt()
	}
	sdk.SetOperatingMode(modeID)
	if currentPrompt != "" && sdk.activeAgent() != nil {
		sdk.activeAgent().SetSystemPrompt(currentPrompt)
	}
}

// GetOperatingMode returns the current operating mode ID.
// Reads from the SDK client (authoritative — SetOperatingMode routes mode
// changes through sdkClient.SetMode) and falls back to the local mirror field
// during the construction window / degraded mode.
func (sdk *SDKIntegration) GetOperatingMode() string {
	if sdk.sdkClient != nil {
		if m := sdk.sdkClient.OperatingModeID(); m != "" {
			return m
		}
	}
	if sdk.operatingMode == "" {
		return "off"
	}
	return sdk.operatingMode
}

// GetOperatingModeDefinition returns the full OperatingMode definition for the current mode.
func (sdk *SDKIntegration) GetOperatingModeDefinition() *mode.OperatingMode {
	return mode.GetBuiltinMode(sdk.GetOperatingMode())
}

// SetCustomAgentDefGetter sets the custom agent definitions getter for Task tool.
// This allows the Task tool to access custom agents from settings.
func (sdk *SDKIntegration) SetCustomAgentDefGetter(getter func() []*agent.Definition) {
	if sdk == nil || sdk.harnessGoverned() || sdk.toolRegistry == nil {
		return
	}

	// Get the Task tool from the registry
	tool, err := sdk.toolRegistry.Get("Task")
	if err != nil {
		return
	}

	// Type-assert to SubagentTool and set the getter
	if delegateTool, ok := tool.(*builtin.SubagentTool); ok {
		delegateTool.SetCustomAgentDefGetter(getter)
	}
}

// EnableThinking enables extended thinking mode with specified token budget
func (sdk *SDKIntegration) EnableThinking(budget int) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	sdk.thinkingEnabled = true
	sdk.thinkingDefaultEnabled = true
	if budget >= 1024 {
		sdk.thinkingBudget = budget
	} else {
		sdk.thinkingBudget = 2048 // Default if invalid
	}
	sdk.thinkingDefaultBudget = sdk.thinkingBudget

	sdk.logger.Info(context.Background(), "sdk.thinking.enabled",
		observability.F("budget", sdk.thinkingBudget),
		observability.F("enabled", sdk.thinkingEnabled),
	)
}

// DisableThinking disables extended thinking mode
func (sdk *SDKIntegration) DisableThinking() {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	sdk.thinkingEnabled = false
	sdk.thinkingDefaultEnabled = false

	sdk.logger.Info(context.Background(), "thinking.disabled")
}

// ToggleThinking toggles extended thinking mode
func (sdk *SDKIntegration) ToggleThinking() bool {
	if sdk == nil || sdk.harnessGoverned() {
		return sdk != nil && sdk.thinkingEnabled
	}
	sdk.thinkingDefaultEnabled = !sdk.thinkingDefaultEnabled
	sdk.thinkingEnabled = sdk.thinkingDefaultEnabled

	if sdk.thinkingEnabled {
		sdk.logger.Info(context.Background(), "sdk.thinking.toggled_on",
			observability.F("budget", sdk.thinkingBudget),
			observability.F("enabled", sdk.thinkingEnabled),
		)
	} else {
		sdk.logger.Info(context.Background(), "sdk.thinking.toggled_off",
			observability.F("enabled", sdk.thinkingEnabled),
		)
	}

	return sdk.thinkingEnabled
}

// IsThinkingEnabled returns whether extended thinking is enabled
func (sdk *SDKIntegration) IsThinkingEnabled() bool {
	return sdk.thinkingEnabled
}

// GetThinkingBudget returns the current thinking token budget
func (sdk *SDKIntegration) GetThinkingBudget() int {
	return sdk.thinkingBudget
}

// IsDiffusionModel reports whether the current model is flagged as a
// text-diffusion model (providers.json model entry has "diffusion": true).
// The flag is derived on provider/model switch; refreshDiffusionFlag covers
// the initial selection at startup.
func (sdk *SDKIntegration) IsDiffusionModel() bool {
	return sdk.diffusionModel
}

// refreshDiffusionFlag re-derives the persisted per-model runtime settings for
// the current provider/model pair. The historical name is retained to keep the
// call sites small.
func (sdk *SDKIntegration) refreshDiffusionFlag() {
	sdk.diffusionModel = false
	if sdk.generationSettings != nil {
		sdk.generationSettings.reset(sdk.currentModel)
	}
	configMgr, err := commands.NewConfigManager()
	if err != nil {
		return
	}
	providers, err := configMgr.LoadProviders()
	if err != nil {
		return
	}
	for _, prov := range providers {
		if strings.EqualFold(prov.Name, sdk.providerName) {
			for _, mdl := range prov.Models {
				if strings.EqualFold(mdl.ID, sdk.currentModel) {
					sdk.diffusionModel = mdl.Diffusion
					sdk.ApplyProviderModelConfig(mdl)
					return
				}
			}
			return
		}
	}
}

// GetToolNames returns the list of available tool names
func (sdk *SDKIntegration) GetToolNames() []string {
	if sdk.sdkClient != nil {
		if reg := sdk.sdkClient.AgentToolRegistry(); reg != nil {
			return reg.List()
		}
	}
	if sdk.toolRegistry == nil {
		return []string{}
	}
	return sdk.toolRegistry.List()
}

// GetSystemPrompt returns the current system prompt from the agent
func (sdk *SDKIntegration) SystemPrompt() string {
	if sdk.activeAgent() == nil {
		return ""
	}
	return sdk.activeAgent().SystemPrompt()
}

// SetSystemPrompt sets a custom system prompt on the agent
func (sdk *SDKIntegration) SetSystemPrompt(prompt string) {
	if sdk == nil || sdk.harnessGoverned() || sdk.activeAgent() == nil {
		return
	}
	if sdk.IsCodexBacked() {
		// For Codex-backed sessions the canonical Codex instructions are required.
		// We still honour the caller's prompt by appending it after the canonical base
		// so that custom instructions are injected without breaking the Codex contract.
		sdk.applyUserSystemPromptToAgent(context.Background(), prompt)
		return
	}
	sdk.activeAgent().SetSystemPrompt(prompt)
}

// SetReasoningEffort sets the global reasoning effort.
func (sdk *SDKIntegration) SetReasoningEffort(effort string) {
	if sdk == nil || sdk.harnessGoverned() {
		return
	}
	sdk.reasoningEffort = provider.NormalizeReasoningEffortSetting(effort)
	if sdk.logger != nil {
		sdk.logger.Info(context.Background(), "sdk.reasoning_effort.changed",
			observability.F("effort", sdk.reasoningEffort),
		)
	}
}

// GetReasoningEffort returns the current global reasoning effort setting.
func (sdk *SDKIntegration) GetReasoningEffort() string {
	if sdk == nil {
		return provider.ReasoningEffortAuto
	}
	return provider.NormalizeReasoningEffortSetting(sdk.reasoningEffort)
}

func findProviderModelConfig(providerCfg *ProviderConfig, modelID string) *ProviderModel {
	if providerCfg == nil {
		return nil
	}

	normalizedTarget := strings.ToLower(strings.TrimSpace(modelID))
	for i := range providerCfg.Models {
		if strings.EqualFold(providerCfg.Models[i].ID, modelID) {
			return &providerCfg.Models[i]
		}
	}
	for i := range providerCfg.Models {
		normalizedModelID := strings.ToLower(strings.TrimSpace(providerCfg.Models[i].ID))
		if normalizedModelID == normalizedTarget {
			return &providerCfg.Models[i]
		}
	}

	return nil
}

func (sdk *SDKIntegration) resolvedReasoningEffortsForSelection() []string {
	if sdk == nil {
		return nil
	}

	var apiType string
	var providerName string = sdk.GetProviderName()
	var supportsReasoningEffort *bool
	var reasoningEfforts []string

	providerCfg, err := getProviderConfigFromFile(sdk.GetProviderName())
	if err == nil && providerCfg != nil {
		apiType = providerCfg.APIType
		providerName = providerCfg.Name
		if modelCfg := findProviderModelConfig(providerCfg, sdk.currentModel); modelCfg != nil {
			supportsReasoningEffort = modelCfg.SupportsReasoningEffort
			reasoningEfforts = modelCfg.ReasoningEfforts
		}
	}

	return commands.ResolveReasoningEffortsForModel(
		apiType,
		providerName,
		sdk.currentModel,
		supportsReasoningEffort,
		reasoningEfforts,
	)
}

func (sdk *SDKIntegration) selectionSupportsReasoningEffort() bool {
	return len(sdk.resolvedReasoningEffortsForSelection()) > 0
}

func (sdk *SDKIntegration) shouldIncludeReasoningEffortInRequest() bool {
	if sdk == nil {
		return false
	}
	if provider.NormalizeReasoningEffortSetting(sdk.reasoningEffort) == provider.ReasoningEffortAuto {
		return false
	}
	return sdk.selectionSupportsReasoningEffort()
}

// GetBackgroundAgents returns information about running SDK background agents.
func (sdk *SDKIntegration) GetBackgroundAgents() []builtin.BackgroundAgentInfo {
	if sdk == nil || sdk.sdkBgManager == nil {
		return nil
	}
	return sdk.sdkBgManager.List()
}

// GetBackgroundAgentCount returns the number of running SDK background agents.
func (sdk *SDKIntegration) GetBackgroundAgentCount() int {
	if sdk == nil || sdk.sdkBgManager == nil {
		return 0
	}
	return sdk.sdkBgManager.Size()
}

// GetSDKBackgroundAgentManager returns the SDK background agent manager
// so callers can register lifecycle callbacks.
func (sdk *SDKIntegration) GetSDKBackgroundAgentManager() *SDKBackgroundAgentManager {
	if sdk == nil {
		return nil
	}
	return sdk.sdkBgManager
}

// GetBackgroundProcesses returns information about background bash processes.
// Returns all processes tracked by the background process manager.
// Uses admin role to bypass ABAC visibility filtering since the TUI side panel
// is the local admin view and should see all processes.
func (sdk *SDKIntegration) GetBackgroundProcesses() []bgprocess.ProcessInfo {
	if sdk == nil || sdk.bgProcessManager == nil {
		return nil
	}
	// The TUI side panel is the local admin view — it should see all processes
	// regardless of which agent/user spawned them.
	adminSubject := bgprocess.OwnerInfo{Role: "admin"}
	processes, err := sdk.bgProcessManager.List(context.Background(), adminSubject)
	if err != nil {
		return nil
	}
	return processes
}

// sidePanelBashHistoryWindow controls how long an explicitly backgrounded command
// remains visible after reaching a terminal state. Foreground command history is
// never retained in the side panel.
const sidePanelBashHistoryWindow = 5 * time.Minute

// GetSidePanelBashProcesses returns only Bash processes that represent current
// activity or useful background-command status. Every running process is shown,
// including the current foreground command. Non-running processes are shown only
// when they were explicitly backgrounded; terminal background commands age out
// after sidePanelBashHistoryWindow.
func (sdk *SDKIntegration) GetSidePanelBashProcesses() []bgprocess.ProcessInfo {
	processes := sdk.GetBackgroundProcesses()
	if sdk == nil || sdk.bgProcessManager == nil {
		return nil
	}
	return filterSidePanelBashProcesses(processes, sdk.bgProcessManager.IsBackgrounded, time.Now())
}

func filterSidePanelBashProcesses(processes []bgprocess.ProcessInfo, isBackgrounded func(string) bool, now time.Time) []bgprocess.ProcessInfo {
	visible := make([]bgprocess.ProcessInfo, 0, len(processes))
	for _, proc := range processes {
		if proc.State == bgprocess.StateRunning {
			visible = append(visible, proc)
			continue
		}
		if isBackgrounded == nil || !isBackgrounded(proc.Handle.ID()) {
			continue
		}
		if !proc.State.IsTerminal() || (proc.CompletedAt != nil && now.Sub(*proc.CompletedAt) < sidePanelBashHistoryWindow) {
			visible = append(visible, proc)
		}
	}
	return visible
}

// GetActiveBackgroundProcessCount returns the number of Bash processes eligible
// for the side panel. It includes currently running foreground commands, active
// background commands, and recently finished explicitly backgrounded commands.
// Completed foreground command history is excluded.
func (sdk *SDKIntegration) GetActiveBackgroundProcessCount() int {
	return len(sdk.GetSidePanelBashProcesses())
}

// BackgroundProcessLiveLine is one line of live background-process output.
type BackgroundProcessLiveLine struct {
	Stream  string
	Content string
}

// BackgroundProcessLiveView is a snapshot of a background bash process's current
// state and tail output, used to render an inline live "terminal" block.
type BackgroundProcessLiveView struct {
	TaskID   string
	Command  string
	State    string
	Running  bool
	PID      int
	ExitCode *int
	Elapsed  time.Duration
	Lines    []BackgroundProcessLiveLine
}

// GetBackgroundProcessOutput returns a live snapshot (state + tail output) of the
// background bash process identified by taskID. It reads directly from the
// background process manager, so the data is fresh on every call — this is what
// makes the inline terminal block update live while a command runs.
//
// Returns (nil, false) when the process is unknown (e.g. already reaped after
// completion), so callers can fall back to the frozen tool-result snapshot.
func (sdk *SDKIntegration) GetBackgroundProcessOutput(taskID string, maxLines int) (*BackgroundProcessLiveView, bool) {
	if sdk == nil || sdk.bgProcessManager == nil || taskID == "" {
		return nil, false
	}
	ctx := context.Background()

	info, err := sdk.bgProcessManager.GetInfoByID(ctx, taskID)
	if err != nil || info == nil {
		return nil, false
	}

	view := &BackgroundProcessLiveView{
		TaskID:   taskID,
		Command:  info.Command,
		State:    string(info.State),
		Running:  !info.State.IsTerminal(),
		ExitCode: info.ExitCode,
		Elapsed:  info.Duration,
	}
	// Elapsed: while running, Duration may be zero — derive from StartedAt.
	if view.Running && !info.StartedAt.IsZero() {
		view.Elapsed = time.Since(info.StartedAt)
	}

	// PID lives on the executor, not ProcessInfo.
	if exec, execErr := sdk.bgProcessManager.GetByID(ctx, taskID); execErr == nil && exec != nil {
		if impl, ok := exec.(*bgprocess.ProcessExecutorImpl); ok {
			view.PID = impl.PID()
		}
	}

	// Tail the last maxLines of output (admin subject: the TUI is the local
	// admin view and should see everything, matching GetBackgroundProcesses).
	if maxLines <= 0 {
		maxLines = 100
	}
	adminSubject := bgprocess.OwnerInfo{Role: "admin"}
	// Tail directly. The previous form fetched the ENTIRE buffer with empty
	// opts just to compute a start offset, then fetched the tail again — two
	// full copies of up to 10 MB of OutputLine structs, on a view that
	// refreshes at 10 Hz while a command is live.
	lines, err := sdk.bgProcessManager.TailOutputByID(ctx, taskID, adminSubject, maxLines)
	if err == nil {
		for _, ln := range lines {
			view.Lines = append(view.Lines, BackgroundProcessLiveLine{
				Stream:  ln.Stream,
				Content: ln.Content,
			})
		}
	}

	return view, true
}

// BackgroundProcessSignature is a cheap, comparable change signal for a
// background process. Comparing it across ticks answers "did anything actually
// change?" without copying a single output line.
type BackgroundProcessSignature struct {
	Lines int
	State string
}

// GetBackgroundProcessSignature returns the current output line count and
// state for a background process. Both reads are O(1) map/counter lookups.
func (sdk *SDKIntegration) GetBackgroundProcessSignature(taskID string) (BackgroundProcessSignature, bool) {
	if sdk == nil || sdk.bgProcessManager == nil || taskID == "" {
		return BackgroundProcessSignature{}, false
	}
	ctx := context.Background()
	adminSubject := bgprocess.OwnerInfo{Role: "admin"}

	sig := BackgroundProcessSignature{}
	if n, err := sdk.bgProcessManager.LineCountByID(ctx, taskID, adminSubject); err == nil {
		sig.Lines = n
	} else {
		return BackgroundProcessSignature{}, false
	}
	if info, err := sdk.bgProcessManager.GetInfoByID(ctx, taskID); err == nil && info != nil {
		sig.State = string(info.State)
	}
	return sig, true
}

// GetCurrentBashOutputPreview returns the last N lines of output from the currently
// running foreground bash command. Returns nil if no command is running.
// This is used by the chat renderer to show a live preview beneath the tool call.
func (sdk *SDKIntegration) GetCurrentBashOutputPreview(maxLines int) []string {
	if sdk == nil || sdk.toolRegistry == nil {
		return nil
	}
	tool, err := sdk.toolRegistry.Get("Bash")
	if err != nil {
		return nil
	}
	bgTool, ok := tool.(*bgprocess.BackgroundBashTool)
	if !ok {
		return nil
	}
	exec := bgTool.GetCurrentExecution()
	if exec == nil || !exec.Running {
		return nil
	}
	// Get the process from the manager and read tail lines
	mgr := bgTool.GetManager()
	if mgr == nil {
		return nil
	}
	procExec, err := mgr.Get(context.Background(), exec.Handle)
	if err != nil {
		return nil
	}
	lines, err := procExec.Output().Tail(maxLines)
	if err != nil || len(lines) == 0 {
		return nil
	}
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		result = append(result, line.Content)
	}
	return result
}

// GetBashProcessTail returns the last n output lines for any tracked Bash process
// (running or terminal) identified by id. It generalizes GetCurrentBashOutputPreview
// to arbitrary processes so the side panel can show a live tail beneath running
// commands. Returns nil on any error or when no output is available.
func (sdk *SDKIntegration) GetBashProcessTail(id string, n int) []string {
	if sdk == nil || sdk.bgProcessManager == nil || id == "" || n <= 0 {
		return nil
	}
	procExec, err := sdk.bgProcessManager.Get(context.Background(), bgprocess.NewProcessHandle(id))
	if err != nil || procExec == nil {
		return nil
	}
	lines, err := procExec.Output().Tail(n)
	if err != nil || len(lines) == 0 {
		return nil
	}
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		result = append(result, line.Content)
	}
	return result
}

// CancelBashProcess terminates a tracked Bash process by id. The TUI side panel
// is the local admin view, so it uses an admin subject to bypass ABAC control
// checks — the same pattern GetBackgroundProcesses uses for visibility.
func (sdk *SDKIntegration) CancelBashProcess(id string) error {
	if sdk == nil || sdk.bgProcessManager == nil {
		return fmt.Errorf("background process manager unavailable")
	}
	if id == "" {
		return fmt.Errorf("empty process id")
	}
	adminSubject := bgprocess.OwnerInfo{Role: "admin"}
	return sdk.bgProcessManager.CancelByID(context.Background(), id, adminSubject)
}

// GetCurrentForegroundProcessID returns the process ID of the currently executing
// foreground bash command, or empty string if no command is running in foreground.
// Used by the side panel to exclude foreground commands from the Background Commands section.
func (sdk *SDKIntegration) GetCurrentForegroundProcessID() string {
	if sdk == nil || sdk.toolRegistry == nil {
		return ""
	}
	tool, err := sdk.toolRegistry.Get("Bash")
	if err != nil {
		return ""
	}
	bgTool, ok := tool.(*bgprocess.BackgroundBashTool)
	if !ok {
		return ""
	}
	exec := bgTool.GetCurrentExecution()
	if exec == nil || !exec.Running {
		return ""
	}
	return exec.Handle.ID()
}

// LoadAndInjectContext loads context files and injects them into the system prompt.
// This should be called after the settings manager is available.
func (sdk *SDKIntegration) LoadAndInjectContext(loader *chatcontext.FileLoader, basePrompt string) error {
	if sdk == nil || sdk.harnessGoverned() || loader == nil {
		return nil
	}
	sdk.applyContextExclusions(loader)
	if sdk.IsCodexBacked() {
		sdk.logger.Info(sdk.ctx, "context.inject.skip_codex_prompt_integrity")
		return nil
	}

	// Load context based on config
	config := loader.GetConfig()
	loadedCtx, err := loader.LoadContext(config, sdk.WorkspaceRoot())
	if err != nil {
		sdk.logger.Warn(sdk.ctx, "context.load_failed",
			observability.F("error", err.Error()),
		)
		return nil // Non-fatal error
	}

	enhancedPrompt := basePrompt

	// Format remaining context as XML and append to system prompt
	// (AGENTS.md, git status, etc.)
	contextXML := loadedCtx.FormatAsXML()
	if contextXML != "" {
		enhancedPrompt = chatcontext.InjectContext(enhancedPrompt, contextXML)
	}

	// Advertise the static swarm-flow parallel-workflow capability as part of the
	// SYSTEM PROMPT (the stable, cached prefix) — set ONCE here, not re-appended to
	// every user turn. Interactive TUI only (headless `swarm -p` workers are
	// themselves swarm-flow workers — telling them to spawn more would recurse) and
	// only when the CLI is actually installed. Codex is handled separately: this
	// function returns early for Codex because its system prompt is backend-locked.
	if !sdk.activeAgent().IsHeadless() && swarmFlowAvailable() {
		enhancedPrompt = enhancedPrompt + "\n\n" + buildSwarmFlowGuidance()
	}

	if enhancedPrompt != basePrompt {
		sdk.activeAgent().SetSystemPrompt(enhancedPrompt)

		// Prompt provenance: record what we added on top of the base prompt.
		sdk.recordStartupPromptContribution("context_xml", len(contextXML))

		sdk.logger.Info(sdk.ctx, "context.injected",
			observability.F("source_count", len(loadedCtx.Sources)),
			observability.F("context_length", len(contextXML)),
		)
	}

	return nil
}

// GetContextLoader creates a new context loader
func (sdk *SDKIntegration) GetContextLoader() *chatcontext.FileLoader {
	loader := chatcontext.NewFileLoaderWithRoot(sdk.workspaceRoot)
	sdk.applyContextExclusions(loader)
	return loader
}

func (sdk *SDKIntegration) applyContextExclusions(loader *chatcontext.FileLoader) {
	if sdk == nil || loader == nil {
		return
	}
	if sdk.disableGlobalMemory {
		loader.ExcludeSources(chatcontext.SourceIDGlobalClaudeMd, chatcontext.SourceIDGlobalSwarmMd)
	}
	if sdk.disableProjectMemory {
		loader.ExcludeSources(
			chatcontext.SourceIDProjectClaudeMd,
			chatcontext.SourceIDProjectSwarmMd,
			chatcontext.SourceIDAgentsMd,
			chatcontext.SourceIDIndexMd,
		)
	}
	if sdk.disableSkills {
		loader.ExcludeSources(chatcontext.SourceIDSkills)
	}
}

// AgentSystemPrompt returns the agent's current system prompt, or empty string
// when no agent has been initialized. Used by headless callers that need to
// pass the current base prompt back into LoadAndInjectContext.
func (sdk *SDKIntegration) AgentSystemPrompt() string {
	if sdk.activeAgent() == nil {
		return ""
	}
	return sdk.activeAgent().SystemPrompt()
}
