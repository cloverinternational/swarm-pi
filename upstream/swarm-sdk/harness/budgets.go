package harness

import "strconv"

// Phase 9a — declarative BUDGETS.
//
// This file is DECLARATION + RESOLUTION ONLY. It parses, validates, classifies,
// and resolves budget declarations; it NEVER enforces one, never reads a clock,
// never starts a goroutine, and never constructs a client/agent/provider.
// Enforcement wiring is Phase 9d, exactly as SkillEntry (5a), HookEntry (6a),
// McpEntry (7a), and the 8a sections each preceded their execution phase.
//
// THE CENTRAL DESIGN RULE: a budget must tell the TRUTH about what the runtime
// can actually do with it. Phase 9's exit gate is "the same plan can run
// interactively or unattended". A declared budget that silently stops nothing
// would make an unattended run unsafe while LOOKING safe on the page — that is
// the precise failure this slice exists to prevent. So every budget kind is
// annotated with a compile-time EnforcementClass drawn from the runtime's real
// behaviour (see budgetFacts below, each cell carrying its file:line evidence),
// and the class GATES declaration:
//
//   - enforced    -> declarable normally;
//   - observed    -> declarable ONLY with an explicit per-kind acknowledgement,
//                    and reported as observed-not-enforced in Explain;
//   - unavailable -> hard reject, with no override of any kind.
//
// The classes are facts about the runtime, not preferences. When Phase 9d (or a
// later slice) makes a kind genuinely enforceable, the fix is to change the
// table below together with the code that makes it true — a reviewable change,
// never a manifest flag.

// BudgetKind is the closed set of budget kinds. Each kind is modeled SEPARATELY
// (its own struct, its own unit of account, its own enforcement class); they are
// deliberately NOT collapsed into one number or one free-form string-keyed map,
// because the kinds differ in exactly the way that matters — what the runtime
// can do about them.
type BudgetKind string

const (
	// BudgetKindRun bounds how many RUNS of the harness may occur. This is a
	// schedule-level concept; see budgetFacts for its classification.
	BudgetKindRun BudgetKind = "run"
	// BudgetKindTurn bounds agent turns within one run.
	BudgetKindTurn BudgetKind = "turn"
	// BudgetKindOutputToken bounds CUMULATIVE output tokens across a whole run.
	// It is NOT the provider's per-request max_tokens cap; see OutputTokenBudget.
	BudgetKindOutputToken BudgetKind = "outputToken"
	// BudgetKindTotalToken bounds total (input + output) tokens for a run.
	BudgetKindTotalToken BudgetKind = "totalToken"
	// BudgetKindToolCall bounds the number of tool calls executed in a run.
	BudgetKindToolCall BudgetKind = "toolCall"
	// BudgetKindCost bounds spend in money.
	BudgetKindCost BudgetKind = "cost"
	// BudgetKindWallClock bounds elapsed wall-clock time for a run.
	BudgetKindWallClock BudgetKind = "wallClock"
)

// EnforcementClass is the closed enum describing what the runtime can ACTUALLY
// do with a budget kind today. It is a statement of fact, and it is part of the
// public, inspectable plan surface (Explain reports it for every declared
// budget) so an operator reading a report is never misled about which declared
// bounds can actually stop a run.
type EnforcementClass string

const (
	// BudgetEnforced: the runtime compares this budget against live state and
	// STOPS execution when it is exhausted.
	BudgetEnforced EnforcementClass = "enforced"
	// BudgetObserved: the quantity is tracked by the runtime, but it is never
	// compared against any threshold, so declaring a bound changes NOTHING
	// about when execution stops. Declarable only with an explicit
	// acknowledgement (see BudgetsSection.AcknowledgeObserved).
	BudgetObserved EnforcementClass = "observed"
	// BudgetUnavailable: the runtime cannot honour this budget at all — the
	// quantity is not even computed, or the concept has no counterpart in the
	// agent runtime. Declaring it is a hard compile error with no override.
	BudgetUnavailable EnforcementClass = "unavailable"
	// EnforcementBindingRequired: the value is DECLARABLE and carried on the
	// plan, the in-tree runtime does not honour it today, and the plan therefore
	// records a REQUIRED RUNTIME BINDING that preflight must satisfy before
	// execution (plan.md §3.3). It is distinct from `observed` (recorded and
	// ignored forever, with no route to enforcement) and from `unavailable` (hard
	// reject). Introduced by Phase 9b for schedules and reused by Phase 9c for
	// workflows; budgets never emit it, and budgetFacts is untouched by either
	// slice. It lives HERE, beside its siblings, so EnforcementClass has exactly
	// ONE declaration site (Phase 9c D6 moved it from schedules.go without
	// changing its name, its value, or any behaviour).
	EnforcementBindingRequired EnforcementClass = "bindingRequired"
)

