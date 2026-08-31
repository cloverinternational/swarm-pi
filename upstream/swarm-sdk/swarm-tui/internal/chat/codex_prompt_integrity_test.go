package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestIsCodexBackedRequest(t *testing.T) {
	if !isCodexBackedRequest("openai", "gpt-5.2-codex", nil) {
		t.Fatalf("expected codex model id to be treated as codex-backed")
	}
	if isCodexBackedRequest("anthropic", "claude-sonnet-4-20250514", nil) {
		t.Fatalf("did not expect anthropic model to be treated as codex-backed")
	}
}

func TestUpsertSwarmRuntimeGuidanceSection_ReplacesSectionIdempotently(t *testing.T) {
	content := "Write a hello world program."
	content = upsertSwarmRuntimeGuidanceSection(content, "mode", "MODE_ONE")
	content = upsertSwarmRuntimeGuidanceSection(content, "context", "CTX_ONE")
	content = upsertSwarmRuntimeGuidanceSection(content, "context", "CTX_TWO")

	if strings.Count(content, "<swarm_runtime_context>") != 1 {
		t.Fatalf("expected single context section after upsert, got content:\n%s", content)
	}
	if !strings.Contains(content, "CTX_TWO") || strings.Contains(content, "CTX_ONE") {
		t.Fatalf("expected updated context section, got content:\n%s", content)
	}
	if !strings.Contains(content, "<swarm_runtime_mode>") || !strings.Contains(content, "MODE_ONE") {
		t.Fatalf("expected other sections to be preserved, got content:\n%s", content)
	}
}

func TestUpsertSwarmRuntimeGuidanceSectionOnLatestUserMessage(t *testing.T) {
	messages := []*conversation.Message{
		{Role: conversation.RoleUser, Content: "Earlier user"},
		{Role: conversation.RoleAssistant, Content: "Assistant"},
		{Role: conversation.RoleUser, Content: "Latest user"},
	}

	updated := upsertSwarmRuntimeGuidanceSectionOnLatestUserMessage(messages, "skills", "SKILL_BLOCK")
	if len(updated) != len(messages) {
		t.Fatalf("expected message count to remain unchanged")
	}
	if updated[0].Content != "Earlier user" {
		t.Fatalf("expected earlier user message unchanged")
	}
	if !strings.Contains(updated[2].Content, "SKILL_BLOCK") {
		t.Fatalf("expected latest user message to include runtime guidance")
	}
}
