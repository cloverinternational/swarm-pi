// Harness Phase 9d — HOST RUNTIME BINDINGS + FAIL-CLOSED PREFLIGHT.
//
// plan.md §3.3, verbatim:
//
//	"The harness plan declares requirements; the active interface adapter
//	 supplies implementations. Compilation can succeed with unresolved runtime
//	 requirements, but preflight must fail before execution if the selected
//	 interface cannot supply a required binding."
//
// Phase 9b/9c built the DECLARATION half: `Plan.RequiredRuntimeBindings()`
// returns a deduped, canonically ordered []harness.RuntimeBindingRequirement,
// each naming a host capability the plan needs, every field path that needs it,
// and the reason the in-tree runtime cannot supply it. This file builds the
// SUPPLY + REFUSAL half:
//
//   - D1. One NARROW interface per binding kind (no god-interface, no
//     map[string]any, no interface{} payloads), plus HarnessBindings — the
//     registry a host populates — and client.WithHarnessBindings to supply it.
//     harnessBindingSlots is the single, exhaustive kind -> interface table, so
//     the mapping is data and is directly testable.
//   - D2. preflightHarnessBindings is a real, explicit, FAIL-CLOSED step that
//     runs BEFORE execution and NEVER at compile time. A manifest that compiled
//     before this slice still compiles; it is the RUN that is refused. The
//     error names every unmet binding AND its exact RequiredBy field paths.
//     There is deliberately NO bypass option, flag, or environment escape: the
//     only way to satisfy preflight is to supply the binding or change the
//     manifest.
//   - D3. NEVER fake a binding. There is no ambient default, no zero-value
//     fallback, and no no-op implementation anywhere in this file. An
//     unsupplied binding is MISSING, and missing means refuse. Reference
//     implementations exist ONLY for the two capabilities the existing runtime
//     can honestly satisfy (SystemZonedClock over time.LoadLocation;
//     FileHarnessOccurrenceStore over a JSON file in the workspace's .swarm
//     directory) and both must be constructed EXPLICITLY by a host — neither is
//     ever installed implicitly.
//   - D4. Preflight runs at the ONE chokepoint every execution path crosses
//     (Client.initHarnessAgent, harness_plan.go), plus an early, atomic call in
//     newClientFromHarness. See harnessPreflightChokepoints (below) for the
//     enumeration and the proof there is no private bypass.
//
// UNKNOWN KINDS FAIL CLOSED. harnessBindingSlots covers the eleven
// scheduler/approval kinds Phase 9b declares. Phase 9c added six WORKFLOW kinds
// (harness.BindingWorkflowEngine and friends) whose host interfaces are Phase 10
// scope and are deliberately NOT invented here. A requirement whose kind has no
// slot is reported as UNSATISFIABLE rather than silently ignored, so the newer
// vocabulary can never sneak past preflight as an unrecognised string.
//
// REDACTION. Nothing in this file ever formats a credential value, a system
// prompt, or a schedule payload. Preflight errors quote only: the binding kind,
// the plan FIELD PATHS that require it (e.g. "schedules[0].timezone"), and the
// compile-time reason string authored in harness/schedules.go. A ScheduleSpec
// deliberately carries no prompt (harness/schedules.go:515-517), so there is no
// payload here to leak in the first place.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// harnessPreflightChokepoints documents, as code-adjacent prose, EVERY path in
// this repository that can execute a compiled harness plan, and where each one
// crosses preflight. It is referenced by TestHarnessPreflightNoBypass, which
// re-derives the call graph rather than trusting this comment.
//
//	(1) interactive / TUI
//	    swarm-tui/internal/chat/sdk_integration.go:2280
//	      -> sdkclient.New(sdkclient.WithHarnessPlan(...))
//	      -> client.New (client.go:922) -> newClientFromHarness
//	      -> PREFLIGHT (harness_plan.go step 1b) -> initHarnessAgent
//	      -> PREFLIGHT (harness_plan.go, first statement).
//	(2) print / `swarm -p` CLI
//	    swarm-tui/cmd/swarmos/harness_cli.go:131
//	      -> client.WithHarnessPlan(...) -> client.New -> as (1).
//	(3) live re-apply (hot reload, TUI harness editor, WatchHarness)
//	    client/harness_apply.go:211 (and the :214 rollback)
//	      -> Client.initHarnessAgent -> PREFLIGHT (first statement, BEFORE the
//	      first mutation, which is the mcpManager teardown).
//	    client/harness_reload.go:101 -> ApplyHarnessPlan -> as above.
//	    swarm-tui/internal/chat/harness_editor.go:318 -> ApplyHarnessPlan.
//	(4) daemon / unattended
//	    cmd/swarm-agent-daemon, cmd/headless and cmd/swarm-gateway have NO
//	    harness-plan path today: `grep -rn WithHarnessPlan` finds no reference in
//	    any of them, so there is no separate daemon adapter that could skip
//	    preflight. Any future daemon MUST construct through client.New or
//	    ApplyHarnessPlan — both of which route through initHarnessAgent — or call
//	    the exported PreflightHarnessPlan directly before dispatching.
//
// initHarnessAgent has exactly two call sites in the whole tree
// (harness_plan.go:245 and harness_apply.go:211/:214), and newClientFromHarness
// exactly one (client.go:923). There is no third door.
const harnessPreflightChokepoints = "client.New -> newClientFromHarness -> initHarnessAgent; " +
	"Client.ApplyHarnessPlan -> initHarnessAgent"

