// Package chat provides a chat-focused terminal UI
package chat

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/settings"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/debuglog"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/termimage"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/update"
)

func tr(messageID string, args ...any) string {
	return i18n.T(messageID, args...)
}

// logDebug delegates to the shared debuglog helper. It is a no-op unless a sink
// is installed via debuglog.SetLogger, preserving the prior behavior of never
// unconditionally writing a debug log to the current working directory.
func logDebug(format string, args ...any) {
	debuglog.Logf(format, args...)
}

func hookEntryToConfig(entry settings.HookEntry, workspaceRoot string) (*hooks.ShellHookConfig, error) {
	if strings.TrimSpace(entry.Name) == "" {
		return nil, fmt.Errorf("hook name is required")
	}

	action := entry.Action
	if action == "" {
		action = "block_exit2"
	}

	timeout := entry.Timeout
	if timeout == "" {
		timeout = "60s"
	}

	workingDir := entry.WorkingDir
	if workingDir == "" && workspaceRoot != "" {
		if resolved, err := SanitizeHookWorkingDir("", workspaceRoot); err == nil {
			workingDir = resolved
		}
	}

	return &hooks.ShellHookConfig{
		Name:             entry.Name,
		Description:      entry.Description,
		EventPatterns:    entry.Events,
		Command:          entry.Command,
		ToolMatcher:      entry.ToolMatcher,
		Priority:         entry.Priority,
		Timeout:          timeout,
		Action:           action,
		Enabled:          entry.Enabled,
		PermissionPolicy: hooks.NormalizeHookPermissionPolicy(hooks.HookPermissionPolicy(entry.PermissionPolicy)),
		PathAllowlist:    entry.PathAllowlist,
		PathDenylist:     entry.PathDenylist,
		PassEventAsJSON:  entry.PassEventAsJSON,
		WorkingDir:       workingDir,
		CreatedAt:        entry.CreatedAt,
	}, nil
}

func (a *App) SetProgram(program *tea.Program) {
	a.program = program
}

// SetModelPromotedCallback registers a lightweight callback that updates
// entrypoint-owned references when the bootstrap shell hands control to the
// fully constructed App. The callback runs synchronously on the event loop and
// must not perform blocking I/O.
func (a *App) SetModelPromotedCallback(callback func(*App)) {
	a.modelPromotedCallback = callback
}

// SetRuntimeReadyCallback registers entrypoint-owned work that depends on the
// SDK client. The callback is executed by a Bubble Tea command after runtime
// installation, never on the event loop.
func (a *App) SetRuntimeReadyCallback(callback func()) {
	a.runtimeReadyCallback = callback
}

// NotifyAsync delivers a notification banner to the TUI from any goroutine
// (level: "info" | "success" | "warning" | "error"). It is the bridge for
// package-main background work (e.g. the global-daemon auto-start) to surface
// outcomes visibly instead of dying in a debug log. Safe from any goroutine
// (tea.Program.Send is concurrency-safe); no-op before SetProgram or for an
// empty message.
func (a *App) NotifyAsync(level, message string) {
	if a == nil || a.program == nil || message == "" {
		return
	}
	a.program.Send(notificationMsg{level: level, message: message})
}

// getConversationPreview returns a one-line preview string for the sidebar.
// It prefers the pre-computed summary (free), falling back to scanning Messages
// only for legacy conversations that were saved before Summary existed.
func getConversationPreview(conv *conversation.Conversation) string {
	const max = 100
	if conv == nil {
		return ""
	}
	if s := conv.Summary; s != nil {
		if s.LastPreview != "" {
			return truncateRune(s.LastPreview, max)
		}
		if s.FirstUserPrompt != "" {
			return truncateRune(s.FirstUserPrompt, max)
		}
		if s.MessageCount == 0 {
			return tr("classic.chat.no_messages")
		}
	}
	for i := len(conv.Messages) - 1; i >= 0; i-- {
		msg := conv.Messages[i]
		if msg.Role == conversation.RoleAssistant && msg.Content != "" {
			return truncateRune(msg.Content, max)
		}
	}
	for _, msg := range conv.Messages {
		if msg.Role == conversation.RoleUser && msg.Content != "" {
			return truncateRune(msg.Content, max)
		}
	}
	return tr("classic.chat.no_messages")
}

