package agent

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/hosted"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

func TestPostExecuteToolProcessPreservesHostedMetadataInIntermediateUpdates(t *testing.T) {
	var captured ToolResultUpdate
	agent := &Agent{
		definition: &Definition{ID: "agent-test"},
		logger:     observability.NewNopLogger(),
	}
	agent.intermediateCallback = func(ctx context.Context, update IntermediateUpdate) error {
		toolUpdate, ok := update.(ToolResultUpdate)
		if ok {
			captured = toolUpdate
		}
		return nil
	}

	result := &tools.ToolResult{
		Output: "ok",
		Metadata: map[string]any{
			"task_class": string(hosted.TaskClassHeavyOutput),
		},
		Hosted: &hosted.ResultMetadata{
			TaskClass:   hosted.TaskClassHeavyOutput,
			HeavyOutput: true,
		},
	}

	execResult := agent.postExecuteToolProcess(
		context.Background(),
		conversation.ToolCall{ID: "call-1", Name: "demo"},
		result,
		nil,
	)
	if execResult == nil {
		t.Fatal("expected execution result")
	}
	if captured.Hosted == nil {
		t.Fatal("expected hosted metadata to be forwarded in ToolResultUpdate")
	}
	if captured.Hosted.TaskClass != hosted.TaskClassHeavyOutput {
		t.Fatalf("hosted task class=%q want %q", captured.Hosted.TaskClass, hosted.TaskClassHeavyOutput)
	}
	if captured.TaskClass != hosted.TaskClassHeavyOutput {
		t.Fatalf("task class=%q want %q", captured.TaskClass, hosted.TaskClassHeavyOutput)
	}
}

func newToolBoundingTestAgent() *Agent {
	return &Agent{
		definition: &Definition{ID: "tool-bound-test"},
		logger:     observability.NewNopLogger(),
	}
}

func TestPostExecuteToolProcessTruncatesOversizedText(t *testing.T) {
	agent := newToolBoundingTestAgent()
	input := "HEAD-MARKER\n" + strings.Repeat("x", MaxToolOutputChars) + "\nTAIL-MARKER"
	result := &tools.ToolResult{Output: input}

	processed := agent.postExecuteToolProcess(
		context.Background(),
		conversation.ToolCall{ID: "call-large", Name: "large"},
		result,
		nil,
	)
	if processed.err != nil {
		t.Fatalf("oversized result became an error: %v", processed.err)
	}
	if !strings.Contains(processed.result.Output, "HEAD-MARKER") {
		t.Fatalf("truncated output lost head: %q", processed.result.Output[:100])
	}
	if !strings.Contains(processed.result.Output, "TAIL-MARKER") {
		t.Fatal("truncated output lost tail")
	}
	if processed.result.Output == "TOOL RESULT TOO LARGE" ||
		strings.HasPrefix(processed.result.Output, "TOOL RESULT TOO LARGE -") {
		t.Fatal("oversized result was replaced by the old rejection payload")
	}
}

func TestPostExecuteToolProcessReportsElidedAmount(t *testing.T) {
	agent := newToolBoundingTestAgent()
	input := strings.Repeat("z", MaxToolOutputChars+1_000)
	processed := agent.postExecuteToolProcess(
		context.Background(),
		conversation.ToolCall{ID: "call-marker", Name: "large"},
		&tools.ToolResult{Output: input},
		nil,
	)
	elided := len(input) - (MaxToolOutputChars - 256)
	if !strings.Contains(processed.result.Output, strconv.Itoa(elided)+" chars elided") {
		t.Fatalf("output lacks truthful elision marker for %d chars", elided)
	}
}

func TestPostExecuteToolProcessLeavesOversizedImageResultUnchanged(t *testing.T) {
	agent := newToolBoundingTestAgent()
	input := strings.Repeat("i", MaxToolOutputChars+1)
	processed := agent.postExecuteToolProcess(
		context.Background(),
		conversation.ToolCall{ID: "call-image", Name: "image"},
		&tools.ToolResult{
			Output:  input,
			Content: []tools.ContentBlock{tools.ImageContent([]byte("image"), "image/png")},
		},
		nil,
	)
	if processed.result.Output != input {
		t.Fatal("image-containing result did not retain its size exemption")
	}
}
