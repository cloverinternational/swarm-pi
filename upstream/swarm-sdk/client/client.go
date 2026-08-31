// The package documentation lives in doc.go.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/configformat"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/analytics"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/configbundle"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/manager"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation/storage"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/filetracker"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	dreambuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	hooksloader "github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/loader"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mcp"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability/noop"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	provanthropic "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	provgemini "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/gemini"
	provopenai "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/openai"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/taskstore"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	toolsbuiltin "github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/projectmemory"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/websearch"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	"github.com/google/uuid"
)

// ─── ClientType ───────────────────────────────────────────────────────────────

// ClientType identifies the runtime surface that constructed a Client.
// It is stamped onto agent.Definition so every execution log can be
// correlated back to the originating surface without additional context.
type ClientType = string

const (
	// ClientTypeTUI is the interactive terminal UI (swarm-tui).
	ClientTypeTUI ClientType = "tui"

	// ClientTypeHeadless is the headless / swarm -p CLI surface.
	ClientTypeHeadless ClientType = "headless"

	// ClientTypeManaged is the managed-agents scheduler path.
	ClientTypeManaged ClientType = "managed"

	// ClientTypeSDK is a direct SDK consumer (library usage, demos, tests).
	ClientTypeSDK ClientType = "sdk"
)

// ─── Option ──────────────────────────────────────────────────────────────────

// Option is a functional option for configuring a Client.
type Option func(*options)

type options struct {
	providerName         string
	originalProviderName string // preserved for credential lookup (e.g., "fireworks" not "openai")
	model                string
	apiKey               string
	baseURL              string
	systemPrompt         string
	maxTokens            int
	temperature          float64
	// contextWindow overrides the model's context window (tokens). When 0 the
	// client falls back to ~/.swarm/config/providers.json lookup (auto) and then to
	// provider.Capabilities().MaxContextWindow. Explicit callers always win.
	contextWindow int
	workspaceDir  string
	storageDir    string
	logger        observability.Logger
	tracer        observability.Tracer
	noAutoConfig  bool // skip ~/.swarm loading
	noIndexMd     bool // skip automatic INDEX.md loading from workspace
	// noFallback, when true, collapses the agent's fallback chain to its
	// primary so a pinned provider/model failure surfaces verbatim instead of
	// cascading into (possibly unregistered) fallback providers.
	noFallback bool
	// extensibility
	extraTools []tools.Tool // extra tools registered after agent init
	extraHooks []hooks.Hook // extra hooks registered after agent init
	// toolRegistryInstance, when non-nil, is used directly as the agent's
	// tool registry instead of building a fresh one.  All extra tools are
	// still appended.  Intended for adapters (e.g. swarm-tui) that have
	// already constructed a fully-configured registry.
	toolRegistryInstance tools.Registry
	hooksFromConfig      bool   // load hooks from default disk locations
	hooksFromPath        string // load hooks from explicit path (overrides default)

	// ── Full-agent options ───────────────────────────────────────────────
	// Hooks
	enableDefaultHooks    bool // wire steering, task enforcement, findings bridge
	enableSteering        bool // just steering pre-tool hook
	enableTaskEnforce     bool // just task enforcement hook
	enableSleepBlocker    bool // block sleep/timeout commands in bash, suggest cron_scheduler
	enableGitUserEnforce  bool //	enforce git user on main branch (grace period then block)
	enableProtectedBranch bool // block mutations on a protected git branch, force worktree
	enableAutoModeHook    bool // classify tool calls against allow/soft-deny lists
	enableRecapHook       bool // generate recap on session resume / context stress
	// nil = library defaults. Use WithAutoModeHookConfig / WithRecapHookConfig
	// to override.
	autoModeConfig *dreambuiltin.AutoModeConfig
	recapConfig    *dreambuiltin.RecapConfig

	// Extended tools
	enableIITools       bool // task management, subagent, apply_patch, etc.
	enableWebSearch     bool // web search tool
	enableProjectMemory bool // project context persistence (SQLite)
	enableBuiltinTools  bool // agent_browser, grep, list_dir, bash
	enableAllTools      bool // all of the above

	// Compaction
	enableCompaction bool                        // auto context compaction
	compactionConfig *agent.AutoCompactionConfig // custom config (nil = defaults)

	// Credential rotation
	enableCredRotation bool // multi-account credential rotation

	// Task store & nudge
	enableTaskStore    bool // persistent task/todo storage
	enableTaskNudge    bool // inject task nudge ephemeral prompt
	enableNestedAgents bool // expose model-facing nested-agent orchestration tools

	// Autogen skills
	enableAutogenSkills bool                  // enable autogenskills system
	autogenSkillsConfig *autogenskills.Config // custom config (nil = defaults)
	enableSkills        bool                  // discover and expose regular skills
	skillPaths          []string              // additional regular-skill search paths

	// Approval mode — empty = interactive default (registry's built-in checker).
	// "yolo" installs a YOLO checker that auto-approves everything.
	// "readonly" installs a checker that denies writes, deletes, bash, and network.
	approvalMode string

	// Operating mode filters the tool surface the LLM sees and appends a
	// system-instruction suffix. Runtime switches go through
	// Client.SetOperatingMode, which reads the atomic pointer on Client.
	operatingMode *mode.OperatingMode

	// MCP: when set, the Client constructs and owns an mcp.Manager that
	// loads global and project MCP configs and registers discovered tools
	// into the agent's tool registry. enableMCP is separate from
	// mcpConfigPath so WithMCP("") can opt into the default XDG path
	// (~/.swarm/config/mcp_servers.json) without looking like an unset string.
	enableMCP     bool
	mcpConfigPath string

	// Code mode: when enabled, selected tools are wrapped inside a run_code
	// tool that lets the LLM orchestrate multiple tool calls with JavaScript
	// code in a sandboxed goja VM. enableCodeMode activates with default
	// settings; codeModeConfig allows full customisation.
	enableCodeMode bool
	codeModeConfig *codemode.Config

	// Cloud sync: when set, the Client records the auth token so callers
	// (currently the IPC server) can construct a cloudsync.Manager via
	// Client.CloudSyncToken(). Full in-Client wiring of the manager is
	// deferred to the IPC-server cutover (PLAN.md Step 3.1).
	cloudSyncToken string

	// ── Session-state options ────────────────────────────────────────────
	//
	// providerInstance, when non-nil, bypasses the standard provider
	// construction (registry lookup, credential loading) and uses the
	// pre-built provider directly.  The adapter value must implement
	// provider.Provider.  Useful when a caller (e.g. swarm-tui's
	// SDKIntegration) has already built a complex provider stack (reliability
	// wrapper, OAuth refresh, custom callbacks) and wants the Client to use
	// it without duplicating that logic.
	providerInstance provider.Provider

	// agentInstance, when non-nil, bypasses agent construction entirely and
	// uses the pre-built agent directly.  The client does NOT stop the
	// supplied agent on Close or Reconfigure; lifecycle management remains
	// the caller's responsibility.  Implies providerInstance and
	// toolRegistryInstance are ignored for the initial build (the agent
	// carries its own provider and registry).
	agentInstance *agent.Agent

	// configManager unlocks the CRUD-style methods (CreateAgent,
	// CreateProfile, CreateHook, etc.) that mutate a configbundle and
	// persist to disk.  Optional — when nil those methods return
	// ErrNoConfigManager.
	configManager *configbundle.Manager

	// initialMode seeds State.OperatingMode at construction time
	// (defaults to "act" when empty).
	initialMode string

	// initialAgent seeds State.ActiveAgent at construction time.
	initialAgent string

	// initialProfile seeds State.ActiveProfile at construction time.
	initialProfile string

	// clientType identifies the runtime surface that constructed this Client.
	// Stamped onto agent.Definition so execution logs can be correlated back
	// to the originating surface. Defaults to ClientTypeSDK when unset.
	clientType ClientType

	// harness activates the closed harness-construction path when it carries a
	// compiled plan. It is populated only by harness-specific options.
	harness *harnessConstruction
}

// WithProvider is defined in provider_type.go with type-safe Provider parameter.
// See provider_type.go for documentation and examples.

// WithClientType records the runtime surface identifier for this Client so
// every agent execution log includes a client_type field. Use the predefined
// constants (ClientTypeTUI, ClientTypeHeadless, ClientTypeManaged,
// ClientTypeSDK). Defaults to ClientTypeSDK when not called.
func WithClientType(t ClientType) Option {
	return func(o *options) { o.clientType = t }
}

// WithAPIKey sets an explicit API key, bypassing credential auto-detection.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithProfileName records the active profile name so it is included in the
// agent metadata block injected into the system prompt. Call this from any
// adapter (e.g. swarm-tui, headless server) that knows which profile is active.
//
// CONTRACT: name must not contain newlines. Empty string is valid (omitted from block).
func WithProfileName(name string) Option {
	return func(o *options) { o.initialProfile = name }
}

// WithProviderInstance supplies a pre-built provider.Provider that the Client
// uses directly, bypassing the standard credential-lookup and registry
// construction.  This is intended for adapters (e.g. swarm-tui) that have
// already assembled a complex provider stack (reliability wrappers, OAuth
// refresh, raw-event callbacks) and do not want the Client to duplicate that
// work.
//
// When this option is set, WithProvider/WithAPIKey/WithBaseURL are ignored
// for provider construction purposes, though providerName and model are still
// read from options for metadata and context-window lookup.
func WithProviderInstance(p provider.Provider) Option {
	return func(o *options) { o.providerInstance = p }
}

// WithBaseURL overrides the provider base URL (useful for Ollama or proxies).
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithSystemPrompt sets the agent system prompt.
func WithSystemPrompt(prompt string) Option {
	return func(o *options) { o.systemPrompt = prompt }
}

// WithMaxTokens sets the maximum response tokens.
func WithMaxTokens(n int) Option {
	return func(o *options) { o.maxTokens = n }
}

// WithNoFallback disables fallback-chain cascading. When set, only the chain
// primary (the pinned provider/model) is attempted; on failure the real
// primary error surfaces instead of routing to fallback providers that may not
// be registered. The setting survives Reconfigure (it is re-applied to each
// freshly-built agent).
func WithNoFallback(noFallback bool) Option {
	return func(o *options) { o.noFallback = noFallback }
}

// WithTemperature sets the sampling temperature (0.0-2.0).
// Lower values (0.0-0.3) produce more deterministic outputs.
// Higher values (0.7-1.0+) produce more creative/diverse outputs.
// Default: 0.7
func WithTemperature(temp float64) Option {
	return func(o *options) { o.temperature = temp }
}

// WithContextWindow pins the model's context window in tokens. Overrides any
// value discovered from ~/.swarm/config/providers.json or the provider's
// advertised capabilities. Pass 0 to disable and fall back to auto-detection.
//
// When unset, Client auto-detects in this order:
//  1. ~/.swarm/config/providers.json entry matching provider + model (supports
//     integer tokens or human strings like "200k" / "262144" / "1M").
//  2. provider.Capabilities().MaxContextWindow
//  3. agent's hardcoded 128 000 fallback
//
// The effective window drives the agent's auto-compaction threshold and the
// blocking limit before an API call — so setting this too low forces
// premature compaction, too high risks HTTP 400 "prompt too long".
func WithContextWindow(n int) Option {
	return func(o *options) { o.contextWindow = n }
}

// WithWorkspace sets the workspace root directory that tools operate in.
// Defaults to os.Getwd().
func WithWorkspace(dir string) Option {
	return func(o *options) { o.workspaceDir = dir }
}

// WithStorageDir sets the directory where conversations are persisted.
// Defaults to ~/.swarm/conversations.
func WithStorageDir(dir string) Option {
	return func(o *options) { o.storageDir = dir }
}

// WithLogger injects a custom observability logger.
func WithLogger(l observability.Logger) Option {
	return func(o *options) { o.logger = l }
}

// WithTracer injects a custom observability tracer.
func WithTracer(t observability.Tracer) Option {
	return func(o *options) { o.tracer = t }
}

// WithoutAutoConfig disables automatic loading of ~/.swarm/config/config.yaml.
// Use this when you want to control every setting explicitly.
func WithoutAutoConfig() Option {
	return func(o *options) { o.noAutoConfig = true }
}

// WithoutIndexMd disables automatic INDEX.md loading from the workspace.
// By default, client.New() walks up from the workspace directory and prepends
// any INDEX.md files found to the system prompt, giving every agent session
// a structural map of the codebase without tool calls.
// Use this for headless, test, or minimal clients that do not need repository
// navigation context.
func WithoutIndexMd() Option {
	return func(o *options) { o.noIndexMd = true }
}

// WithTool registers a custom tool with the agent.
// The tool is added to the agent's tool registry after initialization.
// Multiple WithTool calls are additive.
func WithTool(t tools.Tool) Option {
	return func(o *options) { o.extraTools = append(o.extraTools, t) }
}

// WithToolRegistry supplies a pre-built tools.Registry that the agent uses
// directly, bypassing the fresh registry built inside client.New.  Any tools
// added via WithTool are still appended to this registry after construction.
//
// This is intended for adapters (e.g. swarm-tui's SDKIntegration) that have
// already assembled a fully-configured registry (permission checker wired,
// tool-level policies set, advanced-mode deferred registry installed) and do
// not want the Client to build a duplicate one.
func WithToolRegistry(reg tools.Registry) Option {
	return func(o *options) { o.toolRegistryInstance = reg }
}

// WithAgentInstance supplies a pre-built *agent.Agent that the Client uses
// directly, bypassing the full agent construction in initAgent.  The Client
// does NOT call Stop on the supplied agent during Close or Reconfigure;
// lifecycle management remains the caller's responsibility.
//
// When this option is set, WithProviderInstance and WithToolRegistry are
// silently ignored for the initial build — the pre-built agent carries its
// own provider and registry.  The agent's intermediate-update fanout IS
// wired so Subscribe() listeners still receive events.
func WithAgentInstance(a *agent.Agent) Option {
	return func(o *options) { o.agentInstance = a }
}

// WithHook registers a lifecycle hook on the agent.
// The hook is added after agent initialization.
// Multiple WithHook calls are additive.
func WithHook(h hooks.Hook) Option {
	return func(o *options) { o.extraHooks = append(o.extraHooks, h) }
}

// WithSleepBlocker enables the sleep-blocker hook, which prevents agents from
// using sleep/timeout commands and directs them to use cron_scheduler or
// background agent tools instead.
func WithSleepBlocker() Option {
	return func(o *options) { o.enableSleepBlocker = true }
}

// WithGitUserEnforcement enables the git-user-enforcement hook, which
// enforces that the expected git user is the only one allowed to make
// tool calls on the main branch. Others get a 5-tool grace period with
// a warning, then everything is blocked.
func WithGitUserEnforcement() Option {
	return func(o *options) { o.enableGitUserEnforce = true }
}

// WithProtectedBranchGuard enables the protected-branch hook, which hard-blocks
// all mutating tool calls (file writes/edits and mutating shell/git commands)
// while the workspace's git repo is checked out on a protected branch (default
// main/master). Read-only tools and escape operations (git
// checkout/switch/worktree/branch/stash) always remain allowed, and the block
// message steers the agent to create a worktree and merge consciously.
//
// Mutations targeting a path inside a git worktree whose branch is NOT protected
// are allowed, so an agent that creates a worktree can work freely inside it.
//
// Protection defaults to the standard branches (main, master) but is fully
// configurable per-repo via <repoRoot>/config.swarm ("protected" and
// "protectedBranches"); that file, when present, overrides the defaults and can
// disable the guard entirely. The guard fails open on any git/config error.
func WithProtectedBranchGuard() Option {
	return func(o *options) { o.enableProtectedBranch = true }
}

