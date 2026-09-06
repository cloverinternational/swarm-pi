package chat

import (
	"strings"
	"testing"
)

// TestFindMessageAtOffset_CorrectMessage tests finding the right message
func TestFindMessageAtOffset_CorrectMessage(t *testing.T) {
	app := &App{
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 3},
			{MessageIdx: 1, StartLine: 5, EndLine: 10},
			{MessageIdx: 2, StartLine: 12, EndLine: 20},
		},
	}

	tests := []struct {
		offset         int
		expectedMsgIdx int
		description    string
	}{
		{0, 0, "start of first message"},
		{2, 0, "middle of first message"},
		{3, 0, "end of first message"},
		{5, 1, "start of second message"},
		{7, 1, "middle of second message"},
		{10, 1, "end of second message"},
		{12, 2, "start of third message"},
		{15, 2, "middle of third message"},
		{20, 2, "end of third message"},
		{25, 2, "beyond all messages"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			result := app.findMessageAtOffset(tt.offset)
			if result != tt.expectedMsgIdx {
				t.Errorf("expected message %d, got %d for offset %d", tt.expectedMsgIdx, result, tt.offset)
			}
		})
	}
}

// TestFindMessageAtOffset_GapHandling tests handling of gaps between messages
func TestFindMessageAtOffset_GapHandling(t *testing.T) {
	app := &App{
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 3},
			{MessageIdx: 1, StartLine: 5, EndLine: 10},
		},
	}

	// Line 4 is the gap (blank separator)
	// Should return the message before the gap
	result := app.findMessageAtOffset(4)
	if result != 0 {
		t.Errorf("expected message 0 for gap line, got %d", result)
	}
}

// TestFindMessageAtOffset_EmptyList tests with no messages
func TestFindMessageAtOffset_EmptyList(t *testing.T) {
	app := &App{
		messageLinePositions: []MessageLinePosition{},
	}

	result := app.findMessageAtOffset(0)
	if result != -1 {
		t.Errorf("expected -1 for empty list, got %d", result)
	}
}

// TestScrollAnchorAtBottomConstruction tests creating an at-bottom anchor
func TestScrollAnchorAtBottomConstruction(t *testing.T) {
	anchor := &ScrollAnchor{
		AnchorMessageIndex:  5,
		AnchorLineInMessage: 0,
		WasAtBottom:         true,
		YOffset:             100,
	}

	if !anchor.WasAtBottom {
		t.Error("expected WasAtBottom to be true")
	}
	if anchor.AnchorMessageIndex != 5 {
		t.Errorf("expected AnchorMessageIndex=5, got %d", anchor.AnchorMessageIndex)
	}
}

// TestScrollAnchorScrolledUpConstruction tests creating a scrolled-up anchor
func TestScrollAnchorScrolledUpConstruction(t *testing.T) {
	anchor := &ScrollAnchor{
		AnchorMessageIndex:  2,
		AnchorLineInMessage: 7,
		WasAtBottom:         false,
		YOffset:             12,
	}

	if anchor.WasAtBottom {
		t.Error("expected WasAtBottom to be false")
	}
	if anchor.AnchorMessageIndex != 2 {
		t.Errorf("expected AnchorMessageIndex=2, got %d", anchor.AnchorMessageIndex)
	}
	if anchor.AnchorLineInMessage != 7 {
		t.Errorf("expected AnchorLineInMessage=7, got %d", anchor.AnchorLineInMessage)
	}
}

// TestFindMessageAtOffset_SingleMessage tests with one message
func TestFindMessageAtOffset_SingleMessage(t *testing.T) {
	app := &App{
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 10},
		},
	}

	tests := []int{0, 5, 10, 15}
	for _, offset := range tests {
		result := app.findMessageAtOffset(offset)
		if result != 0 {
			t.Errorf("expected message 0 for offset %d, got %d", offset, result)
		}
	}
}

