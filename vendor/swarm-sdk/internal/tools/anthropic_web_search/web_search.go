// Package claudewebsearch provides a Go implementation of Claude's web search tool
// Reverse engineered from Claude Code v2.1.25 binary
// Document: docs/analysis-reports/WEB_SEARCH_TOOL_REVERSE_ENGINEERING.md
// Analysis Date: 2025-01-30
//
// This implementation recreates the web search functionality based on:
// - String extraction from the Claude Code binary
// - Pattern analysis of beta flags and API structures
// - Usage tracking mechanism reverse engineering
//
// Key discovery: Web search is a SERVER-SIDE tool enabled via the
// "web-search-2025-03-05" beta flag. The API handles search execution
// and returns results via the server_tool_use.web_search_requests field.

package anthropic_web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// ============================================================================
// CONSTANTS - Discovered from binary analysis
// ============================================================================

// BetaFlag represents an Anthropic API beta feature flag
// These flags were extracted from the minified JavaScript in the Claude Code binary
type BetaFlag string

// Known beta flags discovered in Claude Code binary v2.1.25
// Variable mappings:
//   - $VR = WEB_SEARCH_BETA ("web-search-2025-03-05")
//   - ZVR = Set of standard betas
//   - KVR = Set of code-specific betas
const (
	// OAuth-related betas (CRITICAL for OAuth token authentication)
	BetaOAuth               BetaFlag = "oauth-2025-04-20" // REQUIRED for OAuth tokens
	BetaInterleavedThinking BetaFlag = "interleaved-thinking-2025-05-14"

	// Context and feature betas
	BetaContext1M BetaFlag = "context-1m-2025-08-07" // Used in getContextWindow for 1M context detection

	// Tool-related betas
	BetaWebSearch       BetaFlag = "web-search-2025-03-05" // KEY: Enables server-side web search
	BetaToolExamples    BetaFlag = "tool-examples-2025-10-29"
	BetaAdvancedToolUse BetaFlag = "advanced-tool-use-2025-11-20"
	BetaToolSearch      BetaFlag = "tool-search-tool-2025-10-19"
)

// API constants
const (
	DefaultBaseURL   = "https://api.anthropic.com"
	APIVersion       = "2023-06-01"
	MessagesEndpoint = "/v1/messages"
	DefaultTimeout   = 60 * time.Second
)

// Model constants (from getMaxOutputTokens in binary)
const (
	DefaultMaxOutputTokens = 32000
	ContextWindow200K      = 200000
	ContextWindow1M        = 1000000
)

// ============================================================================
// TYPES - API Request/Response structures
// ============================================================================

// ServerToolUse represents the server-side tool usage from API response
// This structure was discovered by analyzing the usage tracking code:
//
//	D.webSearchRequests += R.server_tool_use?.web_search_requests ?? 0
type ServerToolUse struct {
	WebSearchRequests int `json:"web_search_requests"`
}

// Usage represents the token and tool usage from API response
// Maps to the usage block returned by Anthropic's Messages API
type Usage struct {
	InputTokens              int            `json:"input_tokens"`
	OutputTokens             int            `json:"output_tokens"`
	CacheReadInputTokens     int            `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int            `json:"cache_creation_input_tokens,omitempty"`
	ServerToolUse            *ServerToolUse `json:"server_tool_use,omitempty"`
}

// ModelUsage tracks usage statistics for a specific model
// Recreated from the session state structure in the binary:
//
//	{
//	  inputTokens: 0,
//	  outputTokens: 0,
//	  cacheReadInputTokens: 0,
//	  cacheCreationInputTokens: 0,
//	  webSearchRequests: 0,
//	  costUSD: 0,
//	  contextWindow: 0,
//	  maxOutputTokens: 0
//	}
type ModelUsage struct {
	InputTokens              int     `json:"inputTokens"`
	OutputTokens             int     `json:"outputTokens"`
	CacheReadInputTokens     int     `json:"cacheReadInputTokens"`
	CacheCreationInputTokens int     `json:"cacheCreationInputTokens"`
	WebSearchRequests        int     `json:"webSearchRequests"`
	CostUSD                  float64 `json:"costUSD"`
	ContextWindow            int     `json:"contextWindow"`
	MaxOutputTokens          int     `json:"maxOutputTokens"`
}

// Message represents a conversation message
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ContentBlock represents a content block in the response
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	// Tool use blocks can have additional fields
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// APIRequest represents a request to the Anthropic Messages API
type APIRequest struct {
	Model       string    `json:"model"`
	MaxTokens   int       `json:"max_tokens"`
	Messages    []Message `json:"messages"`
	System      string    `json:"system,omitempty"`
	Stream      bool      `json:"stream,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	TopP        *float64  `json:"top_p,omitempty"`
	TopK        *int      `json:"top_k,omitempty"`
}

