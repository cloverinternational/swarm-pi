package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
)

// =============================================================================
// Empty Message Rendering Tests
//
// These tests verify that the message rendering pipeline handles all message
// types correctly, including edge cases that cause empty "█" blocks in the UI.
//
// Root Cause: The SDK agent loop stores one message PER TURN. A multi-turn
// agent response creates multiple assistant messages in the conversation store:
//   Turn 1: {content: "I'll help", tool_calls: [Read]}  → saved
//   Tool results message saved
//   Turn 2: {content: "", tool_calls: [Edit]}            → saved (empty content!)
//   Tool results message saved
//   Turn 3: {content: "", tool_calls: []}                → saved (totally empty!)
//
// The TUI merges these into one message during streaming, but when loading
// from disk via loadMessagesFromSDK, each is reconstructed independently.
// Empty assistant messages produce empty "█" blocks.
// =============================================================================

// TestEmptyAssistantMessageDetection verifies we can detect assistant messages
// that would render as empty "█" blocks via both rendering paths.
func TestEmptyAssistantMessageDetection(t *testing.T) {
	tests := []struct {
		name        string
		msg         Message
		expectEmpty bool // Would render as empty "█"
		description string
	}{
		{
			name: "normal content message",
			msg: Message{
				Role:    "assistant",
				Content: "Hello, I can help with that.",
			},
			expectEmpty: false,
			description: "Standard assistant message with text content",
		},
		{
			name: "totally empty assistant",
			msg: Message{
				Role:    "assistant",
				Content: "",
			},
			expectEmpty: true,
			description: "Agent turn that returned no content, no tools (stop turn)",
		},
		{
			name: "whitespace only content",
			msg: Message{
				Role:    "assistant",
				Content: "   \n\t  ",
			},
			expectEmpty: true,
			description: "Content is only whitespace - renders as empty",
		},
		{
			name: "tool calls with no content",
			msg: Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCallDisplay{
					{ID: "tc_1", Name: "Read", Parameters: map[string]any{"file_path": "/tmp/test"}},
				},
			},
			expectEmpty: false,
			description: "Tool-only turn - tool calls should render even without content",
		},
		{
			name: "tool calls and results with no content",
			msg: Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []ToolCallDisplay{
					{ID: "tc_1", Name: "Bash", Parameters: map[string]any{"command": "ls"}},
				},
				ToolResults: []ToolResultDisplay{
					{CallID: "tc_1", Output: "file1.go\nfile2.go"},
				},
			},
			expectEmpty: false,
			description: "Complete tool cycle without text content - tools render",
		},
		{
			name: "thinking only",
			msg: Message{
				Role:     "assistant",
				Content:  "",
				Thinking: "Let me analyze this code...",
			},
			expectEmpty: false,
			description: "Thinking-only message should render thinking block",
		},
		{
			name: "empty with metadata elapsed_time",
			msg: Message{
				Role:    "assistant",
				Content: "",
				Metadata: map[string]any{
					"elapsed_time_ns": float64(5000000000),
				},
				Model: "claude-opus-4-20250514",
			},
			expectEmpty: true,
			description: "Completion marker message - has metadata but no displayable content",
		},
		{
			name: "pre-rendered workflow content",
			msg: Message{
				Role:          "assistant",
				Content:       "┌─ Workflow Output ─┐\n│ result │\n└───────────────────┘",
				IsPreRendered: true,
			},
			expectEmpty: false,
			description: "Pre-rendered workflow content passes through directly",
		},
		{
			name: "empty pre-rendered",
			msg: Message{
				Role:          "assistant",
				Content:       "",
				IsPreRendered: true,
			},
			expectEmpty: true,
			description: "Pre-rendered but empty content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isEmpty := wouldRenderEmpty(&tt.msg)
			if isEmpty != tt.expectEmpty {
				t.Errorf("wouldRenderEmpty() = %v, want %v\n  Description: %s\n  Content: %q\n  ToolCalls: %d\n  Thinking: %q",
					isEmpty, tt.expectEmpty, tt.description,
					tt.msg.Content, len(tt.msg.ToolCalls), tt.msg.Thinking)
			}
		})
	}
}

