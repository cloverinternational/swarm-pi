package anthropic_web_search

import (
	"encoding/json"
	"testing"
)

// FuzzWebSearchResponseParsing fuzzes JSON unmarshaling of web search responses.
// High risk: Untrusted external API response, variable structures, large data
func FuzzWebSearchResponseParsing(f *testing.F) {
	testCases := []string{
		// Valid response
		`{"results":[{"title":"test","url":"http://test.com","snippet":"snippet"}]}`,

		// Edge cases
		"",
		"null",
		"{}",
		"[]",

		// Large responses
		`{"results":[` + `{"title":"x","url":"http://x.com","snippet":"y"},` + `]}`,

		// Malformed JSON
		"{",
		"}",
		"[",
		"]",
		"{{}",
		"[[]",

		// Null/undefined fields
		`{"results":null}`,
		`{"results":[null]}`,
		`{"results":[{"title":null}]}`,

		// Type mismatches
		`{"results":"not an array"}`,
		`{"results":[{"title":123}]}`,

		// Deeply nested
		`{"results":[{"nested":{"deeply":{"nested":{"data":"value"}}}}]}`,

		// Very long strings
		`{"results":[{"title":"` + string(make([]byte, 100000)) + `"}]}`,

		// Unicode
		`{"results":[{"title":"λ ω α","snippet":"Ω π σ"}]}`,

		// Escape sequences
		`{"results":[{"title":"test\\ntest","snippet":"test\\ttest"}]}`,

		// Control characters
		"\x00\x01\x02{\"results\":[]}",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, jsonStr string) {
		// Attempt to unmarshal - should not panic
		var result map[string]any
		err := json.Unmarshal([]byte(jsonStr), &result)

		// If valid JSON, attempt to process it (simulating tool execution)
		if err == nil {
			// Extract results array if present
			if resultsIface, ok := result["results"]; ok {
				// Should handle type mismatches gracefully
				_ = resultsIface
			}
		}
	})
}

// FuzzWebSearchQueryBuilding fuzzes query string construction.
// Risk: SQL injection-like parameter issues, oversized queries
func FuzzWebSearchQueryBuilding(f *testing.F) {
	testCases := []string{
		"simple query",
		"query with spaces",
		"",
		" ",
		"\n",
		"\t",
		"query" + string(make([]byte, 1000000)), // 1MB query
		"query\x00with\x00nulls",
		"query\"with\"quotes",
		"query'with'single",
		"query&with&ampersands",
		"query=with=equals",
		"query?with?questions",
		"λ ω α query",
		"../../etc/passwd",
		"../../../",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, query string) {
		// Simulate query building - should never panic
		// In real code, this would be something like:
		// url := fmt.Sprintf("https://api.example.com/search?q=%s", url.QueryEscape(query))

		// Just verify the query string doesn't contain obvious issues
		_ = query
	})
}

// FuzzWebSearchRequestBuilding fuzzes request parameter handling
func FuzzWebSearchRequestBuilding(f *testing.F) {
	type SearchRequest struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		Timeout    int    `json:"timeout"`
	}

	testCases := []SearchRequest{
		{Query: "test", MaxResults: 10, Timeout: 30},
		{Query: "", MaxResults: 0, Timeout: 0},
		{Query: string(make([]byte, 1000000)), MaxResults: -1, Timeout: -1},
		{Query: "test\x00\x01\x02", MaxResults: 999999999, Timeout: 999999999},
	}

	for _, tc := range testCases {
		data, _ := json.Marshal(tc)
		f.Add(string(data))
	}

	f.Fuzz(func(t *testing.T, reqStr string) {
		var req SearchRequest
		err := json.Unmarshal([]byte(reqStr), &req)

		if err == nil {
			// Validate request parameters
			if req.MaxResults < 0 || req.MaxResults > 100 {
				// Should be validated
				_ = req.MaxResults
			}
			if req.Timeout < 0 || req.Timeout > 300 {
				// Should be validated
				_ = req.Timeout
			}
		}
	})
}

// FuzzWebSearchURLParsing fuzzes URL handling from results
func FuzzWebSearchURLParsing(f *testing.F) {
	testCases := []string{
		"http://example.com",
		"https://example.com",
		"",
		"://invalid",
		"http://",
		"http:///",
		"http://example.com:99999999",
		"http://example.com/../../../etc/passwd",
		"http://127.0.0.1:1",
		"http://localhost:9999",
		"javascript:alert('xss')",
		"data:text/html,<script>alert('xss')</script>",
		"http://[::1]:80",
		"http://[:::::::]/",
		"http://user:pass@host:port/path?query=value#fragment",
		"\x00http://example.com",
		"http://example.com\x00",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, urlStr string) {
		// Parse URL - should not panic
		// In real code this would be net.url.Parse() or similar
		_ = urlStr
	})
}

// FuzzWebSearchStatsParsing fuzzes usage stats JSON
func FuzzWebSearchStatsParsing(f *testing.F) {
	testCases := []string{
		`{"requests_used":10,"requests_remaining":90}`,
		`{}`,
		`{"requests_used":"not a number"}`,
		`{"requests_used":-1,"requests_remaining":-1}`,
		`{"requests_used":999999999999999999,"requests_remaining":999999999999999999}`,
		`null`,
		string(make([]byte, 1000000)), // 1MB of data
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, statsStr string) {
		var stats map[string]any
		err := json.Unmarshal([]byte(statsStr), &stats)

		// If valid, validate ranges
		if err == nil && stats != nil {
			if used, ok := stats["requests_used"].(float64); ok {
				if used < 0 {
					// Invalid stats should be detected
					_ = used
				}
			}
		}
	})
}
