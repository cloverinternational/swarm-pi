// Package codemode_test provides integration tests for code mode.
package codemode_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools/codemode"
)

// mockTool is a simple tool for testing.
type mockTool struct {
	name        string
	description string
	fn          func(ctx context.Context, params map[string]any) (*tools.ToolResult, error)
	callCount   int32
	mu          sync.Mutex
}

func (t *mockTool) Name() string        { return t.name }
func (t *mockTool) Description() string { return t.description }
func (t *mockTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"input": map[string]any{
				"type": "string",
			},
		},
	}
}
func (t *mockTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	atomic.AddInt32(&t.callCount, 1)
	if t.fn != nil {
		return t.fn(ctx, params)
	}
	input, _ := params["input"].(string)
	return tools.NewToolResult("result for " + input), nil
}
func (t *mockTool) CallCount() int32 { return atomic.LoadInt32(&t.callCount) }

// mockAsyncTool is an async tool that can be called in parallel.
type mockAsyncTool struct {
	mockTool
}

func (t *mockAsyncTool) SupportsParallel() bool { return true }

// mockSequentialTool is a sequential tool that must be called one at a time.
type mockSequentialTool struct {
	mockTool
}

func (t *mockSequentialTool) SupportsParallel() bool { return false }
func (t *mockSequentialTool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{PreferSequential: true}
}

// mockRegistry is a simple registry for testing.
type mockRegistry struct {
	tools map[string]tools.Tool
	mu    sync.RWMutex
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{tools: make(map[string]tools.Tool)}
}

func (r *mockRegistry) Register(tool tools.Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
	return nil
}

func (r *mockRegistry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	return nil
}

func (r *mockRegistry) Get(name string) (tools.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if t, ok := r.tools[name]; ok {
		return t, nil
	}
	return nil, errors.New("tool not found")
}

func (r *mockRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	return names
}

func (r *mockRegistry) IsRegistered(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.tools[name]
	return ok
}

func (r *mockRegistry) HideTool(name string) error { return nil }

func (r *mockRegistry) Execute(ctx context.Context, name string, params map[string]any) (*tools.ToolResult, error) {
	t, err := r.Get(name)
	if err != nil {
		return nil, err
	}
	return t.Execute(ctx, params)
}

func (r *mockRegistry) SetPermissionChecker(tools.PermissionChecker) {}

// TestParallelBatching tests that Promise.all executes tools concurrently.
func TestParallelBatching(t *testing.T) {
	registry := newMockRegistry()

	var callTimes []time.Time
	var callTimesMu sync.Mutex

	// Create async tools that record their call times
	for i := range 3 {
		tool := &mockAsyncTool{
			mockTool: mockTool{
				name:        "tool_" + string(rune('a'+i)),
				description: "Test tool " + string(rune('a'+i)),
				fn: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
					callTimesMu.Lock()
					callTimes = append(callTimes, time.Now())
					callTimesMu.Unlock()
					time.Sleep(100 * time.Millisecond) // Simulate work
					input, _ := params["input"].(string)
					return tools.NewToolResult("result for " + input), nil
				},
			},
		}
		_ = registry.Register(tool)
	}

	// Install code mode
	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Get the run_code tool
	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	// Execute code that uses Promise.all
	code := `
		const results = await Promise.all([
			tool_a({input: "a"}),
			tool_b({input: "b"}),
			tool_c({input: "c"})
		]);
		return results;
	`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := runCodeTool.Execute(ctx, map[string]any{"code": code})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result.IsError {
		t.Errorf("Execution returned error: %s", result.Output)
	}

	// Verify tools were called concurrently (within 50ms of each other)
	callTimesMu.Lock()
	defer callTimesMu.Unlock()

	if len(callTimes) != 3 {
		t.Errorf("Expected 3 tool calls, got %d", len(callTimes))
		return
	}

	// All calls should start within 50ms of each other (they're parallel)
	for i := 1; i < len(callTimes); i++ {
		diff := callTimes[i].Sub(callTimes[0])
		if diff > 50*time.Millisecond {
			t.Errorf("Tool calls not parallel: call %d started %v after call 0", i, diff)
		}
	}
}

