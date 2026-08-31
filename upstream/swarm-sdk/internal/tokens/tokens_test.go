package tokens

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestEstimate_Empty(t *testing.T) {
	if got := Estimate(""); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}

func TestEstimate_ShortClampedToOne(t *testing.T) {
	// Any non-empty input — even one byte — should report at least 1 token.
	// Otherwise caller code that does `if Estimate(s) > 0` silently skips
	// tiny-but-present payloads.
	for _, in := range []string{"a", "hi", "x"} {
		if got := Estimate(in); got != 1 {
			t.Fatalf("Estimate(%q) = %d, want 1", in, got)
		}
	}
}

func TestEstimate_LongText(t *testing.T) {
	// 4 chars/token on a 400-char string → 100 tokens.
	s := strings.Repeat("a", 400)
	if got := Estimate(s); got != 100 {
		t.Fatalf("want 100, got %d", got)
	}
}

func TestTruncate_FitsUnchanged(t *testing.T) {
	s := "hello world"
	if got := Truncate(s, 100); got != s {
		t.Fatalf("expected unchanged, got %q", got)
	}
}

func TestTruncate_ZeroBudget(t *testing.T) {
	if got := Truncate("anything", 0); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestTruncate_ClipsToBudget(t *testing.T) {
	s := strings.Repeat("a", 1000)
	got := Truncate(s, 50) // 50 tokens * 4 chars = 200
	if len(got) != 200 {
		t.Fatalf("len=%d, want 200", len(got))
	}
}

func TestTruncate_RuneBoundary(t *testing.T) {
	// Pick a budget that would otherwise land in the middle of a 3-byte
	// rune. Input is a stream of "✓" (3 bytes each, valid UTF-8).
	s := strings.Repeat("✓", 100) // 300 bytes
	// budget=25 tokens → 100 bytes. 100 is inside a rune (33rd check: 33*3=99).
	got := Truncate(s, 25)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8: %q", got)
	}
	// Should be trimmed to a rune boundary: 99 bytes (33 runes).
	if len(got) != 99 {
		t.Fatalf("len=%d, want 99 (rune-aligned)", len(got))
	}
}

func TestTruncateWithMarker_AppendsMarker(t *testing.T) {
	s := strings.Repeat("a", 1000)
	marker := "\n[truncated]"
	got := TruncateWithMarker(s, 50, marker)
	if !strings.HasSuffix(got, marker) {
		t.Fatalf("missing marker suffix: %q", got)
	}
	if Estimate(got) > 50 {
		t.Fatalf("result exceeds budget: est=%d, budget=50", Estimate(got))
	}
}

func TestTruncateWithMarker_MarkerFitsBudget(t *testing.T) {
	// Marker alone is bigger than the budget — should return empty, not a
	// partial marker that blows the limit.
	long := strings.Repeat("x", 100)
	marker := strings.Repeat("M", 200) // 50 tokens
	if got := TruncateWithMarker(long, 10, marker); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestTruncateWithMarker_FitsUnchanged(t *testing.T) {
	s := "fits easily"
	marker := "\n[cut]"
	if got := TruncateWithMarker(s, 1000, marker); got != s {
		t.Fatalf("expected unchanged, got %q", got)
	}
}
