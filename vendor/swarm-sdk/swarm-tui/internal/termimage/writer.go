package termimage

import (
	"bytes"
	"io"
	"sync"
)

var (
	synchronizedOutputStart = []byte("\x1b[?2026h")
	synchronizedOutputEnd   = []byte("\x1b[?2026l")
)

// Writer inserts Manager reconciliation commands immediately after every
// synchronized-output start marker. Kitty virtual placements therefore exist
// before the frame's Unicode placeholder cells are interpreted. End-marker
// reconciliation remains as a fallback for renderers without a start marker.
type Writer struct {
	Writer  io.Writer
	Manager *Manager
	mu      sync.Mutex
	pending []byte
}

// NewWriter wraps w with synchronized-output reconciliation.
func NewWriter(w io.Writer, manager *Manager) *Writer { return &Writer{Writer: w, Manager: manager} }

// Writer must satisfy the same method set as charmbracelet/x/term.File
// (io.ReadWriteCloser + Fd). Bubble Tea v2 type-asserts its output writer to
// term.File in initInput; if the assertion fails it leaves ttyOutput nil, never
// calls term.GetSize, and the renderer is stuck at the WithWindowSize fallback
// (an 80x24 island on an otherwise black alt-screen). This compile-time guard
// prevents regressing that interface.
var _ interface {
	io.ReadWriteCloser
	Fd() uintptr
} = (*Writer)(nil)

// Fd preserves terminal identity when the wrapped writer exposes a file
// descriptor. Bubble Tea uses this interface to retain TTY sizing and setup.
func (w *Writer) Fd() uintptr {
	if f, ok := w.Writer.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return ^uintptr(0)
}

// Read satisfies term.File. Bubble Tea reads input from its input reader, never
// from the output writer, so this is a harmless non-blocking stub. Delegating
// to the underlying descriptor would risk blocking on a non-readable stdout.
func (w *Writer) Read([]byte) (int, error) { return 0, io.EOF }

// Close satisfies term.File. It is deliberately a no-op: the Writer does not own
// the wrapped descriptor (typically os.Stdout) and must never close it, or the
// process would lose stdout on program teardown.
func (w *Writer) Close() error { return nil }

func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.Writer == nil {
		return 0, io.ErrClosedPipe
	}
	pendingLen := len(w.pending)
	data := make([]byte, 0, len(w.pending)+len(p))
	data = append(data, w.pending...)
	data = append(data, p...)
	w.pending = w.pending[:0]
	consumed := 0
	for {
		i, marker := nextSyncMarker(data[consumed:])
		if i < 0 {
			break
		}
		i += consumed
		if bytes.Equal(marker, synchronizedOutputStart) {
			if err := writeAll(w.Writer, data[consumed:i+len(marker)]); err != nil {
				return 0, err
			}
			if w.Manager != nil {
				if err := w.Manager.Reconcile(w.Writer); err != nil {
					return committedInputBytes(i+len(marker), pendingLen, len(p)), err
				}
			}
		} else {
			if err := writeAll(w.Writer, data[consumed:i]); err != nil {
				return 0, err
			}
			if w.Manager != nil {
				if err := w.Manager.Reconcile(w.Writer); err != nil {
					return committedInputBytes(i, pendingLen, len(p)), err
				}
			}
			if err := writeAll(w.Writer, marker); err != nil {
				return 0, err
			}
		}
		consumed = i + len(marker)
	}
	remainder := data[consumed:]
	keep := markerPrefixSuffixLen(remainder)
	if err := writeAll(w.Writer, remainder[:len(remainder)-keep]); err != nil {
		return 0, err
	}
	w.pending = append(w.pending, remainder[len(remainder)-keep:]...)
	return len(p), nil
}

func committedInputBytes(committed, pending, input int) int {
	committed -= pending
	if committed < 0 {
		return 0
	}
	return min(committed, input)
}

func markerPrefixSuffixLen(p []byte) int {
	keep := 0
	for _, marker := range [][]byte{synchronizedOutputStart, synchronizedOutputEnd} {
		maxLen := min(len(p), len(marker)-1)
		for n := maxLen; n > keep; n-- {
			if bytes.Equal(p[len(p)-n:], marker[:n]) {
				keep = n
				break
			}
		}
	}
	return keep
}

func nextSyncMarker(p []byte) (int, []byte) {
	start := bytes.Index(p, synchronizedOutputStart)
	end := bytes.Index(p, synchronizedOutputEnd)
	switch {
	case start < 0:
		return end, synchronizedOutputEnd
	case end < 0 || start < end:
		return start, synchronizedOutputStart
	default:
		return end, synchronizedOutputEnd
	}
}

// Flush writes a buffered partial synchronized-output marker verbatim. Call it
// after the renderer has stopped so a trailing ESC prefix cannot be stranded.
func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) == 0 {
		return nil
	}
	if err := writeAll(w.Writer, w.pending); err != nil {
		return err
	}
	w.pending = w.pending[:0]
	return nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