// WithHooksFromConfig loads hooks from the default on-disk locations:
//
//  1. `<workspace>/.claude/settings.json`
//  2. `~/.claude/settings.json`
//  3. `~/.swarm/config/hooks.json`
//
// Both Claude Code and SwarmOS-native formats are supported (auto-detected
// per file). Loaded hooks are appended to whatever WithHook already
// registered. Malformed files cause the Client.New call to fail; missing
// files are skipped silently.
//
// The workspace directory is resolved at agent-init time from
// [WithWorkspace] or the current working directory.
func WithHooksFromConfig() Option {
	return func(o *options) { o.hooksFromConfig = true }
}

// WithHooksFromPath loads hooks from an explicit settings file. Overrides the
// WithHooksFromConfig default-locations search if both are set.
func WithHooksFromPath(path string) Option {
	return func(o *options) { o.hooksFromPath = path }
}

// ─── Full-agent options ─────────────────────────────────────────────────────

// WithDefaultHooks enables the standard hook pipeline: steering pre-tool hook,
// task enforcement, and findings bridge. This is what the headless IPC server
// wires manually. Without hooks, agents drift off-task and ignore guidance.
func WithDefaultHooks() Option {
	return func(o *options) {
		o.enableDefaultHooks = true
		o.enableSteering = true
		o.enableTaskEnforce = true
	}
}

// WithSteering enables only the steering pre-tool hook, which evaluates every
// tool call against the active task for relevance.
func WithSteering() Option {
	return func(o *options) { o.enableSteering = true }
}

// WithTaskEnforcement enables only the task enforcement hook, which blocks
// tool execution when no active task is tracked.
func WithTaskEnforcement() Option {
	return func(o *options) { o.enableTaskEnforce = true }
}

// WithAutoModeHook enables the auto-mode hook, which classifies tool calls
// against allow / soft-deny lists (and an optional AI classifier) so low-risk
// tools can be auto-approved while risky ones still surface a prompt. Opt-in
// is gated by AutoModeConfig.SkipAutoPermissionPrompt or the
// SWARM_AUTO_MODE_OPT_IN=1 env var; without opt-in the hook is inert.
func WithAutoModeHook() Option {
	return func(o *options) { o.enableAutoModeHook = true }
}

// WithAutoModeHookConfig enables the auto-mode hook with a specific config.
// Passing a nil config is equivalent to WithAutoModeHook().
func WithAutoModeHookConfig(cfg *dreambuiltin.AutoModeConfig) Option {
	return func(o *options) {
		o.enableAutoModeHook = true
		o.autoModeConfig = cfg
	}
}

// WithRecapHook enables the recap hook, which generates a conversation
// summary on session resume or when the context window is under pressure.
func WithRecapHook() Option {
	return func(o *options) { o.enableRecapHook = true }
}

// WithRecapHookConfig enables the recap hook with a specific config. Passing
// a nil config is equivalent to WithRecapHook().
func WithRecapHookConfig(cfg *dreambuiltin.RecapConfig) Option {
	return func(o *options) {
		o.enableRecapHook = true
		o.recapConfig = cfg
	}
}

// WithIITools registers the II productivity tools (task management, subagent
// dispatch, apply_patch, browser tools, etc.).
func WithIITools() Option {
	return func(o *options) { o.enableIITools = true }
}

// WithWebSearch registers the web search tool.
func WithWebSearch() Option {
	return func(o *options) { o.enableWebSearch = true }
}

// WithProjectMemory registers the project memory tools (persistent project
// context via SQLite: view_project_context, update_project_context, etc.).
func WithProjectMemory() Option {
	return func(o *options) { o.enableProjectMemory = true }
}

// WithBuiltinTools registers builtin tools (agent_browser, grep, list_dir, bash).
func WithBuiltinTools() Option {
	return func(o *options) { o.enableBuiltinTools = true }
}

// WithAllTools registers all available tool sets: forge (Read, Write, Edit,
// Grep, Bash, SemanticGrep) + II + websearch + project memory + builtins.
func WithAllTools() Option {
	return func(o *options) { o.enableAllTools = true }
}

// WithCompaction enables automatic context window compaction with default settings.
func WithCompaction() Option {
	return func(o *options) { o.enableCompaction = true }
}

// WithCompactionConfig enables automatic context window compaction with custom settings.
func WithCompactionConfig(cfg agent.AutoCompactionConfig) Option {
	return func(o *options) {
		o.enableCompaction = true
		o.compactionConfig = &cfg
	}
}

// WithCredentialRotation enables multi-account credential rotation.
// Loads credentials from ~/.swarm/config/tui_accounts.json and rotates
// across accounts to avoid rate limits.
func WithCredentialRotation() Option {
	return func(o *options) { o.enableCredRotation = true }
}

// WithTaskStore enables persistent task/todo storage for the agent.
func WithTaskStore() Option {
	return func(o *options) { o.enableTaskStore = true }
}

// WithTaskNudge enables the ephemeral task nudge system prompt that reminds
// the agent to track work when it has no active tasks.
func WithTaskNudge() Option {
	return func(o *options) { o.enableTaskNudge = true }
}

// WithAutogenSkills enables the autogenskills system that learns from agent
// behaviors and creates reusable skills. Pass nil config for defaults.
func WithAutogenSkills(cfg *autogenskills.Config) Option {
	return func(o *options) {
		o.enableAutogenSkills = true
		o.autogenSkillsConfig = cfg
	}
}

// WithApprovalMode configures the permission checker installed on the tool
// registry. Recognised values:
//
//   - ""            — leave the registry's default checker in place (interactive).
//   - "interactive" — explicit default; same as empty.
//   - "yolo"        — auto-approve every tool execution (autonomous mode).
//   - "readonly"    — allow reads, deny writes/deletes/bash/network access.
//
// Unknown values are treated as "interactive" so callers don't get surprised
// by typos in autonomous setups.
func WithApprovalMode(mode string) Option {
	return func(o *options) { o.approvalMode = strings.ToLower(strings.TrimSpace(mode)) }
}

// WithOperatingMode installs an operating mode that filters the tool surface
// seen by the LLM and appends the mode's SystemInstruction to every provider
// request.
//
// The mode is consulted on each Chat/Execute call, so a later
// SetOperatingMode swap takes effect on the next turn without needing
// Reconfigure. Passing nil through either WithOperatingMode(nil) or
// SetOperatingMode(nil) removes the active mode.
func WithOperatingMode(m *mode.OperatingMode) Option {
	return func(o *options) { o.operatingMode = m }
}

// WithMCP opts the Client into MCP (Model Context Protocol) server management.
// Pass "" to use the default global config path (~/.swarm/config/mcp_servers.json)
// plus project-level discovery (mcp.json, .mcp.json, .swarm/mcp.json) rooted
// at the workspace directory. Pass an explicit path to override the global
// config location (useful for tests).
//
// When MCP is enabled, New starts all servers whose policy says they should
// auto-connect and registers their tools with the agent's ToolRegistry.
// The live manager is reachable via Client.MCPManager().
func WithMCP(configPath string) Option {
	return func(o *options) {
		o.enableMCP = true
		o.mcpConfigPath = configPath
	}
}

// WithCloudSync configures the client to push/pull conversations and
// settings to/from the Swarm cloud, authenticated by the supplied token.
//
// The token is typically obtained via cloud.AuthenticateDevice() or stored
// in the cloud config. The token is recorded on the client and accessible
// via Client.CloudSyncToken(); full wiring of a cloudsync.Manager into the
// SDK runtime is performed during the IPC-server cutover (PLAN.md Step 3.1).
//
// Until that cutover lands, callers that need cloud-sync behaviour today
// should construct a *cloudsync.Manager directly using the new SDK package
// (github.com/Swarm-Code/mono/swarm-sdk/cloudsync) and feed it the conversation
// storage and config manager they already own.
func WithCloudSync(token string) Option {
	return func(o *options) {
		o.cloudSyncToken = token
	}
}

// WithConfigManager wires a configbundle.Manager into the client so the
// CRUD-style methods (CreateAgent, CreateProfile, CreateHook,
// SetSystemPrompt, AddContextSource, SetConfig, etc.) can mutate the
// active config bundle and persist it to disk.  Without a Manager, those
// methods return ErrNoConfigManager.
func WithConfigManager(cfgMgr *configbundle.Manager) Option {
	return func(o *options) {
		o.configManager = cfgMgr
	}
}

// WithAutoConfigManager attaches a config-bundle manager backed by the standard
// ~/.swarm/config/config.yaml global config (plus any project config found under the
// process working directory). This UNLOCKS the CRUD/config methods —
// CreateAgent, CreateHook, ToggleTool, ToggleSkill, ToggleHook, SetConfig,
// SetSystemPrompt, SwitchProfile, etc. — which otherwise no-op or return
// ErrNoConfigManager. Convenience wrapper over WithConfigDir("").
func WithAutoConfigManager() Option {
	return WithConfigDir("")
}

// WithConfigDir attaches a config-bundle manager that searches workDir for a
// project config bundle, falling back to ~/.swarm/config/config.yaml for global
// settings. Pass "" to use the process working directory. Enables the
// CRUD/config methods (see WithAutoConfigManager). A construction error leaves
// the client without a config manager (same as the default), so callers that
// require config persistence should verify via the CRUD methods' errors.
func WithConfigDir(workDir string) Option {
	return func(o *options) {
		mgr, err := configbundle.NewManager(context.Background(), configbundle.Options{
			WorkDir:    workDir,
			AutoSwitch: true,
		})
		if err == nil {
			o.configManager = mgr
		}
	}
}

// WithConfigBundlePath attaches a config-bundle manager whose GLOBAL bundle is
// stored at globalPath instead of the default ~/.swarm/config/config.yaml. Use this
// when the host process has its own settings system that owns
// ~/.swarm/config/config.yaml (a different schema), so the config-bundle CRUD
// (ToggleTool/SetConfig/CreateHook/...) persists to a dedicated file and never
// clobbers the host's config. Enables the CRUD/config methods.
func WithConfigBundlePath(globalPath string) Option {
	return func(o *options) {
		mgr, err := configbundle.NewManager(context.Background(), configbundle.Options{
			GlobalPath: globalPath,
			AutoSwitch: true,
		})
		if err == nil {
			o.configManager = mgr
		}
	}
}

// WithActiveMode seeds State.OperatingMode at construction time.
// Defaults to "act" when empty.
func WithActiveMode(mode string) Option {
	return func(o *options) {
		o.initialMode = mode
	}
}

// WithInitialMode seeds State.OperatingMode at construction time.
// Equivalent to WithActiveMode.
func WithInitialMode(mode string) Option { return WithActiveMode(mode) }

// WithActiveAgent seeds State.ActiveAgent at construction time.
// CRUD methods like SetAgent update the field at runtime.
func WithActiveAgent(name string) Option {
	return func(o *options) {
		o.initialAgent = name
	}
}

// WithInitialAgent seeds State.ActiveAgent at construction time.
// Equivalent to WithActiveAgent.
func WithInitialAgent(name string) Option { return WithActiveAgent(name) }

// WithActiveProfile (alias: WithInitialProfile) seeds State.ActiveProfile
// at construction time.  CRUD methods like SwitchProfile update the field
// at runtime.
func WithActiveProfile(name string) Option {
	return func(o *options) {
		o.initialProfile = name
	}
}

// WithInitialProfile seeds State.ActiveProfile at construction time.
// Equivalent to WithActiveProfile.
func WithInitialProfile(name string) Option { return WithActiveProfile(name) }

// WithCodeMode enables code mode with default settings. When code mode is
// active, the agent's tool registry is wrapped so that all eligible tools
// become callable JavaScript functions inside a sandboxed goja VM. The LLM
// sees a single "run_code" tool and can batch multiple tool calls in one turn
// via Promise.all(), reducing round-trips and context window usage.
//
//	c, err := client.New(client.WithCodeMode())
func WithCodeMode() Option {
	return func(o *options) {
		o.enableCodeMode = true
	}
}

// WithCodeModeConfig enables code mode with custom configuration. Use this to
// control which tools are sandboxed, the execution timeout, retry behaviour,
// and REPL persistence.
//
//	c, err := client.New(client.WithCodeModeConfig(&codemode.Config{
//	    Enabled:    true,
//	    Timeout:    30 * time.Second,
//	    ToolNames:  []string{"grep", "bash"},
//	    MaxRetries: 5,
//	}))
func WithCodeModeConfig(cfg *codemode.Config) Option {
	return func(o *options) {
		o.enableCodeMode = true
		o.codeModeConfig = cfg
	}
}

// WithFullAgent enables everything needed for a production-quality agent:
// default hooks (steering + task enforcement), all tools, compaction,
// credential rotation, task store, and task nudge. This produces an agent
// with identical capabilities to what the headless IPC server creates.
func WithFullAgent() Option {
	return func(o *options) {
		WithDefaultHooks()(o)
		WithAllTools()(o)
		WithCompaction()(o)
		WithCredentialRotation()(o)
		WithTaskStore()(o)
		WithTaskNudge()(o)
	}
}

// WithNestedAgents enables the model-facing nested-agent orchestration bundle.
//
// The bundle is opt-in because nested agents can multiply provider usage. When
// enabled, Task, Subagent, BackgroundTask, TaskOutput, wait_for_agent, and
// multi_agent_wait are registered using the client's resolved provider/model,
// workspace, permission checker, and complete parent tool registry.
func WithNestedAgents() Option {
	return func(o *options) {
		o.enableNestedAgents = true
	}
}

// WithSkills enables regular skill discovery and the model-facing Skill tool.
//
// With no paths, the SDK uses its canonical user-level skill directories. Any
// supplied paths are added as project/application-specific skill sources.
// This is intentionally separate from WithAutogenSkills: ordinary skills do
// not enable skill creation, budget enforcement, or curator lifecycle hooks.
func WithSkills(paths ...string) Option {
	return func(o *options) {
		o.enableSkills = true
		o.skillPaths = append(o.skillPaths, paths...)
	}
}

// ─── swarmosConfig ──────────────────────────────────────────────────────────

// swarmosConfig mirrors the fields we care about from ~/.swarm/config/config.yaml.
type swarmosConfig struct {
	CurrentProvider string `json:"current_provider"`
	CurrentModel    string `json:"current_model"`
}

// ─── credentials ────────────────────────────────────────────────────────────

// credentialsFile mirrors ~/.swarm/config/credentials.json.
type credentialsFile struct {
	Providers map[string]struct {
		APIKey  string `json:"api_key"`
		BaseURL string `json:"base_url"`
	} `json:"providers"`
}

// ─── Client ──────────────────────────────────────────────────────────────────

