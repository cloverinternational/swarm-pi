package builtin

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/hooks"
)

// mockRegistrar records RegisterFileRead calls for testing.
type mockRegistrar struct {
	paths []string
	count atomic.Int32
}

func (m *mockRegistrar) RegisterFileRead(filePath string) {
	m.count.Add(1)
	m.paths = append(m.paths, filePath)
}

func TestNestedIndexDiscoveryHookFiltersCorrectly(t *testing.T) {
	reg := &mockRegistrar{}
	hook := NewNestedIndexDiscoveryHook(reg, "/workspace")

	// Should filter for tool.after_execute
	event := hooks.Event{Type: hooks.EventToolAfterExecute}
	if !hook.Filter(event) {
		t.Error("expected Filter to return true for tool.after_execute")
	}

	// Should NOT filter for other events
	otherEvent := hooks.Event{Type: hooks.EventAgentStopped}
	if hook.Filter(otherEvent) {
		t.Error("expected Filter to return false for non-tool events")
	}
}

func TestNestedIndexDiscoveryHookExtractsPaths(t *testing.T) {
	reg := &mockRegistrar{}
	hook := NewNestedIndexDiscoveryHook(reg, "/workspace")

	// Simulate a file_read tool after_execute event
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "file_read",
			"tool_input": map[string]any{
				"file_path": "/workspace/swarm-sdk/client.go",
			},
			"tool_output": map[string]any{
				"success": true,
			},
		},
	}

	result, err := hook.OnEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Action != hooks.ActionContinue {
		t.Errorf("expected ActionContinue, got %v", result.Action)
	}

	if reg.count.Load() != 1 {
		t.Errorf("expected 1 RegisterFileRead call, got %d", reg.count.Load())
	}
	if len(reg.paths) != 1 || reg.paths[0] != "/workspace/swarm-sdk/client.go" {
		t.Errorf("expected path /workspace/swarm-sdk/client.go, got %v", reg.paths)
	}
}

func TestNestedIndexDiscoveryHookIgnoresNonFileTools(t *testing.T) {
	reg := &mockRegistrar{}
	hook := NewNestedIndexDiscoveryHook(reg, "/workspace")

	// Simulate a Bash tool after_execute event
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "Bash",
			"tool_input": map[string]any{
				"command": "ls -la",
			},
			"tool_output": map[string]any{
				"success": true,
			},
		},
	}

	_, err := hook.OnEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if reg.count.Load() != 0 {
		t.Errorf("expected 0 RegisterFileRead calls for Bash tool, got %d", reg.count.Load())
	}
}

func TestNestedIndexDiscoveryHookIgnoresFailedExecutions(t *testing.T) {
	reg := &mockRegistrar{}
	hook := NewNestedIndexDiscoveryHook(reg, "/workspace")

	// Simulate a failed file_read execution
	event := hooks.Event{
		Type: hooks.EventToolAfterExecute,
		Data: map[string]any{
			"tool_name": "file_read",
			"tool_input": map[string]any{
				"file_path": "/workspace/nonexistent.go",
			},
			"tool_output": map[string]any{
				"success": false,
			},
		},
	}

	_, err := hook.OnEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if reg.count.Load() != 0 {
		t.Errorf("expected 0 RegisterFileRead calls for failed execution, got %d", reg.count.Load())
	}
}

func TestIsFileReadTool(t *testing.T) {
	tests := []struct {
		tool     string
		expected bool
	}{
		{"file_read", true},
		{"read_file", true},
		{"Read", true},
		{"file_edit", true},
		{"file_write", true},
		{"Write", true},
		{"Edit", true},
		{"Bash", false},
		{"bash", false},
		{"web_search", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			got := isFileReadTool(tt.tool)
			if got != tt.expected {
				t.Errorf("isFileReadTool(%q) = %v, want %v", tt.tool, got, tt.expected)
			}
		})
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]any
		expected string
	}{
		{
			name:     "file_path key",
			params:   map[string]any{"file_path": "/foo/bar.go"},
			expected: "/foo/bar.go",
		},
		{
			name:     "path key",
			params:   map[string]any{"path": "/foo/baz.go"},
			expected: "/foo/baz.go",
		},
		{
			name:     "filePath key",
			params:   map[string]any{"filePath": "/foo/qux.go"},
			expected: "/foo/qux.go",
		},
		{
			name:     "filename key",
			params:   map[string]any{"filename": "/foo/quux.go"},
			expected: "/foo/quux.go",
		},
		{
			name:     "no matching key",
			params:   map[string]any{"command": "echo hello"},
			expected: "",
		},
		{
			name:     "nil params",
			params:   nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFilePath(tt.params)
			if got != tt.expected {
				t.Errorf("extractFilePath() = %q, want %q", got, tt.expected)
			}
		})
	}
}
