package toolout

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBoundUnderLimitsUnchanged(t *testing.T) {
	input := "first\nsecond\nlast"
	got := Bound(input, 100, 10)
	if got.Output != input || got.Truncated {
		t.Fatalf("Bound() = %#v, want unchanged input", got)
	}
}

func TestBoundCharLimitPreservesHeadAndTail(t *testing.T) {
	input := "FIRST-LINE\n" + strings.Repeat("middle-", 20) + "\nLAST-LINE"
	got := Bound(input, 40, 100)
	if !got.Truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(got.Output, "FIRST-LINE") {
		t.Fatalf("bounded output lost head: %q", got.Output)
	}
	if !strings.Contains(got.Output, "LAST-LINE") {
		t.Fatalf("bounded output lost tail: %q", got.Output)
	}
	if len(got.Output) > 40 {
		t.Fatalf("bounded output has %d chars, limit 40", len(got.Output))
	}
}

func TestBoundLineLimit(t *testing.T) {
	input := strings.Join([]string{"one", "two", "three", "four", "five", "six"}, "\n")
	got := Bound(input, 1_000, 4)
	if !got.Truncated {
		t.Fatal("expected line-only truncation")
	}
	if lines := strings.Count(got.Output, "\n") + 1; lines > 4 {
		t.Fatalf("bounded output has %d lines, limit 4: %q", lines, got.Output)
	}
	if !strings.HasPrefix(got.Output, "one") || !strings.HasSuffix(got.Output, "six") {
		t.Fatalf("bounded output did not preserve head and tail: %q", got.Output)
	}

	oneLine := Bound(input, 1_000, 1)
	if strings.Contains(oneLine.Output, "\n") ||
		!strings.HasPrefix(oneLine.Output, "one") ||
		!strings.HasSuffix(oneLine.Output, "six") {
		t.Fatalf("one-line bound did not preserve both ends: %q", oneLine.Output)
	}
}

func TestBoundUTF8Boundary(t *testing.T) {
	input := "HEAD🙂🙂🙂🙂🙂TAIL"
	got := Bound(input, 13, 10)
	if !got.Truncated {
		t.Fatal("expected truncation")
	}
	if !utf8.ValidString(got.Output) || !utf8.ValidString(got.Head) || !utf8.ValidString(got.Tail) {
		t.Fatalf("Bound split a UTF-8 rune: %#v", got)
	}
}

func TestBoundProvenanceAccounting(t *testing.T) {
	input := "header\n" + strings.Repeat("payload", 20) + "\nfooter"
	got := Bound(input, 35, 10)
	if got.OriginalChars != len(input) {
		t.Fatalf("OriginalChars = %d, want %d", got.OriginalChars, len(input))
	}
	if got.OriginalLines != 3 {
		t.Fatalf("OriginalLines = %d, want 3", got.OriginalLines)
	}
	if got.ElidedChars != len(input)-len(got.Head)-len(got.Tail) {
		t.Fatalf("ElidedChars = %d, want %d", got.ElidedChars, len(input)-len(got.Head)-len(got.Tail))
	}
	if got.HeadChars+got.TailChars+got.ElidedChars != got.OriginalChars {
		t.Fatalf("accounting mismatch: head=%d tail=%d elided=%d original=%d",
			got.HeadChars, got.TailChars, got.ElidedChars, got.OriginalChars)
	}
}

func TestBoundEmptyAndExactLimit(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    string
		maxChars int
		maxLines int
	}{
		{name: "empty", input: "", maxChars: 10, maxLines: 2},
		{name: "exact", input: "one\ntwo", maxChars: len("one\ntwo"), maxLines: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := Bound(test.input, test.maxChars, test.maxLines)
			if got.Truncated || got.Output != test.input {
				t.Fatalf("Bound() = %#v, want unchanged input", got)
			}
		})
	}
}