// Client is the top-level Swarm SDK client.
type Client struct {
	mu          sync.RWMutex
	agent       *agent.Agent
	agentDef    *agent.Definition
	provider    provider.Provider // active provider, used by Generate (bypasses agent loop)
	oauthActive bool              // true when the active Anthropic provider authenticates via OAuth (drives the request-time CLI prompt prefix)
	convManager *manager.Manager
	convStorage storage.Storage
	logger      observability.Logger
	tracer      observability.Tracer
	workspace   string
	opts        options

	// runtimeMu tracks request leases on concrete agent generations. Hot apply
	// publishes a replacement without waiting inside a synchronous agent
	// callback; the retired generation is stopped after its final lease exits.
	runtimeMu       sync.Mutex
	runtimeRefs     map[*agent.Agent]int
	retiredRuntimes map[*agent.Agent]retiredClientRuntime

	// metadataLocks serializes title/recap generation per conversation. The
	// provider call is intentionally outside the main client mutex: metadata
	// generation must never block model/config reads for unrelated agents.
	metadataMu    sync.Mutex
	metadataLocks map[string]*sync.Mutex
	metadataRuns  map[string]struct{}

	// Subscribers receive fanned-out IntermediateUpdate events during Chat/Execute.
	// Registered via Subscribe(), preserved across Reconfigure.
	subMu       sync.RWMutex
	subscribers []subscriber
	nextSubID   atomic.Uint64

	// Cumulative token bookkeeping fed from TokenCountUpdate events.
	// lastInputTokens/lastOutputTokens are the most recent per-turn values;
	// they are updated in place on each TokenCountUpdate so TokenUsage()
	// reflects the current conversation state, not a running sum. Cache
	// stats are accumulated across messages since there is no
	// IntermediateUpdate that carries them directly — see CacheStats for
	// the walk over conversation storage.
	lastInputTokens  atomic.Int64
	lastOutputTokens atomic.Int64

	// Operating mode is stored in an atomic pointer so SetOperatingMode can
	// swap it at runtime without taking a write lock. Chat/Execute read it
	// per-call and inject the matching mode_filter into the request context.
	// nil means "no mode" — no filter, no instruction suffix.
	opMode atomic.Pointer[mode.OperatingMode]

	// MCP manager (nil unless WithMCP was passed). Owns server connections
	// and registers MCP-discovered tools into the agent's ToolRegistry.
	mcpManager *mcp.RuntimeManager

	// harnessMCPExposure keeps provider-visible tool hints synchronized with
	// plan-declared MCP tools. It is non-nil only on the harness path.
	harnessMCPExposure *harnessMCPExposure

	// ── Session-state surface (lifted from session.ClientSession) ────────
	//
	// stateMu protects sessState; eventSubMu protects eventSubs.  These
	// are populated lazily — Snapshot() returns a zero-valued State if
	// the client has not been driven through the session-style entry
	// points (Start, SetMode, SetModel, etc.).
	cfgMgr *configbundle.Manager

	stateMu   sync.RWMutex
	sessState State

	eventSubMu     sync.RWMutex
	eventSubs      map[uint64]EventHandler
	nextEventSubID atomic.Uint64

	// Interactive tool-approval registry (daemon G1). A pending approval is a
	// channel the interactive PermissionChecker blocks on until a remote UI
	// calls RespondApproval(callID, allow). See approval.go.
	approvalMu       sync.Mutex
	pendingApprovals map[string]chan bool
	approvalSeq      atomic.Uint64

	// skillBudgetHook is the wired autogenskills budget-enforcement hook (nil
	// when autogenskills/budget is disabled). Held so SkillBudget() can surface
	// the live budget state for UI display.
	skillBudgetHook *autogenskills.BudgetEnforcementHook

	// skillRegistry is the autogenskills skill registry (the real loaded skills,
	// not the config-bundle's installed list). Held so LoadedSkills() can surface
	// them for UI display, matching the TUI's source.
	skillRegistry *skills.Registry

	// Lifecycle for the themed-event bridge.
	sessStartOnce  sync.Once
	sessStopOnce   sync.Once
	sessStopped    atomic.Bool
	sessStopCtx    context.Context
	sessStopCanc   context.CancelFunc
	sessAgentUnsub func()

	// Active turn cancel (protected by sessExecMu).  Used by SendMessage
	// to install a cancel hook for Cancel().
	sessExecMu     sync.Mutex
	sessExecCancel context.CancelFunc

	// sessionID is a stable identifier for this Client instance, generated once
	// at New() time. Injected into the agent system prompt so the agent and any
	// downstream analysis can correlate logs, traces, and outputs to one session.
	sessionID string
}

// subscriber wraps a registered listener with a stable ID for unsubscribe.
type subscriber struct {
	id uint64
	fn agent.IntermediateCallback
}

// New creates a Client, auto-loading ~/.swarm/config/config.yaml and credentials
// unless WithoutAutoConfig() is passed.
//
//	c, err := client.New()
//	c, err := client.New(client.WithProvider("openai", "gpt-4o"), client.WithAPIKey("sk-..."))
func New(optFns ...Option) (*Client, error) {
	o := options{
		maxTokens:    32000,
		systemPrompt: "You are a helpful AI assistant.",
	}

	// Apply caller options first so they can override auto-config.
	for _, fn := range optFns {
		fn(&o)
	}

	// A supplied harness is a closed construction boundary. Return before
	// consulting ambient config, credentials, prompts, tools, hooks, or MCP.
	if o.harness != nil && o.harness.plan != nil {
		return newClientFromHarness(o)
	}

	// Apply SWARM_PROVIDER / SWARM_MODEL env vars.
	// These override config.json but not explicit caller options.
	// Convention matches the headless CLI (cmd/headless/main.go).
	if !o.noAutoConfig {
		if o.providerName == "" {
			if v := os.Getenv("SWARM_PROVIDER"); v != "" {
				o.providerName = v
				// SWARM_PROVIDER carries the raw provider name (e.g., "wafer"),
				// which is also the original name for credential lookup.
				if o.originalProviderName == "" {
					o.originalProviderName = v
				}
			}
		}
		if o.model == "" {
			if v := os.Getenv("SWARM_MODEL"); v != "" {
				o.model = v
			}
		}
	}

	// Auto-load ~/.swarm/config/config.yaml unless suppressed.
	if !o.noAutoConfig {
		if err := loadSwarmosConfig(&o); err != nil {
			// Non-fatal: log but continue with defaults.
			_ = err
		}
	}

	// Apply defaults after auto-config.
	if o.providerName == "" {
		o.providerName = "anthropic"
	}
	if o.model == "" {
		o.model = defaultModelFor(o.providerName)
	}

	// CONTRACT ENFORCEMENT: Validate provider is known.
	//
	// IsValid() accepts canonical names, curated aliases, and user-defined
	// custom names registered in ~/.swarm/config/providers.json with a valid
	// api_type. The custom name is preserved for credential lookup so each
	// alias carries its own API key / base URL.
	if !Provider(o.providerName).IsValid() {
		return nil, &ContractViolation{
			Violation: fmt.Sprintf("Unknown provider: %q", o.providerName),
			Required:  "Provider must be one of: anthropic, openai, gemini (or a supported alias), or a custom name defined in ~/.swarm/config/providers.json with api_type set",
			Hint:      fmt.Sprintf("Use client.WithProvider(client.ProviderAnthropic, \"claude-sonnet-4-5\"), client.ParseProvider() for dynamic selection, or add the provider in TUI Settings. Got: %q", o.providerName),
		}
	}

	// Resolve workspace.
	// CONTRACT: ClientTypeManaged requires an explicit WithWorkspace() call.
	// os.Getwd() returns the container process working directory which is not
	// guaranteed to be the project workspace, leading to silent wrong-path bugs.
	if o.clientType == ClientTypeManaged && o.workspaceDir == "" {
		return nil, fmt.Errorf("client.New: WithWorkspace() is required when ClientType is %q — "+
			"os.Getwd() is unsafe in managed containers; set the workspace path explicitly", ClientTypeManaged)
	}
	if o.workspaceDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			o.workspaceDir = cwd
		}
	}

	// Auto-load INDEX.md files from workspace tree into system prompt.
	// This gives every agent session a structural map of the codebase without
	// needing to explore via tools. Disable with WithoutIndexMd().
	if !o.noIndexMd {
		if indexCtx := loadIndexMdWalk(o.workspaceDir); indexCtx != "" {
			o.systemPrompt = indexCtx + "\n\n" + o.systemPrompt
		}
	}

	// Tell the agent its working directory so relative file/shell operations
	// land in the opened project. The Write/Read/bash tools are already bound to
	// this directory, but without stating it in the prompt the model invents an
	// absolute path (e.g. $HOME) whenever the user doesn't specify a directory —
	// so "create foo.py" lands outside the project. Mirrors Claude Code's <env>
	// working-directory block. Prepended so it sits at the very top of the prompt.
	if o.workspaceDir != "" {
		envBlock := fmt.Sprintf("<env>\nWorking directory: %s\n</env>\n"+
			"Unless the user gives an absolute path, create files and run commands relative to the "+
			"working directory above. Do not write to the home directory unless explicitly asked.\n\n",
			o.workspaceDir)
		o.systemPrompt = envBlock + o.systemPrompt
	}

	// Resolve storage directory.
	if o.storageDir == "" {
		o.storageDir = paths.ConversationsDir()
	}

	// Wire observability.
	if o.logger == nil {
		o.logger = noop.NewLogger()
	}
	if o.tracer == nil {
		o.tracer = noop.NewTracer()
	}

	// Resolve API key / credentials.
	if o.apiKey == "" && !o.noAutoConfig {
		// Use originalProviderName for credential lookup to ensure
		// OpenAI-compatible providers (fireworks, groq, etc.) look up
		// their specific API keys (FIREWORKS_API_KEY) rather than
		// the generic OPENAI_API_KEY.
		credProvider := o.originalProviderName
		if credProvider == "" {
			credProvider = o.providerName
		}
		key, baseURL := resolveCredentials(credProvider)
		o.apiKey = key
		if o.baseURL == "" && baseURL != "" {
			o.baseURL = baseURL
		}
	}

	c := &Client{
		logger:        o.logger,
		tracer:        o.tracer,
		workspace:     o.workspaceDir,
		opts:          o,
		cfgMgr:        o.configManager,
		sessionID:     fmt.Sprintf("sess-%d", time.Now().UnixNano()),
		metadataLocks: make(map[string]*sync.Mutex),
		metadataRuns:  make(map[string]struct{}),
	}

	// Seed the operating-mode atomic pointer from options so Chat/Execute see
	// it on the very first turn. Reconfigure will re-seed through the same
	// path if the caller changes the initial mode.
	if o.operatingMode != nil {
		c.opMode.Store(o.operatingMode)
	}

	// Build storage.
	if err := c.initStorage(o.storageDir); err != nil {
		return nil, fmt.Errorf("client: init storage: %w", err)
	}

	// Build provider + agent.
	if err := c.initAgent(o); err != nil {
		return nil, fmt.Errorf("client: init agent: %w", err)
	}

	// Seed the session-state surface (used by Snapshot, ActiveAgent,
	// SetMode, SetModel, etc.) with provider info now that the agent
	// is initialised.
	c.initSessionState(o.initialMode, o.initialAgent, o.initialProfile)

	return c, nil
}

// ─── Chat ─────────────────────────────────────────────────────────────────────

// Chat sends a single message and returns the assistant reply.
// A new conversation is created automatically if conversationID is empty.
//
//	reply, err := c.Chat("What is the capital of France?")
func (c *Client) Chat(message string) (string, error) {
	return c.ChatInConversation("", message)
}

// ChatInConversation sends a message in an existing conversation.
// Pass an empty string to start a new conversation.
func (c *Client) ChatInConversation(conversationID, message string) (string, error) {
	return c.ChatCtx(context.Background(), conversationID, message)
}

// ChatCtx is the full-featured version of Chat with context and conversation control.
func (c *Client) ChatCtx(ctx context.Context, conversationID, message string) (string, error) {
	return c.chatCtxWithOpts(ctx, conversationID, message, SendMessageOptions{})
}

// chatCtxWithOpts is ChatCtx plus per-turn SendMessageOptions controls. The
// SystemPrompt / MaxTurns / DisableTools / DisableHooks fields are forwarded to
// the agent via ExecuteRequest and apply to THIS execution only; the agent
// definition (its stored system prompt, tool registry, hooks manager) is never
// mutated, so concurrent and subsequent turns are unaffected.
func (c *Client) chatCtxWithOpts(ctx context.Context, conversationID, message string, opts SendMessageOptions) (string, error) {
	// Load prior conversation history BEFORE persisting the current user message,
	// so the agent gets multi-turn context (the agent does not load history from
	// convManager on its own — without this each turn is stateless). history holds
	// only prior turns; the current message is passed separately as req.Message.
	history, err := c.conversationHistoryForExecution(ctx, conversationID)
	if err != nil {
		return "", err
	}

	// Persist the user's input to the conversation store so the transcript shows
	// it (in order, before the assistant reply persisted by the agent's message
	// callback).
	if conversationID != "" && message != "" && c.convManager != nil {
		if err := c.AddMessageToConversation(ctx, conversationID, &conversation.Message{
			ID:        fmt.Sprintf("msg_%d", time.Now().UnixNano()),
			Timestamp: time.Now(),
			Role:      conversation.RoleUser,
			Content:   message,
		}); err != nil {
			return "", fmt.Errorf("persist user message: %w", err)
		}
	}

	req := agent.ExecuteRequest{
		ConversationID:       conversationID,
		Message:              message,
		ConversationHistory:  history,
		SystemPromptOverride: opts.SystemPrompt,
		MaxTurns:             opts.MaxTurns,
		DisableTools:         opts.DisableTools,
		DisableHooks:         opts.DisableHooks,
	}
	c.injectModeFilter(&req)

	// Lease the active generation for the request. ApplyHarnessPlan may publish
	// a replacement while this turn is running, but it cannot stop this
	// generation until releaseActiveAgent runs. No client lock is held while
	// agent callbacks execute, so a callback may safely request a hot apply.
	active, enableTaskStore := c.acquireActiveAgent()
	if active == nil {
		return "", fmt.Errorf("client: no agent configured — call New() first")
	}
	defer c.releaseActiveAgent(active)
	if conversationID != "" && enableTaskStore {
		if err := active.SetupTaskPersistence(conversationID); err != nil && c.logger != nil {
			c.logger.Warn(ctx, "client.task_persistence_setup_failed",
				observability.F("conversation_id", conversationID),
				observability.F("error", err.Error()))
		}
	}
	// Put the fanout in the context so the Subagent tool forwards sub-agent
	// activity (it reads agent.GetIntermediateCallback(ctx)); without this the
	// agent-level callback alone never sees sub-agent updates and they run
	// invisibly. Tagged SourceSubAgent in startAgentBridge so a UI can route
	// them to a separate window.
	resp, err := active.Execute(agent.WithIntermediateCallback(ctx, c.fanout), req)
	c.emitAgentStopped(ctx, conversationID, resp, err)
	if err != nil {
		c.refreshConversationMetadataBestEffort(ctx, conversationID)
		return "", err
	}
	// Emit an authoritative end-of-turn usage summary through the same fanout
	// the live stream rides, so streaming clients (e.g. the daemon's /sse
	// consumers) get final per-turn totals + cost without the CLI's
	// stream-json. Additive: a non-streaming or token_count-only client simply
	// ignores the turn_usage update. Best-effort — never fail the turn on it.
	if resp != nil {
		_ = c.fanout(ctx, agent.TurnUsageUpdate{
			ConversationID: conversationID,
			TurnCount:      resp.TurnCount,
			InputTokens:    resp.InputTokens,
			OutputTokens:   resp.OutputTokens,
			TotalTokens:    resp.InputTokens + resp.OutputTokens,
			CostUSD:        resp.CostUSD,
			FinishReason:   string(resp.FinishReason),
			DurationMillis: resp.Duration.Milliseconds(),
		})
	}
	c.refreshConversationMetadataBestEffort(ctx, conversationID)
	return resp.Message, nil
}

// ─── Subscribe ───────────────────────────────────────────────────────────────

// Subscribe registers a listener that receives every IntermediateUpdate emitted
// during Chat/Execute. Use this to stream content, tool calls, token counts,
// and other lifecycle events to a UI or log.
//
// Listeners are invoked sequentially in registration order; if one returns an
// error, the remaining listeners for that event are skipped and the error
// bubbles up to the agent. The returned unsubscribe function is safe to call
// from within a listener.
//
// Subscribers are preserved across Reconfigure — each rebuild re-wires the
// fanout onto the new agent.
//
//	unsub := c.Subscribe(func(ctx context.Context, u agent.IntermediateUpdate) error {
//	    if cu, ok := u.(agent.ContentUpdate); ok {
//	        fmt.Print(cu.Content)
//	    }
//	    return nil
//	})
//	defer unsub()
//
// (Renamed from Subscribe to free that name for the themed-event
// Subscribe(EventHandler) declared in events.go.  Callers that need
// session-level events (mode/model/agent changes etc.) should use
// Subscribe instead.)
func (c *Client) SubscribeUpdates(fn func(ctx context.Context, u agent.IntermediateUpdate) error) func() {
	id := c.nextSubID.Add(1)
	c.subMu.Lock()
	c.subscribers = append(c.subscribers, subscriber{id: id, fn: fn})
	c.subMu.Unlock()
	return func() {
		c.subMu.Lock()
		defer c.subMu.Unlock()
		for i, s := range c.subscribers {
			if s.id == id {
				c.subscribers = append(c.subscribers[:i], c.subscribers[i+1:]...)
				return
			}
		}
	}
}

