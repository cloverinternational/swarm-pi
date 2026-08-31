package fallback

import "testing"

func TestIsKnownNonChatModel(t *testing.T) {
	nonChat := []string{
		"babbage-002",
		"BABBAGE-002",
		"davinci-002",
		"dall-e-2",
		"dall-e-3",
		"whisper-1",
		"tts-1",
		"tts-1-hd",
		"text-embedding-3-small",
		"text-moderation-latest",
		"omni-moderation-latest",
		"computer-use-preview",
		"computer-use-preview-2025-03-11",
		"text-davinci-003",
		"code-davinci-002",
	}
	for _, m := range nonChat {
		if !IsKnownNonChatModel(m) {
			t.Errorf("expected %q to be classified as a non-chat model", m)
		}
	}

	chatCapable := []string{
		"gpt-4o",
		"gpt-4o-mini",
		"gpt-5.1",
		"gpt-3.5-turbo",
		"claude-sonnet-4-6",
		"claude-haiku-4-5-20251001",
		"gemini-3-pro-preview",
		"llama-3.3-70b",
		"",
	}
	for _, m := range chatCapable {
		if IsKnownNonChatModel(m) {
			t.Errorf("expected %q to NOT be classified as a non-chat model", m)
		}
	}
}

func TestSanitizeChatChainDropsNonChatPrimaryWithNoFallbacks(t *testing.T) {
	// Reproduces the exact shape observed in a live, broken
	// ~/.swarmos/config.json (Swarm-Code/mono#66): a chat_fallback_chain
	// whose only entry is a legacy non-chat OpenAI model.
	chain := NewChain("openai", "babbage-002")

	if got := SanitizeChatChain(chain); got != nil {
		t.Fatalf("expected nil when every entry is a known non-chat model, got %+v", got)
	}
}

func TestSanitizeChatChainPromotesFirstUsableFallbackToPrimary(t *testing.T) {
	chain := NewChain("openai", "babbage-002")
	chain.AddFallback("openai", "dall-e-3")
	chain.AddFallback("anthropic", "claude-sonnet-4-6")
	chain.AddFallback("cerebras", "llama-3.3-70b")

	got := SanitizeChatChain(chain)
	if got == nil {
		t.Fatalf("expected a sanitized chain, got nil")
	}
	if got.Primary.Provider != "anthropic" || got.Primary.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected first usable fallback promoted to primary, got %s/%s", got.Primary.Provider, got.Primary.Model)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0].Provider != "cerebras" || got.Fallbacks[0].Model != "llama-3.3-70b" {
		t.Fatalf("expected remaining usable fallback preserved, got %+v", got.Fallbacks)
	}
}

func TestSanitizeChatChainLeavesCleanChainUntouched(t *testing.T) {
	chain := NewChain("anthropic", "claude-sonnet-4-6")
	chain.AddFallback("cerebras", "llama-3.3-70b")

	got := SanitizeChatChain(chain)
	if got == nil {
		t.Fatalf("expected chain to survive sanitization, got nil")
	}
	if got.Primary.Provider != "anthropic" || got.Primary.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected primary unchanged, got %s/%s", got.Primary.Provider, got.Primary.Model)
	}
	if len(got.Fallbacks) != 1 || got.Fallbacks[0].Model != "llama-3.3-70b" {
		t.Fatalf("expected fallback unchanged, got %+v", got.Fallbacks)
	}
}

func TestSanitizeChatChainNilAndEmptyChain(t *testing.T) {
	if got := SanitizeChatChain(nil); got != nil {
		t.Fatalf("expected nil in, nil out, got %+v", got)
	}
	// An already-empty chain (no primary set) is returned unchanged — callers
	// are expected to check IsEmpty() themselves before/after sanitizing, the
	// same way GetChatFallbackChain does. SanitizeChatChain only needs to
	// guarantee it never turns a non-empty chain into a poisoned one.
	empty := &Chain{}
	if got := SanitizeChatChain(empty); got != empty {
		t.Fatalf("expected an already-empty chain to be returned as-is, got %+v", got)
	}
}
