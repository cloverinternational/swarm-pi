package compaction

import (
	"fmt"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// buildCallIDMap scans all messages and builds a map from tool-call ID to the
// original ToolCall (which carries the parameters — file path, command, pattern,
// etc.). This is used by the micro-compactor so that pointer messages can include
// the exact invocation details rather than generic placeholders.
func buildCallIDMap(messages []*conversation.Message) map[string]conversation.ToolCall {
	m := make(map[string]conversation.ToolCall)
	for _, msg := range messages {
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				m[tc.ID] = tc
			}
		}
	}
	return m
}

// HeavyTools defines the set of tools eligible for micro-compaction.
// These tools produce large outputs relative to their semantic value.
// Matches Claude Code's sgH set.
//
// Both legacy PascalCase names and current SDK snake_case names are included
// so the compactor works regardless of which name convention a tool uses.
var HeavyTools = map[string]bool{
	// Legacy PascalCase names (kept for backward compatibility with tests)
	"Read":      true, // File contents (500-5000 tokens)
	"Bash":      true, // Command outputs (100-2000 tokens)
	"Grep":      true, // Search results (200-3000 tokens)
	"Glob":      true, // File listings (50-500 tokens)
	"WebSearch": true, // Search results (1000-5000 tokens)
	"WebFetch":  true, // Web content (2000-10000 tokens)
	"Edit":      true, // Edit confirmations (100-1000 tokens)
	"Write":     true, // Write confirmations (50-500 tokens)
	// Current SDK snake_case tool names
	"file_read":   true, // maps to Read
	"bash":        true, // maps to Bash
	"grep":        true, // maps to Grep
	"list_dir":    true, // maps to Glob
	"file_write":  true, // maps to Write
	"str_replace": true, // maps to Edit
}

// DefaultRetentionCount is the default number of recent results to keep per tool.
// Matches Claude Code's ogH constant (3).
const DefaultRetentionCount = 3

// MicroCompactor handles lightweight tool result compaction.
// Implements Claude Code's wM function adapted for our architecture.
type MicroCompactor struct {
	heavyTools           map[string]bool
	retentionCount       int
	stats                MicroCompactionStats
	conversationJSONPath string // optional: absolute path of the conversation JSON on disk
}

// MicroCompactionStats tracks compaction metrics.
type MicroCompactionStats struct {
	TotalCompactions int       // Number of compaction runs
	ResultsCompacted int       // Total tool results compacted
	TokensSaved      int       // Total tokens saved
	LastCompaction   time.Time // Last compaction timestamp
}

// NewMicroCompactor creates a new micro-compaction service.
func NewMicroCompactor() *MicroCompactor {
	return &MicroCompactor{
		heavyTools:     HeavyTools,
		retentionCount: DefaultRetentionCount,
		stats:          MicroCompactionStats{},
	}
}

// SetRetentionCount configures how many recent results to keep per tool.
func (mc *MicroCompactor) SetRetentionCount(count int) {
	if count < 1 {
		count = 1
	}
	mc.retentionCount = count
}

// SetConversationPath stores the absolute path of the conversation JSON file so
// that pointer messages can reference it. When set, compacted tool result
// placeholders will include the path so the agent can read the raw history if
// it ever needs to reconstruct compacted details.
func (mc *MicroCompactor) SetConversationPath(path string) {
	mc.conversationJSONPath = path
}

