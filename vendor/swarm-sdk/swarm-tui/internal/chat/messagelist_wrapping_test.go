package chat

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestWrapLineToWidth_HardBreaksOversizedWhitespaceFreeToken is the
// regression for the 2026-07-24 "TaskManage gigantic output treated as 1
// line" bug report. extractWordsWithStyles splits only on spaces/tabs, so a
// single whitespace-free token (e.g. compact/minified JSON like
// `{"status":"succeeded","results":[...]}` — exactly what a raw TaskManage
// tool-result blob looks like) is one "word" that can be thousands of
// columns wide. wrapLineToWidth used to leave such a word entirely
// unbroken on a single result line ("Word is too long, will need to break
// it" was commented but never implemented), producing a wrapped line whose
// display width vastly exceeds the requested width. That corrupts every
// downstream assumption (YOffset arithmetic, preWrappedMapping, scroll
// anchoring, hit-testing) that each entry in preWrappedLines fits within
// `width` columns. This test proves the line is now hard-broken to width.
func TestWrapLineToWidth_HardBreaksOversizedWhitespaceFreeToken(t *testing.T) {
	// Simulate a compact JSON blob: no spaces at all, far wider than any
	// realistic terminal width.
	var sb strings.Builder
	sb.WriteString(`{"status":"succeeded","results":[`)
	for i := 0; i < 40; i++ {
		sb.WriteString(`{"key":"t","op":"update","status":"succeeded"},`)
	}
	sb.WriteString(`]}`)
	blob := sb.String()

	const width = 80
	if lipgloss.Width(blob) <= width {
		t.Fatalf("test setup broken: blob width %d must exceed wrap width %d", lipgloss.Width(blob), width)
	}

	got := wrapLineToWidth(blob, width)

	if len(got) < 2 {
		t.Fatalf("expected the oversized blob to be hard-broken into multiple lines, got %d line(s)", len(got))
	}

	for i, line := range got {
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("wrapped line %d has width %d, exceeds requested width %d — hard-break failed: %q", i, w, width, line)
		}
	}

	// Reassembling the wrapped chunks (order-preserving, no dropped/duplicated
	// characters) must reproduce the original content. Each intermediate
	// chunk gets a trailing "\x1b[0m" reset appended (consistent with how
	// every other continuation line in this function is terminated before
	// being appended to result), which must be stripped before comparing —
	// that's an intentional style boundary, not lost/duplicated content.
	rejoined := strings.ReplaceAll(strings.Join(got, ""), "\x1b[0m", "")
	if rejoined != blob {
		t.Fatalf("hard-broken chunks do not reconstruct the original text.\n got: %q\nwant: %q", rejoined, blob)
	}
}

// TestWrapLinesToWidthWithMapping_MappingStaysConsistentForOversizedToken
// proves the raw->wrapped mapping produced alongside the hard-broken lines
// still satisfies its documented invariant (mapping[i+1]-mapping[i] equals
// the number of wrapped lines raw line i produced, and every wrapped line
// fits within width) — the invariant every offset-space conversion
// (rawFromWrappedIndex / wrappedFromRawIndex) depends on.
func TestWrapLinesToWidthWithMapping_MappingStaysConsistentForOversizedToken(t *testing.T) {
	blob := `{"status":"succeeded","results":[` + strings.Repeat(`{"a":"b"},`, 30) + `]}`
	lines := []string{"before", blob, "after"}
	const width = 40

	wrapped, mapping := wrapLinesToWidthWithMapping(lines, width)

	if len(mapping) != len(lines)+1 {
		t.Fatalf("expected mapping length %d, got %d", len(lines)+1, len(mapping))
	}
	if mapping[len(mapping)-1] != len(wrapped) {
		t.Fatalf("mapping's final cumulative count %d does not match len(wrapped) %d", mapping[len(mapping)-1], len(wrapped))
	}
	for i := 1; i < len(mapping); i++ {
		if mapping[i] < mapping[i-1] {
			t.Fatalf("mapping must be monotonically non-decreasing, got mapping[%d]=%d < mapping[%d]=%d", i, mapping[i], i-1, mapping[i-1])
		}
	}
	// The oversized blob (raw line 1) must have produced more than one
	// wrapped line, and every wrapped line it produced must fit in width.
	blobWrappedCount := mapping[2] - mapping[1]
	if blobWrappedCount < 2 {
		t.Fatalf("expected oversized raw line to expand into multiple wrapped lines, got %d", blobWrappedCount)
	}
	for i := mapping[1]; i < mapping[2]; i++ {
		if w := lipgloss.Width(wrapped[i]); w > width {
			t.Fatalf("wrapped line %d (from oversized raw line) has width %d > %d", i, w, width)
		}
	}
}
