package visual

import (
	"fmt"
	"regexp"
)

const maxRawHTMLBytes = 256 * 1024

var (
	scriptRe     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>|<script[^>]*/>`)
	iframeRe     = regexp.MustCompile(`(?is)<iframe[^>]*>.*?</iframe>|<iframe[^>]*/>`)
	onHandlerRe  = regexp.MustCompile(`(?i)\son\w+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	javascriptRe = regexp.MustCompile(`(?i)javascript:`)
)

// Sanitize strips script tags, iframes, event handlers, and javascript: URLs.
// Style blocks, SVG, and other standard HTML are kept so agents can still
// sketch rich mockups. Content over maxRawHTMLBytes is truncated.
func Sanitize(raw string) string {
	if len(raw) > maxRawHTMLBytes {
		raw = raw[:maxRawHTMLBytes] + fmt.Sprintf(
			`<p class="rawhtml-truncated">[truncated: %d bytes cap reached]</p>`, maxRawHTMLBytes)
	}
	out := scriptRe.ReplaceAllString(raw, "")
	out = iframeRe.ReplaceAllString(out, "")
	out = onHandlerRe.ReplaceAllString(out, "")
	out = javascriptRe.ReplaceAllString(out, "")
	return out
}
