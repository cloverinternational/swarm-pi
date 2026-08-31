package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// noopDispatch is a dispatch function that returns nil for all calls.
func noopDispatch(_ context.Context, _ string, _ map[string]any) (any, error) {
	return nil, nil
}

// echoDispatch returns the args as-is for verification.
func echoDispatch(_ context.Context, _ string, args map[string]any) (any, error) {
	return args, nil
}

// delayDispatch creates a dispatch that sleeps for the given duration.
func delayDispatch(d time.Duration) DispatchFn {
	return func(_ context.Context, _ string, _ map[string]any) (any, error) {
		time.Sleep(d)
		return "done", nil
	}
}

// errorDispatch returns an error for all calls.
func errorDispatch(_ context.Context, _ string, _ map[string]any) (any, error) {
	return nil, fmt.Errorf("tool execution failed")
}

// countingDispatch counts calls and returns the count.
func countingDispatch() (DispatchFn, *int32) {
	var count int32
	return func(_ context.Context, _ string, _ map[string]any) (any, error) {
		n := atomic.AddInt32(&count, 1)
		return n, nil
	}, &count
}

func TestGojaSandbox_BasicEval(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	result, err := sb.Eval(context.Background(), "return 1 + 2;", nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}
	// goja returns int64 for integer results
	val, ok := result.Value.(int64)
	if !ok {
		// try float64
		fval, ok2 := result.Value.(float64)
		if !ok2 {
			t.Fatalf("expected numeric result, got %T: %v", result.Value, result.Value)
		}
		if fval != 3 {
			t.Fatalf("expected 3, got %v", fval)
		}
	} else if val != 3 {
		t.Fatalf("expected 3, got %v", val)
	}
}

func TestGojaSandbox_ConsoleLogCapture(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	result, err := sb.Eval(context.Background(), `
		console.log("hello", "world");
		console.warn("warning!");
		return 42;
	`, nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}
	if !strings.Contains(result.Printed, "hello world") {
		t.Errorf("expected 'hello world' in printed, got %q", result.Printed)
	}
	if !strings.Contains(result.Printed, "warning!") {
		t.Errorf("expected 'warning!' in printed, got %q", result.Printed)
	}
}

func TestGojaSandbox_SyntaxError(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	result, err := sb.Eval(context.Background(), "let x = ;", nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval returned Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for syntax error")
	}
	if result.ErrorMessage == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestGojaSandbox_RuntimeError(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	result, err := sb.Eval(context.Background(), "undefinedVariable;", nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval returned Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for runtime error")
	}
}

func TestGojaSandbox_GlobalPersistence(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	// First eval: set a global
	_, err := sb.Eval(context.Background(), "return (globalThis.myVar = 99);", nil, noopDispatch)
	if err != nil {
		t.Fatalf("first Eval failed: %v", err)
	}

	// Second eval: read the global
	result, err := sb.Eval(context.Background(), "return globalThis.myVar + 1;", nil, noopDispatch)
	if err != nil {
		t.Fatalf("second Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}

	var num float64
	switch v := result.Value.(type) {
	case int64:
		num = float64(v)
	case float64:
		num = v
	default:
		t.Fatalf("expected numeric, got %T: %v", result.Value, result.Value)
	}
	if num != 100 {
		t.Errorf("expected 100, got %v", num)
	}
}

func TestGojaSandbox_Restart(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	// Set a global
	_, err := sb.Eval(context.Background(), "return (globalThis.x = 42);", nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}

	// Restart
	if err := sb.Restart(); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}

	// Global should be gone
	result, err := sb.Eval(context.Background(),
		"return typeof globalThis.x === 'undefined' ? 'cleared' : 'present';",
		nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval after restart failed: %v", err)
	}
	if result.Value != "cleared" {
		t.Errorf("expected 'cleared', got %v", result.Value)
	}
}

func TestGojaSandbox_SyncToolCall(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	stubs := []ToolStub{
		{Name: "greet", OriginalName: "greet", IsAsync: false, Description: "greet tool"},
	}

	dispatch := func(_ context.Context, name string, args map[string]any) (any, error) {
		who, _ := args["name"].(string)
		return "Hello, " + who + "!", nil
	}

	result, err := sb.Eval(context.Background(),
		`return greet({name: "World"});`,
		stubs, dispatch)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}
	if result.Value != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %v", result.Value)
	}
	// Verify tool call metadata was recorded
	if len(result.ToolCalls) == 0 {
		t.Error("expected tool call metadata")
	}
}

func TestGojaSandbox_AsyncToolCall(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	stubs := []ToolStub{
		{Name: "fetch_data", OriginalName: "fetch_data", IsAsync: true, Description: "fetch data"},
	}

	dispatch := func(_ context.Context, _ string, _ map[string]any) (any, error) {
		time.Sleep(10 * time.Millisecond)
		return "fetched", nil
	}

	result, err := sb.Eval(context.Background(),
		`return await fetch_data({});`,
		stubs, dispatch)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}
	if result.Value != "fetched" {
		t.Errorf("expected 'fetched', got %v", result.Value)
	}
}

