// Package websearch provides a unified web search tool with support for multiple
// backends: Anthropic's server-side web search (via OAuth) and Exa's AI-powered
// search API. The tool is named "websearch" and replaces the legacy
// "anthropic_web_search" tool.
//
// # Backends
//
// BackendAnthropic (default): uses Claude's built-in web-search beta feature
// and the user's Claude Code OAuth token. Requires `claude login`.
//
// BackendExa: uses the Exa API (https://exa.ai). Requires an EXA_API_KEY
// environment variable or an explicit ExaAPIKey in the config.
//
// # Usage
//
//	// Auto-detect: uses Exa if EXA_API_KEY is set, otherwise Anthropic OAuth.
//	tool := websearch.New()
//
//	// Explicitly choose a backend:
//	tool := websearch.NewWithConfig(websearch.WebSearchConfig{
//	    Backend: websearch.BackendExa,
//	    ExaAPIKey: "exa-...",
//	})
package websearch

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// BackendType identifies which search engine backs the tool.
type BackendType string

const (
	// BackendAnthropic uses Anthropic's server-side web search (Claude OAuth).
	BackendAnthropic BackendType = "anthropic"
	// BackendExa uses the Exa AI search API (api.exa.ai).
	BackendExa BackendType = "exa"
)

// Tool implements the tools.Tool interface for web search.
type Tool struct {
	config WebSearchConfig
	model  string // model for the Anthropic backend (empty = default)
}

// Ensure Tool satisfies the interface.
var _ tools.Tool = &Tool{}

// New creates a new websearch tool with auto-detected backend.
// If EXA_API_KEY is set in the environment the Exa backend is used;
// otherwise the Anthropic OAuth backend is used.
func New() *Tool {
	cfg := DefaultWebSearchConfig()
	if key := os.Getenv("EXA_API_KEY"); key != "" {
		cfg.Backend = BackendExa
		cfg.ExaAPIKey = key
	}
	return &Tool{config: cfg}
}

// NewWithConfig creates a new websearch tool with custom configuration.
func NewWithConfig(config WebSearchConfig) *Tool {
	// Fall back to env var if key not explicitly provided.
	if config.Backend == BackendExa && config.ExaAPIKey == "" {
		config.ExaAPIKey = os.Getenv("EXA_API_KEY")
	}
	return &Tool{config: config}
}

// NewWithModel creates a websearch tool with a custom model (Anthropic backend only).
func NewWithModel(config WebSearchConfig, model string) *Tool {
	t := NewWithConfig(config)
	t.model = model
	return t
}

// NewWithExa creates a websearch tool configured to use the Exa backend.
func NewWithExa(apiKey string) *Tool {
	return &Tool{
		config: WebSearchConfig{
			Backend:            BackendExa,
			ExaAPIKey:          apiKey,
			ExaBaseURL:         DefaultExaBaseURL,
			Enabled:            true,
			MaxResultsPerQuery: 10,
			Timeout:            30 * time.Second,
			MaxPerSession:      100,
			MaxPerMinute:       10,
		},
	}
}

// ─── tools.Tool implementation ──────────────────────────────────────────────

func (t *Tool) Name() string { return "websearch" }

func (t *Tool) Description() string {
	return `Performs a web search and returns current, sourced results.

Supports two backends:
- Anthropic (default): powered by Claude's built-in web search via your Claude Code OAuth session.
- Exa: powered by Exa's AI search API (set EXA_API_KEY environment variable or configure explicitly).

Use this tool to:
- Find current information, news, or recent events
- Look up technical documentation, APIs, or library usage
- Research topics that require up-to-date information
- Verify facts or find authoritative sources

For coding agents, prefer the Exa backend which returns query-focused highlights (token-efficient,
high groundedness per WebCode benchmarks) and supports deep search modes for niche/complex queries.

You can restrict results to specific domains with allowed_domains, or exclude domains with
blocked_domains (mutually exclusive). The max_results parameter controls how many results
are fetched (default 10, max 20).

Example queries:
- "latest Go 1.22 features"
- "Kubernetes deployment best practices 2024"
- "current weather API providers"`
}

