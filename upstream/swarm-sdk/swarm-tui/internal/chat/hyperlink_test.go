package chat

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// countLinkSegments returns how many OSC 8 openings and closings a string has.
func countLinkSegments(s string) (opens, closes int) {
	i := 0
	for {
		idx := strings.Index(s[i:], oscPrefix)
		if idx < 0 {
			return opens, closes
		}
		start := i + idx
		end, width := -1, 0
		for j := start + len(oscPrefix); j < len(s); j++ {
			if s[j] == '\x07' {
				end, width = j, 1
				break
			}
			if s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\' {
				end, width = j, 2
				break
			}
		}
		if end < 0 {
			return opens, closes
		}
		payload := s[start+len(oscPrefix) : end]
		if semi := strings.IndexByte(payload, ';'); semi == len(payload)-1 {
			closes++
		} else {
			opens++
		}
		i = end + width
	}
}

// TestHyperlinkSchemeAllowlist is the security test. A link target is a
// capability: the terminal hands it to a handler on click. Anything outside the
// allowlist must degrade to plain text rather than become clickable.
func TestHyperlinkSchemeAllowlist(t *testing.T) {
	refused := []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox",
		"jAvAsCrIpT:alert(1)",
		"ssh://host/etc/passwd",
		"",
		"   ",
		"not a url at all",
		"https://example.com/\x1b]8;;javascript:alert(1)\x1b\\",
		"https://example.com/\x07",
		"https://example.com/" + strings.Repeat("x", 4000),
	}
	for _, uri := range refused {
		t.Run(fmt.Sprintf("refuse_%.24q", uri), func(t *testing.T) {
			if got := Hyperlink("label", uri); got != "label" {
				t.Errorf("Hyperlink(%q) produced a link: %q", uri, got)
			}
		})
	}

	accepted := []string{
		"https://example.com",
		"http://example.com/path?q=1#frag",
		"mailto:someone@example.com",
		"file://host/tmp/main.go#42",
	}
	for _, uri := range accepted {
		t.Run(fmt.Sprintf("accept_%.24q", uri), func(t *testing.T) {
			got := Hyperlink("label", uri)
			if got == "label" {
				t.Errorf("Hyperlink(%q) refused a permitted scheme", uri)
			}
			if ansi.StringWidth(got) != len("label") {
				t.Errorf("link changed the visible width: %d", ansi.StringWidth(got))
			}
			if StripANSI(got) != "label" {
				t.Errorf("link markup leaked into text: %q", StripANSI(got))
			}
		})
	}
}

// TestHyperlinkNeverEmitsControlBytes guards the escape-injection boundary: the
// URI is embedded inside an escape sequence, so a raw ESC/BEL in the URI would
// terminate that sequence early and let the rest be read as terminal commands.
func TestHyperlinkNeverEmitsControlBytes(t *testing.T) {
	hostile := []string{
		"https://example.com/\x1b\\evil",
		"https://example.com/\x07evil",
		"https://example.com/\x00evil",
		"https://example.com/\x9bevil",
	}
	for _, uri := range hostile {
		got := Hyperlink("x", uri)
		for _, b := range []byte(got) {
			if b == 0x07 || b == 0x00 || b == 0x9b {
				t.Errorf("control byte %#x survived into the emitted sequence for %q", b, uri)
			}
		}
	}
}

// TestLinkSurvivesWrapping is the counterpart to the image-placeholder test: a
// link that spans a wrap boundary must be reopened on every physical line, and
// must not be left open past the end of the last one.
func TestLinkSurvivesWrapping(t *testing.T) {
	label := strings.TrimSpace(strings.Repeat("clickable ", 12))
	line := Hyperlink(label, "https://example.com/docs")

	for _, width := range []int{10, 20, 30, 48, 80} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			wrapped := wrapLineToWidth(line, width)

			for i, out := range wrapped {
				opens, closes := countLinkSegments(out)
				if opens != closes {
					t.Errorf("line %d leaves the link unbalanced (%d opens, %d closes): %q",
						i, opens, closes, out)
				}
				if strings.Contains(StripANSI(out), "clickable") && opens == 0 {
					t.Errorf("line %d shows link text but reopens no link: %q", i, out)
				}
				if got := ansi.StringWidth(out); got > width {
					t.Errorf("line %d is %d columns wide, limit %d", i, got, width)
				}
			}
		})
	}
}

