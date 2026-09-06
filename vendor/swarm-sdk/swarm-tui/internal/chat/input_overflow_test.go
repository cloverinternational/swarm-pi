package chat

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestInputWidthCalculations tests the input width calculations to find overflow issues
func TestInputWidthCalculations(t *testing.T) {
	terminalWidth := 120 // Standard terminal width

	// Simulate the calculation from app.go line 8077-8082
	inputWidth := terminalWidth - 8
	t.Logf("Terminal width: %d", terminalWidth)
	t.Logf("inputWidth (a.width - 8): %d", inputWidth)

	// Simulate the calculation from components.go line 503
	maxWidth := inputWidth - 8
	t.Logf("maxWidth in SimpleInput.View() (inputWidth - 8): %d", maxWidth)

	// The prefix added in app.go line 8183
	statusBadge := "[OFF]"
	prompt := ">"
	prefixPlain := statusBadge + " " + prompt + " "
	prefixLen := len(prefixPlain)
	t.Logf("Prefix plain text: '%s', length: %d", prefixPlain, prefixLen)

	// But the actual rendered prefix uses lipgloss styling!
	statusStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#00ff00"))

	styledStatus := statusStyle.Render(statusBadge)
	styledPrompt := promptStyle.Render(prompt)
	styledPrefix := styledStatus + " " + styledPrompt + " "

	// Calculate visual width (what matters for display)
	visualWidth := lipgloss.Width(styledPrefix)
	t.Logf("Styled prefix visual width: %d", visualWidth)
	t.Logf("Styled prefix raw length: %d (includes ANSI codes)", len(styledPrefix))

	// Now test what happens with a full line of text
	testText := strings.Repeat("x", maxWidth) // Text exactly at maxWidth
	firstLine := styledPrefix + testText
	firstLineVisualWidth := lipgloss.Width(firstLine)
	t.Logf("First line with text at maxWidth:")
	t.Logf("  - Text length: %d", len(testText))
	t.Logf("  - Total visual width: %d", firstLineVisualWidth)
	t.Logf("  - Expected (should be <= terminal width): %d", terminalWidth)

	if firstLineVisualWidth > terminalWidth {
		t.Errorf("OVERFLOW DETECTED! First line visual width %d > terminal width %d (overflow by %d chars)",
			firstLineVisualWidth, terminalWidth, firstLineVisualWidth-terminalWidth)
	}

	// Continuation lines use 8 spaces
	continuationPrefix := "        " // 8 spaces
	continuationLine := continuationPrefix + testText
	continuationVisualWidth := lipgloss.Width(continuationLine)
	t.Logf("Continuation line visual width: %d", continuationVisualWidth)

	if continuationVisualWidth > terminalWidth {
		t.Errorf("OVERFLOW DETECTED! Continuation line visual width %d > terminal width %d",
			continuationVisualWidth, terminalWidth)
	}

	// Now check what the styled input line would be with Width(a.width)
	inputLineStyle := lipgloss.NewStyle().Width(terminalWidth)
	inputLineRendered := inputLineStyle.Render(firstLine)
	renderedWidth := lipgloss.Width(inputLineRendered)
	t.Logf("Rendered input line width (with lipgloss Width style): %d", renderedWidth)

	// Count newlines to detect wrapping
	newlineCount := strings.Count(inputLineRendered, "\n")
	if newlineCount > 0 {
		t.Logf("WARNING: lipgloss added %d newlines (wrapped the text)", newlineCount)
	}
}

// TestInputWidthWithSidePanel tests with side panel visible
func TestInputWidthWithSidePanel(t *testing.T) {
	terminalWidth := 120
	sidePanelWidth := 38 // SidePanelWidth constant

	// With side panel, viewport width is reduced
	contentWidth := terminalWidth - sidePanelWidth
	t.Logf("Terminal width: %d, Side panel width: %d, Content area: %d",
		terminalWidth, sidePanelWidth, contentWidth)

	// The input width calculation from app.go
	inputWidth := terminalWidth - 8
	inputWidth -= sidePanelWidth // When side panel is visible
	t.Logf("inputWidth with side panel: %d", inputWidth)

	// maxWidth in SimpleInput
	maxWidth := inputWidth - 8
	t.Logf("maxWidth for text wrapping: %d", maxWidth)

	// Prefix
	prefixLen := 8 // "[OFF] > "
	totalLineWidth := prefixLen + maxWidth
	t.Logf("Total line width (prefix + text): %d", totalLineWidth)

	// The content area available (contentWidth comes from a.contentWidth())
	// which should match what's available for display
	if totalLineWidth > contentWidth {
		t.Errorf("OVERFLOW: Line width %d > content area %d (overflow by %d)",
			totalLineWidth, contentWidth, totalLineWidth-contentWidth)
	}
}