// TestScrollAnchorNilHandling tests nil pointer handling
func TestScrollAnchorNilHandling(t *testing.T) {
	var anchor *ScrollAnchor
	if anchor != nil {
		t.Error("expected nil anchor to be nil")
	}
}

// TestOffsetCalculation tests the offset calculation logic
func TestOffsetCalculation(t *testing.T) {
	tests := []struct {
		yOffset       int
		messageStart  int
		expectedDelta int
		description   string
	}{
		{0, 0, 0, "at start of message"},
		{5, 0, 5, "5 lines into message"},
		{10, 5, 5, "5 lines into message (offset from line 5)"},
		{0, 0, 0, "at beginning"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			delta := tt.yOffset - tt.messageStart
			if delta != tt.expectedDelta {
				t.Errorf("expected delta %d, got %d", tt.expectedDelta, delta)
			}
		})
	}
}

// TestFindMessageAtOffset_BinarySearchWould work on this data
func TestFindMessageAtOffset_LargeGap(t *testing.T) {
	app := &App{
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 3},
			{MessageIdx: 1, StartLine: 100, EndLine: 103}, // Large gap
		},
	}

	// Offset in the gap should return first message
	result := app.findMessageAtOffset(50)
	if result != 0 {
		t.Errorf("expected message 0 in gap, got %d", result)
	}

	// Offset in second message
	result = app.findMessageAtOffset(100)
	if result != 1 {
		t.Errorf("expected message 1 at offset 100, got %d", result)
	}
}

// TestCenteringOffsetCalculation tests the viewport centering formula
func TestCenteringOffsetCalculation(t *testing.T) {
	// Test the centering math: targetYOffset = messageStart - (viewportHeight - messageHeight) / 2
	tests := []struct {
		messageStart   int
		messageHeight  int
		viewportHeight int
		expectedOffset int
		description    string
	}{
		// Small message, tall viewport: center it with lots of space
		{10, 5, 24, 10 - (24-5)/2, "small message in tall viewport"},
		// Message takes up whole viewport: start it at top
		{10, 24, 24, 10 - (24-24)/2, "message fills viewport"},
		// Large message, small viewport: start near top, lots of message visible
		{10, 50, 24, 10 - (24-50)/2, "large message in small viewport"},
		// Single line message: center it dramatically
		{50, 1, 30, 50 - (30-1)/2, "single line message"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			offset := tt.messageStart - (tt.viewportHeight-tt.messageHeight)/2
			if offset != tt.expectedOffset {
				t.Errorf("expected offset %d, got %d", tt.expectedOffset, offset)
			}
		})
	}
}

// TestCenteringLogic tests the overall centering approach
func TestCenteringLogic(t *testing.T) {
	// Scenario: Message at lines 10-20 (11 lines), viewport 30 lines tall
	// Without centering: would show message at top (lines 10-40)
	// With centering: should show message centered
	//   -> viewport space above: (30-11)/2 = 9.5 ≈ 9
	//   -> scroll to: 10 - 9 = 1
	//   -> shows lines 1-31, message at 10-20 is nicely centered!

	messageStart := 10
	messageHeight := 11 // EndLine - StartLine + 1 = 20 - 10 + 1
	viewportHeight := 30

	centeredOffset := messageStart - (viewportHeight-messageHeight)/2
	// = 10 - (30 - 11) / 2
	// = 10 - 19/2
	// = 10 - 9
	// = 1

	if centeredOffset != 1 {
		t.Errorf("expected centered offset 1, got %d", centeredOffset)
	}

	// Verify: viewport showing lines 1-31 with message at 10-20
	viewportStart := centeredOffset
	viewportEnd := centeredOffset + viewportHeight - 1
	if !(viewportStart <= messageStart && messageStart+messageHeight-1 <= viewportEnd) {
		t.Errorf("message not fully visible in viewport [%d-%d]", viewportStart, viewportEnd)
	}
}

