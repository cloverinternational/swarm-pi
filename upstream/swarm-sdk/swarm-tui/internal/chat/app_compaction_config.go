package chat

import (
	"context"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
)

// buildAutoCompactionConfig is the single TUI adapter from persisted settings
// to the SDK contract. It deliberately passes the unresolved descriptor: only
// the agent knows the active model's real context and output budgets.
func buildAutoCompactionConfig(
	compactionSettings *settings.CompactionSettings,
	provider string,
	model string,
) *agent.AutoCompactionConfig {
	if compactionSettings == nil {
		return nil
	}

	persisted := compactionSettings.ResolveThresholdForModel(provider, model)
	threshold := agent.AutoCompactionThreshold{Value: persisted.Value}
	switch persisted.Mode {
	case commands.CompactionThresholdFixedTokens:
		threshold.Mode = agent.AutoCompactionThresholdFixedTokens
	default:
		threshold.Mode = agent.AutoCompactionThresholdPercent
	}

	return &agent.AutoCompactionConfig{
		EnableAutoCompaction: compactionSettings.GetEnableAutoCompaction(),
		ContinueIfRunning:    compactionSettings.GetAutoCompactionContinueIfRunning(),
		Threshold:            threshold,
	}
}

func (sdk *SDKIntegration) reloadAutoCompactionConfig() {
	if sdk == nil {
		return
	}
	sdk.reloadAutoCompactionConfigFor(sdk.activeAgent())
}

func (sdk *SDKIntegration) reloadAutoCompactionConfigFor(target *agent.Agent) {
	if sdk == nil || target == nil {
		return
	}
	configManager, err := commands.NewConfigManager()
	if err != nil {
		return
	}
	compactionSettings := settings.NewCompactionSettings(configManager)
	config := applyDefaultCompactionThreshold(buildAutoCompactionConfig(
		compactionSettings,
		sdk.providerName,
		sdk.currentModel,
	))
	if config != nil {
		if sdk.compactFuncWirer != nil {
			sdk.compactFuncWirer(config, sdk.runtimeSnapshot())
		} else {
			wireAgentCompactFunc(config, sdk.runtimeSnapshot())
		}
		target.SetAutoCompactionConfig(*config)
	}
}

func (sdk *SDKIntegration) activeAgentWithFreshCompactionConfig() *agent.Agent {
	if sdk == nil {
		return nil
	}
	target := sdk.activeAgent()
	sdk.reloadAutoCompactionConfigFor(target)
	return target
}

func (a *App) syncAutoCompactionConfig() {
	if a == nil || a.sdk == nil || a.sdk.activeAgent() == nil || a.settingsManager == nil {
		return
	}
	compactionSettings := a.settingsManager.GetCompactionSettings()
	config := applyDefaultCompactionThreshold(buildAutoCompactionConfig(
		compactionSettings,
		a.currentProvider,
		a.currentModel,
	))
	if config != nil {
		a.wireSharedAgentCompactFunc(config, a.sdk.runtimeSnapshot())
		a.sdk.activeAgent().SetAutoCompactionConfig(*config)
	}
}

// wireSharedAgentCompactFunc wires the agent's in-loop CompactFunc to the
// TUI's SHARED compaction service so threshold-triggered auto-compaction runs
// the SAME pipeline as manual /compact: the same summarizer (with
// compaction.log hooks), the same file-access tracking, the same determinate
// progress bar, and the user's configured post-compaction budget. Falls back
// to the standalone wiring when the shared service is unavailable.
//
// Called on the Update loop (settings reload / model switch / bridge setup).
// The service setters are mutex-guarded, so re-wiring is safe even while the
// agent goroutine is mid-execution.
func (a *App) wireSharedAgentCompactFunc(cfg *agent.AutoCompactionConfig, runtime providerRuntimeSnapshot) {
	if cfg == nil || !cfg.EnableAutoCompaction || runtime.Provider == nil || runtime.Model == "" {
		return
	}
	if a == nil || a.compactionService == nil {
		wireAgentCompactFunc(cfg, runtime)
		return
	}
	contextLimit := runtime.ContextWindow
	if contextLimit <= 0 {
		contextLimit = provider.DefaultUnknownContextWindow
	}
	// Respect the user's configured threshold for the post-compaction budget,
	// mirroring performCompaction's snapshotCompactionRun.
	thresholdTokens := 0
	if a.settingsManager != nil {
		if compactionSettings := a.settingsManager.GetCompactionSettings(); compactionSettings != nil {
			thresholdTokens = compactionSettings.ResolveThresholdTokensForModel(
				runtime.ProviderName,
				runtime.Model,
				contextLimit,
			)
		}
	}
	summaryOutputLimit := compaction.DefaultSummaryMaxTokens
	if caps := runtime.Provider.Capabilities(); caps.MaxOutputTokens > 0 {
		summaryOutputLimit = min(summaryOutputLimit, caps.MaxOutputTokens)
	} else {
		summaryOutputLimit = min(summaryOutputLimit, 4_096)
	}
	svc := a.compactionService
	svc.SetContextLimit(contextLimit)
	svc.SetSummaryMaxTokens(summaryOutputLimit)
	svc.SetSummarizeFunc(func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error) {
		summaryCtx, cancel := context.WithTimeout(ctx, 1500*time.Second)
		defer cancel()
		return a.summarizeForCompaction(
			summaryCtx,
			messages,
			runtime.Provider,
			runtime.ProviderName,
			runtime.Model,
			prompt,
		)
	})
	svc.SetReadFileFunc(func(path string) (string, error) {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(content), nil
	})
	// Drive the TUI's determinate progress bar from real pipeline stages, the
	// same as manual /compact (see performCompaction). Safe cross-goroutine:
	// LoadingIndicator setters are mutex-guarded.
	svc.SetProgressFunc(func(stage string, pct float64) {
		a.loadingIndicator.SetText(compactionStageLabel(stage))
		a.loadingIndicator.SetProgress(pct)
	})
	result, err := compaction.CreateCompactFunc(compaction.AgentCompactionConfig{
		Provider:                runtime.Provider,
		Model:                   runtime.Model,
		ContextLimit:            contextLimit,
		Service:                 svc,
		PostCompactBudgetTokens: thresholdTokens,
	})
	if err != nil {
		logDebug("[AutoCompaction] shared CompactFunc wiring failed, falling back to standalone: %v", err)
		wireAgentCompactFunc(cfg, runtime)
		return
	}
	cfg.CompactFunc = result.CompactFunc
}

// wireAgentCompactFunc is the standalone fallback used when the App's shared
// compaction service is unavailable (headless/degraded mode). It builds a
// self-contained pipeline from the provider snapshot.
func wireAgentCompactFunc(cfg *agent.AutoCompactionConfig, runtime providerRuntimeSnapshot) {
	if cfg == nil || !cfg.EnableAutoCompaction || runtime.Provider == nil || runtime.Model == "" {
		return
	}
	contextLimit := runtime.ContextWindow
	if contextLimit <= 0 {
		contextLimit = provider.DefaultUnknownContextWindow
	}
	result, err := compaction.CreateCompactFunc(compaction.AgentCompactionConfig{
		Provider:     runtime.Provider,
		Model:        runtime.Model,
		ContextLimit: contextLimit,
	})
	if err == nil {
		cfg.CompactFunc = result.CompactFunc
	}
}
