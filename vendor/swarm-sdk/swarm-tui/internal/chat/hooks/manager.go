// Package hooks provides hook management for the TUI
package hooks

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/findings"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/gold"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/builtin"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/silver"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/skills/autogenskills"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	tuianalytics "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/analytics"
)

// logDebugToFile routes to the package-level debug logger instead of
// writing to a hard-coded file path. Kept as a thin wrapper so call
// sites compile without changes.
func logDebugToFile(format string, args ...any) {
	logDebug(format, args...)
}

// HooksManager manages SDK hooks for the TUI
type HooksManager struct {
	manager       *hooks.Manager
	logger        observability.Logger
	tracer        observability.Tracer
	mu            sync.RWMutex
	workspaceRoot string

	// toolCallCount tracks total tool executions for diagnostics; taskNudgeBudget
	// separately tracks per-conversation activity and user-turn cadence.
	toolCallCount   atomic.Int64
	taskNudgeBudget *builtin.TaskNudgeBudget

	// Built-in hooks
	loggingHook          *builtin.LoggingHook
	metricsHook          *builtin.MetricsHook
	auditHook            *builtin.AuditHook
	tracingHook          *builtin.TracingHook
	taskEnforce          *builtin.TaskEnforcementHook
	taskMaintenance      *builtin.TaskMaintenanceReminderHook
	taskAudit            *builtin.TaskAuditHook
	annoyanceNudge       *builtin.AnnoyanceNudgeHook
	sessionStart         *builtin.SessionStartHook
	postActing           *builtin.PostActingHook
	verification         *builtin.VerificationProtocolHook
	planModeFirst        *builtin.PlanModeFirstToolHook
	autoMode             *builtin.AutoModeHook
	sleepBlocker         *builtin.SleepBlockerHook
	stdinConflict        *builtin.StdinConflictHook
	protectedBranch      *builtin.ProtectedBranchHook
	complexityDetection  *builtin.ComplexityDetectionHook
	midSessionRecovery   *builtin.MidSessionRecoveryHook
	nestedIndexDiscovery *builtin.NestedIndexDiscoveryHook
	goalHook             *builtin.GoalHook

	// Findings hooks (optional, registered based on settings)
	localFindings        *builtin.LocalFindingsHook
	bronzeHook           *builtin.BronzeEventHook
	findingsAnalysis     *builtin.FindingsAnalysisHook
	findingsCacheDir     string // Stored when EnableFindings is called; used for debug logs
	steeringPreTool      *builtin.SteeringPreToolHook
	steeringStream       *builtin.SteeringStreamHook
	steeringStreamDriver *agent.StreamingSteeringDriver
	remoteAnalytics      *tuianalytics.Hook

	// Bronze-Silver-Gold pipeline components
	goldAnalyzer      *builtin.GoldAnalyzer      // Gold tier analysis
	silverTreeHook    *builtin.SilverTreeHook    // Silver tier: PageIndex-style tree index
	goldHook          *builtin.GoldHook          // Gold tier: statistical insight engine
	systemMetricsHook *builtin.SystemMetricsHook // System telemetry: Go runtime + OS resource snapshots

	// Autogenskills lifecycle service (skill creation + budget enforcement)
	autogenSkills   *autogenskills.Service
	autogenSkillReg *skills.Registry
	curatorAgent    *autogenskills.CuratorAgent
	// curatorAgentRun is a test seam for the optional consolidation pass.
	// Production falls back to curatorAgent.RunWithReview.
	curatorAgentRun func(context.Context, []autogenskills.ReviewResult, bool) (*autogenskills.CuratorAgentResult, error)

	// Curator inactivity timer
	curatorIdleTimer *time.Timer
	curatorRunning   bool
	// curatorAgentBusy mirrors the main agent's execution state
	// (NotifyAgentBusy/NotifyAgentIdle). The idle timer's callback re-checks
	// it at fire time: Stop() can lose the race with an already-fired timer,
	// and the curator must never run an LLM maintenance pass while the main
	// agent is mid-turn.
	curatorAgentBusy bool
	curatorMu        sync.Mutex

	// Hook states (for UI toggles)
	hookStates map[string]bool

	// pendingToolMessages collects AdditionalContext from tool-time hooks
	// (SteeringPreTool, PostActing, PlanModeFirst, AutoMode) so they can be
	// injected into the agent on the next user turn via EmitUserPromptSubmit.
	// Tool-time hook messages are shown in the TUI sidebar but never reach the
	// agent's conversation — this buffer bridges that gap.
	pendingToolMessages []string
	pendingToolMu       sync.Mutex
}

// NewHooksManager creates a new hooks manager for the TUI
func NewHooksManager(logger observability.Logger, tracer observability.Tracer, workspaceRoot string) *HooksManager {
	manager := hooks.NewManager(hooks.ManagerConfig{
		MaxHooksPerScope: 100,
		EnableMetrics:    true,
		EnableTracing:    true,
		ErrorHandler: func(hookName string, err error) {
			if logger != nil {
				logger.Error(context.Background(), "hook.execution_error",
					observability.F("hook", hookName),
					observability.F("error", err.Error()))
			}
		},
	})

	hm := &HooksManager{
		manager:         manager,
		logger:          logger,
		tracer:          tracer,
		hookStates:      make(map[string]bool),
		workspaceRoot:   workspaceRoot,
		taskNudgeBudget: builtin.NewTaskNudgeBudget(builtin.TaskNudgeConfig{}),
	}

	// Register built-in hooks (disabled by default, user can enable)
	hm.registerBuiltinHooks()

	return hm

}

// SetTaskNudgeConfig applies the task-nudge turn budget. As with the skill
// budget config, zero-valued thresholds use product defaults.
func (hm *HooksManager) SetTaskNudgeConfig(cfg builtin.TaskNudgeConfig) {
	hm.taskNudgeBudget = builtin.NewTaskNudgeBudget(cfg)
	hooks.ConfigureDefaultMetaNudgeBudget(cfg.WithDefaults().NudgeInterval)
}

// SetFileReadRegistrar wires the nested INDEX.md discovery hook.
// When the agent reads a file in a new subdirectory, the registrar
// records that directory so its INDEX.md can be discovered on the
// next context refresh. This mirrors Claude Code's nestedMemoryAttachmentTriggers.
func (hm *HooksManager) SetFileReadRegistrar(registrar builtin.FileReadRegistrar) {
	if err := hm.EnableNestedIndexDiscovery(registrar); err != nil && hm.logger != nil {
		hm.logger.Error(context.Background(), "hooks.set_file_read_registrar_failed",
			observability.F("error", err.Error()))
	}
}

// EnableNestedIndexDiscovery creates and registers the nested INDEX.md discovery hook.
// This hook listens for file-reading tool completions and registers the parent
// directories with the given FileReadRegistrar (typically the ContextOrchestrator).
// On the next context refresh, the registrar walks those directories to find
// INDEX.md files, mirroring Claude Code's nestedMemoryAttachmentTriggers pattern.
func (hm *HooksManager) EnableNestedIndexDiscovery(registrar builtin.FileReadRegistrar) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.nestedIndexDiscovery != nil {
		return nil // already enabled
	}

	hook := builtin.NewNestedIndexDiscoveryHook(registrar, hm.workspaceRoot)
	hook.SetLogger(hm.logger)
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("failed to register nested index discovery hook: %w", err)
	}
	hm.nestedIndexDiscovery = hook
	hm.hookStates["nested-index-discovery"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.nested_index_discovery_enabled")
	}
	return nil
}

