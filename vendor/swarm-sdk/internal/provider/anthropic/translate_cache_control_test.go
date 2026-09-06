package anthropic

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestTranslateRequest_SystemCacheControlSplitNonOAuth(t *testing.T) {
	basePrompt := "Base instructions."
	cachedContext := "<swarmos_cached_context>\nCached context line\n</swarmos_cached_context>"
	dynamicContext := "<swarmos_context>\nDynamic context line\n</swarmos_context>"
	systemPrompt := basePrompt + "\n\n" + cachedContext + "\n\n" + dynamicContext

	req := provider.ChatRequest{
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
		SystemPrompt: systemPrompt,
		Metadata: map[string]any{
			"system_cache_control": map[string]string{
				"type": "ephemeral",
				"ttl":  "5m",
			},
		},
	}

	msg, err := TranslateRequest(req)
	if err != nil {
		t.Fatalf("TranslateRequest returned error: %v", err)
	}

	blocks, ok := msg.System.([]SystemBlock)
	if !ok {
		t.Fatalf("expected msg.System to be []SystemBlock, got %T", msg.System)
	}
	if len(blocks) != 3 {
		t.Fatalf("expected 3 system blocks, got %d", len(blocks))
	}

	if blocks[0].Text != basePrompt {
		t.Fatalf("expected base prompt %q, got %q", basePrompt, blocks[0].Text)
	}
	if blocks[0].CacheControl == nil {
		t.Fatalf("expected cache control on base prompt")
	}
	if blocks[0].CacheControl.Type != "ephemeral" || blocks[0].CacheControl.TTL != "5m" {
		t.Fatalf("unexpected cache control on base: %+v", blocks[0].CacheControl)
	}

	expectedCached := "Cached context line"
	if blocks[1].Text != expectedCached {
		t.Fatalf("expected cached context %q, got %q", expectedCached, blocks[1].Text)
	}
	if blocks[1].CacheControl == nil {
		t.Fatalf("expected cache control on cached context")
	}
	if blocks[1].CacheControl.Type != "ephemeral" || blocks[1].CacheControl.TTL != "5m" {
		t.Fatalf("unexpected cache control on cached: %+v", blocks[1].CacheControl)
	}

	expectedDynamic := "Dynamic context line"
	if blocks[2].Text != expectedDynamic {
		t.Fatalf("expected dynamic context %q, got %q", expectedDynamic, blocks[2].Text)
	}
	if blocks[2].CacheControl != nil {
		t.Fatalf("expected no cache control on dynamic context")
	}
}

func TestTranslateRequest_SystemCacheControlSplitOAuth(t *testing.T) {
	oauthPrefix := GetCLISystemPromptPrefix()
	basePrompt := "Base instructions."
	cachedContext := "<swarmos_cached_context>\nCached context line\n</swarmos_cached_context>"
	dynamicContext := "<swarmos_context>\nDynamic context line\n</swarmos_context>"
	systemPrompt := oauthPrefix + "\n\n" + basePrompt + "\n\n" + cachedContext + "\n\n" + dynamicContext

	req := provider.ChatRequest{
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
		SystemPrompt: systemPrompt,
		Metadata: map[string]any{
			"system_cache_control": map[string]string{
				"type": "ephemeral",
				"ttl":  "5m",
			},
		},
	}

	msg, _, err := TranslateRequestOAuth(req)
	if err != nil {
		t.Fatalf("TranslateRequestOAuth returned error: %v", err)
	}

	blocks, ok := msg.System.([]SystemBlock)
	if !ok {
		t.Fatalf("expected msg.System to be []SystemBlock, got %T", msg.System)
	}
	if len(blocks) != 4 {
		t.Fatalf("expected 4 system blocks, got %d", len(blocks))
	}

	if blocks[0].Text != oauthPrefix {
		t.Fatalf("expected oauth prefix block %q, got %q", oauthPrefix, blocks[0].Text)
	}
	if blocks[0].CacheControl != nil {
		t.Fatalf("expected no cache control on oauth prefix")
	}

	if blocks[1].Text != basePrompt {
		t.Fatalf("expected base prompt %q, got %q", basePrompt, blocks[1].Text)
	}
	if blocks[1].CacheControl == nil {
		t.Fatalf("expected cache control on base prompt")
	}
	if blocks[1].CacheControl.Type != "ephemeral" || blocks[1].CacheControl.TTL != "5m" {
		t.Fatalf("unexpected cache control on base: %+v", blocks[1].CacheControl)
	}

	expectedCached := "Cached context line"
	if blocks[2].Text != expectedCached {
		t.Fatalf("expected cached context %q, got %q", expectedCached, blocks[2].Text)
	}
	if blocks[2].CacheControl == nil {
		t.Fatalf("expected cache control on cached context")
	}
	if blocks[2].CacheControl.Type != "ephemeral" || blocks[2].CacheControl.TTL != "5m" {
		t.Fatalf("unexpected cache control on cached: %+v", blocks[2].CacheControl)
	}

	expectedDynamic := "Dynamic context line"
	if blocks[3].Text != expectedDynamic {
		t.Fatalf("expected dynamic context %q, got %q", expectedDynamic, blocks[3].Text)
	}
	if blocks[3].CacheControl != nil {
		t.Fatalf("expected no cache control on dynamic context")
	}
}

