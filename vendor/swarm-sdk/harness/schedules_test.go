package harness

import (
	"strings"
	"testing"
)

// baseSchedulesManifest returns a minimal valid manifest with the given raw
// block spliced in verbatim at document level.
func baseSchedulesManifest(block string) string {
	return "apiVersion: swarm.ai/v1alpha1\n" +
		"kind: Harness\n" +
		"metadata:\n  name: schedulestest\n" +
		"provider:\n  id: anthropic\n  model: claude-x\n" +
		"agent:\n  systemPrompt:\n    inline: \"hi\"\n  tools: []\n" +
		block
}

// scheduleTargets declares the reference vocabulary a schedule target resolves
// against: one profile and one subagent (Phase 8a sections).
const scheduleTargets = "profiles:\n" +
	"  - id: fast\n    provider: anthropic\n    model: claude-haiku\n" +
	"agents:\n" +
	"  - id: reporter\n    systemPrompt:\n      inline: \"report\"\n    tools: []\n"

// scheduleFieldOrder is the emission order used by scheduleBlock. Keeping it
// explicit (rather than ranging a map) keeps every fixture byte-stable.
var scheduleFieldOrder = []string{
	"id", "cron", "timezone", "overlap", "misfire", "retry",
	"concurrency", "target", "approvalPosture", "resultSink", "ack",
}

// scheduleBlock builds a `schedules:` block with one fully-declared schedule,
// applying per-key overrides. An override of "" OMITS that key, which is how
// the D1 "every dimension is required" cases are expressed.
func scheduleBlock(overrides map[string]string) string {
	f := map[string]string{
		"id":              "  - id: nightly\n",
		"cron":            "    cron: \"0 3 * * *\"\n",
		"timezone":        "    timezone: UTC\n",
		"overlap":         "    overlap: skip\n",
		"misfire":         "    misfire: drop\n",
		"retry":           "    retry:\n      policy: none\n",
		"concurrency":     "    concurrency:\n      policy: unlimited\n",
		"target":          "    target:\n      kind: agent\n      id: reporter\n",
		"approvalPosture": "    approvalPosture: deny\n",
		"resultSink":      "    resultSink:\n      kind: discard\n",
		"ack":             "    acknowledgeUnenforced:\n      - overlap\n",
	}
	for k, v := range overrides {
		f[k] = v
	}
	out := "schedules:\n"
	for _, k := range scheduleFieldOrder {
		out += f[k]
	}
	return out
}

// schedulesManifest is the common case: targets + one schedule with overrides.
func schedulesManifest(overrides map[string]string) string {
	return baseSchedulesManifest(scheduleTargets + scheduleBlock(overrides))
}

// --- omitted / empty / digest backward compatibility -----------------------

// TestSchedulesOmittedAndEmptyValid: an omitted section and an explicitly empty
// `schedules: []` are both valid, carry zero schedules and zero bindings,
// produce the SAME digest, and add no key to the explained report.
func TestSchedulesOmittedAndEmptyValid(t *testing.T) {
	p := compileOK(t, baseSchedulesManifest(""))
	if len(p.Schedules()) != 0 {
		t.Errorf("omitted: want zero schedules, got %v", p.Schedules())
	}
	if len(p.RequiredRuntimeBindings()) != 0 {
		t.Errorf("omitted: want zero runtime bindings, got %v", p.RequiredRuntimeBindings())
	}
	if _, ok := p.Schedule("nightly"); ok {
		t.Errorf("omitted: Schedule() should report ok=false")
	}
	digestOmitted := p.Digest()

	p2 := compileOK(t, baseSchedulesManifest("schedules: []\n"))
	if len(p2.Schedules()) != 0 {
		t.Errorf("empty: want zero schedules, got %v", p2.Schedules())
	}
	if p2.Digest() != digestOmitted {
		t.Errorf("`schedules: []` vs omitted digests differ: %q vs %q", p2.Digest(), digestOmitted)
	}

	js, err := p2.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	for _, key := range []string{"\"schedules\"", "\"runtimeBindings\""} {
		if strings.Contains(string(js), key) {
			t.Errorf("an unused schedules section must not add %s to ExplainJSON", key)
		}
	}
	for _, fs := range p2.Provenance() {
		if strings.HasPrefix(fs.Field, "schedules") {
			t.Errorf("unused schedules section leaked provenance %+v", fs)
		}
	}
}