// registerBuiltinHooks registers all built-in hooks
func (hm *HooksManager) registerBuiltinHooks() {
	// Logging hook (info level)
	hm.loggingHook = builtin.NewLoggingHook(hm.logger, observability.LevelInfo)
	hm.hookStates["logging"] = false // Disabled by default

	// Metrics hook
	hm.metricsHook = builtin.NewMetricsHook(nil) // Will need metrics interface
	hm.hookStates["metrics"] = false

	// Audit hook
	hm.auditHook = builtin.NewAuditHook(nil) // Will need auditor interface
	hm.hookStates["audit"] = false

	// Tracing hook
	hm.tracingHook = builtin.NewTracingHook(hm.tracer)
	hm.hookStates["tracing"] = false

	// ── Task workflow hooks (ENABLED BY DEFAULT) ─────────────────────────────────
	// These are critical for proper agent behavior and should always be on.

	// Task enforcement hook - blocks all tools until task exists
	hm.taskEnforce = builtin.NewTaskEnforcementHook()
	if err := hm.manager.Register(hm.taskEnforce, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.task_enforcement_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["task-enforcement"] = true // Always enabled

	// Task maintenance reminder hook - reminds about task updates after user messages and N tools
	hm.taskMaintenance = builtin.NewTaskMaintenanceReminderHook()
	if err := hm.manager.Register(hm.taskMaintenance, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.task_maintenance_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["task-maintenance"] = true // Always enabled

	// Task audit hook - attaches compact sanitized tool events to the active task
	hm.taskAudit = builtin.NewTaskAuditHook()
	if err := hm.manager.Register(hm.taskAudit, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.task_audit_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["task-audit"] = true // Always enabled

	// Enabled after SDK construction only when the annoyed tool is present.
	hm.hookStates["annoyance-nudge"] = false

	// Session start hook - injects plan mode guidance at session start
	hm.sessionStart = builtin.NewSessionStartHook()
	if err := hm.manager.Register(hm.sessionStart, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.session_start_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["session-start"] = true // Always enabled

	// Post-acting hook - prompts for verifying/documenting after acting
	hm.postActing = builtin.NewPostActingHook()
	if err := hm.manager.Register(hm.postActing, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.post_acting_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["post-acting"] = true // Always enabled

	// Verification protocol hook - 5-layer verification before declaring done
	hm.verification = builtin.NewVerificationProtocolHook()
	if err := hm.manager.Register(hm.verification, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.verification_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["verification-protocol"] = true // Always enabled

	// Plan mode first tool hook - injects problem breakdown prompt on first tool in plan mode
	hm.planModeFirst = builtin.NewPlanModeFirstToolHook()
	if err := hm.manager.Register(hm.planModeFirst, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.plan_mode_first_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["plan-mode-first-tool"] = true // Always enabled

	// Remote analytics hook - OPT-IN, OFF BY DEFAULT. NewHook returns nil
	// unless the user explicitly enabled telemetry (SWARM_ANALYTICS_ENABLED=1)
	// and configured their own collector URL + token, so this block is inert
	// in the open-source build.
	if remoteHook := tuianalytics.NewHook(hm.workspaceRoot); remoteHook != nil {
		hm.remoteAnalytics = remoteHook
		if err := hm.manager.Register(remoteHook, hooks.ScopeGlobal, ""); err != nil {
			if hm.logger != nil {
				hm.logger.Error(context.Background(), "hook.remote_analytics_register_failed",
					observability.F("error", err.Error()))
			}
		} else {
			hm.hookStates["remote-analytics"] = true
		}
	}

	// Auto-mode hook - classifies tool calls against allow / soft-deny lists so
	// low-risk tools can be auto-approved and high-risk ones auto-denied. The
	// hook is opt-in gated (SWARM_AUTO_MODE_OPT_IN=1 env var or
	// SkipAutoPermissionPrompt=true in config); without opt-in its Filter()
	// returns false and it is inert.
	hm.autoMode = builtin.NewAutoModeHook(hm.logger)
	if err := hm.manager.Register(hm.autoMode, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.auto_mode_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["auto-mode"] = true // Registered always; opt-in gates activity

	// Sleep blocker hook - blocks sleep commands and suggests cron_scheduler
	hm.sleepBlocker = builtin.NewSleepBlockerHook()
	if err := hm.manager.Register(hm.sleepBlocker, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.sleep_blocker_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["sleep-blocker"] = true // Always enabled
	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.sleep_blocker_registered")
	}

	// Stdin conflict hook - advisory only. Warns when a bash command feeds one
	// simple command from both a pipe and a heredoc (issue #116), where bash
	// silently discards one of the two inputs. It never blocks: the Bash tool
	// already pins child stdin to /dev/null, so there is nothing to repair,
	// and a false positive must only cost a sentence of text.
	hm.stdinConflict = builtin.NewStdinConflictHook()
	if err := hm.manager.Register(hm.stdinConflict, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.stdin_conflict_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["stdin-conflict-advisory"] = true // Always enabled; advisory only

	// Protected-branch hook - blocks mutating tools/commands while on a
	// protected git branch (opt-in per-repo via .swarm/worktree.json). It is
	// always registered but inert until the user enables protection, so it is
	// safe to install unconditionally.
	hm.protectedBranch = builtin.NewProtectedBranchHook()
	if err := hm.manager.Register(hm.protectedBranch, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.protected_branch_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["protected-branch"] = true // Registered always; opt-in gates activity

	// Complexity detection hook - detects if first message indicates complex work
	hm.complexityDetection = builtin.NewComplexityDetectionHook()
	if err := hm.manager.Register(hm.complexityDetection, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.complexity_detection_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["complexity-detection"] = true // Always enabled
	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.complexity_detection_registered")
	}

	// Mid-session recovery hook - one-time nudge after 5+ tasks with no plan
	hm.midSessionRecovery = builtin.NewMidSessionRecoveryHook()
	if err := hm.manager.Register(hm.midSessionRecovery, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.mid_session_recovery_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["mid-session-recovery"] = true // Always enabled
	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.mid_session_recovery_registered")
	}

	// System metrics hook — captures Go runtime + OS resource snapshots into bronze
	hm.systemMetricsHook = builtin.NewSystemMetricsHook(60*time.Second, hm.logger)
	hm.systemMetricsHook.SetEventEmitter(hm.manager)
	if err := hm.manager.Register(hm.systemMetricsHook, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.system_metrics_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["system-metrics"] = true // Always enabled

	// Goal hook - /goal conditional stop-hook. Evaluates the user's goal
	// condition after each agent turn (AfterAgent) and halts when met.
	hm.goalHook = builtin.NewGoalHook()
	hm.goalHook.SetLogger(hm.logger)
	if err := hm.manager.Register(hm.goalHook, hooks.ScopeGlobal, ""); err != nil {
		if hm.logger != nil {
			hm.logger.Error(context.Background(), "hook.goal_register_failed",
				observability.F("error", err.Error()))
		}
	}
	hm.hookStates["goal"] = true // Always registered; inert until a goal is set

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.workflow_hooks_registered",
			observability.F("task_enforcement", hm.hookStates["task-enforcement"]),
			observability.F("task_maintenance", hm.hookStates["task-maintenance"]),
			observability.F("session_start", hm.hookStates["session-start"]),
			observability.F("post_acting", hm.hookStates["post-acting"]),
			observability.F("verification", hm.hookStates["verification-protocol"]),
			observability.F("plan_mode_first", hm.hookStates["plan-mode-first-tool"]),
			observability.F("auto_mode", hm.hookStates["auto-mode"]),
			observability.F("sleep_blocker", hm.hookStates["sleep-blocker"]))
	}
}

// EnableHook enables a built-in hook by name
// EnableAnnoyanceNudge enables proactive complaint guidance only after the
// caller has verified that the model-facing annoyed tool is registered.
func (hm *HooksManager) EnableAnnoyanceNudge() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	if hm.hookStates["annoyance-nudge"] {
		return nil
	}
	hook := builtin.NewAnnoyanceNudgeHook()
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("failed to register annoyance nudge hook: %w", err)
	}
	hm.annoyanceNudge = hook
	hm.hookStates["annoyance-nudge"] = true
	return nil
}

func (hm *HooksManager) EnableHook(name string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	switch name {
	case "logging":
		if !hm.hookStates["logging"] {
			if err := hm.manager.Register(hm.loggingHook, hooks.ScopeGlobal, ""); err != nil {
				return fmt.Errorf("failed to enable logging hook: %w", err)
			}
			hm.hookStates["logging"] = true
		}
	case "metrics":
		if !hm.hookStates["metrics"] {
			if err := hm.manager.Register(hm.metricsHook, hooks.ScopeGlobal, ""); err != nil {
				return fmt.Errorf("failed to enable metrics hook: %w", err)
			}
			hm.hookStates["metrics"] = true
		}
	case "audit":
		if !hm.hookStates["audit"] {
			if err := hm.manager.Register(hm.auditHook, hooks.ScopeGlobal, ""); err != nil {
				return fmt.Errorf("failed to enable audit hook: %w", err)
			}
			hm.hookStates["audit"] = true
		}
	case "tracing":
		if !hm.hookStates["tracing"] {
			if err := hm.manager.Register(hm.tracingHook, hooks.ScopeGlobal, ""); err != nil {
				return fmt.Errorf("failed to enable tracing hook: %w", err)
			}
			hm.hookStates["tracing"] = true
		}
	default:
		return fmt.Errorf("unknown hook: %s", name)
	}

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hook.enabled",
			observability.F("hook", name))
	}

	return nil
}

// DisableHook disables a built-in hook by name
func (hm *HooksManager) DisableHook(name string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	var hookName string
	switch name {
	case "logging":
		hookName = hm.loggingHook.Name()
	case "metrics":
		hookName = hm.metricsHook.Name()
	case "audit":
		hookName = hm.auditHook.Name()
	case "tracing":
		hookName = hm.tracingHook.Name()
	default:
		return fmt.Errorf("unknown hook: %s", name)
	}

	if hm.hookStates[name] {
		if err := hm.manager.Unregister(hookName, hooks.ScopeGlobal, ""); err != nil {
			return fmt.Errorf("failed to disable %s hook: %w", name, err)
		}
		hm.hookStates[name] = false
	}

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hook.disabled",
			observability.F("hook", name))
	}

	return nil
}

// IsEnabled checks if a hook is enabled
func (hm *HooksManager) IsEnabled(name string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.hookStates[name]
}

// EmitToolBeforeExecute emits a tool.before_execute event
// Returns hook execution results for UI display and an error if a hook blocks execution.
func (hm *HooksManager) EmitToolBeforeExecute(ctx context.Context, toolName string, params map[string]any) ([]agent.HookResult, error) {
	registeredHooks := hm.manager.List()
	var results []agent.HookResult
	toolCallID := tools.ToolCallID(ctx)

	// Always log to file-based debug log as fallback
	logDebug("[HooksManager] EmitToolBeforeExecute called: tool=%s, hooks=%d, logger_nil=%v", toolName, len(registeredHooks), hm.logger == nil)

	if hm.logger != nil {
		hm.logger.Info(ctx, "hooks.emit_before_execute",
			observability.F("tool", toolName),
			observability.F("registered_hooks", len(registeredHooks)))
	}

	event := hooks.Event{
		ID:        toolCallID,
		Type:      hooks.EventToolBeforeExecute,
		Timestamp: time.Now(),
		// Identity is placed on ctx by the agent via tools.WithOwnerInfo
		// (internal/agent/agent_tools.go) and was previously dropped, leaving tool
		// events un-attributable. Population only fills the fields; it does not
		// alter dispatch (no hook registers with ScopeConversation).
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"tool_call_id": toolCallID,
			"tool_name":    toolName,
			"tool_input":   params,
			"params":       params,
		},
	}

	// Execute hooks and get full result with outputs
	execResult, err := hm.manager.EmitWithResult(ctx, event)

	// Convert hook outputs to agent.HookResult for UI display
	// IMPORTANT: Also set AdditionalContext so messages are injected into agent context
	if execResult != nil && len(execResult.HookOutputs) > 0 {
		for _, ho := range execResult.HookOutputs {
			logDebug("[HooksManager] HookOutput: name=%s, output_len=%d, success=%v, blocked=%v",
				ho.HookName, len(ho.Output), ho.Success, execResult.Blocked)
			isBlocked := execResult.Blocked && execResult.BlockedBy == ho.HookName
			results = append(results, agent.HookResult{
				HookName:          ho.HookName,
				Success:           ho.Success,
				Output:            ho.Output,
				AdditionalContext: ho.Output, // Inject into agent context
				Blocked:           isBlocked,
				Error:             ho.Error,
			})
			// Queue non-empty, non-block tool hook messages for the next user turn.
			// Tool-time hooks (SteeringPreTool, PostActing, etc.) fire outside the
			// user-prompt path so their messages never reach the agent directly.
			// We queue them here and flush in EmitUserPromptSubmit.
			if ho.Output != "" && !isBlocked {
				hm.AddPendingToolMessage(ho.Output)
			}
			// DEBUG: Log the conversion
			logDebugToFile("manager: hook=%s output_len=%d additional_context_len=%d output=%q",
				ho.HookName, len(ho.Output), len(ho.Output), ho.Output)
		}
	}

	// ── Built-in plan-mode first-tool hook ───────────────────────────────────────
	// Fires on the first tool after entering plan mode. Injects a problem
	// breakdown prompt that teaches agents to decompose problems at the lowest
	// logical level (what you need, what you expect, how logic works).
	if msg, shouldInject := builtin.CheckPlanModeFirstTool(toolName); shouldInject {
		results = append(results, agent.HookResult{
			HookName:          "plan-mode-first-tool",
			Success:           true,
			Output:            msg,
			AdditionalContext: msg, // Inject into agent context
		})
		hm.AddPendingToolMessage(msg)
	}

	// Emit mode.entered / mode.exited for plan mode transitions so bronze captures them.
	lowerTool := strings.ToLower(strings.ReplaceAll(toolName, "_", ""))
	if lowerTool == "enterplanmode" {
		_, _ = hm.manager.Emit(ctx, hooks.Event{
			Type:      hooks.EventModeEntered,
			Timestamp: time.Now(),
			Data:      map[string]any{"mode_id": "plan"},
		})
	} else if lowerTool == "exitplanmode" {
		_, _ = hm.manager.Emit(ctx, hooks.Event{
			Type:      hooks.EventModeExited,
			Timestamp: time.Now(),
			Data:      map[string]any{"mode_id": "plan"},
		})
	}

	if hm.logger != nil {
		if err != nil {
			hm.logger.Error(ctx, "hooks.emit_before_error",
				observability.F("tool", toolName),
				observability.F("error", err.Error()))
		} else {
			hm.logger.Info(ctx, "hooks.emit_before_success",
				observability.F("tool", toolName),
				observability.F("hooks_executed", len(results)))
		}
	}

	logDebug("[HooksManager] EmitToolBeforeExecute complete: tool=%s, results=%d, blocked=%v", toolName, len(results), err != nil)
	return results, err
}

// EmitToolAfterExecute emits a tool.after_execute event
// Returns hook execution results for UI display.
func (hm *HooksManager) EmitToolAfterExecute(ctx context.Context, toolName string, params map[string]any, result any, execErr error) []agent.HookResult {
	var results []agent.HookResult
	toolCallID := tools.ToolCallID(ctx)

	if hm.logger != nil {
		hm.logger.Info(ctx, "hooks.emit_after_execute",
			observability.F("tool", toolName))
	}

	// Build tool_output as a map for hooks that expect map[string]any
	var toolOutput map[string]any
	switch v := result.(type) {
	case map[string]any:
		toolOutput = v
	case string:
		toolOutput = map[string]any{
			"stdout":  v,
			"success": execErr == nil,
		}
		if execErr != nil {
			toolOutput["error"] = execErr.Error()
		}
	case *tools.ToolResult:
		if v == nil {
			toolOutput = map[string]any{
				"result":  nil,
				"success": execErr == nil,
			}
			if execErr != nil {
				toolOutput["error"] = execErr.Error()
			}
		} else {
			toolOutput = map[string]any{
				"stdout":      v.Output,
				"success":     v.Error == nil && !v.IsError,
				"duration_ms": v.DurationMS,
			}
			if v.Error != nil {
				toolOutput["error"] = v.Error.Error()
			} else if v.IsError {
				toolOutput["error"] = v.Output
			}
		}
	default:
		toolOutput = map[string]any{
			"result":  result,
			"success": execErr == nil,
		}
	}

	event := hooks.Event{
		ID:             toolCallID,
		Type:           hooks.EventToolAfterExecute,
		Timestamp:      time.Now(),
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"tool_call_id": toolCallID,
			"tool_name":    toolName,
			"tool_input":   params,
			"tool_output":  toolOutput,
			"params":       params,
			"result":       result,
			"error":        execErr,
		},
		// Typed structured outcome (G4) — see internal/toolout. Nil when the
		// tool does not report one.
		ToolOutcome: tools.NamedOutcomeOf(toolName, result),
	}

	// Execute user-configured hooks and get full result with outputs
	execResult, err := hm.manager.EmitWithResult(ctx, event)

	// Convert hook outputs to agent.HookResult for UI display
	// IMPORTANT: Also set AdditionalContext so messages are injected into agent context
	if execResult != nil && len(execResult.HookOutputs) > 0 {
		for _, ho := range execResult.HookOutputs {
			results = append(results, agent.HookResult{
				HookName:          ho.HookName,
				Success:           ho.Success,
				Output:            ho.Output,
				AdditionalContext: ho.Output, // Inject into agent context
				Blocked:           execResult.Blocked && execResult.BlockedBy == ho.HookName,
				Error:             ho.Error,
			})
		}
	}

	// Record tool activity; the turn-budgeted UserPromptSubmit hook decides
	// whether the conversation has become multi-step and is due for a nudge.
	hm.toolCallCount.Add(1)
	hm.taskNudgeBudget.RecordToolCall(tools.OwnerConversationID(ctx))

	// ── Built-in simulation PostToolUse hook ──────────────────────────────────
	// Fires after plan-related tools complete. Instructs agent to simulate
	// execution mentally before acting - based on "From Control to Foresight"
	// (arXiv:2603.11677) - simulation-in-the-loop collaboration paradigm.
	if builtin.PlanExitDetected(toolName) {
		if msg, shouldSimulate := builtin.CheckShouldSimulate(ctx); shouldSimulate {
			results = append(results, agent.HookResult{
				HookName:          "simulation",
				Success:           true,
				Output:            msg,
				AdditionalContext: msg,
			})
		}
	}

	if hm.logger != nil && err != nil {
		hm.logger.Error(ctx, "hooks.emit_after_error",
			observability.F("tool", toolName),
			observability.F("error", err.Error()))
	}

	logDebug("[HooksManager] EmitToolAfterExecute complete: tool=%s, results=%d", toolName, len(results))
	return results
}

// EmitUserPromptSubmit emits a user.prompt_submit event before the prompt is sent to the LLM.
// Returns:
// - injectedContext: stdout from hooks that should be prepended to the user's message
// - hookResults: results for UI display
// - error: if a hook blocks the prompt (exit code 2)
//
// The third parameter is a CONVERSATION id, not a session id — despite the
// wire-level Data["session_id"]/external hook JSON key it ends up under
// (kept as "session_id" deliberately, for Claude Code hook-script
// compatibility; see shell_hook.go:buildClaudeCodeInputImpl). Do not pass
// conversation.ProcessSessionID() here.
func (hm *HooksManager) EmitUserPromptSubmit(ctx context.Context, prompt string, conversationID string) (string, []agent.HookResult, error) {
	var results []agent.HookResult
	var injectedContext []string

	logDebug("[HooksManager] EmitUserPromptSubmit called: prompt_len=%d, conversation=%s", len(prompt), conversationID)

	if hm.logger != nil {
		hm.logger.Info(ctx, "hooks.emit_user_prompt_submit",
			observability.F("prompt_length", len(prompt)),
			observability.F("conversation_id", conversationID))
	}

	event := hooks.Event{
		Type:           "user.prompt_submit",
		Timestamp:      time.Now(),
		ConversationID: conversationID,
		Data: map[string]any{
			"prompt": prompt,
			// Key stays "session_id" on purpose (Claude Code hook-script
			// compatibility, see buildClaudeCodeInputImpl); the VALUE is a
			// conversation id, not conversation.ProcessSessionID().
			"session_id": conversationID,
		},
	}

	// Reset per-turn tool call counter so Gate B (periodic nudge) counts from
	// zero on each new user request, not across the entire session lifetime.
	hm.toolCallCount.Store(0)

	// Execute hooks and get full result with outputs
	execResult, err := hm.manager.EmitWithResult(ctx, event)

	// Convert hook outputs to agent.HookResult for UI display
	// AND collect stdout for context injection (Claude Code behavior)
	if execResult != nil && len(execResult.HookOutputs) > 0 {
		for _, ho := range execResult.HookOutputs {
			results = append(results, agent.HookResult{
				HookName: ho.HookName,
				Success:  ho.Success,
				Output:   ho.Output,
				Blocked:  execResult.Blocked && execResult.BlockedBy == ho.HookName,
				Error:    ho.Error,
			})
			// Collect non-empty stdout for context injection
			if ho.Success && ho.Output != "" {
				injectedContext = append(injectedContext, ho.Output)
			}
		}
	}

	if hm.logger != nil {
		if err != nil {
			hm.logger.Error(ctx, "hooks.user_prompt_blocked",
				observability.F("error", err.Error()))
		} else {
			hm.logger.Info(ctx, "hooks.user_prompt_success",
				observability.F("hooks_executed", len(results)),
				observability.F("context_injected", len(injectedContext) > 0))
		}
	}

	// ── Built-in task-nudge UserPromptSubmit hook ─────────────────────────────
	// Never fires on turn one. After multi-step tool activity, it is limited by
	// the same configurable turn-budget pattern used by the skill nudge.
	if nudgeText, shouldNudge := hm.taskNudgeBudget.CheckTurn(ctx, conversationID, prompt); shouldNudge {
		results = append(results, agent.HookResult{
			HookName:          "task-nudge",
			Success:           true,
			Output:            nudgeText,
			AdditionalContext: nudgeText,
		})
		injectedContext = append(injectedContext, nudgeText)
	}

	// ── Flush pending tool-time hook messages ────────────────────────────────────
	// Drain messages queued during tool execution (SteeringPreTool, PostActing,
	// PlanModeFirst, auto-mode approvals) so the agent sees them this turn.
	// Cap at 3 to avoid drowning the context window when many tools ran — prior
	// cap of 5 still permitted "5 identical decompose-first" stacks in the field.
	if pendingMsgs := hm.drainPendingToolMessages(); len(pendingMsgs) > 0 {
		if len(pendingMsgs) > 3 {
			pendingMsgs = pendingMsgs[len(pendingMsgs)-3:] // keep most recent 3
		}
		injectedContext = append(injectedContext, pendingMsgs...)
	}

	// Dedupe by first-line prefix so repeated "[Steering] decompose first"
	// banners collapse to one. Order-preserving: the first occurrence wins so
	// the agent still sees the oldest queued signal rather than a rewrite.
	injectedContext = dedupeByLeadingLine(injectedContext)

	// Join all injected context — built AFTER all hooks (including built-ins) have
	// appended their output so nothing is lost.
	contextStr := ""
	if len(injectedContext) > 0 {
		contextStr = strings.Join(injectedContext, "\n")
	}

	logDebug("[HooksManager] EmitUserPromptSubmit complete: results=%d, injected_len=%d, blocked=%v",
		len(results), len(contextStr), err != nil)

	return contextStr, results, err
}

// GetStats returns hook execution statistics
func (hm *HooksManager) GetStats() hooks.RegistryStats {
	return hm.manager.GetStats()
}

// ListHooks returns all registered hooks
func (hm *HooksManager) ListHooks() []hooks.HookRegistration {
	return hm.manager.List()
}

// GetManager returns the underlying hook manager
func (hm *HooksManager) GetManager() *hooks.Manager {
	return hm.manager
}

// GetGoalHook returns the registered /goal stop-hook so UI surfaces (e.g. the
// ACP /goal slash command) can set, clear, and read the active goal. Returns
// nil only if registerBuiltinHooks has not run.
func (hm *HooksManager) GetGoalHook() *builtin.GoalHook {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.goalHook
}

// SetupGoalPersistence points the GoalHook at the goal.json file for the given
// conversation and loads any persisted goal. Passing an empty metadataDir (or a
// hook that isn't registered yet) is a safe no-op. Call this alongside
// SetupTaskPersistence whenever a conversation is opened, created, or switched.
func (hm *HooksManager) SetupGoalPersistence(metadataDir string) {
	hm.mu.RLock()
	gh := hm.goalHook
	hm.mu.RUnlock()
	if gh == nil {
		return
	}
	if metadataDir == "" {
		gh.SetStorePath("")
		return
	}
	gh.SetStorePath(filepath.Join(metadataDir, builtin.GoalStoreFileName))
}

// EnableGoalLLMEvaluator replaces the placeholder DefaultEvaluator on the
// GoalHook with a real LLM-backed evaluator. Call this after the provider is
// ready (e.g. from SDKIntegration after provider init).
func (hm *HooksManager) EnableGoalLLMEvaluator(fn func(ctx context.Context, condition, transcript string) (builtin.GoalResult, error)) {
	hm.mu.RLock()
	gh := hm.goalHook
	hm.mu.RUnlock()
	if gh == nil || fn == nil {
		return
	}
	gh.SetEvaluator(builtin.NewFuncEvaluator(fn))
}

// SetHookPermissionPolicy updates the permission policy for a hook.
func (hm *HooksManager) SetHookPermissionPolicy(name string, policy hooks.HookPermissionPolicy) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if err := hm.manager.SetPermissionPolicy(name, policy); err != nil {
		return fmt.Errorf("failed to update hook permission policy for %s: %w", name, err)
	}

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hook.permission_policy_updated",
			observability.F("hook", name),
			observability.F("policy", policy))
	}

	return nil
}

