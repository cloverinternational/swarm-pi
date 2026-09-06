package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/ii"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

type compactionRunSnapshot struct {
	Context             *compaction.CompactionContext
	Provider            provider.Provider
	ProviderName        string
	Model               string
	ContextLimit        int
	ThresholdTokens     int
	SummaryContextLimit int
	SummaryOutputLimit  int
}

// snapshotCompactionRun captures all Bubble Tea/settings-owned state before a
// background worker starts.
func (a *App) snapshotCompactionRun(req commands.CompactRequestMsg, convID string) *compactionRunSnapshot {
	var runtime providerRuntimeSnapshot
	if a.sdk != nil {
		runtime = a.sdk.runtimeSnapshot()
	}
	providerName := runtime.ProviderName
	if providerName == "" {
		providerName = a.currentProvider
	}
	model := runtime.Model
	if model == "" {
		model = a.currentModel
	}
	contextLimit := runtime.ContextWindow
	if contextLimit <= 0 {
		contextLimit = provider.DefaultUnknownContextWindow
	}

	thresholdTokens := 0
	if a.settingsManager != nil {
		if settings := a.settingsManager.GetCompactionSettings(); settings != nil {
			thresholdTokens = settings.ResolveThresholdTokensForModel(
				providerName,
				model,
				contextLimit,
			)
		}
	}
	activeProvider := runtime.Provider
	// The summary request uses the same effective context budget as the active
	// agent. Separate low-context summarizer models are no longer configured.
	summaryContextLimit := contextLimit
	summaryOutputLimit := compaction.DefaultSummaryMaxTokens
	if activeProvider == nil {
		summaryContextLimit = min(summaryContextLimit, 32_000)
		summaryOutputLimit = min(summaryOutputLimit, 4_096)
	} else {
		caps := activeProvider.Capabilities()
		if caps.MaxOutputTokens > 0 {
			summaryOutputLimit = min(summaryOutputLimit, caps.MaxOutputTokens)
		} else {
			summaryOutputLimit = min(summaryOutputLimit, 4_096)
		}
	}
	return &compactionRunSnapshot{
		Context:             a.buildCompactionContext(req, convID),
		Provider:            activeProvider,
		ProviderName:        providerName,
		Model:               model,
		ContextLimit:        contextLimit,
		ThresholdTokens:     thresholdTokens,
		SummaryContextLimit: summaryContextLimit,
		SummaryOutputLimit:  summaryOutputLimit,
	}
}