// TestHistoryLoadOrderedBlocksReconstruction tests the OrderedBlocks reconstruction
// path used by loadMessagesFromSDK. This is the "history load" rendering path.
func TestHistoryLoadOrderedBlocksReconstruction(t *testing.T) {
	tests := []struct {
		name        string
		sdkMsg      conversation.Message
		wantBlocks  int
		wantEmpty   bool
		description string
	}{
		{
			name: "normal assistant with content",
			sdkMsg: conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "Here's the analysis.",
			},
			wantBlocks: 1, // one content block
			wantEmpty:  false,
		},
		{
			name: "assistant with tool calls and content",
			sdkMsg: conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "Let me read that file.",
				ToolCalls: []conversation.ToolCall{
					{ID: "tc_1", Name: "Read", Parameters: map[string]any{"file_path": "/tmp/x"}},
				},
			},
			wantBlocks: 2, // tool_call + content
			wantEmpty:  false,
		},
		{
			name: "assistant with tool calls only (no content)",
			sdkMsg: conversation.Message{
				Role: conversation.RoleAssistant,
				ToolCalls: []conversation.ToolCall{
					{ID: "tc_1", Name: "Bash", Parameters: map[string]any{"command": "ls"}},
				},
			},
			wantBlocks: 1, // just tool_call, no content block
			wantEmpty:  false,
		},
		{
			name: "totally empty assistant (the bug case)",
			sdkMsg: conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "",
			},
			wantBlocks: 0, // no blocks at all
			wantEmpty:  true,
		},
		{
			name: "empty assistant with elapsed_time metadata",
			sdkMsg: conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "",
				Model:   "claude-opus-4-20250514",
				Metadata: map[string]any{
					"elapsed_time_ns": float64(11114464828),
				},
			},
			wantBlocks: 0,
			wantEmpty:  true,
		},
		{
			name: "assistant with thinking only",
			sdkMsg: conversation.Message{
				Role:     conversation.RoleAssistant,
				Content:  "",
				Thinking: "I need to think about this...",
			},
			wantBlocks: 1, // thinking block only
			wantEmpty:  false,
		},
		{
			name: "assistant with thinking and content",
			sdkMsg: conversation.Message{
				Role:     conversation.RoleAssistant,
				Content:  "After careful thought, here's my answer.",
				Thinking: "Let me consider the options...",
			},
			wantBlocks: 2, // thinking + content
			wantEmpty:  false,
		},
		{
			name: "assistant with tool calls, results via separate messages",
			sdkMsg: conversation.Message{
				Role:    conversation.RoleAssistant,
				Content: "",
				ToolCalls: []conversation.ToolCall{
					{ID: "tc_1", Name: "Read", Parameters: map[string]any{"file_path": "/a.go"}},
					{ID: "tc_2", Name: "Read", Parameters: map[string]any{"file_path": "/b.go"}},
				},
			},
			wantBlocks: 2, // two tool_call blocks
			wantEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uiMsg := reconstructOrderedBlocks(&tt.sdkMsg)

			if len(uiMsg.OrderedBlocks) != tt.wantBlocks {
				t.Errorf("OrderedBlocks count = %d, want %d", len(uiMsg.OrderedBlocks), tt.wantBlocks)
				for i, b := range uiMsg.OrderedBlocks {
					t.Logf("  Block[%d]: type=%s content=%q", i, b.Type, truncateStr(b.GetContent(), 50))
				}
			}

			isEmpty := wouldRenderEmpty(&uiMsg)
			if isEmpty != tt.wantEmpty {
				t.Errorf("wouldRenderEmpty() = %v, want %v", isEmpty, tt.wantEmpty)
			}
		})
	}
}

