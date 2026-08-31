// Package agent — steering_ask_adapter.go.
//
// Phase 3+4 of the steering-agent-with-tools redesign
// (docs/steering-redesign/steering-redesign.pdf).
//
// SteeringAskAdapter is the concrete AskUserPusher used by the streaming
// driver to surface ask_user questions to the user. It is a thin function
// wrapper so the agent package never has to import interaction-layer code:
// the host (TUI / library consumer) supplies a closure that knows how to
// talk to its own broker.
//
// Phase 4 contract: synchronous. Ask blocks until the user replies (or the
// host's timeout fires). The returned answer flows back to the observer
// LLM as the ask_user tool's result. The subject agent continues running
// during the wait — only the observer goroutine is paused.
package agent

import (
	"context"
	"time"
)

// SteeringAskAdapter is a concrete AskUserPusher backed by a function. The
// TUI constructs it with a closure that calls its UserInteractionBroker.
// Tests can construct one with a recording fn.
type SteeringAskAdapter struct {
	askFn func(ctx context.Context, question, urgency string, timeout time.Duration) (string, error)
}

// NewSteeringAskAdapter wraps fn into an AskUserPusher. fn may be nil; in
// that case Ask returns ("", nil) — callers fall back to defaults.
func NewSteeringAskAdapter(fn func(ctx context.Context, question, urgency string, timeout time.Duration) (string, error)) *SteeringAskAdapter {
	return &SteeringAskAdapter{askFn: fn}
}

// Ask forwards the question + urgency + timeout to the wrapped function.
// Returns the user's answer or an error. When the inner fn is nil (or the
// receiver is nil), returns ("", nil) so callers can fall back to
// params.Default without ambiguity.
func (a *SteeringAskAdapter) Ask(ctx context.Context, question, urgency string, timeout time.Duration) (string, error) {
	if a == nil || a.askFn == nil {
		return "", nil
	}
	return a.askFn(ctx, question, urgency, timeout)
}

// Compile-time assertion that SteeringAskAdapter satisfies AskUserPusher.
var _ AskUserPusher = (*SteeringAskAdapter)(nil)
