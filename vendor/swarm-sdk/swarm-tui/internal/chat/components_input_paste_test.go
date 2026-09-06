package chat

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestMultiplePasteHandling tests the fix for the multiple paste bug
// With the pasteEntries implementation, multiple pastes are tracked correctly
func TestMultiplePasteHandling(t *testing.T) {
	tests := []struct {
		name           string
		actions        []testAction
		expectedValue  string
		expectedSubmit string
	}{
		{
			name: "single paste",
			actions: []testAction{
				{actionType: "paste", content: "Hello World"},
			},
			// Shows indicator, not actual text
			expectedValue:  "[#1 Pasted 1 line]",
			expectedSubmit: "Hello World",
		},
		{
			name: "multiple pastes without send",
			actions: []testAction{
				{actionType: "paste", content: "First"},
				{actionType: "paste", content: "Second"},
				{actionType: "paste", content: "Third"},
			},
			// Multiple indicators shown (one per paste), each uniquely numbered
			expectedValue:  "[#1 Pasted 1 line][#2 Pasted 1 line][#3 Pasted 1 line]",
			expectedSubmit: "FirstSecondThird",
		},
		{
			name: "paste, send, paste again",
			actions: []testAction{
				{actionType: "paste", content: "First message"},
				{actionType: "send"},
				{actionType: "paste", content: "Second message"},
			},
			expectedValue:  "[#2 Pasted 1 line]",
			expectedSubmit: "Second message",
		},
		{
			name: "multiline paste",
			actions: []testAction{
				{actionType: "paste", content: "Line 1\nLine 2\nLine 3"},
			},
			// 2 newlines = 3 lines.
			expectedValue:  "[#1 Pasted 3 lines]",
			expectedSubmit: "Line 1\nLine 2\nLine 3",
		},
		{
			name: "typing then paste",
			actions: []testAction{
				{actionType: "type", content: "Start "},
				{actionType: "paste", content: "pasted"},
			},
			expectedValue:  "Start [#1 Pasted 1 line]",
			expectedSubmit: "Start pasted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new SimpleInput
			input := NewSimpleInput()
			input.Focus()

			// Execute actions
			for _, action := range tt.actions {
				switch action.actionType {
				case "paste":
					msg := tea.PasteMsg{Content: action.content}
					input.Update(msg)
				case "type":
					// Directly set value to simulate typing
					input.value += action.content
					input.cursor = len(input.value)
				case "send":
					// Simulate sending by clearing the input
					input.SetValue("")
				}
			}

			// Check the displayed value
			if input.value != tt.expectedValue {
				t.Errorf("expected displayed value %q, got %q", tt.expectedValue, input.value)
			}

			// Check the submit value (this is where the fix matters - all pastes should be restored)
			submitValue := input.GetSubmitValue()
			if submitValue != tt.expectedSubmit {
				t.Errorf("expected submit value %q, got %q", tt.expectedSubmit, submitValue)
			}
		})
	}
}

type testAction struct {
	actionType string // "paste", "type", or "send"
	content    string
}

// TestPasteStateClearing verifies that paste state is properly cleared
func TestPasteStateClearing(t *testing.T) {
	input := NewSimpleInput()
	input.Focus()

	// Paste something
	input.Update(tea.PasteMsg{Content: "Test paste"})

	// Verify paste state is set
	if len(input.pasteEntries) == 0 {
		t.Error("pasteEntries should not be empty after paste")
	}

	// Clear by setting value (simulating send)
	input.SetValue("")

	// Verify paste state is cleared
	if len(input.pasteEntries) != 0 {
		t.Errorf("pasteEntries should be empty after SetValue, got %d entries", len(input.pasteEntries))
	}
}

// TestCursorPositionAfterMultiplePastes verifies cursor position is correct
// Each paste adds an indicator, cursor moves past each indicator
func TestCursorPositionAfterMultiplePastes(t *testing.T) {
	input := NewSimpleInput()
	input.Focus()

	// First paste - cursor moves past the first paste indicator.
	input.Update(tea.PasteMsg{Content: "AAA"})
	firstCursor := input.cursor
	expectedFirst := len(getPastedTextPrompt("AAA", 1))
	if firstCursor != expectedFirst {
		t.Errorf("after first paste, cursor should be at %d, got %d", expectedFirst, firstCursor)
	}

	// Second paste - cursor moves past the second paste indicator.
	input.Update(tea.PasteMsg{Content: "BBB"})
	secondCursor := input.cursor
	expectedSecond := len(getPastedTextPrompt("AAA", 1)) + len(getPastedTextPrompt("BBB", 2))
	if secondCursor != expectedSecond {
		t.Errorf("after second paste, cursor should be at %d, got %d", expectedSecond, secondCursor)
	}

	// Verify the final value shows both indicators
	expectedValue := getPastedTextPrompt("AAA", 1) + getPastedTextPrompt("BBB", 2)
	if input.value != expectedValue {
		t.Errorf("expected value %q, got %q", expectedValue, input.value)
	}

	// Verify submit restores both pastes
	expectedSubmit := "AAABBB"
	if submit := input.GetSubmitValue(); submit != expectedSubmit {
		t.Errorf("expected submit value %q, got %q", expectedSubmit, submit)
	}
}

// TestPasteEntriesAccumulation verifies that multiple paste entries accumulate correctly
func TestPasteEntriesAccumulation(t *testing.T) {
	input := NewSimpleInput()
	input.Focus()

	// Paste three different texts
	input.Update(tea.PasteMsg{Content: "First"})
	if len(input.pasteEntries) != 1 {
		t.Errorf("expected 1 paste entry, got %d", len(input.pasteEntries))
	}

	input.Update(tea.PasteMsg{Content: "Second"})
	if len(input.pasteEntries) != 2 {
		t.Errorf("expected 2 paste entries, got %d", len(input.pasteEntries))
	}

	input.Update(tea.PasteMsg{Content: "Third"})
	if len(input.pasteEntries) != 3 {
		t.Errorf("expected 3 paste entries, got %d", len(input.pasteEntries))
	}

	// Verify all original texts are preserved
	expected := []string{"First", "Second", "Third"}
	for i, entry := range input.pasteEntries {
		if entry.original != expected[i] {
			t.Errorf("paste entry %d: expected %q, got %q", i, expected[i], entry.original)
		}
	}
}