// TestREPLPersistence tests that state persists across run_code calls.
func TestREPLPersistence(t *testing.T) {
	registry := newMockRegistry()

	tool := &mockTool{
		name:        "increment",
		description: "Increment a value",
	}
	_ = registry.Register(tool)

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	ctx := context.Background()

	// First call: set a variable
	_, err = runCodeTool.Execute(ctx, map[string]any{"code": "return (globalThis.x = 42);"})
	if err != nil {
		t.Fatalf("First execute failed: %v", err)
	}

	// Second call: access the variable (use globalThis for persistence)
	result, err := runCodeTool.Execute(ctx, map[string]any{"code": "return globalThis.x + 8;"})
	if err != nil {
		t.Fatalf("Second execute failed: %v", err)
	}

	if result.IsError {
		t.Errorf("Execution returned error: %s", result.Output)
	}

	// Check that x was preserved
	// The result should contain 50
	resultVal := result.Metadata["result"]
	if resultVal == nil {
		t.Errorf("Expected result 50, got nil")
	} else {
		// Compare numerically regardless of Go type
		var resultNum float64
		switch v := resultVal.(type) {
		case int:
			resultNum = float64(v)
		case int64:
			resultNum = float64(v)
		case float64:
			resultNum = v
		default:
			t.Errorf("Expected numeric result, got %T: %v", resultVal, resultVal)
		}
		if resultNum != 50 {
			t.Errorf("Expected result 50, got %v", resultNum)
		}
	}
}

// TestREPLRestart tests that restart clears state.
func TestREPLRestart(t *testing.T) {
	registry := newMockRegistry()

	tool := &mockTool{
		name:        "test_tool",
		description: "Test tool",
	}
	_ = registry.Register(tool)

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	ctx := context.Background()

	// First call: set a variable
	_, err = runCodeTool.Execute(ctx, map[string]any{"code": "return (globalThis.y = 100);"})
	if err != nil {
		t.Fatalf("First execute failed: %v", err)
	}

	// Second call with restart: should clear state
	result, err := runCodeTool.Execute(ctx, map[string]any{
		"code":    "return typeof globalThis.y === 'undefined' ? 'cleared' : 'still_there';",
		"restart": true,
	})
	if err != nil {
		t.Fatalf("Second execute failed: %v", err)
	}

	if result.IsError {
		t.Errorf("Execution returned error: %s", result.Output)
	}

	// Variable should be undefined after restart
	if result.Metadata["result"] != "cleared" {
		t.Errorf("Expected state to be cleared after restart, got %v", result.Metadata["result"])
	}
}

// TestErrorSurfacing tests that JS errors are properly surfaced.
func TestErrorSurfacing(t *testing.T) {
	registry := newMockRegistry()

	tool := &mockTool{
		name:        "test_tool",
		description: "Test tool",
	}
	_ = registry.Register(tool)

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	ctx := context.Background()

	// Execute code with a syntax error
	result, err := runCodeTool.Execute(ctx, map[string]any{"code": "let x = "})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.IsError {
		t.Error("Expected error result for syntax error")
	}

	// Execute code with a runtime error
	result, err = runCodeTool.Execute(ctx, map[string]any{"code": "undefinedVar;"})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if !result.IsError {
		t.Error("Expected error result for runtime error")
	}
}

// TestSelectorByName tests that ByNames selector works correctly.
func TestSelectorByName(t *testing.T) {
	registry := newMockRegistry()

	// Register three tools
	for _, name := range []string{"tool_a", "tool_b", "tool_c"} {
		tool := &mockTool{
			name:        name,
			description: "Test tool " + name,
		}
		_ = registry.Register(tool)
	}

	// Install code mode with selector for only tool_a and tool_b
	cm := codemode.New(codemode.WithSelector(codemode.NewByNames("tool_a", "tool_b")))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// tool_c should still be visible as a native tool
	_, err = wrappedRegistry.Get("tool_c")
	if err != nil {
		t.Errorf("tool_c should be visible as native tool: %v", err)
	}

	// tool_a and tool_b should be hidden (sandboxed)
	_, err = wrappedRegistry.Get("tool_a")
	if err == nil {
		t.Error("tool_a should be hidden (sandboxed)")
	}

	// run_code should be available
	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("run_code should be available: %v", err)
	}

	// Verify run_code description mentions tool_a and tool_b but not tool_c
	desc := runCodeTool.Description()
	if !containsString(desc, "tool_a") || !containsString(desc, "tool_b") {
		t.Error("run_code description should mention tool_a and tool_b")
	}
	if containsString(desc, "tool_c") {
		t.Error("run_code description should not mention tool_c")
	}
}