// TestStreamingPathOrderedBlocks simulates the streaming path where blocks
// are built incrementally (as in agentContentUpdateMsg, agentToolCallMsg, etc.)
func TestStreamingPathOrderedBlocks(t *testing.T) {
	tests := []struct {
		name        string
		steps       []streamStep
		wantEmpty   bool
		description string
	}{
		{
			name: "normal streaming: content chunks",
			steps: []streamStep{
				{typ: "content", content: "Hello ", append: false},
				{typ: "content", content: "world!", append: true},
			},
			wantEmpty: false,
		},
		{
			name: "streaming: thinking then content",
			steps: []streamStep{
				{typ: "thinking", content: "Let me think..."},
				{typ: "content", content: "Here's my answer.", append: false},
			},
			wantEmpty: false,
		},
		{
			name: "streaming: content then tool call then more content",
			steps: []streamStep{
				{typ: "content", content: "Let me check. ", append: false},
				{typ: "tool_call", toolCall: &ToolCallDisplay{ID: "tc_1", Name: "Read"}},
				{typ: "tool_result", toolResult: &ToolResultDisplay{CallID: "tc_1", Output: "file contents"}},
				{typ: "content", content: "I found the issue.", append: true},
			},
			wantEmpty: false,
		},
		{
			name: "streaming: tool call only (no content)",
			steps: []streamStep{
				{typ: "tool_call", toolCall: &ToolCallDisplay{ID: "tc_1", Name: "Bash"}},
				{typ: "tool_result", toolResult: &ToolResultDisplay{CallID: "tc_1", Output: "output"}},
			},
			wantEmpty: false,
		},
		{
			name: "streaming: empty content update",
			steps: []streamStep{
				{typ: "content", content: "", append: false},
			},
			wantEmpty:   true,
			description: "Provider sent empty content delta - creates empty content block",
		},
		{
			name:        "streaming: no updates at all",
			steps:       []streamStep{},
			wantEmpty:   true,
			description: "No streaming updates received - message stays empty",
		},
		{
			name: "streaming: only thinking (no content)",
			steps: []streamStep{
				{typ: "thinking", content: "I need to analyze this carefully..."},
			},
			wantEmpty:   false,
			description: "Thinking-only turn - should render thinking block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := simulateStreamingBuild(tt.steps)
			isEmpty := wouldRenderEmpty(&msg)
			if isEmpty != tt.wantEmpty {
				t.Errorf("wouldRenderEmpty() = %v, want %v (%s)", isEmpty, tt.wantEmpty, tt.description)
				t.Logf("  Message: content=%q, blocks=%d", msg.GetContent(), len(msg.OrderedBlocks))
				for i, b := range msg.OrderedBlocks {
					t.Logf("    Block[%d]: type=%s, content=%q", i, b.Type, truncateStr(b.GetContent(), 50))
				}
			}
		})
	}
}

// TestMarkdownRendererEmptyInput verifies that renderMarkdownWithWrappingVerbose
// returns meaningful results for empty/whitespace input. This is the root of
// the empty "█" display issue.
func TestMarkdownRendererEmptyInput(t *testing.T) {
	th := defaultTheme()

	tests := []struct {
		name      string
		input     string
		wantEmpty bool // true if all returned lines are empty/whitespace
	}{
		{"empty string", "", true},
		{"single space", " ", true},
		{"newline only", "\n", true},
		{"whitespace only", "  \t\n  ", true},
		{"real content", "Hello world", false},
		{"content with whitespace", "  Hello  ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := renderMarkdownWithWrappingVerbose(tt.input, 80, th, false)
			allEmpty := true
			for _, line := range lines {
				// Strip ANSI codes before checking emptiness - background styling
				// adds ANSI sequences to empty lines which is correct behavior
				plain := stripANSI(line)
				if strings.TrimSpace(plain) != "" {
					allEmpty = false
					break
				}
			}
			if allEmpty != tt.wantEmpty {
				t.Errorf("renderMarkdownWithWrappingVerbose(%q) allEmpty=%v, want %v (lines=%v)",
					tt.input, allEmpty, tt.wantEmpty, lines)
			}
		})
	}
}