// --- D1: one narrow interface per binding kind ------------------------------

// HarnessSchedulerZonedClock supplies harness.BindingSchedulerZonedClock: cron
// evaluation in an EXPLICITLY NAMED IANA zone rather than the process's local
// zone. The in-tree scheduler parses with cron.ParseStandard against the local
// clock and ScheduledTask has no zone field, so a declared `timezone:` is inert
// without this.
type HarnessSchedulerZonedClock interface {
	// NowInZone returns the current instant located in the named IANA zone.
	// An unloadable zone MUST be an error, never a silent fall back to Local.
	NowInZone(zone string) (time.Time, error)
	// NextAfter returns the next firing instant of a 5-field cron expression
	// strictly after `after`, evaluated in the named IANA zone.
	NextAfter(zone, cronExpr string, after time.Time) (time.Time, error)
}

// HarnessScheduleOwnership is the lease returned by HarnessSchedulerSingleOwner.
// Owned() reports whether THIS process won the schedule; a false Owned() means
// another process holds it and this one must not fire.
type HarnessScheduleOwnership interface {
	Owned() bool
	Release(ctx context.Context) error
}

// HarnessSchedulerSingleOwner supplies harness.BindingSchedulerSingleOwner:
// leader/lock ownership so exactly ONE process fires a given schedule. Required
// unconditionally by the mere existence of a schedule, because the in-tree
// scheduler has no election, no file lock, and no PID record, so two processes
// sharing a workDir both fire every occurrence.
type HarnessSchedulerSingleOwner interface {
	AcquireScheduleOwnership(ctx context.Context, scheduleID string) (HarnessScheduleOwnership, error)
}

// HarnessSchedulerOverlapGuard supplies harness.BindingSchedulerOverlapGuard: an
// overlap decision applied IDENTICALLY on every dispatch path. The in-tree guard
// is path-dependent (consulted for every occurrence, populated only on the
// background-agent path), which is why every overlap value requires this.
type HarnessSchedulerOverlapGuard interface {
	// BeginOccurrence reports whether occurrenceID may start now given the
	// schedule's declared overlap policy. It must be path-independent.
	BeginOccurrence(ctx context.Context, scheduleID, occurrenceID string) (admitted bool, err error)
	// EndOccurrence releases the guard for a finished occurrence.
	EndOccurrence(ctx context.Context, scheduleID, occurrenceID string) error
}

// HarnessSchedulerMisfireDetector supplies
// harness.BindingSchedulerMisfireDetector: knowing which occurrences were due
// while the process was not running. The in-tree scheduler advances NextFireAt
// past a missed occurrence, so a miss leaves no trace to detect.
type HarnessSchedulerMisfireDetector interface {
	// MissedOccurrences returns the due instants of scheduleID in (since, now]
	// that never fired, oldest first.
	MissedOccurrences(ctx context.Context, scheduleID string, since, now time.Time) ([]time.Time, error)
}

// HarnessOccurrenceRecord is one durable occurrence-history entry. It carries
// NO prompt body and NO payload by construction: a harness schedule names a
// target whose prompt is declared and hashed elsewhere (harness/schedules.go
// :515-517). Outcome is a short, redaction-safe status label.
type HarnessOccurrenceRecord struct {
	ScheduleID   string    `json:"scheduleId"`
	OccurrenceID string    `json:"occurrenceId"`
	DueAt        time.Time `json:"dueAt"`
	FiredAt      time.Time `json:"firedAt"`
	Outcome      string    `json:"outcome"`
}

