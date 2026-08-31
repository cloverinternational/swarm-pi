package tools

import (
	"context"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/toolmetrics"
)

// probeTool is a minimal Tool used to verify that each execution path records
// exactly one measurement.
type probeTool struct {
	name    string
	out     string
	isErr   bool
	content []ContentBlock
}

func (p *probeTool) Name() string        { return p.name }
func (p *probeTool) Description() string { return "probe" }
func (p *probeTool) Parameters() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *probeTool) Execute(ctx context.Context, params map[string]any) (*ToolResult, error) {
	return &ToolResult{Output: p.out, IsError: p.isErr, Content: p.content}, nil
}

func statFor(t *testing.T, tool string) toolmetrics.Snapshot {
	t.Helper()
	for _, s := range toolmetrics.SnapshotAll() {
		if s.Tool == tool {
			return s
		}
	}
	t.Fatalf("no stats recorded for %q; recorded=%v", tool, toolmetrics.SnapshotAll())
	return toolmetrics.Snapshot{}
}

// The registry path must record exactly one call.
func TestRegistryPathRecordsOnce(t *testing.T) {
	toolmetrics.Reset()
	reg := NewSimpleRegistry(nil, nil)
	if err := reg.Register(&probeTool{name: "probe_reg", out: "hello"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "probe_reg", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := statFor(t, "probe_reg")
	if s.Calls != 1 {
		t.Fatalf("calls = %d, want exactly 1 (double-counting between registry and helper?)", s.Calls)
	}
	if s.OutputBytes != int64(len("hello")) {
		t.Fatalf("output bytes = %d, want 5", s.OutputBytes)
	}
}

// The parallel batch path bypasses SimpleRegistry.Execute, so it needs its own
// coverage; without it the heatmap would be blind to batched tool calls.
func TestParallelPathRecordsOnce(t *testing.T) {
	toolmetrics.Reset()
	reg := NewSimpleRegistry(nil, nil)
	if err := reg.Register(&probeTool{name: "probe_par", out: "xy"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	rt := NewOptimizedToolCallRuntime(reg, nil, nil, nil)
	res := rt.executeSingle(context.Background(), &ToolInvocation{Name: "probe_par"})
	if res.Error != nil {
		t.Fatalf("executeSingle: %v", res.Error)
	}
	s := statFor(t, "probe_par")
	if s.Calls != 1 {
		t.Fatalf("calls = %d, want 1", s.Calls)
	}
}

// Binary content must count toward payload size: images dominate the cost that
// history persistence and provider serialization actually pay.
func TestPayloadSizeCountsBinaryContent(t *testing.T) {
	r := &ToolResult{
		Output:  "abc",
		Content: []ContentBlock{{Text: "de", Data: []byte{1, 2, 3, 4}}},
	}
	if got := ResultPayloadSize(r); got != 3+2+4 {
		t.Fatalf("payload size = %d, want 9", got)
	}
	if got := ResultPayloadSize(nil); got != 0 {
		t.Fatalf("nil payload size = %d, want 0", got)
	}
}

// A tool that signals failure via IsError while returning a nil error must be
// counted as an error.
func TestIsErrorResultCountsResultFlag(t *testing.T) {
	toolmetrics.Reset()
	reg := NewSimpleRegistry(nil, nil)
	if err := reg.Register(&probeTool{name: "probe_err", out: "boom", isErr: true}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := reg.Execute(context.Background(), "probe_err", nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	s := statFor(t, "probe_err")
	if s.Errors != 1 {
		t.Fatalf("errors = %d, want 1 (IsError with nil error must count)", s.Errors)
	}
}