// BudgetAction is the closed enum for a budget's REQUIRED onExhaustion action.
// There are exactly two variants and their semantics are pinned here so Phase 9d
// cannot invent a silent third behaviour:
//
//   - graceful: on exhaustion the runtime stops starting NEW work, runs the
//     normal end-of-run path (final summarization), and returns a non-error
//     result to the caller. The stop is recorded (audit/report) rather than
//     raised. This is exactly what turn exhaustion does today
//     (internal/agent/agent_execute.go:973-995: audit action "max_turns_limit"
//     followed by doFinalSummarization(..., "max_turns", ...)).
//
//   - fail: on exhaustion the run terminates and the failure is SURFACED to the
//     caller as an error. No summarization is promised and the caller is
//     expected to treat the run as failed. This is exactly what wall-clock
//     exhaustion does today (internal/agent/agent_execute.go:156-164 installs a
//     context.WithTimeout, so exhaustion arrives as context.DeadlineExceeded).
//
// FINDING, recorded here because it is load-bearing for anyone reading a plan:
// the runtime's two ENFORCED budgets already exhaust with these two DIFFERENT
// semantics. A caller cannot distinguish a turn-budget stop from a normal
// completion except via the audit log, while a wall-clock stop is a hard error.
// That inconsistency is runtime behaviour and is deliberately NOT changed here;
// it is the reason onExhaustion has no default (see BudgetsSection).
type BudgetAction string

const (
	// BudgetOnExhaustionGraceful finishes and summarizes; no error is raised.
	BudgetOnExhaustionGraceful BudgetAction = "graceful"
	// BudgetOnExhaustionFail terminates the run and surfaces an error.
	BudgetOnExhaustionFail BudgetAction = "fail"
)

// budgetFact is one row of the hard-coded kind -> enforcement table. evidence is
// the file:line proof for the classification so the next person can re-verify it
// cheaply instead of re-deriving it; unavailableReason is the body of the
// rejection diagnostic for an unavailable kind (each unavailable kind is
// unavailable for its OWN reason, and the diagnostic must say which).
type budgetFact struct {
	class             EnforcementClass
	unit              string
	evidence          string
	unavailableReason string
}

// budgetKindOrder is the canonical order budgets are reported in, independent of
// the order the manifest happens to declare them. A deterministic order keeps
// Explain/Digest stable.
var budgetKindOrder = []BudgetKind{
	BudgetKindRun,
	BudgetKindTurn,
	BudgetKindOutputToken,
	BudgetKindTotalToken,
	BudgetKindToolCall,
	BudgetKindCost,
	BudgetKindWallClock,
}