// HarnessSchedulerDurableStore supplies harness.BindingSchedulerDurableStore:
// per-occurrence history that survives a restart. Note that the SCHEDULE itself
// does not need this — a manifest-declared schedule survives because the
// manifest IS the state — which is exactly why Phase 9b makes this required only
// by the misfire policies that READ occurrence history.
type HarnessSchedulerDurableStore interface {
	// RecordOccurrence durably persists one occurrence. A write failure MUST be
	// returned, never swallowed: a store that silently drops history is
	// indistinguishable from having no store.
	RecordOccurrence(ctx context.Context, rec HarnessOccurrenceRecord) error
	// LastOccurrence returns the most recent record for scheduleID. found=false
	// means "no history"; a corrupt or unreadable store MUST return an error
	// rather than found=false, so corruption cannot masquerade as a fresh start.
	LastOccurrence(ctx context.Context, scheduleID string) (rec HarnessOccurrenceRecord, found bool, err error)
}

// HarnessSchedulerRetryDriver supplies harness.BindingSchedulerRetryDriver:
// re-attempting a failed occurrence. Both in-tree dispatch failure paths log and
// return, dropping the occurrence with no attempt counter anywhere.
type HarnessSchedulerRetryDriver interface {
	// NextRetry reports whether attempt number `attempt` (1-based, of an already
	// failed occurrence) should be retried and after what delay.
	NextRetry(ctx context.Context, scheduleID string, attempt int) (delay time.Duration, retry bool, err error)
}

// HarnessOccurrenceSlot is the admission token returned by
// HarnessSchedulerConcurrencyLimiter. Admitted()==false means the declared
// bound is already saturated and the occurrence must not start.
type HarnessOccurrenceSlot interface {
	Admitted() bool
	Release(ctx context.Context) error
}

// HarnessSchedulerConcurrencyLimiter supplies
// harness.BindingSchedulerConcurrencyLimiter: a bound on simultaneous
// occurrences. Neither CronScheduler nor CronSchedulerConfig has any such bound.
type HarnessSchedulerConcurrencyLimiter interface {
	AcquireOccurrenceSlot(ctx context.Context, scheduleID string) (HarnessOccurrenceSlot, error)
}

// HarnessOccurrenceDispatch names WHAT an occurrence should run. Its fields
// mirror the plan's resolved schedule target exactly (harness.ScheduleTargetKind
// + the declared agents[]/profiles[]/workflows[] id) and nothing else: there is
// deliberately no prompt/payload field, because the harness schedule surface has
// none to carry.
type HarnessOccurrenceDispatch struct {
	ScheduleID   string
	OccurrenceID string
	DueAt        time.Time
	TargetKind   harness.ScheduleTargetKind
	TargetID     string
}

// HarnessSchedulerTargetDispatcher supplies
// harness.BindingSchedulerTargetDispatcher: starting a NAMED declared
// agent/profile/workflow. The in-tree scheduler can only enqueue into the host's
// ambient session or start a generic background agent; neither selects a
// declared entry.
type HarnessSchedulerTargetDispatcher interface {
	DispatchOccurrence(ctx context.Context, d HarnessOccurrenceDispatch) error
}

// HarnessOccurrenceResult is an occurrence outcome for delivery. Detail is a
// short, redaction-safe summary; implementations must not place transcript
// bodies or credentials in it.
type HarnessOccurrenceResult struct {
	ScheduleID   string
	OccurrenceID string
	Succeeded    bool
	Detail       string
}

// HarnessSchedulerResultSink supplies harness.BindingSchedulerResultSink:
// delivery of an occurrence's outcome to the declared destination. In-tree,
// completion is only logged and PromptSink is an INPUT sink, so no result is
// delivered anywhere.
//
// A no-op implementation of this interface would be a LIE of exactly the class
// Phase 8d exists to eliminate, which is why none is provided here.
type HarnessSchedulerResultSink interface {
	DeliverOccurrenceResult(ctx context.Context, res HarnessOccurrenceResult) error
}

// HarnessApprovalDecision is the closed outcome vocabulary of an unattended
// approval. There is no "defer to the UI" member on purpose: a decision that
// escalates to a TTY is precisely what the unattended posture forbids.
type HarnessApprovalDecision string

const (
	// HarnessApprovalApproved permits the requested operation.
	HarnessApprovalApproved HarnessApprovalDecision = "approved"
	// HarnessApprovalDenied refuses the requested operation.
	HarnessApprovalDenied HarnessApprovalDecision = "denied"
)

