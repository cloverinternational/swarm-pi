package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/contextaudit"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/mode"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// Helper functions for mode switching and system prompt handling

// getAbsoluteWorkingDir returns the absolute working directory
func getAbsoluteWorkingDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return cwd
	}
	return absPath
}

// appendModeMessage appends a mode system message to history (never replaces).
// This is used when a message is sent to record the active mode at send time.
// Unlike addOrReplaceModeMessage, this preserves earlier messages for cache stability.
func (a *App) appendModeMessage(modeUpper string, modeObj *mode.OperatingMode) {
	absPath := getAbsoluteWorkingDir()
	var content string
	if modeObj != nil && modeObj.SystemInstruction != "" {
		content = fmt.Sprintf("**Mode: %s**\n\nWorking Directory: %s\n\n%s", modeUpper, absPath, modeObj.SystemInstruction)
	} else {
		content = fmt.Sprintf("**Mode: %s**\n\nWorking Directory: %s\n\nNo filtering active. All tools available.", modeUpper, absPath)
	}

	systemMsg := Message{
		Role:      "system",
		Content:   content,
		Timestamp: time.Now(),
	}

	// ALWAYS append, never replace - this preserves cache prefix stability
	a.messages = append(a.messages, systemMsg)
	a.invalidateViewportCache()
	a.updateViewportContent()
}

// appendModeMessageIfChanged appends a mode message only if the mode changed
// since the last message was sent. This is called before sending a new message.
// Returns true if a mode message was appended.
func (a *App) appendModeMessageIfChanged() bool {
	// Skip if mode hasn't changed since last send
	if a.operatingMode == a.lastCommittedMode {
		return false
	}

	// Get mode definition
	modeObj := mode.GetBuiltinMode(a.operatingMode)
	modeUpper := strings.ToUpper(a.operatingMode)
	if modeUpper == "" {
		modeUpper = "OFF"
	}

	// Append mode message
	a.appendModeMessage(modeUpper, modeObj)

	// Update last committed mode
	a.lastCommittedMode = a.operatingMode

	logDebug("[MODE] Appended mode message for %s (changed from %s)", a.operatingMode, a.lastCommittedMode)
	return true
}

// addSystemPromptToHistory adds the base system prompt to the message history
// This ensures the system prompt is visible in the chat context
// Now builds decomposed sections for better rendering control
func (a *App) addSystemPromptToHistory() {
	if a.sdk == nil {
		return
	}

	// Show the prompt as it is ACTUALLY sent to the provider, including the
	// send-time OAuth tricks (e.g. the Anthropic "You are Claude Code" prefix
	// the provider prepends inside Chat()). The agent's stored prompt does not
	// carry those, so we mirror them here for a faithful raw view.
	rawPrompt := a.rawSystemPromptForDisplay()
	if rawPrompt == "" {
		return
	}

	// Decompose into sections: the base prompt stays RAW/verbatim; the
	// injected extras (<available_skills>, any <context name="…"> blocks) are
	// split out so each renders as its own coloured, char-counted section.
	var sections []SystemMessageSection
	priority := 0
	addSection := func(s SystemMessageSection) {
		if strings.TrimSpace(s.Content) == "" {
			return
		}
		s.LineCount = strings.Count(s.Content, "\n") + 1
		if s.CharCount == 0 {
			s.CharCount = len(s.Content)
		}
		s.Collapsed = true
		s.Priority = priority
		priority++
		sections = append(sections, s)
	}

	// Base prompt + any injected <context> blocks. <available_skills> is handled
	// separately below (sourced live) so it always shows even before the
	// execution-time skills injection has run.
	for _, s := range contextaudit.SplitSystemPrompt(rawPrompt) {
		if s.Label == "available_skills" {
			continue
		}
		secType, title, icon := systemSectionMeta(s.Label)
		addSection(SystemMessageSection{
			ID:      "sysprompt-" + strings.ReplaceAll(s.Label, ":", "-"),
			Type:    secType,
			Title:   title,
			Icon:    icon,
			Content: s.Content,
		})
	}

	// Available skills — sourced live from the skills manager so the section is
	// present regardless of when the prompt injection happens. Hidden when the
	// "skills" injection source is disabled in the Context settings, since the
	// block is then never sent to the provider.
	if a.sdk.injectionEnabled(chatcontext.SourceIDSkills) {
		addSection(SystemMessageSection{
			ID:      "skills",
			Type:    SectionSkills,
			Title:   i18n.T("classic_chat_2.context.available_skills"),
			Icon:    "✦",
			Content: a.sdk.GetSkillsPromptContext(),
		})
	}

	// Tool schemas — these ship in the request's Tools field, not the system
	// prompt, but they are a major slice of context, so surface them as their
	// own section with a per-tool character breakdown. CharCount is the total
	// schema size (the real context cost), not the length of the summary text.
	if toolList := a.sdk.getToolsForRequest(); len(toolList) > 0 {
		content, totalChars := buildToolSectionContent(toolList)
		addSection(SystemMessageSection{
			ID:        "tool-schemas",
			Type:      SectionTools,
			Title:     i18n.T("classic_chat_2.context.tool_schemas", len(toolList)),
			Icon:      "⚙",
			Content:   content,
			CharCount: totalChars,
		})
	}

	// Git Context — sourced from the TUI's git helper, separate from the prompt.
	// Mirrors the "git_status" context source: when that source is disabled the
	// request carries no git context, so the panel must not show it either.
	gitContent := ""
	if a.sdk.injectionEnabled(chatcontext.SourceIDGitStatus) {
		if gitContext := a.buildGitContextString(); gitContext != "" {
			gitContent = strings.TrimPrefix(gitContext, "## Git Context\n")
			addSection(SystemMessageSection{
				ID:      "git-context",
				Type:    SectionGitContext,
				Title:   i18n.T("classic_chat_2.context.git"),
				Icon:    "󰊢",
				Content: gitContent,
			})
		}
	}

	// Backward-compat Content: the full raw prompt (then git), kept verbatim so
	// any Content consumer and the dedup check below see the real text.
	fullContent := rawPrompt
	if gitContent != "" {
		fullContent += "\n\n## Git Context\n" + gitContent
	}

	// Dedup: skip if a system message with this exact prompt is already present.
	for _, msg := range a.messages {
		if msg.Role == "system" && strings.HasPrefix(msg.Content, rawPrompt) {
			return
		}
	}

	// Add system prompt at the beginning of the message list
	systemMsg := Message{
		Role:      "system",
		Content:   fullContent, // Backward compatibility - full raw prompt
		Sections:  sections,    // Decomposed sections for rendering
		Timestamp: time.Now(),
	}

	// Prepend to messages
	a.messages = append([]Message{systemMsg}, a.messages...)
	a.invalidateViewportCache()
}

