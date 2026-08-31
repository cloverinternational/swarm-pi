package harness

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Phase 9b — declarative SCHEDULES.
//
// This file is DECLARATION + RESOLUTION ONLY. It parses, validates, classifies,
// and resolves schedule declarations; it NEVER constructs a scheduler, registers
// a cron expression, reads a clock (`time.Now` appears nowhere in this package),
// starts a goroutine, or writes a file. Wiring and preflight are Phase 9d.
//
// WHY EVERY DIMENSION IS WRITTEN DOWN. Phase 9's exit gate is "the same plan can
// run interactively or unattended; schedule state survives restart; no TUI
// interaction is silently required in unattended mode." A schedule that inherits
// a hidden default — the daemon's local timezone, an unguarded overlap path, a
// dropped retry — is precisely how an unattended run goes wrong at 3am, and an
// operator reading the manifest would have no way to discover it. So every
// operational dimension is REQUIRED per schedule (D1) and every one of them
// carries a compile-time EnforcementClass drawn from what the runtime actually
// does today (scheduleFacts below, each row citing its file:line evidence).
//
// TWO HONEST ROUTES FOR A DIMENSION THE RUNTIME DOES NOT HONOUR (D3), chosen per
// value rather than per dimension, because for a scheduler the truth is
// value-dependent: `misfire: drop` IS today's behaviour while `misfire:
// runImmediately` does not exist at all.
//
//   - ACKNOWLEDGEMENT (mirrors budgets.acknowledgeObserved, Phase 9a): used
//     where a PARTIAL, misleading implementation already exists and an author
//     could reasonably believe they are protected. Today that is exactly one
//     dimension: `overlap` (see the path-dependence note on scheduleFacts).
//   - REQUIRED RUNTIME BINDING: used where the capability is a HOST capability a
//     conforming interface could supply. plan.md §3.3: "Compilation can succeed
//     with unresolved runtime requirements, but preflight must fail before
//     execution if the selected interface cannot supply a required binding." So
//     a missing binding is NOT a compile error — it is recorded on the Plan,
//     enumerable via Plan.RequiredRuntimeBindings(), for Phase 9d preflight to
//     refuse to run without.
//
// A schedule deliberately carries NO PROMPT. It names a TARGET whose prompt is
// already declared, hashed, and redacted elsewhere in the plan, so this section
// adds ids, cron strings, zone names, enum values and counts to the plan surface
// and nothing else — no new tainted category enters Explain or the digest.

// EnforcementBindingRequired extends the Phase 9a EnforcementClass vocabulary
// rather than forking a parallel enum — one implementation of the concept, per
// the standing rule. Its DECLARATION now lives beside its siblings in budgets.go
// (Phase 9c D6: declaration site only; the name, the value, and every behaviour
// are unchanged), so EnforcementClass has exactly one declaration site.

// ScheduleDimension is the closed set of operational dimensions every schedule
// must declare. It is exactly plan.md Phase 9 action #6's list, and the list is
// closed so that "which knobs exist" is answerable from the type alone.
type ScheduleDimension string

const (
	// ScheduleDimTimezone is the required explicit IANA zone (D2).
	ScheduleDimTimezone ScheduleDimension = "timezone"
	// ScheduleDimOverlap is what happens when an occurrence is still running.
	ScheduleDimOverlap ScheduleDimension = "overlap"
	// ScheduleDimMisfire is what happens to an occurrence that was missed.
	ScheduleDimMisfire ScheduleDimension = "misfire"
	// ScheduleDimRetry is what happens when an occurrence fails.
	ScheduleDimRetry ScheduleDimension = "retry"
	// ScheduleDimConcurrency bounds simultaneously running occurrences.
	ScheduleDimConcurrency ScheduleDimension = "concurrency"
	// ScheduleDimTarget is what the occurrence runs (agent|profile|workflow).
	ScheduleDimTarget ScheduleDimension = "target"
	// ScheduleDimApprovalPosture is the unattended-safety posture (D5).
	ScheduleDimApprovalPosture ScheduleDimension = "approvalPosture"
	// ScheduleDimResultSink is where an occurrence's outcome is delivered.
	ScheduleDimResultSink ScheduleDimension = "resultSink"
)

// scheduleDimensionOrder is the canonical reporting order, independent of the
// order a manifest happens to write the keys in. A deterministic order keeps
// Explain and the digest stable.
var scheduleDimensionOrder = []ScheduleDimension{
	ScheduleDimTimezone,
	ScheduleDimOverlap,
	ScheduleDimMisfire,
	ScheduleDimRetry,
	ScheduleDimConcurrency,
	ScheduleDimTarget,
	ScheduleDimApprovalPosture,
	ScheduleDimResultSink,
}

// ScheduleOverlap is the closed overlap policy enum.
type ScheduleOverlap string

const (
	// ScheduleOverlapSkip drops the new occurrence while one is running.
	ScheduleOverlapSkip ScheduleOverlap = "skip"
	// ScheduleOverlapAllowConcurrent lets occurrences run simultaneously.
	ScheduleOverlapAllowConcurrent ScheduleOverlap = "allowConcurrent"
	// ScheduleOverlapQueue defers the new occurrence until the running one ends.
	ScheduleOverlapQueue ScheduleOverlap = "queue"
	// ScheduleOverlapCancelPrevious cancels the running occurrence and starts the
	// new one.
	ScheduleOverlapCancelPrevious ScheduleOverlap = "cancelPrevious"
)

// ScheduleMisfire is the closed missed-occurrence policy enum.
type ScheduleMisfire string

const (
	// ScheduleMisfireDrop discards every occurrence missed while down.
	ScheduleMisfireDrop ScheduleMisfire = "drop"
	// ScheduleMisfireRunImmediately runs ONE occurrence as soon as the miss is
	// noticed, regardless of how many were missed.
	ScheduleMisfireRunImmediately ScheduleMisfire = "runImmediately"
	// ScheduleMisfireBackfillAll replays every missed occurrence in order.
	ScheduleMisfireBackfillAll ScheduleMisfire = "backfillAll"
)

// ScheduleRetryPolicy is the closed failed-occurrence retry enum.
type ScheduleRetryPolicy string

const (
	// ScheduleRetryNone never re-attempts a failed occurrence.
	ScheduleRetryNone ScheduleRetryPolicy = "none"
	// ScheduleRetryFixedDelay re-attempts after a constant delay.
	ScheduleRetryFixedDelay ScheduleRetryPolicy = "fixedDelay"
	// ScheduleRetryExponentialBackoff re-attempts with a doubling delay.
	ScheduleRetryExponentialBackoff ScheduleRetryPolicy = "exponentialBackoff"
)

// ScheduleConcurrencyPolicy is the closed simultaneous-occurrence enum.
type ScheduleConcurrencyPolicy string

const (
	// ScheduleConcurrencyUnlimited places no bound on simultaneous occurrences.
	ScheduleConcurrencyUnlimited ScheduleConcurrencyPolicy = "unlimited"
	// ScheduleConcurrencyMax bounds simultaneous occurrences to `max`.
	ScheduleConcurrencyMax ScheduleConcurrencyPolicy = "maxConcurrent"
)

// ScheduleResultSinkKind is the closed outcome-delivery enum. It is deliberately
// id-based rather than URL/path-based: a named host sink cannot smuggle a
// credential (a webhook URL can) and needs no filesystem resolution, which keeps
// this slice free of I/O and keeps Explain free of a new tainted category.
type ScheduleResultSinkKind string

const (
	// ScheduleResultSinkDiscard delivers the outcome nowhere.
	ScheduleResultSinkDiscard ScheduleResultSinkKind = "discard"
	// ScheduleResultSinkNamed delivers the outcome to a host sink named by id.
	ScheduleResultSinkNamed ScheduleResultSinkKind = "named"
)

// ScheduleApprovalPosture is the closed unattended-approval enum (D5). The three
// variants are exactly the three materially different unattended behaviours:
// refuse, decide-without-a-human, or route to a non-TUI decider.
type ScheduleApprovalPosture string

const (
	// ScheduleApprovalDeny never prompts: anything requiring approval is DENIED
	// and the occurrence proceeds without it (or fails). Strictest.
	ScheduleApprovalDeny ScheduleApprovalPosture = "deny"
	// ScheduleApprovalBroker routes approval decisions to a non-interactive
	// broker. It REQUIRES that broker as a runtime binding, so preflight fails
	// when the selected interface cannot supply one — this is what stops a TUI
	// prompt being SILENTLY required in unattended mode.
	ScheduleApprovalBroker ScheduleApprovalPosture = "broker"
	// ScheduleApprovalAutoApprove auto-approves within the plan's existing
	// permission posture. Widest; legal only when the plan itself already
	// auto-approves (see checkPostureWidening).
	ScheduleApprovalAutoApprove ScheduleApprovalPosture = "autoApprove"
)

// ScheduleTargetKind is the closed set of things a schedule can start.
type ScheduleTargetKind string

const (
	// ScheduleTargetAgent references a declared agents[] id.
	ScheduleTargetAgent ScheduleTargetKind = "agent"
	// ScheduleTargetProfile references a declared profiles[] id.
	ScheduleTargetProfile ScheduleTargetKind = "profile"
	// ScheduleTargetWorkflow is a KNOWN but not-yet-supported kind (D4): it is
	// named in the enum so the diagnostic can say "not until Phase 9c" instead
	// of "unknown kind", and it is rejected rather than carried as a dangling
	// forward reference (see resolveScheduleTarget).
	ScheduleTargetWorkflow ScheduleTargetKind = "workflow"
)

