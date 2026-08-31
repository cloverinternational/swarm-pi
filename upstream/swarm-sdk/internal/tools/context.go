package tools

import (
	"context"
	"path/filepath"
	"strings"
)

// contextKey is an unexported type for context keys in this package.
type contextKey string

const (
	ctxKeyAgentID        contextKey = "tools.agent_id"
	ctxKeyUserID         contextKey = "tools.user_id"
	ctxKeyConversationID contextKey = "tools.conversation_id"
	ctxKeyRootSessionID  contextKey = "tools.root_session_id"
	ctxKeyToolCallID     contextKey = "tools.tool_call_id"
	// ctxKeyUserMessage carries the original user message that triggered the
	// current agent turn.  Injected at Execute() time so every tool call made
	// during the turn can record "why" the change was made.
	ctxKeyUserMessage contextKey = "tools.user_message"
	// ctxKeyWorkspacePath carries the workspace root for the current execution.
	// Injected alongside the conversation ID so tools can resolve project paths
	// without needing to import the agent package.
	ctxKeyWorkspacePath contextKey = "tools.workspace_path"
	// ctxKeyApprovedPaths carries the set of paths that the user has explicitly
	// approved for the current tool execution. Tools that enforce workspace
	// boundary checks (e.g. forge FSWrite/FSPatch/FSRead) consult this set so
	// that a user-approved out-of-workspace path is not rejected a second time
	// by the tool's own path guard.
	ctxKeyApprovedPaths contextKey = "tools.approved_paths"
)

// WithOwnerInfo injects agent/user identity into the context for observability
// and access control. Tools can retrieve these values to track who spawned them.
func WithOwnerInfo(ctx context.Context, agentID, userID, conversationID string) context.Context {
	if agentID != "" {
		ctx = context.WithValue(ctx, ctxKeyAgentID, agentID)
	}
	if userID != "" {
		ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	}
	if conversationID != "" {
		ctx = context.WithValue(ctx, ctxKeyConversationID, conversationID)
	}
	return ctx
}

// OwnerAgentID retrieves the agent ID from the context.
func OwnerAgentID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyAgentID).(string); ok {
		return id
	}
	return ""
}

// OwnerUserID retrieves the user ID from the context.
func OwnerUserID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyUserID).(string); ok {
		return id
	}
	return ""
}

// OwnerConversationID retrieves the conversation ID from the context.
func OwnerConversationID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyConversationID).(string); ok {
		return id
	}
	return ""
}

// WithRootSessionID injects the stable root-session identity used for
// session-scoped authorization. Unlike the active conversation ID, this value
// remains stable across compaction and conversation replacement.
func WithRootSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyRootSessionID, sessionID)
}

// RootSessionID retrieves the stable root-session identity from the context.
func RootSessionID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyRootSessionID).(string); ok {
		return id
	}
	return ""
}

// WithUserMessage injects the original user message that triggered the current
// agent turn into the context.  Call this once at the start of Agent.Execute()
// so all tool calls made during the turn can read the user's intent.
func WithUserMessage(ctx context.Context, msg string) context.Context {
	if msg == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyUserMessage, msg)
}

// UserMessageFromContext retrieves the user message injected by WithUserMessage.
// Returns an empty string when not set.
func UserMessageFromContext(ctx context.Context) string {
	if msg, ok := ctx.Value(ctxKeyUserMessage).(string); ok {
		return msg
	}
	return ""
}

// WithWorkspacePath injects the workspace root directory into the context.
func WithWorkspacePath(ctx context.Context, path string) context.Context {
	if path == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyWorkspacePath, path)
}

// WorkspacePathFromContext retrieves the workspace path injected by WithWorkspacePath.
// Returns an empty string when not set.
func WorkspacePathFromContext(ctx context.Context) string {
	if p, ok := ctx.Value(ctxKeyWorkspacePath).(string); ok {
		return p
	}
	return ""
}

// WithToolCallID injects the tool call ID into the context for unique identification.
func WithToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, ctxKeyToolCallID, toolCallID)
}

// ToolCallID retrieves the tool call ID from the context.
func ToolCallID(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKeyToolCallID).(string); ok {
		return id
	}
	return ""
}

// WithApprovedPaths records one or more file-system paths as explicitly approved
// by the user for the current tool invocation. Forge tools (FSRead, FSWrite,
// FSPatch) consult this set in their validatePath helpers so that an approved
// out-of-workspace path is not rejected by the tool's workspace boundary check.
//
// Paths are stored as cleaned absolute paths. Relative paths are left as-is
// (the tool will absolutise them anyway).
func WithApprovedPaths(ctx context.Context, paths ...string) context.Context {
	existing := approvedPathsFromCtx(ctx)
	next := make(map[string]struct{}, len(existing)+len(paths))
	for p := range existing {
		next[p] = struct{}{}
	}
	for _, p := range paths {
		if p != "" {
			// Resolve to an absolute path so lookups work correctly regardless
			// of whether the caller passed a relative or absolute path.
			if abs, err := filepath.Abs(p); err == nil {
				next[filepath.Clean(abs)] = struct{}{}
			} else {
				next[filepath.Clean(p)] = struct{}{}
			}
		}
	}
	return context.WithValue(ctx, ctxKeyApprovedPaths, next)
}

// IsPathApproved reports whether absPath (or any directory that contains it)
// was stored as an approved path in ctx by WithApprovedPaths.
func IsPathApproved(ctx context.Context, absPath string) bool {
	m := approvedPathsFromCtx(ctx)
	if len(m) == 0 {
		return false
	}
	clean := filepath.Clean(absPath)
	// Exact match.
	if _, ok := m[clean]; ok {
		return true
	}
	// Check whether any approved entry is a parent of absPath (approved dir covers the file).
	for approved := range m {
		rel, err := filepath.Rel(approved, clean)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, "..") && rel != "") {
			return true
		}
	}
	return false
}

func approvedPathsFromCtx(ctx context.Context) map[string]struct{} {
	if m, ok := ctx.Value(ctxKeyApprovedPaths).(map[string]struct{}); ok {
		return m
	}
	return nil
}

// ctxKeyDenialReason carries a pointer to a string that a permission checker can
// write an actionable denial reason into. Because context values are passed by
// value into CheckWithContext (which returns only a bool), a pointer holder is
// used so the reason set inside the checker is visible to the caller (the tool
// registry) after the check returns. This lets the model-visible tool-result
// carry a recovery hint instead of a generic "permission denied" string.
const ctxKeyDenialReason contextKey = "tools.denial_reason"

// WithDenialReasonSink installs a writable denial-reason holder into ctx and
// returns the new context plus the holder. The registry installs this before
// calling CheckWithContext; a checker calls SetDenialReason(ctx, ...) on deny;
// the registry then reads *holder to enrich the denied tool-result.
func WithDenialReasonSink(ctx context.Context) (context.Context, *string) {
	holder := new(string)
	return context.WithValue(ctx, ctxKeyDenialReason, holder), holder
}

// SetDenialReason records an actionable denial reason into the sink installed by
// WithDenialReasonSink, if present. Safe to call when no sink is installed.
func SetDenialReason(ctx context.Context, reason string) {
	if reason == "" {
		return
	}
	if holder, ok := ctx.Value(ctxKeyDenialReason).(*string); ok && holder != nil {
		*holder = reason
	}
}
