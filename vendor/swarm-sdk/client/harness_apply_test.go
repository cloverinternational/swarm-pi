package client

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

// compilePlanIn compiles a manifest rooted at a specific directory so that
// workspace/storage resolve to a caller-chosen (shared) location. This lets a
// hot-apply test keep the SAME workspace/storage across the old and new plan
// (only workspace/storage EQUALITY keeps a change out of the restart bucket).
func compilePlanIn(t *testing.T, dir, manifest string) *harness.Plan {
	t.Helper()
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	return plan
}

// exposedSorted returns the client's real provider-visible tool names, sorted.
func exposedSorted(c *Client) []string {
	names := providerToolNames(c)
	sort.Strings(names)
	return names
}

// snapEqual compares two snapshots by every field (HarnessSnapshot has slice
// fields, so it is not directly comparable with ==).
func snapEqual(a, b HarnessSnapshot) bool {
	if a.Harness != b.Harness ||
		a.PlanName != b.PlanName ||
		a.PlanDigest != b.PlanDigest ||
		a.Provider != b.Provider ||
		a.Model != b.Model ||
		a.SystemPromptSHA256 != b.SystemPromptSHA256 ||
		a.SystemPromptBytes != b.SystemPromptBytes ||
		a.ApprovalMode != b.ApprovalMode ||
		a.WorkspaceBoundary != b.WorkspaceBoundary ||
		a.AllowMutation != b.AllowMutation ||
		a.CredentialSource != b.CredentialSource ||
		a.WorkspaceSHA256 != b.WorkspaceSHA256 ||
		a.StorageSHA256 != b.StorageSHA256 {
		return false
	}
	return strings.Join(a.ExposedTools, ",") == strings.Join(b.ExposedTools, ",") &&
		strings.Join(a.SelectedCatalogIDs, ",") == strings.Join(b.SelectedCatalogIDs, ",")
}

// baseApplyManifest is a hot-apply baseline: anthropic (sealed), inline cred,
// read-only tools, so no live provider handshake is needed.
const baseApplyManifest = `apiVersion: swarm.ai/v1alpha1
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
  approvalMode: interactive
`

// TestApplyHarnessHotOnlyPromptToolsTurns: changing prompt + tool set + maxTurns
// (all within the same provider/workspace) hot-applies and updates the snapshot
// and digest.
func TestApplyHarnessHotOnlyPromptToolsTurns(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	before := c.HarnessSnapshot()

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
    - forge.apply_patch
  limits:
    maxOutputTokens: 4096
    maxTurns: 20
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanIn(t, dir, newManifest)

	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("ApplyHarnessPlan hot: unexpected error: %v", err)
	}
	if len(res.Forbidden) != 0 || len(res.RestartRequired) != 0 {
		t.Fatalf("hot apply misclassified: forbidden=%v restart=%v", res.Forbidden, res.RestartRequired)
	}
	wantApplied := map[string]bool{harnessFieldSystemPrompt: true, harnessFieldTools: true, harnessFieldMaxTurns: true}
	if len(res.Applied) != len(wantApplied) {
		t.Fatalf("Applied = %v, want exactly %v", res.Applied, wantApplied)
	}
	for _, f := range res.Applied {
		if !wantApplied[f] {
			t.Errorf("unexpected Applied field %q", f)
		}
	}
	if res.Digest != newPlan.Digest() {
		t.Errorf("result digest = %q, want new plan digest %q", res.Digest, newPlan.Digest())
	}

	after := c.HarnessSnapshot()
	if after.PlanDigest != newPlan.Digest() {
		t.Errorf("snapshot digest = %q, want %q", after.PlanDigest, newPlan.Digest())
	}
	if after.SystemPromptSHA256 == before.SystemPromptSHA256 {
		t.Errorf("prompt hash unchanged after hot apply")
	}
	if after.SystemPromptSHA256 != newPlan.SystemPromptHash() {
		t.Errorf("live prompt hash %q != new plan hash %q", after.SystemPromptSHA256, newPlan.SystemPromptHash())
	}
	got := exposedSorted(c)
	if len(got) != 2 || got[0] != "Read" || got[1] != "apply_patch" {
		t.Errorf("exposed tools = %v, want [Read apply_patch]", got)
	}
	if c.agentDef.Capabilities == nil || c.agentDef.Capabilities.MaxTurns != 20 {
		t.Errorf("MaxTurns not hot-applied: %+v", c.agentDef.Capabilities)
	}
}