// APIResponse represents a response from the Anthropic Messages API
type APIResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        Usage          `json:"usage"`
}

// APIError represents an error response from the API
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// WebSearchConfig holds configuration for web search functionality
type WebSearchConfig struct {
	Enabled            bool          `json:"enabled"`
	MaxResultsPerQuery int           `json:"maxResultsPerQuery"`
	Timeout            time.Duration `json:"timeout"`
	MaxPerSession      int           `json:"maxPerSession"`
	MaxPerMinute       int           `json:"maxPerMinute"`
}

// DefaultWebSearchConfig returns the default web search configuration
// Values based on reasonable defaults and binary analysis
func DefaultWebSearchConfig() WebSearchConfig {
	return WebSearchConfig{
		Enabled:            true,
		MaxResultsPerQuery: 10,
		Timeout:            30 * time.Second,
		MaxPerSession:      100,
		MaxPerMinute:       10,
	}
}

// ============================================================================
// CLIENT - Main API client with web search support
// ============================================================================

// Client is the main client for interacting with Claude API
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	betas      []BetaFlag
	config     WebSearchConfig
	isOAuth    bool   // Flag to indicate if using OAuth token instead of API key
	model      string // Model to use for web search requests

	// Usage tracking (mirrors sessionState.modelUsage from binary)
	modelUsage map[string]*ModelUsage
	usageMu    sync.RWMutex

	// Rate limiting
	searchCount  int
	lastSearches []time.Time
	searchMu     sync.Mutex
}

// ClientOption is a function that configures the client
type ClientOption func(*Client)

