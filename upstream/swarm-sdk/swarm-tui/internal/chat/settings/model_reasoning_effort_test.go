package settings

import (
	"slices"
	"testing"

	sdkprovider "github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

func TestModelSettings_MenuOptionIDs_HidesReasoningEffort_WhenUnsupportedSelection(t *testing.T) {
	ms := &ModelSettings{
		currentProvider: "ClaudeCode",
		currentModel:    "claude-opus-4-20250514",
		providers: []commands.Provider{
			{Name: "ClaudeCode", APIType: "anthropic"},
		},
		reasoningEffort: sdkprovider.ReasoningEffortAuto,
	}

	ids := ms.menuOptionIDs()
	for _, id := range ids {
		if id == MenuReasoningEffort {
			t.Fatalf("expected Reasoning Effort menu item to be hidden for anthropic selection")
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 menu items, got %d", len(ids))
	}
}

func TestModelSettings_MenuOptionIDs_ShowsReasoningEffort_ForOpenAIGPT5Selection(t *testing.T) {
	ms := &ModelSettings{
		currentProvider: "OpenAI",
		currentModel:    "gpt-5.1",
		providers: []commands.Provider{
			{Name: "OpenAI", APIType: "openai"},
		},
		reasoningEffort: sdkprovider.ReasoningEffortAuto,
	}

	ids := ms.menuOptionIDs()
	found := slices.Contains(ids, MenuReasoningEffort)
	if !found {
		t.Fatalf("expected Reasoning Effort menu item to be visible for openai gpt-5 selection")
	}
}

func TestModelSettings_HandleMenuKey_EnterCyclesReasoningEffort_WhenSupported(t *testing.T) {
	ms := &ModelSettings{
		state:           "menu",
		currentProvider: "OpenAI",
		currentModel:    "gpt-5.1",
		providers: []commands.Provider{
			{Name: "OpenAI", APIType: "openai"},
		},
		reasoningEffort: sdkprovider.ReasoningEffortAuto,
	}

	ids := ms.menuOptionIDs()
	reasoningPos := -1
	for pos, id := range ids {
		if id == MenuReasoningEffort {
			reasoningPos = pos
			break
		}
	}
	if reasoningPos == -1 {
		t.Fatalf("test setup failed: reasoning effort menu item not found")
	}

	ms.selectedMenuItem = reasoningPos

	if got := ms.GetReasoningEffort(); got != sdkprovider.ReasoningEffortAuto {
		t.Fatalf("expected starting effort auto, got %q", got)
	}

	handled := ms.HandleKey("enter")
	if !handled {
		t.Fatalf("expected enter to be handled")
	}
	if got := ms.GetReasoningEffort(); got == sdkprovider.ReasoningEffortAuto {
		t.Fatalf("expected effort to change, still %q", got)
	}
}

func TestModelSettings_MenuOptionIDs_UsesExplicitCatalogCapability(t *testing.T) {
	supports := true
	ms := &ModelSettings{
		currentProvider: "OpenAI",
		currentModel:    "gpt-4.1",
		providers: []commands.Provider{
			{
				Name:    "OpenAI",
				APIType: "openai",
				Models: []commands.ModelInfo{
					{
						ID:                      "gpt-4.1",
						SupportsReasoningEffort: &supports,
						ReasoningEfforts:        []string{"low", "medium", "high"},
					},
				},
			},
		},
		reasoningEffort: sdkprovider.ReasoningEffortAuto,
	}

	ids := ms.menuOptionIDs()
	found := slices.Contains(ids, MenuReasoningEffort)
	if !found {
		t.Fatalf("expected Reasoning Effort menu item to be visible from explicit catalog capability")
	}
}

func TestModelSettings_MenuOptionIDs_ExplicitDisableOverridesInference(t *testing.T) {
	supports := false
	ms := &ModelSettings{
		currentProvider: "OpenAI",
		currentModel:    "gpt-5.1",
		providers: []commands.Provider{
			{
				Name:    "OpenAI",
				APIType: "openai",
				Models: []commands.ModelInfo{
					{
						ID:                      "gpt-5.1",
						SupportsReasoningEffort: &supports,
					},
				},
			},
		},
		reasoningEffort: sdkprovider.ReasoningEffortAuto,
	}

	ids := ms.menuOptionIDs()
	for _, id := range ids {
		if id == MenuReasoningEffort {
			t.Fatalf("expected Reasoning Effort menu item to be hidden when explicitly disabled")
		}
	}
}
