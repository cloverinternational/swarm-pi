package chat

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sdkclient "github.com/Swarm-Code/mono/swarm-sdk/client"
	"github.com/Swarm-Code/mono/swarm-sdk/harness"
)

func writeInteractiveHarnessFixture(t *testing.T, approvalMode string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "harness.yaml")
	manifest := `apiVersion: swarm.ai/v1alpha1
kind: Harness
metadata:
  name: interactive-tui
provider:
  id: anthropic
  model: claude-harness-test
  credential:
    inline: test-key-not-used
agent:
  systemPrompt:
    inline: "HARNESS-TUI-CLOSED-PROMPT"
  tools:
    - forge.read
permissions:
  approvalMode: ` + approvalMode + "\n"
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write harness: %v", err)
	}
	return path
}

func TestHarnessTUIClientSnapshotConformsToDirectClient(t *testing.T) {
	path := writeInteractiveHarnessFixture(t, "interactive")
	plan, err := harness.Compile(path)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	direct, err := sdkclient.New(sdkclient.WithHarnessPlan(plan))
	if err != nil {
		t.Fatalf("direct client.New: %v", err)
	}
	want := direct.HarnessSnapshot()
	if err := direct.Close(); err != nil {
		t.Fatalf("close direct client: %v", err)
	}

	approvalBroker := NewPermissionsBroker(nil)
	questionBroker := NewQuestionBroker()
	planBroker := NewPlanBroker()
	integration, err := newSDKIntegrationForAppOptions("", "", AppOptions{HarnessPath: path}, SDKIntegrationOptions{
		ApprovalBroker: approvalBroker,
		QuestionBroker: questionBroker,
		PlanBroker:     planBroker,
	})
	if err != nil {
		t.Fatalf("TUI harness integration: %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })
	if integration.permissionChecker == nil || integration.approvalBroker != approvalBroker ||
		integration.questionBroker != questionBroker || integration.planBroker != planBroker {
		t.Fatal("interactive TUI brokers were not attached to the harness adapter")
	}

	got := integration.sdkClient.HarnessSnapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TUI snapshot differs from direct client:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestHarnessTUIClosedPostureIgnoresAmbientPromptAndProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ANTHROPIC_API_KEY", "ambient-key-must-not-win")
	t.Setenv("SWARM_PROVIDER", "ambient-provider-must-not-win")
	t.Setenv("SWARM_MODEL", "ambient-model-must-not-win")
	if err := os.WriteFile(filepath.Join(home, "INDEX.md"), []byte("AMBIENT-INDEX-MUST-NOT-APPEAR"), 0o600); err != nil {
		t.Fatalf("write INDEX.md: %v", err)
	}

	path := writeInteractiveHarnessFixture(t, "interactive")
	plan, allowYolo, err := harnessPlanForAppOptions(AppOptions{HarnessPath: path})
	if err != nil {
		t.Fatalf("harnessPlanForAppOptions: %v", err)
	}
	if allowYolo {
		t.Fatal("allowYolo = true without explicit app option")
	}
	integration, err := newSDKIntegrationForAppOptions("", "", AppOptions{HarnessPath: path}, SDKIntegrationOptions{})
	if err != nil {
		t.Fatalf("TUI harness integration: %v", err)
	}
	t.Cleanup(func() { _ = integration.Close() })

	snapshot := integration.sdkClient.HarnessSnapshot()
	if snapshot.Provider != "anthropic" || snapshot.Model != "claude-harness-test" {
		t.Fatalf("ambient provider/model leaked into snapshot: %#v", snapshot)
	}
	if snapshot.SystemPromptSHA256 != plan.SystemPromptHash() {
		t.Fatalf("prompt hash = %q, want %q", snapshot.SystemPromptSHA256, plan.SystemPromptHash())
	}
	if prompt := integration.agent.SystemPrompt(); prompt != "HARNESS-TUI-CLOSED-PROMPT" ||
		strings.Contains(prompt, "AMBIENT-INDEX-MUST-NOT-APPEAR") {
		t.Fatalf("ambient prompt content leaked: %q", prompt)
	}
}

func TestHarnessTUIYoloRequiresExplicitPosture(t *testing.T) {
	path := writeInteractiveHarnessFixture(t, "yolo")
	_, err := newSDKIntegrationForAppOptions("", "", AppOptions{HarnessPath: path}, SDKIntegrationOptions{})
	if err == nil {
		t.Fatal("expected yolo harness construction to fail without HarnessAllowYolo")
	}
	if !strings.Contains(err.Error(), "WithHarnessAllowYolo") {
		t.Fatalf("unexpected yolo rejection: %v", err)
	}
}

func TestHarnessCompileFailureIsFatalToSynchronousStartup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	opts := AppOptions{
		HarnessPath:   filepath.Join(t.TempDir(), "missing-harness.yaml"),
		WorkspaceRoot: t.TempDir(),
	}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("NewAppWithOptions returned after harness compile failure; startup must be fatal")
		}
	}()
	_ = NewAppWithOptions(opts)
}