// TestToolResultInCode tests that tool results can be used in JS.
func TestToolResultInCode(t *testing.T) {
	registry := newMockRegistry()

	tool := &mockTool{
		name:        "get_value",
		description: "Get a value",
		fn: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			return tools.NewToolResult("hello world"), nil
		},
	}
	_ = registry.Register(tool)

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	ctx := context.Background()

	// Execute code that calls a tool and transforms the result
	result, err := runCodeTool.Execute(ctx, map[string]any{
		"code": `
			const val = await get_value({});
			return val.toUpperCase();
		`,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result.IsError {
		t.Errorf("Execution returned error: %s", result.Output)
	}

	// Result should be "HELLO WORLD"
	if result.Metadata["result"] != "HELLO WORLD" {
		t.Errorf("Expected 'HELLO WORLD', got %v", result.Metadata["result"])
	}
}

// TestConsoleLogCapture tests that console.log output is captured.
func TestConsoleLogCapture(t *testing.T) {
	registry := newMockRegistry()

	tool := &mockTool{
		name:        "test_tool",
		description: "Test tool",
	}
	_ = registry.Register(tool)

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	ctx := context.Background()

	result, err := runCodeTool.Execute(ctx, map[string]any{
		"code": `
			console.log("Hello", "world");
			return 42;
		`,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result.IsError {
		t.Errorf("Execution returned error: %s", result.Output)
	}

	// Check printed output
	if result.Output != "Hello world" {
		t.Errorf("Expected printed 'Hello world', got %q", result.Output)
	}

	// Check return value (JS numbers become int64 or float64 in Go)
	resultVal := result.Metadata["result"]
	if resultVal == nil {
		t.Errorf("Expected result 42, got nil")
	} else {
		// Compare numerically regardless of Go type
		var resultNum float64
		switch v := resultVal.(type) {
		case int:
			resultNum = float64(v)
		case int64:
			resultNum = float64(v)
		case float64:
			resultNum = v
		default:
			t.Errorf("Expected numeric result, got %T: %v", resultVal, resultVal)
		}
		if resultNum != 42 {
			t.Errorf("Expected result 42, got %v", resultNum)
		}
	}
}

// Helper function to check if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && stringContains(s, substr)
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────────────────
// Integration Tests - Testing code mode with tools.Registry interface
// ─────────────────────────────────────────────────────────────────────────────

// TestIntegrationWithRegistry tests code mode integration with tools.Registry interface.
func TestIntegrationWithRegistry(t *testing.T) {
	registry := tools.NewRegistry()

	// Register some tools
	tool1 := &mockTool{name: "read_file", description: "Read a file"}
	tool2 := &mockTool{name: "write_file", description: "Write a file"}
	tool3 := &mockTool{name: "run_bash", description: "Run bash command"}

	if err := registry.Register(tool1); err != nil {
		t.Fatalf("Failed to register tool1: %v", err)
	}
	if err := registry.Register(tool2); err != nil {
		t.Fatalf("Failed to register tool2: %v", err)
	}
	if err := registry.Register(tool3); err != nil {
		t.Fatalf("Failed to register tool3: %v", err)
	}

	// Install code mode with all tools selected
	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// Verify run_code is registered
	if !wrappedRegistry.IsRegistered("run_code") {
		t.Error("run_code should be registered in wrapped registry")
	}

	// Verify sandboxed tools are hidden
	if wrappedRegistry.IsRegistered("read_file") {
		t.Error("read_file should be hidden (sandboxed)")
	}
	if wrappedRegistry.IsRegistered("write_file") {
		t.Error("write_file should be hidden (sandboxed)")
	}

	// Verify run_bash is still visible (sandboxed)
	// Note: AllTools selector should sandbox all tools
	if wrappedRegistry.IsRegistered("run_bash") {
		t.Error("run_bash should be hidden (sandboxed) with AllTools selector")
	}

	// Verify List() includes run_code and excludes sandboxed tools
	names := wrappedRegistry.List()
	hasRunCode := false
	for _, name := range names {
		if name == "run_code" {
			hasRunCode = true
		}
		if name == "read_file" || name == "write_file" {
			t.Errorf("List() should not include sandboxed tool %s", name)
		}
	}
	if !hasRunCode {
		t.Error("List() should include run_code")
	}
}

// TestConfigOptionality tests that code mode can be optionally enabled/disabled.
func TestConfigOptionality(t *testing.T) {
	registry := tools.NewRegistry()

	tool := &mockTool{name: "test_tool", description: "Test tool"}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	// Scenario 1: Code mode disabled (no selector means no tools sandboxed)
	cmDisabled := codemode.New(codemode.WithSelector(codemode.NewByNames())) // Empty selector
	wrappedDisabled, err := cmDisabled.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// run_code should still be registered but with no sandboxed tools
	runCodeTool, err := wrappedDisabled.Get("run_code")
	if err != nil {
		t.Fatalf("run_code should be available: %v", err)
	}

	// Description should not mention test_tool
	desc := runCodeTool.Description()
	if containsString(desc, "test_tool") {
		t.Error("run_code description should not mention test_tool when selector is empty")
	}

	// test_tool should still be visible as a native tool
	_, err = wrappedDisabled.Get("test_tool")
	if err != nil {
		t.Errorf("test_tool should be visible when not sandboxed: %v", err)
	}
}

// TestTimeout tests that code mode respects context timeout.
func TestTimeout(t *testing.T) {
	registry := newMockRegistry()

	// Create a tool that takes a long time
	slowTool := &mockTool{
		name:        "slow_tool",
		description: "A slow tool",
		fn: func(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
			time.Sleep(5 * time.Second)
			return tools.NewToolResult("done"), nil
		},
	}
	_ = registry.Register(slowTool)

	cm := codemode.New(
		codemode.WithSelector(codemode.AllTools{}),
		codemode.WithTimeout(100*time.Millisecond),
	)
	wrappedRegistry, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	runCodeTool, err := wrappedRegistry.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	// Execute with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err = runCodeTool.Execute(ctx, map[string]any{
		"code": `await slow_tool({});`,
	})
	elapsed := time.Since(start)

	// Should timeout quickly, not wait 5 seconds
	if elapsed > 500*time.Millisecond {
		t.Errorf("Execution should have timed out quickly, took %v", elapsed)
	}
}

// TestConfigEnabled tests that Config.Enabled controls code mode installation.
func TestConfigEnabled(t *testing.T) {
	registry := tools.NewRegistry()
	tool := &mockTool{name: "test_tool", description: "Test tool"}
	if err := registry.Register(tool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	// Test with Enabled = false (default)
	cfgDisabled := codemode.DefaultConfig()
	if cfgDisabled.Enabled {
		t.Error("DefaultConfig should have Enabled = false")
	}

	// When disabled, NewFromConfig should still return a CodeMode
	// but it's up to the caller to check cfg.Enabled before installing
	cmDisabled := codemode.NewFromConfig(cfgDisabled)
	if cmDisabled == nil {
		t.Error("NewFromConfig should not return nil even when disabled")
	}

	// Test with Enabled = true
	cfgEnabled := &codemode.Config{
		Enabled: true,
		Timeout: 30 * time.Second,
		Persist: true,
	}
	cmEnabled := codemode.NewFromConfig(cfgEnabled)
	if cmEnabled == nil {
		t.Fatal("NewFromConfig should not return nil")
	}

	// Install should work
	wrappedReg, err := cmEnabled.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	// run_code should be registered
	if !wrappedReg.IsRegistered("run_code") {
		t.Error("run_code should be registered when Enabled = true")
	}
}

// TestConfigToOptions tests that Config.ToOptions converts correctly.
func TestConfigToOptions(t *testing.T) {
	tests := []struct {
		name   string
		config *codemode.Config
	}{
		{
			name:   "nil config",
			config: nil,
		},
		{
			name:   "empty config",
			config: &codemode.Config{},
		},
		{
			name: "full config",
			config: &codemode.Config{
				Enabled:    true,
				Timeout:    30 * time.Second,
				MaxRetries: 5,
				Persist:    false,
				ToolNames:  []string{"tool_a", "tool_b"},
			},
		},
		{
			name: "with custom selector",
			config: &codemode.Config{
				Enabled:  true,
				Selector: codemode.AllTools{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.config.ToOptions()
			// Should not panic
			cm := codemode.New(opts...)
			if cm == nil {
				t.Error("New should not return nil")
			}
		})
	}
}

// TestConfigClone tests that Config.Clone creates a proper copy.
func TestConfigClone(t *testing.T) {
	original := &codemode.Config{
		Enabled:    true,
		Timeout:    30 * time.Second,
		MaxRetries: 5,
		Persist:    false,
		ToolNames:  []string{"tool_a", "tool_b"},
	}

	clone := original.Clone()
	if clone == nil {
		t.Fatal("Clone should not return nil")
	}

	if clone.Enabled != original.Enabled {
		t.Errorf("Enabled mismatch: got %v, want %v", clone.Enabled, original.Enabled)
	}
	if clone.Timeout != original.Timeout {
		t.Errorf("Timeout mismatch: got %v, want %v", clone.Timeout, original.Timeout)
	}
	if clone.MaxRetries != original.MaxRetries {
		t.Errorf("MaxRetries mismatch: got %v, want %v", clone.MaxRetries, original.MaxRetries)
	}
	if clone.Persist != original.Persist {
		t.Errorf("Persist mismatch: got %v, want %v", clone.Persist, original.Persist)
	}

	// Verify ToolNames is a copy
	clone.ToolNames[0] = "modified"
	if original.ToolNames[0] == "modified" {
		t.Error("Modifying clone's ToolNames should not affect original")
	}

	// Test nil clone
	var nilConfig *codemode.Config
	if nilConfig.Clone() != nil {
		t.Error("nil.Clone() should return nil")
	}
}

// TestSystemPrompt verifies the code-mode instruction block is present and
// mentions the essentials the model needs to operate in code mode. This locks
// in the guidance that steers the model to use run_code instead of falling
// back to individual tool calls (the root cause of "uses tools the original
// way" when code mode is toggled on).
func TestSystemPrompt(t *testing.T) {
	p := codemode.SystemPrompt
	if p == "" {
		t.Fatal("codemode.SystemPrompt must not be empty")
	}
	mustContain := []string{
		"run_code",    // the tool the model must call
		"Code Mode",   // clear framing
		"await",       // async calling convention
		"Promise.all", // batching guidance
	}
	for _, sub := range mustContain {
		if !contains(p, sub) {
			t.Errorf("SystemPrompt missing expected guidance %q", sub)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (indexOf(haystack, needle) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestInstallInPlace_HidesToolsAndRuns is the end-to-end proof for the TUI's
// code-mode toggle. Unlike the other tests here (which use a mockRegistry whose
// HideTool is a no-op), this exercises InstallInPlace against a REAL
// *tools.SimpleRegistry — the exact type the TUI toggles — and proves:
//
//  1. Before enabling, run_code is registered but hidden; native tools are visible.
//  2. toggle(true) hides EVERY native tool from List() and shows only run_code
//     (this is the literal "tools are hidden from the LLM" guarantee).
//  3. Hidden tools remain executable via the executor (so the sandbox can call
//     them) — the SimpleRegistry.HideTool invariant.
//  4. run_code actually RUNS: JS inside the sandbox calls a hidden tool and the
//     tool's Execute fires (callCount increments) and the value flows back.
//  5. toggle(false) restores the native tools and re-hides run_code.
func TestInstallInPlace_HidesToolsAndRuns(t *testing.T) {
	reg := tools.NewSimpleRegistry(nil, nil)

	// Async so the sandbox exposes it as an awaitable function.
	greet := &mockAsyncTool{mockTool: mockTool{name: "greet", description: "greets"}}
	if err := reg.Register(greet); err != nil {
		t.Fatalf("register greet: %v", err)
	}
	ping := &mockAsyncTool{mockTool: mockTool{name: "ping", description: "pings"}}
	if err := reg.Register(ping); err != nil {
		t.Fatalf("register ping: %v", err)
	}

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	exec := tools.NewExecutor(reg, nil)
	toggle, err := cm.InstallInPlace(reg, exec)
	if err != nil {
		t.Fatalf("InstallInPlace: %v", err)
	}

	// (1) Before enabling: native tools visible, run_code hidden.
	before := listSet(reg)
	if !before["greet"] || !before["ping"] {
		t.Fatalf("expected native tools visible before enable, got %v", before)
	}
	if before["run_code"] {
		t.Fatalf("run_code should be hidden before enable, got %v", before)
	}

	// (2) Enable: only run_code visible to the LLM.
	if err := toggle(true); err != nil {
		t.Fatalf("toggle(true): %v", err)
	}
	on := listSet(reg)
	if len(on) != 1 || !on["run_code"] {
		t.Fatalf("code mode ON must expose ONLY run_code, got %v", on)
	}

	// (3) Hidden tool still executable via executor (sandbox can reach it).
	if _, err := exec.Execute(context.Background(), "greet", map[string]any{"input": "x"}); err != nil {
		t.Fatalf("hidden tool should still execute via executor: %v", err)
	}
	if greet.CallCount() != 1 {
		t.Fatalf("expected greet callCount=1 after direct executor call, got %d", greet.CallCount())
	}

	// (4) run_code RUNS: JS calls the hidden tool via await.
	runCode, err := reg.Get("run_code")
	if err != nil {
		t.Fatalf("get run_code: %v", err)
	}
	res, err := runCode.Execute(context.Background(), map[string]any{
		"code": `const r = await ping({input: "hi"}); r`,
	})
	if err != nil {
		t.Fatalf("run_code execute: %v", err)
	}
	if res != nil && res.IsError {
		t.Fatalf("run_code returned error: %s", res.Error)
	}
	if ping.CallCount() != 1 {
		t.Fatalf("expected ping to be called once from inside run_code, got %d", ping.CallCount())
	}

	// (5) Disable: native tools restored, run_code hidden again.
	if err := toggle(false); err != nil {
		t.Fatalf("toggle(false): %v", err)
	}
	off := listSet(reg)
	if !off["greet"] || !off["ping"] {
		t.Fatalf("expected native tools restored after disable, got %v", off)
	}
	if off["run_code"] {
		t.Fatalf("run_code should be hidden again after disable, got %v", off)
	}
}

func listSet(reg interface{ List() []string }) map[string]bool {
	m := make(map[string]bool)
	for _, n := range reg.List() {
		m[n] = true
	}
	return m
}

// TestControlPlaneToolsNotSandboxed proves the fix for the code-mode
// enforcement deadlock: control-plane tools (task/skill/plan/user-interaction)
// must remain directly visible and callable while code mode is ON. Otherwise
// the task-enforcement hook (which blocks run_code until a task is focused) and
// the skill-budget hook (which blocks run_code after N calls until a skill is
// created/used) become unsatisfiable, because the only tools that clear those
// gates would themselves be hidden inside run_code.
func TestControlPlaneToolsNotSandboxed(t *testing.T) {
	reg := tools.NewSimpleRegistry(nil, nil)

	// A normal work tool (should be sandboxed) plus the control-plane tools
	// that MUST stay visible. Names cover both snake_case and PascalCase.
	work := &mockAsyncTool{mockTool: mockTool{name: "read", description: "reads"}}
	controlPlane := []string{
		"task_create", "TaskUpdate", "task_get", "task_list",
		"SkillManage", "Skill",
		"enter_plan_mode", "exit_plan_mode",
		"ask_user_question",
	}
	if err := reg.Register(work); err != nil {
		t.Fatalf("register read: %v", err)
	}
	for _, name := range controlPlane {
		tl := &mockTool{name: name, description: name}
		if err := reg.Register(tl); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	exec := tools.NewExecutor(reg, nil)
	toggle, err := cm.InstallInPlace(reg, exec)
	if err != nil {
		t.Fatalf("InstallInPlace: %v", err)
	}

	// Enable code mode.
	if err := toggle(true); err != nil {
		t.Fatalf("toggle(true): %v", err)
	}
	on := listSet(reg)

	// run_code must be visible.
	if !on["run_code"] {
		t.Fatalf("run_code must be visible in code mode, got %v", on)
	}
	// The work tool must be hidden (sandboxed).
	if on["read"] {
		t.Fatalf("work tool 'read' should be sandboxed/hidden in code mode, got %v", on)
	}
	// Every control-plane tool must stay visible AND directly callable.
	for _, name := range controlPlane {
		if !on[name] {
			t.Errorf("control-plane tool %q must remain visible in code mode, got %v", name, on)
		}
		if _, err := reg.Get(name); err != nil {
			t.Errorf("control-plane tool %q must remain directly gettable in code mode: %v", name, err)
		}
		if _, err := exec.Execute(context.Background(), name, map[string]any{}); err != nil {
			t.Errorf("control-plane tool %q must remain directly executable in code mode: %v", name, err)
		}
	}

	// Disable: everything restored.
	if err := toggle(false); err != nil {
		t.Fatalf("toggle(false): %v", err)
	}
	off := listSet(reg)
	if !off["read"] {
		t.Fatalf("work tool should be restored after disable, got %v", off)
	}
}

// TestObjectReturnValueRendered is a regression test for the bug where a
// run_code snippet whose last expression is an object or array (with no
// console.log output) reported "(empty)" to the model because the value was
// stored only in Metadata and never rendered into Content/Output.
func TestObjectReturnValueRendered(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)
	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrapped, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	runCodeTool, err := wrapped.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	cases := []struct {
		name string
		code string
		want []string // substrings that must appear in Output
	}{
		{"object_return", `return {hello: "world", n: 42};`, []string{`"hello"`, `"world"`, `"n"`, `42`}},
		{"array_return", `return [1, 2, 3];`, []string{`1`, `2`, `3`}},
		{"object_bare", `({hello: "world", n: 42})`, []string{`"hello"`, `"world"`, `"n"`, `42`}},
		{"array_bare", `[1, 2, 3]`, []string{`1`, `2`, `3`}},
		{"expr_after_stmt", "let x = 7;\n({v: x})", []string{`"v"`, `7`}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := runCodeTool.Execute(context.Background(), map[string]any{"code": tc.code})
			if err != nil {
				t.Fatalf("Execute failed: %v", err)
			}
			if result.IsError {
				t.Fatalf("execution returned error: %s", result.Output)
			}
			if result.Output == "(empty)" || result.Output == "" {
				t.Fatalf("object/array return value was dropped (got %q)", result.Output)
			}
			for _, want := range tc.want {
				if !strings.Contains(result.Output, want) {
					t.Errorf("Output %q missing expected substring %q", result.Output, want)
				}
			}
		})
	}
}

// TestConsoleLogObjectIsJSON is a regression test for the bug where
// console.log of an object printed Go's map[key:val] syntax instead of JSON.
func TestConsoleLogObjectIsJSON(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)
	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrapped, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	runCodeTool, err := wrapped.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	result, err := runCodeTool.Execute(context.Background(), map[string]any{
		"code": `console.log({a: 1, b: "x"}); 1`,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if strings.Contains(result.Output, "map[") {
		t.Fatalf("console.log rendered Go map syntax instead of JSON: %q", result.Output)
	}
	if !strings.Contains(result.Output, `"a"`) || !strings.Contains(result.Output, `"b"`) {
		t.Errorf("console.log object not JSON-encoded: %q", result.Output)
	}
}

// TestAwaitThenBareExprReturned covers the real-world case: await a tool then
// end in a bare object expression. Top-level await must parse AND the trailing
// expression must still be captured (not dropped to "(empty)").
func TestAwaitThenBareExprReturned(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)
	echo := &mockTool{
		name:        "echo",
		description: "echo tool",
		fn: func(_ context.Context, params map[string]any) (*tools.ToolResult, error) {
			in, _ := params["input"].(string)
			return tools.NewToolResult(in), nil
		},
	}
	_ = registry.Register(echo)

	cm := codemode.New(codemode.WithSelector(codemode.AllTools{}))
	wrapped, err := cm.Install(registry, registry)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	runCodeTool, err := wrapped.Get("run_code")
	if err != nil {
		t.Fatalf("Get run_code failed: %v", err)
	}

	result, err := runCodeTool.Execute(context.Background(), map[string]any{
		"code": "const r = await echo({input: \"pong\"});\n({wrapped: r})",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("execution returned error: %s", result.Output)
	}
	if result.Output == "(empty)" || !strings.Contains(result.Output, "pong") {
		t.Fatalf("await-then-bare-expr result dropped or wrong: %q", result.Output)
	}
}
