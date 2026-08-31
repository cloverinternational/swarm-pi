package provider

import (
	"maps"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// IsMessageValid reports whether a message has valid content for API translation.
// A message is valid if it has non-empty text content, tool calls, tool results,
// or thinking content.
func IsMessageValid(msg *conversation.Message) bool {
	if msg == nil {
		return false
	}
	if strings.TrimSpace(msg.Content) != "" {
		return true
	}
	if len(msg.ToolCalls) > 0 {
		return true
	}
	if len(msg.ToolResults) > 0 {
		return true
	}
	if msg.Metadata != nil {
		if thinking, ok := msg.Metadata["thinking"].(string); ok && strings.TrimSpace(thinking) != "" {
			return true
		}
		if reasoning, ok := msg.Metadata["reasoning_content"].(string); ok && strings.TrimSpace(reasoning) != "" {
			return true
		}
	}
	return strings.TrimSpace(msg.Thinking) != ""
}

// SanitizeMessage attempts to repair an invalid message.
// Returns nil if the message cannot be repaired.
func SanitizeMessage(msg *conversation.Message) *conversation.Message {
	if msg == nil {
		return nil
	}
	if IsMessageValid(msg) {
		return msg
	}
	sanitized := &conversation.Message{
		ID:          msg.ID,
		Timestamp:   msg.Timestamp,
		Role:        msg.Role,
		Provider:    msg.Provider,
		Model:       msg.Model,
		ToolCalls:   msg.ToolCalls,
		ToolResults: msg.ToolResults,
		Tokens:      msg.Tokens,
		Thinking:    msg.Thinking,
	}
	if msg.Metadata != nil {
		sanitized.Metadata = make(map[string]any)
		maps.Copy(sanitized.Metadata, msg.Metadata)
	}
	if sanitized.Thinking != "" {
		sanitized.Content = sanitized.Thinking
		return sanitized
	}
	if sanitized.Metadata != nil {
		if thinking, ok := sanitized.Metadata["thinking"].(string); ok && thinking != "" {
			sanitized.Content = thinking
			return sanitized
		}
		if reasoning, ok := sanitized.Metadata["reasoning_content"].(string); ok && reasoning != "" {
			sanitized.Content = reasoning
			return sanitized
		}
	}
	if sanitized.Role == conversation.RoleAssistant {
		sanitized.Content = "[Response truncated due to token limit]"
		if sanitized.Metadata == nil {
			sanitized.Metadata = make(map[string]any)
		}
		sanitized.Metadata["truncated"] = true
		sanitized.Metadata["sanitized"] = true
		return sanitized
	}
	return nil
}

// SanitizeMessages filters and repairs a slice of messages, skipping those that
// cannot be repaired. Call before translating messages to provider-native format.
func SanitizeMessages(messages []*conversation.Message) []*conversation.Message {
	result := make([]*conversation.Message, 0, len(messages))
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		if IsMessageValid(msg) {
			result = append(result, msg)
			continue
		}
		if sanitized := SanitizeMessage(msg); sanitized != nil {
			result = append(result, sanitized)
		}
	}
	return result
}

// EnsureValidResponse ensures a ChatResponse has valid content, adding a
// placeholder if the response was truncated or empty.
func EnsureValidResponse(resp *ChatResponse) *ChatResponse {
	if resp == nil || resp.Message == nil {
		return resp
	}
	msg := resp.Message
	if IsMessageValid(msg) {
		return resp
	}
	if resp.FinishReason == FinishReasonLength {
		msg.Content = "[Response truncated due to token limit]"
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["truncated"] = true
		msg.Metadata["finish_reason"] = "length"
		return resp
	}
	if msg.Role == conversation.RoleAssistant {
		msg.Content = "[Empty response]"
		if msg.Metadata == nil {
			msg.Metadata = make(map[string]any)
		}
		msg.Metadata["empty_response"] = true
	}
	return resp
}