// TestApplyHarnessRestartRequiredProvider: changing the provider is classified
// restart-required, is NOT applied, and leaves the snapshot + applied plan intact.
func TestApplyHarnessRestartRequiredProvider(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

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
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if !errors.Is(err, ErrHarnessRestartRequired) {
		t.Fatalf("err = %v, want ErrHarnessRestartRequired", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("Applied = %v, want empty on restart-required", res.Applied)
	}
	found := false
	for _, f := range res.RestartRequired {
		if f == harnessFieldProvider {
			found = true
		}
	}
	if !found {
		t.Errorf("RestartRequired = %v, want to include %q", res.RestartRequired, harnessFieldProvider)
	}
	after := c.HarnessSnapshot()
	if after.PlanDigest != before.PlanDigest {
		t.Errorf("applied plan changed on restart-required: %q -> %q", before.PlanDigest, after.PlanDigest)
	}
	if after.Provider != "anthropic" {
		t.Errorf("provider changed on restart-required: %q", after.Provider)
	}
}

// TestApplyHarnessRestartRequiredWorkspace: a different workspace is
// restart-required (never hot).
func TestApplyHarnessRestartRequiredWorkspace(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

	// Compile the SAME manifest in a DIFFERENT dir => different resolved workspace.
	newPlan := compilePlanIn(t, t.TempDir(), baseApplyManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if !errors.Is(err, ErrHarnessRestartRequired) {
		t.Fatalf("err = %v, want ErrHarnessRestartRequired", err)
	}
	if len(res.Applied) != 0 {
		t.Errorf("Applied = %v, want empty", res.Applied)
	}
	sawWorkspace := false
	for _, f := range res.RestartRequired {
		if f == harnessFieldWorkspace {
			sawWorkspace = true
		}
	}
	if !sawWorkspace {
		t.Errorf("RestartRequired = %v, want workspace", res.RestartRequired)
	}
	if c.HarnessSnapshot().PlanDigest != before.PlanDigest {
		t.Errorf("applied plan changed on restart-required workspace")
	}
}

// TestApplyHarnessForbiddenYoloWithoutPosture: switching to yolo without
// AllowYolo is forbidden; the client is untouched.
func TestApplyHarnessForbiddenYoloWithoutPosture(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

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
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{AllowYolo: false})
	if err == nil {
		t.Fatalf("expected error for yolo without posture")
	}
	if errors.Is(err, ErrHarnessRestartRequired) {
		t.Fatalf("yolo without posture should be forbidden, not restart-required: %v", err)
	}
	sawApproval := false
	for _, f := range res.Forbidden {
		if f == harnessFieldApprovalMode {
			sawApproval = true
		}
	}
	if !sawApproval {
		t.Errorf("Forbidden = %v, want approvalMode", res.Forbidden)
	}
	if len(res.Applied) != 0 {
		t.Errorf("Applied = %v, want empty on forbidden", res.Applied)
	}
	after := c.HarnessSnapshot()
	if after.ApprovalMode != before.ApprovalMode || after.PlanDigest != before.PlanDigest {
		t.Errorf("client mutated on forbidden yolo: approval %q->%q digest %q->%q",
			before.ApprovalMode, after.ApprovalMode, before.PlanDigest, after.PlanDigest)
	}
}

// TestApplyHarnessYoloWithPostureIsHot: same yolo change WITH AllowYolo is a hot
// approval-mode change and applies.
func TestApplyHarnessYoloWithPostureIsHot(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
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
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{AllowYolo: true})
	if err != nil {
		t.Fatalf("yolo WITH posture should hot-apply, got: %v", err)
	}
	if len(res.Applied) != 1 || res.Applied[0] != harnessFieldApprovalMode {
		t.Fatalf("Applied = %v, want [approvalMode]", res.Applied)
	}
	if c.HarnessSnapshot().ApprovalMode != "yolo" {
		t.Errorf("approvalMode = %q, want yolo after hot apply", c.HarnessSnapshot().ApprovalMode)
	}
}

