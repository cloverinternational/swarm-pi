package hooks

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// The observational view: hook coverage without hook authority.
// ---------------------------------------------------------------------------
//
// PLAN.md §1/P1 recorded that measurement coverage is currently decided by
// which delegation tool the model happens to pick: the Subagent tool
// deliberately skips hooks (internal/tools/builtin/subagent.go:694) while the
// Task/delegate tool inherits them (delegate_task.go:558), and background
// agents are dark entirely. A ledger built on that is confidently wrong — it
// silently omits exactly the sub-agent work we most want attributed.
//
// The naive fix (make Subagent inherit the hooks manager too) is wrong for the
// reason the skip comment already gives: hooks in a sub-agent add synchronous
// steering latency to every tool call, and sub-agents must not be steered.
//
// This file provides the other half: a view of the hook set that can OBSERVE
// but has no authority. The guarantee is structural, in four independent
// layers, each of which alone would be sufficient:
//
//  1. ENROLMENT. Only hooks that implement the ObservationalHook marker are
//     reachable. The marker is an explicit self-declaration in the hook's own
//     type — not a name allowlist, not a heuristic over Priority or event
//     type. A hook that does not declare itself is never invoked at all, so it
//     cannot block, and it also cannot cost latency.
//
//  2. NO VERDICT CHANNEL. Observe() has NO return values. There is no
//     syntactic location in which a verdict could be returned to the caller,
//     so no caller can branch on one. Inside dispatch, the (HookResult, error)
//     pair from OnEvent is discarded at the single call site.
//
//  3. NO EXECUTOR. This path never touches *Executor. The Executor is the only
//     component in this package that knows how to turn a HookResult into a
//     block, a modification, or an aggregated error (see executor.go and
//     Manager.EmitWithResult, which returns `fmt.Errorf("event blocked by ...")`).
//     By never entering it, ActionBlock and ActionModify have no interpreter.
//
//  4. TEMPORAL SEPARATION. Dispatch happens on a private goroutine, after the
//     caller has already returned. By the time a hook body runs, the decision
//     it might have wanted to influence has already been made. A verdict is
//     therefore unreachable in time as well as in type.
//
// Layers 2-4 also deliver the latency property the sub-agent skip comment was
// protecting: the cost imposed on a sub-agent tool call is one bounded copy
// plus one non-blocking channel send. No hook body runs on the tool path.

// ObservationalHook is the explicit, self-declared capability marker a Hook
// must implement to be reachable through an ObservationalView.
//
// Implementing it is a promise with teeth: the view discards whatever OnEvent
// returns, so a hook that declares itself observational and then returns
// Block() does not block — it is simply ignored. The marker exists so that
// enrolment is a decision made in the hook's own source, auditable by grepping
// for the method, rather than a property inferred by the dispatcher.
//
// The method is deliberately empty and deliberately not named like a getter:
// it carries no data, cannot be satisfied accidentally by an existing method
// set, and reads as a declaration at the implementation site.
type ObservationalHook interface {
	Hook

	// ObservationalOnly declares that this hook only records what it sees and
	// that discarding its return value loses nothing. It is never called.
	ObservationalOnly()
}

// defaultObservationalQueue bounds the in-flight observation backlog. Chosen
// so a burst of parallel tool calls is absorbed without allocation growth,
// while a wedged hook cannot turn the queue into an unbounded memory leak.
const defaultObservationalQueue = 256

// defaultObservationalHookTimeout is the deadline placed on each hook
// invocation's context. It is cooperative: Go cannot interrupt a hook that
// ignores its context. It is not the isolation mechanism (the bounded
// drop-on-full queue is); it exists so well-behaved hooks bail out instead of
// accumulating backlog.
const defaultObservationalHookTimeout = 5 * time.Second

// ObservationalView is a blocking-incapable view over a Manager's hooks.
//
// Construct it with Manager.ObservationalView. The zero value and a nil
// *ObservationalView are both safe: every method is nil-tolerant, because the
// call sites are on an agent's tool path and must never need a nil check.
type ObservationalView struct {
	mgr *Manager

	queue       chan Event
	workerDone  chan struct{}
	closeOnce   sync.Once
	hookTimeout time.Duration

	// accepted counts events placed on the queue; processed counts events
	// dispatched to hooks; dropped counts events discarded because the queue
	// was full. accepted == processed + inflight, and dropped is the honest
	// coverage hole this view reports rather than hides.
	accepted  atomic.Uint64
	processed atomic.Uint64
	dropped   atomic.Uint64
	panics    atomic.Uint64
}

