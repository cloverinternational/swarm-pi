package context

import (
	"strings"
	"testing"
)

func TestStripInjectedContext_NoBlock(t *testing.T) {
	base := "You are a helpful assistant."
	got := StripInjectedContext(base)
	if got != base {
		t.Fatalf("expected base prompt to remain unchanged, got %q", got)
	}
}

func TestInjectContext_Idempotent(t *testing.T) {
	base := "You are a helpful assistant."
	contextBlock := "<context name=\"projectName\">swarm-dot-dev</context>"

	once := InjectContext(base, contextBlock)
	twice := InjectContext(once, contextBlock)

	if once != twice {
		t.Fatalf("expected idempotent injection, got:\nfirst: %q\nsecond: %q", once, twice)
	}
}

func TestInjectContext_AlreadyWrapped(t *testing.T) {
	base := "System prompt base."
	wrapped := ContextStartTag + "\n" + "<context name=\"foo\">bar</context>" + "\n" + ContextEndTag

	got := InjectContext(base, wrapped)
	if !strings.Contains(got, wrapped) {
		t.Fatalf("expected wrapped context to be appended as-is, got %q", got)
	}
}

func TestStripInjectedContext_RemovesBlock(t *testing.T) {
	base := "System prompt base."
	contextBlock := "<context name=\"projectName\">swarm-dot-dev</context>"
	injected := InjectContext(base, contextBlock)

	stripped := StripInjectedContext(injected)
	if stripped != base {
		t.Fatalf("expected stripped prompt to equal base, got %q", stripped)
	}
}

func TestStripInjectedContext_RemovesCachedBlock(t *testing.T) {
	base := "System prompt base."
	cachedBlock := CachedContextStartTag + "\n" + "<context name=\"cached\">value</context>" + "\n" + CachedContextEndTag

	injected := base + "\n\n" + cachedBlock
	stripped := StripInjectedContext(injected)
	if stripped != base {
		t.Fatalf("expected stripped prompt to equal base, got %q", stripped)
	}
}

func TestInjectContextBlocks_OrderCachedThenEphemeral(t *testing.T) {
	base := "System prompt base."
	cachedBlock := "<context name=\"cached\">value</context>"
	ephemeralBlock := "<context name=\"dynamic\">value</context>"

	got := InjectContextBlocks(base, cachedBlock, ephemeralBlock)
	cachedIdx := strings.Index(got, CachedContextStartTag)
	ephemeralIdx := strings.Index(got, ContextStartTag)

	if cachedIdx == -1 || ephemeralIdx == -1 {
		t.Fatalf("expected both cached and ephemeral tags, got %q", got)
	}
	if cachedIdx > ephemeralIdx {
		t.Fatalf("expected cached block before ephemeral block, got %q", got)
	}
}