// TestApplyHarnessForbiddenUnboundTool: an unknown/unbound tool id is forbidden;
// the client is untouched.
func TestApplyHarnessForbiddenUnboundTool(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

	const unboundManifest = `apiVersion: swarm.ai/v1alpha1
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
    - interactive.ask_user_question
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`
	newPlan := compilePlanIn(t, dir, unboundManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err == nil {
		t.Fatalf("expected error for unbound tool")
	}
	sawTools := false
	for _, f := range res.Forbidden {
		if f == harnessFieldTools {
			sawTools = true
		}
	}
	if !sawTools {
		t.Errorf("Forbidden = %v, want tools", res.Forbidden)
	}
	after := c.HarnessSnapshot()
	if after.PlanDigest != before.PlanDigest {
		t.Errorf("client mutated on forbidden unbound tool")
	}
	if got := exposedSorted(c); len(got) != 1 || got[0] != "Read" {
		t.Errorf("exposed tools changed on forbidden apply: %v", got)
	}
}

// TestApplyHarnessFailClosedInvalidApprovalMode: a plan that compiles but fails
// our approvalMode validation leaves the prior plan intact and the client usable.
func TestApplyHarnessFailClosedInvalidApprovalMode(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

	const badManifest = `apiVersion: swarm.ai/v1alpha1
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
  approvalMode: banana
`
	newPlan := compilePlanIn(t, dir, badManifest)
	_, err = c.ApplyHarnessPlan(context.Background(), newPlan, ApplyHarnessOptions{})
	if err == nil {
		t.Fatalf("expected validation error for bad approvalMode")
	}
	after := c.HarnessSnapshot()
	if !snapEqual(after, before) {
		t.Fatalf("client mutated on fail-closed preflight: before=%+v after=%+v", before, after)
	}
	// Client still usable: the agent + prompt are intact.
	if c.agent == nil || !strings.Contains(c.agent.SystemPrompt(), "PROMPT-ONE") {
		t.Errorf("client not usable after fail-closed preflight")
	}
}

// TestApplyHarnessNonHarnessClientUnsupported: a plain client.New client returns
// an explicit unsupported error and is not mutated.
func TestApplyHarnessNonHarnessClientUnsupported(t *testing.T) {
	dir := t.TempDir()
	c, err := New(
		WithoutAutoConfig(),
		WithoutIndexMd(),
		WithWorkspace(dir),
		WithStorageDir(filepath.Join(dir, "conv")),
		WithProvider(ProviderAnthropic, "claude-sonnet-4-5"),
		WithAPIKey("test-key"),
	)
	if err != nil {
		t.Fatalf("baseline New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	plan := compilePlanIn(t, dir, baseApplyManifest)
	res, err := c.ApplyHarnessPlan(context.Background(), plan, ApplyHarnessOptions{})
	if !errors.Is(err, ErrNotHarnessClient) {
		t.Fatalf("err = %v, want ErrNotHarnessClient", err)
	}
	if len(res.Applied) != 0 || len(res.RestartRequired) != 0 || len(res.Forbidden) != 0 {
		t.Errorf("non-harness result should be empty: %+v", res)
	}
	if c.HarnessSnapshot().Harness {
		t.Errorf("non-harness client became a harness client")
	}
}

// TestApplyHarnessNoOpIdenticalPlan: applying a plan with an identical digest is
// a no-op with empty change sets and no state change.
func TestApplyHarnessNoOpIdenticalPlan(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

	// A freshly-compiled identical manifest in the SAME dir has the same digest.
	samePlan := compilePlanIn(t, dir, baseApplyManifest)
	if samePlan.Digest() != oldPlan.Digest() {
		t.Fatalf("expected identical digests, got %q vs %q", samePlan.Digest(), oldPlan.Digest())
	}
	res, err := c.ApplyHarnessPlan(context.Background(), samePlan, ApplyHarnessOptions{})
	if err != nil {
		t.Fatalf("no-op apply error: %v", err)
	}
	if len(res.Applied) != 0 || len(res.RestartRequired) != 0 || len(res.Forbidden) != 0 || len(res.Diff.Changes) != 0 {
		t.Errorf("no-op should have empty change sets: %+v", res)
	}
	if res.Digest != oldPlan.Digest() {
		t.Errorf("no-op digest = %q, want %q", res.Digest, oldPlan.Digest())
	}
	if !snapEqual(c.HarnessSnapshot(), before) {
		t.Errorf("no-op mutated the client")
	}
}

// TestApplyHarnessNilPlanRejected: a nil newPlan is rejected without mutation.
func TestApplyHarnessNilPlanRejected(t *testing.T) {
	dir := t.TempDir()
	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()
	if _, err := c.ApplyHarnessPlan(context.Background(), nil, ApplyHarnessOptions{}); err == nil {
		t.Fatalf("expected error for nil newPlan")
	}
	if !snapEqual(c.HarnessSnapshot(), before) {
		t.Errorf("nil-plan apply mutated the client")
	}
}