// rawSystemPromptForDisplay returns the system prompt as it is actually sent to
// the provider. It mirrors the send-time transforms the SDK applies that are
// absent from the agent's stored prompt — today that is the Anthropic OAuth
// prefix the provider prepends inside Chat(). Kept in lockstep with the
// provider so the raw view shows the real bytes, "tricks" included.
func (a *App) rawSystemPromptForDisplay() string {
	if a.sdk == nil {
		return ""
	}
	sp := a.sdk.SystemPrompt()
	if sp == "" {
		return ""
	}
	if a.sdk.IsOAuth() && provider.NormalizeProviderName(a.sdk.GetProviderName()) == "anthropic" {
		prefix := anthropic.GetCLISystemPromptPrefix()
		if !strings.HasPrefix(sp, prefix) {
			sp = prefix + "\n\n" + sp
		}
	}
	return sp
}

// buildToolSectionContent renders a per-tool character breakdown (largest
// first, matching the context-probe/ctx-breakdown output) and returns it along
// with the total tool-schema character count.
func buildToolSectionContent(toolList []provider.Tool) (string, int) {
	comps := contextaudit.ToolComponents(toolList)
	maxName := 0
	for _, c := range comps {
		if len(c.Label) > maxName {
			maxName = len(c.Label)
		}
	}
	var b strings.Builder
	total := 0
	for _, c := range comps {
		// Left-align the name to the widest name, right-align the count, so the
		// columns line up. rawWrap preserves this padding at render time.
		fmt.Fprintf(&b, "%-*s   %s\n", maxName, c.Label, i18n.T("classic_chat_2.context.chars", commaInt(c.Bytes)))
		total += c.Bytes
	}
	return strings.TrimRight(b.String(), "\n"), total
}

// systemSectionMeta maps a contextaudit split-label to the section's display
// type, title, and icon.
func systemSectionMeta(label string) (SectionType, string, string) {
	switch {
	case label == "base_prompt":
		return SectionBasePrompt, i18n.T("classic_chat_2.context.system_instructions"), "◆"
	case label == "available_skills":
		return SectionSkills, i18n.T("classic_chat_2.context.available_skills"), "✦"
	case strings.HasPrefix(label, "context:"):
		return SectionContext, i18n.T("classic_chat_2.context.named", strings.TrimPrefix(label, "context:")), "▤"
	default:
		return SectionBasePrompt, label, "◆"
	}
}

// buildGitContextString builds a git context string for the system prompt
// This provides the agent with awareness of the current git state
func (a *App) buildGitContextString() string {
	if a.gitHelper == nil || !a.gitHelper.IsRepo() {
		return ""
	}

	var parts []string
	parts = append(parts, "## Git Context")

	// Current branch
	currentBranch, err := a.gitHelper.CurrentBranch()
	if err == nil && currentBranch != "" {
		parts = append(parts, fmt.Sprintf("Current branch: `%s`", currentBranch))
	}

	// Conversation's associated branch (if different)
	if a.activeConv != nil && a.activeConv.Branch != "" {
		if a.activeConv.Branch != currentBranch {
			parts = append(parts, fmt.Sprintf("Conversation branch: `%s` (different from current)", a.activeConv.Branch))
		} else {
			parts = append(parts, fmt.Sprintf("Conversation branch: `%s`", a.activeConv.Branch))
		}
	}

	// Uncommitted changes warning
	if hasChanges, _ := a.gitHelper.HasUncommittedChanges(); hasChanges {
		parts = append(parts, "**Warning:** There are uncommitted changes in the working directory.")
	}

	// Recent branches for context (up to 5)
	recentBranches, err := a.gitHelper.RecentBranches(5)
	if err == nil && len(recentBranches) > 0 {
		branchList := make([]string, 0, len(recentBranches))
		for _, b := range recentBranches {
			age := a.gitHelper.FormatBranchAge(b.LastCommitTime)
			branchList = append(branchList, fmt.Sprintf("  - `%s` (%s)", b.Name, age))
		}
		parts = append(parts, "\nRecent branches:")
		parts = append(parts, branchList...)
	}

	// Instructions for the agent
	parts = append(parts, "\n### Branch Workflow")
	parts = append(parts, "- Each conversation is associated with a git branch")
	parts = append(parts, "- When working on features, create commits on the associated branch")
	parts = append(parts, "- If you need to switch branches, warn the user about uncommitted changes first")

	return strings.Join(parts, "\n")
}
