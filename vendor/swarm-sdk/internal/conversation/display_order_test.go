package conversation

import (
	"reflect"
	"testing"
)

func content(seq int, text string) MessageBlock {
	return MessageBlock{Type: BlockTypeContent, Content: text, Sequence: seq}
}

func thinking(seq int, text string) MessageBlock {
	return MessageBlock{Type: BlockTypeThinking, Content: text, Sequence: seq}
}

func toolCall(seq int, id, name string) MessageBlock {
	return MessageBlock{
		Type:     BlockTypeToolCall,
		ToolCall: &ToolCall{ID: id, Name: name},
		Sequence: seq,
	}
}

func toolResult(seq int, callID, output string) MessageBlock {
	return MessageBlock{
		Type:       BlockTypeToolResult,
		ToolResult: &ToolResult{CallID: callID, Output: output},
		Sequence:   seq,
	}
}

func subAgentActivity(seq int, content string) MessageBlock {
	return MessageBlock{Type: BlockTypeSubAgentActivity, Content: content, Sequence: seq}
}

func hookExecution(seq int, content string) MessageBlock {
	return MessageBlock{Type: BlockTypeHookExecution, Content: content, Sequence: seq}
}

func blockSig(b MessageBlock) string {
	switch b.Type {
	case BlockTypeToolCall:
		if b.ToolCall != nil {
			return "call:" + b.ToolCall.ID
		}
	case BlockTypeToolResult:
		if b.ToolResult != nil {
			return "result:" + b.ToolResult.CallID
		}
	}
	return string(b.Type) + ":" + b.Content
}

func sigs(blocks []MessageBlock) []string {
	if len(blocks) == 0 {
		return nil
	}
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = blockSig(b)
	}
	return out
}