// --- runtime bindings ------------------------------------------------------

// RuntimeBindingKind names one HOST capability a compiled plan requires but
// cannot itself supply. It exists because of plan.md §3.3: compilation may
// succeed with unresolved runtime requirements, but preflight MUST fail before
// execution if the selected interface cannot supply a required binding. Making
// the requirement an enumerable value on the Plan is what gives Phase 9d
// something exact to check instead of a prose expectation.
type RuntimeBindingKind string

const (
	// BindingSchedulerZonedClock: a scheduler that evaluates cron expressions in
	// an EXPLICITLY NAMED IANA zone rather than the process's local zone.
	BindingSchedulerZonedClock RuntimeBindingKind = "scheduler.zonedClock"
	// BindingSchedulerSingleOwner: single-owner/leader ownership, so exactly one
	// process fires a given schedule.
	BindingSchedulerSingleOwner RuntimeBindingKind = "scheduler.singleOwner"
	// BindingSchedulerOverlapGuard: an overlap guard that covers EVERY dispatch
	// path identically (see the path-dependence note on scheduleFacts).
	BindingSchedulerOverlapGuard RuntimeBindingKind = "scheduler.overlapGuard"
	// BindingSchedulerMisfireDetector: detection of occurrences missed while the
	// process was not running.
	BindingSchedulerMisfireDetector RuntimeBindingKind = "scheduler.misfireDetector"
	// BindingSchedulerDurableStore: occurrence state (last fired / what was
	// missed) that survives a restart.
	BindingSchedulerDurableStore RuntimeBindingKind = "scheduler.durableStore"
	// BindingSchedulerRetryDriver: re-attempting a failed occurrence.
	BindingSchedulerRetryDriver RuntimeBindingKind = "scheduler.retryDriver"
	// BindingSchedulerConcurrencyLimiter: a bound on simultaneous occurrences.
	BindingSchedulerConcurrencyLimiter RuntimeBindingKind = "scheduler.concurrencyLimiter"
	// BindingSchedulerTargetDispatcher: starting a NAMED declared agent/profile
	// rather than the host's ambient session or a generic background agent.
	BindingSchedulerTargetDispatcher RuntimeBindingKind = "scheduler.targetDispatcher"
	// BindingSchedulerResultSink: delivery of an occurrence's outcome to a
	// declared destination.
	BindingSchedulerResultSink RuntimeBindingKind = "scheduler.resultSink"
	// BindingApprovalResolver: a non-interactive resolver that can settle an
	// approval request without a TTY (deny / auto-approve postures).
	BindingApprovalResolver RuntimeBindingKind = "approval.unattendedResolver"
	// BindingApprovalBroker: an actual approval BROKER that can reach a decider
	// other than the TUI. Required by the `broker` posture (D5).
	BindingApprovalBroker RuntimeBindingKind = "approval.broker"
)

// runtimeBindingOrder is the canonical order bindings are reported in, so
// Explain and the digest do not depend on manifest ordering.
var runtimeBindingOrder = []RuntimeBindingKind{
	BindingSchedulerZonedClock,
	BindingSchedulerSingleOwner,
	BindingSchedulerOverlapGuard,
	BindingSchedulerMisfireDetector,
	BindingSchedulerDurableStore,
	BindingSchedulerRetryDriver,
	BindingSchedulerConcurrencyLimiter,
	BindingSchedulerTargetDispatcher,
	BindingSchedulerResultSink,
	BindingApprovalResolver,
	BindingApprovalBroker,
}

// runtimeBindingReasons explains, per binding, WHAT IS MISSING TODAY. The text
// is the operator-facing reason a preflight failure will quote, so it states the
// gap rather than the wish.
var runtimeBindingReasons = map[RuntimeBindingKind]string{
	BindingSchedulerZonedClock: "the in-tree scheduler has no per-schedule timezone: it parses with cron.ParseStandard " +
		"(internal/tools/builtin/cron_scheduler.go:254,281) and compares against the process's LOCAL clock, and ScheduledTask (:77) has no zone field",
	BindingSchedulerSingleOwner: "the in-tree scheduler has no leader election, file lock, or PID record; state is in memory plus a plain JSON file " +
		"(persistTaskLocked, cron_scheduler.go:623-653), so two processes sharing a workDir BOTH fire every occurrence",
	BindingSchedulerOverlapGuard: "the in-tree overlap guard is PATH-DEPENDENT: runningTasks is consulted at cron_scheduler.go:347-356 but populated ONLY on " +
		"the background-agent path (:436-438), so on the PromptSink path there is no overlap protection at all",
	BindingSchedulerMisfireDetector: "the in-tree scheduler cannot notice a miss: on claim it advances NextFireAt to schedule.Next(now) " +
		"(cron_scheduler.go:253-256), so a missed occurrence leaves no trace to detect",
	BindingSchedulerDurableStore: "occurrence state is persisted only for Durable=true tasks (cron_scheduler.go:623-653 / loadDurableTasks:572) and " +
		"ScheduleWakeup explicitly sets Durable:false (schedule_wakeup.go:151), so occurrence history does not reliably survive a restart",
	BindingSchedulerRetryDriver: "the in-tree scheduler never re-attempts: both dispatch failure paths log and return " +
		"(cron_scheduler.go:414 prompt_sink_failed, :443 agent_start_failed) and the occurrence is dropped",
	BindingSchedulerConcurrencyLimiter: "neither CronScheduler (cron_scheduler.go:91) nor CronSchedulerConfig (:112) has any concurrency bound, " +
		"global or per-schedule",
	BindingSchedulerTargetDispatcher: "the in-tree scheduler dispatches EITHER into the host's ambient session (PromptSink.EnqueuePrompt, cron_scheduler.go:72) " +
		"OR into a generic background agent (:430-445); neither selects a DECLARED agents[]/profiles[] entry",
	BindingSchedulerResultSink: "completion is only logged (waitForTaskCompletion, cron_scheduler.go:487-500); PromptSink (:72) is an INPUT sink " +
		"(EnqueuePrompt), so no result is delivered anywhere",
	BindingApprovalResolver: "the schedule has no say in approvals today: an occurrence inherits whatever posture the hosting process installed " +
		"(swarm-tui/cmd/swarmos/main.go:116,939-943), and the scheduler passes no posture alongside the prompt",
	BindingApprovalBroker: "an approval BROKER reachable without a TTY is a host binding the plan cannot supply; the capability catalog already records " +
		"the same rule for interactive.ask_user_question (\"missing broker must preflight-fail\", catalog.go:188)",
}

// --- the dimension/value -> enforcement table ------------------------------

// scheduleFact is one row of the hard-coded (dimension, value) -> what the
// runtime really does table. evidence is the file:line proof so the next person
// can re-verify cheaply instead of re-deriving; ackReason is the body of the
// diagnostic when requiresAck is set.
//
// Classification is per VALUE, not per dimension, because for a scheduler that
// is where the truth lives: `misfire: drop` is LITERALLY today's behaviour while
// `misfire: runImmediately` does not exist at all. Flattening them to one
// per-dimension class would have to lie about one of them.
type scheduleFact struct {
	class    EnforcementClass
	bindings []RuntimeBindingKind
	// requiresAck gates declaration behind an explicit per-dimension
	// acknowledgement, exactly like budgets.acknowledgeObserved (Phase 9a).
	requiresAck bool
	// pathDependent marks a value whose runtime behaviour differs by DISPATCH
	// PATH. It is reported in Explain because "it works" and "it works here"
	// are different claims and only one of them is true.
	pathDependent bool
	evidence      string
	ackReason     string
}

// dimValue keys the fact table. scheduleAnyValue is the wildcard used for a
// dimension whose value is not a closed enum (timezone: an IANA zone name).
type dimValue struct {
	dim   ScheduleDimension
	value string
}

const scheduleAnyValue = "*"

// overlapPathDependenceAck is shared by EVERY overlap value, and that is the
// point: the hazard is not attached to one policy, it is attached to the
// dimension. The runtime consults its overlap guard at cron_scheduler.go:347-356
// but populates runningTasks ONLY on the background-agent path (:436-438). So:
//
//   - on the PromptSink path the guard is structurally dead — `skip`, `queue`
//     and `cancelPrevious` all silently degrade to allowConcurrent;
//   - on the background-agent path the guard is an UNCONDITIONAL skip — so
//     `allowConcurrent`, `queue` and `cancelPrevious` all silently degrade to
//     skip.
//
// Every value is therefore wrong on one of the two paths, and which path a
// schedule takes is decided by the HOST (whether a PromptSink was configured),
// not by anything the manifest can see. That is why overlap is the one dimension
// that requires BOTH an acknowledgement (author-time visibility of a partial,
// misleading implementation) AND a runtime binding (preflight-time refusal
// unless the host supplies a path-independent guard).
const overlapPathDependenceAck = "overlap is PATH-DEPENDENT in the current runtime and is the one dimension where a PARTIAL guard already exists, " +
	"which is exactly what makes it dangerous: the guard is consulted for every occurrence (cron_scheduler.go:347-356) but runningTasks is populated " +
	"ONLY on the background-agent path (:436-438). On the PromptSink path the guard is structurally dead, so skip/queue/cancelPrevious silently degrade " +
	"to allowConcurrent; on the background-agent path the guard is an unconditional skip, so allowConcurrent/queue/cancelPrevious silently degrade to " +
	"skip. Which path is taken is decided by the host, not by this manifest. Declaring any overlap policy therefore requires acknowledging that the " +
	"declared policy is honoured only once a path-independent guard is bound: add " + quotedOverlapDim + " to this schedule's acknowledgeUnenforced"

