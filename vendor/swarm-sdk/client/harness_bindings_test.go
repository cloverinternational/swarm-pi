// Harness Phase 9d tests — TIER 1: the binding vocabulary itself.
//
// This file pins the D1 contract (one narrow interface per binding kind, an
// exhaustive and drift-proof kind -> interface table) and the D3 contract (the
// reference implementations are HONEST, and everything else is deliberately
// absent rather than stubbed).
//
// The end-to-end refusal behaviour lives in harness_preflight_test.go.
package client

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// ─── shared fixtures ───────────────────────────────────────────────────────

// harness9dBase is a minimal, valid manifest declaring NO schedules and NO
// workflows — the ZERO-BINDING baseline. It declares one subagent so the
// schedule fixtures below have a legal `target:` to name, which by itself
// requires nothing.
const harness9dBase = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: preflight9d
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: SECRET-CREDENTIAL-VALUE-9D
agent:
  systemPrompt:
    inline: "PROMPT-BODY-SENTINEL-9D"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
agents:
  - id: reporter
    systemPrompt:
      inline: "SUBAGENT-PROMPT-SENTINEL-9D"
    tools: []
`

// harness9dOneSchedule declares the LIGHTEST possible schedule: every dimension
// set to the value that is literally today's runtime behaviour where one exists.
// It still requires FIVE bindings, which is the Phase 9b point — even the
// most conservative schedule cannot be honestly run by the in-tree scheduler.
const harness9dOneSchedule = harness9dBase + `schedules:
  - id: nightly
    cron: "0 3 * * *"
    timezone: UTC
    overlap: skip
    misfire: drop
    retry:
      policy: none
    concurrency:
      policy: unlimited
    target:
      kind: agent
      id: reporter
    approvalPosture: deny
    resultSink:
      kind: discard
    acknowledgeUnenforced:
      - overlap
`

// harness9dAllElevenBindings declares two schedules whose union requires ALL
// ELEVEN scheduler/approval binding kinds: the light one above (deny posture)
// plus a heavy one exercising every binding-required value (broker posture,
// runImmediately misfire, fixedDelay retry, maxConcurrent, named result sink).
const harness9dAllElevenBindings = harness9dOneSchedule + `  - id: heavy
    cron: "*/15 * * * *"
    timezone: America/New_York
    overlap: queue
    misfire: runImmediately
    retry:
      policy: fixedDelay
      delaySeconds: 30
      maxAttempts: 3
    concurrency:
      policy: maxConcurrent
      max: 2
    target:
      kind: agent
      id: reporter
    approvalPosture: broker
    resultSink:
      kind: named
      id: ops
    acknowledgeUnenforced:
      - overlap