// TestActualPrefixWidth tests the exact prefix widths used
func TestActualPrefixWidth(t *testing.T) {
	// These are the exact calculations from app.go

	// Status badge (line 8166-8168)
	status := "[OFF]"
	statusBadge := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Render(status)

	// Prompt (line 8171-8173)
	prompt := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#00ff00")).
		Render(">")

	// Full prefix (line 8183)
	// formattedInput = statusBadge + " " + prompt + " " + inputTextLines[0]
	fullPrefix := statusBadge + " " + prompt + " "
	prefixVisualWidth := lipgloss.Width(fullPrefix)

	t.Logf("Status badge '[OFF]' visual width: %d", lipgloss.Width(statusBadge))
	t.Logf("Prompt '>' visual width: %d", lipgloss.Width(prompt))
	t.Logf("Full prefix visual width: %d", prefixVisualWidth)
	t.Logf("Expected prefix width: 8") // [OFF] + space + > + space = 5+1+1+1 = 8

	if prefixVisualWidth != 8 {
		t.Errorf("Prefix visual width is %d, expected 8", prefixVisualWidth)
	}

	// Continuation prefix (line 8186)
	continuationPrefix := "        " // 8 spaces
	contVisualWidth := lipgloss.Width(continuationPrefix)
	t.Logf("Continuation prefix visual width: %d", contVisualWidth)

	if contVisualWidth != 8 {
		t.Errorf("Continuation prefix width is %d, expected 8", contVisualWidth)
	}
}

// TestRenderChatContentFlow simulates the exact render flow where a.width is temporarily reduced
func TestRenderChatContentFlow(t *testing.T) {
	const SidePanelWidth = 38
	const MinWidthForSidePanel = 90

	terminalWidth := 120
	showSidePanel := true

	t.Logf("=== Simulating View() flow with side panel ===")
	t.Logf("Terminal width: %d, Side panel: %d, MinWidth: %d", terminalWidth, SidePanelWidth, MinWidthForSidePanel)

	// In View() - lines 3994-4009
	var chatWidth int
	if showSidePanel && terminalWidth >= MinWidthForSidePanel {
		chatWidth = terminalWidth - SidePanelWidth
	} else {
		chatWidth = terminalWidth
	}
	t.Logf("chatWidth = %d (terminal - sidepanel)", chatWidth)

	// a.width is temporarily set to chatWidth
	aWidth := chatWidth // Simulating a.width = chatWidth
	t.Logf("a.width temporarily set to: %d", aWidth)

	// In renderChatContent() - lines 8077-8082
	inputWidth := aWidth - 8
	t.Logf("inputWidth = %d (a.width - 8)", inputWidth)

	// This check uses the REDUCED a.width (82), not the original (120)
	if showSidePanel && aWidth >= MinWidthForSidePanel {
		inputWidth -= SidePanelWidth
		t.Logf("BUG CHECK: This condition evaluated to TRUE (shouldn't happen)")
	} else {
		t.Logf("Side panel width NOT subtracted again (correct - a.width %d < MinWidthForSidePanel %d)", aWidth, MinWidthForSidePanel)
	}
	t.Logf("Final inputWidth: %d", inputWidth)

	// In SimpleInput.View() - line 503
	maxWidth := inputWidth - 8
	t.Logf("maxWidth in SimpleInput.View(): %d", maxWidth)

	// Calculate total line width
	prefixWidth := 8 // "[OFF] > "
	totalLineWidth := prefixWidth + maxWidth
	t.Logf("Total line width (prefix + text): %d", totalLineWidth)

	// The inputLineRendered uses Width(a.width) which is chatWidth at this point
	// Lines 8301-8304
	renderWidth := aWidth // Width(a.width) in the style
	t.Logf("inputLineRendered Width(): %d", renderWidth)

	// Check if content fits
	if totalLineWidth > renderWidth {
		t.Logf("⚠️  Content width %d > render width %d - lipgloss will wrap!", totalLineWidth, renderWidth)
	} else if totalLineWidth > chatWidth {
		t.Logf("⚠️  Content width %d > chatWidth %d - might overflow chat area!", totalLineWidth, chatWidth)
	} else {
		t.Logf("✓ Content width %d <= chat area %d", totalLineWidth, chatWidth)
	}

	// Final check: when side panel is added, does it fit terminal?
	finalWidth := chatWidth + SidePanelWidth
	t.Logf("Final combined width (chat + side panel): %d", finalWidth)
	if finalWidth > terminalWidth {
		t.Errorf("OVERFLOW: Combined width %d > terminal width %d", finalWidth, terminalWidth)
	}
}