// truncateRune returns up to max bytes of s with an ellipsis on truncation.
func truncateRune(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// AppOptions configures the App instance
type AppOptions struct {
	// AttachScreenFactory constructs the Ctrl+Q attach screen. It is optional
	// until the runtime can provide peer discovery/session wiring; when nil,
	// Ctrl+Q still opens an empty, safe AttachScreen.
	AttachScreenFactory func() *AttachScreen
	// ImageManager coordinates native Kitty graphics with Bubble Tea frames.
	// It is shared by the bootstrap shell and the initialized runtime.
	ImageManager *termimage.Manager
	// DebugMode enables the DebugLogs tool for searching session logs
	DebugMode bool
	// UseNewUI enables the new modular chat UI rendering (experimental)
	UseNewUI bool
	// DebugLineageFixture starts the app in a deterministic chat fixture focused
	// on error-lineage rendering and toggling.
	DebugLineageFixture bool
	// Updater is the auto-update manager (optional)
	// If provided, the app will check for updates on startup
	Updater *update.Updater
	// A2AEnabled enables the minimal A2A test wiring for this TUI session.
	A2AEnabled bool
	// A2AHandle overrides the default human-readable peer handle.
	A2AHandle string
	// A2AListenAddress overrides the local A2A listen address.
	A2AListenAddress string
	// WorkspaceRoot is the execution root used by tools and subprocesses.
	WorkspaceRoot string
	// ProjectRoot is the stable primary checkout used for conversation and peer identity.
	// Linked worktrees share this root while retaining independent WorkspaceRoot values.
	ProjectRoot string
	// TrackWorkspaceSession registers this TUI in the active-workspace lease registry.
	TrackWorkspaceSession bool
	// MemoryWatch enables the non-blocking heap threshold watcher (350/500/750/1000MB).
	MemoryWatch bool
	// ResumeConversationID opens the requested conversation once startup finishes.
	ResumeConversationID string
	// InitialPrompt starts a fresh interactive conversation and submits this text once.
	InitialPrompt string
	// HarnessPath selects a compiled, closed-posture harness for the interactive TUI.
	HarnessPath string
	// HarnessAllowYolo is the explicit CLI posture required by a yolo plan.
	HarnessAllowYolo bool
	// startupBrand is selected by the lightweight shell so the same logo and
	// splash survive the asynchronous handoff into the fully initialized app.
	startupBrand *startupBrandSelection
}

// ============================================================================
// TOKEN ESTIMATION HELPERS
// ============================================================================

// estimateTokens estimates token count using 3.7 chars per token ratio
// This matches historical analysis from token-counting-experiment
// Returns 0 for empty strings, handles edge cases gracefully
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	// Round up: ceil(chars / 3.7)
	chars := float64(len(text))
	tokens := int(math.Ceil(chars / 3.7))
	if tokens < 0 {
		return 0
	}
	return tokens
}

// imageBlockTokenBudget mirrors Anthropic's conservative per-image budget used
// by Claude Code (services/tokenEstimation.ts). Raw base64 bytes would blow up
// a char/ratio estimate; 2000 tracks the actual image-token cost closely
// enough without a provider round-trip.
const imageBlockTokenBudget = 2000

// roughTokenCountForMessage estimates tokens for a single message, walking
// every payload block — text, thinking, tool-call arguments, tool-result
// output/error, images, and attachments. The prior implementation counted
// only msg.Content, which is zero for tool-result messages and misses
// tool_use arguments entirely; that structural blindness was the root cause
// of the sidebar "jumping up" by 2-4x when an API response returned the
// real input_tokens. Mirrors Claude Code's
// services/tokenEstimation.ts:roughTokenCountEstimationForBlock.
func roughTokenCountForMessage(msg Message) int {
	total := estimateTokens(msg.Content)
	total += estimateTokens(msg.Thinking)

	for _, tc := range msg.ToolCalls {
		total += estimateTokens(tc.Name)
		if len(tc.Parameters) > 0 {
			if b, err := json.Marshal(tc.Parameters); err == nil {
				total += estimateTokens(string(b))
			}
		}
	}

	for i := range msg.ToolResults {
		total += estimateTokens(msg.ToolResults[i].GetOutput())
		total += estimateTokens(msg.ToolResults[i].Error)
		for range msg.ToolResults[i].Attachments {
			total += imageBlockTokenBudget
		}
	}

	for range msg.Images {
		total += imageBlockTokenBudget
	}

	for _, att := range msg.Attachments {
		if isImageLikeMime(att.MimeType) {
			total += imageBlockTokenBudget
		} else {
			total += estimateTokens(string(att.Content))
		}
	}

	return total
}

func isImageLikeMime(mime string) bool {
	return strings.HasPrefix(mime, "image/") || mime == "application/pdf"
}

// estimateTokensFromMessages sums rough token estimates across every message.
// Use tokenCountWithEstimation when an API-anchored count is available — this
// fallback re-estimates the full slice and intentionally does not include
// system prompt or tool-schema overhead (callers that need total context size
// should add getSystemPromptEstimate separately, or anchor on real usage).
func estimateTokensFromMessages(messages []Message) int {
	total := 0
	for i := range messages {
		total += roughTokenCountForMessage(messages[i])
	}
	return total
}

// tokenCountWithEstimation returns the best context-size estimate for a
// message slice by anchoring on the most recent message that carries real
// API-reported usage (InputTokens > 0) and rough-estimating only the tail
// appended after it. This matches Claude Code's tokens.ts:tokenCountWithEstimation.
//
// The anchor's InputTokens already accounts for system prompt, tool schemas,
// and all prior history as charged by the provider, so we only need to
// estimate messages the API hasn't yet seen.
//
// Falls back to estimateTokensFromMessages when no anchor exists (fresh
// conversation or freshly loaded without persisted usage).
func tokenCountWithEstimation(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].InputTokens > 0 {
			base := messages[i].InputTokens + messages[i].OutputTokens
			tail := 0
			for j := i + 1; j < len(messages); j++ {
				tail += roughTokenCountForMessage(messages[j])
			}
			return base + tail
		}
	}
	return estimateTokensFromMessages(messages)
}

// getSystemPromptEstimate gets estimated tokens for system prompt
// Returns 0 if no SDK is initialized or no system prompt is available
func (a *App) getSystemPromptEstimate() int {
	if a.sdk == nil {
		return 0
	}

	// Get system prompt from SDK (it stores it)
	systemPrompt := a.sdk.SystemPrompt()
	if systemPrompt == "" {
		return 0
	}

	return estimateTokens(systemPrompt)
}
