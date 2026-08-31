package client

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/atomicfile"
)

// startWatch runs WatchHarness in a goroutine, delivering each outcome onto a
// buffered channel. It returns the outcome channel, a cancel func, and a done
// channel closed when WatchHarness returns (with its error).
func startWatch(t *testing.T, c *Client, path string, policy ReloadPolicy) (<-chan ReloadOutcome, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	outcomes := make(chan ReloadOutcome, 8)
	done := make(chan error, 1)
	go func() {
		done <- c.WatchHarness(ctx, path, policy, func(o ReloadOutcome) {
			outcomes <- o
		})
	}()
	return outcomes, cancel, done
}

func recvOutcome(t *testing.T, ch <-chan ReloadOutcome) ReloadOutcome {
	t.Helper()
	select {
	case o := <-ch:
		return o
	case <-time.After(4 * time.Second):
		t.Fatalf("timed out waiting for a reload outcome")
		return ReloadOutcome{}
	}
}

// stopWatch cancels the watch and waits for WatchHarness to return.
func stopWatch(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("WatchHarness did not return after cancel")
	}
}

// TestWatchHarnessHotChangeAutoApplies: a hot manifest change (prompt + tools +
// turns) written to the watched path is auto-applied; the snapshot reflects it.
func TestWatchHarnessHotChangeAutoApplies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")

	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

	const hotManifest = `apiVersion: swarm.ai/v1alpha1
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
	mustWriteFile(t, path, hotManifest)
	newPlan := compilePlanIn(t, dir, hotManifest)

	outcomes, cancel, done := startWatch(t, c, path, DefaultReloadPolicy())
	o := recvOutcome(t, outcomes)
	stopWatch(t, cancel, done)

	if !o.Applied {
		t.Fatalf("hot change not applied: outcome=%+v", o)
	}
	if o.RestartRequired || o.Rejected || o.Err != nil {
		t.Fatalf("hot change misclassified: %+v", o)
	}
	after := c.HarnessSnapshot()
	if after.PlanDigest != newPlan.Digest() {
		t.Fatalf("snapshot digest = %q, want new %q", after.PlanDigest, newPlan.Digest())
	}
	if after.SystemPromptSHA256 == before.SystemPromptSHA256 {
		t.Fatalf("prompt hash unchanged after hot auto-apply")
	}
	if after.SystemPromptSHA256 != newPlan.SystemPromptHash() {
		t.Fatalf("live prompt hash %q != new plan hash %q", after.SystemPromptSHA256, newPlan.SystemPromptHash())
	}
}

// TestWatchHarnessRestartRequiredNotApplied: a provider change is restart-
// required, reported but NOT applied; the snapshot is unchanged.
func TestWatchHarnessRestartRequiredNotApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")

	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan))
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	before := c.HarnessSnapshot()

	const restartManifest = `apiVersion: swarm.ai/v1alpha1
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
	mustWriteFile(t, path, restartManifest)

	outcomes, cancel, done := startWatch(t, c, path, DefaultReloadPolicy())
	o := recvOutcome(t, outcomes)
	stopWatch(t, cancel, done)

	if !o.RestartRequired {
		t.Fatalf("provider change not classified restart-required: %+v", o)
	}
	if o.Applied {
		t.Fatalf("restart-required change must NOT be applied: %+v", o)
	}
	after := c.HarnessSnapshot()
	if after.PlanDigest != before.PlanDigest {
		t.Fatalf("applied plan changed on restart-required: %q -> %q", before.PlanDigest, after.PlanDigest)
	}
	if after.Provider != "anthropic" {
		t.Fatalf("provider changed on restart-required: %q", after.Provider)
	}
}

// TestWatchHarnessForbiddenYoloRejected: a yolo change without the AllowYolo
// posture is rejected, not applied, and the client stays usable.
func TestWatchHarnessForbiddenYoloRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")

	oldPlan := compilePlanIn(t, dir, baseApplyManifest)
	c, err := New(WithHarnessPlan(oldPlan)) // NO WithHarnessAllowYolo
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
	mustWriteFile(t, path, yoloManifest)

	outcomes, cancel, done := startWatch(t, c, path, DefaultReloadPolicy())
	o := recvOutcome(t, outcomes)
	stopWatch(t, cancel, done)

	if !o.Rejected || o.Err == nil {
		t.Fatalf("forbidden yolo not rejected: %+v", o)
	}
	if o.Applied || o.RestartRequired {
		t.Fatalf("forbidden yolo misclassified: %+v", o)
	}
	after := c.HarnessSnapshot()
	if after.ApprovalMode != before.ApprovalMode || after.PlanDigest != before.PlanDigest {
		t.Fatalf("client mutated on forbidden yolo: approval %q->%q digest %q->%q",
			before.ApprovalMode, after.ApprovalMode, before.PlanDigest, after.PlanDigest)
	}
	// Client remains usable: the agent + original prompt are intact.
	if c.agent == nil {
		t.Fatalf("client agent nil after forbidden reload")
	}
}

// TestWatchHarnessInvalidRejected: an invalid manifest yields a Rejected outcome
// carrying the compile error; nothing is applied.
func TestWatchHarnessInvalidRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harness.yaml")

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
  id: ""
  model: ""
agent:
  systemPrompt:
    inline: "X"
  tools: []
permissions:
  approvalMode: interactive
`
	mustWriteFile(t, path, badManifest)

	outcomes, cancel, done := startWatch(t, c, path, DefaultReloadPolicy())
	o := recvOutcome(t, outcomes)
	stopWatch(t, cancel, done)

	if !o.Rejected || o.Err == nil {
		t.Fatalf("invalid manifest not rejected: %+v", o)
	}
	if o.Applied || o.RestartRequired {
		t.Fatalf("invalid manifest misclassified: %+v", o)
	}
	if !o.Event.Diagnostics.HasErrors() {
		t.Fatalf("invalid manifest outcome missing diagnostics: %+v", o.Event)
	}
	if c.HarnessSnapshot().PlanDigest != before.PlanDigest {
		t.Fatalf("client mutated on invalid reload")
	}
}

// TestWatchHarnessNonHarnessUnsupported: a plain client returns the unsupported
// sentinel immediately, without starting a watcher.
func TestWatchHarnessNonHarnessUnsupported(t *testing.T) {
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

	err = c.WatchHarness(context.Background(), filepath.Join(dir, "harness.yaml"), DefaultReloadPolicy(), nil)
	if !errors.Is(err, ErrNotHarnessClient) {
		t.Fatalf("err = %v, want ErrNotHarnessClient", err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	// WatchHarness observes production-style file replacement. An in-place
	// os.WriteFile truncates first and can expose an empty/partial manifest to
	// the watcher, making reload-cycle tests depend on scheduler timing.
	if err := atomicfile.Write(path, []byte(content), atomicfile.WithPerm(0o644)); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
