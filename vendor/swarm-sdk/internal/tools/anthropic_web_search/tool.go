// Package anthropic_web_search provides Anthropic's server-side web search tool integration.
// This tool leverages Claude's built-in web search capability via the web-search beta flag.
//
// The tool uses the main Anthropic Chat provider with web search enabled, integrating
// seamlessly with the existing OAuth infrastructure and provider system.
//
// Key features:
// - Server-side web search powered by Anthropic's infrastructure
// - Uses OAuth token from Claude Code login
// - Integrates with the main Chat provider
// - Automatic token refresh
// - Rate limiting and usage tracking
package anthropic_web_search

import (
	"context"
	"fmt"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/conversation"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider/anthropic"
	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

// Tool implements the tools.Tool interface for Anthropic Web Search.
// This tool provides web search capabilities powered by Anthropic's server-side
// web search feature, using the user's Claude Code OAuth token for authentication.
type Tool struct {
	// config holds the web search configuration
	config WebSearchConfig
	// model is the model to use for web search requests (optional, uses default if empty)
	model string
}

// Ensure Tool satisfies the interface
var _ tools.Tool = &Tool{}

// New creates a new Anthropic Web Search tool instance with default configuration.
func New() *Tool {
	return &Tool{
		config: DefaultWebSearchConfig(),
		model:  "", // Will use client default
	}
}

// NewWithConfig creates a new Anthropic Web Search tool instance with custom configuration.
func NewWithConfig(config WebSearchConfig) *Tool {
	return &Tool{
		config: config,
		model:  "", // Will use client default
	}
}

// NewWithModel creates a new Anthropic Web Search tool instance with custom configuration and model.
// model parameter specifies which Claude model to use for web searches.
// If empty, uses the default model (claude-sonnet-4-20250514).
func NewWithModel(config WebSearchConfig, model string) *Tool {
	return &Tool{
		config: config,
		model:  model,
	}
}

// Name returns the tool name.
// This follows the naming convention for SDK tools.
func (t *Tool) Name() string {
	return "anthropic_web_search"
}

// Description returns the tool description.
// This description helps the LLM understand when and how to use the tool.
func (t *Tool) Description() string {
	return `Performs a web search using Anthropic's server-side web search capability. 
This tool is powered by Claude's built-in web search feature and uses your Claude Code OAuth authentication.

Use this tool to:
- Find current information, news, or recent events
- Look up technical documentation, APIs, or library usage
- Research topics that require up-to-date information
- Verify facts or find authoritative sources

The search is performed server-side by Anthropic and returns comprehensive results. 
This tool requires an active Claude Code OAuth session (run 'claude login' if not authenticated).

Example queries:
- "latest Go 1.22 features"
- "Kubernetes deployment best practices 2024"
- "current weather API providers"`
}

// Parameters returns the JSON schema for tool parameters.
// Uses the standard map[string]any pattern for JSON schema definition.
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
		},
		"required": []string{"query"},
	}
}

// Validate checks if the given parameters are valid for this tool.
// This is called before Execute to fail fast on invalid input.
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

	// Validate max_results if provided
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

	return nil
}