// performCompaction runs the compaction pipeline for convID and, on success,
// advances that conversation's provider-visible context boundary in place. It
// is called from tea.Cmd
// / background goroutines, so it must NOT mutate UI state (a.messages, viewport,
// indices) — handleCompactCompleted owns applying the result to the UI.
func (a *App) performCompaction(
	req commands.CompactRequestMsg,
	convID string,
	snapshot *compactionRunSnapshot,
) (*compaction.CompactionResult, *compaction.CompactionContext, error) {
	if a.compactionMu == nil {
		return nil, nil, fmt.Errorf("compaction lock is not initialized")
	}
	if !a.compactionMu.TryLock() {
		return nil, nil, fmt.Errorf("compaction already in progress")
	}
	defer a.compactionMu.Unlock()

	if a.compactionService == nil || a.sdk == nil {
		a.logCompactionError("initialization", fmt.Errorf("compaction service or SDK not initialized"))
		return nil, nil, fmt.Errorf("compaction service or SDK not initialized")
	}

	if convID == "" {
		a.logCompactionError("initialization", fmt.Errorf("no active conversation"))
		return nil, nil, fmt.Errorf("no active conversation")
	}

	ctx := context.Background()

	// Load the conversation
	conv := a.sdk.GetConversation(ctx, convID)
	if conv == nil {
		a.logCompactionError("load_conversation", fmt.Errorf("failed to load conversation %s", convID))
		return nil, nil, fmt.Errorf("failed to load conversation")
	}

	if snapshot == nil {
		return nil, nil, fmt.Errorf("compaction run snapshot is required")
	}
	compCtx := snapshot.Context
	if snapshot.Provider == nil {
		return nil, compCtx, fmt.Errorf("active provider snapshot is required")
	}

	// Use the exact provider stack and model captured from the active profile.
	// Rebuilding a provider here would lose profile-specific endpoint, credential,
	// reliability, and fallback configuration.
	compactionModelDisplay := fmt.Sprintf("%s/%s", snapshot.ProviderName, snapshot.Model)

	// === DEBUG: Pre-flight information ===
	activeMessages := conv.ActiveMessages()
	a.logCompactionPreFlight(conv.ID, len(activeMessages), conv.CurrentContextSize, req.Strategy, compactionModelDisplay)

	if compCtx == nil {
		return nil, nil, fmt.Errorf("compaction context snapshot is required")
	}
	compCtx.MessageCount = len(activeMessages)

	// Restore persisted state from conversation into service + context
	if a.compactionService != nil {
		if restored := a.compactionService.RestoreFromConversation(conv); restored != nil {
			// Merge tasks if live context came up empty
			if len(compCtx.ActiveTodos) == 0 && len(restored.ActiveTodos) > 0 {
				compCtx.ActiveTodos = restored.ActiveTodos
			}
			if len(compCtx.CompletedTodos) == 0 && len(restored.CompletedTodos) > 0 {
				compCtx.CompletedTodos = restored.CompletedTodos
			}
			if compCtx.CurrentMode == "" && restored.CurrentMode != "" {
				compCtx.CurrentMode = restored.CurrentMode
				compCtx.ModeName = restored.ModeName
			}
			if len(compCtx.ActiveMCPServers) == 0 && len(restored.ActiveMCPServers) > 0 {
				compCtx.ActiveMCPServers = restored.ActiveMCPServers
			}
			// Merge plan state — prefer live in-memory state, fall back to persisted
			if !compCtx.PlanModeActive && restored.PlanModeActive {
				compCtx.PlanModeActive = true
				compCtx.ActivePlan = restored.ActivePlan
			}
			if compCtx.RootSessionID == "" && restored.RootSessionID != "" {
				compCtx.RootSessionID = restored.RootSessionID
			}
		}
	}

	// === DEBUG: Log the compaction context ===
	a.logCompactionContext(compCtx)

	// === DEBUG: Log LLM call start ===
	estimatedInputTokens := conv.TotalTokens + 500 // prompt overhead
	a.logCompactionLLMCall(compactionModelDisplay, estimatedInputTokens)

	// Update the existing compaction service's functions without recreating it.
	// Recreating the service loses the fileAccess map (tracked file reads/writes).
	// Get the context limit from the SDK's provider capabilities instead of hardcoding 200k.
	contextLimit := snapshot.ContextLimit
	a.compactionService.SetContextLimit(snapshot.SummaryContextLimit)
	a.compactionService.SetSummaryMaxTokens(snapshot.SummaryOutputLimit)
	a.compactionService.SetSummarizeFunc(func(ctx context.Context, messages []*conversation.Message, prompt string) (string, error) {
		if a.sdk == nil {
			return "", fmt.Errorf("SDK not available")
		}
		summaryCtx, cancel := context.WithTimeout(ctx, 1500*time.Second)
		defer cancel()
		return a.summarizeForCompaction(
			summaryCtx,
			messages,
			snapshot.Provider,
			snapshot.ProviderName,
			snapshot.Model,
			prompt,
		)
	})
	a.compactionService.SetReadFileFunc(func(path string) (string, error) {
		content, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		return string(content), nil
	})
	// Drive the TUI's determinate progress bar from real pipeline stages
	// (compaction.CompactionConfig.ProgressFunc) instead of a bare spinner.
	// This callback runs on this background tea.Cmd goroutine; LoadingIndicator's
	// SetText/SetProgress are mutex-guarded so this is safe to call concurrently
	// with the Update-loop's render of a.loadingIndicator.View().
	a.compactionService.SetProgressFunc(func(stage string, pct float64) {
		a.loadingIndicator.SetText(compactionStageLabel(stage))
		a.loadingIndicator.SetProgress(pct)
	})

	// Perform compaction with context
	result, err := a.compactionService.CompactWithContext(ctx, conv, req.Manual, compCtx)
	if err != nil {
		a.logCompactionError("compaction_execution", err)
		return nil, compCtx, err
	}

	// Check for errors in the result. A summarization failure MUST be surfaced to
	// the caller as a real error so it visibly notifies the user and counts toward
	// the circuit breaker (compactionFailCount via handleCompactError), instead
	// of masquerading as a successful compaction.
	// Both callers already inspect result.Error, but returning it here guarantees
	// the failure is never swallowed regardless of caller shape.
	if result.Error != nil {
		a.logCompactionSummaryResult("", false, result.Error.Error())
		return result, compCtx, result.Error
	}
	result.FallbackUsed = false
	result.UsedProvider = snapshot.ProviderName
	result.UsedModel = snapshot.Model

	// === DEBUG: Log summary result ===
	a.logCompactionSummaryResult(result.Summary, true, "")

	// === DEBUG: Log file recovery ===
	a.logCompactionFileRecovery(result.RecoveredFiles)

	// If compaction was successful, append a new active generation to the same
	// durable conversation. The full transcript remains available to the TUI and
	// history tools; provider execution reads Conversation.ActiveMessages().
	if result.Compacted && result.Error == nil {
		compactedMessages := a.compactionService.BuildCompactedMessagesWithContext(result, compCtx, activeMessages...)
		if len(compactedMessages) == 0 {
			return nil, compCtx, fmt.Errorf("compaction produced no active messages")
		}
		postCompactBudget := int(float64(contextLimit) * compaction.AutoCompactThreshold)
		if snapshot.ThresholdTokens > 0 {
			postCompactBudget = min(postCompactBudget, snapshot.ThresholdTokens)
		}
		var restorationTrimmed bool
		compactedMessages, result.CompactedTokens, restorationTrimmed =
			compaction.FitCompactedMessagesToBudget(compactedMessages, postCompactBudget)
		if len(compactedMessages) == 0 {
			return nil, compCtx, fmt.Errorf("compaction could not fit a safe active context")
		}
		if restorationTrimmed {
			result.SizeWarning = fmt.Sprintf(
				"post-compaction recovery attachments trimmed to %d-token budget",
				postCompactBudget,
			)
		}
		result.CompactedMessages = compactedMessages

		handoffContent := ""
		handoffContent = compactedMessages[0].Content

		// A compaction commit is only safe at a provider-call boundary. Never
		// proceed after a timeout: that recreates the state fork this stable-ID
		// design is meant to eliminate.
		if a.sdk != nil && a.sdk.activeAgent() != nil {
			if stats := a.sdk.activeAgent().Stats(); stats.State != agent.StateIdle {
				return nil, compCtx, fmt.Errorf(
					"compaction unsafe while agent is %s; retry at the next turn boundary",
					stats.State,
				)
			}
		}

		durableCompacted, err := cloneGeneratedCompactionMessages(compactedMessages)
		if err != nil {
			return nil, compCtx, err
		}
		conv.AdvanceActiveContext(durableCompacted, result.Summary, result.CompactedTokens)
		a.compactionService.SnapshotToConversation(conv, compCtx)
		if err := a.sdk.saveConv(ctx, conv); err != nil {
			a.logCompactionError("save_conversation", err)
			result.Error = fmt.Errorf("failed to save compacted conversation: %w", err)
			result.Compacted = false
			return result, compCtx, result.Error
		}

		// Authoritatively reset the agent's live input-token counter to the
		// post-compaction estimate. Without this, the blocking guard/threshold
		// keeps evaluating the STALE pre-compaction total and immediately re-fires
		// compaction on the very next turn. Mirrors Crush resetting PromptTokens
		// after a summary. See /tmp/orch/CONTRACT.md (Agent.ResetContextTokens).
		if a.sdk != nil && a.sdk.activeAgent() != nil {
			a.sdk.activeAgent().ResetContextTokens(result.CompactedTokens)
		}

		// Compatibility field: compaction no longer changes identity.
		result.NewConvID = convID

		a.logCompactionNewConversation(convID, convID, len(compactedMessages), handoffContent)

		if path := a.sdk.GetConversationPath(convID); path != "" {
			a.sdk.SetConversationJSONPath(path)
		}

		// NOTE: the UI message list is deliberately NOT rebuilt here. This
		// function runs in tea.Cmd / background goroutines, and mutating
		// a.messages from here raced the render loop (lost writes, stale
		// indices). handleCompactCompleted rebuilds the view on the Update loop
		// from result.NewConvID.

		// Refresh the existing conversation's recap from the compacted history.
		// The SDK owns generation and pins the write to this exact conversation.
		go func(id string) {
			if err := a.sdk.RefreshConversationMetadata(context.Background(), id); err != nil {
				logDebug("[compaction] Failed to refresh conversation metadata for %s: %v", id, err)
			}
		}(convID)
	}

	return result, compCtx, nil
}