// quotedOverlapDim keeps the long constant above readable.
const quotedOverlapDim = "\"overlap\""

// scheduleFacts is the hard-coded (dimension, value) -> runtime-truth table,
// verified against the tree at this commit. THIS TABLE IS THE POINT OF PHASE 9b:
// change a row only together with the runtime change that makes the new
// classification true — never as a manifest flag.
var scheduleFacts = map[dimValue]scheduleFact{
	// TIMEZONE — binding route. Not merely unhonoured: the concept is absent,
	// and it is a host capability a conforming scheduler can supply, so the
	// compile-vs-preflight rule says record a requirement, not reject.
	{ScheduleDimTimezone, scheduleAnyValue}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerZonedClock},
		evidence: "cron.ParseStandard (internal/tools/builtin/cron_scheduler.go:254,281,718) builds a zone-less schedule evaluated against the process's " +
			"LOCAL clock, and ScheduledTask (:77) has no timezone field, so a declared zone is not honoured by the in-tree scheduler",
	},

	// OVERLAP — binding route AND acknowledgement, for every value.
	{ScheduleDimOverlap, string(ScheduleOverlapSkip)}: {
		class:         EnforcementBindingRequired,
		bindings:      []RuntimeBindingKind{BindingSchedulerOverlapGuard},
		requiresAck:   true,
		pathDependent: true,
		evidence: "the runningTasks guard (cron_scheduler.go:347-356) implements skip, but runningTasks is written only on the background-agent path " +
			"(:436-438), so on the PromptSink path the guard never matches and skip does not happen",
		ackReason: overlapPathDependenceAck,
	},
	{ScheduleDimOverlap, string(ScheduleOverlapAllowConcurrent)}: {
		class:         EnforcementBindingRequired,
		bindings:      []RuntimeBindingKind{BindingSchedulerOverlapGuard},
		requiresAck:   true,
		pathDependent: true,
		evidence: "the same asymmetry with the opposite hazard: on the background-agent path the guard at cron_scheduler.go:347-356 SKIPS a due " +
			"occurrence, so a declared allowConcurrent is silently violated there",
		ackReason: overlapPathDependenceAck,
	},
	{ScheduleDimOverlap, string(ScheduleOverlapQueue)}: {
		class:         EnforcementBindingRequired,
		bindings:      []RuntimeBindingKind{BindingSchedulerOverlapGuard},
		requiresAck:   true,
		pathDependent: true,
		evidence: "no queue exists on either path: a blocked occurrence is returned from (cron_scheduler.go:347-356) and dropped, never deferred; on the " +
			"PromptSink path it is not even blocked",
		ackReason: overlapPathDependenceAck,
	},
	{ScheduleDimOverlap, string(ScheduleOverlapCancelPrevious)}: {
		class:         EnforcementBindingRequired,
		bindings:      []RuntimeBindingKind{BindingSchedulerOverlapGuard},
		requiresAck:   true,
		pathDependent: true,
		evidence: "no cancellation handle for a running occurrence is retained: runningTasks stores only an agent id (cron_scheduler.go:436-438) and " +
			"completion is awaited passively (waitForTaskCompletion:487-500)",
		ackReason: overlapPathDependenceAck,
	},

	// MISFIRE — `drop` is literally today's behaviour; the others need a
	// detector AND durable occurrence state to know what was missed.
	{ScheduleDimMisfire, string(ScheduleMisfireDrop)}: {
		class: BudgetEnforced,
		evidence: "on claim the scheduler advances NextFireAt to schedule.Next(now) (cron_scheduler.go:253-256), so every occurrence missed while the " +
			"process was down is silently discarded — declaring drop asks for exactly what already happens",
	},
	{ScheduleDimMisfire, string(ScheduleMisfireRunImmediately)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerMisfireDetector, BindingSchedulerDurableStore},
		evidence: "nothing records that an occurrence was missed (NextFireAt is advanced past it at cron_scheduler.go:253-256) and only Durable=true tasks " +
			"are written at all (persistTaskLocked:623-653, loadDurableTasks:572), so after a restart there is nothing to compare against",
	},
	{ScheduleDimMisfire, string(ScheduleMisfireBackfillAll)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerMisfireDetector, BindingSchedulerDurableStore},
		evidence: "replaying a backlog needs both the missed-occurrence record the runtime does not keep (cron_scheduler.go:253-256) and durable occurrence " +
			"state (persistTaskLocked:623-653), and without single-owner ownership two processes would replay the same backlog twice",
	},

	// RETRY — `none` is literally today's behaviour.
	{ScheduleDimRetry, string(ScheduleRetryNone)}: {
		class: BudgetEnforced,
		evidence: "both dispatch failure paths log and return (cron_scheduler.go:414 prompt_sink_failed, :443 agent_start_failed); the occurrence is " +
			"dropped and never re-attempted, so declaring none asks for exactly what already happens",
	},
	{ScheduleDimRetry, string(ScheduleRetryFixedDelay)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerRetryDriver},
		evidence: "no re-attempt mechanism exists: the failure paths at cron_scheduler.go:414 and :443 log and return with no attempt counter anywhere",
	},
	{ScheduleDimRetry, string(ScheduleRetryExponentialBackoff)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerRetryDriver},
		evidence: "as fixedDelay, plus a backoff schedule the runtime has no state to carry: nothing survives the failure paths at cron_scheduler.go:414,:443",
	},

	// CONCURRENCY — `unlimited` is literally today's behaviour.
	{ScheduleDimConcurrency, string(ScheduleConcurrencyUnlimited)}: {
		class: BudgetEnforced,
		evidence: "no concurrency bound exists anywhere in the scheduler: neither CronScheduler (cron_scheduler.go:91) nor CronSchedulerConfig (:112) has " +
			"such a field, so unlimited is exactly what already happens",
	},
	{ScheduleDimConcurrency, string(ScheduleConcurrencyMax)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerConcurrencyLimiter},
		evidence: "there is no counter to compare a maximum against (cron_scheduler.go:91,:112); runningTasks (:436-438) is a per-task presence map on one " +
			"dispatch path, not a concurrency count",
	},

	// TARGET — starting a NAMED declared agent/profile is a dispatcher the
	// in-tree scheduler does not have.
	{ScheduleDimTarget, string(ScheduleTargetAgent)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerTargetDispatcher},
		evidence: "dispatch is EITHER PromptSink.EnqueuePrompt into the host's ambient session (cron_scheduler.go:72,:358-372) OR a generic background agent " +
			"(:430-445); neither can select a declared agents[] entry",
	},
	{ScheduleDimTarget, string(ScheduleTargetProfile)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerTargetDispatcher},
		evidence: "same dispatch paths (cron_scheduler.go:72,:430-445): neither selects a declared profiles[] provider/model for the occurrence",
	},

	// APPROVAL POSTURE — the schedule has no say today on any path.
	{ScheduleDimApprovalPosture, string(ScheduleApprovalDeny)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingApprovalResolver},
		evidence: "an occurrence inherits whatever posture the hosting process installed (swarm-tui/cmd/swarmos/main.go:116,939-943 build the headless " +
			"broker from --approval-mode); the scheduler passes no posture with the prompt, so a schedule cannot impose deny by itself",
	},
	{ScheduleDimApprovalPosture, string(ScheduleApprovalAutoApprove)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingApprovalResolver},
		evidence: "same inheritance (swarm-tui/cmd/swarmos/main.go:116,939-943): the schedule cannot install an auto-approving resolver of its own",
	},
	{ScheduleDimApprovalPosture, string(ScheduleApprovalBroker)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingApprovalBroker},
		evidence: "a broker reachable without a TTY is a host binding; the catalog already states the rule for interactive.ask_user_question " +
			"(catalog.go:188, \"missing broker must preflight-fail\"), and requiring it here is what keeps a TUI prompt from being SILENTLY required",
	},

	// RESULT SINK — `discard` is literally today's behaviour.
	{ScheduleDimResultSink, string(ScheduleResultSinkDiscard)}: {
		class: BudgetEnforced,
		evidence: "completion is only logged (waitForTaskCompletion, cron_scheduler.go:487-500) and PromptSink (:72) is an INPUT sink (EnqueuePrompt), so no " +
			"outcome is delivered anywhere — discard is exactly what already happens",
	},
	{ScheduleDimResultSink, string(ScheduleResultSinkNamed)}: {
		class:    EnforcementBindingRequired,
		bindings: []RuntimeBindingKind{BindingSchedulerResultSink},
		evidence: "there is no result-delivery mechanism at all: waitForTaskCompletion (cron_scheduler.go:487-500) logs status/duration and returns",
	},
}

