// Package bench holds the opt-in measurement instrumentation for agent runs.
//
// This file contains ONLY the gate. It has no dependencies on any other
// package in the SDK by design: every layer that must ask "is measurement
// instrumentation turned on?" can import this without creating a cycle, and
// nothing here can drag the rest of the SDK into a leaf package.
//
// # Why a gate at all
//
// PLAN.md §9.3 — "Nothing on by default. No recorder registered unless
// explicitly enabled. ('Registered but disabled' is how --no-hooks came to lie
// for months.)"
//
// That parenthetical is the whole design constraint. The historical --no-hooks
// bug was NOT a missing flag; the flag existed and was read. The bug was that
// the hooks manager was constructed and attached BEFORE the flag was consulted,
// so the flag only suppressed a later cosmetic branch while the hooks kept
// firing. The lesson: a gate must be consulted at the WIRING site, and when it
// is off the instrumentation object must never be constructed or attached at
// all — so that "is it running?" is answerable by looking for a nil field
// rather than by trusting a bool.
//
// Accordingly:
//
//   - Enabled() is called exactly once per sub-agent spawn, at the point where
//     the observational view would be attached (internal/tools/builtin/subagent.go).
//   - When it returns false, nothing is constructed and the agent's
//     observational field stays nil. There is no "attached but disabled" state
//     for this feature to lie about.
//   - Status() reports what is actually in force and WHY, so a caller can print
//     the truth instead of restating its own intent.
package bench

import (
	"os"
	"strings"
	"sync"
)

// EnvObservationalHooks is the environment variable that opts a process in to
// observational hook coverage for sub-agent and background runs.
//
// Recognised true values (case-insensitive): "1", "true", "yes", "on".
// Every other value — including unset, "", "0", "false", and garbage — is OFF.
// Garbage is deliberately OFF rather than an error: a typo must never silently
// enable instrumentation, and a leaf gate has nowhere to report an error to.
const EnvObservationalHooks = "SWARM_BENCH_OBSERVE"

// GateSource describes where an effective gate value came from. It exists so
// Status() can be truthful about provenance rather than just the value.
type GateSource string

const (
	// SourceDefault means nothing opted in; the compiled-in default (OFF) holds.
	SourceDefault GateSource = "default"
	// SourceEnv means EnvObservationalHooks was set to a recognised true value.
	SourceEnv GateSource = "env"
	// SourceProgrammatic means an embedder called SetObservationalHooksEnabled.
	SourceProgrammatic GateSource = "programmatic"
)

// GateStatus is a truthful snapshot of the observational gate.
type GateStatus struct {
	// Enabled is the effective value the wiring sites will act on.
	Enabled bool
	// Source records which input produced Enabled.
	Source GateSource
	// EnvValue is the raw environment value as read, for diagnosis of typos.
	EnvValue string
}

var (
	gateMu sync.RWMutex
	// programmaticSet distinguishes "explicitly set to false" from "never set",
	// so an embedder can force OFF even in a process whose environment says ON.
	programmaticSet   bool
	programmaticValue bool
)

// SetObservationalHooksEnabled lets an embedder or a test set the gate
// explicitly. A programmatic value takes precedence over the environment in
// both directions: an embedder that must guarantee no instrumentation can
// force false even when EnvObservationalHooks is set.
//
// Returns a restore function so tests can defer-restore the previous state
// without reaching into package internals.
func SetObservationalHooksEnabled(enabled bool) (restore func()) {
	gateMu.Lock()
	prevSet, prevValue := programmaticSet, programmaticValue
	programmaticSet, programmaticValue = true, enabled
	gateMu.Unlock()
	return func() {
		gateMu.Lock()
		programmaticSet, programmaticValue = prevSet, prevValue
		gateMu.Unlock()
	}
}

// ObservationalHooksEnabled reports whether observational hook coverage for
// sub-agent and background runs is opted in for this process.
//
// The compiled-in default is FALSE. There is no code path that turns this on
// implicitly.
func ObservationalHooksEnabled() bool {
	return ObservationalHooksStatus().Enabled
}

// ObservationalHooksStatus reports the effective gate value together with its
// provenance. Callers that log "instrumentation is on" should log this, not
// their own intent — the --no-hooks incident was a case of a surface reporting
// intent while the code did something else.
func ObservationalHooksStatus() GateStatus {
	gateMu.RLock()
	set, value := programmaticSet, programmaticValue
	gateMu.RUnlock()

	raw := os.Getenv(EnvObservationalHooks)
	if set {
		return GateStatus{Enabled: value, Source: SourceProgrammatic, EnvValue: raw}
	}
	if truthy(raw) {
		return GateStatus{Enabled: true, Source: SourceEnv, EnvValue: raw}
	}
	return GateStatus{Enabled: false, Source: SourceDefault, EnvValue: raw}
}

// truthy reports whether raw is one of the recognised opt-in spellings.
func truthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