// TestScanConversationStoreForEmptyMessages scans real conversation JSON files
// from ~/.swarmos/conversations/ and reports any that contain empty assistant
// messages which would render as empty "█" blocks.
func TestScanConversationStoreForEmptyMessages(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	convDir := filepath.Join(homeDir, ".swarmos", "conversations")
	if _, err := os.Stat(convDir); os.IsNotExist(err) {
		t.Skip("No conversation store found at " + convDir)
	}

	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Skipf("Cannot read conversation directory: %v", err)
	}

	type emptyMessageInfo struct {
		ConvFile string
		MsgIndex int
		MsgID    string
		Category string // "totally_empty", "whitespace_only", "empty_with_metadata"
		PrevRole string
		NextRole string
	}

	var (
		totalConvs     int
		totalMsgs      int
		totalAssistant int
		emptyMessages  []emptyMessageInfo
		convsWithEmpty = make(map[string]int)
		categoryCounts = make(map[string]int)
		scannedFiles   int
		maxScan        = 500 // Limit for test runtime
	)

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if scannedFiles >= maxScan {
			break
		}
		scannedFiles++

		filePath := filepath.Join(convDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var conv conversation.Conversation
		if err := json.Unmarshal(data, &conv); err != nil {
			continue
		}

		totalConvs++
		totalMsgs += len(conv.Messages)

		for i, msg := range conv.Messages {
			if msg.Role != conversation.RoleAssistant {
				continue
			}
			totalAssistant++

			// Simulate the loadMessagesFromSDK reconstruction
			uiMsg := reconstructOrderedBlocks(msg)

			if !wouldRenderEmpty(&uiMsg) {
				continue
			}

			// Categorize the empty message
			category := "totally_empty"
			if strings.TrimSpace(msg.Content) != msg.Content && msg.Content != "" {
				category = "whitespace_only"
			}
			if msg.Metadata != nil {
				if _, ok := msg.Metadata["elapsed_time_ns"]; ok {
					category = "completion_marker"
				}
			}

			prevRole := ""
			if i > 0 {
				prevRole = string(conv.Messages[i-1].Role)
			}
			nextRole := ""
			if i+1 < len(conv.Messages) {
				nextRole = string(conv.Messages[i+1].Role)
			}

			emptyMessages = append(emptyMessages, emptyMessageInfo{
				ConvFile: entry.Name(),
				MsgIndex: i,
				MsgID:    msg.ID,
				Category: category,
				PrevRole: prevRole,
				NextRole: nextRole,
			})

			convsWithEmpty[entry.Name()]++
			categoryCounts[category]++
		}
	}

	// Report findings
	t.Logf("=== Conversation Store Scan Results ===")
	t.Logf("Scanned: %d conversations, %d total messages, %d assistant messages",
		totalConvs, totalMsgs, totalAssistant)
	t.Logf("Empty assistant messages: %d (in %d conversations)", len(emptyMessages), len(convsWithEmpty))

	for cat, count := range categoryCounts {
		t.Logf("  Category '%s': %d", cat, count)
	}

	if len(emptyMessages) > 0 {
		t.Logf("\nSample empty messages (first 20):")
		limit := 20
		if len(emptyMessages) < limit {
			limit = len(emptyMessages)
		}
		for _, em := range emptyMessages[:limit] {
			t.Logf("  %s msg[%d] (%s) prev=%s next=%s",
				em.ConvFile, em.MsgIndex, em.Category, em.PrevRole, em.NextRole)
		}
	}

	// This test REPORTS but does not FAIL - it's a diagnostic scan.
	// The real fix is in the rendering pipeline. But we flag the count.
	if len(emptyMessages) > 0 {
		t.Logf("\nWARNING: %d assistant messages would render as empty '█' blocks", len(emptyMessages))
		t.Logf("These are created by the SDK agent loop saving one message per turn.")
		t.Logf("The TUI should filter or merge these during loadMessagesFromSDK.")
	}
}

