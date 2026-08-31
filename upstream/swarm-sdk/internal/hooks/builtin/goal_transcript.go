package builtin

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

const (
	goalTranscriptUserChars   = 8000
	goalTranscriptOutputChars = 12000
	goalTranscriptParamsChars = 4000
	goalTranscriptTotalChars  = 64000
)

// BuildGoalEvaluationTranscript renders the most recent persisted turn,
// including structured tool calls/results that have empty message Content.
// Large injected runtime guidance is tail-truncated so it cannot crowd the
// execution evidence out of the evaluator request.
func BuildGoalEvaluationTranscript(messages []*conversation.Message, fallbackPrompt, fallbackResponse string) string {
	start := -1
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil || msg.Role != conversation.RoleUser || isGoalHookContext(msg) {
			continue
		}
		if start < 0 {
			start = i
		}
		// Prefer the actual submitted prompt over synthetic user-role context.
		if msg.Content == fallbackPrompt || strings.HasPrefix(msg.Content, fallbackPrompt+"\n\n") {
			start = i
			break
		}
	}
	if start < 0 {
		return fmt.Sprintf("User: %s\n\nAssistant: %s", fallbackPrompt, fallbackResponse)
	}

	var b strings.Builder
	for _, msg := range messages[start:] {
		if msg == nil {
			continue
		}
		if content := strings.TrimSpace(msg.Content); content != "" {
			limit := goalTranscriptOutputChars
			if msg.Role == conversation.RoleUser {
				limit = goalTranscriptUserChars
			}
			fmt.Fprintf(&b, "%s: %s\n\n", goalTranscriptRole(msg.Role), tailForGoalTranscript(content, limit))
		}
		for _, call := range msg.ToolCalls {
			params, err := json.Marshal(call.Parameters)
			if err != nil {
				params = []byte(`"<unavailable>"`)
			}
			fmt.Fprintf(&b, "Assistant tool call: %s %s\n\n",
				call.Name, tailForGoalTranscript(string(params), goalTranscriptParamsChars))
		}
		for _, result := range msg.ToolResults {
			status := "succeeded"
			output := result.Output
			if result.Error != nil {
				status = "failed"
				if output == "" {
					output = result.Error.Message
				}
			}
			fmt.Fprintf(&b, "Tool result (%s, %s):\n%s\n\n",
				result.Name, status, tailForGoalTranscript(output, goalTranscriptOutputChars))
		}
	}
	if strings.TrimSpace(b.String()) == "" {
		return fmt.Sprintf("User: %s\n\nAssistant: %s", fallbackPrompt, fallbackResponse)
	}
	transcript := strings.TrimSpace(b.String())
	if len(transcript) > goalTranscriptTotalChars {
		transcript = tailForGoalTranscript(transcript, goalTranscriptTotalChars)
	}
	return transcript
}

func isGoalHookContext(msg *conversation.Message) bool {
	if msg == nil || msg.Metadata == nil {
		return false
	}
	isHook, _ := msg.Metadata["is_hook_context"].(bool)
	return isHook
}

func goalTranscriptRole(role conversation.Role) string {
	switch role {
	case conversation.RoleUser:
		return "User"
	case conversation.RoleAssistant:
		return "Assistant"
	case conversation.RoleTool:
		return "Tool"
	default:
		return string(role)
	}
}

func tailForGoalTranscript(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return fmt.Sprintf("[...%d leading characters truncated...]\n%s", len(text)-limit, text[len(text)-limit:])
}
