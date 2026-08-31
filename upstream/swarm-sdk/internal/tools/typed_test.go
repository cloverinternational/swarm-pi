package tools_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ── test fixtures ────────────────────────────────────────────────────────────

type echoParams struct {
	Message string `json:"message"`
	Repeat  int    `json:"repeat"`
}

type echoTool struct{ tools.BaseTool }

func (t *echoTool) Name() string        { return "echo" }
func (t *echoTool) Description() string { return "Echoes a message N times" }
func (t *echoTool) Parameters() any {
	return tools.SchemaFor[echoParams]()
}
func (t *echoTool) Run(_ context.Context, p echoParams) (*tools.ToolResult, error) {
	if p.Message == "" {
		return nil, fmt.Errorf("message is required")
	}
	var out strings.Builder
	n := p.Repeat
	if n <= 0 {
		n = 1
	}
	for i := 0; i < n; i++ {
		if i > 0 {
			out.WriteString(" ")
		}
		out.WriteString(p.Message)
	}
	return tools.NewToolResult(out.String()), nil
}

// ── Typed[P] adapter ────────────────────────────────────────────────────────

func TestTyped_BasicRoundtrip(t *testing.T) {
	tool := tools.Typed[echoParams](&echoTool{})
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"message": "hello",
		"repeat":  float64(3), // LLM sends numbers as float64
	})

	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output != "hello hello hello" {
		t.Errorf("output = %q, want %q", result.Output, "hello hello hello")
	}
}

func TestTyped_IntFromFloat64(t *testing.T) {
	// LLM always sends numbers as float64 in map[string]any.
	// The adapter must convert float64 → int correctly.
	tool := tools.Typed[echoParams](&echoTool{})
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"message": "x",
		"repeat":  float64(5),
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output != "x x x x x" {
		t.Errorf("output = %q, want %q", result.Output, "x x x x x")
	}
}

func TestTyped_MissingOptionalField(t *testing.T) {
	// Omitting an optional field should use the zero value, not error.
	tool := tools.Typed[echoParams](&echoTool{})
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]any{
		"message": "hi",
		// repeat omitted → zero value → defaults to 1 in Run()
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output != "hi" {
		t.Errorf("output = %q, want %q", result.Output, "hi")
	}
}

func TestTyped_RunErrorPropagates(t *testing.T) {
	tool := tools.Typed[echoParams](&echoTool{})
	ctx := context.Background()

	_, err := tool.Execute(ctx, map[string]any{
		"message": "", // Run() returns error for empty message
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTyped_NameDescriptionParameters(t *testing.T) {
	tool := tools.Typed[echoParams](&echoTool{})

	if tool.Name() != "echo" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "echo")
	}
	if tool.Description() != "Echoes a message N times" {
		t.Errorf("Description() mismatch")
	}
	if tool.Parameters() == nil {
		t.Error("Parameters() returned nil")
	}
}

func TestTyped_SatisfiesToolInterface(t *testing.T) {
	var v any = tools.Typed[echoParams](&echoTool{})
	if _, ok := v.(tools.Tool); !ok {
		t.Fatal("Typed[...] does not satisfy tools.Tool")
	}
}

func TestTyped_EmptyParams(t *testing.T) {
	tool := tools.Typed[echoParams](&echoTool{})
	ctx := context.Background()

	// nil map should decode to zero-value struct, then Run() returns an error.
	// It must not panic, and it must surface an error (echoTool.Run rejects empty input).
	_, err := tool.Execute(ctx, nil)
	if err == nil {
		t.Error("expected an error executing with nil params, got nil")
	}
}

// ── Optional interface forwarding ────────────────────────────────────────────

type readOnlyEcho struct{ tools.BaseTool }

func (t *readOnlyEcho) Name() string        { return "echo_ro" }
func (t *readOnlyEcho) Description() string { return "read-only echo" }
func (t *readOnlyEcho) Parameters() any     { return tools.SchemaFor[echoParams]() }
func (t *readOnlyEcho) Run(_ context.Context, p echoParams) (*tools.ToolResult, error) {
	return tools.NewToolResult(p.Message), nil
}
func (t *readOnlyEcho) IsIdempotent() bool     { return true }
func (t *readOnlyEcho) SupportsParallel() bool { return true }

func TestTyped_ForwardsIdempotent(t *testing.T) {
	tool := tools.Typed[echoParams](&readOnlyEcho{})

	type idempotent interface{ IsIdempotent() bool }
	if i, ok := tool.(idempotent); !ok {
		t.Error("adapter should forward IsIdempotent")
	} else if !i.IsIdempotent() {
		t.Error("IsIdempotent() should return true")
	}
}

func TestTyped_ForwardsParallel(t *testing.T) {
	tool := tools.Typed[echoParams](&readOnlyEcho{})

	type parallel interface{ SupportsParallel() bool }
	if p, ok := tool.(parallel); !ok {
		t.Error("adapter should forward SupportsParallel")
	} else if !p.SupportsParallel() {
		t.Error("SupportsParallel() should return true")
	}
}

// ── TypedStreamingTool[P] routing ───────────────────────────────────────────

// streamingEcho implements TypedStreamingTool[echoParams].
type streamingEcho struct{ tools.BaseTool }

func (t *streamingEcho) Name() string        { return "streaming_echo" }
func (t *streamingEcho) Description() string { return "streaming echo" }
func (t *streamingEcho) Parameters() any     { return tools.SchemaFor[echoParams]() }
func (t *streamingEcho) Run(_ context.Context, p echoParams) (*tools.ToolResult, error) {
	return tools.NewToolResult(p.Message), nil
}
func (t *streamingEcho) RunStreaming(_ context.Context, p echoParams, onOutput func(string, string)) (*tools.ToolResult, error) {
	// Emit the message word by word.
	onOutput(p.Message, "stdout")
	return tools.NewToolResult(p.Message), nil
}

// Compile-time check: *streamingEcho satisfies TypedStreamingTool[echoParams].
var _ tools.TypedStreamingTool[echoParams] = (*streamingEcho)(nil)

func TestTyped_StreamingRouting(t *testing.T) {
	tool := tools.Typed[echoParams](&streamingEcho{})
	ctx := context.Background()

	// Verify the adapter exposes ExecuteStreaming.
	type streaming interface {
		ExecuteStreaming(context.Context, map[string]any, func(string, string)) (*tools.ToolResult, error)
	}
	st, ok := tool.(streaming)
	if !ok {
		t.Fatal("adapter should expose ExecuteStreaming for TypedStreamingTool")
	}

	// Verify the typed params are decoded before RunStreaming is called.
	var received []string
	result, err := st.ExecuteStreaming(ctx, map[string]any{
		"message": "hello-stream",
		"repeat":  float64(1),
	}, func(chunk, stream string) {
		received = append(received, chunk)
	})
	if err != nil {
		t.Fatalf("ExecuteStreaming error: %v", err)
	}
	if result.Output != "hello-stream" {
		t.Errorf("result.Output = %q, want %q", result.Output, "hello-stream")
	}
	if len(received) == 0 {
		t.Error("onOutput was never called")
	}
	if received[0] != "hello-stream" {
		t.Errorf("received[0] = %q, want %q", received[0], "hello-stream")
	}
}
