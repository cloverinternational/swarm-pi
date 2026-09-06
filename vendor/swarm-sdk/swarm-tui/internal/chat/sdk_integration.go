// Package chat provides SDK integration helpers
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	hooksbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/lifecycle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/pkg/computeruse/x11"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/plan"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles/bridge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/advanced"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
	historytools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/history"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/projectmemory"
	skilltools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/skilltools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/swarm"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
	xaitools "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/xai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	sdkversion "github.com/Swarm-Code/mono/swarm-sdk/internal/version"
	tuiversion "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/version"

	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/visual"
)

// GetProxySettings returns the proxy settings for debugging (used in headless mode)
func GetProxySettings() *settings.ProxySettings {
	return settings.NewProxySettings()
}

// SDKIntegration wraps SDK components for the chat app.
//
// Migration: this type is being incrementally refactored so that all core
// execution, conversation, and configuration logic delegates to the embedded
// *sdkclient.Client.  TUI-specific state (MCPManager UI, permission checker
// UI, token update channel, A2A callbacks, etc.) remains here.
// Track in PLAN.md Task #12.
type providerSlot struct {
	mu            sync.RWMutex
	provider      provider.Provider
	providerName  string
	model         string
	contextWindow int
}

type providerRuntimeSnapshot struct {
	Provider      provider.Provider
	ProviderName  string
	Model         string
	ContextWindow int
}

func (s *providerSlot) Get() provider.Provider {
	return s.Snapshot().Provider
}

func (s *providerSlot) Snapshot() providerRuntimeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return providerRuntimeSnapshot{
		Provider:      s.provider,
		ProviderName:  s.providerName,
		Model:         s.model,
		ContextWindow: s.contextWindow,
	}
}

func (s *providerSlot) Set(p provider.Provider) {
	s.mu.Lock()
	s.provider = p
	s.mu.Unlock()
}

func (s *providerSlot) SetRuntime(p provider.Provider, providerName, model string, contextWindow int) {
	s.mu.Lock()
	s.provider = p
	s.providerName = providerName
	s.model = model
	s.contextWindow = contextWindow
	s.mu.Unlock()
}

type SDKIntegration struct {
	// sdkClient is the unified SDK surface that owns provider, agent, manager,
	// storage, and tool registry.  All core logic migrates here; SDKIntegration
	// becomes a thin TUI adapter.  May be nil if client.New fails at startup
	// (degraded mode — legacy fields remain as fallback).
	sdkClient      *sdkclient.Client
	sdkClientUnsub func() // unsubscribe func returned by sdkClient.SubscribeUpdates; called on close/reconfigure

	provider              provider.Provider
	providerName          string
	logger                observability.Logger
	tracer                observability.Tracer
	lookupStore           observability.LookupStore
	traceSink             observability.Sink
	ctx                   context.Context
	debugScreen           *DebugScreen              // For capturing requests
	rawEventCallback      provider.RawEventCallback // For capturing raw API events before transformation
	lastReqStart          time.Time
	toolRegistry          tools.Registry
	codeModeToggle        func(bool) error // nil until InstallInPlace succeeds; call to flip codemode on/off in-place
	agent                 *agent.Agent
	currentModel          string // Track current model
	authToken             string // Raw auth token/api key currently in use
	mcpManager            *MCPManager
	hooksManager          *HooksManager                       // Hook system manager
	lifecycleHookExecutor *lifecycle.Executor                 // Lifecycle hook executor for custom hooks
	skillsManager         *SkillsManager                      // Skills system manager for dynamic skill loading
	pluginsManager        *PluginsManager                     // Plugins system manager for Claude Code-compatible plugins
	sdkBgManager          *SDKBackgroundAgentManager          // SDK background agent manager for tools
	bgProcessManager      *bgprocess.BackgroundProcessManager // Background bash process manager
	agentFactory          agent.Factory                       // Agent factory for sub-agent and background agent tools
	providerRegistry      *provider.SimpleRegistry            // Shared provider registry used by agentFactory and executeWithChain
	isOAuth               bool                                // Whether this integration is using OAuth
	workspaceRoot         string                              // Execution root for tools and sandboxing
	projectRoot           string                              // Stable project root for conversation identity
	permissionChecker     *tools.InteractivePermissionChecker // Tool permission checker
	permissionConfig      *PermissionConfig                   // Persisted permission policies
	approvalBroker        tools.ApprovalBroker                // Interactive harness/TUI approval bridge
	questionBroker        *QuestionBroker                     // Retained host broker for selected interactive capabilities
	planBroker            *PlanBroker                         // Retained host broker for selected plan capabilities
	maxTokens             int                                 // Max output tokens for requests
	rawDebug              bool                                // Raw JSON debug mode

	// compactFuncWirer, when installed by the TUI App (configureA2ABridge),
	// wires the agent's in-loop CompactFunc to the App's SHARED compaction
	// service so threshold-triggered auto-compaction and manual /compact run
	// the same pipeline. When nil (headless/degraded mode) the standalone
	// wireAgentCompactFunc fallback is used instead.
	compactFuncWirer func(*agent.AutoCompactionConfig, providerRuntimeSnapshot)

	// promptTraceStartupEntries holds prompt-construction contributions that
	// happened before any per-request context existed (skills injection,
	// dream contract injection, headless custom prompt override, etc.).
	// The agent's promptTraceSeedFn replays them into the per-request
	// collector so they appear in the [PROMPT PROVENANCE] banner.
	promptTraceMu             sync.Mutex
	promptTraceStartupEntries []promptTraceStartupEntry
	providerBuilder           providerBuilderFunc         // Injectable provider builder (used by reload + tests)
	reliabilityConfigLoader   reliabilityConfigLoaderFunc // Injectable reliability config loader
	endpointOverride          *ProviderEndpointOverride
	registryProviderSlot      *providerSlot

	// Agent profile system
	profileManager *settings.ProfileManager // Profile manager for alias resolution
	activeProfile  *settings.AgentProfile   // Currently active profile

	// Hooks tool executor - set by app to execute hooks tools
	hooksToolExecutor func(ctx context.Context, name string, params map[string]interface{}) string

	// Extended thinking configuration. The default fields preserve the user's
	// global preference while the effective fields may carry per-model overrides.
	thinkingEnabled        bool
	thinkingBudget         int
	thinkingEffort         string
	thinkingDefaultEnabled bool
	thinkingDefaultBudget  int
	thinkingDefaultEffort  string
	generationSettings     *modelGenerationSettings

	// diffusionModel is true when the current model is flagged as a
	// text-diffusion model in providers.json (Diffusion: true). The chat view
	// renders such replies with a denoising reveal animation.
	diffusionModel bool

	// cronPromptSink delivers prompts fired by the cron scheduler (/loop,
	// /goal, ScheduleWakeup) into the main chat session instead of spawning
	// invisible background agents. The app attaches its delivery callback via
	// SetCronPromptHandler.
	cronPromptSink *tuiCronPromptSink
	// cronScheduler backs cronPromptSink and lets goal continuation account for
	// other scheduled work. Nil when no scheduler was created.
	cronScheduler *builtin.CronScheduler

	// Reasoning effort configuration for GPT/Codex-style models.
	// Stored as a normalized setting value: auto|none|minimal|low|medium|high|xhigh.
	reasoningEffort string

	// Prompt caching configuration (Context7-compliant)
	cachingEnabled     bool           // Enable prompt caching
	cacheTTL           string         // Cache TTL: "5m" or "1h"
	lastCacheMetrics   map[string]int // Last cache metrics from response
	cacheHitRate       float64        // Cache hit rate percentage
	totalCacheCreation int            // Total tokens cached
	totalCacheRead     int            // Total tokens read from cache

	// Cache invalidation tracking
	cacheManager *CacheManager // Smart cache invalidation manager

	// Real-time token updates
	tokenUpdateCh       chan TokenUpdate  // Channel for publishing token updates
	tokenUpdateCallback func(TokenUpdate) // Callback for token updates

	// Operating mode (PLAN/ACT/AUTO) for tool filtering and instruction injection
	operatingMode string // Current mode ID (default: "act")

	capabilityManifestMu            sync.Mutex
	lastCapabilityManifestConvID    string
	lastCapabilityManifestSignature string

	// Debug mode - enables DebugLogs tool
	debugMode bool

	// Advanced Tool Use Framework
	deferredRegistry    *advanced.DeferredRegistry // Deferred tool registry (wraps toolRegistry)
	toolSearchTool      *advanced.ToolSearchTool   // Meta-tool for discovering deferred tools
	advancedToolMode    bool                       // Whether advanced tool mode is active
	deferTokenThreshold int                        // Token threshold for auto-deferring

	// Debug inspect provider for agent-based introspection
	debugInspectProvider builtin.DebugInspectProvider

	// Provider event callback
	providerEventCallback func(providerEventMsg)

	// userSystemPromptGetter returns the user's currently active system prompt from settings.
	// It is set by the app after SDK initialisation and used by ReloadProvider so that
	// provider rebuilds (OAuth reload, model switch, etc.) preserve the user selection.
	userSystemPromptGetter func() string

	// Real-time context injection
	contextOrchestrator  *chatcontext.ContextOrchestrator
	contextCapture       *providerContextCaptureStore
	disableGlobalMemory  bool
	disableProjectMemory bool
	disableSkills        bool

	// SteeringAgent for findings analysis (nil when not configured)
	steeringAgent *agent.SteeringAgent

	// Vault provider for credential management tools
	vaultProvider vault.VaultProvider
	vaultUnlocker builtin.VaultUnlocker

	a2aEnabled           bool
	a2aHandle            string
	a2aListenAddress     string
	a2aMu                sync.RWMutex
	activeConversationID string
	localUpdateChan      chan<- agent.IntermediateUpdate
	a2aEventCallback     func(A2AEvent)
	a2aEventQueue        chan A2AEvent
	a2aEventDispatcher   sync.Once
	a2aConversationFn    func(context.Context, string, string) (A2AConversationResolution, error)

	// Spawned peer processes (headless A2A agents)
	spawnedPeers   map[string]*exec.Cmd // handle -> process mapping
	spawnedPeersMu sync.RWMutex

	// hub is the workspace-scoped WebSocket hub (always-on when set).
	// nil means local A2A uses SQLite backend instead.
	hub *a2a.WorkspaceHub
}

// GetLogger returns the SDK logger (implements hooks.SDKProvider)
func (sdk *SDKIntegration) Logger() observability.Logger {
	if sdk.sdkClient != nil {
		if l := sdk.sdkClient.Logger(); l != nil {
			return l
		}
	}
	return sdk.logger
}

// GetTracer returns the SDK tracer (implements hooks.SDKProvider)
func (sdk *SDKIntegration) Tracer() observability.Tracer {
	if sdk.sdkClient != nil {
		if t := sdk.sdkClient.Tracer(); t != nil {
			return t
		}
	}
	return sdk.tracer
}

func (sdk *SDKIntegration) LookupErrorLineage(errorID string) (*observability.LineageReport, error) {
	if sdk == nil || sdk.lookupStore == nil {
		return nil, fmt.Errorf("diagnostics lookup store unavailable")
	}
	return observability.LookupByErrorID(sdk.lookupStore, errorID)
}

// GetPermissionChecker returns the tool permission checker (implements hooks.SDKProvider)
func (sdk *SDKIntegration) PermissionChecker() *tools.InteractivePermissionChecker {
	return sdk.permissionChecker
}

// SetProviderEventCallback sets the callback for provider events (retries, switching)
func (sdk *SDKIntegration) SetProviderEventCallback(callback func(providerEventMsg)) {
	sdk.providerEventCallback = callback
}

// SetUserSystemPromptGetter wires in the callback that returns the user's currently
// active system prompt from the settings manager.  ReloadProvider calls this after
// rebuilding the provider so that model/OAuth reloads always honour the user selection.
func (sdk *SDKIntegration) SetUserSystemPromptGetter(fn func() string) {
	sdk.userSystemPromptGetter = fn
}

// SDKClient returns the underlying *sdkclient.Client backing this integration.
// Callers (e.g. serve.NewMux, IPC/ACP handlers) that need the full SDK API
// surface should use this instead of going through SDKIntegration.
// Returns nil when client.New failed at startup (degraded mode).
func (sdk *SDKIntegration) SDKClient() *sdkclient.Client {
	return sdk.sdkClient
}

// SDKClient exposes the App's underlying *sdkclient.Client so external callers
// (e.g. the swarmos entrypoint mounting a serve.Mux for the rich native attach
// path) can serve this TUI session's live client over /rpc + /sse. Returns nil
// when the SDK is not initialised or client.New failed at startup (degraded
// mode), so callers must nil-check before use.
func (a *App) SDKClient() *sdkclient.Client {
	if a == nil || a.sdk == nil {
		return nil
	}
	return a.sdk.SDKClient()
}

// GetProvider returns the provider instance (for headless mode)
func (sdk *SDKIntegration) GetProvider() provider.Provider {
	if sdk == nil {
		return nil
	}
	if sdk.registryProviderSlot != nil {
		return sdk.registryProviderSlot.Get()
	}
	return sdk.provider
}

func (sdk *SDKIntegration) runtimeSnapshot() providerRuntimeSnapshot {
	if sdk == nil {
		return providerRuntimeSnapshot{}
	}
	if sdk.registryProviderSlot != nil {
		return sdk.registryProviderSlot.Snapshot()
	}
	p := sdk.provider
	contextWindow := 0
	if activeAgent := sdk.activeAgent(); activeAgent != nil {
		contextWindow = activeAgent.ConfiguredContextWindow()
	}
	if contextWindow <= 0 && p != nil && p.Capabilities().MaxContextWindow > 0 {
		contextWindow = provider.ClampContextWindow(p.Capabilities().MaxContextWindow)
	}
	if contextWindow <= 0 {
		contextWindow = provider.DefaultUnknownContextWindow
	}
	return providerRuntimeSnapshot{
		Provider:      p,
		ProviderName:  sdk.providerName,
		Model:         sdk.currentModel,
		ContextWindow: contextWindow,
	}
}

// GetProviderName returns the provider name (for headless mode).
// Falls back to the local field when sdkClient is nil (degraded mode).
func (sdk *SDKIntegration) GetProviderName() string {
	if sdk.sdkClient != nil {
		if name := sdk.sdkClient.ProviderName(); name != "" {
			return name
		}
	}
	return sdk.providerName
}

// GetCurrentModel returns the currently configured model ID.
// Falls back to the local field when sdkClient is nil (degraded mode).
func (sdk *SDKIntegration) GetCurrentModel() string {
	if sdk.sdkClient != nil {
		if model := sdk.sdkClient.CurrentModel(); model != "" {
			return model
		}
	}
	return sdk.currentModel
}

// GetMaxTokens returns the max output tokens per request, reading from the SDK
// client (authoritative — the client is constructed WithMaxTokens(sdk.maxTokens)
// and the value is never mutated independently). Falls back to the local mirror
// field during the construction window / degraded mode.
func (sdk *SDKIntegration) GetMaxTokens() int {
	if sdk.sdkClient != nil {
		if mt := sdk.sdkClient.MaxTokens(); mt > 0 {
			return mt
		}
	}
	return sdk.maxTokens
}

// GetToolRegistry returns the effective tool registry.
// Codemode works in-place by hiding/unhiding tools in this same registry,
// so callers always get the correct view without needing a separate wrapped registry.
func (sdk *SDKIntegration) GetToolRegistry() tools.Registry {
	if sdk.sdkClient != nil {
		if reg := sdk.sdkClient.AgentToolRegistry(); reg != nil {
			return reg
		}
	}
	return sdk.toolRegistry
}

// GetMCPManager returns the MCP manager for accessing MCP server state.
func (sdk *SDKIntegration) GetMCPManager() *MCPManager {
	return sdk.mcpManager
}

// GetHooksManager returns the hooks manager (for headless mode)
func (sdk *SDKIntegration) GetHooksManager() *HooksManager {
	return sdk.hooksManager
}

// SetHooksManager sets the hooks manager (for headless mode initialization)
func (sdk *SDKIntegration) SetHooksManager(hm *HooksManager) {
	sdk.hooksManager = hm
	// Also set on the agent if it exists
	if sdk.activeAgent() != nil && hm != nil {
		sdk.activeAgent().SetHooksManager(hm)
	}
}

// ContextOrchestrator returns the context orchestrator for context injection.
func (sdk *SDKIntegration) ContextOrchestrator() *chatcontext.ContextOrchestrator {
	return sdk.contextOrchestrator
}

// SetCronPromptHandler attaches the callback used to deliver prompts fired by
// the cron scheduler / ScheduleWakeup into the main chat session. Prompts
// fired before the handler attaches are buffered and flushed on attach.
func (sdk *SDKIntegration) SetCronPromptHandler(h func(prompt string)) {
	if sdk.cronPromptSink != nil {
		sdk.cronPromptSink.SetHandler(h)
	}
}

