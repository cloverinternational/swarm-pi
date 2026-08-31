package anthropic

import (
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// TestGetModelContextWindow verifies the per-model defaults track Anthropic's
// published windows (1M GA for Opus 4.6+, Sonnet 4.6+/5, Mythos-class) while
// enforcing the SDK's 300K default cap, and keep 200K models at 200K.
func TestGetModelContextWindow(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		// 1M-class models → capped at the 300K default policy
		{"claude-opus-4-6", provider.DefaultContextWindowCap},
		{"claude-opus-4-7", provider.DefaultContextWindowCap},
		{"claude-opus-4-8", provider.DefaultContextWindowCap},
		{"claude-sonnet-4-6", provider.DefaultContextWindowCap},
		{"claude-sonnet-5-20260301", provider.DefaultContextWindowCap},
		{"claude-fable-5", provider.DefaultContextWindowCap},
		{"claude-mythos-5", provider.DefaultContextWindowCap},

		// 200K models stay at their published window
		{"claude-opus-4-5-20251101", 200_000},
		{"claude-sonnet-4-5-20250929", 200_000},
		{"claude-haiku-4-5-20251001", 200_000},
		{"claude-opus-4-20250514", 200_000}, // date stamp, minor=0
		{"claude-3-7-sonnet", 200_000},

		// Unknown format falls back to the conservative 200K
		{"some-custom-model", 200_000},
	}
	for _, tc := range cases {
		if got := GetModelContextWindow(tc.model); got != tc.want {
			t.Errorf("GetModelContextWindow(%q) = %d, want %d", tc.model, got, tc.want)
		}
	}
}

// TestClampContextWindow pins the cap behavior: published windows above the
// policy ceiling clamp to it; smaller and unknown (0) values pass through.
func TestClampContextWindow(t *testing.T) {
	if got := provider.ClampContextWindow(1_000_000); got != provider.DefaultContextWindowCap {
		t.Errorf("clamp(1M) = %d, want %d", got, provider.DefaultContextWindowCap)
	}
	if got := provider.ClampContextWindow(200_000); got != 200_000 {
		t.Errorf("clamp(200K) = %d, want 200000", got)
	}
	if got := provider.ClampContextWindow(0); got != 0 {
		t.Errorf("clamp(0) = %d, want 0", got)
	}
}