// RegisterCustomHook registers a custom shell hook
func (hm *HooksManager) RegisterCustomHook(hook *hooks.ShellHook) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	workingDir, err := SanitizeHookWorkingDir(hook.WorkingDir, hm.workspaceRoot)
	if err != nil {
		return fmt.Errorf("invalid hook working_dir: %w", err)
	}
	hook.WorkingDir = workingDir

	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("failed to register custom hook %s: %w", hook.Name(), err)
	}
	hm.hookStates[hook.Name()] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hook.custom_registered",
			observability.F("hook", hook.Name()))
	}

	return nil
}

// UnregisterCustomHook unregisters a custom hook by name
func (hm *HooksManager) UnregisterCustomHook(name string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if err := hm.manager.Unregister(name, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("failed to unregister custom hook %s: %w", name, err)
	}
	delete(hm.hookStates, name)

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hook.custom_unregistered",
			observability.F("hook", name))
	}

	return nil
}

// GetAllHookStates returns all hook states (for UI sync)
func (hm *HooksManager) GetAllHookStates() map[string]bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	states := make(map[string]bool)
	maps.Copy(states, hm.hookStates)
	return states
}

// EmitSessionStart emits an agent.started event at session start.
// This fires the session start hook which injects plan mode guidance.
//
// sessionID here really is a session id (conversation.ProcessSessionID(),
// via opts.RootSessionID) — unlike EmitUserPromptSubmit/EmitAgentStopped
// below, whose same-shaped parameter is actually a conversation id. This
// fires once at process boot, before any specific conversation necessarily
// exists, so the process session id is the best available identity; the
// resulting event.ConversationID is therefore an exception to what its name
// implies elsewhere in the stream — see hooks.Event.ConversationID's doc
// comment.
func (hm *HooksManager) EmitSessionStart(ctx context.Context, sessionID string) []agent.HookResult {
	var results []agent.HookResult

	if hm.logger != nil {
		hm.logger.Info(ctx, "hooks.emit_session_start",
			observability.F("session_id", sessionID))
	}

	event := hooks.Event{
		Type:           hooks.EventAgentStarted,
		Timestamp:      time.Now(),
		ConversationID: sessionID,
		Data: map[string]any{
			"session_id":     sessionID,
			"workspace_path": hm.workspaceRoot,
		},
	}

	// Execute hooks and get full result with outputs
	execResult, err := hm.manager.EmitWithResult(ctx, event)

	// Convert hook outputs to agent.HookResult for UI display
	if execResult != nil && len(execResult.HookOutputs) > 0 {
		for _, ho := range execResult.HookOutputs {
			results = append(results, agent.HookResult{
				HookName:          ho.HookName,
				Success:           ho.Success,
				Output:            ho.Output,
				AdditionalContext: ho.Output, // Inject as system message
				Blocked:           execResult.Blocked && execResult.BlockedBy == ho.HookName,
				Error:             ho.Error,
			})
		}
	}

	if hm.logger != nil {
		if err != nil {
			hm.logger.Error(ctx, "hooks.session_start_error",
				observability.F("error", err.Error()))
		} else {
			hm.logger.Info(ctx, "hooks.session_start_success",
				observability.F("hooks_executed", len(results)))
		}
	}

	return results
}

