package chat

import (
	"strings"
	"testing"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/provider"
)

// testTheme returns a theme with every colour populated so lipgloss styling in
// the section renderer has valid inputs.
func testTheme() Theme {
	return Theme{
		Primary: "#88aaff", Secondary: "#aa88ff", Accent: "#ffaa44",
		Success: "#44cc88", Warning: "#ffcc44", Error: "#ff5555", Info: "#44ccff",
		BG: "#101218", Border: "#333333", Text: "#eeeeee", TextDim: "#bbbbbb", TextMuted: "#888888",
	}
}

func TestRenderSystemSectionLines_ShowsCharCountAndAllContent(t *testing.T) {
	a := &App{theme: testTheme(), showFullToolOutput: true} // force expanded

	content := "FIRSTLINE\nsecondline\nthirdline\nfourthline"
	section := SystemMessageSection{
		Type:      SectionBasePrompt,
		Title:     "System Instructions (raw)",
		Icon:      "◆",
		Content:   content,
		LineCount: strings.Count(content, "\n") + 1,
		CharCount: len(content),
	}

	out := strings.Join(a.renderSystemSectionLines(section, 80), "\n")

	// Character count is shown in the header (len of content).
	wantCount := commaInt(len(content))
	if !strings.Contains(out, "["+wantCount+" chars]") {
		t.Errorf("expected char count [%s chars] in header, got:\n%s", wantCount, out)
	}
	// Every content line renders — the previous renderer dropped the first one
	// by overwriting it with the title.
	for _, want := range []string{"FIRSTLINE", "secondline", "thirdline", "fourthline", "System Instructions (raw)"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in rendered output, got:\n%s", want, out)
		}
	}
}

func TestRenderSystemSectionLines_CollapsedShowsCharCount(t *testing.T) {
	a := &App{theme: testTheme(), showFullToolOutput: false}

	section := SystemMessageSection{
		Type:      SectionSkills,
		Title:     "Available Skills",
		Icon:      "✦",
		Content:   "a\nb\nc\nd\ne", // >3 lines so it collapses
		LineCount: 5,
		CharCount: 9,
		Collapsed: true,
	}

	out := strings.Join(a.renderSystemSectionLines(section, 80), "\n")
	if !strings.Contains(out, "[9 chars]") {
		t.Errorf("collapsed header should show char count, got:\n%s", out)
	}
	if !strings.Contains(out, "Available Skills") {
		t.Errorf("collapsed header should show title, got:\n%s", out)
	}
	// Collapsed: body content must NOT be present.
	if strings.Contains(out, "\n    ") {
		t.Errorf("collapsed section should not render body lines, got:\n%s", out)
	}
}

func TestBuildToolSectionContent(t *testing.T) {
	toolList := []provider.Tool{
		{Name: "bash", Description: "run a command", Parameters: map[string]any{"type": "object"}},
		{Name: "read_file", Description: strings.Repeat("d", 120), Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}},
	}
	content, total := buildToolSectionContent(toolList)
	if total <= 0 {
		t.Fatalf("total schema chars should be positive, got %d", total)
	}
	for _, want := range []string{"read_file", "bash", "chars"} {
		if !strings.Contains(content, want) {
			t.Errorf("tool section content missing %q:\n%s", want, content)
		}
	}
	// Largest-first: read_file (big description + schema) before bash.
	if strings.Index(content, "read_file") > strings.Index(content, "bash") {
		t.Errorf("tools not sorted largest-first:\n%s", content)
	}
}

func TestRawWrap_PreservesWhitespaceAndNeverTruncates(t *testing.T) {
	in := "name1        2,000 chars\nshort line\n" + strings.Repeat("x", 50)
	out := strings.Join(rawWrap(in, 20), "\n")

	// Column-aligning runs of spaces must survive (wrapText would collapse them).
	if !strings.Contains(out, "name1        2,000") {
		t.Errorf("rawWrap collapsed internal whitespace:\n%q", out)
	}
	// A 50-char run wrapped at width 20 must keep all 50 characters (no truncation).
	if got := strings.Count(out, "x"); got != 50 {
		t.Errorf("rawWrap truncated content: got %d x's, want 50", got)
	}
}

func TestSectionAccentColor_DistinctPerType(t *testing.T) {
	a := &App{theme: testTheme()}
	base := a.sectionAccentColor(SectionBasePrompt)
	skills := a.sectionAccentColor(SectionSkills)
	gitc := a.sectionAccentColor(SectionGitContext)
	ctx := a.sectionAccentColor(SectionContext)
	// Base, skills, git, and context must not all collapse to one colour.
	if base == skills || base == gitc || skills == ctx {
		t.Errorf("section colours not distinct: base=%s skills=%s git=%s ctx=%s", base, skills, gitc, ctx)
	}
}
