// Harness Phase 11b client audit seam.
//
// Today (before this file) client/harness_*.go emits ZERO audit events, and
// client.Client has no auditor at all — the seam does not exist yet. This
// file adds it: the auditor lives on harnessConstruction (client/
// harness_option.go's WithHarnessAuditor), mirroring Phase 9d's
// HarnessBindings seam EXACTLY (see harnessSuppliedBindings in
// harness_bindings.go:494-499, and harnessSuppliedAuditor below, which is
// the same nil-safe accessor shape). client.go itself is untouched: it
// already has the one `harness *harnessConstruction` field
// (client.go:221 in the options struct) that both seams read through.
//
// # D4 — all four ApplyHarnessPlan outcomes, not just success
//
// ApplyHarnessPlan (harness_apply.go:1-25) is FORBIDDEN to edit in this
// slice (concurrently dirty / out of scope), so this file cannot add
// emission calls inside it directly. Instead, ApplyHarnessPlanAudited below
// is a thin wrapper that calls the real, unedited Client.ApplyHarnessPlan
// exactly once and classifies its (result, error) pair into exactly one of
// the four documented terminal outcomes, emitting the matching event BEFORE
// returning. Because the classification only ever reads ApplyHarnessPlan's
// own already-computed return values (never re-derives or second-guesses
// them), it cannot disagree with what ApplyHarnessPlan actually did.
//
// This wiring is real but PARTIAL: any caller that invokes
// Client.ApplyHarnessPlan directly, bypassing ApplyHarnessPlanAudited,
// produces no audit event. See the SUMMARY (D4) for the exact unwired call
// sites found in this tree.
//
// # D5 — plan identity reuses the existing digest
//
// Every emitted event's Details carries plan_digest = newPlan.Digest() —
// harness/explain.go's existing, secret-free-by-construction digest over the
// REDACTED report (see explain.go:205's computeDigest). No second digest is
// computed here, so an audit record can never disagree with the digest
// edit_diff.go / ApplyHarnessResult.Digest already exposes.
//
// # D6 — build identity
//
// Every emitted event's Details also carries the build identity from
// internal/version's Effective* accessors (version.go), so an audit record
// can always be traced back to exactly which SDK build produced it — see
// harnessBuildDetails.
package client

import (
	"context"
	"errors"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/version"
)

// Stable, dot-notation audit EventTypes for the harness system, all under a
// single "harness." prefix (D4). This is the complete, documented set for
// Phase 11b; do not invent ad hoc harness event type strings elsewhere.
const (
	// HarnessAuditEventApplied fires when ApplyHarnessPlan hot-applied a
	// plan change to a live client (Applied is non-empty, err == nil).
	// Outcome: success.
	HarnessAuditEventApplied = "harness.plan.applied"

	// HarnessAuditEventNoop fires when ApplyHarnessPlan found the proposed
	// plan's digest identical to the currently-applied plan — nothing to
	// change, nothing mutated. Outcome: success, distinguished from
	// HarnessAuditEventApplied by Action == "noop".
	HarnessAuditEventNoop = "harness.plan.noop"

	// HarnessAuditEventRejected fires when ApplyHarnessPlan refused a plan
	// outright: at least one FORBIDDEN field change, or a validation error
	// (unauthorized yolo, unbound tool, ...). Nothing was mutated. Outcome:
	// denied.
	HarnessAuditEventRejected = "harness.plan.rejected"

	// HarnessAuditEventRestartRequired fires when ApplyHarnessPlan
	// classified the change as valid but requiring a client restart
	// (provider, credential, workspace, storage, or presentation change).
	// Nothing was mutated; a future host must build a new client. Outcome:
	// failure — it is a refusal to apply in place.
	HarnessAuditEventRestartRequired = "harness.plan.restart_required"
)

// harnessAuditActor is the fixed Actor recorded on every harness audit
// event. ApplyHarnessPlan carries no notion of a calling user/principal
// today (adding one is a larger, separate change, out of scope here), so a
// stable system actor is used rather than fabricating an identity.
const harnessAuditActor = "system"

// harnessSuppliedAuditor reads the host-supplied auditor for this client,
// modelled EXACTLY on harnessSuppliedBindings (harness_bindings.go:494-499):
// nil-safe, and returns nil (never a synthesized no-op value) when no
// auditor was supplied at construction. A nil return is itself the correct,
// safe default — every emit call in this file already treats a nil auditor
// as a silent no-op.
func (c *Client) harnessSuppliedAuditor() observability.Auditor {
	if c == nil || c.opts.harness == nil {
		return nil
	}
	return c.opts.harness.auditor
}

// harnessBuildDetails returns the build-identity fields (D6) attached to
// every emitted harness audit event, so an audit record can always be
// traced back to exactly which SDK build produced it. It never returns
// fabricated data: EffectiveGitCommit/EffectiveBuildTime/EffectiveBuildDirty
// report "unknown"/false rather than guessing when the toolchain did not
// embed VCS info and no -ldflags override was supplied.
func harnessBuildDetails() map[string]any {
	return map[string]any{
		"build_version": version.Version,
		"build_commit":  version.EffectiveGitCommit(),
		"build_time":    version.EffectiveBuildTime(),
		"build_dirty":   version.EffectiveBuildDirty(),
	}
}

