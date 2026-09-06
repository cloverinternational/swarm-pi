package chat

import (
	"fmt"
	"time"
)

// This file owns the Update-loop reaction to IN-LOOP (SDK-side) auto
// compaction. Since wireSharedAgentCompactFunc routes the agent's CompactFunc
// through the App's shared compaction service, the %-threshold path runs the
// same pipeline as manual /compact; these handlers give it the same UI
// treatment (determinate progress bar, notifications, circuit breaker).
//
// All three handlers run on the Update loop via the queue drain in
// app_update.go (sendToRuntime → updateQueue), so mutating App state here is
// safe.

// handleAutoCompactionStarted reacts to agent.CompactionNeededUpdate: the SDK
// crossed the auto-compaction threshold mid-turn and is about to compact.
func (a *App) handleAutoCompactionStarted(msg agentAutoCompactionStartedMsg) {
	pct := 0
	if msg.contextLimit > 0 {
		pct = msg.currentTokens * 100 / msg.contextLimit
	}
	logDebug("[AutoCompaction] started: tokens=%d threshold=%d limit=%d (%d%%) conv=%s",
		msg.currentTokens, msg.threshold, msg.contextLimit, pct, msg.conversationID)

	if !a.isCompacting {
		// Mirror handleCompactRequest: remember the user's chosen indicator
		// style so it can be restored when compaction finishes.
		a.preCompactIndicatorType = a.loadingIndicator.GetIndicatorType()
	}
	a.isCompacting = true
	a.lastCompactionAttempt = time.Now()
	// The plain spinner branch in app_chat_render.go would shadow the
	// determinate bar; stop it for the duration of compaction. Stream
	// finalization (chat_state.go) restores normal indicator state.
	a.spinner.Stop(a.animationClock)
	a.loadingIndicator.SetIndicatorType(LoadingBar)
	a.loadingIndicator.ClearProgress()
	a.loadingIndicator.SetText(fmt.Sprintf("Auto-compacting context (%d%% used)…", pct))
	if !a.loadingIndicator.IsActive() {
		a.loadingIndicator.Start(a.animationClock)
	}
	a.addNotification("info", fmt.Sprintf(
		"Context reached %d%% (%d tokens) — auto-compacting…", pct, msg.currentTokens))
}

// handleAutoCompactionDone reacts to agent.CompactionDoneUpdate.
func (a *App) handleAutoCompactionDone(msg agentAutoCompactionDoneMsg) {
	logDebug("[AutoCompaction] done: %d → %d tokens conv=%s",
		msg.tokensBefore, msg.tokensAfter, msg.conversationID)
	a.isCompacting = false
	a.loadingIndicator.ClearProgress()
	a.loadingIndicator.SetIndicatorType(a.preCompactIndicatorType)
	// Success closes the circuit breaker (mirrors handleCompactCompleted).
	a.compactionFailCount = 0
	a.compactionBlocked = false
	if msg.tokensAfter > 0 {
		a.tokenCount = msg.tokensAfter
		a.tokenCountIsEstimate = false
		a.lastRealTokenCount = msg.tokensAfter
		a.sidePanelCache.valid = false
	}
	// NOTE: the transcript is deliberately NOT rebuilt here. In-loop
	// compaction happens mid-turn while streaming state owns a.messages; the
	// durable conversation was already persisted by persistInLoopCompaction,
	// and the next conversation load surfaces the compaction handoff via
	// loadMessagesFromSDK. Rebuilding now would clobber the in-progress
	// assistant message.
	a.addNotification("success", fmt.Sprintf(
		"Context compacted: %d → %d tokens", msg.tokensBefore, msg.tokensAfter))
}

// handleAutoCompactionFailed reacts to agent.CompactionFailedUpdate. Counts
// toward the same circuit breaker as manual compaction failures; after
// maxCompactionRetries consecutive failures, in-loop auto-compaction is
// disabled on the live agent until a successful compaction or a settings /
// model reload re-wires it.
func (a *App) handleAutoCompactionFailed(msg agentAutoCompactionFailedMsg) {
	logDebug("[AutoCompaction] failed (fatal=%v): %s", msg.fatal, msg.errMsg)
	a.isCompacting = false
	a.loadingIndicator.ClearProgress()
	a.loadingIndicator.SetIndicatorType(a.preCompactIndicatorType)
	a.compactionFailCount++
	a.lastCompactionAttempt = time.Now()
	if msg.fatal {
		// The turn is aborting with ContextPressureError; block sends into the
		// known-unsafe context until a compaction succeeds (app_messaging.go).
		a.compactionBlocked = true
	}
	if a.compactionFailCount >= maxCompactionRetries {
		// Open the circuit breaker: stop in-loop retries. A successful manual
		// /compact (handleCompactCompleted → syncAutoCompactionConfig) or a
		// settings/model reload re-enables auto-compaction.
		if a.sdk != nil && a.sdk.activeAgent() != nil {
			cfg := a.sdk.activeAgent().AutoCompactionConfig()
			cfg.EnableAutoCompaction = false
			a.sdk.activeAgent().SetAutoCompactionConfig(cfg)
		}
		a.addNotification("warning", fmt.Sprintf(
			"Auto-compaction disabled after %d failures — run /compact manually (last error: %s)",
			a.compactionFailCount, msg.errMsg))
		return
	}
	a.addNotification("warning", fmt.Sprintf("Auto-compaction failed: %s", msg.errMsg))
}
