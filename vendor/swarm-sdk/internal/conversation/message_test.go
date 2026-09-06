package conversation

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCloneCopiesOrderedBlocks(t *testing.T) {
	orig := &Message{
		Role:    RoleAssistant,
		Content: "hello",
		OrderedBlocks: []MessageBlock{
			{Type: BlockTypeContent, Content: "hello", Sequence: 0},
			{Type: BlockTypeToolCall, ToolCall: &ToolCall{ID: "t1", Name: "X"}, Sequence: 1},
		},
	}
	clone := orig.Clone()

	if !reflect.DeepEqual(clone.OrderedBlocks, orig.OrderedBlocks) {
		t.Fatalf("clone OrderedBlocks mismatch\n got: %v\nwant: %v", clone.OrderedBlocks, orig.OrderedBlocks)
	}
	// Mutating the clone's slice must not affect the original.
	clone.OrderedBlocks[0].Content = "mutated"
	if orig.OrderedBlocks[0].Content == "mutated" {
		t.Fatalf("clone shares backing array with original")
	}
}

func TestCloneNilOrderedBlocks(t *testing.T) {
	orig := &Message{Role: RoleUser, Content: "hi"}
	clone := orig.Clone()
	if clone.OrderedBlocks != nil {
		t.Fatalf("expected nil OrderedBlocks on clone, got %v", clone.OrderedBlocks)
	}
}

func TestCloneDoesNotAliasNestedMutableData(t *testing.T) {
	type customMetadata struct {
		Values []int
	}
	size := int64(3)
	orig := &Message{
		Metadata: map[string]any{
			"nested": map[string]any{"value": "original"},
			"typed":  map[string][]int{"values": {1, 2, 3}},
			"struct": customMetadata{Values: []int{1, 2, 3}},
		},
		A2A: &A2AMetadata{References: []Reference{{
			Metadata: map[string]any{"nested": map[string]any{"value": "original"}},
		}}},
		ToolCalls: []ToolCall{{
			Parameters: map[string]any{"path": "original.txt"},
		}},
		ToolResults: []ToolResult{{
			Error: &ToolError{Message: "original error"},
			Content: []ContentBlock{{
				Data:        []byte{1, 2, 3},
				Size:        &size,
				Annotations: map[string]any{"nested": map[string]any{"value": "original"}},
			}},
		}},
		OrderedBlocks: []MessageBlock{{
			ToolCall: &ToolCall{Parameters: map[string]any{"path": "block.txt"}},
		}},
		SubAgentActivity: []*SubAgentActivity{{
			Blocks: []MessageBlock{{
				ToolCall: &ToolCall{Parameters: map[string]any{"path": "subagent.txt"}},
			}},
		}},
	}

	clone := orig.Clone()
	clone.Metadata["nested"].(map[string]any)["value"] = "changed"
	clone.Metadata["typed"].(map[string][]int)["values"][0] = 9
	structValue := clone.Metadata["struct"].(customMetadata)
	structValue.Values[0] = 9
	clone.A2A.References[0].Metadata["nested"].(map[string]any)["value"] = "changed"
	clone.ToolCalls[0].Parameters["path"] = "changed.txt"
	clone.ToolResults[0].Error.Message = "changed error"
	clone.ToolResults[0].Content[0].Data[0] = 9
	*clone.ToolResults[0].Content[0].Size = 9
	clone.ToolResults[0].Content[0].Annotations["nested"].(map[string]any)["value"] = "changed"
	clone.OrderedBlocks[0].ToolCall.Parameters["path"] = "changed-block.txt"
	clone.SubAgentActivity[0].Blocks[0].ToolCall.Parameters["path"] = "changed-subagent.txt"

	if got := orig.Metadata["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("metadata was aliased: %v", got)
	}
	if got := orig.Metadata["typed"].(map[string][]int)["values"][0]; got != 1 {
		t.Fatalf("typed metadata was aliased: %v", got)
	}
	if got := orig.Metadata["struct"].(customMetadata).Values[0]; got != 1 {
		t.Fatalf("struct metadata was aliased: %v", got)
	}
	if got := orig.A2A.References[0].Metadata["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("A2A reference metadata was aliased: %v", got)
	}
	if got := orig.ToolCalls[0].Parameters["path"]; got != "original.txt" {
		t.Fatalf("tool parameters were aliased: %v", got)
	}
	if orig.ToolResults[0].Error.Message != "original error" ||
		orig.ToolResults[0].Content[0].Data[0] != 1 ||
		*orig.ToolResults[0].Content[0].Size != 3 {
		t.Fatalf("tool result was aliased: %#v", orig.ToolResults[0])
	}
	if got := orig.ToolResults[0].Content[0].Annotations["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("content annotations were aliased: %v", got)
	}
	if got := orig.OrderedBlocks[0].ToolCall.Parameters["path"]; got != "block.txt" {
		t.Fatalf("ordered block was aliased: %v", got)
	}
	if got := orig.SubAgentActivity[0].Blocks[0].ToolCall.Parameters["path"]; got != "subagent.txt" {
		t.Fatalf("top-level sub-agent activity was aliased: %v", got)
	}
}

