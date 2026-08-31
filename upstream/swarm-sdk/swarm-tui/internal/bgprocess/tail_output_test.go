package bgprocess

import (
	"fmt"
	"testing"
)

// TailOutput replaced a two-step "fetch everything to learn the length, then
// fetch the tail" pattern. It must return exactly the same lines that pattern
// produced, including when the buffer holds fewer lines than requested.
func TestTailMatchesOffsetWindowSemantics(t *testing.T) {
	for _, total := range []int{0, 1, 5, 100, 101, 500} {
		for _, maxLines := range []int{1, 10, 100} {
			buf := NewMemoryBuffer(10 * 1024 * 1024)
			for i := 0; i < total; i++ {
				if err := buf.WriteLine("stdout", fmt.Sprintf("line-%d", i)); err != nil {
					t.Fatalf("WriteLine: %v", err)
				}
			}

			// The old call shape, reproduced exactly.
			all, err := buf.Lines(LineQueryOpts{})
			if err != nil {
				t.Fatalf("Lines(all): %v", err)
			}
			fromLine := 0
			if len(all) > maxLines {
				fromLine = len(all) - maxLines + 1
			}
			want, err := buf.Lines(LineQueryOpts{FromLine: fromLine, MaxLines: maxLines})
			if err != nil {
				t.Fatalf("Lines(window): %v", err)
			}

			got, err := buf.Tail(maxLines)
			if err != nil {
				t.Fatalf("Tail: %v", err)
			}

			if len(got) != len(want) {
				t.Fatalf("total=%d maxLines=%d: Tail returned %d lines, offset-window returned %d",
					total, maxLines, len(got), len(want))
			}
			for i := range want {
				if got[i].Content != want[i].Content {
					t.Fatalf("total=%d maxLines=%d line %d: Tail=%q want %q",
						total, maxLines, i, got[i].Content, want[i].Content)
				}
			}
		}
	}
}

// LineCount is the O(1) signal used to detect new output without copying it.
func TestLineCountTracksWritesWithoutCopying(t *testing.T) {
	buf := NewMemoryBuffer(10 * 1024 * 1024)
	if got := buf.LineCount(); got != 0 {
		t.Fatalf("empty buffer LineCount = %d, want 0", got)
	}
	for i := 1; i <= 250; i++ {
		if err := buf.WriteLine("stdout", fmt.Sprintf("l%d", i)); err != nil {
			t.Fatalf("WriteLine: %v", err)
		}
		if got := buf.LineCount(); got != i {
			t.Fatalf("after %d writes LineCount = %d", i, got)
		}
	}
}