// budgetFacts is the hard-coded map from budget kind to what the runtime can
// really do, verified against the tree at this commit. Every entry cites its
// evidence. THIS TABLE IS THE WHOLE POINT OF PHASE 9a: change it only together
// with the runtime change that makes the new classification true.
var budgetFacts = map[BudgetKind]budgetFact{
	// ENFORCED. The turn loop guard is a real comparison against a resolved
	// bound, and exceeding it exits the loop.
	BudgetKindTurn: {
		class:    BudgetEnforced,
		unit:     "turns",
		evidence: "internal/agent/agent_execute.go:369 loop guard `for effectiveMaxTurns == 0 || a.turnCount < effectiveMaxTurns`; bound resolved at :150-153; wired from the plan at client/harness_plan.go:411 (Capabilities.MaxTurns)",
	},
	// ENFORCED. A real context deadline: exhaustion cancels the context and
	// surfaces context.DeadlineExceeded.
	BudgetKindWallClock: {
		class:    BudgetEnforced,
		unit:     "seconds",
		evidence: "internal/agent/agent_execute.go:156-164 context.WithTimeout(ctx, timeout); wired from the plan at client/harness_plan.go:412 (Capabilities.Timeout from Limits.TimeoutSeconds)",
	},
	// OBSERVED. Cumulative output tokens ARE accumulated across turns, but the
	// accumulator is never compared against any threshold: nothing stops.
	BudgetKindOutputToken: {
		class:    BudgetObserved,
		unit:     "tokens",
		evidence: "internal/agent/agent_execute.go:631 `a.outputTokens += resp.Usage.Output` accumulates across turns; no comparison against any bound exists anywhere in the execute loop",
	},
	// OBSERVED. Note the accurate mechanism: the old totalTokens FIELD was
	// removed as double-counting; the total is computed on demand as
	// inputTokens + outputTokens. Either way it is only ever reported.
	BudgetKindTotalToken: {
		class:    BudgetObserved,
		unit:     "tokens",
		evidence: "internal/agent/agent.go:553-557 (the totalTokens field was removed as double-counting; Stats().TotalTokens computes inputTokens + outputTokens on demand from the values tracked at internal/agent/agent_execute.go:622-663) — tracked and reported, never compared to a threshold",
	},
	// OBSERVED. The counter exists and is read for reporting only.
	BudgetKindToolCall: {
		class:    BudgetObserved,
		unit:     "calls",
		evidence: "internal/agent/agent_tools.go:542 `a.toolCallsTotal += len(message.ToolCalls)`; read for reporting at internal/agent/agent_config.go:554; no comparison against any bound",
	},
	// UNAVAILABLE. Not merely unenforced: never computed at all.
	BudgetKindCost: {
		class:    BudgetUnavailable,
		unit:     "microUSD",
		evidence: "internal/agent/agent_execute.go:632 is literally `// TODO: Calculate cost based on model pricing`; the accumulator is never incremented and cost is reported as 0",
		unavailableReason: "the runtime never COMPUTES cost at all — it is not merely unenforced. The cost accumulator is never incremented " +
			"(internal/agent/agent_execute.go:632 is still `// TODO: Calculate cost based on model pricing`), so a cost bound could only ever be compared " +
			"against a constant 0 and would never trigger. There is deliberately no acknowledgement, posture, or override that enables it: a manifest flag " +
			"cannot make a number exist. Making cost declarable requires making cost computable first, which is a reviewable code change",
	},
	// UNAVAILABLE. A per-RUN count is a scheduling concept: it can only be
	// counted by whatever decides to start runs, and the agent runtime has no
	// such counterpart. Accepting it here would let a manifest state a bound
	// that nothing in the compiled plan could ever map onto.
	BudgetKindRun: {
		class:    BudgetUnavailable,
		unit:     "runs",
		evidence: "no counterpart exists in the agent runtime: a run count can only be maintained by the component that decides to START runs (a schedule), and no such component is modeled in v1alpha1 at this commit",
		unavailableReason: "a run budget is a SCHEDULE-level concept and has no counterpart in the agent runtime: one compiled plan describes one run, so nothing " +
			"in it can count runs. Accepting it would mean the manifest declares a bound the plan provably cannot carry to anything. It is deferred to the " +
			"schedule slice (Phase 9b), which introduces the component that starts runs and can therefore count them; declare it there, not here",
	},
}

// EnforcementClassOf returns the compile-time enforcement class for a budget
// kind. ok is false for an unknown kind. It is exported so a consumer (and a
// human auditing a manifest) can ask the same question the compiler asks.
func EnforcementClassOf(kind BudgetKind) (EnforcementClass, bool) {
	f, ok := budgetFacts[kind]
	if !ok {
		return "", false
	}
	return f.class, true
}

