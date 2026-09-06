package provider

import (
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// PrepareMessagesForLLM converts projected peer messages into provider-safe canonical messages.
func PrepareMessagesForLLM(messages []*conversation.Message) []*conversation.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]*conversation.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if msg.Role != conversation.RolePeer {
			out = append(out, msg)
			continue
		}
		clone := msg.Clone()
		clone.Role = conversation.RoleUser
		clone.Content = formatPeerMessage(msg)
		out = append(out, clone)
	}
	return out
}

func formatPeerMessage(msg *conversation.Message) string {
	if msg == nil {
		return ""
	}
	if msg.A2A == nil {
		if msg.Content != "" {
			return "[Peer]\n" + msg.Content
		}
		return "[Peer]"
	}

	parts := []string{
		fmt.Sprintf("[A2A from @%s]", strings.TrimSpace(msg.A2A.RemoteAgentHandle)),
	}
	if taskID := strings.TrimSpace(msg.A2A.TaskID); taskID != "" {
		parts = append(parts, fmt.Sprintf("task=%s", taskID))
	}
	if contextID := strings.TrimSpace(msg.A2A.ContextID); contextID != "" {
		parts = append(parts, fmt.Sprintf("context=%s", contextID))
	}
	if state := strings.TrimSpace(msg.A2A.TaskState); state != "" {
		parts = append(parts, fmt.Sprintf("state=%s", state))
	}
	header := strings.Join(parts, " ")
	body := strings.TrimSpace(msg.Content)
	if len(msg.A2A.ReferenceTaskIDs) > 0 {
		body += "\nReference Tasks: " + strings.Join(msg.A2A.ReferenceTaskIDs, ", ")
	}
	refs := msg.A2A.References
	if len(refs) > 0 {
		refLines := make([]string, 0, len(refs))
		for _, ref := range refs {
			if strings.TrimSpace(ref.Type) == "" || strings.TrimSpace(ref.Value) == "" {
				continue
			}
			line := fmt.Sprintf("- %s: %s", ref.Type, ref.Value)
			if ref.Label != "" {
				line += fmt.Sprintf(" (%s)", ref.Label)
			}
			refLines = append(refLines, line)
		}
		if len(refLines) > 0 {
			body += "\nReferences:\n" + strings.Join(refLines, "\n")
		}
	}
	if body == "" {
		return header
	}
	return header + "\n" + body
}
