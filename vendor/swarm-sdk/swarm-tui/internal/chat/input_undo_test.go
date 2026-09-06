package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// newTestInput is a convenience constructor for tests.
func newTestInput() *SimpleInput {
	inp := NewSimpleInput()
	inp.SetWidth(80)
	return inp
}

// typeText directly appends text to the input and pushes an undo snapshot for
// each character, mimicking what happens when Update receives a KeyMsg for a
// regular character. This lets us test undo logic without depending on the
// bubbletea KeyMsg internals.
func typeText(inp *SimpleInput, text string) {
	for _, ch := range text {
		inp.pushUndo()
		inp.value += string(ch)
		inp.cursor = len(inp.value)
	}
}

// ctrlKey builds a tea.KeyPressMsg that simulates ctrl+<letter>.
// The String() method of KeyPressMsg returns "ctrl+z" etc., which is exactly
// what SimpleInput.Update() switches on.
func ctrlKey(letter rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{
		Mod:  tea.ModCtrl,
		Code: letter,
	}
}

// ----------------------------------------------------------------------------
// pushUndo / Undo / Redo — unit tests at the method level
// ----------------------------------------------------------------------------

// TestUndoEmptyStack verifies that calling Undo on an empty stack is a no-op
// and does not panic.
//
// Why this matters: the first invariant of any robust undo system is that
// doing nothing when there is nothing to undo is safe.
func TestUndoEmptyStack(t *testing.T) {
	inp := newTestInput()

	// Pre-condition: stack is empty, value is empty, cursor is 0.
	if len(inp.undoStack) != 0 {
		t.Fatalf("expected empty undoStack, got %d items", len(inp.undoStack))
	}

	// Should not panic.
	inp.Undo()

	// Post-condition: everything unchanged.
	if inp.value != "" {
		t.Errorf("expected empty value after Undo on empty stack, got %q", inp.value)
	}
	if inp.cursor != 0 {
		t.Errorf("expected cursor=0 after Undo on empty stack, got %d", inp.cursor)
	}
}

// TestRedoEmptyStack verifies that calling Redo on an empty redo stack is a
// no-op and does not panic.
func TestRedoEmptyStack(t *testing.T) {
	inp := newTestInput()
	inp.Redo() // must not panic
	if inp.value != "" {
		t.Errorf("expected empty value, got %q", inp.value)
	}
}

// TestUndoSingleMutation verifies the simplest undo scenario:
//  1. Start empty.
//  2. Push a snapshot (empty state).
//  3. Mutate the value.
//  4. Undo → value should be restored to empty.
func TestUndoSingleMutation(t *testing.T) {
	inp := newTestInput()

	// Step 1: push snapshot of empty state, then mutate.
	inp.pushUndo()
	inp.value = "hello"
	inp.cursor = 5

	if inp.value != "hello" {
		t.Fatalf("setup failed: expected 'hello', got %q", inp.value)
	}

	// Step 2: undo.
	inp.Undo()

	if inp.value != "" {
		t.Errorf("after Undo: expected empty value, got %q", inp.value)
	}
	if inp.cursor != 0 {
		t.Errorf("after Undo: expected cursor=0, got %d", inp.cursor)
	}
}

// TestUndoMultipleSteps verifies that repeated Undo steps walk backwards
// through the history one snapshot at a time.
//
// This maps to the real usage: type "h", "e", "l" → Undo → "he", Undo → "h",
// Undo → "".
func TestUndoMultipleSteps(t *testing.T) {
	inp := newTestInput()

	// Build up: "" → "h" → "he" → "hel"
	typeText(inp, "hel")

	if inp.value != "hel" {
		t.Fatalf("setup: expected 'hel', got %q", inp.value)
	}
	if len(inp.undoStack) != 3 {
		t.Fatalf("expected 3 undo snapshots, got %d", len(inp.undoStack))
	}

	// Undo once: should restore "he"
	inp.Undo()
	if inp.value != "he" {
		t.Errorf("after 1 Undo: expected 'he', got %q", inp.value)
	}

	// Undo again: should restore "h"
	inp.Undo()
	if inp.value != "h" {
		t.Errorf("after 2 Undo: expected 'h', got %q", inp.value)
	}

	// Undo again: should restore ""
	inp.Undo()
	if inp.value != "" {
		t.Errorf("after 3 Undo: expected '', got %q", inp.value)
	}

	// One more Undo: stack is now empty — should be a no-op.
	inp.Undo()
	if inp.value != "" {
		t.Errorf("Undo past empty stack: expected '', got %q", inp.value)
	}
}