// --- schema ---------------------------------------------------------------

// RunBudget bounds how many runs may occur. See budgetFacts: unavailable.
type RunBudget struct {
	MaxRuns      int          `json:"maxRuns,omitempty"`
	OnExhaustion BudgetAction `json:"onExhaustion,omitempty"`
}

// TurnBudget bounds agent turns. Overlaps agent.limits.maxTurns in meaning; see
// the D5 overlap rule in checkBudgetLimitsOverlap.
type TurnBudget struct {
	MaxTurns     int          `json:"maxTurns,omitempty"`
	OnExhaustion BudgetAction `json:"onExhaustion,omitempty"`
}

// OutputTokenBudget bounds CUMULATIVE output tokens for a whole run.
//
// D3 ANTI-CONFLATION: agent.limits.maxOutputTokens already exists and is a
// completely different quantity — it is the provider's PER-REQUEST `max_tokens`
// cap, passed straight through (client/harness_plan.go:214 -> the per-request
// max tokens on the provider call). A cumulative run budget has a different unit
// of account: one caps a SINGLE response, the other caps the SUM of every
// response in the run. The field is therefore named MaxCumulativeOutputTokens
// (`maxCumulativeOutputTokens`) and never `maxOutputTokens`, so no manifest can
// read as though one were the other. Declaring both is legal (they are genuinely
// different bounds) but is coherence-checked: see checkBudgetLimitsOverlap.
type OutputTokenBudget struct {
	MaxCumulativeOutputTokens int          `json:"maxCumulativeOutputTokens,omitempty"`
	OnExhaustion              BudgetAction `json:"onExhaustion,omitempty"`
}

// TotalTokenBudget bounds total (input + output) tokens for a run.
type TotalTokenBudget struct {
	MaxTotalTokens int          `json:"maxTotalTokens,omitempty"`
	OnExhaustion   BudgetAction `json:"onExhaustion,omitempty"`
}

// ToolCallBudget bounds executed tool calls for a run.
type ToolCallBudget struct {
	MaxToolCalls int          `json:"maxToolCalls,omitempty"`
	OnExhaustion BudgetAction `json:"onExhaustion,omitempty"`
}

// CostBudget bounds spend. The unit is integer MICRO-US-DOLLARS (1e-6 USD) and
// never a float: a float would make the plan digest depend on floating-point
// formatting, and money must not be compared with rounding error. See
// budgetFacts: unavailable (cost is never computed by the runtime).
type CostBudget struct {
	MaxCostMicroUSD int          `json:"maxCostMicroUSD,omitempty"`
	OnExhaustion    BudgetAction `json:"onExhaustion,omitempty"`
}

// WallClockBudget bounds elapsed time. Overlaps agent.limits.timeoutSeconds in
// meaning; see the D5 overlap rule in checkBudgetLimitsOverlap.
type WallClockBudget struct {
	MaxSeconds   int          `json:"maxSeconds,omitempty"`
	OnExhaustion BudgetAction `json:"onExhaustion,omitempty"`
}

