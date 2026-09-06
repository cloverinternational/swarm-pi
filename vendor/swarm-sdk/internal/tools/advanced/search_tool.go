package advanced

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// ToolSearchName is the canonical name of the tool search tool.
// It is exported so that other parts of the framework (e.g. DeferredRegistry)
// can ensure the search tool is never deferred.
const ToolSearchName = "tool_search"

// ---------------------------------------------------------------------------
// ToolSearchTool — dynamic tool discovery for deferred loading
// ---------------------------------------------------------------------------

// ToolSearchTool implements tools.Tool and enables the LLM to discover
// registered tools by name, description, category, or tags. It always
// appears in the eager (non-deferred) tool set so the model can use it
// to find and load deferred tools on demand.
//
// This mirrors the "Tool Search Tool" pattern observed in Claude Code
// where a meta-tool allows the model to search across hundreds of
// available tools without loading all their definitions into context.
type ToolSearchTool struct {
	// registry is the SDK tool registry containing all tools (eager + deferred).
	registry tools.Registry

	// specs are pre-built AdvancedToolSpecs for fast searching. They are
	// rebuilt when Refresh() is called.
	specs []AdvancedToolSpec

	// onPromote is called when a deferred tool is discovered via search.
	// The DeferredRegistry uses this to "promote" the tool from deferred
	// to eager so it appears in subsequent buildProviderTools() calls.
	onPromote func(toolName string)
}

// NewToolSearchTool creates a ToolSearchTool backed by the given registry.
// Call Refresh() after adding or removing tools to update the search index.
func NewToolSearchTool(registry tools.Registry) *ToolSearchTool {
	tst := &ToolSearchTool{
		registry: registry,
	}
	tst.Refresh()
	return tst
}

// Refresh rebuilds the internal search index from the registry.
func (t *ToolSearchTool) Refresh() {
	t.specs = BuildSpecs(t.registry)
}

// SetPromoteCallback sets a function that is called whenever a deferred tool
// is discovered via search. This typically calls DeferredRegistry.ForceDefer(name, false)
// to promote the tool from deferred → eager for subsequent turns.
func (t *ToolSearchTool) SetPromoteCallback(fn func(toolName string)) {
	t.onPromote = fn
}

// Name implements tools.Tool.
func (t *ToolSearchTool) Name() string { return ToolSearchName }

// Description implements tools.Tool.
func (t *ToolSearchTool) Description() string {
	return `Search for available tools by name, description, category, or tags. ` +
		`Use this tool to discover capabilities that are not currently loaded in your context. ` +
		`Returns matching tool specifications including name, description, category, tags, ` +
		`and whether the tool supports parallel execution. ` +
		`When you need a tool that is not in your current tool list, search for it here first.`
}

// Parameters implements tools.Tool.
func (t *ToolSearchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type": "string",
				"description": "Search query to match against tool names, descriptions, categories, and tags. " +
					"Case-insensitive. Supports fuzzy matching (typos OK) and multi-word queries. " +
					"Omit or leave empty to list all available tools.",
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Optional: filter results to a specific category (e.g., 'filesystem', 'network', 'database').",
			},
			"server": map[string]any{
				"type":        "string",
				"description": "Optional: filter results to tools from a specific MCP server.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return. Default: 10.",
			},
		},
		"required": []string{},
	}
}

// Execute implements tools.Tool.
func (t *ToolSearchTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	query, _ := params["query"].(string)

	category, _ := params["category"].(string)
	server, _ := params["server"].(string)
	maxResults := 10
	if mr, ok := params["max_results"].(float64); ok && mr > 0 {
		maxResults = int(mr)
	}

	// Search (empty query lists all tools)
	matches := t.search(query, category, server, maxResults)

	if len(matches) == 0 {
		if query == "" {
			return tools.NewToolResult("No tools available."), nil
		}
		return tools.NewToolResult(fmt.Sprintf("No tools found matching query: %q", query)), nil
	}

	// Auto-promote deferred tools that were found.
	// When the LLM searches for a tool, it clearly needs it, so we promote
	// it from deferred → eager so it appears in the next buildProviderTools() call.
	if t.onPromote != nil {
		for _, match := range matches {
			if match.Deferred {
				t.onPromote(match.Name)
			}
		}
	}

	// Serialize results
	data, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		return tools.NewErrorResult(fmt.Errorf("failed to serialize search results: %w", err)), nil
	}

	result := tools.NewToolResult(string(data))
	result.WithMetadata("match_count", len(matches))
	result.WithMetadata("query", query)
	return result, nil
}

