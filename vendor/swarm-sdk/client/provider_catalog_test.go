package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTokenCount(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"262144", 262144, true},
		{"200k", 200_000, true},
		{"200K", 200_000, true},
		{"1M", 1_000_000, true},
		{"1.5M", 1_500_000, true},
		{"1_000_000", 1_000_000, true},
		{"", 0, false},
		{"abc", 0, false},
		{"-5k", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTokenCount(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseTokenCount(%q) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestLookupContextWindowFrom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "providers.json")
	// Mirrors the real schema: one fireworks entry with the legacy "context"
	// string field, one openrouter entry with the canonical context_window int.
	payload := `[
		{"name":"fireworks","models":[
			{"id":"accounts/fireworks/routers/kimi-k2p5-turbo","context":"200k"},
			{"id":"accounts/fireworks/models/llama-v3p1","context_window":131072}
		]},
		{"name":"openrouter","models":[
			{"id":"moonshotai/kimi-k2.5","context_window":262144}
		]},
		{"name":"broken","models":[
			{"id":"no-window","context":""},
			{"id":"bad-window","context":"abc"}
		]}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		provider, model string
		want            int
		ok              bool
	}{
		{"fireworks", "accounts/fireworks/routers/kimi-k2p5-turbo", 200_000, true},
		{"Fireworks", "accounts/fireworks/routers/kimi-k2p5-turbo", 200_000, true},
		{"fireworks", "accounts/fireworks/models/llama-v3p1", 131_072, true},
		{"openrouter", "moonshotai/kimi-k2.5", 262_144, true},
		{"", "moonshotai/kimi-k2.5", 262_144, true},     // provider-agnostic fallback
		{"nope", "moonshotai/kimi-k2.5", 262_144, true}, // wrong provider, still resolves via model id
		{"fireworks", "no-such-model", 0, false},
		{"broken", "no-window", 0, false},
		{"broken", "bad-window", 0, false},
	}
	for _, c := range cases {
		got, ok := lookupContextWindowFrom(path, c.provider, c.model)
		if got != c.want || ok != c.ok {
			t.Errorf("lookupContextWindowFrom(%q, %q) = (%d, %v), want (%d, %v)",
				c.provider, c.model, got, ok, c.want, c.ok)
		}
	}
}

func TestLookupContextWindowFrom_MissingFile(t *testing.T) {
	got, ok := lookupContextWindowFrom(filepath.Join(t.TempDir(), "nope.json"), "x", "y")
	if ok || got != 0 {
		t.Errorf("expected (0,false) for missing file, got (%d,%v)", got, ok)
	}
}
