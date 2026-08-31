// Package settings — agent profile type shims.
//
// All profile types are now canonical in sdk/profiles.
// This file re-exports them as type aliases so the rest of the settings
// package compiles without any changes.
//
// DO NOT add new type definitions here.  Add them to sdk/profiles/types.go
// so every application (TUI, headless, SDK consumers) gets them.
package settings

import (
	sdkprofiles "github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// ── Type aliases — all point at sdk/profiles ─────────────────────────────────

// ModelAlias is a symbolic role name.  See sdk/profiles.ModelAlias.
type ModelAlias = sdkprofiles.ModelAlias

// RoleConfig is the per-role pool configuration.  See sdk/profiles.RoleConfig.
type RoleConfig = sdkprofiles.RoleConfig

// AgentProfile is a named set of per-role model pools.  See sdk/profiles.AgentProfile.
type AgentProfile = sdkprofiles.AgentProfile

// ProfilesConfig is the root object persisted to agent_profiles.json.
type ProfilesConfig = sdkprofiles.ProfilesConfig

// ProfileRetryPolicy controls pool rotation.  See sdk/profiles.RetryPolicy.
// Aliased under the old TUI name for zero diff in existing code.
type ProfileRetryPolicy = sdkprofiles.RetryPolicy

// ModelPointer is the legacy single-model config (backward compat).
type ModelPointer = sdkprofiles.ModelPointer

// AgentCapabilities defines runtime constraints.
// NOTE: also declared in agents.go for CustomAgentEntry — they are the
// same struct via the alias; Go is fine with one canonical definition.
type AgentCapabilities = sdkprofiles.AgentCapabilities

// ValidationError is a structured validation failure.
type ValidationError = sdkprofiles.ValidationError

// ── Constant aliases ──────────────────────────────────────────────────────────

const (
	AliasMain        = sdkprofiles.AliasMain
	AliasSteering    = sdkprofiles.AliasSteering
	AliasBackground  = sdkprofiles.AliasBackground
	AliasSubAgent    = sdkprofiles.AliasSubAgent
	AliasInference   = sdkprofiles.AliasInference
	AliasLongContext = sdkprofiles.AliasLongContext
	AliasCompaction  = sdkprofiles.AliasCompaction
	AliasThinking    = sdkprofiles.AliasThinking
)

// ── Error aliases ─────────────────────────────────────────────────────────────

var (
	ErrEmptyProvider          = sdkprofiles.ErrEmptyProvider
	ErrEmptyModel             = sdkprofiles.ErrEmptyModel
	ErrInvalidMaxTokens       = sdkprofiles.ErrInvalidMaxTokens
	ErrMaxTokensTooLarge      = sdkprofiles.ErrMaxTokensTooLarge
	ErrInvalidTemperature     = sdkprofiles.ErrInvalidTemperature
	ErrInvalidMaxTurns        = sdkprofiles.ErrInvalidMaxTurns
	ErrInvalidTimeout         = sdkprofiles.ErrInvalidTimeout
	ErrEmptyProfileID         = sdkprofiles.ErrEmptyProfileID
	ErrEmptyProfileName       = sdkprofiles.ErrEmptyProfileName
	ErrInvalidAlias           = sdkprofiles.ErrInvalidAlias
	ErrAliasNotConfigured     = sdkprofiles.ErrAliasNotConfigured
	ErrProfileNotFound        = sdkprofiles.ErrProfileNotFound
	ErrDefaultProfileNotFound = sdkprofiles.ErrDefaultProfileNotFound
	ErrNoProfiles             = sdkprofiles.ErrNoProfiles
)

// ── Function aliases ──────────────────────────────────────────────────────────

// AllAliases returns the canonical set of role aliases.
func AllAliases() []ModelAlias { return sdkprofiles.AllAliases() }

// AliasDisplayName returns a human-readable label for a role alias.
func AliasDisplayName(a ModelAlias) string {
	messageIDs := map[ModelAlias]string{
		AliasMain:               "settings.agent_profiles.role.main",
		AliasSteering:           "settings.agent_profiles.role.steering",
		AliasBackground:         "settings.agent_profiles.role.background",
		AliasSubAgent:           "settings.agent_profiles.role.sub_agent",
		AliasInference:          "settings.agent_profiles.role.inference",
		AliasLongContext:        "settings.agent_profiles.role.long_context",
		AliasCompaction:         "settings.agent_profiles.role.compaction",
		AliasThinking:           "settings.agent_profiles.role.thinking",
		sdkprofiles.AliasVision: "settings.agent_profiles.role.vision",
	}
	if messageID, ok := messageIDs[a]; ok {
		return i18n.T(messageID)
	}
	return sdkprofiles.AliasDisplayName(a)
}

// AliasDescription returns the localized UI description for a role alias.
func AliasDescription(a ModelAlias) string {
	messageIDs := map[ModelAlias]string{
		AliasMain:               "settings.agent_profiles.role.main.description",
		AliasSteering:           "settings.agent_profiles.role.steering.description",
		AliasBackground:         "settings.agent_profiles.role.background.description",
		AliasSubAgent:           "settings.agent_profiles.role.sub_agent.description",
		AliasInference:          "settings.agent_profiles.role.inference.description",
		AliasLongContext:        "settings.agent_profiles.role.long_context.description",
		AliasCompaction:         "settings.agent_profiles.role.compaction.description",
		sdkprofiles.AliasVision: "settings.agent_profiles.role.vision.description",
	}
	if messageID, ok := messageIDs[a]; ok {
		return i18n.T(messageID)
	}
	return sdkprofiles.AliasDescription(a)
}

// GetAvailableRoleAliases returns alias strings for dropdown UI.
func GetAvailableRoleAliases() []string {
	aliases := sdkprofiles.AllAliases()
	out := make([]string, len(aliases))
	for i, a := range aliases {
		out[i] = string(a)
	}
	return out
}

// DefaultRetryPolicy returns sensible production retry defaults.
func DefaultRetryPolicy() *ProfileRetryPolicy { return sdkprofiles.DefaultRetryPolicy() }

// NewRoleConfig creates a single-model RoleConfig.
func NewRoleConfig(provider, model string) RoleConfig {
	return sdkprofiles.NewRoleConfig(provider, model)
}

// NewRoleConfigChain creates a RoleConfig from alternating provider/model pairs.
func NewRoleConfigChain(pairs ...string) RoleConfig {
	return sdkprofiles.NewRoleConfigChain(pairs...)
}

// GenerateBuiltinProfiles returns the 8 default profiles from the SDK.
func GenerateBuiltinProfiles() []AgentProfile {
	return sdkprofiles.GenerateBuiltinProfiles()
}
