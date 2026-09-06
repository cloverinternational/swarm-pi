// Package agent — steering_reentrancy.go.
//
// Phase 2 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// The steering subsystem invokes its own evaluator/observer LLM. That call
// re-enters the agent runtime — including any registered steering hooks —
// which would cause infinite recursion (observer asks LLM → pre-tool hook
// fires → would invoke observer again → ...).
//
// To break the cycle, every entry path into the steering layer must mark
// its context as "reentrant", and every steering hook must short-circuit
// when it sees that mark.
//
// Phase 1 of the redesign kept this guard private to the polling hook
// (`hooks/builtin/steering_pretool_hook.go`). Phase 2 introduces a SECOND
// hook (`steering_stream_hook.go`) that must share the SAME key — Go
// context keys are compared by package+type identity, so a duplicate
// declaration would silently produce two separate guards.
//
// This file is the single source of truth. Both hooks import the agent
// package (already a dependency) and use these helpers.
package agent

import "context"

// steeringReentrancyKey is the unexported context key used to mark a
// context as already inside the steering layer. Unexported so external
// packages cannot accidentally set or read it through the raw key —
// callers must use the helpers below.
type steeringReentrancyKey struct{}

// WithSteeringReentrancy returns a derived context marked as already
// inside the steering layer. Hooks observing this context should
// short-circuit (return Continue) rather than recursing.
//
// Use this whenever the steering subsystem makes its own LLM call (the
// polling evaluator, the streaming observer, the findings analyzer, …).
func WithSteeringReentrancy(ctx context.Context) context.Context {
	return context.WithValue(ctx, steeringReentrancyKey{}, true)
}

// IsSteeringReentrant reports whether ctx was previously marked by
// WithSteeringReentrancy. Steering hooks call this first thing in
// OnEvent to avoid infinite recursion.
func IsSteeringReentrant(ctx context.Context) bool {
	return ctx.Value(steeringReentrancyKey{}) != nil
}
