// exa_api.go — low-level HTTP client for the Exa search API.
//
// Reference (Context7 / exa-labs/openapi-spec):
//
//	POST https://api.exa.ai/search
//	  x-api-key: <key>
//	  { query, type, numResults, includeDomains, excludeDomains, contents: {text:true} }
//	  → { results: [{title, url, score, publishedDate, author, text, summary}] }
//
//	POST https://api.exa.ai/answer
//	  x-api-key: <key>
//	  { query, text: true }
//	  → { answer, citations: [{url, title, author, publishedDate, text}] }
package exa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ─── Request types ────────────────────────────────────────────────────────────

type searchRequest struct {
	Query          string        `json:"query"`
	Type           string        `json:"type,omitempty"`
	NumResults     int           `json:"numResults,omitempty"`
	Category       string        `json:"category,omitempty"`
	IncludeDomains []string      `json:"includeDomains,omitempty"`
	ExcludeDomains []string      `json:"excludeDomains,omitempty"`
	Contents       *contentsOpts `json:"contents,omitempty"`
	StartPublished string        `json:"startPublishedDate,omitempty"`
}

type contentsOpts struct {
	Text    bool         `json:"text,omitempty"`
	Summary *summaryOpts `json:"summary,omitempty"`
}

type summaryOpts struct {
	Query string `json:"query,omitempty"`
}

type answerRequest struct {
	Query string `json:"query"`
	Text  bool   `json:"text"`
}

// ─── Response types ───────────────────────────────────────────────────────────

// SearchResponse is the JSON payload returned by POST /search.
type SearchResponse struct {
	RequestID     string         `json:"requestId,omitempty"`
	AutopromptStr string         `json:"autopromptString,omitempty"`
	Results       []SearchResult `json:"results"`
}

// SearchResult is a single item in a SearchResponse.
type SearchResult struct {
	Score         float64 `json:"score,omitempty"`
	Title         string  `json:"title"`
	ID            string  `json:"id"`
	URL           string  `json:"url"`
	PublishedDate string  `json:"publishedDate,omitempty"`
	Author        string  `json:"author,omitempty"`
	Text          string  `json:"text,omitempty"`
	Summary       string  `json:"summary,omitempty"`
}

// AnswerResponse is the JSON payload returned by POST /answer.
type AnswerResponse struct {
	Answer    string         `json:"answer"`
	Citations []SearchResult `json:"citations"`
}

type apiErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ─── SearchOpts ───────────────────────────────────────────────────────────────

// SearchOpts carries optional search parameters.
type SearchOpts struct {
	NumResults     int
	SearchType     string // "auto" | "neural" | "keyword"
	IncludeDomains []string
	ExcludeDomains []string
	Category       string
	StartPublished string // ISO 8601
	WithText       bool   // fetch full text for each result
}

// ─── Client ───────────────────────────────────────────────────────────────────

// apiClient wraps the Exa HTTP API.
type apiClient struct {
	cfg        *Config
	httpClient *http.Client
}

func newAPIClient(cfg *Config) *apiClient {
	return &apiClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}
}

// Search calls POST /search and returns the results.
func (c *apiClient) Search(ctx context.Context, query string, opts SearchOpts) (*SearchResponse, error) {
	numResults := opts.NumResults
	if numResults <= 0 {
		numResults = c.cfg.NumResults
	}

	searchType := opts.SearchType
	if searchType == "" {
		searchType = c.cfg.SearchType
	}

	req := searchRequest{
		Query:          query,
		Type:           searchType,
		NumResults:     numResults,
		Category:       opts.Category,
		IncludeDomains: opts.IncludeDomains,
		ExcludeDomains: opts.ExcludeDomains,
		StartPublished: opts.StartPublished,
	}

	if opts.WithText {
		req.Contents = &contentsOpts{Text: true}
	} else {
		// Always request text by default for useful results.
		req.Contents = &contentsOpts{Text: true}
	}

	return post[searchRequest, SearchResponse](ctx, c.httpClient, c.cfg.BaseURL+"/search", c.cfg.APIKey, req)
}

// Answer calls POST /answer and returns the AI-generated answer with citations.
func (c *apiClient) Answer(ctx context.Context, query string) (*AnswerResponse, error) {
	req := answerRequest{Query: query, Text: true}
	return post[answerRequest, AnswerResponse](ctx, c.httpClient, c.cfg.BaseURL+"/answer", c.cfg.APIKey, req)
}

// ─── Generic HTTP POST ────────────────────────────────────────────────────────

func post[Req any, Resp any](ctx context.Context, client *http.Client, url, apiKey string, body Req) (*Resp, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr apiErrorResponse
		if jsonErr := json.Unmarshal(raw, &apiErr); jsonErr == nil && (apiErr.Error != "" || apiErr.Message != "") {
			msg := apiErr.Error
			if msg == "" {
				msg = apiErr.Message
			}
			return nil, fmt.Errorf("Exa API %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("Exa API %d: %s", resp.StatusCode, string(raw))
	}

	var result Resp
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &result, nil
}
