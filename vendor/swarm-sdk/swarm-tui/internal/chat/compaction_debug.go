// Package chat provides compaction debug logging utilities
package chat

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/compaction"
)

// compactDebugLog logs a compaction debug message to both file log and debug screen
func (a *App) compactDebugLog(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	logDebug("[COMPACT] %s", msg)
	if a.debugScreen != nil {
		a.debugScreen.AddLog(fmt.Sprintf("[COMPACT] %s", msg))
	}
}

// compactDebugSection logs a section header for compaction debug
func (a *App) compactDebugSection(title string) {
	separator := strings.Repeat("─", 50)
	a.compactDebugLog("%s", separator)
	a.compactDebugLog("▶ %s", title)
	a.compactDebugLog("%s", separator)
}

// compactDebugKeyValue logs a key-value pair for compaction debug
func (a *App) compactDebugKeyValue(key string, value any) {
	a.compactDebugLog("  %-20s: %v", key, value)
}

// compactDebugList logs a list of items for compaction debug
func (a *App) compactDebugList(title string, items []string) {
	if len(items) == 0 {
		a.compactDebugLog("  %-20s: (none)", title)
		return
	}
	a.compactDebugLog("  %-20s:", title)
	for i, item := range items {
		a.compactDebugLog("    [%d] %s", i+1, item)
	}
}

// compactDebugFingerprint logs content provenance without persisting content.
func (a *App) compactDebugFingerprint(title string, content string) {
	if content == "" {
		a.compactDebugLog("  %-20s: (empty)", title)
		return
	}
	a.compactDebugLog("  %-20s: %d chars (~%d tokens)", title, len(content), len(content)/4)
	a.compactDebugKeyValue(title+" SHA-256", compactionPayloadSHA256([]byte(content)))
}

// logCompactionPreFlight logs pre-flight compaction information
func (a *App) logCompactionPreFlight(convID string, messageCount int, tokenCount int, strategy compaction.CompactionStrategy, model string) {
	a.compactDebugSection("COMPACTION PRE-FLIGHT")
	a.compactDebugKeyValue("Conversation ID", convID)
	a.compactDebugKeyValue("Message Count", messageCount)
	a.compactDebugKeyValue("Token Count", tokenCount)
	a.compactDebugKeyValue("Strategy", strategy)
	a.compactDebugKeyValue("Compaction Model", model)
}

// logCompactionContext logs the compaction context being used
func (a *App) logCompactionContext(compCtx *compaction.CompactionContext) {
	a.compactDebugSection("COMPACTION CONTEXT")
	a.compactDebugKeyValue("Current Mode", compCtx.CurrentMode)
	a.compactDebugKeyValue("Mode Name", compCtx.ModeName)
	a.compactDebugKeyValue("Message Count", compCtx.MessageCount)
	a.compactDebugKeyValue("Strategy", compCtx.Strategy)

	// MCP servers
	if len(compCtx.ActiveMCPServers) > 0 {
		a.compactDebugList("Active MCP Servers", compCtx.ActiveMCPServers)
	} else {
		a.compactDebugKeyValue("Active MCP Servers", "(none)")
	}

	// Todos
	a.compactDebugKeyValue("Active Todos", len(compCtx.ActiveTodos))
	a.compactDebugKeyValue("Completed Todos", len(compCtx.CompletedTodos))

	// Files
	if len(compCtx.ModifiedFiles) > 0 {
		a.compactDebugList("Modified Files", compCtx.ModifiedFiles)
	}
	if len(compCtx.ReadFiles) > 0 {
		a.compactDebugList("Read Files", compCtx.ReadFiles)
	}
}

// logCompactionLLMCall logs the LLM call for summarization
func (a *App) logCompactionLLMCall(model string, inputTokens int) {
	a.compactDebugSection("LLM SUMMARIZATION")
	a.compactDebugKeyValue("Model", model)
	a.compactDebugKeyValue("Input Tokens (est)", inputTokens)
	a.compactDebugLog("  ⏳ Calling LLM for summary generation...")
}

// logCompactionSummaryResult logs the summary result from LLM
func (a *App) logCompactionSummaryResult(summary string, success bool, errMsg string) {
	if !success {
		category, digest := compactionErrorMetadata(errMsg)
		a.compactDebugLog("  ❌ LLM Summary Failed: category=%s sha256=%s", category, digest)
		return
	}

	a.compactDebugLog("  ✓ LLM Summary Received")
	a.compactDebugFingerprint("Summary Content", summary)
}

// logCompactionFileRecovery logs file recovery details
func (a *App) logCompactionFileRecovery(files []compaction.RecoveredFile) {
	a.compactDebugSection("FILE RECOVERY")
	a.compactDebugKeyValue("Files Recovered", len(files))

	if len(files) == 0 {
		a.compactDebugLog("  (no files recovered)")
		return
	}

	totalTokens := 0
	for i, f := range files {
		truncatedNote := ""
		if f.Truncated {
			truncatedNote = " [TRUNCATED]"
		}
		a.compactDebugLog("  [%d] %s (%d tokens)%s", i+1, f.Path, f.Tokens, truncatedNote)
		totalTokens += f.Tokens
	}
	a.compactDebugKeyValue("Total File Tokens", totalTokens)
}

// logCompactionNewConversation logs new conversation creation details
func (a *App) logCompactionNewConversation(oldConvID string, newConvID string, compactedMessages int, handoffContent string) {
	a.compactDebugSection("NEW CONVERSATION CREATED")
	a.compactDebugKeyValue("Old Conversation", oldConvID)
	a.compactDebugKeyValue("New Conversation", newConvID)
	a.compactDebugKeyValue("Compacted Messages", compactedMessages)

	// Show the handoff document structure
	a.compactDebugLog("")
	a.compactDebugLog("  📄 HANDOFF DOCUMENT:")
	a.compactDebugFingerprint("Content", handoffContent)
}

// logCompactionComplete logs the final compaction summary
func (a *App) logCompactionComplete(originalTokens int, compactedTokens int, strategy compaction.CompactionStrategy, mode string, fileCount int, todoCount int) {
	a.compactDebugSection("COMPACTION COMPLETE")

	reduction := 0
	if originalTokens > 0 {
		reduction = 100 - (compactedTokens * 100 / originalTokens)
	}

	a.compactDebugKeyValue("Original Tokens", originalTokens)
	a.compactDebugKeyValue("Compacted Tokens", compactedTokens)
	a.compactDebugKeyValue("Reduction", fmt.Sprintf("%d%%", reduction))
	a.compactDebugKeyValue("Strategy", strategy)
	a.compactDebugKeyValue("Mode Preserved", mode)
	a.compactDebugKeyValue("Files Recovered", fileCount)
	a.compactDebugKeyValue("Todos Preserved", todoCount)

	a.compactDebugLog("")
	a.compactDebugLog("  ✅ Compaction successful! New conversation ready.")
	a.compactDebugLog("%s", strings.Repeat("═", 50))
}

// logCompactionError logs a compaction error
func (a *App) logCompactionError(stage string, err error) {
	a.compactDebugSection("COMPACTION ERROR")
	a.compactDebugKeyValue("Stage", stage)
	a.compactDebugKeyValue("Error", err.Error())
	a.compactDebugLog("  ❌ Compaction failed at stage: %s", stage)
}