// emitHarnessAuditEvent records one harness audit event through auditor, if
// non-nil. It is unconditionally safe to call:
//
//   - a nil auditor is a silent no-op (checked first, before anything else
//     in this function can run);
//   - a panicking auditor.Record (or a panic anywhere else in this
//     function, e.g. a hostile plan accessor) is recovered and swallowed;
//   - a failing auditor.Record (non-nil error return) is swallowed.
//
// In every case, auditing NEVER changes control flow for the caller — this
// function deliberately has no return value, so there is nothing for a
// caller to even accidentally propagate.
func emitHarnessAuditEvent(ctx context.Context, auditor observability.Auditor, eventType, action string, outcome observability.AuditOutcome, plan *harness.Plan, extraDetails map[string]any) {
	if auditor == nil {
		return
	}
	defer func() {
		_ = recover()
	}()

	details := harnessBuildDetails()
	for k, v := range extraDetails {
		details[k] = v
	}

	var digest, name, apiVersion string
	if plan != nil {
		digest = plan.Digest()
		name = plan.Name()
		apiVersion = plan.APIVersion()
	}
	details["plan_digest"] = digest
	details["plan_name"] = name
	details["plan_api_version"] = apiVersion

	_ = auditor.Record(ctx, observability.AuditEvent{
		Timestamp: time.Now().UTC(),
		EventType: eventType,
		Actor:     harnessAuditActor,
		Action:    action,
		Resource:  name,
		Outcome:   outcome,
		Details:   details,
	})
}

// ApplyHarnessPlanAudited is the audited entry point for ApplyHarnessPlan
// (D4). It calls the real, unedited Client.ApplyHarnessPlan exactly once,
// then classifies the (result, error) pair into exactly one of the four
// documented terminal outcomes (harness_apply.go:1-25) and emits the
// matching event from the const block above before returning control to the
// caller:
//
//  1. no-op:            err == nil && len(result.Applied) == 0 -> Noop, success
//  2. hot-applied:       err == nil && len(result.Applied)  > 0 -> Applied, success
//  3. restart-required:  errors.Is(err, ErrHarnessRestartRequired)  -> RestartRequired, failure
//  4. forbidden/invalid: any other non-nil err                      -> Rejected, denied
//
// The special case of newPlan == nil (ApplyHarnessPlan's own input-validation
// guard, before any outcome is even classified) is deliberately NOT audited:
// there is no plan identity to attach an event to, and it is a caller
// programming error, not one of the four terminal outcomes.
//
// The plan identity attached to every event is newPlan (the PROPOSED plan)
// via Plan.Digest()/Name()/APIVersion() (D5) — never a second, independently
// computed digest.
// KNOWN GAP (discovered while wiring this, not asked about — see SUMMARY):
// a SUCCESSFUL hot apply replaces c.opts.harness wholesale with
// &harnessConstruction{plan: newPlan, allowYolo: opts.AllowYolo}
// (harness_apply.go:191), which does NOT carry forward the PRIOR
// harnessConstruction's bindings or auditor fields. That is why this
// function reads the auditor BEFORE calling ApplyHarnessPlan, not after:
// reading first means THIS call's own event is still emitted correctly even
// though c.opts.harness has been replaced by the time ApplyHarnessPlan
// returns. It does NOT fix the underlying loss: a SECOND ApplyHarnessPlan
// (or ApplyHarnessPlanAudited) call, made after a first successful hot
// apply, will see harnessSuppliedAuditor() == nil (and, pre-existing and
// equally real, harnessSuppliedBindings() == HarnessBindings{}) until the
// host supplies WithHarnessAuditor/WithHarnessBindings again on a fresh
// Client. This function cannot fix that without editing harness_apply.go,
// which is out of scope for this slice.
func (c *Client) ApplyHarnessPlanAudited(ctx context.Context, newPlan *harness.Plan, opts ApplyHarnessOptions) (ApplyHarnessResult, error) {
	auditor := c.harnessSuppliedAuditor()

	result, err := c.ApplyHarnessPlan(ctx, newPlan, opts)

	if newPlan == nil {
		return result, err
	}
	if auditor == nil {
		return result, err
	}

	details := map[string]any{
		"digest": result.Digest,
	}

	switch {
	case err == nil && len(result.Applied) > 0:
		details["applied"] = result.Applied
		emitHarnessAuditEvent(ctx, auditor, HarnessAuditEventApplied, "applied", observability.AuditOutcomeSuccess, newPlan, details)
	case err == nil:
		emitHarnessAuditEvent(ctx, auditor, HarnessAuditEventNoop, "noop", observability.AuditOutcomeSuccess, newPlan, details)
	case errors.Is(err, ErrHarnessRestartRequired):
		details["restartRequired"] = result.RestartRequired
		emitHarnessAuditEvent(ctx, auditor, HarnessAuditEventRestartRequired, "restart_required", observability.AuditOutcomeFailure, newPlan, details)
	default:
		details["forbidden"] = result.Forbidden
		emitHarnessAuditEvent(ctx, auditor, HarnessAuditEventRejected, "rejected", observability.AuditOutcomeDenied, newPlan, details)
	}

	return result, err
}
