package settings

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
	"github.com/charmbracelet/x/ansi"
)

func TestModelGenerationFormAdjustments(t *testing.T) {
	ms := &ModelSettings{}

	ms.modelFormField = modelFieldTemperature
	ms.handleEditModelKey("right")
	if ms.modelFormTemperature == nil || *ms.modelFormTemperature != 0 {
		t.Fatalf("temperature Auto -> explicit zero failed: %#v", ms.modelFormTemperature)
	}
	ms.handleEditModelKey("right")
	if *ms.modelFormTemperature != 0.1 {
		t.Fatalf("temperature increment = %v, want 0.1", *ms.modelFormTemperature)
	}
	ms.handleEditModelKey("left")
	ms.handleEditModelKey("left")
	if ms.modelFormTemperature != nil {
		t.Fatalf("temperature should clear to Auto: %v", *ms.modelFormTemperature)
	}

	ms.modelFormField = modelFieldTopP
	for range 30 {
		ms.handleEditModelKey("right")
	}
	if ms.modelFormTopP == nil || *ms.modelFormTopP != 1 {
		t.Fatalf("top_p should clamp to 1: %#v", ms.modelFormTopP)
	}

	ms.modelFormField = modelFieldTopK
	ms.handleEditModelKey("right")
	if ms.modelFormTopK == nil || *ms.modelFormTopK != 0 {
		t.Fatalf("top_k Auto -> explicit zero failed: %#v", ms.modelFormTopK)
	}
	ms.handleEditModelKey("left")
	if ms.modelFormTopK != nil {
		t.Fatalf("top_k should clear to Auto: %v", *ms.modelFormTopK)
	}

	ms.modelFormField = modelFieldMaxTokens
	ms.handleEditModelKey("right")
	if ms.modelFormMaxTokens != 1024 {
		t.Fatalf("max tokens increment = %d, want 1024", ms.modelFormMaxTokens)
	}
	ms.handleEditModelKey("left")
	if ms.modelFormMaxTokens != 0 {
		t.Fatalf("max tokens should return to inherited: %d", ms.modelFormMaxTokens)
	}
}

