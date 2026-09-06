package client

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// fakeAuditor is a minimal, concurrency-safe observability.Auditor test
// double that records every event it is given, and can optionally be
// configured to fail or panic on Record — used to prove
// emitHarnessAuditEvent/ApplyHarnessPlanAudited never let that affect the
// caller (D3).
type fakeAuditor struct {
	mu     sync.Mutex
	events []observability.AuditEvent
	fail   error
	panic  bool
}

func (f *fakeAuditor) Record(_ context.Context, event observability.AuditEvent) error {
	if f.panic {
		panic("fakeAuditor: deliberate panic for D3 safety test")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
	if f.fail != nil {
		return f.fail
	}
	return nil
}

func (f *fakeAuditor) Query(_ context.Context, _ observability.AuditCriteria) ([]observability.AuditEvent, error) {
	return nil, nil
}

func (f *fakeAuditor) recorded() []observability.AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]observability.AuditEvent, len(f.events))
	copy(out, f.events)
	return out
}

// ── D3: the auditor seam lives on harnessConstruction, is nil-safe, and
// never gates or alters an apply ────────────────────────────────────────────

// TestHarnessSuppliedAuditorNilSafe proves the accessor is nil-safe exactly
// like harnessSuppliedBindings: a nil *Client, a Client with no harness
// construction at all, and a harness Client that never called
// WithHarnessAuditor all yield a nil auditor (never a synthesized no-op
// value) rather than panicking.
func TestHarnessSuppliedAuditorNilSafe(t *testing.T) {
	var nilClient *Client
	if got := nilClient.harnessSuppliedAuditor(); got != nil {
		t.Errorf("nil *Client: harnessSuppliedAuditor() = %v, want nil", got)
	}

	dir := t.TempDir()
	plain, err := New(
		WithoutAutoConfig(),
		WithoutIndexMd(),
		WithWorkspace(dir),
		WithStorageDir(filepath.Join(dir, "conv")),
		WithProvider(ProviderAnthropic, "claude-sonnet-4-5"),
		WithAPIKey("test-key"),
	)
	if err != nil {
		t.Fatalf("New (non-harness): %v", err)
	}
	t.Cleanup(func() { _ = plain.Close() })
	if got := plain.harnessSuppliedAuditor(); got != nil {
		t.Errorf("non-harness Client: harnessSuppliedAuditor() = %v, want nil", got)
	}

	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	noAuditor, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness, no auditor): %v", err)
	}
	t.Cleanup(func() { _ = noAuditor.Close() })
	if got := noAuditor.harnessSuppliedAuditor(); got != nil {
		t.Errorf("harness Client without WithHarnessAuditor: harnessSuppliedAuditor() = %v, want nil", got)
	}
}

// TestHarnessSuppliedAuditorReadsWhatWasSupplied proves WithHarnessAuditor
// installs exactly the auditor instance passed, read back via the accessor —
// the D3 seam round-trips.
func TestHarnessSuppliedAuditorReadsWhatWasSupplied(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness, auditor): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	got := c.harnessSuppliedAuditor()
	if got == nil {
		t.Fatal("harnessSuppliedAuditor() = nil, want the supplied fakeAuditor")
	}
	if got.(*fakeAuditor) != fa {
		t.Error("harnessSuppliedAuditor() returned a different instance than was supplied")
	}
}

// TestApplyHarnessPlanAuditedNilAuditorIsInert proves a Client with NO
// auditor supplied behaves identically through ApplyHarnessPlanAudited as
// through plain ApplyHarnessPlan (no panics, no altered result/error) — the
// default is a true no-op, not merely "usually harmless".
func TestApplyHarnessPlanAuditedNilAuditorIsInert(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	samePlan := compilePlanIn(t, dir, baseApplyManifest)
	res, err := c.ApplyHarnessPlanAudited(context.Background(), samePlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlanAudited (no-op, no auditor): unexpected error: %v", err)
	}
	if res.Digest != oldPlan.Digest() {
		t.Errorf("digest = %q, want %q", res.Digest, oldPlan.Digest())
	}
}

