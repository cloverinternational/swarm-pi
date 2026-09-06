package advanced

import (
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ---------------------------------------------------------------------------
// Provider Bridge — enriches provider.Tool Metadata with advanced fields
// ---------------------------------------------------------------------------

// EnrichProviderTool takes a provider.Tool (as produced by
// agent.convertToProviderTool) and its underlying tools.Tool, then populates
// the provider.Tool's Metadata map with all advanced fields that the
// provider translator can optionally consume.
//
// Fields written to Metadata:
//   - input_examples  []InputExample (if tool implements ToolWithExamples)
//   - should_defer    bool           (if tool implements Deferrable)
//   - concurrency_safe bool          (from ParallelCapable or heuristics)
//   - category        string         (from MetadataProvider)
//   - tags            []string       (from MetadataProvider)
//   - estimated_tokens int           (computed)
//   - source          string         (from MetadataProvider)
//   - server_name     string         (from MetadataProvider, MCP tools)
//
// This function is safe to call on any provider.Tool — it will not overwrite
// existing Metadata keys that were set by the caller or the provider.
func EnrichProviderTool(pt provider.Tool, sdkTool tools.Tool) provider.Tool {
	if pt.Metadata == nil {
		pt.Metadata = make(map[string]any)
	}

	spec := BuildSpec(sdkTool)

	// Input examples
	if len(spec.InputExamples) > 0 {
		setIfAbsent(pt.Metadata, MetaKeyInputExamples, FromToolExamples(spec.InputExamples))
	}

	// Deferred
	if spec.ShouldDefer {
		setIfAbsent(pt.Metadata, MetaKeyShouldDefer, true)
	}

	// Concurrency safety
	setIfAbsent(pt.Metadata, MetaKeyConcurrencySafe, spec.ConcurrencySafe)

	// Category
	if spec.Category != "" {
		setIfAbsent(pt.Metadata, MetaKeyCategory, spec.Category)
	}

	// Tags
	if len(spec.Tags) > 0 {
		setIfAbsent(pt.Metadata, MetaKeyTags, spec.Tags)
	}

	// Estimated tokens
	if spec.EstimatedTokens > 0 {
		setIfAbsent(pt.Metadata, MetaKeyEstimatedTokens, spec.EstimatedTokens)
	}

	// Source
	if spec.Source != "" {
		setIfAbsent(pt.Metadata, MetaKeySource, spec.Source)
	}

	// Server name
	if spec.ServerName != "" {
		setIfAbsent(pt.Metadata, MetaKeyServerName, spec.ServerName)
	}

	return pt
}

// EnrichProviderTools enriches a slice of provider.Tool in bulk. The
// sdkToolsByName map is keyed by tool name and used to look up the
// underlying tools.Tool for each provider.Tool.
//
// Tools without a matching SDK tool are returned unchanged.
func EnrichProviderTools(pts []provider.Tool, sdkToolsByName map[string]tools.Tool) []provider.Tool {
	enriched := make([]provider.Tool, len(pts))
	for i, pt := range pts {
		if sdkTool, ok := sdkToolsByName[pt.Name]; ok {
			enriched[i] = EnrichProviderTool(pt, sdkTool)
		} else {
			enriched[i] = pt
		}
	}
	return enriched
}

// EnrichProviderToolsFromRegistry enriches a slice of provider.Tool using
// the SDK registry to look up each tool. This is a convenience wrapper
// around EnrichProviderTools.
func EnrichProviderToolsFromRegistry(pts []provider.Tool, registry tools.Registry) []provider.Tool {
	lookup := make(map[string]tools.Tool, len(pts))
	for _, pt := range pts {
		if t, err := registry.Get(pt.Name); err == nil && t != nil {
			lookup[pt.Name] = t
		}
	}
	return EnrichProviderTools(pts, lookup)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setIfAbsent sets a key in a map only if the key is not already present.
func setIfAbsent(m map[string]any, key string, value any) {
	if _, exists := m[key]; !exists {
		m[key] = value
	}
}
