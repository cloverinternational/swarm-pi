package tools_test

import (
	"context"
	"testing"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// mockSlowTool simulates a slow tool that sleeps before completing
type mockSlowTool struct {
	name          string
	sleepDuration time.Duration
}

func (t *mockSlowTool) Name() string {
	return t.name
}

func (t *mockSlowTool) Description() string {
	return "A tool that takes a long time to execute"
}

func (t *mockSlowTool) Parameters() any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *mockSlowTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	// Simulate slow operation
	select {
	case <-time.After(t.sleepDuration):
		return tools.NewToolResult("completed"), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *mockSlowTool) Validate(params map[string]any) error {
	return nil
}

func (t *mockSlowTool) IsIdempotent() bool {
	return false
}

func (t *mockSlowTool) RequiresPermission() []tools.Permission {
	return []tools.Permission{}
}

func (t *mockSlowTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *mockSlowTool) OptimizationHints() *tools.OptimizationHints {
	return nil
}

func TestRegistryTimeout(t *testing.T) {
	registry := tools.NewSimpleRegistry(nil, nil)

	// Register a slow tool that takes 10 seconds
	slowTool := &mockSlowTool{name: "slow_tool", sleepDuration: 10 * time.Second}
	if err := registry.Register(slowTool); err != nil {
		t.Fatalf("Failed to register tool: %v", err)
	}

	t.Run("default timeout should be 5 minutes", func(t *testing.T) {
		timeout := registry.ToolTimeout()
		if timeout != 5*time.Minute {
			t.Errorf("Expected default timeout of 5 minutes, got %v", timeout)
		}
	})

	t.Run("should timeout slow tool", func(t *testing.T) {
		// Set a short timeout
		// Bash now defaults to 60 seconds if no timeout_seconds param is provided
		// But the registry timeout can still be set to any value (no enforcement)
		registry.SetToolTimeout(100 * time.Millisecond)

		start := time.Now()
		_, err := registry.Execute(context.Background(), "slow_tool", map[string]any{})
		duration := time.Since(start)

		if err == nil {
			t.Error("Expected timeout error, got nil")
		}

		// Should timeout around 100ms as set
		if duration > 2*time.Second {
			t.Errorf("Timeout took too long: %v (expected ~100ms)", duration)
		}
	})

	t.Run("should respect existing context deadline", func(t *testing.T) {
		// Set registry timeout to 2 minutes
		registry.SetToolTimeout(2 * time.Minute)

		// But use a context with shorter deadline (shorter than 1 minute minimum)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		start := time.Now()
		_, err := registry.Execute(ctx, "slow_tool", map[string]any{})
		duration := time.Since(start)

		if err == nil {
			t.Error("Expected timeout error, got nil")
		}

		// Should respect the context deadline (50ms), not registry timeout (2min)
		if duration > 2*time.Second {
			t.Errorf("Should respect context deadline: took %v", duration)
		}
	})

	t.Run("should allow disabling timeout", func(t *testing.T) {
		// Disable timeout
		registry.SetToolTimeout(0)

		// Register a fast tool with unique name
		fastTool := &mockSlowTool{name: "fast_tool_1", sleepDuration: 50 * time.Millisecond}
		if err := registry.Register(fastTool); err != nil {
			t.Fatalf("Failed to register fast tool: %v", err)
		}

		result, err := registry.Execute(context.Background(), "fast_tool_1", map[string]any{})
		if err != nil {
			t.Errorf("Expected no error with disabled timeout, got: %v", err)
		}
		if result == nil {
			t.Error("Expected result from fast tool")
		}
	})

	t.Run("fast tool should complete within timeout", func(t *testing.T) {
		// Set reasonable timeout
		registry.SetToolTimeout(1 * time.Second)

		// Register a fast tool with unique name
		fastTool2 := &mockSlowTool{name: "fast_tool_2", sleepDuration: 50 * time.Millisecond}
		if err := registry.Register(fastTool2); err != nil {
			t.Fatalf("Failed to register fast tool: %v", err)
		}

		result, err := registry.Execute(context.Background(), "fast_tool_2", map[string]any{})
		if err != nil {
			t.Errorf("Fast tool should complete: %v", err)
		}
		if result == nil {
			t.Error("Expected result from fast tool")
		}
	})
}
