package chat

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var mermaidResponsiveWidths = []int{12, 23, 40, 80, 120}

type mermaidResponsiveCase struct {
	name       string
	lines      []string
	direct     func([]string, Theme, int) []MarkdownLine
	narrowWant []string
}

func TestMermaidResponsiveRenderers(t *testing.T) {
	theme := makeTestTheme()
	cases := []mermaidResponsiveCase{
		{
			name: "dense_unicode_flowchart",
			lines: []string{
				"flowchart TD",
				"title 全球部署管線 — café 🚀 with a deliberately long title",
				"Start[開始 Start request with an exceptionally long label] --> Validate{Validate résumé and permissions}",
				"Validate -- accepted --> Queue[Queue asynchronous deployment work]",
				"Validate -- rejected --> Reject[Reject request and explain every validation error]",
				"Queue --> Build[Build artifacts for Linux macOS and Windows]",
				"Queue --> Audit[Audit dependencies and provenance]",
				"Build --> Test[Run unit integration race and end-to-end tests]",
				"Audit --> Test",
				"Test -.-> Ship((Ship 世界-wide))",
			},
			direct:     renderMermaidFlowchart,
			narrowWant: []string{"Start", "Validate", "開始", "Queue"},
		},
		{
			name: "many_actor_unicode_sequence",
			lines: []string{
				"sequenceDiagram",
				"title 多地域 authentication exchange with a very long title",
				"participant Client",
				"participant Gateway",
				"participant Identity",
				"participant Policy",
				"participant Database",
				"participant Audit",
				"Client->>Gateway: submit café credentials and device attestation",
				"Gateway->>Identity: verify identity 世界-wide",
				"Identity-->>Policy: evaluate a deliberately long authorization policy",
				"Policy->>Database: load roles and grants",
				"Database-->>Audit: record immutable decision",
				"Audit-->>Client: return final result",
			},
			direct:     renderMermaidSequence,
			narrowWant: []string{"Client", "Gateway", "submit", "verify"},
		},
		{
			name: "unicode_pie",
			lines: []string{
				"pie showData",
				"title Répartition mondiale 世界 🚀 with a long explanatory title",
				`"東京 engineering and research" : 37`,
				`"München operations" : 29`,
				`"São Paulo support" : 21`,
				`"Other regions" : 13`,
			},
			direct:     renderMermaidPie,
			narrowWant: []string{"東京", "München", "São", "Other"},
		},
		{
			name: "dense_unicode_xy_chart",
			lines: []string{
				"xychart-beta",
				"title Déploiements 世界 by month with a long title",
				"x-axis [Janvier-long, Février-long, 三月-long, April-long, May-long, June-long, July-long, August-long]",
				`y-axis "successful builds" 0 --> 1000`,
				"bar [80, 240, 390, 510, 670, 740, 880, 960]",
			},
			direct:     renderMermaidXYChart,
			narrowWant: []string{"Jan", "Fév", "三月", "80"},
		},
		{
			name: "malformed_pie_falls_back",
			lines: []string{
				"pie",
				"title malformed but still inspectable",
				`"missing numeric value" : nope`,
				`this is not valid Mermaid ::: 世界`,
			},
			direct: func(lines []string, theme Theme, width int) []MarkdownLine {
				return renderMermaidPie(lines, theme, width)
			},
			narrowWant: []string{"pie", "malformed", "missing"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range mermaidResponsiveWidths {
				width := width
				t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
					dispatch := mermaidResponsiveNoPanic(t, "dispatcher", func() []MarkdownLine {
						return renderMermaidDiagram(tc.lines, theme, width, true)
					})
					mermaidResponsiveAssertOutput(t, dispatch, width, tc.narrowWant)

					direct := mermaidResponsiveNoPanic(t, "specialized renderer", func() []MarkdownLine {
						return tc.direct(tc.lines, theme, width)
					})
					mermaidResponsiveAssertOutput(t, direct, width, tc.narrowWant)

					dispatchAgain := renderMermaidDiagram(tc.lines, theme, width, true)
					if !reflect.DeepEqual(dispatch, dispatchAgain) {
						t.Fatalf("dispatcher output changed between identical calls at width %d", width)
					}
					directAgain := tc.direct(tc.lines, theme, width)
					if !reflect.DeepEqual(direct, directAgain) {
						t.Fatalf("specialized output changed between identical calls at width %d", width)
					}
				})
			}
		})
	}
}