// WithBaseURL sets a custom base URL for the API
func WithBaseURL(url string) ClientOption {
	return func(c *Client) {
		c.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithBetas sets the beta flags for the client
func WithBetas(betas ...BetaFlag) ClientOption {
	return func(c *Client) {
		c.betas = append(c.betas, betas...)
	}
}

// WithWebSearch enables web search functionality
func WithWebSearch(config WebSearchConfig) ClientOption {
	return func(c *Client) {
		c.config = config
		if config.Enabled {
			// Ensure web search beta is included
			hasBeta := slices.Contains(c.betas, BetaWebSearch)
			if !hasBeta {
				c.betas = append(c.betas, BetaWebSearch)
			}
		}
	}
}

// WithOAuth indicates that the API key is an OAuth token (Bearer authentication)
// OAuth tokens require different header handling than API keys
func WithOAuth(isOAuth bool) ClientOption {
	return func(c *Client) {
		c.isOAuth = isOAuth
		// OAuth always requires the oauth beta header
		if isOAuth {
			hasBeta := slices.Contains(c.betas, BetaOAuth)
			if !hasBeta {
				// Add OAuth beta header
				c.betas = append(c.betas, BetaOAuth)
			}
		}
	}
}

// WithModel sets the model to use for web search requests
// If not specified, defaults to claude-sonnet-4-20250514
func WithModel(model string) ClientOption {
	return func(c *Client) {
		if model != "" {
			c.model = model
		}
	}
}

// NewClient creates a new Claude API client
func NewClient(apiKey string, opts ...ClientOption) *Client {
	c := &Client{
		apiKey:       apiKey,
		baseURL:      DefaultBaseURL,
		httpClient:   &http.Client{Timeout: DefaultTimeout},
		betas:        make([]BetaFlag, 0), // Start with empty betas, add only what's needed
		config:       DefaultWebSearchConfig(),
		model:        "claude-sonnet-4-20250514", // Default model for web search
		modelUsage:   make(map[string]*ModelUsage),
		lastSearches: make([]time.Time, 0),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// buildBetaHeader builds the anthropic-beta header value
// Format: comma-separated list of beta flag strings
func (c *Client) buildBetaHeader() string {
	betaStrings := make([]string, len(c.betas))
	for i, b := range c.betas {
		betaStrings[i] = string(b)
	}
	return strings.Join(betaStrings, ",")
}

// checkRateLimit checks if we can make another web search request
func (c *Client) checkRateLimit() error {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()

	// Check session limit
	if c.searchCount >= c.config.MaxPerSession {
		return &RateLimitError{
			Message: fmt.Sprintf("web search session limit reached: %d/%d",
				c.searchCount, c.config.MaxPerSession),
			Limit:   c.config.MaxPerSession,
			Current: c.searchCount,
		}
	}

	// Check per-minute rate limit
	now := time.Now()
	oneMinuteAgo := now.Add(-time.Minute)

	// Remove old entries
	newSearches := make([]time.Time, 0, len(c.lastSearches))
	for _, t := range c.lastSearches {
		if t.After(oneMinuteAgo) {
			newSearches = append(newSearches, t)
		}
	}
	c.lastSearches = newSearches

	if len(c.lastSearches) >= c.config.MaxPerMinute {
		return &RateLimitError{
			Message: fmt.Sprintf("web search rate limit reached: %d/%d per minute",
				len(c.lastSearches), c.config.MaxPerMinute),
			Limit:   c.config.MaxPerMinute,
			Current: len(c.lastSearches),
		}
	}

	return nil
}

// recordWebSearch records a web search request for rate limiting
func (c *Client) recordWebSearch(count int) {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()

	c.searchCount += count
	now := time.Now()
	for range count {
		c.lastSearches = append(c.lastSearches, now)
	}
}

// SendMessage sends a message to the Claude API
func (c *Client) SendMessage(ctx context.Context, req *APIRequest) (*APIResponse, error) {
	// Check if web search might be needed and validate rate limit
	if c.config.Enabled && containsSearchIntent(req) {
		if err := c.checkRateLimit(); err != nil {
			return nil, err
		}
	}

	// Build HTTP request
	jsonBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST",
		c.baseURL+MessagesEndpoint, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers (matching Claude Code's request format)
	httpReq.Header.Set("Content-Type", "application/json")

	// OAuth tokens use Bearer authentication, API keys use x-api-key
	if c.isOAuth {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		httpReq.Header.Set("x-app", "cli") // CLI identifier for OAuth
		// User-Agent is required for OAuth requests for proper routing
		httpReq.Header.Set("User-Agent", "claude-cli/2.1.2 (external, cli)")
	} else {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}

	httpReq.Header.Set("anthropic-version", APIVersion)
	httpReq.Header.Set("anthropic-beta", c.buildBetaHeader())

	// Send request
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Check for errors
	if resp.StatusCode != http.StatusOK {
		var apiErr APIError
		if json.Unmarshal(body, &apiErr) == nil {
			return nil, fmt.Errorf("API error %d: %s - %s",
				resp.StatusCode, apiErr.Type, apiErr.Message)
		}
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// Update usage statistics (mirrors eSA function from binary)
	c.updateUsage(&apiResp)

	return &apiResp, nil
}

// updateUsage updates the model usage statistics based on API response
// This mirrors the eSA (updateModelUsage) function discovered in the binary
func (c *Client) updateUsage(resp *APIResponse) {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()

	usage, exists := c.modelUsage[resp.Model]
	if !exists {
		usage = &ModelUsage{
			ContextWindow:   getContextWindow(resp.Model, c.hasBeta(BetaContext1M)),
			MaxOutputTokens: getMaxOutputTokens(resp.Model),
		}
		c.modelUsage[resp.Model] = usage
	}

	// Update token counts
	usage.InputTokens += resp.Usage.InputTokens
	usage.OutputTokens += resp.Usage.OutputTokens
	usage.CacheReadInputTokens += resp.Usage.CacheReadInputTokens
	usage.CacheCreationInputTokens += resp.Usage.CacheCreationInputTokens

	// Update web search count (key insight from reverse engineering)
	// Binary: D.webSearchRequests += R.server_tool_use?.web_search_requests ?? 0
	if resp.Usage.ServerToolUse != nil {
		webSearchCount := resp.Usage.ServerToolUse.WebSearchRequests
		usage.WebSearchRequests += webSearchCount

		// Record for rate limiting
		if webSearchCount > 0 {
			c.recordWebSearch(webSearchCount)
		}
	}

	// Calculate cost (simplified)
	usage.CostUSD += calculateCost(resp.Usage, resp.Model)
}

// hasBeta checks if a beta flag is enabled
func (c *Client) hasBeta(beta BetaFlag) bool {
	return slices.Contains(c.betas, beta)
}

// ============================================================================
// HELPER FUNCTIONS - Ported from binary
// ============================================================================

// containsSearchIntent checks if a request might trigger web search
func containsSearchIntent(req *APIRequest) bool {
	searchKeywords := []string{
		"search the web",
		"search for",
		"look up",
		"find online",
		"latest news",
		"current events",
		"recent updates",
		"web search",
		"search online",
	}

	for _, msg := range req.Messages {
		lowerContent := strings.ToLower(msg.Content)
		for _, keyword := range searchKeywords {
			if strings.Contains(lowerContent, keyword) {
				return true
			}
		}
	}
	return false
}

// getContextWindow returns the context window for a model
// Ported from the QK function in the binary:
//
//	function QK(T, R) {
//	  if (T.includes("[1m]") || R?.includes(W8T) && u8_(T)) return 1e6;
//	  return v8_;  // 200000
//	}
func getContextWindow(model string, has1MBeta bool) int {
	modelLower := strings.ToLower(model)

	// Check for 1M context indicators
	if strings.Contains(model, "[1m]") ||
		(has1MBeta && strings.Contains(modelLower, "claude-sonnet-4")) {
		return ContextWindow1M
	}
	return ContextWindow200K
}

// getMaxOutputTokens returns the max output tokens for a model
// Ported from the G8T function in the binary
func getMaxOutputTokens(model string) int {
	modelLower := strings.ToLower(model)

	switch {
	case strings.Contains(modelLower, "3-5"):
		return 8192
	case strings.Contains(modelLower, "claude-3-opus"):
		return 4096
	case strings.Contains(modelLower, "claude-3-sonnet"):
		return 8192
	case strings.Contains(modelLower, "claude-3-haiku"):
		return 4096
	case strings.Contains(modelLower, "opus-4-5"):
		return 64000
	case strings.Contains(modelLower, "opus-4"):
		return 32000
	case strings.Contains(modelLower, "sonnet-4"),
		strings.Contains(modelLower, "haiku-4"):
		return 64000
	default:
		return DefaultMaxOutputTokens
	}
}

// calculateCost calculates the cost of an API call (simplified estimation)
// Note: Actual pricing varies by model and should be obtained from Anthropic
func calculateCost(usage Usage, model string) float64 {
	// Simplified cost calculation (prices per 1K tokens)
	// These are estimates - real pricing should be fetched from Anthropic
	var inputCostPer1K, outputCostPer1K float64 = 0.003, 0.015
	webSearchCostPerRequest := 0.01

	// Adjust for model
	modelLower := strings.ToLower(model)
	if strings.Contains(modelLower, "opus") {
		inputCostPer1K, outputCostPer1K = 0.015, 0.075
	} else if strings.Contains(modelLower, "haiku") {
		inputCostPer1K, outputCostPer1K = 0.00025, 0.00125
	}

	inputCost := float64(usage.InputTokens) / 1000 * inputCostPer1K
	outputCost := float64(usage.OutputTokens) / 1000 * outputCostPer1K

	var searchCost float64
	if usage.ServerToolUse != nil {
		searchCost = float64(usage.ServerToolUse.WebSearchRequests) * webSearchCostPerRequest
	}

	return inputCost + outputCost + searchCost
}

// ============================================================================
// ERROR TYPES
// ============================================================================

// RateLimitError represents a rate limit exceeded error
type RateLimitError struct {
	Message string
	Limit   int
	Current int
}

func (e *RateLimitError) Error() string {
	return e.Message
}

// IsRateLimitError checks if an error is a rate limit error
func IsRateLimitError(err error) bool {
	_, ok := err.(*RateLimitError)
	return ok
}