func TestEditModelGenerationSettingsPersistAndRender(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	zero := 0.0
	zeroK := 0
	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.SaveProviders([]commands.ProviderConfig{{
		Name: "local", Type: "api_key", APIType: "openai-compatible", Available: true,
		Models: []commands.ModelConfig{{
			ID: "model", DisplayName: "Model", Temperature: &zero, MaxTokens: 4096,
			TopP: &zero, TopK: &zeroK, Reasoning: true, ThinkingEffort: "high",
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	ms := NewModelSettings("local", "model")
	ms.SetConfigManager(cm)
	providerIndex, modelIndex := -1, -1
	for i, provider := range ms.providers {
		if provider.Name == "local" {
			providerIndex = i
			for j, model := range provider.Models {
				if model.ID == "model" {
					modelIndex = j
				}
			}
		}
	}
	if providerIndex < 0 || modelIndex < 0 {
		t.Fatal("seed model not loaded")
	}
	ms.selectedProvider, ms.editingModelIndex = providerIndex, modelIndex
	ms.loadModelToForm(providerIndex, modelIndex)
	if ms.modelFormTemperature == nil || *ms.modelFormTemperature != 0 ||
		ms.modelFormTopP == nil || *ms.modelFormTopP != 0 ||
		ms.modelFormTopK == nil || *ms.modelFormTopK != 0 ||
		ms.modelFormMaxTokens != 4096 {
		t.Fatal("form did not load explicit-zero overrides")
	}
	if rendered := strings.Join(ms.renderGenerationOverrideFields(60, testSettingsTheme()), "\n"); !strings.Contains(rendered, "Temperature") {
		t.Fatalf("generation controls missing from render: %q", rendered)
	}
	ms.modelFormField = modelFieldTemperature
	ms.handleEditModelKey("right")
	ms.saveEditedModel()
	providers, err := cm.LoadProviders()
	if err != nil {
		t.Fatal(err)
	}
	for _, provider := range providers {
		if provider.Name == "local" {
			model := provider.Models[0]
			if model.Temperature == nil || *model.Temperature != 0.1 || model.MaxTokens != 4096 {
				t.Fatalf("overrides not persisted: %#v", model)
			}
			if !model.Reasoning || model.ThinkingEffort != "high" {
				t.Fatalf("reasoning metadata not preserved: %#v", model)
			}
			return
		}
	}
	t.Fatal("persisted provider missing")
}

func TestGenerationCapabilitiesMatchConcreteTranslators(t *testing.T) {
	ms := &ModelSettings{
		selectedProvider: 0,
		providers: []commands.Provider{{
			Name:    "OpenRouter",
			APIType: "openai-compatible",
		}},
	}
	if caps := ms.generationCapabilities(); caps.topK {
		t.Fatal("OpenRouter top_k must stay disabled until its translator sends it")
	}
	ms.providers[0] = commands.Provider{Name: "Anthropic", APIType: "anthropic"}
	if caps := ms.generationCapabilities(); !caps.temperature || caps.temperatureMax != 1 || !caps.topK {
		t.Fatalf("unexpected Anthropic capabilities: %+v", caps)
	}
	ms.providers[0] = commands.Provider{Name: "Codex", APIType: "openai"}
	if caps := ms.generationCapabilities(); caps.temperature || caps.maxTokens || caps.topP || caps.topK {
		t.Fatalf("Codex exposes ignored controls: %+v", caps)
	}
}

func TestFitModelEditorPanelKeepsFocusedRowVisible(t *testing.T) {
	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "row"
	}
	lines[18] = "▶ selected"
	fitted := fitModelEditorPanel(lines, 5, false)
	if len(fitted) != 5 || !strings.Contains(strings.Join(fitted, "\n"), "▶ selected") {
		t.Fatalf("focused row not visible: %#v", fitted)
	}
	fitted = fitModelEditorPanel(lines, 5, true)
	if len(fitted) != 5 || fitted[len(fitted)-1] != lines[len(lines)-1] {
		t.Fatalf("end-aligned Save window incorrect: %#v", fitted)
	}
}

func TestCompactModelEditorFitsWideAndNarrowSettingsPanes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "wide 120x40 terminal content", width: 88, height: 38},
		{name: "narrow 90x28 terminal content", width: 58, height: 26},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ms := &ModelSettings{
				selectedProvider: 0,
				providers: []commands.Provider{{
					Name:    "Anthropic",
					APIType: "anthropic",
				}},
				modelFormID:              "claude-opus-4-6",
				modelFormDisplayName:     "Claude Opus 4.6",
				modelFormThinkingEnabled: true,
				modelFormThinkingBudget:  8192,
				modelFormThinkingEffort:  "high",
				modelFormField:           modelFieldTopK,
			}
			rendered := ms.renderEditModel(tc.width, tc.height, testSettingsTheme())
			plain := ansi.Strip(rendered)
			for _, want := range []string{
				"EDIT MODEL",
				"IDENTITY",
				"REASONING",
				"GENERATION",
				"Model ID",
				"Display name",
				"Context window",
				"Extended thinking",
				"Thinking budget",
				"Reasoning effort",
				"Diffusion rendering",
				"Temperature",
				"Max output tokens",
				"Top P",
				"Top K",
				"Save changes",
				"▶ Top K",
				"Enter/→ +",
			} {
				if !strings.Contains(plain, want) {
					t.Fatalf("missing %q in:\n%s", want, plain)
				}
			}
			if strings.Contains(plain, "claude-opus-4-\n6") {
				t.Fatalf("model identifier split across lines:\n%s", plain)
			}
			if lines := strings.Split(plain, "\n"); len(lines) > tc.height {
				t.Fatalf("rendered %d lines at height %d:\n%s", len(lines), tc.height, plain)
			} else {
				for i, line := range lines {
					if got := ansi.StringWidth(line); got > tc.width {
						t.Fatalf("line %d width=%d exceeds %d: %q", i, got, tc.width, line)
					}
				}
			}
		})
	}
}