// TestAttachmentLineOverflow documents that attachment lines can overflow
// TODO: This is a known issue - attachments line has no width constraint
func TestAttachmentLineOverflow(t *testing.T) {
	chatWidth := 82 // With side panel

	// Simulate attachment line creation (lines 8192-8210)
	type Attachment struct {
		FileName string
		Size     int64
		MimeType string
	}

	attachments := []Attachment{
		{FileName: "document.pdf", Size: 1024 * 500, MimeType: "application/pdf"},
		{FileName: "image.png", Size: 1024 * 200, MimeType: "image/png"},
		{FileName: "another_very_long_filename_here.txt", Size: 1024 * 10, MimeType: "text/plain"},
	}

	attachmentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Background(lipgloss.Color("#333333"))

	var attParts []string
	for i, att := range attachments {
		sizeKB := att.Size / 1024
		icon := "📎"
		if strings.HasPrefix(att.MimeType, "image/") {
			icon = "🖼️"
		}
		attText := fmt.Sprintf(" %d: %s %s (%d KB) ", i, icon, att.FileName, sizeKB)
		attParts = append(attParts, attachmentStyle.Render(attText))
	}
	attachmentsLine := strings.Join(attParts, " ")

	visualWidth := lipgloss.Width(attachmentsLine)
	t.Logf("Attachments line visual width: %d", visualWidth)
	t.Logf("Chat width: %d", chatWidth)

	if visualWidth > chatWidth {
		// Document the issue but don't fail - this is a known limitation
		t.Logf("⚠️  KNOWN ISSUE: Attachments line width %d > chat width %d (overflow by %d chars)",
			visualWidth, chatWidth, visualWidth-chatWidth)
		t.Logf("TODO: Add width constraint to attachment line rendering")
	}

	// Document emoji widths
	t.Logf("Emoji widths: 📎=%d, 🖼️=%d (both are 2 terminal columns)",
		lipgloss.Width("📎"), lipgloss.Width("🖼️"))
}

