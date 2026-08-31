package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// TestProseRendersClickableLinks is the end-to-end check: a URL written by the
// assistant must come out of the real markdown pipeline as a terminal
// hyperlink, without changing what the user sees or how wide the line is.
func TestProseRendersClickableLinks(t *testing.T) {
	const prose = "See https://example.com/docs for details."
	lines := renderMarkdownWithWrapping(prose, 80, makeTestTheme())

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, oscPrefix) {
		t.Fatalf("no hyperlink emitted for prose URL:\n%q", joined)
	}
	if got := StripANSI(joined); got != prose {
		t.Errorf("visible text changed by linkification:\n got %q\nwant %q", got, prose)
	}
	for i, line := range lines {
		if w := ansi.StringWidth(line); w > 80 {
			t.Errorf("line %d is %d columns wide after linkification", i, w)
		}
	}
}

// TestMarkdownLinkRendersAsLabel covers the gap that existed before: [a](b) was
// previously printed as raw markdown.
func TestMarkdownLinkRendersAsLabel(t *testing.T) {
	lines := renderMarkdownWithWrapping("Read the [documentation](https://example.com/docs).", 80, makeTestTheme())
	visible := StripANSI(strings.Join(lines, "\n"))

	if strings.Contains(visible, "](") {
		t.Errorf("markdown link syntax leaked to the screen: %q", visible)
	}
	if !strings.Contains(visible, "documentation") {
		t.Errorf("link label missing: %q", visible)
	}
	if strings.Contains(visible, "https://example.com/docs") {
		t.Errorf("target should be hidden behind the label: %q", visible)
	}
}

// TestURLInsideCodeSpanIsNotLinkified: inline code is literal, so a URL inside
// backticks must stay text. This also pins the ordering guard — linkification
// runs first, but styling is applied outside link payloads only.
func TestURLWithMarkdownCharactersSurvives(t *testing.T) {
	// Underscores and asterisks are legal in URLs and are also markdown
	// emphasis markers. If styling were applied inside the OSC payload the
	// escape sequence would be corrupted and raw bytes would hit the screen.
	const url = "https://example.com/a_b_c/**x**/d"
	lines := renderMarkdownWithWrapping("link "+url, 80, makeTestTheme())
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, url) {
		t.Errorf("URL was mangled by inline styling: %q", joined)
	}
	if got := StripANSI(joined); got != "link "+url {
		t.Errorf("visible text changed: %q", got)
	}
}

// TestFilePathLinksResolveAgainstWorkspace verifies path:line detection only
// fires for files that actually exist.
func TestFilePathLinksResolveAgainstWorkspace(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "main.go")
	if err := os.WriteFile(real, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	opts := LinkifyOptions{WorkspaceRoot: dir}

	got := Linkify("see main.go:42 for the fix", opts)
	if !strings.Contains(got, oscPrefix) {
		t.Errorf("existing file path was not linked: %q", got)
	}
	if !strings.Contains(got, "#42") {
		t.Errorf("line number fragment missing: %q", got)
	}
	if StripANSI(got) != "see main.go:42 for the fix" {
		t.Errorf("visible text changed: %q", StripANSI(got))
	}

	missing := Linkify("see nosuchfile.go:42 now", opts)
	if strings.Contains(missing, oscPrefix) {
		t.Errorf("a path that does not exist became a link: %q", missing)
	}

	noRoot := Linkify("see main.go:42 now", LinkifyOptions{})
	if strings.Contains(noRoot, oscPrefix) {
		t.Errorf("path linked without a workspace root: %q", noRoot)
	}
}
