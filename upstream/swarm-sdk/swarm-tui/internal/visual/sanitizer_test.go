package visual

import (
	"strings"
	"testing"
)

func TestSanitize_StripsScript(t *testing.T) {
	for _, in := range []string{
		`<script>alert(1)</script>`,
		`<SCRIPT>alert(1)</SCRIPT>`,
		`<script src="evil.js"></script>`,
		"<script>\nlots of code\n</script>",
	} {
		out := Sanitize(in)
		if strings.Contains(strings.ToLower(out), "<script") {
			t.Errorf("script survived: %q -> %q", in, out)
		}
	}
}

func TestSanitize_StripsIframe(t *testing.T) {
	in := `<iframe src="https://evil.com"></iframe>`
	if strings.Contains(strings.ToLower(Sanitize(in)), "<iframe") {
		t.Error("iframe survived")
	}
}

func TestSanitize_StripsEventHandlers(t *testing.T) {
	in := `<div onclick="alert(1)" onmouseover='alert(2)'>hi</div>`
	out := Sanitize(in)
	if strings.Contains(out, "onclick") || strings.Contains(out, "onmouseover") {
		t.Errorf("handlers survived: %q", out)
	}
	if !strings.Contains(out, "<div") {
		t.Errorf("<div> dropped: %q", out)
	}
}

func TestSanitize_StripsJavascriptURLs(t *testing.T) {
	in := `<a href="javascript:alert(1)">click</a>`
	if strings.Contains(strings.ToLower(Sanitize(in)), "javascript:") {
		t.Error("javascript: survived")
	}
}

func TestSanitize_AllowsSafeTags(t *testing.T) {
	in := `<div><h2>T</h2><p>Text <strong>bold</strong></p><ul><li>i</li></ul></div>`
	out := Sanitize(in)
	for _, tag := range []string{"<div>", "<h2>", "<p>", "<strong>", "<ul>", "<li>"} {
		if !strings.Contains(out, tag) {
			t.Errorf("safe tag %q dropped", tag)
		}
	}
}

func TestSanitize_AllowsStyleTag(t *testing.T) {
	in := `<style>.hi{color:red}</style><div class="hi">hi</div>`
	if !strings.Contains(Sanitize(in), "<style>") {
		t.Error("<style> should be allowed")
	}
}

func TestSanitize_SizeCap(t *testing.T) {
	big := strings.Repeat("<p>a</p>", 50000)
	out := Sanitize(big)
	if len(out) > 256*1024+1024 {
		t.Errorf("expected ~256KB cap, got %d", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Error("expected truncation notice")
	}
}
