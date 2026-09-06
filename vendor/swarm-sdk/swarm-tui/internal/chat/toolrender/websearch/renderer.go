package websearch

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/chat/toolrender/shared"
	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/i18n"
)

// WebSearchRenderer renders web search and web fetch tool results
// with structured result cards showing title, URL, and content snippets.
type WebSearchRenderer struct{}

// New creates a new WebSearchRenderer.
func New() *WebSearchRenderer {
	return &WebSearchRenderer{}
}

// CanRender returns true if this renderer handles the given tool context.
// Matches: WebSearch, web_search, WebFetch, web_fetch, and MCP tools
// containing "web_search" or "search_web".
// Content fallback: output is JSON containing both "results" and "query" keys.
func (r *WebSearchRenderer) CanRender(ctx *toolrender.RenderContext) bool {
	name := ctx.ToolName

	// Direct name matches
	switch name {
	case "WebSearch", "web_search", "web-search", "WebFetch", "web_fetch":
		return true
	}

	// MCP tool name patterns
	lower := strings.ToLower(name)
	if strings.Contains(lower, "web_search") || strings.Contains(lower, "search_web") {
		return true
	}

	// Content-based fallback: JSON containing "results" and "query"
	if ctx.Output != "" {
		output := strings.TrimSpace(ctx.Output)
		if strings.HasPrefix(output, "{") &&
			strings.Contains(output, `"results"`) &&
			strings.Contains(output, `"query"`) {
			return true
		}
	}

	return false
}