// TestRenderingPathConsistency checks that the streaming path and history-load
// path produce equivalent rendering results for the same logical conversation.
func TestRenderingPathConsistency(t *testing.T) {
	// Simulate a conversation that would be stored as multiple SDK messages
	// but rendered as a single message during streaming.
	sdkMessages := []*conversation.Message{
		{
			ID:        "msg_1",
			Role:      conversation.RoleUser,
			Content:   "Read the file and tell me what it does",
			Timestamp: time.Now(),
		},
		{
			// Turn 1: assistant says something and calls a tool
			ID:      "msg_2",
			Role:    conversation.RoleAssistant,
			Content: "Let me read that file for you.",
			ToolCalls: []conversation.ToolCall{
				{ID: "tc_1", Name: "Read", Parameters: map[string]any{"file_path": "/tmp/test.go"}},
			},
			Timestamp: time.Now(),
		},
		{
			// Tool results
			ID:   "msg_3",
			Role: conversation.RoleTool,
			ToolResults: []conversation.ToolResult{
				{CallID: "tc_1", Output: "package main\nfunc main() {}"},
			},
			Timestamp: time.Now(),
		},
		{
			// Turn 2: assistant responds with analysis (no more tools)
			ID:      "msg_4",
			Role:    conversation.RoleAssistant,
			Content: "This is a minimal Go main package.",
			Metadata: map[string]any{
				"elapsed_time_ns": float64(3000000000),
			},
			Model:     "claude-opus-4-20250514",
			Timestamp: time.Now(),
		},
	}

	// Path 1: History load - each SDK message becomes a UI message
	historyMsgs := reconstructAllMessages(sdkMessages)

	// Path 2: Streaming - all assistant turns merged into one UI message
	streamingMsgs := simulateStreamingFromSDK(sdkMessages)

	// Count non-empty assistant messages in each path
	historyAssistantCount := 0
	historyEmptyCount := 0
	for _, msg := range historyMsgs {
		if msg.Role != "assistant" {
			continue
		}
		historyAssistantCount++
		if wouldRenderEmpty(&msg) {
			historyEmptyCount++
		}
	}

	streamingAssistantCount := 0
	streamingEmptyCount := 0
	for _, msg := range streamingMsgs {
		if msg.Role != "assistant" {
			continue
		}
		streamingAssistantCount++
		if wouldRenderEmpty(&msg) {
			streamingEmptyCount++
		}
	}

	t.Logf("History path:   %d assistant msgs (%d empty)", historyAssistantCount, historyEmptyCount)
	t.Logf("Streaming path: %d assistant msgs (%d empty)", streamingAssistantCount, streamingEmptyCount)

	// The streaming path should never produce empty messages
	if streamingEmptyCount > 0 {
		t.Errorf("Streaming path produced %d empty assistant messages - should be 0", streamingEmptyCount)
	}

	// The history path currently produces empty messages - this is the bug
	// When the fix is applied, change this to Errorf
	if historyEmptyCount > 0 {
		t.Logf("BUG CONFIRMED: History load path produces %d empty assistant messages that streaming does not", historyEmptyCount)
	}
}

// =============================================================================
// Helper functions
// =============================================================================

// wouldRenderEmpty returns true if an assistant message would render as an
// empty "█" block with no visible content after it.
func wouldRenderEmpty(msg *Message) bool {
	if msg.Role != "assistant" {
		return false // Only assistant messages use "█"
	}

	// Check OrderedBlocks path (primary rendering)
	if len(msg.OrderedBlocks) > 0 {
		for _, block := range msg.OrderedBlocks {
			switch block.Type {
			case "content":
				if strings.TrimSpace(block.GetContent()) != "" {
					return false // Has visible content
				}
			case "tool_call", "tool_result", "sub_agent_activity", "hook_execution":
				return false // These block types render their own UI
			case "thinking":
				if strings.TrimSpace(block.GetContent()) != "" {
					return false // Has thinking content
				}
			}
		}
		// All blocks are empty content or there are only empty blocks
		return true
	}

	// Check pre-rendered path
	if msg.IsPreRendered {
		return strings.TrimSpace(msg.Content) == ""
	}

	// Fallback path: no OrderedBlocks
	// Check all content sources
	if strings.TrimSpace(msg.GetContent()) != "" {
		return false
	}
	if strings.TrimSpace(msg.GetThinking()) != "" {
		return false
	}
	if len(msg.ToolCalls) > 0 {
		return false
	}
	if len(msg.ToolResults) > 0 {
		return false
	}
	if msg.BashResult != nil {
		return false
	}

	return true
}