// TestSchedulesDigestBackwardCompatiblePre9b pins EXACT plan digests captured
// from a throwaway detached `git worktree` at HEAD (b00cb5d7, pre-9b) for
// manifests that declare no schedules. They must be BYTE IDENTICAL after this
// slice: the Document field and both ExplainReport fields are `omitempty`, so an
// absent section contributes nothing to the canonical JSON the digest hashes.
func TestSchedulesDigestBackwardCompatiblePre9b(t *testing.T) {
	const (
		pre9bBase    = "sha256:9935d8682d0698d474a67a0ce4cf76f6e305206d15f0122199821f5edf847348"
		pre9bTargets = "sha256:d6b951ad29bf5824d4cc0e4849f73b9d51c4b427afef8a8dab0c0259f6bc206d"
	)
	if got := compileOK(t, baseSchedulesManifest("")).Digest(); got != pre9bBase {
		t.Fatalf("digest changed for a manifest without a schedules section:\n got  %s\n want %s", got, pre9bBase)
	}
	// A manifest that declares the sections a schedule TARGETS is likewise
	// untouched as long as it declares no schedules.
	if got := compileOK(t, baseSchedulesManifest(scheduleTargets)).Digest(); got != pre9bTargets {
		t.Fatalf("digest changed for a profiles+agents manifest:\n got  %s\n want %s", got, pre9bTargets)
	}
}

// --- the happy path ---------------------------------------------------------

// TestScheduleValidFullDeclaration compiles a fully-declared schedule and pins
// what the plan carries: all eight dimensions in canonical order, each with the
// enforcement class its VALUE earns, and the union of required bindings.
func TestScheduleValidFullDeclaration(t *testing.T) {
	p := compileOK(t, schedulesManifest(nil))
	scheds := p.Schedules()
	if len(scheds) != 1 {
		t.Fatalf("want 1 schedule, got %d", len(scheds))
	}
	s := scheds[0]
	if s.ID != "nightly" || s.Cron != "0 3 * * *" {
		t.Errorf("id/cron = %q/%q", s.ID, s.Cron)
	}
	if len(s.Dimensions) != len(scheduleDimensionOrder) {
		t.Fatalf("want all %d dimensions reported, got %d", len(scheduleDimensionOrder), len(s.Dimensions))
	}
	for i, dim := range scheduleDimensionOrder {
		if s.Dimensions[i].Dimension != dim {
			t.Errorf("dimension %d = %q, want canonical %q", i, s.Dimensions[i].Dimension, dim)
		}
	}
	want := map[ScheduleDimension]struct {
		value string
		class EnforcementClass
	}{
		ScheduleDimTimezone:        {"UTC", EnforcementBindingRequired},
		ScheduleDimOverlap:         {"skip", EnforcementBindingRequired},
		ScheduleDimMisfire:         {"drop", BudgetEnforced},
		ScheduleDimRetry:           {"none", BudgetEnforced},
		ScheduleDimConcurrency:     {"unlimited", BudgetEnforced},
		ScheduleDimTarget:          {"agent", EnforcementBindingRequired},
		ScheduleDimApprovalPosture: {"deny", EnforcementBindingRequired},
		ScheduleDimResultSink:      {"discard", BudgetEnforced},
	}
	for dim, w := range want {
		d, ok := s.Dimension(dim)
		if !ok {
			t.Errorf("%s: not reported", dim)
			continue
		}
		if d.Value != w.value || d.Enforcement != w.class {
			t.Errorf("%s: value/class = %q/%q, want %q/%q", dim, d.Value, d.Enforcement, w.value, w.class)
		}
	}
	// The overlap row is the path-dependent, acknowledged one.
	ov, _ := s.Dimension(ScheduleDimOverlap)
	if !ov.PathDependent || !ov.Acknowledged {
		t.Errorf("overlap: pathDependent=%v acknowledged=%v, want both true", ov.PathDependent, ov.Acknowledged)
	}
	// The target row carries the referenced id.
	tg, _ := s.Dimension(ScheduleDimTarget)
	if tg.Ref != "reporter" {
		t.Errorf("target ref = %q, want %q", tg.Ref, "reporter")
	}
	// An enforced dimension requires no binding at all.
	mf, _ := s.Dimension(ScheduleDimMisfire)
	if len(mf.Bindings) != 0 {
		t.Errorf("misfire: drop is enforced today and must require no binding, got %v", mf.Bindings)
	}
	if !p.hasProvenance("schedules.nightly") {
		t.Errorf("a declared schedule must record provenance")
	}
}

// hasProvenance is a small test helper.
func (p *Plan) hasProvenance(field string) bool {
	for _, fs := range p.Provenance() {
		if fs.Field == field {
			return true
		}
	}
	return false
}

// --- ids --------------------------------------------------------------------

func TestScheduleIDMissingAndDuplicate(t *testing.T) {
	// An entry with no id at all: the sequence item still needs one key, so the
	// cron key carries the entry.
	missing := baseSchedulesManifest(scheduleTargets + "schedules:\n  - cron: \"0 3 * * *\"\n")
	if ds := compileErr(t, missing, syntheticYAML); !hasCode(ds, "harness.schedules.id.missing") {
		t.Errorf("missing id not rejected: %v", ds)
	}
	empty := baseSchedulesManifest(scheduleTargets + scheduleBlock(map[string]string{"id": "  - id: \"\"\n"}))
	if ds := compileErr(t, empty, syntheticYAML); !hasCode(ds, "harness.schedules.id.missing") {
		t.Errorf("empty id not rejected: %v", ds)
	}
	dup := baseSchedulesManifest(scheduleTargets + scheduleBlock(nil) + strings.TrimPrefix(scheduleBlock(nil), "schedules:\n"))
	if ds := compileErr(t, dup, syntheticYAML); !hasCode(ds, "harness.schedules.id.duplicate") {
		t.Errorf("duplicate id not rejected: %v", ds)
	}
}