// fanout delivers an IntermediateUpdate to every current subscriber in
// registration order. The subscriber slice is snapshotted under the read lock
// so unsubscribe during callback is safe (it modifies the real slice while we
// iterate the copy).
func (c *Client) fanout(ctx context.Context, u agent.IntermediateUpdate) error {
	// Tap token counts off the event stream before fanout so TokenUsage()
	// reflects the latest turn even if no caller has subscribed.
	if tc, ok := u.(agent.TokenCountUpdate); ok {
		c.lastInputTokens.Store(int64(tc.InputTokens))
		c.lastOutputTokens.Store(int64(tc.OutputTokens))
	}
	c.subMu.RLock()
	subs := make([]subscriber, len(c.subscribers))
	copy(subs, c.subscribers)
	c.subMu.RUnlock()
	for _, s := range subs {
		if err := s.fn(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// TokenUsage returns the most recent per-turn token counts the agent
// reported via TokenCountUpdate. InputTokens is the full context size sent
// to the model (Input + CacheCreation + CacheRead); OutputTokens is the
// generation this turn. Both are zero until the agent has completed at
// least one turn.
func (c *Client) TokenUsage() (input, output int) {
	return int(c.lastInputTokens.Load()), int(c.lastOutputTokens.Load())
}

// CacheStats walks the most recent active conversation and sums the
// CacheCreation / CacheRead fields across its assistant messages. Returns
// (0, 0) if no conversation is active or storage is nil. Walks in O(n)
// where n is the number of messages; callers that need high-frequency
// reads should cache the result.
func (c *Client) CacheStats() (creation, read int) {
	c.mu.RLock()
	store := c.convStorage
	c.mu.RUnlock()
	if store == nil {
		return 0, 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2000000000) // 2s
	defer cancel()
	convs, err := store.Query(ctx, storage.Filter{})
	if err != nil || len(convs) == 0 {
		return 0, 0
	}

	// Use the most recently updated conversation.
	latest := convs[0]
	for _, cv := range convs[1:] {
		if cv.UpdatedAt.After(latest.UpdatedAt) {
			latest = cv
		}
	}
	for _, m := range latest.Messages {
		if m == nil || m.Tokens == nil {
			continue
		}
		creation += m.Tokens.CacheCreation
		read += m.Tokens.CacheRead
	}
	return creation, read
}

// ─── Generate ────────────────────────────────────────────────────────────────

// Generate runs a single provider turn with the given prompt and returns the
// text reply. It deliberately skips the agent loop: no tool execution, no
// conversation persistence, no subscriber fanout. Use this for one-shot LLM
// calls (commit messages, summarisers, classifiers) where the full agent
// pipeline would be overkill.
//
// The active system prompt (configured via WithSystemPrompt) is applied; the
// provided prompt becomes the single user message.
func (c *Client) Generate(ctx context.Context, prompt string) (string, error) {
	return c.GenerateMessages(ctx, "", []*conversation.Message{
		{Role: conversation.RoleUser, Content: prompt},
	})
}

// GenerateMessages is the multi-message variant of Generate. It accepts a
// structured message list and an optional per-call system prompt override.
// When systemPrompt is empty the client's configured system prompt is used.
// Like Generate, this path bypasses tools, storage, and subscribers.
func (c *Client) GenerateMessages(ctx context.Context, systemPrompt string, messages []*conversation.Message) (string, error) {
	c.mu.RLock()
	prov := c.provider
	model := c.opts.model
	defaultSystem := c.opts.systemPrompt
	maxTokens := c.opts.maxTokens
	c.mu.RUnlock()

	if prov == nil {
		return "", fmt.Errorf("client: no provider configured")
	}

	sys := systemPrompt
	if sys == "" {
		sys = defaultSystem
	}

	req := provider.ChatRequest{
		Model:        model,
		SystemPrompt: sys,
		Messages:     messages,
	}
	if maxTokens > 0 {
		req.MaxTokens = &maxTokens
	}

	resp, err := prov.Chat(ctx, req)
	c.emitGenerateStopped(ctx, resp, err)
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Message == nil {
		return "", fmt.Errorf("client: empty response from provider")
	}
	return resp.Message.Content, nil
}

// ─── Execute ─────────────────────────────────────────────────────────────────

// Execute runs a full agent turn and returns the complete response.
func (c *Client) Execute(ctx context.Context, req agent.ExecuteRequest) (*agent.ExecuteResponse, error) {
	active, _ := c.acquireActiveAgent()
	if active == nil {
		return nil, fmt.Errorf("client: no agent configured")
	}
	defer c.releaseActiveAgent(active)
	c.injectModeFilter(&req)
	// Use ExecuteWhenIdle so a message sent while sub-agents are running
	// queues behind the current execution rather than returning agent.busy.
	resp, err := active.ExecuteWhenIdle(ctx, req)
	c.emitAgentStopped(ctx, req.ConversationID, resp, err)
	c.refreshConversationMetadataBestEffort(ctx, req.ConversationID)
	return resp, err
}

type retiredClientRuntime struct {
	agent *agent.Agent
	mcp   *mcp.RuntimeManager
}

func (c *Client) acquireActiveAgent() (*agent.Agent, bool) {
	c.mu.RLock()
	active := c.agent
	enableTaskStore := c.opts.enableTaskStore
	if active != nil {
		c.runtimeMu.Lock()
		if c.runtimeRefs == nil {
			c.runtimeRefs = make(map[*agent.Agent]int)
		}
		c.runtimeRefs[active]++
		c.runtimeMu.Unlock()
	}
	c.mu.RUnlock()
	return active, enableTaskStore
}

func (c *Client) releaseActiveAgent(active *agent.Agent) {
	if active == nil {
		return
	}

	var retired retiredClientRuntime
	var stop bool
	c.runtimeMu.Lock()
	if refs := c.runtimeRefs[active]; refs > 1 {
		c.runtimeRefs[active] = refs - 1
	} else {
		delete(c.runtimeRefs, active)
		if c.retiredRuntimes != nil {
			retired, stop = c.retiredRuntimes[active]
			delete(c.retiredRuntimes, active)
		}
	}
	c.runtimeMu.Unlock()

	if stop {
		stopClientRuntime(retired)
	}
}

// retireClientRuntime marks an old generation for cleanup. A nil return means
// an in-flight request owns the final cleanup; otherwise the caller may stop the
// generation after releasing Client.mu.
func (c *Client) retireClientRuntime(oldAgent *agent.Agent, oldMCP *mcp.RuntimeManager) *retiredClientRuntime {
	if oldAgent == nil && oldMCP == nil {
		return nil
	}
	retired := retiredClientRuntime{agent: oldAgent, mcp: oldMCP}

	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if oldAgent != nil && c.runtimeRefs[oldAgent] > 0 {
		if c.retiredRuntimes == nil {
			c.retiredRuntimes = make(map[*agent.Agent]retiredClientRuntime)
		}
		c.retiredRuntimes[oldAgent] = retired
		return nil
	}
	return &retired
}

func stopClientRuntime(retired retiredClientRuntime) {
	if retired.mcp != nil {
		retired.mcp.Stop()
	}
	if retired.agent != nil {
		_ = retired.agent.Stop()
	}
}

// ─── Agent control ───────────────────────────────────────────────────────────

// StopAgent stops the running agent.  This is the low-level agent-loop
// halt; for the full session-style lifecycle teardown (cancels pending
// turns + tears down the themed-event bridge), use Stop(ctx).
func (c *Client) StopAgent() {
	if c.agent != nil {
		c.agent.Stop()
	}
}

// AgentInfo returns a defensive snapshot of the active agent's definition.
func (c *Client) AgentInfo() *agent.Definition {
	c.mu.RLock()
	active := c.agent
	fallback := c.agentDef
	c.mu.RUnlock()
	if active != nil {
		return active.Definition()
	}
	if fallback == nil {
		return nil
	}
	return fallback.Clone()
}

// WorkspaceDir returns the resolved workspace directory.
func (c *Client) WorkspaceDir() string { return c.workspace }

// ─── Operating mode ──────────────────────────────────────────────────────────

// OperatingMode returns the currently active operating mode, or nil if none.
// The caller must not mutate the returned value; call Clone() first if a
// modified copy is needed.
func (c *Client) OperatingMode() *mode.OperatingMode {
	return c.opMode.Load()
}

// SetOperatingMode swaps the active operating mode. The new mode takes effect
// on the next Chat/Execute call — in-flight requests keep the mode they
// started with. Pass nil to clear the mode.
func (c *Client) SetOperatingMode(m *mode.OperatingMode) {
	if m == nil {
		c.opMode.Store(nil)
	} else {
		c.opMode.Store(m)
	}
	// Update agent system prompt to reflect mode change.
	// The base prompt is re-applied and the mode instruction appended.
	if c.agent != nil {
		c.mu.RLock()
		base := c.opts.systemPrompt
		c.mu.RUnlock()
		if m != nil && m.SystemInstruction != "" {
			modeInstr := strings.TrimSpace(m.SystemInstruction)
			if base != "" {
				c.agent.SetSystemPrompt(base + "\n\n" + modeInstr)
			} else {
				c.agent.SetSystemPrompt(modeInstr)
			}
		} else {
			c.agent.SetSystemPrompt(base)
		}
	}
}

// emitLifecycle dispatches a single event to every extraHook whose Filter
// matches. It is fire-and-forget from the caller's perspective — hook errors
// and block-actions are logged but do not propagate, because lifecycle events
// are observation points, not enforcement points (enforcement rides on the
// tool-surface hooks wired into clientHooksManager).
//
// Event naming follows the loader's mapping: SessionStart uses the Claude
// Code string "SessionStart"; agent-stopped uses the native SDK string
// "agent.stopped". Disk-loaded hooks produced by hooks/loader already carry
// the matching EventPatterns, so their Filter lines up without translation.
func (c *Client) emitLifecycle(ctx context.Context, eventType string, data map[string]any) {
	c.mu.RLock()
	hooksSnapshot := append([]hooks.Hook(nil), c.opts.extraHooks...)
	c.mu.RUnlock()
	if len(hooksSnapshot) == 0 {
		return
	}
	event := hooks.Event{
		Type: eventType,
		Data: data,
	}
	for _, h := range hooksSnapshot {
		if h == nil {
			continue
		}
		if !h.Filter(event) {
			continue
		}
		if _, err := h.OnEvent(ctx, event); err != nil {
			if c.logger != nil {
				c.logger.Warn(ctx, "client.lifecycle_hook_error",
					observability.F("hook", h.Name()),
					observability.F("event", eventType),
					observability.F("error", err.Error()))
			}
		}
	}
}

// emitAgentStopped raises an "agent.stopped" lifecycle event after a
// Chat/Execute returns. It is a no-op when no hooks are registered, and it
// never converts a hook failure into a user-facing error — the provider
// response is the source of truth.
func (c *Client) emitAgentStopped(ctx context.Context, conversationID string, resp *agent.ExecuteResponse, err error) {
	data := map[string]any{
		"conversation_id": conversationID,
		"source":          "client.Execute",
	}
	if err != nil {
		data["finish_reason"] = "error"
		data["error"] = err.Error()
	} else if resp != nil {
		data["finish_reason"] = string(resp.FinishReason)
		data["turn_count"] = resp.TurnCount
		data["input_tokens"] = resp.InputTokens
		data["output_tokens"] = resp.OutputTokens
	} else {
		data["finish_reason"] = "unknown"
	}
	c.emitLifecycle(ctx, hooks.EventAgentStopped, data)
}

// emitGenerateStopped mirrors emitAgentStopped for the Generate path. Generate
// bypasses the agent loop, so the payload keys differ (no turn count).
func (c *Client) emitGenerateStopped(ctx context.Context, resp *provider.ChatResponse, err error) {
	data := map[string]any{
		"source": "client.Generate",
	}
	if err != nil {
		data["finish_reason"] = "error"
		data["error"] = err.Error()
	} else if resp != nil {
		data["finish_reason"] = string(resp.FinishReason)
	} else {
		data["finish_reason"] = "unknown"
	}
	c.emitLifecycle(ctx, hooks.EventAgentStopped, data)
}

// injectModeFilter populates req.Context["mode_filter"] with a
// ModeFilterConfig derived from the active operating mode. Existing keys in
// req.Context are preserved; if the caller supplied their own mode_filter it
// takes precedence. A nil active mode leaves the request untouched.
func (c *Client) injectModeFilter(req *agent.ExecuteRequest) {
	m := c.opMode.Load()
	if m == nil {
		return
	}
	if req.Context == nil {
		req.Context = make(map[string]any)
	}
	if _, exists := req.Context["mode_filter"]; exists {
		return
	}
	req.Context["mode_filter"] = &agent.ModeFilterConfig{
		ModeName:         m.Name,
		AllowedTools:     append([]string(nil), m.AllowedTools...),
		BlockedTools:     append([]string(nil), m.BlockedTools...),
		HideBlockedTools: m.HideBlockedTools,
	}
}

// ─── Conversation helpers ────────────────────────────────────────────────────

// NewConversation creates a new conversation and returns its ID. A SessionStart
// lifecycle event is emitted to any registered hooks once the conversation is
// persisted; a creation error suppresses the emission (nothing has begun).
func (c *Client) NewConversation(ctx context.Context) (*conversation.Conversation, error) {
	if c.convManager == nil {
		return nil, fmt.Errorf("client: no conversation manager")
	}
	conv, err := c.convManager.Create(ctx, manager.CreateOptions{
		WorkspacePath: c.workspace,
		Mode:          "chat", // Default mode
	})
	if err != nil {
		return nil, err
	}
	c.emitLifecycle(ctx, string(hooks.EventSessionStart), map[string]any{
		"conversation_id": conv.ID,
		"workspace":       c.workspace,
		"source":          "client.NewConversation",
	})
	return conv, nil
}

// LoadConversation loads a single conversation by ID with full message history.
func (c *Client) LoadConversation(ctx context.Context, id string) (*conversation.Conversation, error) {
	if c.convStorage == nil {
		return nil, fmt.Errorf("client: no conversation storage")
	}
	return c.convStorage.Load(ctx, id)
}

// ListAllConversations returns all stored conversations with full message history.
// For sidebar/listing use cases prefer ListConversationsMeta which is O(100x) cheaper,
// or ListConversations which returns the IPC-friendly summary shape.
func (c *Client) ListAllConversations(ctx context.Context) ([]*conversation.Conversation, error) {
	if c.convStorage == nil {
		return nil, fmt.Errorf("client: no conversation storage")
	}
	return c.convStorage.Query(ctx, storage.Filter{})
}

// ListConversationsMeta returns conversations for a workspace with messages
// stripped (first message kept for title derivation).
// Pass workspacePath="" to list all workspaces (slow with many files).
// Pass the current working directory to scope to just that project.
func (c *Client) ListConversationsMeta(ctx context.Context, workspacePath string) ([]*conversation.Conversation, error) {
	return c.listConversationsMeta(ctx, workspacePath, false)
}

func (c *Client) listConversationsMeta(ctx context.Context, workspacePath string, includeHeadless bool) ([]*conversation.Conversation, error) {
	if c.convStorage == nil {
		return nil, fmt.Errorf("client: no conversation storage")
	}
	var excludeTags []string
	if !includeHeadless {
		excludeTags = []string{conversation.HeadlessTag}
	}
	return c.convStorage.Query(ctx, storage.Filter{
		WorkspacePath:       workspacePath,
		FirstMessagePreview: 1,
		SortBy:              "updated_at",
		SortOrder:           storage.SortDescending,
		// Keep non-interactive headless (`swarm -p`) runs out of browsable and
		// searchable TUI history. Both the conversation picker and the agent
		// HistorySearch tool funnel through this method, so excluding here is
		// the single choke point. Conversations remain on disk and reachable by
		// ID (resume / HistoryGet by id / delete are unaffected).
		ExcludeTags: excludeTags,
	})
}

// ListWorkspaces returns all workspace paths that have stored conversations,
// decoded from the storage directory names.
func (c *Client) ListWorkspaces() ([]string, error) {
	if c.convStorage == nil {
		return nil, fmt.Errorf("client: no conversation storage")
	}
	dir, ok := c.convStorage.(interface{ BaseDir() string })
	if !ok {
		return nil, fmt.Errorf("storage does not expose BaseDir")
	}
	entries, err := os.ReadDir(dir.BaseDir())
	if err != nil {
		return nil, err
	}
	var workspaces []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !storage.IsWorkspaceDir(e.Name()) {
			continue
		}
		decoded, err := storage.DecodeWorkspacePath(e.Name())
		// Legacy conversation-id directories are intentionally accepted by
		// storage.IsWorkspaceDir for history migration, but they are not
		// workspace paths and must not be advertised to remote workspace
		// switchers. Only the encoded absolute-path layout belongs here.
		if err != nil || decoded == "" || !filepath.IsAbs(decoded) {
			continue
		}
		workspaces = append(workspaces, decoded)
	}
	return workspaces, nil
}

// SetWorkspace changes the active workspace directory. All subsequent
// ListConversationsMeta calls will scope to this directory.
func (c *Client) SetWorkspace(dir string) {
	c.mu.Lock()
	c.workspace = dir
	c.opts.workspaceDir = dir
	c.mu.Unlock()
}

// DeleteConversation permanently removes a conversation by ID.
func (c *Client) DeleteConversation(ctx context.Context, id string) error {
	if c.convStorage == nil {
		return fmt.Errorf("client: no conversation storage")
	}
	return c.convStorage.Delete(ctx, id)
}

// ─── Config ───────────────────────────────────────────────────────────────────

// ClientConfig is a typed snapshot of the resolved client options.
// It is part of the daemon STATE surface: a remote UI (TUI/webapp) reads it to
// render the active session config without owning any of it.
type ClientConfig struct {
	Provider      string
	Model         string
	MaxTokens     int
	Temperature   float64
	ContextWindow int
	WorkspaceDir  string
	StorageDir    string
	SystemPrompt  string
}

// Config returns a typed snapshot of the resolved client options.
func (c *Client) Config() ClientConfig {
	return ClientConfig{
		Provider:      c.opts.providerName,
		Model:         c.opts.model,
		MaxTokens:     c.opts.maxTokens,
		Temperature:   c.opts.temperature,
		ContextWindow: c.opts.contextWindow,
		WorkspaceDir:  c.opts.workspaceDir,
		StorageDir:    c.opts.storageDir,
		SystemPrompt:  c.opts.systemPrompt,
	}
}

// ─── Lifecycle ────────────────────────────────────────────────────────────────

// Close stops background subsystems and flushes state.
// Always call Close when done with the client to ensure clean shutdown.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mcpManager != nil {
		c.mcpManager.Stop()
	}
	if c.agent != nil {
		return c.agent.Stop()
	}
	return nil
}