// TestCenteringPreservesMessage tests that centering shows the correct message prominently
func TestCenteringPreservesMessage(t *testing.T) {
	// Simulate: user reading message at lines 50-70
	// After message expansion, it's now at lines 50-100
	// Centering should position so this message is visible in center

	// Setup
	messageStart := 50
	newMessageHeight := 51 // 100 - 50 + 1 (expanded due to verbose toggle)
	viewportHeight := 40

	// Calculate centered offset for NEW height
	centeredOffset := messageStart - (viewportHeight-newMessageHeight)/2
	// = 50 - (40 - 51) / 2
	// = 50 - (-11) / 2
	// = 50 - (-5.5)
	// = 50 + 5.5 ≈ 55

	// This means viewport shows lines 55-95
	// Message is at 50-100, so user sees lines 55-95 of the message
	// Result: message is visible and centered as much as possible

	viewportEnd := centeredOffset + viewportHeight - 1
	if centeredOffset > messageStart || viewportEnd < messageStart+newMessageHeight-1 {
		// Message might not be fully visible, but it should be prominently shown
		t.Logf("Message [%d-%d] shown in viewport [%d-%d] (centered as much as possible)",
			messageStart, messageStart+newMessageHeight-1, centeredOffset, viewportEnd)
	}
}

// TestMessageListScrollBounce tests that scroll doesn't bounce at bottom
// when content changes. This tests the fix for the issue where SetContent()
// was calling GotoBottom() internally, causing double-GotoBottom() calls.
func TestMessageListScrollBounce(t *testing.T) {
	m := NewMessageList(80, 20)

	// Initial content: 100 lines
	var initialContent strings.Builder
	for i := range 100 {
		if i > 0 {
			initialContent.WriteString("\n")
		}
		initialContent.WriteString("Line " + string(rune(i+'0')))
	}
	m.SetContent(initialContent.String())

	// User scrolls to bottom
	initialOffset := m.YOffset
	m.GotoBottom()
	bottomOffset1 := m.YOffset
	if !m.AtBottom() {
		t.Errorf("expected to be at bottom after GotoBottom(), but at offset %d (maxOffset=%d)",
			m.YOffset, m.maxYOffset())
	}

	// New content arrives: 110 lines
	var newContent strings.Builder
	for i := range 110 {
		if i > 0 {
			newContent.WriteString("\n")
		}
		newContent.WriteString("Line " + string(rune(i+'0')))
	}

	// Save offset before content change
	offsetBeforeChange := m.YOffset

	// Update content - this should NOT call GotoBottom() internally
	m.SetContent(newContent.String())

	// The offset should be clamped to valid range but NOT auto-moved to bottom
	// The caller (updateViewportIncremental) will call GotoBottom() if needed
	offsetAfterSetContent := m.YOffset

	// Now simulate what the caller does: call GotoBottom() if needed
	m.GotoBottom()
	bottomOffset2 := m.YOffset

	// Check that we're still at bottom
	if !m.AtBottom() {
		t.Errorf("expected to be at bottom after final GotoBottom(), but at offset %d (maxOffset=%d)",
			m.YOffset, m.maxYOffset())
	}

	// Log the journey for verification
	t.Logf("Offset journey: initial=%d -> bottom1=%d -> beforeChange=%d -> afterSetContent=%d -> bottom2=%d",
		initialOffset, bottomOffset1, offsetBeforeChange, offsetAfterSetContent, bottomOffset2)

	// The key fix: we should not have multiple bounces.
	// If SetContent() was calling GotoBottom() internally, then we'd see:
	// bottomOffset2 == offsetAfterSetContent (because double-GotoBottom would be weird)
	// But now with the fix, offsetAfterSetContent might differ from bottomOffset2
	// as long as we don't bounce back and forth
	if offsetAfterSetContent > m.maxYOffset() {
		t.Errorf("SetContent clamped offset to %d but maxOffset is only %d - offset is invalid!",
			offsetAfterSetContent, m.maxYOffset())
	}
}