// EmitAgentStopped emits an agent.stopped event when the agent finishes execution.
// This fires the verification protocol hook and other stop-related hooks.
// prompt and response carry the last turn's text. fullTranscript optionally
// carries its tool calls and results for evidence-aware hooks like GoalHook.
//
// conversationID: same caveat as EmitUserPromptSubmit above — this ends up
// under Data["session_id"] on the wire (Claude Code compatibility), not
// conversation.ProcessSessionID().
func (hm *HooksManager) EmitAgentStopped(ctx context.Context, conversationID string, finishReason string, prompt string, response string, fullTranscript ...string) []agent.HookResult {
	var results []agent.HookResult

	if hm.logger != nil {
		hm.logger.Info(ctx, "hooks.emit_agent_stopped",
			observability.F("conversation_id", conversationID),
			observability.F("finish_reason", finishReason))
	}

	transcript := ""
	if len(fullTranscript) > 0 {
		transcript = fullTranscript[0]
	}
	event := hooks.Event{
		Type:           hooks.EventAgentStopped,
		Timestamp:      time.Now(),
		ConversationID: conversationID,
		Data: map[string]any{
			// Key stays "session_id" on purpose (Claude Code hook-script
			// compatibility); the VALUE is a conversation id.
			"session_id":      conversationID,
			"finish_reason":   finishReason,
			"prompt":          prompt,
			"prompt_response": response,
			"transcript":      transcript,
		},
	}

	// Execute hooks and get full result with outputs
	execResult, err := hm.manager.EmitWithResult(ctx, event)

	// Convert hook outputs to agent.HookResult for UI display
	// IMPORTANT: Also set AdditionalContext so messages are injected into agent context
	if execResult != nil && len(execResult.HookOutputs) > 0 {
		for _, ho := range execResult.HookOutputs {
			results = append(results, agent.HookResult{
				HookName:          ho.HookName,
				Success:           ho.Success,
				Output:            ho.Output,
				AdditionalContext: ho.Output, // Inject as system message
				Blocked:           execResult.Blocked && execResult.BlockedBy == ho.HookName,
				Error:             ho.Error,
			})
		}
	}

	if hm.logger != nil {
		if err != nil {
			hm.logger.Error(ctx, "hooks.agent_stopped_error",
				observability.F("error", err.Error()))
		} else {
			hm.logger.Info(ctx, "hooks.agent_stopped_success",
				observability.F("hooks_executed", len(results)))
		}
	}

	return results
}

