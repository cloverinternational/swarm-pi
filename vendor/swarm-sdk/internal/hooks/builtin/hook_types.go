package builtin

import "strings"

// HookContext is a minimal context for built-in hook execution.
//
// It intentionally mirrors lifecycle.HookContext without importing the
// lifecycle package, so built-in hooks stay free of that dependency.
type HookContext struct {
	ToolName   string
	ToolInput  map[string]any
	ToolOutput any
	Message    string
}

// HookDecision represents the outcome of a built-in hook evaluation.
type HookDecision struct {
	Decision      string
	SystemMessage string
}

// containsSubstringCaseInsensitive reports whether s contains substr,
// ignoring case. Used by built-in hooks for tolerant message matching.
func containsSubstringCaseInsensitive(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}