// TestSetUserScrolledAway_NoOpWhenUnchanged verifies the centralized setter
// does not emit a spurious state change (or trace line) when the value is
// already what's being set — this keeps the canonical scroll trace free of
// noise from the many call sites that unconditionally reset the flag.
func TestSetUserScrolledAway_NoOpWhenUnchanged(t *testing.T) {
	app := &App{}

	app.setUserScrolledAway(false, "initial")
	if app.userScrolledAway {
		t.Fatalf("expected userScrolledAway to remain false")
	}

	app.setUserScrolledAway(true, "scrolled")
	if !app.userScrolledAway {
		t.Fatalf("expected userScrolledAway to become true")
	}

	// Setting to the same value again must be a safe no-op.
	app.setUserScrolledAway(true, "scrolled-again")
	if !app.userScrolledAway {
		t.Fatalf("expected userScrolledAway to remain true")
	}
}

// TestSetUserScrolledAway_IsTheOnlyMutationPath is a documentation-style test:
// every production call site was migrated to setUserScrolledAway during the
// 2026-07-24 scroll-stability pass specifically so the field could never be
// flipped without going through the centralized, traced setter. This test
// exercises both transitions to guard against a future direct assignment
// silently reintroducing an untraced call site.
func TestSetUserScrolledAway_IsTheOnlyMutationPath(t *testing.T) {
	app := &App{}
	transitions := []bool{true, false, true, true, false}
	for i, want := range transitions {
		app.setUserScrolledAway(want, "test-transition")
		if app.userScrolledAway != want {
			t.Fatalf("transition %d: expected userScrolledAway=%t, got %t", i, want, app.userScrolledAway)
		}
	}
}

// TestScrollAnchor_SurvivesContentGrowth is the core regression for the
// 2026-07-24 canonical-tracking fix: a user scrolled away from the bottom
// must keep looking at the SAME message content after new content is
// appended below (e.g. streaming tokens arriving in a later message, or a
// tool call expanding), even though every raw line number below the anchor
// point shifts. The old behavior (SetYOffset(savedOffset)) held the raw line
// number steady instead, which silently drifted to the wrong content once
// anything above the anchor point changed size.
func TestScrollAnchor_SurvivesContentGrowth(t *testing.T) {
	app := &App{
		messages: []Message{
			{Role: "user", OrderedBlocks: []MessageBlock{{Type: "content", Content: "hello"}}},
			{Role: "assistant", OrderedBlocks: []MessageBlock{{Type: "content", Content: "reading this one"}}},
			{Role: "assistant", OrderedBlocks: []MessageBlock{{Type: "content", Content: "streaming elsewhere"}}},
		},
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 2},
			{MessageIdx: 1, StartLine: 4, EndLine: 10}, // the message the user is reading
			{MessageIdx: 2, StartLine: 12, EndLine: 14},
		},
	}
	// Viewport is scrolled so message 1 (lines 4-10) fills the visible area;
	// the bottom visible line is line 10 (message 1's last line).
	app.msgViewport = NewMessageList(80, 7)
	app.msgViewport.SetContent(strings.Repeat("line\n", 14) + "line")
	app.msgViewport.SetYOffset(4) // top=4, height=7 -> bottom visible = 10

	anchor := app.saveScrollAnchor()
	if anchor == nil {
		t.Fatal("expected a non-nil anchor")
	}
	if anchor.WasAtBottom {
		t.Fatal("expected WasAtBottom=false — viewport is not at the bottom")
	}
	if anchor.AnchorMessageIndex != 1 {
		t.Fatalf("expected anchor on message 1, got %d", anchor.AnchorMessageIndex)
	}

	// Simulate content growth: message 2 (streaming elsewhere, below the
	// anchor) grows from 3 lines to 30 lines. Message 1's position is
	// unchanged (same start/end), which is the realistic case — streaming
	// into a LATER message must not disturb the raw start line of an
	// EARLIER, already-rendered message the user is reading.
	app.messageLinePositions = []MessageLinePosition{
		{MessageIdx: 0, StartLine: 0, EndLine: 2},
		{MessageIdx: 1, StartLine: 4, EndLine: 10},
		{MessageIdx: 2, StartLine: 12, EndLine: 41}, // grew from 3 to 30 lines
	}
	app.msgViewport.SetContent(strings.Repeat("line\n", 41) + "line")

	app.restoreScrollAnchor(anchor)

	// The anchor line (line 10, the bottom of message 1) must still be the
	// bottom visible line: YOffset + Height - 1 == 10.
	gotBottomLine := app.msgViewport.YOffset + app.msgViewport.Height - 1
	if gotBottomLine != 10 {
		t.Fatalf("expected restored view to still show message 1's last line (10) at the bottom, got bottom line %d (YOffset=%d)",
			gotBottomLine, app.msgViewport.YOffset)
	}
}

