package chat

import (
	"testing"
)

// TestMessageRenderContextIndexMapping verifies the index mapping logic
func TestMessageRenderContextIndexMapping(t *testing.T) {
	app := &App{
		focusedMessageIdx: 5,
		messageNavMode:    true,
	}

	tests := []struct {
		name           string
		startIndex     int
		loopIndex      int
		expectedActual int
	}{
		{
			name:           "start_at_0_loop_0",
			startIndex:     0,
			loopIndex:      0,
			expectedActual: 0,
		},
		{
			name:           "start_at_0_loop_3",
			startIndex:     0,
			loopIndex:      3,
			expectedActual: 3,
		},
		{
			name:           "start_at_5_loop_0",
			startIndex:     5,
			loopIndex:      0,
			expectedActual: 5,
		},
		{
			name:           "start_at_3_loop_2",
			startIndex:     3,
			loopIndex:      2,
			expectedActual: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := MessageRenderContext{
				StartIndex:          tt.startIndex,
				FocusedMessageIndex: app.focusedMessageIdx,
				MessageNavMode:      app.messageNavMode,
				Width:               80,
			}

			actual := ctx.ActualMessageIndex(tt.loopIndex)
			if actual != tt.expectedActual {
				t.Errorf("ActualMessageIndex(%d) = %d, want %d", tt.loopIndex, actual, tt.expectedActual)
			}
		})
	}
}

// TestMessageRenderContextFocusLogic verifies focus detection
func TestMessageRenderContextFocusLogic(t *testing.T) {
	tests := []struct {
		name                string
		startIndex          int
		focusedMessageIndex int
		messageNavMode      bool
		loopIndex           int
		expectFocused       bool
	}{
		{
			name:                "focused_nav_mode_on",
			startIndex:          0,
			focusedMessageIndex: 2,
			messageNavMode:      true,
			loopIndex:           2,
			expectFocused:       true,
		},
		{
			name:                "not_focused_nav_mode_on",
			startIndex:          0,
			focusedMessageIndex: 2,
			messageNavMode:      true,
			loopIndex:           1,
			expectFocused:       false,
		},
		{
			name:                "nav_mode_off",
			startIndex:          0,
			focusedMessageIndex: 2,
			messageNavMode:      false,
			loopIndex:           2,
			expectFocused:       false,
		},
		{
			name:                "single_message_focused",
			startIndex:          5,
			focusedMessageIndex: 5,
			messageNavMode:      true,
			loopIndex:           0, // First (only) message in slice
			expectFocused:       true,
		},
		{
			name:                "single_message_not_focused",
			startIndex:          5,
			focusedMessageIndex: 3,
			messageNavMode:      true,
			loopIndex:           0,
			expectFocused:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := MessageRenderContext{
				StartIndex:          tt.startIndex,
				FocusedMessageIndex: tt.focusedMessageIndex,
				MessageNavMode:      tt.messageNavMode,
				Width:               80,
			}

			focused := ctx.IsFocused(tt.loopIndex)
			if focused != tt.expectFocused {
				t.Errorf("IsFocused(%d) = %v, want %v (startIndex=%d, focusedIdx=%d, navMode=%v)",
					tt.loopIndex, focused, tt.expectFocused,
					tt.startIndex, tt.focusedMessageIndex, tt.messageNavMode)
			}
		})
	}
}

// TestPreviewContextSettings verifies preview context has correct settings
func TestPreviewContextSettings(t *testing.T) {
	app := &App{
		streamingMessage:  true, // App is streaming
		focusedMessageIdx: 3,
		messageNavMode:    true,
	}

	ctx := app.NewPreviewMessageContext(80)

	if ctx.IsActiveMessage {
		t.Error("Preview context should have IsActiveMessage=false")
	}
	if !ctx.IsPreview {
		t.Error("Preview context should have IsPreview=true")
	}
	if ctx.FocusedMessageIndex != -1 {
		t.Errorf("Preview context should have FocusedMessageIndex=-1, got %d", ctx.FocusedMessageIndex)
	}
	if ctx.MessageNavMode {
		t.Error("Preview context should have MessageNavMode=false")
	}
}

// TestSingleMessageContextSettings verifies single message context
func TestSingleMessageContextSettings(t *testing.T) {
	app := &App{
		focusedMessageIdx: 3,
		messageNavMode:    true,
	}

	ctx := app.NewSingleMessageContext(5, 80, true)

	if !ctx.IsActiveMessage {
		t.Error("Active single message context should have IsActiveMessage=true")
	}
	if ctx.StartIndex != 5 {
		t.Errorf("Single message context should have StartIndex=5, got %d", ctx.StartIndex)
	}
	if ctx.FocusedMessageIndex != 3 {
		t.Errorf("Single message context should preserve FocusedMessageIndex=3, got %d", ctx.FocusedMessageIndex)
	}

	// Non-active message
	ctx2 := app.NewSingleMessageContext(2, 80, false)
	if ctx2.IsActiveMessage {
		t.Error("Non-active single message context should have IsActiveMessage=false")
	}
}

// TestDefaultContextSettings verifies default context has correct settings
func TestDefaultContextSettings(t *testing.T) {
	app := &App{
		streamingMessage:  true,
		focusedMessageIdx: 7,
		messageNavMode:    true,
	}

	ctx := app.NewMessageRenderContext(100)

	if ctx.IsActiveMessage {
		t.Error("Default context should have IsActiveMessage=false (unified renderer sets it per-message)")
	}
	if ctx.StartIndex != 0 {
		t.Errorf("Default context should have StartIndex=0, got %d", ctx.StartIndex)
	}
	if ctx.FocusedMessageIndex != 7 {
		t.Errorf("Default context should have FocusedMessageIndex=7, got %d", ctx.FocusedMessageIndex)
	}
	if !ctx.MessageNavMode {
		t.Error("Default context should have MessageNavMode=true")
	}
	if ctx.Width != 100 {
		t.Errorf("Default context should have Width=100, got %d", ctx.Width)
	}
}

// TestCollapseManagerKeyConsistency verifies that the collapse manager
// gets the same key regardless of rendering path
func TestCollapseManagerKeyConsistency(t *testing.T) {
	// Test that makeKey produces consistent output
	key1 := makeKey(3, "call_abc")
	key2 := makeKey(3, "call_abc")

	if key1 != key2 {
		t.Errorf("makeKey should be deterministic: %s != %s", key1, key2)
	}

	// Test that different indices produce different keys
	key3 := makeKey(0, "call_abc")
	key4 := makeKey(3, "call_abc")

	if key3 == key4 {
		t.Error("Different message indices should produce different keys")
	}

	// The critical test: when rendering uses StartIndex=3 and loopIndex=0,
	// the actualIdx should be 3, producing the same key as full rendering
	// where loopIndex=3 and StartIndex=0

	fullCtx := MessageRenderContext{StartIndex: 0}
	fullActualIdx := fullCtx.ActualMessageIndex(3) // Loop index 3
	fullKey := makeKey(fullActualIdx, "call_xyz")

	singleCtx := MessageRenderContext{StartIndex: 3}
	singleActualIdx := singleCtx.ActualMessageIndex(0) // Loop index 0
	singleKey := makeKey(singleActualIdx, "call_xyz")

	if fullKey != singleKey {
		t.Errorf("Full and single-message render should produce same key: %s != %s", fullKey, singleKey)
	}
}
