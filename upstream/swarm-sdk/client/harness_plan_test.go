package client

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	provanthropic "github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
)

// compilePlan is a helper that compiles an in-memory manifest into a *harness.Plan
// rooted at a temp directory, so manifest-relative workspace/storage resolve to a
// real, isolated location.
func compilePlan(t *testing.T, manifest string) *harness.Plan {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	return plan
}

const minimalManifest = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: min
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "HARNESS-PROMPT-SENTINEL"
  tools:
    - forge.read
    - forge.undo
  limits:
    maxOutputTokens: 4096
    maxTurns: 12
    timeoutSeconds: 600
permissions:
  approvalMode: interactive
`

func buildHarnessClient(t *testing.T, manifest string, extra ...Option) (*Client, error) {
	t.Helper()
	plan := compilePlan(t, manifest)
	opts := append([]Option{WithHarnessPlan(plan)}, extra...)
	return New(opts...)
}

func mustBuild(t *testing.T, manifest string, extra ...Option) *Client {
	t.Helper()
	c, err := buildHarnessClient(t, manifest, extra...)
	if err != nil {
		t.Fatalf("New(harness): %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestHarnessMinimalBuild: a minimal plan builds a client with provider/model/
// prompt/limits applied.
func TestHarnessMinimalBuild(t *testing.T) {
	c := mustBuild(t, minimalManifest)

	if got := c.agentDef.Provider; got != "anthropic" {
		t.Errorf("provider = %q, want anthropic", got)
	}
	if got := c.agentDef.Model; got != "claude-x" {
		t.Errorf("model = %q, want claude-x", got)
	}
	if !strings.Contains(c.agent.SystemPrompt(), "HARNESS-PROMPT-SENTINEL") {
		t.Errorf("system prompt does not contain the plan prompt: %q", c.agent.SystemPrompt())
	}
	if c.opts.maxTokens != 4096 {
		t.Errorf("maxTokens = %d, want 4096", c.opts.maxTokens)
	}
	if c.agentDef.Capabilities == nil || c.agentDef.Capabilities.MaxTurns != 12 {
		t.Errorf("MaxTurns not applied: %+v", c.agentDef.Capabilities)
	}
	if c.agentDef.Capabilities.Timeout.Seconds() != 600 {
		t.Errorf("Timeout = %v, want 600s", c.agentDef.Capabilities.Timeout)
	}
}

// TestHarnessExactExposure: exact-tools plan exposes exactly those tools, no more.
func TestHarnessExactExposure(t *testing.T) {
	c := mustBuild(t, minimalManifest)
	names := providerToolNames(c)
	want := map[string]bool{"Read": true, "Undo": true}
	if len(names) != len(want) {
		t.Fatalf("exposed tools = %v, want exactly %v", names, keys(want))
	}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected exposed tool %q (want only %v)", n, keys(want))
		}
	}
}

// TestHarnessZeroToolsExposeNothing: a zero-tool plan exposes zero tools through
// the REAL exposure path.
func TestHarnessZeroToolsExposeNothing(t *testing.T) {
	const zero = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: zero
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "zero"
  tools: []
permissions:
  approvalMode: interactive
`
	c := mustBuild(t, zero)
	names := providerToolNames(c)
	if len(names) != 0 {
		t.Fatalf("zero-tool plan exposed %v, want none", names)
	}
	// The sentinel must never appear as a provider-visible tool.
	for _, n := range names {
		if n == harnessNoToolsSentinel {
			t.Fatalf("sentinel leaked into provider tools")
		}
	}
}

// TestHarnessEmptySelectionNeverAllTools is the P0 regression: empty selection
// must never yield "all tools" (empty-hints-means-all). Even though the closed
// registry is empty, we assert the exposure path returns nothing.
func TestHarnessEmptySelectionNeverAllTools(t *testing.T) {
	const zero = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: zero
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "zero"
  tools: []
permissions:
  approvalMode: interactive
`
	c := mustBuild(t, zero)
	// ToolHints must be the non-empty sentinel, NOT empty (which means all).
	if len(c.agentDef.ToolHints) == 0 {
		t.Fatalf("ToolHints is empty for a zero-tool plan; empty means ALL tools")
	}
	if c.agentDef.ToolHints[0] != harnessNoToolsSentinel {
		t.Fatalf("ToolHints[0] = %q, want sentinel", c.agentDef.ToolHints[0])
	}
	if len(providerToolNames(c)) != 0 {
		t.Fatalf("empty selection widened to tools: %v", providerToolNames(c))
	}
}

// TestHarnessUnboundCatalogID: a class-legal catalog ID that requires a host
// runtime binding this client does not supply (Phase 10b D1 route (b)) still
// fails construction, with no partial client — now with a PRECISE reason
// instead of the old generic "not yet bound in Phase 2" message, because
// Phase 10b bound every capability constructible from the workspace alone
// (builtin.bash included) and routed the rest through
// harnessToolHostBindingReasons.
func TestHarnessUnboundCatalogID(t *testing.T) {
	const m = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: unbound
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "x"
  tools:
    - interactive.ask_user_question
permissions:
  approvalMode: interactive
`
	c, err := buildHarnessClient(t, m)
	if err == nil {
		_ = c.Close()
		t.Fatalf("expected construction error for a host-binding-required catalog id, got nil")
	}
	if c != nil {
		t.Fatalf("expected nil client on failure, got %+v", c)
	}
	if !strings.Contains(err.Error(), "requires a host runtime binding") {
		t.Errorf("error = %v, want 'requires a host runtime binding'", err)
	}
}

