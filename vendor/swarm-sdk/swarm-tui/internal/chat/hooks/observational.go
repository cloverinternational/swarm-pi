package hooks

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/agent"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks/agentbridge"
)

// This file is additive: it does not change any existing behaviour of
// HooksManager. It only lets the TUI's hook set be OBSERVED by sub-agent and
// background runs (PLAN.md G3).
//
// Why it matters that this exists at all: the observational view is derived
// from whatever hooks manager the parent agent put on the context. In a TUI
// session that is *this* type, not agentbridge.Bridge. Without this method the
// gate could be switched on and every TUI sub-agent would still be dark — the
// change would appear to work in unit tests and do nothing in the product.
//
// Compile-time proof that the TUI manager can hand out an observational view.
var _ agent.ObservationalHooksProvider = (*HooksManager)(nil)

// ObservationalHooks returns a blocking-incapable view of this manager's hooks,
// restricted to hooks that implement hooks.ObservationalHook.
//
// It reuses agentbridge's adapter rather than re-deriving the event shape, so
// an observed TUI sub-agent event is byte-identical in structure to an observed
// SDK sub-agent event. Two spellings of the same event would make the ledger's
// join key unreliable, which is the failure this whole effort exists to fix.
//
// The underlying view is memoized per *hooks.Manager, so calling this once per
// sub-agent spawn does not allocate a goroutine per spawn. Callers must not
// close it: they do not own it.
//
// Returns nil when no manager is present, which an agent treats as "emit
// nothing". Note that --no-hooks makes NewSDKIntegrationWithOptions skip
// building a HooksManager entirely, so a --no-hooks session has no manager for
// this method to be called on and stays unobserved. That residual gap is
// documented rather than silently closed here.
func (m *HooksManager) ObservationalHooks() agent.ObservationalHooks {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	mgr := m.manager
	m.mu.RUnlock()
	if mgr == nil {
		return nil
	}
	return agentbridge.New(mgr).ObservationalHooks()
}
