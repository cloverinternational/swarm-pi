package chat

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

var imperativeVerbRE = regexp.MustCompile(`^(make|add|create|build|fix|update|change|modify|delete|remove|refactor|implement|write)\s+`)

// hasExistingTitle checks if a conversation already has a meaningful title set.
// Returns true if the conversation has a non-empty title in either the Title field
// or the custom_title metadata field, and the title is NOT the default "New Chat" placeholder.
func hasExistingTitle(conv *conversation.Conversation) bool {
	if conv == nil {
		return false
	}

	// Check Title field first (primary location) — skip the default "New Chat" placeholder
	if conv.Title != "" && conv.Title != "New Chat" {
		return true
	}

	// Check metadata for title
	if conv.Metadata.Custom != nil {
		if title, ok := conv.Metadata.Custom["title"].(string); ok && title != "" && title != "New Chat" {
			return true
		}
	}

	// Check metadata for custom_title
	if conv.Metadata.Custom != nil {
		if customTitle, ok := conv.Metadata.Custom["custom_title"]; ok {
			if titleStr, ok := customTitle.(string); ok && titleStr != "" && titleStr != "New Chat" {
				return true
			}
		}
	}

	return false
}

// cleanGeneratedTitle normalizes raw LLM output into a tight, single-line title.
// LLMs frequently return the title wrapped in quotes, prefixed with "Title:",
// or padded with a leading explanation line. Without this, those artifacts leak
// straight into the conversation sidebar and make titles look broken.
func cleanGeneratedTitle(raw string) string {
	title := strings.TrimSpace(raw)
	if title == "" {
		return ""
	}

	// Take only the first non-empty line — models sometimes add a second line
	// of commentary ("This title captures ...").
	for _, line := range strings.Split(title, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			title = s
			break
		}
	}

	// Strip a leading "Title:"/"Chat title:" style label (case-insensitive).
	if idx := strings.Index(title, ":"); idx >= 0 && idx <= 12 {
		label := strings.ToLower(strings.TrimSpace(title[:idx]))
		if label == "title" || label == "chat title" || label == "conversation title" {
			title = strings.TrimSpace(title[idx+1:])
		}
	}

	// Strip surrounding matching quotes (straight or smart) and backticks.
	title = strings.TrimSpace(title)
	for len(title) >= 2 {
		first, last := title[0], title[len(title)-1]
		if (first == '"' && last == '"') ||
			(first == '\'' && last == '\'') ||
			(first == '`' && last == '`') {
			title = strings.TrimSpace(title[1 : len(title)-1])
			continue
		}
		break
	}
	title = strings.Trim(title, "“”‘’")

	// Drop a trailing period (titles are labels, not sentences).
	title = strings.TrimSpace(strings.TrimSuffix(title, "."))

	return strings.TrimSpace(title)
}

// reindexConversationTitles generates titles for the last N conversations that don't have titles.
// This is useful for batch processing conversations that were created before the title generation feature.
// Pass count=0 to process ALL untitled conversations.
// Returns the number of conversations that were processed.
// maxReindexBatch caps how many conversations a single /reindex run processes,
// so a live backfill never fans out into a huge number of LLM calls.
const maxReindexBatch = 10

// reindexConversationTitles backfills titles + LLM recaps for conversations
// that lack them. Driven by the /reindex slash command.
//   - scopeAll=true  -> consider conversations across all workspaces.
//   - scopeAll=false -> only conversations in the CURRENT workspace ("your folder").
//
// Regardless of scope it processes at most maxReindexBatch (10) conversations,
// most-recent first. Returns the number of conversations that were processed.
func (a *App) reindexConversationTitles(ctx context.Context, scopeAll bool) int {
	if a.sdk == nil {
		logDebug("[reindexConversationTitles] SDK not available")
		return 0
	}

	// Scope: current workspace unless the user asked for "all".
	workspace := ""
	if !scopeAll {
		workspace = a.sdk.ProjectRoot()
	}

	convs, err := a.sdk.ListConversations(ctx, workspace)
	if err != nil {
		logDebug("[reindexConversationTitles] Failed to list conversations: %v", err)
		return 0
	}

	// Sort by updated time (most recent first)
	sort.Slice(convs, func(i, j int) bool {
		return convs[i].UpdatedAt.After(convs[j].UpdatedAt)
	})

	processed := 0
	for _, conv := range convs {
		// Hard cap: never process more than maxReindexBatch (10) per run.
		if processed >= maxReindexBatch {
			break
		}

		didWork := false

		// A conversation needs work if it lacks a title or recap, OR if it has
		// both but they are POISONED — stale metadata generated before the
		// summarizer input was sanitized (e.g. "Add Google Font to Next.js" or a
		// recap mentioning runtime guidance / an available skill). Without the
		// poison check /reindex would skip these forever and the history menu
		// would keep showing the injected-context artifacts.
		needsWork := !hasExistingTitle(conv) || !hasExistingRecap(conv)
		if !needsWork {
			recap := ""
			if conv.Summary != nil {
				recap = conv.Summary.Recap
			}
			if conversationTitleOrRecapLooksPoisoned(conv.Title, recap) {
				needsWork = true
			}
		}

		if needsWork {
			logDebug("[reindexConversationTitles] Refreshing title/recap for conv %s", conv.ID)
			if err := a.sdk.RefreshConversationMetadata(ctx, conv.ID); err != nil {
				logDebug("[reindexConversationTitles] Failed for conv %s: %v", conv.ID, err)
			}
			didWork = true
		}

		if didWork {
			processed++
		}
	}

	return processed
}

