package chat

// Phase 4d editor tests. They exercise the editor over a REAL comment-preserving
// harness.EditSession (temp manifest, offline compile) while injecting a FAKE
// client so no real client.New / provider is required.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

const editorTestManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: editor-test
provider:
  id: anthropic
  model: claude-x
agent:
  systemPrompt:
    inline: "SECRET-PROMPT-SENTINEL-DO-NOT-LEAK"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
`

// fakeHarnessClient injects deterministic snapshot/apply/watch behavior.
type fakeHarnessClient struct {
	snap        sdkclient.HarnessSnapshot
	applyResult sdkclient.ApplyHarnessResult
	applyErr    error
	applyCalls  int
	watchCalls  int
}

func (f *fakeHarnessClient) HarnessSnapshot() sdkclient.HarnessSnapshot { return f.snap }

func (f *fakeHarnessClient) ApplyHarnessPlanAudited(_ context.Context, _ *harness.Plan, _ sdkclient.ApplyHarnessOptions) (sdkclient.ApplyHarnessResult, error) {
	f.applyCalls++
	return f.applyResult, f.applyErr
}

func (f *fakeHarnessClient) WatchHarness(ctx context.Context, _ string, _ sdkclient.ReloadPolicy, _ func(sdkclient.ReloadOutcome)) error {
	f.watchCalls++
	<-ctx.Done()
	return ctx.Err()
}

func writeEditorManifest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")
	if err := os.WriteFile(path, []byte(editorTestManifest), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func newEditorForTest(t *testing.T, cl harnessEditorClient, allowYolo bool) *harnessEditorModel {
	t.Helper()
	path := writeEditorManifest(t)
	ed := newHarnessEditor(path, allowYolo, cl)
	if ed == nil {
		t.Fatal("newHarnessEditor returned nil for a valid harness path")
	}
	if ed.openErr != nil {
		t.Fatalf("open edit session: %v", ed.openErr)
	}
	return ed
}

func snapshotFixture() sdkclient.HarnessSnapshot {
	return sdkclient.HarnessSnapshot{
		Harness:            true,
		PlanName:           "editor-test",
		PlanDigest:         "digestabcdef012345",
		Provider:           "anthropic",
		Model:              "claude-x",
		SystemPromptSHA256: "sha256:deadbeefcafef00d",
		SystemPromptBytes:  33,
		ApprovalMode:       "interactive",
		SelectedCatalogIDs: []string{"forge.read"},
		ExposedTools:       []string{"read"},
		CredentialSource:   "env:ANTHROPIC_API_KEY",
		WorkspaceSHA256:    "aaaabbbbcccc",
	}
}

// 1. Snapshot rendering must never contain a secret value (the inline prompt).
func TestHarnessEditorSnapshotNoSecret(t *testing.T) {
	fake := &fakeHarnessClient{snap: snapshotFixture()}
	ed := newEditorForTest(t, fake, false)

	view := ed.View()
	if strings.Contains(view, "SECRET-PROMPT-SENTINEL") {
		t.Fatalf("view leaked the inline system prompt:\n%s", view)
	}
	// Redacted provenance label is fine; raw provider/model/hash shown.
	if !strings.Contains(view, "env:ANTHROPIC_API_KEY") {
		t.Fatalf("view should show the credential provenance label")
	}
	if !strings.Contains(view, "anthropic / claude-x") {
		t.Fatalf("view should render provider/model; got:\n%s", view)
	}
}

// 2. Editing a hot field then apply => Applied outcome; diff shows the field HOT.
func TestHarnessEditorApplyHotField(t *testing.T) {
	fake := &fakeHarnessClient{
		snap: snapshotFixture(),
		applyResult: sdkclient.ApplyHarnessResult{
			Applied: []string{"model"},
			Diff: sdkclient.HarnessPlanDiff{Changes: []sdkclient.HarnessFieldChange{
				{Field: "model", Old: "claude-x", New: "claude-y", Class: "hot"},
			}},
			Digest: "newdigest",
		},
	}
	ed := newEditorForTest(t, fake, false)

	ed.commitEdit(fieldModel, "claude-y")
	if ed.diff == nil || !ed.diff.Changed {
		t.Fatalf("expected a pending diff after editing model")
	}
	if ed.diff.After.Model != "claude-y" {
		t.Fatalf("diff after.model = %q; want claude-y", ed.diff.After.Model)
	}

	ed.apply()
	if fake.applyCalls != 1 {
		t.Fatalf("ApplyHarnessPlan calls = %d; want 1", fake.applyCalls)
	}
	if !strings.HasPrefix(ed.status, "Applied") {
		t.Fatalf("status = %q; want Applied…", ed.status)
	}
	out := ed.renderOutcome()
	if !strings.Contains(out, "HOT") || !strings.Contains(out, "model") {
		t.Fatalf("outcome should classify model as HOT; got:\n%s", out)
	}
}

// 3. Editing a restart-required field => outcome shows RestartRequired, not applied.
func TestHarnessEditorRestartRequired(t *testing.T) {
	fake := &fakeHarnessClient{
		snap:     snapshotFixture(),
		applyErr: sdkclient.ErrHarnessRestartRequired,
		applyResult: sdkclient.ApplyHarnessResult{
			RestartRequired: []string{"provider"},
			Diff: sdkclient.HarnessPlanDiff{Changes: []sdkclient.HarnessFieldChange{
				{Field: "provider", Class: "restart"},
			}},
		},
	}
	ed := newEditorForTest(t, fake, false)

	ed.commitEdit(fieldModel, "claude-z")
	ed.apply()

	if fake.applyCalls != 1 {
		t.Fatalf("ApplyHarnessPlan calls = %d; want 1", fake.applyCalls)
	}
	if !strings.HasPrefix(ed.status, "Restart required") {
		t.Fatalf("status = %q; want Restart required…", ed.status)
	}
	if ed.applyResult == nil || len(ed.applyResult.Applied) != 0 {
		t.Fatalf("nothing should be Applied on a restart-required outcome")
	}
}

// 4. Forbidden (e.g. yolo without AllowYolo) => outcome Rejected.
func TestHarnessEditorForbiddenRejected(t *testing.T) {
	fake := &fakeHarnessClient{
		snap:     snapshotFixture(),
		applyErr: errForbiddenApply,
		applyResult: sdkclient.ApplyHarnessResult{
			Forbidden: []string{"approvalMode"},
			Diff: sdkclient.HarnessPlanDiff{Changes: []sdkclient.HarnessFieldChange{
				{Field: "approvalMode", Old: "interactive", New: "yolo", Class: "forbidden"},
			}},
		},
	}
	ed := newEditorForTest(t, fake, false)

	ed.commitEdit(fieldApprovalMode, "yolo")
	ed.apply()

	if fake.applyCalls != 1 {
		t.Fatalf("ApplyHarnessPlan calls = %d; want 1", fake.applyCalls)
	}
	if !strings.HasPrefix(ed.status, "Rejected") {
		t.Fatalf("status = %q; want Rejected…", ed.status)
	}
	out := ed.renderOutcome()
	if !strings.Contains(out, "FORBIDDEN") {
		t.Fatalf("outcome should classify approvalMode FORBIDDEN; got:\n%s", out)
	}
}

// 5. Validation failure => no Save/Apply call; diagnostics shown.
func TestHarnessEditorValidationFailureNoSave(t *testing.T) {
	fake := &fakeHarnessClient{snap: snapshotFixture()}
	ed := newEditorForTest(t, fake, false)

	// Point the system prompt at a non-existent file: compiles to a diagnostic.
	ed.commitEdit(fieldPromptFile, "/nonexistent/does-not-exist-xyz.txt")
	ed.apply()

	if fake.applyCalls != 0 {
		t.Fatalf("ApplyHarnessPlan must NOT be called on validation failure; calls=%d", fake.applyCalls)
	}
	if len(ed.diagnostics) == 0 {
		t.Fatalf("expected diagnostics on validation failure")
	}
	if !strings.Contains(ed.status, "validation failed") {
		t.Fatalf("status = %q; want validation failed…", ed.status)
	}
	view := ed.View()
	if !strings.Contains(view, "Diagnostics:") {
		t.Fatalf("view should surface diagnostics; got:\n%s", view)
	}
}

// 6. Non-harness session => the editor constructor / open decision is inert.
func TestHarnessEditorNonHarnessInert(t *testing.T) {
	if ed := newHarnessEditor("", false, &fakeHarnessClient{}); ed != nil {
		t.Fatalf("expected nil editor for empty harness path (inert open-hook)")
	}
	if ed := newHarnessEditor("   ", false, &fakeHarnessClient{}); ed != nil {
		t.Fatalf("expected nil editor for blank harness path (inert open-hook)")
	}
}

// Watch toggle: opt-in, off by default, starts and stops cleanly.
func TestHarnessEditorWatchToggle(t *testing.T) {
	fake := &fakeHarnessClient{snap: snapshotFixture()}
	ed := newEditorForTest(t, fake, false)

	if ed.watching {
		t.Fatalf("watch must be off by default")
	}
	ed.toggleWatch()
	ed.mu.Lock()
	on := ed.watching
	ed.mu.Unlock()
	if !on {
		t.Fatalf("watch should be ON after first toggle")
	}
	ed.stopWatch()
	ed.mu.Lock()
	off := ed.watching
	ed.mu.Unlock()
	if off {
		t.Fatalf("watch should be off after stopWatch")
	}
}

var errForbiddenApply = &forbiddenApplyError{}

type forbiddenApplyError struct{}

func (*forbiddenApplyError) Error() string {
	return "harness: apply rejected: forbidden change(s): approvalMode"
}

// 7. Skills section: INSPECTION renders the selected skills + search roots
// from the snapshot (never a body/absolute path), and EDITING an id-only
// skill list drives SetSkillsExact through the same Validate -> Diff ->
// Save -> ApplyHarnessPlan transaction as every other field.
func TestHarnessEditorSkillsSectionAndEdit(t *testing.T) {
	snap := snapshotFixture()
	snap.Skills = []sdkclient.HarnessSkillView{
		{ID: "demo-skill", Path: "skills/demo-skill", ContentHash: "sha256:deadbeefcafef00d", Source: "file"},
	}
	snap.SkillSearchRoots = []string{"skills"}
	fake := &fakeHarnessClient{
		snap: snap,
		applyResult: sdkclient.ApplyHarnessResult{
			Applied: []string{"skills"},
			Diff: sdkclient.HarnessPlanDiff{Changes: []sdkclient.HarnessFieldChange{
				{Field: "skills", Old: "demo-skill*", New: "extra-skill", Class: "hot"},
			}},
		},
	}
	ed := newEditorForTest(t, fake, false)

	// INSPECTION: the snapshot's selected skill and search root render, with
	// no body and no absolute path (HarnessSkillView is already redacted).
	view := ed.View()
	if !strings.Contains(view, "demo-skill") || !strings.Contains(view, "path=skills/demo-skill") {
		t.Fatalf("view should render the selected skill; got:\n%s", view)
	}
	if !strings.Contains(view, "source=file") {
		t.Fatalf("view should render the skill source; got:\n%s", view)
	}
	if !strings.Contains(view, "skillSearchRoots: skills") {
		t.Fatalf("view should render the skill search roots; got:\n%s", view)
	}

	// EDITING: replacing the field's exact list both adds "extra-skill" and
	// removes "demo-skill" (the same "re-type the exact list" convention the
	// Tools field already uses) via SetSkillsExact.
	ed.commitEdit(fieldSkills, "extra-skill")
	if ed.diff == nil || !ed.diff.Changed {
		t.Fatalf("expected a pending diff after editing skills")
	}
	if len(ed.diff.After.Skills) != 1 || ed.diff.After.Skills[0] != "extra-skill" {
		t.Fatalf("diff after.Skills = %v; want [extra-skill]", ed.diff.After.Skills)
	}
	if len(ed.diff.Before.Skills) != 0 {
		t.Fatalf("diff before.Skills = %v; want empty (baseline manifest has no skills)", ed.diff.Before.Skills)
	}

	// APPLY: same Validate -> Save -> ApplyHarnessPlan transaction as every
	// other field — no parallel apply path.
	ed.apply()
	if fake.applyCalls != 1 {
		t.Fatalf("ApplyHarnessPlan calls = %d; want 1", fake.applyCalls)
	}
	if !strings.HasPrefix(ed.status, "Applied") {
		t.Fatalf("status = %q; want Applied…", ed.status)
	}
	out := ed.renderOutcome()
	if !strings.Contains(out, "HOT") || !strings.Contains(out, "skills") {
		t.Fatalf("outcome should classify skills as HOT; got:\n%s", out)
	}
}

// 8. Non-harness session: the editor never reaches skill state (constructor
// stays inert), consistent with every other field.
func TestHarnessEditorSkillsNonHarnessInert(t *testing.T) {
	if ed := newHarnessEditor("", false, &fakeHarnessClient{}); ed != nil {
		t.Fatalf("expected nil editor for empty harness path (skills must not be reachable)")
	}
}