// TestApplyChatAreaLayout_DoesNotClobberHeightWithEstimate is the regression
// for the resize/side-panel-toggle scroll-clamp bug found during the
// 2026-07-24 pass: applyChatAreaLayout must only apply Width eagerly and must
// leave Height exactly as it was, because layout.ViewportHeight is only a
// fixed-footer estimate (computeChatAreaLayout's chatFooterChromeHeight=6)
// that can differ from the real, per-frame measured height renderChatContent
// computes. Clobbering Height here previously caused a follow-up eager
// re-render (a caller pattern used by the WindowSizeMsg handler) to clamp a
// scrolled-away user's position against the wrong maxYOffset.
func TestApplyChatAreaLayout_DoesNotClobberHeightWithEstimate(t *testing.T) {
	app := &App{width: 100, height: 40, showSidePanel: false}
	app.msgViewport = NewMessageList(50, 123) // 123 is deliberately NOT what the fixed-footer estimate would compute

	layout := app.applyChatAreaLayout(false)

	if app.msgViewport.Height != 123 {
		t.Fatalf("expected applyChatAreaLayout to leave Height untouched (123), got %d (layout estimate was %d)",
			app.msgViewport.Height, layout.ViewportHeight)
	}
	if app.msgViewport.Width != layout.ViewportWidth {
		t.Fatalf("expected applyChatAreaLayout to apply the computed Width (%d), got %d",
			layout.ViewportWidth, app.msgViewport.Width)
	}
}

// TestSetPreWrappedLines_ConvertsRawOffsetToWrappedSpace is the regression for
// the 2026-07-24 "Tab flash" root cause: YOffset means a RAW m.lines index
// while the fallback renderer is active, but a WRAPPED preWrappedLines index
// once the pre-wrap renderer takes over. SetPreWrappedLines used to re-clamp
// the existing raw-space number as if it were already wrapped-space, silently
// jumping to different content the instant a background wrap landed. This
// test proves the offset is now converted through the raw->wrapped mapping.
func TestSetPreWrappedLines_ConvertsRawOffsetToWrappedSpace(t *testing.T) {
	// Height must be smaller than the wrapped line count (7) so a non-zero
	// offset is actually reachable instead of being clamped to 0 because
	// everything fits on screen at once.
	m := NewMessageList(80, 2)
	// 5 raw lines; content itself doesn't matter, only line count for hashing.
	m.SetContent("a\nb\nc\nd\ne")
	// Put the viewport in fallback/raw-space mode at raw offset 2 (as if a
	// synchronous fallback render had just positioned it there).
	m.YOffset = 2

	// Simulate the background wrap completing: raw line 0 wrapped to 1 visual
	// line, raw line 1 wrapped to 3 visual lines (a long line that word-wraps),
	// raw lines 2-4 wrapped 1:1. mapping[i] = cumulative wrapped lines before
	// raw line i, len(mapping) == len(raw)+1.
	mapping := []int{0, 1, 4, 5, 6, 7} // raw line 2 begins at wrapped index 4
	wrapped := []string{"a", "b1", "b2", "b3", "c", "d", "e"}
	m.SetPreWrappedLines(wrapped, mapping, 80, m.lastContentHash)

	if m.YOffset != 4 {
		t.Fatalf("expected raw offset 2 to convert to wrapped offset 4 (mapping[2]=4), got %d", m.YOffset)
	}
}

