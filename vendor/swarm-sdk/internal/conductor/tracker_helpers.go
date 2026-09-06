package conductor

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Shared helpers for IssueTracker REST adapters (GitHub, ...). Previously these
// lived alongside the (now-removed) Forgejo adapter.

func isConductorLabel(name string) bool {
	return strings.HasPrefix(strings.ToLower(name), "symphony:")
}

func mergeIssues(existing, incoming []Issue) []Issue {
	seen := map[string]bool{}
	for _, i := range existing {
		seen[i.ID] = true
	}
	for _, i := range incoming {
		if !seen[i.ID] {
			existing = append(existing, i)
			seen[i.ID] = true
		}
	}
	return existing
}

func decodeJSON(resp *http.Response, dst any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
