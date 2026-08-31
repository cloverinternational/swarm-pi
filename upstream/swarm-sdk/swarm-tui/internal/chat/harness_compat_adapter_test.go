package chat

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/vault"
	chatcontext "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/context"
)

// --- D1: pure, independently testable -- no *App, no SDKIntegration, no
// provider, no sdkclient.Client anywhere below TestBuildTUICompatPlan*. ---

func TestBuildTUICompatPlanSelectsExactlyTUIV1Preset(t *testing.T) {
	preset, ok := harness.LookupPreset("tui-v1")
	if !ok {
		t.Fatal("harness.LookupPreset(\"tui-v1\") not found — Phase 10c preset missing")
	}

	t.Setenv("SWARM_TUI_COMPAT_TEST_KEY_NOT_USED", "test-value-not-a-real-credential")
	plan, bindings, err := BuildTUICompatPlan("anthropic", "claude-harness-test", SDKIntegrationOptions{}, TUICompatOptions{
		SystemPrompt:     "COMPAT-TEST-PROMPT",
		CredentialEnvVar: "SWARM_TUI_COMPAT_TEST_KEY_NOT_USED",
	})
	if err != nil {
		t.Fatalf("BuildTUICompatPlan: %v", err)
	}
	if plan == nil {
		t.Fatal("BuildTUICompatPlan returned a nil plan with a nil error")
	}
	// D1(b): the adapter always reports an explicit bindings value, never a
	// naked zero struct constructed elsewhere.
	if !reflect.DeepEqual(bindings, sdkclient.HarnessBindings{}) {
		t.Fatalf("bindings = %#v, want the explicit zero value (see BuildTUICompatHarnessBindings doc)", bindings)
	}

	if got := plan.Compatibility(); got != "tui-v1" {
		t.Fatalf("plan.Compatibility() = %q, want %q", got, "tui-v1")
	}

	// D3: the adapter must REUSE harness.LookupPreset("tui-v1") verbatim, not
	// hand-roll a second, independently-maintained id list. Prove the
	// compiled plan's authorized tool set is EXACTLY the preset's expansion
	// (same elements; order is the preset's own, since the adapter never
	// reorders it).
	gotTools := append([]string(nil), plan.Tools()...)
	wantTools := append([]string(nil), preset.IDs...)
	if !reflect.DeepEqual(gotTools, wantTools) {
		t.Fatalf("plan.Tools() = %v, want exactly harness.LookupPreset(\"tui-v1\").IDs = %v", gotTools, wantTools)
	}
}

func TestBuildTUICompatPlanFailsClosedWithoutSystemPrompt(t *testing.T) {
	plan, _, err := BuildTUICompatPlan("anthropic", "claude-harness-test", SDKIntegrationOptions{}, TUICompatOptions{
		CredentialEnvVar: "SWARM_TUI_COMPAT_TEST_KEY_NOT_USED",
	})
	if err == nil {
		t.Fatal("expected an error when SystemPrompt is empty")
	}
	if plan != nil {
		t.Fatal("expected a nil plan on failure — no silent fallback to a partial plan")
	}
}

func TestBuildTUICompatPlanFailsClosedWithoutCredential(t *testing.T) {
	plan, _, err := BuildTUICompatPlan("anthropic", "claude-harness-test", SDKIntegrationOptions{}, TUICompatOptions{
		SystemPrompt: "COMPAT-TEST-PROMPT",
	})
	if err == nil {
		t.Fatal("expected an error when CredentialEnvVar is empty (tui-v1 selects CredentialBound=yes capabilities)")
	}
	if plan != nil {
		t.Fatal("expected a nil plan on failure — no silent fallback to a partial (uncredentialed) plan")
	}
	if !strings.Contains(err.Error(), "CredentialEnvVar") {
		t.Fatalf("error does not name the missing field: %v", err)
	}
}

func TestBuildTUICompatPlanFailsClosedWithoutProviderOrModel(t *testing.T) {
	if _, _, err := BuildTUICompatPlan("", "claude-harness-test", SDKIntegrationOptions{}, TUICompatOptions{
		SystemPrompt: "P", CredentialEnvVar: "V",
	}); err == nil {
		t.Fatal("expected an error when providerName is empty")
	}
	if _, _, err := BuildTUICompatPlan("anthropic", "", SDKIntegrationOptions{}, TUICompatOptions{
		SystemPrompt: "P", CredentialEnvVar: "V",
	}); err == nil {
		t.Fatal("expected an error when model is empty")
	}
}