func TestMermaidResponsiveFallbackTypes(t *testing.T) {
	theme := makeTestTheme()
	cases := []struct {
		name       string
		lines      []string
		narrowWant []string
	}{
		{
			name: "class_diagram",
			lines: []string{
				"classDiagram",
				"class RésuméService {",
				"+validateWorldwideRequest(世界) Result",
				"}",
			},
			narrowWant: []string{"class", "Résumé", "validate"},
		},
		{
			name: "state_diagram",
			lines: []string{
				"stateDiagram-v2",
				"[*] --> WaitingForApproval",
				"WaitingForApproval --> Processing : autorisé 世界",
				"Processing --> [*]",
			},
			narrowWant: []string{"state", "Waiting", "Processing"},
		},
		{
			name: "gantt",
			lines: []string{
				"gantt",
				"title Programme de livraison 世界",
				"section Préparation",
				"Long requirements review :a1, 2026-01-01, 30d",
			},
			narrowWant: []string{"gantt", "Programme", "section"},
		},
		{
			name:       "unrecognized_malformed_input",
			lines:      []string{"%%% not a diagram 世界", "broken --> [", "\x1b[31mstyle-like input"},
			narrowWant: []string{"not", "diagram", "broken"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range mermaidResponsiveWidths {
				width := width
				t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
					got := mermaidResponsiveNoPanic(t, "fallback dispatcher", func() []MarkdownLine {
						return renderMermaidDiagram(tc.lines, theme, width, false)
					})
					mermaidResponsiveAssertOutput(t, got, width, tc.narrowWant)

					again := renderMermaidDiagram(tc.lines, theme, width, false)
					if !reflect.DeepEqual(got, again) {
						t.Fatalf("fallback output changed between identical calls at width %d", width)
					}
				})
			}
		})
	}
}

func TestMermaidResponsiveNonPositiveWidthsDoNotPanic(t *testing.T) {
	theme := makeTestTheme()
	inputs := [][]string{
		{"flowchart LR", "A[Alpha] --> B[Beta]"},
		{"sequenceDiagram", "Alpha->>Beta: hello"},
		{"pie", `"Alpha" : 1`, `"Beta" : 2`},
		{"xychart-beta", "x-axis [Alpha, Beta]", "bar [1, 2]"},
		{"classDiagram", "class Alpha"},
		{"pie", `"broken" : nope`},
	}

	for _, width := range []int{-100, -1, 0} {
		for i, lines := range inputs {
			name := fmt.Sprintf("width_%d/input_%d", width, i)
			t.Run(name, func(t *testing.T) {
				got := mermaidResponsiveNoPanic(t, name, func() []MarkdownLine {
					return renderMermaidDiagram(lines, theme, width, true)
				})
				mermaidResponsiveAssertOutput(t, got, width, nil)
			})
		}
	}
}

func TestMermaidResponsiveRejectsNonFinitePieValues(t *testing.T) {
	theme := makeTestTheme()
	lines := []string{
		"pie",
		`"overflow" : ` + strings.Repeat("9", 10000),
		`"valid" : 1`,
	}
	got := mermaidResponsiveNoPanic(t, "non-finite pie", func() []MarkdownLine {
		return renderMermaidDiagram(lines, theme, 40, true)
	})
	mermaidResponsiveAssertOutput(t, got, 40, []string{"valid"})
}