// injectionEnabled reports whether an injection-kind context source (skills,
// workspace_env, dream_contract, hook_context) is enabled in the context
// settings. Defaults to enabled when no orchestrator is wired (headless/tests).
func (sdk *SDKIntegration) injectionEnabled(id string) bool {
	if sdk.contextOrchestrator == nil {
		return true
	}
	return sdk.contextOrchestrator.IsInjectionEnabled(id)
}

// SetLifecycleHooksConfig sets the lifecycle hooks configuration
func (sdk *SDKIntegration) SetLifecycleHooksConfig(config lifecycle.LifecycleHooksConfig) {
	if sdk.lifecycleHookExecutor != nil {
		sdk.lifecycleHookExecutor = lifecycle.NewExecutor(config)
	}
}

// SetupTaskPersistence sets up automatic task persistence for the TodoManager.
// It creates a TaskStore keyed to convID, wires it to the global TodoManager syncer,
// and restores any previously persisted tasks from disk.
//
// This must be called whenever a conversation is opened or created. It must use
// the provided convID directly — it cannot rely on sdk.activeAgent().TaskStore() because
// the agent's internal conversationID is only set during Execute(), which has not
// run yet at open time.
func (sdk *SDKIntegration) SetupTaskPersistence(convID string) error {
	if convID == "" {
		return nil // No conversation to attach to
	}
	ag := sdk.activeAgent()
	if ag == nil {
		return nil
	}
	// Wire goal persistence to the same conversation metadata dir so /goal
	// state survives restarts and conversation switches, exactly like tasks.
	sdk.SetupGoalPersistence(convID)
	return ag.SetupTaskPersistence(convID)
}

// SetupGoalPersistence points the GoalHook at goal.json inside the given
// conversation's metadata directory and loads any persisted goal. It resolves
// the same canonical conversation metadata path used by
// SetupTaskPersistence. Safe to call with an empty convID (clears the store).
func (sdk *SDKIntegration) SetupGoalPersistence(convID string) {
	if sdk.hooksManager == nil {
		return
	}
	if convID == "" {
		sdk.hooksManager.SetupGoalPersistence("")
		return
	}
	metadataDir := filepath.Join(paths.ConversationsDir(), convID, "metadata")
	sdk.hooksManager.SetupGoalPersistence(metadataDir)
}

// SaveTasksForConversation explicitly saves the current tasks from the global
// TodoManager to the taskstore for the specified conversation ID.
// This must be called BEFORE switching to a different conversation to ensure
// tasks are persisted correctly.
//
// The issue this solves: TodoManager is a global singleton. When switching
// conversations, the syncer is changed to the new conversation's taskstore.
// This means tasks for the OLD conversation would be lost. By calling this
// method before switching, we explicitly save the tasks to the correct store.
func (sdk *SDKIntegration) SaveTasksForConversation(convID string) error {
	if convID == "" {
		return nil
	}
	ag := sdk.activeAgent()
	if ag == nil {
		return nil
	}
	return ag.SaveTasksForConversation(convID)
}

// EnableAdvancedToolMode activates the deferred tool loading framework.
// It creates a DeferredRegistry, applies smart defaults (core tools always
// eager, heavy tools deferred), registers the ToolSearchTool, and wires
// the agent's ToolFilterFunc so buildProviderTools() actually filters.
func (sdk *SDKIntegration) EnableAdvancedToolMode(deferTokenThreshold int) {
	if sdk.harnessGoverned() {
		if sdk.logger != nil {
			sdk.logger.Info(context.Background(), "advanced_tool_mode.skipped_harness_governed",
				observability.F("reason", "meta.tool_search is host-binding-required on the harness branch; the plan is the only source of the tool set"))
		}
		return
	}
	if sdk.advancedToolMode {
		return // Already enabled
	}

	sdk.advancedToolMode = true
	sdk.deferTokenThreshold = deferTokenThreshold

	// Create deferred registry wrapping the existing tool registry
	sdk.deferredRegistry = advanced.NewDeferredRegistry(sdk.toolRegistry)

	// Apply intelligent defaults (Claude Code pattern):
	// - Core tools (bash, read, write, edit, grep, apply_patch) → always eager
	// - Heavy/niche tools (fullstack_project_init, etc.) → always deferred
	// - Tools exceeding token threshold → auto-deferred (unless core)
	deferredCount := advanced.ApplySmartDefaults(sdk.deferredRegistry, sdk.toolRegistry, deferTokenThreshold)

	// Create and register the ToolSearchTool (always-eager meta-tool)
	sdk.toolSearchTool = advanced.NewToolSearchTool(sdk.toolRegistry)

	// Wire promotion callback: when tool_search finds a deferred tool,
	// auto-promote it so it appears in subsequent buildProviderTools() calls
	sdk.toolSearchTool.SetPromoteCallback(func(toolName string) {
		if sdk.deferredRegistry != nil {
			sdk.deferredRegistry.ForceDefer(toolName, false) // false = eager
			if sdk.logger != nil {
				sdk.logger.Info(context.Background(), "advanced_tool_mode.tool_promoted",
					observability.F("tool", toolName))
			}
		}
	})

	if err := sdk.toolRegistry.Register(sdk.toolSearchTool); err != nil {
		if sdk.logger != nil {
			sdk.logger.Warn(context.Background(), "failed to register tool_search tool",
				observability.F("error", err.Error()))
		}
	}

	// Wire the agent's ToolFilterFunc so buildProviderTools() actually
	// strips deferred tools from the provider request
	if sdk.activeAgent() != nil {
		dr := sdk.deferredRegistry
		sdk.activeAgent().SetToolFilter(func(allTools []provider.Tool) []provider.Tool {
			eager, _ := dr.SplitTools(allTools)
			return eager
		})
	}

	stats := sdk.deferredRegistry.CalculateStats()
	if sdk.logger != nil {
		sdk.logger.Info(context.Background(), "advanced_tool_mode.enabled",
			observability.F("threshold", deferTokenThreshold),
			observability.F("deferred_count", deferredCount),
			observability.F("eager_count", stats.EagerCount),
			observability.F("total_tools", stats.TotalCount),
			observability.F("saved_tokens", stats.DeferredSavedTokens),
			observability.F("savings_percent", stats.SavingsPercent))
	}
}

// DisableAdvancedToolMode deactivates the deferred tool loading framework.
// It removes the ToolSearchTool, clears the deferred registry, and removes
// the agent's ToolFilterFunc so all tools are sent again.
func (sdk *SDKIntegration) DisableAdvancedToolMode() {
	if !sdk.advancedToolMode {
		return // Already disabled
	}

	sdk.advancedToolMode = false

	// Remove the agent's tool filter (back to sending all tools)
	if sdk.activeAgent() != nil {
		sdk.activeAgent().SetToolFilter(nil)
	}

	// Unregister the ToolSearchTool
	if sdk.toolSearchTool != nil {
		sdk.toolRegistry.Unregister(advanced.ToolSearchName)
		sdk.toolSearchTool = nil
	}

	// Clear the deferred registry
	sdk.deferredRegistry = nil

	if sdk.logger != nil {
		sdk.logger.Info(context.Background(), "advanced_tool_mode.disabled")
	}
}

// IsAdvancedToolMode returns whether advanced tool mode is active
func (sdk *SDKIntegration) IsAdvancedToolMode() bool {
	return sdk.advancedToolMode
}

// GetDeferredRegistry returns the deferred registry (nil if advanced tool mode is disabled)
func (sdk *SDKIntegration) GetDeferredRegistry() *advanced.DeferredRegistry {
	return sdk.deferredRegistry
}

// GetAdvancedToolStats returns context statistics for the current tool set
// (only meaningful when advanced tool mode is enabled)
func (sdk *SDKIntegration) GetAdvancedToolStats() *advanced.ContextStats {
	if sdk.deferredRegistry == nil {
		return nil
	}
	stats := sdk.deferredRegistry.CalculateStats()
	return &stats
}

// RefreshToolSearch refreshes the ToolSearchTool's index after tools have been
// added or removed. Call this after MCP servers finish loading, for example.
func (sdk *SDKIntegration) RefreshToolSearch() {
	if sdk.toolSearchTool != nil {
		sdk.toolSearchTool.Refresh()
	}
}

// TokenUpdate represents a real-time token usage update
type TokenUpdate struct {
	InputTokens    int  // Current context size (full input sent to model)
	OutputTokens   int  // Tokens generated so far
	TotalTokens    int  // Total tokens used
	IsStreaming    bool // Whether this is during streaming
	IsFinal        bool // Whether this is the final update for this turn
	ConversationID string
}

// ProviderModel represents a model from providers.json
type ProviderModel struct {
	ID                      string   `json:"id"`
	DisplayName             string   `json:"display_name"`
	ContextWindow           int      `json:"context_window"` // Integer token count (e.g., 200000)
	Context                 string   `json:"context"`        // Legacy string format (e.g., "128000" or "128k")
	SupportsReasoningEffort *bool    `json:"supports_reasoning_effort,omitempty"`
	ReasoningEfforts        []string `json:"reasoning_efforts,omitempty"`

	// Extended thinking (per-model)
	ThinkingEnabled bool   `json:"thinking_enabled,omitempty"`
	ThinkingBudget  int    `json:"thinking_budget,omitempty"`
	ThinkingEffort  string `json:"thinking_effort,omitempty"`

	// Enriched from models.dev
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
	CostInput       float64 `json:"cost_input,omitempty"`  // Cost per 1M input tokens (USD)
	CostOutput      float64 `json:"cost_output,omitempty"` // Cost per 1M output tokens (USD)
	Reasoning       bool    `json:"reasoning,omitempty"`
	ToolCall        bool    `json:"tool_call,omitempty"`
}

