package termimage

import (
	"io"
	"os"
	"testing"
)

// termFile mirrors charmbracelet/x/term.File, the interface Bubble Tea v2
// asserts its output writer against to decide whether the output is a TTY. If
// *Writer stops satisfying it, ttyOutput becomes nil, term.GetSize is never
// called, and the renderer is pinned to the 80x24 WithWindowSize fallback,
// which shows a tiny UI island on an otherwise black alt-screen.
type termFile interface {
	io.ReadWriteCloser
	Fd() uintptr
}

func TestWriterSatisfiesTermFile(t *testing.T) {
	var w io.Writer = NewWriter(os.Stdout, nil)
	if _, ok := w.(termFile); !ok {
		t.Fatal("*Writer must satisfy term.File (io.ReadWriteCloser + Fd) or Bubble Tea renders at the 80x24 fallback on a black screen")
	}
}

func TestWriterReadIsNonBlockingEOF(t *testing.T) {
	w := NewWriter(os.Stdout, nil)
	n, err := w.Read(make([]byte, 8))
	if n != 0 || err != io.EOF {
		t.Fatalf("Read stub = (%d, %v), want (0, io.EOF)", n, err)
	}
}

func TestWriterCloseDoesNotCloseUnderlying(t *testing.T) {
	r, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer pw.Close()
	w := NewWriter(pw, nil)
	if err := w.Close(); err != nil {
		t.Fatalf("Close returned %v, want nil", err)
	}
	// The underlying descriptor must remain usable after Writer.Close.
	if _, err := pw.Write([]byte("x")); err != nil {
		t.Fatalf("underlying writer closed by Writer.Close: %v", err)
	}
}

func TestWriterFdMatchesUnderlyingFile(t *testing.T) {
	w := NewWriter(os.Stdout, nil)
	if got := w.Fd(); got != os.Stdout.Fd() {
		t.Fatalf("Fd() = %d, want underlying %d", got, os.Stdout.Fd())
	}
}
