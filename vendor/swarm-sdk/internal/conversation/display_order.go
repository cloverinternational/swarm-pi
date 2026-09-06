package conversation

import "sort"

// OrderBlocksForDisplay returns blocks in rendering order.
//
// Pipeline:
//  1. stable sort by Sequence (arrival order),
//  2. local thinking hoist: any thinking block that arrived after a run of
//     content blocks (non-streaming providers) is moved before them,
//  3. each tool_result is spliced in immediately after its matching tool_call,
//  4. orphan tool_results (no matching call in the input) are dropped.
//
// The input slice is not mutated; a new slice is returned.
func OrderBlocksForDisplay(blocks []MessageBlock) []MessageBlock {
	if len(blocks) == 0 {
		return nil
	}

	sorted := make([]MessageBlock, len(blocks))
	copy(sorted, blocks)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Sequence < sorted[j].Sequence
	})

	hoisted := make([]MessageBlock, 0, len(sorted))
	for _, b := range sorted {
		if b.Type != BlockTypeThinking {
			hoisted = append(hoisted, b)
			continue
		}
		insertAt := len(hoisted)
		for insertAt > 0 && hoisted[insertAt-1].Type == BlockTypeContent {
			insertAt--
		}
		hoisted = append(hoisted, MessageBlock{})
		copy(hoisted[insertAt+1:], hoisted[insertAt:])
		hoisted[insertAt] = b
	}

	resultByCallID := make(map[string]MessageBlock, len(hoisted)/2)
	for _, b := range hoisted {
		if b.Type == BlockTypeToolResult && b.ToolResult != nil {
			resultByCallID[b.ToolResult.CallID] = b
		}
	}

	out := make([]MessageBlock, 0, len(hoisted))
	inserted := make(map[string]bool, len(resultByCallID))
	for _, b := range hoisted {
		switch {
		case b.Type == BlockTypeToolResult && b.ToolResult != nil:
			// Already spliced after its call, or orphan — drop either way.
		case b.Type == BlockTypeToolCall && b.ToolCall != nil:
			out = append(out, b)
			if r, ok := resultByCallID[b.ToolCall.ID]; ok && !inserted[b.ToolCall.ID] {
				out = append(out, r)
				inserted[b.ToolCall.ID] = true
			}
		default:
			out = append(out, b)
		}
	}
	return out
}

// BlocksInDisplayOrder returns m.OrderedBlocks transformed for rendering.
// When OrderedBlocks is empty the blocks are synthesized from flat fields
// (Thinking, Content, ToolCalls, ToolResults) so legacy messages and
// not-yet-upgraded providers still render correctly. See OrderBlocksForDisplay
// for the full ordering contract. Safe on nil receivers.
func (m *Message) BlocksInDisplayOrder() []MessageBlock {
	if m == nil {
		return nil
	}
	blocks := m.OrderedBlocks
	if len(blocks) == 0 {
		blocks = deriveBlocks(m)
	}
	return OrderBlocksForDisplay(blocks)
}

// deriveBlocks synthesizes a block list from a Message's flat fields.
// Sequence numbers reflect arrival order: thinking → 0, content → 1,
// tool_call[i] → 2+i, tool_result[j] → 2+len(tool_calls)+j.
func deriveBlocks(m *Message) []MessageBlock {
	if m == nil {
		return nil
	}
	count := len(m.ToolCalls) + len(m.ToolResults)
	if m.Thinking != "" {
		count++
	}
	if m.Content != "" {
		count++
	}
	if count == 0 {
		return nil
	}

	blocks := make([]MessageBlock, 0, count)
	seq := 0
	if m.Thinking != "" {
		blocks = append(blocks, MessageBlock{
			Type:     BlockTypeThinking,
			Content:  m.Thinking,
			Sequence: seq,
		})
		seq++
	}
	if m.Content != "" {
		blocks = append(blocks, MessageBlock{
			Type:     BlockTypeContent,
			Content:  m.Content,
			Sequence: seq,
		})
		seq++
	}
	for i := range m.ToolCalls {
		tc := m.ToolCalls[i]
		blocks = append(blocks, MessageBlock{
			Type:     BlockTypeToolCall,
			ToolCall: &tc,
			Sequence: seq,
		})
		seq++
	}
	for j := range m.ToolResults {
		tr := m.ToolResults[j]
		blocks = append(blocks, MessageBlock{
			Type:       BlockTypeToolResult,
			ToolResult: &tr,
			Sequence:   seq,
		})
		seq++
	}
	return blocks
}
