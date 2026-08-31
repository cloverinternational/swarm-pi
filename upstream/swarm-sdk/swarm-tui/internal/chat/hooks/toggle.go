package hooks

import (
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// HookState is a UI-facing snapshot of a single hook's registration.
// Returned by ListHookStates so callers can render toggles without importing
// the SDK's hooks package directly.
type HookState struct {
	Name    string
	Enabled bool
	Scope   hooks.HookScope
	ScopeID string
}

// ListHookStates returns the current enable/disable state of every
// registered hook in the SDK manager. The result is a snapshot — it is
// safe to use across goroutines but may be stale by the time callers
// act on it. Use SetHookEnabledByName to change state.
func (hm *HooksManager) ListHookStates() []HookState {
	if hm == nil || hm.manager == nil {
		return nil
	}
	regs := hm.manager.List()
	out := make([]HookState, 0, len(regs))
	for _, r := range regs {
		if r.Hook == nil {
			continue
		}
		out = append(out, HookState{
			Name:    r.Hook.Name(),
			Enabled: r.Enabled,
			Scope:   r.Scope,
			ScopeID: r.ScopeID,
		})
	}
	return out
}

// SetHookEnabledByName flips a single hook's enabled bit at runtime. Safe
// to call during an in-flight agent execution — the SDK manager consults
// the enabled bit under its RWMutex on every event dispatch, so the next
// event sees the new state and no in-flight hook is interrupted.
//
// The TUI's hookStates cache is kept in sync so hooksManager.IsEnabled
// (used by tools.go to render the hook dashboard) doesn't go stale after
// a live flip.
func (hm *HooksManager) SetHookEnabledByName(name string, enabled bool) error {
	if hm == nil || hm.manager == nil {
		return nil
	}
	if err := hm.manager.SetEnabled(name, enabled); err != nil {
		return err
	}
	hm.mu.Lock()
	hm.hookStates[name] = enabled
	hm.mu.Unlock()
	return nil
}

// ToggleGroupByPrefix flips all hooks whose Name() starts with prefix.
// Semantics:
//   - if any matching hook is currently enabled → disable all; newState=false
//   - if none are enabled                       → enable all;  newState=true
//
// Returns the count of hooks that actually changed state and the resulting
// state. Matching is case-insensitive on the prefix so "steering" matches
// both "steering-pretool" and "Steering-whatever".
func (hm *HooksManager) ToggleGroupByPrefix(prefix string) (count int, newState bool, err error) {
	if hm == nil || hm.manager == nil {
		return 0, false, nil
	}
	prefix = strings.ToLower(prefix)
	states := hm.ListHookStates()

	var matched []HookState
	anyEnabled := false
	for _, s := range states {
		if !strings.HasPrefix(strings.ToLower(s.Name), prefix) {
			continue
		}
		matched = append(matched, s)
		if s.Enabled {
			anyEnabled = true
		}
	}
	if len(matched) == 0 {
		return 0, false, nil
	}

	newState = !anyEnabled
	for _, s := range matched {
		if s.Enabled == newState {
			continue
		}
		if err := hm.manager.SetEnabled(s.Name, newState); err != nil {
			return count, newState, err
		}
		hm.mu.Lock()
		hm.hookStates[s.Name] = newState
		hm.mu.Unlock()
		count++
	}
	return count, newState, nil
}

// ToggleAllHooks flips every registered hook using the same majority-flip
// semantics as ToggleGroupByPrefix: any enabled → disable all; else enable
// all. Acts as a master kill-switch for runtime overrides.
func (hm *HooksManager) ToggleAllHooks() (count int, newState bool, err error) {
	return hm.ToggleGroupByPrefix("")
}