// TestTranslateRequest_OAuthIdentityBlockStandaloneExact guards the critical
// OAuth invariant: when an OAuth request's system prompt carries the Claude Code
// identity prefix (concatenated upstream as "<prefix>\n\n<rest>"), the first
// system block emitted to the Anthropic API must be EXACTLY the identity prefix
// and nothing else. The Claude OAuth gateway rejects (with a misleading HTTP
// 429) any request whose first system text block is not byte-for-byte the
// identity string, so the prefix must NEVER be concatenated with other system
// text into a single block. This test asserts the prefix is its own standalone,
// exact first block and that the remaining instructions land in a separate
// subsequent block.
func TestTranslateRequest_OAuthIdentityBlockStandaloneExact(t *testing.T) {
	oauthPrefix := GetCLISystemPromptPrefix()
	rest := "You are an expert Go engineer. Be concise."
	// This is exactly how chat.go / stream.go hand the prompt to translateRequest.
	systemPrompt := oauthPrefix + "\n\n" + rest

	req := provider.ChatRequest{
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
		SystemPrompt: systemPrompt,
	}

	msg, _, err := TranslateRequestOAuth(req)
	if err != nil {
		t.Fatalf("TranslateRequestOAuth returned error: %v", err)
	}

	blocks, ok := msg.System.([]SystemBlock)
	if !ok {
		t.Fatalf("expected msg.System to be []SystemBlock, got %T", msg.System)
	}
	if len(blocks) < 2 {
		t.Fatalf("expected at least 2 system blocks (identity + rest), got %d", len(blocks))
	}

	// Block 0 MUST be the identity prefix, exactly — no concatenation, no
	// leading/trailing additions.
	if blocks[0].Text != oauthPrefix {
		t.Fatalf("first system block must be EXACTLY the identity prefix.\n got: %q\nwant: %q", blocks[0].Text, oauthPrefix)
	}

	// The rest of the prompt must NOT be merged into the identity block.
	if blocks[1].Text == oauthPrefix {
		t.Fatalf("second block unexpectedly equals the identity prefix; rest-of-prompt was lost")
	}
	if blocks[1].Text != rest {
		t.Fatalf("second system block should carry the remaining instructions.\n got: %q\nwant: %q", blocks[1].Text, rest)
	}

	// Defensive: the identity block must never contain the rest-of-prompt text.
	if strings.Contains(blocks[0].Text, rest) {
		t.Fatalf("identity block must not contain other system text, got: %q", blocks[0].Text)
	}
}

// TestTranslateRequest_OAuthIdentityOnlyExact covers the bare case where the
// OAuth system prompt is just the identity prefix with no additional
// instructions: the single emitted system block must still be exactly the
// identity string.
func TestTranslateRequest_OAuthIdentityOnlyExact(t *testing.T) {
	oauthPrefix := GetCLISystemPromptPrefix()

	req := provider.ChatRequest{
		Messages: []*conversation.Message{
			{Role: conversation.RoleUser, Content: "hi"},
		},
		SystemPrompt: oauthPrefix,
	}

	msg, _, err := TranslateRequestOAuth(req)
	if err != nil {
		t.Fatalf("TranslateRequestOAuth returned error: %v", err)
	}

	blocks, ok := msg.System.([]SystemBlock)
	if !ok {
		t.Fatalf("expected msg.System to be []SystemBlock, got %T", msg.System)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected exactly 1 system block for identity-only prompt, got %d", len(blocks))
	}
	if blocks[0].Text != oauthPrefix {
		t.Fatalf("identity-only system block must be EXACTLY the prefix.\n got: %q\nwant: %q", blocks[0].Text, oauthPrefix)
	}
}