// BudgetsSection is the v1alpha1 `budgets:` section. Every kind is an
// independently optional pointer, so "declared" is unambiguous and each kind is
// independently absent.
//
// OMITTING THE SECTION CHANGES NOTHING (D5). An absent `budgets:` compiles to
// exactly the pre-9a plan, byte-for-byte, including the digest; `budgets: {}`
// decodes to this zero value and is identical to omitting it. Declaring budgets
// may only ever ADD a constraint — it can never relax an existing `limits:`
// value, because the one place a budget and a limit mean the same thing is
// rejected outright rather than silently merged (checkBudgetLimitsOverlap).
type BudgetsSection struct {
	Run         *RunBudget         `json:"run,omitempty"`
	Turn        *TurnBudget        `json:"turn,omitempty"`
	OutputToken *OutputTokenBudget `json:"outputToken,omitempty"`
	TotalToken  *TotalTokenBudget  `json:"totalToken,omitempty"`
	ToolCall    *ToolCallBudget    `json:"toolCall,omitempty"`
	Cost        *CostBudget        `json:"cost,omitempty"`
	WallClock   *WallClockBudget   `json:"wallClock,omitempty"`

	// AcknowledgeObserved is the explicit, per-KIND acknowledgement required to
	// declare an OBSERVED budget. It mirrors the Phase 8d
	// permissions.acknowledgeNeverDefault shape exactly:
	//
	//   - it is a list of EXACT budget-kind names and nothing else: there is NO
	//     blanket boolean and NO wildcard, so "acknowledge everything" is
	//     unexpressible by construction and the posture can only ever be
	//     satisfied one kind at a time;
	//   - a kind here that is not DECLARED is an ERROR (a dangling
	//     acknowledgement would silently pre-authorize a future edit that adds
	//     the declaration);
	//   - a kind here whose class is not OBSERVED is an ERROR (this keeps the
	//     field honest and stops it becoming a junk drawer).
	//
	// It lives under `budgets:` rather than under `permissions:` because its
	// vocabulary is budget KINDS, not capability ids: mixing the two vocabularies
	// in one list would make both fields harder to audit and would let a typo in
	// one namespace be silently accepted as a member of the other.
	//
	// Acknowledging a kind does NOT make it enforced. It records that the author
	// knows the bound is observed-only, and Explain reports the kind as
	// `enforcement: observed` regardless.
	AcknowledgeObserved []BudgetKind `json:"acknowledgeObserved,omitempty"`
}

// BudgetSpec is one resolved budget carried on the immutable Plan and reported
// by Explain. Every field is a non-secret number, enum, or kind name: budgets
// add NOTHING to the tainted surface.
type BudgetSpec struct {
	Kind BudgetKind `json:"kind"`
	// Enforcement is the compile-time truth about this kind. It is reported so
	// an operator reading a plan can never mistake an observed budget for a
	// bound that will actually stop the run.
	Enforcement EnforcementClass `json:"enforcement"`
	// Limit is the declared bound, in Unit.
	Limit int    `json:"limit"`
	Unit  string `json:"unit"`
	// OnExhaustion is the REQUIRED, explicitly declared action.
	OnExhaustion BudgetAction `json:"onExhaustion"`
	// Acknowledged is true only for an observed budget, which cannot be
	// declared without the acknowledgement. It is reported so the redacted
	// plan records that the observed-only posture was accepted knowingly.
	Acknowledged bool `json:"acknowledged,omitempty"`
}

// --- resolution -----------------------------------------------------------

// declaredBudget is one budget as written by the author, before classification.
type declaredBudget struct {
	kind   BudgetKind
	field  string
	limit  int
	action BudgetAction
}

// collectDeclaredBudgets flattens the section into canonical kind order. Only
// non-nil (i.e. actually declared) kinds appear.
func collectDeclaredBudgets(b BudgetsSection) []declaredBudget {
	out := make([]declaredBudget, 0, len(budgetKindOrder))
	add := func(kind BudgetKind, limit int, action BudgetAction) {
		out = append(out, declaredBudget{kind: kind, field: "budgets." + string(kind), limit: limit, action: action})
	}
	for _, kind := range budgetKindOrder {
		switch kind {
		case BudgetKindRun:
			if b.Run != nil {
				add(kind, b.Run.MaxRuns, b.Run.OnExhaustion)
			}
		case BudgetKindTurn:
			if b.Turn != nil {
				add(kind, b.Turn.MaxTurns, b.Turn.OnExhaustion)
			}
		case BudgetKindOutputToken:
			if b.OutputToken != nil {
				add(kind, b.OutputToken.MaxCumulativeOutputTokens, b.OutputToken.OnExhaustion)
			}
		case BudgetKindTotalToken:
			if b.TotalToken != nil {
				add(kind, b.TotalToken.MaxTotalTokens, b.TotalToken.OnExhaustion)
			}
		case BudgetKindToolCall:
			if b.ToolCall != nil {
				add(kind, b.ToolCall.MaxToolCalls, b.ToolCall.OnExhaustion)
			}
		case BudgetKindCost:
			if b.Cost != nil {
				add(kind, b.Cost.MaxCostMicroUSD, b.Cost.OnExhaustion)
			}
		case BudgetKindWallClock:
			if b.WallClock != nil {
				add(kind, b.WallClock.MaxSeconds, b.WallClock.OnExhaustion)
			}
		}
	}
	return out
}