// MCPManager returns the MCP server manager, or nil if WithMCP was not set.
func (c *Client) MCPManager() *mcp.RuntimeManager { return c.mcpManager }

// CloudSyncToken returns the auth token registered via WithCloudSync, or "" if
// cloud sync was not requested. Callers that own a cloudsync.Manager (the IPC
// server today) read this to decide whether to start the sync loops. Full
// in-Client construction of the manager will land with the IPC cutover in
// PLAN.md Step 3.1.
func (c *Client) CloudSyncToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.opts.cloudSyncToken
}

// ─── Hook / Tool registration ─────────────────────────────────────────────────

// RegisterHook stores a lifecycle hook for future Reconfigure registrations.
func (c *Client) RegisterHook(h hooks.Hook) {
	c.mu.Lock()
	c.opts.extraHooks = append(c.opts.extraHooks, h)
	c.mu.Unlock()
}

// RegisterTool stores a custom tool for future Reconfigure registrations.
func (c *Client) RegisterTool(t tools.Tool) error {
	if c.agent == nil {
		return fmt.Errorf("client: agent not initialised")
	}
	c.mu.Lock()
	c.opts.extraTools = append(c.opts.extraTools, t)
	c.mu.Unlock()
	return nil
}

// AgentToolRegistry returns the agent's tool registry for permission
// configuration.  Returns nil if the agent is not initialized.  This is
// the low-level *tools.Registry handle; for the IPC-friendly summary
// (counts + names) used by SessionDriver consumers, see ToolRegistry()
// in state.go.
func (c *Client) AgentToolRegistry() tools.Registry {
	c.mu.RLock()
	active := c.agent
	c.mu.RUnlock()
	if active == nil {
		return nil
	}
	return active.ToolRegistry()
}

// ─── Re-configure ─────────────────────────────────────────────────────────────

// Reconfigure rebuilds the agent with new options, keeping existing storage.
// Useful after the user changes the provider in the UI.
func (c *Client) Reconfigure(optFns ...Option) error {
	if err := c.rejectHarnessGovernedMutation("Reconfigure"); err != nil {
		return err
	}
	oldProvider := c.opts.providerName
	newOpts := c.opts // start from current options
	for _, fn := range optFns {
		fn(&newOpts)
	}
	// Clear provider-scoped credentials if provider changed
	if newOpts.providerName != oldProvider {
		newOpts.apiKey = ""
		newOpts.baseURL = ""
	}
	if err := c.initAgent(newOpts); err != nil {
		return fmt.Errorf("client: reconfigure: %w", err)
	}
	c.opts = newOpts // only assign after success
	// Propagate cfgMgr so Reconfigure(WithConfigManager(...)) installs the
	// new manager onto the client (used by CRUD methods + SwitchProfile).
	if newOpts.configManager != nil {
		c.cfgMgr = newOpts.configManager
	}
	// Re-seed the operating-mode pointer so a caller passing a new
	// WithOperatingMode in Reconfigure sees it on the next turn.
	if newOpts.operatingMode != nil {
		c.SetOperatingMode(newOpts.operatingMode)
	} else {
		c.SetOperatingMode(nil)
	}
	return nil
}

// ─── internal ────────────────────────────────────────────────────────────────

func (c *Client) initStorage(dir string) error {
	_ = os.MkdirAll(dir, 0755)
	store, err := storage.NewDirectoryFileStorage(storage.DirectoryFileStorageConfig{
		BaseDir: dir,
	})
	if err != nil {
		// Fall back to in-memory storage so the app still works.
		// Log a warning: conversations will NOT be persisted across restarts.
		c.logger.Warn(context.Background(), "client.storage_init_failed_using_memory",
			observability.F("dir", dir),
			observability.F("error", err.Error()),
			observability.F("warning", "conversations will not persist across restarts"))
		c.convStorage = storage.NewMemoryStorage()
	} else {
		c.convStorage = store
	}

	mgr, err := manager.NewManager(manager.Config{
		Storage: c.convStorage,
		Logger:  c.logger,
		Tracer:  c.tracer,
	})
	if err != nil {
		return err
	}
	c.convManager = mgr
	return nil
}