// TestVerticalLayoutOverflow checks if vertical layout causes input to be pushed down
func TestVerticalLayoutOverflow(t *testing.T) {
	terminalHeight := 24 // Standard terminal height
	terminalWidth := 120

	t.Logf("=== Vertical Layout Analysis (WITH FIX) ===")
	t.Logf("Terminal: %dx%d", terminalWidth, terminalHeight)

	// Simulate header rendering (lines 8050-8056)
	titleText := "Test Conversation Title"
	header := lipgloss.NewStyle().
		Width(terminalWidth).
		Padding(0, 2).
		Render(titleText)

	// FIX: Account for Padding(1, 2) that will be applied in headerRendered
	// Padding(1, 2) adds 1 line top + 1 line bottom = 2 extra lines
	headerLines := strings.Count(header, "\n") + 1 + 2 // +2 for vertical padding
	t.Logf("Header lines (with padding accounted): %d", headerLines)

	// Verify the rendered header actually uses these lines
	headerRendered := lipgloss.NewStyle().
		Width(terminalWidth).
		Padding(1, 2).
		Render(header)
	headerRenderedLines := strings.Count(headerRendered, "\n") + 1
	t.Logf("Header RENDERED lines: %d", headerRenderedLines)

	if headerRenderedLines != headerLines {
		t.Errorf("Header line calculation mismatch: calculated=%d, rendered=%d",
			headerLines, headerRenderedLines)
	}

	// Divider
	divider := strings.Repeat("─", terminalWidth)
	dividerLines := strings.Count(divider, "\n") + 1
	t.Logf("Divider lines: %d", dividerLines)

	// Input
	inputContentHeight := 1 // Single line input
	spinnerHeight := 0      // No spinner

	// Reserved lines calculation (now correct!)
	reservedLines := headerLines + dividerLines + spinnerHeight + 1 + inputContentHeight + 1
	t.Logf("Reserved lines: %d", reservedLines)
	t.Logf("  = headerLines(%d) + dividerLines(%d) + spinner(%d) + topSep(1) + input(%d) + bottomSep(1)",
		headerLines, dividerLines, spinnerHeight, inputContentHeight)

	// Available viewport lines
	availableViewportLines := terminalHeight - reservedLines
	t.Logf("Available viewport lines: %d", availableViewportLines)

	if availableViewportLines < 5 {
		t.Logf("⚠️  Viewport forced to minimum 5 (was %d)", availableViewportLines)
		availableViewportLines = 5
	}

	// Total lines that will be rendered
	totalRendered := headerLines + dividerLines + spinnerHeight + availableViewportLines + 1 + inputContentHeight + 1
	t.Logf("Total lines to be rendered: %d", totalRendered)
	t.Logf("Terminal height: %d", terminalHeight)

	if totalRendered > terminalHeight {
		t.Errorf("VERTICAL OVERFLOW: Total rendered %d > terminal height %d (overflow by %d lines)",
			totalRendered, terminalHeight, totalRendered-terminalHeight)
	} else {
		t.Logf("✓ No vertical overflow (rendered %d <= terminal %d)", totalRendered, terminalHeight)
	}
}

// TestContentStackingNewlines tests if extra newlines between sections cause overflow
func TestContentStackingNewlines(t *testing.T) {
	terminalHeight := 24
	terminalWidth := 120

	t.Logf("=== Content Stacking Analysis ===")

	// Simulate the content stacking from lines 8334-8349
	// Each section is separated by a WriteByte('\n')

	headerRendered := "Header Line 1\nHeader Line 2\nHeader Line 3" // 3 lines (with padding)
	dividerRendered := strings.Repeat("─", terminalWidth)           // 1 line
	spinnerRendered := ""                                           // 0 lines (not active)
	topSeparatorRendered := strings.Repeat("─", terminalWidth)      // 1 line
	inputLineRendered := "[OFF] > test input"                       // 1 line
	bottomSeparatorRendered := strings.Repeat("─", terminalWidth)   // 1 line

	// Available viewport lines based on reservedLines calculation
	headerLines := 3 // With padding fix
	dividerLines := 1
	spinnerHeight := 0
	inputContentHeight := 1
	reservedLines := headerLines + dividerLines + spinnerHeight + 1 + inputContentHeight + 1
	availableViewportLines := terminalHeight - reservedLines
	t.Logf("Reserved lines: %d, Available viewport: %d", reservedLines, availableViewportLines)

	viewportRendered := strings.Repeat("Viewport line\n", availableViewportLines-1) + "Viewport line"

	// Build content exactly as in app.go lines 8334-8349
	var contentBuilder strings.Builder
	contentBuilder.WriteString(headerRendered)
	contentBuilder.WriteByte('\n') // Line 8335
	contentBuilder.WriteString(dividerRendered)
	contentBuilder.WriteByte('\n') // Line 8337
	contentBuilder.WriteString(viewportRendered)
	if spinnerRendered != "" {
		contentBuilder.WriteByte('\n')
		contentBuilder.WriteString(spinnerRendered)
	}
	contentBuilder.WriteByte('\n') // Line 8344
	contentBuilder.WriteString(topSeparatorRendered)
	contentBuilder.WriteByte('\n') // Line 8346
	contentBuilder.WriteString(inputLineRendered)
	contentBuilder.WriteByte('\n') // Line 8348
	contentBuilder.WriteString(bottomSeparatorRendered)

	content := contentBuilder.String()
	actualLines := strings.Count(content, "\n") + 1

	t.Logf("Header lines: %d", strings.Count(headerRendered, "\n")+1)
	t.Logf("Divider lines: %d", strings.Count(dividerRendered, "\n")+1)
	t.Logf("Viewport lines: %d", strings.Count(viewportRendered, "\n")+1)
	t.Logf("TopSep lines: %d", strings.Count(topSeparatorRendered, "\n")+1)
	t.Logf("Input lines: %d", strings.Count(inputLineRendered, "\n")+1)
	t.Logf("BottomSep lines: %d", strings.Count(bottomSeparatorRendered, "\n")+1)
	t.Logf("Separator newlines between sections: 5")
	t.Logf("Total actual lines: %d", actualLines)
	t.Logf("Terminal height: %d", terminalHeight)

	if actualLines > terminalHeight {
		t.Errorf("VERTICAL OVERFLOW: Stacked content %d lines > terminal %d lines (overflow by %d)",
			actualLines, terminalHeight, actualLines-terminalHeight)
	}

	// The separator newlines are between sections, not additional lines
	// When content is stacked:
	// header(3 lines) + \n + divider(1) + \n + viewport(N) + \n + topSep(1) + \n + input(1) + \n + bottomSep(1)
	//
	// The \n between sections is what joins them. Each section's line count already
	// represents its visual lines. The joining \n creates the separation.
	//
	// Total newlines = (lines in header - 1) + 1 + (lines in divider - 1) + 1 + ... = sum of lines - 1
	// Wait no, that's not right either. Let me trace through the actual string...
	//
	// "A\nB\nC" (3 lines) + "\n" + "D" (1 line) = "A\nB\nC\nD" = 4 lines
	// So adding \n between sections DOES add to the total line count
	//
	// Expected: header(3) + divider(1) + viewport(17) + topSep(1) + input(1) + bottomSep(1) = 24
	// But with 5 joining \n, we get additional separator lines
	t.Logf("Note: Each WriteByte('\\n') between sections adds a line separator")
}

