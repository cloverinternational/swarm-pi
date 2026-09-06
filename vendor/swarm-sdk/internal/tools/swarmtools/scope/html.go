package scope

import (
	"regexp"
	"strings"
)

// HTMLDetector detects scope boundaries in HTML, XML, JSX, Vue, and Svelte files.
// Uses opening/closing tag matching for scope. Void elements (br, img, etc.)
// don't create scopes.
type HTMLDetector struct{}

func init() {
	Register(&HTMLDetector{},
		".html", ".htm", ".xhtml", ".xml", ".svg",
		".vue", ".svelte", ".astro", ".njk", ".hbs", ".ejs",
		".jsp", ".erb", ".blade.php",
	)
}

func (d *HTMLDetector) Name() string { return "html" }

var (
	// Match opening tags: <tagname ...> (we filter self-closing separately)
	htmlOpenTagRe   = regexp.MustCompile(`<(\w[\w-]*)[^>]*>`)
	htmlCloseTagRe  = regexp.MustCompile(`</(\w[\w-]*)>`)
	htmlSelfCloseRe = regexp.MustCompile(`<\w[\w-]*[^>]*/\s*>`)
)

// Void elements that never have children
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

func (d *HTMLDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	type stackEntry struct {
		tag string
	}
	var stack []stackEntry

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Process closing tags first
		closeMatches := htmlCloseTagRe.FindAllStringSubmatch(trimmed, -1)
		for _, m := range closeMatches {
			tag := strings.ToLower(m[1])
			// Pop stack up to matching tag
			for j := len(stack) - 1; j >= 0; j-- {
				if stack[j].tag == tag {
					stack = stack[:j]
					break
				}
			}
		}

		// Build chain from current stack
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.tag}
		}
		result[i] = chain

		// Process opening tags (skip self-closing and void elements)
		// Strategy: remove self-closing tags and closing tags first,
		// then match remaining open tags.
		cleaned := htmlSelfCloseRe.ReplaceAllString(trimmed, "")
		cleaned = htmlCloseTagRe.ReplaceAllString(cleaned, "")

		openMatches := htmlOpenTagRe.FindAllStringSubmatch(cleaned, -1)
		for _, m := range openMatches {
			fullMatch := m[0]
			tag := strings.ToLower(m[1])
			// Skip if this is actually self-closing (ends with />)
			if strings.HasSuffix(strings.TrimSpace(fullMatch), "/>") {
				continue
			}
			if !voidElements[tag] {
				stack = append(stack, stackEntry{tag: tag})
			}
		}
	}

	return result
}