func TestOrderBlocksForDisplay(t *testing.T) {
	cases := []struct {
		name string
		in   []MessageBlock
		want []string
	}{
		{
			name: "empty",
			in:   nil,
			want: nil,
		},
		{
			name: "content only",
			in:   []MessageBlock{content(1, "a"), content(2, "b")},
			want: []string{"content:a", "content:b"},
		},
		{
			name: "streaming order thinking then content",
			in:   []MessageBlock{thinking(1, "t"), content(2, "a")},
			want: []string{"thinking:t", "content:a"},
		},
		{
			name: "non-streaming order content then thinking hoists",
			in:   []MessageBlock{content(1, "a"), thinking(2, "t")},
			want: []string{"thinking:t", "content:a"},
		},
		{
			name: "call then result in order",
			in:   []MessageBlock{toolCall(1, "t1", "X"), toolResult(2, "t1", "ok")},
			want: []string{"call:t1", "result:t1"},
		},
		{
			name: "parallel calls pair inline",
			in: []MessageBlock{
				toolCall(1, "t1", "X"),
				toolCall(2, "t2", "Y"),
				toolResult(3, "t1", "ok1"),
				toolResult(4, "t2", "ok2"),
			},
			want: []string{"call:t1", "result:t1", "call:t2", "result:t2"},
		},
		{
			name: "async-gated result splices back inline",
			in: []MessageBlock{
				content(1, "plan"),
				toolCall(2, "t1", "ExitPlanMode"),
				content(3, "cont1"),
				content(4, "cont2"),
				toolResult(29, "t1", "approved"),
			},
			want: []string{
				"content:plan",
				"call:t1",
				"result:t1",
				"content:cont1",
				"content:cont2",
			},
		},
		{
			name: "orphan result dropped",
			in: []MessageBlock{
				content(1, "a"),
				toolResult(2, "missing", "ghost"),
				content(3, "b"),
			},
			want: []string{"content:a", "content:b"},
		},
		{
			name: "multi turn thinking hoist is local",
			in: []MessageBlock{
				thinking(1, "t1"),
				content(2, "c1"),
				toolCall(3, "a", "X"),
				toolResult(4, "a", "ok"),
				content(5, "c2"),
				thinking(6, "t2"),
				content(7, "c3"),
			},
			want: []string{
				"thinking:t1",
				"content:c1",
				"call:a",
				"result:a",
				"thinking:t2",
				"content:c2",
				"content:c3",
			},
		},
		{
			name: "unsorted input sorts first",
			in: []MessageBlock{
				content(3, "c"),
				content(1, "a"),
				content(2, "b"),
			},
			want: []string{"content:a", "content:b", "content:c"},
		},
		{
			name: "sub_agent_activity passthrough",
			in: []MessageBlock{
				content(1, "a"),
				subAgentActivity(2, "sub-work"),
				content(3, "b"),
			},
			want: []string{"content:a", "sub_agent_activity:sub-work", "content:b"},
		},
		{
			name: "hook_execution passthrough",
			in: []MessageBlock{
				content(1, "a"),
				hookExecution(2, "post-commit"),
				content(3, "b"),
			},
			want: []string{"content:a", "hook_execution:post-commit", "content:b"},
		},
		{
			name: "mixed block types with sub_agent and hook",
			in: []MessageBlock{
				thinking(1, "t"),
				content(2, "a"),
				toolCall(3, "t1", "X"),
				subAgentActivity(4, "sub"),
				hookExecution(5, "hook"),
				toolResult(6, "t1", "ok"),
				content(7, "b"),
			},
			want: []string{
				"thinking:t",
				"content:a",
				"call:t1",
				"result:t1",
				"sub_agent_activity:sub",
				"hook_execution:hook",
				"content:b",
			},
		},
		{
			name: "unsorted mixed block types sort by sequence",
			in: []MessageBlock{
				toolResult(5, "t1", "ok"),
				content(1, "a"),
				toolCall(4, "t1", "X"),
				thinking(2, "t"),
				content(3, "b"),
			},
			want: []string{
				"thinking:t",
				"content:a",
				"content:b",
				"call:t1",
				"result:t1",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sigs(OrderBlocksForDisplay(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("OrderBlocksForDisplay\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func TestOrderBlocksForDisplayDoesNotMutateInput(t *testing.T) {
	in := []MessageBlock{
		content(3, "c"),
		toolCall(1, "t1", "X"),
		toolResult(2, "t1", "ok"),
	}
	snapshot := make([]MessageBlock, len(in))
	copy(snapshot, in)

	_ = OrderBlocksForDisplay(in)

	if !reflect.DeepEqual(in, snapshot) {
		t.Fatalf("input was mutated:\n got: %v\nwant: %v", in, snapshot)
	}
}

func TestMessageBlocksInDisplayOrder(t *testing.T) {
	var nilMsg *Message
	if got := nilMsg.BlocksInDisplayOrder(); got != nil {
		t.Fatalf("nil receiver: got %v, want nil", got)
	}

	m := &Message{OrderedBlocks: []MessageBlock{
		content(1, "plan"),
		toolCall(2, "t1", "ExitPlanMode"),
		content(3, "cont"),
		toolResult(10, "t1", "approved"),
	}}
	got := sigs(m.BlocksInDisplayOrder())
	want := []string{"content:plan", "call:t1", "result:t1", "content:cont"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BlocksInDisplayOrder\n got: %v\nwant: %v", got, want)
	}
}

func TestBlocksInDisplayOrderDerivesFromFlatFields(t *testing.T) {
	// Legacy / not-yet-upgraded provider path: OrderedBlocks empty, flat fields set.
	m := &Message{
		Role:     RoleAssistant,
		Thinking: "reasoning",
		Content:  "hello",
		ToolCalls: []ToolCall{
			{ID: "t1", Name: "X"},
			{ID: "t2", Name: "Y"},
		},
	}
	got := sigs(m.BlocksInDisplayOrder())
	want := []string{"thinking:reasoning", "content:hello", "call:t1", "call:t2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("derivation\n got: %v\nwant: %v", got, want)
	}
}

func TestBlocksInDisplayOrderPrefersOrderedBlocks(t *testing.T) {
	// When both are set, OrderedBlocks wins — flat fields are redundant accumulation
	// that the provider adapter emits alongside the ordered view.
	m := &Message{
		Content:  "flat-ignored",
		Thinking: "flat-ignored",
		OrderedBlocks: []MessageBlock{
			content(1, "from-ordered"),
		},
	}
	got := sigs(m.BlocksInDisplayOrder())
	want := []string{"content:from-ordered"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderedBlocks precedence\n got: %v\nwant: %v", got, want)
	}
}

func TestBlocksInDisplayOrderDeriveEmptyMessage(t *testing.T) {
	m := &Message{Role: RoleAssistant}
	if got := m.BlocksInDisplayOrder(); got != nil {
		t.Fatalf("empty message: got %v, want nil", got)
	}
}
