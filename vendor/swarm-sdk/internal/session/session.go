// Package session — session.go
//
// Defines the Session interface and the legacy session.New shim.  The
// canonical implementation is now *client.Client; this file exists as a
// thin compat layer for callers that have not yet migrated.
package session

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// Session is the SDK-native abstraction that *client.Client satisfies.
// Every method maps directly to one user-observable action (send a
// message, switch conversation, change model, etc.).  Existing IPC/ACP
// servers should accept *Session for backwards compat; new code should
// take *client.Client directly.
type Session interface {
	// ── Lifecycle ─────────────────────────────────────────────────────────
	Start(ctx context.Context) error
	Stop(ctx context.Context) error

	// ── Input: conversation ──────────────────────────────────────────────
	SendMessage(ctx context.Context, convID, message string, opts SendMessageOptions) error
	Cancel(ctx context.Context) error
	SwitchConversation(ctx context.Context, id string) error
	CreateConversation(ctx context.Context, opts CreateOptions) (string, error)
	DeleteConversation(ctx context.Context, id string) error
	LoadConversation(ctx context.Context, id string) (*conversation.Conversation, error)
	ListConversations(ctx context.Context, opts ListOptions) ([]ConversationSummary, error)
	EditMessage(ctx context.Context, convID, messageID, newText string) error

	// ── Input: configuration ─────────────────────────────────────────────
	SetMode(ctx context.Context, mode string) error
	SetModel(ctx context.Context, model string) error
	SetProvider(ctx context.Context, provider, model string) error
	SwitchProfile(ctx context.Context, profileID string) error

	// ── Compaction ───────────────────────────────────────────────────────
	Compact(ctx context.Context, convID string) error

	// ── Input: agent CRUD ────────────────────────────────────────────────
	SetAgent(ctx context.Context, name string) error
	CreateAgent(ctx context.Context, spec AgentSpec) error
	UpdateAgent(ctx context.Context, spec AgentSpec) error
	DeleteAgent(ctx context.Context, name string) error

	// ── Input: profile CRUD ──────────────────────────────────────────────
	CreateProfile(ctx context.Context, spec ProfileSpec) error
	UpdateProfile(ctx context.Context, spec ProfileSpec) error
	DeleteProfile(ctx context.Context, name string) error

	// ── Input: hook CRUD ─────────────────────────────────────────────────
	CreateHook(ctx context.Context, spec HookSpec) error
	UpdateHook(ctx context.Context, spec HookSpec) error
	DeleteHook(ctx context.Context, name string) error
	ToggleHook(ctx context.Context, name string) error

	// ── Input: prompt + context source CRUD ──────────────────────────────
	SetSystemPrompt(ctx context.Context, name, content string) error
	AddContextSource(ctx context.Context, spec ContextSourceSpec) error
	RemoveContextSource(ctx context.Context, name string) error
	ToggleContextSource(ctx context.Context, name string) error

	// ── Input: tool toggle ───────────────────────────────────────────────
	ToggleTool(ctx context.Context, name string, enabled bool) error

	// ── Input: config + display ──────────────────────────────────────────
	SetConfig(ctx context.Context, key string, value any) error
	LoadConfig(ctx context.Context) error
	SaveConfig(ctx context.Context) error
	SetTheme(ctx context.Context, theme string) error
	ToggleCompactMode(ctx context.Context) error

	// ── Input: history ───────────────────────────────────────────────────
	ClearHistory(ctx context.Context) error
	SearchHistory(ctx context.Context, query string) ([]ConversationSummary, error)

	// ── Output ────────────────────────────────────────────────────────────
	Subscribe(handler EventHandler) Unsubscribe
	Snapshot() State

	// ── Auxiliary ─────────────────────────────────────────────────────────
	ToolRegistry() ToolRegistryInfo
	ActiveAgent() string
}

// Compile-time check: *client.Client satisfies Session.
var _ Session = (*client.Client)(nil)

// ClientSession is a deprecated alias for *client.Client.  New code
// should use *client.Client directly.
//
// Deprecated: use *client.Client.
type ClientSession = client.Client

// ErrNoConfigManager is a deprecated alias for client.ErrNoConfigManager.
//
// Deprecated: use client.ErrNoConfigManager.
var ErrNoConfigManager = client.ErrNoConfigManager