// ScheduleEnforcementOf returns the compile-time enforcement class for one
// (dimension, value) pair, exported so a consumer — or a human auditing a
// manifest — can ask exactly the question the compiler asks. ok is false for an
// unknown pair.
func ScheduleEnforcementOf(dim ScheduleDimension, value string) (EnforcementClass, bool) {
	if f, ok := scheduleFacts[dimValue{dim, value}]; ok {
		return f.class, true
	}
	if f, ok := scheduleFacts[dimValue{dim, scheduleAnyValue}]; ok {
		return f.class, true
	}
	return "", false
}

// --- schema ----------------------------------------------------------------

// ScheduleEntry is one declared entry under the v1alpha1 `schedules:` section.
//
// EVERY DIMENSION IS REQUIRED (D1) and every field is `omitempty`/a pointer so
// that "omitted" is distinguishable from "explicitly zero" and can be reported
// by name. An author may express "none"/"disabled" — `misfire: drop`,
// `retry: {policy: none}`, `concurrency: {policy: unlimited}`,
// `resultSink: {kind: discard}` — but must express it, because today every one
// of these silently inherits a default that is either absent or wrong and an
// unattended operator has no way to discover that from the manifest.
//
// There is deliberately NO prompt field: a schedule names a target whose prompt
// is already declared and hashed elsewhere, so schedules add no new tainted
// category to the plan surface.
type ScheduleEntry struct {
	// ID is the schedule's logical name; it must be unique and non-empty.
	ID string `json:"id,omitempty"`
	// Cron is a 5-field standard cron expression. Only the SHAPE is validated
	// here (validateCronShape); nothing is registered or evaluated.
	Cron string `json:"cron,omitempty"`
	// Timezone is a required explicit IANA zone name (D2). "Local" and bare
	// offsets are rejected; there is no inheritance of the daemon's zone.
	Timezone string `json:"timezone,omitempty"`
	// Overlap is what happens when the previous occurrence is still running.
	Overlap ScheduleOverlap `json:"overlap,omitempty"`
	// Misfire is what happens to an occurrence missed while the process was down.
	Misfire ScheduleMisfire `json:"misfire,omitempty"`
	// Retry is the failed-occurrence policy (an object so "none" is explicit).
	Retry *ScheduleRetryEntry `json:"retry,omitempty"`
	// Concurrency bounds simultaneously running occurrences.
	Concurrency *ScheduleConcurrencyEntry `json:"concurrency,omitempty"`
	// Target is what the occurrence runs.
	Target *ScheduleTargetEntry `json:"target,omitempty"`
	// ApprovalPosture is the unattended-approval posture (D5). It may never
	// widen the document's permissions (checkPostureWidening).
	ApprovalPosture ScheduleApprovalPosture `json:"approvalPosture,omitempty"`
	// ResultSink is where the occurrence's outcome is delivered.
	ResultSink *ScheduleResultSinkEntry `json:"resultSink,omitempty"`
	// AcknowledgeUnenforced is the explicit, per-DIMENSION acknowledgement
	// required to declare a value the runtime does not honour as written. It
	// mirrors budgets.acknowledgeObserved (Phase 9a) rule for rule:
	//
	//   - it is a list of EXACT dimension names and nothing else: no blanket
	//     boolean, no wildcard, so "acknowledge everything" is unexpressible by
	//     construction and the posture is only ever satisfied one dimension at
	//     a time;
	//   - a dimension whose DECLARED VALUE does not require acknowledgement is
	//     an ERROR (a dangling acknowledgement would silently pre-authorize a
	//     future edit that changes the value to one that does);
	//   - an unknown or duplicated dimension name is an ERROR.
	//
	// It lives per-SCHEDULE rather than document-wide because whether an
	// acknowledgement is needed depends on the VALUE this schedule declares.
	// Acknowledging does NOT make a dimension enforced: Explain still reports
	// its true class.
	AcknowledgeUnenforced []ScheduleDimension `json:"acknowledgeUnenforced,omitempty"`
}

// ScheduleRetryEntry is the declared retry policy. MaxAttempts/DelaySeconds are
// required for a real policy and must be absent for `none`, so a manifest can
// never carry dead numbers that look effective.
type ScheduleRetryEntry struct {
	Policy       ScheduleRetryPolicy `json:"policy,omitempty"`
	MaxAttempts  int                 `json:"maxAttempts,omitempty"`
	DelaySeconds int                 `json:"delaySeconds,omitempty"`
}

// ScheduleConcurrencyEntry is the declared concurrency policy. Max is required
// for `maxConcurrent` and must be absent for `unlimited`.
type ScheduleConcurrencyEntry struct {
	Policy ScheduleConcurrencyPolicy `json:"policy,omitempty"`
	Max    int                       `json:"max,omitempty"`
}

// ScheduleTargetEntry names what the occurrence runs. ID must resolve against
// the plan's declared agents[]/profiles[] (D4) — it is a REFERENCE, not a host
// binding, so an unknown id fails at COMPILE time like a Phase 8a delegate.
type ScheduleTargetEntry struct {
	Kind ScheduleTargetKind `json:"kind,omitempty"`
	ID   string             `json:"id,omitempty"`
}

// ScheduleResultSinkEntry names where the outcome goes. ID is required for
// `named` and must be absent for `discard`. It is an opaque host sink NAME, not
// a URL or path: no I/O, no credential surface (see ScheduleResultSinkKind).
type ScheduleResultSinkEntry struct {
	Kind ScheduleResultSinkKind `json:"kind,omitempty"`
	ID   string                 `json:"id,omitempty"`
}

// ScheduleDimensionSpec is one RESOLVED dimension of one schedule, as carried on
// the immutable Plan and reported by Explain. Every field is a non-secret id,
// enum value, or number.
type ScheduleDimensionSpec struct {
	Dimension ScheduleDimension `json:"dimension"`
	// Value is the declared enum value, or the IANA zone name for timezone.
	Value string `json:"value"`
	// Enforcement is the compile-time truth about this dimension AS DECLARED.
	Enforcement EnforcementClass `json:"enforcement"`
	// PathDependent marks a value whose honoured behaviour differs by dispatch
	// path in the current runtime (today: every overlap value).
	PathDependent bool `json:"pathDependent,omitempty"`
	// Acknowledged records that an acknowledgement-gated value was declared
	// knowingly. It never changes Enforcement.
	Acknowledged bool `json:"acknowledged,omitempty"`
	// Bindings are the runtime bindings this dimension requires, if any.
	Bindings []RuntimeBindingKind `json:"bindings,omitempty"`
	// Ref is the referenced id for dimensions that name something: the target
	// agent/profile id, or the result sink id.
	Ref string `json:"ref,omitempty"`
	// The remaining fields are the dimension's declared numbers, reported
	// individually rather than as free text so they stay auditable.
	MaxAttempts   int `json:"maxAttempts,omitempty"`
	DelaySeconds  int `json:"delaySeconds,omitempty"`
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
}

// ScheduleSpec is one resolved schedule carried on the immutable Plan. Every
// one of the eight dimensions is present in Dimensions, in canonical order, so
// an operator reading a report sees the complete operational posture and its
// enforcement class rather than only the interesting parts.
type ScheduleSpec struct {
	ID   string `json:"id"`
	Cron string `json:"cron"`
	// Dimensions holds all eight resolved dimensions in scheduleDimensionOrder.
	Dimensions []ScheduleDimensionSpec `json:"dimensions"`
	// Bindings is the deduplicated union of every dimension's required runtime
	// bindings PLUS the unconditional per-schedule ones.
	Bindings []RuntimeBindingKind `json:"bindings,omitempty"`
}

// Dimension returns one resolved dimension of the schedule. ok is false only for
// a dimension name outside the closed set.
func (s ScheduleSpec) Dimension(dim ScheduleDimension) (ScheduleDimensionSpec, bool) {
	for _, d := range s.Dimensions {
		if d.Dimension == dim {
			return d, true
		}
	}
	return ScheduleDimensionSpec{}, false
}

// RuntimeBindingRequirement is one host capability the compiled plan REQUIRES
// but cannot supply, together with every plan field that requires it. Phase 9d
// preflight enumerates these and must refuse to execute when the selected
// interface cannot supply one (plan.md §3.3).
type RuntimeBindingRequirement struct {
	Binding RuntimeBindingKind `json:"binding"`
	// RequiredBy lists the requiring field paths in deterministic encounter
	// order, for example "schedules[0].timezone".
	RequiredBy []string `json:"requiredBy"`
	// Reason states what is MISSING today, so a preflight failure can quote it.
	Reason string `json:"reason"`
}

// --- cron SHAPE validation (no dependency, deliberately a strict subset) ----

// cronFieldBound describes one of the five standard cron fields.
type cronFieldBound struct {
	name  string
	min   int
	max   int
	names map[string]int
}

// cronStandardFields mirrors robfig/cron/v3's standard 5-field grammar
// (spec.go:23-26,40: minutes 0-59, hours 0-23, dom 1-31, months 1-12 with
// names, dow 0-6 with names), which is the parser the runtime uses via
// cron.ParseStandard. It is duplicated as DATA here on purpose: importing
// robfig/cron into `harness` would add a dependency to a package whose whole
// contract is purity (yaml.v3/configformat/stdlib only), and a 5-field bounds
// table is not a dependency's worth of logic.
var cronStandardFields = []cronFieldBound{
	{"minute", 0, 59, nil},
	{"hour", 0, 23, nil},
	{"day-of-month", 1, 31, nil},
	{"month", 1, 12, map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}},
	{"day-of-week", 0, 6, map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}},
}