// --- D1: every dimension is required ---------------------------------------

// TestScheduleEveryDimensionRequired omits each of the eight dimensions in turn
// and requires a compile error that NAMES the schedule and the dimension. This
// is the "no silent defaults" guarantee, tested one dimension at a time.
func TestScheduleEveryDimensionRequired(t *testing.T) {
	cases := map[string]ScheduleDimension{
		"timezone":        ScheduleDimTimezone,
		"overlap":         ScheduleDimOverlap,
		"misfire":         ScheduleDimMisfire,
		"retry":           ScheduleDimRetry,
		"concurrency":     ScheduleDimConcurrency,
		"target":          ScheduleDimTarget,
		"approvalPosture": ScheduleDimApprovalPosture,
		"resultSink":      ScheduleDimResultSink,
	}
	for key, dim := range cases {
		raw := schedulesManifest(map[string]string{key: ""})
		ds := compileErr(t, raw, syntheticYAML)
		code := "harness.schedules." + string(dim) + ".missing"
		if !hasCode(ds, code) {
			t.Errorf("omitting %s: want code %s, got %v", key, code, ds)
			continue
		}
		if msg := diagMessage(ds, code); !strings.Contains(msg, "nightly") {
			t.Errorf("omitting %s: diagnostic must name the schedule, got %q", key, msg)
		}
	}
}

// TestScheduleSubPolicyKeysRequired: the object-shaped dimensions must declare
// their discriminator too — an empty object is not a default.
func TestScheduleSubPolicyKeysRequired(t *testing.T) {
	cases := map[string]string{
		"retry":       "harness.schedules.retry.policy.missing",
		"concurrency": "harness.schedules.concurrency.policy.missing",
		"resultSink":  "harness.schedules.resultSink.kind.missing",
	}
	blocks := map[string]string{
		"retry":       "    retry:\n      maxAttempts: 2\n",
		"concurrency": "    concurrency:\n      max: 2\n",
		"resultSink":  "    resultSink:\n      id: audit\n",
	}
	for key, code := range cases {
		ds := compileErr(t, schedulesManifest(map[string]string{key: blocks[key]}), syntheticYAML)
		if !hasCode(ds, code) {
			t.Errorf("%s without its discriminator: want %s, got %v", key, code, ds)
		}
	}
}

// --- D2: timezone -----------------------------------------------------------

// TestScheduleTimezoneCases covers UTC (must work with no tzdata), a real zone,
// empty, "Local" (the banned inheritance), and a bogus name.
func TestScheduleTimezoneCases(t *testing.T) {
	valid := []string{"UTC", "America/New_York"}
	for _, zone := range valid {
		p := compileOK(t, schedulesManifest(map[string]string{"timezone": "    timezone: " + zone + "\n"}))
		s, ok := p.Schedule("nightly")
		if !ok {
			t.Fatalf("%s: schedule not carried", zone)
		}
		tz, _ := s.Dimension(ScheduleDimTimezone)
		if tz.Value != zone {
			t.Errorf("%s: carried zone = %q", zone, tz.Value)
		}
	}
	bad := map[string]string{
		"":            "harness.schedules.timezone.missing",
		"Local":       "harness.schedules.timezone.invalid",
		"+05:00":      "harness.schedules.timezone.invalid",
		"Mars/Olymus": "harness.schedules.timezone.invalid",
	}
	for zone, code := range bad {
		block := "    timezone: \"" + zone + "\"\n"
		if zone == "" {
			block = ""
		}
		ds := compileErr(t, schedulesManifest(map[string]string{"timezone": block}), syntheticYAML)
		if !hasCode(ds, code) {
			t.Errorf("timezone %q: want %s, got %v", zone, code, ds)
		}
	}
	// The "Local" rejection must explain itself rather than just refusing.
	ds := compileErr(t, schedulesManifest(map[string]string{"timezone": "    timezone: Local\n"}), syntheticYAML)
	if msg := diagMessage(ds, "harness.schedules.timezone.invalid"); !strings.Contains(msg, "daemon") {
		t.Errorf("Local rejection should explain the inheritance hazard, got %q", msg)
	}
}

// --- D6: cron SHAPE ---------------------------------------------------------