func TestMermaidResponsivePieTotalIsOverflowSafe(t *testing.T) {
	theme := makeTestTheme()
	hugeFinite := "1" + strings.Repeat("0", 308)
	got := renderMermaidPie([]string{
		"pie",
		`"first" : ` + hugeFinite,
		`"second" : ` + hugeFinite,
	}, theme, 40)
	plain := mermaidResponsivePlain(got)
	if strings.Count(plain, "50.0%") != 2 {
		t.Fatalf("finite large pie slices were not normalized safely:\n%s", plain)
	}
}

func TestMermaidResponsiveSanitizesTerminalControls(t *testing.T) {
	theme := makeTestTheme()
	lines := []string{
		"classDiagram",
		"class \x1b[2JUnsafe",
		"\x1b]8;;https://example.invalid\a+click()\x1b]8;;\a",
		"class Bad\rOverwrite\b\a\x7f\u0085Safe",
	}
	got := renderMermaidDiagram(lines, theme, 40, true)
	var raw strings.Builder
	for _, line := range got {
		raw.WriteString(line.Content)
		raw.WriteByte('\n')
	}
	if strings.Contains(raw.String(), "\x1b[2J") ||
		strings.Contains(raw.String(), "\x1b]8") ||
		strings.ContainsAny(raw.String(), "\r\b\a\x7f") ||
		strings.ContainsRune(raw.String(), '\u0085') ||
		strings.Contains(raw.String(), "example.invalid") {
		t.Fatalf("source terminal control sequence survived sanitization: %q", raw.String())
	}
	plain := mermaidResponsivePlain(got)
	if !strings.Contains(plain, "Unsafe") ||
		!strings.Contains(plain, "click") ||
		!strings.Contains(plain, "BadOverwriteSafe") {
		t.Fatalf("sanitization removed useful source text: %q", plain)
	}
}

func TestMermaidResponsiveFlowchartUsesActualEdges(t *testing.T) {
	theme := makeTestTheme()
	got := renderMermaidFlowchart([]string{
		"flowchart LR",
		"A[Alpha] --> C[Charlie]",
		"B[Beta] --> D[Delta]",
	}, theme, 80)
	plainLines := strings.Split(mermaidResponsivePlain(got), "\n")
	var alphaCharlie, betaDelta bool
	for _, line := range plainLines {
		alphaCharlie = alphaCharlie || (strings.Contains(line, "Alpha") && strings.Contains(line, "Charlie"))
		betaDelta = betaDelta || (strings.Contains(line, "Beta") && strings.Contains(line, "Delta"))
	}
	if !alphaCharlie || !betaDelta {
		t.Fatalf("independent flowchart edges were omitted or merged:\n%s", mermaidResponsivePlain(got))
	}

	chained := mermaidResponsivePlain(renderMermaidFlowchart([]string{
		"flowchart LR",
		"A[Alpha] --> B[Beta] --> C[Gamma]",
	}, theme, 80))
	if !strings.Contains(chained, "Alpha") ||
		!strings.Contains(chained, "Beta") ||
		!strings.Contains(chained, "Gamma") {
		t.Fatalf("chained flowchart omitted an intermediate node:\n%s", chained)
	}
	var alphaBeta, betaGamma bool
	for _, line := range strings.Split(chained, "\n") {
		alphaBeta = alphaBeta || (strings.Contains(line, "Alpha") && strings.Contains(line, "Beta"))
		betaGamma = betaGamma || (strings.Contains(line, "Beta") && strings.Contains(line, "Gamma"))
	}
	if !alphaBeta || !betaGamma {
		t.Fatalf("chained flowchart did not preserve consecutive edges:\n%s", chained)
	}
}

