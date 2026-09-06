package settings

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// TestSaveEditedModelPersistsContextWindow reproduces the "context window edit
// doesn't stick" bug: editing a model's context in the edit-model screen must
// persist BOTH the context string and the parsed context_window int to
// providers.json — the runtime prefers the int (sdk_integration_provider.go),
// so a stale context_window would shadow the edited string.
func TestSaveEditedModelPersistsContextWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}

	// Seed providers.json with a provider whose model has an OLD context window.
	seed := []commands.ProviderConfig{{
		Name:        "local",
		DisplayName: "Local",
		Type:        "api_key",
		APIType:     "openai-compatible",
		BaseURL:     "http://localhost:8082/v1",
		Available:   true,
		Models: []commands.ModelConfig{{
			ID:            "nvidia/diffusiongemma-26B-A4B-it-NVFP4",
			DisplayName:   "DiffusionGemma",
			Context:       "31000",
			ContextWindow: 31000,
		}},
	}}
	if err := cm.SaveProviders(seed); err != nil {
		t.Fatalf("seed providers: %v", err)
	}

	ms := NewModelSettings("local", "nvidia/diffusiongemma-26B-A4B-it-NVFP4")
	ms.SetConfigManager(cm)

	// Locate the seeded provider/model in the loaded list (defaults may be
	// injected around it by the self-heal migration).
	provIdx, modelIdx := -1, -1
	for i, p := range ms.providers {
		if p.Name == "local" {
			provIdx = i
			for j, mdl := range p.Models {
				if mdl.ID == "nvidia/diffusiongemma-26B-A4B-it-NVFP4" {
					modelIdx = j
				}
			}
		}
	}
	if provIdx < 0 || modelIdx < 0 {
		t.Fatalf("seeded provider/model not loaded (provIdx=%d modelIdx=%d)", provIdx, modelIdx)
	}

	// Simulate the edit-model flow: load form, change the context, save.
	ms.selectedProvider = provIdx
	ms.editingModelIndex = modelIdx
	ms.loadModelToForm(provIdx, modelIdx)
	ms.modelFormContext = "262144"
	ms.saveEditedModel()

	// The edit must survive a fresh load from disk.
	persisted, err := cm.LoadProviders()
	if err != nil {
		t.Fatalf("reload providers: %v", err)
	}
	for _, p := range persisted {
		if p.Name != "local" {
			continue
		}
		for _, mdl := range p.Models {
			if mdl.ID != "nvidia/diffusiongemma-26B-A4B-it-NVFP4" {
				continue
			}
			if mdl.Context != "262144" {
				t.Errorf("context string not persisted: got %q, want %q", mdl.Context, "262144")
			}
			if mdl.ContextWindow != 262144 {
				t.Errorf("context_window int not persisted: got %d, want %d (stale int shadows the string at runtime)",
					mdl.ContextWindow, 262144)
			}
			return
		}
		t.Fatal("model vanished from persisted provider")
	}
	t.Fatal("provider vanished from persisted config")
}

// TestSaveEditedModelInMemoryContextWindow verifies the in-memory model list
// keeps a usable parsed ContextWindow after an edit (it was hardcoded to 0).
func TestSaveEditedModelInMemoryContextWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cm, err := commands.NewConfigManager()
	if err != nil {
		t.Fatalf("config manager: %v", err)
	}
	seed := []commands.ProviderConfig{{
		Name:      "local",
		Type:      "api_key",
		APIType:   "openai-compatible",
		Available: true,
		Models: []commands.ModelConfig{{
			ID:            "test-model",
			DisplayName:   "Test Model",
			Context:       "32k",
			ContextWindow: 32000,
		}},
	}}
	if err := cm.SaveProviders(seed); err != nil {
		t.Fatalf("seed providers: %v", err)
	}

	ms := NewModelSettings("local", "test-model")
	ms.SetConfigManager(cm)

	provIdx, modelIdx := -1, -1
	for i, p := range ms.providers {
		if p.Name == "local" {
			provIdx = i
			for j, mdl := range p.Models {
				if mdl.ID == "test-model" {
					modelIdx = j
				}
			}
		}
	}
	if provIdx < 0 || modelIdx < 0 {
		t.Fatal("seeded provider/model not loaded")
	}

	ms.selectedProvider = provIdx
	ms.editingModelIndex = modelIdx
	ms.loadModelToForm(provIdx, modelIdx)
	ms.modelFormContext = "128k"
	ms.saveEditedModel()

	got := ms.providers[provIdx].Models[modelIdx]
	if got.Context != "128k" {
		t.Errorf("in-memory context string: got %q, want %q", got.Context, "128k")
	}
	if got.ContextWindow != 128000 {
		t.Errorf("in-memory context window: got %d, want %d", got.ContextWindow, 128000)
	}
}
