// exa.go — Exa AI search backend for the websearch tool.
//
// API reference: https://exa.ai/docs/reference/search
//
//	POST https://api.exa.ai/search
//	  Header:  x-api-key: <key>
//	  Body:    {
//	             query, type, numResults, category,
//	             includeDomains, excludeDomains,
//	             includeText, excludeText,
//	             startPublishedDate, endPublishedDate,
//	             startCrawlDate, endCrawlDate,
//	             contents: {
//	               highlights: { numSentences, highlightsPerUrl, query },
//	               text: true,            // fallback if highlights disabled
//	             }
//	           }
//	  Resp:    {
//	             results: [{title, url, score, publishedDate, author,
//	                        text, highlights, highlightScores, subpages}],
//	             searchType, costDollars
//	           }
//
// Key insight from WebCode (https://exa.ai/blog/webcode):
//
//	Using `highlights` instead of raw `text` yields 94.8% groundedness at
//	only ~696 avg tokens — far more token-efficient for coding agents than
//	dumping full page text. We default to highlights mode.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/tools"
)

const DefaultExaBaseURL = "https://api.exa.ai"

// ─── Exa request types ────────────────────────────────────────────────────────

type exaSearchRequest struct {
	Query              string       `json:"query"`
	AdditionalQueries  []string     `json:"additionalQueries,omitempty"`
	Type               string       `json:"type,omitempty"` // "auto" | "neural" | "fast" | "instant" | "deep-lite" | "deep" | "deep-reasoning" | "deep-max"
	NumResults         int          `json:"numResults,omitempty"`
	Category           string       `json:"category,omitempty"` // "company" | "research paper" | "news" | "personal site" | "financial report" | "people"
	IncludeDomains     []string     `json:"includeDomains,omitempty"`
	ExcludeDomains     []string     `json:"excludeDomains,omitempty"`
	IncludeText        []string     `json:"includeText,omitempty"`        // up to 1 phrase, max 5 words
	ExcludeText        []string     `json:"excludeText,omitempty"`        // up to 1 phrase, max 5 words
	StartPublishedDate string       `json:"startPublishedDate,omitempty"` // ISO 8601
	EndPublishedDate   string       `json:"endPublishedDate,omitempty"`   // ISO 8601
	StartCrawlDate     string       `json:"startCrawlDate,omitempty"`     // ISO 8601
	EndCrawlDate       string       `json:"endCrawlDate,omitempty"`       // ISO 8601
	Contents           *exaContents `json:"contents,omitempty"`
}

type exaContents struct {
	Text       bool               `json:"text,omitempty"`
	Highlights *exaHighlightsOpts `json:"highlights,omitempty"`
	Summary    *exaSummaryOpts    `json:"summary,omitempty"`
}

// exaHighlightsOpts configures in-document highlight extraction.
// This is the preferred mode for coding agents: it surfaces only the
// relevant section(s) of the page rather than dumping the full text,
// drastically reducing token consumption while improving groundedness.
type exaHighlightsOpts struct {
	NumSentences     int    `json:"numSentences,omitempty"`     // sentences per highlight, default 5
	HighlightsPerUrl int    `json:"highlightsPerUrl,omitempty"` // highlights to return per result, default 3
	Query            string `json:"query,omitempty"`            // query hint for relevance scoring
}

type exaSummaryOpts struct {
	Query string `json:"query,omitempty"`
}

// ─── Exa response types ───────────────────────────────────────────────────────

type exaSearchResponse struct {
	RequestID     string          `json:"requestId,omitempty"`
	AutopromptStr string          `json:"autopromptString,omitempty"`
	Results       []exaResult     `json:"results"`
	SearchType    string          `json:"searchType,omitempty"` // resolved type for "auto" searches
	CostDollars   *exaCostDollars `json:"costDollars,omitempty"`
}

type exaResult struct {
	Score           float64     `json:"score,omitempty"`
	Title           string      `json:"title"`
	ID              string      `json:"id"`
	URL             string      `json:"url"`
	PublishedDate   string      `json:"publishedDate,omitempty"`
	Author          string      `json:"author,omitempty"`
	Text            string      `json:"text,omitempty"`
	Summary         string      `json:"summary,omitempty"`
	Highlights      []string    `json:"highlights,omitempty"`
	HighlightScores []float64   `json:"highlightScores,omitempty"`
	Subpages        []exaResult `json:"subpages,omitempty"`
}