func TestScheduleCronShapes(t *testing.T) {
	valid := []string{
		"0 3 * * *", "*/15 * * * *", "0 0 1 1 *", "5,35 2-4 * * MON-FRI",
		"0 12 ? * SUN", "0 0 1 JAN *", "30 1 1-7 * 1",
	}
	for _, expr := range valid {
		if reason, ok := validateCronShape(expr); !ok {
			t.Errorf("valid cron %q rejected: %s", expr, reason)
		}
	}
	invalid := []string{
		"", "0 3 * *", "0 3 * * * *", "@daily", "@every 1h",
		"CRON_TZ=America/New_York 0 3 * * *", "TZ=UTC 0 3 * * *",
		"60 3 * * *", "0 24 * * *", "0 3 0 * *", "0 3 * 13 *", "0 3 * * 7",
		"0 3 * * MONDAY", "*/0 * * * *", "5-1 * * * *", "0 3 * * */a", "0 -3 * * *",
	}
	for _, expr := range invalid {
		if _, ok := validateCronShape(expr); ok {
			t.Errorf("invalid cron %q accepted", expr)
		}
	}
	// End to end: a malformed shape is a compile error naming the schedule.
	ds := compileErr(t, schedulesManifest(map[string]string{"cron": "    cron: \"@daily\"\n"}), syntheticYAML)
	if !hasCode(ds, "harness.schedules.cron.invalid") {
		t.Errorf("descriptor cron not rejected: %v", ds)
	}
	ds = compileErr(t, schedulesManifest(map[string]string{"cron": "    cron: \"CRON_TZ=UTC 0 3 * * *\"\n"}), syntheticYAML)
	if msg := diagMessage(ds, "harness.schedules.cron.invalid"); !strings.Contains(msg, "timezone") {
		t.Errorf("a TZ-prefixed cron must point at the timezone dimension, got %q", msg)
	}
}

// --- D4: target reference integrity ----------------------------------------

func TestScheduleTargetReferences(t *testing.T) {
	// A declared profile id resolves.
	p := compileOK(t, schedulesManifest(map[string]string{"target": "    target:\n      kind: profile\n      id: fast\n"}))
	s, _ := p.Schedule("nightly")
	tg, _ := s.Dimension(ScheduleDimTarget)
	if tg.Value != "profile" || tg.Ref != "fast" {
		t.Errorf("profile target = %q/%q", tg.Value, tg.Ref)
	}

	bad := map[string]string{
		"    target:\n      kind: agent\n      id: nope\n":   "harness.schedules.target.unknownAgent",
		"    target:\n      kind: profile\n      id: nope\n": "harness.schedules.target.unknownProfile",
		"    target:\n      kind: agent\n":                   "harness.schedules.target.id.missing",
		"    target:\n      id: reporter\n":                  "harness.schedules.target.kind.missing",
		"    target:\n      kind: cluster\n      id: x\n":    "harness.schedules.target.unknown",
	}
	for block, code := range bad {
		ds := compileErr(t, schedulesManifest(map[string]string{"target": block}), syntheticYAML)
		if !hasCode(ds, code) {
			t.Errorf("target %q: want %s, got %v", block, code, ds)
		}
	}
}

// TestScheduleWorkflowTargetNeedsDeclaredWorkflow is the Phase 9b assertion UPDATED
// by Phase 9c, which closed the forward reference this test used to pin. The 9b
// rule was "a workflow target is never silently accepted or carried as a dangling
// reference"; that rule is UNCHANGED — only the mechanism moved from "always
// rejected, no workflows section exists" to "resolved against the declared
// workflows[] ids, and rejected when the id is not declared".
//
// A schedule that names a workflow while the document declares NO workflows section
// therefore still fails to compile, which is the property 9b actually cared about.
// The positive case (a declared id resolves, with an honest enforcement class and
// its bindings) is TestScheduleWorkflowTargetResolves in workflows_test.go, where
// the referenced file fixture lives.
func TestScheduleWorkflowTargetNeedsDeclaredWorkflow(t *testing.T) {
	ds := compileErr(t, schedulesManifest(map[string]string{
		"target": "    target:\n      kind: workflow\n      id: nightly-flow\n",
	}), syntheticYAML)
	msg := diagMessage(ds, "harness.schedules.target.unknownWorkflow")
	if msg == "" {
		t.Fatalf("a workflow target with no declared workflow must still be rejected: %v", ds)
	}
	if !strings.Contains(msg, "nightly-flow") || !strings.Contains(msg, "workflows[]") {
		t.Errorf("the rejection must name the undeclared id and the section that would declare it, got %q", msg)
	}
	if hasCode(ds, "harness.schedules.target.workflowUnsupported") {
		t.Errorf("Phase 9c replaced the unsupported-kind branch; that code must no longer be emitted")
	}
}

// --- D3: the acknowledgement gate ------------------------------------------