// TestApplyHarnessPlanAuditedPanickingAuditorDoesNotPropagate proves a
// panicking auditor is fully contained: the apply itself still completes
// and returns its normal result/error, and the panic never reaches the
// caller of ApplyHarnessPlanAudited (D3: "a failing/slow/panicking auditor
// must not fail, block, or alter an apply").
func TestApplyHarnessPlanAuditedPanickingAuditorDoesNotPropagate(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{panic: true}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness, panicking auditor): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	const newManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-TWO-CHANGED"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanIn(t, dir, newManifest)

	res, err := c.ApplyHarnessPlanAudited(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlanAudited with panicking auditor: apply itself failed: %v", err)
	}
	if res.Digest != newPlan.Digest() {
		t.Errorf("apply did not actually take effect despite a panicking auditor: digest = %q, want %q", res.Digest, newPlan.Digest())
	}
}

// TestApplyHarnessPlanAuditedFailingAuditorDoesNotPropagate proves an
// auditor whose Record returns a non-nil error does not surface that error
// to ApplyHarnessPlanAudited's caller.
func TestApplyHarnessPlanAuditedFailingAuditorDoesNotPropagate(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{fail: errors.New("audit sink unavailable")}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness, failing auditor): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	samePlan := compilePlanIn(t, dir, baseApplyManifest)
	if _, err := c.ApplyHarnessPlanAudited(context.Background(), samePlan, ApplyHarnessOptions{}); err != nil {
		t.Fatalf("ApplyHarnessPlanAudited with failing auditor: unexpected error: %v", err)
	}
	if len(fa.recorded()) != 1 {
		t.Fatalf("recorded events = %d, want 1 (the failing Record call still happened)", len(fa.recorded()))
	}
}

// ── D4: all four outcomes are classified and emitted correctly ─────────────

func firstEventOrFatal(t *testing.T, fa *fakeAuditor) observability.AuditEvent {
	t.Helper()
	events := fa.recorded()
	if len(events) != 1 {
		t.Fatalf("recorded events = %d, want exactly 1: %+v", len(events), events)
	}
	return events[0]
}

// TestApplyHarnessPlanAuditedHotApplied covers outcome 1/4: a hot-only
// change emits HarnessAuditEventApplied with outcome success.
func TestApplyHarnessPlanAuditedHotApplied(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	const newManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-TWO-CHANGED"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanIn(t, dir, newManifest)
	res, err := c.ApplyHarnessPlanAudited(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlanAudited hot: %v", err)
	}
	if len(res.Applied) == 0 {
		t.Fatal("expected a hot apply, got zero Applied fields")
	}

	ev := firstEventOrFatal(t, fa)
	if ev.EventType != HarnessAuditEventApplied {
		t.Errorf("EventType = %q, want %q", ev.EventType, HarnessAuditEventApplied)
	}
	if ev.Outcome != observability.AuditOutcomeSuccess {
		t.Errorf("Outcome = %q, want success", ev.Outcome)
	}
	if ev.Details["plan_digest"] != newPlan.Digest() {
		t.Errorf("plan_digest = %v, want %q (D5: reuse Plan.Digest())", ev.Details["plan_digest"], newPlan.Digest())
	}
	if ev.Details["plan_name"] != newPlan.Name() {
		t.Errorf("plan_name = %v, want %q", ev.Details["plan_name"], newPlan.Name())
	}
	if ev.Details["plan_api_version"] != newPlan.APIVersion() {
		t.Errorf("plan_api_version = %v, want %q", ev.Details["plan_api_version"], newPlan.APIVersion())
	}
	if _, ok := ev.Details["build_commit"]; !ok {
		t.Error("Details missing build_commit (D6)")
	}
}

// TestApplyHarnessPlanAuditedNoop covers outcome 2/4: an identical-digest
// apply emits HarnessAuditEventNoop with outcome success, distinguished from
// a real apply by Action.
func TestApplyHarnessPlanAuditedNoop(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	samePlan := compilePlanIn(t, dir, baseApplyManifest)
	if _, err := c.ApplyHarnessPlanAudited(context.Background(), samePlan, ApplyHarnessOptions{}); err != nil {
		t.Fatalf("ApplyHarnessPlanAudited no-op: %v", err)
	}

	ev := firstEventOrFatal(t, fa)
	if ev.EventType != HarnessAuditEventNoop {
		t.Errorf("EventType = %q, want %q", ev.EventType, HarnessAuditEventNoop)
	}
	if ev.Outcome != observability.AuditOutcomeSuccess {
		t.Errorf("Outcome = %q, want success", ev.Outcome)
	}
	if ev.Action != "noop" {
		t.Errorf("Action = %q, want %q", ev.Action, "noop")
	}
}