// ObservationalView returns a view over this manager restricted to hooks that
// declare themselves observational.
//
// The view is MEMOIZED per manager and every call returns the same instance.
// That is a lifetime decision, not an optimisation: the view owns a goroutine
// and a bounded queue, while sub-agent and background spawns are unbounded, so
// a view per spawn would leak one goroutine and one queue per delegation. The
// resource that needs exactly one view is the hook set, and there is one hook
// set per manager.
//
// Consequently delegation sites must NOT Close the value they receive — they
// do not own it. Close belongs to whoever owns the manager.
//
// A nil manager yields a nil view, which is inert and safe to call.
func (m *Manager) ObservationalView() *ObservationalView {
	if m == nil {
		return nil
	}
	m.obsMu.Lock()
	defer m.obsMu.Unlock()
	if m.obsView != nil {
		return m.obsView
	}
	v := &ObservationalView{
		mgr:         m,
		queue:       make(chan Event, defaultObservationalQueue),
		workerDone:  make(chan struct{}),
		hookTimeout: defaultObservationalHookTimeout,
	}
	go v.run()
	m.obsView = v
	return v
}

// Observe hands an event to the observational hook set.
//
// It returns NOTHING. That is the contract, not an oversight: there is no
// channel through which a hook reached this way can block, deny, ask, steer,
// mutate the event the caller holds, or inject text into a conversation.
//
// It never blocks: if the backlog is full the event is dropped and counted.
// It never panics: a nil view and a nil queue are both handled.
func (v *ObservationalView) Observe(ctx context.Context, event Event) {
	if v == nil || v.queue == nil {
		return
	}
	// Copy before queueing, on the caller's goroutine. The caller may still be
	// holding and mutating the maps it passed in (the agent reuses tool
	// parameter maps), so handing the original across a goroutine boundary
	// would be a data race AND would let a hook mutate a live tool call.
	copied := sanitizeEventForObservation(event)

	// The caller's context is deliberately NOT propagated. Two reasons:
	// (1) a sub-agent's context is cancelled at teardown, which would silently
	// truncate the tail of every run's observations — exactly the coverage
	// hole this work exists to close; (2) it keeps agent-scoped context values
	// (callbacks, permission state, steering channels) out of reach of a hook
	// that is only allowed to look.
	_ = ctx

	select {
	case v.queue <- copied:
		v.accepted.Add(1)
	default:
		v.dropped.Add(1)
	}
}

// run drains the queue until Close. One goroutine, so observations are
// dispatched in the order they were accepted.
func (v *ObservationalView) run() {
	defer close(v.workerDone)
	for event := range v.queue {
		v.dispatch(event)
		v.processed.Add(1)
	}
}

// dispatch invokes every enrolled hook for one event, isolating each from the
// others and from the view.
func (v *ObservationalView) dispatch(event Event) {
	for _, h := range v.mgr.enrolledObservationalHooks() {
		if !v.safeFilter(h, event) {
			continue
		}
		v.safeOnEvent(h, event)
	}
}

// safeFilter calls Filter with panic isolation. A hook whose Filter panics is
// treated as not interested, which is the conservative reading.
func (v *ObservationalView) safeFilter(h Hook, event Event) (matched bool) {
	defer func() {
		if r := recover(); r != nil {
			v.panics.Add(1)
			matched = false
		}
	}()
	return h.Filter(sanitizeEventForObservation(event))
}

// safeOnEvent is the single site at which an observational hook body runs.
//
// This function is where the "cannot block" guarantee is cashed: OnEvent's
// (HookResult, error) pair is assigned to the blank identifier and goes
// nowhere. safeOnEvent itself returns nothing, so its own caller has nothing
// to propagate either. A hook returning Block("stop"), Modify(&evt), or a
// non-nil error is indistinguishable here from one returning Continue().
func (v *ObservationalView) safeOnEvent(h Hook, event Event) {
	defer func() {
		if r := recover(); r != nil {
			// A panicking observational hook is contained here. It cannot
			// reach the agent goroutine, because it is not on it.
			v.panics.Add(1)
		}
	}()

	hookCtx, cancel := context.WithTimeout(context.Background(), v.hookTimeout)
	defer cancel()

	// Each hook gets its own copy, so one hook mutating event.Data cannot be
	// observed by the next hook, and no hook can reach the caller's maps.
	//
	// THE DISCARD. Both return values are dropped, by construction.
	_, _ = h.OnEvent(hookCtx, sanitizeEventForObservation(event))
}

// Close stops the dispatch goroutine. It is idempotent and bounded: if a hook
// is wedged, Close gives up waiting rather than hanging the caller.
func (v *ObservationalView) Close() {
	if v == nil || v.queue == nil {
		return
	}
	v.closeOnce.Do(func() {
		close(v.queue)
	})
	select {
	case <-v.workerDone:
	case <-time.After(v.hookTimeout):
	}
}

// ObservationalStats is a truthful account of what this view actually did.
// Dropped is published rather than hidden: an observational view that silently
// discarded events would produce the same class of confidently-wrong ledger
// this work exists to prevent.
type ObservationalStats struct {
	// Accepted is the number of events placed on the queue.
	Accepted uint64
	// Processed is the number of events dispatched to hooks.
	Processed uint64
	// Dropped is the number of events discarded because the backlog was full.
	Dropped uint64
	// Panics is the number of recovered panics from hook bodies or filters.
	Panics uint64
	// EnrolledHooks names the hooks currently reachable through this view.
	EnrolledHooks []string
}