func (t *Tool) Parameters() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to execute. Be specific and descriptive for better results.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 10, max: 20)",
				"default":     10,
			},
			"allowed_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Only include search results from these domains (e.g. [\"nytimes.com\",\"bbc.com\"]). Cannot be combined with blocked_domains.",
			},
			"blocked_domains": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Never include search results from these domains. Cannot be combined with allowed_domains.",
			},
			"type": map[string]any{
				"type": "string",
				"description": "Search mode (Exa backend only). " +
					"\"auto\" (default) intelligently combines methods. " +
					"\"neural\" uses embedding-based semantic search. " +
					"\"fast\"/\"instant\" trade quality for lower latency. " +
					"\"deep-lite\", \"deep\", \"deep-reasoning\", \"deep-max\" perform deep web research with synthesised output \u2014 " +
					"use \"deep\" or \"deep-reasoning\" for niche coding questions, fresh SDK docs, or changelogs.",
				"enum": []string{"auto", "fast", "instant", "deep-lite", "deep", "deep-reasoning"},
			},
			"start_published_date": map[string]any{
				"type":        "string",
				"description": "Only return pages published after this date (ISO 8601, e.g. \"2025-08-01T00:00:00.000Z\"). Useful for filtering to recent library releases or changelogs. Exa backend only.",
			},
			"end_published_date": map[string]any{
				"type":        "string",
				"description": "Only return pages published before this date (ISO 8601). Exa backend only.",
			},
			"include_text": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Text that must appear in result pages (up to 1 phrase, max 5 words). Pin results to a specific library name or version. Exa backend only.",
			},
			"exclude_text": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Text that must NOT appear in result pages (up to 1 phrase, max 5 words). Exa backend only.",
			},
			"category": map[string]any{
				"type":        "string",
				"description": "Filter results to a content category. Valid values: \"news\", \"company\", \"financial report\", \"research paper\", \"pdf\", \"personal site\", \"people\". Exa backend only.",
				"enum":        []string{"news", "company", "financial report", "research paper", "pdf", "personal site", "people"},
			},
		},
		"required": []string{"query"},
	}
}

// Validate checks parameters before execution.
func (t *Tool) Validate(params map[string]any) error {
	query, ok := params["query"].(string)
	if !ok {
		return fmt.Errorf("query parameter is required and must be a string")
	}
	if query == "" {
		return fmt.Errorf("query cannot be empty")
	}
	if len(query) > 1000 {
		return fmt.Errorf("query exceeds maximum length of 1000 characters")
	}
	if maxResults, ok := params["max_results"]; ok {
		switch v := maxResults.(type) {
		case int:
			if v < 1 || v > 20 {
				return fmt.Errorf("max_results must be between 1 and 20")
			}
		case float64:
			if v < 1 || v > 20 {
				return fmt.Errorf("max_results must be between 1 and 20")
			}
		}
	}
	// allowed_domains and blocked_domains are mutually exclusive.
	_, hasAllowed := params["allowed_domains"]
	_, hasBlocked := params["blocked_domains"]
	if hasAllowed && hasBlocked {
		return fmt.Errorf("allowed_domains and blocked_domains cannot both be specified in the same request")
	}
	// Validate search type if provided.
	if st, ok := params["type"].(string); ok && st != "" {
		validTypes := map[string]bool{
			"auto": true, "fast": true, "instant": true,
			"deep-lite": true, "deep": true, "deep-reasoning": true,
		}
		if !validTypes[st] {
			return fmt.Errorf("invalid search type %q; must be one of: auto, neural, fast, instant, deep-lite, deep, deep-reasoning, deep-max", st)
		}
	}
	return nil
}

// Execute dispatches to the configured backend.
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	if err := t.Validate(params); err != nil {
		return nil, err
	}

	query := params["query"].(string)
	maxResults := 10
	if mr, ok := params["max_results"].(float64); ok {
		maxResults = int(mr)
	} else if mr, ok := params["max_results"].(int); ok {
		maxResults = mr
	}

	var allowedDomains, blockedDomains []string
	if v, ok := params["allowed_domains"]; ok {
		allowedDomains = toStringSlice(v)
	}
	if v, ok := params["blocked_domains"]; ok {
		blockedDomains = toStringSlice(v)
	}

	switch t.config.Backend {
	case BackendExa:
		p := exaExecParams{
			query:          query,
			maxResults:     maxResults,
			allowedDomains: allowedDomains,
			blockedDomains: blockedDomains,
		}
		if st, ok := params["type"].(string); ok {
			p.searchType = st
		}
		if v, ok := params["start_published_date"].(string); ok {
			p.startPublishedDate = v
		}
		if v, ok := params["end_published_date"].(string); ok {
			p.endPublishedDate = v
		}
		if v, ok := params["include_text"]; ok {
			p.includeText = toStringSlice(v)
		}
		if v, ok := params["exclude_text"]; ok {
			p.excludeText = toStringSlice(v)
		}
		if v, ok := params["category"].(string); ok {
			p.category = v
		}
		return t.executeExa(ctx, p)
	default:
		return t.executeAnthropic(ctx, query, maxResults, allowedDomains, blockedDomains)
	}
}

// ─── Anthropic backend ───────────────────────────────────────────────────────