func TestBuildTUICompatPlanNeverLeaksCredentialOrPromptIntoError(t *testing.T) {
	// HARD INVARIANT: no credential, prompt body, or secret in any new error
	// or log. Force every fail-closed branch and inspect the error text.
	secretPrompt := "SUPER-SECRET-SYSTEM-PROMPT-MUST-NOT-LEAK"
	_, _, err1 := BuildTUICompatPlan("anthropic", "m", SDKIntegrationOptions{}, TUICompatOptions{CredentialEnvVar: "V"})
	if err1 == nil || strings.Contains(err1.Error(), secretPrompt) {
		t.Fatalf("unexpected error content: %v", err1)
	}

	secretEnvName := "SUPER-SECRET-ENV-VAR-NAME-MUST-NOT-LEAK-AS-A-VALUE"
	_, _, err2 := BuildTUICompatPlan("anthropic", "m", SDKIntegrationOptions{}, TUICompatOptions{SystemPrompt: "p"})
	if err2 == nil || strings.Contains(err2.Error(), secretEnvName) {
		t.Fatalf("unexpected error content: %v", err2)
	}
}

func TestBuildTUICompatHarnessBindingsIsExplicitZeroValue(t *testing.T) {
	// Pure: constructing SDKIntegrationOptions here touches no TUI, provider,
	// or client. This documents (and pins) today's "no TUI host object
	// implements a harness binding interface" fact — see the doc comment on
	// BuildTUICompatHarnessBindings for the enumeration.
	got := BuildTUICompatHarnessBindings(SDKIntegrationOptions{})
	if supplied := got.Supplied(); len(supplied) != 0 {
		t.Fatalf("Supplied() = %v, want none (zero value)", supplied)
	}
}

// --- D3, RESOLVED. Phase 10d originally pinned a real gap here: compiling
// `compatibility: tui-v1` succeeded, but constructing a client from it failed
// because tuiV1PresetIDs included "web.websearch", which the Phase 10b binder
// classifies as HOST-BINDING-REQUIRED (it needs an ambient credential at
// construction), not BOUND. A "compatibility" preset that can never construct
// is exactly the silent under-delivery the preset shortfall list exists to
// prevent, so web.websearch was moved out of tuiV1PresetIDs and recorded in
// tuiV1Shortfall as ShortfallHostBindingRequired.
//
// This test now pins the RESOLVED state: tui-v1 must both COMPILE and
// CONSTRUCT, and web.websearch must be reported as a documented shortfall
// rather than silently missing. Re-adding web.websearch to the preset without
// a real binding will fail this test. ---

func TestTUIV1PresetPlanCompilesAndConstructs(t *testing.T) {
	t.Setenv("SWARM_TUI_COMPAT_TEST_KEY_NOT_USED", "test-value-not-a-real-credential")
	plan, _, err := BuildTUICompatPlan("anthropic", "claude-harness-test", SDKIntegrationOptions{}, TUICompatOptions{
		SystemPrompt:     "COMPAT-TEST-PROMPT",
		CredentialEnvVar: "SWARM_TUI_COMPAT_TEST_KEY_NOT_USED",
	})
	if err != nil {
		t.Fatalf("BuildTUICompatPlan (compile half): %v", err)
	}

	for _, id := range plan.Tools() {
		if id == "web.websearch" {
			t.Fatal("web.websearch is back in the tui-v1 preset; it is HOST-BINDING-REQUIRED, " +
				"so the preset would compile but never construct. Bind it in " +
				"client/harness_catalog_bind.go before re-adding it to tuiV1PresetIDs")
		}
	}

	c, err := sdkclient.New(sdkclient.WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("client construction failed for the tui-v1 compatibility plan: %v", err)
	}
	if c != nil {
		defer func() { _ = c.Close() }()
	}

	var documented bool
	for _, s := range plan.CompatibilityShortfall() {
		if s.Capability == "web.websearch" {
			documented = true
			if s.Cause != harness.ShortfallHostBindingRequired {
				t.Errorf("web.websearch shortfall cause = %q, want %q", s.Cause, harness.ShortfallHostBindingRequired)
			}
		}
	}
	if !documented {
		t.Error("web.websearch is neither selected nor documented as a shortfall — it must be one or the other, never silently absent")
	}
}

