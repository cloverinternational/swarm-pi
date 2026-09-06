// html_to_markdown.go — converts HTML to plain readable text using the
// golang.org/x/net/html tokenizer. No external dependencies beyond what is
// already in go.mod.
//
// The conversion is intentionally simple:
//   - <script>, <style>, <head> content is stripped entirely.
//   - Block-level elements (<p>, <div>, <h1>–<h6>, <li>, <br>, <tr>) emit a newline.
//   - <a href=...> links are rendered as "text (href)".
//   - <img alt=...> renders as "[alt]" when alt is non-empty.
//   - All other tags are stripped; their text content is preserved.
//   - Consecutive blank lines are collapsed to a single blank line.
//   - Output is truncated at maxMarkdownLength characters.
package web_fetch

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
)

const maxMarkdownLength = 100_000

// htmlToMarkdown converts an HTML string to plain readable text/markdown.
func htmlToMarkdown(src string) string {
	result, _ := htmlToMarkdownWithTruncation(src)
	return result
}

// htmlToMarkdownWithTruncation also reports whether conversion stopped at its
// internal safety limit. Callers that expose truncation metadata must use this
// form so the cutoff remains machine-detectable.
func htmlToMarkdownWithTruncation(src string) (string, bool) {
	z := html.NewTokenizer(strings.NewReader(src))

	var sb strings.Builder
	truncated := false
	// skip is > 0 while inside a tag whose content should be discarded.
	skip := 0
	skipTags := map[string]bool{
		"script": true, "style": true, "head": true,
		"noscript": true, "iframe": true, "svg": true,
	}
	// block tags emit a newline before their content.
	blockTags := map[string]bool{
		"p": true, "div": true, "section": true, "article": true,
		"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
		"li": true, "dt": true, "dd": true, "tr": true, "blockquote": true,
		"pre": true, "br": true, "hr": true, "header": true, "footer": true,
		"nav": true, "main": true, "aside": true,
	}

	var hrefStack []string // track <a href> values

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			tag, hasAttr := z.TagName()
			tagName := string(tag)

			if skipTags[tagName] {
				skip++
				continue
			}
			if skip > 0 {
				continue
			}

			if blockTags[tagName] {
				sb.WriteByte('\n')
			}

			// Render headings with markdown prefix.
			switch tagName {
			case "h1":
				sb.WriteString("# ")
			case "h2":
				sb.WriteString("## ")
			case "h3":
				sb.WriteString("### ")
			case "h4":
				sb.WriteString("#### ")
			}

			// Capture href for <a> tags.
			if tagName == "a" && hasAttr {
				href := attrValue(z, "href")
				hrefStack = append(hrefStack, href)
			}

			// Render alt text for images.
			if tagName == "img" && hasAttr {
				alt := attrValue(z, "alt")
				if alt != "" {
					sb.WriteByte('[')
					sb.WriteString(alt)
					sb.WriteByte(']')
				}
			}

		case html.EndTagToken:
			tag, _ := z.TagName()
			tagName := string(tag)

			if skipTags[tagName] {
				if skip > 0 {
					skip--
				}
				continue
			}
			if skip > 0 {
				continue
			}

			if blockTags[tagName] {
				sb.WriteByte('\n')
			}

			// Append href when closing an <a> tag.
			if tagName == "a" && len(hrefStack) > 0 {
				href := hrefStack[len(hrefStack)-1]
				hrefStack = hrefStack[:len(hrefStack)-1]
				if href != "" && !strings.HasPrefix(href, "javascript:") {
					sb.WriteString(" (")
					sb.WriteString(href)
					sb.WriteByte(')')
				}
			}

		case html.TextToken:
			if skip > 0 {
				continue
			}
			text := string(z.Text())
			// Normalize whitespace.
			text = strings.Join(strings.Fields(text), " ")
			if text != "" {
				sb.WriteString(text)
				sb.WriteByte(' ')
			}
		}

		// Truncate early to avoid building huge strings.
		if sb.Len() >= maxMarkdownLength {
			truncated = true
			break
		}
	}

	result := collapseBlankLines(sb.String())
	if len(result) > maxMarkdownLength {
		truncated = true
		// Truncate at a valid UTF-8 rune boundary to avoid sending invalid
		// byte sequences downstream (e.g. into PostgreSQL TEXT columns).
		truncAt := maxMarkdownLength
		for truncAt > 0 && !utf8.ValidString(result[:truncAt]) {
			truncAt--
		}
		result = result[:truncAt] + "\n\n[Content truncated…]"
	}
	return result, truncated
}

// attrValue reads through token attributes and returns the value for key.
// IMPORTANT: must be called immediately after z.TagName() while hasAttr is true.
func attrValue(z *html.Tokenizer, key string) string {
	for {
		k, v, more := z.TagAttr()
		if string(k) == key {
			return string(v)
		}
		if !more {
			break
		}
	}
	return ""
}

// collapseBlankLines reduces runs of 3+ newlines to exactly 2 newlines.
func collapseBlankLines(s string) string {
	var sb strings.Builder
	consecutive := 0
	for _, ch := range s {
		if ch == '\n' {
			consecutive++
			if consecutive <= 2 {
				sb.WriteRune(ch)
			}
		} else {
			consecutive = 0
			sb.WriteRune(ch)
		}
	}
	return strings.TrimSpace(sb.String())
}