// compactionStageLabel converts a compaction.CompactionConfig.ProgressFunc
// stage identifier into a short human-readable label for the loading
// indicator. Falls back to the raw stage string (with underscores turned into
// spaces) for any stage this switch doesn't yet know about, so a new SDK-side
// stage never renders as a blank label.
func compactionStageLabel(stage string) string {
	switch {
	case stage == "micro_compaction":
		return "Trimming tool output"
	case stage == "summarizing":
		return "Summarizing conversation"
	case strings.HasPrefix(stage, "summarizing_chunk_"):
		parts := strings.TrimPrefix(stage, "summarizing_chunk_")
		return fmt.Sprintf("Summarizing (chunk %s)", strings.ReplaceAll(parts, "_of_", " of "))
	case stage == "verifying":
		return "Verifying summary"
	case stage == "recovering_files":
		return "Restoring files"
	case stage == "done":
		return "Finalizing"
	default:
		return strings.ReplaceAll(stage, "_", " ")
	}
}

// buildCompactionContext creates a CompactionContext from current app state.
// MessageCount is left at zero — performCompaction stamps it from the loaded
// conversation (the UI slice belongs to the Update loop and must not be read
// from the goroutines this runs in).
func (a *App) buildCompactionContext(req commands.CompactRequestMsg, convID string) *compaction.CompactionContext {
	compCtx := &compaction.CompactionContext{
		Strategy: req.Strategy,
		// Auto-compact suppresses follow-up questions so the model resumes
		// immediately without greeting or asking what to work on.
		// Manual /compact leaves the model free to respond naturally.
		// Mirrors Claude Code's suppressFollowUpQuestions parameter.
		SuppressFollowUpQuestions: !req.Manual,
	}

	// Get current operating mode
	if a.sdk != nil {
		compCtx.CurrentMode = a.sdk.GetOperatingMode()
		modeDefinition := a.sdk.GetOperatingModeDefinition()
		if modeDefinition != nil {
			compCtx.ModeName = modeDefinition.Name
		}
	}
	if compCtx.CurrentMode == "" {
		compCtx.CurrentMode = a.operatingMode
	}

	// Collect modified and read files from file access records
	// Note: compactionService maintains file access records
	if a.compactionService != nil {
		// Files are tracked in the compaction service's fileAccess map
		// The recoverFilesWithContext method will use the scoring
	}

	// Get MCP server names if available
	if a.sdk != nil && a.sdk.mcpManager != nil {
		servers := a.sdk.mcpManager.GetServers()
		for _, srv := range servers {
			if srv.Connected && srv.Config != nil {
				compCtx.ActiveMCPServers = append(compCtx.ActiveMCPServers, srv.Config.Name)
			}
		}
	}

	// Resolve and store the conversation JSON path so the compaction prompt
	// and micro-compactor pointer messages can reference it.
	if a.sdk != nil && convID != "" {
		if convPath := a.sdk.GetConversationPath(convID); convPath != "" {
			compCtx.ConversationJSONPath = convPath
			logDebug("[buildCompactionContext] Conversation JSON path: %s", convPath)
		}
	}

	// Carry plan mode state across the compaction boundary.
	// This ensures the agent's plan is never lost when context is trimmed.
	compCtx.RootSessionID = a.rootSessionID
	if a.inPlanMode {
		compCtx.PlanModeActive = true
		plan := a.planContent
		if plan == "" {
			// Fall back to reading the plan file if in-memory is empty.
			plan = a.readActivePlan()
		}
		compCtx.ActivePlan = plan
		logDebug("[buildCompactionContext] Plan mode active — plan length: %d bytes", len(plan))
	}

	// Preserve the agent's system prompt across compaction boundaries.
	// Stored in the snapshot so the post-compact handoff message can reference
	// it and so restores (via RestoreFromConversation) can re-populate it.
	if a.sdk != nil {
		if sp := a.sdk.SystemPrompt(); sp != "" {
			compCtx.SystemPrompt = sp
			logDebug("[buildCompactionContext] Captured system prompt: %d bytes", len(sp))
		}
	}

	// Collect todos from the global TodoManager
	todoManager := ii.GetTodoManager()
	if todoManager != nil {
		allTodos := todoManager.Todos()
		for _, todo := range allTodos {
			compTodo := compaction.Todo{
				Content:    todo.Content,
				Status:     string(todo.Status),
				ActiveForm: "", // ii.TodoItem doesn't have ActiveForm
				DependsOn:  todo.DependsOn,
			}
			if todo.Status == ii.TodoStatusCompleted {
				compCtx.CompletedTodos = append(compCtx.CompletedTodos, compTodo)
			} else {
				compCtx.ActiveTodos = append(compCtx.ActiveTodos, compTodo)
			}
		}
		logDebug("[buildCompactionContext] Collected %d active todos, %d completed todos",
			len(compCtx.ActiveTodos), len(compCtx.CompletedTodos))
	}

	return compCtx
}