// limitFieldFor returns the `agent.limits:` field a budget kind OVERLAPS in
// meaning, together with its declared value. ok is false when the kind has no
// overlapping limit at all. Only genuine same-quantity overlaps are listed:
// maxOutputTokens is deliberately ABSENT because it is a per-request cap and not
// the same quantity as a cumulative output-token budget (D3).
func limitFieldFor(kind BudgetKind, l Limits) (field string, value int, ok bool) {
	switch kind {
	case BudgetKindTurn:
		return "agent.limits.maxTurns", l.MaxTurns, true
	case BudgetKindWallClock:
		return "agent.limits.timeoutSeconds", l.TimeoutSeconds, true
	}
	return "", 0, false
}

// resolveBudgets validates and resolves the `budgets:` section. Every rule is
// fail-closed at COMPILE time; none is advisory or deferred to runtime, and
// nothing here enforces, schedules, or measures anything.
func resolveBudgets(p *Plan, sourcePath string, b BudgetsSection, limits Limits) Diagnostics {
	declared := collectDeclaredBudgets(b)
	if len(declared) == 0 && len(b.AcknowledgeObserved) == 0 {
		// Omitted (or `budgets: {}`): behave exactly as pre-9a. Nothing is
		// carried, no provenance is added, and the digest is unchanged.
		return nil
	}

	var ds Diagnostics

	declaredKinds := make(map[BudgetKind]struct{}, len(declared))
	for _, d := range declared {
		declaredKinds[d.kind] = struct{}{}
	}

	// 1. The acknowledgement list itself (validated before it is consulted, so
	//    a bad list can never satisfy an observed budget's gate).
	ack, ackDS := resolveObservedAck(sourcePath, b.AcknowledgeObserved, declaredKinds)
	ds = append(ds, ackDS...)

	// 2. Each declared budget: class gate, numeric validity, required action.
	specs := make([]BudgetSpec, 0, len(declared))
	for _, d := range declared {
		fact, known := budgetFacts[d.kind]
		if !known {
			// Unreachable while collectDeclaredBudgets walks budgetKindOrder,
			// but kept fail-closed so adding a kind without a fact row is an
			// error rather than a silently unclassified budget.
			ds = append(ds, newDiag("harness.budgets.kind.unknown", d.field,
				"unknown budget kind "+quote(string(d.kind))+"; it has no enforcement classification", sourcePath))
			continue
		}

		// 2a. Class gate FIRST: an unavailable kind is rejected outright and
		//     its other fields are not worth diagnosing.
		if fact.class == BudgetUnavailable {
			ds = append(ds, newDiag("harness.budgets."+string(d.kind)+".unavailable", d.field,
				"budget "+quote(string(d.kind))+" cannot be declared: "+fact.unavailableReason, sourcePath))
			continue
		}

		// 2b. Numeric validity. Zero is invalid, not "unset": the pointer
		//     already expresses declared-ness, so a zero bound would mean a
		//     budget that is exhausted before any work happens.
		if d.limit <= 0 {
			ds = append(ds, newDiag("harness.budgets."+string(d.kind)+".invalid", d.field,
				"budget "+quote(string(d.kind))+" must declare a bound greater than zero in "+fact.unit+
					" (got "+strconv.Itoa(d.limit)+")", sourcePath))
			continue
		}

		// 2c. onExhaustion is REQUIRED per budget; there is deliberately no
		//     default, because the runtime's two enforced budgets already
		//     exhaust with two different semantics and any default we picked
		//     would silently misrepresent one of them (see BudgetAction).
		switch d.action {
		case "":
			ds = append(ds, newDiag("harness.budgets."+string(d.kind)+".onExhaustion.missing", d.field+".onExhaustion",
				"budget "+quote(string(d.kind))+" must declare onExhaustion explicitly (one of "+
					quote(string(BudgetOnExhaustionGraceful))+" or "+quote(string(BudgetOnExhaustionFail))+
					"); there is no default, because the two enforced budgets already exhaust with different semantics", sourcePath))
			continue
		case BudgetOnExhaustionGraceful, BudgetOnExhaustionFail:
			// valid
		default:
			ds = append(ds, newDiag("harness.budgets."+string(d.kind)+".onExhaustion.unknown", d.field+".onExhaustion",
				"unknown onExhaustion action "+quote(string(d.action))+" for budget "+quote(string(d.kind))+
					"; valid actions are "+quote(string(BudgetOnExhaustionGraceful))+" and "+quote(string(BudgetOnExhaustionFail)), sourcePath))
			continue
		}

		// 2d. Observed kinds require their per-kind acknowledgement.
		acked := false
		if fact.class == BudgetObserved {
			if _, ok := ack[d.kind]; !ok {
				ds = append(ds, newDiag("harness.budgets."+string(d.kind)+".unacknowledged", d.field,
					"budget "+quote(string(d.kind))+" is OBSERVED, not enforced: the runtime tracks the value but never compares it "+
						"against any threshold, so declaring this bound stops nothing. Declaring it anyway requires the explicit "+
						"acknowledgement: add "+quote(string(d.kind))+" to budgets.acknowledgeObserved", sourcePath))
				continue
			}
			acked = true
		}

		specs = append(specs, BudgetSpec{
			Kind:         d.kind,
			Enforcement:  fact.class,
			Limit:        d.limit,
			Unit:         fact.unit,
			OnExhaustion: d.action,
			Acknowledged: acked,
		})
	}

	// 3. D5 overlap / coherence against agent.limits.
	ds = append(ds, checkBudgetLimitsOverlap(sourcePath, declared, limits)...)

	if ds.HasErrors() {
		return ds
	}

	p.budgets = specs
	for _, s := range specs {
		p.addProvenance("budgets."+string(s.Kind), "manifest", "")
	}
	return nil
}

