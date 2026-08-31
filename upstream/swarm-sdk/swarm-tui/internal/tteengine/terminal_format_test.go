package tteengine

import (
	"strings"
	"testing"
)

// referenceFormattedOutput reproduces the ORIGINAL dense-grid algorithm so we
// can assert the optimized GetFormattedOutputString is byte-for-byte identical.
func referenceFormattedOutput(t *Terminal) string {
	canvas := t.Canvas.Canvas
	rows := canvas.Top
	cols := canvas.Right
	grid := make([][]string, rows+1)
	for r := range grid {
		grid[r] = make([]string, cols+1)
		for c := range grid[r] {
			grid[r][c] = " "
		}
	}
	for _, ch := range t.inputCharacters {
		pos := ch.Motion.CurrentCoord.ToCoord()
		rowIdx := canvas.Top - pos.Row
		colIdx := pos.Column - 1
		if rowIdx < 0 || rowIdx >= rows || colIdx < 0 || colIdx >= cols {
			continue
		}
		if ch.IsVisible {
			grid[rowIdx][colIdx] = ch.FormattedSymbol()
		}
	}
	var sb strings.Builder
	for r := 0; r < rows; r++ {
		if r > 0 {
			sb.WriteByte('\n')
		}
		for c := 0; c < cols; c++ {
			sb.WriteString(grid[r][c])
		}
	}
	return sb.String()
}

func makeVisibleTerminal(input string) *Terminal {
	term := NewTerminal(input, TerminalConfig{})
	for _, ch := range term.GetCharacters() {
		term.SetCharacterVisibility(ch, true)
	}
	return term
}

func TestGetFormattedOutputMatchesReference(t *testing.T) {
	cases := []string{
		"hello",
		"hello\nworld",
		"multi\nline\ntext here",
		"  leading spaces kept inside",
		"unicode héllo wörld",
		"",
	}
	for _, in := range cases {
		term := makeVisibleTerminal(in)
		got := term.GetFormattedOutputString()
		want := referenceFormattedOutput(term)
		if got != want {
			t.Errorf("input %q:\n got=%q\nwant=%q", in, got, want)
		}
	}
}

func TestGetFormattedOutputInvisibleAreSpaces(t *testing.T) {
	term := NewTerminal("ab", TerminalConfig{})
	// Nothing made visible → all spaces, same shape as reference.
	got := term.GetFormattedOutputString()
	want := referenceFormattedOutput(term)
	if got != want {
		t.Fatalf("invisible mismatch: got=%q want=%q", got, want)
	}
	if strings.ContainsAny(got, "ab") {
		t.Fatalf("invisible chars leaked into output: %q", got)
	}
}

func TestGetFormattedOutputStableAcrossCalls(t *testing.T) {
	// The reused cellSymbols scratch map must not leak state between frames.
	term := makeVisibleTerminal("frame")
	first := term.GetFormattedOutputString()
	second := term.GetFormattedOutputString()
	if first != second {
		t.Fatalf("output not stable across calls:\nfirst=%q\nsecond=%q", first, second)
	}
}

func BenchmarkGetFormattedOutputString(b *testing.B) {
	// A realistically sized frame: ~80x24 of text.
	var lines []string
	for r := 0; r < 24; r++ {
		lines = append(lines, strings.Repeat("x", 80))
	}
	term := makeVisibleTerminal(strings.Join(lines, "\n"))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = term.GetFormattedOutputString()
	}
}