// TestScheduleOverlapRequiresAcknowledgement: EVERY overlap value is refused
// without the acknowledgement and accepted with it, because the hazard is the
// dimension's path-dependence rather than any one policy.
func TestScheduleOverlapRequiresAcknowledgement(t *testing.T) {
	for _, v := range []ScheduleOverlap{
		ScheduleOverlapSkip, ScheduleOverlapAllowConcurrent,
		ScheduleOverlapQueue, ScheduleOverlapCancelPrevious,
	} {
		block := "    overlap: " + string(v) + "\n"
		ds := compileErr(t, schedulesManifest(map[string]string{"overlap": block, "ack": ""}), syntheticYAML)
		if !hasCode(ds, "harness.schedules.overlap.unacknowledged") {
			t.Errorf("overlap %q without acknowledgement was accepted: %v", v, ds)
		}
		if msg := diagMessage(ds, "harness.schedules.overlap.unacknowledged"); !strings.Contains(msg, "PATH-DEPENDENT") {
			t.Errorf("overlap %q: diagnostic must state the path-dependence, got %q", v, msg)
		}
		p := compileOK(t, schedulesManifest(map[string]string{"overlap": block}))
		s, _ := p.Schedule("nightly")
		d, _ := s.Dimension(ScheduleDimOverlap)
		if !d.Acknowledged || d.Enforcement != EnforcementBindingRequired {
			t.Errorf("overlap %q acknowledged: acknowledged=%v class=%q (acknowledging must NOT promote the class)", v, d.Acknowledged, d.Enforcement)
		}
	}
}

// TestScheduleAcknowledgementListRules mirrors budgets.acknowledgeObserved:
// empty, duplicate, unknown, and not-required (dangling / wrong class) entries
// are all rejected.
func TestScheduleAcknowledgementListRules(t *testing.T) {
	cases := map[string]string{
		"    acknowledgeUnenforced:\n      - \"\"\n":                     "harness.schedules.acknowledgeUnenforced.empty",
		"    acknowledgeUnenforced:\n      - overlap\n      - overlap\n": "harness.schedules.acknowledgeUnenforced.duplicate",
		"    acknowledgeUnenforced:\n      - overlap\n      - jitter\n":  "harness.schedules.acknowledgeUnenforced.unknown",
		// misfire: drop is ENFORCED, so acknowledging it is a wrong-class entry.
		"    acknowledgeUnenforced:\n      - overlap\n      - misfire\n": "harness.schedules.acknowledgeUnenforced.notRequired",
		// timezone is bindingRequired, NOT acknowledgement-gated: also wrong class.
		"    acknowledgeUnenforced:\n      - overlap\n      - timezone\n": "harness.schedules.acknowledgeUnenforced.notRequired",
	}
	for block, code := range cases {
		ds := compileErr(t, schedulesManifest(map[string]string{"ack": block}), syntheticYAML)
		if !hasCode(ds, code) {
			t.Errorf("ack block %q: want %s, got %v", block, code, ds)
		}
	}
}

// --- D5: approval posture ---------------------------------------------------

// TestSchedulePostureWidening pins the never-widen rule across the full matrix
// of plan posture x schedule posture.
func TestSchedulePostureWidening(t *testing.T) {
	type tc struct {
		planMode string
		posture  string
		wantOK   bool
	}
	cases := []tc{
		// readonly is the strictest plan posture: only deny is equal-or-stricter.
		{"readonly", "deny", true},
		{"readonly", "broker", false},
		{"readonly", "autoApprove", false},
		// interactive: a broker is equal, autoApprove is wider.
		{"interactive", "deny", true},
		{"interactive", "broker", true},
		{"interactive", "autoApprove", false},
		// yolo already auto-approves, so nothing can widen it.
		{"yolo", "deny", true},
		{"yolo", "broker", true},
		{"yolo", "autoApprove", true},
	}
	for _, c := range cases {
		raw := baseSchedulesManifest(
			"permissions:\n  approvalMode: " + c.planMode + "\n" +
				scheduleTargets + scheduleBlock(map[string]string{"approvalPosture": "    approvalPosture: " + c.posture + "\n"}))
		if c.wantOK {
			p := compileOK(t, raw)
			s, _ := p.Schedule("nightly")
			if d, _ := s.Dimension(ScheduleDimApprovalPosture); d.Value != c.posture {
				t.Errorf("%s/%s: carried posture = %q", c.planMode, c.posture, d.Value)
			}
			continue
		}
		ds := compileErr(t, raw, syntheticYAML)
		if !hasCode(ds, "harness.schedules.approvalPosture.widensPermissions") {
			t.Errorf("%s/%s: widening not rejected: %v", c.planMode, c.posture, ds)
		}
	}
	// A plan approvalMode outside the closed vocabulary cannot be compared, so
	// a posture is refused rather than silently assumed safe.
	raw := baseSchedulesManifest("permissions:\n  approvalMode: whatever\n" + scheduleTargets + scheduleBlock(nil))
	if ds := compileErr(t, raw, syntheticYAML); !hasCode(ds, "harness.schedules.approvalPosture.planModeUnknown") {
		t.Errorf("uncomparable plan approvalMode not refused: %v", ds)
	}
}

// --- closed enums and numeric validity --------------------------------------

