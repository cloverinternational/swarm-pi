package codex

import (
	"encoding/json"
	"testing"
)

// The Responses API reports input_tokens inclusive of the cached prefix.
// toTokenUsage must split cached vs uncached (issue: all codex traffic was
// recorded as fresh input, hiding ~50-90% cache discounts from accounting).
func TestToTokenUsageSplitsCachedTokens(t *testing.T) {
	var r codexResponse
	if err := json.Unmarshal([]byte(`{"usage":{"input_tokens":120000,"output_tokens":500,"input_tokens_details":{"cached_tokens":110000}}}`), &r); err != nil {
		t.Fatal(err)
	}
	u := r.toTokenUsage()
	if u == nil {
		t.Fatal("nil usage")
	}
	if u.CacheRead != 110000 {
		t.Errorf("CacheRead = %d, want 110000", u.CacheRead)
	}
	if u.Input != 10000 {
		t.Errorf("Input (uncached) = %d, want 10000", u.Input)
	}
	if u.Total != 120500 {
		t.Errorf("Total = %d, want 120500", u.Total)
	}
}

// Without details the whole prompt counts as fresh input (legacy shape).
func TestToTokenUsageNoDetails(t *testing.T) {
	var r codexResponse
	if err := json.Unmarshal([]byte(`{"usage":{"input_tokens":5000,"output_tokens":10}}`), &r); err != nil {
		t.Fatal(err)
	}
	u := r.toTokenUsage()
	if u.Input != 5000 || u.CacheRead != 0 || u.Total != 5010 {
		t.Errorf("got Input=%d CacheRead=%d Total=%d", u.Input, u.CacheRead, u.Total)
	}
}