func (c *Client) initAgent(o options) error {
	// Stop existing agent (but never stop a pre-supplied agentInstance — the
	// caller owns its lifecycle).
	if c.agent != nil && o.agentInstance == nil {
		c.agent.Stop()
		c.agent = nil
	}

	// agentInstance short-circuit: caller supplied a fully-built agent.
	// Skip all construction; just wire the fanout and record the reference.
	if o.agentInstance != nil {
		c.agent = o.agentInstance
		c.provider = c.agent.Provider()
		c.agent.SetIntermediateCallback(c.fanout)
		c.wireMessagePersistence()
		return nil
	}

	norm := normalizeProviderName(o.providerName)

	// Build a fresh registry and explicitly register providers.
	// We can't rely on init() side-effects because provider packages
	// require Register() to be called with a registry instance.
	reg := provider.NewSimpleRegistry(c.logger)

	// Register providers — log failures so they don't silently cause "provider not found" later.
	if err := provanthropic.Register(reg); err != nil {
		c.logger.Warn(context.Background(), "client.provider_register_failed",
			observability.F("provider", "anthropic"), observability.F("error", err.Error()))
	}
	if err := provopenai.Register(reg); err != nil {
		c.logger.Warn(context.Background(), "client.provider_register_failed",
			observability.F("provider", "openai"), observability.F("error", err.Error()))
	}
	if err := provgemini.Register(reg); err != nil {
		c.logger.Warn(context.Background(), "client.provider_register_failed",
			observability.F("provider", "gemini"), observability.F("error", err.Error()))
	}
	// Register aliases so OpenAI-compatible providers route to the openai factory.
	reg.RegisterAlias("z.ai", "openai")
	reg.RegisterAlias("zai", "openai")
	reg.RegisterAlias("glm", "openai")
	reg.RegisterAlias("cerebras", "openai")
	reg.RegisterAlias("openrouter", "openai")
	reg.RegisterAlias("codex", "openai")
	reg.RegisterAlias("groq", "openai")
	reg.RegisterAlias("fireworks", "openai")
	reg.RegisterAlias("deepseek", "openai")
	reg.RegisterAlias("mistral", "openai")
	reg.RegisterAlias("together", "openai")
	reg.RegisterAlias("moonshot", "openai")
	reg.RegisterAlias("replicate", "openai")
	reg.RegisterAlias("chutes", "openai")
	reg.RegisterAlias("qwen", "openai")
	reg.RegisterAlias("kimi", "openai")
	reg.RegisterAlias("wafer.ai", "openai")
	reg.RegisterAlias("wafer", "openai")
	reg.RegisterAlias("xai", "openai")
	reg.RegisterAlias("grok", "openai")
	reg.RegisterAlias("x.ai", "openai")
	reg.RegisterAlias("supergrok", "openai")
	// Anthropic-compatible aliases.
	reg.RegisterAlias("claudecode", "anthropic")

	// Register provider compatibility for aliases so agent.New's ProvidersMatch
	// check passes when the definition uses an alias name but the factory reports
	// its canonical underlying type (e.g., definition.Provider="xai", factory="openai").
	for _, alias := range []string{"xai", "grok", "x.ai", "supergrok", "xai-oauth", "grok-oauth"} {
		reg.RegisterCompatible(alias, "openai")
	}
	reg.RegisterCompatible("claudecode", "anthropic")
	// Ollama is registered inline below if selected

	// User-defined custom provider names from ~/.swarm/config/providers.json.
	// Route the custom name to whichever canonical factory matches its
	// declared api_type ("openai-compatible" → openai factory, etc.). The
	// custom name is preserved on the options so credential lookup still
	// pulls the right per-name api_key / base_url, and the compatibility
	// registry is updated so the agent definition's provider check (which
	// compares "fire" against the factory-reported "openai") passes.
	//
	// Use originalProviderName for the lookup since providerName may be the
	// collapsed canonical type (e.g., "openai" when the original name was
	// "wafer"), while the custom provider entry in providers.json is stored
	// under the original name.
	customLookupName := o.originalProviderName
	if customLookupName == "" {
		customLookupName = o.providerName
	}
	customProvInfo, customProvFound := lookupCustomProvider(customLookupName)
	if customProvFound {
		if canonical := canonicalProviderForAPIType(customProvInfo.APIType); canonical != "" {
			if strings.ToLower(o.providerName) != canonical {
				reg.RegisterAlias(strings.ToLower(o.providerName), canonical)
			}
			reg.RegisterCompatible(o.providerName, canonical)
		}
	}

	// toolRegistryInstance short-circuit: caller supplied a pre-built registry.
	// Use it directly; skip all the default forge/builtin registration below.
	var toolReg tools.Registry
	if o.toolRegistryInstance != nil {
		toolReg = o.toolRegistryInstance
	} else {
		toolReg = tools.NewSimpleRegistry(c.logger, c.tracer)
	}

	// Register forge tools for file operations
	workspace := o.workspaceDir
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}

	// Only register default tools when no pre-built registry was supplied.
	// A pre-built registry already has all the tools the caller wants.
	if o.toolRegistryInstance == nil {
		// Register essential tools for file operations and shell commands
		if err := toolReg.Register(forge.NewFSRead(workspace)); err != nil {
			c.logger.Warn(context.Background(), "client.tool_register_failed",
				observability.F("tool", "Read"), observability.F("error", err.Error()))
		}
		if err := forge.RegisterFileEditingTools(toolReg, workspace); err != nil {
			c.logger.Warn(context.Background(), "client.file_editing_tools_register_failed",
				observability.F("error", err.Error()))
		}
	}

	// ── Extended tool registration ───────────────────────────────────────
	registerTool := func(t tools.Tool) {
		if err := toolReg.Register(t); err != nil {
			c.logger.Warn(context.Background(), "client.tool_register_failed",
				observability.F("tool", t.Name()), observability.F("error", err.Error()))
		}
	}

	if o.toolRegistryInstance == nil {
		if o.enableAllTools || o.enableBuiltinTools {
			// Default the bash tool's working directory to the workspace so
			// `pwd` and relative paths resolve inside the opened project, not
			// the daemon's process cwd ($HOME). Without this a phone/desktop
			// that opened /path/to/project still ran shell commands in $HOME.
			bashCfg := toolsbuiltin.DefaultBashConfig()
			bashCfg.DefaultCwd = workspace
			registerTool(toolsbuiltin.NewBashToolWithConfig(bashCfg))
			registerTool(toolsbuiltin.NewAnnoyedToolWithConfig(toolsbuiltin.AnnoyedConfig{
				LoadConversation: c.LoadConversation,
			}))
			registerTool(toolsbuiltin.NewListDirTool())
			registerTool(toolsbuiltin.NewAgentBrowserTool())
		}

		if o.enableAllTools || o.enableIITools {
			// apply_patch is an essential tool registered above; II mode must not
			// advertise a second implementation or duplicate schema.
			// Register TaskManage as the sole live productivity tool. Historical
			// task calls remain renderable, but legacy names are not executable.
			// Every entry writes to the SAME global todo manager
			// (ii.GetTodoManager()) that the task-enforcement hook reads, so the
			// agent can create the task that the hook requires. WITHOUT these, the
			// task-enforcement hook blocks every tool with "YOU CANNOT EXECUTE ANY
			// TOOL WITHOUT A TASK" and the agent has no way to comply (catch-22).
			if err := ii.RegisterProductivityTools(toolReg); err != nil {
				c.logger.Warn(context.Background(), "client.productivity_tools_register_failed",
					observability.F("error", err.Error()))
			}
		}

		if o.enableAllTools || o.enableWebSearch {
			registerTool(websearch.New())
		}

		if o.enableAllTools || o.enableProjectMemory {
			if err := projectmemory.RegisterAllTools(toolReg); err != nil {
				c.logger.Warn(context.Background(), "client.projectmemory_register_failed",
					observability.F("error", err.Error()))
			}
		}
	}

	// Register extra tools supplied via WithTool.
	for _, t := range o.extraTools {
		registerTool(t)
	}

	// Resolve API key / OAuth token at agent-init time.
	apiKey := o.apiKey
	baseURL := o.baseURL
	if apiKey == "" {
		// Use = not := to assign to outer variables (avoid shadowing)
		var baseURL2 string
		// Use originalProviderName for credential lookup to ensure
		// OpenAI-compatible providers (fireworks, groq, etc.) look up
		// their specific API keys (FIREWORKS_API_KEY) rather than
		// the generic OPENAI_API_KEY.
		credProvider := o.originalProviderName
		if credProvider == "" {
			credProvider = o.providerName
		}
		apiKey, baseURL2 = resolveCredentials(credProvider)
		if baseURL == "" {
			baseURL = baseURL2
		}
		// Custom-provider fallback: when the user has registered a custom
		// name in providers.json with a base_url but no credentials.json
		// entry, resolveCredentials returns "". Pull the base_url from
		// the providers.json record so OpenAI-compatible custom names
		// like "fire" → fireworks.ai still reach the right endpoint.
		if baseURL == "" && customProvFound && customProvInfo.BaseURL != "" {
			baseURL = customProvInfo.BaseURL
		}
	}

	// For Anthropic OAuth: if apiKey looks like an OAuth access_token, pass is_oauth=true.
	isOAuth := false
	if norm == "anthropic" && isOAuthToken(apiKey) {
		isOAuth = true
	}
	// Remember OAuth status so SystemPromptComposition can mirror the request-time
	// CLI prefix the provider prepends inside Chat().
	c.oauthActive = isOAuth

	// Build provider config — Logger and Tracer are first-class fields now.
	// is_oauth still goes through Custom since it's Anthropic-specific.
	provCfg := provider.Config{
		Name:    norm,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   o.model,
		Logger:  c.logger,
		Tracer:  c.tracer,
		Custom: map[string]any{
			"is_oauth": isOAuth,
		},
	}
	if o.contextWindow > 0 {
		provCfg.ContextWindow = o.contextWindow
	} else if cw, ok := LookupModelContextWindow(o.providerName, o.model); ok {
		provCfg.ContextWindow = cw
	}
	if builtin, ok := provider.LookupBuiltinProvider(o.providerName); ok {
		if provCfg.BaseURL == "" {
			provCfg.BaseURL = builtin.BaseURL
		}
		provCfg.HTTPMaxRetries = builtin.HTTPMaxRetries
	}
	if customProvFound && customProvInfo.HTTPMaxRetries != nil {
		provCfg.HTTPMaxRetries = customProvInfo.HTTPMaxRetries
	}

	// providerInstance short-circuit: caller supplied a pre-built provider.
	// Skip the registry lookup and use it directly.  providerName/model are
	// still honoured for metadata (agent definition, context-window lookup).
	var prov provider.Provider
	var err error
	if o.providerInstance != nil {
		prov = o.providerInstance
	} else if norm == "ollama" {
		// Ollama doesn't use the registry — build directly.
		prov, err = buildOllama(o.model, o.baseURL, c.logger, c.tracer)
	} else {
		prov, err = reg.Create(provCfg)
	}
	if err != nil {
		return fmt.Errorf("create provider %q: %w", o.providerName, err)
	}

	if o.enableNestedAgents {
		if err := registerNestedAgentTools(c.logger, c.tracer, reg, toolReg, provCfg, workspace); err != nil {
			return fmt.Errorf("register nested-agent tools: %w", err)
		}
	}

	if o.enableSkills {
		if err := registerSkillsTool(c, toolReg, workspace, o.skillPaths); err != nil {
			c.logger.Warn(context.Background(), "client.skills_init_failed",
				observability.F("error", err.Error()))
		}
	}

	// Resolve machine ID hash once at construction time (best-effort).
	machineIDHash, _ := analytics.DeviceIDHash("swarm.sdk.v1")

	// Default to ClientTypeSDK when the caller did not specify a surface.
	clientType := o.clientType
	if clientType == "" {
		clientType = ClientTypeSDK
	}

	def := &agent.Definition{
		ID:            uuid.New().String(),
		Name:          "Swarm",
		Provider:      norm,
		Model:         o.model,
		SystemPrompt:  o.systemPrompt,
		ClientType:    clientType,
		MachineIDHash: machineIDHash,
	}

	// ── Build agent config ──────────────────────────────────────────────
	agentCfg := agent.Config{
		Definition:       def,
		Provider:         prov,
		ProviderRegistry: reg,
		ToolRegistry:     toolReg,
		Logger:           c.logger,
		Tracer:           c.tracer,
		// Auditor defaults to noop inside agent.New when nil.
	}

	// Wire compaction if enabled.
	if o.enableCompaction {
		if o.compactionConfig != nil {
			agentCfg.AutoCompaction = *o.compactionConfig
		} else {
			agentCfg.AutoCompaction = agent.DefaultAutoCompactionConfig()
		}
		agentCfg.CompactionService = compaction.NewService(compaction.CompactionConfig{})
		agentCfg.FileTracker = filetracker.NewRecorder()
	}

	// Wire task store if enabled.
	//
	// This build-time store is only a fallback for callers that never pass a
	// conversationID. The real, persistent store is wired per-conversation in
	// chatCtxWithOpts via agent.SetupTaskPersistence, which loads/saves
	// ~/.swarm/conversations/<id>/metadata/task.json AND wires the TodoManager
	// syncer so task mutations auto-persist. Without that per-conversation setup
	// this store is never connected to the syncer and would not persist.
	if o.enableTaskStore {
		storeDir := filepath.Join(c.workspace, ".swarm", "tasks")
		_ = os.MkdirAll(storeDir, 0755)
		agentCfg.TaskStore = taskstore.New(taskstore.Config{
			MetadataDir: storeDir,
		})
	}

	// Wire workspace path for A2A and storage.
	agentCfg.WorkspacePath = c.workspace
	agentCfg.StoragePath = o.storageDir

	// Wire code mode if enabled.
	if o.enableCodeMode {
		var cm *codemode.CodeMode
		if o.codeModeConfig != nil {
			cm = codemode.NewFromConfig(o.codeModeConfig)
		} else {
			cm = codemode.NewFromConfig(&codemode.Config{
				Enabled: true,
				Persist: true,
			})
		}
		agentCfg.CodeModeInstaller = cm
	}

	a, err := agent.New(agentCfg)
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}

	c.agent = a
	c.agentDef = def
	c.provider = prov

	// Re-apply the no-fallback policy to the freshly-built agent so it survives
	// Reconfigure (provider/model switches rebuild the agent).
	if o.noFallback {
		a.SetNoFallback(true)
	}

	// (Re)wire transcript persistence onto the freshly-built agent so generated
	// messages land in the conversation store across provider switches.
	c.wireMessagePersistence()

	// Apply the resolved context window to the agent. Explicit
	// WithContextWindow always wins; otherwise look up the model in
	// ~/.swarm/config/providers.json. When neither produces a value the agent
	// falls back to provider.Capabilities().MaxContextWindow inside
	// getContextWindow, so nothing needs to be set in that case.
	if provCfg.ContextWindow > 0 {
		a.SetConfiguredContextWindow(provCfg.ContextWindow)
	}

	// Install a custom permission checker when the caller requested one.
	// For "interactive" mode, use the interactive approval checker that
	// emits EventApprovalRequested and blocks until RespondApproval.
	// Skip when a pre-built registry was supplied — it already has its checker.
	if o.toolRegistryInstance == nil {
		checker := approvalCheckerFor(o.approvalMode)
		if checker == nil && o.approvalMode == "interactive" {
			checker = c.NewInteractiveApprovalChecker()
		}
		if checker != nil {
			// SetPermissionChecker is only on *tools.SimpleRegistry, not the interface.
			type permChecker interface {
				SetPermissionChecker(tools.PermissionChecker)
			}
			if pc, ok := toolReg.(permChecker); ok {
				pc.SetPermissionChecker(checker)
			}
		}
	}

	// Wire the subscriber fanout onto the new agent so Subscribe() listeners
	// receive IntermediateUpdate events. Subscribers outlive Reconfigure —
	// they live on the Client — so re-wire fanout on every agent rebuild.
	a.SetIntermediateCallback(c.fanout)

	// ── Post-creation wiring ─────────────────────────────────────────────

	// Load hooks from disk into extraHooks before wiring the hooks manager so
	// they are counted when deciding whether the manager is needed. An
	// explicit WithHooksFromPath wins over WithHooksFromConfig's search.
	if o.hooksFromPath != "" {
		cfg, loadErr := hooksloader.LoadFromFile(o.hooksFromPath)
		if loadErr != nil {
			return fmt.Errorf("load hooks from %q: %w", o.hooksFromPath, loadErr)
		}
		if cfg == nil {
			return fmt.Errorf("load hooks from %q: file not found or empty", o.hooksFromPath)
		}
		if built, buildErr := cfg.ToHooks(); buildErr != nil {
			return fmt.Errorf("build hooks from %q: %w", o.hooksFromPath, buildErr)
		} else {
			o.extraHooks = append(o.extraHooks, built...)
			c.opts.extraHooks = append(c.opts.extraHooks, built...)
		}
	} else if o.hooksFromConfig {
		cfg, loadErr := hooksloader.LoadDefault(workspace)
		if loadErr != nil {
			return fmt.Errorf("load hooks from default locations: %w", loadErr)
		}
		if cfg == nil {
			// No default hooks config found — not an error, skip silently.
		} else if built, buildErr := cfg.ToHooks(); buildErr != nil {
			return fmt.Errorf("build hooks from default locations: %w", buildErr)
		} else {
			o.extraHooks = append(o.extraHooks, built...)
			c.opts.extraHooks = append(c.opts.extraHooks, built...)
		}
	}

	// Wire hooks manager if any hook options are enabled.
	// Note: Steering pre-tool hook requires a SteeringAgent with its own LLM
	// provider, which is set up via SetHooksManager by the caller or via
	// the full headless bridge. Task enforcement and extra hooks work standalone.
	// clientHM is captured so generic extra hooks (autogenskills lifecycle +
	// budget, dream) can be bound to it AFTER they are appended below.
	var clientHM *clientHooksManager
	annoyedEnabled := toolReg.IsRegistered("annoyed")
	if o.enableDefaultHooks || o.enableTaskEnforce || o.enableSleepBlocker || o.enableGitUserEnforce || o.enableAutoModeHook || o.enableRecapHook || o.enableAutogenSkills || annoyedEnabled || len(o.extraHooks) > 0 {
		hm := &clientHooksManager{logger: c.logger}
		// Passive, fail-open observation only: no context injection and no
		// blocking. Keeping it in the shared client path makes headless and TUI
		// runs produce the same vocabulary provenance.
		hm.extra = append(hm.extra, dreambuiltin.NewVocabularyObserverHook())
		clientHM = hm
		if annoyedEnabled {
			enableAnnoyanceNudgeForRegistry(hm, toolReg)
		}
		if o.enableTaskEnforce || o.enableDefaultHooks {
			hm.taskEnforcement = dreambuiltin.NewTaskEnforcementHook()
		}
		if o.enableSleepBlocker || o.enableDefaultHooks {
			hm.sleepBlocker = dreambuiltin.NewSleepBlockerHook()
		}
		if o.enableSleepBlocker || o.enableDefaultHooks {
			// Advisory-only: never blocks, so it is safe wherever the bash
			// command hooks run. Registered here (not only in the TUI hook
			// manager) so headless `swarm -p` and sub-agents also get it.
			hm.stdinConflict = dreambuiltin.NewStdinConflictHook()
		}
		if o.enableGitUserEnforce || o.enableDefaultHooks {
			hm.gitUserEnforcement = dreambuiltin.NewGitUserEnforcementHook()
		}
		if o.enableProtectedBranch {
			// Enabled explicitly => actively protect default branches out of the
			// box (config file can still override or disable). Not wired into
			// enableDefaultHooks so it stays strictly opt-in.
			hm.protectedBranch = dreambuiltin.NewProtectedBranchHookEnabled()
		}
		if o.enableAutoModeHook {
			var opts []dreambuiltin.AutoModeOption
			if o.autoModeConfig != nil {
				opts = append(opts, dreambuiltin.WithAutoModeConfig(*o.autoModeConfig))
			}
			hm.autoMode = dreambuiltin.NewAutoModeHook(c.logger, opts...)
		}
		if o.enableRecapHook {
			var opts []dreambuiltin.RecapOption
			if o.recapConfig != nil {
				opts = append(opts, dreambuiltin.WithRecapConfig(*o.recapConfig))
			}
			hm.recap = dreambuiltin.NewRecapHook(c.logger, opts...)
		}
		// Compact task-audit: attach sanitized tool events to the active task.
		// Registered as a generic extra hook so it runs in the after-execute
		// pipeline alongside standalone-client usage.
		if o.enableTaskEnforce || o.enableDefaultHooks {
			hm.extra = append(hm.extra, dreambuiltin.NewTaskAuditHook())
		}
		a.SetHooksManager(hm)
	}

	// Wire credential rotation if enabled.
	if o.enableCredRotation {
		if credStore, csErr := agent.NewCredentialStoreFromTuiAccounts(nil); csErr == nil && credStore.CredentialCount() > 1 {
			a.SetCredentialStore(credStore)
		}
	}

	// Create autogenskills service if enabled.
	// autogenReg is declared here so the skillNameGetter closure below can
	// reference it even after the if-block completes.
	var autogenReg *skills.Registry
	var autogenService *autogenskills.Service
	if o.enableAutogenSkills {
		cfg := o.autogenSkillsConfig
		if cfg == nil {
			cfg = &autogenskills.Config{
				Mode: autogenskills.ModeAuto,
				// Budget thresholds match the TUI: a 5-call onboarding budget
				// (hard block until the agent creates/uses a skill) then a 90-call
				// working budget (soft nudge). SkillManage + Skill tools are exempt
				// so the agent can always comply (no dead-end).
				Trigger: autogenskills.TriggerConfig{
					ToolCallThreshold: 10,
					ToolCallBudget:    5,
					WorkingBudget:     90,
					MaxNudgeIgnores:   3,
					NudgeInterval:     5,
				},
				// Curator lifecycle: leave the staged stale→archive ladder at
				// the Hermes-derived defaults (StaleAfterDays=30, ArchiveAfterDays=90)
				// applied by WithDefaults(). Consolidate stays off by default.
				Curator:    autogenskills.CuratorConfig{Interval: "1h"},
				AutogenDir: paths.AutogenSkillsDir(),
			}
		}

		// Create a skills loader rooted at the autogen dir so that:
		// 1. Existing autogen skills on disk are discovered at startup.
		// 2. Newly-created skills are registered into the same registry and
		//    immediately available via the Skill tool without a restart.
		autogenLoader := skills.NewLoader(cfg.AutogenDir)
		if initErr := autogenLoader.Initialize(context.Background()); initErr != nil {
			c.logger.Warn(context.Background(), "client.autogenskills_loader_init_failed",
				observability.F("error", initErr.Error()))
		}
		autogenReg = autogenLoader.Registry
		c.skillRegistry = autogenReg

		// Create service with the shared registry and no metrics
		var err error
		autogenService, err = autogenskills.NewService(*cfg, autogenReg, nil)
		if err != nil {
			return fmt.Errorf("create autogenskills service: %w", err)
		}

		// Add lifecycle hook to extraHooks so it gets registered
		if hook, err := autogenskills.NewLifecycleHook(autogenService); err == nil {
			o.extraHooks = append(o.extraHooks, hook)
		}

		// Wire the two-tier budget enforcement hook (onboarding hard-block →
		// working soft-nudge) and keep a reference so the budget state can be
		// surfaced for UI display via Client.SkillBudget(). Returns nil when the
		// budget is disabled (ToolCallBudget == 0), in which case nothing is wired.
		if budgetHook := autogenskills.NewBudgetEnforcementHook(autogenService, *cfg); budgetHook != nil {
			o.extraHooks = append(o.extraHooks, budgetHook)
			c.skillBudgetHook = budgetHook
		}

		// Register skill manage tool
		skillTool, err := autogenskills.NewSkillManageTool(autogenService)
		if err == nil {
			if err := a.ToolRegistry().Register(skillTool); err != nil {
				c.logger.Warn(context.Background(), "client.autogenskills_tool_register_failed",
					observability.F("error", err.Error()))
			}
		}
	}

	// Inject skill guidance and index into the system prompt so the
	// agent knows about the SkillManage tool and existing skills.
	if autogenService != nil {
		var promptAdditions []string
		if guidance := autogenService.GetSKILLSGuidance(); guidance != "" {
			promptAdditions = append(promptAdditions, guidance)
		}
		if index := autogenService.GetSkillIndex(); index != "" {
			promptAdditions = append(promptAdditions, index)
		}
		if len(promptAdditions) > 0 {
			currentPrompt := a.SystemPrompt()
			updatedPrompt := currentPrompt + "\n\n" + strings.Join(promptAdditions, "\n\n")
			a.SetSystemPrompt(updatedPrompt)
		}
	}

	// Inject the fixed operating-rules block when the skill-budget enforcement
	// is active (c.skillBudgetHook is non-nil only when the budget is on). This
	// tells the agent the two non-negotiable startup rules up front — create a
	// task first, and create/invoke a skill early to unlock the working budget —
	// rather than surfacing them reactively only after the onboarding budget is
	// exhausted. Gated on the budget hook so the "unlock the budget" instruction
	// is never shown when there is no budget to unlock.
	if c.skillBudgetHook != nil {
		rulesBlock := "<context name=\"operatingRules\">\n" +
			"MANDATORY WORKFLOW:\n" +
			"1. Create a task FIRST. Use TaskManage with a create operation to plan your work before " +
			"acting on any non-trivial request.\n" +
			"2. Skill budget: you start with a small onboarding tool-call budget. To " +
			"unlock the full working budget, create OR invoke a skill early " +
			"(SkillManage create/patch, or the Skill tool). Capture reusable patterns " +
			"as skills.\n" +
			"These are operating requirements, not suggestions.\n" +
			"</context>"
		a.SetSystemPrompt(a.SystemPrompt() + "\n\n" + rulesBlock)
	}

	// Inject agent operational metadata so the agent can reason about its
	// runtime context: when the session started, which session it is, which
	// model and provider are active, and which profile (if any) was selected.
	// Satisfies SWA-15; enables smarter self-referential reasoning and
	// easier post-mortem analysis of agent outputs.
	{
		var meta strings.Builder
		meta.WriteString(fmt.Sprintf("timestamp: %s\n", time.Now().Format(time.RFC3339)))
		meta.WriteString(fmt.Sprintf("session_id: %s\n", c.sessionID))
		if o.providerName != "" {
			meta.WriteString(fmt.Sprintf("provider: %s\n", o.providerName))
		}
		if o.model != "" {
			meta.WriteString(fmt.Sprintf("model: %s\n", o.model))
		}
		if o.initialProfile != "" {
			meta.WriteString(fmt.Sprintf("profile: %s\n", o.initialProfile))
		}
		if clientType != "" {
			meta.WriteString(fmt.Sprintf("client_type: %s\n", clientType))
		}
		metaBlock := "<context name=\"agentMetadata\">\n" + strings.TrimSpace(meta.String()) + "\n</context>"
		a.SetSystemPrompt(a.SystemPrompt() + "\n\n" + metaBlock)
	}

	// Apply operating mode instruction directly to the system prompt.
	// Mode changes are handled by SetOperatingMode updating the agent's
	// system prompt in-place — no ephemeral per-request injection.
	if o.operatingMode != nil && o.operatingMode.SystemInstruction != "" {
		base := o.systemPrompt
		modeInstr := strings.TrimSpace(o.operatingMode.SystemInstruction)
		if base != "" {
			a.SetSystemPrompt(base + "\n\n" + modeInstr)
		} else {
			a.SetSystemPrompt(modeInstr)
		}
	}

	// Task nudge removed from ephemeral — the TUI implements it as a
	// PostToolUse hook that returns persistent RoleUser messages. Headless
	// servers should migrate to the same hook-based approach.

	// Bind generic extra hooks (autogenskills lifecycle + budget, disk-loaded)
	// to the client hooks manager now that they have all been appended above —
	// otherwise they are silently never emitted in standalone-client mode.
	if clientHM != nil {
		clientHM.extra = o.extraHooks
	}

	// Wire agent initialization (resolves tools, etc.).
	if err := a.Initialize(); err != nil {
		c.logger.Warn(context.Background(), "client.agent_initialize_warning",
			observability.F("error", err.Error()))
	}

	// ── MCP manager ──────────────────────────────────────────────────────
	// Built after the agent's tool registry exists so MCP tools can be
	// registered into it. Any connection error is non-fatal — MCP servers
	// are best-effort; the agent stays usable without them.
	if o.enableMCP {
		if err := c.initMCPManager(o); err != nil {
			c.logger.Warn(context.Background(), "client.mcp_init_failed",
				observability.F("error", err.Error()))
		}
	}

	return nil
}

