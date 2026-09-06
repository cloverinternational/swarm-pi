package chat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestStreamingRenderingOrder reproduces the ACTUAL streaming behavior where
// tool_call updates arrive BEFORE content updates from the LLM API.
//
// This test simulates the real order of events during streaming:
// 1. LLM sends tool_call (triggers immediately)
// 2. LLM sends content (text appears after tool execution starts)
//
// Expected: Content should render BEFORE tool_call (logical order)
// Actual: tool_call gets sequence=1, content gets sequence=2 (arrival order)
func TestStreamingRenderingOrder(t *testing.T) {
	app := &App{
		messages:             []Message{},
		updateQueue:          make(chan tea.Msg, 10000),
		globalUpdateSequence: 0,
	}

	// Add an assistant message
	app.messages = append(app.messages, Message{
		Role:          "assistant",
		Content:       "",
		OrderedBlocks: []MessageBlock{},
	})
	messageIdx := len(app.messages) - 1

	// SIMULATE STREAMING: This is the order updates arrive from the LLM API
	t.Log("=== Simulating Streaming Updates ===")

	// Step 1: Tool call arrives FIRST (this is how Anthropic API works)
	app.globalUpdateSequence++
	toolCallSeq := app.globalUpdateSequence
	t.Logf("Update arrives: tool_call (bash) - assigned sequence=%d", toolCallSeq)

	app.messages[messageIdx].OrderedBlocks = append(app.messages[messageIdx].OrderedBlocks,
		MessageBlock{
			Type: "tool_call",
			ToolCall: &ToolCallDisplay{
				ID:         "toolu_01Q1EYLSWxV8skTvpxp2o9GK",
				Name:       "bash",
				Parameters: map[string]any{"command": "echo 1"},
			},
			Sequence: toolCallSeq,
		},
	)

	// Step 2: Content arrives SECOND (after tool call metadata)
	app.globalUpdateSequence++
	contentSeq := app.globalUpdateSequence
	t.Logf("Update arrives: content ('Hi! Testing...') - assigned sequence=%d", contentSeq)

	app.messages[messageIdx].OrderedBlocks = append(app.messages[messageIdx].OrderedBlocks,
		MessageBlock{
			Type:     "content",
			Content:  "Hi! Testing bash command and message rendering.",
			Sequence: contentSeq,
		},
	)

	t.Log("\n=== Current Block State (insertion order) ===")
	for i, block := range app.messages[messageIdx].OrderedBlocks {
		t.Logf("  Block[%d]: type=%s, sequence=%d", i, block.Type, block.Sequence)
	}

	// This is the problem! Blocks are in arrival order, not logical order:
	// Block[0]: type=tool_call, sequence=1
	// Block[1]: type=content, sequence=2

	if len(app.messages[messageIdx].OrderedBlocks) != 2 {
		t.Fatalf("Expected 2 blocks, got %d", len(app.messages[messageIdx].OrderedBlocks))
	}

	if app.messages[messageIdx].OrderedBlocks[0].Type != "tool_call" {
		t.Errorf("Expected first block to be tool_call (arrival order), got %s",
			app.messages[messageIdx].OrderedBlocks[0].Type)
	}

	if app.messages[messageIdx].OrderedBlocks[1].Type != "content" {
		t.Errorf("Expected second block to be content (arrival order), got %s",
			app.messages[messageIdx].OrderedBlocks[1].Type)
	}

	// NOW TEST RENDERING: The rendering code sorts by sequence
	t.Log("\n=== Rendering (with sort) ===")
	blocks := make([]MessageBlock, len(app.messages[messageIdx].OrderedBlocks))
	copy(blocks, app.messages[messageIdx].OrderedBlocks)
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Sequence < blocks[j].Sequence
	})

	t.Log("After sorting by sequence:")
	for i, block := range blocks {
		t.Logf("  Block[%d]: type=%s, sequence=%d", i, block.Type, block.Sequence)
	}

	// After sort, order is STILL WRONG because sequence numbers preserve arrival order!
	// Block[0]: type=tool_call, sequence=1  (tool arrived first)
	// Block[1]: type=content, sequence=2    (content arrived second)

	if blocks[0].Type != "tool_call" {
		t.Errorf("After sort: Expected first block to be tool_call (seq=1), got %s", blocks[0].Type)
	}

	if blocks[1].Type != "content" {
		t.Errorf("After sort: Expected second block to be content (seq=2), got %s", blocks[1].Type)
	}

	t.Log("\n=== PROBLEM IDENTIFIED ===")
	t.Log("❌ Rendering shows tool_call BEFORE content")
	t.Log("✓ Expected: Hi! Testing... (content) THEN ● bash (tool)")
	t.Log("✗ Actual:   ● bash (tool) THEN Hi! Testing... (content)")
	t.Log("")
	t.Log("Root cause: Sequence numbers are assigned based on LLM API arrival order,")
	t.Log("            not logical/chronological order for rendering.")
}