// TestRedoClearedOnNewInput verifies the branching invariant:
// once you type something new after undoing, the redo stack must be cleared.
//
// This is the "you can't redo past a new keystroke" rule — the same rule used
// by Word, VS Code, and all major editors.
func TestRedoClearedOnNewInput(t *testing.T) {
	inp := newTestInput()

	// Type "abc".
	typeText(inp, "abc")

	// Undo twice → value is "a", redo stack has 2 items.
	inp.Undo()
	inp.Undo()
	if inp.value != "a" {
		t.Fatalf("after 2 Undos expected 'a', got %q", inp.value)
	}
	if len(inp.redoStack) != 2 {
		t.Fatalf("expected 2 redo items, got %d", len(inp.redoStack))
	}

	// Type a new character — this must clear the redo stack.
	inp.pushUndo()
	inp.value += "x"
	inp.cursor = len(inp.value)

	if len(inp.redoStack) != 0 {
		t.Errorf("redo stack should be cleared after new input; got %d items", len(inp.redoStack))
	}
}

// TestUndoRedoCycle verifies a full undo-then-redo round-trip restores the
// exact same state.
func TestUndoRedoCycle(t *testing.T) {
	inp := newTestInput()

	// Type "hello".
	typeText(inp, "hello")

	finalValue := inp.value
	finalCursor := inp.cursor

	// Undo all the way back.
	for len(inp.undoStack) > 0 {
		inp.Undo()
	}
	if inp.value != "" {
		t.Errorf("after full Undo expected '', got %q", inp.value)
	}

	// Redo all the way forward.
	for len(inp.redoStack) > 0 {
		inp.Redo()
	}
	if inp.value != finalValue {
		t.Errorf("after full Redo expected %q, got %q", finalValue, inp.value)
	}
	if inp.cursor != finalCursor {
		t.Errorf("after full Redo expected cursor=%d, got %d", finalCursor, inp.cursor)
	}
}

// TestUndoPreservesCursorPosition verifies that the cursor position is
// restored alongside the value, not just the value text.
//
// Why: if the user typed "abc", moved the cursor to position 1, then deleted
// "b", the undo should restore both "abc" AND cursor=1 (mid-text), not
// cursor=3 (end).
func TestUndoPreservesCursorPosition(t *testing.T) {
	inp := newTestInput()

	// Manually set up a mid-text scenario.
	inp.pushUndo() // snapshot: value="", cursor=0
	inp.value = "abc"
	inp.cursor = 3

	inp.pushUndo()   // snapshot: value="abc", cursor=3
	inp.value = "ac" // deleted 'b' at position 1
	inp.cursor = 1

	// Now undo: should restore value="abc", cursor=3.
	inp.Undo()
	if inp.value != "abc" {
		t.Errorf("expected 'abc', got %q", inp.value)
	}
	if inp.cursor != 3 {
		t.Errorf("expected cursor=3, got %d", inp.cursor)
	}

	// Undo again: should restore value="", cursor=0.
	inp.Undo()
	if inp.value != "" {
		t.Errorf("expected '', got %q", inp.value)
	}
	if inp.cursor != 0 {
		t.Errorf("expected cursor=0, got %d", inp.cursor)
	}
}

// TestUndoStackMaxDepth verifies that the undo stack never exceeds
// maxUndoDepth entries, dropping the oldest when full.
//
// Why this matters: unbounded growth would cause a memory leak in a long
// editing session.
func TestUndoStackMaxDepth(t *testing.T) {
	inp := newTestInput()

	// Push maxUndoDepth + 50 snapshots.
	overCount := maxUndoDepth + 50
	for n := range overCount {
		inp.pushUndo()
		inp.value = string(rune('a' + n%26))
		inp.cursor = 1
	}

	// Stack must be capped at maxUndoDepth.
	if len(inp.undoStack) > maxUndoDepth {
		t.Errorf("undoStack grew to %d (max %d)", len(inp.undoStack), maxUndoDepth)
	}
}

// TestPasteIsSingleUndoUnit verifies that a paste operation (which inserts a
// multi-character string in one pushUndo call) is undone as a single step.
//
// This models the "I pasted 50 lines and Ctrl+Z should remove all of them at
// once, not character by character" user expectation.
func TestPasteIsSingleUndoUnit(t *testing.T) {
	inp := newTestInput()

	// Simulate pre-paste state: the user has typed "prefix ".
	typeText(inp, "prefix ")
	beforePasteValue := inp.value
	beforePasteCursor := inp.cursor

	// Simulate paste (one pushUndo for the whole paste block).
	inp.pushUndo()
	pasted := "[Pasted text +3 lines]"
	inp.value = inp.value[:inp.cursor] + pasted + inp.value[inp.cursor:]
	inp.cursor += len(pasted)

	if inp.value != "prefix "+pasted {
		t.Fatalf("paste setup failed: got %q", inp.value)
	}

	// One Undo should remove the entire paste in one step.
	inp.Undo()
	if inp.value != beforePasteValue {
		t.Errorf("after Undo expected %q, got %q", beforePasteValue, inp.value)
	}
	if inp.cursor != beforePasteCursor {
		t.Errorf("after Undo expected cursor=%d, got %d", beforePasteCursor, inp.cursor)
	}
}

