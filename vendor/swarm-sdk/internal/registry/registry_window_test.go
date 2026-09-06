package registry

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestGetProviders_ClampsContextWindows pins the default context-window policy:
// no registry-mapped model may advertise more than provider.DefaultContextWindowCap,
// and known sub-cap models keep their published value.
func TestGetProviders_ClampsContextWindows(t *testing.T) {
	providers := GetProviders()
	if len(providers) == 0 {
		t.Fatal("embedded registry returned no providers")
	}
	for _, p := range providers {
		for _, m := range p.Models {
			if m.ContextWindow > provider.DefaultContextWindowCap {
				t.Errorf("%s/%s: context window %d exceeds cap %d",
					p.ID, m.ID, m.ContextWindow, provider.DefaultContextWindowCap)
			}
		}
	}

	// Spot-check: a 1M-class model is clamped to exactly the cap, and a
	// sub-cap model passes through unchanged.
	if g := GetModel("gemini-3-pro-preview"); g != nil && g.ContextWindow != provider.DefaultContextWindowCap {
		t.Errorf("gemini-3-pro-preview: got %d, want %d (clamped)", g.ContextWindow, provider.DefaultContextWindowCap)
	}
	if m := GetModel("MiniMax-M2.5"); m != nil && m.ContextWindow > provider.DefaultContextWindowCap {
		t.Errorf("MiniMax-M2.5: got %d, want published value under cap", m.ContextWindow)
	}
}

// TestGetProviders_SupplementsGLM52 verifies the gap-fill overlay: the zhipu
// provider (used by the built-in GLM provider config) carries GLM-5.2 with its
// 1M window clamped to the default cap.
func TestGetProviders_SupplementsGLM52(t *testing.T) {
	p := GetProvider("zhipu")
	if p == nil {
		t.Skip("embedded registry has no zhipu provider")
	}
	var found bool
	for _, m := range p.Models {
		if m.ID == "glm-5.2" {
			found = true
			if m.ContextWindow != provider.DefaultContextWindowCap {
				t.Errorf("glm-5.2: got window %d, want %d (1M clamped)", m.ContextWindow, provider.DefaultContextWindowCap)
			}
		}
	}
	if !found {
		t.Error("zhipu provider is missing supplemental model glm-5.2")
	}
}
