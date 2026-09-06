package chat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/swarm-tui/internal/ui/palette"
)

// TestMarkdownRendering tests markdown rendering with all formatting
func TestMarkdownRendering(t *testing.T) {
	theme := Theme{
		Primary:   palette.Accent,
		Secondary: palette.AccentSoft,
		Accent:    palette.Teal,
		Success:   palette.Success,
		Warning:   palette.Warning,
		Error:     palette.Error,
		Info:      palette.Info,
		BG:        palette.Surface,
		BGLight:   palette.Panel,
		BGLighter: palette.PanelAlt,
		Border:    palette.Border,
		Text:      palette.Text,
		TextDim:   palette.TextDim,
		TextMuted: palette.TextMuted,
	}

	testCases := []struct {
		name     string
		input    string
		width    int
		describe string
	}{
		{
			name:     "Simple header",
			input:    "# Hello World",
			width:    80,
			describe: "H1 header",
		},
		{
			name:     "H2 header",
			input:    "## Section Title",
			width:    80,
			describe: "H2 header",
		},
		{
			name:     "Bullet list",
			input:    "- Item 1\n- Item 2\n- Item 3",
			width:    80,
			describe: "Bullet list",
		},
		{
			name:     "Numbered list",
			input:    "1. First\n2. Second\n3. Third",
			width:    80,
			describe: "Numbered list",
		},
		{
			name:     "Bold text",
			input:    "This is **bold text** in a sentence",
			width:    80,
			describe: "Inline bold",
		},
		{
			name:     "Italic text",
			input:    "This is *italic text* in a sentence",
			width:    80,
			describe: "Inline italic",
		},
		{
			name:     "Inline code",
			input:    "Use `git commit` to save changes",
			width:    80,
			describe: "Inline code",
		},
		{
			name:     "Mixed formatting",
			input:    "# Title\n\nUse **bold** and *italic* with `code` in sentence\n\n- List item with **bold**\n- Another item",
			width:    80,
			describe: "Mixed markdown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			lines := renderMarkdownWithWrappingVerbose(tc.input, tc.width, theme, false)

			fmt.Printf("\n========================================\n")
			fmt.Printf("TEST: %s (%s)\n", tc.name, tc.describe)
			fmt.Printf("INPUT:\n%s\n", tc.input)
			fmt.Printf("OUTPUT (%d lines):\n", len(lines))
			fmt.Printf("========================================\n")

			for i, line := range lines {
				fmt.Printf("[%2d] %s\n", i, line)
			}

			fmt.Printf("\nRAW ANSI (repr):\n")
			for i, line := range lines {
				fmt.Printf("[%2d] %q\n", i, line)
			}

			// Check for background colors in ANSI codes
			hasBackground := false
			for _, line := range lines {
				if strings.Contains(line, "\x1b[48;") {
					hasBackground = true
					break
				}
			}

			fmt.Printf("\nHAS BACKGROUND COLORS: %v\n", hasBackground)
			if !hasBackground {
				fmt.Printf("⚠️  WARNING: No background color codes found!\n")
			}
		})
	}
}

// TestProcessMarkdownLine tests individual line processing
func TestProcessMarkdownLine(t *testing.T) {
	theme := Theme{
		Primary:   palette.Accent,
		Secondary: palette.AccentSoft,
		Accent:    palette.Teal,
		BG:        palette.Surface,
		BGLight:   palette.Panel,
		BGLighter: palette.PanelAlt,
		Text:      palette.Text,
		TextDim:   palette.TextDim,
		TextMuted: palette.TextMuted,
	}

	testCases := []struct {
		input    string
		expected string
	}{
		{
			input:    "# Header",
			expected: "header",
		},
		{
			input:    "- Bullet",
			expected: "bullet",
		},
		{
			input:    "1. Numbered",
			expected: "number",
		},
		{
			input:    "Normal text",
			expected: "normal",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			result := processMarkdownLine(tc.input, theme)

			fmt.Printf("\nLine: %q\n", tc.input)
			fmt.Printf("Type: %s (expected %s)\n", result.Type, tc.expected)
			fmt.Printf("Content: %s\n", result.Content)
			fmt.Printf("Content (repr): %q\n", result.Content)

			if result.Type != tc.expected {
				t.Errorf("expected type %q, got %q", tc.expected, result.Type)
			}

			// Check for background
			if !strings.Contains(result.Content, "\x1b[48;") {
				fmt.Printf("⚠️  WARNING: No background in styled content!\n")
			} else {
				fmt.Printf("✓ Has background colors\n")
			}
		})
	}
}