func TestScheduleUnknownEnumValuesRejected(t *testing.T) {
	cases := map[string]string{
		"overlap":         "    overlap: sometimes\n",
		"misfire":         "    misfire: maybe\n",
		"retry":           "    retry:\n      policy: keepTrying\n",
		"concurrency":     "    concurrency:\n      policy: some\n",
		"approvalPosture": "    approvalPosture: askNicely\n",
		"resultSink":      "    resultSink:\n      kind: webhook\n",
	}
	for key, block := range cases {
		ds := compileErr(t, schedulesManifest(map[string]string{key: block}), syntheticYAML)
		code := "harness.schedules." + key + ".unknown"
		if !hasCode(ds, code) {
			t.Errorf("%s: want %s, got %v", key, code, ds)
		}
	}
}

// TestScheduleNumericValidity: numbers must match their policy — required and
// positive where they act, ABSENT where they would be ignored.
func TestScheduleNumericValidity(t *testing.T) {
	cases := map[string]struct {
		key, block, code string
	}{
		"numbers with none":  {"retry", "    retry:\n      policy: none\n      maxAttempts: 3\n", "harness.schedules.retry.numbersWithNone"},
		"zero attempts":      {"retry", "    retry:\n      policy: fixedDelay\n      delaySeconds: 5\n", "harness.schedules.retry.maxAttempts.invalid"},
		"negative attempts":  {"retry", "    retry:\n      policy: fixedDelay\n      maxAttempts: -1\n      delaySeconds: 5\n", "harness.schedules.retry.maxAttempts.invalid"},
		"zero delay":         {"retry", "    retry:\n      policy: fixedDelay\n      maxAttempts: 2\n", "harness.schedules.retry.delaySeconds.invalid"},
		"max with unlimited": {"concurrency", "    concurrency:\n      policy: unlimited\n      max: 3\n", "harness.schedules.concurrency.maxWithUnlimited"},
		"zero max":           {"concurrency", "    concurrency:\n      policy: maxConcurrent\n", "harness.schedules.concurrency.max.invalid"},
		"negative max":       {"concurrency", "    concurrency:\n      policy: maxConcurrent\n      max: -2\n", "harness.schedules.concurrency.max.invalid"},
		"id with discard":    {"resultSink", "    resultSink:\n      kind: discard\n      id: audit\n", "harness.schedules.resultSink.idWithDiscard"},
		"named without id":   {"resultSink", "    resultSink:\n      kind: named\n", "harness.schedules.resultSink.id.missing"},
	}
	for name, c := range cases {
		ds := compileErr(t, schedulesManifest(map[string]string{c.key: c.block}), syntheticYAML)
		if !hasCode(ds, c.code) {
			t.Errorf("%s: want %s, got %v", name, c.code, ds)
		}
	}
	// The valid non-trivial forms compile and carry their numbers.
	p := compileOK(t, schedulesManifest(map[string]string{
		"retry":       "    retry:\n      policy: exponentialBackoff\n      maxAttempts: 3\n      delaySeconds: 30\n",
		"concurrency": "    concurrency:\n      policy: maxConcurrent\n      max: 2\n",
		"resultSink":  "    resultSink:\n      kind: named\n      id: audit-log\n",
	}))
	s, _ := p.Schedule("nightly")
	r, _ := s.Dimension(ScheduleDimRetry)
	if r.MaxAttempts != 3 || r.DelaySeconds != 30 || r.Enforcement != EnforcementBindingRequired {
		t.Errorf("retry row = %+v", r)
	}
	c, _ := s.Dimension(ScheduleDimConcurrency)
	if c.MaxConcurrent != 2 {
		t.Errorf("concurrency row = %+v", c)
	}
	sink, _ := s.Dimension(ScheduleDimResultSink)
	if sink.Ref != "audit-log" {
		t.Errorf("resultSink row = %+v", sink)
	}
}

// --- required runtime bindings (what Phase 9d preflight enumerates) ---------