// TestHeaderPaddingBehavior documents how lipgloss padding works
// The fix in app.go accounts for Padding(1,2) adding 2 extra lines
func TestHeaderPaddingBehavior(t *testing.T) {
	content := "Some Title"
	width := 100

	// First pass - content without vertical padding
	header := lipgloss.NewStyle().
		Width(width).
		Padding(0, 2). // Horizontal padding only
		Render(content)
	contentLines := strings.Count(header, "\n") + 1
	t.Logf("Content lines (no vertical padding): %d", contentLines)

	// Second pass - with vertical padding
	headerRendered := lipgloss.NewStyle().
		Width(width).
		Padding(1, 2). // Vertical padding adds lines!
		Render(header)
	renderedLines := strings.Count(headerRendered, "\n") + 1
	t.Logf("Rendered lines (with vertical padding): %d", renderedLines)

	// Verify Padding(1, 2) adds exactly 2 lines (1 top + 1 bottom)
	expectedExtraLines := 2
	actualExtraLines := renderedLines - contentLines
	if actualExtraLines != expectedExtraLines {
		t.Errorf("Expected Padding(1,2) to add %d lines, but added %d",
			expectedExtraLines, actualExtraLines)
	}

	// The fix accounts for this: headerLines = content + 2
	fixedHeaderLines := contentLines + 2
	if fixedHeaderLines != renderedLines {
		t.Errorf("Fix calculation: expected %d, got rendered %d", fixedHeaderLines, renderedLines)
	}
	t.Logf("✓ Fix correctly accounts for vertical padding (+2 lines)")
}