// EmitMessageAfterReceive emits a message.after_receive event when a user message is received.
// This fires the task enforcement hook's plan-following detection logic.
//
// conversationID: same caveat as EmitUserPromptSubmit — this is a
// conversation id (all call sites pass convID), not
// conversation.ProcessSessionID(), despite ending up under the
// Claude-Code-compatible "session_id" wire key.
func (hm *HooksManager) EmitMessageAfterReceive(ctx context.Context, conversationID string, content string) {
	if hm.logger != nil {
		hm.logger.Info(ctx, "hooks.emit_message_after_receive",
			observability.F("conversation_id", conversationID),
			observability.F("content_length", len(content)))
	}

	event := hooks.Event{
		Type:           hooks.EventMessageAfterReceive,
		Timestamp:      time.Now(),
		ConversationID: conversationID,
		Data: map[string]any{
			// Key stays "session_id" on purpose (Claude Code hook-script
			// compatibility); the VALUE is a conversation id.
			"session_id": conversationID,
			"role":       "user",
			"content":    content,
		},
	}

	// Execute hooks (fire-and-forget, no results needed for message events)
	_, _ = hm.manager.EmitWithResult(ctx, event)
}

// EmitContextTrimmed fires a context.trimmed event after a successful compaction.
// originalTokens and compactedTokens are the before/after token counts.
// This populates silver_context_events on the data platform.
func (hm *HooksManager) EmitContextTrimmed(ctx context.Context, originalTokens, compactedTokens int) {
	if hm == nil || hm.manager == nil {
		return
	}
	_, _ = hm.manager.Emit(ctx, hooks.Event{
		Type:      hooks.EventContextTrimmed,
		Timestamp: time.Now(),
		Data: map[string]any{
			"token_count":      compactedTokens,
			"context_limit":    originalTokens,
			"original_tokens":  originalTokens,
			"compacted_tokens": compactedTokens,
			"trigger":          "auto_compaction",
		},
	})
}

// EmitProviderResponse fires a provider.after_response event with latency and token data.
// Called from the agent execute chain after each provider round-trip.
// This populates silver_provider_requests on the data platform once the silver
// transform is added.
//
// Identity (G2 follow-up, PLAN.md): AgentID/ConversationID come from ctx via
// tools.OwnerAgentID/tools.OwnerConversationID, same as EmitToolBeforeExecute
// above — internal/agent's agent_execute_chain.go now stamps the provider
// round-trip's ctx with tools.WithOwnerInfo before calling this method, so
// it is populated here for free rather than being permanently un-attributable.
func (hm *HooksManager) EmitProviderResponse(ctx context.Context, providerName, model string, inputTokens, outputTokens int, durationMs int64) {
	if hm == nil || hm.manager == nil {
		return
	}
	_, _ = hm.manager.Emit(ctx, hooks.Event{
		Type:           hooks.EventProviderAfterResponse,
		Timestamp:      time.Now(),
		AgentID:        tools.OwnerAgentID(ctx),
		ConversationID: tools.OwnerConversationID(ctx),
		Data: map[string]any{
			"provider":      providerName,
			"model":         model,
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
			"tokens_used":   inputTokens + outputTokens,
			"duration_ms":   durationMs,
			"usage": map[string]any{
				"input":  inputTokens,
				"output": outputTokens,
			},
		},
	})
}

// EnableFindings creates and registers the findings capture hooks.
// This should be called when findings are enabled in settings.
func (hm *HooksManager) EnableFindings(cacheDir string, autoCaptureTools []string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Already enabled?
	if hm.localFindings != nil {
		return nil
	}

	// Create cache and index
	config := findings.Config{
		LocalCacheDir:    cacheDir,
		AutoCaptureTools: autoCaptureTools,
	}
	cache, err := findings.NewFileCache(config)
	if err != nil {
		return fmt.Errorf("failed to create findings cache: %w", err)
	}

	semanticIdx, err := findings.NewFaissSemanticIndex(cacheDir, nil)
	if err != nil {
		cache.Close()
		return fmt.Errorf("failed to create semantic index: %w", err)
	}

	// Create and register local findings hook (priority 85)
	localHook, err := builtin.NewLocalFindingsHook(cache, semanticIdx, hm.logger)
	if err != nil {
		semanticIdx.Close()
		cache.Close()
		return fmt.Errorf("failed to create local findings hook: %w", err)
	}
	localHook.SetEventEmitter(hm.manager)
	hm.localFindings = localHook

	// Set auto-capture tools if specified
	if len(autoCaptureTools) > 0 {
		hm.localFindings.SetAutoCapture(autoCaptureTools)
	}

	if err := hm.manager.Register(hm.localFindings, hooks.ScopeGlobal, ""); err != nil {
		hm.localFindings.Close()
		hm.localFindings = nil
		return fmt.Errorf("failed to register local findings hook: %w", err)
	}

	hm.hookStates["findings"] = true
	hm.findingsCacheDir = cacheDir

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.findings_enabled",
			observability.F("cache_dir", cacheDir))
	}

	return nil
}

// DisableFindings unregisters and cleans up the findings capture hooks.
func (hm *HooksManager) DisableFindings() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.localFindings == nil {
		return nil
	}

	// Unregister local findings hook
	if err := hm.manager.Unregister(hm.localFindings.Name(), hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_findings_failed",
			observability.F("error", err.Error()))
	}

	// Close the local findings hook (which closes cache and index)
	if err := hm.localFindings.Close(); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.close_findings_failed",
			observability.F("error", err.Error()))
	}

	hm.localFindings = nil
	hm.hookStates["findings"] = false

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.findings_disabled")
	}

	return nil
}

// FindingsEnabled returns whether the findings hooks are currently registered.
func (hm *HooksManager) FindingsEnabled() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.localFindings != nil
}