// TestScheduleRuntimeBindingsEnumerable: a plan makes its unmet HOST
// requirements explicit and enumerable, each naming the fields that require it
// and the reason it is missing. This is the compile-vs-preflight contract: none
// of these is a compile error, all of them must be checkable before execution.
func TestScheduleRuntimeBindingsEnumerable(t *testing.T) {
	p := compileOK(t, schedulesManifest(map[string]string{
		"retry":           "    retry:\n      policy: fixedDelay\n      maxAttempts: 2\n      delaySeconds: 5\n",
		"concurrency":     "    concurrency:\n      policy: maxConcurrent\n      max: 2\n",
		"misfire":         "    misfire: backfillAll\n",
		"resultSink":      "    resultSink:\n      kind: named\n      id: audit-log\n",
		"approvalPosture": "    approvalPosture: broker\n",
	}))
	got := make(map[RuntimeBindingKind][]string)
	for _, b := range p.RequiredRuntimeBindings() {
		got[b.Binding] = b.RequiredBy
		if b.Reason == "" {
			t.Errorf("binding %s has no reason; preflight would have nothing to quote", b.Binding)
		}
	}
	want := []RuntimeBindingKind{
		BindingSchedulerZonedClock,
		BindingSchedulerSingleOwner,
		BindingSchedulerOverlapGuard,
		BindingSchedulerMisfireDetector,
		BindingSchedulerDurableStore,
		BindingSchedulerRetryDriver,
		BindingSchedulerConcurrencyLimiter,
		BindingSchedulerTargetDispatcher,
		BindingSchedulerResultSink,
		BindingApprovalBroker,
	}
	for _, w := range want {
		if _, ok := got[w]; !ok {
			t.Errorf("binding %s not enumerated on the plan", w)
		}
	}
	// Single-owner is required by the mere existence of a schedule, so it is
	// attributed to the schedule itself rather than to any one dimension.
	if fields := got[BindingSchedulerSingleOwner]; len(fields) != 1 || fields[0] != "schedules[0]" {
		t.Errorf("singleOwner requiredBy = %v, want [schedules[0]]", fields)
	}
	if fields := got[BindingSchedulerZonedClock]; len(fields) != 1 || fields[0] != "schedules[0].timezone" {
		t.Errorf("zonedClock requiredBy = %v", fields)
	}
	// Canonical order, so the report and the digest are deterministic.
	bindings := p.RequiredRuntimeBindings()
	rank := map[RuntimeBindingKind]int{}
	for i, k := range runtimeBindingOrder {
		rank[k] = i
	}
	for i := 1; i < len(bindings); i++ {
		if rank[bindings[i-1].Binding] >= rank[bindings[i].Binding] {
			t.Errorf("bindings not in canonical order at %d: %v", i, bindings)
		}
	}
	// A fully enforced-today posture still requires single-owner and the two
	// dimensions that have no in-tree implementation, and nothing else.
	p2 := compileOK(t, schedulesManifest(nil))
	for _, b := range p2.RequiredRuntimeBindings() {
		switch b.Binding {
		case BindingSchedulerSingleOwner, BindingSchedulerZonedClock, BindingSchedulerOverlapGuard,
			BindingSchedulerTargetDispatcher, BindingApprovalResolver:
		default:
			t.Errorf("unexpected binding %s for an otherwise enforced-today schedule", b.Binding)
		}
	}
}

// --- Explain / redaction ----------------------------------------------------

// TestScheduleExplainShowsEveryDimension: the redacted report carries every
// dimension with its enforcement class, plus the binding requirements — and
// nothing that could be a prompt body or a credential.
func TestScheduleExplainShowsEveryDimension(t *testing.T) {
	p := compileOK(t, schedulesManifest(nil))
	js, err := p.ExplainJSON()
	if err != nil {
		t.Fatalf("ExplainJSON: %v", err)
	}
	out := string(js)
	for _, dim := range scheduleDimensionOrder {
		if !strings.Contains(out, "\""+string(dim)+"\"") {
			t.Errorf("ExplainJSON omits dimension %q", dim)
		}
	}
	for _, want := range []string{
		"\"nightly\"", "\"0 3 * * *\"", "\"UTC\"", "\"reporter\"",
		string(EnforcementBindingRequired), string(BudgetEnforced),
		"\"runtimeBindings\"", string(BindingSchedulerSingleOwner),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("ExplainJSON omits %q", want)
		}
	}
	// The report must never carry prompt text; a schedule has no prompt at all,
	// and the agent prompt it targets stays hashed.
	if strings.Contains(out, "report\"") && strings.Contains(out, "\"prompt\"") {
		t.Errorf("ExplainJSON leaked a prompt body")
	}
	// Declaring a schedule must change the digest (it is semantic content).
	if p.Digest() == compileOK(t, baseSchedulesManifest(scheduleTargets)).Digest() {
		t.Errorf("a declared schedule must be part of the plan digest")
	}
}

// --- immutability -----------------------------------------------------------

// TestScheduleAccessorsAreDefensiveCopies: mutating a returned value must never
// reach the immutable Plan, at every nesting level.
func TestScheduleAccessorsAreDefensiveCopies(t *testing.T) {
	p := compileOK(t, schedulesManifest(nil))

	got := p.Schedules()
	got[0].ID = "mutated"
	got[0].Dimensions[0].Value = "mutated"
	got[0].Dimensions[0].Bindings[0] = "mutated"
	got[0].Bindings[0] = "mutated"

	again := p.Schedules()
	if again[0].ID != "nightly" || again[0].Dimensions[0].Value != "UTC" {
		t.Errorf("Schedules() is not a defensive copy: %+v", again[0])
	}
	if again[0].Bindings[0] == "mutated" || again[0].Dimensions[0].Bindings[0] == "mutated" {
		t.Errorf("nested binding slices are not defensively copied: %+v", again[0])
	}

	one, ok := p.Schedule("nightly")
	if !ok {
		t.Fatalf("Schedule(nightly) not found")
	}
	one.Dimensions[0].Value = "mutated"
	if again2, _ := p.Schedule("nightly"); again2.Dimensions[0].Value != "UTC" {
		t.Errorf("Schedule() is not a defensive copy")
	}

	binds := p.RequiredRuntimeBindings()
	binds[0].RequiredBy[0] = "mutated"
	if again3 := p.RequiredRuntimeBindings(); again3[0].RequiredBy[0] == "mutated" {
		t.Errorf("RequiredRuntimeBindings() is not a defensive copy")
	}
}