// Process performs micro-compaction on a message array.
// Returns the compacted messages and token savings.
//
// Algorithm (adapted from Claude Code's wM function):
//  1. Pre-pass: build a callID→ToolCall map so pointer messages can reference
//     the exact parameters (file path, command, pattern, etc.).
//  2. Process messages in REVERSE order (newest first)
//  3. Track count of each tool type across ALL ToolResults
//  4. Keep last N (retentionCount) results per tool
//  5. Replace older results with verbose pointer strings
//
// Note: In our architecture, tool results are stored as ToolResults[] within Messages.
// We adapt Claude Code's algorithm to work with this structure.
func (mc *MicroCompactor) Process(messages []*conversation.Message) ([]*conversation.Message, int) {
	if len(messages) == 0 {
		return messages, 0
	}

	// Pre-pass: build callID → ToolCall map from all messages so pointer
	// messages can include the actual tool invocation parameters.
	callMap := buildCallIDMap(messages)

	// Track tool usage counts per tool type across all messages
	toolCounts := make(map[string]int)
	tokensSaved := 0

	// Process in REVERSE order (newest first) - matches Claude Code exactly
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]

		// Skip messages with no tool results
		if len(msg.ToolResults) == 0 {
			continue
		}

		// Process each tool result within this message (also in reverse)
		for j := len(msg.ToolResults) - 1; j >= 0; j-- {
			toolResult := &msg.ToolResults[j]
			toolName := toolResult.Name

			if toolName == "" {
				continue
			}

			// Check if this tool is eligible for compaction (matches Claude Code's sgH check)
			if !mc.heavyTools[toolName] {
				continue
			}

			// Increment counter for this tool type (matches Claude Code's counter logic)
			count := toolCounts[toolName]
			toolCounts[toolName] = count + 1

			// Keep the last N results per tool (matches Claude Code's ogH retention)
			if count < mc.retentionCount {
				continue
			}

			// Beyond retention window - compact this result
			originalTokens := EstimateTokens(toolResult.Output)

			// Look up original ToolCall parameters for richer pointer message
			var callParams map[string]any
			if tc, ok := callMap[toolResult.CallID]; ok {
				callParams = tc.Parameters
			}

			pointer := createPointerMessage(toolName, callParams, mc.conversationJSONPath)
			pointerTokens := EstimateTokens(pointer)

			// Replace output with pointer (matches Claude Code's replacement strategy)
			toolResult.Output = pointer

			// Track compaction in message metadata
			if msg.Metadata == nil {
				msg.Metadata = make(map[string]any)
			}

			// Use unique key per tool result
			compactedKey := fmt.Sprintf("microCompacted_%s_%d", toolResult.CallID, j)
			msg.Metadata[compactedKey] = map[string]any{
				"compacted":      true,
				"originalTokens": originalTokens,
				"compactedAt":    time.Now().Unix(),
				"toolName":       toolName,
			}

			saved := originalTokens - pointerTokens
			if saved > 0 {
				tokensSaved += saved
			}

			mc.stats.ResultsCompacted++
		}
	}

	// Update statistics
	if tokensSaved > 0 {
		mc.stats.TotalCompactions++
		mc.stats.TokensSaved += tokensSaved
		mc.stats.LastCompaction = time.Now()
	}

	return messages, tokensSaved
}

