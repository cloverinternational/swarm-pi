package settings

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/commands"
)

// TestFallbackPickerProviderSelectSkipsNonChatDefaultModel reproduces the
// picker-side half of Swarm-Code/mono#66: OpenAI's raw model catalog is an
// unfiltered listing (legacy completion, image, audio, embedding, and
// moderation models included) sorted alphabetically, so "babbage-002" sorts
// first. Selecting a provider that has no "haiku/flash/mini/lite" model
// used to leave selectedModelIdx at raw index 0 — silently defaulting to a
// model that always 400s on a chat-completion request. The default must
// skip known non-chat models even when no lightweight-model keyword matches.
func TestFallbackPickerProviderSelectSkipsNonChatDefaultModel(t *testing.T) {
	picker := &FallbackPicker{
		chain: fallback.NewChainWithDefaults(),
		providers: []commands.ProviderConfig{
			{
				Name: "OpenAI",
				Models: []commands.ModelConfig{
					{ID: "babbage-002"},
					{ID: "chatgpt-4o-latest", ContextWindow: 128000},
					{ID: "computer-use-preview"},
					{ID: "dall-e-2"},
					{ID: "dall-e-3"},
					{ID: "davinci-002"},
					{ID: "gpt-3.5-turbo", ContextWindow: 16385},
				},
			},
		},
		state:        "select_provider",
		editingIndex: 0,
	}

	if !picker.handleProviderKey("enter") {
		t.Fatalf("expected handleProviderKey(enter) to be handled")
	}

	selected := picker.providers[picker.selectedProviderIdx].Models[picker.selectedModelIdx]
	if fallback.IsKnownNonChatModel(selected.ID) {
		t.Fatalf("provider-select default must never land on a known non-chat model, got %q", selected.ID)
	}
	if selected.ID != "chatgpt-4o-latest" {
		t.Fatalf("expected first chat-capable model 'chatgpt-4o-latest' (no haiku/flash/mini/lite match available), got %q", selected.ID)
	}
}

// TestFallbackPickerProviderSelectStillPrefersLightweightModel verifies the
// non-chat-model skip doesn't regress the existing "prefer haiku/flash/mini/lite"
// recommendation when a genuinely chat-capable lightweight model exists.
func TestFallbackPickerProviderSelectStillPrefersLightweightModel(t *testing.T) {
	picker := &FallbackPicker{
		chain: fallback.NewChainWithDefaults(),
		providers: []commands.ProviderConfig{
			{
				Name: "OpenAI",
				Models: []commands.ModelConfig{
					{ID: "babbage-002"},
					{ID: "gpt-4o", ContextWindow: 128000},
					{ID: "gpt-4o-mini", ContextWindow: 128000},
					{ID: "gpt-5.1", ContextWindow: 200000},
				},
			},
		},
		state:        "select_provider",
		editingIndex: 0,
	}

	if !picker.handleProviderKey("enter") {
		t.Fatalf("expected handleProviderKey(enter) to be handled")
	}

	selected := picker.providers[picker.selectedProviderIdx].Models[picker.selectedModelIdx]
	if selected.ID != "gpt-4o-mini" {
		t.Fatalf("expected lightweight model 'gpt-4o-mini' to still be preferred, got %q", selected.ID)
	}
}