// maxCompactionRetries is the circuit breaker limit — after this many consecutive
// failures, proactive compaction is disabled until a successful manual compact or
// a new conversation.
const maxCompactionRetries = 3

// summarizeForCompaction is the TUI's thin wrapper around
// compaction.SummarizeWithProvider that:
//
//  1. Uses the exact active provider stack captured before compaction starts.
//  2. Opens a per-attempt CompactionLog (~/.swarmos/logs/compaction.log) and
//     attaches it to the context as a provider.RawLogger so HTTP-level hooks
//     (e.g. Gemini) write to the same file.
//  3. Delegates the actual chat call to the SDK using a SummarizeHook that
//     mirrors the previous LogStart/LogRequest/LogResponse/LogError lifecycle.
//
// All compaction-prompt construction, system-prompt selection, and max-token
// capping live in the SDK.
func (a *App) summarizeForCompaction(
	ctx context.Context,
	messages []*conversation.Message,
	prov provider.Provider,
	providerName, modelName, prompt string,
) (string, error) {
	if a == nil || a.sdk == nil {
		return "", fmt.Errorf("SDK not initialized")
	}
	if prov == nil {
		return "", fmt.Errorf("provider is not initialized")
	}

	cl := openCompactionLog(providerName, modelName)
	// Attach so provider-level HTTP hooks also write here.
	ctx = cl.AttachToContext(ctx)

	hook := &compaction.SummarizeHook{
		OnStart: func(req compaction.SummarizeRequest) {
			cl.LogStart(len(req.Messages), len(req.ChatRequest.Messages[0].Content))
			if reqJSON, mErr := json.Marshal(req.ChatRequest); mErr == nil {
				cl.LogRequest(reqJSON)
			}
		},
		OnSuccess: func(summary string) {
			cl.LogResponse(summary)
		},
		OnError: func(err error) {
			cl.LogError(err, nil)
		},
	}

	summary, err := compaction.SummarizeWithProvider(ctx, prov, modelName, messages, prompt, a.sdk.logger, hook)
	if err != nil {
		// Wrap error with the provider/model pair so callers see the same
		// context the previous TUI implementation provided.
		return "", fmt.Errorf("failed to generate summary with %s/%s: %w", providerName, modelName, err)
	}
	return summary, nil
}

// renderMessageList renders a list of messages and returns the formatted lines.
// This is the SINGLE SOURCE OF TRUTH for message rendering - used by both the
// main chat view and conversation preview to ensure identical styling.