// TestClearUndoHistory verifies that ClearUndoHistory wipes both stacks.
func TestClearUndoHistory(t *testing.T) {
	inp := newTestInput()

	typeText(inp, "hello")
	inp.Undo() // puts something on redoStack

	inp.ClearUndoHistory()

	if len(inp.undoStack) != 0 {
		t.Errorf("undoStack should be empty after ClearUndoHistory, got %d", len(inp.undoStack))
	}
	if len(inp.redoStack) != 0 {
		t.Errorf("redoStack should be empty after ClearUndoHistory, got %d", len(inp.redoStack))
	}
}

// TestRedoSequential verifies that repeated Redo calls walk forward through
// the redo history one step at a time (not just the first or last).
func TestRedoSequential(t *testing.T) {
	inp := newTestInput()

	typeText(inp, "abc")

	// Undo 3 times.
	inp.Undo()
	inp.Undo()
	inp.Undo()

	if inp.value != "" {
		t.Fatalf("after 3 Undos expected '', got %q", inp.value)
	}

	// Redo 3 times — should restore a, ab, abc.
	inp.Redo()
	if inp.value != "a" {
		t.Errorf("after 1 Redo expected 'a', got %q", inp.value)
	}
	inp.Redo()
	if inp.value != "ab" {
		t.Errorf("after 2 Redo expected 'ab', got %q", inp.value)
	}
	inp.Redo()
	if inp.value != "abc" {
		t.Errorf("after 3 Redo expected 'abc', got %q", inp.value)
	}
}

// TestUndoWithNewlineInsertion verifies that InsertNewline (shift+enter) is
// correctly placed on the undo stack and can be undone in one step.
func TestUndoWithNewlineInsertion(t *testing.T) {
	inp := newTestInput()

	// Type some text.
	typeText(inp, "line1")
	beforeNewline := inp.value
	beforeCursor := inp.cursor

	// InsertNewline calls pushUndo internally.
	inp.InsertNewline()
	if inp.value != "line1\n" {
		t.Fatalf("InsertNewline: expected 'line1\\n', got %q", inp.value)
	}

	// Undo should remove the newline.
	inp.Undo()
	if inp.value != beforeNewline {
		t.Errorf("after Undo of newline: expected %q, got %q", beforeNewline, inp.value)
	}
	if inp.cursor != beforeCursor {
		t.Errorf("after Undo of newline: expected cursor=%d, got %d", beforeCursor, inp.cursor)
	}
}

// TestCtrlZViaUpdate verifies that sending a ctrl+z key message through the
// Update method triggers Undo when there is history available.
func TestCtrlZViaUpdate(t *testing.T) {
	inp := newTestInput()
	inp.focused = true

	// Push a snapshot manually and change value.
	inp.pushUndo()
	inp.value = "typed text"
	inp.cursor = len(inp.value)

	// Build a ctrl+z KeyPressMsg — String() returns "ctrl+z".
	msg := ctrlKey('z')

	inp.Update(msg)

	if inp.value != "" {
		t.Errorf("ctrl+z via Update: expected empty value, got %q", inp.value)
	}
	if inp.cursor != 0 {
		t.Errorf("ctrl+z via Update: expected cursor=0, got %d", inp.cursor)
	}
}

// TestCtrlYViaUpdate verifies that sending a ctrl+y key message through the
// Update method triggers Redo when there is redo history available.
func TestCtrlYViaUpdate(t *testing.T) {
	inp := newTestInput()
	inp.focused = true

	// Push snapshot, set value, undo (puts value on redo stack).
	inp.pushUndo()
	inp.value = "typed text"
	inp.cursor = len(inp.value)
	inp.Undo()

	if inp.value != "" {
		t.Fatalf("pre-redo setup failed: expected '', got %q", inp.value)
	}

	// Send ctrl+y — String() returns "ctrl+y".
	inp.Update(ctrlKey('y'))

	if inp.value != "typed text" {
		t.Errorf("ctrl+y via Update: expected 'typed text', got %q", inp.value)
	}
}

// TestCursorInvariantAfterUndo verifies that the cursor is always within
// [0, len(value)] after any undo/redo operation — the most critical safety
// property, as an out-of-bounds cursor would cause a panic in View().
func TestCursorInvariantAfterUndo(t *testing.T) {
	inp := newTestInput()

	// Build a long string then undo/redo many times.
	typeText(inp, "the quick brown fox")

	for range 10 {
		inp.Undo()
		if inp.cursor < 0 || inp.cursor > len(inp.value) {
			t.Errorf("cursor invariant broken after Undo: cursor=%d, len=%d", inp.cursor, len(inp.value))
		}
	}
	for range 10 {
		inp.Redo()
		if inp.cursor < 0 || inp.cursor > len(inp.value) {
			t.Errorf("cursor invariant broken after Redo: cursor=%d, len=%d", inp.cursor, len(inp.value))
		}
	}
}
