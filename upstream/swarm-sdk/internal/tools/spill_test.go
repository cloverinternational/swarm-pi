package tools

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func spillTestContext(t *testing.T, cfg ResultSpillConfig) context.Context {
	t.Helper()
	ctx := WithOwnerInfo(context.Background(), "agent", "user", "conversation")
	ctx = WithToolCallID(ctx, "call-1")
	cfg.Directory = t.TempDir()
	return WithResultSpillConfig(ctx, cfg)
}

func TestSpillToolResultUnderThresholdUntouched(t *testing.T) {
	ctx := spillTestContext(t, ResultSpillConfig{ThresholdBytes: 32})
	result := NewToolResult("small result")

	got := SpillToolResult(ctx, "websearch", result, nil)
	if got.Output != "small result" || got.Content[0].Text != "small result" {
		t.Fatalf("under-threshold result changed: %#v", got)
	}
	if got.Metadata["truncated"] != nil {
		t.Fatal("under-threshold result marked truncated")
	}
}

func TestSpillToolResultPersistsCompleteOutput(t *testing.T) {
	ctx := spillTestContext(t, ResultSpillConfig{ThresholdBytes: 512})
	full := "HEAD\n" + strings.Repeat("payload-", 300) + "\nTAIL"
	result := NewToolResult(full)

	got := SpillToolResult(ctx, "websearch", result, nil)
	path, _ := got.Metadata["full_output_path"].(string)
	if path == "" {
		t.Fatal("missing full_output_path")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spill: %v", err)
	}
	if string(data) != full {
		t.Fatal("spill file does not contain the complete original output")
	}
	if !strings.Contains(got.Output, "HEAD") || !strings.Contains(got.Output, "TAIL") ||
		!strings.Contains(got.Output, "[output truncated:") ||
		!strings.Contains(got.Output, "full output: "+path) {
		t.Fatalf("bad excerpt/pointer: %q", got.Output)
	}
	if got.Content[0].Text != got.Output {
		t.Fatal("rich text block retained the oversized output")
	}
}

func TestSpillToolResultErrorsNeverSpill(t *testing.T) {
	ctx := spillTestContext(t, ResultSpillConfig{ThresholdBytes: 8})
	full := strings.Repeat("failure detail ", 100)
	result := NewErrorResult(errors.New(full))

	got := SpillToolResult(ctx, "websearch", result, nil)
	if got.Output != full {
		t.Fatal("error result was truncated")
	}
	entries, err := os.ReadDir(ctx.Value(resultSpillConfigKey{}).(ResultSpillConfig).Directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("error result created a spill file")
	}
}

func TestSpillToolResultPerToolThresholdOverride(t *testing.T) {
	ctx := spillTestContext(t, ResultSpillConfig{
		ThresholdBytes: 8,
		ToolThresholds: map[string]int{"WebSearch": 4096},
	})
	result := NewToolResult(strings.Repeat("x", 1024))

	got := SpillToolResult(ctx, "websearch", result, nil)
	if got.Metadata["truncated"] != nil {
		t.Fatal("case-insensitive per-tool threshold override was ignored")
	}
}

func TestSpillToolResultToolOptOut(t *testing.T) {
	ctx := spillTestContext(t, ResultSpillConfig{
		ThresholdBytes: 8,
		ToolThresholds: map[string]int{"custom": -1},
	})
	full := strings.Repeat("x", 1024)
	got := SpillToolResult(ctx, "custom", NewToolResult(full), nil)
	if got.Output != full {
		t.Fatal("opted-out tool result was truncated")
	}
}

func TestRegistryExecutionAppliesSpillLayer(t *testing.T) {
	ctx := spillTestContext(t, ResultSpillConfig{ThresholdBytes: 128})
	reg := NewSimpleRegistry(nil, nil)
	if err := reg.Register(&probeTool{name: "large_probe", out: strings.Repeat("z", 2048)}); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, err := reg.Execute(ctx, "large_probe", nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.Metadata["full_output_path"] == nil || !strings.Contains(got.Output, "[output truncated:") {
		t.Fatal("SimpleRegistry execution bypassed the shared spill layer")
	}
}