// initMCPManager wires the MCP manager into the freshly-built agent's tool
// registry and kicks off auto-connect servers.
func (c *Client) initMCPManager(o options) error {
	configDir := o.mcpConfigPath
	if configDir == "" {
		// Canonical global MCP config lives at ~/.swarm/config/mcp_servers.json;
		// the config manager joins "mcp_servers.json" onto this dir.
		configDir = paths.Config()
	}
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("mcp config manager: %w", err)
	}

	cfgMgr := mcp.NewConfigManager(configDir, c.workspace, nil, nil)

	toolReg := c.agent.ToolRegistry()
	mgr := mcp.NewRuntimeManager(cfgMgr, toolReg, nil, c.logger, c.tracer)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("mcp start: %w", err)
	}

	c.mcpManager = mgr
	return nil
}

// ─── Config auto-loading ─────────────────────────────────────────────────────

// loadSwarmosConfig reads ~/.swarm/config.{yaml,yml,json} and applies it to o.
// Caller-provided values (already set in o) are not overwritten. It resolves
// the YAML-default config while still reading a legacy config.json.
func loadSwarmosConfig(o *options) error {
	var cfg swarmosConfig
	if _, err := configformat.Load(paths.Config(), "config", &cfg); err != nil {
		return err
	}
	if o.providerName == "" && cfg.CurrentProvider != "" {
		o.providerName = cfg.CurrentProvider
		// CurrentProvider is the raw provider name (e.g., "wafer"),
		// which is also the original name for credential lookup.
		if o.originalProviderName == "" {
			o.originalProviderName = cfg.CurrentProvider
		}
	}
	if o.model == "" && cfg.CurrentModel != "" {
		o.model = cfg.CurrentModel
	}
	return nil
}

// ResolveCredentials is the canonical credential resolver for the Swarm SDK.
// It looks up the API key and optional base URL for the named provider using
// the same precedence order that client.New() uses internally:
//
//  1. Environment variable  (ANTHROPIC_API_KEY, OPENAI_API_KEY, …)
//  2. ~/.swarm/config/credentials.json
//  3. OAuth token (Anthropic only)
//
// Use this instead of duplicating credential resolution in CLI tools or tests.
func ResolveCredentials(providerName string) (apiKey, baseURL string) {
	return resolveCredentials(providerName)
}

// ParseModelString splits a "provider:model" (or "provider/model") spec into
// its two parts. Returns an error if the string lacks a separator.
//
// Examples:
//
//	ParseModelString("anthropic:claude-sonnet-4-5") // ("anthropic", "claude-sonnet-4-5", nil)
//	ParseModelString("openai/gpt-4o")               // ("openai", "gpt-4o", nil)
func ParseModelString(spec string) (providerName, modelName string, err error) {
	s := strings.TrimSpace(spec)
	if s == "" {
		return "", "", fmt.Errorf("empty model string")
	}
	for _, sep := range []string{":", "/"} {
		if i := strings.Index(s, sep); i > 0 && i < len(s)-1 {
			return s[:i], s[i+1:], nil
		}
	}
	return "", "", fmt.Errorf("invalid model format %q: expected 'provider:model' or 'provider/model'", spec)
}

// ─── ProviderOption ──────────────────────────────────────────────────────────

// ProviderOption configures the package-level NewProvider factory.
type ProviderOption func(*providerOptions)

type providerOptions struct {
	apiKey  string
	baseURL string
	logger  observability.Logger
	tracer  observability.Tracer
}

// WithProviderAPIKey overrides credential resolution with an explicit API key.
func WithProviderAPIKey(k string) ProviderOption {
	return func(o *providerOptions) { o.apiKey = k }
}

// WithProviderBaseURL overrides the base URL (for OpenAI-compatible providers).
func WithProviderBaseURL(u string) ProviderOption {
	return func(o *providerOptions) { o.baseURL = u }
}

// WithProviderLogger injects an observability.Logger. Defaults to noop.
func WithProviderLogger(l observability.Logger) ProviderOption {
	return func(o *providerOptions) { o.logger = l }
}

// WithProviderTracer injects an observability.Tracer. Defaults to noop.
func WithProviderTracer(t observability.Tracer) ProviderOption {
	return func(o *providerOptions) { o.tracer = t }
}

// NewProvider builds a provider.Provider from a provider name and model,
// performing the same alias resolution, credential lookup, OAuth detection,
// and OpenAI-compatible base-URL routing the full Client does — but without
// constructing an agent, tool registry, or conversation store.
//
// Use this when you need direct provider access (e.g. to hand-roll a
// streaming call or wire a provider into a non-client code path) and still
// want the full Swarm credential chain.
func NewProvider(name, model string, opts ...ProviderOption) (provider.Provider, error) {
	po := providerOptions{}
	for _, fn := range opts {
		fn(&po)
	}
	if po.logger == nil {
		po.logger = noop.NewLogger()
	}
	if po.tracer == nil {
		po.tracer = noop.NewTracer()
	}

	norm := normalizeProviderName(name)

	// Resolve credentials if not overridden.
	apiKey := po.apiKey
	baseURL := po.baseURL
	if apiKey == "" {
		var resolvedBase string
		apiKey, resolvedBase = resolveCredentials(norm)
		if baseURL == "" {
			baseURL = resolvedBase
		}
	}

	// Ollama is special: build directly without the shared registry.
	if norm == "ollama" {
		return buildOllama(model, baseURL, po.logger, po.tracer)
	}

	// Detect Anthropic OAuth tokens.
	isOAuth := false
	if norm == "anthropic" && isOAuthToken(apiKey) {
		isOAuth = true
	}

	// Build a registry identical to initAgent's so alias routing behaves the same.
	reg := provider.NewSimpleRegistry(po.logger)
	if err := provanthropic.Register(reg); err != nil {
		po.logger.Warn(context.Background(), "client.provider_register_failed",
			observability.F("provider", "anthropic"), observability.F("error", err.Error()))
	}
	if err := provopenai.Register(reg); err != nil {
		po.logger.Warn(context.Background(), "client.provider_register_failed",
			observability.F("provider", "openai"), observability.F("error", err.Error()))
	}
	if err := provgemini.Register(reg); err != nil {
		po.logger.Warn(context.Background(), "client.provider_register_failed",
			observability.F("provider", "gemini"), observability.F("error", err.Error()))
	}
	// OpenAI-compatible aliases.
	for _, alias := range []string{
		"z.ai", "zai", "glm", "cerebras", "openrouter", "codex", "groq",
		"fireworks", "deepseek", "mistral", "together", "moonshot",
		"replicate", "chutes", "qwen", "kimi", "wafer", "wafer.ai",
		"plexus",
	} {
		reg.RegisterAlias(alias, "openai")
	}
	reg.RegisterAlias("claudecode", "anthropic")

	cfg := provider.Config{
		Name:    norm,
		APIKey:  apiKey,
		BaseURL: baseURL,
		Model:   model,
		Logger:  po.logger,
		Tracer:  po.tracer,
		Custom: map[string]any{
			"is_oauth": isOAuth,
		},
	}
	if builtin, ok := provider.LookupBuiltinProvider(name); ok {
		if cfg.BaseURL == "" {
			cfg.BaseURL = builtin.BaseURL
		}
		cfg.HTTPMaxRetries = builtin.HTTPMaxRetries
	}
	if info, ok := lookupCustomProvider(name); ok {
		if cfg.BaseURL == "" {
			cfg.BaseURL = info.BaseURL
		}
		if info.HTTPMaxRetries != nil {
			cfg.HTTPMaxRetries = info.HTTPMaxRetries
		}
	}
	prov, err := reg.Create(cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider %q: %w", name, err)
	}
	return prov, nil
}

// resolveCredentials looks up API key and base URL for a provider in this order:
//  1. Environment variable  (ANTHROPIC_API_KEY, OPENAI_API_KEY, …)
//  2. ~/.swarm/config/credentials.json
//  3. ~/.swarm/config/providers.json  (settings UI / TUI store keys here)
//  4. OAuth token (Anthropic only)
func resolveCredentials(providerName string) (apiKey, baseURL string) {
	norm := strings.ToLower(strings.TrimSpace(providerName))

	// 1. Environment variables
	envMap := map[string]string{
		"anthropic":  "ANTHROPIC_API_KEY",
		"claudecode": "ANTHROPIC_API_KEY",
		"openai":     "OPENAI_API_KEY",
		"gemini":     "GEMINI_API_KEY",
		"google":     "GEMINI_API_KEY",
		"cerebras":   "CEREBRAS_API_KEY",
		"groq":       "GROQ_API_KEY",
		"openrouter": "OPENROUTER_API_KEY",
		"fireworks":  "FIREWORKS_API_KEY",
		"deepseek":   "DEEPSEEK_API_KEY",
		"mistral":    "MISTRAL_API_KEY",
		"together":   "TOGETHER_API_KEY",
		"moonshot":   "MOONSHOT_API_KEY",
		"replicate":  "REPLICATE_API_KEY",
		"xai":        "XAI_API_KEY",
		"grok":       "XAI_API_KEY",
		"ollama":     "", // no key needed
	}
	if envVar, ok := envMap[norm]; ok && envVar != "" {
		if v := os.Getenv(envVar); v != "" {
			// For OpenAI-compatible providers, also return the default base URL
			defaultBase := defaultBaseURLFor(norm)
			return v, defaultBase
		}
	}

	// 1b. Convention-based env var for providers not in the hardcoded map.
	// For custom/OpenAI-compatible providers like "wafer", try:
	//   - WAFER_API_KEY (underscored, uppercased provider name)
	//   - WAFER_AI_API_KEY (dots replaced with underscores)
	conventionEnvVar := strings.ToUpper(strings.ReplaceAll(norm, ".", "_")) + "_API_KEY"
	if v := os.Getenv(conventionEnvVar); v != "" {
		defaultBase := defaultBaseURLFor(norm)
		return v, defaultBase
	}

	// 2. ~/.swarm/config/credentials.json
	if key, base := readCredentialsFile(norm); key != "" {
		// providers.json is the file the settings UI edits — its base_url is
		// authoritative when both files define one. credentials.json keeps a
		// copy from when the key was first saved, which goes stale when the
		// user later changes the endpoint in the UI.
		if info, ok := lookupCustomProvider(norm); ok && info.BaseURL != "" {
			base = info.BaseURL
		}
		// If base URL is empty but this is an OpenAI-compatible provider, use default
		if base == "" {
			base = defaultBaseURLFor(norm)
		}
		return key, base
	}

	// 3. ~/.swarm/config/providers.json (fallback for TUI / settings UI stored keys)
	if key, base := readProvidersJSON(norm); key != "" {
		if base == "" {
			base = defaultBaseURLFor(norm)
		}
		return key, base
	}

	// 4. Anthropic OAuth token
	if norm == "anthropic" || norm == "claudecode" {
		if token := readAnthropicOAuthToken(); token != "" {
			return token, ""
		}
	}

	// 4b. xAI / SuperGrok OAuth token (~/.swarm/config/oauth/xai.json)
	if norm == "xai" || norm == "grok" || norm == "x.ai" || norm == "supergrok" || norm == "xai-oauth" || norm == "grok-oauth" {
		if token, base := readXAIOAuthToken(); token != "" {
			if base == "" {
				base = "https://api.x.ai/v1"
			}
			return token, base
		}
	}

	// 5. Default base URL for OpenAI-compatible providers (no API key found)
	// Return the default base URL so the provider can fail gracefully with a clear error
	// about missing API key, rather than "unknown provider".
	if baseURL := defaultBaseURLFor(norm); baseURL != "" {
		return "", baseURL
	}

	return "", ""
}

