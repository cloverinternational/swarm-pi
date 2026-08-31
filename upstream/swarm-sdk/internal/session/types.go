// Package session — types.go
//
// All public types in this package are now aliases for the canonical
// definitions in github.com/Swarm-Code/mono/swarm-sdk/client.  The session
// package itself is preserved as a thin compatibility layer for callers
// that have not yet migrated to *client.Client directly.
//
// Migration: prefer `client.<Type>` over `session.<Type>` in new code.
package session

import (
	"github.com/Swarm-Code/mono/swarm-sdk/client"
)

// ─── Public state ─────────────────────────────────────────────────────────────

// State is an alias for client.State.
type State = client.State

// ConversationSummary is an alias for client.ConversationSummary.
type ConversationSummary = client.ConversationSummary

// CreateOptions is an alias for client.CreateOptions.
type CreateOptions = client.CreateOptions

// ListOptions is an alias for client.ListOptions.
type ListOptions = client.ListOptions

// SendMessageOptions is an alias for client.SendMessageOptions.
type SendMessageOptions = client.SendMessageOptions

// ToolRegistryInfo is an alias for client.ToolRegistryInfo.
type ToolRegistryInfo = client.ToolRegistryInfo

// ─── Output stream ────────────────────────────────────────────────────────────

// EventKind is an alias for client.EventKind.
type EventKind = client.EventKind

const (
	EventAgent                = client.EventAgent
	EventStreamStart          = client.EventStreamStart
	EventStreamEnd            = client.EventStreamEnd
	EventConvSwitched         = client.EventConvSwitched
	EventConvCreated          = client.EventConvCreated
	EventConvDeleted          = client.EventConvDeleted
	EventModelChanged         = client.EventModelChanged
	EventModeChanged          = client.EventModeChanged
	EventProfileChanged       = client.EventProfileChanged
	EventAgentChanged         = client.EventAgentChanged
	EventHookChanged          = client.EventHookChanged
	EventToolChanged          = client.EventToolChanged
	EventSystemPromptChanged  = client.EventSystemPromptChanged
	EventContextSourceChanged = client.EventContextSourceChanged
	EventConfigChanged        = client.EventConfigChanged
	EventConfigLoaded         = client.EventConfigLoaded
	EventConfigSaved          = client.EventConfigSaved
	EventThemeChanged         = client.EventThemeChanged
	EventHistoryCleared       = client.EventHistoryCleared
	EventHistoryResult        = client.EventHistoryResult
	EventError                = client.EventError
)

// Event is an alias for client.Event.
type Event = client.Event

// ─── Non-agent payloads ───────────────────────────────────────────────────────

// ConvPayload is an alias for client.ConvPayload.
type ConvPayload = client.ConvPayload

// ModelPayload is an alias for client.ModelPayload.
type ModelPayload = client.ModelPayload

// ModePayload is an alias for client.ModePayload.
type ModePayload = client.ModePayload

// ProfilePayload is an alias for client.ProfilePayload.
type ProfilePayload = client.ProfilePayload

// AgentPayload is an alias for client.AgentPayload.
type AgentPayload = client.AgentPayload

// HookPayload is an alias for client.HookPayload.
type HookPayload = client.HookPayload

// ToolPayload is an alias for client.ToolPayload.
type ToolPayload = client.ToolPayload

// SystemPromptPayload is an alias for client.SystemPromptPayload.
type SystemPromptPayload = client.SystemPromptPayload

// ContextSourcePayload is an alias for client.ContextSourcePayload.
type ContextSourcePayload = client.ContextSourcePayload

// ConfigPayload is an alias for client.ConfigPayload.
type ConfigPayload = client.ConfigPayload

// ThemePayload is an alias for client.ThemePayload.
type ThemePayload = client.ThemePayload

// HistoryResultPayload is an alias for client.HistoryResultPayload.
type HistoryResultPayload = client.HistoryResultPayload

// ErrorPayload is an alias for client.ErrorPayload.
type ErrorPayload = client.ErrorPayload

// ─── Input specifications ─────────────────────────────────────────────────────

// AgentSpec is an alias for client.AgentSpec.
type AgentSpec = client.AgentSpec

// ProfileSpec is an alias for client.ProfileSpec.
type ProfileSpec = client.ProfileSpec

// HookSpec is an alias for client.HookSpec.
type HookSpec = client.HookSpec

// ContextSourceSpec is an alias for client.ContextSourceSpec.
type ContextSourceSpec = client.ContextSourceSpec

// ─── Subscription ─────────────────────────────────────────────────────────────

// EventHandler is an alias for client.EventHandler.
type EventHandler = client.EventHandler

// Unsubscribe is an alias for client.Unsubscribe.
type Unsubscribe = client.Unsubscribe