func TestMermaidResponsiveVisualization(t *testing.T) {
	if os.Getenv("MERMAID_VISUALIZE") != "1" {
		t.Skip("set MERMAID_VISUALIZE=1 to print responsive Mermaid renderings")
	}

	theme := makeTestTheme()
	diagrams := []struct {
		name  string
		lines []string
	}{
		{
			name: "flowchart",
			lines: []string{
				"flowchart TD",
				"title Responsive deployment 世界",
				"Start[Receive request] --> Check{Policy valid?}",
				"Check -- yes --> Build[Build and test artifacts]",
				"Check -- no --> Explain[Explain validation errors]",
				"Build --> Ship((Ship 🚀))",
			},
		},
		{
			name: "sequence",
			lines: []string{
				"sequenceDiagram",
				"title Responsive authentication 世界",
				"participant Client",
				"participant Gateway",
				"participant Identity",
				"participant Audit",
				"Client->>Gateway: submit credentials",
				"Gateway->>Identity: verify identity and policy",
				"Identity-->>Audit: record decision",
				"Audit-->>Client: return result",
			},
		},
	}

	for _, diagram := range diagrams {
		for _, width := range []int{12, 40, 120} {
			got := renderMermaidDiagram(diagram.lines, theme, width, true)
			mermaidResponsiveAssertOutput(t, got, width, nil)
			t.Logf("\n%s / width %d\n%s", diagram.name, width, mermaidResponsivePlain(got))
		}
	}
}

func mermaidResponsiveNoPanic(t *testing.T, operation string, render func() []MarkdownLine) (got []MarkdownLine) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("%s panicked: %v", operation, recovered)
		}
	}()
	return render()
}

func mermaidResponsiveAssertOutput(t *testing.T, got []MarkdownLine, width int, narrowWant []string) {
	t.Helper()
	if len(got) == 0 {
		t.Fatal("renderer returned no lines")
	}

	limit := width
	if limit < 1 {
		limit = 1
	}
	for i, line := range got {
		if line.Type != "mermaid" {
			t.Errorf("line %d has Type %q, want %q", i, line.Type, "mermaid")
		}
		if visualWidth := lipgloss.Width(line.Content); visualWidth > limit {
			t.Errorf("line %d visual width = %d, want <= %d: %q",
				i, visualWidth, limit, StripANSI(line.Content))
		}
	}

	plain := mermaidResponsivePlain(got)
	mermaidResponsiveAssertBalancedBorders(t, plain)
	if width <= 23 && len(narrowWant) > 0 {
		found := false
		for _, want := range narrowWant {
			if strings.Contains(plain, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("narrow rendering contains none of the useful labels %q:\n%s", narrowWant, plain)
		}
	}
}

func mermaidResponsiveAssertBalancedBorders(t *testing.T, plain string) {
	t.Helper()
	lines := strings.Split(plain, "\n")
	if len(lines) == 0 {
		return
	}

	for _, check := range []struct {
		line        string
		left, right string
	}{
		{line: lines[0], left: "┌", right: "┐"},
		{line: lines[0], left: "╭", right: "╮"},
		{line: lines[len(lines)-1], left: "└", right: "┘"},
		{line: lines[len(lines)-1], left: "╰", right: "╯"},
	} {
		left := strings.Count(check.line, check.left)
		right := strings.Count(check.line, check.right)
		if left != right {
			t.Errorf("unbalanced outer border pair %q/%q: %d/%d in %q",
				check.left, check.right, left, right, check.line)
		}
	}

	hasOuterBox := (strings.Contains(lines[0], "┌") && strings.Contains(lines[len(lines)-1], "┘")) ||
		(strings.Contains(lines[0], "╭") && strings.Contains(lines[len(lines)-1], "╯"))
	if hasOuterBox && len(lines) > 2 {
		for i, line := range lines[1 : len(lines)-1] {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "│") != strings.HasSuffix(trimmed, "│") {
				t.Errorf("line %d has only one outer vertical border: %q", i+1, line)
			}
		}
	}
}

func mermaidResponsivePlain(lines []MarkdownLine) string {
	plain := make([]string, len(lines))
	for i, line := range lines {
		plain[i] = StripANSI(line.Content)
	}
	return strings.Join(plain, "\n")
}
