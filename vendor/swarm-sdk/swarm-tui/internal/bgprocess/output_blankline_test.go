package bgprocess

import (
	"path/filepath"
	"testing"
)

// blankLineFixture is the canonical shape: blank lines at the start, in the
// middle, consecutively, and at the end. Every one of them is real content the
// caller wrote and must survive the buffer.
const blankLineFixture = "\nA\n\nB\n\n\nC\n"

func lineContents(t *testing.T, buf OutputBuffer) []string {
	t.Helper()
	lines, err := buf.Lines(LineQueryOpts{})
	if err != nil {
		t.Fatalf("Lines: %v", err)
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		out = append(out, l.Content)
	}
	return out
}

func assertFixtureLines(t *testing.T, got []string) {
	t.Helper()
	// "\nA\n\nB\n\n\nC\n" is seven newline-terminated lines:
	want := []string{"", "A", "", "B", "", "", "C"}
	if len(got) != len(want) {
		t.Fatalf("line count = %d, want %d\ngot:  %q\nwant: %q", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("line %d = %q, want %q\ngot:  %q\nwant: %q", i, got[i], want[i], got, want)
		}
	}
}

// TestMemoryBufferPreservesBlankLines is the regression for the defect that
// silently corrupted every file the agent read through the shell: a completely
// empty line was dropped on the floor, so `cat file` delivered content the file
// did not contain. Patches written from that view could never match.
func TestMemoryBufferPreservesBlankLines(t *testing.T) {
	buf := NewMemoryBuffer(1 << 20)
	if _, err := buf.Write([]byte(blankLineFixture)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertFixtureLines(t, lineContents(t, buf))
}

func TestFileBufferPreservesBlankLines(t *testing.T) {
	buf, err := NewFileBuffer(filepath.Join(t.TempDir(), "out.txt"), 1<<20)
	if err != nil {
		t.Fatalf("NewFileBuffer: %v", err)
	}
	defer buf.Close()
	if _, err := buf.Write([]byte(blankLineFixture)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertFixtureLines(t, lineContents(t, buf))
}

func TestMultiWriterPreservesBlankLines(t *testing.T) {
	buf := NewMemoryBuffer(1 << 20)
	w := NewStdoutWriter(buf)
	if _, err := w.Write([]byte(blankLineFixture)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertFixtureLines(t, lineContents(t, buf))
}

// TestWriteDoesNotInventTrailingBlankLine is the near-miss. Fixing the drop
// must not go the other way and append a phantom empty line for the newline
// that terminates the final real line.
func TestWriteDoesNotInventTrailingBlankLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  []string
	}{
		{"trailing newline", "A\nB\n", []string{"A", "B"}},
		{"no trailing newline", "A\nB", []string{"A", "B"}},
		{"single line no newline", "A", []string{"A"}},
		{"only a newline", "\n", []string{""}},
		{"empty write", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := NewMemoryBuffer(1 << 20)
			if _, err := buf.Write([]byte(tc.input)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got := lineContents(t, buf)
			if len(got) != len(tc.want) {
				t.Fatalf("input %q: got %q, want %q", tc.input, got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("input %q: got %q, want %q", tc.input, got, tc.want)
				}
			}
		})
	}
}