`

// harness9dEveryKind is the canonical list of the eleven scheduler/approval
// binding kinds Phase 9b declares. It is written out longhand (rather than read
// from an accessor) because harness's canonical order is unexported and this
// slice modifies the harness package by exactly zero bytes.
var harness9dEveryKind = []harness.RuntimeBindingKind{
	harness.BindingSchedulerZonedClock,
	harness.BindingSchedulerSingleOwner,
	harness.BindingSchedulerOverlapGuard,
	harness.BindingSchedulerMisfireDetector,
	harness.BindingSchedulerDurableStore,
	harness.BindingSchedulerRetryDriver,
	harness.BindingSchedulerConcurrencyLimiter,
	harness.BindingSchedulerTargetDispatcher,
	harness.BindingSchedulerResultSink,
	harness.BindingApprovalResolver,
	harness.BindingApprovalBroker,
}

// ─── D1: the kind -> interface mapping is exhaustive and one-to-one ────────

// TestHarnessBindingSlotsExhaustive proves every declarable scheduler/approval
// kind has EXACTLY ONE slot, and that the table has no extra rows. A kind added
// to harness without a slot here would fail this test rather than silently
// becoming unsatisfiable-by-omission.
func TestHarnessBindingSlotsExhaustive(t *testing.T) {
	if got, want := len(harnessBindingSlots), len(harness9dEveryKind); got != want {
		t.Fatalf("harnessBindingSlots has %d rows, want %d (one per declarable kind)", got, want)
	}
	for _, kind := range harness9dEveryKind {
		if _, ok := harnessBindingSlots[kind]; !ok {
			t.Errorf("binding kind %q has no interface slot", kind)
		}
	}
	supported := harnessBindingSupportedKinds()
	if len(supported) != len(harness9dEveryKind) {
		t.Errorf("harnessBindingSupportedKinds() = %v, want %d kinds", supported, len(harness9dEveryKind))
	}
	for i := 1; i < len(supported); i++ {
		if supported[i-1] >= supported[i] {
			t.Errorf("harnessBindingSupportedKinds() is not sorted: %v", supported)
			break
		}
	}
}

// TestHarnessBindingSlotsAreOneToOne sets exactly ONE registry field at a time
// and asserts Supplied() reports exactly the matching kind. This is what makes
// the kind -> interface mapping a proven property rather than a comment: a
// copy-paste error in harnessBindingSlots (two kinds reading the same field)
// fails here.
func TestHarnessBindingSlotsAreOneToOne(t *testing.T) {
	f := &harness9dFakeBindings{}
	cases := []struct {
		kind harness.RuntimeBindingKind
		set  func(*HarnessBindings)
	}{
		{harness.BindingSchedulerZonedClock, func(b *HarnessBindings) { b.SchedulerZonedClock = f }},
		{harness.BindingSchedulerSingleOwner, func(b *HarnessBindings) { b.SchedulerSingleOwner = f }},
		{harness.BindingSchedulerOverlapGuard, func(b *HarnessBindings) { b.SchedulerOverlapGuard = f }},
		{harness.BindingSchedulerMisfireDetector, func(b *HarnessBindings) { b.SchedulerMisfireDetector = f }},
		{harness.BindingSchedulerDurableStore, func(b *HarnessBindings) { b.SchedulerDurableStore = f }},
		{harness.BindingSchedulerRetryDriver, func(b *HarnessBindings) { b.SchedulerRetryDriver = f }},
		{harness.BindingSchedulerConcurrencyLimiter, func(b *HarnessBindings) { b.SchedulerConcurrencyLimiter = f }},
		{harness.BindingSchedulerTargetDispatcher, func(b *HarnessBindings) { b.SchedulerTargetDispatcher = f }},
		{harness.BindingSchedulerResultSink, func(b *HarnessBindings) { b.SchedulerResultSink = f }},
		{harness.BindingApprovalResolver, func(b *HarnessBindings) { b.ApprovalUnattendedResolver = f }},
		{harness.BindingApprovalBroker, func(b *HarnessBindings) { b.ApprovalBroker = f }},
	}
	if len(cases) != len(harness9dEveryKind) {
		t.Fatalf("this test covers %d kinds, want %d", len(cases), len(harness9dEveryKind))
	}
	for _, tc := range cases {
		var b HarnessBindings
		tc.set(&b)
		got := b.Supplied()
		if len(got) != 1 || got[0] != tc.kind {
			t.Errorf("setting the field for %q yielded Supplied()=%v, want exactly [%s]", tc.kind, got, tc.kind)
		}
	}
}

// TestHarnessBindingsZeroValueSuppliesNothing pins D3's core rule at the type
// level: the zero registry is an ABSENCE, not a set of defaults.
func TestHarnessBindingsZeroValueSuppliesNothing(t *testing.T) {
	var b HarnessBindings
	if got := b.Supplied(); len(got) != 0 {
		t.Fatalf("zero HarnessBindings supplies %v, want nothing", got)
	}
	for kind, present := range harnessBindingSlots {
		if present(b) {
			t.Errorf("zero HarnessBindings reports %q as supplied; there must be no ambient default", kind)
		}
	}
}

func TestHostAssertedSingleInstanceScheduleOwnerRequiresExplicitConstruction(t *testing.T) {
	var bindings HarnessBindings
	if got := bindings.Supplied(); len(got) != 0 {
		t.Fatalf("zero bindings supplied %v, want none", got)
	}

	bindings.SchedulerSingleOwner = NewHostAssertedSingleInstanceScheduleOwner()
	got := bindings.Supplied()
	if len(got) != 1 || got[0] != harness.BindingSchedulerSingleOwner {
		t.Fatalf("explicit owner supplied %v, want [%s]", got, harness.BindingSchedulerSingleOwner)
	}

	ownership, err := bindings.SchedulerSingleOwner.AcquireScheduleOwnership(context.Background(), "nightly")
	if err != nil {
		t.Fatalf("AcquireScheduleOwnership: %v", err)
	}
	if ownership == nil || !ownership.Owned() {
		t.Fatal("host assertion did not return owned schedule ownership")
	}
	if err := ownership.Release(context.Background()); err != nil {
		t.Fatalf("Release: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ownership, err := bindings.SchedulerSingleOwner.AcquireScheduleOwnership(ctx, "nightly"); !errors.Is(err, context.Canceled) || ownership != nil {
		t.Fatalf("canceled acquire = (%v, %v), want (nil, context.Canceled)", ownership, err)
	}
}

// TestHarnessWorkflowBindingKindsHaveNoSlot pins the DELIBERATE scope boundary:
// Phase 9c's six workflow binding kinds have no client interface in this slice
// (Phase 10 owns them), so they must be reported UNSATISFIABLE rather than
// silently accepted as unrecognised strings.
func TestHarnessWorkflowBindingKindsHaveNoSlot(t *testing.T) {
	for _, kind := range []harness.RuntimeBindingKind{
		harness.BindingWorkflowEngine,
		harness.BindingWorkflowSteeringController,
		harness.BindingWorkflowPlanValidator,
		harness.BindingWorkflowOutputSynthesizer,
		harness.BindingWorkflowParameterProvider,
		harness.BindingWorkflowHumanInterventionBroker,
	} {
		if _, ok := harnessBindingSlots[kind]; ok {
			t.Errorf("workflow binding %q unexpectedly has a slot; this slice must not invent Phase 10 interfaces", kind)
		}
	}
}

// ─── D3: the reference zoned clock is honest ───────────────────────────────

// TestSystemZonedClockUsesTheNamedZone proves the clock really resolves the
// named zone rather than the process's local one — the exact gap
// scheduler.zonedClock names.
func TestSystemZonedClockUsesTheNamedZone(t *testing.T) {
	c := NewSystemZonedClock()

	utc, err := c.NowInZone("UTC")
	if err != nil {
		t.Fatalf("NowInZone(UTC): %v", err)
	}
	if utc.Location().String() != "UTC" {
		t.Errorf("NowInZone(UTC) located in %q, want UTC", utc.Location())
	}
	ny, err := c.NowInZone("America/New_York")
	if err != nil {
		t.Skipf("zoneinfo unavailable in this environment: %v", err)
	}
	if ny.Location().String() != "America/New_York" {
		t.Errorf("NowInZone located in %q, want America/New_York", ny.Location())
	}
	// Same instant, different wall clock: proof the zone is applied, not ignored.
	if utc.Sub(ny).Abs() > time.Second {
		t.Errorf("the two readings are not the same instant: %v vs %v", utc, ny)
	}
	if utc.Hour() == ny.Hour() && utc.Minute() == ny.Minute() {
		t.Errorf("UTC and New York wall clocks are identical (%v/%v); the zone was not applied", utc, ny)
	}
}

// TestSystemZonedClockUnknownZoneFailsClosed: an unloadable zone is an ERROR.
// Falling back to Local is exactly the silent lie this binding exists to end.
func TestSystemZonedClockUnknownZoneFailsClosed(t *testing.T) {
	c := NewSystemZonedClock()
	if _, err := c.NowInZone("Mars/Olympus_Mons"); err == nil {
		t.Fatal("NowInZone accepted an unknown zone; it must fail closed, never fall back to Local")
	}
	if _, err := c.NextAfter("Mars/Olympus_Mons", "0 3 * * *", time.Now()); err == nil {
		t.Fatal("NextAfter accepted an unknown zone; it must fail closed")
	}
}

// TestSystemZonedClockNextAfterIsZoned proves the SAME cron expression fires at
// different absolute instants in two zones — i.e. the zone participates in the
// computation rather than decorating the result.
func TestSystemZonedClockNextAfterIsZoned(t *testing.T) {
	c := NewSystemZonedClock()
	base := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)

	inUTC, err := c.NextAfter("UTC", "0 3 * * *", base)
	if err != nil {
		t.Fatalf("NextAfter(UTC): %v", err)
	}
	if inUTC.Hour() != 3 || inUTC.Minute() != 0 {
		t.Errorf("NextAfter(UTC) = %v, want 03:00 wall clock", inUTC)
	}
	inNY, err := c.NextAfter("America/New_York", "0 3 * * *", base)
	if err != nil {
		t.Skipf("zoneinfo unavailable: %v", err)
	}
	if inNY.Hour() != 3 || inNY.Minute() != 0 {
		t.Errorf("NextAfter(NY) = %v, want 03:00 wall clock", inNY)
	}
	if inUTC.Equal(inNY) {
		t.Errorf("the same cron fired at the same instant in UTC and New York (%v); the zone was ignored", inUTC)
	}
}

// TestSystemZonedClockCronShapes covers the standard-cron subset the clock
// claims to support, and pins that malformed input FAILS rather than misparses.
func TestSystemZonedClockCronShapes(t *testing.T) {
	c := NewSystemZonedClock()
	base := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC) // a Saturday

	ok := []struct {
		expr string
		want string
	}{
		{"* * * * *", "2026-07-25T00:01:00Z"},
		{"*/15 * * * *", "2026-07-25T00:15:00Z"},
		{"0 3 * * *", "2026-07-25T03:00:00Z"},
		{"30 4 1 * *", "2026-08-01T04:30:00Z"},
		{"0 0 * * 1", "2026-07-27T00:00:00Z"}, // next Monday
		{"0,30 6 * * *", "2026-07-25T06:00:00Z"},
		{"0 0 29 2 *", "2028-02-29T00:00:00Z"}, // leap day, inside the horizon
	}
	for _, tc := range ok {
		got, err := c.NextAfter("UTC", tc.expr, base)
		if err != nil {
			t.Errorf("NextAfter(%q): %v", tc.expr, err)
			continue
		}
		if got.UTC().Format(time.RFC3339) != tc.want {
			t.Errorf("NextAfter(%q) = %v, want %s", tc.expr, got.UTC().Format(time.RFC3339), tc.want)
		}
	}

	bad := []string{"", "0 3 * *", "0 3 * * * *", "99 3 * * *", "0 3 * * mon", "a b c d e", "0 3 5-1 * *", "0 3 * * */0"}
	for _, expr := range bad {
		if got, err := c.NextAfter("UTC", expr, base); err == nil {
			t.Errorf("NextAfter(%q) accepted a malformed expression and returned %v; it must fail closed", expr, got)
		}
	}
}

// ─── D3 + D5: the reference durable store is honest AND fails closed ───────

// TestFileHarnessOccurrenceStoreRoundTrip: history written by one instance is
// readable by a FRESH instance over the same path — the restart property.
func TestFileHarnessOccurrenceStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := NewFileHarnessOccurrenceStore(dir)

	if _, found, err := store.LastOccurrence(ctx, "nightly"); err != nil || found {
		t.Fatalf("empty store: got found=%v err=%v, want false,nil", found, err)
	}

	first := HarnessOccurrenceRecord{
		ScheduleID: "nightly", OccurrenceID: "occ-1",
		DueAt:   time.Date(2026, 7, 25, 3, 0, 0, 0, time.UTC),
		FiredAt: time.Date(2026, 7, 25, 3, 0, 1, 0, time.UTC),
		Outcome: "succeeded",
	}
	if err := store.RecordOccurrence(ctx, first); err != nil {
		t.Fatalf("RecordOccurrence: %v", err)
	}
	second := first
	second.OccurrenceID = "occ-2"
	second.DueAt = first.DueAt.Add(24 * time.Hour)
	second.FiredAt = first.FiredAt.Add(24 * time.Hour)
	if err := store.RecordOccurrence(ctx, second); err != nil {
		t.Fatalf("RecordOccurrence(2): %v", err)
	}

	// A DIFFERENT store instance over the same path — i.e. after a restart.
	reopened := NewFileHarnessOccurrenceStore(dir)
	got, found, err := reopened.LastOccurrence(ctx, "nightly")
	if err != nil || !found {
		t.Fatalf("after restart: found=%v err=%v, want true,nil", found, err)
	}
	if got.OccurrenceID != "occ-2" {
		t.Errorf("after restart LastOccurrence = %q, want occ-2", got.OccurrenceID)
	}
	// A schedule with no history is still "no history", not the other one's.
	if _, found, err := reopened.LastOccurrence(ctx, "other"); err != nil || found {
		t.Errorf("unrelated schedule: found=%v err=%v, want false,nil", found, err)
	}
}

// TestFileHarnessOccurrenceStoreUsesItsOwnFile pins the deliberate decision NOT
// to co-mingle occurrence history into the scheduler-owned scheduled_tasks.json,
// whose schema is a different record type entirely.
func TestFileHarnessOccurrenceStoreUsesItsOwnFile(t *testing.T) {
	store := NewFileHarnessOccurrenceStore("/ws")
	if strings.Contains(store.Path(), "scheduled_tasks.json") {
		t.Fatalf("store path %q collides with the scheduler's own state file", store.Path())
	}
	if want := filepath.Join("/ws", ".swarm", "harness_schedule_occurrences.json"); store.Path() != want {
		t.Errorf("store path = %q, want %q", store.Path(), want)
	}
}

// TestFileHarnessOccurrenceStoreConstructionTouchesNoFilesystem: constructing a
// store neither creates a directory nor reads a file, so a host that supplies it
// but never dispatches an occurrence pays nothing (HARD INVARIANT).
func TestFileHarnessOccurrenceStoreConstructionTouchesNoFilesystem(t *testing.T) {
	dir := t.TempDir()
	_ = NewFileHarnessOccurrenceStore(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("construction created %d filesystem entries, want 0", len(entries))
	}
}

// TestFileHarnessOccurrenceStoreUnwritableFailsClosed (§7.5 failure injection):
// a store that cannot persist must SAY SO. Reporting success for history it
// dropped would make a restart silently re-fire or silently skip occurrences.
func TestFileHarnessOccurrenceStoreUnwritableFailsClosed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not deny writes")
	}
	dir := t.TempDir()
	// Make .swarm exist but be unwritable, so MkdirAll succeeds and WriteFile
	// cannot: that isolates the WRITE failure specifically.
	swarmDir := filepath.Join(dir, ".swarm")
	if err := os.MkdirAll(swarmDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(swarmDir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(swarmDir, 0o755) })

	store := NewFileHarnessOccurrenceStore(dir)
	err := store.RecordOccurrence(context.Background(), HarnessOccurrenceRecord{
		ScheduleID: "nightly", OccurrenceID: "occ-1", Outcome: "succeeded",
	})
	if err == nil {
		t.Fatal("RecordOccurrence reported success against an unwritable store; it must fail closed")
	}
	if !strings.Contains(err.Error(), "occurrence store") {
		t.Errorf("error does not identify the store: %v", err)
	}
}

// TestFileHarnessOccurrenceStoreCorruptFailsClosed (§7.5 failure injection): a
// corrupt history file is an ERROR, never found=false. If corruption could
// masquerade as "no history yet", a misfire detector reading this store would
// silently replay or silently drop an entire backlog.
func TestFileHarnessOccurrenceStoreCorruptFailsClosed(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	store := NewFileHarnessOccurrenceStore(dir)
	if err := store.RecordOccurrence(ctx, HarnessOccurrenceRecord{
		ScheduleID: "nightly", OccurrenceID: "occ-1", Outcome: "succeeded",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{ this is not history"), 0o600); err != nil {
		t.Fatalf("corrupt: %v", err)
	}

	reopened := NewFileHarnessOccurrenceStore(dir)
	rec, found, err := reopened.LastOccurrence(ctx, "nightly")
	if err == nil {
		t.Fatalf("corrupt history read as found=%v rec=%+v; it must fail closed with an error", found, rec)
	}
	if found {
		t.Error("corrupt history reported found=true")
	}
	if !strings.Contains(err.Error(), "corrupt history") {
		t.Errorf("error does not name the corruption: %v", err)
	}
	// A subsequent WRITE must also refuse rather than silently truncating the
	// operator's damaged file and losing whatever could have been recovered.
	if err := reopened.RecordOccurrence(ctx, HarnessOccurrenceRecord{
		ScheduleID: "nightly", OccurrenceID: "occ-2",
	}); err == nil {
		t.Error("RecordOccurrence overwrote a corrupt history instead of refusing")
	}
}

// TestFileHarnessOccurrenceStoreRejectsEmptyScheduleID: an unattributable record
// is refused rather than written under an empty key where nothing can find it.
func TestFileHarnessOccurrenceStoreRejectsEmptyScheduleID(t *testing.T) {
	store := NewFileHarnessOccurrenceStore(t.TempDir())
	if err := store.RecordOccurrence(context.Background(), HarnessOccurrenceRecord{OccurrenceID: "occ"}); err == nil {
		t.Fatal("RecordOccurrence accepted an empty scheduleId")
	}
}

// TestFileHarnessOccurrenceStoreConcurrentWrites is the -race companion: the
// store is documented as safe for concurrent use and must not lose records.
func TestFileHarnessOccurrenceStoreConcurrentWrites(t *testing.T) {
	store := NewFileHarnessOccurrenceStore(t.TempDir())
	ctx := context.Background()
	const n = 16
	before := runtime.NumGoroutine()
	done := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			done <- store.RecordOccurrence(ctx, HarnessOccurrenceRecord{
				ScheduleID:   "nightly",
				OccurrenceID: "occ",
				FiredAt:      time.Unix(int64(i), 0).UTC(),
			})
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent RecordOccurrence: %v", err)
		}
	}
	rec, found, err := store.LastOccurrence(ctx, "nightly")
	if err != nil || !found {
		t.Fatalf("LastOccurrence: found=%v err=%v", found, err)
	}
	if want := time.Unix(int64(n-1), 0).UTC(); !rec.FiredAt.Equal(want) {
		t.Errorf("last record firedAt = %v, want %v (a record was lost)", rec.FiredAt, want)
	}
	harness9dAssertNoGoroutineLeak(t, before)
}

// ─── shared fakes ──────────────────────────────────────────────────────────

// harness9dFakeBindings implements EVERY binding interface at once. It exists
// only so a TEST can satisfy preflight; it is deliberately confined to _test.go
// so no production code path can ever reach a fake implementation (D3).
//
// Its methods return explicit "not implemented" errors rather than plausible
// zero values, so a test that accidentally depends on fake BEHAVIOUR (as opposed
// to fake PRESENCE) fails loudly.
type harness9dFakeBindings struct{}

var harness9dErrFake = errFakeBinding{}

type errFakeBinding struct{}

func (errFakeBinding) Error() string {
	return "harness test fake: this binding exists to satisfy preflight, not to do work"
}

func (*harness9dFakeBindings) NowInZone(string) (time.Time, error) {
	return time.Time{}, harness9dErrFake
}
func (*harness9dFakeBindings) NextAfter(string, string, time.Time) (time.Time, error) {
	return time.Time{}, harness9dErrFake
}
func (*harness9dFakeBindings) AcquireScheduleOwnership(context.Context, string) (HarnessScheduleOwnership, error) {
	return nil, harness9dErrFake
}
func (*harness9dFakeBindings) BeginOccurrence(context.Context, string, string) (bool, error) {
	return false, harness9dErrFake
}
func (*harness9dFakeBindings) EndOccurrence(context.Context, string, string) error {
	return harness9dErrFake
}
func (*harness9dFakeBindings) MissedOccurrences(context.Context, string, time.Time, time.Time) ([]time.Time, error) {
	return nil, harness9dErrFake
}
func (*harness9dFakeBindings) RecordOccurrence(context.Context, HarnessOccurrenceRecord) error {
	return harness9dErrFake
}
func (*harness9dFakeBindings) LastOccurrence(context.Context, string) (HarnessOccurrenceRecord, bool, error) {
	return HarnessOccurrenceRecord{}, false, harness9dErrFake
}
func (*harness9dFakeBindings) NextRetry(context.Context, string, int) (time.Duration, bool, error) {
	return 0, false, harness9dErrFake
}
func (*harness9dFakeBindings) AcquireOccurrenceSlot(context.Context, string) (HarnessOccurrenceSlot, error) {
	return nil, harness9dErrFake
}
func (*harness9dFakeBindings) DispatchOccurrence(context.Context, HarnessOccurrenceDispatch) error {
	return harness9dErrFake
}
func (*harness9dFakeBindings) DeliverOccurrenceResult(context.Context, HarnessOccurrenceResult) error {
	return harness9dErrFake
}
func (*harness9dFakeBindings) ResolveUnattended(context.Context, HarnessApprovalAsk) (HarnessApprovalDecision, error) {
	return "", harness9dErrFake
}
func (*harness9dFakeBindings) RequestApproval(context.Context, HarnessApprovalAsk) (HarnessApprovalDecision, error) {
	return "", harness9dErrFake
}

// harness9dAllBindings returns a registry supplying every kind, for tests that
// need preflight to PASS.
func harness9dAllBindings() HarnessBindings {
	f := &harness9dFakeBindings{}
	return HarnessBindings{
		SchedulerZonedClock:         f,
		SchedulerSingleOwner:        f,
		SchedulerOverlapGuard:       f,
		SchedulerMisfireDetector:    f,
		SchedulerDurableStore:       f,
		SchedulerRetryDriver:        f,
		SchedulerConcurrencyLimiter: f,
		SchedulerTargetDispatcher:   f,
		SchedulerResultSink:         f,
		ApprovalUnattendedResolver:  f,
		ApprovalBroker:              f,
	}
}

// harness9dAssertNoGoroutineLeak compares the live goroutine count against a
// baseline, tolerating the scheduler's own settling delay.
func harness9dAssertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutine leak: %d before, %d after", before, runtime.NumGoroutine())
}