// Stats returns a snapshot. Safe on a nil view.
func (v *ObservationalView) Stats() ObservationalStats {
	if v == nil {
		return ObservationalStats{}
	}
	return ObservationalStats{
		Accepted:      v.accepted.Load(),
		Processed:     v.processed.Load(),
		Dropped:       v.dropped.Load(),
		Panics:        v.panics.Load(),
		EnrolledHooks: v.mgr.ObservationalHookNames(),
	}
}

// WaitDrained blocks until every accepted event has been dispatched, or the
// timeout elapses. It reports whether the queue actually drained.
//
// Needed because dispatch is asynchronous: a caller that wants to assert on
// what hooks saw (a test, or a shutdown path flushing a ledger) otherwise has
// no synchronisation point. It intentionally does NOT make Observe
// synchronous.
func (v *ObservationalView) WaitDrained(timeout time.Duration) bool {
	if v == nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	for {
		if v.processed.Load()+v.dropped.Load() >= v.accepted.Load()+v.dropped.Load() &&
			v.processed.Load() >= v.accepted.Load() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Microsecond)
	}
}

// enrolledObservationalHooks snapshots the hooks reachable through an
// observational view.
//
// It deliberately does NOT reuse getApplicableHooks. Historically that
// function wrote to m.sortedCache while holding only a read lock — a real
// data race calling it from this package's own dispatch goroutine would
// have hit concurrently with the parent agent's tool path. getApplicableHooks
// now takes the full write lock for exactly that reason (see its doc
// comment), so the race itself is fixed — but this function stays a
// separate, pure read (no cache write, no mutation of any kind, only
// m.mu.RLock()) rather than being merged into it: the observational
// dispatch goroutine should never contend for or block on the same write
// lock the parent's hot tool-call path needs, and duplicating this small
// read keeps that guarantee structural rather than incidental.
//
// Enabled and PermissionPolicy are still honoured, so runtime hook toggling
// (Manager.SetEnabled) works through the view exactly as it does through the
// full path.
func (m *Manager) enrolledObservationalHooks() []Hook {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	var out []Hook
	appendEnrolled := func(entry *hookEntry) {
		if entry == nil || !entry.Enabled || entry.PermissionPolicy == HookPermissionDeny {
			return
		}
		// THE ENROLMENT GATE. A hook that does not declare itself
		// observational is not merely ignored downstream — it is never
		// invoked, so its blocking behaviour is unreachable by construction.
		if _, ok := entry.Hook.(ObservationalHook); !ok {
			return
		}
		out = append(out, entry.Hook)
	}

	for _, entry := range m.global {
		appendEnrolled(entry)
	}
	for _, scopeMap := range m.scoped {
		for _, hookMap := range scopeMap {
			for _, entry := range hookMap {
				appendEnrolled(entry)
			}
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority() != out[j].Priority() {
			return out[i].Priority() > out[j].Priority()
		}
		return out[i].Name() < out[j].Name()
	})
	return out
}

// ObservationalHookNames returns the names of hooks that have declared
// themselves observational and are currently enabled. Exposed so a surface can
// report what is actually running instead of what it intended to run.
func (m *Manager) ObservationalHookNames() []string {
	enrolled := m.enrolledObservationalHooks()
	if len(enrolled) == 0 {
		return nil
	}
	names := make([]string, 0, len(enrolled))
	for _, h := range enrolled {
		names = append(names, h.Name())
	}
	return names
}

// maxObservationCopyDepth bounds the recursive copy so a self-referential or
// pathologically nested payload cannot turn observation into a stack overflow.
// Beyond this depth values are shared rather than copied; the agent-side
// adapters never place live pointers or deep structures in Data, so the bound
// is not reached in practice.
const maxObservationCopyDepth = 8

// sanitizeEventForObservation returns a copy of event whose mutable containers
// are not shared with the caller.
//
// Event is passed by value everywhere in this package, but its Data and
// Metadata maps are reference types: without this, a hook could reach into
// event.Data["params"] and rewrite a live tool call's arguments — a mutation
// channel that would defeat the whole point of an observation-only view.
func sanitizeEventForObservation(event Event) Event {
	out := event
	out.Data = copyAnyMap(event.Data, 0)
	out.Metadata = copyAnyMap(event.Metadata, 0)
	out.ToolOutcome = event.ToolOutcome.Clone()
	return out
}

func copyAnyMap(in map[string]any, depth int) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, val := range in {
		out[k] = copyAnyValue(val, depth+1)
	}
	return out
}

func copyAnyValue(val any, depth int) any {
	if depth >= maxObservationCopyDepth {
		return val
	}
	switch typed := val.(type) {
	case map[string]any:
		return copyAnyMap(typed, depth)
	case []any:
		outSlice := make([]any, len(typed))
		for i := range typed {
			outSlice[i] = copyAnyValue(typed[i], depth+1)
		}
		return outSlice
	case []string:
		outSlice := make([]string, len(typed))
		copy(outSlice, typed)
		return outSlice
	default:
		// Scalars and strings are immutable in Go. Anything else (a pointer to
		// a live struct, a channel, a func) is passed through: the adapters in
		// internal/agent are responsible for not putting such values on an
		// observational event, and they do not.
		return val
	}
}