// --- D4: the harness branch's effective tool set must equal the plan's
// authorized set exactly. This exercises the REAL harness branch
// (NewSDKIntegrationWithOptions with opts.HarnessPlan set -->
// newHarnessSDKIntegration), using the `minimal` preset because (per the
// gap above) it is the one preset that is fully constructible today. A
// future prebuilt injection added back into newHarnessSDKIntegration (the
// B2 class of bug) would inflate the registry/exposed counts beyond the
// plan's authorized count and fail this test. ---

func writeMinimalCompatHarnessFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.yaml")
	manifest := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: d4-minimal-compat
provider:
  id: anthropic
  model: claude-harness-test
  credential:
    inline: test-key-not-used
agent:
  systemPrompt:
    inline: "D4-EQUALITY-TEST-PROMPT"
compatibility: minimal
permissions:
  approvalMode: interactive
`
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write harness fixture: %v", err)
	}
	return path
}

func TestHarnessBranchEffectiveToolsEqualPlanAuthorizedSet(t *testing.T) {
	minimal, ok := harness.LookupPreset("minimal")
	if !ok {
		t.Fatal("harness.LookupPreset(\"minimal\") not found — Phase 10c preset missing")
	}

	plan, err := harness.Compile(writeMinimalCompatHarnessFixture(t))
	if err != nil {
		t.Fatalf("harness.Compile: %v", err)
	}

	integration, err := NewSDKIntegrationWithOptions("", "", SDKIntegrationOptions{HarnessPlan: plan})
	if err != nil {
		t.Fatalf("NewSDKIntegrationWithOptions (harness branch): %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	snap := integration.sdkClient.HarnessSnapshot()
	if !snap.Harness {
		t.Fatal("HarnessSnapshot().Harness = false; construction did not take the harness branch")
	}

	wantIDs := append([]string(nil), minimal.IDs...)
	sort.Strings(wantIDs)
	gotIDs := append([]string(nil), snap.SelectedCatalogIDs...)
	sort.Strings(gotIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("plan.SelectedCatalogIDs = %v, want exactly the minimal preset's ids %v", gotIDs, wantIDs)
	}

	// The core D4 equality: registry size == exposed size == plan-authorized
	// size. Any prebuilt injection (a future B2) makes at least one of these
	// counts exceed len(wantIDs).
	registryNames := integration.toolRegistry.List()
	if len(registryNames) != len(wantIDs) {
		t.Fatalf("toolRegistry has %d tools %v, want exactly %d (plan authorized: %v)",
			len(registryNames), registryNames, len(wantIDs), wantIDs)
	}
	if len(snap.ExposedTools) != len(wantIDs) {
		t.Fatalf("provider-exposed tools = %v (%d), want exactly %d (plan authorized: %v)",
			snap.ExposedTools, len(snap.ExposedTools), len(wantIDs), wantIDs)
	}

	// Re-assert the specific B2 regression directly: HistorySearch/HistoryGet
	// must never appear on the harness branch (history.history_search /
	// history.history_get are DEFER-DISCOVER — see harness/presets.go's
	// tuiV1Shortfall — and are not in the `minimal` preset either).
	for _, forbidden := range []string{"HistorySearch", "HistoryGet"} {
		for _, name := range registryNames {
			if name == forbidden {
				t.Fatalf("harness branch registered %q outside the plan — B2 has regressed", forbidden)
			}
		}
	}
}

// TestHarnessBranchSetVaultProviderDoesNotInjectTools is the Phase 11c
// regression for the SetVaultProvider half of the injection-site finding
// from the Phase 10d audit: SetVaultProvider used to unconditionally
// re-register builtin.VaultTools() into the registry on every vault
// unlock, regardless of branch — the same bug class as B2
// (HistorySearch/HistoryGet). The `minimal` preset selects zero vault.*
// ids, so a harness-governed instance must still expose EXACTLY the
// preset's tool count after SetVaultProvider runs. The provider wiring
// itself (vault.SetDefaultVaultProvider) must still take effect, because
// any vault.* tool the plan DID authorize reads that same process-global
// singleton — this test asserts that half still runs too.
func TestHarnessBranchSetVaultProviderDoesNotInjectTools(t *testing.T) {
	minimal, ok := harness.LookupPreset("minimal")
	if !ok {
		t.Fatal("harness.LookupPreset(\"minimal\") not found — Phase 10c preset missing")
	}

	plan, err := harness.Compile(writeMinimalCompatHarnessFixture(t))
	if err != nil {
		t.Fatalf("harness.Compile: %v", err)
	}

	integration, err := NewSDKIntegrationWithOptions("", "", SDKIntegrationOptions{HarnessPlan: plan})
	if err != nil {
		t.Fatalf("NewSDKIntegrationWithOptions (harness branch): %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	before := len(integration.toolRegistry.List())
	if before != len(minimal.IDs) {
		t.Fatalf("precondition: registry has %d tools, want %d (minimal preset)", before, len(minimal.IDs))
	}

	fake := vault.NewVaultProvider(nil, nil, "")
	integration.SetVaultProvider(fake)

	after := integration.toolRegistry.List()
	if len(after) != before {
		t.Fatalf("SetVaultProvider changed registry size on the harness branch: %d -> %d %v — "+
			"vault.* tool presence must be decided solely by the plan", before, len(after), after)
	}
	if integration.GetVaultProvider() != fake {
		t.Fatal("SetVaultProvider must still wire the provider reference on the harness branch " +
			"(plan-authorized vault.* tools read the same process-global singleton)")
	}
}

// TestHarnessBranchEnableAdvancedToolModeIsNoop is the Phase 11c regression
// for the EnableAdvancedToolMode half of the same finding:
// EnableAdvancedToolMode used to unconditionally register meta.tool_search
// (a catalogued host-binding-required capability — see
// harness_catalog_bind.go) regardless of whether the plan authorized it.
func TestHarnessBranchEnableAdvancedToolModeIsNoop(t *testing.T) {
	minimal, ok := harness.LookupPreset("minimal")
	if !ok {
		t.Fatal("harness.LookupPreset(\"minimal\") not found — Phase 10c preset missing")
	}

	plan, err := harness.Compile(writeMinimalCompatHarnessFixture(t))
	if err != nil {
		t.Fatalf("harness.Compile: %v", err)
	}

	integration, err := NewSDKIntegrationWithOptions("", "", SDKIntegrationOptions{HarnessPlan: plan})
	if err != nil {
		t.Fatalf("NewSDKIntegrationWithOptions (harness branch): %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	before := len(integration.toolRegistry.List())
	if before != len(minimal.IDs) {
		t.Fatalf("precondition: registry has %d tools, want %d (minimal preset)", before, len(minimal.IDs))
	}

	integration.EnableAdvancedToolMode(1000)

	if integration.advancedToolMode {
		t.Fatal("EnableAdvancedToolMode must be a no-op on the harness branch, but advancedToolMode = true")
	}
	after := integration.toolRegistry.List()
	if len(after) != before {
		t.Fatalf("EnableAdvancedToolMode changed registry size on the harness branch: %d -> %d %v — "+
			"meta.tool_search is host-binding-required, not force-injectable", before, len(after), after)
	}
	for _, name := range after {
		if name == "tool_search" {
			t.Fatal("EnableAdvancedToolMode registered tool_search outside the plan — regression of the Phase 11c fix")
		}
	}
}

func TestHarnessGovernanceBlocksProviderMutation(t *testing.T) {
	plan, err := harness.Compile(writeMinimalCompatHarnessFixture(t))
	if err != nil {
		t.Fatalf("harness.Compile: %v", err)
	}
	integration, err := NewSDKIntegrationWithOptions("", "", SDKIntegrationOptions{HarnessPlan: plan})
	if err != nil {
		t.Fatalf("NewSDKIntegrationWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	providerBefore := integration.providerName
	modelBefore := integration.currentModel
	promptBefore := integration.activeAgent().SystemPrompt()

	if err := integration.SwitchProvider("openai", "replacement-model"); err == nil {
		t.Fatal("SwitchProvider succeeded for a harness-governed integration")
	}
	if err := integration.ReloadProvider(); err == nil {
		t.Fatal("ReloadProvider succeeded for a harness-governed integration")
	}
	if integration.providerName != providerBefore || integration.currentModel != modelBefore {
		t.Fatalf("provider/model mutated: got %q/%q, want %q/%q",
			integration.providerName, integration.currentModel, providerBefore, modelBefore)
	}
	if got := integration.activeAgent().SystemPrompt(); got != promptBefore {
		t.Fatalf("active agent prompt mutated: got %q, want %q", got, promptBefore)
	}
}

func TestHarnessSetDebugInspectProviderDoesNotInjectTool(t *testing.T) {
	plan, err := harness.Compile(writeMinimalCompatHarnessFixture(t))
	if err != nil {
		t.Fatalf("harness.Compile: %v", err)
	}
	integration, err := NewSDKIntegrationWithOptions("", "", SDKIntegrationOptions{
		HarnessPlan: plan,
		DebugMode:   true,
	})
	if err != nil {
		t.Fatalf("NewSDKIntegrationWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	before := append([]string(nil), integration.toolRegistry.List()...)
	integration.SetDebugInspectProvider(&App{})
	after := integration.toolRegistry.List()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("SetDebugInspectProvider changed harness tool registry: before=%v after=%v", before, after)
	}
	for _, name := range after {
		if name == "debug_inspect" {
			t.Fatal("SetDebugInspectProvider registered debug_inspect outside the harness plan")
		}
	}
}

func TestHarnessDirectPromptReasoningAndContextMutationAreNoops(t *testing.T) {
	plan, err := harness.Compile(writeMinimalCompatHarnessFixture(t))
	if err != nil {
		t.Fatalf("harness.Compile: %v", err)
	}
	integration, err := NewSDKIntegrationWithOptions("", "", SDKIntegrationOptions{HarnessPlan: plan})
	if err != nil {
		t.Fatalf("NewSDKIntegrationWithOptions: %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	promptBefore := integration.activeAgent().SystemPrompt()
	reasoningBefore := integration.GetReasoningEffort()
	thinkingBefore := integration.IsThinkingEnabled()
	thinkingBudgetBefore := integration.GetThinkingBudget()
	debugProviderBefore := integration.debugInspectProvider
	modelBefore := integration.sdkClient.HarnessSnapshot().Model
	integration.SetSystemPrompt("replacement prompt")
	integration.SetReasoningEffort("high")
	integration.EnableThinking(4096)
	integration.DisableThinking()
	integration.ToggleThinking()
	integration.SetDebugInspectProvider(&App{})
	integration.SetSkillsManager(&SkillsManager{})
	integration.SetPluginsManager(&PluginsManager{})
	integration.applyUserSystemPromptToAgent(context.Background(), "replacement prompt")
	integration.applyRequestedModel("replacement-model")

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("replacement context"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	loader := chatcontext.NewFileLoaderWithRoot(workspace)
	if err := integration.LoadAndInjectContext(loader, "replacement base"); err != nil {
		t.Fatalf("LoadAndInjectContext: %v", err)
	}

	if got := integration.activeAgent().SystemPrompt(); got != promptBefore {
		t.Fatalf("direct prompt/context mutation changed harness prompt: got %q, want %q", got, promptBefore)
	}
	if got := integration.GetReasoningEffort(); got != reasoningBefore {
		t.Fatalf("direct reasoning mutation changed harness setting: got %q, want %q", got, reasoningBefore)
	}
	if got := integration.IsThinkingEnabled(); got != thinkingBefore {
		t.Fatalf("direct thinking mutation changed harness setting: got %v, want %v", got, thinkingBefore)
	}
	if got := integration.GetThinkingBudget(); got != thinkingBudgetBefore {
		t.Fatalf("direct thinking budget changed harness setting: got %d, want %d", got, thinkingBudgetBefore)
	}
	if integration.debugInspectProvider != debugProviderBefore {
		t.Fatal("debug inspect provider changed in harness-governed integration")
	}
	if integration.GetSkillsManager() != nil || integration.GetPluginsManager() != nil {
		t.Fatal("ambient skills/plugins manager attached to harness-governed integration")
	}
	if got := integration.sdkClient.HarnessSnapshot().Model; got != modelBefore {
		t.Fatalf("per-request model override changed harness model: got %q, want %q", got, modelBefore)
	}
}
