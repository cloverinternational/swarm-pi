package chat

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
)

var errThinkingSwitchTestReload = errors.New("stop after model config lookup")

func TestApplyProviderModelThinkingConfig(t *testing.T) {
	tests := []struct {
		name        string
		model       commands.ModelConfig
		startOn     bool
		startBudget int
		startEffort string
		wantOn      bool
		wantBudget  int
		wantEffort  string
	}{
		{name: "unspecified inherits", model: commands.ModelConfig{ID: "catalog-only"}, startOn: true, startBudget: 4096, startEffort: "high", wantOn: true, wantBudget: 4096, wantEffort: "high"},
		{name: "explicit enabled overrides", model: commands.ModelConfig{ID: "enabled", ThinkingEnabled: true, ThinkingBudget: 8192, ThinkingEffort: "max"}, startOn: false, startBudget: 2048, wantOn: true, wantBudget: 8192, wantEffort: "max"},
		{name: "explicit disabled overrides", model: commands.ModelConfig{ID: "disabled", ThinkingBudget: 2048}, startOn: true, startBudget: 4096, startEffort: "high", wantOn: false, wantBudget: 2048, wantEffort: ""},
		{name: "explicit bare false overrides", model: commands.ModelConfig{ID: "disabled", ThinkingEnabledSet: true}, startOn: true, startBudget: 4096, startEffort: "high", wantOn: false, wantBudget: 4096, wantEffort: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdk := &SDKIntegration{
				thinkingEnabled:        tt.startOn,
				thinkingBudget:         tt.startBudget,
				thinkingEffort:         tt.startEffort,
				thinkingDefaultEnabled: tt.startOn,
				thinkingDefaultBudget:  tt.startBudget,
				thinkingDefaultEffort:  tt.startEffort,
			}
			sdk.applyProviderModelThinkingConfig(tt.model)
			if sdk.thinkingEnabled != tt.wantOn || sdk.thinkingBudget != tt.wantBudget || sdk.thinkingEffort != tt.wantEffort {
				t.Fatalf("got enabled=%v budget=%d effort=%q; want enabled=%v budget=%d effort=%q", sdk.thinkingEnabled, sdk.thinkingBudget, sdk.thinkingEffort, tt.wantOn, tt.wantBudget, tt.wantEffort)
			}
		})
	}
}

func newThinkingSwitchTestSDK(t *testing.T) *SDKIntegration {
	t.Helper()
	initial := &testChatProvider{name: "openai"}
	logger := tuiobs.NewTUILogger()
	return &SDKIntegration{
		provider:               initial,
		providerName:           "openai",
		currentModel:           "old-model",
		logger:                 logger,
		tracer:                 tuiobs.NewTUITracer(logger),
		agent:                  newTestAgent(t, initial, "openai", "old-model"),
		thinkingEnabled:        false,
		thinkingBudget:         2048,
		thinkingEffort:         "",
		thinkingDefaultEnabled: true,
		thinkingDefaultBudget:  4096,
		thinkingDefaultEffort:  "high",
		diffusionModel:         true,
		providerBuilder: func(string, string, providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return nil, "", "", false, errThinkingSwitchTestReload
		},
	}
}

func TestSwitchProviderUnchangedPreservesEffectiveModelState(t *testing.T) {
	sdk := newThinkingSwitchTestSDK(t)
	sdk.providerBuilder = func(string, string, providerBuildDeps) (provider.Provider, string, string, bool, error) {
		t.Fatal("unchanged provider/model should not reload")
		return nil, "", "", false, nil
	}

	if err := sdk.SwitchProvider("openai", "old-model"); err != nil {
		t.Fatalf("SwitchProvider() error = %v", err)
	}
	if sdk.thinkingEnabled || sdk.thinkingBudget != 2048 || sdk.thinkingEffort != "" || !sdk.diffusionModel {
		t.Fatalf("unchanged switch altered effective state: enabled=%v budget=%d effort=%q diffusion=%v", sdk.thinkingEnabled, sdk.thinkingBudget, sdk.thinkingEffort, sdk.diffusionModel)
	}
}

func TestSwitchProviderResetsEffectiveModelStateBeforeProvidersLookupFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "providers.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}

	sdk := newThinkingSwitchTestSDK(t)
	err := sdk.SwitchProvider("openai", "new-model")
	if !errors.Is(err, errThinkingSwitchTestReload) {
		t.Fatalf("SwitchProvider() error = %v, want %v", err, errThinkingSwitchTestReload)
	}
	if !sdk.thinkingEnabled || sdk.thinkingBudget != 4096 || sdk.thinkingEffort != "high" || sdk.diffusionModel {
		t.Fatalf("failed providers lookup left stale effective state: enabled=%v budget=%d effort=%q diffusion=%v", sdk.thinkingEnabled, sdk.thinkingBudget, sdk.thinkingEffort, sdk.diffusionModel)
	}
}

func TestSwitchProviderAppliesExplicitTargetModelStateAfterReset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configDir := filepath.Join(home, ".swarmos")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	providers := `[{"name":"openai","display_name":"OpenAI","type":"api_key","api_type":"openai","models":[{"id":"new-model","display_name":"New Model","thinking_enabled":true,"thinking_budget":8192,"thinking_effort":"max","diffusion":true}]}]`
	if err := os.WriteFile(filepath.Join(configDir, "providers.json"), []byte(providers), 0o644); err != nil {
		t.Fatal(err)
	}

	sdk := newThinkingSwitchTestSDK(t)
	err := sdk.SwitchProvider("openai", "new-model")
	if !errors.Is(err, errThinkingSwitchTestReload) {
		t.Fatalf("SwitchProvider() error = %v, want %v", err, errThinkingSwitchTestReload)
	}
	if !sdk.thinkingEnabled || sdk.thinkingBudget != 8192 || sdk.thinkingEffort != "max" || !sdk.diffusionModel {
		t.Fatalf("target override not applied: enabled=%v budget=%d effort=%q diffusion=%v", sdk.thinkingEnabled, sdk.thinkingBudget, sdk.thinkingEffort, sdk.diffusionModel)
	}
}