// ProviderConfig represents a provider from providers.json
type ProviderConfig struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"display_name"`
	Color            string            `json:"color"`
	Type             string            `json:"type"`               // "api_key" or "oauth"
	APIType          string            `json:"api_type"`           // "anthropic", "openai", or "openai-compatible"
	BaseURL          string            `json:"base_url,omitempty"` // Custom base URL for openai-compatible providers
	HTTPMaxRetries   *int              `json:"http_max_retries,omitempty"`
	Available        bool              `json:"available"`
	Models           []ProviderModel   `json:"models"`
	CodexQueryParams map[string]string `json:"codex_query_params,omitempty"`
	CodexHTTPHeaders map[string]string `json:"codex_http_headers,omitempty"`
}

// noopAuditor is a no-op implementation of observability.Auditor for cases where auditing is not needed
type noopAuditor struct{}

func (n *noopAuditor) Record(ctx context.Context, event observability.AuditEvent) error {
	return nil // No-op
}

func (n *noopAuditor) Query(ctx context.Context, criteria observability.AuditCriteria) ([]observability.AuditEvent, error) {
	return nil, nil // No-op
}

// getModelContextWindow returns the context window size for a given model
// by reading from the providers.json config file. Local config takes priority.
// Supports both new format (context_window: int) and legacy format (context: string)
// Uses smart matching: exact match > normalized match > partial match
// NewSDKIntegration creates SDK components based on current provider config.
// initialModel can be empty - use SetModel() before Execute()
func NewSDKIntegration(providerName, initialModel string) (*SDKIntegration, error) {
	return NewSDKIntegrationWithOptions(providerName, initialModel, SDKIntegrationOptions{
		MaxTokens: 31999, // Default to ~32k (must be under 32000 for Opus 4)
	})
}

// NewSDKIntegrationWithMaxTokens creates a new SDK integration with configurable max tokens
func NewSDKIntegrationWithMaxTokens(providerName, initialModel string, maxTokens int) (*SDKIntegration, error) {
	return NewSDKIntegrationWithOptions(providerName, initialModel, SDKIntegrationOptions{
		MaxTokens: maxTokens,
	})
}

// SDKIntegrationOptions configures the SDK integration
type SDKIntegrationOptions struct {
	// MaxTokens is the max output tokens for requests (default: 31999)
	MaxTokens int
	// EndpointOverride supplies per-instance custom endpoint credentials and
	// protocol. It takes precedence over saved credentials, OAuth, and proxies.
	EndpointOverride *ProviderEndpointOverride
	// DebugMode enables the DebugLogs tool for searching session logs
	DebugMode bool
	// RawDebug enables raw JSON request/response logging to stderr
	RawDebug bool
	// VerboseDebug enables verbose DEBUG-level logging including raw SSE events
	VerboseDebug bool
	// BGProcessManager is the background process manager for background bash commands
	BGProcessManager *bgprocess.BackgroundProcessManager
	// HybridConfig contains per-role model configuration for sub-agents (Feature 025)
	// Deprecated: Use ProfileManager instead. Will be removed in a future release.
	HybridConfig *core.HybridConfig
	// ProfileManager provides profile-based model alias resolution for sub-agents (Feature 026)
	ProfileManager *settings.ProfileManager
	// PermissionConfig overrides the default permissions config loader.
	PermissionConfig *PermissionConfig
	// ApprovalBroker handles interactive permission approvals.
	ApprovalBroker tools.ApprovalBroker
	// QuestionBroker handles interactive user questions from agents.
	QuestionBroker *QuestionBroker
	// AutoCompactionConfig configures automatic context compaction
	AutoCompactionConfig *agent.AutoCompactionConfig
	// PlanBroker enables the enter_plan_mode and exit_plan_mode tools.
	// When set, the plan tools are registered in the agent's tool registry.
	// nil = plan mode tools are not available to the agent.
	PlanBroker *PlanBroker
	// RootSessionID is the stable TUI session identifier generated at startup.
	// It is passed to the plan config so plan files are stored per-session
	// (~/.swarmos/conversations/<RootSessionID>/plan.md) instead of in WorkDir.
	RootSessionID string
	// ProviderEventCallback receives notifications about provider retries and switching
	ProviderEventCallback func(providerEventMsg)
	// ExaAPIKey is the Exa API key for web search (from providers.json, not env var)
	ExaAPIKey string
	// VaultProvider enables the vault tools for credential management.
	// When set, VaultExec and VaultList tools are registered.
	// nil = vault tools are not available to the agent.
	VaultProvider vault.VaultProvider
	// VaultUnlocker optionally lets an interactive host unlock and resume a
	// vault operation. Headless integrations leave it nil.
	VaultUnlocker builtin.VaultUnlocker

	// VisualRegistry, when set, enables non-blocking visual companion updates.
	// Blocking ask_user_question interactions always render in the terminal.
	VisualRegistry *visual.Registry

	// A2AEnabled enables the minimal TUI A2A integration for this session.
	A2AEnabled bool
	// A2AHandle overrides the published peer handle.
	A2AHandle string
	// A2AListenAddress overrides the local A2A listen address.
	A2AListenAddress string
	// WorkspaceRoot overrides the default cwd-derived execution root.
	WorkspaceRoot string
	// ProjectRoot is the stable primary checkout used for conversation identity.
	ProjectRoot string
	// EnableCodeMode wraps tools into a JavaScript sandbox (run_code) so the
	// LLM can batch multiple tool calls in one turn via Promise.all().
	EnableCodeMode bool

	// Hub, when non-nil, uses the WorkspaceHub WebSocket transport instead of
	// SQLite for local peer discovery. The hub must already be started
	// (StartOrConnect called) before the SDK integration is created.
	Hub *a2a.WorkspaceHub

	// HeadlessMode indicates the SDK is running without a UI and should not
	// attempt interactive operations like showing profile picker modals.
	HeadlessMode bool
	// DisableGlobalMemory suppresses user-level CLAUDE.md and SWARM.md context
	// for this SDK instance without changing persisted context configuration.
	DisableGlobalMemory bool
	// DisableProjectMemory suppresses project CLAUDE.md, SWARM.md, AGENTS.md,
	// and INDEX.md context without changing persisted context configuration.
	DisableProjectMemory bool
	// DisableSkills prevents skill discovery, prompt injection, skill tools,
	// autogenskills enforcement, curation, and plugin-skill loading.
	DisableSkills bool

	// ExplicitProviderModel indicates the caller passed an explicit
	// provider/model (e.g. `-P fireworks -m <deployment>`) that must be
	// honored verbatim. When true, the agent's fallback chain is built as a
	// single entry from the configured provider/model instead of being
	// resolved from the active profile (which would otherwise silently
	// override the explicit selection with the profile's primary provider).
	ExplicitProviderModel bool

	// Temperature, when ForceTemperature is true, pins the sampling temperature
	// for every request. Headless `-p` mode defaults this to 0.0 (greedy) so
	// re-running the same (model, question) cell is deterministic. Provider
	// translators that reject non-default sampling params still drop it.
	Temperature float64
	// ForceTemperature forces Temperature to be sent even when it is 0.0
	// (which omitempty would otherwise treat as "unset"). See Temperature.
	ForceTemperature bool

	// NoHooks, when true, disables ALL hooks for this SDK integration. The
	// hooks manager is not created and never attached to the agent, so no
	// builtin hooks (task-enforcement, protected-branch, autogenskills budget,
	// plan-mode, task-nudge) fire. This must be honored HERE at construction —
	// checking it after the fact (as the headless `-p` path historically did)
	// still let the builtin hooks attach and fire during tool execution.
	NoHooks bool

	// AllowAllPaths, when true, removes the workspace boundary so file tools
	// may read/write ANY path without the out-of-workspace interactive gate.
	// This backs the `--allow-all-paths` CLI flag (DANGEROUS), which was
	// previously advertised but had no effect. When false (default) the
	// permission checker keeps enforcing the workspace root so writes/deletes
	// outside the project directory are still presented for approval.
	AllowAllPaths bool
	// HarnessPlan activates the closed client-owned harness construction path.
	HarnessPlan *harness.Plan
	// HarnessAllowYolo is the explicit posture required by a yolo plan.
	HarnessAllowYolo bool
}

// buildProfileRoleModelSelector creates a RoleModelSelector function from ProfileManager.
// This maps SADD RoleTypes to profile ModelAliases:
//   - supervisor → steering
//   - implementer → subagent
//   - specReviewer → steering
//   - qualityReviewer → steering
//   - (default) → subagent
//
// Returns nil if ProfileManager is nil (uses default model).
// Feature 026: Unify Agent Profiles with Sub-Agent Routing
func buildProfileRoleModelSelector(mgr *settings.ProfileManager) builtin.RoleModelSelector {
	if mgr == nil {
		return nil
	}
	return bridge.BuildRoleModelSelector(mgr.Manager)
}

// buildCurrentProviderGetter creates a CurrentProviderGetter function from ProfileManager.
// This returns the provider name from the active profile's "subagent" alias.
// Returns nil if ProfileManager is nil.
// Falls back to AliasMain if AliasSubAgent is not configured.
func buildCurrentProviderGetter(mgr *settings.ProfileManager) func() string {
	if mgr == nil {
		return nil
	}
	return bridge.BuildCurrentProviderGetter(mgr.Manager)
}

// NewSDKIntegrationWithOptions creates a new SDK integration with configurable options
func NewSDKIntegrationWithOptions(providerName, initialModel string, opts SDKIntegrationOptions) (*SDKIntegration, error) {

	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 31999
	}
	// Create observability components
	logger := tuiobs.NewTUILogger()

	// Enable DEBUG level logging if VerboseDebug is requested (for raw SSE event logs)
	if opts.VerboseDebug {
		logger.SetLevel(observability.LevelDebug)
	}

	if opts.HarnessPlan != nil {
		lookupStore := observability.NewInMemoryLookupStore()
		tracer := observability.NewLocalTracer(observability.LocalTracerConfig{
			LookupStore: lookupStore,
			Redactor:    observability.NewDefaultRedactor(),
			Component:   "tui",
		})
		return newHarnessSDKIntegration(opts, logger, tracer, lookupStore)
	}

	lookupStore := observability.NewInMemoryLookupStore()

	var traceSink observability.Sink
	// Optional: persist redacted trace events when SWARM_TRACE_PATH is set.
	if tracePath := os.Getenv("SWARM_TRACE_PATH"); tracePath != "" {
		if abs, err := filepath.Abs(tracePath); err == nil {
			tracePath = abs
		}
		sink, err := observability.NewJSONLSink(tracePath)
		if err != nil {
			logger.Warn(context.Background(), "failed to create trace jsonl sink",
				observability.F("error", err.Error()),
				observability.F("path", tracePath),
			)
		} else {
			traceSink = sink
		}
	}

	tracer := observability.NewLocalTracer(observability.LocalTracerConfig{
		Sink:        traceSink,
		LookupStore: lookupStore,
		Redactor:    observability.NewDefaultRedactor(),
		Component:   "tui",
	})

	// Create token tracker for persistent token usage logging
	// Note: TokenTracker support removed - continuing without token tracking

	// Build the LLM provider.  If credentials are missing or invalid the SDK
	// still initialises fully (tools, storage, hooks, MCP, skills…) — the user
	// can add a provider later via /auth or settings without restarting.
	prov, systemPrompt, authToken, isOAuth, err := buildProviderWithTracker(providerName, initialModel, logger, tracer, nil, opts.RawDebug, opts.EndpointOverride)
	if err != nil {
		logger.Warn(context.Background(), "sdk.provider_build_deferred",
			observability.F("provider", providerName),
			observability.F("error", err.Error()),
			observability.F("reason", "SDK will initialise without a provider; add one via /auth or settings"),
		)
		// prov stays nil — guarded below wherever it is used.
	}

	// Create conversation storage (workspace-partitioned for efficient per-workspace listing)
	storageDir := paths.ConversationsDir()

	// Determine initial model early to resolve context window consistently
	// This ensures both storage and agent use the same user-configured context window
	if initialModel == "" {
		initialModel = defaultModelForProvider(providerName)
	}

	// Compute resolved context window once from user config (providers.json)
	// This value takes precedence over provider.Capabilities() so user overrides always win.
	// We use this same value for both storage initialization and agent setup.
	resolvedContextWindow := GetContextWindowForModel(initialModel, providerName)
	if resolvedContextWindow == 0 && prov != nil {
		// Fallback to provider capabilities if not configured by user
		resolvedContextWindow = prov.Capabilities().MaxContextWindow
	}

	// Create tool registry and register builtin tools
	// Workspace root is where swarm-tui was opened from
	// Sub-agents inherit this to see the same directory tree
	workspaceRoot := strings.TrimSpace(opts.WorkspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot, _ = os.Getwd()
	}
	logDebug("Workspace root: %s (source: %s)", workspaceRoot, func() string {
		if strings.TrimSpace(opts.WorkspaceRoot) != "" {
			return "SDKIntegrationOptions.WorkspaceRoot"
		}
		if os.Getenv("SWARM_WORKSPACE_ROOT") != "" {
			return "SWARM_WORKSPACE_ROOT env"
		}
		return "cwd"
	}())
	if err != nil {
		workspaceRoot = ""
	}
	if workspaceRoot != "" {
		if absRoot, err := filepath.Abs(workspaceRoot); err == nil {
			workspaceRoot = absRoot
		}
	}
	projectRoot := strings.TrimSpace(opts.ProjectRoot)
	if projectRoot == "" {
		projectRoot = workspaceRoot
	} else if absRoot, err := filepath.Abs(projectRoot); err == nil {
		projectRoot = absRoot
	}

	toolRegistry := tools.NewSimpleRegistry(logger, tracer)

	// Load permission policies and configure the registry
	permConfig := opts.PermissionConfig
	if permConfig == nil {
		var err error
		permConfig, err = LoadPermissionConfig()
		if err != nil {
			logger.Warn(context.Background(), "failed to load permission config",
				observability.F("error", err.Error()))
			permConfig = NewPermissionConfigWithDefaults()
			if saveErr := permConfig.Save(); saveErr != nil {
				logger.Warn(context.Background(), "failed to save default permission config",
					observability.F("error", saveErr.Error()))
			}
		}
	}

	permChecker := tools.NewInteractivePermissionChecker(permConfig.Config(), opts.ApprovalBroker)
	// Wire workspace root so the checker can enforce out-of-workspace write
	// protection even in YOLO mode (writes outside workspace still ask).
	// When AllowAllPaths is set (--allow-all-paths, DANGEROUS) we deliberately
	// leave the workspace root unset so no path boundary is enforced.
	if workspaceRoot != "" && !opts.AllowAllPaths {
		permChecker.SetWorkspaceRoot(workspaceRoot)
	}
	if opts.AllowAllPaths {
		logDebug("PERMISSIONS: --allow-all-paths active — workspace boundary disabled for permission checker AND builtin/forge file tools (blocked system dirs like /proc,/sys still refused)")
	}
	toolRegistry.SetPermissionChecker(permChecker)

	// Register builtin tools
	// builtinAllowedPaths includes workspace + /tmp for writes, and ~/.swarmos/
	// for reading conversation history, plan files, skills, and session state.
	//
	// When AllowAllPaths is set (--allow-all-paths, DANGEROUS) we leave this nil.
	// The builtin file/bash/grep/list tools treat an empty AllowedPaths as
	// UNRESTRICTED (see builtin.checkAllowedPath: len==0 → allowed), so this is
	// what actually lets those tools touch any path — lifting the permission
	// checker's workspace boundary alone is NOT sufficient because these tools
	// enforce their own independent path allowlist.
	var builtinAllowedPaths []string
	if !opts.AllowAllPaths {
		swarmosDir := ""
		if home, err := os.UserHomeDir(); err == nil {
			swarmosDir = filepath.Join(home, ".swarmos")
		}
		builtinAllowedPaths = []string{"/tmp"}
		if swarmosDir != "" {
			builtinAllowedPaths = append(builtinAllowedPaths, swarmosDir)
		}
		if workspaceRoot != "" {
			builtinAllowedPaths = append([]string{workspaceRoot}, builtinAllowedPaths...)
		}
	}

	// Register background bash tool (with background process support)
	if opts.BGProcessManager != nil {
		// Get default bash config to include shell path, then override allowed paths
		bashConfig := builtin.DefaultBashConfig()
		bashConfig.AllowedPaths = builtinAllowedPaths
		bashConfig.DefaultCwd = workspaceRoot // Ensure commands run in workspace by default

		// Create background bash tool that wraps the standard bash tool
		bgBashTool := bgprocess.NewBackgroundBashTool(bgprocess.BackgroundBashToolConfig{
			BashConfig:        bashConfig,
			Manager:           opts.BGProcessManager,
			DefaultTimeout:    5 * time.Minute,
			AutoBackgroundSec: 30, // Auto-background commands that run longer than 30 seconds
		})

		if err := toolRegistry.Register(bgBashTool); err != nil {
			logger.Warn(context.Background(), "failed to register background bash tool", observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered", observability.F("tool", "Bash (with background support)"))
		}

		// Register ReadBackgroundCommand tool for querying background processes
		readBgTool := bgprocess.NewReadBackgroundCommandTool(opts.BGProcessManager)
		if err := toolRegistry.Register(readBgTool); err != nil {
			logger.Warn(context.Background(), "failed to register ReadBackgroundCommand tool", observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered", observability.F("tool", "ReadBackgroundCommand"))
		}
	} else {
		// Fallback to standard bash tool if no background process manager
		bashConfig := builtin.DefaultBashConfig()
		bashConfig.AllowedPaths = builtinAllowedPaths
		bashConfig.DefaultCwd = workspaceRoot // Ensure commands run in workspace by default
		if err := toolRegistry.Register(builtin.NewBashToolWithConfig(bashConfig)); err != nil {
			logger.Warn(context.Background(), "failed to register bash tool", observability.F("error", err.Error()))
		}
	}

	// NOTE: agent_browser tool intentionally not registered. Its schema is the
	// single largest tool (~2.2k tokens) and headless browser automation is not
	// needed by default; re-add builtin.NewAgentBrowserTool() here if required.

	// Keep TaskManage as the sole live task-record tool. Historical calls remain
	// renderable without keeping legacy names executable.
	if err := ii.RegisterProductivityTools(toolRegistry); err != nil {
		logger.Warn(context.Background(), "failed to register productivity tools", observability.F("error", err.Error()))
	}

	// Register ask_user_question tool if QuestionBroker is available.
	// Visual-choice questions render through the terminal QuestionBroker.
	if opts.QuestionBroker != nil {
		interactionBroker := NewTUIInteractionBroker(opts.QuestionBroker, nil)
		if opts.ApprovalBroker != nil {
			if pb, ok := opts.ApprovalBroker.(*PermissionsBroker); ok {
				interactionBroker = NewTUIInteractionBroker(opts.QuestionBroker, pb)
			}
		}
		if opts.VisualRegistry != nil {
			interactionBroker.SetVisualRegistry(opts.VisualRegistry)
			interactionBroker.SetSessionID(opts.RootSessionID)
		}
		askUserTool := builtin.NewAskUserQuestionTool(interactionBroker)
		if err := toolRegistry.Register(askUserTool); err != nil {
			logger.Warn(context.Background(), "failed to register ask_user_question tool",
				observability.F("error", err.Error()))
		}
	}

	// Register Plan Mode tools (enter_plan_mode, exit_plan_mode) if PlanBroker is available
	if opts.PlanBroker != nil {
		planCfg := plan.DefaultConfig()
		// WorkDir remains authoritative for validating agent-submitted local
		// plans even when SessionID selects a conversation-scoped canonical copy.
		planCfg.WorkDir = workspaceRoot
		if opts.RootSessionID != "" {
			planCfg.SessionID = opts.RootSessionID
		}
		enterPlanTool := plan.NewEnterPlanModeTool(opts.PlanBroker, planCfg)
		exitPlanTool := plan.NewExitPlanModeTool(opts.PlanBroker, planCfg)
		if err := toolRegistry.Register(enterPlanTool); err != nil {
			logger.Warn(context.Background(), "failed to register enter_plan_mode tool",
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered",
				observability.F("tool", "enter_plan_mode"))
		}
		if err := toolRegistry.Register(exitPlanTool); err != nil {
			logger.Warn(context.Background(), "failed to register exit_plan_mode tool",
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered",
				observability.F("tool", "exit_plan_mode"))
		}
	}

	// Register Vault tools (vault_list, vault_exec, vault_add)
	// These are ALWAYS registered so agents know they exist, even when locked.
	// When locked, the tools return helpful messages telling the user how to unlock.
	vaultTools := builtin.ConfigureVaultWithUnlocker(opts.VaultProvider, opts.VaultUnlocker)
	for _, tool := range vaultTools {
		if err := toolRegistry.Register(tool); err != nil {
			logger.Warn(context.Background(), "failed to register vault tool",
				observability.F("tool", tool.Name()),
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered",
				observability.F("tool", tool.Name()),
				observability.F("vault_state", func() string {
					if opts.VaultProvider != nil && opts.VaultProvider.IsEnabled() {
						return "unlocked"
					}
					return "locked"
				}()))
		}
	}

	// Create WorkspaceManager for ii file system and dev tools
	var iiWorkspaceManager *ii.WorkspaceManager
	if workspaceRoot != "" {
		var err error
		iiWorkspaceManager, err = ii.NewWorkspaceManager(workspaceRoot)
		if err != nil {
			logger.Warn(context.Background(), "failed to create ii workspace manager", observability.F("error", err.Error()))
		}
	}

	// Register Forge file system tools (Read, Write, Edit, Grep, Undo)
	// These are 1:1 implementations of Forge's tools without hash-based verification
	if iiWorkspaceManager != nil {
		// Forge tools scope reads/writes to their workspacePath (validateScope).
		// Passing an empty workspacePath makes them trusted/unrestricted (still
		// blocklist-gated against pathological system dirs like /proc, /sys), so
		// --allow-all-paths genuinely lifts the boundary for Read/Write/Edit too
		// — not just for bash/grep via builtinAllowedPaths.
		forgeWorkspace := workspaceRoot
		if opts.AllowAllPaths {
			forgeWorkspace = ""
		}
		for _, tool := range forge.DefaultTools(forgeWorkspace) {
			if err := toolRegistry.Register(tool); err != nil {
				logger.Warn(context.Background(), "failed to register forge tool",
					observability.F("tool", tool.Name()),
					observability.F("error", err.Error()))
			}
		}

	}

	// Register debug tools if debug mode is enabled
	if opts.DebugMode {
		debugLogsTool := ii.NewDebugLogsTool("swarmos_debug.log")
		if err := toolRegistry.Register(debugLogsTool); err != nil {
			logger.Warn(context.Background(), "failed to register debug_logs tool",
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "debug mode enabled - debug_logs tool registered")
		}
	}

	// Register web search tool with proper backend selection
	// Use NewFromCoreConfig to respect provider config Exa API key (not just env var)
	wsCoreCfg := &core.WebSearchConfig{}
	exaAPIKey := opts.ExaAPIKey
	webSearchTool := websearch.NewFromCoreConfig(wsCoreCfg, exaAPIKey)
	if err := toolRegistry.Register(webSearchTool); err != nil {
		logger.Warn(context.Background(), "failed to register websearch tool",
			observability.F("error", err.Error()))
	} else {
		backend := "auto"
		if exaAPIKey != "" || os.Getenv("EXA_API_KEY") != "" {
			backend = "exa"
		} else if websearch.IsAnthropicAuthConfigured() {
			backend = "anthropic"
		}
		logger.Info(context.Background(), "tool.registered",
			observability.F("tool", "websearch"),
			observability.F("backend", backend))
	}

	// Register xAI tools (x_search + xai_web_search) when SuperGrok OAuth or
	// XAI_API_KEY is available. These use xAI's Responses API (/v1/responses)
	// with server-side tools that give Grok privileged access to X (Twitter)
	// data and real-time web browsing — fundamentally richer than chat completions.
	if xaitools.HasCredentials() {
		xsearch := xaitools.NewXSearchTool()
		if err := toolRegistry.Register(xsearch); err != nil {
			logger.Warn(context.Background(), "failed to register x_search tool",
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered",
				observability.F("tool", "x_search"),
				observability.F("provider", "xai"))
		}
		xws := xaitools.NewWebSearchTool()
		if err := toolRegistry.Register(xws); err != nil {
			logger.Warn(context.Background(), "failed to register xai_web_search tool",
				observability.F("error", err.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered",
				observability.F("tool", "xai_web_search"),
				observability.F("provider", "xai"))
		}
	}

	// Use provided model or fallback for validation
	// Note: initialModel was already determined earlier for storage initialization,
	// so this is just a safety check (should always be set by now)
	if initialModel == "" {
		initialModel = defaultModelForProvider(providerName)
	}

	// Initialize project memory for workspace-scoped context.
	if workspaceRoot != "" {
		if err := projectmemory.InitializeTools(workspaceRoot); err != nil {
			logger.Warn(context.Background(), "failed to initialize project memory database",
				observability.F("error", err.Error()))
		}
	}

	// Register Computer Use tools (screen control, mouse, keyboard) if enabled
	// Only available on Linux with X11/Wayland display server
	if runtime.GOOS == "linux" {
		if configMgr, err := commands.NewConfigManager(); err == nil {
			if config, err := configMgr.LoadConfig(); err == nil && config != nil && config.ComputerUseEnabled {
				// Create X11 backend (auto-detects display)
				backend, err := x11.NewBackend(x11.DefaultOptions())
				if err != nil {
					logger.Warn(context.Background(), "failed to create computer use backend",
						observability.F("error", err.Error()))
				} else {
					// Create Linux executor
					executor, err := computeruse.NewLinuxExecutor(backend, computeruse.DefaultOptions())
					if err != nil {
						logger.Warn(context.Background(), "failed to create computer use executor",
							observability.F("error", err.Error()))
						backend.Close()
					} else {
						// Register all computer use tools
						computerUseTools := computeruse.NewToolsFromExecutor(executor, logger)
						for _, tool := range computerUseTools {
							if err := toolRegistry.Register(tool); err != nil {
								logger.Warn(context.Background(), "failed to register computer use tool",
									observability.F("tool", tool.Name()),
									observability.F("error", err.Error()))
							} else {
								logger.Info(context.Background(), "tool.registered",
									observability.F("tool", tool.Name()))
							}
						}
						logger.Info(context.Background(), "computer_use.tools_registered",
							observability.F("count", len(computerUseTools)))
					}
				}
			}
		}
	}

	// Create agent factory for sub-agent and background agent tools
	// IMPORTANT: This must happen AFTER initialModel is determined
	providerRegistry := provider.NewSimpleRegistry(logger)
	registryProviderSlot := &providerSlot{
		provider:      prov,
		providerName:  providerName,
		model:         initialModel,
		contextWindow: resolvedContextWindow,
	}

	// Register ALL available providers from providers.json for agent spawning
	// This allows Task to use any configured provider, not just the current one
	// A run-scoped endpoint override deliberately registers only its current
	// provider so subagents cannot resolve a same-named persisted factory with
	// different credentials or protocol.
	if opts.EndpointOverride == nil {
		if err := registerAllProviders(providerRegistry, logger, tracer, isOAuth); err != nil {
			logger.Warn(context.Background(), "failed to register all providers",
				observability.F("error", err.Error()))
		}
	}

	// Also register the current provider as a fallback (in case providers.json isn't available)
	// Use the name as-is (lowercased) — don't normalize, to match the registry keys
	// set by registerAllProviders which uses pc.Name directly.
	lowerProviderName := strings.ToLower(strings.TrimSpace(providerName))
	if err := providerRegistry.Register(lowerProviderName, func(cfg provider.Config) (provider.Provider, error) {
		current := registryProviderSlot.Get()
		if current == nil {
			return nil, fmt.Errorf("provider %q is not initialized", lowerProviderName)
		}
		return current, nil
	}); err != nil {
		// This may error if already registered by registerAllProviders, which is fine
		logger.Debug(context.Background(), "skipped registering current provider (already registered)",
			observability.F("provider", lowerProviderName))
	}

	// Ensure the factory's normalized lookup (e.g. claudecode -> anthropic)
	// resolves to this current-provider factory even when providers.json was
	// unavailable and registerAllProviders registered nothing. Without this an
	// OAuth-only session has Task/sub-agents broken from launch.
	ensureNormalizedAlias(providerRegistry, lowerProviderName)

	// Register display-name aliases so fallback chains from providers.json
	// (which use display names like "ClaudeCode", "Gemini") resolve to the
	// normalized registry keys ("anthropic", "gemini").
	// Uses SDK StandardAliases for comprehensive coverage.
	providerRegistry.SetupStandardAliases()

	// Create noop auditor for agent factory (required but not used in TUI)
	noopAuditor := &noopAuditor{}

	agentFactory, err := agent.NewSimpleFactory(agent.FactoryConfig{
		ProviderRegistry: providerRegistry,
		Logger:           logger,
		Tracer:           tracer,
		Auditor:          noopAuditor,
	})
	if err != nil {
		logger.Warn(context.Background(), "failed to create agent factory", observability.F("error", err.Error()))
	}

	// Create SDK background agent manager
	sdkBgManager := NewSDKBackgroundAgentManager()

	// Debug log before attempting registration
	logDebug("AGENT_TOOLS: Attempting registration, factory_nil=%v, model=%s", agentFactory == nil, initialModel)

	// Register agent delegation and background agent tools (if factory created successfully)
	if agentFactory != nil {
		logDebug("AGENT_TOOLS: Registering tools with model=%s, provider=%s", initialModel, lowerProviderName)

		// Delegate task tool
		delegateTool, err := builtin.NewSubagentTool(builtin.DelegateTaskConfig{
			Factory: agentFactory,
			ProviderConfig: provider.Config{
				Name:          lowerProviderName,
				APIKey:        authToken, // Pass the current provider's API key/token
				Model:         initialModel,
				ContextWindow: resolvedContextWindow,
				Custom: map[string]interface{}{
					"is_oauth": isOAuth,
					"logger":   logger,
					"tracer":   tracer,
				},
			},
			ProviderConfigResolver: func(pName string) (provider.Config, error) {
				return getProviderConfigForResolver(pName, logger, tracer)
			},
			ContextWindowResolver: func(pName, model string) int {
				return GetContextWindowForModel(model, pName)
			},
			ParentToolReg:         toolRegistry, // Pass parent's tool registry so sub-agents inherit tools
			Logger:                logger,
			Tracer:                tracer,
			RoleModelSelector:     buildProfileRoleModelSelector(opts.ProfileManager), // Feature 026: Profile-based model selection
			CurrentProviderGetter: buildCurrentProviderGetter(opts.ProfileManager),    // Fall back to active profile's provider
			BGManager:             sdkBgManager,                                       // Background agent manager for run_in_background support
			WorkspaceRoot:         workspaceRoot,                                      // Pass workspace boundary to sub-agents
		})
		if err != nil {
			logger.Warn(context.Background(), "failed to create Task tool", observability.F("error", err.Error()))
		} else {
			if err := toolRegistry.Register(delegateTool); err != nil {
				logger.Warn(context.Background(), "failed to register Task tool", observability.F("error", err.Error()))
				logDebug("AGENT_TOOLS: Failed to register Task: %v", err)
			} else {
				logger.Info(context.Background(), "tool.registered", observability.F("tool", "Task"))
				logDebug("AGENT_TOOLS: ✓ Registered Task")
			}
		}

		// Check background agent tool
		checkBgTool, err := builtin.NewSubagentOutputTool(builtin.SubagentOutputConfig{
			BGManager: sdkBgManager,
			Logger:    logger,
			Tracer:    tracer,
		})
		if err != nil {
			logger.Warn(context.Background(), "failed to create TaskOutput tool", observability.F("error", err.Error()))
		} else {
			if err := toolRegistry.Register(checkBgTool); err != nil {
				logger.Warn(context.Background(), "failed to register TaskOutput tool", observability.F("error", err.Error()))
				logDebug("AGENT_TOOLS: Failed to register TaskOutput: %v", err)
			} else {
				logger.Info(context.Background(), "tool.registered", observability.F("tool", "TaskOutput"))
				logDebug("AGENT_TOOLS: ✓ Registered TaskOutput")
			}
		}

		logger.Info(context.Background(), "agent_tools.registration_complete",
			observability.F("tools", []string{"Task", "TaskOutput"}))
		logDebug("AGENT_TOOLS: ✓✓ All 2 agent tools registered successfully")

		// Register Delegate + DelegateOutput — forked task agent with parent Q&A
		delegateRegistry := builtin.NewDelegateRegistry()
		delegateForkTool, delErr := builtin.NewDelegateTool(builtin.DelegateToolConfig{
			Factory:   agentFactory,
			Registry:  delegateRegistry,
			BGManager: sdkBgManager,
			ProviderConfig: provider.Config{
				Name:   lowerProviderName,
				APIKey: authToken,
				Model:  initialModel,
				Custom: map[string]interface{}{
					"is_oauth": isOAuth,
					"logger":   logger,
					"tracer":   tracer,
				},
			},
			ProviderConfigResolver: func(pName string) (provider.Config, error) {
				return getProviderConfigForResolver(pName, logger, tracer)
			},
			ContextWindowResolver: func(pName, model string) int {
				return GetContextWindowForModel(model, pName)
			},
			ParentToolReg: toolRegistry,
			Logger:        logger,
			Tracer:        tracer,
		})
		if delErr != nil {
			logger.Warn(context.Background(), "delegate.create_failed", observability.F("error", delErr.Error()))
		} else if regErr := toolRegistry.Register(delegateForkTool); regErr != nil {
			logger.Warn(context.Background(), "delegate.register_failed", observability.F("error", regErr.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered", observability.F("tool", "Delegate"))
		}

		delegateOutputTool, delOutErr := builtin.NewDelegateOutputTool(builtin.DelegateOutputConfig{
			Registry:  delegateRegistry,
			BGManager: sdkBgManager,
			Logger:    logger,
			Tracer:    tracer,
		})
		if delOutErr != nil {
			logger.Warn(context.Background(), "delegate_output.create_failed", observability.F("error", delOutErr.Error()))
		} else if regErr := toolRegistry.Register(delegateOutputTool); regErr != nil {
			logger.Warn(context.Background(), "delegate_output.register_failed", observability.F("error", regErr.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered", observability.F("tool", "DelegateOutput"))
		}
	} else {
		logger.Warn(context.Background(), "agent_tools.skipped", observability.F("reason", "factory creation failed"))
		logDebug("AGENT_TOOLS: ✗✗✗ Skipping agent tools - factory is nil")
	}

	// Load and apply disabled builtin tools from config
	// This ensures tools disabled via settings UI remain disabled on startup
	if configMgr, err := commands.NewConfigManager(); err == nil {
		if config, err := configMgr.LoadConfig(); err == nil && config != nil && config.DisabledBuiltinTools != nil {
			for toolName, disabled := range config.DisabledBuiltinTools {
				if disabled {
					if err := toolRegistry.DisableTool(toolName); err == nil {
						logger.Debug(context.Background(), "tool.disabled_from_config",
							observability.F("tool", toolName))
					}
				}
			}
		}
	}

	// Initialize profile manager and load active profile
	profileMgr := opts.ProfileManager
	if profileMgr == nil {
		profileMgr = settings.NewProfileManager()
	}
	activeProfile, err := profileMgr.GetActiveProfile()
	if err != nil {
		logger.Warn(context.Background(), "failed to load active profile, using default model selection",
			observability.F("error", err.Error()))
		activeProfile = nil // Will use hardcoded models as fallback
	} else {
		logger.Info(context.Background(), "profile.loaded",
			observability.F("profile_id", activeProfile.ID),
			observability.F("profile_name", activeProfile.Name))
	}
	// Create agent definition
	agentDef := &agent.Definition{
		ID:           "tui-agent",
		Name:         "TUI Agent",
		Description:  "Main agent for TUI interactions",
		Provider:     lowerProviderName,
		Model:        initialModel,  // From config or fallback
		ToolHints:    []string{"*"}, // All tools
		SystemPrompt: systemPrompt,
		Capabilities: &agent.Capabilities{
			MaxTokens:         maxTokens, // From settings (default 32k)
			MaxTurns:          0,         // Unlimited turns for interactive chat
			SupportsTools:     true,
			SupportsStreaming: false, // TUI uses synchronous execution
			// Temperature is only pinned when the caller forces it (headless -p
			// defaults to greedy/0.0 for deterministic benchmark re-runs). When
			// ForceTemperature is false these stay zero-valued and the request
			// builder omits temperature entirely (provider default), preserving
			// existing TUI behavior.
			Temperature:      opts.Temperature,
			ForceTemperature: opts.ForceTemperature,
		},
	}

	// Feature 026: Resolve the chain for the 'main' role from active profile.
	// The chain handles provider routing at runtime via executeWithChain —
	// it dynamically creates providers from the registry and falls back through
	// the pool. We set the chain on the definition but keep Provider/Model
	// matching the actual configured provider so agent.New() validation passes.
	// The chain overrides provider selection at execution time.
	if opts.ExplicitProviderModel && lowerProviderName != "" && initialModel != "" {
		// An explicit `-P <provider> -m <model>` was supplied. Build a
		// single-entry chain so execution targets exactly that provider/model
		// with no profile-driven fallback. Without this, ResolveChain below
		// would replace the explicit selection with the active profile's
		// primary provider (e.g. ClaudeCode), causing execution to fail with
		// "provider not registered" even though -P/-m were valid.
		agentDef.Chain = fallback.NewChain(lowerProviderName, initialModel)
		logger.Info(context.Background(), "profile.chain_explicit_override",
			observability.F("provider", lowerProviderName),
			observability.F("model", initialModel))
	} else if activeProfile != nil {
		if chain, err := profileMgr.ResolveChain(settings.AliasMain); err == nil {
			configuredProviderName := ""
			if prov != nil {
				configuredProviderName = prov.Name()
			}
			// Only apply the profile chain when its PRIMARY provider is
			// actually registered. Otherwise the chain (e.g. a profile whose
			// main provider is a custom "OpenRouter" that failed to register)
			// overrides the working configured provider and EVERY turn fails
			// "provider X not registered". Skipping the bad chain lets the
			// agent fall back to agentDef.Provider/Model (the configured,
			// always-registered provider) — graceful degradation instead of a
			// dead daemon. A runtime setProvider/switchProfile still applies it
			// correctly (that path re-registers the provider).
			if chain != nil && !providerRegistry.IsRegistered(chain.Primary.Provider) {
				logger.Warn(context.Background(), "profile.chain_primary_unregistered_skipped",
					observability.F("profile", activeProfile.ID),
					observability.F("primary_provider", chain.Primary.Provider),
					observability.F("fallback_provider", configuredProviderName))
			} else {
				agentDef.Chain = chain
				logger.Info(context.Background(), "profile.chain_resolved",
					observability.F("profile", activeProfile.ID),
					observability.F("primary_provider", chain.Primary.Provider),
					observability.F("primary_model", chain.Primary.Model),
					observability.F("configured_provider", configuredProviderName))
			}
		}
	}

	// Create agent and reliability stack only when a provider is available.
	// When prov is nil (no credentials at startup) the SDK still initialises
	// fully — the agent will be created later via SwitchProvider / ReloadProvider
	// once the user configures credentials.
	var agt *agent.Agent
	var a2aCfg *a2a.Config
	if opts.A2AEnabled {
		a2aCfg = &a2a.Config{
			Handle:        defaultTUIA2AHandle(opts.A2AHandle, opts.RootSessionID),
			SessionID:     opts.RootSessionID,
			WorkspacePath: workspaceRoot,
			SwarmName:     a2a.DefaultSwarmName, // Use default swarm for peer discovery
			ListenAddress: strings.TrimSpace(opts.A2AListenAddress),
		}
	}
	// codeModeToggleFn is set by InstallInPlace below; nil if install failed.
	var codeModeToggleFn func(bool) error
	// Cron prompt sink — created unconditionally so SetCronPromptHandler is
	// always safe to call; only effective when the scheduler is built below.
	cronPromptSink := &tuiCronPromptSink{}
	var cronScheduler *builtin.CronScheduler
	if prov != nil {
		// Create agent (no auditor needed for TUI — defaults to noop)

		// Register cron/wait/spawn tools directly into toolRegistry so they are
		// visible to the agent in both TUI and headless (-p) mode.
		//
		// The PromptSink makes fired cron/wakeup prompts wake the MAIN chat
		// agent (delivered as chat prompts via the app's handler — see
		// SetCronPromptHandler). Without it the scheduler spawns invisible
		// background agents, so /loop, /goal and ScheduleWakeup silently did
		// nothing in the TUI.
		if sdkBgManager != nil {
			scheduler, schedErr := builtin.NewCronScheduler(builtin.CronSchedulerConfig{
				AgentFactory: agentFactory,
				BGManager:    sdkBgManager,
				Logger:       logger,
				WorkDir:      workspaceRoot,
				PromptSink:   cronPromptSink,
			})
			if schedErr != nil {
				logger.Warn(context.Background(), "cron_scheduler.create_failed", observability.F("error", schedErr.Error()))
			} else {
				scheduler.Start()
				cronScheduler = scheduler
				for _, entry := range []func() (tools.Tool, error){
					func() (tools.Tool, error) {
						return builtin.NewCronCreateTool(builtin.CronCreateConfig{Scheduler: scheduler, Logger: logger, Tracer: tracer})
					},
					func() (tools.Tool, error) {
						return builtin.NewCronListTool(builtin.CronListConfig{Scheduler: scheduler, Logger: logger, Tracer: tracer})
					},
					func() (tools.Tool, error) {
						return builtin.NewCronDeleteTool(builtin.CronDeleteConfig{Scheduler: scheduler, Logger: logger, Tracer: tracer})
					},
					func() (tools.Tool, error) {
						return builtin.NewScheduleWakeupTool(builtin.ScheduleWakeupConfig{Scheduler: scheduler, Logger: logger, Tracer: tracer})
					},
				} {
					if t, err := entry(); err == nil {
						if regErr := toolRegistry.Register(t); regErr != nil {
							logger.Warn(context.Background(), "tool.register_failed", observability.F("tool", t.Name()), observability.F("error", regErr.Error()))
						} else {
							logger.Info(context.Background(), "tool.registered", observability.F("tool", t.Name()))
						}
					}
				}
			}
			for _, entry := range []func() (tools.Tool, error){
				func() (tools.Tool, error) {
					return builtin.NewWaitForAgentTool(builtin.WaitForAgentConfig{BGManager: sdkBgManager, Logger: logger, Tracer: tracer})
				},
				func() (tools.Tool, error) {
					return builtin.NewCheckBackgroundAgentTool(builtin.CheckBackgroundAgentConfig{BGManager: sdkBgManager, Logger: logger, Tracer: tracer})
				},
				func() (tools.Tool, error) {
					return builtin.NewMultiAgentWaitTool(builtin.MultiAgentWaitConfig{BGManager: sdkBgManager, Logger: logger, Tracer: tracer})
				},
			} {
				if t, err := entry(); err == nil {
					if regErr := toolRegistry.Register(t); regErr != nil {
						logger.Warn(context.Background(), "tool.register_failed", observability.F("tool", t.Name()), observability.F("error", regErr.Error()))
					} else {
						logger.Info(context.Background(), "tool.registered", observability.F("tool", t.Name()))
					}
				}
			}
		}
		if agentFactory != nil && sdkBgManager != nil {
			if spawnBgTool, err := builtin.NewSpawnBackgroundAgentTool(builtin.SpawnBackgroundAgentConfig{
				Factory:   agentFactory,
				BGManager: sdkBgManager,
				ProviderConfig: provider.Config{
					Name:          lowerProviderName,
					APIKey:        authToken,
					Model:         initialModel,
					ContextWindow: resolvedContextWindow,
					Custom: map[string]interface{}{
						"is_oauth": isOAuth,
						"logger":   logger,
						"tracer":   tracer,
					},
				},
				ParentToolReg:         toolRegistry,
				Logger:                logger,
				Tracer:                tracer,
				CurrentProviderGetter: buildCurrentProviderGetter(opts.ProfileManager),
			}); err == nil {
				if regErr := toolRegistry.Register(spawnBgTool); regErr != nil {
					logger.Warn(context.Background(), "tool.register_failed", observability.F("tool", spawnBgTool.Name()), observability.F("error", regErr.Error()))
				} else {
					logger.Info(context.Background(), "tool.registered", observability.F("tool", spawnBgTool.Name()))
				}
			}
		}

		var agentErr error
		agentCfg := agent.Config{
			Definition:       agentDef,
			Provider:         prov,
			ProviderRegistry: providerRegistry,
			ToolRegistry:     toolRegistry,
			Logger:           logger,
			Tracer:           tracer,
			A2A:              a2aCfg,
			StoragePath:      storageDir,    // For A2A registry and persistent state
			WorkspacePath:    workspaceRoot, // Workspace root for A2A scope
		}
		// Install codemode in-place: run_code is registered into toolRegistry
		// (hidden by default). A toggle func is returned so /codemode on/off
		// flips visibility without recreating the agent.
		if prov != nil {
			cm := codemode.NewFromConfig(&codemode.Config{Enabled: true, Persist: true})
			exec := tools.NewExecutor(toolRegistry, nil)
			if toggleFn, cmErr := cm.InstallInPlace(toolRegistry, exec); cmErr != nil {
				logDebug("[SDK] Code mode InstallInPlace failed: %v — run_code unavailable", cmErr)
			} else {
				codeModeToggleFn = toggleFn
				if opts.EnableCodeMode {
					if err := toggleFn(true); err != nil {
						logDebug("[SDK] Code mode initial enable failed: %v", err)
					} else {
						logDebug("[SDK] Code mode enabled at startup — only run_code visible")
					}
				}
			}
		}
		agt, agentErr = agent.New(agentCfg)

		if agentErr != nil {
			return nil, fmt.Errorf("failed to create agent: %w", agentErr)
		}

		// If code mode was enabled at startup, the toggle ran before the agent
		// existed (above), so wire the code-mode instruction block onto the
		// freshly-created agent now. SetCodeMode handles this for runtime
		// toggles; this covers the startup case.
		if codeModeToggleFn != nil && opts.EnableCodeMode {
			agt.SetEphemeralSystemFn(func(_ []*conversation.Message) string {
				return codemode.SystemPrompt
			})
		}

		// Propagate the resolved context window into the agent.
		if resolvedContextWindow > 0 {
			agt.SetConfiguredContextWindow(resolvedContextWindow)
			logDebug("[SDK] Agent context window set from resolved config: model=%s window=%d", initialModel, resolvedContextWindow)
		}

		// Wire multi-account credential rotation from tui_accounts.json.
		if credStore, csErr := agent.NewCredentialStoreFromTuiAccounts(nil); csErr == nil && credStore.CredentialCount() > 1 {
			agt.SetCredentialStore(credStore)
			logger.Info(context.Background(), "agent.credential_store.loaded",
				observability.F("account_count", credStore.CredentialCount()))
		}

		// Configure auto-compaction if provided in options
		if opts.AutoCompactionConfig != nil {
			agt.SetAutoCompactionConfig(*opts.AutoCompactionConfig)
			logger.Info(context.Background(), "agent.auto_compaction.configured",
				observability.F("enabled", opts.AutoCompactionConfig.EnableAutoCompaction),
				observability.F("threshold_mode", opts.AutoCompactionConfig.Threshold.Mode),
				observability.F("threshold_value", opts.AutoCompactionConfig.Threshold.Value),
				observability.F("continue_if_running", opts.AutoCompactionConfig.ContinueIfRunning))
		}

		// Initialize agent
		if initErr := agt.Initialize(); initErr != nil {
			return nil, fmt.Errorf("failed to initialize agent: %w", initErr)
		}

		// When --raw is enabled, tee a [PROMPT PROVENANCE] banner to stderr
		// before each provider call. The banner makes it possible to grep the
		// raw dump and immediately see which file:line built each section of
		// the system prompt and each constructed message. Same destination as
		// the provider's RawDebugWriter so the banner appears inline with the
		// REQUEST BODY dump.
		if opts.RawDebug {
			agt.SetPromptTraceWriter(os.Stderr)
		}

	} else {
		logger.Info(context.Background(), "sdk.agent_deferred",
			observability.F("reason", "no provider available at startup; agent will be created on first provider switch"))
	}

	// Create MCP manager
	mcpMgr, err := NewMCPManager(toolRegistry, logger, tracer, workspaceRoot)
	if err != nil {
		logger.Warn(context.Background(), "failed to create MCP manager", observability.F("error", err.Error()))
		// Don't fail - MCP is optional
	} else {
		// Load and connect to MCP servers
		if err := mcpMgr.LoadAndConnect(context.Background()); err != nil {
			logger.Warn(context.Background(), "failed to load MCP servers", observability.F("error", err.Error()))
		}
	}

	// Create hooks manager (unless the caller disabled hooks entirely).
	// When NoHooks is set we skip creating AND attaching the hooks manager so
	// that no builtin hooks (task-enforcement, protected-branch, autogenskills,
	// plan-mode, task-nudge) are ever wired into the agent. Downstream code
	// that uses hooksMgr must tolerate a nil manager (guarded below).
	var hooksMgr *HooksManager
	if !opts.NoHooks {
		hooksMgr = NewHooksManager(logger, tracer, workspaceRoot)

		// Link hooks manager to agent (CRITICAL: enables hooks during tool execution)
		if agt != nil {
			agt.SetHooksManager(hooksMgr)
		}
		// Task nudge is now a built-in PostToolUse hook wired inside HooksManager.
		// It fires every ii.TaskNudgeInterval tool calls, surfaces as a BlockHook
		// in the TUI, and injects context ephemerally — never as a stored message.
	}

	// Create skills manager
	skillsDir := DefaultSkillsDir()
	skillsMgr := NewSkillsManager(skillsDir)
	if !opts.DisableSkills {
		if err := skillsMgr.Initialize(context.Background()); err != nil {
			logger.Warn(context.Background(), "failed to initialize skills manager",
				observability.F("error", err.Error()))
			// Don't fail - skills are optional
		}
		// Also discover skills from the project's .claude/skills/ directory
		// (mirrors Claude Code's convention for project-scoped skills).
		if workspaceRoot != "" {
			if err := skillsMgr.AddProjectSearchPaths(workspaceRoot); err != nil {
				logger.Warn(context.Background(), "failed to discover project skills",
					observability.F("error", err.Error()))
			}
		}
	}

	// Register the Skill tool — allows the LLM to invoke skills mid-conversation.
	// This is the keystone feature that bridges the gap between Swarm's passive-context
	// approach and Claude Code's active-tool-invocation architecture.
	// Only register when the skills manager is initialized and the tool registry exists.
	if skillsMgr.IsInitialized() && toolRegistry != nil {
		skillTool, skillErr := skilltools.NewSkillTool(skilltools.SkillToolConfig{
			Registry: skillsMgr.GetLoader().Registry,
			Logger:   logger,
			Tracer:   tracer,
			SessionIDGetter: func() string {
				// Session ID is not easily accessible here; use empty string
				// as a safe default. Variable substitution for ${SWARM_SESSION_ID}
				// will resolve to "" which is acceptable for Phase 1.
				return ""
			},
		})
		if skillErr != nil {
			logger.Warn(context.Background(), "skill_tool.create_failed",
				observability.F("error", skillErr.Error()))
		} else if regErr := toolRegistry.Register(skillTool); regErr != nil {
			logger.Warn(context.Background(), "skill_tool.register_failed",
				observability.F("error", regErr.Error()))
		} else {
			logger.Info(context.Background(), "tool.registered",
				observability.F("tool", "Skill"))
			logDebug("AGENT_TOOLS: ✓ Registered Skill tool")
		}

		// ── Autogenskills lifecycle ────────────────────────────────────────────
		// Wire the autogenskills service (budget enforcement + skill creation).
		autogenCfg := autogenskills.Config{
			Mode:       autogenskills.ModeAuto,
			AutogenDir: filepath.Join(getStorageRoot(), ".swarm", "skills", "autogen"),
			Trigger: autogenskills.TriggerConfig{
				ToolCallBudget:           5,
				WorkingBudget:            90,
				MaxNudgeIgnores:          3,
				NudgeInterval:            5,
				ErrorResolutionThreshold: 1,
			},
		}
		// Overlay user overrides from <autogen>/config.yaml — this is the only
		// way to enable curator consolidation (curator.consolidate: true),
		// mirroring Hermes' curator.* config block. Without it the hardcoded
		// defaults above were final and the consolidation pass was unreachable.
		if overridden, cfgPath := autogenskills.ApplyUserConfig(autogenCfg); cfgPath != "" {
			autogenCfg = overridden
			logger.Info(context.Background(), "autogenskills.user_config_applied",
				observability.F("path", cfgPath),
				observability.F("consolidate", autogenCfg.Curator.Consolidate))
		}
		// Autogenskills budget enforcement is hook-based, so it only wires up
		// when a hooks manager exists (i.e. NoHooks is false). When hooks are
		// disabled we skip the whole autogenskills lifecycle (budget hooks,
		// SkillManage tool, curator, prompt injection) rather than nil-panic.
		autogenValidErr := autogenCfg.Validate()
		if hooksMgr != nil && autogenValidErr == nil {
			skillRegistry := skillsMgr.GetLoader().Registry
			autogenSvc, autogenErr := autogenskills.NewService(autogenCfg, skillRegistry, nil)
			if autogenErr != nil {
				logger.Warn(context.Background(), "autogenskills.create_failed",
					observability.F("error", autogenErr.Error()))
			} else {
				// Wire hooks into TUI hooks manager
				if err := hooksMgr.EnableAutogenSkills(autogenSvc, skillRegistry); err != nil {
					logger.Warn(context.Background(), "autogenskills.hook_register_failed",
						observability.F("error", err.Error()))
				}
				// Register SkillManage tool for LLM-driven skill creation
				if manageTool, toolErr := autogenskills.NewSkillManageTool(autogenSvc); toolErr == nil {
					if regErr := toolRegistry.Register(manageTool); regErr != nil {
						logger.Warn(context.Background(), "autogenskills.tool_register_failed",
							observability.F("error", regErr.Error()))
					} else {
						logger.Info(context.Background(), "tool.registered",
							observability.F("tool", "SkillManage"))
						logDebug("AGENT_TOOLS: ✓ Registered SkillManage tool (autogenskills)")
					}
				}

				// Inject skill guidance and index into the system prompt so the
				// agent knows about the SkillManage tool and existing skills.
				var promptAdditions []string
				if guidance := hooksMgr.GetSkillGuidance(); guidance != "" {
					promptAdditions = append(promptAdditions, guidance)
				}
				if index := hooksMgr.GetSkillIndex(); index != "" {
					promptAdditions = append(promptAdditions, index)
				}
				if len(promptAdditions) > 0 && agt != nil {
					updatedPrompt := agt.SystemPrompt() + "\n\n" + strings.Join(promptAdditions, "\n\n")
					agt.SetSystemPrompt(updatedPrompt)
					logger.Info(context.Background(), "autogenskills.prompt_injected",
						observability.F("guidance_len", len(hooksMgr.GetSkillGuidance())),
						observability.F("index_len", len(hooksMgr.GetSkillIndex())))
				}

				// ── Autogenskills curator (background skill maintenance) ──────────
				// Create a rule-based Curator with state persistence.
				curatorStatePath := filepath.Join(getStorageRoot(), ".swarm", "skills", "autogen", ".curator_state")
				curator := autogenskills.NewCurator(autogenCfg.Curator, nil, curatorStatePath)
				autogenSvc.SetCurator(curator)

				// Create LLM-powered CuratorAgent for semantic skill maintenance.
				// Runs during idle periods (inactivity-triggered).
				if agentFactory != nil {
					curatorProvCfg := provider.Config{
						Name:   lowerProviderName,
						APIKey: authToken,
						Model:  initialModel,
						Custom: map[string]interface{}{
							"is_oauth": isOAuth,
							"logger":   logger,
						},
					}
					curatorAgent, curatorErr := autogenskills.NewCuratorAgent(agentFactory, curatorProvCfg, autogenSvc, logger)
					if curatorErr != nil {
						logger.Warn(context.Background(), "autogenskills.curator_agent_failed",
							observability.F("error", curatorErr.Error()))
					} else {
						hooksMgr.SetCuratorAgent(curatorAgent)
						logDebug("AUTOGEN: \u2713 curator agent ready (provider=%s model=%s)", lowerProviderName, initialModel)
						logger.Info(context.Background(), "autogenskills.curator_agent_ready",
							observability.F("provider", lowerProviderName),
							observability.F("model", initialModel))
					}
				}
			}
		} else if autogenValidErr != nil {
			logger.Warn(context.Background(), "autogenskills.config_invalid",
				observability.F("error", autogenValidErr.Error()))
		}
	}

	// Create plugins manager (Claude Code-compatible plugins)
	pluginsMgr := NewPluginsManager(skillsMgr.GetLoader())
	if !opts.DisableSkills {
		if err := pluginsMgr.Initialize(context.Background()); err != nil {
			logger.Warn(context.Background(), "failed to initialize plugins manager",
				observability.F("error", err.Error()))
			// Don't fail - plugins are optional
		}
	}

	// Register MCP servers from plugins with the MCP manager
	if pluginsMgr.IsInitialized() && mcpMgr != nil {
		mcpConfigs := pluginsMgr.GetMCPServerConfigs()
		for _, config := range mcpConfigs {
			if err := mcpMgr.AddServer(context.Background(), config); err != nil {
				logger.Warn(context.Background(), "failed to add plugin MCP server",
					observability.F("server", config.Name),
					observability.F("error", err.Error()))
			} else {
				logger.Info(context.Background(), "plugin.mcp_server.added",
					observability.F("server", config.Name),
					observability.F("type", string(config.Type)))
			}
		}
	}

	// Log hooks available from plugins
	// Note: Full hooks integration to be done in future - hooks from plugins
	// need to be converted to the SDK hooks format
	if pluginsMgr.IsInitialized() {
		pluginHooks := pluginsMgr.GetAllHooks()
		hooksCount := len(pluginHooks)
		if hooksCount > 0 {
			logger.Info(context.Background(), "plugin.hooks.available",
				observability.F("count", hooksCount))
		}
	}

	// Initialize real-time context orchestrator and wrap provider
	contextLoader := chatcontext.NewFileLoaderWithRoot(workspaceRoot)
	if opts.DisableGlobalMemory {
		contextLoader.ExcludeSources(chatcontext.SourceIDGlobalClaudeMd, chatcontext.SourceIDGlobalSwarmMd)
	}
	if opts.DisableProjectMemory {
		contextLoader.ExcludeSources(
			chatcontext.SourceIDProjectClaudeMd,
			chatcontext.SourceIDProjectSwarmMd,
			chatcontext.SourceIDAgentsMd,
			chatcontext.SourceIDIndexMd,
		)
	}
	if opts.DisableSkills {
		contextLoader.ExcludeSources(chatcontext.SourceIDSkills)
	}
	contextConfig := contextLoader.GetConfig()
	contextOrchestrator := chatcontext.NewContextOrchestrator(contextConfig, workspaceRoot, contextLoader, mcpMgr, logger)
	contextCapture := newProviderContextCaptureStore()
	generationSettings := newModelGenerationSettings(initialModel)

	// Seed the orchestrator with runtime agent metadata so every ephemeral
	// context block carries session, model, provider, profile, and surface info.
	activeProfileName := ""
	if activeProfile != nil {
		activeProfileName = activeProfile.Name
	}
	contextOrchestrator.SetAgentMetadata(chatcontext.AgentMetadata{
		ModelName:    initialModel,
		ProviderName: lowerProviderName,
		ProfileName:  activeProfileName,
		ClientType:   sdkclient.ClientTypeTUI,
	})

	// Wire the nested INDEX.md discovery hook into the context orchestrator.
	// When the agent reads a file in a new directory, the hook registers
	// that directory so the orchestrator can discover INDEX.md files there
	// on the next context refresh (mirrors Claude Code's nestedMemoryAttachmentTriggers).
	// Skipped when hooks or project memory are disabled.
	if hooksMgr != nil && !opts.DisableProjectMemory {
		if err := hooksMgr.EnableNestedIndexDiscovery(contextOrchestrator); err != nil {
			logger.Warn(context.Background(), "sdk.nested_index_discovery_failed",
				observability.F("error", err.Error()))
		}
	}

	// Build the reliability provider stack only when a base provider exists.
	if prov != nil {
		reliableProvider, stackErr := buildRuntimeProviderStack(
			prov,
			providerName,
			initialModel,
			providerBuildDeps{
				Logger:           logger,
				Tracer:           tracer,
				RawDebug:         opts.RawDebug,
				EventCallback:    opts.ProviderEventCallback,
				EndpointOverride: opts.EndpointOverride,
			},
			loadSwarmOSConfig,
			defaultSDKProviderBuilder,
			func(p provider.Provider) provider.Provider {
				contextProvider := newContextInjectingProviderWithCapture(p, contextOrchestrator, contextCapture)
				return newGenerationSettingsProvider(contextProvider, generationSettings)
			},
			nil,
		)
		if stackErr != nil {
			return nil, fmt.Errorf("failed to build reliability provider stack: %w", stackErr)
		}
		prov = reliableProvider
		registryProviderSlot.SetRuntime(prov, providerName, initialModel, resolvedContextWindow)
		if agt != nil {
			agt.SetProvider(prov)
		}
	}

	logger.Info(context.Background(), "sdk.initialized",
		observability.F("provider", lowerProviderName),
		observability.F("tool_count", len(toolRegistry.List())),
		observability.F("agent_initialized", agt != nil),
		observability.F("hooks_enabled", true),
		observability.F("skills_enabled", skillsMgr.IsInitialized()),
		observability.F("plugins_enabled", pluginsMgr.IsInitialized()),
	)

	var defaultReasoningEffort string = provider.ReasoningEffortAuto
	if configMgr, cfgErr := commands.NewConfigManager(); cfgErr == nil {
		if cfg, loadErr := configMgr.LoadConfig(); loadErr == nil && cfg != nil {
			defaultReasoningEffort = cfg.GetReasoningEffortForModel(providerName, initialModel)
		}
	}

	sdk := &SDKIntegration{
		provider:                prov,
		providerName:            lowerProviderName,
		logger:                  logger,
		tracer:                  tracer,
		lookupStore:             lookupStore,
		traceSink:               traceSink,
		ctx:                     context.Background(),
		debugScreen:             nil, // Set later by app
		toolRegistry:            toolRegistry,
		codeModeToggle:          codeModeToggleFn, // nil if InstallInPlace failed
		agent:                   agt,
		currentModel:            initialModel,
		authToken:               authToken,
		mcpManager:              mcpMgr,
		hooksManager:            hooksMgr,
		lifecycleHookExecutor:   nil,                   // Custom lifecycle hooks loaded via SetLifecycleHooksConfig
		skillsManager:           skillsMgr,             // Skills system manager
		pluginsManager:          pluginsMgr,            // Plugins system manager (Claude Code-compatible)
		sdkBgManager:            sdkBgManager,          // SDK background agent manager
		bgProcessManager:        opts.BGProcessManager, // Background bash process manager
		agentFactory:            agentFactory,          // Agent factory for tools
		providerRegistry:        providerRegistry,      // Shared registry for executeWithChain and sub-agents
		isOAuth:                 isOAuth,
		workspaceRoot:           workspaceRoot,
		projectRoot:             projectRoot,
		permissionChecker:       permChecker,
		permissionConfig:        permConfig,
		maxTokens:               maxTokens, // From settings (default 32k)
		rawDebug:                opts.RawDebug,
		providerBuilder:         defaultSDKProviderBuilder,
		reliabilityConfigLoader: loadSwarmOSConfig,
		endpointOverride:        opts.EndpointOverride,
		registryProviderSlot:    registryProviderSlot,
		contextOrchestrator:     contextOrchestrator,
		contextCapture:          contextCapture,
		disableGlobalMemory:     opts.DisableGlobalMemory,
		disableProjectMemory:    opts.DisableProjectMemory,
		disableSkills:           opts.DisableSkills,

		// Agent profile system
		profileManager: profileMgr,
		activeProfile:  activeProfile,

		// Extended thinking defaults. Capable providers should reason unless the
		// user or a per-model setting explicitly disables it.
		thinkingEnabled:        true,
		thinkingBudget:         10000, // 10000 tokens default
		thinkingDefaultEnabled: true,
		thinkingDefaultBudget:  10000,
		reasoningEffort:        provider.NormalizeReasoningEffortSetting(defaultReasoningEffort),
		generationSettings:     generationSettings,

		// Prompt caching defaults (Context7-compliant)
		cachingEnabled:     true, // Enabled by default for cost savings
		cacheTTL:           "1h", // 1-hour default (stable content, better cost savings)
		lastCacheMetrics:   make(map[string]int),
		cacheHitRate:       0.0,
		totalCacheCreation: 0,
		totalCacheRead:     0,
		cacheManager:       NewCacheManager(), // Smart invalidation

		// Operating mode defaults
		operatingMode: "off", // Default to OFF mode (no filtering)

		// Debug mode
		debugMode: opts.DebugMode,

		// Provider event callback
		providerEventCallback: opts.ProviderEventCallback,

		// Vault provider for credential management
		vaultProvider: opts.VaultProvider,
		vaultUnlocker: opts.VaultUnlocker,

		a2aEnabled:       opts.A2AEnabled,
		a2aHandle:        defaultTUIA2AHandle(opts.A2AHandle, opts.RootSessionID),
		a2aListenAddress: strings.TrimSpace(opts.A2AListenAddress),

		// WorkspaceHub for always-on local peer transport
		hub: opts.Hub,

		// Cron scheduler prompt sink (fired prompts → main chat agent)
		cronPromptSink: cronPromptSink,
		cronScheduler:  cronScheduler,
	}

	// Derive the diffusion flag for the initial provider/model selection.
	sdk.refreshDiffusionFlag()

	if agt != nil {
		// Propagate headless mode to the agent so it never blocks on
		// interactive prompts (e.g. the fallback-exhausted profile picker).
		// installPermanentAgentCallbacks below always installs an intermediate
		// callback, so without this flag the agent's onExhausted handler would
		// deadlock waiting for a UI decision that never arrives in -p mode.
		if opts.HeadlessMode {
			agt.SetHeadlessMode(true)
		}
		sdk.installPermanentAgentCallbacks(agt)
		sdk.installTUIA2ARequestHandler(agt)
		sdk.reloadAutoCompactionConfig()
	}

	// Emit session start event to fire hooks (plan mode guidance, etc.)
	if sdk.hooksManager != nil && opts.RootSessionID != "" {
		sessionStartResults := sdk.hooksManager.EmitSessionStart(context.Background(), opts.RootSessionID)
		if len(sessionStartResults) > 0 {
			logger.Info(context.Background(), "session.start_hooks_fired",
				observability.F("count", len(sessionStartResults)))
		}
	}

	// Wire a real LLM-backed evaluator into the GoalHook so /goal can actually
	// determine when the condition is met instead of using the placeholder.
	// The provider/model are resolved at CALL time — capturing them here froze
	// the startup pair, so switching models mid-session sent evaluations to a
	// stale (possibly dead) provider.
	if sdk.hooksManager != nil {
		sdk.hooksManager.EnableGoalLLMEvaluator(func(ctx context.Context, condition, transcript string) (hooksbuiltin.GoalResult, error) {
			prov := sdk.provider
			model := sdk.currentModel
			if prov == nil {
				return hooksbuiltin.GoalResult{Ok: false, Reason: "evaluator unavailable: no provider"}, nil
			}
			return evaluateGoalWithLLM(ctx, prov, model, condition, transcript)
		})
	}

	// Wire up the swarm tool PeerCreator and Communicator after agent is initialized
	if sdk.toolRegistry != nil {
		if tool, err := sdk.toolRegistry.Get("swarm"); err == nil {
			if swarmTool, ok := tool.(*swarm.SwarmTool); ok {
				swarmTool.SetCommunicator(sdk)
			}
		}
	}

	// ── Phase 1-4: construct the canonical *sdkclient.Client ─────────────
	// Share the same agent, provider, and tool registry that SDKIntegration
	// already built.  This means sdkClient.Execute() operates on the exact
	// same agent instance — SetModel, SetSystemPrompt, and all pre-execution
	// config applied to sdk.agent are visible through sdkClient without any
	// sync layer.
	//
	// WithAgentInstance:    Reuse sdk.agent directly; skip agent construction.
	// WithProviderInstance: SDKIntegration's provider (reliability wrapper,
	//   OAuth refresh, raw-event callbacks) flows through untouched (only
	//   used when agentInstance is nil, kept for metadata).
	// WithToolRegistry:     Same registry kept for future Reconfigure calls.
	// WithoutAutoConfig:    Credentials already resolved; skip auto-load.
	//
	// Use original provider name for credential resolution to ensure
	// OpenAI-compatible providers (fireworks, groq, etc.) look up their
	// specific API keys (FIREWORKS_API_KEY) rather than OPENAI_API_KEY.
	//
	// CONTRACT ENFORCEMENT: Validate provider is known, OR exists in
	// providers.json with a valid api_type. Custom provider names from
	// providers.json are accepted as long as they have api_type set.
	_, err = sdkclient.ParseProvider(providerName)
	if err != nil {
		// Provider not in SDK's known providers - check if it's a custom
		// provider defined in providers.json with a valid api_type
		if cfg, cfgErr := getProviderConfigFromFile(providerName); cfgErr == nil && cfg.APIType != "" {
			// Custom provider from providers.json is valid - proceed
			// The SDK will use the api_type from providers.json for routing
		} else {
			return nil, fmt.Errorf("NewSDKIntegration: %w", err)
		}
	}
	clientOpts := []sdkclient.Option{
		sdkclient.WithProviderString(providerName, initialModel),
		sdkclient.WithProfileName(activeProfileName),
		sdkclient.WithWorkspace(workspaceRoot),
		sdkclient.WithLogger(logger),
		sdkclient.WithTracer(tracer),
		sdkclient.WithStorageDir(storageDir),
		sdkclient.WithMaxTokens(maxTokens),
		sdkclient.WithoutAutoConfig(),
		sdkclient.WithToolRegistry(sdk.toolRegistry),
		sdkclient.WithClientType(sdkclient.ClientTypeTUI),
	}
	if prov != nil {
		clientOpts = append(clientOpts, sdkclient.WithProviderInstance(prov))
	}
	if agt != nil {
		clientOpts = append(clientOpts, sdkclient.WithAgentInstance(agt))
	}
	// The SDK client is mandatory: it owns execution, conversation, and
	// provider management. A construction failure here surfaces to the caller
	// (app_init shows an "SDK unavailable" error), rather than silently
	// degrading into a parallel legacy stack. The only realistic failure is
	// storage-dir init — a TUI running without persistence is worse than a
	// clear error.
	sdkC, err := sdkclient.New(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("NewSDKIntegration: SDK client is required: %w", err)
	}
	sdk.sdkClient = sdkC

	// Populate `transcript_path` for external shell hooks. This is part of the
	// Claude Code hook contract, not a SwarmOS extension: a portable hook reads
	// it to recover the assistant turn -- prose and reasoning -- that a tool
	// event payload does not carry. Installed here because this is the first
	// point at which a storage-backed path resolver exists.
	hooks.SetTranscriptPathResolver(sdk.GetConversationPath)

	// Stamp the product version onto `annoyed` reports. Registered here rather
	// than imported by the SDK because swarm-tui is downstream of it; see
	// version.SetProductVersionResolver. Installed immediately before the
	// annoyed tool is registered below, so no report can be published without
	// the build identity a triager needs to tell a live defect apart from one
	// filed against an older binary.
	sdkversion.SetProductVersionResolver(func() string { return tuiversion.Version })

	// The TUI supplies a curated pre-built registry, so SDK client construction
	// intentionally skips its default builtin registration. Register annoyed
	// here, after the canonical client exists, so feedback evidence can load the
	// exact active conversation without enabling a duplicate SDK-level nudge.
	if err := sdk.registerAnnoyedTool(sdkC.LoadConversation); err != nil {
		_ = sdkC.Close()
		return nil, fmt.Errorf("NewSDKIntegration: register annoyed: %w", err)
	}

	historySearchTool := historytools.NewSearchToolWithOptions(workspaceRoot, func(ctx context.Context, request historytools.SearchRequest) ([]historytools.Summary, error) {
		return indexedHistorySearch(ctx, sdkC, request)
	}, historySegmentIndex{})
	historyGetTool := historytools.NewGetTool(workspaceRoot, sdkC.LoadConversation)
	for _, historyTool := range []tools.Tool{historySearchTool, historyGetTool} {
		if registerErr := sdk.toolRegistry.Register(historyTool); registerErr != nil {
			return nil, fmt.Errorf("NewSDKIntegration: register %s: %w", historyTool.Name(), registerErr)
		}
	}
	// WithAgentInstance overwrites sdk.agent's intermediate callback with
	// sdkClient.fanout, replacing the permanent TUI routing callback set by
	// installPermanentAgentCallbacks(). Re-establish routing via SubscribeUpdates
	// so streaming updates still reach sdk.localUpdateChan and the TUI render loop.
	sdk.sdkClientUnsub = sdkC.SubscribeUpdates(sdk.handlePermanentIntermediateUpdate)

	// Register the prompt-trace seed callback now that the agent exists.
	// When --raw is enabled, every Execute call replays the SDK's recorded
	// startup-time prompt contributions (skills injection, dream contract
	// injection, headless override, etc.) into the per-request collector
	// so they appear in the [PROMPT PROVENANCE] banner.
	if agt != nil {
		agt.SetPromptTraceSeedFn(sdk.seedPromptTrace)
	}

	return sdk, nil
}

func (sdk *SDKIntegration) registerAnnoyedTool(loadConversation builtin.ConversationLoader) error {
	if sdk == nil || sdk.toolRegistry == nil {
		return fmt.Errorf("tool registry is unavailable")
	}
	if loadConversation == nil {
		return fmt.Errorf("conversation loader is unavailable")
	}
	if !sdk.toolRegistry.IsRegistered("annoyed") {
		if err := sdk.toolRegistry.Register(builtin.NewAnnoyedToolWithConfig(builtin.AnnoyedConfig{
			LoadConversation: loadConversation,
		})); err != nil {
			return err
		}
	}
	if sdk.hooksManager != nil {
		if err := sdk.hooksManager.EnableAnnoyanceNudge(); err != nil {
			return err
		}
	}
	return nil
}

func newHarnessSDKIntegration(
	opts SDKIntegrationOptions,
	logger observability.Logger,
	tracer observability.Tracer,
	lookupStore observability.LookupStore,
) (*SDKIntegration, error) {
	clientOpts := []sdkclient.Option{
		sdkclient.WithHarnessPlan(opts.HarnessPlan),
		sdkclient.WithLogger(logger),
		sdkclient.WithTracer(tracer),
		sdkclient.WithClientType(sdkclient.ClientTypeTUI),
		sdkclient.WithHarnessBindings(BuildTUICompatHarnessBindings(opts)),
	}
	if opts.HarnessAllowYolo {
		clientOpts = append(clientOpts, sdkclient.WithHarnessAllowYolo())
	}
	sdkC, err := sdkclient.New(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("NewSDKIntegration: harness SDK client is required: %w", err)
	}

	agt := sdkC.Agent()
	toolRegistry := sdkC.AgentToolRegistry()
	sdk := &SDKIntegration{
		sdkClient:             sdkC,
		providerName:          opts.HarnessPlan.ProviderID(),
		logger:                logger,
		tracer:                tracer,
		lookupStore:           lookupStore,
		ctx:                   context.Background(),
		toolRegistry:          toolRegistry,
		agent:                 agt,
		currentModel:          opts.HarnessPlan.Model(),
		workspaceRoot:         opts.HarnessPlan.Workspace(),
		projectRoot:           opts.ProjectRoot,
		approvalBroker:        opts.ApprovalBroker,
		questionBroker:        opts.QuestionBroker,
		planBroker:            opts.PlanBroker,
		maxTokens:             opts.HarnessPlan.Limits().MaxOutputTokens,
		debugMode:             opts.DebugMode,
		providerEventCallback: opts.ProviderEventCallback,
		a2aEnabled:            opts.A2AEnabled,
		a2aHandle:             defaultTUIA2AHandle(opts.A2AHandle, opts.RootSessionID),
		a2aListenAddress:      strings.TrimSpace(opts.A2AListenAddress),
		hub:                   opts.Hub,
		cacheManager:          NewCacheManager(),
		lastCacheMetrics:      make(map[string]int),
		operatingMode:         "off",
	}

	if opts.HarnessPlan.Permissions().ApprovalMode == "interactive" && opts.ApprovalBroker != nil {
		permConfig := tools.DefaultPermissionConfig()
		if opts.PermissionConfig != nil {
			permConfig = opts.PermissionConfig.Config()
		}
		permChecker := tools.NewInteractivePermissionChecker(permConfig, opts.ApprovalBroker)
		permChecker.SetWorkspaceRoot(opts.HarnessPlan.Workspace())
		permissionRegistry, ok := toolRegistry.(interface {
			SetPermissionChecker(tools.PermissionChecker)
		})
		if !ok {
			_ = sdkC.Close()
			return nil, fmt.Errorf("NewSDKIntegration: harness tool registry cannot attach interactive approval broker")
		}
		permissionRegistry.SetPermissionChecker(permChecker)
		sdk.permissionChecker = permChecker
		sdk.permissionConfig = opts.PermissionConfig
	}

	sdk.sdkClientUnsub = sdkC.SubscribeUpdates(sdk.handlePermanentIntermediateUpdate)
	return sdk, nil
}

func (sdk *SDKIntegration) harnessGoverned() bool {
	if sdk == nil || sdk.sdkClient == nil {
		return false
	}
	return sdk.sdkClient.HarnessSnapshot().Harness
}

// SetDebugScreen sets the debug screen for request capturing
// IsOAuth returns true if this integration is using OAuth authentication

// GetSteeringAgent returns the steering agent, or nil if not configured.
func (sdk *SDKIntegration) GetSteeringAgent() *agent.SteeringAgent {
	return sdk.steeringAgent
}

// SetSteeringAgent sets the steering agent for findings analysis.
func (sdk *SDKIntegration) SetSteeringAgent(sa *agent.SteeringAgent) {
	sdk.steeringAgent = sa
}

// BuildSteeringAgent creates a SteeringAgent from the given provider name and model.
// It loads the user's persisted steering config from config.json and converts it
// to the SDK's agent.SteeringConfig, so runtime settings (intervention points,
// max retries, auto-approve, logging) honour what the user configured in Settings.
// Returns nil without error if providerName or model are empty.
func (sdk *SDKIntegration) BuildSteeringAgent(providerName, model string) error {
	if providerName == "" || model == "" {
		return nil
	}

	// Build a provider for the steering agent
	prov, _, _, _, err := buildProvider(providerName, model, sdk.logger, sdk.tracer, false)
	if err != nil {
		return fmt.Errorf("build steering provider %s: %w", providerName, err)
	}

	// Pin to the specific model
	pinnedProv := newModelPinnedProvider(prov, model)

	// Create a lightweight agent for the evaluator
	evaluatorAgent, err := agent.NewQuick(pinnedProv, model)
	if err != nil {
		return fmt.Errorf("create steering evaluator agent: %w", err)
	}

	// Load the user's persisted steering config from config.json.
	// If unavailable, fall back to defaults so we never fail to build the agent
	// just because the config file is missing.
	agentCfg := agent.DefaultSteeringConfig()
	cm, cmErr := commands.NewConfigManager()
	if cmErr == nil {
		if cfg, loadErr := cm.LoadConfig(); loadErr == nil && cfg != nil && cfg.SteeringConfig != nil {
			sc := cfg.SteeringConfig
			if len(sc.InterventionPoints) > 0 {
				agentCfg.InterventionPoints = make([]agent.InterventionPoint, 0, len(sc.InterventionPoints))
				for _, ip := range sc.InterventionPoints {
					agentCfg.InterventionPoints = append(agentCfg.InterventionPoints, agent.InterventionPoint(ip))
				}
			}
			if sc.MaxRetries > 0 {
				agentCfg.MaxRetries = sc.MaxRetries
			}
			agentCfg.AutoApproveSimple = sc.AutoApproveSimple
			agentCfg.LogInterventions = sc.LogDecisions
			sdk.logger.Info(sdk.ctx, "steering.config_loaded_from_disk",
				observability.F("intervention_points", len(agentCfg.InterventionPoints)),
				observability.F("max_retries", agentCfg.MaxRetries),
				observability.F("auto_approve_simple", agentCfg.AutoApproveSimple),
			)
		}
	}

	// Create the SteeringAgent with the user's config (or defaults)
	sa, err := agent.NewSteeringAgent(evaluatorAgent, agentCfg, sdk.logger, sdk.tracer)
	if err != nil {
		return fmt.Errorf("create steering agent: %w", err)
	}

	sdk.steeringAgent = sa
	return nil
}

// SetVaultProvider sets the vault provider and registers vault tools.
// This is called after vault unlock to make vault tools available to the agent.
// If a vault provider was already set, the tools are re-registered with the new provider.
func (sdk *SDKIntegration) SetVaultProvider(vp vault.VaultProvider) {
	sdk.vaultProvider = vp

	// Set the default provider for vault tools
	vault.SetDefaultVaultProvider(vp)
	if sdk.harnessGoverned() {
		return
	}

	// Register or re-register vault tools
	vaultTools := builtin.VaultToolsWithUnlocker(sdk.vaultUnlocker)
	for _, tool := range vaultTools {
		// Check if already registered
		if sdk.toolRegistry.IsRegistered(tool.Name()) {
			// Unregister first to replace with new instance
			_ = sdk.toolRegistry.Unregister(tool.Name())
		}
		if err := sdk.toolRegistry.Register(tool); err != nil {
			sdk.logger.Warn(context.Background(), "failed to register vault tool",
				observability.F("tool", tool.Name()),
				observability.F("error", err.Error()))
		} else {
			sdk.logger.Info(context.Background(), "vault.tool.registered",
				observability.F("tool", tool.Name()))
		}
	}
}

// GetVaultProvider returns the current vault provider, or nil if not set.
func (sdk *SDKIntegration) GetVaultProvider() vault.VaultProvider {
	return sdk.vaultProvider
}

// IsA2AEnabled returns true if A2A is enabled for this session
func (sdk *SDKIntegration) IsA2AEnabled() bool {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()
	return sdk.a2aEnabled && sdk.activeAgent() != nil && sdk.activeAgent().A2ARuntime() != nil
}

// GetA2AHandle returns the A2A handle for this agent
func (sdk *SDKIntegration) GetA2AHandle() string {
	if sdk == nil {
		return ""
	}
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()
	return sdk.a2aHandle
}

// GetLocalHandle returns the local agent's A2A handle (alias for GetA2AHandle).
// Implements the swarm.PeerCreator interface.
func (sdk *SDKIntegration) GetLocalHandle() string {
	return sdk.GetA2AHandle()
}

// GetA2AAddress returns the A2A listen address
func (sdk *SDKIntegration) GetA2AAddress() string {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()
	return sdk.a2aListenAddress
}

// ListSwarmPeers returns the list of connected A2A peers (TUI variant)
// This satisfies the commands.SwarmProvider interface.
// When a WorkspaceHub is configured it is the authoritative liveness source;
// otherwise falls back to the SQLite-backed runtime peer list.
func (sdk *SDKIntegration) ListPeers() []a2a.SwarmPeerInfo {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	// Hub path — live connections only, no SQLite
	if sdk.hub != nil && sdk.hub.IsConnected() {
		handles := sdk.hub.GetPeers()
		peers := make([]a2a.SwarmPeerInfo, 0, len(handles))
		for _, h := range handles {
			peerInfo := a2a.SwarmPeerInfo{Handle: h}
			if status, ok := sdk.hub.GetPeerStatus(h); ok {
				peerInfo.Status = a2a.SwarmStatus(status.Kind)
				peerInfo.CurrentTask = status.Task
			}
			peers = append(peers, peerInfo)
		}
		return peers
	}

	// SQLite fallback path
	if sdk.activeAgent() != nil && sdk.activeAgent().A2ARuntime() != nil {
		ctx := context.Background()
		result, err := sdk.activeAgent().A2ARuntime().ListPeers(ctx)
		if err != nil {
			return nil
		}
		if result != nil {
			return result.Peers
		}
	}
	return nil
}

// GetSwarmStatus returns the current swarm status
func (sdk *SDKIntegration) GetSwarmStatus() a2a.AgentSwarmStatus {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()
	if sdk.activeAgent() != nil && sdk.activeAgent().A2ARuntime() != nil {
		return sdk.activeAgent().A2ARuntime().GetSwarmStatus()
	}
	return a2a.AgentSwarmStatus{}
}

// PeerCreator implements the swarm.PeerCreator interface
func (sdk *SDKIntegration) CreatePeer(ctx context.Context, config swarm.PeerConfig) (*swarm.PeerInfo, error) {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()

	// Debug logging for peer creation (disabled in production).
	// To enable, set SWARM_DEBUG_PEER_LOG=/tmp/peer-debug.log
	log := func(format string, args ...interface{}) {
		if debugFile := os.Getenv("SWARM_DEBUG_PEER_LOG"); debugFile != "" {
			msg := fmt.Sprintf("[PEER-CREATE %s] "+format+"\n", append([]interface{}{time.Now().Format("15:04:05.000")}, args...)...)
			f, _ := os.OpenFile(debugFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if f != nil {
				f.WriteString(msg)
				f.Close()
			}
		}
	}

	log("=== CreatePeer START === handle=%s", config.Handle)
	log("Config: Name=%s Desc=%s Tools=%v", config.Name, config.Description, config.Tools)

	if sdk.activeAgent() == nil {
		log("ERROR: agent not initialized")
		return nil, fmt.Errorf("agent not initialized")
	}
	log("Agent is initialized")

	if sdk.activeAgent().A2ARuntime() == nil {
		log("ERROR: A2A is not enabled")
		return nil, fmt.Errorf("A2A is not enabled")
	}
	log("A2A runtime is enabled")

	log("A2A runtime check passed")

	// Get workspace path for the peer storage
	workspacePath := sdk.workspaceRoot
	if workspacePath == "" {
		workspacePath, _ = os.Getwd()
	}
	log("Workspace path: %s", workspacePath)

	// Create unique storage directory for this peer
	peerStoragePath := filepath.Join(workspacePath, ".swarm", "peers", config.Handle)
	log("Peer storage path: %s", peerStoragePath)

	if err := os.MkdirAll(peerStoragePath, 0755); err != nil {
		log("ERROR: failed to create peer storage directory: %v", err)
		return nil, fmt.Errorf("failed to create peer storage directory: %w", err)
	}
	log("Created peer storage directory")

	// Build headless peer command
	// Find the headless binary - assume it's in the same directory as the TUI
	headlessBinary := "headless" // Default to just 'headless' (assumes it's in PATH)
	log("Default headless binary: %s", headlessBinary)

	// Try to find headless in common locations relative to the executable
	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		possiblePaths := []string{
			filepath.Join(exeDir, "headless"),
			filepath.Join(exeDir, "swarm-headless"),
			filepath.Join(filepath.Dir(exeDir), "swarm-sdk", "cmd", "headless", "headless"),
		}
		log("Checking possible paths from exeDir=%s", exeDir)
		for _, path := range possiblePaths {
			log("Checking path: %s", path)
			if _, err := os.Stat(path); err == nil {
				headlessBinary = path
				log("Found headless binary at: %s", headlessBinary)
				break
			} else {
				log("Path not found: %s (err: %v)", path, err)
			}
		}
	} else {
		log("ERROR: could not get executable path: %v", err)
	}
	log("Final headless binary: %s", headlessBinary)

	// Build command arguments
	// Note: A2A uses global registry (~/.swarmos/a2a-registry.sqlite) automatically
	args := []string{
		"peer",
		"-handle", config.Handle,
		"-storage", peerStoragePath,
		"-swarm", a2a.DefaultSwarmName, // Use default swarm for peer discovery
		"-listen", "127.0.0.1:0", // Enable A2A with auto-assigned port (IPv4 only)
	}
	log("Base args: %v", args)

	if config.Name != "" {
		args = append(args, "-name", config.Name)
		log("Added name: %s", config.Name)
	}
	if config.Description != "" {
		args = append(args, "-description", config.Description)
		log("Added description (len=%d)", len(config.Description))
	}
	if config.SystemPrompt != "" {
		args = append(args, "-system-prompt", config.SystemPrompt)
		log("Added system-prompt (len=%d)", len(config.SystemPrompt))
	}
	if len(config.Tools) > 0 {
		args = append(args, "-tools", strings.Join(config.Tools, ","))
		log("Added tools: %v", config.Tools)
	}

	// Pass the current provider and model to the spawned peer so it has the same configuration.
	// Read both from the client (authoritative) rather than the local mirror fields, so a
	// spawned peer inherits exactly what the SDK client considers active.
	providerName := sdk.GetProviderName()
	currentModel := sdk.GetCurrentModel()
	log("SDK provider: %s, model: %s", providerName, currentModel)
	if providerName != "" {
		args = append(args, "-provider", providerName)
		log("Added provider: %s", providerName)
	}
	if currentModel != "" {
		args = append(args, "-model", currentModel)
		log("Added model: %s", currentModel)
	}
	// Set a default timeout of 30 minutes (peer will auto-shutdown after inactivity)
	args = append(args, "-timeout", "30m")
	log("Added timeout: 30m")
	log("Final args: %v", args)

	// Create command - use exec.Command (not CommandContext) so the peer
	// continues running even if the spawning context is cancelled
	cmd := exec.Command(headlessBinary, args...)
	cmd.Dir = workspacePath
	log("Created command with Dir=%s", workspacePath)

	// Set environment to inherit API keys and other settings
	cmd.Env = os.Environ()
	log("Inherited environment (len=%d)", len(cmd.Env))

	// Add scope key so spawned peers share the same scope as their parent (TUI)
	// This allows peer-to-peer communication within the same swarm
	scopeKey := a2a.NormalizeScopeKey("", workspacePath)
	cmd.Env = append(cmd.Env, fmt.Sprintf("SWARM_SWARM_SCOPE_ID=%s", scopeKey))
	log("Added SWARM_SWARM_SCOPE_ID env var: %s", scopeKey)

	// Pass through context service configuration to spawned peers
	// This enables peers to connect to the same remote context service as the parent
	if url := os.Getenv("SWARM_CONTEXT_SERVICE_URL"); url != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("SWARM_CONTEXT_SERVICE_URL=%s", url))
		log("Added SWARM_CONTEXT_SERVICE_URL env var: %s", url)
	}
	if apiKey := os.Getenv("SWARM_CONTEXT_SERVICE_API_KEY"); apiKey != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("SWARM_CONTEXT_SERVICE_API_KEY=%s", apiKey))
		log("Added SWARM_CONTEXT_SERVICE_API_KEY env var: [REDACTED]")
	}

	// Detach the process so it runs independently (platform-specific)
	cmd = detachPeerProcess(cmd)
	log("Detached peer process (platform-specific)")

	// Start the process
	log("STARTING peer process...")
	sdk.logger.Info(ctx, "peer.spawning",
		observability.F("handle", config.Handle),
		observability.F("binary", headlessBinary),
		observability.F("storage", peerStoragePath))

	if err := cmd.Start(); err != nil {
		log("ERROR: failed to spawn peer process: %v", err)
		return nil, fmt.Errorf("failed to spawn peer process: %w", err)
	}
	log("Peer process started! PID=%d", cmd.Process.Pid)

	// Release the process so we don't need to wait for it
	// This allows the peer to run independently of the parent
	if err := cmd.Process.Release(); err != nil {
		log("WARNING: failed to release process: %v", err)
		sdk.logger.Warn(ctx, "peer.release_failed",
			observability.F("handle", config.Handle),
			observability.F("error", err.Error()))
	} else {
		log("Process released successfully")
	}

	// Track the spawned process info (we can still signal it if needed)
	sdk.spawnedPeersMu.Lock()
	if sdk.spawnedPeers == nil {
		sdk.spawnedPeers = make(map[string]*exec.Cmd)
	}
	sdk.spawnedPeers[config.Handle] = cmd
	sdk.spawnedPeersMu.Unlock()
	log("Tracked spawned peer in map")

	// Wait for peer to start up and write its status file
	log("Waiting 2 seconds for peer to initialize...")
	sdk.logger.Info(ctx, "peer.waiting_for_startup",
		observability.F("handle", config.Handle))

	time.Sleep(2 * time.Second) // Give peer time to initialize

	// Read peer status file to get its endpoint
	statusFile := filepath.Join(peerStoragePath, "peer-status.json")
	log("Looking for status file: %s", statusFile)

	var peerEndpoint string
	statusData, err := os.ReadFile(statusFile)
	if err == nil {
		log("Status file read successfully (len=%d)", len(statusData))
		var status struct {
			Endpoint string `json:"endpoint"`
		}
		if json.Unmarshal(statusData, &status) == nil && status.Endpoint != "" {
			peerEndpoint = status.Endpoint
			log("Found endpoint in status file: %s", peerEndpoint)
			sdk.logger.Info(ctx, "peer.discovered_via_status",
				observability.F("handle", config.Handle),
				observability.F("endpoint", peerEndpoint))
		} else {
			log("Status file parsed but no endpoint found")
		}
	} else {
		log("ERROR: failed to read status file: %v", err)
	}

	// If we have an endpoint, add the peer to our A2A runtime for communication
	if peerEndpoint != "" && sdk.activeAgent() != nil && sdk.activeAgent().A2ARuntime() != nil {
		log("Registering peer with A2A runtime at endpoint: %s", peerEndpoint)
		sdk.logger.Info(ctx, "peer.registering",
			observability.F("handle", config.Handle),
			observability.F("endpoint", peerEndpoint))

		// Extract base URL from the RPC endpoint (strip /rpc or /a2a path)
		// FetchAgentCard expects http://host:port, not http://host:port/rpc
		baseURL := peerEndpoint
		if idx := strings.Index(baseURL, "/rpc"); idx > 0 {
			baseURL = baseURL[:idx]
		} else if idx := strings.Index(baseURL, "/a2a"); idx > 0 {
			baseURL = baseURL[:idx]
		}
		log("Base URL for agent card fetch: %s (from endpoint: %s)", baseURL, peerEndpoint)

		// Fetch agent card to verify and get peer info
		if card, err := sdk.activeAgent().A2ARuntime().FetchAgentCard(ctx, baseURL); err == nil {
			log("Agent card fetched successfully: name=%s", card.Name)
			sdk.logger.Info(ctx, "peer.registered",
				observability.F("handle", config.Handle),
				observability.F("endpoint", peerEndpoint),
				observability.F("name", card.Name))
		} else {
			log("ERROR: failed to fetch agent card: %v", err)
			sdk.logger.Warn(ctx, "peer.card_fetch_failed",
				observability.F("handle", config.Handle),
				observability.F("endpoint", peerEndpoint),
				observability.F("error", err.Error()))
		}
	} else {
		log("WARNING: no endpoint found - peer may not have started correctly")
		sdk.logger.Warn(ctx, "peer.no_endpoint",
			observability.F("handle", config.Handle),
			observability.F("status_file", statusFile))
	}

	log("=== CreatePeer END - handle=%s endpoint=%s ===", config.Handle, peerEndpoint)
	return &swarm.PeerInfo{
		Handle:      config.Handle,
		Name:        config.Name,
		Endpoint:    peerEndpoint,
		Status:      "online",
		IsLocal:     true,
		ProcessID:   cmd.Process.Pid,
		StoragePath: peerStoragePath,
	}, nil
}

// ListCreatedPeers implements the swarm.PeerCreator interface
// Note: This is specifically for the PeerCreator interface, different from ListSwarmPeers
func (sdk *SDKIntegration) ListCreatedPeers(ctx context.Context) ([]swarm.PeerInfo, error) {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return nil, nil
	}

	// Get peers from A2A runtime
	result, err := sdk.activeAgent().A2ARuntime().ListPeers(ctx)
	if err != nil {
		return nil, err
	}

	// Convert to swarm.PeerInfo
	peers := make([]swarm.PeerInfo, 0, len(result.Peers))
	for _, p := range result.Peers {
		peers = append(peers, swarm.PeerInfo{
			Handle:   p.Handle,
			Name:     p.Handle, // Use handle as name for now
			Endpoint: "",       // Not available from SwarmPeerInfo
			Status:   string(p.Status),
			IsLocal:  false,
		})
	}

	return peers, nil
}

// StopSpawnedPeer stops a spawned peer process by handle.
func (sdk *SDKIntegration) StopSpawnedPeer(handle string) error {
	sdk.spawnedPeersMu.Lock()
	defer sdk.spawnedPeersMu.Unlock()

	cmd, exists := sdk.spawnedPeers[handle]
	if !exists {
		return fmt.Errorf("peer '%s' not found", handle)
	}

	// Try to gracefully terminate the process
	if cmd.Process != nil {
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			// If interrupt fails, try kill
			_ = cmd.Process.Kill()
		}
	}

	delete(sdk.spawnedPeers, handle)
	sdk.logger.Info(context.Background(), "peer.stopped",
		observability.F("handle", handle))

	return nil
}

// StopAllSpawnedPeers stops all spawned peer processes.
// This should be called when the TUI shuts down.
func (sdk *SDKIntegration) StopAllSpawnedPeers() {
	sdk.spawnedPeersMu.Lock()
	defer sdk.spawnedPeersMu.Unlock()

	for handle, cmd := range sdk.spawnedPeers {
		if cmd.Process != nil {
			// Try graceful termination first
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				// If interrupt fails, force kill
				_ = cmd.Process.Kill()
			}
			sdk.logger.Info(context.Background(), "peer.stopped",
				observability.F("handle", handle))
		}
	}

	// Clear the map
	sdk.spawnedPeers = make(map[string]*exec.Cmd)
}

// EnableA2A enables A2A mode with the given handle
func (sdk *SDKIntegration) EnableA2A(ctx context.Context, handle string) error {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()

	if sdk.activeAgent() == nil {
		return fmt.Errorf("agent not initialized")
	}

	if sdk.activeAgent().A2ARuntime() != nil {
		return fmt.Errorf("A2A is already enabled")
	}

	// Enable A2A on the agent, using hub-backed or SQLite backend
	if sdk.hub != nil {
		if err := sdk.activeAgent().EnableA2AWithHub(ctx, handle, sdk.hub); err != nil {
			return err
		}
	} else {
		if err := sdk.activeAgent().EnableA2A(ctx, handle); err != nil {
			return err
		}
	}

	// Update local state
	sdk.a2aEnabled = true
	sdk.a2aHandle = handle

	// Get the listen address from the runtime
	if runtime := sdk.activeAgent().A2ARuntime(); runtime != nil {
		sdk.a2aListenAddress = runtime.Peer().EndpointURL
	}

	sdk.logger.Info(ctx, "a2a.enabled",
		observability.F("handle", handle),
		observability.F("address", sdk.a2aListenAddress))

	return nil
}

// DisableA2A disables A2A mode
func (sdk *SDKIntegration) DisableA2A(ctx context.Context) error {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()

	if sdk.activeAgent() == nil {
		return fmt.Errorf("agent not initialized")
	}

	if sdk.activeAgent().A2ARuntime() == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	// Disable A2A on the agent
	if err := sdk.activeAgent().DisableA2A(ctx); err != nil {
		return err
	}

	// Update local state
	sdk.a2aEnabled = false
	sdk.a2aHandle = ""
	sdk.a2aListenAddress = ""

	sdk.logger.Info(ctx, "a2a.disabled")

	return nil
}

// AddPeer adds a remote peer by endpoint URL
// This fetches the peer's agent card to verify connectivity and discover their identity
func (sdk *SDKIntegration) AddPeer(ctx context.Context, endpointURL string) error {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	runtime := sdk.activeAgent().A2ARuntime()

	// Fetch the agent card from the remote peer to verify connectivity
	card, err := runtime.FetchAgentCard(ctx, endpointURL)
	if err != nil {
		return fmt.Errorf("failed to fetch agent card from %s: %w", endpointURL, err)
	}

	// Extract handle from the card
	handle := card.Name
	if handle == "" {
		handle = "remote-peer"
	}

	sdk.logger.Info(ctx, "a2a.peer.discovered",
		observability.F("endpoint", endpointURL),
		observability.F("handle", handle),
		observability.F("name", card.Name))

	return nil
}

// SwarmCommunicator interface implementations
// These methods satisfy the swarm.SwarmCommunicator interface

// Broadcast sends a message to all connected peers
func (sdk *SDKIntegration) Broadcast(ctx context.Context, message string) error {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	_, err := sdk.activeAgent().A2ARuntime().Broadcast(ctx, message)
	return err
}

// SendMessage sends a direct message to a specific peer
func (sdk *SDKIntegration) SendMessage(ctx context.Context, targetHandle string, message string) error {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	_, err := sdk.activeAgent().A2ARuntime().SendDM(ctx, targetHandle, message)
	return err
}

// UpdateStatus updates the local agent's status and current task
func (sdk *SDKIntegration) UpdateStatus(ctx context.Context, status string, currentTask string) error {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	var swarmStatus a2a.SwarmStatus
	switch status {
	case "idle":
		swarmStatus = a2a.SwarmStatusIdle
	case "working":
		swarmStatus = a2a.SwarmStatusWorking
	case "busy":
		swarmStatus = a2a.SwarmStatusBusy
	case "away":
		swarmStatus = a2a.SwarmStatusAway
	default:
		swarmStatus = a2a.SwarmStatusIdle
	}

	_, err := sdk.activeAgent().A2ARuntime().UpdateStatus(ctx, swarmStatus, currentTask)
	return err
}

// SyncTasks syncs completed tasks with the swarm
func (sdk *SDKIntegration) SyncTasks(ctx context.Context, completedTasks []string) error {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return fmt.Errorf("A2A is not enabled")
	}

	// Note: AgentSwarmStatus doesn't have CompletedTasks field in current implementation
	// Log the sync event and update local state
	sdk.logger.Info(ctx, "swarm.tasks_synced",
		observability.F("completed_count", len(completedTasks)),
		observability.F("tasks", fmt.Sprintf("%v", completedTasks)))

	return nil
}

// GetLocalStatus returns the local agent's current status
func (sdk *SDKIntegration) GetLocalStatus() swarm.AgentStatus {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return swarm.AgentStatus{}
	}

	status := sdk.activeAgent().A2ARuntime().GetSwarmStatus()
	return swarm.AgentStatus{
		Handle:      status.Handle,
		Status:      string(status.Status),
		CurrentTask: status.CurrentTask,
	}
}

// ListSwarmPeers satisfies the swarm.SwarmCommunicator interface
// This returns detailed peer status with context
func (sdk *SDKIntegration) ListSwarmPeers(ctx context.Context) ([]swarm.PeerStatus, error) {
	sdk.a2aMu.RLock()
	defer sdk.a2aMu.RUnlock()

	if sdk.activeAgent() == nil || sdk.activeAgent().A2ARuntime() == nil {
		return nil, nil
	}

	// Get peers from A2A runtime
	result, err := sdk.activeAgent().A2ARuntime().ListPeers(ctx)
	if err != nil {
		return nil, err
	}

	// Convert to swarm.PeerStatus
	peers := make([]swarm.PeerStatus, 0, len(result.Peers))
	for _, p := range result.Peers {
		peerStatus := swarm.PeerStatus{
			Handle:      p.Handle,
			Name:        p.Handle,
			Status:      string(p.Status),
			CurrentTask: "",
			LastSeen:    time.Now().Format(time.RFC3339),
			IsLocal:     false,
		}
		peers = append(peers, peerStatus)
	}

	return peers, nil
}

// JoinSwarm enables A2A mode and joins the swarm with the given handle
// This satisfies the swarm.SwarmCommunicator interface
func (sdk *SDKIntegration) JoinSwarm(ctx context.Context, handle string, username string) error {
	sdk.a2aMu.Lock()
	defer sdk.a2aMu.Unlock()

	if sdk.activeAgent() == nil {
		return fmt.Errorf("SDK agent not initialized")
	}

	// Idempotent: if already in the swarm, return nil — A2A is always-on now.
	if sdk.a2aEnabled && sdk.a2aHandle != "" {
		return nil
	}

	// Enable A2A mode with the given handle
	if err := sdk.activeAgent().EnableA2A(ctx, handle); err != nil {
		return fmt.Errorf("failed to enable A2A: %w", err)
	}

	// Store the handle and mark as enabled
	sdk.a2aEnabled = true
	sdk.a2aHandle = handle

	sdk.logger.Info(ctx, "swarm.joined",
		observability.F("handle", handle),
		observability.F("username", username))

	// Wire up the swarm tool communicator now that A2A is enabled
	if sdk.toolRegistry != nil {
		if tool, err := sdk.toolRegistry.Get("swarm"); err == nil {
			if swarmTool, ok := tool.(*swarm.SwarmTool); ok {
				swarmTool.SetCommunicator(sdk)
			}
		}
	}

	return nil
}

// SetCodeMode toggles codemode on or off in-place on the raw tool registry.
// When enable=true, all tools except run_code are hidden from the LLM tool
// list; run_code's sandbox can still call them internally via the executor.
// When enable=false those tools are restored and run_code is re-hidden.
// No agent recreation is needed — the same agent sees the updated registry.
func (sdk *SDKIntegration) SetCodeMode(enable bool) error {
	if sdk.codeModeToggle == nil {
		return fmt.Errorf("codemode not available (InstallInPlace did not succeed at startup)")
	}
	if err := sdk.codeModeToggle(enable); err != nil {
		return fmt.Errorf("codemode toggle: %w", err)
	}
	// Inject (or clear) the code-mode instruction block on the agent's system
	// prompt. Hiding the individual tools from the LLM tool list is not enough
	// on its own — without an explicit instruction the model tends to ignore
	// run_code, hallucinate the old per-call tools, or give up on one error.
	// The suffix only appears while code mode is enabled and is cleared on
	// disable, so normal mode is never polluted.
	if ag := sdk.activeAgent(); ag != nil {
		if enable {
			ag.SetEphemeralSystemFn(func(_ []*conversation.Message) string {
				return codemode.SystemPrompt
			})
		} else {
			ag.SetEphemeralSystemFn(nil)
		}
	}
	logDebug("SetCodeMode: codemode=%v — registry.List()=%v", enable, sdk.toolRegistry.List())
	return nil
}

// RecreateAgent delegates to SetCodeMode. Agent recreation is no longer
// needed for codemode toggling — the same agent sees the in-place registry.
func (sdk *SDKIntegration) RecreateAgent(enableCodeMode bool) error {
	return sdk.SetCodeMode(enableCodeMode)
}

// getStorageRoot returns the home directory for storage paths
func getStorageRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp"
	}
	return home
}