type exaCostBreakdown struct {
	NeuralSearch     float64 `json:"neuralSearch,omitempty"`
	DeepSearch       float64 `json:"deepSearch,omitempty"`
	ContentText      float64 `json:"contentText,omitempty"`
	ContentHighlight float64 `json:"contentHighlight,omitempty"`
	ContentSummary   float64 `json:"contentSummary,omitempty"`
}

type exaCostEntry struct {
	Search    float64           `json:"search,omitempty"`
	Contents  float64           `json:"contents,omitempty"`
	Breakdown *exaCostBreakdown `json:"breakdown,omitempty"`
}

type exaCostDollars struct {
	Total     float64        `json:"total,omitempty"`
	BreakDown []exaCostEntry `json:"breakDown,omitempty"`
}

type exaErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// ─── exaExecParams — all parameters for a single Exa search call ─────────────

type exaExecParams struct {
	query              string
	maxResults         int
	allowedDomains     []string
	blockedDomains     []string
	searchType         string   // "auto" | "fast" | "instant" | "deep-lite" | "deep" | "deep-reasoning"
	category           string   // "news" | "company" | "financial report" | "research paper" | "pdf" | "personal site" | "people"
	startPublishedDate string   // ISO 8601 — only results published after this date
	endPublishedDate   string   // ISO 8601 — only results published before this date
	includeText        []string // text that must appear in result page
	excludeText        []string // text that must not appear in result page
}

// ─── executeExa — called from Tool.Execute ───────────────────────────────────

func (t *Tool) executeExa(ctx context.Context, p exaExecParams) (*tools.ToolResult, error) {
	startTime := time.Now()

	apiKey := t.config.ExaAPIKey
	if apiKey == "" {
		return nil, fmt.Errorf("Exa API key is required: set EXA_API_KEY environment variable or provide ExaAPIKey in config")
	}

	baseURL := t.config.ExaBaseURL
	if baseURL == "" {
		baseURL = DefaultExaBaseURL
	}

	client := &http.Client{Timeout: t.config.Timeout}

	result := tools.NewToolResult("")
	result.WithMetadata("query", p.query)
	result.WithMetadata("backend", "exa")
	result.WithMetadata("max_results", p.maxResults)

	// Resolve search type: explicit param > config default > "auto".
	searchType := p.searchType
	if searchType == "" {
		searchType = t.config.ExaSearchType
	}
	if searchType == "" {
		searchType = "auto"
	}
	result.WithMetadata("search_type", searchType)

	// Use highlights by default for maximum groundedness and token efficiency.
	// Per WebCode benchmarks: highlights achieve 94.8% groundedness at ~696 avg tokens,
	// vs raw text which can be 1-13x longer with more noise.
	contents := &exaContents{
		Highlights: &exaHighlightsOpts{
			NumSentences:     5,
			HighlightsPerUrl: 3,
			Query:            p.query,
		},
	}

	searchReq := exaSearchRequest{
		Query:      p.query,
		Type:       searchType,
		NumResults: p.maxResults,
		Contents:   contents,
	}

	if len(p.allowedDomains) > 0 {
		searchReq.IncludeDomains = p.allowedDomains
	}
	if len(p.blockedDomains) > 0 {
		searchReq.ExcludeDomains = p.blockedDomains
	}
	if p.startPublishedDate != "" {
		searchReq.StartPublishedDate = p.startPublishedDate
	}
	if p.endPublishedDate != "" {
		searchReq.EndPublishedDate = p.endPublishedDate
	}
	if len(p.includeText) > 0 {
		searchReq.IncludeText = p.includeText
	}
	if len(p.excludeText) > 0 {
		searchReq.ExcludeText = p.excludeText
	}
	if p.category != "" {
		searchReq.Category = p.category
	}

	searchResp, err := exaPost[exaSearchRequest, exaSearchResponse](ctx, client, baseURL+"/search", apiKey, searchReq)
	if err != nil {
		return nil, fmt.Errorf("exa search failed: %w", err)
	}

	dur := time.Since(startTime)
	result.WithDuration(dur.Milliseconds())
	result.WithMetadata("num_results", len(searchResp.Results))

	// Surface resolved search type (useful when "auto" is used).
	if searchResp.SearchType != "" {
		result.WithMetadata("resolved_search_type", searchResp.SearchType)
	}

	// Surface API-reported cost when available.
	if searchResp.CostDollars != nil && searchResp.CostDollars.Total > 0 {
		result.WithMetadata("cost_dollars", searchResp.CostDollars.Total)
	}

	out := formatExaResults(p.query, searchResp.Results, dur)
	result.Output = out
	result.Content = []tools.ContentBlock{tools.TextContent(out)}

	return result, nil
}