// reconstructOrderedBlocks simulates the OrderedBlocks reconstruction from
// loadMessagesFromSDK (chat_state.go:920-976). This is the history-load path.
func reconstructOrderedBlocks(sdkMsg *conversation.Message) Message {
	uiMsg := Message{
		Role:      string(sdkMsg.Role),
		Content:   sdkMsg.Content,
		Timestamp: sdkMsg.Timestamp,
		Metadata:  sdkMsg.Metadata,
	}

	// Convert tool calls
	if len(sdkMsg.ToolCalls) > 0 {
		uiMsg.ToolCalls = make([]ToolCallDisplay, len(sdkMsg.ToolCalls))
		for i, tc := range sdkMsg.ToolCalls {
			uiMsg.ToolCalls[i] = ToolCallDisplay{
				ID:         tc.ID,
				Name:       tc.Name,
				Parameters: tc.Parameters,
			}
		}
	}

	// Convert tool results
	if len(sdkMsg.ToolResults) > 0 {
		uiMsg.ToolResults = make([]ToolResultDisplay, len(sdkMsg.ToolResults))
		for i, tr := range sdkMsg.ToolResults {
			errorMsg := ""
			if tr.Error != nil {
				errorMsg = tr.Error.Message
			}
			uiMsg.ToolResults[i] = ToolResultDisplay{
				CallID: tr.CallID,
				Output: tr.Output,
				Error:  errorMsg,
			}
		}
	}

	// Get thinking
	if sdkMsg.Thinking != "" {
		uiMsg.Thinking = sdkMsg.Thinking
	}

	// Reconstruct OrderedBlocks (same logic as loadMessagesFromSDK)
	if sdkMsg.Role == conversation.RoleAssistant {
		seq := 0
		uiMsg.OrderedBlocks = []MessageBlock{}

		if uiMsg.Thinking != "" {
			seq++
			uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
				Type:     "thinking",
				Content:  uiMsg.Thinking,
				Sequence: seq,
			})
		}

		for _, tc := range uiMsg.ToolCalls {
			seq++
			uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
				Type: "tool_call",
				ToolCall: &ToolCallDisplay{
					ID:         tc.ID,
					Name:       tc.Name,
					Parameters: tc.Parameters,
				},
				Sequence: seq,
			})

			for j := range uiMsg.ToolResults {
				tr := &uiMsg.ToolResults[j]
				if tr.CallID == tc.ID {
					tr.ToolName = tc.Name
					seq++
					uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
						Type:       "tool_result",
						ToolResult: tr,
						Sequence:   seq,
					})
					break
				}
			}
		}

		// Add content block last (same as loadMessagesFromSDK line 968)
		if uiMsg.Content != "" {
			seq++
			uiMsg.OrderedBlocks = append(uiMsg.OrderedBlocks, MessageBlock{
				Type:     "content",
				Content:  uiMsg.Content,
				Sequence: seq,
			})
		}
	}

	return uiMsg
}

// reconstructAllMessages simulates loadMessagesFromSDK for a full conversation.
func reconstructAllMessages(sdkMsgs []*conversation.Message) []Message {
	var messages []Message
	for _, sdkMsg := range sdkMsgs {
		uiMsg := reconstructOrderedBlocks(sdkMsg)
		messages = append(messages, uiMsg)
	}
	return messages
}

// streamStep represents a single update in the streaming path.
type streamStep struct {
	typ        string // "content", "thinking", "tool_call", "tool_result"
	content    string
	append     bool
	toolCall   *ToolCallDisplay
	toolResult *ToolResultDisplay
}

// simulateStreamingBuild simulates building a Message incrementally via
// the streaming path (as agentContentUpdateMsg etc. handlers do in app_update.go).
func simulateStreamingBuild(steps []streamStep) Message {
	msg := Message{
		Role: "assistant",
	}

	seq := 0
	for _, step := range steps {
		seq++
		switch step.typ {
		case "content":
			if step.append {
				msg.AppendContent(step.content)
			} else {
				msg.Content = step.content
				msg.contentBuilder = nil
			}

			// Update OrderedBlocks (same logic as agentContentUpdateMsg handler)
			if step.append && len(msg.OrderedBlocks) > 0 &&
				msg.OrderedBlocks[len(msg.OrderedBlocks)-1].Type == "content" {
				lastIdx := len(msg.OrderedBlocks) - 1
				msg.OrderedBlocks[lastIdx].AppendContent(step.content)
			} else if step.append {
				msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
					Type:     "content",
					Content:  step.content,
					Sequence: seq,
				})
			} else {
				hasContentBlock := false
				for i := range msg.OrderedBlocks {
					if msg.OrderedBlocks[i].Type == "content" {
						msg.OrderedBlocks[i].Content = step.content
						hasContentBlock = true
						break
					}
				}
				if !hasContentBlock {
					msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
						Type:     "content",
						Content:  step.content,
						Sequence: seq,
					})
				}
			}

		case "thinking":
			msg.Thinking = step.content
			msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
				Type:     "thinking",
				Content:  step.content,
				Sequence: seq,
			})

		case "tool_call":
			if step.toolCall != nil {
				msg.ToolCalls = append(msg.ToolCalls, *step.toolCall)
				msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
					Type:     "tool_call",
					ToolCall: step.toolCall,
					Sequence: seq,
				})
			}

		case "tool_result":
			if step.toolResult != nil {
				msg.ToolResults = append(msg.ToolResults, *step.toolResult)
				msg.OrderedBlocks = append(msg.OrderedBlocks, MessageBlock{
					Type:       "tool_result",
					ToolResult: step.toolResult,
					Sequence:   seq,
				})
			}
		}
	}

	return msg
}