// validateCronShape validates the SHAPE of a cron expression and nothing else:
// it never builds a schedule, never computes a next fire time, and never reads
// a clock. It returns a human reason when the expression is rejected.
//
// THE SAFETY PROPERTY: this validator accepts a STRICT SUBSET of what
// cron.ParseStandard accepts, never a superset. Anything accepted here parses at
// runtime; two constructs the standard parser WOULD accept are refused here on
// purpose:
//
//   - a "TZ="/"CRON_TZ=" prefix (robfig parser.go:95). A zone inside the cron
//     string would be a SECOND source of truth for the timezone dimension, and
//     Phase 9a already established that the same bound declared twice is a bug
//     waiting for the next edit. The zone belongs in `timezone:`, which is
//     required and validated.
//   - "@"-descriptors (@daily, @every 1h). One shape keeps every schedule
//     comparable, and @every has no zone semantics at all, which would make the
//     required timezone dimension meaningless for those entries.
func validateCronShape(expr string) (string, bool) {
	if strings.TrimSpace(expr) == "" {
		return "cron expression is required and must be a 5-field standard expression (minute hour day-of-month month day-of-week)", false
	}
	if expr != strings.TrimSpace(expr) {
		return "cron expression must not have leading or trailing whitespace", false
	}
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return "cron expression must not carry a TZ=/CRON_TZ= prefix; the zone is declared once in the required `timezone` dimension, " +
			"and accepting it in two places would make one of them a lie", false
	}
	if strings.HasPrefix(expr, "@") {
		return "cron descriptors (@daily, @hourly, @every ...) are not accepted; use the 5-field form " +
			"(minute hour day-of-month month day-of-week) so every schedule is comparable and the declared timezone is meaningful", false
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return "cron expression must have exactly 5 fields (minute hour day-of-month month day-of-week), got " + strconv.Itoa(len(fields)), false
	}
	for i, f := range fields {
		if reason, ok := validateCronField(f, cronStandardFields[i]); !ok {
			return "cron " + cronStandardFields[i].name + " field " + quote(f) + " is invalid: " + reason, false
		}
	}
	return "", true
}

// validateCronField validates one comma-separated field against its bounds.
func validateCronField(field string, b cronFieldBound) (string, bool) {
	if field == "" {
		return "empty field", false
	}
	for _, part := range strings.Split(field, ",") {
		if reason, ok := validateCronRange(part, b); !ok {
			return reason, false
		}
	}
	return "", true
}

// validateCronRange validates one range item: "*" | "?" | n | n-m, each with an
// optional "/step" suffix (robfig getRange, parser.go:250-300).
func validateCronRange(expr string, b cronFieldBound) (string, bool) {
	rangeAndStep := strings.Split(expr, "/")
	if len(rangeAndStep) > 2 {
		return "too many slashes in " + quote(expr), false
	}
	if len(rangeAndStep) == 2 {
		step, ok := parseCronInt(rangeAndStep[1])
		if !ok || step < 1 {
			return "step must be a positive integer, got " + quote(rangeAndStep[1]), false
		}
	}
	base := rangeAndStep[0]
	if base == "*" || base == "?" {
		return "", true
	}
	lowAndHigh := strings.Split(base, "-")
	if len(lowAndHigh) > 2 {
		return "too many hyphens in " + quote(base), false
	}
	low, ok := parseCronValue(lowAndHigh[0], b)
	if !ok {
		return quote(lowAndHigh[0]) + " is not a value in " + strconv.Itoa(b.min) + "-" + strconv.Itoa(b.max), false
	}
	if len(lowAndHigh) == 1 {
		return "", true
	}
	high, ok := parseCronValue(lowAndHigh[1], b)
	if !ok {
		return quote(lowAndHigh[1]) + " is not a value in " + strconv.Itoa(b.min) + "-" + strconv.Itoa(b.max), false
	}
	if low > high {
		return "range " + quote(base) + " runs backwards", false
	}
	return "", true
}

// parseCronValue accepts an in-range integer or a three-letter name.
func parseCronValue(s string, b cronFieldBound) (int, bool) {
	if b.names != nil {
		if v, ok := b.names[strings.ToLower(s)]; ok {
			return v, true
		}
	}
	v, ok := parseCronInt(s)
	if !ok || v < b.min || v > b.max {
		return 0, false
	}
	return v, true
}

// parseCronInt parses a non-negative decimal integer with no sign or padding
// surprises (strconv.Atoi would accept "+3"/"-3"; a cron field must not).
func parseCronInt(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return v, true
}

// --- timezone validation (D2) ----------------------------------------------

// validateTimezone enforces D2: a required, explicit IANA zone. Local-time
// inheritance is BANNED, because inheriting the daemon's zone is exactly how an
// unattended schedule fires at the wrong hour on a host nobody looked at.
//
// time.LoadLocation is a tzdata NAME LOOKUP, not a clock read: no current time
// is observed anywhere in this package.
func validateTimezone(zone string) (string, bool) {
	switch {
	case zone == "":
		return "timezone is required and must be an explicit IANA zone name (for example \"UTC\" or \"America/New_York\"); " +
			"there is no default, because inheriting the daemon's local zone is invisible in the manifest", false
	case zone == "Local":
		return "timezone \"Local\" is not accepted: it inherits the daemon's zone, which is exactly the hidden default this dimension exists to " +
			"eliminate. Name the zone explicitly (for example \"UTC\")", false
	case strings.HasPrefix(zone, "+"), strings.HasPrefix(zone, "-"):
		return "timezone " + quote(zone) + " looks like a fixed UTC offset; a bare offset cannot express daylight-saving transitions, so an IANA " +
			"zone name is required (for example \"Europe/Madrid\")", false
	case zone == "UTC":
		// Special-cased so a UTC schedule stays loadable on a minimal container
		// that ships no tzdata at all. time.LoadLocation("UTC") happens to be
		// total today, but relying on that would make the guarantee incidental.
		return "", true
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return "timezone " + quote(zone) + " is not a loadable IANA zone name; either it is misspelled or the host is missing its tzdata database " +
			"(only \"UTC\" is guaranteed without tzdata)", false
	}
	return "", true
}

// --- approval posture ranking (D5) -----------------------------------------

// planApprovalRank ranks the document's approvalMode by PERMISSIVENESS, using
// the same closed vocabulary the client enum-validates
// (client/harness_plan.go:133-145: interactive|readonly|yolo). Higher is wider.
// ok is false for a mode outside that vocabulary.
func planApprovalRank(mode string) (int, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "readonly":
		return 0, true
	case "", "interactive": // normalizePermissions defaults empty to interactive
		return 1, true
	case "yolo":
		return 2, true
	}
	return 0, false
}

// posturePermissiveness ranks a schedule posture on the SAME scale, so the two
// are directly comparable:
//
//	deny (0)        never approves anything -> as strict as readonly
//	broker (1)      a decider outside the plan decides -> as wide as interactive
//	autoApprove (2) approves without any decider -> as wide as yolo
//
// ok is false for a value outside the closed enum.
func posturePermissiveness(p ScheduleApprovalPosture) (int, bool) {
	switch p {
	case ScheduleApprovalDeny:
		return 0, true
	case ScheduleApprovalBroker:
		return 1, true
	case ScheduleApprovalAutoApprove:
		return 2, true
	}
	return 0, false
}

// --- resolution ------------------------------------------------------------

// bindingUse is one (binding, requiring field) pair, accumulated in encounter
// order so the plan's binding list is deterministic without sorting paths.
type bindingUse struct {
	binding RuntimeBindingKind
	field   string
}

// scheduleEnv carries the document-scoped facts a schedule is validated
// against, resolved once and passed by value so every schedule is gated by
// identical rules.
type scheduleEnv struct {
	// approvalMode is the document's NORMALIZED permissions.approvalMode.
	approvalMode string
	// agentIDs / profileIDs are the declared reference vocabularies (Phase 8a).
	agentIDs   map[string]struct{}
	profileIDs map[string]struct{}
	// targetsResolvable is false when an earlier section already failed, in
	// which case the id sets are incomplete and a reference check would turn
	// one real error into a cascade of misleading "unknown target" errors (the
	// same discipline as detectDelegateCycles / checkAcknowledgementsUsed).
	targetsResolvable bool
}

// dimSpecFromFact builds the resolved dimension row from its fact row.
func dimSpecFromFact(dim ScheduleDimension, value string, fact scheduleFact) ScheduleDimensionSpec {
	return ScheduleDimensionSpec{
		Dimension:     dim,
		Value:         value,
		Enforcement:   fact.class,
		PathDependent: fact.pathDependent,
		Bindings:      append([]RuntimeBindingKind(nil), fact.bindings...),
	}
}