// formatExaResults renders Exa results as readable markdown.
// Highlights are preferred over raw text; raw text is used as a fallback.
func formatExaResults(query string, results []exaResult, dur time.Duration) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Web Search Results for: %q\n", query)
	sb.WriteString("================================================\n\n")

	if len(results) == 0 {
		sb.WriteString("No results found.\n")
	}

	for i, r := range results {
		fmt.Fprintf(&sb, "## %d. %s\n", i+1, r.Title)
		fmt.Fprintf(&sb, "**URL:** %s\n", r.URL)
		if r.PublishedDate != "" {
			fmt.Fprintf(&sb, "**Published:** %s\n", r.PublishedDate)
		}
		if r.Author != "" {
			fmt.Fprintf(&sb, "**Author:** %s\n", r.Author)
		}
		if r.Score > 0 {
			fmt.Fprintf(&sb, "**Relevance:** %.3f\n", r.Score)
		}

		// Prefer highlights (query-focused sections) over raw text.
		// This yields higher groundedness and far fewer tokens per WebCode.
		if len(r.Highlights) > 0 {
			sb.WriteString("\n**Highlights:**\n")
			for j, h := range r.Highlights {
				score := ""
				if j < len(r.HighlightScores) {
					score = fmt.Sprintf(" _(score: %.3f)_", r.HighlightScores[j])
				}
				fmt.Fprintf(&sb, "> %s%s\n\n", h, score)
			}
		} else if r.Summary != "" {
			fmt.Fprintf(&sb, "\n%s\n", r.Summary)
		} else if r.Text != "" {
			// Raw text fallback — truncate to avoid token bloat.
			preview := r.Text
			if len(preview) > 1200 {
				preview = preview[:1200] + "…"
			}
			fmt.Fprintf(&sb, "\n%s\n", preview)
		}

		// Surface subpages when present (e.g. related sections within a doc site).
		if len(r.Subpages) > 0 {
			sb.WriteString("\n**Related sections:**\n")
			for _, sp := range r.Subpages {
				fmt.Fprintf(&sb, "- [%s](%s)\n", sp.Title, sp.URL)
				if len(sp.Highlights) > 0 {
					fmt.Fprintf(&sb, "  > %s\n", sp.Highlights[0])
				}
			}
		}

		sb.WriteString("\n")
	}

	sb.WriteString("================================================\n")
	fmt.Fprintf(&sb, "Search Metadata:\n- Backend: Exa (api.exa.ai)\n- Duration: %dms\n- Results: %d",
		dur.Milliseconds(), len(results))

	return sb.String()
}

// ─── generic HTTP helper ─────────────────────────────────────────────────────

// exaPost posts JSON body to url, authenticates with apiKey, and decodes
// the response into T. Returns an error for non-200 status codes.
func exaPost[Req any, Resp any](ctx context.Context, client *http.Client, url, apiKey string, body Req) (*Resp, error) {
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
		var apiErr exaErrorResponse
		if jsonErr := json.Unmarshal(raw, &apiErr); jsonErr == nil && (apiErr.Error != "" || apiErr.Message != "") {
			msg := apiErr.Error
			if msg == "" {
				msg = apiErr.Message
			}
			return nil, fmt.Errorf("Exa API error %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("Exa API error %d: %s", resp.StatusCode, string(raw))
	}

	var result Resp
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}
