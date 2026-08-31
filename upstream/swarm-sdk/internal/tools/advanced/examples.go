package advanced

import (
	"encoding/json"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Example extraction helpers
// ---------------------------------------------------------------------------

// ExtractExamples returns the InputExamples for a tool if it implements
// ToolWithExamples, otherwise nil.
func ExtractExamples(tool tools.Tool) []InputExample {
	if twe, ok := tool.(tools.ToolWithExamples); ok {
		return FromToolExamples(twe.InputExamples())
	}
	return nil
}

// ---------------------------------------------------------------------------
// Token estimation
// ---------------------------------------------------------------------------

// EstimateToolTokens provides a rough estimate of the tokens a single tool
// definition would consume in a provider's context window. The estimate uses
// the ~4 chars ≈ 1 token heuristic applied to the serialised JSON of the
// tool's name, description, parameters schema, and (if present) examples.
//
// This is intentionally approximate; providers differ in tokenisation. The
// goal is order-of-magnitude sizing for deferred-loading decisions.
func EstimateToolTokens(tool tools.Tool) int {
	// Base: name + description
	chars := len(tool.Name()) + len(tool.Description())

	// Parameters schema
	if tool.Parameters() != nil {
		if b, err := json.Marshal(tool.Parameters()); err == nil {
			chars += len(b)
		}
	}

	// Examples (if any)
	examples := ExtractExamples(tool)
	if len(examples) > 0 {
		if b, err := json.Marshal(examples); err == nil {
			chars += len(b)
		}
	}

	// Overhead for JSON envelope (type, required, etc.)
	chars += 100

	tokens := max(chars/4, 1)
	return tokens
}

// ---------------------------------------------------------------------------
// AdvancedToolSpec builder from a tools.Tool
// ---------------------------------------------------------------------------

// BuildSpec constructs an AdvancedToolSpec from a tools.Tool by inspecting
// all opt-in interfaces (Deferrable, ToolWithExamples, ParallelCapable,
// MetadataProvider). This is a read-only snapshot.
func BuildSpec(tool tools.Tool) AdvancedToolSpec {
	spec := AdvancedToolSpec{
		Name:            tool.Name(),
		Description:     tool.Description(),
		EstimatedTokens: EstimateToolTokens(tool),
	}

	// Deferred
	if d, ok := tool.(Deferrable); ok {
		spec.ShouldDefer = d.ShouldDefer()
	}

	// Concurrency
	if pc, ok := tool.(tools.ParallelCapable); ok {
		spec.ConcurrencySafe = pc.SupportsParallel()
	} else {
		// Fallback: idempotent + read-like name → parallel-safe
		spec.ConcurrencySafe = isLikelyParallelSafe(tool)
	}

	// Examples
	spec.InputExamples = extractToolExamples(tool)

	// Metadata (category, tags, source)
	if mp, ok := tool.(tools.MetadataProvider); ok {
		if meta := mp.ToolMetadata(); meta != nil {
			spec.Category = meta.Category
			spec.Tags = meta.Tags
			spec.Source = string(meta.Source)
			spec.ServerName = meta.ServerName
			if meta.SupportsParallel {
				spec.ConcurrencySafe = true
			}
		}
	}

	return spec
}

// extractToolExamples pulls examples from ToolWithExamples if implemented.
func extractToolExamples(tool tools.Tool) []tools.ToolExample {
	if twe, ok := tool.(tools.ToolWithExamples); ok {
		return twe.InputExamples()
	}
	return nil
}

// isLikelyParallelSafe applies simple heuristics (same logic as router.go
// but without importing it — we keep the advanced package dependency-light).
func isLikelyParallelSafe(tool tools.Tool) bool {
	if it, ok := tool.(tools.IdempotentTool); !ok || !it.IsIdempotent() {
		return false
	}
	name := strings.ToLower(tool.Name())
	readPatterns := []string{"read", "get", "list", "search", "grep", "find", "check", "status", "view", "show", "cat"}
	for _, p := range readPatterns {
		if strings.Contains(name, p) {
			return true
		}
	}
	return false
}

// BuildSpecs constructs AdvancedToolSpecs for every tool in a registry.
func BuildSpecs(registry tools.Registry) []AdvancedToolSpec {
	names := registry.List()
	specs := make([]AdvancedToolSpec, 0, len(names))
	for _, name := range names {
		tool, err := registry.Get(name)
		if err != nil || tool == nil {
			continue
		}
		specs = append(specs, BuildSpec(tool))
	}
	return specs
}