// TestApplyHarnessPlanAuditedRejected covers outcome 3/4: a forbidden change
// (yolo without posture) emits HarnessAuditEventRejected with outcome
// denied, and the client is left untouched.
func TestApplyHarnessPlanAuditedRejected(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	const yoloManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: yolo
`
	newPlan := compilePlanIn(t, dir, yoloManifest)
	_, err = c.ApplyHarnessPlanAudited(context.Background(), newPlan, ApplyHarnessOptions{AllowYolo: false})
	if err == nil {
		t.Fatal("expected an error for yolo without posture")
	}
	if errors.Is(err, ErrHarnessRestartRequired) {
		t.Fatalf("misclassified as restart-required: %v", err)
	}

	ev := firstEventOrFatal(t, fa)
	if ev.EventType != HarnessAuditEventRejected {
		t.Errorf("EventType = %q, want %q", ev.EventType, HarnessAuditEventRejected)
	}
	if ev.Outcome != observability.AuditOutcomeDenied {
		t.Errorf("Outcome = %q, want denied", ev.Outcome)
	}
}

// TestApplyHarnessPlanAuditedRestartRequired covers outcome 4/4: a
// provider change is classified restart-required (nothing mutated), emits
// HarnessAuditEventRestartRequired with outcome failure.
func TestApplyHarnessPlanAuditedRestartRequired(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	const newManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: openai
  model: gpt-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanIn(t, dir, newManifest)
	_, err = c.ApplyHarnessPlanAudited(context.Background(), newPlan, ApplyHarnessOptions{})
	if !errors.Is(err, ErrHarnessRestartRequired) {
		t.Fatalf("err = %v, want ErrHarnessRestartRequired", err)
	}

	ev := firstEventOrFatal(t, fa)
	if ev.EventType != HarnessAuditEventRestartRequired {
		t.Errorf("EventType = %q, want %q", ev.EventType, HarnessAuditEventRestartRequired)
	}
	if ev.Outcome != observability.AuditOutcomeFailure {
		t.Errorf("Outcome = %q, want failure", ev.Outcome)
	}
}

// TestApplyHarnessPlanAuditedNilPlanIsNotAudited proves the newPlan == nil
// guard case (not one of the four terminal outcomes) produces NO audit
// event at all.
func TestApplyHarnessPlanAuditedNilPlanIsNotAudited(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	if _, err := c.ApplyHarnessPlanAudited(context.Background(), nil, ApplyHarnessOptions{}); err == nil {
		t.Fatal("expected an error for nil newPlan")
	}
	if len(fa.recorded()) != 0 {
		t.Errorf("recorded events = %d, want 0 for the nil-plan guard case", len(fa.recorded()))
	}
}

// ── D5: no emitted event's Details ever carries a raw secret value ─────────

// TestHarnessAuditEventsNeverCarryTheRawCredential compiles a plan whose
// credential is the KNOWN literal "test-key" (baseApplyManifest) and, across
// every one of the four outcome scenarios above, asserts the serialized
// Details of every recorded event never contains that literal — proving the
// D5 claim that reusing Plan.Digest() (built over the REDACTED report) keeps
// secrets out of the audit trail.
func TestHarnessAuditEventsNeverCarryTheRawCredential(t *testing.T) {
	const secret = "test-key"
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	fa := &fakeAuditor{}
	c, err := New(WithHarnessPlan(oldPlan), WithHarnessAuditor(fa))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	scenarios := []string{
		baseApplyManifest, // no-op
		`apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-CHANGED"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`, // hot apply
		`apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: apply-base
provider:
  id: openai
  model: gpt-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "PROMPT-ONE"
  tools:
    - forge.read
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`, // restart required
	}

	for _, manifest := range scenarios {
		p := compilePlanIn(t, dir, manifest)
		_, _ = c.ApplyHarnessPlanAudited(context.Background(), p, ApplyHarnessOptions{})
	}

	for _, ev := range fa.recorded() {
		b, err := json.Marshal(ev.Details)
		if err != nil {
			t.Fatalf("marshal Details: %v", err)
		}
		if strings.Contains(string(b), secret) {
			t.Fatalf("event %q Details leaked the raw credential: %s", ev.EventType, b)
		}
	}
}
