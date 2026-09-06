package chat

import "testing"

// newSelTestInput returns a focused SimpleInput pre-loaded with value and a known
// width so word-wrap maths are deterministic.
func newSelTestInput(value string) *SimpleInput {
	in := NewSimpleInput()
	in.SetValue(value)
	in.SetWidth(120) // wide enough that short test strings never wrap
	in.SetHeight(3)
	in.SetInputOrigin(2, 0)
	return in
}

func TestInputSelectionRangeAndText(t *testing.T) {
	in := newSelTestInput("hello world")
	in.StartSelection(0)
	in.UpdateSelectionEnd(5)
	in.EndSelection()

	a, b := in.SelectionRange()
	if a != 0 || b != 5 {
		t.Fatalf("range = (%d,%d), want (0,5)", a, b)
	}
	if got := in.SelectedText(); got != "hello" {
		t.Fatalf("SelectedText = %q, want %q", got, "hello")
	}
	if !in.HasSelection() {
		t.Fatal("HasSelection = false, want true")
	}
}

func TestInputSelectionReversedDrag(t *testing.T) {
	// Dragging right-to-left must still yield an ordered range.
	in := newSelTestInput("hello world")
	in.StartSelection(11)
	in.UpdateSelectionEnd(6)
	in.EndSelection()

	if got := in.SelectedText(); got != "world" {
		t.Fatalf("SelectedText = %q, want %q", got, "world")
	}
}

func TestInputSelectWordAt(t *testing.T) {
	in := newSelTestInput("hello world foo")
	in.SelectWordAt(7) // inside "world"
	if got := in.SelectedText(); got != "world" {
		t.Fatalf("SelectWordAt -> %q, want %q", got, "world")
	}
}

func TestInputSelectLineAt(t *testing.T) {
	in := newSelTestInput("line one\nline two\nline three")
	// Offset inside the second line.
	pos := len("line one\n") + 2
	in.SelectLineAt(pos)
	if got := in.SelectedText(); got != "line two" {
		t.Fatalf("SelectLineAt -> %q, want %q", got, "line two")
	}
}

func TestInputSelectAll(t *testing.T) {
	in := newSelTestInput("abc def")
	in.SelectAll()
	if got := in.SelectedText(); got != "abc def" {
		t.Fatalf("SelectAll -> %q, want full value", got)
	}
}

func TestInputDeleteSelection(t *testing.T) {
	in := newSelTestInput("hello world")
	in.StartSelection(0)
	in.UpdateSelectionEnd(6) // "hello "
	in.EndSelection()
	if !in.DeleteSelection() {
		t.Fatal("DeleteSelection returned false")
	}
	if in.Value() != "world" {
		t.Fatalf("after delete value = %q, want %q", in.Value(), "world")
	}
	if in.HasSelection() {
		t.Fatal("selection should be cleared after delete")
	}
	if in.GetCursor() != 0 {
		t.Fatalf("cursor = %d, want 0", in.GetCursor())
	}
}

func TestInputClearSelectionOnEmptyClick(t *testing.T) {
	// A click with no drag must not register as a selection.
	in := newSelTestInput("hello")
	in.StartSelection(3)
	in.EndSelection() // no UpdateSelectionEnd -> anchor == focus
	if in.HasSelection() {
		t.Fatal("zero-width selection should not be active")
	}
}

func TestInputScreenToIndexSingleRow(t *testing.T) {
	in := newSelTestInput("hello world")
	// originX=2, originY=0. Screen col 2 maps to byte 0; col 7 -> byte 5.
	idx, ok := in.ScreenToIndex(7, 0)
	if !ok {
		t.Fatal("ScreenToIndex not ok")
	}
	if idx != 5 {
		t.Fatalf("ScreenToIndex(7,0) = %d, want 5", idx)
	}
}

func TestInputScreenToIndexBelowGoesToEnd(t *testing.T) {
	in := newSelTestInput("hi")
	idx, ok := in.ScreenToIndex(2, 50) // far below the text
	if !ok {
		t.Fatal("ScreenToIndex not ok")
	}
	if idx != len("hi") {
		t.Fatalf("click below -> %d, want %d", idx, len("hi"))
	}
}

func TestInputScreenToIndexMultiByte(t *testing.T) {
	// "héllo": é is 2 bytes (U+00E9). Clicking after é must land on byte 3.
	in := newSelTestInput("héllo")
	idx, ok := in.ScreenToIndex(2+2, 0) // 2 display cells past origin -> after "hé"
	if !ok {
		t.Fatal("ScreenToIndex not ok")
	}
	if idx != 3 { // 'h'(1) + 'é'(2) = 3
		t.Fatalf("multibyte map = %d, want 3", idx)
	}
}

func TestInputCopySelectionNoPanicWhenEmpty(t *testing.T) {
	in := newSelTestInput("")
	if in.CopySelection() {
		t.Fatal("CopySelection on empty should return false")
	}
}