// resolveObservedAck validates budgets.acknowledgeObserved ITSELF and returns
// the acknowledged kind set. Mirrors resolveNeverDefaultAck (Phase 8d) rule for
// rule, including the dangling check — which CAN live here (unlike the 8d one)
// because a budget's declared-ness is known from this same section rather than
// from every agent's resolved tool selection.
func resolveObservedAck(sourcePath string, kinds []BudgetKind, declared map[BudgetKind]struct{}) (map[BudgetKind]struct{}, Diagnostics) {
	if len(kinds) == 0 {
		return nil, nil
	}
	const field = "budgets.acknowledgeObserved"
	var ds Diagnostics
	ack := make(map[BudgetKind]struct{}, len(kinds))
	for _, k := range kinds {
		if k == "" {
			ds = append(ds, newDiag("harness.budgets.acknowledgeObserved.empty", field,
				"budgets.acknowledgeObserved entry must be a non-empty budget kind", sourcePath))
			continue
		}
		if _, dup := ack[k]; dup {
			ds = append(ds, newDiag("harness.budgets.acknowledgeObserved.duplicate", field,
				"duplicate acknowledged budget kind "+quote(string(k)), sourcePath))
			continue
		}
		fact, known := budgetFacts[k]
		if !known {
			ds = append(ds, newDiag("harness.budgets.acknowledgeObserved.unknown", field,
				"unknown budget kind "+quote(string(k))+"; it is not one of the declared budget kinds", sourcePath))
			continue
		}
		if fact.class != BudgetObserved {
			ds = append(ds, newDiag("harness.budgets.acknowledgeObserved.notObserved", field,
				"budget kind "+quote(string(k))+" has enforcement class "+string(fact.class)+
					"; budgets.acknowledgeObserved accepts only "+string(BudgetObserved)+" kinds, so remove it", sourcePath))
			continue
		}
		if _, ok := declared[k]; !ok {
			ds = append(ds, newDiag("harness.budgets.acknowledgeObserved.unused", field,
				"budget kind "+quote(string(k))+" is acknowledged but no such budget is declared; "+
					"remove the acknowledgement or declare the budget", sourcePath))
			continue
		}
		ack[k] = struct{}{}
	}
	if ds.HasErrors() {
		return nil, ds
	}
	return ack, nil
}