// TestWrappedLinkSegmentsShareAnID verifies the segments of a split link are
// grouped: the same id means the terminal treats them as one link and
// highlights them together, not as several unrelated links that happen to abut.
func TestWrappedLinkSegmentsShareAnID(t *testing.T) {
	line := Hyperlink(strings.TrimSpace(strings.Repeat("word ", 20)), "https://example.com/x")
	wrapped := wrapLineToWidth(line, 18)

	var ids []string
	for _, out := range wrapped {
		for _, part := range strings.Split(out, oscPrefix)[1:] {
			if idx := strings.Index(part, ";"); idx > 0 {
				ids = append(ids, part[:idx])
			}
		}
	}
	if len(ids) < 2 {
		t.Fatalf("expected the link to be split across lines, got ids %v", ids)
	}
	for _, id := range ids {
		if id != ids[0] {
			t.Errorf("wrapped segments used different ids (%v) — the terminal will treat them as separate links", ids)
			break
		}
	}
}

// TestLinkifyDetection covers the detectors and the trust boundary.
func TestLinkifyDetection(t *testing.T) {
	t.Run("bare_url", func(t *testing.T) {
		got := Linkify("see https://example.com/docs for more", LinkifyOptions{})
		if !strings.Contains(got, oscPrefix) {
			t.Errorf("bare URL was not linked: %q", got)
		}
		if StripANSI(got) != "see https://example.com/docs for more" {
			t.Errorf("visible text changed: %q", StripANSI(got))
		}
	})

	t.Run("trailing_punctuation_not_swallowed", func(t *testing.T) {
		got := Linkify("visit https://example.com.", LinkifyOptions{})
		if !strings.Contains(got, "https://example.com"+stringTerm) {
			t.Errorf("trailing full stop was captured into the target: %q", got)
		}
		if !strings.HasSuffix(StripANSI(got), "example.com.") {
			t.Errorf("the full stop disappeared from the text: %q", StripANSI(got))
		}
	})

	t.Run("markdown_label_requires_permission", func(t *testing.T) {
		md := "[docs](https://example.com)"
		withLabels := Linkify(md, LinkifyOptions{AllowLabels: true})
		if StripANSI(withLabels) != "docs" {
			t.Errorf("labelled link should render as its label, got %q", StripANSI(withLabels))
		}
		withoutLabels := Linkify(md, LinkifyOptions{})
		if !strings.Contains(StripANSI(withoutLabels), "example.com") {
			t.Errorf("untrusted source should keep the raw target visible, got %q", StripANSI(withoutLabels))
		}
	})

	t.Run("hostile_markdown_target_refused", func(t *testing.T) {
		got := Linkify("[totally safe](javascript:alert(1))", LinkifyOptions{AllowLabels: true})
		if strings.Contains(got, oscPrefix) {
			t.Errorf("javascript: target became a link: %q", got)
		}
	})

	t.Run("preformatted_text_untouched", func(t *testing.T) {
		styled := "\x1b[31mhttps://example.com\x1b[0m"
		if got := Linkify(styled, LinkifyOptions{}); got != styled {
			t.Errorf("Linkify modified already-styled text: %q", got)
		}
	})
}

// TestFileURL pins the encoding relied on for jump-to-line.
func TestFileURL(t *testing.T) {
	got := FileURL("/tmp/project/main.go", 42)
	if !strings.HasPrefix(got, "file://") {
		t.Fatalf("missing scheme: %q", got)
	}
	if !strings.HasSuffix(got, "/tmp/project/main.go#42") {
		t.Errorf("path or line fragment wrong: %q", got)
	}
	if noLine := FileURL("/tmp/project/main.go", 0); strings.Contains(noLine, "#") {
		t.Errorf("line 0 should omit the fragment: %q", noLine)
	}
}