// --- the fact table ---------------------------------------------------------

// TestScheduleFactsTablePinned pins the classification of every (dimension,
// value) pair. If a runtime change makes one of these genuinely honoured, this
// test is what forces the table — and the reasoning around it — to be updated
// together with that change rather than drifting quietly.
func TestScheduleFactsTablePinned(t *testing.T) {
	want := map[dimValue]EnforcementClass{
		{ScheduleDimTimezone, scheduleAnyValue}:     EnforcementBindingRequired,
		{ScheduleDimOverlap, "skip"}:                EnforcementBindingRequired,
		{ScheduleDimOverlap, "allowConcurrent"}:     EnforcementBindingRequired,
		{ScheduleDimOverlap, "queue"}:               EnforcementBindingRequired,
		{ScheduleDimOverlap, "cancelPrevious"}:      EnforcementBindingRequired,
		{ScheduleDimMisfire, "drop"}:                BudgetEnforced,
		{ScheduleDimMisfire, "runImmediately"}:      EnforcementBindingRequired,
		{ScheduleDimMisfire, "backfillAll"}:         EnforcementBindingRequired,
		{ScheduleDimRetry, "none"}:                  BudgetEnforced,
		{ScheduleDimRetry, "fixedDelay"}:            EnforcementBindingRequired,
		{ScheduleDimRetry, "exponentialBackoff"}:    EnforcementBindingRequired,
		{ScheduleDimConcurrency, "unlimited"}:       BudgetEnforced,
		{ScheduleDimConcurrency, "maxConcurrent"}:   EnforcementBindingRequired,
		{ScheduleDimTarget, "agent"}:                EnforcementBindingRequired,
		{ScheduleDimTarget, "profile"}:              EnforcementBindingRequired,
		{ScheduleDimApprovalPosture, "deny"}:        EnforcementBindingRequired,
		{ScheduleDimApprovalPosture, "autoApprove"}: EnforcementBindingRequired,
		{ScheduleDimApprovalPosture, "broker"}:      EnforcementBindingRequired,
		{ScheduleDimResultSink, "discard"}:          BudgetEnforced,
		{ScheduleDimResultSink, "named"}:            EnforcementBindingRequired,
	}
	if len(scheduleFacts) != len(want) {
		t.Fatalf("fact row count drift: table=%d want=%d", len(scheduleFacts), len(want))
	}
	for key, wantClass := range want {
		fact, ok := scheduleFacts[key]
		if !ok {
			t.Errorf("%s/%s: not classified", key.dim, key.value)
			continue
		}
		if fact.class != wantClass {
			t.Errorf("%s/%s: class = %s, want %s", key.dim, key.value, fact.class, wantClass)
		}
		if fact.evidence == "" {
			t.Errorf("%s/%s: a classification without evidence is an opinion", key.dim, key.value)
		}
		// Only the acknowledgement-gated rows may carry an ack reason, and
		// every one of them must.
		if fact.requiresAck != (fact.ackReason != "") {
			t.Errorf("%s/%s: requiresAck=%v but ackReason=%q", key.dim, key.value, fact.requiresAck, fact.ackReason)
		}
		// Enforced today means: nothing to bind.
		if fact.class == BudgetEnforced && len(fact.bindings) != 0 {
			t.Errorf("%s/%s: enforced rows must require no binding, got %v", key.dim, key.value, fact.bindings)
		}
		// Binding-required means: at least one binding, and it must be a known
		// kind with a reason preflight can quote.
		if fact.class == EnforcementBindingRequired {
			if len(fact.bindings) == 0 {
				t.Errorf("%s/%s: bindingRequired with no binding named", key.dim, key.value)
			}
			for _, b := range fact.bindings {
				if runtimeBindingReasons[b] == "" {
					t.Errorf("%s/%s: binding %s has no reason", key.dim, key.value, b)
				}
			}
		}
		// Path-dependence is exactly the overlap dimension today.
		if fact.pathDependent != (key.dim == ScheduleDimOverlap) {
			t.Errorf("%s/%s: pathDependent=%v", key.dim, key.value, fact.pathDependent)
		}
	}
	// The exported lookup answers the same question the compiler asks.
	if c, ok := ScheduleEnforcementOf(ScheduleDimTimezone, "Europe/Madrid"); !ok || c != EnforcementBindingRequired {
		t.Errorf("ScheduleEnforcementOf(timezone) = %q/%v", c, ok)
	}
	if _, ok := ScheduleEnforcementOf("nonsense", "x"); ok {
		t.Errorf("an unknown dimension must not classify")
	}
}