// TestRenderComparisonWithJSON creates a 1:1 reproduction using actual conversation data
func TestRenderComparisonWithJSON(t *testing.T) {
	// This is the actual data from our conversation (from debug_inspect)
	conversationJSON := `{
  "messages": [
    {
      "role": "assistant",
      "content": "Hi! Testing bash command and message rendering.",
      "ordered_blocks": [
        {
          "type": "tool_call",
          "sequence": 1,
          "tool_call": {
            "id": "toolu_01Q1EYLSWxV8skTvpxp2o9GK",
            "name": "bash",
            "parameters": {"command": "echo 1"}
          }
        },
        {
          "type": "content",
          "sequence": 2,
          "content": "Hi! Testing bash command and message rendering."
        }
      ]
    }
  ]
}`

	// Parse the JSON
	var data struct {
		Messages []struct {
			Role          string `json:"role"`
			Content       string `json:"content"`
			OrderedBlocks []struct {
				Type     string `json:"type"`
				Sequence int    `json:"sequence"`
				Content  string `json:"content,omitempty"`
				ToolCall *struct {
					ID         string         `json:"id"`
					Name       string         `json:"name"`
					Parameters map[string]any `json:"parameters"`
				} `json:"tool_call,omitempty"`
			} `json:"ordered_blocks"`
		} `json:"messages"`
	}

	if err := json.Unmarshal([]byte(conversationJSON), &data); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	t.Log("=== Actual Conversation Data ===")
	msg := data.Messages[0]
	t.Logf("Message: role=%s", msg.Role)
	t.Logf("Content: %s", msg.Content)
	t.Log("\nBlocks (as stored):")
	for i, block := range msg.OrderedBlocks {
		t.Logf("  [%d] type=%s seq=%d", i, block.Type, block.Sequence)
	}

	// Test rendering simulation
	t.Log("\n=== Simulating Rendering ===")

	// This is what the rendering code does (from app.go:6783)
	type renderBlock struct {
		Type     string
		Sequence int
		Content  string
		ToolName string
	}

	var blocks []renderBlock
	for _, b := range msg.OrderedBlocks {
		rb := renderBlock{
			Type:     b.Type,
			Sequence: b.Sequence,
		}
		if b.Type == "content" {
			rb.Content = b.Content
		} else if b.Type == "tool_call" && b.ToolCall != nil {
			rb.ToolName = b.ToolCall.Name
		}
		blocks = append(blocks, rb)
	}

	// Sort by sequence (what app.go:6783 does)
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Sequence < blocks[j].Sequence
	})

	t.Log("After sorting by sequence:")
	var renderedOutput []string
	for i, block := range blocks {
		var display string
		if block.Type == "content" {
			display = fmt.Sprintf("    ▌ %s", block.Content)
		} else if block.Type == "tool_call" {
			display = fmt.Sprintf("        ● %s", block.ToolName)
		}
		renderedOutput = append(renderedOutput, display)
		t.Logf("  [%d] %s", i, display)
	}

	// Verify the bug
	expectedOrder := []string{"content", "tool_call"}
	actualOrder := []string{blocks[0].Type, blocks[1].Type}

	if actualOrder[0] != expectedOrder[0] || actualOrder[1] != expectedOrder[1] {
		t.Logf("\n❌ RENDERING BUG CONFIRMED")
		t.Logf("Expected order: %v", expectedOrder)
		t.Logf("Actual order:   %v", actualOrder)
	} else {
		t.Logf("\n✓ Rendering order is correct")
	}

	t.Log("\n=== Visual Comparison ===")
	t.Log("Expected rendering:")
	t.Log("    ▌ Hi! Testing bash command and message rendering.")
	t.Log("        ● bash")
	t.Log("")
	t.Log("Actual rendering (from sequence order):")
	t.Log(strings.Join(renderedOutput, "\n"))
}

// TestStreamingVsNonStreaming compares streaming vs non-streaming behavior
func TestStreamingVsNonStreaming(t *testing.T) {
	t.Log("=== Streaming Mode (Real-time updates) ===")

	// Streaming: Updates arrive as they're generated by LLM
	streaming := []struct {
		updateType string
		sequence   int
	}{
		{"tool_call", 1},   // Tool metadata arrives first
		{"content", 2},     // Content arrives second
		{"tool_result", 3}, // Tool result arrives last
	}

	t.Log("Update arrival order:")
	for _, update := range streaming {
		t.Logf("  %d. %s", update.sequence, update.updateType)
	}

	t.Log("\n=== Non-Streaming Mode (Batch response) ===")

	// Non-streaming: All content arrives at once, can be ordered logically
	nonStreaming := []struct {
		logicalOrder int
		blockType    string
	}{
		{1, "content"},     // Logical: user sees text first
		{2, "tool_call"},   // Then tool call
		{3, "tool_result"}, // Then result
	}

	t.Log("Logical rendering order:")
	for _, block := range nonStreaming {
		t.Logf("  %d. %s", block.logicalOrder, block.blockType)
	}

	t.Log("\n=== The Issue ===")
	t.Log("Streaming mode assigns sequences based on API arrival order,")
	t.Log("but rendering should show logical/chronological order.")
	t.Log("")
	t.Log("Solution options:")
	t.Log("1. Assign sequence based on logical order, not arrival order")
	t.Log("2. Use block type priority when rendering (content < tool_call < tool_result)")
	t.Log("3. Track both 'arrival sequence' and 'display sequence' separately")
}