func TestHarnessClientConstructionFailureIsFatalToSynchronousStartup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := writeInteractiveHarnessFixture(t, "yolo")
	opts := AppOptions{
		HarnessPath:   path,
		WorkspaceRoot: t.TempDir(),
		// Deliberately omit HarnessAllowYolo so client construction fails.
	}

	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("NewAppWithOptions returned after harness client construction failure; startup must be fatal")
		}
	}()
	_ = NewAppWithOptions(opts)
}

func TestHarnessModelAndPromptSettingsCallbacksFailClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	workspace := t.TempDir()
	app := NewAppWithOptions(AppOptions{
		HarnessPath:   writeInteractiveHarnessFixture(t, "interactive"),
		WorkspaceRoot: workspace,
		ProjectRoot:   workspace,
	})
	if app.sdk == nil || !app.sdk.harnessGoverned() {
		t.Fatal("precondition: app did not start with a harness-governed SDK")
	}
	if app.currentProvider != "anthropic" || app.currentModel != "claude-harness-test" {
		t.Fatalf("app restored ambient provider/model over harness selection: got %q/%q",
			app.currentProvider, app.currentModel)
	}
	t.Cleanup(func() { _ = app.sdk.Close() })

	appProviderBefore := app.currentProvider
	appModelBefore := app.currentModel
	sdkProviderBefore := app.sdk.providerName
	sdkModelBefore := app.sdk.currentModel
	promptBefore := app.sdk.activeAgent().SystemPrompt()

	modelSettings := app.settingsManager.GetModelSettings()
	if modelSettings == nil {
		t.Fatal("model settings unavailable")
	}
	modelSettings.SetModel("openai", "replacement-model")
	if app.currentProvider != appProviderBefore || app.currentModel != appModelBefore {
		t.Fatalf("model callback mutated app provider/model: got %q/%q, want %q/%q",
			app.currentProvider, app.currentModel, appProviderBefore, appModelBefore)
	}
	if app.sdk.providerName != sdkProviderBefore || app.sdk.currentModel != sdkModelBefore {
		t.Fatalf("model callback mutated SDK provider/model: got %q/%q, want %q/%q",
			app.sdk.providerName, app.sdk.currentModel, sdkProviderBefore, sdkModelBefore)
	}

	promptSettings := app.settingsManager.GetSystemPromptSettings()
	if promptSettings == nil {
		t.Fatal("system prompt settings unavailable")
	}
	if err := promptSettings.CreatePrompt("governance-regression", "replacement prompt"); err != nil {
		t.Fatalf("CreatePrompt: %v", err)
	}
	if err := promptSettings.SetActivePrompt("governance-regression"); err != nil {
		t.Fatalf("SetActivePrompt: %v", err)
	}
	if got := app.sdk.activeAgent().SystemPrompt(); got != promptBefore {
		t.Fatalf("prompt callback mutated active agent prompt: got %q, want %q", got, promptBefore)
	}

	// The active prompt is persisted even though its callback is denied. A new
	// harness session must not restore that saved prompt over the compiled plan.
	restarted := NewAppWithOptions(AppOptions{
		HarnessPath:   writeInteractiveHarnessFixture(t, "interactive"),
		WorkspaceRoot: workspace,
		ProjectRoot:   workspace,
	})
	if restarted.sdk == nil || !restarted.sdk.harnessGoverned() {
		t.Fatal("precondition: restarted app is not harness-governed")
	}
	t.Cleanup(func() { _ = restarted.sdk.Close() })
	if got := restarted.sdk.activeAgent().SystemPrompt(); got != promptBefore {
		t.Fatalf("startup restored saved prompt over harness prompt: got %q, want %q", got, promptBefore)
	}
}
