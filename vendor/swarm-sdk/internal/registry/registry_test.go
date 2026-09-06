package registry

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

func TestGetProviders(t *testing.T) {
	providers := GetProviders()
	if len(providers) == 0 {
		t.Fatal("expected non-empty list of providers")
	}

	foundAnthropic := false
	for _, p := range providers {
		if p.ID == "anthropic" {
			foundAnthropic = true
			if len(p.Models) == 0 {
				t.Error("anthropic provider has no models")
			}
			break
		}
	}

	if !foundAnthropic {
		t.Error("expected to find anthropic provider in registry")
	}
}

func TestSupplementalOpenAICodexModels(t *testing.T) {
	p := GetProvider("openai")
	if p == nil {
		t.Fatal("expected to find openai provider in registry")
	}

	byID := make(map[string]Model, len(p.Models))
	for _, m := range p.Models {
		byID[m.ID] = m
	}

	// gpt-5.2 is also in the supplemental map, but the embedded catwalk
	// snapshot already carries it, so the embedded entry wins by design —
	// only assert presence for it below.
	if _, ok := byID["gpt-5.2"]; !ok {
		t.Error("openai provider is missing gpt-5.2")
	}

	cases := []struct {
		id     string
		window int64
		hasMax bool
		defRE  string
	}{
		{"gpt-5.6-sol", int64(provider.DefaultContextWindowCap), true, "low"},
		{"gpt-5.6-terra", int64(provider.DefaultContextWindowCap), true, "medium"},
		{"gpt-5.6-luna", int64(provider.DefaultContextWindowCap), true, "medium"},
		{"gpt-5.5", 272_000, false, "medium"},
	}
	for _, tc := range cases {
		m, ok := byID[tc.id]
		if !ok {
			t.Errorf("openai provider is missing supplemental model %s", tc.id)
			continue
		}
		if m.ContextWindow != tc.window {
			t.Errorf("%s: got window %d, want %d", tc.id, m.ContextWindow, tc.window)
		}
		if m.DefaultMaxTokens != 32_768 {
			t.Errorf("%s: got DefaultMaxTokens %d, want 32768", tc.id, m.DefaultMaxTokens)
		}
		if !m.CanReason {
			t.Errorf("%s: expected CanReason", tc.id)
		}
		if m.DefaultReasoningEffort != tc.defRE {
			t.Errorf("%s: got DefaultReasoningEffort %q, want %q", tc.id, m.DefaultReasoningEffort, tc.defRE)
		}
		foundMax := false
		for _, level := range m.ReasoningLevels {
			if level == "max" {
				foundMax = true
			}
		}
		if foundMax != tc.hasMax {
			t.Errorf("%s: max reasoning level presence = %v, want %v (levels %v)", tc.id, foundMax, tc.hasMax, m.ReasoningLevels)
		}
	}
}

// TestSupplementalModelsDedupeAgainstEmbedded asserts the supplemental
// gap-fill never duplicates a model the embedded catwalk snapshot already
// carries — i.e. a future catwalk bump that adds gpt-5.6-* supersedes the
// supplemental entries automatically.
func TestSupplementalModelsDedupeAgainstEmbedded(t *testing.T) {
	for _, p := range GetProviders() {
		seen := make(map[string]bool, len(p.Models))
		for _, m := range p.Models {
			id := strings.ToLower(m.ID)
			if seen[id] {
				t.Errorf("provider %s carries duplicate model ID %s", p.ID, m.ID)
			}
			seen[id] = true
		}
	}
}

func TestGetModel(t *testing.T) {
	model := GetModel("gpt-4o")
	if model == nil {
		t.Error("expected to find gpt-4o in registry")
	} else if model.Name != "GPT-4o" {
		t.Errorf("expected model name GPT-4o, got %s", model.Name)
	}
}
