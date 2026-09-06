package xai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// WebSearchTool performs real-time web searches using xAI's native web_search
// Responses API tool. Grok browses the web server-side, including X.com.
type WebSearchTool struct{}

var _ tools.Tool = &WebSearchTool{}

func NewWebSearchTool() *WebSearchTool { return &WebSearchTool{} }

func (t *WebSearchTool) Name() string { return "xai_web_search" }

func (t *WebSearchTool) Description() string {
	return `Search the web in real-time using xAI's (Grok's) built-in web search.

Grok browses the live web server-side — fetching pages, reading content, and
synthesizing an answer with source citations. Unlike static search engines,
Grok can follow links and read full page content.

Optionally filter to or exclude specific domains (max 5 each).

Returns Grok's synthesized answer plus citation URLs. Use for current events,
documentation, research — anything that benefits from live web access.`
}

func (t *WebSearchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What to search for.",
			},
			"allowed_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only include results from these domains (max 5, e.g. [\"github.com\"]). Mutually exclusive with excluded_domains.",
			},
			"excluded_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude results from these domains (max 5). Mutually exclusive with allowed_domains.",
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: query is required")), nil
	}

	if !HasCredentials() {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: no xAI credentials found — run /auth xai to sign in with SuperGrok OAuth")), nil
	}

	toolDef := map[string]any{"type": "web_search"}

	allowed := extractDomains(params, "allowed_domains")
	excluded := extractDomains(params, "excluded_domains")

	if len(allowed) > 0 && len(excluded) > 0 {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: allowed_domains and excluded_domains cannot both be set")), nil
	}
	if len(allowed) > 5 {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: allowed_domains supports at most 5 domains")), nil
	}
	if len(excluded) > 5 {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: excluded_domains supports at most 5 domains")), nil
	}

	if len(allowed) > 0 {
		toolDef["filters"] = map[string]any{"allowed_domains": allowed}
	} else if len(excluded) > 0 {
		toolDef["filters"] = map[string]any{"excluded_domains": excluded}
	}

	result, err := callResponses(ctx, query, toolDef)
	if err != nil {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", fmt.Sprintf("xai_web_search error: %v", err))), nil
	}

	out := map[string]any{
		"success": true,
		"tool":    "xai_web_search",
		"query":   query,
		"answer":  result.Answer,
	}
	if len(result.Citations) > 0 {
		out["citations"] = result.Citations
	}
	if len(result.InlineCitations) > 0 {
		out["inline_citations"] = result.InlineCitations
	}

	outJSON, _ := json.MarshalIndent(out, "", "  ")
	return tools.NewXMLResult(tools.NewXML("result").
		Attr("tool", "xai_web_search").
		Attr("query", query).
		Field("content", string(outJSON))), nil
}

func extractDomains(params map[string]any, key string) []string {
	raw, ok := params[key]
	if !ok {
		return nil
	}
	var result []string
	switch v := raw.(type) {
	case []string:
		for _, d := range v {
			d = strings.TrimSpace(d)
			if d != "" {
				result = append(result, d)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok2 := item.(string); ok2 {
				s = strings.TrimSpace(s)
				if s != "" {
					result = append(result, s)
				}
			}
		}
	}
	return result
}
