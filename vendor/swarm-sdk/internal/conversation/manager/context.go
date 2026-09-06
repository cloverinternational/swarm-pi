package manager

import (
	"context"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	sdkerr "github.com/Swarm-Code/mono/swarm-sdk/internal/sdkerr"
)

// ContextWindowStrategy defines how to trim conversations when context window is exceeded.
type ContextWindowStrategy interface {
	// ShouldTrim returns true if the conversation should be trimmed.
	ShouldTrim(conv *conversation.Conversation, maxTokens int) bool

	// Trim reduces the conversation to fit within maxTokens.
	Trim(ctx context.Context, conv *conversation.Conversation, maxTokens int) error
}

// LRUContextStrategy removes the oldest messages first (Least Recently Used).
type LRUContextStrategy struct{}

// NewLRUContextStrategy creates a new LRU context strategy.
func NewLRUContextStrategy() *LRUContextStrategy {
	return &LRUContextStrategy{}
}

// ShouldTrim checks if trimming is needed.
func (s *LRUContextStrategy) ShouldTrim(conv *conversation.Conversation, maxTokens int) bool {
	return conv.TotalTokens > maxTokens
}

// Trim removes oldest messages until within token budget.
// CRITICAL: Maintains tool_use/tool_result pairing to avoid API errors.
// The Anthropic API requires that every tool_result has a corresponding tool_use
// in the immediately preceding assistant message.
func (s *LRUContextStrategy) Trim(ctx context.Context, conv *conversation.Conversation, maxTokens int) error {
	if !s.ShouldTrim(conv, maxTokens) {
		return nil
	}

	// Build a map of tool_use IDs to their message indices
	// and tool_result IDs to their message indices
	toolUseToMsgIdx := make(map[string]int)    // tool_use_id -> message index
	toolResultToMsgIdx := make(map[string]int) // tool_use_id (from tool_result.CallID) -> message index

	for i, msg := range conv.Messages {
		// Track tool_use blocks in assistant messages
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				toolUseToMsgIdx[tc.ID] = i
			}
		}
		// Track tool_result blocks (they reference tool_use by CallID)
		for _, tr := range msg.ToolResults {
			if tr.CallID != "" {
				toolResultToMsgIdx[tr.CallID] = i
			}
		}
	}

	// Determine which messages to remove
	// We'll mark messages for removal and then filter them out
	toRemove := make(map[int]bool)

	// Remove from the beginning until under budget
	for i := 0; i < len(conv.Messages) && conv.TotalTokens > maxTokens; i++ {
		if toRemove[i] {
			continue // Already marked for removal
		}

		msg := conv.Messages[i]

		// Mark this message for removal
		toRemove[i] = true
		if msg.Tokens != nil {
			conv.TotalTokens -= msg.Tokens.Total
		}

		// If this message has tool_use blocks, also mark corresponding tool_result messages
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				if resultMsgIdx, exists := toolResultToMsgIdx[tc.ID]; exists && !toRemove[resultMsgIdx] {
					toRemove[resultMsgIdx] = true
					resultMsg := conv.Messages[resultMsgIdx]
					if resultMsg.Tokens != nil {
						conv.TotalTokens -= resultMsg.Tokens.Total
					}
				}
			}
		}

		// If this message has tool_result blocks, also mark corresponding tool_use messages
		for _, tr := range msg.ToolResults {
			if tr.CallID != "" {
				if useMsgIdx, exists := toolUseToMsgIdx[tr.CallID]; exists && !toRemove[useMsgIdx] {
					toRemove[useMsgIdx] = true
					useMsg := conv.Messages[useMsgIdx]
					if useMsg.Tokens != nil {
						conv.TotalTokens -= useMsg.Tokens.Total
					}
				}
			}
		}
	}

	// Build the new message slice, filtering out removed messages
	newMessages := make([]*conversation.Message, 0, len(conv.Messages)-len(toRemove))
	for i, msg := range conv.Messages {
		if !toRemove[i] {
			newMessages = append(newMessages, msg)
		}
	}
	conv.Messages = newMessages

	// Final validation pass: ensure no orphaned tool_results remain
	// This catches any edge cases from the above logic
	for {
		changed := false

		// Rebuild maps for remaining messages
		remainingToolUses := make(map[string]bool)
		for _, msg := range conv.Messages {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					remainingToolUses[tc.ID] = true
				}
			}
		}

		// Find and remove any messages with orphaned tool_results
		newMessages := make([]*conversation.Message, 0, len(conv.Messages))
		for _, msg := range conv.Messages {
			hasOrphanedResult := false
			for _, tr := range msg.ToolResults {
				if tr.CallID != "" && !remainingToolUses[tr.CallID] {
					hasOrphanedResult = true
					break
				}
			}
			if hasOrphanedResult {
				changed = true
				if msg.Tokens != nil {
					conv.TotalTokens -= msg.Tokens.Total
				}
				continue // Skip this message
			}
			newMessages = append(newMessages, msg)
		}
		conv.Messages = newMessages

		if !changed {
			break
		}
	}

	// Ensure first message is from user (Anthropic API requirement)
	for len(conv.Messages) > 0 && conv.Messages[0].Role != conversation.RoleUser {
		msg := conv.Messages[0]

		// If removing an assistant message with tool_use, also remove orphaned tool_results
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				newMessages := make([]*conversation.Message, 0, len(conv.Messages)-1)
				for _, m := range conv.Messages[1:] {
					hasThisToolResult := false
					for _, tr := range m.ToolResults {
						if tr.CallID == tc.ID {
							hasThisToolResult = true
							break
						}
					}
					if hasThisToolResult {
						if m.Tokens != nil {
							conv.TotalTokens -= m.Tokens.Total
						}
						continue
					}
					newMessages = append(newMessages, m)
				}
				conv.Messages = newMessages
			}
		}

		// If first message was not removed by the above (no tool_use), remove it now
		if len(conv.Messages) > 0 && conv.Messages[0].Role != conversation.RoleUser {
			if conv.Messages[0].Tokens != nil {
				conv.TotalTokens -= conv.Messages[0].Tokens.Total
			}
			conv.Messages = conv.Messages[1:]
		}
	}

	return nil
}