// hasExistingRecap reports whether a conversation already has a persisted
// LLM recap in ConversationSummary.Recap. Guards the nil Summary on legacy
// (metadata-only or pre-recap) conversations.
func hasExistingRecap(conv *conversation.Conversation) bool {
	return conv != nil && conv.Summary != nil && strings.TrimSpace(conv.Summary.Recap) != ""
}

// conversationTitleOrRecapLooksPoisoned mirrors the SDK-side detection
// (client.conversationMetadataLooksPoisoned, which is unexported) so /reindex can
// recognise conversations whose stored title/recap contains injected-context
// artifacts and re-process them. Keep the marker list consistent with the SDK.
func conversationTitleOrRecapLooksPoisoned(title string, recap string) bool {
	haystack := strings.ToLower(title + "\n" + recap)
	markers := []string{
		"runtime guidance",
		"available skill",
		"add-google-font",
		"google font",
		"next.js app router page",
		"swarm_runtime",
		"system prompt",
		"these instructions",
	}
	for _, marker := range markers {
		if strings.Contains(haystack, marker) {
			return true
		}
	}
	return false
}

// analyzeMessageForCodeIntent examines a message to determine if it suggests code changes.
// Returns true if the message appears to be requesting implementation/modification work.
func analyzeMessageForCodeIntent(message string) bool {
	if message == "" {
		return false
	}

	msgLower := strings.ToLower(message)

	// Keywords that strongly suggest code work
	codeActionKeywords := []string{
		"implement", "add", "create", "build", "develop", "write",
		"fix", "bug", "debug", "solve", "repair", "patch",
		"refactor", "optimize", "improve", "enhance", "update",
		"modify", "change", "edit", "delete", "remove",
		"integrate", "connect", "setup", "configure",
		"migrate", "upgrade", "convert", "port",
		"test", "unit test", "integration test",
	}

	// Check for action keywords
	for _, keyword := range codeActionKeywords {
		if strings.Contains(msgLower, keyword) {
			return true
		}
	}

	// Check for imperative verbs at start (commands)
	if imperativeVerbRE.MatchString(msgLower) {
		return true
	}

	// Keywords that suggest questions/explanations (not code work)
	questionKeywords := []string{
		"what is", "how does", "why", "explain", "tell me", "show me",
		"can you explain", "help me understand", "what's the difference",
		"how do i learn", "tutorial", "documentation",
	}

	for _, keyword := range questionKeywords {
		if strings.Contains(msgLower, keyword) {
			return false
		}
	}

	// Check for file/code references (suggests working with actual code)
	codeIndicators := []string{
		".go", ".py", ".js", ".ts", ".java", ".cpp", ".c", ".rs",
		"function", "class", "method", "variable", "struct",
		"api", "endpoint", "route", "handler",
		"database", "query", "schema", "table",
	}

	for _, indicator := range codeIndicators {
		if strings.Contains(msgLower, indicator) {
			return true
		}
	}

	// If message is very short and question-like, probably not code work
	if len(message) < 50 && (strings.HasSuffix(message, "?") || strings.Contains(msgLower, "?")) {
		return false
	}

	// Default to false for ambiguous cases (don't annoy user)
	return false
}