// simulateStreamingFromSDK simulates how the TUI builds messages during
// streaming: all assistant turns are merged into a single UI message.
func simulateStreamingFromSDK(sdkMsgs []*conversation.Message) []Message {
	var messages []Message
	var currentAssistant *Message

	for _, sdkMsg := range sdkMsgs {
		switch sdkMsg.Role {
		case conversation.RoleUser:
			// Flush any pending assistant message
			if currentAssistant != nil {
				messages = append(messages, *currentAssistant)
				currentAssistant = nil
			}
			messages = append(messages, Message{
				Role:      "user",
				Content:   sdkMsg.Content,
				Timestamp: sdkMsg.Timestamp,
			})

		case conversation.RoleAssistant:
			if currentAssistant == nil {
				currentAssistant = &Message{
					Role:      "assistant",
					Timestamp: sdkMsg.Timestamp,
				}
			}

			// Merge content
			if sdkMsg.Content != "" {
				if currentAssistant.GetContent() != "" {
					currentAssistant.AppendContent("\n\n" + sdkMsg.Content)
				} else {
					currentAssistant.Content = sdkMsg.Content
				}
			}

			// Merge tool calls
			for _, tc := range sdkMsg.ToolCalls {
				tcd := ToolCallDisplay{
					ID:         tc.ID,
					Name:       tc.Name,
					Parameters: tc.Parameters,
				}
				currentAssistant.ToolCalls = append(currentAssistant.ToolCalls, tcd)
				currentAssistant.OrderedBlocks = append(currentAssistant.OrderedBlocks, MessageBlock{
					Type:     "tool_call",
					ToolCall: &tcd,
					Sequence: len(currentAssistant.OrderedBlocks) + 1,
				})
			}

			// Content block
			if sdkMsg.Content != "" {
				currentAssistant.OrderedBlocks = append(currentAssistant.OrderedBlocks, MessageBlock{
					Type:     "content",
					Content:  sdkMsg.Content,
					Sequence: len(currentAssistant.OrderedBlocks) + 1,
				})
			}

			// Copy completion metadata
			if sdkMsg.Model != "" {
				currentAssistant.Model = sdkMsg.Model
			}
			if sdkMsg.Metadata != nil {
				if _, ok := sdkMsg.Metadata["elapsed_time_ns"]; ok {
					currentAssistant.Metadata = sdkMsg.Metadata
					currentAssistant.IsComplete = true
				}
			}

		case conversation.RoleTool:
			// Tool results get merged into the current assistant message
			if currentAssistant != nil {
				for _, tr := range sdkMsg.ToolResults {
					errorMsg := ""
					if tr.Error != nil {
						errorMsg = tr.Error.Message
					}
					trd := ToolResultDisplay{
						CallID: tr.CallID,
						Output: tr.Output,
						Error:  errorMsg,
					}
					currentAssistant.ToolResults = append(currentAssistant.ToolResults, trd)
					currentAssistant.OrderedBlocks = append(currentAssistant.OrderedBlocks, MessageBlock{
						Type:       "tool_result",
						ToolResult: &trd,
						Sequence:   len(currentAssistant.OrderedBlocks) + 1,
					})
				}
			}
		}
	}

	// Flush final assistant message
	if currentAssistant != nil {
		messages = append(messages, *currentAssistant)
	}

	return messages
}

// defaultTheme returns a minimal theme for testing rendering functions.
func defaultTheme() Theme {
	return Theme{
		Primary:    "#7C3AED",
		PrimaryDim: "#5B21B6",
		Accent:     "#06B6D4",
		Success:    "#10B981",
		Warning:    "#F59E0B",
		Error:      "#EF4444",
		Info:       "#3B82F6",
		Text:       "#E5E7EB",
		TextDim:    "#9CA3AF",
		TextMuted:  "#6B7280",
		BG:         "#111827",
		BGLight:    "#1F2937",
		BGLighter:  "#374151",
		Border:     "#374151",
	}
}

// truncateStr truncates a string to maxLen with "..." suffix.
// (Duplicated here for test isolation.)
func truncateStrTest(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// Ensure the test file compiles even if unused helper exists
var _ = fmt.Sprintf