// TestInlineStyles tests inline style application
func TestInlineStyles(t *testing.T) {
	theme := Theme{
		Primary:   palette.Accent,
		Secondary: palette.AccentSoft,
		BG:        palette.Surface,
		BGLight:   palette.Panel,
		Text:      palette.Text,
	}

	testCases := []struct {
		input string
		name  string
	}{
		{
			input: "This is **bold text** in a line",
			name:  "bold",
		},
		{
			input: "This is *italic text* in a line",
			name:  "italic",
		},
		{
			input: "Use inline code here",
			name:  "normal",
		},
		{
			input: "Mix **bold** and *italic* together",
			name:  "mixed",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := applyInlineStyles(tc.input, theme)

			fmt.Printf("\nInline Style Test: %s\n", tc.name)
			fmt.Printf("Input:  %q\n", tc.input)
			fmt.Printf("Output: %s\n", result)
			fmt.Printf("Output (repr): %q\n", result)

			// Check for background colors
			if strings.Contains(result, "\x1b[48;") {
				fmt.Printf("✓ Has background colors\n")
			} else {
				fmt.Printf("✗ NO background colors found!\n")
			}
		})
	}
}

// ── Mermaid diagram rendering tests ─────────────────────────────────────────

func makeTestTheme() Theme {
	return Theme{
		Primary:   palette.Accent,
		Secondary: palette.AccentSoft,
		Accent:    palette.Teal,
		Success:   palette.Success,
		Warning:   palette.Warning,
		Error:     palette.Error,
		Info:      palette.Info,
		BG:        palette.Surface,
		BGLight:   palette.Panel,
		BGLighter: palette.PanelAlt,
		Border:    palette.Border,
		Text:      palette.Text,
		TextDim:   palette.TextDim,
		TextMuted: palette.TextMuted,
	}
}

func TestDetectMermaidType(t *testing.T) {
	cases := []struct {
		lines    []string
		wantType string
	}{
		{[]string{"pie title My Chart"}, "pie"},
		{[]string{"pie", `"A" : 30`}, "pie"},
		{[]string{"xychart-beta", "title \"Q1\""}, "xychart-beta"},
		{[]string{"sequenceDiagram", "Alice->>Bob: hi"}, "sequenceDiagram"},
		{[]string{"flowchart LR", "A-->B"}, "flowchart"},
		{[]string{"graph TD", "A-->B"}, "graph"},
		{[]string{"gantt", "title foo"}, "gantt"},
		{[]string{"", "pie"}, "pie"},
	}
	for _, tc := range cases {
		got := detectMermaidType(tc.lines)
		if got != tc.wantType {
			t.Errorf("detectMermaidType(%v) = %q, want %q", tc.lines, got, tc.wantType)
		}
	}
}

func TestParseMermaidTitle(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
	}{
		{[]string{"pie title My Chart"}, "My Chart"},
		{[]string{"pie", "title Sales Data"}, "Sales Data"},
		{[]string{"xychart-beta", `title "Q1 Revenue"`}, "Q1 Revenue"},
		{[]string{"sequenceDiagram"}, ""},
	}
	for _, tc := range cases {
		got := parseMermaidTitle(tc.lines)
		if got != tc.want {
			t.Errorf("parseMermaidTitle(%v) = %q, want %q", tc.lines, got, tc.want)
		}
	}
}

