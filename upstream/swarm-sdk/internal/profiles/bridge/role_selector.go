// Package bridge provides profile-to-execution glue that was previously
// duplicated in swarm-tui/internal/chat/sdk_integration.go.
//
// It lives in a separate sub-package to avoid an import cycle:
//
//	profiles → builtin (RoleType) → agent → profiles
//	bridge  imports both profiles and builtin; nothing imports bridge.
//
// See CONTRACT.md for the full integration contract.
package bridge

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/profiles"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/builtin"
)

// RoleTypeToModelAlias maps a builtin.RoleType to its canonical profile ModelAlias.
//
// Mapping rationale (from SADD workflow specification):
//
//   - supervisor:      oversight / evaluation  → steering
//   - implementer:   task execution          → sub_agent
//   - specReviewer:  review / evaluation     → steering
//   - qualityReviewer: review / evaluation   → steering
//   - (default):     unknown roles           → sub_agent (fallback)
func RoleTypeToModelAlias(role builtin.RoleType) profiles.ModelAlias {
	switch role {
	case builtin.RoleSupervisor:
		return profiles.AliasSteering
	case builtin.RoleImplementer:
		return profiles.AliasSubAgent
	case builtin.RoleSpecReviewer:
		return profiles.AliasSteering
	case builtin.RoleQualityReviewer:
		return profiles.AliasSteering
	default:
		// Unknown role — default to subagent (task execution)
		return profiles.AliasSubAgent
	}
}

// BuildRoleModelSelector creates a builtin.RoleModelSelector from a Manager.
//
// It maps SADD RoleTypes to profile ModelAliases, resolves the fallback chain,
// and falls back to AliasMain when the role-specific alias is not configured.
//
// Returns nil if mgr is nil, which causes the runtime to use its default model.
//
// Usage (TUI / headless / CLI):
//
//	selector := bridge.BuildRoleModelSelector(mgr)
//	// Pass selector to agent.New() or delegate_task tool config
func BuildRoleModelSelector(mgr *profiles.Manager) builtin.RoleModelSelector {
	if mgr == nil {
		return nil
	}

	return func(role builtin.RoleType) *builtin.RoleModelConfig {
		alias := RoleTypeToModelAlias(role)

		chain, err := mgr.ResolveChain(alias)
		if err != nil {
			// Alias not configured — fallback to AliasMain so that
			// sub-agents can still deploy even when the role alias
			// is not explicitly configured.
			chain, err = mgr.ResolveChain(profiles.AliasMain)
			if err != nil {
				// AliasMain also missing — return nil to use default model
				return nil
			}
		}

		return &builtin.RoleModelConfig{
			Provider: chain.Primary.Provider,
			Model:    chain.Primary.Model,
			Chain:    chain,
		}
	}
}

// BuildCurrentProviderGetter returns a function that yields the provider name
// from the active profile's sub-agent (or main) alias.
//
// This is used by sub-agent spawning logic to determine which provider
// should handle a delegated task.
//
// Falls back to AliasMain when AliasSubAgent is not configured.
// Returns nil if mgr is nil.
func BuildCurrentProviderGetter(mgr *profiles.Manager) func() string {
	if mgr == nil {
		return nil
	}

	return func() string {
		chain, err := mgr.ResolveChain(profiles.AliasSubAgent)
		if err != nil {
			chain, err = mgr.ResolveChain(profiles.AliasMain)
			if err != nil {
				return ""
			}
		}
		return chain.Primary.Provider
	}
}

// ResolveRoleChain is a convenience that resolves the fallback chain for a
// given SADD role, falling back to AliasMain when needed.
//
// Returns the chain, or nil if neither the role alias nor AliasMain is
// configured.
func ResolveRoleChain(mgr *profiles.Manager, role builtin.RoleType) *fallback.Chain {
	if mgr == nil {
		return nil
	}

	alias := RoleTypeToModelAlias(role)
	chain, err := mgr.ResolveChain(alias)
	if err != nil {
		chain, err = mgr.ResolveChain(profiles.AliasMain)
		if err != nil {
			return nil
		}
	}
	return chain
}