func TestClonePreservesCyclesWithoutAliasingOriginal(t *testing.T) {
	originalCycle := map[string]any{"value": "original"}
	originalCycle["self"] = originalCycle
	original := &Message{Metadata: map[string]any{"cycle": originalCycle}}

	cloned := original.Clone()
	clonedCycle := cloned.Metadata["cycle"].(map[string]any)
	clonedSelf := clonedCycle["self"].(map[string]any)
	clonedSelf["value"] = "changed"

	if got := clonedCycle["value"]; got != "changed" {
		t.Fatalf("clone did not preserve cycle identity: %v", got)
	}
	if got := originalCycle["value"]; got != "original" {
		t.Fatalf("clone cycle aliases original: %v", got)
	}
}

// TestOrderedBlocksJSONRoundTrip marshals a Message with a diverse mix of
// ordered blocks — content, thinking, tool_call, tool_result, sub_agent —
// and verifies they deserialize identically. This guards against the
// persistence layer silently dropping OrderedBlocks on save/load, which
// would leave the TUI falling back to flat-field synthesis on reload.
func TestOrderedBlocksJSONRoundTrip(t *testing.T) {
	orig := &Message{
		Role:    RoleAssistant,
		Content: "hello world",
		OrderedBlocks: []MessageBlock{
			{Type: BlockTypeThinking, Content: "reasoning", Sequence: 0},
			{Type: BlockTypeContent, Content: "hello ", Sequence: 1},
			{
				Type:     BlockTypeToolCall,
				ToolCall: &ToolCall{ID: "call_1", Name: "Bash", Parameters: map[string]any{"cmd": "ls"}},
				Sequence: 2,
			},
			{
				Type:       BlockTypeToolResult,
				ToolResult: &ToolResult{CallID: "call_1", Name: "Bash", Output: "file.txt\n"},
				Sequence:   3,
			},
			{Type: BlockTypeContent, Content: "world", Sequence: 4},
			{
				Type: BlockTypeSubAgentActivity,
				SubAgentActivity: &SubAgentActivity{
					AgentID: "sub_1", AgentName: "search", Status: "complete",
					TaskInstruction: "look up X",
				},
				Sequence: 5,
			},
		},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var round Message
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(round.OrderedBlocks) != len(orig.OrderedBlocks) {
		t.Fatalf("block count: got %d, want %d", len(round.OrderedBlocks), len(orig.OrderedBlocks))
	}
	for i := range orig.OrderedBlocks {
		if !reflect.DeepEqual(round.OrderedBlocks[i], orig.OrderedBlocks[i]) {
			t.Errorf("block[%d] mismatch\n got:  %+v\n want: %+v", i, round.OrderedBlocks[i], orig.OrderedBlocks[i])
		}
	}
}

// TestOrderedBlocksEmptyOmitted confirms that a Message with nil
// OrderedBlocks does not emit the field at all — the `omitempty` tag
// keeps legacy serialized messages byte-identical to pre-Stage-1 output,
// so upgrading the SDK doesn't churn every stored conversation.
func TestOrderedBlocksEmptyOmitted(t *testing.T) {
	msg := &Message{Role: RoleUser, Content: "hi"}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); containsSubstr(got, `"ordered_blocks"`) {
		t.Fatalf("nil OrderedBlocks should be omitted; got: %s", got)
	}
}

func containsSubstr(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