// lookupScheduleFact resolves the (dimension, value) fact, falling back to the
// wildcard row for dimensions whose value is not a closed enum.
func lookupScheduleFact(dim ScheduleDimension, value string) (scheduleFact, bool) {
	if f, ok := scheduleFacts[dimValue{dim, value}]; ok {
		return f, true
	}
	f, ok := scheduleFacts[dimValue{dim, scheduleAnyValue}]
	return f, ok
}

// missingDimensionDiag is the D1 diagnostic: it names the schedule AND the
// dimension, and says what the explicit "none" spelling is, so the fix never
// requires reading this file.
func missingDimensionDiag(sourcePath, field string, id string, dim ScheduleDimension, howToDisable string) Diagnostic {
	return newDiag("harness.schedules."+string(dim)+".missing", field+"."+string(dim),
		"schedule "+quote(id)+" must declare "+string(dim)+" explicitly; there is no default, because today it silently inherits a behaviour that is "+
			"either absent or wrong and an unattended operator cannot discover that from the manifest. "+howToDisable, sourcePath)
}

// resolveSchedules validates and resolves the `schedules:` section. Every rule
// is fail-closed at COMPILE time except a missing HOST BINDING, which per
// plan.md §3.3 is recorded as a requirement for Phase 9d preflight instead.
// Nothing here schedules, registers, persists, or times anything.
func resolveSchedules(p *Plan, sourcePath string, entries []ScheduleEntry, env scheduleEnv) Diagnostics {
	if len(entries) == 0 {
		// Omitted or `schedules: []`: behave exactly as pre-9b. Nothing is
		// carried, no provenance is added, and the digest is unchanged.
		return nil
	}

	var ds Diagnostics
	specs := make([]ScheduleSpec, 0, len(entries))
	var uses []bindingUse

	// Pass 1: id validity/uniqueness, so later diagnostics can name a schedule.
	ids := make(map[string]struct{}, len(entries))
	fieldOf := make(map[string]string, len(entries))
	for i, e := range entries {
		field := "schedules[" + strconv.Itoa(i) + "]"
		if e.ID == "" {
			ds = append(ds, newDiag("harness.schedules.id.missing", field+".id",
				"schedule entry requires a non-empty id", sourcePath))
			continue
		}
		if _, dup := ids[e.ID]; dup {
			ds = append(ds, newDiag("harness.schedules.id.duplicate", field+".id",
				"duplicate schedule id "+quote(e.ID), sourcePath))
			continue
		}
		ids[e.ID] = struct{}{}
		fieldOf[e.ID] = field
	}

	// Pass 2: per-entry resolution.
	for i, e := range entries {
		field := "schedules[" + strconv.Itoa(i) + "]"
		if e.ID == "" || fieldOf[e.ID] != field {
			continue // already reported in pass 1
		}
		spec, eu, eds := resolveSchedule(sourcePath, field, e, env)
		if len(eds) > 0 {
			ds = append(ds, eds...)
			continue
		}
		uses = append(uses, eu...)
		specs = append(specs, spec)
	}

	if ds.HasErrors() {
		return ds
	}

	p.schedules = specs
	p.bindings = collectBindingRequirements(uses)
	for _, s := range specs {
		p.addProvenance("schedules."+s.ID, "manifest", "")
	}
	return nil
}

// collectBindingRequirements folds the per-dimension binding uses into one
// deduplicated, canonically ordered list. Ordering is by runtimeBindingOrder
// (not by map iteration), so Explain and the digest are deterministic.
func collectBindingRequirements(uses []bindingUse) []RuntimeBindingRequirement {
	if len(uses) == 0 {
		return nil
	}
	byKind := make(map[RuntimeBindingKind][]string, len(uses))
	seen := make(map[bindingUse]struct{}, len(uses))
	for _, u := range uses {
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		byKind[u.binding] = append(byKind[u.binding], u.field)
	}
	out := make([]RuntimeBindingRequirement, 0, len(byKind))
	for _, kind := range runtimeBindingOrder {
		fields, ok := byKind[kind]
		if !ok {
			continue
		}
		out = append(out, RuntimeBindingRequirement{
			Binding:    kind,
			RequiredBy: fields,
			Reason:     runtimeBindingReasons[kind],
		})
	}
	return out
}

// resolveSchedule resolves ONE schedule: the cron shape, all eight required
// dimensions, the acknowledgement gate, and the posture-widening rule. It
// returns no partial spec when any rule fails.
func resolveSchedule(sourcePath, field string, e ScheduleEntry, env scheduleEnv) (ScheduleSpec, []bindingUse, Diagnostics) {
	var ds Diagnostics
	dims := make(map[ScheduleDimension]ScheduleDimensionSpec, len(scheduleDimensionOrder))

	// Cron SHAPE only: nothing is registered and no fire time is computed (D6).
	if reason, ok := validateCronShape(e.Cron); !ok {
		ds = append(ds, newDiag("harness.schedules.cron.invalid", field+".cron",
			"schedule "+quote(e.ID)+": "+reason, sourcePath))
	}

	add := func(spec ScheduleDimensionSpec, d Diagnostics) {
		if len(d) > 0 {
			ds = append(ds, d...)
			return
		}
		dims[spec.Dimension] = spec
	}

	add(resolveScheduleTimezone(sourcePath, field, e.ID, e.Timezone))
	add(resolveScheduleOverlap(sourcePath, field, e.ID, e.Overlap))
	add(resolveScheduleMisfire(sourcePath, field, e.ID, e.Misfire))
	add(resolveScheduleRetry(sourcePath, field, e.ID, e.Retry))
	add(resolveScheduleConcurrency(sourcePath, field, e.ID, e.Concurrency))
	add(resolveScheduleTarget(sourcePath, field, e.ID, e.Target, env))
	add(resolveSchedulePosture(sourcePath, field, e.ID, e.ApprovalPosture, env))
	add(resolveScheduleResultSink(sourcePath, field, e.ID, e.ResultSink))

	// The acknowledgement list is validated BEFORE it is consulted, so a bad
	// list can never satisfy a dimension's gate (mirrors resolveObservedAck).
	ack, ackDS := resolveScheduleAck(sourcePath, field, e.ID, e.AcknowledgeUnenforced, dims)
	ds = append(ds, ackDS...)

	// Apply the gate: a value whose fact row requires acknowledgement cannot be
	// declared without it. Acknowledging never changes the reported class.
	for dim, spec := range dims {
		fact, known := lookupScheduleFact(dim, spec.Value)
		if !known || !fact.requiresAck {
			continue
		}
		if _, ok := ack[dim]; !ok {
			ds = append(ds, newDiag("harness.schedules."+string(dim)+".unacknowledged", field+"."+string(dim),
				"schedule "+quote(e.ID)+": "+fact.ackReason, sourcePath))
			continue
		}
		spec.Acknowledged = true
		dims[dim] = spec
	}

	if ds.HasErrors() {
		return ScheduleSpec{}, nil, ds
	}

	// Assemble in canonical dimension order and register every binding.
	spec := ScheduleSpec{ID: e.ID, Cron: e.Cron, Dimensions: make([]ScheduleDimensionSpec, 0, len(scheduleDimensionOrder))}
	// SINGLE-OWNER is required by the mere EXISTENCE of a schedule, not by any
	// one dimension: with no election, lock, or PID record, two processes
	// sharing a workDir both fire every occurrence, which no per-dimension
	// policy can fix. Note that DURABLE STORE is deliberately NOT unconditional
	// here: a manifest-declared schedule survives a restart because the
	// MANIFEST is the state. What needs durability is per-occurrence history,
	// so only the misfire policies that read that history require it.
	uses := []bindingUse{{binding: BindingSchedulerSingleOwner, field: field}}
	bindingSeen := map[RuntimeBindingKind]struct{}{BindingSchedulerSingleOwner: {}}
	for _, dim := range scheduleDimensionOrder {
		d, ok := dims[dim]
		if !ok {
			// Unreachable: a missing dimension is an error above.
			return ScheduleSpec{}, nil, Diagnostics{newDiag("harness.schedules.dimension.unresolved", field+"."+string(dim),
				"schedule "+quote(e.ID)+" left dimension "+quote(string(dim))+" unresolved", sourcePath)}
		}
		spec.Dimensions = append(spec.Dimensions, d)
		for _, b := range d.Bindings {
			uses = append(uses, bindingUse{binding: b, field: field + "." + string(dim)})
			if _, dup := bindingSeen[b]; !dup {
				bindingSeen[b] = struct{}{}
				spec.Bindings = append(spec.Bindings, b)
			}
		}
	}
	spec.Bindings = append(spec.Bindings, BindingSchedulerSingleOwner)
	sortBindings(spec.Bindings)
	return spec, uses, nil
}

// sortBindings orders a binding list by runtimeBindingOrder so a schedule's
// binding set is reported deterministically.
func sortBindings(b []RuntimeBindingKind) {
	rank := make(map[RuntimeBindingKind]int, len(runtimeBindingOrder))
	for i, k := range runtimeBindingOrder {
		rank[k] = i
	}
	sort.SliceStable(b, func(i, j int) bool { return rank[b[i]] < rank[b[j]] })
}