// HarnessApprovalAsk is one approval question posed without a TTY. Summary is a
// short, redaction-safe description; it is never a prompt body or a credential.
type HarnessApprovalAsk struct {
	// ScheduleID is the requesting schedule, or "" when not schedule-driven.
	ScheduleID string
	// ToolName is the catalog/runtime name of the operation seeking approval.
	ToolName string
	// Summary is a short, redaction-safe reason string.
	Summary string
}

// HarnessApprovalUnattendedResolver supplies
// harness.BindingApprovalResolver: a resolver that settles an approval WITHOUT a
// TTY, honouring the schedule's declared deny/autoApprove posture. Today an
// occurrence simply inherits whatever posture the hosting process installed and
// the scheduler passes no posture alongside the prompt.
type HarnessApprovalUnattendedResolver interface {
	ResolveUnattended(ctx context.Context, ask HarnessApprovalAsk) (HarnessApprovalDecision, error)
}

// HarnessApprovalBroker supplies harness.BindingApprovalBroker: an approval
// broker that can reach a decider OTHER than the TUI (the `broker` posture).
// Requiring it is what keeps a TUI prompt from being SILENTLY required in an
// unattended run.
type HarnessApprovalBroker interface {
	RequestApproval(ctx context.Context, ask HarnessApprovalAsk) (HarnessApprovalDecision, error)
}

// --- D1: the registry a host populates --------------------------------------

// HarnessBindings is the set of host runtime implementations supplied for a
// harness plan. Every field is a narrow interface and every field is OPTIONAL in
// the Go sense and MANDATORY in the preflight sense: a nil field is not a
// default, it is an absence, and preflight refuses to run any plan that requires
// an absent binding (D3).
//
// The zero value supplies nothing, which is the correct and safe default: a plan
// requiring zero bindings is unaffected, and a plan requiring any binding is
// refused rather than silently degraded.
type HarnessBindings struct {
	SchedulerZonedClock         HarnessSchedulerZonedClock
	SchedulerSingleOwner        HarnessSchedulerSingleOwner
	SchedulerOverlapGuard       HarnessSchedulerOverlapGuard
	SchedulerMisfireDetector    HarnessSchedulerMisfireDetector
	SchedulerDurableStore       HarnessSchedulerDurableStore
	SchedulerRetryDriver        HarnessSchedulerRetryDriver
	SchedulerConcurrencyLimiter HarnessSchedulerConcurrencyLimiter
	SchedulerTargetDispatcher   HarnessSchedulerTargetDispatcher
	SchedulerResultSink         HarnessSchedulerResultSink
	ApprovalUnattendedResolver  HarnessApprovalUnattendedResolver
	ApprovalBroker              HarnessApprovalBroker
}

// harnessBindingSlots is the SINGLE, exhaustive kind -> "is it supplied?" table.
// Keeping the mapping as data (rather than a switch scattered across call sites)
// is what makes "every declarable kind maps to exactly one interface" a
// testable property instead of a claim.
//
// A kind ABSENT from this table is not satisfiable by any HarnessBindings value
// and is therefore always reported unmet — see harnessBindingSupportedKinds and
// the unknown-kind branch of preflightHarnessBindings.
var harnessBindingSlots = map[harness.RuntimeBindingKind]func(HarnessBindings) bool{
	harness.BindingSchedulerZonedClock:         func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerZonedClock) },
	harness.BindingSchedulerSingleOwner:        func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerSingleOwner) },
	harness.BindingSchedulerOverlapGuard:       func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerOverlapGuard) },
	harness.BindingSchedulerMisfireDetector:    func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerMisfireDetector) },
	harness.BindingSchedulerDurableStore:       func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerDurableStore) },
	harness.BindingSchedulerRetryDriver:        func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerRetryDriver) },
	harness.BindingSchedulerConcurrencyLimiter: func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerConcurrencyLimiter) },
	harness.BindingSchedulerTargetDispatcher:   func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerTargetDispatcher) },
	harness.BindingSchedulerResultSink:         func(b HarnessBindings) bool { return harnessBindingPresent(b.SchedulerResultSink) },
	harness.BindingApprovalResolver:            func(b HarnessBindings) bool { return harnessBindingPresent(b.ApprovalUnattendedResolver) },
	harness.BindingApprovalBroker:              func(b HarnessBindings) bool { return harnessBindingPresent(b.ApprovalBroker) },
}

// harnessBindingPresent rejects both a nil interface and an interface containing
// a typed nil pointer/map/slice/function/channel. The latter compares non-nil as
// an interface but cannot provide a usable runtime implementation.
func harnessBindingPresent(binding any) bool {
	if binding == nil {
		return false
	}
	v := reflect.ValueOf(binding)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return !v.IsNil()
	default:
		return true
	}
}

