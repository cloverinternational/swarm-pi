package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestMermaidViewport(t *testing.T) {
	tests := []struct {
		name  string
		width int
		want  mermaidViewport
	}{
		{
			name:  "negative width saturates safely",
			width: -10,
			want:  mermaidViewport{Available: 1, Outer: 1, Inner: 1, Content: 1, Tiny: true, Compact: true},
		},
		{
			name:  "tiny width is not forced to old minimum",
			width: 12,
			want:  mermaidViewport{Available: 12, Outer: 12, Inner: 10, Content: 8, Tiny: true, Compact: true},
		},
		{
			name:  "tiny boundary",
			width: 23,
			want:  mermaidViewport{Available: 23, Outer: 23, Inner: 21, Content: 19, Tiny: true, Compact: true},
		},
		{
			name:  "compact boundary",
			width: 24,
			want:  mermaidViewport{Available: 24, Outer: 24, Inner: 22, Content: 20, Compact: true},
		},
		{
			name:  "regular boundary",
			width: 48,
			want:  mermaidViewport{Available: 48, Outer: 48, Inner: 46, Content: 44},
		},
		{
			name:  "wide boundary",
			width: 88,
			want:  mermaidViewport{Available: 88, Outer: 88, Inner: 86, Content: 84, Wide: true},
		},
		{
			name:  "wide viewport caps outer dimensions",
			width: 200,
			want:  mermaidViewport{Available: 200, Outer: 120, Inner: 118, Content: 116, Wide: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newMermaidViewport(tt.width)
			if got != tt.want {
				t.Fatalf("newMermaidViewport(%d) = %#v, want %#v", tt.width, got, tt.want)
			}
			callerLimit := tt.width
			if callerLimit < 1 {
				callerLimit = 1
			}
			if got.Outer > callerLimit {
				t.Fatalf("outer width %d exceeds caller limit %d", got.Outer, callerLimit)
			}
			if got.Outer > mermaidMaxViewportWidth {
				t.Fatalf("outer width %d exceeds wide-screen cap", got.Outer)
			}
		})
	}
}

func TestMermaidTruncate(t *testing.T) {
	const red = "\x1b[31m"
	const reset = "\x1b[0m"

	tests := []struct {
		name  string
		input string
		width int
		plain string
	}{
		{name: "negative width", input: "anything", width: -1, plain: ""},
		{name: "zero width", input: "anything", width: 0, plain: ""},
		{name: "ASCII", input: "abcdef", width: 4, plain: "abcd"},
		{name: "wide Unicode", input: "界界a", width: 4, plain: "界界"},
		{name: "does not split wide Unicode", input: "界a", width: 1, plain: ""},
		{name: "combining character stays attached", input: "e\u0301x", width: 1, plain: "e\u0301"},
		{name: "ANSI styling consumes no cells", input: red + "hello" + reset, width: 3, plain: "hel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mermaidTruncateCell(tt.input, tt.width)
			if plain := ansi.Strip(got); plain != tt.plain {
				t.Fatalf("plain result = %q, want %q (raw %q)", plain, tt.plain, got)
			}
			limit := tt.width
			if limit < 0 {
				limit = 0
			}
			if gotWidth := ansi.StringWidth(got); gotWidth > limit {
				t.Fatalf("result width = %d, exceeds %d (raw %q)", gotWidth, limit, got)
			}
		})
	}
}

func TestMermaidWrap(t *testing.T) {
	t.Run("non-positive width is deterministic", func(t *testing.T) {
		for _, width := range []int{-4, 0} {
			got := mermaidWrapCell("unbounded", width)
			if len(got) != 1 || got[0] != "" {
				t.Fatalf("width %d: got %#v, want one empty line", width, got)
			}
		}
	})

	t.Run("wide Unicode and combining characters", func(t *testing.T) {
		got := mermaidWrapCell("界e\u0301界x", 3)
		want := []string{"界e\u0301", "界x"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("got %#v, want %#v", got, want)
		}
		assertMermaidLineWidths(t, got, 3)
	})

	t.Run("ANSI styled text", func(t *testing.T) {
		got := mermaidWrapCell("\x1b[1;31mabcdef\x1b[0m", 2)
		if plain := ansi.Strip(strings.Join(got, "")); plain != "abcdef" {
			t.Fatalf("wrapped plain text = %q, want %q; raw lines %#v", plain, "abcdef", got)
		}
		assertMermaidLineWidths(t, got, 2)
	})

	t.Run("explicit and blank lines remain stable", func(t *testing.T) {
		got := mermaidWrapCell("abcd\n\n界界\n", 2)
		wantPlain := []string{"ab", "cd", "", "界", "界", ""}
		if len(got) != len(wantPlain) {
			t.Fatalf("got %#v, want %d lines", got, len(wantPlain))
		}
		for i := range got {
			if plain := ansi.Strip(got[i]); plain != wantPlain[i] {
				t.Fatalf("line %d = %q, want %q (raw %q)", i, plain, wantPlain[i], got[i])
			}
		}
		assertMermaidLineWidths(t, got, 2)
	})
}

func TestMermaidPad(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		width     int
		wantPlain string
	}{
		{name: "negative width", input: "abc", width: -1, wantPlain: ""},
		{name: "zero width", input: "abc", width: 0, wantPlain: ""},
		{name: "pads ASCII", input: "ab", width: 4, wantPlain: "ab  "},
		{name: "pads wide Unicode by cells", input: "界", width: 4, wantPlain: "界  "},
		{name: "combining character is one cell", input: "e\u0301", width: 3, wantPlain: "e\u0301  "},
		{name: "truncates overflow before padding", input: "界界x", width: 3, wantPlain: "界 "},
		{name: "ANSI style has no padding cost", input: "\x1b[32mgo\x1b[0m", width: 4, wantPlain: "go  "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mermaidPadCell(tt.input, tt.width)
			if plain := ansi.Strip(got); plain != tt.wantPlain {
				t.Fatalf("plain result = %q, want %q (raw %q)", plain, tt.wantPlain, got)
			}
			wantWidth := tt.width
			if wantWidth < 0 {
				wantWidth = 0
			}
			if gotWidth := ansi.StringWidth(got); gotWidth != wantWidth {
				t.Fatalf("result width = %d, want %d (raw %q)", gotWidth, wantWidth, got)
			}
		})
	}
}

func assertMermaidLineWidths(t *testing.T, lines []string, width int) {
	t.Helper()
	for i, line := range lines {
		if got := ansi.StringWidth(line); got > width {
			t.Fatalf("line %d width = %d, exceeds %d (raw %q)", i, got, width, line)
		}
	}
}