func TestMermaidHBar(t *testing.T) {
	theme := makeTestTheme()
	colors := mermaidPalette(theme)

	// Fraction 0 → no filled blocks
	zeroBar := mermaidHBar(0, 20, colors[0], theme.TextMuted)
	if strings.Contains(zeroBar, "█") {
		t.Error("fraction=0 bar should have no filled blocks")
	}

	// Fraction 1 → all filled blocks
	fullBar := mermaidHBar(1, 20, colors[0], theme.TextMuted)
	if strings.Contains(fullBar, "░") {
		t.Error("fraction=1 bar should have no empty blocks")
	}

	// Fraction 0.5 → both kinds
	halfBar := mermaidHBar(0.5, 20, colors[0], theme.TextMuted)
	if !strings.Contains(halfBar, "█") || !strings.Contains(halfBar, "░") {
		t.Error("fraction=0.5 bar should have both filled and empty blocks")
	}

	// Out of range clamped
	over := mermaidHBar(2.0, 10, colors[0], theme.TextMuted)
	if strings.Contains(over, "░") {
		t.Error("fraction=2 should be clamped to 1 (no empty blocks)")
	}
}

func TestRenderMermaidPie(t *testing.T) {
	theme := makeTestTheme()

	t.Run("basic_pie", func(t *testing.T) {
		lines := []string{
			"pie title Browser Share",
			`"Chrome" : 64.5`,
			`"Firefox" : 24.0`,
			`"Safari" : 11.5`,
		}
		result := renderMermaidPie(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
		// Should contain box-drawing chars
		joined := joinMermaidLines(result)
		if !strings.Contains(joined, "┌") || !strings.Contains(joined, "┘") {
			t.Error("output should contain box-drawing characters")
		}
		// Should contain the labels
		if !strings.Contains(joined, "Chrome") {
			t.Error("output should contain label 'Chrome'")
		}
		// Should contain percentage symbol
		if !strings.Contains(joined, "%") {
			t.Error("output should contain percentage values")
		}
		// Should contain bar fill characters
		if !strings.Contains(joined, "█") {
			t.Error("output should contain bar fill characters")
		}
		// All lines should be MarkdownLine with Type=mermaid
		for _, ml := range result {
			if ml.Type != "mermaid" {
				t.Errorf("MarkdownLine.Type = %q, want \"mermaid\"", ml.Type)
			}
		}
		fmt.Printf("\nPie Chart output:\n")
		for _, ml := range result {
			fmt.Println(ml.Content)
		}
	})

	t.Run("no_title", func(t *testing.T) {
		lines := []string{"pie", `"A" : 70`, `"B" : 30`}
		result := renderMermaidPie(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
		joined := joinMermaidLines(result)
		if !strings.Contains(joined, "Pie Chart") {
			t.Error("default title 'Pie Chart' should be used when no title given")
		}
	})

	t.Run("empty_slices_fallback", func(t *testing.T) {
		lines := []string{"pie", "// no slices here"}
		result := renderMermaidPie(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected fallback output")
		}
	})

	t.Run("narrow_terminal", func(t *testing.T) {
		lines := []string{"pie title X", `"A" : 60`, `"B" : 40`}
		// Should not panic on narrow width
		result := renderMermaidPie(lines, theme, 30)
		if len(result) == 0 {
			t.Fatal("expected non-empty output even on narrow terminal")
		}
	})
}

func TestRenderMermaidXYChart(t *testing.T) {
	theme := makeTestTheme()

	t.Run("basic_xychart", func(t *testing.T) {
		lines := []string{
			"xychart-beta",
			`title "Q1 Sales"`,
			"x-axis [Jan, Feb, Mar, Apr]",
			"y-axis 0 --> 10000",
			"bar [4000, 6500, 8200, 9100]",
		}
		result := renderMermaidXYChart(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
		joined := joinMermaidLines(result)
		// Should contain the chart title
		if !strings.Contains(joined, "Q1 Sales") {
			t.Error("output should contain chart title")
		}
		// Should contain the x-axis labels
		if !strings.Contains(joined, "Jan") {
			t.Error("output should contain x-axis label")
		}
		// Should contain bar characters
		if !strings.Contains(joined, "▓") {
			t.Error("output should contain vertical bar characters")
		}
		// Should contain y-axis divider
		if !strings.Contains(joined, "┴") {
			t.Error("output should contain y-axis bottom divider")
		}
		fmt.Printf("\nXY Chart output:\n")
		for _, ml := range result {
			fmt.Println(ml.Content)
		}
	})

	t.Run("no_bar_data_fallback", func(t *testing.T) {
		lines := []string{"xychart-beta", "title \"Foo\""}
		result := renderMermaidXYChart(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected fallback output")
		}
	})

	t.Run("inferred_x_labels", func(t *testing.T) {
		lines := []string{"xychart-beta", "bar [10, 20, 30]"}
		// Should not panic when x-axis not specified
		result := renderMermaidXYChart(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
	})
}

func TestRenderMermaidSequence(t *testing.T) {
	theme := makeTestTheme()

	t.Run("basic_sequence", func(t *testing.T) {
		lines := []string{
			"sequenceDiagram",
			"participant Alice",
			"participant Bob",
			"Alice->>Bob: Hello!",
			"Bob-->>Alice: Hi there!",
		}
		result := renderMermaidSequence(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
		joined := joinMermaidLines(result)
		if !strings.Contains(joined, "Alice") {
			t.Error("output should contain actor name 'Alice'")
		}
		if !strings.Contains(joined, "Bob") {
			t.Error("output should contain actor name 'Bob'")
		}
		// Should contain arrow characters
		if !strings.Contains(joined, ">") {
			t.Error("output should contain arrow characters")
		}
		// Should contain lifeline
		if !strings.Contains(joined, "|") {
			t.Error("output should contain lifeline characters")
		}
		fmt.Printf("\nSequence Diagram output:\n")
		for _, ml := range result {
			fmt.Println(ml.Content)
		}
	})

	t.Run("implicit_actors", func(t *testing.T) {
		lines := []string{
			"sequenceDiagram",
			"Alice->>Bob: Ping",
			"Bob-->>Alice: Pong",
		}
		result := renderMermaidSequence(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output for implicit actors")
		}
	})

	t.Run("no_messages_fallback", func(t *testing.T) {
		lines := []string{"sequenceDiagram", "participant Alice"}
		result := renderMermaidSequence(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected fallback output")
		}
	})
}

func TestRenderMermaidFlowchart(t *testing.T) {
	theme := makeTestTheme()

	t.Run("lr_chain", func(t *testing.T) {
		lines := []string{
			"flowchart LR",
			"A[Start] --> B{Decision}",
			"B -- Yes --> C[OK]",
			"B -- No --> D[Fail]",
			"C --> E[End]",
			"D --> E",
		}
		result := renderMermaidFlowchart(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
		joined := joinMermaidLines(result)
		if !strings.Contains(joined, "Start") {
			t.Error("output should contain 'Start' node label")
		}
		if !strings.Contains(joined, "Decision") {
			t.Error("output should contain 'Decision' node label")
		}
		// Should have arrow indicators
		if !strings.Contains(joined, ">") {
			t.Error("output should contain arrow characters")
		}
		fmt.Printf("\nFlowchart (LR) output:\n")
		for _, ml := range result {
			fmt.Println(ml.Content)
		}
	})

	t.Run("td_layout", func(t *testing.T) {
		lines := []string{
			"flowchart TD",
			"A[Top] --> B[Middle]",
			"B --> C[Bottom]",
		}
		result := renderMermaidFlowchart(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output for TD layout")
		}
		joined := joinMermaidLines(result)
		if !strings.Contains(joined, "↓") {
			t.Error("TD layout should contain downward arrow")
		}
		fmt.Printf("\nFlowchart (TD) output:\n")
		for _, ml := range result {
			fmt.Println(ml.Content)
		}
	})

	t.Run("simple_graph", func(t *testing.T) {
		lines := []string{"graph LR", "A-->B", "B-->C"}
		result := renderMermaidFlowchart(lines, theme, 80)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
	})
}

func TestRenderMermaidFallback(t *testing.T) {
	theme := makeTestTheme()

	t.Run("gantt_fallback", func(t *testing.T) {
		lines := []string{
			"gantt",
			"title Project Schedule",
			"dateFormat YYYY-MM-DD",
			"section Phase 1",
			"Task A : 2024-01-01, 7d",
		}
		result := renderMermaidFallback(lines, "gantt", theme, 80, true)
		if len(result) == 0 {
			t.Fatal("expected non-empty output")
		}
		joined := joinMermaidLines(result)
		// Header should mention the type
		if !strings.Contains(joined, "Gantt") {
			t.Error("fallback should mention diagram type in header")
		}
		// Should show some source lines
		if !strings.Contains(joined, "gantt") {
			t.Error("fallback should show source preview")
		}
		fmt.Printf("\nFallback (gantt) output:\n")
		for _, ml := range result {
			fmt.Println(ml.Content)
		}
	})

	t.Run("compact_truncation", func(t *testing.T) {
		// More than 6 lines → truncated in compact mode
		lines := make([]string, 15)
		for i := range lines {
			lines[i] = fmt.Sprintf("line %d", i+1)
		}
		result := renderMermaidFallback(lines, "classDiagram", theme, 80, false)
		joined := joinMermaidLines(result)
		if !strings.Contains(joined, "more lines") {
			t.Error("compact fallback should show truncation message for long diagrams")
		}
	})
}

func TestRenderMermaidDiagramDispatch(t *testing.T) {
	theme := makeTestTheme()

	cases := []struct {
		name    string
		lines   []string
		wantBox bool
	}{
		{
			name:    "pie_dispatch",
			lines:   []string{"pie title X", `"A" : 50`, `"B" : 50`},
			wantBox: true,
		},
		{
			name:    "xychart_dispatch",
			lines:   []string{"xychart-beta", "x-axis [a,b]", "y-axis 0 --> 100", "bar [40, 80]"},
			wantBox: true,
		},
		{
			name:    "sequence_dispatch",
			lines:   []string{"sequenceDiagram", "A->>B: hi", "B-->>A: ok"},
			wantBox: true,
		},
		{
			name:    "flowchart_dispatch",
			lines:   []string{"flowchart LR", "A[s]-->B[e]"},
			wantBox: true,
		},
		{
			name:    "gantt_dispatch",
			lines:   []string{"gantt", "title foo"},
			wantBox: true,
		},
		{
			name:    "empty_lines",
			lines:   []string{"", ""},
			wantBox: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Should never panic
			result := renderMermaidDiagram(tc.lines, theme, 80, true)
			if len(result) == 0 {
				t.Fatal("renderMermaidDiagram returned empty output")
			}
			if tc.wantBox {
				joined := joinMermaidLines(result)
				if !strings.Contains(joined, "┌") {
					t.Error("expected box-drawing characters in output")
				}
			}
		})
	}
}

func TestMermaidViaMarkdownPipeline(t *testing.T) {
	theme := makeTestTheme()

	// Test that mermaid blocks flow through the full markdown pipeline
	markdown := "Here is a chart:\n\n```mermaid\npie title OS Share\n\"Linux\" : 55\n\"macOS\" : 30\n\"Windows\" : 15\n```\n\nAnd some text after."

	result := renderMarkdownWithWrapping(markdown, 80, theme)
	if len(result) == 0 {
		t.Fatal("expected non-empty output from full pipeline")
	}

	joined := strings.Join(result, "\n")
	// Diagram should be rendered
	if !strings.Contains(joined, "Linux") {
		t.Error("full pipeline should render mermaid pie slice labels")
	}
	// Text around the block should still render
	if !strings.Contains(joined, "Here is a chart") {
		t.Error("text before mermaid block should still appear")
	}
	if !strings.Contains(joined, "And some text after") {
		t.Error("text after mermaid block should still appear")
	}
	fmt.Printf("\nFull pipeline output (%d lines):\n", len(result))
	for _, line := range result {
		fmt.Println(line)
	}
}

// joinMermaidLines concatenates MarkdownLine contents for assertion checks.
func joinMermaidLines(lines []MarkdownLine) string {
	var parts []string
	for _, ml := range lines {
		parts = append(parts, ml.Content)
	}
	return strings.Join(parts, "\n")
}

// ═══════════════════════════════════════════════════════════════════════════════
// TABLE RENDERING TESTS — viewport width regression suite
// ═══════════════════════════════════════════════════════════════════════════════

// stripANSIForTest removes ANSI escape sequences for measurement assertions.
func stripANSIForTest(s string) string {
	return StripANSI(s)
}

// TestRenderMarkdownTable_ViewportWidths reproduces the table rendering bug
// where tables overflow or render incorrectly in smaller viewports.
func TestRenderMarkdownTable_ViewportWidths(t *testing.T) {
	theme := makeTestTheme()

	// ── Test tables with increasing complexity ──────────────────────────────

	tables := []struct {
		name  string
		input string
	}{
		{
			name: "simple_3col",
			input: `| Name | Age | City |
| --- | --- | --- |
| Alice | 30 | New York |
| Bob | 25 | London |`,
		},
		{
			name: "wide_content",
			input: `| Feature | Description | Status |
| --- | --- | --- |
| Authentication | OAuth 2.0 with PKCE flow for secure login | Complete |
| Authorization | Role-based access control with fine-grained permissions | In Progress |
| Data Export | Export all user data in CSV, JSON, or XML format | Planned |`,
		},
		{
			name: "many_columns",
			input: `| ID | Name | Email | Role | Department | Location | Status |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | Alice Johnson | alice@example.com | Admin | Engineering | NYC | Active |
| 2 | Bob Smith | bob@example.com | User | Marketing | London | Active |
| 3 | Carol White | carol@example.com | Moderator | Support | Tokyo | Inactive |`,
		},
		{
			name: "two_columns_wide_text",
			input: `| Clause | Issue |
| --- | --- |
| Limitation of Liability | The cap is set at fees paid in prior 12 months which is standard but should be negotiated to 6 months for better protection |
| Indemnification | Asymmetric indemnification obligations favor the vendor significantly and should be made mutual |`,
		},
	}

	// ── Viewport widths to test (from extreme narrow to ultrawide) ─────────
	widths := []int{30, 40, 50, 60, 80, 100, 120, 160, 200}

	for _, table := range tables {
		t.Run(table.name, func(t *testing.T) {
			for _, w := range widths {
				t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
					lines := renderMarkdownWithWrapping(table.input, w, theme)

					fmt.Printf("\n┌─ TABLE: %s @ width=%d ─────────────────────\n", table.name, w)
					fmt.Printf("│ ruler: ")
					for i := 1; i <= w; i++ {
						if i%10 == 0 {
							fmt.Printf("%d", (i/10)%10)
						} else if i%5 == 0 {
							fmt.Printf("+")
						} else {
							fmt.Printf("·")
						}
					}
					fmt.Printf("\n")

					overflowDetected := false
					for i, line := range lines {
						plain := stripANSIForTest(line)
						visWidth := PrintableWidth(line)
						marker := " "
						if visWidth > w {
							marker = "!"
							overflowDetected = true
						}
						fmt.Printf("│%s[%2d] (vw=%3d) %s\n", marker, i, visWidth, plain)
					}
					fmt.Printf("└────────────────────────────────────────────\n")

					if overflowDetected {
						t.Errorf("TABLE OVERFLOW: table %q at width=%d has lines exceeding viewport", table.name, w)
					}

					// Also verify no line is empty when it shouldn't be
					if len(lines) == 0 {
						t.Errorf("TABLE EMPTY: table %q at width=%d produced zero output lines", table.name, w)
					}
				})
			}
		})
	}
}

// TestRenderMarkdownTable_DirectUnit tests renderMarkdownTable directly
// to isolate table-specific rendering issues from the full pipeline.
func TestRenderMarkdownTable_DirectUnit(t *testing.T) {
	theme := makeTestTheme()

	rows := []string{
		"| Name | Description | Status |",
		"| --- | --- | --- |",
		"| Feature A | A long description that should get truncated or wrapped in narrow viewports | Done |",
		"| Feature B | Short desc | In Progress |",
	}

	widths := []int{30, 40, 50, 60, 80, 120}

	for _, w := range widths {
		t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
			result := renderMarkdownTable(rows, theme, w)

			fmt.Printf("\n┌─ DIRECT TABLE @ width=%d ──────────────────\n", w)
			for i, ml := range result {
				plain := stripANSIForTest(ml.Content)
				visWidth := PrintableWidth(ml.Content)
				marker := " "
				if visWidth > w {
					marker = "!"
				}
				fmt.Printf("│%s[%2d] (vw=%3d) %s\n", marker, i, visWidth, plain)
			}
			fmt.Printf("└────────────────────────────────────────────\n")

			for i, ml := range result {
				visWidth := PrintableWidth(ml.Content)
				if visWidth > w {
					t.Errorf("line %d: visual width %d exceeds maxWidth %d", i, visWidth, w)
				}
			}
		})
	}
}

// TestRenderMarkdownTable_ShrinkOnlyLastColumn demonstrates that the
// current algorithm only shrinks the LAST column, which fails when
// other columns are also wide or when the last column is already tiny.
func TestRenderMarkdownTable_ShrinkOnlyLastColumn(t *testing.T) {
	theme := makeTestTheme()

	// Case where FIRST column is wide — last-column-only shrink won't help
	rows := []string{
		"| Very Long First Column Header That Is Extremely Wide | B |",
		"| --- | --- |",
		"| This cell has a ton of content that overflows badly | x |",
	}

	for _, w := range []int{40, 50, 60} {
		t.Run(fmt.Sprintf("wide_first_col_width_%d", w), func(t *testing.T) {
			result := renderMarkdownTable(rows, theme, w)

			fmt.Printf("\n┌─ WIDE FIRST COL @ width=%d ─────────────────\n", w)
			for i, ml := range result {
				plain := stripANSIForTest(ml.Content)
				visWidth := PrintableWidth(ml.Content)
				marker := " "
				if visWidth > w {
					marker = "!"
				}
				fmt.Printf("│%s[%2d] (vw=%3d) %s\n", marker, i, visWidth, plain)
			}
			fmt.Printf("└────────────────────────────────────────────\n")

			for i, ml := range result {
				visWidth := PrintableWidth(ml.Content)
				if visWidth > w {
					t.Errorf("line %d: visual width %d exceeds maxWidth %d", i, visWidth, w)
				}
			}
		})
	}
}

// TestRenderMarkdownTable_ExtremeNarrow tests behavior at very small widths
// where the table cannot possibly fit — should degrade gracefully.
// Structural minimum for N columns = 1 + N*(1+3) = 1 + 4N.
// For 3 columns: minimum = 13.  Viewports below that physically cannot hold the table.
func TestRenderMarkdownTable_ExtremeNarrow(t *testing.T) {
	theme := makeTestTheme()

	rows := []string{
		"| A | B | C |",
		"| --- | --- | --- |",
		"| 1 | 2 | 3 |",
	}

	// Structural minimum for 3 cols with 1-char minimum = 1 + 3*4 = 13
	structuralMin := 1 + 3*4

	for _, w := range []int{10, 13, 15, 20, 25} {
		t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
			result := renderMarkdownTable(rows, theme, w)

			fmt.Printf("\n┌─ EXTREME NARROW @ width=%d ──────────────────\n", w)
			for i, ml := range result {
				plain := stripANSIForTest(ml.Content)
				visWidth := PrintableWidth(ml.Content)
				marker := " "
				if visWidth > w {
					marker = "!"
				}
				fmt.Printf("│%s[%2d] (vw=%3d) %s\n", marker, i, visWidth, plain)
			}
			fmt.Printf("└────────────────────────────────────────────\n")

			if w >= structuralMin {
				// At or above structural minimum, table MUST fit
				for i, ml := range result {
					visWidth := PrintableWidth(ml.Content)
					if visWidth > w {
						t.Errorf("line %d: visual width %d exceeds maxWidth %d (at or above structural min %d)",
							i, visWidth, w, structuralMin)
					}
				}
			} else {
				// Below structural minimum — verify it renders at structural min, not larger
				for _, ml := range result {
					visWidth := PrintableWidth(ml.Content)
					if visWidth > structuralMin {
						t.Errorf("below structural min: width %d but rendered to %d (larger than structural min %d)",
							w, visWidth, structuralMin)
					}
				}
			}
		})
	}
}