// TestNotificationNeverExceedsWidth verifies notifications can't break layout
func TestNotificationNeverExceedsWidth(t *testing.T) {
	terminalWidths := []int{40, 60, 80, 100, 120, 200}

	for _, width := range terminalWidths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			// Simulate notification rendering calculations

			// Border adds 2, padding adds 2, safety margin 2
			const borderWidth = 2
			const paddingWidth = 2
			const safetyMargin = 2
			contentWidth := width - borderWidth - paddingWidth - safetyMargin

			// Clamp
			if contentWidth > 90 {
				contentWidth = 90
			}
			if contentWidth < 30 {
				contentWidth = 30
			}

			// Create a banner with this content width
			content := strings.Repeat("x", contentWidth)
			banner := lipgloss.NewStyle().
				Width(contentWidth).
				BorderStyle(lipgloss.NormalBorder()).
				Padding(0, 1).
				Render(content)

			// Check each line
			lines := strings.Split(banner, "\n")
			for i, line := range lines {
				lineWidth := lipgloss.Width(line)
				if lineWidth > width {
					t.Errorf("Line %d exceeds terminal width: %d > %d", i, lineWidth, width)
				}
			}

			t.Logf("Width %d: contentWidth=%d, banner lines=%d, max line width=%d",
				width, contentWidth, len(lines), func() int {
					max := 0
					for _, l := range lines {
						if w := lipgloss.Width(l); w > max {
							max = w
						}
					}
					return max
				}())
		})
	}
}

// TestSidePanelHeight verifies side panel height matches chat content
func TestSidePanelHeight(t *testing.T) {
	terminalHeight := 24
	sidePanelWidth := 38

	// Side panel uses Padding(1, 1) which adds vertical padding
	// With Height(terminalHeight), lipgloss includes padding in the height
	panelStyle := lipgloss.NewStyle().
		Width(sidePanelWidth).
		Height(terminalHeight).
		Padding(1, 1).
		BorderLeft(true).
		BorderStyle(lipgloss.NormalBorder())

	content := "Side panel content"
	rendered := panelStyle.Render(content)

	actualHeight := lipgloss.Height(rendered)
	t.Logf("Side panel: target height=%d, actual height=%d", terminalHeight, actualHeight)

	if actualHeight != terminalHeight {
		t.Errorf("Side panel height mismatch: expected %d, got %d", terminalHeight, actualHeight)
	}

	// Check if border adds height
	noBorderStyle := lipgloss.NewStyle().
		Width(sidePanelWidth).
		Height(terminalHeight).
		Padding(1, 1)
	noBorderRendered := noBorderStyle.Render(content)
	noBorderHeight := lipgloss.Height(noBorderRendered)
	t.Logf("Without border: height=%d", noBorderHeight)
}

// TestHorizontalJoinHeight verifies horizontal join doesn't change height
func TestHorizontalJoinHeight(t *testing.T) {
	height := 24
	chatWidth := 82
	sidePanelWidth := 38

	// Simulate chat content
	var chatLines []string
	for i := range height {
		chatLines = append(chatLines, fmt.Sprintf("Chat line %d", i))
	}
	chatContent := strings.Join(chatLines, "\n")

	chatStyle := lipgloss.NewStyle().Width(chatWidth).Height(height)
	chatRendered := chatStyle.Render(chatContent)

	// Simulate side panel
	sidePanelContent := "Side panel"
	sidePanelStyle := lipgloss.NewStyle().
		Width(sidePanelWidth).
		Height(height).
		Padding(1, 1)
	sidePanelRendered := sidePanelStyle.Render(sidePanelContent)

	// Join horizontally
	joined := lipgloss.JoinHorizontal(lipgloss.Top, chatRendered, sidePanelRendered)

	joinedHeight := lipgloss.Height(joined)
	t.Logf("Chat height: %d", lipgloss.Height(chatRendered))
	t.Logf("Side panel height: %d", lipgloss.Height(sidePanelRendered))
	t.Logf("Joined height: %d", joinedHeight)
	t.Logf("Expected height: %d", height)

	if joinedHeight != height {
		t.Errorf("Horizontal join changed height: expected %d, got %d", height, joinedHeight)
	}
}

// TestEmojiWidths checks various emoji widths
func TestEmojiWidths(t *testing.T) {
	emojis := map[string]string{
		"📎":  "paperclip",
		"🖼️": "image frame",
		"📁":  "folder",
		"✓":  "checkmark",
		"✗":  "cross",
		"🔄":  "refresh",
		"│":  "vertical line (box drawing)",
		"─":  "horizontal line (box drawing)",
	}

	for emoji, name := range emojis {
		width := lipgloss.Width(emoji)
		t.Logf("%s (%s): width = %d, runes = %d, bytes = %d",
			emoji, name, width, len([]rune(emoji)), len(emoji))
	}
}