// TestProposedFix demonstrates how the fix should work
func TestProposedFix(t *testing.T) {
	t.Log("=== Proposed Fix: Type-Based Display Priority ===")

	type BlockWithPriority struct {
		Type            string
		Sequence        int
		DisplayPriority int // New: overrides sequence for rendering
	}

	// Simulate the buggy current state
	blocks := []BlockWithPriority{
		{Type: "tool_call", Sequence: 1, DisplayPriority: 0},   // arrives first
		{Type: "content", Sequence: 2, DisplayPriority: 0},     // arrives second
		{Type: "tool_result", Sequence: 3, DisplayPriority: 0}, // arrives third
	}

	t.Log("Current (buggy) - sorted by sequence:")
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].Sequence < blocks[j].Sequence
	})
	for i, b := range blocks {
		t.Logf("  [%d] %s (seq=%d)", i, b.Type, b.Sequence)
	}

	// Apply fix: assign display priorities based on type
	typeToDisplayPriority := map[string]int{
		"thinking":    0, // Always first (if enabled)
		"content":     1, // User-visible text
		"tool_call":   2, // Tool invocation
		"tool_result": 3, // Tool output
	}

	for i := range blocks {
		blocks[i].DisplayPriority = typeToDisplayPriority[blocks[i].Type]
	}

	t.Log("\nProposed fix - sorted by display priority:")
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].DisplayPriority != blocks[j].DisplayPriority {
			return blocks[i].DisplayPriority < blocks[j].DisplayPriority
		}
		// Fallback to sequence for same-priority blocks
		return blocks[i].Sequence < blocks[j].Sequence
	})

	for i, b := range blocks {
		t.Logf("  [%d] %s (seq=%d, priority=%d)", i, b.Type, b.Sequence, b.DisplayPriority)
	}

	// Verify fix works
	expectedOrder := []string{"content", "tool_call", "tool_result"}
	for i, expected := range expectedOrder {
		if blocks[i].Type != expected {
			t.Errorf("Expected block %d to be %s, got %s", i, expected, blocks[i].Type)
		}
	}

	t.Log("\n✓ Fix verified: Blocks now render in logical order")
	t.Log("\nImplementation location:")
	t.Log("  File: internal/chat/app.go")
	t.Log("  Function: updateViewportContent()")
	t.Log("  Line: ~6783 (the sort.Slice call)")
}

// TestDisplayPrioritySort verifies that the actual getDisplayPriority() method
// is used for sorting, fixing the streaming order bug.
func TestDisplayPrioritySort(t *testing.T) {
	// Create blocks in the order they arrive from the API (wrong display order)
	blocks := []MessageBlock{
		{Type: "tool_call", Sequence: 1, ToolCall: &ToolCallDisplay{Name: "bash"}},
		{Type: "content", Content: "Hello!", Sequence: 2},
		{Type: "thinking", Content: "Let me think...", Sequence: 0},
		{Type: "tool_result", Sequence: 3, ToolResult: &ToolResultDisplay{CallID: "call_1", Output: "done"}},
	}

	t.Log("Before sorting (API arrival order):")
	for i, b := range blocks {
		t.Logf("  [%d] type=%s seq=%d priority=%d", i, b.Type, b.Sequence, b.GetDisplayPriority())
	}

	// Sort using display priority (the actual fix applied in app.go)
	sort.Slice(blocks, func(i, j int) bool {
		pi, pj := blocks[i].GetDisplayPriority(), blocks[j].GetDisplayPriority()
		if pi != pj {
			return pi < pj
		}
		return blocks[i].Sequence < blocks[j].Sequence
	})

	t.Log("\nAfter sorting (display priority order):")
	for i, b := range blocks {
		t.Logf("  [%d] type=%s seq=%d priority=%d", i, b.Type, b.Sequence, b.GetDisplayPriority())
	}

	// Verify the expected order
	expectedOrder := []string{"thinking", "content", "tool_call", "tool_result"}
	for i, expected := range expectedOrder {
		if blocks[i].Type != expected {
			t.Errorf("Block %d: expected type=%s, got type=%s", i, expected, blocks[i].Type)
		}
	}

	t.Log("\n✓ Display priority sorting works correctly")
	t.Log("  thinking (priority 0) → content (priority 1) → tool_call (priority 2) → tool_result (priority 3)")
}