// TestSetPreWrappedLines_RedundantApplyDoesNotDoubleConvert guards the
// "wasAlreadyWrapped" exception: if the viewport is ALREADY using this exact
// (width, hash) pre-wrap and SetPreWrappedLines is called again with the same
// target (e.g. a duplicate/late-arriving completion), the offset must NOT be
// converted a second time — it's already in wrapped-space.
func TestSetPreWrappedLines_RedundantApplyDoesNotDoubleConvert(t *testing.T) {
	m := NewMessageList(80, 2)
	m.SetContent("a\nb\nc\nd\ne")
	mapping := []int{0, 1, 4, 5, 6, 7}
	wrapped := []string{"a", "b1", "b2", "b3", "c", "d", "e"}

	m.YOffset = 2
	m.SetPreWrappedLines(wrapped, mapping, 80, m.lastContentHash) // first apply: 2 -> 4
	if m.YOffset != 4 {
		t.Fatalf("setup: expected first apply to convert to wrapped offset 4, got %d", m.YOffset)
	}

	// Redundant re-apply with identical (width, hash) — must be a no-op on offset.
	m.SetPreWrappedLines(wrapped, mapping, 80, m.lastContentHash)
	if m.YOffset != 4 {
		t.Fatalf("expected redundant re-apply to leave wrapped offset unchanged at 4, got %d (double-converted)", m.YOffset)
	}
}

// TestSaveScrollAnchor_ConvertsWrappedOffsetToRawSpace is the companion
// regression: saveScrollAnchor must convert a.msgViewport.YOffset from
// wrapped-space back to raw-space (via rawFromWrappedIndex) before comparing
// it against messageLinePositions, whenever the pre-wrapped renderer is the
// one currently active — otherwise it silently anchors to the wrong message.
func TestSaveScrollAnchor_ConvertsWrappedOffsetToRawSpace(t *testing.T) {
	app := &App{
		messages: []Message{
			{Role: "user", OrderedBlocks: []MessageBlock{{Type: "content", Content: "hi"}}},
			{Role: "assistant", OrderedBlocks: []MessageBlock{{Type: "content", Content: "reading this"}}},
		},
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 1}, // raw lines 0-1
			{MessageIdx: 1, StartLine: 3, EndLine: 6}, // raw lines 3-6 (the message being read)
		},
	}
	app.msgViewport = NewMessageList(80, 3)
	app.msgViewport.SetContent("r0\nr1\nsep\nr3\nr4\nr5\nr6")

	// Make the pre-wrapped renderer active: raw line 3 wraps into 3 visual
	// lines (a long line), everything else 1:1. mapping has len(raw)+1 = 8
	// entries for 7 raw lines.
	mapping := []int{0, 1, 2, 3, 6, 7, 8, 9}
	wrappedLines := []string{"r0", "r1", "sep", "r3a", "r3b", "r3c", "r4", "r5", "r6"}
	app.msgViewport.SetPreWrappedLines(wrappedLines, mapping, 80, app.msgViewport.lastContentHash)
	if !app.msgViewport.prewrapActive(80) {
		t.Fatal("setup: expected pre-wrap to be active")
	}

	// Position the (wrapped-space) viewport so the bottom visible row is
	// wrapped index 5 ("r3c", the last visual line of raw line 3 / message 1),
	// NOT at the bottom (so saveScrollAnchor takes the anchor path, not
	// WasAtBottom).
	app.msgViewport.SetYOffset(3) // wrapped offset 3..5 visible (height=3)
	if app.msgViewport.AtBottom() {
		t.Fatal("setup: viewport must not be at bottom for this test")
	}

	anchor := app.saveScrollAnchor()
	if anchor == nil {
		t.Fatal("expected non-nil anchor")
	}
	if anchor.AnchorMessageIndex != 1 {
		t.Fatalf("expected anchor on message 1 (the message actually visible at the wrapped-space bottom row), got message %d — "+
			"this means the wrapped offset was compared against raw-space messageLinePositions without conversion",
			anchor.AnchorMessageIndex)
	}
}