// createPointerMessage generates a rich, verbose pointer for a compacted tool result.
//
// Instead of a generic "use Read again" stub, it includes:
//   - The exact invocation parameters extracted from the original ToolCall
//   - Instructions for re-running the tool with the same arguments
//   - The conversation JSON path (when available) so the agent can inspect
//     the raw transcript if it absolutely needs to recover the original output
//
// This matches Claude Code's pointer strategy but adds parameter context that
// the original implementation was missing.
func createPointerMessage(toolName string, params map[string]any, convJSONPath string) string {
	var b strings.Builder

	// Helper to extract a string param safely
	strParam := func(key string) string {
		if params == nil {
			return ""
		}
		if v, ok := params[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	b.WriteString("[Tool result compacted — output removed from context to save tokens]\n")

	switch toolName {
	case "file_read", "Read", "ReadLegacy":
		filePath := strParam("file_path")
		if filePath == "" {
			filePath = strParam("path")
		}
		if filePath != "" {
			b.WriteString(fmt.Sprintf("Tool: file_read | File: %s\n", filePath))
			b.WriteString(fmt.Sprintf("To retrieve: use file_read with file_path=%q", filePath))
		} else {
			b.WriteString("Tool: file_read\nTo retrieve: use file_read with the same file_path argument.")
		}

	case "bash", "Bash":
		cmd := strParam("command")
		if cmd == "" {
			cmd = strParam("cmd")
		}
		if cmd != "" {
			b.WriteString(fmt.Sprintf("Tool: bash | Command: %s\n", cmd))
			b.WriteString(fmt.Sprintf("To re-run: use bash with command=%q", cmd))
		} else {
			b.WriteString("Tool: bash\nTo retrieve: re-run the same bash command.")
		}

	case "grep", "Grep":
		pattern := strParam("pattern")
		path := strParam("path")
		glob := strParam("glob")
		b.WriteString("Tool: grep")
		if pattern != "" {
			b.WriteString(fmt.Sprintf(" | Pattern: %q", pattern))
		}
		if path != "" {
			b.WriteString(fmt.Sprintf(" | Path: %s", path))
		}
		if glob != "" {
			b.WriteString(fmt.Sprintf(" | Glob: %s", glob))
		}
		b.WriteString("\n")
		if pattern != "" {
			b.WriteString(fmt.Sprintf("To retrieve: re-run grep with pattern=%q", pattern))
			if path != "" {
				b.WriteString(fmt.Sprintf(", path=%q", path))
			}
		} else {
			b.WriteString("To retrieve: re-run grep with the same arguments.")
		}

	case "list_dir", "Glob":
		path := strParam("path")
		if path == "" {
			path = strParam("directory")
		}
		if path != "" {
			b.WriteString(fmt.Sprintf("Tool: list_dir | Directory: %s\n", path))
			b.WriteString(fmt.Sprintf("To retrieve: use list_dir with path=%q", path))
		} else {
			b.WriteString("Tool: list_dir\nTo retrieve: re-run list_dir with the same path argument.")
		}

	case "str_replace", "Edit", "EditLegacy":
		filePath := strParam("file_path")
		if filePath == "" {
			filePath = strParam("path")
		}
		if filePath != "" {
			b.WriteString(fmt.Sprintf("Tool: str_replace | File edited: %s\n", filePath))
			b.WriteString("Edit was applied successfully. The file on disk is the authoritative source of truth.")
		} else {
			b.WriteString("Tool: str_replace\nEdit was applied successfully. Read the modified file to verify the current state.")
		}

	case "file_write", "Write":
		filePath := strParam("file_path")
		if filePath == "" {
			filePath = strParam("path")
		}
		if filePath != "" {
			b.WriteString(fmt.Sprintf("Tool: file_write | File written: %s\n", filePath))
			b.WriteString(fmt.Sprintf("Write completed. Use file_read with file_path=%q to view the current content.", filePath))
		} else {
			b.WriteString("Tool: file_write\nWrite completed. Use file_read to view the written file.")
		}

	default:
		b.WriteString(fmt.Sprintf("Tool: %s\n", toolName))
		if params != nil && len(params) > 0 {
			b.WriteString("Parameters: ")
			first := true
			for k, v := range params {
				if !first {
					b.WriteString(", ")
				}
				b.WriteString(fmt.Sprintf("%s=%v", k, v))
				first = false
			}
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("To retrieve: re-run %s with the same arguments.", toolName))
	}

	// Include conversation JSON path so the agent can inspect raw history if needed
	if convJSONPath != "" {
		b.WriteString(fmt.Sprintf(
			"\nConversation history (including original output) is persisted at: %s",
			convJSONPath,
		))
	}

	return b.String()
}

// GetStats returns current compaction statistics.
func (mc *MicroCompactor) GetStats() MicroCompactionStats {
	return mc.stats
}

// ResetStats clears statistics.
func (mc *MicroCompactor) ResetStats() {
	mc.stats = MicroCompactionStats{}
}

// IsMessageCompacted checks if any tool results in a message have been micro-compacted.
func IsMessageCompacted(msg *conversation.Message) bool {
	if msg.Metadata == nil {
		return false
	}
	for key := range msg.Metadata {
		if strings.HasPrefix(key, "microCompacted_") {
			return true
		}
	}
	return false
}

// GetCompactedCount returns the number of compacted tool results in a message.
func GetCompactedCount(msg *conversation.Message) int {
	count := 0
	if msg.Metadata == nil {
		return count
	}
	for key := range msg.Metadata {
		if strings.HasPrefix(key, "microCompacted_") {
			count++
		}
	}
	return count
}

// ProcessEnabled checks if micro-compaction should run based on config.
// Convenience function for integration with config system.
func ProcessEnabled(messages []*conversation.Message, enabled bool, retentionCount int) ([]*conversation.Message, int) {
	if !enabled {
		return messages, 0
	}

	mc := NewMicroCompactor()
	mc.SetRetentionCount(retentionCount)
	return mc.Process(messages)
}

// ProcessEnabledWithPath is like ProcessEnabled but also sets the conversation
// JSON path so that pointer messages can reference it.
func ProcessEnabledWithPath(messages []*conversation.Message, enabled bool, retentionCount int, convJSONPath string) ([]*conversation.Message, int) {
	if !enabled {
		return messages, 0
	}

	mc := NewMicroCompactor()
	mc.SetRetentionCount(retentionCount)
	mc.SetConversationPath(convJSONPath)
	return mc.Process(messages)
}