// checkBudgetLimitsOverlap implements D5: declaring a budget must never weaken
// anything.
//
// THE RULE, and why it is REJECT rather than stricter-wins: `budgets.turn` and
// `agent.limits.maxTurns` are the SAME quantity wired to the SAME runtime field
// (Capabilities.MaxTurns), and likewise `budgets.wallClock` and
// `agent.limits.timeoutSeconds`. If both are declared, stricter-wins would make
// one of the two written numbers a lie: the manifest would say maxTurns: 100
// while the effective bound was 40, and an operator auditing `limits:` alone
// would be misled — the exact failure mode Phase 9a exists to prevent. So a
// co-declaration is REJECTED and the author must keep exactly one source of
// truth. Rejection is also trivially safe for D5's "may only ADD" requirement,
// and it cannot break an existing manifest: pre-9a manifests have no `budgets:`
// section, so they cannot reach this rule.
//
// Equal values are rejected too, deliberately: a bound duplicated in two places
// is a bound that will drift on the next edit, and "they happened to match on
// the day it was written" is not a property worth preserving.
//
// maxOutputTokens is NOT an overlap (D3): the per-request provider cap and a
// cumulative run budget are different quantities, so both may be declared. They
// are only COHERENCE-checked — a cumulative run budget smaller than the cap on a
// single response is incoherent (the first response alone could exceed the whole
// run's budget), and that incoherence is the signature of an author who thought
// the two fields meant the same thing.
func checkBudgetLimitsOverlap(sourcePath string, declared []declaredBudget, l Limits) Diagnostics {
	var ds Diagnostics
	for _, d := range declared {
		if field, value, ok := limitFieldFor(d.kind, l); ok && value != 0 {
			ds = append(ds, newDiag("harness.budgets."+string(d.kind)+".overlapsLimits", d.field,
				"budget "+quote(string(d.kind))+" and "+field+" are the same bound declared twice ("+
					field+"="+strconv.Itoa(value)+", "+d.field+"="+strconv.Itoa(d.limit)+
					"); declare exactly one of them so the manifest has a single source of truth for this bound", sourcePath))
			continue
		}
		if d.kind == BudgetKindOutputToken && l.MaxOutputTokens > 0 && d.limit > 0 && d.limit < l.MaxOutputTokens {
			ds = append(ds, newDiag("harness.budgets.outputToken.belowPerRequestCap", d.field+".maxCumulativeOutputTokens",
				"budgets.outputToken.maxCumulativeOutputTokens ("+strconv.Itoa(d.limit)+") is smaller than the PER-REQUEST cap "+
					"agent.limits.maxOutputTokens ("+strconv.Itoa(l.MaxOutputTokens)+"). These are different quantities: maxOutputTokens caps a "+
					"SINGLE provider response, while maxCumulativeOutputTokens bounds the SUM over the whole run, so a cumulative budget below the "+
					"per-request cap could be exhausted by the first response alone", sourcePath))
		}
	}
	return ds
}

// --- immutable accessors --------------------------------------------------

// Budgets returns a copy of the resolved budget specs carried on the plan, in
// canonical kind order. BudgetSpec contains only value fields, so a slice copy
// is a full defensive copy.
func (p *Plan) Budgets() []BudgetSpec {
	out := make([]BudgetSpec, len(p.budgets))
	copy(out, p.budgets)
	return out
}

// Budget returns the resolved spec for one kind. ok is false when the kind was
// not declared.
func (p *Plan) Budget(kind BudgetKind) (BudgetSpec, bool) {
	for _, s := range p.budgets {
		if s.Kind == kind {
			return s, true
		}
	}
	return BudgetSpec{}, false
}