// Render returns styled output lines for web search results.
// If a cached WebSearchResult is available, its pre-rendered lines are returned directly.
// Otherwise the output is parsed and rendered on the fly.
func (r *WebSearchRenderer) Render(ctx *toolrender.RenderContext, cached toolrender.CachedResult) []string {
	// Use cached result if available and still valid
	if cached != nil {
		if wsr, ok := cached.(*WebSearchResult); ok && !wsr.NeedsRerender(ctx.Width) {
			return wsr.GetRenderedLines()
		}
	}

	// Render from scratch
	result := r.renderFromOutput(ctx.Output, ctx.Width)
	if len(result) == 0 {
		return []string{shared.FitLine("    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+shared.AnsiFgDim+i18n.T("toolrender.websearch.no_results")+shared.AnsiReset, ctx.Width)}
	}
	return result
}

// PreProcess parses the JSON output and pre-renders search result cards.
// Returns a *WebSearchResult implementing CachedResult.
func (r *WebSearchRenderer) PreProcess(ctx *toolrender.RenderContext) toolrender.CachedResult {
	if ctx.Output == "" {
		return nil
	}

	result := &WebSearchResult{
		RawContent:  ctx.Output,
		RenderWidth: ctx.Width,
		Language:    i18n.CurrentLanguage(),
	}

	result.Lines = r.renderFromOutput(ctx.Output, ctx.Width)
	if len(result.Lines) == 0 {
		result.Lines = []string{shared.FitLine("    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+shared.AnsiFgDim+i18n.T("toolrender.websearch.no_results")+shared.AnsiReset, ctx.Width)}
	}

	return result
}

// renderFromOutput parses the output and returns styled lines.
func (r *WebSearchRenderer) renderFromOutput(output string, width int) []string {
	if output == "" {
		return nil
	}

	// Try JSON parse
	var searchData map[string]any
	if err := json.Unmarshal([]byte(output), &searchData); err != nil {
		// Fall back to plain text
		return shared.FitLines(r.renderPlainText(output), width)
	}

	// WHY THE FINAL CLAMP: every truncation below compares len() (BYTES)
	// against a column budget and then slices bytes — `title[:maxTitleLen-3]`.
	// That mismeasures every wide rune (two columns, three-plus bytes) and can
	// cut a UTF-8 sequence in half, putting invalid bytes into the frame. Each
	// budget is also max(width-N, 10|20): a FLOOR, not a cap, so a narrow pane
	// gets content wider than itself, plus an unmeasured connector prefix on
	// top. Search results are third-party page text, the most attacker-
	// controlled input any renderer here receives.
	//
	// The per-field truncations are kept because they bound work before the
	// styling runs; this clamp makes the result actually fit.
	return shared.FitLines(r.renderSearchResults(searchData, width), width)
}

// renderSearchResults renders structured search result data.
func (r *WebSearchRenderer) renderSearchResults(data map[string]any, width int) []string {
	var lines []string

	// Extract query and add header
	if query, ok := data["query"].(string); ok && query != "" {
		maxQueryLen := max(width-20, 10)
		// Width-aware, not byte-aware: `query[:n]` sliced BYTES against a
		// COLUMN budget, so any CJK/emoji query was cut mid-UTF-8-sequence and
		// the invalid bytes reached the frame. See renderFromOutput.
		displayQuery := shared.ExpandTabsANSI(query)
		if shared.PrintableWidth(displayQuery) > maxQueryLen {
			displayQuery = shared.TruncateANSI(displayQuery, maxQueryLen, "...")
		}
		header := shared.AnsiBold + shared.AnsiFgBlue + i18n.T("toolrender.websearch.search", displayQuery) + shared.AnsiReset
		lines = append(lines, "    "+shared.AnsiFgMuted+"\u23bf"+shared.AnsiReset+" "+header)
		lines = append(lines, "")
	}

	// Extract and format results
	if resultsData, ok := data["results"].([]any); ok && len(resultsData) > 0 {
		for i, resultItem := range resultsData {
			if i > 0 {
				lines = append(lines, "")
			}
			if resultMap, ok := resultItem.(map[string]any); ok {
				resultLines := r.renderSingleResult(resultMap, i+1, width)
				lines = append(lines, resultLines...)
			}
		}
	} else if response, ok := data["response"].(string); ok && response != "" {
		// Fallback: format response text
		responseLines := strings.SplitSeq(response, "\n")
		for line := range responseLines {
			if strings.TrimSpace(line) != "" {
				lines = append(lines, "  "+shared.AnsiFgMuted+"| "+shared.AnsiReset+line)
			}
		}
	}

	return lines
}

// renderSingleResult renders one search result card with title, URL, and snippet.
func (r *WebSearchRenderer) renderSingleResult(result map[string]any, index int, width int) []string {
	var lines []string

	// Title
	title := extractString(result, "title", "url")
	if title != "" {
		maxTitleLen := max(width-20, 10)
		// Width-aware; see renderSearchResults.
		title = shared.ExpandTabsANSI(title)
		if shared.PrintableWidth(title) > maxTitleLen {
			title = shared.TruncateANSI(title, maxTitleLen, "...")
		}
		titleLine := fmt.Sprintf("  %s%d.%s %s%s%s",
			shared.AnsiFgGreen, index, shared.AnsiReset,
			shared.AnsiFgBlue, title, shared.AnsiReset)
		lines = append(lines, titleLine)
	}

	// URL
	if url, ok := result["url"].(string); ok && url != "" {
		maxURLLen := max(width-10, 20)
		// Width-aware; see renderSearchResults.
		displayURL := shared.ExpandTabsANSI(url)
		if shared.PrintableWidth(displayURL) > maxURLLen {
			displayURL = shared.TruncateANSI(displayURL, maxURLLen, "...")
		}
		urlLine := fmt.Sprintf("  %s\u251c\u2500 %s%s%s",
			shared.AnsiFgMuted,
			shared.AnsiFgYellow, displayURL, shared.AnsiReset)
		lines = append(lines, urlLine)
	}

	// Content snippet
	if content, ok := result["encrypted_content"].(string); ok && content != "" {
		contentLines := strings.Split(strings.TrimSpace(content), "\n")
		for i, contentLine := range contentLines {
			if strings.TrimSpace(contentLine) == "" {
				continue
			}

			connector := "\u2502" // middle
			if i == len(contentLines)-1 {
				connector = "\u2514" // last
			}

			maxContentLen := max(width-16, 20)
			// Width-aware; see renderSearchResults. Snippets are third-party
			// page text and are the likeliest field to carry wide runes.
			displayContent := shared.ExpandTabsANSI(contentLine)
			if shared.PrintableWidth(displayContent) > maxContentLen {
				displayContent = shared.TruncateANSI(displayContent, maxContentLen, "...")
			}

			line := fmt.Sprintf("  %s%s\u2500 %s%s%s",
				shared.AnsiFgMuted, connector,
				shared.AnsiFgWhite, displayContent, shared.AnsiReset)
			lines = append(lines, line)
		}
	}

	// Page age metadata
	if age, ok := result["page_age"].(string); ok && age != "" {
		ageLine := fmt.Sprintf("  %s\u2514\u2500 %s%s%s",
			shared.AnsiFgMuted,
			shared.AnsiFgMuted, i18n.T("toolrender.websearch.age", age), shared.AnsiReset)
		lines = append(lines, ageLine)
	}

	return lines
}

// renderPlainText renders output as plain text lines when JSON parsing fails.
func (r *WebSearchRenderer) renderPlainText(output string) []string {
	var result []string
	lines := strings.SplitSeq(output, "\n")
	for line := range lines {
		if strings.TrimSpace(line) != "" {
			result = append(result, "  "+line)
		}
	}
	return result
}

// extractString extracts the first non-empty string value from a map for the given keys.
func extractString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := m[key].(string); ok && val != "" {
			return val
		}
	}
	return ""
}