// smartAskBranchCreationAsync intelligently asks about branch creation only when
// the conversation appears to involve code changes. Continuously monitors all messages.
func (a *App) smartAskBranchCreationAsync() {
	// Skip if git is not available
	if a.gitHelper == nil || !a.gitHelper.IsRepo() {
		logDebug("[smartAskBranchCreationAsync] Git not available, skipping branch question")
		return
	}

	// Continuously monitor conversation for code-related work
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.After(10 * time.Minute) // Monitor for up to 10 minutes
	lastCheckedMsgCount := 0

	for {
		select {
		case <-timeout:
			logDebug("[smartAskBranchCreationAsync] Monitoring timeout reached")
			return
		case <-ticker.C:
			// Check if we've already prompted
			if a.branchPromptAsked {
				logDebug("[smartAskBranchCreationAsync] Already asked about branch, stopping monitor")
				return
			}

			if a.sdk == nil || a.currentConvID == "" {
				continue
			}

			ctx := context.Background()
			conv := a.sdk.GetConversation(ctx, a.currentConvID)
			if conv == nil || len(conv.Messages) == 0 {
				continue
			}

			// Check if there are new messages to analyze
			currentMsgCount := len(conv.Messages)
			if currentMsgCount <= lastCheckedMsgCount {
				continue
			}

			// Analyze new user messages for code intent
			foundCodeWork := false
			var triggerMessage string

			for _, msg := range conv.Messages {
				if msg.Role == conversation.RoleUser {
					if analyzeMessageForCodeIntent(msg.Content) {
						foundCodeWork = true
						triggerMessage = msg.Content
						truncLen := 100
						if len(msg.Content) < truncLen {
							truncLen = len(msg.Content)
						}
						logDebug("[smartAskBranchCreationAsync] Detected code work in message: %q", msg.Content[:truncLen])
						break
					}
				}
			}

			lastCheckedMsgCount = currentMsgCount

			if foundCodeWork {
				// Trigger branch creation prompt
				logDebug("[smartAskBranchCreationAsync] Code work detected, prompting for branch creation")
				a.promptForBranchCreation(triggerMessage)
				return
			}
		}
	}
}

// promptForBranchCreation handles the actual branch creation prompt and logic.
func (a *App) promptForBranchCreation(triggerMessage string) {
	// Mark that we've asked (do this first to prevent duplicate prompts)
	a.branchPromptAsked = true

	// Generate suggested branch name
	suggestedBranch := SuggestBranchName(triggerMessage)
	logDebug("[promptForBranchCreation] Suggesting branch: %s", suggestedBranch)

	if a.sdk == nil {
		return
	}

	ctx := context.Background()

	// Get the ask_user_question tool from registry
	questionTool, err := a.sdk.toolRegistry.Get("ask_user_question")
	if err != nil {
		logDebug("[promptForBranchCreation] ask_user_question tool not found: %v", err)
		return
	}

	// Ask if user wants to create a branch
	confirmResult, err := questionTool.Execute(ctx, map[string]interface{}{
		"question": "Create a git branch for this conversation?",
		"type":     "confirm",
		"title":    "Branch Creation",
		"default":  "no",
		"timeout":  60, // 60 second timeout
	})

	if err != nil {
		logDebug("[promptForBranchCreation] Failed to ask branch question: %v", err)
		return
	}

	// Parse confirmation
	createBranch := strings.ToLower(strings.TrimSpace(confirmResult.Output)) == "yes"
	if !createBranch {
		logDebug("[promptForBranchCreation] User declined branch creation")
		return
	}

	// Ask for branch name with suggestion
	nameResult, err := questionTool.Execute(ctx, map[string]interface{}{
		"question": "Branch name:",
		"type":     "text",
		"default":  suggestedBranch,
		"title":    "Create Branch",
		"timeout":  60,
	})

	if err != nil {
		logDebug("[promptForBranchCreation] Failed to ask branch name: %v", err)
		return
	}

	branchName := strings.TrimSpace(nameResult.Output)
	if branchName == "" {
		branchName = suggestedBranch
	}

	// Create the branch
	err = a.gitHelper.CreateAndCheckout(branchName)
	if err != nil {
		logDebug("[promptForBranchCreation] Failed to create branch %s: %v", branchName, err)
		a.addNotification("error", fmt.Sprintf("Failed to create branch: %v", err))
		return
	}

	logDebug("[promptForBranchCreation] Created and checked out branch: %s", branchName)
	a.addNotification("success", fmt.Sprintf("Created branch: %s", branchName))

	// Update conversation branch in memory and SDK metadata
	if a.activeConv != nil {
		a.activeConv.Branch = branchName

		// Also update in conversations list
		for i := range a.conversations {
			if a.conversations[i].ID == a.activeConv.ID {
				a.conversations[i].Branch = branchName
				break
			}
		}

		// Update SDK metadata so branch persists across reloads
		if a.sdk != nil && a.currentConvID != "" {
			ctx := context.Background()
			conv := a.sdk.GetConversation(ctx, a.currentConvID)
			if conv != nil {
				if conv.Metadata.Custom == nil {
					conv.Metadata.Custom = make(map[string]interface{})
				}
				conv.Metadata.Custom["git_branch"] = branchName
				if err := a.sdk.UpdateConversationMetadata(ctx, a.currentConvID, &conv.Metadata); err != nil {
					logDebug("[promptForBranchCreation] Failed to update SDK metadata with branch: %v", err)
				}
			}
		}
	}
}

// openConversation opens an existing conversation and loads its state