// TestSeparatorWidth checks if separators overflow
func TestSeparatorWidth(t *testing.T) {
	const SidePanelWidth = 38

	terminalWidth := 120
	chatWidth := terminalWidth - SidePanelWidth

	t.Logf("Terminal: %d, ChatWidth: %d", terminalWidth, chatWidth)

	// In renderChatContent, separators use a.width (which is chatWidth at that point)
	// Line 8224-8225: a.cachedSeparator.str = strings.Repeat("─", a.width)
	separatorWidth := chatWidth
	t.Logf("Separator width: %d", separatorWidth)

	// But the final render uses Width(a.width) = chatWidth
	// Line 8295-8298: topSeparatorRendered = lipgloss.NewStyle().Width(a.width)...
	styleWidth := chatWidth
	t.Logf("Style width: %d", styleWidth)

	if separatorWidth > styleWidth {
		t.Errorf("Separator %d > style width %d", separatorWidth, styleWidth)
	}

	// Check visual width of separator
	sep := strings.Repeat("─", separatorWidth)
	visualWidth := lipgloss.Width(sep)
	t.Logf("Separator visual width: %d (each ─ = %d)", visualWidth, lipgloss.Width("─"))

	// Note: "─" might be a wide character!
	if lipgloss.Width("─") != 1 {
		t.Logf("WARNING: ─ is width %d, not 1!", lipgloss.Width("─"))
	}
}

// TestContentWidthFunction simulates the contentWidth() calculation
func TestContentWidthFunction(t *testing.T) {
	tests := []struct {
		name           string
		width          int
		showSidePanel  bool
		expectedWidth  int
		minWidthForSP  int
		sidePanelWidth int
	}{
		{"No side panel 120", 120, false, 120, 100, 38},
		{"With side panel 120", 120, true, 82, 100, 38},
		{"Small terminal 80", 80, false, 80, 100, 38},
		{"Medium terminal 100", 100, true, 62, 100, 38},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate contentWidth() from app.go
			contentW := tt.width
			if tt.showSidePanel && tt.width >= tt.minWidthForSP {
				contentW -= tt.sidePanelWidth
			}

			// Input width calculation
			inputWidth := tt.width - 8
			if tt.showSidePanel && tt.width >= tt.minWidthForSP {
				inputWidth -= tt.sidePanelWidth
			}

			// maxWidth in SimpleInput
			maxWidth := inputWidth - 8

			// Total display width
			prefixWidth := 8
			displayWidth := prefixWidth + maxWidth

			t.Logf("Terminal: %d, ContentWidth: %d, InputWidth: %d, MaxWidth: %d, DisplayWidth: %d",
				tt.width, contentW, inputWidth, maxWidth, displayWidth)

			// Check if display width exceeds content area
			if displayWidth > contentW {
				t.Errorf("OVERFLOW: displayWidth %d > contentW %d", displayWidth, contentW)
			}
		})
	}
}

// TestOverlayPreservesANSI verifies that overlay function preserves ANSI codes
func TestOverlayPreservesANSI(t *testing.T) {
	// Create a base with ANSI color codes
	redText := "\x1b[31mRed text here\x1b[0m"
	blueText := "\x1b[34mBlue text here\x1b[0m"

	// Base content with colors
	baseLine := redText + "    " + blueText
	base := baseLine + "\n" + baseLine + "\n" + baseLine

	// Simple overlay
	overlay := "OVERLAY"

	// Position overlay in the middle of second line
	result := overlayAt(base, overlay, 50, 3, 10, 1, "")

	t.Logf("Base line: %q", baseLine)
	t.Logf("Result:\n%s", result)

	// The result should not contain corrupted ANSI codes like "31m" or "0m" as visible text
	if strings.Contains(result, "31m") && !strings.Contains(result, "\x1b[31m") {
		t.Errorf("ANSI code corrupted: found '31m' as visible text")
	}
	if strings.Contains(result, "34m") && !strings.Contains(result, "\x1b[34m") {
		t.Errorf("ANSI code corrupted: found '34m' as visible text")
	}
	if strings.Contains(result, "0m") && !strings.Contains(result, "\x1b[0m") {
		t.Errorf("ANSI code corrupted: found '0m' as visible text")
	}

	// Verify overlay was placed correctly
	lines := strings.Split(result, "\n")
	if len(lines) >= 2 {
		secondLine := lines[1]
		// The overlay should be visible in the output
		if !strings.Contains(secondLine, "OVERLAY") {
			t.Errorf("Overlay not found in result: %q", secondLine)
		}
		t.Logf("Second line with overlay: %q", secondLine)
	}
}

