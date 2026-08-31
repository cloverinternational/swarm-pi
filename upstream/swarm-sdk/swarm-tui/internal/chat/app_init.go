package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-core/core"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/a2a"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/bgprocess"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/appshell"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/prerender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/prompts"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	bashrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/bash"
	editrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/edit"
	genericrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/generic"
	greprender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/grep"
	historyrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/history"
	patchrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/patch"
	readrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/read"
	readbgrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/readbg"
	subagentrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/subagent"
	todorender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/todo"
	websearchrender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/websearch"
	writerender "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/write"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/components/gitpanel"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/gitops"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/visual"
)

// ============================================================================
// INIT CONSTANTS
// ============================================================================

const (
	// workerPoolSize is the number of background goroutines used by the
	// pre-render buffer for off-thread content wrapping.
	// Kept as a named constant so it can be tuned or made config-driven later.
	workerPoolSize = 4
)

// ============================================================================
// CONSTRUCTOR
// ============================================================================

// NewApp creates a new chat application with default options
func NewApp() *App {
	return NewAppWithOptions(AppOptions{})
}

// NewAsyncAppWithOptions creates the bounded, interactive shell used by the
// CLI. The complete runtime is constructed by App.Init after Bubble Tea starts.
func NewAsyncAppWithOptions(opts AppOptions) *App {
	ti := NewSimpleInput()
	ti.SetWidth(72)
	brand := selectStartupBrand()
	opts.startupBrand = &brand
	theme := DefaultTheme
	if brand.autovac {
		theme = AutoVacTheme
	}
	return &App{
		appOptions:          opts,
		compactionMu:        &sync.Mutex{},
		width:               80,
		height:              24,
		textInput:           ti,
		theme:               AdaptThemeForTerminal(theme, true),
		hasDarkTerminal:     true,
		autovacMode:         brand.autovac,
		autovacLogoText:     brand.logo,
		autovacBrandName:    brand.brand,
		currentSplashText:   brand.splash,
		bootstrapPending:    true,
		bootstrapGeneration: 1,
		bootstrapStartedAt:  time.Now(),
		introFrame:          brand.logo,
		introFromBootstrap:  true,
		updateQueue:         make(chan tea.Msg, 10000),
		viewNeedsRefresh:    true,
	}
}

func (a *App) resumeConversationAtStartup(conversationID string, openConversation func(string) bool) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return
	}

	resumed := false
	var panicValue any
	func() {
		defer func() {
			panicValue = recover()
		}()
		if openConversation != nil {
			resumed = openConversation(conversationID)
		}
	}()

	if resumed {
		logDebug("[startup-resume] Opened conversation %s", conversationID)
		return
	}

	if panicValue != nil {
		logDebug("[startup-resume] Failed to open conversation %s: %v", conversationID, panicValue)
	} else {
		logDebug("[startup-resume] Conversation %s was not found", conversationID)
	}
	a.addNotification("warning", i18n.T("residual.init.resume_failed", conversationID))
}

// NewAppWithOptions creates a fully initialized application. It remains the
// synchronous constructor for tests and non-program callers; the CLI uses
// NewAsyncAppWithOptions so this work runs behind an already-visible shell.
func NewAppWithOptions(opts AppOptions) *App {
	return newAppWithOptions(opts)
}