// resolveScheduleAck validates acknowledgeUnenforced ITSELF: non-empty, unique,
// a known dimension, and a dimension whose DECLARED VALUE actually requires the
// acknowledgement. The last rule is what stops the field becoming a junk drawer
// and what makes a dangling acknowledgement (left behind after the value was
// changed to an enforced one) an error rather than dead text.
func resolveScheduleAck(sourcePath, field, id string, list []ScheduleDimension, dims map[ScheduleDimension]ScheduleDimensionSpec) (map[ScheduleDimension]struct{}, Diagnostics) {
	if len(list) == 0 {
		return nil, nil
	}
	f := field + ".acknowledgeUnenforced"
	var ds Diagnostics
	ack := make(map[ScheduleDimension]struct{}, len(list))
	for _, dim := range list {
		if dim == "" {
			ds = append(ds, newDiag("harness.schedules.acknowledgeUnenforced.empty", f,
				"schedule "+quote(id)+": acknowledgeUnenforced entry must be a non-empty dimension name", sourcePath))
			continue
		}
		if _, dup := ack[dim]; dup {
			ds = append(ds, newDiag("harness.schedules.acknowledgeUnenforced.duplicate", f,
				"schedule "+quote(id)+": duplicate acknowledged dimension "+quote(string(dim)), sourcePath))
			continue
		}
		if !knownScheduleDimension(dim) {
			ds = append(ds, newDiag("harness.schedules.acknowledgeUnenforced.unknown", f,
				"schedule "+quote(id)+": unknown dimension "+quote(string(dim))+"; valid dimensions are "+scheduleDimensionList(), sourcePath))
			continue
		}
		spec, resolved := dims[dim]
		if !resolved {
			// The dimension itself failed to resolve; its own diagnostic is
			// already reported and judging the acknowledgement would be noise.
			ack[dim] = struct{}{}
			continue
		}
		fact, known := lookupScheduleFact(dim, spec.Value)
		if !known || !fact.requiresAck {
			ds = append(ds, newDiag("harness.schedules.acknowledgeUnenforced.notRequired", f,
				"schedule "+quote(id)+": dimension "+quote(string(dim))+" is declared as "+quote(spec.Value)+", which has enforcement class "+
					string(spec.Enforcement)+" and requires no acknowledgement; remove it, because a dangling acknowledgement would silently "+
					"pre-authorize a future edit that changes the value to one that does", sourcePath))
			continue
		}
		ack[dim] = struct{}{}
	}
	if ds.HasErrors() {
		return nil, ds
	}
	return ack, nil
}

// knownScheduleDimension reports membership of the closed dimension set.
func knownScheduleDimension(dim ScheduleDimension) bool {
	for _, d := range scheduleDimensionOrder {
		if d == dim {
			return true
		}
	}
	return false
}

// scheduleDimensionList renders the closed dimension set for diagnostics.
func scheduleDimensionList() string {
	parts := make([]string, 0, len(scheduleDimensionOrder))
	for _, d := range scheduleDimensionOrder {
		parts = append(parts, quote(string(d)))
	}
	return strings.Join(parts, ", ")
}

// --- per-dimension resolvers ------------------------------------------------

// unknownValueDiag is the shared "not in the closed enum" diagnostic.
func unknownValueDiag(sourcePath, field, id string, dim ScheduleDimension, got string, valid ...string) Diagnostics {
	quoted := make([]string, 0, len(valid))
	for _, v := range valid {
		quoted = append(quoted, quote(v))
	}
	return Diagnostics{newDiag("harness.schedules."+string(dim)+".unknown", field+"."+string(dim),
		"schedule "+quote(id)+": unknown "+string(dim)+" value "+quote(got)+"; valid values are "+strings.Join(quoted, ", "), sourcePath)}
}

func resolveScheduleTimezone(sourcePath, field, id, zone string) (ScheduleDimensionSpec, Diagnostics) {
	if reason, ok := validateTimezone(zone); !ok {
		code := "harness.schedules.timezone.invalid"
		if zone == "" {
			code = "harness.schedules.timezone.missing"
		}
		return ScheduleDimensionSpec{}, Diagnostics{newDiag(code, field+".timezone",
			"schedule "+quote(id)+": "+reason, sourcePath)}
	}
	fact, _ := lookupScheduleFact(ScheduleDimTimezone, scheduleAnyValue)
	return dimSpecFromFact(ScheduleDimTimezone, zone, fact), nil
}

func resolveScheduleOverlap(sourcePath, field, id string, v ScheduleOverlap) (ScheduleDimensionSpec, Diagnostics) {
	if v == "" {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimOverlap,
			"To keep today's behaviour explicitly, declare "+quote(string(ScheduleOverlapAllowConcurrent))+".")}
	}
	fact, known := lookupScheduleFact(ScheduleDimOverlap, string(v))
	if !known {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimOverlap, string(v),
			string(ScheduleOverlapSkip), string(ScheduleOverlapAllowConcurrent), string(ScheduleOverlapQueue), string(ScheduleOverlapCancelPrevious))
	}
	return dimSpecFromFact(ScheduleDimOverlap, string(v), fact), nil
}

func resolveScheduleMisfire(sourcePath, field, id string, v ScheduleMisfire) (ScheduleDimensionSpec, Diagnostics) {
	if v == "" {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimMisfire,
			"To keep today's behaviour (missed occurrences are silently discarded), declare "+quote(string(ScheduleMisfireDrop))+".")}
	}
	fact, known := lookupScheduleFact(ScheduleDimMisfire, string(v))
	if !known {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimMisfire, string(v),
			string(ScheduleMisfireDrop), string(ScheduleMisfireRunImmediately), string(ScheduleMisfireBackfillAll))
	}
	return dimSpecFromFact(ScheduleDimMisfire, string(v), fact), nil
}

// resolveScheduleRetry additionally enforces that the numbers match the policy:
// required and positive for a real policy, ABSENT for `none`. A manifest must
// never carry numbers that look effective but are ignored.
func resolveScheduleRetry(sourcePath, field, id string, e *ScheduleRetryEntry) (ScheduleDimensionSpec, Diagnostics) {
	f := field + ".retry"
	if e == nil {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimRetry,
			"To keep today's behaviour (a failed occurrence is dropped), declare "+quote("retry: {policy: none}")+".")}
	}
	if e.Policy == "" {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.retry.policy.missing", f+".policy",
			"schedule "+quote(id)+": retry.policy is required; declare "+quote(string(ScheduleRetryNone))+" to keep today's drop-on-failure behaviour", sourcePath)}
	}
	fact, known := lookupScheduleFact(ScheduleDimRetry, string(e.Policy))
	if !known {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimRetry, string(e.Policy),
			string(ScheduleRetryNone), string(ScheduleRetryFixedDelay), string(ScheduleRetryExponentialBackoff))
	}
	spec := dimSpecFromFact(ScheduleDimRetry, string(e.Policy), fact)
	if e.Policy == ScheduleRetryNone {
		if e.MaxAttempts != 0 || e.DelaySeconds != 0 {
			return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.retry.numbersWithNone", f,
				"schedule "+quote(id)+": retry.policy is "+quote(string(ScheduleRetryNone))+" but maxAttempts/delaySeconds are set; they would be "+
					"ignored, so remove them or choose a retrying policy", sourcePath)}
		}
		return spec, nil
	}
	var ds Diagnostics
	if e.MaxAttempts < 1 {
		ds = append(ds, newDiag("harness.schedules.retry.maxAttempts.invalid", f+".maxAttempts",
			"schedule "+quote(id)+": retry.maxAttempts must be at least 1 for policy "+quote(string(e.Policy))+" (got "+strconv.Itoa(e.MaxAttempts)+")", sourcePath))
	}
	if e.DelaySeconds < 1 {
		ds = append(ds, newDiag("harness.schedules.retry.delaySeconds.invalid", f+".delaySeconds",
			"schedule "+quote(id)+": retry.delaySeconds must be at least 1 for policy "+quote(string(e.Policy))+" (got "+strconv.Itoa(e.DelaySeconds)+")", sourcePath))
	}
	if ds.HasErrors() {
		return ScheduleDimensionSpec{}, ds
	}
	spec.MaxAttempts = e.MaxAttempts
	spec.DelaySeconds = e.DelaySeconds
	return spec, nil
}

func resolveScheduleConcurrency(sourcePath, field, id string, e *ScheduleConcurrencyEntry) (ScheduleDimensionSpec, Diagnostics) {
	f := field + ".concurrency"
	if e == nil {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimConcurrency,
			"To keep today's behaviour (no bound at all), declare "+quote("concurrency: {policy: unlimited}")+".")}
	}
	if e.Policy == "" {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.concurrency.policy.missing", f+".policy",
			"schedule "+quote(id)+": concurrency.policy is required; declare "+quote(string(ScheduleConcurrencyUnlimited))+" to keep today's unbounded behaviour", sourcePath)}
	}
	fact, known := lookupScheduleFact(ScheduleDimConcurrency, string(e.Policy))
	if !known {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimConcurrency, string(e.Policy),
			string(ScheduleConcurrencyUnlimited), string(ScheduleConcurrencyMax))
	}
	spec := dimSpecFromFact(ScheduleDimConcurrency, string(e.Policy), fact)
	if e.Policy == ScheduleConcurrencyUnlimited {
		if e.Max != 0 {
			return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.concurrency.maxWithUnlimited", f+".max",
				"schedule "+quote(id)+": concurrency.policy is "+quote(string(ScheduleConcurrencyUnlimited))+" but max is set; it would be ignored, "+
					"so remove it or declare "+quote(string(ScheduleConcurrencyMax)), sourcePath)}
		}
		return spec, nil
	}
	if e.Max < 1 {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.concurrency.max.invalid", f+".max",
			"schedule "+quote(id)+": concurrency.max must be at least 1 for policy "+quote(string(ScheduleConcurrencyMax))+" (got "+strconv.Itoa(e.Max)+
				"); a bound of zero would mean a schedule that can never run", sourcePath)}
	}
	spec.MaxConcurrent = e.Max
	return spec, nil
}

