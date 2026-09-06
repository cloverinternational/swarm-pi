package builtin

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

func TestGoalEvaluationTranscriptIncludesToolEvidence(t *testing.T) {
	messages := goalTranscriptSmokeMessages()

	legacy := "User: " + messages[0].Content + "\n\nAssistant: " + messages[len(messages)-1].Content
	if strings.Contains(legacy, "ok github.com") {
		t.Fatal("smoke setup invalid: legacy transcript unexpectedly contains tool evidence")
	}

	got := BuildGoalEvaluationTranscript(messages, "fallback prompt", "fallback response")
	for _, want := range []string{
		"Assistant tool call: Bash",
		"go test ./internal/tools/forge",
		"Tool result (Bash, succeeded):",
		"ok github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge",
		"Run the targeted test.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("evaluator transcript missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, strings.Repeat("runtime guidance ", 1000)) {
		t.Fatal("oversized injected user guidance was not truncated")
	}
}

func TestGoalEvaluationTranscriptSkipsSyntheticUserBoundaryAndCapsTotal(t *testing.T) {
	messages := goalTranscriptSmokeMessages()
	base := BuildGoalEvaluationTranscript(messages, "Run the targeted test.", "")
	if !strings.Contains(base, "Assistant tool call: Bash") {
		t.Fatal("synthetic user-role hook context incorrectly became the turn boundary")
	}

	huge := strings.Repeat("passing evidence ", 10000)
	for i := 0; i < 10; i++ {
		messages = append(messages[:len(messages)-1],
			&conversation.Message{
				Role: conversation.RoleTool,
				ToolResults: []conversation.ToolResult{{
					Name: "Bash", Output: huge,
				}},
			},
			messages[len(messages)-1],
		)
	}

	got := BuildGoalEvaluationTranscript(messages, "Run the targeted test.", "")
	if len(got) > goalTranscriptTotalChars+100 {
		t.Fatalf("transcript exceeded total budget: got %d", len(got))
	}
	if !strings.Contains(got, "The targeted test passes.") {
		t.Fatal("total-budget truncation discarded the latest completion evidence")
	}
}

// TestGoalEvaluationTranscriptExistingConversation is an opt-in smoke for a
// real persisted conversation:
//
//	SWARM_GOAL_SMOKE_CONVERSATION=/path/to/conversation.json \
//	  go test ./internal/hooks/builtin -run ExistingConversation -v
func TestGoalEvaluationTranscriptExistingConversation(t *testing.T) {
	path := os.Getenv("SWARM_GOAL_SMOKE_CONVERSATION")
	if path == "" {
		t.Skip("set SWARM_GOAL_SMOKE_CONVERSATION to a persisted conversation JSON file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Messages []*conversation.Message `json:"messages"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}

	turn := mostRecentEvidenceTurn(stored.Messages)
	if len(turn) == 0 {
		t.Fatal("conversation has no completed turn containing tool results")
	}
	legacy := "User: " + turn[0].Content + "\n\nAssistant: " + turn[len(turn)-1].Content
	got := BuildGoalEvaluationTranscript(turn, "", "")
	if strings.Contains(legacy, "Tool result (") {
		t.Fatal("legacy prompt/response unexpectedly rendered structured tool results")
	}
	if !strings.Contains(got, "Tool result (") {
		t.Fatalf("fixed transcript omitted persisted tool results:\n%s", got)
	}
	t.Logf("legacy evaluator input: %d chars, structured tool evidence visible=false", len(legacy))
	t.Logf("fixed evaluator input: %d chars, structured tool evidence visible=true", len(got))
}

func mostRecentEvidenceTurn(messages []*conversation.Message) []*conversation.Message {
	end := len(messages)
	for end > 0 {
		start := -1
		for i := end - 1; i >= 0; i-- {
			if messages[i] != nil && messages[i].Role == conversation.RoleUser {
				start = i
				break
			}
		}
		if start < 0 {
			return nil
		}
		for _, msg := range messages[start:end] {
			if msg != nil && len(msg.ToolResults) > 0 {
				return messages[start:end]
			}
		}
		end = start
	}
	return nil
}

func goalTranscriptSmokeMessages() []*conversation.Message {
	return []*conversation.Message{
		{Role: conversation.RoleUser, Content: "Run the targeted test."},
		{
			Role:    conversation.RoleUser,
			Content: strings.Repeat("runtime guidance ", 1000),
			Metadata: map[string]any{
				"type": "hook_context", "is_hook_context": true,
			},
		},
		{
			Role: conversation.RoleAssistant,
			ToolCalls: []conversation.ToolCall{{
				ID: "call-1", Name: "Bash",
				Parameters: map[string]any{"command": "go test ./internal/tools/forge"},
			}},
		},
		{
			Role: conversation.RoleTool,
			ToolResults: []conversation.ToolResult{{
				CallID: "call-1", Name: "Bash",
				Output: "ok github.com/Swarm-Code/mono/swarm-sdk/internal/tools/forge 0.123s",
			}},
		},
		{Role: conversation.RoleAssistant, Content: "The targeted test passes."},
	}
}
