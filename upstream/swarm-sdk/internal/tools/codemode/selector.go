// Package codemode provides code-mode execution for the swarm-sdk.
//
// Code mode wraps multiple tools into a single run_code tool, allowing the
// model to orchestrate tool calls with JavaScript code instead of one model
// round-trip per tool call.
package codemode

import (
	"slices"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Selector determines which tools are sandboxed inside run_code.
// Tools that match the selector become callable functions inside the
// JavaScript sandbox; non-matching tools remain visible as normal tools.
type Selector interface {
	// Match returns true if the tool should be sandboxed.
	Match(td ToolDefinition) bool
}

// ToolDefinition is a minimal view of tools.Tool for selectors.
// This avoids importing the full Tool interface and its dependencies.
type ToolDefinition interface {
	Name() string
	Description() string
	Parameters() any
}

// toolDefinitionAdapter adapts tools.Tool to ToolDefinition.
type toolDefinitionAdapter struct {
	tool tools.Tool
}

func (a *toolDefinitionAdapter) Name() string        { return a.tool.Name() }
func (a *toolDefinitionAdapter) Description() string { return a.tool.Description() }
func (a *toolDefinitionAdapter) Parameters() any     { return a.tool.Parameters() }

// AdaptTool converts a tools.Tool to a ToolDefinition for selectors.
func AdaptTool(t tools.Tool) ToolDefinition {
	return &toolDefinitionAdapter{tool: t}
}

// AllTools selects all tools for sandboxing.
// This is the default behavior when no selector is specified.
type AllTools struct{}

// Match returns true for all tools.
func (AllTools) Match(td ToolDefinition) bool {
	return true
}

// ByNames selects tools by their exact names.
// Only tools whose names appear in the list are sandboxed.
type ByNames struct {
	names map[string]bool
}

// NewByNames creates a selector that matches tools by name.
func NewByNames(names ...string) *ByNames {
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}
	return &ByNames{names: nameSet}
}

// Match returns true if the tool's name is in the list.
func (s *ByNames) Match(td ToolDefinition) bool {
	return s.names[td.Name()]
}

// ByPredicate selects tools using a custom predicate function.
type ByPredicate struct {
	fn func(ToolDefinition) bool
}

// NewByPredicate creates a selector using a custom function.
// The function receives the tool definition and returns true to sandbox.
func NewByPredicate(fn func(ToolDefinition) bool) *ByPredicate {
	return &ByPredicate{fn: fn}
}

// Match delegates to the predicate function.
func (s *ByPredicate) Match(td ToolDefinition) bool {
	return s.fn(td)
}

// ByMetadata selects tools by matching metadata tags.
// Tools that implement CodeModeEligible with matching tags are sandboxed.
// This is an opt-in mechanism for tools to declare they're safe for code mode.
type ByMetadata struct {
	tags map[string]any
}

// CodeModeEligible is an optional interface tools can implement to declare
// their eligibility for code-mode sandboxing with associated metadata.
type CodeModeEligible interface {
	// CodeModeMetadata returns key-value pairs describing the tool's
	// code-mode capabilities (e.g., "read-only": true, "safe": true).
	CodeModeMetadata() map[string]any
}

// NewByMetadata creates a selector that matches metadata tags.
// The tool must have all specified tags with matching values.
func NewByMetadata(tags map[string]any) *ByMetadata {
	return &ByMetadata{tags: tags}
}

// Match returns true if the tool has matching metadata.
// For tools that don't implement CodeModeEligible, this returns false.
func (s *ByMetadata) Match(td ToolDefinition) bool {
	// Try to get the underlying tool if it's an adapter
	adapter, ok := td.(*toolDefinitionAdapter)
	if !ok {
		return false
	}

	// Check if it implements CodeModeEligible
	eligible, ok := adapter.tool.(CodeModeEligible)
	if !ok {
		return false
	}

	metadata := eligible.CodeModeMetadata()
	if metadata == nil {
		return false
	}

	// All specified tags must match
	for key, val := range s.tags {
		toolVal, exists := metadata[key]
		if !exists {
			return false
		}
		if !s.valuesEqual(toolVal, val) {
			return false
		}
	}
	return true
}

// valuesEqual compares two values for equality.
func (s *ByMetadata) valuesEqual(a, b any) bool {
	// Handle common cases
	switch a := a.(type) {
	case string:
		if bStr, ok := b.(string); ok {
			return a == bStr
		}
	case bool:
		if bBool, ok := b.(bool); ok {
			return a == bBool
		}
	case int:
		if bInt, ok := b.(int); ok {
			return a == bInt
		}
	case float64:
		if bFloat, ok := b.(float64); ok {
			return a == bFloat
		}
	case []string:
		if bSlice, ok := b.([]string); ok {
			return slices.Equal(a, bSlice)
		}
	case map[string]any:
		if bMap, ok := b.(map[string]any); ok {
			if len(a) != len(bMap) {
				return false
			}
			for k, v := range a {
				if !s.valuesEqual(v, bMap[k]) {
					return false
				}
			}
			return true
		}
	}
	return false
}

// ExcludeNames wraps another selector and excludes specific names.
// Useful for combining with AllTools to exclude a few tools.
type ExcludeNames struct {
	inner    Selector
	excluded map[string]bool
}

// NewExcludeNames creates a selector that excludes specific names.
func NewExcludeNames(inner Selector, names ...string) *ExcludeNames {
	excluded := make(map[string]bool, len(names))
	for _, n := range names {
		excluded[n] = true
	}
	return &ExcludeNames{inner: inner, excluded: excluded}
}

// Match returns true if inner matches and name is not excluded.
func (s *ExcludeNames) Match(td ToolDefinition) bool {
	if s.excluded[td.Name()] {
		return false
	}
	return s.inner.Match(td)
}

// Chain combines multiple selectors with AND semantics.
// All selectors must match for the tool to be sandboxed.
type Chain struct {
	selectors []Selector
}

// NewChain creates a selector that requires all inner selectors to match.
func NewChain(selectors ...Selector) *Chain {
	return &Chain{selectors: selectors}
}

// Match returns true if all inner selectors match.
func (s *Chain) Match(td ToolDefinition) bool {
	for _, sel := range s.selectors {
		if !sel.Match(td) {
			return false
		}
	}
	return true
}

// Any combines multiple selectors with OR semantics.
// At least one selector must match for the tool to be sandboxed.
type Any struct {
	selectors []Selector
}

// NewAny creates a selector that requires any inner selector to match.
func NewAny(selectors ...Selector) *Any {
	return &Any{selectors: selectors}
}

// Match returns true if any inner selector matches.
func (s *Any) Match(td ToolDefinition) bool {
	for _, sel := range s.selectors {
		if sel.Match(td) {
			return true
		}
	}
	return false
}