// harnessBindingSupportedKinds returns the sorted kinds this client package can
// accept an implementation for. It exists for diagnostics and for the
// exhaustiveness test; it is NOT a promise that any of them is supplied.
func harnessBindingSupportedKinds() []harness.RuntimeBindingKind {
	out := make([]harness.RuntimeBindingKind, 0, len(harnessBindingSlots))
	for k := range harnessBindingSlots {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Supplied returns, sorted, the binding kinds this registry actually provides.
// It reads through harnessBindingSlots so it can never drift from preflight.
func (b HarnessBindings) Supplied() []harness.RuntimeBindingKind {
	out := make([]harness.RuntimeBindingKind, 0, len(harnessBindingSlots))
	for kind, present := range harnessBindingSlots {
		if present(b) {
			out = append(out, kind)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// --- D2: fail-closed preflight ----------------------------------------------

// ErrHarnessBindingUnsatisfied is the sentinel every preflight refusal wraps, so
// a host can branch on errors.Is without string matching. It is deliberately
// distinct from ErrHarnessRestartRequired: a restart adopts a plan later, a
// preflight refusal means the plan must not run AT ALL under this interface.
var ErrHarnessBindingUnsatisfied = errors.New("harness: preflight refused: required runtime binding not supplied")

// HarnessUnmetBinding is one refused requirement, carried structurally so a host
// can render it however it likes without parsing the message.
type HarnessUnmetBinding struct {
	// Binding is the required kind.
	Binding harness.RuntimeBindingKind
	// RequiredBy is the plan field paths that require it, in the plan's
	// deterministic order (e.g. "schedules[0].timezone").
	RequiredBy []string
	// Reason is the compile-time reason authored in the harness package: what
	// is MISSING today. Never a value, never a payload.
	Reason string
	// Unsupported is true when this client package defines no interface for the
	// kind at all (a newer harness vocabulary than this adapter knows). Such a
	// requirement can never be satisfied here and is refused on sight.
	Unsupported bool
}

// HarnessPreflightError is returned (wrapping ErrHarnessBindingUnsatisfied) when
// a plan requires at least one binding the selected interface cannot supply. It
// names EVERY unmet binding and EVERY requiring field path, so an operator can
// fix the manifest or the host without guessing.
type HarnessPreflightError struct {
	Unmet []HarnessUnmetBinding
}

// Error renders a deterministic, redaction-safe multi-requirement message.
func (e *HarnessPreflightError) Error() string {
	var sb strings.Builder
	sb.WriteString("harness: preflight refused: ")
	sb.WriteString(fmt.Sprintf("%d required runtime binding(s) not supplied by the selected interface", len(e.Unmet)))
	for _, u := range e.Unmet {
		sb.WriteString("\n  - ")
		sb.WriteString(string(u.Binding))
		if u.Unsupported {
			sb.WriteString(" [no client binding interface is defined for this kind]")
		}
		sb.WriteString("\n      requiredBy: ")
		sb.WriteString(strings.Join(u.RequiredBy, ", "))
		if u.Reason != "" {
			sb.WriteString("\n      reason: ")
			sb.WriteString(u.Reason)
		}
	}
	sb.WriteString("\n  supply the missing implementation(s) via client.WithHarnessBindings, or change the manifest so they are not required")
	return sb.String()
}

// Unwrap ties the typed error to the sentinel for errors.Is.
func (e *HarnessPreflightError) Unwrap() error { return ErrHarnessBindingUnsatisfied }

// PreflightHarnessPlan is the EXPORTED preflight gate, for a host (a daemon
// adapter above all) that wants to refuse a plan before it even constructs a
// client. It is the exact same function the internal chokepoints call, so a host
// using it gets byte-identical semantics and cannot drift.
//
// A nil plan is an error, not a pass. Zero required bindings is a no-op returning
// nil, and performs no allocation-visible work, no file read and no goroutine
// start (HARD INVARIANT).
func PreflightHarnessPlan(plan *harness.Plan, bindings HarnessBindings) error {
	return PreflightHarnessPlanWithOptions(plan, HarnessPreflightOptions{Bindings: bindings})
}

// PreflightHarnessPlanWithOptions applies the same binding and optional
// executable-consent gates used by client construction.
func PreflightHarnessPlanWithOptions(plan *harness.Plan, opts HarnessPreflightOptions) error {
	if plan == nil {
		return fmt.Errorf("harness: preflight: nil plan")
	}
	if err := preflightHarnessExecutableConsent(
		plan,
		opts.RequireExecutableConsent,
		opts.ExecutableConsentDigest,
	); err != nil {
		return err
	}
	return preflightHarnessBindings(plan, opts.Bindings)
}

// preflightHarnessBindings compares the plan's declared requirements against
// what the host supplied and refuses when any is unmet.
//
// FAIL-CLOSED, three ways:
//  1. a required kind with a nil slot   -> unmet;
//  2. a required kind with NO slot at all (a newer harness vocabulary, e.g. the
//     Phase 9c workflow bindings) -> unmet AND marked Unsupported, never
//     ignored as "unrecognised";
//  3. any unmet requirement at all      -> the whole run is refused; there is no
//     partial/best-effort mode and no bypass.
func preflightHarnessBindings(plan *harness.Plan, bindings HarnessBindings) error {
	required := plan.RequiredRuntimeBindings()
	if len(required) == 0 {
		// Zero requirements: byte-for-byte the pre-9d path. No work at all.
		return nil
	}

	var unmet []HarnessUnmetBinding
	for _, req := range required {
		slot, known := harnessBindingSlots[req.Binding]
		switch {
		case !known:
			unmet = append(unmet, HarnessUnmetBinding{
				Binding:     req.Binding,
				RequiredBy:  append([]string(nil), req.RequiredBy...),
				Reason:      req.Reason,
				Unsupported: true,
			})
		case !slot(bindings):
			unmet = append(unmet, HarnessUnmetBinding{
				Binding:    req.Binding,
				RequiredBy: append([]string(nil), req.RequiredBy...),
				Reason:     req.Reason,
			})
		}
	}
	if len(unmet) == 0 {
		return nil
	}
	return &HarnessPreflightError{Unmet: unmet}
}

// harnessSuppliedBindings reads the host-supplied binding set for this client.
//
// It deliberately reads c.opts.harness (the set installed at CONSTRUCTION) and
// not the per-apply harnessConstruction, because bindings are a property of the
// HOST PROCESS, not of a manifest: ApplyHarnessPlan builds a fresh
// harnessConstruction for the incoming plan (harness_apply.go:191) and a host
// does not re-supply implementations on every hot reload. Reading the
// construction-time set is what lets a hot re-apply of a binding-requiring plan
// succeed for a host that supplied them, while still refusing one that did not.
//
// A nil harness construction yields the zero registry — which supplies nothing,
// i.e. fails closed.
func (c *Client) harnessSuppliedBindings() HarnessBindings {
	if c == nil || c.opts.harness == nil {
		return HarnessBindings{}
	}
	return c.opts.harness.bindings
}

// --- D3: reference implementations, ONLY where the runtime is honest ---------
//
// IMPLEMENTED (3 of 11): two capabilities the existing runtime can satisfy
// directly, plus an explicit host-topology assertion for single-instance
// deployments.
//
// DELIBERATELY UNSUPPLIED (8 of 11) — there is no implementation anywhere in
// this package, and preflight refuses any plan that needs one:
//
//	scheduler.overlapGuard       the in-tree guard is path-dependent by
//	                             construction; a "guard" that works on one
//	                             dispatch path is worse than none.
//	scheduler.misfireDetector    nothing records that an occurrence was missed.
//	scheduler.retryDriver        no attempt counter survives a dispatch failure.
//	scheduler.concurrencyLimiter no counter to bound.
//	scheduler.targetDispatcher   nothing can start a NAMED declared entry.
//	scheduler.resultSink         no result-delivery mechanism exists; a no-op
//	                             sink would silently drop outcomes.
//	approval.unattendedResolver  the scheduler passes no posture with a prompt.
//	approval.broker              a non-TTY decider is a host capability.
//
// Each of those would require a runtime change to implement honestly. Shipping a
// stub for any of them would convert a loud preflight refusal into a silent lie.

// NewHostAssertedSingleInstanceScheduleOwner returns a host binding for
// deployments whose embedding process guarantees that exactly one
// scheduler-capable process owns the harness schedule domain. It performs no
// election, locking, or lease renewal. Supplying it is an explicit host trust
// decision and must never be inferred from a manifest, environment variable, or
// compatibility preset.
func NewHostAssertedSingleInstanceScheduleOwner() HarnessSchedulerSingleOwner {
	return hostAssertedSingleInstanceScheduleOwner{}
}

type hostAssertedSingleInstanceScheduleOwner struct{}

func (hostAssertedSingleInstanceScheduleOwner) AcquireScheduleOwnership(
	ctx context.Context,
	_ string,
) (HarnessScheduleOwnership, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return hostAssertedScheduleOwnership{}, nil
}

type hostAssertedScheduleOwnership struct{}

func (hostAssertedScheduleOwnership) Owned() bool { return true }

func (hostAssertedScheduleOwnership) Release(context.Context) error { return nil }

var (
	_ HarnessSchedulerSingleOwner = hostAssertedSingleInstanceScheduleOwner{}
	_ HarnessScheduleOwnership    = hostAssertedScheduleOwnership{}
)

// SystemZonedClock is an HONEST reference HarnessSchedulerZonedClock over the
// standard library: time.LoadLocation for the zone and a self-contained
// evaluation of the same 5-field standard cron grammar the runtime uses.
//
// It is honest because everything it claims is real: the zone is resolved
// explicitly (an unloadable zone is an error, never a fall back to Local) and
// the next-fire instant is computed IN that zone. It is constructed only by a
// host that asks for it; nothing installs it implicitly.
type SystemZonedClock struct{}

// NewSystemZonedClock returns the stdlib-backed zoned clock.
func NewSystemZonedClock() SystemZonedClock { return SystemZonedClock{} }

// NowInZone returns time.Now() located in the named IANA zone.
func (SystemZonedClock) NowInZone(zone string) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("harness: zoned clock: load zone %q: %w", zone, err)
	}
	return time.Now().In(loc), nil
}

// NextAfter evaluates a 5-field standard cron expression in the named zone and
// returns the first matching minute strictly after `after`.
//
// It scans forward minute by minute over a bounded horizon rather than solving
// the expression algebraically: the horizon (4 years of minutes) covers every
// expressible standard-cron recurrence including Feb 29, and a bounded scan
// cannot loop forever on an unsatisfiable expression — it returns an error,
// which is the fail-closed answer.
func (SystemZonedClock) NextAfter(zone, cronExpr string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, fmt.Errorf("harness: zoned clock: load zone %q: %w", zone, err)
	}
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("harness: zoned clock: cron expression must have 5 fields, got %d", len(fields))
	}
	sets := make([]map[int]bool, 5)
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, f := range fields {
		s, err := cronFieldSet(f, bounds[i][0], bounds[i][1])
		if err != nil {
			return time.Time{}, fmt.Errorf("harness: zoned clock: cron field %d: %w", i+1, err)
		}
		sets[i] = s
	}
	// Start at the next whole minute strictly after `after`, in-zone.
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	const horizonMinutes = 4 * 366 * 24 * 60
	for i := 0; i < horizonMinutes; i++ {
		if sets[0][t.Minute()] && sets[1][t.Hour()] && sets[2][t.Day()] &&
			sets[3][int(t.Month())] && sets[4][int(t.Weekday())] {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("harness: zoned clock: cron expression %q has no occurrence within 4 years", cronExpr)
}

// cronFieldSet expands one standard cron field ("*", "5", "1-3", "*/15",
// "1,3,5", "0-30/10") into the set of matching values within [min,max].
// Names (jan/mon) are deliberately unsupported here and rejected, because the
// harness compiler already validates shape and a silent misparse would be worse
// than a refusal.
func cronFieldSet(field string, min, max int) (map[int]bool, error) {
	out := make(map[int]bool)
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty term in %q", field)
		}
		step := 1
		if idx := strings.Index(part, "/"); idx >= 0 {
			var err error
			step, err = atoiRange(part[idx+1:], 1, max-min+1)
			if err != nil {
				return nil, fmt.Errorf("step in %q: %w", part, err)
			}
			part = part[:idx]
		}
		lo, hi := min, max
		if part != "*" {
			if idx := strings.Index(part, "-"); idx >= 0 {
				var err error
				if lo, err = atoiRange(part[:idx], min, max); err != nil {
					return nil, fmt.Errorf("range start in %q: %w", part, err)
				}
				if hi, err = atoiRange(part[idx+1:], min, max); err != nil {
					return nil, fmt.Errorf("range end in %q: %w", part, err)
				}
			} else {
				var err error
				if lo, err = atoiRange(part, min, max); err != nil {
					return nil, fmt.Errorf("value in %q: %w", part, err)
				}
				hi = lo
			}
		}
		if lo > hi {
			return nil, fmt.Errorf("inverted range in %q", field)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no values matched in %q", field)
	}
	return out, nil
}

// atoiRange parses a bounded non-negative integer, refusing anything outside
// [min,max] rather than clamping.
func atoiRange(s string, min, max int) (int, error) {
	if s == "" {
		return 0, errors.New("empty number")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", s)
		}
		n = n*10 + int(r-'0')
		if n > max {
			return 0, fmt.Errorf("%q out of range [%d,%d]", s, min, max)
		}
	}
	if n < min {
		return 0, fmt.Errorf("%q out of range [%d,%d]", s, min, max)
	}
	return n, nil
}

// FileHarnessOccurrenceStore is an HONEST reference
// HarnessSchedulerDurableStore: occurrence history in a JSON file under the
// workspace's `.swarm` directory, mirroring the persistence shape the in-tree
// scheduler already uses for durable tasks
// (internal/tools/builtin/cron_scheduler.go:623-653 persistTaskLocked, :572
// loadDurableTasks).
//
// It deliberately uses its OWN file (harness_schedule_occurrences.json) rather
// than the scheduler's scheduled_tasks.json: that file's schema is the
// scheduler-owned ScheduledTask record set, and co-mingling a different record
// type into it would corrupt the scheduler's own state — a data-loss bug wearing
// the costume of reuse.
//
// FAIL-CLOSED PROPERTIES (these are the point, and they are tested):
//   - an unwritable directory/file makes RecordOccurrence return an error; it
//     never reports success for history it did not persist;
//   - a corrupt/unparsable file makes LastOccurrence return an ERROR, not
//     found=false, so corruption cannot masquerade as "no history yet" and
//     silently re-fire or silently skip occurrences;
//   - writes are atomic (temp file + rename), so a crash mid-write leaves the
//     previous valid state rather than a truncated file.
type FileHarnessOccurrenceStore struct {
	path string
	mu   sync.Mutex
}

// NewFileHarnessOccurrenceStore returns a durable store rooted at
// <workspaceDir>/.swarm/harness_schedule_occurrences.json. It creates no
// directory and reads no file here: construction is pure, so a host that never
// dispatches an occurrence never touches the filesystem.
func NewFileHarnessOccurrenceStore(workspaceDir string) *FileHarnessOccurrenceStore {
	return &FileHarnessOccurrenceStore{
		path: filepath.Join(workspaceDir, ".swarm", "harness_schedule_occurrences.json"),
	}
}

// Path reports the backing file, for diagnostics and tests.
func (s *FileHarnessOccurrenceStore) Path() string { return s.path }

// RecordOccurrence appends one record, atomically. Any I/O or encoding failure
// is returned; nothing is swallowed.
func (s *FileHarnessOccurrenceStore) RecordOccurrence(_ context.Context, rec HarnessOccurrenceRecord) error {
	if strings.TrimSpace(rec.ScheduleID) == "" {
		return errors.New("harness: occurrence store: empty scheduleId")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, err := s.loadLocked()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	existing = append(existing, rec)

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("harness: occurrence store: create dir: %w", err)
	}
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("harness: occurrence store: encode: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("harness: occurrence store: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("harness: occurrence store: commit: %w", err)
	}
	return nil
}

// LastOccurrence returns the most recent record for scheduleID. A missing file
// is "no history" (found=false, nil error); an unreadable or CORRUPT file is an
// ERROR, never a silent fresh start.
func (s *FileHarnessOccurrenceStore) LastOccurrence(_ context.Context, scheduleID string) (HarnessOccurrenceRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	recs, err := s.loadLocked()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return HarnessOccurrenceRecord{}, false, nil
		}
		return HarnessOccurrenceRecord{}, false, err
	}
	var best HarnessOccurrenceRecord
	found := false
	for _, r := range recs {
		if r.ScheduleID != scheduleID {
			continue
		}
		if !found || r.FiredAt.After(best.FiredAt) {
			best, found = r, true
		}
	}
	return best, found, nil
}

// loadLocked reads and decodes the backing file. It returns a wrapped
// os.ErrNotExist when the file is absent and a DECODE ERROR when the file exists
// but is not valid history — the distinction the fail-closed contract rests on.
func (s *FileHarnessOccurrenceStore) loadLocked() ([]HarnessOccurrenceRecord, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("harness: occurrence store: %w", os.ErrNotExist)
		}
		return nil, fmt.Errorf("harness: occurrence store: read: %w", err)
	}
	var recs []HarnessOccurrenceRecord
	if err := json.Unmarshal(data, &recs); err != nil {
		return nil, fmt.Errorf("harness: occurrence store: corrupt history at %s: %w", s.path, err)
	}
	return recs, nil
}

// Compile-time proof that the two reference implementations really satisfy the
// interfaces they claim. If a signature drifts, the build breaks here rather
// than a host silently failing preflight.
var (
	_ HarnessSchedulerZonedClock   = SystemZonedClock{}
	_ HarnessSchedulerDurableStore = (*FileHarnessOccurrenceStore)(nil)
)
