package chat

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/fallback"
)

// TestResolveReliabilityChainEntriesSkipsKnownNonChatModels reproduces
// Swarm-Code/mono#66: a persisted chat_fallback_chain whose primary is a
// legacy non-chat OpenAI model (e.g. "openai/babbage-002", which this
// exact JSON shape was observed in a live ~/.swarmos/config.json) must
// never be appended as an executable fallback attempt. Previously it was
// appended verbatim, and once the explicit primary model failed for any
// reason, the runtime would try babbage-002 next, which always 400s under
// a ChatGPT-account Codex backend — aborting the entire headless run with
// "all N models failed" instead of surfacing the original model's error.
func TestResolveReliabilityChainEntriesSkipsKnownNonChatModels(t *testing.T) {
	chain := fallback.NewChain("openai", "babbage-002")

	entries := resolveReliabilityChainEntries("claudecode", "claude-sonnet-5", chain)

	if len(entries) != 1 {
		t.Fatalf("expected only the explicit primary entry to survive (poisoned chain has no usable fallback), got %d entries: %+v", len(entries), entries)
	}
	if entries[0].Provider != "claudecode" || entries[0].Model != "claude-sonnet-5" {
		t.Fatalf("expected explicit primary claudecode/claude-sonnet-5 first, got %s/%s", entries[0].Provider, entries[0].Model)
	}
	for _, e := range entries {
		if fallback.IsKnownNonChatModel(e.Model) {
			t.Fatalf("resolved chain entries must never contain a known non-chat model, found %s/%s", e.Provider, e.Model)
		}
	}
}

// TestResolveReliabilityChainEntriesKeepsUsableFallbacksAlongsideNonChatOnes
// verifies that a chain mixing a poisoned entry with legitimate fallbacks
// still yields the legitimate ones — the fix must not throw away good
// fallback entries just because a bad one sits earlier/later in the chain.
func TestResolveReliabilityChainEntriesKeepsUsableFallbacksAlongsideNonChatOnes(t *testing.T) {
	chain := fallback.NewChain("openai", "babbage-002")
	chain.AddFallback("anthropic", "claude-sonnet-4-6")
	chain.AddFallback("openai", "dall-e-3")
	chain.AddFallback("cerebras", "llama-3.3-70b")

	entries := resolveReliabilityChainEntries("claudecode", "claude-sonnet-5", chain)

	want := []fallback.ModelRef{
		{Provider: "claudecode", Model: "claude-sonnet-5"},
		{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		{Provider: "cerebras", Model: "llama-3.3-70b"},
	}
	if len(entries) != len(want) {
		t.Fatalf("expected %d entries, got %d: %+v", len(want), len(entries), entries)
	}
	for i, w := range want {
		if entries[i].Provider != w.Provider || entries[i].Model != w.Model {
			t.Fatalf("entry %d: expected %s/%s, got %s/%s", i, w.Provider, w.Model, entries[i].Provider, entries[i].Model)
		}
	}
}

// TestResolveReliabilityChainEntriesExplicitPrimaryIsNeverFiltered ensures
// an explicitly requested model (e.g. via -P/-m flags) is always tried as
// the primary attempt even if it happens to match the non-chat-model
// denylist — filtering only applies to entries pulled from the persisted
// fallback chain, never to the caller's own explicit request.
func TestResolveReliabilityChainEntriesExplicitPrimaryIsNeverFiltered(t *testing.T) {
	entries := resolveReliabilityChainEntries("openai", "babbage-002", fallback.NewChain("anthropic", "claude-sonnet-4-6"))

	if len(entries) != 2 {
		t.Fatalf("expected explicit primary + one usable fallback, got %d: %+v", len(entries), entries)
	}
	if entries[0].Provider != "openai" || entries[0].Model != "babbage-002" {
		t.Fatalf("explicit caller-requested model must never be filtered, got %s/%s", entries[0].Provider, entries[0].Model)
	}
}
