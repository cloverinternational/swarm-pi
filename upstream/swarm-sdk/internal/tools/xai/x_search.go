package xai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// XSearchTool searches X (Twitter) using xAI's native x_search Responses API tool.
// xAI has privileged, real-time access to all X data (posts, threads, profiles,
// images, videos). Requires SuperGrok OAuth or XAI_API_KEY.
type XSearchTool struct{}

var _ tools.Tool = &XSearchTool{}

func NewXSearchTool() *XSearchTool { return &XSearchTool{} }

func (t *XSearchTool) Name() string { return "x_search" }

func (t *XSearchTool) Description() string {
	return `Search X (Twitter) posts, threads, and profiles using xAI's native X Search.

xAI has real-time, privileged access to all of X's data (Elon Musk owns both
companies). This tool is fundamentally different from web scraping — Grok
runs the search server-side with full fidelity to actual post content,
engagement, and recency.

Use this tool to:
- Find recent tweets on any topic
- Search specific users' posts (allowed_x_handles)
- Research what's being said about a person, company, or event on X
- Analyze media attached to posts (images, videos)
- Filter by date range

Returns the answer synthesized by Grok with direct citation links to posts.`
}

func (t *XSearchTool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "What to search for on X. Can be a topic, hashtag, question, or statement.",
			},
			"allowed_x_handles": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only include posts from these X handles (max 10, without @). Mutually exclusive with excluded_x_handles.",
			},
			"excluded_x_handles": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exclude posts from these X handles (max 10, without @). Mutually exclusive with allowed_x_handles.",
			},
			"from_date": map[string]any{
				"type":        "string",
				"description": "Start date for results in YYYY-MM-DD format.",
			},
			"to_date": map[string]any{
				"type":        "string",
				"description": "End date for results in YYYY-MM-DD format.",
			},
			"enable_image_understanding": map[string]any{
				"type":        "boolean",
				"description": "Whether to analyze images attached to X posts. Default false.",
				"default":     false,
			},
			"enable_video_understanding": map[string]any{
				"type":        "boolean",
				"description": "Whether to analyze videos attached to X posts. Default false.",
				"default":     false,
			},
		},
		"required": []string{"query"},
	}
}

func (t *XSearchTool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	query, _ := params["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: query is required")), nil
	}

	if !HasCredentials() {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: no xAI credentials found — run /auth xai to sign in with SuperGrok OAuth")), nil
	}

	// Build the x_search tool definition.
	toolDef := map[string]any{"type": "x_search"}

	if handles, ok := extractHandles(params, "allowed_x_handles"); ok {
		if len(handles) > 10 {
			return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: allowed_x_handles supports at most 10 handles")), nil
		}
		if excl, ok2 := extractHandles(params, "excluded_x_handles"); ok2 && len(excl) > 0 {
			return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: allowed_x_handles and excluded_x_handles cannot both be set")), nil
		}
		toolDef["allowed_x_handles"] = handles
	} else if handles, ok := extractHandles(params, "excluded_x_handles"); ok {
		if len(handles) > 10 {
			return tools.NewXMLResult(tools.NewXML("error").Field("message", "error: excluded_x_handles supports at most 10 handles")), nil
		}
		toolDef["excluded_x_handles"] = handles
	}

	if from, _ := params["from_date"].(string); strings.TrimSpace(from) != "" {
		toolDef["from_date"] = strings.TrimSpace(from)
	}
	if to, _ := params["to_date"].(string); strings.TrimSpace(to) != "" {
		toolDef["to_date"] = strings.TrimSpace(to)
	}
	if imgUnderstanding, _ := params["enable_image_understanding"].(bool); imgUnderstanding {
		toolDef["enable_image_understanding"] = true
	}
	if vidUnderstanding, _ := params["enable_video_understanding"].(bool); vidUnderstanding {
		toolDef["enable_video_understanding"] = true
	}

	result, err := callResponses(ctx, query, toolDef)
	if err != nil {
		return tools.NewXMLResult(tools.NewXML("error").Field("message", fmt.Sprintf("x_search error: %v", err))), nil
	}

	out := map[string]any{
		"success": true,
		"tool":    "x_search",
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
		Attr("tool", "x_search").
		Field("content", string(outJSON))), nil
}

// extractHandles coerces an array param to a []string, stripping leading @.
func extractHandles(params map[string]any, key string) ([]string, bool) {
	raw, ok := params[key]
	if !ok {
		return nil, false
	}
	var result []string
	switch v := raw.(type) {
	case []string:
		for _, h := range v {
			h = strings.TrimSpace(strings.TrimPrefix(h, "@"))
			if h != "" {
				result = append(result, h)
			}
		}
	case []any:
		for _, item := range v {
			if s, ok2 := item.(string); ok2 {
				s = strings.TrimSpace(strings.TrimPrefix(s, "@"))
				if s != "" {
					result = append(result, s)
				}
			}
		}
	default:
		return nil, false
	}
	return result, len(result) > 0
}
