package agent

import (
	"context"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/bench"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// bench_capture.go — the effect-ledger capture seam (PLAN.md §4 C1, Phase B
// step 1).
//
// # Why this is a SEPARATE call site from observeToolAfter, not a reuse of it
//
// internal/agent/observational.go's observeToolAfter is deliberately gated
// by observationalHooksActive(), which returns nil whenever
// `a.hooksManager != nil && !a.reqDisableHooks` — i.e. it is SILENT
// precisely when the full hook path is running, and its own doc comment
// records the residual hole: "--no-hooks still yields no observation,
// because it prevents the parent hooks manager from being built at all".
// That hole is exactly what PLAN.md §4 C1 was raised to close: "a hooks-
// derived collector cannot observe --no-hooks runs at all... the capture
// point for the ledger must be independent of the hooks manager."
//
// recordBenchEffects below therefore reads NEITHER a.hooksManager NOR
// a.reqDisableHooks NOR a.observationalHooks. Its only precondition is the
// package-level bench gate (internal/bench.ObservationalHooksEnabled,
// SWARM_BENCH_OBSERVE / SetObservationalHooksEnabled — the SAME gate G3
// already built; this file does not invent a second switch). That gate has
// no relationship to hooks at all, so this call site fires identically
// whether the turn was run with hooks on, with hooks off
// (ExecuteRequest.DisableHooks / the CLI's --no-hooks), or on an agent that
// never had a HooksManager attached in the first place (a sub-agent).
// TestRecordBenchEffects_SurvivesNoHooks in bench_capture_test.go is the
// acceptance proof.
//
// # Why this never blocks, mutates, or errors into the agent path
//
// Every step here is either a pure in-memory field read (agent state already
// held under a's own mutex) or a call into internal/bench.Record, which is
// itself gate-checked, non-blocking (a full channel drops and counts, never
// waits), and panic-safe. This function ALSO wraps its own body in a
// recover, so even a bug in this file's own field extraction — a bad type
// assertion, a nil map access — cannot reach the caller. Nothing here
// returns a value the caller could act on, so there is no channel through
// which this feature could steer, block, or fail a tool call even if every
// one of those defenses had a bug in it.
func (a *Agent) recordBenchEffects(ctx context.Context, toolName string, result any, execErr error) {
	defer recoverBenchCapture()
	if !bench.ObservationalHooksEnabled() {
		return
	}

	outcome := tools.NamedOutcomeOf(toolName, result)
	if outcome == nil || len(outcome.Effects) == 0 {
		// No structured file effects reported (read-only tool, a tool that
		// has not been lifted onto typed outcomes yet, or the call did not
		// reach a tool at all). Nothing to record — absence of a typed
		// outcome is not evidence of anything, per the same rule PLAN.md §3
		// applies to verdicts: missing instrumentation, not missing effect.
		return
	}

	a.mu.RLock()
	agentID := ""
	if a.definition != nil {
		agentID = a.definition.ID
	}
	conversationID := a.conversationID
	workspacePath := a.workspacePath
	if workspacePath == "" {
		workspacePath = a.storagePath
	}
	a.mu.RUnlock()

	sessionID := conversation.ProcessSessionID()
	ts := time.Now().UTC()

	for _, fe := range outcome.Effects {
		bench.Record(bench.Effect{
			Path:           fe.Path,
			Op:             string(fe.Op),
			PreBlob:        fe.PreBlobSHA1,
			PostBlob:       fe.PostBlobSHA1,
			Tool:           toolName,
			SessionID:      sessionID,
			AgentID:        agentID,
			ConversationID: conversationID,
			TS:             ts,
		}, workspacePath, fe.PostBlobSHA1 == "" && fe.Op != "delete")
	}
}

// recoverBenchCapture contains a panic originating anywhere in effect
// capture so it can never end the agent's turn. Mirrors
// internal/agent/observational.go:recoverObservation — the panic is
// swallowed, not logged, for the same reason: this runs inline on the
// tool-call path (the enqueue itself is O(1) and non-blocking, but the
// surrounding field extraction still executes synchronously), a.logger may
// itself be implicated in a hypothetical panic, and a measurement subsystem
// that can spam the agent's log is a measurement subsystem that can change
// the run it is supposed to be observing only from the outside.
func recoverBenchCapture() {
	_ = recover()
}