func (t *Tool) executeAnthropic(ctx context.Context, query string, maxResults int, allowedDomains, blockedDomains []string) (*tools.ToolResult, error) {
	startTime := time.Now()

	token, err := anthropic.GetStoredOAuthToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get OAuth token: %w (run 'claude login')", err)
	}
	if token == nil || token.AccessToken == "" {
		return nil, fmt.Errorf("OAuth token is empty — run 'claude login' to authenticate")
	}
	if anthropic.IsTokenExpired(token) {
		token, err = anthropic.RefreshAndStoreToken()
		if err != nil {
			return nil, fmt.Errorf("OAuth token expired and refresh failed: %w — run 'claude login'", err)
		}
	}

	cfg := anthropic.Config{
		APIKey:       token.AccessToken,
		IsOAuth:      true,
		DefaultModel: "claude-sonnet-4-6",
		BetaHeaders:  []string{"web-search-2025-03-05"},
	}
	cfg.Logger = observability.NewNopLogger()
	cfg.Tracer = &noopTracer{}

	prov, err := anthropic.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create anthropic provider: %w", err)
	}

	modelToUse := t.model
	if modelToUse == "" {
		modelToUse = "claude-sonnet-4-6"
	}

	// Build web_search tool definition.
	webSearchTool := provider.Tool{
		Name: "web_search",
		Type: "web_search_20250305",
		Metadata: map[string]any{
			"max_uses": maxResults,
		},
	}
	// Inject domain filters into the tool metadata when provided.
	if len(allowedDomains) > 0 {
		webSearchTool.Metadata["allowed_domains"] = allowedDomains
	}
	if len(blockedDomains) > 0 {
		webSearchTool.Metadata["blocked_domains"] = blockedDomains
	}

	chatReq := provider.ChatRequest{
		Model: modelToUse,
		Messages: []*conversation.Message{
			{
				Role:    "user",
				Content: fmt.Sprintf("Search for information about: %s", query),
			},
		},
		Tools:        []provider.Tool{webSearchTool},
		SystemPrompt: "You are a helpful assistant. Use the web_search tool to find current information. Provide accurate, sourced information with URLs when available.",
	}

	resp, err := prov.Chat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
	}

	result := tools.NewToolResult("")
	result.WithDuration(time.Since(startTime).Milliseconds())
	result.WithMetadata("query", query)
	result.WithMetadata("backend", "anthropic")
	result.WithMetadata("model", modelToUse)
	result.WithMetadata("max_results", maxResults)
	if resp.Usage != nil {
		result.WithMetadata("input_tokens", resp.Usage.Input)
		result.WithMetadata("output_tokens", resp.Usage.Output)
	}

	var textContent string
	if resp.Message != nil {
		textContent = resp.Message.Content
	}

	if textContent != "" {
		inputTokens, outputTokens := 0, 0
		if resp.Usage != nil {
			inputTokens = resp.Usage.Input
			outputTokens = resp.Usage.Output
		}
		out := fmt.Sprintf(`Web Search Results for: "%s"
================================================

%s

================================================
Search Metadata:
- Backend: Anthropic (Claude web-search beta)
- Model Used: %s
- Input Tokens: %d
- Output Tokens: %d
- Duration: %dms`, query, textContent, modelToUse, inputTokens, outputTokens, time.Since(startTime).Milliseconds())
		result.Output = out
		result.Content = []tools.ContentBlock{tools.TextContent(out)}
	} else {
		result.Output = fmt.Sprintf("No results returned from web search for: %s", query)
		result.Content = []tools.ContentBlock{tools.TextContent(result.Output)}
	}

	return result, nil
}

// ─── Optional interfaces ─────────────────────────────────────────────────────

func (t *Tool) IsIdempotent() bool { return false }

func (t *Tool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionNetworkAccess}
}

func (t *Tool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

func (t *Tool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{
		PreferSequential:  true,
		EstimatedDuration: 3 * time.Second,
		CanBatch:          false,
		BatchSize:         0,
		Priority:          50,
		MinimalLatency:    false,
		Cacheable:         true,
		CacheTTL:          5 * time.Minute,
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// toStringSlice coerces an interface{} that should be []string (as decoded from JSON).
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		result := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				result = append(result, str)
			}
		}
		return result
	}
	return nil
}

// ─── noopTracer / noopSpan ───────────────────────────────────────────────────

type noopTracer struct{}

func (t *noopTracer) StartSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	return ctx, &noopSpan{}
}
func (t *noopTracer) StartSpanWithOptions(ctx context.Context, name string, opts observability.SpanOptions) (context.Context, observability.Span) {
	return ctx, &noopSpan{}
}
func (t *noopTracer) SpanFromContext(ctx context.Context) observability.Span { return &noopSpan{} }
func (t *noopTracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	return nil
}
func (t *noopTracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	return context.Background(), nil
}

type noopSpan struct{}

func (s *noopSpan) End()                                                    {}
func (s *noopSpan) SetAttribute(key string, value any)                      {}
func (s *noopSpan) SetAttributes(attrs map[string]any)                      {}
func (s *noopSpan) SetStatus(code observability.StatusCode, message string) {}
func (s *noopSpan) RecordError(err error)                                   {}
func (s *noopSpan) SpanID() string                                          { return "" }
func (s *noopSpan) TraceID() string                                         { return "" }
func (s *noopSpan) Context() context.Context                                { return context.Background() }