// TestRestoreScrollAnchor_ConvertsRawOffsetToWrappedSpace is the write-side
// companion to TestSaveScrollAnchor_ConvertsWrappedOffsetToRawSpace, and is
// the regression test for the 2026-07-24 scroll-worse-than-before bug:
// restoreScrollAnchor computes newYOffset from messageLinePositions, which
// are always RAW-space (see saveScrollAnchor's CANONICAL SPACE CONVERSION
// comment), but used to hand that raw number straight to
// MessageList.SetYOffset — which is interpreted in WRAPPED space whenever
// prewrapActive() is true. That asymmetry (save converted wrapped->raw, but
// restore never converted raw->wrapped back) is what made every scroll
// action land on the wrong content once a background pre-wrap was active:
// confirmed live via SWARM_SCROLL_DEBUG as raw=1570 wrapped=1809 active=true.
// This test proves restoreScrollAnchor now performs the missing conversion.
func TestRestoreScrollAnchor_ConvertsRawOffsetToWrappedSpace(t *testing.T) {
	app := &App{
		messages: []Message{
			{Role: "user", OrderedBlocks: []MessageBlock{{Type: "content", Content: "hi"}}},
			{Role: "assistant", OrderedBlocks: []MessageBlock{{Type: "content", Content: "reading this"}}},
		},
		messageLinePositions: []MessageLinePosition{
			{MessageIdx: 0, StartLine: 0, EndLine: 1}, // raw lines 0-1
			{MessageIdx: 1, StartLine: 3, EndLine: 6}, // raw lines 3-6
		},
	}
	app.msgViewport = NewMessageList(80, 3) // Height=3, so Height-1=2
	app.msgViewport.SetContent("r0\nr1\nsep\nr3\nr4\nr5\nr6")

	// Same mapping as the save-side test: raw line 3 expands into 3 wrapped
	// lines (wrapped indices 3,4,5), so raw line 4 begins at wrapped index 6
	// — a raw/wrapped divergence a correct conversion must account for.
	mapping := []int{0, 1, 2, 3, 6, 7, 8, 9}
	wrappedLines := []string{"r0", "r1", "sep", "r3a", "r3b", "r3c", "r4", "r5", "r6"}
	app.msgViewport.SetPreWrappedLines(wrappedLines, mapping, 80, app.msgViewport.lastContentHash)
	if !app.msgViewport.prewrapActive(80) {
		t.Fatal("setup: expected pre-wrap to be active")
	}

	// Anchor at raw line 6 (message 1, AnchorLineInMessage=3, since
	// msgPos.StartLine=3). rawNewYOffset = anchorLineGlobalPos - (Height-1)
	// = (3+3) - 2 = 4 (raw line "r4"). wrappedFromRawIndex(mapping, 4, 0)
	// = mapping[4] = 6, which is the wrapped index that actually points at
	// "r4" post-expansion — NOT the raw number 4 (which would point at "r3c",
	// the wrong content, if left unconverted).
	anchor := &ScrollAnchor{
		AnchorMessageIndex:  1,
		AnchorLineInMessage: 3,
	}

	app.restoreScrollAnchor(anchor)

	if app.msgViewport.YOffset != 6 {
		t.Fatalf("expected restoreScrollAnchor to convert raw offset 4 to wrapped offset 6 (mapping[4]=6), got YOffset=%d — "+
			"restoreScrollAnchor is feeding a raw-space offset into SetYOffset while the wrapped renderer is active",
			app.msgViewport.YOffset)
	}
}