// SlidingWindowStrategy keeps only the most recent N messages.
type SlidingWindowStrategy struct {
	// WindowSize is the number of recent messages to keep.
	WindowSize int
}

// NewSlidingWindowStrategy creates a new sliding window strategy.
func NewSlidingWindowStrategy(windowSize int) *SlidingWindowStrategy {
	return &SlidingWindowStrategy{
		WindowSize: windowSize,
	}
}

// ShouldTrim checks if trimming is needed.
func (s *SlidingWindowStrategy) ShouldTrim(conv *conversation.Conversation, maxTokens int) bool {
	return conv.TotalTokens > maxTokens || len(conv.Messages) > s.WindowSize
}

// Trim keeps only the most recent messages while maintaining tool_use/tool_result pairing.
func (s *SlidingWindowStrategy) Trim(ctx context.Context, conv *conversation.Conversation, maxTokens int) error {
	if len(conv.Messages) <= s.WindowSize {
		return nil
	}

	// Keep only recent messages
	conv.Messages = conv.Messages[len(conv.Messages)-s.WindowSize:]

	// Build a map of remaining tool_use IDs
	remainingToolUses := make(map[string]bool)
	for _, msg := range conv.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				remainingToolUses[tc.ID] = true
			}
		}
	}

	// Remove any messages with orphaned tool_results
	for {
		changed := false
		newMessages := make([]*conversation.Message, 0, len(conv.Messages))
		for _, msg := range conv.Messages {
			hasOrphanedResult := false
			for _, tr := range msg.ToolResults {
				if tr.CallID != "" && !remainingToolUses[tr.CallID] {
					hasOrphanedResult = true
					break
				}
			}
			if hasOrphanedResult {
				changed = true
				continue // Skip this message
			}
			newMessages = append(newMessages, msg)
		}
		conv.Messages = newMessages

		if !changed {
			break
		}

		// Rebuild map for next iteration
		remainingToolUses = make(map[string]bool)
		for _, msg := range conv.Messages {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					remainingToolUses[tc.ID] = true
				}
			}
		}
	}

	// Ensure first message is from user
	for len(conv.Messages) > 0 && conv.Messages[0].Role != conversation.RoleUser {
		msg := conv.Messages[0]
		// Remove orphaned tool_results that reference this message's tool_use
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				newMessages := make([]*conversation.Message, 0, len(conv.Messages)-1)
				for _, m := range conv.Messages[1:] {
					hasThisToolResult := false
					for _, tr := range m.ToolResults {
						if tr.CallID == tc.ID {
							hasThisToolResult = true
							break
						}
					}
					if !hasThisToolResult {
						newMessages = append(newMessages, m)
					}
				}
				conv.Messages = newMessages
			}
		}
		if len(conv.Messages) > 0 && conv.Messages[0].Role != conversation.RoleUser {
			conv.Messages = conv.Messages[1:]
		}
	}

	// Recalculate token count
	conv.TotalTokens = 0
	for _, msg := range conv.Messages {
		if msg.Tokens != nil {
			conv.TotalTokens += msg.Tokens.Total
		}
	}

	return nil
}