// defaultBaseURLs maps provider names to their default base URLs for OpenAI-compatible providers.
var defaultBaseURLs = map[string]string{
	"groq":       "https://api.groq.com/openai/v1",
	"deepseek":   "https://api.deepseek.com/v1",
	"cerebras":   "https://api.cerebras.ai/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"together":   "https://api.together.xyz/v1",
	"moonshot":   "https://api.moonshot.cn/v1",
	"fireworks":  "https://api.fireworks.ai/inference/v1",
	"replicate":  "https://api.replicate.com/v1",
	"chutes":     "https://llm.chutes.ai/v1",
	"qwen":       "https://dashscope.aliyuncs.com/compatible-mode/v1",
	"xai":        "https://api.x.ai/v1",
	"grok":       "https://api.x.ai/v1",
	"x.ai":       "https://api.x.ai/v1",
	"plexus":     "http://localhost:4000/v1",
	"supergrok":  "https://api.x.ai/v1",
}

// defaultBaseURLFor returns the default base URL for an OpenAI-compatible provider.
func defaultBaseURLFor(providerName string) string {
	return defaultBaseURLs[providerName]
}

// readProvidersJSON reads ~/.swarm/config/providers.json (array format used by the TUI/settings UI)
// and returns the api_key and base_url for the named provider.
//
// The lookup tries the exact name first, then falls back to the SDK-normalized
// form (e.g., "wafer" -> "wafer.ai") and the display_name field. This handles
// the common case where a provider's canonical name in providers.json includes
// a domain suffix that callers may omit.
func readProvidersJSON(normalizedProvider string) (apiKey, baseURL string) {
	data, err := os.ReadFile(paths.ProvidersFile())
	if err != nil {
		return "", ""
	}
	var providers []struct {
		Name            string `json:"name"`
		DisplayName     string `json:"display_name"`
		APIKey          string `json:"api_key"`
		APIKeySecretRef string `json:"api_key_secret_ref"`
		BaseURL         string `json:"base_url"`
	}
	if err := json.Unmarshal(data, &providers); err != nil {
		return "", ""
	}
	// Compute the SDK-normalized form once (e.g., "wafer" -> "wafer.ai").
	normalized := normalizeProviderName(normalizedProvider)

	for _, p := range providers {
		pName := strings.ToLower(strings.TrimSpace(p.Name))
		pDisplay := strings.ToLower(strings.TrimSpace(p.DisplayName))
		matches := pName == normalizedProvider ||
			(normalized != normalizedProvider && pName == normalized) ||
			pDisplay == normalizedProvider ||
			(normalized != normalizedProvider && pDisplay == normalized)
		if !matches {
			continue
		}
		if p.APIKey != "" {
			return p.APIKey, p.BaseURL
		}
		if p.APIKeySecretRef != "" {
			vaultProvider := vault.GetDefaultVaultProvider()
			if vaultProvider != nil && vaultProvider.IsEnabled() && vaultProvider.GetVault() != nil {
				cred, err := vaultProvider.GetVault().ResolveCredential(context.Background(), p.APIKeySecretRef, vaultProvider.GetProjectID())
				if err == nil && cred != nil && !cred.IsExpired() {
					return cred.Secret, p.BaseURL
				}
			}
		}
		return "", p.BaseURL
	}
	return "", ""
}

func readCredentialsFile(normalizedProvider string) (apiKey, baseURL string) {
	data, err := os.ReadFile(paths.CredentialsFile())
	if err != nil {
		return "", ""
	}
	var creds credentialsFile
	if err := json.Unmarshal(data, &creds); err != nil {
		return "", ""
	}
	for name, cred := range creds.Providers {
		if strings.ToLower(name) == normalizedProvider {
			return cred.APIKey, cred.BaseURL
		}
	}
	// Fallback: try normalized form (e.g., "wafer" -> "wafer.ai").
	normalized := normalizeProviderName(normalizedProvider)
	if normalized != normalizedProvider {
		for name, cred := range creds.Providers {
			if strings.ToLower(name) == normalized {
				return cred.APIKey, cred.BaseURL
			}
		}
	}
	return "", ""
}

func readAnthropicOAuthToken() string {
	// Anthropic stores the OAuth token at ~/.swarm/config/oauth/anthropic.json
	data, err := os.ReadFile(paths.OAuthFile("anthropic"))
	if err != nil {
		return ""
	}
	var tok struct {
		Token *struct {
			AccessToken string `json:"access_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return ""
	}
	if tok.Token != nil {
		return tok.Token.AccessToken
	}
	return ""
}

// readXAIOAuthToken reads the xAI / SuperGrok OAuth access token from
// ~/.swarm/config/oauth/xai.json and returns the bearer token and base URL.
// The file stores a nested {"token": {"access_token": "...", ...}} structure
// identical to the Anthropic oauth.json format.
func readXAIOAuthToken() (token, baseURL string) {
	data, err := os.ReadFile(paths.OAuthFile("xai"))
	if err != nil {
		return "", ""
	}
	var tok struct {
		Token *struct {
			AccessToken string `json:"access_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(data, &tok); err != nil {
		return "", ""
	}
	if tok.Token != nil && tok.Token.AccessToken != "" {
		return tok.Token.AccessToken, "https://api.x.ai/v1"
	}
	return "", ""
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// isOAuthToken returns true if the string looks like a Claude Code OAuth access token.
func isOAuthToken(s string) bool {
	return strings.HasPrefix(s, "sk-ant-oat") || strings.HasPrefix(s, "eyJ")
}

// buildOllama constructs an Ollama provider directly via the openai-compatible provider.
func buildOllama(model, baseURL string, logger observability.Logger, tracer observability.Tracer) (provider.Provider, error) {
	provCfg := provider.Config{
		Name:    "openai",
		APIKey:  "ollama",
		BaseURL: baseURL,
		Model:   model,
		Logger:  logger,
		Tracer:  tracer,
	}
	if provCfg.BaseURL == "" {
		provCfg.BaseURL = "http://localhost:11434/v1"
	}
	reg := provider.NewSimpleRegistry(logger)
	if err := provopenai.Register(reg); err != nil {
		logger.Warn(context.Background(), "client.ollama_register_failed",
			observability.F("error", err.Error()))
	}
	return reg.Create(provCfg)
}

// normalizeProviderName maps TUI display names to SDK provider names.
func normalizeProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "claudecode", "claude-code", "claude_code":
		return "anthropic"
	case "google":
		return "gemini"
	case "codex":
		return "openai"
	case "wafer", "wafer.ai":
		return "wafer.ai"
	default:
		return strings.ToLower(strings.TrimSpace(name))
	}
}

// approvalCheckerFor returns the permission checker that should replace the
// registry's default for the given approval mode, or nil to leave the default
// in place. Exposed separately so unit tests can verify mode→checker mapping
// without building a full Client.
func approvalCheckerFor(mode string) tools.PermissionChecker {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "yolo":
		factory := tools.NewPermissionCheckerFactory(nil)
		return factory.CreateYOLOChecker(nil)
	case "readonly":
		return tools.NewSimplePermissionChecker(tools.DefaultPermissionPolicies())
	default:
		return nil
	}
}

func defaultModelFor(prov string) string {
	switch strings.ToLower(prov) {
	case "anthropic", "claudecode":
		return "claude-sonnet-4-5"
	case "openai":
		return "gpt-4o"
	case "gemini", "google":
		return "gemini-2.0-flash"
	case "ollama":
		return "llama3.2"
	case "cerebras":
		return "llama-3.3-70b"
	case "groq":
		return "llama-3.3-70b-versatile"
	default:
		return ""
	}
}

// ─── end of file ─────────────────────────────────────────────────────────────

// ─── clientHooksManager ──────────────────────────────────────────────────────

// clientHooksManager implements agent.HooksManager for standalone client usage.
// It wraps SDK hooks (task enforcement, etc.) into the interface the agent
// execution pipeline expects.
type clientHooksManager struct {
	logger             observability.Logger
	taskEnforcement    *dreambuiltin.TaskEnforcementHook
	sleepBlocker       *dreambuiltin.SleepBlockerHook
	stdinConflict      *dreambuiltin.StdinConflictHook
	autoMode           *dreambuiltin.AutoModeHook
	recap              *dreambuiltin.RecapHook
	gitUserEnforcement *dreambuiltin.GitUserEnforcementHook
	protectedBranch    *dreambuiltin.ProtectedBranchHook
	// extra are generic hooks registered via WithHook/WithAutogenSkills/Dream/etc.
	// They were previously never emitted in standalone-client mode (only the named
	// hooks above ran), so the autogenskills lifecycle + budget hooks never fired.
	extra []hooks.Hook
}

func enableAnnoyanceNudgeForRegistry(manager *clientHooksManager, registry tools.Registry) bool {
	if manager == nil || registry == nil || !registry.IsRegistered("annoyed") {
		return false
	}
	manager.extra = append(manager.extra, dreambuiltin.NewAnnoyanceNudgeHook())
	return true
}

// AutoModeHook returns the auto-mode hook if enabled, or nil.
func (m *clientHooksManager) AutoModeHook() *dreambuiltin.AutoModeHook { return m.autoMode }

// RecapHook returns the recap hook if enabled, or nil. Callers (TUI, sac)
// can use this to trigger a manual recap on session resume via
// RecapHook.GenerateRecap / OnEvent.
func (m *clientHooksManager) RecapHook() *dreambuiltin.RecapHook { return m.recap }

func (m *clientHooksManager) EmitToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) ([]agent.HookResult, error) {
	var results []agent.HookResult

	event := hooks.Event{
		Type: hooks.EventToolBeforeExecute,
		Data: map[string]any{
			"tool_name":  toolName,
			"params":     params,
			"tool_input": params,
		},
	}

	if m.taskEnforcement != nil && m.taskEnforcement.Filter(event) {
		hookRes, err := m.taskEnforcement.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		r := agent.HookResult{
			HookName: "task-enforcement",
			Output:   hookRes.Message,
		}
		if hookRes.Action == hooks.ActionBlock {
			r.Blocked = true
			results = append(results, r)
			return results, fmt.Errorf("blocked by task-enforcement: %s", hookRes.Message)
		}
		if hookRes.Message != "" {
			results = append(results, r)
		}
	}

	if m.sleepBlocker != nil && m.sleepBlocker.Filter(event) {
		hookRes, err := m.sleepBlocker.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		r := agent.HookResult{
			HookName: "sleep-blocker",
			Output:   hookRes.Message,
		}
		if hookRes.Action == hooks.ActionBlock {
			r.Blocked = true
			results = append(results, r)
			return results, fmt.Errorf("blocked by sleep-blocker: %s", hookRes.Message)
		}
		if hookRes.Message != "" {
			results = append(results, r)
		}
	}

	if m.stdinConflict != nil && m.stdinConflict.Filter(event) {
		hookRes, err := m.stdinConflict.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		// Advisory by contract: this hook never blocks, so only its message is
		// surfaced. Do not add an ActionBlock branch here.
		if hookRes.Message != "" {
			results = append(results, agent.HookResult{
				HookName: "stdin-conflict",
				Output:   hookRes.Message,
			})
		}
	}

	if m.gitUserEnforcement != nil && m.gitUserEnforcement.Filter(event) {
		hookRes, err := m.gitUserEnforcement.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		r := agent.HookResult{
			HookName: "git-user-enforcement",
			Output:   hookRes.Message,
		}
		if hookRes.Action == hooks.ActionBlock {
			r.Blocked = true
			results = append(results, r)
			return results, fmt.Errorf("blocked by git-user-enforcement: %s", hookRes.Message)
		}
		if hookRes.Message != "" {
			results = append(results, r)
		}
	}
	if m.protectedBranch != nil && m.protectedBranch.Filter(event) {
		hookRes, err := m.protectedBranch.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		r := agent.HookResult{
			HookName: "protected-branch",
			Output:   hookRes.Message,
		}
		if hookRes.Action == hooks.ActionBlock {
			r.Blocked = true
			results = append(results, r)
			return results, fmt.Errorf("blocked by protected-branch: %s", hookRes.Message)
		}
		if hookRes.Message != "" {
			results = append(results, r)
		}
	}
	if m.autoMode != nil && m.autoMode.Filter(event) {
		hookRes, err := m.autoMode.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		r := agent.HookResult{
			HookName: "auto-mode",
			Output:   hookRes.Message,
		}
		if hookRes.Action == hooks.ActionBlock {
			r.Blocked = true
			results = append(results, r)
			return results, fmt.Errorf("blocked by auto-mode: %s", hookRes.Message)
		}
		// ActionModify annotates the event with classification metadata; surface
		// it on the HookResult so UI consumers can show the auto-mode verdict.
		if hookRes.Action == hooks.ActionModify && hookRes.ModifiedEvent.Metadata != nil {
			r.Metadata = hookRes.ModifiedEvent.Metadata
		}
		if hookRes.Message != "" || r.Metadata != nil {
			results = append(results, r)
		}
	}

	// Generic extra hooks (autogenskills lifecycle + budget, dream, disk-loaded).
	// These were previously never emitted in standalone-client mode. A blocking
	// result aborts the tool (like the named hooks); a non-blocking message
	// (e.g. the budget soft-nudge) is surfaced.
	for _, h := range m.extra {
		if h == nil || !h.Filter(event) {
			continue
		}
		hookRes, err := h.OnEvent(ctx, event)
		if err != nil {
			return results, err
		}
		r := agent.HookResult{HookName: h.Name(), Output: hookRes.Message}
		if hookRes.Action == hooks.ActionBlock {
			r.Blocked = true
			results = append(results, r)
			return results, fmt.Errorf("blocked by %s: %s", h.Name(), hookRes.Message)
		}
		if hookRes.Message != "" {
			results = append(results, r)
		}
	}

	return results, nil
}

func (m *clientHooksManager) EmitToolAfterExecute(ctx context.Context, toolName string, params map[string]any, result any, execErr error) []agent.HookResult {
	if len(m.extra) == 0 {
		return nil
	}
	// After-execute lets stateful hooks observe outcomes — e.g. the budget hook
	// upgrades/refills the budget when a skill tool succeeds.
	data := map[string]any{
		"tool_name":   toolName,
		"params":      params,
		"tool_input":  params,
		"tool_output": clientToolOutput(result, execErr),
		"result":      result,
	}
	if execErr != nil {
		data["error"] = execErr
	}
	event := hooks.Event{
		ID:             tools.ToolCallID(ctx),
		Type:           hooks.EventToolAfterExecute,
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data:           data,
		ToolOutcome:    tools.NamedOutcomeOf(toolName, result),
	}
	var results []agent.HookResult
	for _, h := range m.extra {
		if h == nil || !h.Filter(event) {
			continue
		}
		if hookRes, err := h.OnEvent(ctx, event); err == nil && hookRes.Message != "" {
			results = append(results, agent.HookResult{HookName: h.Name(), Output: hookRes.Message})
		}
	}
	return results
}

func clientToolOutput(result any, execErr error) map[string]any {
	switch value := result.(type) {
	case map[string]any:
		output := make(map[string]any, len(value)+1)
		for key, item := range value {
			output[key] = item
		}
		if _, ok := output["success"]; !ok {
			output["success"] = execErr == nil
		}
		if execErr != nil {
			output["error"] = execErr.Error()
		}
		return output
	case string:
		output := map[string]any{"stdout": value, "success": execErr == nil}
		if execErr != nil {
			output["error"] = execErr.Error()
		}
		return output
	case *tools.ToolResult:
		output := map[string]any{"success": execErr == nil}
		if value != nil {
			output["stdout"] = value.Output
			output["success"] = value.Error == nil && !value.IsError && execErr == nil
			output["duration_ms"] = value.DurationMS
			switch {
			case value.Error != nil:
				output["error"] = value.Error.Error()
			case value.IsError:
				output["error"] = value.Output
			}
		}
		if execErr != nil {
			output["error"] = execErr.Error()
		}
		return output
	default:
		output := map[string]any{"result": result, "success": execErr == nil}
		if execErr != nil {
			output["error"] = execErr.Error()
		}
		return output
	}
}

func (m *clientHooksManager) EmitProviderResponse(_ context.Context, _, _ string, _, _ int, _ int64) {
	// no-op for standalone client usage — provider events are captured by the TUI hooks pipeline.
}