func newAppWithOptions(opts AppOptions) *App {
	unresolvedWorkspaceRoot := opts.WorkspaceRoot
	ignoredHomeGit := false
	if binding, err := gitops.ResolveWorkspaceBinding(opts.WorkspaceRoot); err == nil {
		opts.WorkspaceRoot = binding.ExecutionRoot
		ignoredHomeGit = binding.IgnoredHomeGit
		if strings.TrimSpace(opts.ProjectRoot) == "" || opts.ProjectRoot == unresolvedWorkspaceRoot {
			opts.ProjectRoot = binding.ProjectRoot
		}
	}
	if strings.TrimSpace(opts.ProjectRoot) == "" {
		opts.ProjectRoot = opts.WorkspaceRoot
	}

	// Relative file paths in messages ("internal/chat/app.go:42") are resolved
	// against this root before becoming clickable links. Registering it here
	// means every render path picks it up without threading app state through
	// the markdown helpers; an empty root simply leaves paths as plain text.
	SetLinkWorkspaceRoot(opts.WorkspaceRoot)

	// Start with a dark-terminal default so construction never waits for a
	// terminal response. App.Init requests the real background color through
	// Bubble Tea and Update applies it on the event loop when it arrives.
	hasDark := true

	// Initialize text input
	ti := NewSimpleInput()
	ti.SetWidth(80)

	// Initialize clipboard for image support
	if err := initClipboard(); err != nil {
		logDebug("⚠ Clipboard initialization failed: %v (text paste will still work)", err)
	}

	// Initialize viewport for messages
	vp := NewMessageList(80, 20)

	// Initialize slash commands
	cmdReg := commands.InitRegistry(opts.Updater)
	cmdAuto := commands.NewAutocomplete(cmdReg)

	// Load saved model configuration FIRST (before SDK)
	configMgr, _ := commands.NewConfigManager()
	if configMgr != nil {
		if savedConfig, err := configMgr.LoadConfig(); err == nil && savedConfig != nil {
			i18n.SetLanguage(savedConfig.Language)
		}
	}
	var currentProvider, currentModel, providerDisplay, modelDisplay string
	var providerConfigs []commands.ProviderConfig
	var notifications []Notification
	var sdkInitErr string
	harnessSelected := strings.TrimSpace(opts.HarnessPath) != ""

	// Track source of model configuration for logging
	configSource := "defaults"

	// Load provider configs from providers.json for UI rendering (icons, colors)
	if configMgr != nil {
		if providers, err := configMgr.LoadProviders(); err == nil {
			providerConfigs = providers
		}
	}
	resolveDisplays := func() {
		// fallbacks already set
		for _, prov := range providerConfigs {
			if prov.Name == currentProvider {
				providerDisplay = prov.DisplayName
				for _, model := range prov.Models {
					if model.ID == currentModel {
						modelDisplay = model.DisplayName
						return
					}
				}
			}
		}
	}

	// Convert from commands.ProviderConfig to internal ProviderConfig for UI rendering
	var appProviderConfigs []ProviderConfig
	if len(providerConfigs) > 0 {
		appProviderConfigs = make([]ProviderConfig, len(providerConfigs))
		for i, cmdProv := range providerConfigs {
			models := make([]ProviderModel, len(cmdProv.Models))
			for j, cmdModel := range cmdProv.Models {
				models[j] = ProviderModel{
					ID:                      cmdModel.ID,
					DisplayName:             cmdModel.DisplayName,
					ContextWindow:           cmdModel.ContextWindow,
					Context:                 cmdModel.Context,
					SupportsReasoningEffort: cmdModel.SupportsReasoningEffort,
					ReasoningEfforts:        cmdModel.ReasoningEfforts,
					ThinkingEnabled:         cmdModel.ThinkingEnabled,
					ThinkingBudget:          cmdModel.ThinkingBudget,
					ThinkingEffort:          cmdModel.ThinkingEffort,
					MaxOutputTokens:         cmdModel.MaxOutputTokens,
					CostInput:               cmdModel.CostInput,
					CostOutput:              cmdModel.CostOutput,
					Reasoning:               cmdModel.Reasoning,
					ToolCall:                cmdModel.ToolCall,
				}
			}
			appProviderConfigs[i] = ProviderConfig{
				Name:             cmdProv.Name,
				DisplayName:      cmdProv.DisplayName,
				Color:            cmdProv.Color,
				Type:             cmdProv.Type,
				APIType:          cmdProv.APIType,
				BaseURL:          cmdProv.BaseURL,
				HTTPMaxRetries:   cmdProv.HTTPMaxRetries,
				Available:        cmdProv.Available,
				Models:           models,
				CodexQueryParams: cmdProv.CodexQueryParams,
				CodexHTTPHeaders: cmdProv.CodexHTTPHeaders,
			}
		}
		logDebug("Loaded %d provider configs from providers.json for UI rendering", len(appProviderConfigs))
	}

	// PRIORITY ORDER FOR MODEL CONFIGURATION:
	// 1. Active Profile (highest priority) - user's explicitly selected profile
	// 2. Saved config.json - previously used provider/model
	// 3. Hardcoded defaults (lowest priority) - fallback when nothing else exists

	profileMgr := settings.NewProfileManager()
	providerFromProfile := false

	// First, try to use the active profile's main model
	if activeProfile, err := profileMgr.GetActiveProfile(); err == nil && activeProfile != nil {
		if pointer, err := profileMgr.ResolveAlias(settings.AliasMain); err == nil {
			if pointer.Provider != "" && pointer.Model != "" {
				currentProvider = pointer.Provider
				currentModel = pointer.Model
				configSource = fmt.Sprintf("active profile (%s)", activeProfile.ID)
				providerFromProfile = true
				resolveDisplays()
				logDebug("Using active profile: %s / %s (profile: %s)", currentProvider, currentModel, activeProfile.ID)
			}
		} else {
			logDebug("Profile alias resolution failed: %v", err)
		}
	} else {
		logDebug("No active profile found: %v", err)
	}

	// If no active profile, try saved config.json
	if currentProvider == "" || currentModel == "" {
		if configMgr != nil {
			if savedConfig, err := configMgr.LoadConfig(); err == nil && savedConfig != nil {
				currentProvider = savedConfig.CurrentProvider
				currentModel = savedConfig.CurrentModel
				configSource = "saved config.json"
				resolveDisplays()
				logDebug("Loaded from config.json: %s / %s (%s / %s)", currentProvider, currentModel, providerDisplay, modelDisplay)
			}
		}
	}

	// If still no provider/model, use hardcoded defaults (fallback)
	if currentProvider == "" || currentModel == "" {
		currentProvider = "ClaudeCode"
		currentModel = "claude-opus-4-20250514"
		providerDisplay = "Claude Code (OAuth)"
		modelDisplay = "Claude 4 Opus"
		configSource = "hardcoded defaults"
		logDebug("Using hardcoded defaults: %s / %s", currentProvider, currentModel)
	}

	logDebug("Model configuration source: %s", configSource)

	// If the configured provider lacks credentials but another provider has them, auto-select.
	// IMPORTANT: Never override an explicit profile selection — if the user's active profile
	// specifies a provider, trust it. The SDK init will fail with a clear error if creds are
	// missing, rather than silently switching to a different provider.
	if !harnessSelected && !providerFromProfile && !hasProviderCredentials(currentProvider) {
		switch {
		case hasProviderCredentials("openai"):
			currentProvider = "OpenAI"
			currentModel = "gpt-5.6-terra"
			logDebug("Auto-selected provider: OpenAI (Codex) because existing provider lacks credentials")
		case hasProviderCredentials("cerebras"):
			currentProvider = "Cerebras"
			currentModel = "llama-3.3-70b"
			logDebug("Auto-selected provider: Cerebras because existing provider lacks credentials")
		case hasProviderCredentials("claudecode"), hasProviderCredentials("anthropic"):
			currentProvider = "ClaudeCode"
			currentModel = "claude-opus-4-20250514"
			logDebug("Auto-selected provider: Claude Code because configured provider lacks credentials")
		default:
			// Keep defaults; no creds anywhere.
		}
		resolveDisplays()
		if configMgr != nil {
			_, _ = configMgr.UpdateConfig(func(cfg *commands.SwarmOSConfig) {
				cfg.CurrentProvider = currentProvider
				cfg.CurrentModel = currentModel
			})
			logDebug("Saved auto-selected provider/model to config: %s / %s", currentProvider, currentModel)
		}
	}

	// Load max tokens from advanced settings (before SDK init)
	maxTokens := 31999 // Default to ~32k (must be under 32000 for Opus 4)
	// We'll create a temporary AdvancedSettings just to load the max tokens
	tempAdvanced := settings.NewAdvancedSettings(nil)
	if tempAdvanced != nil {
		maxTokens = tempAdvanced.GetMaxTokens()
		logDebug("Loaded max tokens from settings: %d", maxTokens)
	}

	// Load auto-compaction settings (before SDK init)
	tempCompaction := settings.NewCompactionSettings(configMgr)
	var autoCompactionConfig *agent.AutoCompactionConfig
	if tempCompaction != nil {
		autoCompactionConfig = buildAutoCompactionConfig(tempCompaction, currentProvider, currentModel)
		// Guarantee a sane trigger even if the resolved descriptor is absent or
		// out-of-range. Without this the agent falls through to its internal
		// effective-window default, which approaches ~98% of large context
		// windows and compacts far too late. Valid user descriptors pass through.
		autoCompactionConfig = applyDefaultCompactionThreshold(autoCompactionConfig)
		logDebug("============================================================")
		logDebug("AUTO-COMPACTION SETTINGS AT STARTUP:")
		logDebug("  - EnableAutoCompaction: %v", autoCompactionConfig.EnableAutoCompaction)
		logDebug("  - Threshold: %s %.4f",
			autoCompactionConfig.Threshold.Mode,
			autoCompactionConfig.Threshold.Value)
		logDebug("  - ContinueIfRunning: %v", autoCompactionConfig.ContinueIfRunning)
		logDebug("============================================================")
	} else {
		logDebug("WARNING: tempCompaction is nil, auto-compaction config not loaded!")
	}

	// Load code mode setting from config.json
	enableCodeMode := false
	if tempCompaction != nil {
		// Reuse the config manager that compaction loaded to avoid re-reading
		// the file. The core config EnableCodeMode field is read directly.
		cm, cmErr := commands.NewConfigManager()
		if cmErr == nil {
			if cfg, loadErr := cm.LoadConfig(); loadErr == nil {
				enableCodeMode = cfg.EnableCodeMode
			}
		}
		if enableCodeMode {
			logDebug("Code mode enabled from config.json")
		}
	}

	// Load permissions config and create approval broker
	permConfig, permErr := LoadPermissionConfig()
	if permErr != nil {
		logDebug("Failed to load permission config: %v", permErr)
		permConfig = NewPermissionConfigWithDefaults()
		if saveErr := permConfig.Save(); saveErr != nil {
			logDebug("Failed to save default permission config: %v", saveErr)
		}
	}
	workspaceRoot := ""
	if cwd, err := os.Getwd(); err == nil {
		if absRoot, err := filepath.Abs(cwd); err == nil {
			workspaceRoot = absRoot
		} else {
			workspaceRoot = cwd
		}
	}
	projectPermConfig, projectPermErr := LoadProjectPermissionConfig(workspaceRoot)
	if projectPermErr != nil {
		logDebug("Failed to load project permission config: %v", projectPermErr)
	}

	// Disk-based indexing: semantic_grep uses a shared disk cache
	// This eliminates the 80MB memory overhead of pre-indexing
	if workspaceRoot != "" {
		logDebug("[Index] Using disk-based symbol index for: %s", workspaceRoot)
	}

	permBroker := NewPermissionsBroker(permConfig)
	questionBroker := NewQuestionBroker()
	planBroker := NewPlanBroker()
	vaultUnlockBroker := NewVaultUnlockBroker()

	// Generate a stable session ID for this TUI run.
	// This is used to scope plan file storage so multiple agents in the same
	// workspace never share a plan file, and so the plan survives compaction.
	//
	// Sourced from conversation.ProcessSessionID() rather than a fresh UUID so
	// that there is exactly ONE session identity per process: the value below is
	// handed to the plan broker, the interaction broker and SessionStart, and it
	// is the same value stamped into metadata.custom.session_id on every
	// conversation this process creates. That shared value is what makes the
	// hook/event stream joinable to the conversation store (PLAN.md gap G1).
	// Same shape as before (a v4 UUID), minted once, process-wide.
	rootSessionID := conversation.ProcessSessionID()

	// Create a temporary message queue for approval/question messages during SDK initialization
	// (dispatcher won't be ready until after app is created)
	var pendingMessages []tea.Msg
	var mu sync.Mutex
	tempDispatcher := func(msg tea.Msg) {
		mu.Lock()
		pendingMessages = append(pendingMessages, msg)
		mu.Unlock()
	}
	permBroker.SetDispatcher(tempDispatcher)
	questionBroker.SetDispatcher(tempDispatcher)
	planBroker.SetDispatcher(tempDispatcher)
	vaultUnlockBroker.SetDispatcher(tempDispatcher)
	logDebug("Temporary dispatcher set for broker during SDK initialization")

	// Initialize background process manager BEFORE SDK (SDK needs it for tool registration)
	bgProcessConfig := bgprocess.DefaultManagerConfig()
	bgProcessManager := bgprocess.NewManager(bgProcessConfig)
	logDebug("Background process manager initialized")

	// Extract Exa API key from provider configs for web search tool
	var exaAPIKey string
	for _, p := range providerConfigs {
		if p.APIType == "exa" && p.APIKey != "" {
			exaAPIKey = p.APIKey
			break
		}
	}

	// Start the workspace hub — always-on for local peer discovery.
	// The first binary to start becomes the hub; subsequent ones connect as clients.
	// Uses a Unix socket keyed by sha256(workspaceRoot) for isolation.
	var wsHub *a2a.WorkspaceHub
	if opts.ProjectRoot != "" {
		wsHub = a2a.NewWorkspaceHub(opts.ProjectRoot, nil)
		wsHub.SetCwd(opts.WorkspaceRoot)
		hubHandle := defaultTUIA2AHandle(opts.A2AHandle, rootSessionID)
		hubCtx, hubCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer hubCancel()
		if err := wsHub.StartOrConnect(hubCtx, hubHandle); err != nil {
			logDebug("WARN: workspace hub start failed (A2A falls back to SQLite): %v", err)
			wsHub = nil
		} else {
			logDebug("workspace hub started: isHub=%v handle=%s", wsHub.IsHub(), hubHandle)
		}
	}

	// Initialize SDK integration — always attempt, even without credentials.
	// The visual companion registry supports non-blocking agent updates; blocking
	// questions and plan approval stay inside the terminal.
	convRoot := paths.ConversationsDir()
	visualReg := visual.NewRegistry(convRoot)

	// The SDK initialises tools, storage, hooks, MCP, skills etc. regardless
	// of whether a provider is available.  If no credentials are found the SDK
	// will start in "no provider" mode; the user can add one later via /auth
	// or the settings UI without restarting the app.
	var sdk *SDKIntegration
	{
		var err error
		sdk, err = newSDKIntegrationForAppOptions(currentProvider, currentModel, opts, SDKIntegrationOptions{
			MaxTokens:            maxTokens,
			DebugMode:            opts.DebugMode,
			WorkspaceRoot:        opts.WorkspaceRoot,
			ProjectRoot:          opts.ProjectRoot,
			BGProcessManager:     bgProcessManager,
			PermissionConfig:     permConfig,
			ApprovalBroker:       permBroker,
			QuestionBroker:       questionBroker,
			VaultUnlocker:        vaultUnlockBroker,
			AutoCompactionConfig: autoCompactionConfig,
			PlanBroker:           planBroker,
			RootSessionID:        rootSessionID,
			ExaAPIKey:            exaAPIKey,
			ProfileManager:       profileMgr, // Pass ProfileManager for role-based model selection
			A2AEnabled:           opts.A2AEnabled,
			A2AHandle:            opts.A2AHandle,
			A2AListenAddress:     opts.A2AListenAddress,
			EnableCodeMode:       enableCodeMode,
			VisualRegistry:       visualReg,
			Hub:                  wsHub,
		})
		if err != nil {
			logDebug("ERROR: Failed to initialize SDK: %v", err)
			if strings.TrimSpace(opts.HarnessPath) != "" {
				panic(fmt.Errorf("initialize harness-governed TUI: %w", err))
			}
			sdk = nil
			sdkInitErr = err.Error()
			notifications = append(notifications, Notification{
				Kind:      "error",
				Text:      i18n.T("residual.init.sdk_unavailable", err),
				CreatedAt: time.Now(),
			})
		} else {
			logDebug("SUCCESS: SDK initialized with model: %s (max tokens: %d)", currentModel, maxTokens)
			if sdk.harnessGoverned() {
				currentProvider = sdk.providerName
				currentModel = sdk.currentModel
				providerDisplay = currentProvider
				modelDisplay = currentModel
				resolveDisplays()
				configSource = "compiled harness plan"
			}
			sdkInitErr = ""
			notifications = nil
			if sdk.permissionChecker != nil && projectPermErr == nil {
				sdk.permissionChecker.SetProjectConfig(projectPermConfig)
				capturedRoot := workspaceRoot
				sdk.permissionChecker.SetProjectConfigCallback(func(config tools.PermissionConfig) {
					if err := SaveProjectPermissionConfig(capturedRoot, config); err != nil {
						logDebug("Failed to save project permission config: %v", err)
					}
				})
			}
			// If the provider is nil (no credentials) but the SDK is up,
			// show a helpful hint instead of the scary "SDK unavailable" message.
			if sdk.provider == nil {
				notifications = append(notifications, Notification{
					Kind:      "info",
					Text:      i18n.T("residual.init.no_provider"),
					CreatedAt: time.Now(),
				})
			}
		}
	}

	// If we declined to bind to a git repo whose top-level is exactly $HOME
	// (accidental `git init` in home), let the user know why the workspace is
	// being treated as non-git. Appended AFTER SDK init so it survives the
	// notifications reset on successful init.
	if ignoredHomeGit {
		notifications = append(notifications, Notification{
			Kind:      "warning",
			Text:      i18n.T("residual.init.ignored_home_git"),
			CreatedAt: time.Now(),
		})
	}

	// Create debug screen
	debugScreen := NewDebugScreen()

	// Create hooks dashboard (will be initialized with hooks manager after SDK init)
	var hooksDashboard *HooksDashboard
	if sdk != nil && sdk.hooksManager != nil {
		hooksDashboard = NewHooksDashboard(sdk.hooksManager)
	}

	// Load render settings (or use defaults)
	renderSettings, err := LoadRenderSettings()
	if err != nil {
		logDebug("Failed to load render settings, using defaults: %v", err)
		renderSettings = NewDefaultRenderSettings()
	}

	// Wire the real Ctrl+Q "Attach & Monitor" screen factory now that sdk is
	// available. Callers (tests, harness embedders) may already have set an
	// explicit AttachScreenFactory in AppOptions — e.g. attach_screen_test.go
	// constructs a fake factory to test key routing in isolation — so only
	// install the SDK-backed default when the caller left it unset.
	if opts.AttachScreenFactory == nil {
		swarmName := a2a.DefaultSwarmName
		opts.AttachScreenFactory = newRealAttachScreenFactory(sdk, swarmName)
	}

	app := &App{
		appOptions:                 opts,
		attachScreenFactory:        opts.AttachScreenFactory,
		compactionMu:               &sync.Mutex{},
		screen:                     ScreenHome,
		hasDarkTerminal:            hasDark,
		theme:                      AdaptThemeForTerminal(DefaultTheme, hasDark),
		homeButton:                 ButtonNewChat,
		workspaceMode:              false, // Start in simple mode (not workspace mode)
		currentWorkspace:           0,
		textInput:                  ti,
		msgViewport:                vp,
		viewportContentDirty:       true,                           // Force initial render
		inputHistory:               NewInputHistory(workspaceRoot), // Per-workspace command history for up/down navigation
		cmdRegistry:                cmdReg,
		cmdAutocomplete:            cmdAuto,
		commandPalette:             NewCommandPalette(),
		modelSwitcher:              *NewModelSwitcher(),
		agentSwitcher:              *NewAgentSwitcher(),
		promptSwitcher:             *NewPromptSwitcher(),
		profileSwitcher:            *NewProfileSwitcher(),
		skillPicker:                *NewSkillPicker(),
		homeInputFocused:           true,
		homeSidebarCollapsed:       true, // Start collapsed; shift+tab expands
		showSidePanel:              true, // Auto-show if screen is wide enough
		twoPaneMode:                true, // Default to two-pane layout for conversations
		sidebarVisible:             true, // Show sidebar by default in two-pane mode
		currentModel:               currentModel,
		currentProvider:            currentProvider,
		currentModelDisplay:        modelDisplay,
		currentProviderDisplay:     providerDisplay,
		modelContextWindow:         provider.DefaultUnknownContextWindow, // Updated dynamically from the selected model.
		tokenCount:                 0,
		showThinking:               renderSettings.ShowThinking,
		showFullToolOutput:         false, // Default to compact mode
		renderSettings:             renderSettings,
		sdk:                        sdk,
		sdkInitError:               sdkInitErr,
		notifications:              notifications,
		debugScreen:                debugScreen,
		hooksDashboard:             hooksDashboard,
		streamingMessage:           false,
		streamBuffer:               "",
		animationClock:             NewAnimationClock(), // SINGLE animation clock for all animations
		screenLayer:                newSmartLayer(),     // 60fps zero-cost layer: O(1) on unchanged frames
		loadingIndicator:           NewLoadingIndicator(LoadingSpinner, "Agent is thinking", AdaptThemeForTerminal(DefaultTheme, hasDark)),
		spinner:                    NewSpinner(),
		taskPanel:                  NewTaskPanelModel(),       // Task panel above chat input
		updateQueue:                make(chan tea.Msg, 10000), // Large buffer for high-throughput streaming (multi-agent + tools)
		bgManager:                  NewBackgroundAgentManager(),
		messageCache:               NewMessageRenderCache(),                  // Per-message render cache (Crush technique)
		preRenderBuffer:            prerender.NewBuffer(workerPoolSize, 100), // Background pre-rendering for smooth scrolling
		debugMode:                  opts.DebugMode,
		useNewUI:                   opts.UseNewUI,
		debugLineageFixture:        opts.DebugLineageFixture,
		updater:                    opts.Updater,
		permissionBroker:           permBroker,
		permissionConfig:           permConfig,
		questionBroker:             questionBroker,
		vaultUnlockBroker:          vaultUnlockBroker,
		interactionBrkr:            NewTUIInteractionBroker(questionBroker, permBroker),
		planBroker:                 planBroker,
		rootSessionID:              rootSessionID, // Stable session ID for plan file scoping
		a2aEnabled:                 opts.A2AEnabled,
		a2aHandle:                  opts.A2AHandle,
		a2aListenAddress:           opts.A2AListenAddress,
		hub:                        wsHub,
		providerConfigs:            appProviderConfigs, // Provider configs loaded from providers.json
		errorLineageExpandedByConv: make(map[string]bool),
		pendingSubAgentExhaustion:  make(map[string]chan agent.FallbackDecision),
	}

	// Start non-blocking memory threshold watcher (350/500/750/1000MB)
	if opts.MemoryWatch {
		app.initMemoryWatcher()
	}

	// Wire the optional visual companion registry on the app-owned brokers.
	// Blocking questions do not depend on it.
	app.visualReg = visualReg
	app.interactionBrkr.SetVisualRegistry(visualReg)
	app.interactionBrkr.SetSessionID(rootSessionID)
	app.interactionBrkr.SetPlanBroker(planBroker)
	app.planBroker.SetVisualRegistry(visualReg)
	app.planBroker.SetSessionID(rootSessionID)

	// Select branding once. The async shell passes its selection through options
	// so the logo and splash do not change during the runtime-ready handoff.
	brand := selectStartupBrand()
	if opts.startupBrand != nil {
		brand = *opts.startupBrand
	}
	app.autovacMode = brand.autovac
	app.autovacLogoText = brand.logo
	app.autovacBrandName = brand.brand
	app.currentSplashText = brand.splash
	if brand.autovac {
		// Override theme with retro green terminal aesthetic
		app.theme = AdaptThemeForTerminal(AutoVacTheme, app.hasDarkTerminal)
		logDebug("🎱 AutoVac mode activated! 1/10 Easter egg triggered.")
	}

	// Apply startup view preference: "simple" goes straight to chat, "advanced" stays on home.
	if renderSettings.GetStartupView() == "simple" {
		app.screen = ScreenChat
		// Create a new conversation so activeConv is not nil when rendering chat.
		// Without this the chat view shows "No conversation" because no conversation
		// is loaded or created at startup.
		app.activeConv = &Conversation{
			ID:          time.Now().Format("20060102150405"),
			Title:       i18n.T("residual.init.new_chat"),
			Status:      "idle",
			IsActive:    false,
			InputBuffer: "",
		}
		app.currentConvID = "" // Will be assigned by SDK on first message
	}

	app.notifications = stampNotifications(app.notifications)
	// Supply live slash-command argument options. The autocomplete queries App
	// on every input update, so profiles/agents/prompts never need a cache flush.
	app.cmdAutocomplete.SetArgumentOptionProvider(app)
	app.configureA2ABridge()
	if permBroker != nil {
		// Set the real dispatcher and replay any buffered messages
		permBroker.SetDispatcher(app.sendToRuntime)
		questionBroker.SetDispatcher(app.sendToRuntime)
		planBroker.SetDispatcher(app.sendToRuntime)
		vaultUnlockBroker.SetDispatcher(app.sendToRuntime)
		if app.interactionBrkr != nil {
			app.interactionBrkr.SetDispatcher(app.sendToRuntime)
		}
		mu.Lock()
		buffered := pendingMessages
		pendingMessages = nil
		mu.Unlock()

		logDebug("Replaying %d buffered messages to app dispatcher", len(buffered))
		for _, msg := range buffered {
			app.sendToRuntime(msg)
		}
	}

	// Initialize metrics store for TPS and cache tracking
	metricsPath := getMetricsPath()
	app.metrics = metrics.NewMetricsStore(metricsPath)
	// Repair any historic time.Since(zero) overflow in TotalStreamingSecs that
	// pre-dates the guards in metrics/persistence.go (2026-05-01). One-shot
	// per launch — affected entries get TotalStreamingSecs / HistoricalAvgTPS
	// reset to 0 so future deltas accumulate from a sane baseline.
	if n := app.metrics.RepairCorruptedDurations(); n > 0 {
		logDebug("[METRICS] Repaired %d corrupted duration entries in model_metrics.json", n)
		_ = app.metrics.SaveToDisk()
	}
	if currentModel != "" {
		app.metrics.SetCurrentModel(currentModel, modelDisplay)
	}
	logDebug("[METRICS] Initialized metrics store at %s for model %s", metricsPath, currentModel)

	// Initialize new modular chat panel if enabled
	if opts.UseNewUI {
		app.chatPanel = chatui.NewPanel(80, 20,
			chatui.WithTheme(chatui.DefaultTheme()),
			chatui.WithShowThinking(false),
		)
		logDebug("[NEWUI] Initialized chatui.Panel for modular rendering")
	}

	// Initialize home screen input
	homeInput := NewSimpleInput()
	homeInput.SetWidth(60)
	homeInput.SetHeight(6) // Allow growth up to 6 lines (dynamic sizing in render)
	homeInput.placeholder = "Ask anything..."
	homeInput.Focus()
	app.homeInput = homeInput

	// Initialize input protection for streaming (prevents cursor disruption)
	app.inputProtection.debounceDelay = 50 * time.Millisecond // 50ms debounce (20 FPS max)
	app.inputProtection.lastUpdateTime = time.Time{}          // Zero time initially
	app.inputProtection.pendingUpdate = false

	// NOTE: Context window is initialized later after debug screen is connected
	// See the section after sdk.SetDebugScreen() below

	// Initialize compaction service for long conversations
	// Default context limit will be updated from model capabilities
	compactionConfig := compaction.DefaultConfig(provider.DefaultUnknownContextWindow)
	app.compactionService = compaction.NewService(compactionConfig)

	// Use the bgProcessManager that was already created before SDK init
	app.bgProcessManager = bgProcessManager

	// Wire completion/error callbacks so the TUI injects system messages
	// when background bash processes finish.  The callbacks fire on the
	// executor goroutine, so we send through the updateQueue (buffered, non-blocking).
	bgProcessManager.SetCompletionCallback(func(handle bgprocess.ProcessHandle, info bgprocess.ProcessInfo, result *bgprocess.ProcessResult) {
		exitCode := -1
		var preview string
		var truncated bool
		if result != nil {
			if result.ExitCode >= 0 {
				exitCode = result.ExitCode
			}
			preview, truncated = bgOutputPreview(result.Output)
		}
		// Use the actual terminal state (completed or cancelled)
		state := string(info.State)
		select {
		case app.updateQueue <- bgProcessDoneMsg{
			processID:       handle.ID(),
			command:         info.Command,
			state:           state,
			exitCode:        exitCode,
			outputPreview:   preview,
			outputTruncated: truncated,
		}:
			app.wakeRuntimeAsync()
		default:
			logDebug("[bgProcessCallback] updateQueue full, dropping completion for %s", handle.ID())
		}
	})
	bgProcessManager.SetErrorCallback(func(handle bgprocess.ProcessHandle, info bgprocess.ProcessInfo, err error) {
		exitCode := -1
		if info.ExitCode != nil {
			exitCode = *info.ExitCode
		}
		// Error callback has no ProcessResult — surface the error text as the
		// preview so the agent sees *why* it failed without a follow-up call.
		var preview string
		if err != nil {
			preview, _ = bgOutputPreview([]byte(err.Error()))
		}
		select {
		case app.updateQueue <- bgProcessDoneMsg{
			processID:     handle.ID(),
			command:       info.Command,
			state:         "failed",
			exitCode:      exitCode,
			outputPreview: preview,
		}:
			app.wakeRuntimeAsync()
		default:
			logDebug("[bgProcessCallback] updateQueue full, dropping error for %s", handle.ID())
		}
	})

	// Wire background agent completion callback so the TUI injects system
	// messages when background sub-agents finish.
	if sdk != nil {
		if bgAgentMgr := sdk.GetSDKBackgroundAgentManager(); bgAgentMgr != nil {
			bgAgentMgr.SetCompletionCallback(func(agentID, task, status, outputFile, errMsg, resultText string) {
				preview, truncated := bgOutputPreview([]byte(resultText))
				select {
				case app.updateQueue <- bgAgentDoneMsg{
					agentID:         agentID,
					task:            task,
					status:          status,
					outputFilePath:  outputFile,
					errorMessage:    errMsg,
					resultPreview:   preview,
					resultTruncated: truncated,
				}:
					app.wakeRuntimeAsync()
				default:
					logDebug("[bgAgentCallback] updateQueue full, dropping completion for agent %s", agentID)
				}
			})
		}
	}

	// Initialize mention autocomplete with workspace root from SDK
	if sdk != nil {
		workspaceRoot = sdk.WorkspaceRoot()
	}
	app.mentionAutocomplete = NewMentionAutocomplete(workspaceRoot)
	app.mentionAutocomplete.SetSize(80, 10)
	app.operatingMode = "off" // Default to OFF mode (no filtering)

	// Initialize git helper for branch-based conversations
	app.gitHelper = NewGitHelper(workspaceRoot)
	app.branchFilter = FilterAll // Default to showing all conversations

	// Backfill git branch on the startup conversation now that gitHelper is ready.
	if app.activeConv != nil && app.activeConv.Branch == "" && app.gitHelper != nil && app.gitHelper.IsRepo() {
		if branch, err := app.gitHelper.CurrentBranch(); err == nil {
			app.activeConv.Branch = branch
		}
	}

	// Initialize tool collapse management components
	app.collapseManager = NewCollapseManager()
	app.toolNameResolver = NewMCPToolNameResolver()
	app.collapseWidget = NewDefaultCollapseWidget()
	app.toolResultParser = NewToolResultParser()

	// Initialize unified tool render registry (replaces ClassifyTool switch)
	app.toolRegistry = toolrender.NewRegistry()
	app.toolRegistry.Register(bashrender.New())
	app.toolRegistry.Register(readrender.New())
	app.toolRegistry.Register(writerender.New())
	app.toolRegistry.Register(editrender.New())
	app.toolRegistry.Register(patchrender.New())
	app.toolRegistry.Register(greprender.New())
	app.toolRegistry.Register(historyrender.New())
	app.toolRegistry.Register(websearchrender.New())
	app.toolRegistry.Register(todorender.New())
	app.toolRegistry.Register(subagentrender.New())
	app.toolRegistry.Register(readbgrender.New())
	app.toolRegistry.SetFallback(genericrender.New())

	// Register todo manager bridge for rendering
	SetGlobalTodoManagerForRender(func() []TodoItemForRender {
		todoManager := ii.GetTodoManager()
		if todoManager == nil {
			return nil
		}
		todos := todoManager.Todos()
		items := make([]TodoItemForRender, len(todos))
		for i, t := range todos {
			items[i] = TodoItemForRender{
				ID:        t.ID,
				Content:   t.Content,
				Status:    string(t.Status),
				Priority:  string(t.Priority),
				DependsOn: t.DependsOn,
				Blocks:    t.Blocks,
			}
		}
		return items
	})

	// Pre-allocate sub-agent styles (avoids 90+ allocs/sec during streaming)
	app.subAgentStyles = newSubAgentRenderStyles(app.theme)
	app.subAgentRenderer = NewSubAgentRenderer(120, app.subAgentStyles) // Default width, updated on resize
	app.subAgentTable = NewSubAgentTable()

	// Register progress callback with TodoManager to receive sub-agent todo updates
	if tm := ii.GetTodoManager(); tm != nil {
		tm.SetProgressCallback(func(ownerID string, completed, total int, current string) {
			// Update the sub-agent table with progress
			if app.subAgentTable != nil {
				app.subAgentTable.UpdateProgress(ownerID, completed, total, current)
				app.invalidateViewportCache()
			}
		})
	}

	// Initialize settings manager with cache provider
	var cacheProvider settings.CacheStatsProvider
	if sdk != nil && sdk.cacheManager != nil {
		cacheProvider = sdk.cacheManager
	}
	app.settingsManager = settings.NewManager(
		providerDisplay,
		modelDisplay,
		renderSettings.ShowThinking,
		renderSettings.ShowFullToolOutput,
		renderSettings.RichAnimations,
		renderSettings.CopySelectionShortcut,
		renderSettings.AutoCopySelectionOnMouse,
		renderSettings.SpinnerType,
		renderSettings.StartupView,
		cacheProvider,
	)

	// Load persisted settings (e.g. compaction model chain).  Log the error but
	// continue — subsystems fall back to compile-time defaults on failure.
	if err := app.settingsManager.LoadSettings(); err != nil {
		logDebug("⚠ settings.LoadSettings: %v", err)
	}

	// Initialize config bundle integration for unified global/project config management
	configBundleCtx := context.Background()
	configBundle, err := commands.NewConfigBundleIntegration(configBundleCtx, commands.ConfigBundleIntegrationOptions{
		WorkDir: "", // Will be set when needed
	})
	if err != nil {
		logDebug("⚠ ConfigBundleIntegration initialization failed: %v", err)
	} else {
		app.configBundle = configBundle
		// Register callback to update TabBar when config source changes
		// (tabBar will be checked for nil in the callback)
		configBundle.OnSourceChange(func(source, name string) {
			if app.tabBar != nil {
				if source == "project" {
					app.tabBar.SetConfigSource("📦 " + name)
				} else {
					app.tabBar.SetConfigSource("🌍 Global")
				}
			}
		})
		// NOTE: Don't set tabBar config here - tabBar not yet initialized
		// The callback will be called when tabBar is created
		// Connect config bundle to settings manager
		if app.settingsManager != nil {
			app.settingsManager.SetConfigSourceProvider(configBundle)
		}
	}

	// Initialize shared tab bar
	app.tabBar = &appshell.TabBar{
		Brand: "swarm",
		Tabs: []appshell.Tab{
			{ID: "prompt", Label: i18n.T("residual.init.tab_prompt")},
			{ID: "history", Label: i18n.T("residual.init.tab_history")},
			{ID: "usage", Label: i18n.T("residual.init.tab_usage")},
			{ID: "settings", Label: i18n.T("residual.init.tab_settings")},
		},
		ActiveIdx: 0,
		Theme: appshell.TabBarTheme{
			Primary:    app.theme.Primary,
			PrimaryDim: app.theme.PrimaryDim,
			Text:       app.theme.Text,
			TextMuted:  app.theme.TextMuted,
			BG:         app.theme.BG,
		},
	}

	// Now set initial config source on TabBar
	if app.configBundle != nil {
		if app.configBundle.IsUsingProject() {
			app.tabBar.SetConfigSource("📦 " + app.configBundle.ProjectName())
		} else if app.configBundle.HasProjectConfig() {
			app.tabBar.SetConfigSource("🌍 Global")
		}
	}

	// Set up settings callbacks
	app.settingsManager.SetCallbacks(
		// Model change callback
		func() tea.Cmd {
			if modelCmd, ok := cmdReg.Get("model"); ok {
				modelCmd.Execute([]string{})
				if modelCmd.IsInteractive() {
					app.activeCommand = modelCmd
					app.cmdAutocomplete.Hide()
					app.mentionAutocomplete.Hide()
				}
			}
			return nil
		},
		// Auth change callback
		func() tea.Cmd {
			if authCmd, ok := cmdReg.Get("auth"); ok {
				authCmd.Execute([]string{})
				if authCmd.IsInteractive() {
					app.activeCommand = authCmd
					app.cmdAutocomplete.Hide()
					app.mentionAutocomplete.Hide()
				}
			}
			return nil
		},
		// Render change callback
		func() tea.Cmd {
			if renderCmd, ok := cmdReg.Get("render"); ok {
				renderCmd.Execute([]string{})
				if renderCmd.IsInteractive() {
					app.activeCommand = renderCmd
					app.cmdAutocomplete.Hide()
					app.mentionAutocomplete.Hide()
				}
			}
			return nil
		},
	)

	// Set spinner change callback (updates UI only, persistence handled by display sync callback)
	app.settingsManager.SetSpinnerChangeCallback(func(spinnerType string) tea.Cmd {
		// Update the spinner type in UI
		app.spinner.SetType(GetSpinnerTypeFromString(spinnerType))
		// Update in-memory settings (persistence done by onDisplaySettingsSync)
		if app.renderSettings != nil {
			app.renderSettings.SpinnerType = spinnerType
		}
		return nil
	})

	// Set display settings sync callback to persist display settings changes
	app.settingsManager.SetDisplaySettingsSyncCallback(func(showThinking, showFullToolOutput, richAnimations, copySelectionShortcut, autoCopySelectionOnMouse bool) tea.Cmd {
		if app.renderSettings != nil {
			app.renderSettings.ShowThinking = showThinking
			app.renderSettings.ShowFullToolOutput = showFullToolOutput
			app.renderSettings.RichAnimations = richAnimations
			app.renderSettings.CopySelectionShortcut = copySelectionShortcut
			app.renderSettings.AutoCopySelectionOnMouse = autoCopySelectionOnMouse
			if err := SaveRenderSettings(app.renderSettings); err != nil {
				logDebug("[DISPLAY] Failed to save render settings: %v", err)
			}
		}
		// Sync display toggle to app state and refresh viewport
		app.showThinking = showThinking
		app.invalidateViewportCache()
		app.updateViewportContent()
		return nil
	})

	// Set startup view change callback — persists the preference whenever the user toggles it.
	app.settingsManager.SetStartupViewChangeCallback(func(startupView string) tea.Cmd {
		if app.renderSettings != nil {
			app.renderSettings.StartupView = startupView
			if err := SaveRenderSettings(app.renderSettings); err != nil {
				logDebug("[DISPLAY] Failed to save startup view setting: %v", err)
			}
		}
		return nil
	})

	// Set theme change callback — applies the new palette immediately and persists it.
	app.settingsManager.SetThemeChangeCallback(func(themeName, backgroundMode string) tea.Cmd {
		// Apply the new theme to the live app
		newTheme := ThemeByName(themeName)
		if backgroundMode == "none" {
			// Clear ALL background fields so every surface (sidebar, panels, modals)
			// goes transparent — lipgloss.Color("") = no background / terminal default.
			// applyBackground() also no-ops when bgColor == "".
			newTheme.BG = ""
			newTheme.BGLight = ""
			newTheme.BGLighter = ""
		}
		app.theme = AdaptThemeForTerminal(newTheme, app.hasDarkTerminal)
		// Propagate new theme colours to every component that caches them
		app.propagateTheme(app.theme)
		// Invalidate all render caches so next frame picks up new colours
		if app.messageCache != nil {
			app.messageCache.Clear()
		}
		// Persist the selection
		if app.renderSettings != nil {
			app.renderSettings.ThemeName = themeName
			app.renderSettings.BackgroundMode = backgroundMode
			if err := SaveRenderSettings(app.renderSettings); err != nil {
				logDebug("[THEME] Failed to save render settings: %v", err)
			}
		}
		return nil
	})

	// Restore persisted theme on startup
	if renderSettings.ThemeName != "" {
		app.settingsManager.SetTheme(renderSettings.ThemeName, renderSettings.BackgroundMode)
		newTheme := ThemeByName(renderSettings.ThemeName)
		if renderSettings.BackgroundMode == "none" {
			newTheme.BG = ""
			newTheme.BGLight = ""
			newTheme.BGLighter = ""
		}
		app.theme = AdaptThemeForTerminal(newTheme, app.hasDarkTerminal)
		app.propagateTheme(app.theme)
		if app.messageCache != nil {
			app.messageCache.Clear()
		}
	}

	// CRITICAL: Sync SDK thinking state with render settings on initialization
	if sdk != nil && renderSettings.ShowThinking {
		sdk.EnableThinking(renderSettings.ThinkingBudget)
		logDebug("SDK thinking enabled from render settings (budget: %d)", renderSettings.ThinkingBudget)
	}

	// Set up model command callback
	if modelCmd, ok := cmdReg.Get("model"); ok {
		if mc, ok := modelCmd.(*commands.ModelCommand); ok {
			// Ensure initial sync with current provider/model
			mc.SetCurrent(currentProvider, currentModel)
			var plexusSyncMu sync.Mutex
			var lastPlexusCatalogSync time.Time
			var plexusSyncInFlight bool
			mc.SetBeforeOpen(func() tea.Cmd {
				plexusSyncMu.Lock()
				if plexusSyncInFlight || (!lastPlexusCatalogSync.IsZero() && time.Since(lastPlexusCatalogSync) < time.Minute) {
					plexusSyncMu.Unlock()
					return nil
				}
				plexusSyncInFlight = true
				plexusSyncMu.Unlock()
				return func() tea.Msg {
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					err := syncPlexusCatalog(ctx)
					plexusSyncMu.Lock()
					plexusSyncInFlight = false
					if err == nil {
						lastPlexusCatalogSync = time.Now()
					}
					plexusSyncMu.Unlock()
					if err != nil {
						logDebug("Plexus alias auto-sync skipped: %v", err)
					}
					return commands.ProviderCatalogRefreshedMsg{Provider: "plexus", Err: err}
				}
			})
			mc.SetOnSelect(app.applyModelSelection)
		}
	}

	// Set up auth command callback
	if authCmd, ok := cmdReg.Get("auth"); ok {
		if ac, ok := authCmd.(*commands.AuthCommand); ok {
			ac.SetOnComplete(func(provider string) error {
				if app.sdk != nil && app.sdk.harnessGoverned() {
					app.addNotification("error", i18n.T("residual.init.harness_auth_disabled"))
					return fmt.Errorf("authentication is disabled while the session is governed by a harness")
				}
				candidateProvider := "ClaudeCode"
				candidateModel := "claude-opus-4-20250514"
				candidateProviderDisplay := "Claude Code (OAuth)"
				candidateModelDisplay := "Claude 4 Opus"
				switch strings.ToLower(provider) {
				case "openai", "codex":
					candidateProvider = "codex"
					candidateModel = "gpt-5.6-terra"
					candidateProviderDisplay = "OpenAI (OAuth)"
					candidateModelDisplay = "GPT-5.6 Terra"
				}
				logDebug("OAuth login successful for %s, token stored", provider)

				// Initialize SDK if it wasn't created yet (e.g., no creds at startup)
				sdkCreated := false
				if app.sdk == nil {
					maxTokens := 31999 // Default (must be under 32000 for Opus 4)
					if app.settingsManager != nil && app.settingsManager.GetAdvancedSettings() != nil {
						maxTokens = app.settingsManager.GetAdvancedSettings().GetMaxTokens()
					}
					// Get auto-compaction settings
					var autoCompactCfg *agent.AutoCompactionConfig
					if app.settingsManager != nil {
						compactionSettings := app.settingsManager.GetCompactionSettings()
						if compactionSettings != nil {
							autoCompactCfg = buildAutoCompactionConfig(
								compactionSettings,
								candidateProvider,
								candidateModel,
							)
							// Same 80%-default safety net as the startup path above.
							autoCompactCfg = applyDefaultCompactionThreshold(autoCompactCfg)
						}
					}
					// Extract Exa API key from provider configs for web search tool
					var exaAPIKey string
					if cfgMgr, err := commands.NewConfigManager(); err == nil {
						if providers, err := cfgMgr.LoadProviders(); err == nil {
							for _, p := range providers {
								if p.APIType == "exa" && p.APIKey != "" {
									exaAPIKey = p.APIKey
									break
								}
							}
						}
					}
					sdk, err := newSDKIntegrationForAppOptions(candidateProvider, candidateModel, app.appOptions, SDKIntegrationOptions{
						MaxTokens:            maxTokens,
						DebugMode:            app.debugMode,
						BGProcessManager:     app.bgProcessManager,
						PermissionConfig:     app.permissionConfig,
						ApprovalBroker:       app.permissionBroker,
						QuestionBroker:       app.questionBroker,
						VaultUnlocker:        app.vaultUnlockBroker,
						AutoCompactionConfig: autoCompactCfg,
						PlanBroker:           app.planBroker,
						RootSessionID:        app.rootSessionID,
						ExaAPIKey:            exaAPIKey,
						ProfileManager:       settings.NewProfileManager(), // Pass ProfileManager for role-based model selection
						A2AEnabled:           app.a2aEnabled,
						A2AHandle:            app.a2aHandle,
						A2AListenAddress:     app.a2aListenAddress,
						EnableCodeMode:       enableCodeMode,
						Hub:                  app.hub,
					})
					if err != nil {
						logDebug("Failed to initialize SDK after OAuth login: %v", err)
						app.sdkInitError = err.Error()
						app.addNotification("error", i18n.T("residual.init.sdk_unavailable_after_auth", err))
						return err
					} else {
						app.sdk = sdk
						sdkCreated = true
						if app.settingsManager != nil {
							if vaultSettings := app.settingsManager.GetVaultSettings(); vaultSettings != nil {
								vaultSettings.SetProjectPath(app.sdk.WorkspaceRoot())
								if vaultSettings.IsUnlocked() {
									app.sdk.SetVaultProvider(vaultSettings.CreateVaultProvider(""))
								}
							}
						}
						app.configureA2ABridge()
						logDebug("SDK initialized after OAuth login for %s (max tokens: %d)", provider, maxTokens)
						app.sdkInitError = ""
						app.clearNotifications("")

						// Wire background agent completion callback after OAuth SDK init
						if bgAgentMgr := sdk.GetSDKBackgroundAgentManager(); bgAgentMgr != nil {
							bgAgentMgr.SetCompletionCallback(func(agentID, task, status, outputFile, errMsg, resultText string) {
								preview, truncated := bgOutputPreview([]byte(resultText))
								select {
								case app.updateQueue <- bgAgentDoneMsg{
									agentID:         agentID,
									task:            task,
									status:          status,
									outputFilePath:  outputFile,
									errorMessage:    errMsg,
									resultPreview:   preview,
									resultTruncated: truncated,
								}:
									app.wakeRuntimeAsync()
								default:
									logDebug("[bgAgentCallback] updateQueue full, dropping completion for agent %s", agentID)
								}
							})
						}

						// Wire skills manager after OAuth SDK init
						if sdk.skillsManager != nil {
							skillsSettings := app.settingsManager.GetSkillsSettings()
							if skillsSettings != nil {
								skillsSettings.SetLoader(sdk.skillsManager.GetLoader())
							}
							if skillCmd, ok := cmdReg.Get("skill"); ok {
								if sc, ok := skillCmd.(*commands.SkillCommand); ok {
									sc.SetLoader(sdk.skillsManager.GetLoader())
								}
							}
							logDebug("Skills manager wired after OAuth init")
						}

						// Wire plugins manager after OAuth SDK init
						if sdk.pluginsManager != nil {
							pluginsSettings := app.settingsManager.GetPluginsSettings()
							if pluginsSettings != nil {
								pluginsSettings.SetLoader(sdk.pluginsManager.GetLoader())
								pluginsSettings.SetMarketplace(sdk.pluginsManager.GetMarketplace())
								pluginsSettings.SetSearcher(sdk.pluginsManager.GetSearcher())
								// Set plugin action callbacks
								pluginsSettings.SetCallbacks(
									func(name string, enabled bool) error {
										// Toggle callback
										if enabled {
											return sdk.pluginsManager.EnablePlugin(name)
										}
										return sdk.pluginsManager.DisablePlugin(name)
									},
									func(name string) error {
										// Install callback
										ctx := context.Background()
										_, err := sdk.pluginsManager.InstallFromMarketplace(ctx, name)
										return err
									},
									func(name string) error {
										// Uninstall callback
										return sdk.pluginsManager.UninstallPlugin(name)
									},
								)
								// Load marketplace in background for quick access
								pluginsSettings.LoadMarketplace()
							}
							if pluginCmd, ok := cmdReg.Get("plugin"); ok {
								if pc, ok := pluginCmd.(*commands.PluginCommand); ok {
									pc.SetLoader(sdk.pluginsManager.GetLoader())
									pc.SetMarketplace(sdk.pluginsManager.GetMarketplace())
									pc.SetSearcher(sdk.pluginsManager.GetSearcher())
								}
							}
							// Wire commands browser
							if commandsCmd, ok := cmdReg.Get("commands"); ok {
								if cb, ok := commandsCmd.(*commands.CommandsBrowser); ok {
									cb.SetRegistry(cmdReg)
									cb.SetPluginsLoader(sdk.pluginsManager.GetLoader())
								}
							}
							// Wire plugin command provider for autocomplete
							app.cmdAutocomplete.SetPluginProvider(sdk.pluginsManager)
							logDebug("Plugins manager wired after OAuth init")
						}

						// Wire mention autocomplete providers after OAuth init
						{
							agentsSettings := app.settingsManager.GetAgentsSettings()
							agentProvider := NewPluginAgentMentionProvider(sdk.pluginsManager, agentsSettings, sdk)
							app.mentionAutocomplete.SetAgentProvider(agentProvider)

							tmuxProvider := NewTmuxSessionMentionProvider()
							app.mentionAutocomplete.SetTmuxProvider(tmuxProvider)
							logDebug("Mention autocomplete providers wired after OAuth init")
						}
					}
				}

				// Reload/switch provider with fresh token
				if app.sdk != nil && !sdkCreated {
					if err := app.sdk.CredentialChanged(candidateProvider, candidateModel); err != nil {
						logDebug("Failed to reload provider after OAuth login: %v", err)
						app.sdkInitError = err.Error()
						app.addNotification("error", fmt.Sprintf("Provider reload failed: %v", err))
						return err
					} else {
						logDebug("Provider reloaded successfully with new OAuth token")
						app.sdkInitError = ""
						app.clearNotifications("")
					}
				}
				cfgMgr, err := commands.NewConfigManager()
				if err != nil {
					return err
				}
				if err := cfgMgr.MarkOAuthProviderAuthenticated(candidateProvider); err != nil {
					return err
				}
				// Commit UI and persisted selection only after replacement
				// construction and metadata persistence both succeed.
				app.currentProvider = candidateProvider
				app.currentModel = candidateModel
				app.currentProviderDisplay = candidateProviderDisplay
				app.currentModelDisplay = candidateModelDisplay
				if err := app.UpdateConfig(func(cfg *core.Config) {
					cfg.CurrentProvider = candidateProvider
					cfg.CurrentModel = candidateModel
				}); err != nil {
					return err
				}
				if modelCmd, ok := cmdReg.Get("model"); ok {
					if mc, ok := modelCmd.(*commands.ModelCommand); ok {
						mc.SetCurrent(candidateProvider, candidateModel)
					}
				}
				logDebug("Saved provider/model to config: %s / %s", candidateProvider, candidateModel)
				oauthPrefix := ""
				if app.sdk != nil && app.sdk.IsOAuth() {
					if strings.ToLower(provider) == "gemini" {
						oauthPrefix = prompts.GetGeminiSystemPrompt(true)
					} else {
						oauthPrefix = "You are Claude Code, Anthropic's official CLI for Claude."
					}
				}
				app.settingsManager.SetOAuthStatus(app.sdk != nil && app.sdk.IsOAuth(), oauthPrefix)
				return nil
			})
			app.settingsManager.SetCredentialChangedCallback(ac.NotifyCredentialChanged)
		}
	}

	// Set up system prompt change callback
	app.settingsManager.SetPromptChangeCallback(func(prompt string) tea.Cmd {
		if app.sdk != nil && app.sdk.harnessGoverned() {
			app.addNotification("error", i18n.T("residual.init.harness_prompt_changes"))
			return nil
		}
		if app.sdk != nil && app.sdk.activeAgent() != nil {
			if app.sdk.IsCodexBacked() {
				// For Codex: the canonical Codex prompt is mandatory; append the user's
				// custom instructions after it so both are present.
				app.sdk.applyUserSystemPromptToAgent(context.Background(), prompt)
				return nil
			}

			// Update SDK agent's system prompt with context injection
			// First set the base prompt
			app.sdk.activeAgent().SetSystemPrompt(prompt)
			logDebug("System prompt updated: %s", prompt[:min(50, len(prompt))]+"...")

			// Then inject context files on top
			if contextSettings := app.settingsManager.GetContextSettings(); contextSettings != nil {
				loader := contextSettings.GetLoader()
				if err := app.sdk.LoadAndInjectContext(loader, prompt); err != nil {
					logDebug("Failed to inject context on prompt change: %v", err)
				}
			}
		}
		return nil
	})

	// Wire the user system prompt getter so that ReloadProvider (model switch,
	// OAuth reload, etc.) always re-applies the user's active prompt selection.
	if app.sdk != nil && app.settingsManager != nil {
		app.sdk.SetUserSystemPromptGetter(func() string {
			return app.settingsManager.GetActiveSystemPrompt()
		})
	}

	// Wire the cron scheduler's PromptSink into the chat session: prompts
	// fired by cron jobs / ScheduleWakeup (/loop, /goal) are delivered to the
	// MAIN agent as chat prompts. Without this, the scheduler spawns invisible
	// background agents and scheduled tasks appear to do nothing in the TUI.
	// sendToRuntime is goroutine-safe, so the scheduler can call this directly.
	if app.sdk != nil {
		app.sdk.SetCronPromptHandler(func(prompt string) {
			app.sendToRuntime(cronPromptInjectMsg{prompt: prompt})
		})
	}

	// Set up agent change callback
	app.settingsManager.SetAgentChangeCallback(func(agentID string) tea.Cmd {
		logDebug("Default agent changed to: %s", agentID)

		// Refresh custom agent definitions in delegate_task tool
		if app.sdk != nil {
			if agentsSettings := app.settingsManager.GetAgentsSettings(); agentsSettings != nil {
				app.sdk.SetCustomAgentDefGetter(func() []*agent.Definition {
					return agentsSettings.GetAllSDKDefinitions()
				})
				logDebug("Refreshed custom agent definitions for delegate_task tool")
			}
		}

		return nil
	})

	// Set up advanced tool mode callback — toggles deferred loading on/off
	if advSettings := app.settingsManager.GetAdvancedSettings(); advSettings != nil {
		advSettings.SetAdvancedToolModeCallback(func(enabled bool) {
			if app.sdk == nil {
				return
			}
			if enabled {
				threshold := advSettings.GetDeferTokenThreshold()
				app.sdk.EnableAdvancedToolMode(threshold)
				app.addNotification("info", i18n.T("residual.init.advanced_tools_enabled", threshold))
				logDebug("Advanced tool mode enabled via settings (threshold=%d)", threshold)
			} else {
				app.sdk.DisableAdvancedToolMode()
				app.addNotification("info", i18n.T("residual.init.advanced_tools_disabled"))
				logDebug("Advanced tool mode disabled via settings")
			}
		})

		// If advanced tool mode was persisted as enabled, activate it on startup
		if advSettings.GetAdvancedToolMode() && app.sdk != nil {
			app.sdk.EnableAdvancedToolMode(advSettings.GetDeferTokenThreshold())
			logDebug("Advanced tool mode restored from config (threshold=%d)", advSettings.GetDeferTokenThreshold())
		}
	}

	// Set up vault auto-load — transparent vault (no password) is loaded first
	if vaultSettings := app.settingsManager.GetVaultSettings(); vaultSettings != nil {
		if app.sdk != nil {
			vaultSettings.SetProjectPath(app.sdk.WorkspaceRoot())
		}
		// Try to auto-load transparent vault (credentials.json) without password
		if vaultSettings.HasTransparentVault() {
			if vaultSettings.LoadTransparentVault() {
				logDebug("Transparent vault auto-loaded: %d credentials available", vaultSettings.GetCredentialCount())
				if app.sdk != nil {
					// Immediately make vault tools available with transparent provider
					app.sdk.SetVaultProvider(vaultSettings.CreateVaultProvider(""))
					app.addNotification("info", i18n.T("residual.init.vault_autoloaded", vaultSettings.GetCredentialCount()))
				}
			}
		}
		// Set up vault unlock callback — also enables vault tools when encrypted vault is unlocked
		vaultSettings.SetOnVaultUnlockCallback(func(provider vault.VaultProvider) {
			if app.sdk != nil {
				app.sdk.SetVaultProvider(provider)
				app.sidePanelCache.valid = false
				if provider != nil {
					logDebug("Vault unlocked: vault tools enabled")
				} else {
					logDebug("Vault locked: vault tools disabled")
				}
			}
		})

		// Register the /vault slash command so the vault can be unlocked from
		// the chat in real time. It shares this same VaultSettings instance and
		// the settings State, so unlocking via /vault triggers the callback
		// above and injects the provider into the live agent.
		if app.cmdRegistry != nil {
			app.cmdRegistry.Register(newVaultCommand(vaultSettings, app.settingsManager.GetState()))
		}
	}

	// profileFileLog writes to /tmp/swarm_profile_debug.log for tracing the profile→model switch flow.
	profileFileLog := func(format string, args ...any) {
		f, err := os.OpenFile("/tmp/swarm_profile_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		msg := fmt.Sprintf(format, args...)
		fmt.Fprintf(f, "[%s][app_init] %s\n", time.Now().Format("15:04:05.000"), msg)
	}

	// Set up profile activation callback — when user presses "d" to make a profile
	// the default in Agent Profiles settings, switch the main chat model to match
	// the new profile's configured "main" role.
	app.settingsManager.SetProfileChangeCallback(func(profileID string) tea.Cmd {
		profileFileLog("callback fired: profileID=%s", profileID)
		logDebug("[Profile] Activated profile: %s — switching main chat model", profileID)
		if app.sdk == nil {
			profileFileLog("sdk is nil, aborting")
			logDebug("[Profile] SDK not initialized, skipping model switch")
			return nil
		}
		if app.sdk.harnessGoverned() {
			app.addNotification("error", i18n.T("residual.init.harness_profile_switch_disabled"))
			return nil
		}

		// 1. Switch the SDK's active profile
		profileFileLog("calling sdk.SwitchProfile(%s)", profileID)
		if err := app.sdk.SwitchProfile(profileID); err != nil {
			profileFileLog("SwitchProfile error: %v", err)
			logDebug("[Profile] SwitchProfile failed: %v", err)
			app.addNotification("error", i18n.T("residual.init.profile_switch_failed", err))
			return nil
		}
		profileFileLog("SwitchProfile OK")

		// 2. Resolve the provider/model for the main chat role
		profileFileLog("calling sdk.GetModelForRole(AliasMain)")
		provider, model, err := app.sdk.GetModelForRole(settings.AliasMain)
		profileFileLog("GetModelForRole returned: provider=%q model=%q err=%v", provider, model, err)
		if err != nil {
			logDebug("[Profile] GetModelForRole(main) failed: %v", err)
			// Profile switched but main role has no model configured — keep current model
			return nil
		}

		// 3. Update app state
		app.currentProvider = provider
		app.currentModel = model
		profileFileLog("app.currentProvider=%s app.currentModel=%s", provider, model)

		// 4. Resolve display names — fall back to raw IDs for custom providers
		// that aren't in the providers list or whose models aren't enumerated.
		app.currentProviderDisplay = provider
		app.currentModelDisplay = model
		if cfgMgr, err2 := commands.NewConfigManager(); err2 == nil {
			if providerConfigs, err2 := cfgMgr.LoadProviders(); err2 == nil {
				profileFileLog("loaded %d provider configs", len(providerConfigs))
				for _, prov := range providerConfigs {
					if prov.Name == provider {
						app.currentProviderDisplay = prov.DisplayName
						for _, m2 := range prov.Models {
							if m2.ID == model {
								app.currentModelDisplay = m2.DisplayName
								break
							}
						}
						break
					}
				}
			} else {
				profileFileLog("LoadProviders error: %v", err2)
			}
		}
		profileFileLog("display: providerDisplay=%q modelDisplay=%q", app.currentProviderDisplay, app.currentModelDisplay)

		// 5. Switch the SDK provider/model
		profileFileLog("calling sdk.SwitchProvider(%s, %s)", provider, model)
		if err := app.sdk.SwitchProvider(provider, model); err != nil {
			profileFileLog("SwitchProvider error: %v", err)
			logDebug("[Profile] SwitchProvider(%s, %s) failed: %v", provider, model, err)
			app.addNotification("error", i18n.T("residual.init.model_switch_failed", err))
		} else {
			profileFileLog("SwitchProvider OK — context window: %d", app.sdk.GetModelContextWindow())
			logDebug("[Profile] Switched to provider=%s model=%s via profile", provider, model)
			app.modelContextWindow = app.sdk.GetModelContextWindow()
			app.addNotification("success", i18n.T("residual.init.model_switched", provider, model))
		}

		// 6. Persist the new provider/model selection
		_ = app.UpdateConfig(func(cfg *core.Config) {
			cfg.CurrentProvider = app.currentProvider
			cfg.CurrentModel = app.currentModel
		})
		profileFileLog("persisted provider/model to config")

		// 7. Keep model command state in sync
		if modelCmd, ok := cmdReg.Get("model"); ok {
			if mc, ok := modelCmd.(*commands.ModelCommand); ok {
				mc.SetCurrent(app.currentProvider, app.currentModel)
				profileFileLog("model command updated")
			}
		}

		// 8. Update metrics context
		if app.metrics != nil {
			app.metrics.SetCurrentModel(app.currentModel, app.currentModelDisplay)
			profileFileLog("metrics updated")
		}

		// 9. Sync the Model settings screen so it reflects the new provider/model
		if ms := app.settingsManager.GetModelSettings(); ms != nil {
			ms.SyncCurrent(app.currentProvider, app.currentModel)
			profileFileLog("model settings screen synced")
		}

		profileFileLog("callback complete")
		return nil
	})

	// Show a toast notification whenever profile roles are saved (Ctrl+S / s).
	// This fires for both active and non-active profiles so the user always
	// gets visual confirmation that the disk write succeeded.
	// Returns a tea.Cmd (not direct addNotification) so it goes through the
	// BubbleTea message pipeline and is guaranteed to trigger a redraw.
	app.settingsManager.SetProfileSavedCallback(func(profileID, profileName string) tea.Cmd {
		profileFileLog("profile saved toast: id=%s name=%s", profileID, profileName)
		msg := i18n.T("residual.init.profile_saved", profileName)
		return func() tea.Msg {
			return notificationMsg{level: "success", message: msg}
		}
	})

	// Set up proxy change callback to reload provider when proxy settings change
	app.settingsManager.SetProxyCallback(func() tea.Cmd {
		if app.sdk != nil && app.sdk.harnessGoverned() {
			app.addNotification("error", i18n.T("residual.init.harness_provider_reload_disabled"))
			return nil
		}
		logDebug("Proxy settings changed, reloading provider")
		if app.sdk != nil {
			if err := app.sdk.ReloadProvider(); err != nil {
				logDebug("Failed to reload provider after proxy change: %v", err)
				app.addNotification("error", i18n.T("residual.init.provider_reload_failed", err))
			} else {
				app.addNotification("success", i18n.T("residual.init.provider_reloaded_proxy"))
			}
		}
		return nil
	})

	// Set up model select callback for settings page
	// CRITICAL: This ensures the SDK is notified when a model is selected via settings
	// Without this, the SDK would continue using the old provider/model
	if modelSettings := app.settingsManager.GetModelSettings(); modelSettings != nil {
		modelSettings.SetOnModelSelect(func(provider, model string) {
			logDebug("Model selected via settings: provider=%s model=%s", provider, model)
			if app.sdk != nil && app.sdk.harnessGoverned() {
				app.addNotification("error", i18n.T("residual.init.harness_model_switch_disabled"))
				return
			}

			// Update app state
			app.currentProvider = provider
			app.currentModel = model

			// Resolve display names
			if providers, err := commands.NewConfigManager(); err == nil {
				if providerConfigs, err := providers.LoadProviders(); err == nil {
					for _, prov := range providerConfigs {
						if prov.Name == provider {
							app.currentProviderDisplay = prov.DisplayName
							for _, m := range prov.Models {
								if m.ID == model {
									app.currentModelDisplay = m.DisplayName
									break
								}
							}
							break
						}
					}
				}
			}

			// Update SDK
			if app.sdk != nil {
				if err := app.sdk.SwitchProvider(provider, model); err != nil {
					logDebug("Failed to switch provider via settings: %v", err)
					app.addNotification("error", i18n.T("residual.init.provider_switch_failed", err))
				} else {
					logDebug("SDK switched to provider=%s model=%s via settings", provider, model)
					app.modelContextWindow = app.sdk.GetModelContextWindow()

					// Update OAuth status and prefix for system prompt
					isOAuth := app.sdk.IsOAuth()
					oauthPrefix := ""
					if isOAuth {
						if strings.ToLower(provider) == "gemini" {
							oauthPrefix = prompts.GetGeminiSystemPrompt(true)
						} else {
							oauthPrefix = "You are Claude Code, Anthropic's official CLI for Claude."
						}
					}
					app.settingsManager.SetOAuthStatus(isOAuth, oauthPrefix)
				}
			}

			// Update model command state
			if modelCmd, ok := cmdReg.Get("model"); ok {
				if mc, ok := modelCmd.(*commands.ModelCommand); ok {
					mc.SetCurrent(provider, model)
				}
			}
		})

		modelSettings.SetOnReasoningEffortChange(func(effort string) {
			logDebug("Reasoning effort selected via settings: effort=%s", effort)
			if app.sdk != nil && app.sdk.harnessGoverned() {
				app.addNotification("error", i18n.T("residual.init.harness_reasoning_settings"))
				return
			}
			if app.sdk != nil {
				app.sdk.SetReasoningEffort(effort)
			}
		})

		modelSettings.SetOnModelConfigChange(func(providerName string, model commands.ModelConfig) {
			if app.sdk == nil || app.sdk.harnessGoverned() {
				return
			}
			if !strings.EqualFold(app.sdk.GetProviderName(), providerName) ||
				!strings.EqualFold(app.currentModel, model.ID) {
				return
			}
			app.sdk.ApplyProviderModelConfig(model)
			logDebug("Applied live model generation settings: provider=%s model=%s", providerName, model.ID)
		})

		// Rebuild the live provider client when its credentials are edited so
		// new API keys / base URLs take effect without a TUI restart.
		modelSettings.SetOnCredentialsChanged(func(providerName string) {
			if app.sdk == nil {
				return
			}
			if app.sdk.harnessGoverned() {
				app.addNotification("error", i18n.T("residual.init.harness_provider_credentials"))
				return
			}
			if !strings.EqualFold(app.sdk.GetProviderName(), providerName) {
				logDebug("Credentials changed for %s but current provider is %s; skipping live reload", providerName, app.sdk.GetProviderName())
				return
			}
			if err := app.sdk.ReloadProvider(); err != nil {
				logDebug("Failed to reload provider %s after credentials change: %v", providerName, err)
				app.addNotification("error", i18n.T("residual.init.provider_reload_failed", err))
				return
			}
			logDebug("Provider %s reloaded with new credentials", providerName)
		})

		if app.sdk != nil && !app.sdk.harnessGoverned() {
			app.sdk.SetReasoningEffort(modelSettings.GetReasoningEffort())
		}

		// Set up OpenRouter refresh callback to populate models from API
		modelSettings.SetOpenRouterRefreshCallback(RefreshOpenRouterModels)
		// Set up Cursor refresh callback to populate models from AvailableModels
		modelSettings.SetCursorRefreshCallback(RefreshCursorModels)
		// Set up Anthropic refresh callback to populate Claude models from the
		// live /v1/models catalog using the stored Claude Code OAuth token.
		modelSettings.SetAnthropicRefreshCallback(RefreshAnthropicOAuthModels)
		// Set up Codex refresh callback to populate OpenAI models from the
		// ChatGPT backend /models catalog using the stored OpenAI OAuth token.
		modelSettings.SetCodexRefreshCallback(RefreshCodexModels)
	}

	// Set OAuth status for system prompt preview
	if sdk != nil && sdk.provider != nil {
		// Use SDK's OAuth tracking which was set during provider initialization
		isOAuth := sdk.IsOAuth()
		oauthPrefix := ""
		if isOAuth {
			if strings.ToLower(currentProvider) == "gemini" {
				oauthPrefix = prompts.GetGeminiSystemPrompt(true)
			} else {
				oauthPrefix = "You are Claude Code, Anthropic's official CLI for Claude."
			}
		}
		app.settingsManager.SetOAuthStatus(isOAuth, oauthPrefix)
		logDebug("OAuth status set for system prompts: isOAuth=%v, provider=%s", isOAuth, currentProvider)

		// Apply the user's active system prompt on startup.
		// For Codex: applyUserSystemPromptToAgent merges the canonical Codex instructions
		// with the user's custom prompt (so both are present).
		// For all other providers: set the prompt directly, injecting context files on top.
		if activePrompt := app.settingsManager.GetActiveSystemPrompt(); activePrompt != "" && !sdk.harnessGoverned() {
			if sdk != nil && sdk.activeAgent() != nil {
				if sdk.IsCodexBacked() {
					sdk.applyUserSystemPromptToAgent(context.Background(), activePrompt)
					logDebug("Applied user system prompt to Codex agent on startup: %d chars", len(activePrompt))
				} else {
					sdk.activeAgent().SetSystemPrompt(activePrompt)
					logDebug("Applied initial system prompt from settings: %d chars", len(activePrompt))

					// Load and inject context files (CLAUDE.md, SWARM.md, etc.)
					if contextSettings := app.settingsManager.GetContextSettings(); contextSettings != nil {
						loader := contextSettings.GetLoader()
						if err := sdk.LoadAndInjectContext(loader, activePrompt); err != nil {
							logDebug("Failed to inject context: %v", err)
						} else {
							logDebug("Context files injected into system prompt")
						}
					}
				}
			}
		}
	}

	// Initialize hooks assistant for AI-powered hook management
	if sdk != nil && sdk.provider != nil && sdk.hooksManager != nil {
		// Load hooks config from multiple sources (project + user + system)
		hooksConfig, err := LoadHooksConfigWithProject(sdk.WorkspaceRoot())
		if err != nil {
			logDebug("Failed to load hooks config: %v", err)
			hooksConfig = NewHooksConfig()
		}
		app.hooksConfig = hooksConfig

		// Defer enabled custom hook registration until UI is running.
		app.hooksRegistrationPending = true

		// Create hook tools
		hookTools := NewHookTools(hooksConfig, sdk.hooksManager, sdk.WorkspaceRoot(), sdk.toolRegistry)

		// Detect OAuth for system prompt - use SDK's OAuth tracking
		oauthPrefix := ""
		if sdk.IsOAuth() {
			if strings.ToLower(currentProvider) == "gemini" {
				oauthPrefix = prompts.GetGeminiSystemPrompt(true)
			} else {
				oauthPrefix = "You are Claude Code, Anthropic's official CLI for Claude."
			}
		}

		// Create hooks assistant that reuses SDK's provider (same OAuth config as main chat)
		app.hooksAssistant = NewHooksAssistant(sdk, hookTools, oauthPrefix)
		if app.hooksAssistant != nil && app.hooksAssistant.IsInitialized() {
			logDebug("Hooks assistant initialized with tools: %d", app.hooksAssistant.GetToolCount())
		} else {
			logDebug("Failed to initialize hooks assistant")
		}

		// Set up chat callback for hooks settings
		hooksSettings := app.settingsManager.GetHooksSettings()
		if hooksSettings != nil && app.hooksAssistant != nil {
			hooksSettings.SetOnChatSend(func(message string) {
				// Handle chat message in a goroutine to not block UI
				go func() {
					ctx := context.Background()
					if app.hooksAssistant == nil || !app.hooksAssistant.IsInitialized() {
						hooksSettings.AddChatMessage("system", "Hooks assistant not available. Please check your provider configuration.")
						state := app.settingsManager.GetState()
						state.HooksChatWaiting = false
						app.updateQueue <- HooksChatResponseMsg{}
						return
					}
					response, err := app.hooksAssistant.Chat(ctx, message)
					if err != nil {
						errorMsg := app.hooksAssistant.GetLastError(err)
						hooksSettings.AddChatMessage("system", fmt.Sprintf("Error: %s", errorMsg))
					} else {
						hooksSettings.AddChatMessage("assistant", response)
					}
					// Reload hooks from disk in case the AI created/modified any
					if reloadErr := hooksSettings.Reload(); reloadErr != nil {
						logDebug("Failed to reload hooks: %v", reloadErr)
					}
					// Clear waiting state
					state := app.settingsManager.GetState()
					state.HooksChatWaiting = false
					// Send update to trigger re-render (via updateQueue)
					app.updateQueue <- HooksChatResponseMsg{}
				}()
			})
			logDebug("Hooks settings chat callback configured")

			// Set up callback for LIVE hook enable/disable (no reload needed)
			hooksSettings.SetOnHookChange(func(name string, enabled bool) error {
				logDebug("[Hooks] LIVE toggle: name=%s, enabled=%v", name, enabled)

				// Check if it's a built-in hook
				builtinHooks := map[string]string{
					"Logging": "logging",
					"Metrics": "metrics",
					"Audit":   "audit",
					"Tracing": "tracing",
				}

				if builtinName, isBuiltin := builtinHooks[name]; isBuiltin {
					// Built-in hook - use HooksManager's EnableHook/DisableHook
					if enabled {
						if err := app.sdk.hooksManager.EnableHook(builtinName); err != nil {
							logDebug("[Hooks] Failed to enable built-in hook %s: %v", name, err)
							return err
						}
						logDebug("[Hooks] Enabled built-in hook: %s", name)
					} else {
						if err := app.sdk.hooksManager.DisableHook(builtinName); err != nil {
							logDebug("[Hooks] Failed to disable built-in hook %s: %v", name, err)
							return err
						}
						logDebug("[Hooks] Disabled built-in hook: %s", name)
					}
				} else {
					// Custom hook - update config and register/unregister
					if app.hooksConfig != nil {
						// Update in hooksConfig and save
						if err := app.hooksConfig.SetEnabled(name, enabled); err != nil {
							logDebug("[Hooks] Failed to set enabled for custom hook %s: %v", name, err)
							// Hook might be in settings/hooks.go format but not in hooks_config
							// This is OK, we'll try to register anyway
						} else {
							if err := app.hooksConfig.Save(); err != nil {
								logDebug("[Hooks] Failed to save hooks config for %s: %v", name, err)
							}
						}

						if enabled {
							if !app.sdk.HasPermission(tools.PermissionHookManage) {
								return fmt.Errorf("hook permissions are denied")
							}
							// Get the hook config and register with HooksManager
							hookConfig, err := app.hooksConfig.GetHook(name)
							if err == nil && hookConfig != nil {
								shellHook, err := hookConfig.ToShellHook()
								if err != nil {
									logDebug("[Hooks] Failed to create shell hook %s: %v", name, err)
									return err
								}
								if err := app.sdk.hooksManager.RegisterCustomHook(shellHook); err != nil {
									logDebug("[Hooks] Failed to register custom hook %s: %v", name, err)
									return err
								}
								logDebug("[Hooks] Registered custom hook: %s (events: %v)", name, hookConfig.EventPatterns)
							} else {
								logDebug("[Hooks] Hook %s not found in hooksConfig, may be in legacy format", name)
							}
						} else {
							// Unregister the hook
							if err := app.sdk.hooksManager.UnregisterCustomHook(name); err != nil {
								logDebug("[Hooks] Failed to unregister custom hook %s: %v", name, err)
								// Not critical - hook might not have been registered
							} else {
								logDebug("[Hooks] Unregistered custom hook: %s", name)
							}
						}
					}
				}
				return nil
			})
			logDebug("Hooks settings LIVE toggle callback configured")

			hooksSettings.SetOnHookPermissionChange(func(name string, policy string) error {
				logDebug("[Hooks] Permission toggle: name=%s, policy=%s", name, policy)

				if !app.sdk.HasPermission(tools.PermissionHookManage) {
					return fmt.Errorf("hook permissions are denied")
				}

				normalized := hooks.NormalizeHookPermissionPolicy(hooks.HookPermissionPolicy(policy))
				if app.hooksConfig != nil {
					if err := app.hooksConfig.SetPermissionPolicy(name, normalized); err != nil {
						logDebug("[Hooks] Failed to update permission for %s: %v", name, err)
					} else if err := app.hooksConfig.Save(); err != nil {
						logDebug("[Hooks] Failed to save hooks config for %s: %v", name, err)
					}
				}

				if app.sdk.hooksManager != nil {
					if err := app.sdk.hooksManager.SetHookPermissionPolicy(name, normalized); err != nil {
						logDebug("[Hooks] Failed to update manager permission for %s: %v", name, err)
					}
				}

				return nil
			})
			logDebug("Hooks settings permission callback configured")

			// Wire up Swarm Agents model selection (Steering)
			if mp := app.settingsManager.GetModelProfilesSection(); mp != nil {
				mp.SetSwarmAgentsCallback(func(steeringProvider, steeringModel string) {
					logDebug("[SwarmAgents] Config changed: steering=%s/%s", steeringProvider, steeringModel)
					if app.sdk == nil {
						return
					}
					if steeringProvider != "" && steeringModel != "" {
						if err := app.sdk.BuildSteeringAgent(steeringProvider, steeringModel); err != nil {
							logDebug("[SteeringAgent] Rebuild failed: %v", err)
						} else {
							logDebug("[SteeringAgent] Rebuilt with %s/%s", steeringProvider, steeringModel)
							// If findings is active, wire up the pre-tool steering hook
							// so it starts working immediately after the agent is rebuilt.
							if sa := app.sdk.GetSteeringAgent(); sa != nil {
								if err := app.sdk.hooksManager.EnableSteeringPreTool(sa); err != nil {
									logDebug("[SteeringPreTool] Failed to enable after agent rebuild: %v", err)
								} else {
									logDebug("[SteeringPreTool] Enabled after agent rebuild")
								}
							}
						}
					}
				})
				logDebug("Swarm Agents model selection callback configured")
			}
			// Wire up steering settings callbacks to load/save via ConfigManager
			app.settingsManager.SetSteeringCallbacks(
				// Load callback: retrieves steering config from config.json
				func(scope string) (*core.SteeringConfig, error) {
					cm, err := commands.NewConfigManager()
					if err != nil {
						return &core.SteeringConfig{}, nil
					}
					cfg, err := cm.LoadConfig()
					if err != nil || cfg == nil {
						return &core.SteeringConfig{}, nil
					}
					if cfg.SteeringConfig == nil {
						return &core.SteeringConfig{}, nil
					}
					return cfg.SteeringConfig, nil
				},
				// Save callback: persists steering config to config.json
				func(scope string, steeringCfg *core.SteeringConfig) tea.Cmd {
					return func() tea.Msg {
						cm, err := commands.NewConfigManager()
						if err != nil {
							logDebug("[Steering] Failed to create ConfigManager: %v", err)
							return nil
						}
						cfg, err := cm.LoadConfig()
						if err != nil || cfg == nil {
							cfg = &commands.SwarmOSConfig{}
						}
						cfg.SteeringConfig = steeringCfg
						if err := cm.SaveConfig(cfg); err != nil {
							logDebug("[Steering] Failed to save config: %v", err)
							app.addNotification("error", i18n.T("residual.init.steering_save_failed", err))
						} else {
							logDebug("[Steering] Config saved successfully")
						}
						return nil
					}
				},
			)
			logDebug("Steering settings callbacks configured")

			hooksSettings.SetOnHookSave(func(entry settings.HookEntry) error {
				if entry.Builtin {
					return nil
				}

				if app.hooksConfig == nil {
					return nil
				}

				config, err := hookEntryToConfig(entry, sdk.WorkspaceRoot())
				if err != nil {
					return err
				}

				if err := app.hooksConfig.UpdateHook(entry.Name, config); err != nil {
					if _, ok := err.(*HookNotFoundError); ok {
						if addErr := app.hooksConfig.AddHook(config); addErr != nil {
							return addErr
						}
					} else {
						return err
					}
				}

				if err := app.hooksConfig.Save(); err != nil {
					return err
				}

				if app.sdk.hooksManager == nil {
					return nil
				}

				if entry.Enabled {
					if !app.sdk.HasPermission(tools.PermissionHookManage) {
						return fmt.Errorf("hook permissions are denied")
					}

					_ = app.sdk.hooksManager.UnregisterCustomHook(entry.Name)
					shellHook, err := config.ToShellHook()
					if err != nil {
						return err
					}
					if err := app.sdk.hooksManager.RegisterCustomHook(shellHook); err != nil {
						return err
					}
				} else {
					_ = app.sdk.hooksManager.UnregisterCustomHook(entry.Name)
				}

				return nil
			})
			logDebug("Hooks settings save callback configured")
		}

		// Create agents assistant that reuses SDK's provider (same OAuth config as main chat)
		agentTools := NewAgentTools(app.settingsManager.GetAgentsSettings())
		app.agentsAssistant = NewAgentsAssistant(sdk, agentTools, oauthPrefix)
		if app.agentsAssistant != nil && app.agentsAssistant.IsInitialized() {
			logDebug("Agents assistant initialized with tools: %d", app.agentsAssistant.GetToolCount())
		} else {
			logDebug("Failed to initialize agents assistant")
		}

		// Create MCP assistant that reuses SDK's provider (same OAuth config as main chat)
		mcpTools := NewMCPTools(sdk.mcpManager)
		app.mcpAssistant = NewMCPAssistant(sdk, mcpTools, oauthPrefix)
		if app.mcpAssistant != nil {
			logDebug("MCP assistant initialized with %d tools", len(mcpTools.GetTools()))
		} else {
			logDebug("Failed to initialize MCP assistant")
		}

		// Set MCP provider for command autocomplete to enable MCP prompts as /commands
		if sdk.mcpManager != nil {
			app.cmdAutocomplete.SetMCPProvider(sdk.mcpManager)
			logDebug("MCP prompt provider set on command autocomplete")
		}

		// Set plugin provider for command autocomplete to enable plugin commands as /commands
		if sdk.pluginsManager != nil {
			app.cmdAutocomplete.SetPluginProvider(sdk.pluginsManager)
			logDebug("Plugin command provider set on command autocomplete")
		}

		// Wire mention autocomplete providers (agents + tmux)
		{
			agentsSettings := app.settingsManager.GetAgentsSettings()
			agentProvider := NewPluginAgentMentionProvider(sdk.pluginsManager, agentsSettings, sdk)
			app.mentionAutocomplete.SetAgentProvider(agentProvider)

			tmuxProvider := NewTmuxSessionMentionProvider()
			app.mentionAutocomplete.SetTmuxProvider(tmuxProvider)
			logDebug("Mention autocomplete providers set (agents + tmux)")
		}

		// Note: Web search tool registration is now handled in SDKIntegration.NewSDKIntegrationWithOptions
		// which respects the Exa API key from providers.json

		// Set up chat callback for agents settings
		agentsSettings := app.settingsManager.GetAgentsSettings()
		if agentsSettings != nil && app.agentsAssistant != nil {
			agentsSettings.SetChatCallback(func(message string) {
				// Handle chat message in a goroutine to not block UI
				go func() {
					ctx := context.Background()
					if app.agentsAssistant == nil || !app.agentsAssistant.IsInitialized() {
						agentsSettings.AddChatMessage("system", "Agents assistant not available. Please check your provider configuration.", nil, 0)
						state := app.settingsManager.GetState()
						state.AgentsChatWaiting = false
						app.updateQueue <- AgentsChatResponseMsg{}
						return
					}

					// Add user message to history
					agentsSettings.AddChatMessage("user", message, nil, 0)

					// Get response from AI with detailed tool call info
					response, err := app.agentsAssistant.ChatWithDetails(ctx, message)
					if err != nil {
						errorMsg := app.agentsAssistant.GetLastError(err)
						agentsSettings.AddChatMessage("system", fmt.Sprintf("Error: %s", errorMsg), nil, 0)
					} else {
						// Convert tool calls to display format
						toolCalls := make([]settings.ToolCallDisplay, 0, len(response.ToolCalls))
						for _, tc := range response.ToolCalls {
							toolCalls = append(toolCalls, settings.ToolCallDisplay{
								Name:      tc.Name,
								InputJSON: tc.InputJSON,
								Result:    tc.Result,
								Success:   tc.Success,
							})
						}

						// Add assistant response with tool calls
						agentsSettings.AddChatMessage("assistant", response.Content, toolCalls, response.TotalTurns)
					}

					// Clear waiting state
					state := app.settingsManager.GetState()
					state.AgentsChatWaiting = false

					// Send update to trigger re-render (via updateQueue)
					app.updateQueue <- AgentsChatResponseMsg{}
				}()
			})
			logDebug("Agents settings chat callback configured")
		}

		// Set up chat callback for MCP settings
		mcpSettings := app.settingsManager.GetMCPSettings()
		if mcpSettings != nil && app.mcpAssistant != nil {
			mcpSettings.SetChatCallback(func(message string) {
				// Handle chat message in a goroutine to not block UI
				go func() {
					ctx := context.Background()
					if app.mcpAssistant == nil {
						mcpSettings.AddChatMessage("system", "MCP assistant not available. Please check your provider configuration.", nil, 0)
						state := app.settingsManager.GetState()
						state.MCPChatWaiting = false
						app.updateQueue <- MCPChatResponseMsg{}
						return
					}

					// Add user message to history
					mcpSettings.AddChatMessage("user", message, nil, 0)

					// Get response from AI
					response, err := app.mcpAssistant.SendMessage(ctx, message)
					if err != nil {
						mcpSettings.AddChatMessage("system", fmt.Sprintf("Error: %v", err), nil, 0)
					} else {
						// Add assistant response (simple text for now)
						mcpSettings.AddChatMessage("assistant", response, nil, 1)
					}

					// Clear waiting state
					state := app.settingsManager.GetState()
					state.MCPChatWaiting = false

					// Send update to trigger re-render (via updateQueue)
					app.updateQueue <- MCPChatResponseMsg{}
				}()
			})
			logDebug("MCP settings chat callback configured")
		}

		// Provide MCP manager to context settings for MCP source picker.
		if contextSettings := app.settingsManager.GetContextSettings(); contextSettings != nil {
			contextSettings.SetMCPProvider(sdk.mcpManager)
		}

		// Populate agents settings with available tools and hooks
		agentsSettings = app.settingsManager.GetAgentsSettings()
		if agentsSettings != nil && sdk.toolRegistry != nil {
			// Populate available tools
			toolNames := sdk.toolRegistry.List()
			availableTools := make([]settings.AgentToolInfo, 0, len(toolNames))
			for _, toolName := range toolNames {
				tool, err := sdk.toolRegistry.Get(toolName)
				if err != nil {
					continue
				}

				// Categorize tools
				category := "builtin"
				if strings.HasPrefix(toolName, "mcp_") {
					category = "mcp"
				} else if strings.Contains(toolName, ".") {
					category = "custom"
				}

				availableTools = append(availableTools, settings.AgentToolInfo{
					Name:        toolName,
					Description: tool.Description(),
					Category:    category,
				})
			}
			agentsSettings.SetAvailableTools(availableTools)
			logDebug("Populated %d available tools for agents settings", len(availableTools))

			// Populate available hooks
			availableHooks := make([]settings.AgentHookInfo, 0)

			// Add built-in SDK hooks
			builtinHooks := []settings.AgentHookInfo{
				{Name: "logging", Description: i18n.T("residual.init.hook_logging_desc"), Type: "builtin", Source: "sdk"},
				{Name: "metrics", Description: i18n.T("residual.init.hook_metrics_desc"), Type: "builtin", Source: "sdk"},
				{Name: "audit", Description: i18n.T("residual.init.hook_audit_desc"), Type: "builtin", Source: "sdk"},
				{Name: "tracing", Description: i18n.T("residual.init.hook_tracing_desc"), Type: "builtin", Source: "sdk"},
			}
			availableHooks = append(availableHooks, builtinHooks...)

			// Add custom hooks from hooks config
			if app.hooksConfig != nil {
				for _, hookConfig := range app.hooksConfig.CustomHooks {
					availableHooks = append(availableHooks, settings.AgentHookInfo{
						Name:        hookConfig.Name,
						Description: hookConfig.Description,
						Type:        "shell",
						Source:      "custom",
					})
				}
			}

			agentsSettings.SetAvailableHooks(availableHooks)
			logDebug("Populated %d available hooks for agents settings", len(availableHooks))

			// Wire custom agents to delegate_task tool
			sdk.SetCustomAgentDefGetter(func() []*agent.Definition {
				return agentsSettings.GetAllSDKDefinitions()
			})
			logDebug("Wired custom agents to delegate_task tool")
		}
	}

	// Wire skills manager to settings and commands
	if sdk != nil && sdk.skillsManager != nil {
		// Wire skills loader to settings
		skillsSettings := app.settingsManager.GetSkillsSettings()
		if skillsSettings != nil {
			skillsSettings.SetLoader(sdk.skillsManager.GetLoader())
			logDebug("Skills loader wired to settings")
		}

		// Wire skills loader to skill command
		if skillCmd, ok := cmdReg.Get("skill"); ok {
			if sc, ok := skillCmd.(*commands.SkillCommand); ok {
				sc.SetLoader(sdk.skillsManager.GetLoader())
				logDebug("Skills loader wired to skill command")
			}
		}

		// Log skill stats
		allSkills := sdk.skillsManager.GetAllSkills()
		activeSkills := sdk.skillsManager.GetActiveSkills()
		logDebug("Skills initialized: %d total, %d active", len(allSkills), len(activeSkills))
	}

	// Wire plugins manager to settings and commands
	if sdk != nil && sdk.pluginsManager != nil {
		// Wire plugins loader to settings
		pluginsSettings := app.settingsManager.GetPluginsSettings()
		if pluginsSettings != nil {
			pluginsSettings.SetLoader(sdk.pluginsManager.GetLoader())
			pluginsSettings.SetMarketplace(sdk.pluginsManager.GetMarketplace())
			pluginsSettings.SetSearcher(sdk.pluginsManager.GetSearcher())
			// Set plugin action callbacks
			pluginsSettings.SetCallbacks(
				func(name string, enabled bool) error {
					// Toggle callback
					if enabled {
						return sdk.pluginsManager.EnablePlugin(name)
					}
					return sdk.pluginsManager.DisablePlugin(name)
				},
				func(name string) error {
					// Install callback
					ctx := context.Background()
					_, err := sdk.pluginsManager.InstallFromMarketplace(ctx, name)
					return err
				},
				func(name string) error {
					// Uninstall callback
					return sdk.pluginsManager.UninstallPlugin(name)
				},
			)
			// Load marketplace in background for quick access
			pluginsSettings.LoadMarketplace()
			logDebug("Plugins loader wired to settings")
		}

		// Wire plugins loader to plugin command
		if pluginCmd, ok := cmdReg.Get("plugin"); ok {
			if pc, ok := pluginCmd.(*commands.PluginCommand); ok {
				pc.SetLoader(sdk.pluginsManager.GetLoader())
				pc.SetMarketplace(sdk.pluginsManager.GetMarketplace())
				pc.SetSearcher(sdk.pluginsManager.GetSearcher())
				logDebug("Plugins loader wired to plugin command")
			}
		}

		// Wire commands browser
		if commandsCmd, ok := cmdReg.Get("commands"); ok {
			if cb, ok := commandsCmd.(*commands.CommandsBrowser); ok {
				cb.SetRegistry(cmdReg)
				cb.SetPluginsLoader(sdk.pluginsManager.GetLoader())
				logDebug("Commands browser wired")
			}
		}

		// Log plugin stats
		allPlugins := sdk.pluginsManager.GetPlugins()
		enabledPlugins := sdk.pluginsManager.GetEnabledPlugins()
		logDebug("Plugins initialized: %d total, %d enabled", len(allPlugins), len(enabledPlugins))
	}

	// Set up render command callback
	if renderCmd, ok := cmdReg.Get("render"); ok {
		if rc, ok := renderCmd.(*commands.RenderCommand); ok {
			rc.SetSettings(renderSettings)
			rc.SetOnUpdate(func() {
				// Update app state
				app.showThinking = renderSettings.ShowThinking
				app.renderSettings = renderSettings

				// Force refresh of message viewport
				if app.msgViewport != nil {
					app.updateViewportContent()
				}

				logDebug("Render settings updated: thinking=%v", renderSettings.ShowThinking)
			})
		}
	}

	// Set up MCP command callbacks
	if mcpCmd, ok := cmdReg.Get("mcp"); ok {
		if mc, ok := mcpCmd.(*commands.MCPCommand); ok {
			// Callback for when a server is added
			mc.SetOnServerAdded(func(config *commands.MCPServerConfig) {
				if app.sdk != nil && app.sdk.mcpManager != nil {
					ctx := context.Background()
					if err := app.sdk.mcpManager.AddServer(ctx, config); err != nil {
						logDebug("Failed to add MCP server: %v", err)
					} else {
						logDebug("MCP server added: %s", config.Name)
					}
				}
			})

			// Callback for when a server is enabled/disabled
			mc.SetOnServerToggled(func(name string, enabled bool) {
				if app.sdk != nil && app.sdk.mcpManager != nil {
					ctx := context.Background()
					if err := app.sdk.mcpManager.ToggleServer(ctx, name, enabled); err != nil {
						logDebug("Failed to toggle MCP server: %v", err)
					} else {
						logDebug("MCP server %s toggled: enabled=%v", name, enabled)
					}
				}
			})

			// Callback for when a tool is enabled/disabled
			mc.SetOnToolToggled(func(serverName, toolName string, enabled bool) {
				if app.sdk != nil && app.sdk.mcpManager != nil {
					ctx := context.Background()
					if err := app.sdk.mcpManager.ToggleTool(ctx, serverName, toolName, enabled); err != nil {
						logDebug("Failed to toggle MCP tool: %v", err)
					} else {
						logDebug("MCP tool %s:%s toggled: enabled=%v", serverName, toolName, enabled)
					}
				}
			})

			// Callback to refresh server list
			mc.SetOnRefreshServers(func() []*commands.MCPServerState {
				if app.sdk != nil && app.sdk.mcpManager != nil {
					return app.sdk.mcpManager.GetServerStates()
				}
				return []*commands.MCPServerState{}
			})
		}
	}

	// Wire up swarm command with A2A provider
	if swarmCmd, ok := cmdReg.Get("swarm"); ok {
		if sc, ok := swarmCmd.(*commands.SwarmCommand); ok {
			sc.SetSwarmProvider(sdk)
			logDebug("Swarm command wired with A2A provider")
		}
	}

	// Load conversations from SDK storage - will be populated via loadConversationsFromSDK()
	app.conversations = []Conversation{}

	// Initialize workspaces
	app.initWorkspaces()

	// Initialize focused pane (focus first chat pane by default)
	panes := app.getAllPanes()
	if len(panes) > 0 {
		panes[0].Focused = true
		app.focusedPane = panes[0]
	}

	// Set debug screen theme
	debugScreen.theme = app.theme

	// Wire the Usage tab bridge so the debug screen can render provider usage data.
	// The usage renderer lives on *App, so we expose it via callbacks.
	debugScreen.usageRenderer = func(width, height int) []string {
		return app.usageTabLines(width, height)
	}
	debugScreen.usageActivate = func() tea.Cmd {
		// Cached-first: the Usage tab renders app.usageResult immediately via
		// usageTabLines, so we only kick off a network fetch when there is no
		// cached snapshot or the cached one is stale. A refresh that runs while
		// cached data is present is a background refresh — usageTabLines keeps the
		// existing content visible and just appends a "Refreshing…" line. This
		// mirrors the home Usage screen and avoids the blank "Refreshing usage
		// data…" wait every time the debug tab is opened.
		if app.usageLoading {
			return nil
		}
		if app.usageResult != nil && time.Since(app.usageResult.FetchedAt) < usageCacheTTL {
			return nil
		}
		return app.loadUsageAsync()
	}
	debugScreen.usageSubTabPrev = func() {
		if app.usageSubTab > 0 {
			app.usageSubTab--
		}
	}
	debugScreen.usageSubTabNext = func() {
		if app.usageSubTab < 1 {
			app.usageSubTab++
		}
	}

	// Link debug screen to SDK
	if sdk != nil {
		sdk.SetDebugScreen(debugScreen)

		// Set raw event callback for capturing raw API responses
		sdk.SetRawEventCallback(&rawEventCallbackWrapper{debugScreen: debugScreen})

		// Set debug inspect provider for agent-based introspection
		// This allows the agent to inspect logs, messages, requests, tools, and state
		sdk.SetDebugInspectProvider(app)
	}

	// Set global debug screen reference so DebugLog can add messages

	// Initialize context window from SDK (after debug screen is connected so logs appear)
	if sdk != nil {
		app.modelContextWindow = sdk.GetModelContextWindow()
		debugScreen.AddLog(fmt.Sprintf("[APP-INIT] model=%s contextWindow=%d provider=%s",
			app.currentModel, app.modelContextWindow, app.currentProvider))
	}

	// Set SDK logger callback to also send logs to debug screen
	tuiobs.SetLogCallback(func(message string) {
		if debugScreen != nil {
			debugScreen.AddLog(message)
		}
	})

	// Load conversations from SDK storage now that app is fully initialized
	if sdk != nil && !opts.DebugLineageFixture {
		app.loadConversationsFromSDK()
	}

	if opts.DebugLineageFixture {
		app.loadDebugLineageFixture()
	}
	app.resumeConversationAtStartup(opts.ResumeConversationID, app.openConversationByID)

	// Initialize Git TUI panel (OctoGit integration)
	// Use workspace root from SDK for the git repository path
	gitModel := gitpanel.New(chatui.DefaultTheme(), workspaceRoot)
	app.gitPanel = &gitModel
	logDebug("Git panel initialized for workspace: %s", workspaceRoot)

	// Initialize voice input
	// Start background pre-render buffer for smooth scrolling
	app.preRenderBuffer.Start()
	app.initVoice()
	if opts.TrackWorkspaceSession && opts.WorkspaceRoot != "" {
		lease, err := gitops.RegisterWorkspaceLease(opts.WorkspaceRoot)
		if err != nil {
			logDebug("Failed to register workspace lease: %v", err)
		} else {
			app.workspaceLease = lease
		}
	}
	return app
}

// initWorkspaces sets up default workspace layouts
// Sidebar is FIXED and always visible - these layouts only define chat pane arrangements
func (a *App) initWorkspaces() {
	// Workspace 1: Single chat pane (focused experience)
	ws1 := &Workspace{
		ID:   1,
		Name: "Single",
		Root: &PaneContainer{
			Pane: &Pane{
				Type: PaneChat,
			},
		},
	}

	// Workspace 2: Two chat panes side by side
	ws2 := &Workspace{
		ID:   2,
		Name: "Dual",
		Root: &PaneContainer{
			Split: &Split{
				Direction: SplitHorizontal,
				Ratio:     0.5,
				First: &PaneContainer{
					Pane: &Pane{
						Type: PaneChat,
					},
				},
				Second: &PaneContainer{
					Pane: &Pane{
						Type: PaneChat,
					},
				},
			},
		},
	}

	// Workspace 3: Three chat panes for parallel work
	ws3 := &Workspace{
		ID:   3,
		Name: "Triple",
		Root: &PaneContainer{
			Split: &Split{
				Direction: SplitHorizontal,
				Ratio:     0.33,
				First: &PaneContainer{
					Pane: &Pane{
						Type: PaneChat,
					},
				},
				Second: &PaneContainer{
					Split: &Split{
						Direction: SplitHorizontal,
						Ratio:     0.5,
						First: &PaneContainer{
							Pane: &Pane{
								Type: PaneChat,
							},
						},
						Second: &PaneContainer{
							Pane: &Pane{
								Type: PaneChat,
							},
						},
					},
				},
			},
		},
	}

	a.workspaces = []*Workspace{ws1, ws2, ws3}
	a.currentWorkspace = 1 // Default to dual view

	// Initialize viewports for all panes in all workspaces
	for _, ws := range a.workspaces {
		a.initializePaneViewports(ws.Root)
	}
}

// initializePaneViewports recursively initializes viewports for all panes in a workspace
func (a *App) initializePaneViewports(container *PaneContainer) {
	if container == nil {
		return
	}

	if container.Pane != nil {
		// Initialize viewport for this pane (default size, will be resized during layout)
		container.Pane.Viewport = NewMessageList(80, 20)
	} else if container.Split != nil {
		// Recursively initialize viewports in split containers
		a.initializePaneViewports(container.Split.First)
		a.initializePaneViewports(container.Split.Second)
	}
}

// defaultAutoCompactionThresholdPercent is the fraction of the active model's
// context window at which auto-compaction fires when the user has NOT set an
// explicit, in-range threshold. Compacting at 80% leaves headroom for the next
// turn's request+response instead of letting the agent fall through to its
// internal default (effectiveWindow - margin), which approaches ~98% of large
// context windows and triggers late, risky compaction.
const defaultAutoCompactionThresholdPercent = 0.80

// applyDefaultCompactionThreshold guarantees a sane auto-compaction trigger.
//
// A user-supplied descriptor is honored verbatim: a positive fixed_tokens
// value, or a percent value in (0, 1], is passed through unchanged so the agent
// runtime resolves it against the live model window. Only an absent, empty, or
// out-of-range descriptor is replaced with the 80%-of-context-window default.
//
// Per-model override resolution happens upstream in buildAutoCompactionConfig
// (via ResolveThresholdForModel) and is deliberately left untouched here — this
// only backfills a missing/invalid value, never overrides a real user choice.
func applyDefaultCompactionThreshold(cfg *agent.AutoCompactionConfig) *agent.AutoCompactionConfig {
	if cfg == nil {
		return cfg
	}
	switch cfg.Threshold.Mode {
	case agent.AutoCompactionThresholdFixedTokens:
		if cfg.Threshold.Value > 0 {
			return cfg // explicit fixed-token descriptor: honor unchanged
		}
	case agent.AutoCompactionThresholdPercent:
		if cfg.Threshold.Value > 0 && cfg.Threshold.Value <= 1 {
			return cfg // explicit percent descriptor: honor unchanged
		}
	}
	// Absent/empty/out-of-range descriptor: install the sane 80% default so the
	// agent never falls back to its ~98% effective-window ceiling.
	cfg.Threshold = agent.AutoCompactionThreshold{
		Mode:  agent.AutoCompactionThresholdPercent,
		Value: defaultAutoCompactionThresholdPercent,
	}
	return cfg
}