func resolveScheduleResultSink(sourcePath, field, id string, e *ScheduleResultSinkEntry) (ScheduleDimensionSpec, Diagnostics) {
	f := field + ".resultSink"
	if e == nil {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimResultSink,
			"To keep today's behaviour (the outcome is only logged and delivered nowhere), declare "+quote("resultSink: {kind: discard}")+".")}
	}
	if e.Kind == "" {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.resultSink.kind.missing", f+".kind",
			"schedule "+quote(id)+": resultSink.kind is required; declare "+quote(string(ScheduleResultSinkDiscard))+" to keep today's behaviour", sourcePath)}
	}
	fact, known := lookupScheduleFact(ScheduleDimResultSink, string(e.Kind))
	if !known {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimResultSink, string(e.Kind),
			string(ScheduleResultSinkDiscard), string(ScheduleResultSinkNamed))
	}
	spec := dimSpecFromFact(ScheduleDimResultSink, string(e.Kind), fact)
	switch e.Kind {
	case ScheduleResultSinkDiscard:
		if e.ID != "" {
			return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.resultSink.idWithDiscard", f+".id",
				"schedule "+quote(id)+": resultSink.kind is "+quote(string(ScheduleResultSinkDiscard))+" but an id is set; it would be ignored", sourcePath)}
		}
	case ScheduleResultSinkNamed:
		if e.ID == "" {
			return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.resultSink.id.missing", f+".id",
				"schedule "+quote(id)+": resultSink.kind "+quote(string(ScheduleResultSinkNamed))+" requires a non-empty id naming the host sink", sourcePath)}
		}
		spec.Ref = e.ID
	}
	return spec, nil
}

// resolveScheduleTarget implements D4. Agent and profile ids are REFERENCES, not
// host bindings, so an unknown id fails at COMPILE time exactly like a Phase 8a
// delegate reference.
//
// THE WORKFLOW DECISION, CLOSED BY PHASE 9c: a workflow target used to be
// REJECTED here, because workflows did not exist in the harness and carrying an
// unverifiable id would have put a dangling reference into the plan AND its
// digest. `workflow` was a NAMED member of the enum precisely so 9c could close
// it by replacing ONE branch instead of extending a vocabulary — which is what
// the single call to resolveScheduleWorkflowTarget (workflows.go) below does.
// Reference integrity against the declared `workflows:` ids is enforced by
// checkScheduleWorkflowTargets, with the same anti-cascade suppression as the
// unknownAgent/unknownProfile checks in this function.
func resolveScheduleTarget(sourcePath, field, id string, e *ScheduleTargetEntry, env scheduleEnv) (ScheduleDimensionSpec, Diagnostics) {
	f := field + ".target"
	if e == nil {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimTarget,
			"Declare what the occurrence runs, for example "+quote("target: {kind: agent, id: <agents[] id>}")+".")}
	}
	if e.Kind == "" {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.target.kind.missing", f+".kind",
			"schedule "+quote(id)+": target.kind is required (one of "+quote(string(ScheduleTargetAgent))+", "+quote(string(ScheduleTargetProfile))+")", sourcePath)}
	}
	if e.Kind == ScheduleTargetWorkflow {
		return resolveScheduleWorkflowTarget(sourcePath, f, id, e.ID)
	}
	if e.Kind != ScheduleTargetAgent && e.Kind != ScheduleTargetProfile {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimTarget, string(e.Kind),
			string(ScheduleTargetAgent), string(ScheduleTargetProfile), string(ScheduleTargetWorkflow))
	}
	if e.ID == "" {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.target.id.missing", f+".id",
			"schedule "+quote(id)+": target.id is required and must name a declared "+string(e.Kind)+" id", sourcePath)}
	}
	if env.targetsResolvable {
		known := env.agentIDs
		section := "agents[]"
		if e.Kind == ScheduleTargetProfile {
			known = env.profileIDs
			section = "profiles[]"
		}
		if _, ok := known[e.ID]; !ok {
			return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.target.unknown"+strings.ToUpper(string(e.Kind)[:1])+string(e.Kind)[1:], f+".id",
				"schedule "+quote(id)+": target "+string(e.Kind)+" "+quote(e.ID)+" is not declared in "+section, sourcePath)}
		}
	}
	fact, _ := lookupScheduleFact(ScheduleDimTarget, string(e.Kind))
	spec := dimSpecFromFact(ScheduleDimTarget, string(e.Kind), fact)
	spec.Ref = e.ID
	return spec, nil
}

// resolveSchedulePosture implements D5. Besides the closed enum, it enforces the
// NEVER-WIDEN rule: a schedule's posture may be equal to or stricter than the
// document's permissions.approvalMode, never wider. Without this a schedule
// would be a side door that quietly grants an unattended run more authority
// than the audited `permissions:` block says it has.
func resolveSchedulePosture(sourcePath, field, id string, v ScheduleApprovalPosture, env scheduleEnv) (ScheduleDimensionSpec, Diagnostics) {
	f := field + ".approvalPosture"
	if v == "" {
		return ScheduleDimensionSpec{}, Diagnostics{missingDimensionDiag(sourcePath, field, id, ScheduleDimApprovalPosture,
			"Declare "+quote(string(ScheduleApprovalDeny))+" for a run that must never need a human, or "+quote(string(ScheduleApprovalBroker))+
				" to require a non-TUI approval broker.")}
	}
	fact, known := lookupScheduleFact(ScheduleDimApprovalPosture, string(v))
	if !known {
		return ScheduleDimensionSpec{}, unknownValueDiag(sourcePath, field, id, ScheduleDimApprovalPosture, string(v),
			string(ScheduleApprovalDeny), string(ScheduleApprovalBroker), string(ScheduleApprovalAutoApprove))
	}
	postureRank, _ := posturePermissiveness(v)
	planRank, planOK := planApprovalRank(env.approvalMode)
	if !planOK {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.approvalPosture.planModeUnknown", f,
			"schedule "+quote(id)+": cannot compare approvalPosture "+quote(string(v))+" against permissions.approvalMode "+quote(env.approvalMode)+
				", which is not one of interactive|readonly|yolo; a posture that cannot be compared could silently widen the plan, so it is refused", sourcePath)}
	}
	if postureRank > planRank {
		return ScheduleDimensionSpec{}, Diagnostics{newDiag("harness.schedules.approvalPosture.widensPermissions", f,
			"schedule "+quote(id)+": approvalPosture "+quote(string(v))+" is WIDER than the document's permissions.approvalMode "+
				quote(env.approvalMode)+"; a schedule may only be equal or stricter, never a side door that grants an unattended run more authority "+
				"than the audited permissions block", sourcePath)}
	}
	return dimSpecFromFact(ScheduleDimApprovalPosture, string(v), fact), nil
}

// --- immutable accessors ---------------------------------------------------

// Schedules returns a deep copy of the resolved schedules carried on the plan,
// in manifest order. Every nested slice is copied so a caller can never mutate
// the immutable Plan through a returned value.
func (p *Plan) Schedules() []ScheduleSpec {
	out := make([]ScheduleSpec, 0, len(p.schedules))
	for _, s := range p.schedules {
		out = append(out, copyScheduleSpec(s))
	}
	return out
}

// Schedule returns one resolved schedule by id. ok is false when undeclared.
func (p *Plan) Schedule(id string) (ScheduleSpec, bool) {
	for _, s := range p.schedules {
		if s.ID == id {
			return copyScheduleSpec(s), true
		}
	}
	return ScheduleSpec{}, false
}

// RequiredRuntimeBindings returns a deep copy of the host bindings this plan
// requires but cannot supply, in canonical binding order. This is the exact list
// Phase 9d preflight must satisfy before executing: per plan.md §3.3 a missing
// binding is not a compile error, it is a refusal to RUN.
func (p *Plan) RequiredRuntimeBindings() []RuntimeBindingRequirement {
	out := make([]RuntimeBindingRequirement, 0, len(p.bindings))
	for _, b := range p.bindings {
		out = append(out, RuntimeBindingRequirement{
			Binding:    b.Binding,
			RequiredBy: copyStringSlice(b.RequiredBy),
			Reason:     b.Reason,
		})
	}
	return out
}

func copyScheduleSpec(s ScheduleSpec) ScheduleSpec {
	out := ScheduleSpec{ID: s.ID, Cron: s.Cron}
	out.Dimensions = make([]ScheduleDimensionSpec, 0, len(s.Dimensions))
	for _, d := range s.Dimensions {
		d.Bindings = append([]RuntimeBindingKind(nil), d.Bindings...)
		out.Dimensions = append(out.Dimensions, d)
	}
	out.Bindings = append([]RuntimeBindingKind(nil), s.Bindings...)
	return out
}
