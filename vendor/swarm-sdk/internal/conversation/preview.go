package conversation

import "strings"

// GenerateConversationPreview creates a preview string from the last meaningful
// message in a conversation. It skips system messages, task nudges, task
// maintenance reminders, dream prompts, and other non-conversational content.
//
// The returned preview is truncated to 120 characters with an ellipsis suffix
// when longer.  Returns an empty string when no meaningful message is found.
func GenerateConversationPreview(messages []*Message) string {
	if len(messages) == 0 {
		return ""
	}

	// System message prefixes that should be skipped
	systemPrefixes := []string{
		"[Task Nudge]",
		"[Task Maintenance Reminder]",
		"# Dream:",
		"# Dream: Memory Consolidation",
		"## Phase 1",
		"## Phase 2",
		"## Phase 3",
		"## Phase 4",
		"You've used",
		"Has the work expanded",
		"Consider:",
		"No pending tasks",
		"You've used",
		"Track your work with tasks:",
		"Use task_create",
	}

	// Walk backwards through messages to find a meaningful one
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg == nil {
			continue
		}

		// Skip system messages entirely
		if msg.Role == RoleSystem {
			continue
		}

		content := msg.Content
		if content == "" {
			continue
		}

		// Skip messages that start with system prefixes
		skip := false
		for _, prefix := range systemPrefixes {
			if strings.HasPrefix(content, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		// Skip task-related messages (task nudges and reminders have specific patterns)
		if strings.Contains(content, "[Task Nudge]") ||
			strings.Contains(content, "[Task Maintenance Reminder]") ||
			strings.Contains(content, "No pending tasks") ||
			strings.HasPrefix(content, "Track your work with tasks:") {
			continue
		}

		// Found a meaningful message - truncate and return
		preview := content
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		return preview
	}

	// Fallback: return empty string if no meaningful message found
	return ""
}