// EnableBronzeCapture creates and registers the bronze tier event capture hook.
// This captures ALL events to raw JSONL storage for later analysis.
func (hm *HooksManager) EnableBronzeCapture(cacheDir string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Already enabled?
	if hm.bronzeHook != nil {
		return nil
	}

	// Create bronze hook
	bronzeDir := filepath.Join(cacheDir, "bronze")
	hook, err := builtin.NewBronzeEventHook(bronzeDir, hm.logger)
	if err != nil {
		return fmt.Errorf("failed to create bronze event hook: %w", err)
	}
	hook.SetEventEmitter(hm.manager)

	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		hook.Close()
		return fmt.Errorf("failed to register bronze event hook: %w", err)
	}

	hm.bronzeHook = hook
	hm.hookStates["bronze-capture"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.bronze_enabled",
			observability.F("cache_dir", bronzeDir))
	}

	return nil
}

// DisableBronzeCapture unregisters and cleans up the bronze event capture hook.
func (hm *HooksManager) DisableBronzeCapture() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.bronzeHook == nil {
		return nil
	}

	if err := hm.manager.Unregister(hm.bronzeHook.Name(), hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_bronze_failed",
			observability.F("error", err.Error()))
	}

	if err := hm.bronzeHook.Close(); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.close_bronze_failed",
			observability.F("error", err.Error()))
	}

	hm.bronzeHook = nil
	hm.hookStates["bronze-capture"] = false

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.bronze_disabled")
	}

	return nil
}

// BronzeCaptureEnabled returns whether bronze event capture is enabled.
func (hm *HooksManager) BronzeCaptureEnabled() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.bronzeHook != nil
}

// EnableSilverTree registers and activates the Silver tree build hook.
// The hook fires asynchronously at session end (EventAgentStopped) and builds
// a PageIndex-style hierarchical tree index over the Bronze event data.
// cacheDir should be the same findings cache directory used by Bronze.
func (hm *HooksManager) EnableSilverTree(cacheDir, projectHash string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.silverTreeHook != nil {
		return nil // already enabled
	}

	silverDir, err := silver.FindSilverDir(projectHash)
	if err != nil {
		return fmt.Errorf("silver tree hook: resolve silver dir: %w", err)
	}

	cfg := builtin.SilverConfig{
		Enabled:     true,
		ProjectHash: projectHash,
		BronzeDir:   filepath.Join(cacheDir, "bronze"),
		SilverDir:   silverDir,
	}

	hook := builtin.NewSilverTreeHook(cfg, hm.logger)
	hook.SetEventEmitter(hm.manager)
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("silver tree hook: register: %w", err)
	}

	hm.silverTreeHook = hook
	hm.hookStates["silver-tree"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.silver_tree_enabled",
			observability.F("project_hash", projectHash),
			observability.F("silver_dir", silverDir))
	}

	return nil
}

// DisableSilverTree unregisters the Silver tree build hook.
func (hm *HooksManager) DisableSilverTree() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.silverTreeHook == nil {
		return nil
	}

	if err := hm.manager.Unregister(hm.silverTreeHook.Name(), hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_silver_failed",
			observability.F("error", err.Error()))
	}

	hm.silverTreeHook = nil
	hm.hookStates["silver-tree"] = false
	return nil
}

// SilverTreeEnabled reports whether Silver tree building is active.
func (hm *HooksManager) SilverTreeEnabled() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.silverTreeHook != nil
}

// EnableGold registers and activates the Gold analysis hook.
// Gold fires asynchronously at session end and runs statistical analysis
// over Silver trees to produce insight reports (tool efficiency, correction
// patterns, cross-project patterns, domain transfer opportunities).
// cacheDir should be the same findings cache directory used by Bronze.
func (hm *HooksManager) EnableGold(cacheDir, projectHash string) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.goldHook != nil {
		return nil // already enabled
	}

	goldDir, err := gold.GlobalGoldDir()
	if err != nil {
		return fmt.Errorf("gold hook: resolve gold dir: %w", err)
	}

	cfg := builtin.GoldConfig{
		Enabled: true,
		GoldDir: goldDir,
	}

	hook := builtin.NewGoldHook(cfg, hm.logger)
	hook.SetEventEmitter(hm.manager)
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("gold hook: register: %w", err)
	}

	hm.goldHook = hook
	hm.hookStates["gold-insights"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.gold_enabled",
			observability.F("gold_dir", goldDir))
	}

	return nil
}

// DisableGold unregisters the Gold analysis hook.
func (hm *HooksManager) DisableGold() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.goldHook == nil {
		return nil
	}

	if err := hm.manager.Unregister(hm.goldHook.Name(), hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_gold_failed",
			observability.F("error", err.Error()))
	}

	hm.goldHook = nil
	hm.hookStates["gold-insights"] = false
	return nil
}

// GoldEnabled reports whether Gold insight generation is active.
func (hm *HooksManager) GoldEnabled() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.goldHook != nil
}

// GetBronzeStats returns statistics about bronze storage.
func (hm *HooksManager) GetBronzeStats() (builtin.Stats, error) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.bronzeHook == nil {
		return builtin.Stats{}, fmt.Errorf("bronze capture not enabled")
	}

	return hm.bronzeHook.GetStats()
}

// GetLocalFindingsHook returns the local findings hook for testing or advanced use.
// Returns nil if findings are not enabled.
func (hm *HooksManager) GetLocalFindingsHook() *builtin.LocalFindingsHook {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.localFindings
}

// EnableFindingsAnalysis creates and registers the findings analysis hook.
// This hook uses the SteeringAgent's LLM to score findings.
func (hm *HooksManager) EnableFindingsAnalysis(
	steeringAgent *agent.SteeringAgent,
	cache findings.Cache,
	config builtin.FindingsAnalysisConfig,
) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	// Already enabled?
	if hm.findingsAnalysis != nil {
		return nil
	}

	hook := builtin.NewFindingsAnalysisHook(steeringAgent, cache, hm.logger, config, hm.findingsCacheDir)
	hook.SetEventEmitter(hm.manager)
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("failed to register findings analysis hook: %w", err)
	}

	hm.findingsAnalysis = hook
	hm.hookStates["findings-analysis"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.findings_analysis_enabled",
			observability.F("trigger_mode", string(config.TriggerMode)),
			observability.F("batch_size", config.BatchSize))
	}

	return nil
}

// DisableFindingsAnalysis unregisters the findings analysis hook.
func (hm *HooksManager) DisableFindingsAnalysis() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.findingsAnalysis == nil {
		return nil
	}

	if err := hm.manager.Unregister(hm.findingsAnalysis.Name(), hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_findings_analysis_failed",
			observability.F("error", err.Error()))
	}

	hm.findingsAnalysis = nil
	hm.hookStates["findings-analysis"] = false

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.findings_analysis_disabled")
	}

	return nil
}

// UpdateFindingsAnalysisConfig updates the analysis hook config at runtime.
func (hm *HooksManager) UpdateFindingsAnalysisConfig(config builtin.FindingsAnalysisConfig) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	if hm.findingsAnalysis != nil {
		hm.findingsAnalysis.SetConfig(config)
	}
}

// FindingsAnalysisEnabled returns whether findings analysis is enabled.
func (hm *HooksManager) FindingsAnalysisEnabled() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.findingsAnalysis != nil
}

// SetFindingsAnalysisContextProvider sets the rich context provider for the analysis hook.
func (hm *HooksManager) SetFindingsAnalysisContextProvider(cp builtin.AnalysisContextProvider) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if hm.findingsAnalysis != nil {
		hm.findingsAnalysis.SetContextProvider(cp)
	}
}

// SetSteeringPreToolContextProvider sets the rich context provider for the
// pre-tool steering hook. Safe to call before the hook is enabled; if the
// hook is later enabled it will need to be re-set.
func (hm *HooksManager) SetSteeringPreToolContextProvider(cp builtin.SteeringContextProvider) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if hm.steeringPreTool != nil {
		hm.steeringPreTool.SetContextProvider(cp)
	}
}

// EnableSteeringPreTool creates and registers the pre-tool steering hook.
// This hook evaluates tool calls for task relevance before execution.
func (hm *HooksManager) EnableSteeringPreTool(sa *agent.SteeringAgent) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.steeringPreTool != nil {
		return nil // already enabled
	}

	hook := builtin.NewSteeringPreToolHook(sa, hm.logger)
	hook.SetEventEmitter(hm.manager)
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		return fmt.Errorf("failed to register steering pre-tool hook: %w", err)
	}

	hm.steeringPreTool = hook
	hm.hookStates["steering-pretool"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.steering_pretool_enabled")
	}

	return nil
}

// DisableSteeringPreTool unregisters the pre-tool steering hook.
func (hm *HooksManager) DisableSteeringPreTool() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.steeringPreTool == nil {
		return nil
	}

	if err := hm.manager.Unregister("steering-pretool", hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_steering_pretool_failed",
			observability.F("error", err.Error()))
	}

	hm.steeringPreTool = nil
	hm.hookStates["steering-pretool"] = false

	return nil
}

