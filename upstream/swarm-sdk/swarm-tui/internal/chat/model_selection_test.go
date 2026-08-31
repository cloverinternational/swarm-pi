package chat

import (
	"errors"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	tuiobs "github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/metrics"
)

func TestModelCommandFailedBuilderPreservesAllSelectionState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	configManager, err := commands.NewConfigManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := configManager.SaveConfig(&commands.SwarmOSConfig{
		CurrentProvider: "openai",
		CurrentModel:    "old-model",
	}); err != nil {
		t.Fatal(err)
	}

	oldProvider := &testChatProvider{name: "openai"}
	oldAgent := newTestAgent(t, oldProvider, "openai", "old-model")
	logger := tuiobs.NewTUILogger()
	tracer := tuiobs.NewTUITracer(logger)
	sdk := &SDKIntegration{
		provider:     oldProvider,
		providerName: "openai",
		currentModel: "old-model",
		authToken:    "old-token",
		logger:       logger,
		tracer:       tracer,
		agent:        oldAgent,
		providerBuilder: func(string, string, providerBuildDeps) (provider.Provider, string, string, bool, error) {
			return nil, "", "", false, errors.New("invalid replacement credentials")
		},
	}
	metricsStore := metrics.NewMetricsStore(t.TempDir())
	metricsStore.SetCurrentModel("old-model", "Old Model")
	app := &App{
		currentProvider:        "openai",
		currentModel:           "old-model",
		currentProviderDisplay: "OpenAI",
		currentModelDisplay:    "Old Model",
		modelContextWindow:     12345,
		sdk:                    sdk,
		metrics:                metricsStore,
	}
	modelCommand := commands.NewModelCommand()
	modelCommand.SetOnSelect(app.applyModelSelection)

	err = modelCommand.Select("codex", "new-model", "Codex", "New Model")
	if err == nil {
		t.Fatal("expected replacement builder failure")
	}

	config, err := configManager.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.CurrentProvider != "openai" || config.CurrentModel != "old-model" {
		t.Fatalf("config committed rejected selection: %q/%q", config.CurrentProvider, config.CurrentModel)
	}
	commandProvider, commandModel := modelCommand.CurrentSelection()
	if commandProvider != "openai" || commandModel != "old-model" {
		t.Fatalf("command committed rejected selection: %q/%q", commandProvider, commandModel)
	}
	if app.currentProvider != "openai" || app.currentModel != "old-model" ||
		app.currentProviderDisplay != "OpenAI" || app.currentModelDisplay != "Old Model" ||
		app.modelContextWindow != 12345 {
		t.Fatalf("app state committed rejected selection: %+v", app)
	}
	if metricsStore.CurrentModel != "old-model" {
		t.Fatalf("metrics committed rejected model: %q", metricsStore.CurrentModel)
	}
	if sdk.provider != oldProvider || sdk.providerName != "openai" ||
		sdk.currentModel != "old-model" || sdk.authToken != "old-token" {
		t.Fatal("SDK state committed rejected selection")
	}
	if sdk.activeAgent() != oldAgent {
		t.Fatal("active agent identity changed on failed selection")
	}
	definition := oldAgent.Definition()
	if definition.Provider != "openai" || definition.Model != "old-model" {
		t.Fatalf("active agent state changed: %q/%q", definition.Provider, definition.Model)
	}
}