// Execute performs the web search and returns the results.
// This method:
// 1. Gets the OAuth token and creates an Anthropic provider instance
// 2. Enables the web-search beta flag on the provider
// 3. Makes a Chat request with the search query
// 4. The Anthropic API automatically performs web searches
// 5. Returns the synthesized results
func (t *Tool) Execute(ctx context.Context, params map[string]any) (*tools.ToolResult, error) {
	startTime := time.Now()

	// Validate parameters first
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

	// Get the stored OAuth token
	token, err := anthropic.GetStoredOAuthToken()
	if err != nil {
		return nil, fmt.Errorf("failed to get stored OAuth token: %w (ensure you are logged in with 'claude login')", err)
	}

	// Check if token is valid
	if token == nil || token.AccessToken == "" {
		return nil, fmt.Errorf("stored OAuth token is empty or invalid - please run 'claude login' to authenticate")
	}

	// Check if token is expired and try to refresh
	if anthropic.IsTokenExpired(token) {
		refreshedToken, refreshErr := anthropic.RefreshAndStoreToken()
		if refreshErr != nil {
			return nil, fmt.Errorf("OAuth token expired and refresh failed: %w - please run 'claude login' to re-authenticate", refreshErr)
		}
		token = refreshedToken
	}

	// Create Anthropic provider configured for OAuth with web search enabled
	config := anthropic.Config{
		APIKey:       token.AccessToken,
		IsOAuth:      true,
		DefaultModel: "claude-sonnet-4-20250514",
		BetaHeaders:  []string{"web-search-2025-03-05"}, // Enable web search beta
	}

	// Create provider with noop logger and minimal tracer
	logger := observability.NewNopLogger()
	tracer := &noopTracer{}

	config.Logger = logger
	config.Tracer = tracer
	providerInstance, err := anthropic.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create anthropic provider: %w", err)
	}

	// Use provided model or default
	modelToUse := t.model
	if modelToUse == "" {
		modelToUse = "claude-sonnet-4-20250514"
	}

	// Create web search tool definition according to Anthropic API spec
	// Reference: https://platform.claude.com/docs/en/docs/agents-and-tools/tool-use/web-search-tool
	webSearchTool := provider.Tool{
		Name: "web_search",
		Type: "web_search_20250305", // This is the key - must use correct tool type
		Metadata: map[string]any{
			"max_uses": maxResults,
		},
	}

	// Create chat request with the web search tool
	// Claude will automatically decide to use the tool and perform searches
	userContent := fmt.Sprintf(`Search for information about: %s`, query)

	chatReq := provider.ChatRequest{
		Model: modelToUse,
		Messages: []*conversation.Message{
			{
				Role:    "user",
				Content: userContent,
			},
		},
		Tools: []provider.Tool{webSearchTool},
		SystemPrompt: "You are a helpful assistant. Use the web_search tool to find current information. " +
			"Provide accurate, sourced information with URLs when available.",
	}

	// Make the chat request with web search enabled
	// The Anthropic provider will handle OAuth headers, beta headers, etc.
	resp, err := providerInstance.Chat(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("web search request failed: %w", err)
	}

	// Build the result
	result := tools.NewToolResult("")
	result.WithDuration(time.Since(startTime).Milliseconds())

	// Add search metadata
	result.WithMetadata("query", query)
	result.WithMetadata("model", modelToUse)
	result.WithMetadata("max_results", maxResults)
	if resp.Usage != nil {
		result.WithMetadata("input_tokens", resp.Usage.Input)
		result.WithMetadata("output_tokens", resp.Usage.Output)
	}

	// Extract and format the content from response
	var textContent string
	if resp.Message != nil {
		textContent = resp.Message.Content
	}

	if textContent != "" {
		// Format the output with metadata
		inputTokens := 0
		outputTokens := 0
		if resp.Usage != nil {
			inputTokens = resp.Usage.Input
			outputTokens = resp.Usage.Output
		}

		formattedOutput := fmt.Sprintf(`Web Search Results for: "%s"
================================================

%s

================================================
Search Metadata:
- Model Used: %s
- Input Tokens: %d
- Output Tokens: %d
- Search Duration: %dms`, query, textContent, modelToUse, inputTokens, outputTokens, time.Since(startTime).Milliseconds())

		result.Output = formattedOutput
		result.Content = []tools.ContentBlock{tools.TextContent(formattedOutput)}
	} else {
		result.Output = fmt.Sprintf("No results returned from web search for: %s", query)
		result.Content = []tools.ContentBlock{tools.TextContent(result.Output)}
	}

	return result, nil
}

// noopTracer is a minimal implementation of observability.Tracer that does nothing
type noopTracer struct{}

func (t *noopTracer) StartSpan(ctx context.Context, name string) (context.Context, observability.Span) {
	return ctx, &noopSpan{}
}

func (t *noopTracer) StartSpanWithOptions(ctx context.Context, name string, opts observability.SpanOptions) (context.Context, observability.Span) {
	return ctx, &noopSpan{}
}

func (t *noopTracer) SpanFromContext(ctx context.Context) observability.Span {
	return &noopSpan{}
}

func (t *noopTracer) InjectContext(ctx context.Context, carrier map[string]string) error {
	return nil
}

func (t *noopTracer) ExtractContext(carrier map[string]string) (context.Context, error) {
	return context.Background(), nil
}

// noopSpan is a minimal implementation of observability.Span that does nothing
type noopSpan struct{}

func (s *noopSpan) End()                                                    {}
func (s *noopSpan) SetAttribute(key string, value any)                      {}
func (s *noopSpan) SetAttributes(attrs map[string]any)                      {}
func (s *noopSpan) SetStatus(code observability.StatusCode, message string) {}
func (s *noopSpan) RecordError(err error)                                   {}
func (s *noopSpan) SpanID() string                                          { return "" }
func (s *noopSpan) TraceID() string                                         { return "" }
func (s *noopSpan) Context() context.Context                                { return context.Background() }

// Web search results can change over time, so this is false.
func (t *Tool) IsIdempotent() bool {
	return false
}

// RequiresPermission returns the permissions needed to execute this tool.
// Web search requires network access permission.
func (t *Tool) RequiresPermission() []tools.Permission {
	return []tools.Permission{tools.PermissionNetworkAccess}
}

// SupportedContentTypes returns the content types this tool can produce.
// Web search returns text content.
func (t *Tool) SupportedContentTypes() []tools.ContentType {
	return []tools.ContentType{tools.ContentTypeText}
}

// OptimizationHints provides guidance for efficient tool use.
// Web search is an expensive operation that should not be batched.
func (t *Tool) OptimizationHints() *tools.OptimizationHints {
	return &tools.OptimizationHints{
		PreferSequential:  true,            // Don't run multiple searches in parallel
		EstimatedDuration: 3 * time.Second, // Web searches take a few seconds
		CanBatch:          false,           // Each search is independent
		BatchSize:         0,               // No batching
		Priority:          50,              // Medium priority
		MinimalLatency:    false,           // Not latency-critical
		Cacheable:         true,            // Results can be cached short-term
		CacheTTL:          5 * time.Minute, // Cache for 5 minutes
	}
}

// GetUsageStats returns the current usage statistics from the client.
// This is useful for monitoring rate limits and usage.
func (t *Tool) UsageStats(client *Client) *WebSearchStats {
	return client.WebSearchStats()
}