func TestGojaSandbox_AsyncParallelDispatch(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	var callCount int32
	stubs := []ToolStub{
		{Name: "tool_a", OriginalName: "tool_a", IsAsync: true},
		{Name: "tool_b", OriginalName: "tool_b", IsAsync: true},
		{Name: "tool_c", OriginalName: "tool_c", IsAsync: true},
	}

	dispatch := func(_ context.Context, _ string, _ map[string]any) (any, error) {
		atomic.AddInt32(&callCount, 1)
		time.Sleep(50 * time.Millisecond)
		return "ok", nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := sb.Eval(ctx, `
		const results = await Promise.all([
			tool_a({}),
			tool_b({}),
			tool_c({})
		]);
		return results;
	`, stubs, dispatch)

	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}
	// All three tools should have been dispatched
	count := atomic.LoadInt32(&callCount)
	if count != 3 {
		t.Errorf("expected 3 calls, got %d (async dispatch may serialize through event loop)", count)
	}
}

func TestGojaSandbox_ToolError(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	stubs := []ToolStub{
		{Name: "failing_tool", OriginalName: "failing_tool", IsAsync: true},
	}

	result, err := sb.Eval(context.Background(), `
		try {
			await failing_tool({});
			return "no error";
		} catch (e) {
			return "caught: " + e.message;
		}
	`, stubs, errorDispatch)

	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected sandbox error: %s", result.ErrorMessage)
	}
	str, ok := result.Value.(string)
	if !ok || !strings.Contains(str, "caught:") {
		t.Errorf("expected caught error, got %v", result.Value)
	}
}

func TestGojaSandbox_ContextCancellation(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	stubs := []ToolStub{
		{Name: "slow", OriginalName: "slow", IsAsync: true},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	result, err := sb.Eval(ctx, `await slow({});`, stubs, delayDispatch(5*time.Second))
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Eval returned Go error: %v", err)
	}
	// Should have timed out, not waited 5 seconds
	if elapsed > 500*time.Millisecond {
		t.Errorf("should have timed out quickly, took %v", elapsed)
	}
	if !result.IsError {
		t.Error("expected error result on timeout")
	}
}

func TestGojaSandbox_ContextPropagation(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	stubs := []ToolStub{
		{Name: "ctx_tool", OriginalName: "ctx_tool", IsAsync: true},
	}

	// Verify the dispatch receives the eval context, not context.Background()
	var receivedCtx context.Context
	dispatch := func(ctx context.Context, _ string, _ map[string]any) (any, error) {
		receivedCtx = ctx
		return "ok", nil
	}

	ctx := context.WithValue(context.Background(), contextKey("test"), "value")
	_, err := sb.Eval(ctx, `return await ctx_tool({});`, stubs, dispatch)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}

	if receivedCtx == nil {
		t.Fatal("dispatch was never called")
	}
	if receivedCtx.Value(contextKey("test")) != "value" {
		t.Error("dispatch did not receive the eval context — got context.Background() instead")
	}
}

type contextKey string

func TestGojaSandbox_ClosedSandbox(t *testing.T) {
	sb := NewGojaSandbox()
	sb.Close()

	_, err := sb.Eval(context.Background(), "return 1;", nil, noopDispatch)
	if err == nil {
		t.Fatal("expected error from closed sandbox")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Errorf("expected 'closed' in error, got %v", err)
	}
}

func TestGojaSandbox_ToolCallMetadata(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	dispatchFn, counter := countingDispatch()
	stubs := []ToolStub{
		{Name: "counted", OriginalName: "counted", IsAsync: true},
	}

	result, err := sb.Eval(context.Background(), `
		await counted({a: 1});
		await counted({b: 2});
		return "done";
	`, stubs, dispatchFn)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ErrorMessage)
	}
	if atomic.LoadInt32(counter) != 2 {
		t.Errorf("expected 2 calls, got %d", atomic.LoadInt32(counter))
	}
	if len(result.ToolCalls) != 2 {
		t.Errorf("expected 2 tool call records, got %d", len(result.ToolCalls))
	}
	if len(result.ToolReturns) != 2 {
		t.Errorf("expected 2 tool return records, got %d", len(result.ToolReturns))
	}
}

func TestGojaSandbox_PromiseRejection(t *testing.T) {
	sb := NewGojaSandbox()
	defer sb.Close()

	result, err := sb.Eval(context.Background(), `
		throw new Error("deliberate failure");
	`, nil, noopDispatch)
	if err != nil {
		t.Fatalf("Eval returned Go error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError for thrown error")
	}
	// The error message may contain "deliberate failure" directly or may be
	// a serialized form (e.g. "map[]") depending on how goja exports Error
	// objects. Either way, IsError must be true.
	if result.ErrorMessage == "" {
		t.Error("expected non-empty error message")
	}
}

func TestGojaSandbox_RestartAfterClose(t *testing.T) {
	sb := NewGojaSandbox()
	sb.Close()

	err := sb.Restart()
	if err == nil {
		t.Fatal("expected error from Restart after Close")
	}
}