// Validate implements tools.Tool.
func (t *ToolSearchTool) Validate(params map[string]any) error {
	if params == nil {
		return nil // Empty params is valid (list-all mode)
	}
	if query, ok := params["query"]; ok {
		if _, ok := query.(string); !ok {
			return fmt.Errorf("parameter 'query' must be a string")
		}
	}
	return nil
}

// IsIdempotent implements tools.Tool.
func (t *ToolSearchTool) IsIdempotent() bool { return true }

// RequiresPermission implements tools.Tool.
func (t *ToolSearchTool) RequiresPermission() []tools.Permission { return nil }

// SupportedContentTypes implements tools.Tool.
func (t *ToolSearchTool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints implements tools.Tool.
func (t *ToolSearchTool) OptimizationHints() *tools.OptimizationHints {
	return tools.FastReadHints()
}

// SupportsParallel implements tools.ParallelCapable — search is read-only.
func (t *ToolSearchTool) SupportsParallel() bool { return true }

// ---------------------------------------------------------------------------
// Search logic
// ---------------------------------------------------------------------------

// searchResult is the JSON-serialisable search result.
type searchResult struct {
	Name            string              `json:"name"`
	Description     string              `json:"description"`
	Category        string              `json:"category,omitempty"`
	Tags            []string            `json:"tags,omitempty"`
	ConcurrencySafe bool                `json:"concurrency_safe"`
	Deferred        bool                `json:"deferred"`
	Source          string              `json:"source,omitempty"`
	ServerName      string              `json:"server_name,omitempty"`
	Examples        []tools.ToolExample `json:"input_examples,omitempty"`
}

func (t *ToolSearchTool) search(query, category, server string, maxResults int) []searchResult {
	queryLower := strings.ToLower(query)

	// Intermediate type for holding candidates with their scores
	type candidate struct {
		result searchResult
		score  int
	}

	var candidates []candidate

	for _, spec := range t.specs {
		// Skip self
		if spec.Name == ToolSearchName {
			continue
		}

		// Category filter
		if category != "" && !strings.EqualFold(spec.Category, category) {
			continue
		}

		// Server filter
		if server != "" && !strings.EqualFold(spec.ServerName, server) {
			continue
		}

		// Score the match (empty query matches all tools with score 1)
		var score int
		if queryLower == "" {
			score = 1 // List all mode: every tool matches
		} else {
			score = scoreMatch(spec, queryLower)
		}
		if score <= 0 {
			continue
		}

		candidates = append(candidates, candidate{
			result: searchResult{
				Name:            spec.Name,
				Description:     spec.Description,
				Category:        spec.Category,
				Tags:            spec.Tags,
				ConcurrencySafe: spec.ConcurrencySafe,
				Deferred:        spec.ShouldDefer,
				Source:          spec.Source,
				ServerName:      spec.ServerName,
				Examples:        spec.InputExamples,
			},
			score: score,
		})
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Truncate to maxResults and extract results
	var results []searchResult
	for i, cand := range candidates {
		if i >= maxResults {
			break
		}
		results = append(results, cand.result)
	}

	return results
}

// scoreMatch returns a relevance score for a tool spec against a query.
// Returns 0 if the tool does not match at all.
func scoreMatch(spec AdvancedToolSpec, queryLower string) int {
	score := 0

	// Exact name match (highest)
	if strings.EqualFold(spec.Name, queryLower) {
		score += 100
	}

	// Name contains query
	if strings.Contains(strings.ToLower(spec.Name), queryLower) {
		score += 50
	}

	// Description contains query
	if strings.Contains(strings.ToLower(spec.Description), queryLower) {
		score += 20
	}

	// Category match
	if strings.Contains(strings.ToLower(spec.Category), queryLower) {
		score += 15
	}

	// Tag match
	for _, tag := range spec.Tags {
		if strings.Contains(strings.ToLower(tag), queryLower) {
			score += 10
		}
	}

	// Source/server match
	if strings.Contains(strings.ToLower(spec.Source), queryLower) {
		score += 5
	}
	if strings.Contains(strings.ToLower(spec.ServerName), queryLower) {
		score += 5
	}

	return score
}
