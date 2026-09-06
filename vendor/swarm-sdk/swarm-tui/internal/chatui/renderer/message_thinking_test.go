package renderer

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/theme"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chatui/types"
)

func TestAssistantThinkingScalarIsFallbackForOrderedBlocks(t *testing.T) {
	renderer := NewMessageRenderer(theme.DefaultTheme(), 100)
	renderer.SetShowThinking(true)

	tests := []struct {
		name   string
		blocks []types.MessageBlock
	}{
		{name: "ordered thinking renders once", blocks: []types.MessageBlock{{Type: types.BlockThinking, Content: "reason carefully", Sequence: 1}}},
		{name: "scalar fallback renders once"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := renderer.Render(types.Message{Role: "assistant", Thinking: "reason carefully", OrderedBlocks: tt.blocks}, false)
			if count := strings.Count(strings.Join(lines, "\n"), "Thinking"); count != 1 {
				t.Fatalf("thinking header count = %d, want 1", count)
			}
		})
	}
}