// TestHarnessApprovalModeEnum covers interactive/readonly OK; unknown => error;
// yolo without posture => error; yolo WITH posture => allowed.
func TestHarnessApprovalModeEnum(t *testing.T) {
	mk := func(mode string) string {
		return `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: appr
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "x"
  tools:
    - forge.read
permissions:
  approvalMode: ` + mode + "\n"
	}

	t.Run("interactive_ok", func(t *testing.T) {
		c := mustBuild(t, mk("interactive"))
		_ = c
	})
	t.Run("readonly_ok", func(t *testing.T) {
		c := mustBuild(t, mk("readonly"))
		_ = c
	})
	t.Run("unknown_err", func(t *testing.T) {
		c, err := buildHarnessClient(t, mk("banana"))
		if err == nil {
			_ = c.Close()
			t.Fatalf("unknown approvalMode should error")
		}
		if !strings.Contains(err.Error(), "unknown approvalMode") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("yolo_without_posture_err", func(t *testing.T) {
		c, err := buildHarnessClient(t, mk("yolo"))
		if err == nil {
			_ = c.Close()
			t.Fatalf("yolo without posture should error")
		}
		if !strings.Contains(err.Error(), "named posture") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("yolo_with_posture_ok", func(t *testing.T) {
		c := mustBuild(t, mk("yolo"), WithHarnessAllowYolo())
		if c.opts.approvalMode != "yolo" {
			t.Errorf("approvalMode = %q, want yolo", c.opts.approvalMode)
		}
	})
}

// TestHarnessClosedPosture: decoy env vars and a decoy INDEX.md in the workspace
// must NOT influence the effective prompt or tools.
func TestHarnessClosedPosture(t *testing.T) {
	// Decoy env that the non-harness path would read.
	t.Setenv("SWARM_PROVIDER", "openai")
	t.Setenv("SWARM_MODEL", "gpt-4o-decoy")

	// Compile a plan whose workspace is a temp dir that also contains a decoy
	// INDEX.md. The manifest lives in that dir (workspace defaults to it).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "INDEX.md"), []byte("DECOY-INDEX-CONTENT"), 0644); err != nil {
		t.Fatal(err)
	}
	manifest := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: closed
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: test-key
agent:
  systemPrompt:
    inline: "CLOSED-PROMPT-ONLY"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
`
	src := filepath.Join(dir, "harness.yaml")
	plan, err := harness.CompileBytes([]byte(manifest), src)
	if err != nil {
		t.Fatalf("CompileBytes: %v", err)
	}
	c, err := New(WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	sp := c.agent.SystemPrompt()
	if strings.Contains(sp, "DECOY-INDEX-CONTENT") {
		t.Errorf("INDEX.md decoy leaked into system prompt")
	}
	if strings.Contains(sp, "Working directory:") {
		t.Errorf("ambient <env> working-directory block leaked into system prompt")
	}
	if sp != "CLOSED-PROMPT-ONLY" {
		t.Errorf("effective prompt = %q, want exactly the plan prompt", sp)
	}
	// Env decoys must not change provider/model.
	if c.agentDef.Provider != "anthropic" || c.agentDef.Model != "claude-x" {
		t.Errorf("env decoys leaked: provider=%q model=%q", c.agentDef.Provider, c.agentDef.Model)
	}
	// Effective live prompt hash must equal the plan's own prompt hash.
	snap := c.HarnessSnapshot()
	if snap.SystemPromptSHA256 != plan.SystemPromptHash() {
		t.Errorf("live prompt hash %q != plan prompt hash %q", snap.SystemPromptSHA256, plan.SystemPromptHash())
	}
}

// TestHarnessSnapshotRealStateNoSecret: snapshot reflects REAL exposed tools and
// contains no secret value.
func TestHarnessSnapshotRealStateNoSecret(t *testing.T) {
	const m = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: snap
provider:
  id: anthropic
  model: claude-x
  credential:
    inline: super-secret-value
agent:
  systemPrompt:
    inline: "x"
  tools:
    - forge.read
    - forge.apply_patch
permissions:
  approvalMode: interactive
`
	c := mustBuild(t, m)
	snap := c.HarnessSnapshot()
	if !snap.Harness {
		t.Fatal("snapshot.Harness = false")
	}
	// Exposed tools mirror the real provider payload.
	got := map[string]bool{}
	for _, n := range snap.ExposedTools {
		got[n] = true
	}
	if !got["Read"] || !got["apply_patch"] || len(snap.ExposedTools) != 2 {
		t.Errorf("ExposedTools = %v, want exactly [Read apply_patch]", snap.ExposedTools)
	}
	// No absolute paths, no secret.
	blob := snap.PlanName + snap.PlanDigest + snap.Provider + snap.Model +
		snap.SystemPromptSHA256 + snap.ApprovalMode + snap.CredentialSource +
		snap.WorkspaceSHA256 + snap.StorageSHA256 + strings.Join(snap.ExposedTools, ",") +
		strings.Join(snap.SelectedCatalogIDs, ",")
	if strings.Contains(blob, "super-secret-value") {
		t.Fatal("secret value leaked into snapshot")
	}
	if snap.CredentialSource != "inline" {
		t.Errorf("CredentialSource = %q, want redacted label 'inline'", snap.CredentialSource)
	}
	if snap.PlanDigest == "" || snap.WorkspaceSHA256 == "" {
		t.Errorf("snapshot missing digest/workspace hash")
	}
}

// TestNonHarnessClientUnchanged: a client built WITHOUT a harness plan still
// constructs and reports no harness snapshot (baseline construction test).
func TestNonHarnessClientUnchanged(t *testing.T) {
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
	if c.HarnessSnapshot().Harness {
		t.Errorf("non-harness client reports Harness=true")
	}
	if c.agentDef == nil {
		t.Fatal("baseline client has no agent definition")
	}
}

// TestHarnessAbsentCredentialFailsClosed is the primary MAJOR regression: on the
// closed path a plan with NO declared credential must FAIL construction and must
// NOT silently fall back to an ambient ANTHROPIC_API_KEY. A decoy ambient key is
// set to prove it is never consulted, and the returned client must be nil.
func TestHarnessAbsentCredentialFailsClosed(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-DECOY-AMBIENT-MUST-NOT-BE-USED")
	const m = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: nocred
provider:
  id: anthropic
  model: claude-x
agent:
  systemPrompt:
    inline: "x"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
`
	c, err := buildHarnessClient(t, m)
	if err == nil {
		if c != nil {
			_ = c.Close()
		}
		t.Fatalf("expected construction to FAIL with an absent credential, got nil error")
	}
	if c != nil {
		t.Fatalf("expected nil client on fail-closed credential, got %+v", c)
	}
	if !strings.Contains(err.Error(), "empty credential") {
		t.Errorf("error = %v, want a fail-closed 'empty credential' error", err)
	}
	// The decoy must never be reported as the credential source anywhere.
	if strings.Contains(err.Error(), "DECOY") {
		t.Errorf("decoy ambient key leaked into the error: %v", err)
	}
}

// TestHarnessCredentialUsesPlanNotAmbient proves that when the plan DOES declare
// a credential (here via an env var the plan names), the plan's key is the
// effective key and a decoy ambient ANTHROPIC_API_KEY never becomes effective.
// Sentinel values make the assertion unambiguous.
func TestHarnessCredentialUsesPlanNotAmbient(t *testing.T) {
	const decoy = "sk-DECOY-ambient-must-not-win"
	const planKey = "sk-PLAN-declared-sentinel"
	t.Setenv("ANTHROPIC_API_KEY", decoy)
	t.Setenv("HARNESS_PLAN_KEY", planKey)
	const m = `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: plancred
provider:
  id: anthropic
  model: claude-x
  credential:
    env: HARNESS_PLAN_KEY
agent:
  systemPrompt:
    inline: "x"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
`
	c := mustBuild(t, m)
	if c.opts.apiKey != planKey {
		t.Errorf("effective apiKey = %q, want the plan-declared key %q", c.opts.apiKey, planKey)
	}
	if c.opts.apiKey == decoy {
		t.Errorf("decoy ambient key leaked as the effective credential")
	}
	p, ok := c.provider.(*provanthropic.Provider)
	if !ok {
		t.Fatalf("provider boundary type = %T, want *anthropic.Provider", c.provider)
	}
	providerValue := reflect.ValueOf(p).Elem()
	gotKey := providerValue.FieldByName("config").FieldByName("APIKey").String()
	if gotKey != planKey {
		t.Errorf("provider.Config.APIKey = %q, want plan key %q", gotKey, planKey)
	}
	if gotKey == decoy {
		t.Error("provider.Config.APIKey contains the ambient decoy")
	}
}

// TestHarnessProviderIdentityAllowlist checks the ORIGINAL requested identity,
// before normalizeProviderName can collapse an OAuth/keyless alias into an
// allowlisted canonical provider.
func TestHarnessProviderIdentityAllowlist(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "gm-DECOY")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "decoy-project")
	tests := []struct {
		id       string
		accepted bool
	}{
		{id: "anthropic", accepted: true},
		{id: "openai", accepted: true},
		{id: "gemini"},
		{id: "claudecode"},
		{id: "claude-code"},
		{id: "claude_code"},
		{id: "ollama"},
		{id: "cursor"},
		{id: "google"},
		{id: "xai"},
		{id: "grok-oauth"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			m := fmt.Sprintf(`apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: provider-identity
provider:
  id: %s
  model: test-model
  credential:
    inline: present-key
agent:
  systemPrompt:
    inline: "x"
  tools:
    - forge.read
permissions:
  approvalMode: interactive
`, tc.id)
			c, err := buildHarnessClient(t, m)
			if tc.accepted {
				if err != nil {
					t.Fatalf("accepted provider %q failed: %v", tc.id, err)
				}
				if c == nil {
					t.Fatalf("accepted provider %q returned nil client", tc.id)
				}
				t.Cleanup(func() { _ = c.Close() })
				return
			}
			if err == nil {
				if c != nil {
					_ = c.Close()
				}
				t.Fatalf("provider %q was admitted; want closed-mode rejection", tc.id)
			}
			if c != nil {
				t.Fatalf("provider %q returned non-nil client on rejection", tc.id)
			}
			if !strings.Contains(err.Error(), "not yet supported in closed harness mode") {
				t.Errorf("provider %q error = %v, want explicit closed-mode rejection", tc.id, err)
			}
		})
	}
}

func TestHarnessAnthropicSealsAmbientRawDumpAtProviderBoundary(t *testing.T) {
	dumpPath := filepath.Join(t.TempDir(), "must-not-exist.log")
	t.Setenv("SAC_RAW_DUMP", dumpPath)
	t.Setenv("ENVELOPE_TRACE", "decoy-enabled")
	t.Setenv("CACHE_DEBUG", "decoy-enabled")

	c := mustBuild(t, minimalManifest)
	if _, err := os.Stat(dumpPath); !os.IsNotExist(err) {
		t.Fatalf("closed harness created ambient raw dump %q: stat err=%v", dumpPath, err)
	}
	p, ok := c.provider.(*provanthropic.Provider)
	if !ok {
		t.Fatalf("provider boundary type = %T, want *anthropic.Provider", c.provider)
	}
	providerValue := reflect.ValueOf(p).Elem()
	if !providerValue.FieldByName("noAmbientEnv").Bool() {
		t.Fatal("provider boundary did not retain NoAmbientEnv=true")
	}
	configValue := providerValue.FieldByName("config")
	if !configValue.FieldByName("RawDebugWriter").IsNil() {
		t.Fatal("sealed provider boundary installed a RawDebugWriter from ambient env")
	}
}

// TestHarnessSealedProviderAllowlist documents the auditable allowlist contents
// so an accidental broadening (e.g. adding gemini) trips a test.
func TestHarnessSealedProviderAllowlist(t *testing.T) {
	want := map[string]bool{"anthropic": true, "openai": true}
	if len(harnessSealedProviders) != len(want) {
		t.Fatalf("sealed provider set = %v, want exactly %v", harnessSealedProviders, want)
	}
	for name := range harnessSealedProviders {
		if !want[name] {
			t.Errorf("unexpected sealed provider %q; the closed allowlist must stay minimal", name)
		}
	}
	if _, ok := harnessSealedProviders["gemini"]; ok {
		t.Fatal("gemini must NOT be in the sealed allowlist: its factory reads ambient env")
	}
}

// --- helpers ---------------------------------------------------------------

func providerToolNames(c *Client) []string {
	out := make([]string, 0)
	for _, t := range c.agent.ProviderTools() {
		out = append(out, t.Name)
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