// EnableSteeringStream creates and registers the streaming steering hook.
// This is the Phase 2 alternative to EnableSteeringPreTool: instead of firing
// a separate LLM call per tool invocation, a long-lived observer agent watches
// a batched event stream and intervenes via steering tools.
//
// The caller supplies the observer agent (equipped with steeringtools) and
// optionally a SteeringTarget. When target is nil, the driver auto-creates
// a defaultSteeringTarget.
//
// Only ONE of {EnableSteeringPreTool, EnableSteeringStream} should be active
// at a time. If the polling hook is already enabled, EnableSteeringStream
// disables it first.
func (hm *HooksManager) EnableSteeringStream(observerAgent *agent.Agent, target agent.SteeringTarget) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.steeringStream != nil {
		return nil // already enabled
	}

	// Disable polling hook if active (mutual exclusion).
	if hm.steeringPreTool != nil {
		if err := hm.manager.Unregister("steering-pretool", hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
			hm.logger.Warn(context.Background(), "hooks.unregister_steering_pretool_for_stream",
				observability.F("error", err.Error()))
		}
		hm.steeringPreTool = nil
		hm.hookStates["steering-pretool"] = false
	}

	driverCfg := agent.SteeringDriverConfig{
		Mode:          agent.SteeringModeStream,
		SteeringAgent: observerAgent,
		Target:        target, // nil is fine — driver auto-creates a default
	}
	driver := agent.NewStreamingSteeringDriver(driverCfg)
	if err := driver.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start streaming steering driver: %w", err)
	}

	hook := builtin.NewSteeringStreamHook(driver, hm.logger)
	if err := hm.manager.Register(hook, hooks.ScopeGlobal, ""); err != nil {
		_ = driver.Stop()
		return fmt.Errorf("failed to register steering stream hook: %w", err)
	}

	hm.steeringStream = hook
	hm.steeringStreamDriver = driver
	hm.hookStates["steering-stream"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.steering_stream_enabled")
	}

	return nil
}

// DisableSteeringStream unregisters the streaming steering hook and stops
// the driver. Safe to call when not enabled.
func (hm *HooksManager) DisableSteeringStream() error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if hm.steeringStream == nil {
		return nil
	}

	if err := hm.manager.Unregister("steering-stream", hooks.ScopeGlobal, ""); err != nil && hm.logger != nil {
		hm.logger.Warn(context.Background(), "hooks.unregister_steering_stream_failed",
			observability.F("error", err.Error()))
	}

	_ = hm.steeringStreamDriver.Stop()
	hm.steeringStream = nil
	hm.steeringStreamDriver = nil
	hm.hookStates["steering-stream"] = false

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.steering_stream_disabled")
	}

	return nil
}

// SteeringStreamDriver returns the active streaming steering driver, if any.
// Returns nil when stream-mode steering is not enabled.
func (hm *HooksManager) SteeringStreamDriver() *agent.StreamingSteeringDriver {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.steeringStreamDriver
}

// GetAutoModeHook returns the registered auto-mode hook (may be nil if
// registration failed during init). The caller can inspect/modify its
// config (e.g. toggle SkipAutoPermissionPrompt at runtime) or reuse its
// classifier for external callers like the approval UI.
// AddPendingToolMessage queues a message to be injected into the agent's
// conversation context on the next user turn. Call this from any site that
// has context the agent should see but that occurs outside a user prompt
// (e.g. auto-mode approval decisions, tool-time steering guidance).
func (hm *HooksManager) AddPendingToolMessage(msg string) {
	if msg == "" {
		return
	}
	hm.pendingToolMu.Lock()
	defer hm.pendingToolMu.Unlock()
	hm.pendingToolMessages = append(hm.pendingToolMessages, msg)
}

// drainPendingToolMessages returns and clears all pending tool messages.
// Called by EmitUserPromptSubmit to flush them into the injected context.
func (hm *HooksManager) drainPendingToolMessages() []string {
	hm.pendingToolMu.Lock()
	defer hm.pendingToolMu.Unlock()
	if len(hm.pendingToolMessages) == 0 {
		return nil
	}
	msgs := make([]string, len(hm.pendingToolMessages))
	copy(msgs, hm.pendingToolMessages)
	hm.pendingToolMessages = hm.pendingToolMessages[:0]

	// Deduplicate: tool hooks can queue identical messages (e.g. 5x
	// "Requires approval" from consecutive tool calls).  Keep first
	// occurrence, drop exact duplicates to avoid flooding the agent's
	// context with repeated text.
	seen := make(map[string]bool, len(msgs))
	deduped := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if !seen[m] {
			seen[m] = true
			deduped = append(deduped, m)
		}
	}
	return deduped
}

// dedupeByLeadingLine collapses entries that share the same first ~80 chars.
// This catches "near-duplicate" steering blocks where the body varies but the
// banner is identical (e.g. 5x "[Steering] Project context: ..." each with a
// slightly different tail). Order-preserving: the first occurrence is kept.
//
// Pure function — safe to call after exact-dedupe in drainPendingToolMessages
// because it collapses a strictly larger equivalence class.
func dedupeByLeadingLine(msgs []string) []string {
	if len(msgs) < 2 {
		return msgs
	}
	seen := make(map[string]bool, len(msgs))
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		key := m
		// Truncate to the first newline or 80 runes, whichever comes first.
		if nl := strings.IndexByte(key, '\n'); nl > 0 {
			key = key[:nl]
		}
		if len(key) > 80 {
			key = key[:80]
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, m)
		}
	}
	return out
}

func (hm *HooksManager) GetAutoModeHook() *builtin.AutoModeHook {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.autoMode
}

// ClassifyToolForAutoMode returns the auto-mode classification verdict for
// a pending tool call. The second return is false when auto-mode is not
// enabled, in which case callers should fall through to the normal approval prompt.
func (hm *HooksManager) ClassifyToolForAutoMode(ctx context.Context, toolName string, toolInput map[string]any) (builtin.ClassificationResult, bool) {
	hm.mu.RLock()
	h := hm.autoMode
	hm.mu.RUnlock()
	if h == nil || !h.IsEnabled() {
		return builtin.ClassificationResult{}, false
	}
	cfg := h.GetConfig()
	classifier := builtin.NewDefaultAutoModeClassifier(hm.logger)
	res, err := classifier.Classify(ctx, toolName, toolInput, cfg.AutoMode)
	if err != nil {
		if hm.logger != nil {
			hm.logger.Warn(ctx, "hooks.auto_mode.classify_failed",
				observability.F("tool", toolName),
				observability.F("error", err.Error()))
		}
		return builtin.ClassificationResult{}, false
	}
	return res, true
}

// GoldAnalysisEnabled returns whether Gold tier analysis is enabled.
func (hm *HooksManager) GoldAnalysisEnabled() bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.goldAnalyzer != nil
}

// EnableAutogenSkills wires the autogenskills service into the TUI.
// It registers the BudgetEnforcementHook and LifecycleHook with the
// underlying SDK hooks.Manager, and adds the SkillManage tool to the
// agent's tool registry.
//
// CONTRACT:
//   - svc is created by autogenskills.NewService; nil means disabled.
//   - skillReg is the skills.Registry used for skill discovery.
//   - If svc is nil or mode is ModeNever, this is a no-op.
//   - Thread-safe: can be called from agent setup goroutines.
func (hm *HooksManager) EnableAutogenSkills(svc *autogenskills.Service, skillReg *skills.Registry) error {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	if svc == nil || !svc.GetConfig().IsEnabled() {
		return nil // no-op for disabled
	}

	// Register hooks (LifecycleHook + BudgetEnforcementHook)
	if err := svc.RegisterHooksWithManager(hm.manager); err != nil {
		return fmt.Errorf("autogenskills hook registration failed: %w", err)
	}

	hm.autogenSkills = svc
	hm.autogenSkillReg = skillReg
	hm.hookStates["autogenskills"] = true

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "hooks.autogenskills_enabled",
			observability.F("mode", svc.GetConfig().Mode),
			observability.F("onboarding_budget", svc.GetConfig().Trigger.ToolCallBudget),
			observability.F("working_budget", svc.GetConfig().Trigger.WorkingBudget),
			observability.F("max_nudge_ignores", svc.GetConfig().Trigger.MaxNudgeIgnores),
		)
	}
	return nil
}

// GetAutogenSkillsService returns the autogenskills service, or nil if not enabled.
func (hm *HooksManager) GetAutogenSkillsService() *autogenskills.Service {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.autogenSkills
}

// GetBudgetSnapshot returns the current autogenskills budget state for UI display.
// Returns a zero-value BudgetSnapshot when autogenskills is disabled.
func (hm *HooksManager) GetBudgetSnapshot() autogenskills.BudgetSnapshot {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if hm.autogenSkills == nil {
		return autogenskills.BudgetSnapshot{}
	}
	hook := hm.autogenSkills.GetBudgetEnforcementHook()
	if hook == nil {
		return autogenskills.BudgetSnapshot{}
	}
	return hook.GetBudgetSnapshot()
}

