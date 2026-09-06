// Package agent — steering_mode.go.
//
// Phase 1 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf). This file introduces the
// SteeringMode flag without changing any behavior: the default is "poll",
// which preserves the existing per-intervention-point evaluator. Mode
// "stream" enables the long-lived steering agent driver (added in later
// phases). This file is intentionally minimal — the field is declared on
// the Definition, the enum values are constants here, and validation is
// a single helper. No call sites consume the new value yet.
package agent

// SteeringMode selects how the steering subsystem evaluates the subject
// agent's actions.
//
//   - SteeringModePoll   — the legacy path; every intervention point fires a
//     fresh Chat() on the steering evaluator. This is
//     the default to preserve backward compatibility.
//   - SteeringModeStream — the new path; one long-lived steering agent
//     subscribes to the subject's event stream and
//     intervenes via tool calls. Driver lives in
//     steering_stream.go.
type SteeringMode string

const (
	// SteeringModePoll is the legacy per-intervention-point evaluator. Default.
	SteeringModePoll SteeringMode = "poll"

	// SteeringModeStream enables the long-lived steering agent driver.
	SteeringModeStream SteeringMode = "stream"
)

// IsValid reports whether m is one of the recognised steering modes.
//
// An empty value is treated as valid because it serializes to omitempty and
// gets resolved to SteeringModePoll by ResolveSteeringMode.
func (m SteeringMode) IsValid() bool {
	switch m {
	case "", SteeringModePoll, SteeringModeStream:
		return true
	}
	return false
}

// ResolveSteeringMode returns the effective steering mode, applying the
// default when m is empty.
func ResolveSteeringMode(m SteeringMode) SteeringMode {
	if m == "" {
		return SteeringModePoll
	}
	return m
}