// PriorityContextStrategy keeps system messages and recent messages, removes older user messages.
type PriorityContextStrategy struct {
	// RecentMessageCount is how many recent messages to always keep.
	RecentMessageCount int
}

// NewPriorityContextStrategy creates a new priority context strategy.
func NewPriorityContextStrategy(recentCount int) *PriorityContextStrategy {
	return &PriorityContextStrategy{
		RecentMessageCount: recentCount,
	}
}

// ShouldTrim checks if trimming is needed.
func (s *PriorityContextStrategy) ShouldTrim(conv *conversation.Conversation, maxTokens int) bool {
	return conv.TotalTokens > maxTokens
}

// Trim removes low-priority messages while keeping system and recent messages.
// Maintains tool_use/tool_result pairing to avoid API errors.
func (s *PriorityContextStrategy) Trim(ctx context.Context, conv *conversation.Conversation, maxTokens int) error {
	if !s.ShouldTrim(conv, maxTokens) {
		return nil
	}

	// Separate messages by priority
	var systemMsgs []*conversation.Message
	var recentMsgs []*conversation.Message

	// System messages are always kept
	for _, msg := range conv.Messages {
		if msg.Role == conversation.RoleSystem {
			systemMsgs = append(systemMsgs, msg)
		}
	}

	// Recent messages are kept
	nonSystemCount := len(conv.Messages) - len(systemMsgs)
	if nonSystemCount > s.RecentMessageCount {
		nonSystemIdx := 0
		for _, msg := range conv.Messages {
			if msg.Role == conversation.RoleSystem {
				continue
			}
			if nonSystemIdx >= nonSystemCount-s.RecentMessageCount {
				recentMsgs = append(recentMsgs, msg)
			}
			nonSystemIdx++
		}
	} else {
		for _, msg := range conv.Messages {
			if msg.Role != conversation.RoleSystem {
				recentMsgs = append(recentMsgs, msg)
			}
		}
	}

	// Combine system + recent messages
	currentMsgs := make([]*conversation.Message, 0)
	currentMsgs = append(currentMsgs, systemMsgs...)
	currentMsgs = append(currentMsgs, recentMsgs...)

	conv.Messages = currentMsgs

	// Build a map of remaining tool_use IDs
	remainingToolUses := make(map[string]bool)
	for _, msg := range conv.Messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				remainingToolUses[tc.ID] = true
			}
		}
	}

	// Remove any messages with orphaned tool_results
	for {
		changed := false
		newMessages := make([]*conversation.Message, 0, len(conv.Messages))
		for _, msg := range conv.Messages {
			hasOrphanedResult := false
			for _, tr := range msg.ToolResults {
				if tr.CallID != "" && !remainingToolUses[tr.CallID] {
					hasOrphanedResult = true
					break
				}
			}
			if hasOrphanedResult {
				changed = true
				continue // Skip this message
			}
			newMessages = append(newMessages, msg)
		}
		conv.Messages = newMessages

		if !changed {
			break
		}

		// Rebuild map for next iteration
		remainingToolUses = make(map[string]bool)
		for _, msg := range conv.Messages {
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					remainingToolUses[tc.ID] = true
				}
			}
		}
	}

	// Ensure first non-system message is from user
	for len(conv.Messages) > 0 {
		firstNonSystem := -1
		for i, msg := range conv.Messages {
			if msg.Role != conversation.RoleSystem {
				firstNonSystem = i
				break
			}
		}
		if firstNonSystem == -1 || conv.Messages[firstNonSystem].Role == conversation.RoleUser {
			break
		}
		// Remove the first non-system message (it's an assistant message)
		msg := conv.Messages[firstNonSystem]
		// Also remove orphaned tool_results that reference this message's tool_use
		toRemove := make(map[int]bool)
		toRemove[firstNonSystem] = true
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				for j, m := range conv.Messages {
					for _, tr := range m.ToolResults {
						if tr.CallID == tc.ID {
							toRemove[j] = true
						}
					}
				}
			}
		}
		newMessages := make([]*conversation.Message, 0)
		for i, m := range conv.Messages {
			if !toRemove[i] {
				newMessages = append(newMessages, m)
			}
		}
		conv.Messages = newMessages
	}

	// Recalculate token count
	conv.TotalTokens = 0
	for _, msg := range conv.Messages {
		if msg.Tokens != nil {
			conv.TotalTokens += msg.Tokens.Total
		}
	}

	return nil
}

// ValidateStrategy validates a context strategy configuration.
func ValidateStrategy(strategy ContextWindowStrategy) error {
	if strategy == nil {
		return sdkerr.Permanent("manager.invalid_strategy", "ContextWindowStrategy is required")
	}
	return nil
}