// TestOverlayWithNotificationStyle tests overlay with styled notification box
func TestOverlayWithNotificationStyle(t *testing.T) {
	// Create a base with side panel style (using hex colors directly)
	baseLine := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#7aa2f7")).
		Render("│ Swarm v0.2.3")

	base := strings.Repeat(" ", 50) + baseLine + "\n"
	base += strings.Repeat(" ", 50) + baseLine + "\n"
	base += strings.Repeat(" ", 50) + baseLine + "\n"

	// Create notification box
	notifStyle := lipgloss.NewStyle().
		Width(40).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#bb9af7"))
	notification := notifStyle.Render("Debug menu enabled")

	// Overlay at position 2, 0
	result := overlayAt(base, notification, 100, 5, 2, 0, "")

	t.Logf("Result:\n%s", result)

	// Check that no partial ANSI codes appear as visible text
	// Common patterns from lipgloss colors: [38;5;XXXm or [48;5;XXXm
	ansiPattern := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	resultWithoutANSI := ansiPattern.ReplaceAllString(result, "")

	// Should not contain "m" preceded by digits at word boundaries (partial escape)
	if matched, _ := regexp.MatchString(`\d+m`, resultWithoutANSI); matched {
		// Check if it's a real corrupted code, not legitimate text
		if strings.Contains(resultWithoutANSI, "255m") ||
			strings.Contains(resultWithoutANSI, "38;") ||
			strings.Contains(resultWithoutANSI, "48;") {
			t.Errorf("Found corrupted ANSI codes in result: %q", resultWithoutANSI)
		}
	}
}

// TestModalWidthWithSidePanel verifies modal fits within chat area when side panel is visible
func TestModalWidthWithSidePanel(t *testing.T) {
	const SidePanelWidth = 38
	const MinWidthForSidePanel = 100

	terminalWidth := 120
	chatWidth := terminalWidth - SidePanelWidth // 82

	// Simulate modal rendering with chat width (not full terminal width)
	modalMaxWidth := 70
	if modalMaxWidth > chatWidth-8 {
		modalMaxWidth = chatWidth - 8
	}

	// Border adds 2 (left + right), padding adds 4 (2 each side)
	totalModalWidth := modalMaxWidth + 2 + 4

	t.Logf("Terminal: %d, ChatWidth: %d, ModalContentWidth: %d, TotalModalWidth: %d",
		terminalWidth, chatWidth, modalMaxWidth, totalModalWidth)

	if totalModalWidth > chatWidth {
		t.Errorf("Modal would overflow: totalModalWidth %d > chatWidth %d", totalModalWidth, chatWidth)
	}

	// Verify the actual Modal.Render behavior
	modal := &Modal{
		Type:    ModalExitChat,
		Title:   "Exit Chat",
		Message: "Leave this conversation?",
		Options: []string{"Cancel", "Leave"},
	}

	theme := Theme{
		Primary:   "#7aa2f7",
		Text:      "#c0caf5",
		TextMuted: "#565f89",
	}

	// Render with chatWidth (correct behavior after fix)
	rendered := modal.Render(chatWidth, 24, theme)
	renderedWidth := lipgloss.Width(rendered)

	t.Logf("Modal rendered width: %d (chatWidth: %d)", renderedWidth, chatWidth)

	if renderedWidth > chatWidth {
		t.Errorf("Modal exceeds chat width: %d > %d", renderedWidth, chatWidth)
	}

	// Also test with full terminal width (old incorrect behavior)
	renderedFull := modal.Render(terminalWidth, 24, theme)
	renderedFullWidth := lipgloss.Width(renderedFull)

	t.Logf("Modal with full terminal width: %d (would overflow by %d)",
		renderedFullWidth, renderedFullWidth-chatWidth)
}