// GetSkillGuidance returns the SKILLS_GUIDANCE text for system prompt injection.
// Returns empty string when autogenskills is disabled.
// CONTRACT: Call once at session start, not per-turn.
func (hm *HooksManager) GetSkillGuidance() string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if hm.autogenSkills == nil {
		return ""
	}
	return hm.autogenSkills.GetSKILLSGuidance()
}

// GetSkillIndex returns the autogen skill index for system prompt injection.
// Returns empty string when no autogen skills exist or autogenskills is disabled.
// CONTRACT: Call once at session start.
func (hm *HooksManager) GetSkillIndex() string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	if hm.autogenSkills == nil {
		return ""
	}
	return hm.autogenSkills.GetSkillIndex()
}

// SetCuratorAgent sets the optional LLM-powered consolidation agent. Routine
// stale/archive lifecycle maintenance is deterministic and does not use it.
// CONTRACT: Call once during setup. Thread-safe.
func (hm *HooksManager) SetCuratorAgent(ca *autogenskills.CuratorAgent) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.curatorAgent = ca
}

// GetCuratorAgent returns the curator agent, or nil if not set.
func (hm *HooksManager) GetCuratorAgent() *autogenskills.CuratorAgent {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.curatorAgent
}

// NotifyAgentIdle starts the inactivity timer for curator maintenance. When
// the timer fires, it enters through the same ShouldRun cadence gate used by
// daemon/headless callers.
// CONTRACT: Call when the main agent transitions to idle state.
// Resets any existing timer.
func (hm *HooksManager) NotifyAgentIdle() {
	hm.curatorMu.Lock()
	defer hm.curatorMu.Unlock()

	hm.curatorAgentBusy = false

	if hm.curatorRunning {
		return // already running
	}

	// Check if we have any curator capability (LLM agent or rule-based)
	hasCurator := hm.curatorAgent != nil
	if !hasCurator {
		if svc := hm.GetAutogenSkillsService(); svc != nil {
			if svc.GetCurator() != nil {
				hasCurator = true
			}
		}
	}
	if !hasCurator {
		return // no curator of any kind
	}

	// Stop any existing timer
	if hm.curatorIdleTimer != nil {
		hm.curatorIdleTimer.Stop()
	}

	// Get the interval from config (default 1 hour)
	interval := 1 * time.Hour
	if svc := hm.GetAutogenSkillsService(); svc != nil {
		curatorCfg := svc.GetConfig().Curator.WithDefaults()
		if d, err := time.ParseDuration(curatorCfg.Interval); err == nil {
			interval = d
		}
	}

	hm.curatorIdleTimer = time.AfterFunc(interval, func() {
		// Re-check engine state at fire time: NotifyAgentBusy's Stop() can
		// race an already-fired timer, and the curator must not contend with
		// an active turn. The next NotifyAgentIdle re-arms the timer.
		hm.curatorMu.Lock()
		busy := hm.curatorAgentBusy
		hm.curatorMu.Unlock()
		if busy {
			if hm.logger != nil {
				hm.logger.Info(context.Background(), "curator.skipped_agent_busy")
			}
			return
		}
		hm.RunCuratorIfDue()
	})

	if hm.logger != nil {
		hm.logger.Info(context.Background(), "curator.idle_timer_started",
			observability.F("interval", interval.String()))
	}
}

// NotifyAgentBusy cancels the curator inactivity timer.
// CONTRACT: Call when the main agent starts executing (transitions from idle to busy).
func (hm *HooksManager) NotifyAgentBusy() {
	hm.curatorMu.Lock()
	defer hm.curatorMu.Unlock()

	hm.curatorAgentBusy = true

	if hm.curatorIdleTimer != nil {
		hm.curatorIdleTimer.Stop()
		hm.curatorIdleTimer = nil
	}
}

// RunCuratorIfDue runs the curator only if the daily-cadence gate
// (Curator.MinRunGap, default 24h) has elapsed since the last run. This is the
// entry point for headless/daemon callers that don't use the idle timer.
//
// CONTRACT:
//   - Non-blocking: the actual run happens in a background goroutine.
//   - No-op if no curator is configured or the cadence gate has not elapsed.
//   - Returns true if a run was triggered, false otherwise.
func (hm *HooksManager) RunCuratorIfDue() bool {
	// Need a rule-based curator to consult the cadence gate.
	svc := hm.GetAutogenSkillsService()
	if svc == nil {
		return false
	}
	curator := svc.GetCurator()
	if curator == nil {
		return false
	}
	if !curator.ShouldRun() {
		if hm.logger != nil {
			hm.logger.Info(context.Background(), "curator.skipped_not_due",
				observability.F("last_run_at", curator.GetLastRunAt().String()))
		}
		return false
	}
	return hm.runCurator()
}

// runCurator executes deterministic lifecycle transitions in a background
// goroutine, then optionally invokes the agent for consolidation. It returns
// false when the stronger in-process running guard suppresses a duplicate.
func (hm *HooksManager) runCurator() bool {
	hm.curatorMu.Lock()
	if hm.curatorRunning {
		hm.curatorMu.Unlock()
		return false
	}
	hm.curatorRunning = true
	hm.curatorMu.Unlock()

	go func() {
		defer func() {
			hm.curatorMu.Lock()
			hm.curatorRunning = false
			hm.curatorMu.Unlock()
		}()

		if hm.logger != nil {
			hm.logger.Info(context.Background(), "curator.run_started",
				observability.F("message", "Running curator for skill maintenance"))
		}

		// Get service and rule-based curator
		var curator *autogenskills.Curator
		var autogenDir string
		curatorTimeout := time.Hour
		if s := hm.GetAutogenSkillsService(); s != nil {
			curator = s.GetCurator()
			autogenDir = s.GetConfig().AutogenDir
			if configured, err := time.ParseDuration(s.GetConfig().Curator.WithDefaults().Timeout); err == nil && configured > 0 {
				curatorTimeout = configured
			}
		}

		// Phase 1: deterministic lifecycle transitions. Patch-age analysis is
		// intentionally absent: old active skills do not need an LLM refresh.
		var reviewResults []autogenskills.ReviewResult
		var reviewErr error
		if curator != nil && autogenDir != "" {
			reviewResults, reviewErr = curator.AutomaticTransitions(autogenDir, nil)
			if reviewErr != nil && hm.logger != nil {
				hm.logger.Warn(context.Background(), "curator.rule_based_run_failed",
					observability.F("error", reviewErr.Error()))
			} else if hm.logger != nil {
				countArchive := 0
				countStale := 0
				for _, r := range reviewResults {
					switch r.Action {
					case autogenskills.ActionArchive:
						countArchive++
					case autogenskills.ActionMarkStale:
						countStale++
					}
				}
				hm.logger.Info(context.Background(), "curator.transitions_completed",
					observability.F("archived", countArchive),
					observability.F("marked_stale", countStale),
					observability.F("total", len(reviewResults)))
			}
		}

		// Phase 2: LLM-powered consolidation is strictly opt-in. With
		// consolidation off, daemon maintenance remains fully deterministic.
		var agentResult *autogenskills.CuratorAgentResult
		var agentErr error
		consolidateOn := curator != nil && curator.ConsolidateEnabled()
		agentRunner := hm.curatorAgentRun
		if agentRunner == nil && hm.curatorAgent != nil {
			agentRunner = hm.curatorAgent.RunWithReview
		}
		agentInvoked := consolidateOn && agentRunner != nil && reviewErr == nil
		if agentInvoked {
			ctx, cancel := context.WithTimeout(context.Background(), curatorTimeout)
			defer cancel()
			agentResult, agentErr = agentRunner(ctx, reviewResults, true)
			// Agent tools can alter active/archive placement. Reconcile those
			// atomic mutations before recording successful cadence.
			if reconcileErr := curator.ReconcileAndSave(autogenDir); reconcileErr != nil {
				if agentErr == nil {
					agentErr = reconcileErr
				}
				if hm.logger != nil {
					hm.logger.Warn(context.Background(), "curator.post_agent_reconcile_failed",
						observability.F("error", reconcileErr.Error()))
				}
			}
		}

		runSucceeded := reviewErr == nil &&
			(!agentInvoked || (agentErr == nil && agentResult != nil && agentResult.Error == nil))
		if runSucceeded && curator != nil {
			curator.SetLastRunAt(time.Now())
			if saveErr := curator.SaveState(); saveErr != nil && hm.logger != nil {
				hm.logger.Warn(context.Background(), "curator.state_save_failed",
					observability.F("error", saveErr.Error()))
			}
		}

		// Log final results
		if hm.logger != nil {
			if agentErr != nil || (agentResult != nil && agentResult.Error != nil) {
				runErr := agentErr
				if runErr == nil {
					runErr = agentResult.Error
				}
				hm.logger.Warn(context.Background(), "curator.agent_run_failed",
					observability.F("error", runErr.Error()))
			} else if agentResult != nil {
				hm.logger.Info(context.Background(), "curator.agent_run_completed",
					observability.F("duration", agentResult.Duration.String()),
					observability.F("turns", agentResult.TurnCount),
					observability.F("tokens", agentResult.TokensUsed),
					observability.F("summary_len", len(agentResult.Summary)))
			}
		}
	}()
	return true
}
