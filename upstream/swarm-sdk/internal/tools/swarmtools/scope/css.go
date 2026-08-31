package scope

import (
	"regexp"
	"strings"
)

// CSSDetector detects scope boundaries in CSS, SCSS, LESS, and Sass files.
// Tracks: selectors, @media, @keyframes, @mixin, @supports, and nesting.
type CSSDetector struct{}

func init() {
	Register(&CSSDetector{}, ".css", ".scss", ".less", ".sass")
}

func (d *CSSDetector) Name() string { return "css" }

var cssPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// @media
	{
		re: regexp.MustCompile(`^\s*@media\s+(.+?)\s*\{`),
		label: func(m []string) string {
			q := m[1]
			if len(q) > 40 {
				q = q[:40]
			}
			return "@media " + q
		},
	},
	// @keyframes
	{
		re:    regexp.MustCompile(`^\s*@keyframes\s+(\w[\w-]*)`),
		label: func(m []string) string { return "@keyframes " + m[1] },
	},
	// @mixin (SCSS)
	{
		re:    regexp.MustCompile(`^\s*@mixin\s+(\w[\w-]*)`),
		label: func(m []string) string { return "@mixin " + m[1] },
	},
	// @supports
	{
		re:    regexp.MustCompile(`^\s*@supports\s+`),
		label: func(m []string) string { return "@supports" },
	},
	// @layer
	{
		re:    regexp.MustCompile(`^\s*@layer\s+(\w[\w-]*)`),
		label: func(m []string) string { return "@layer " + m[1] },
	},
	// @container
	{
		re:    regexp.MustCompile(`^\s*@container\s+`),
		label: func(m []string) string { return "@container" },
	},
	// General selector (anything ending with {)
	{
		re: regexp.MustCompile(`^\s*([.#&:\w][\w\s>+~,.:*#&\[\]="-]*?)\s*\{`),
		label: func(m []string) string {
			sel := strings.TrimSpace(m[1])
			if len(sel) > 50 {
				sel = sel[:50]
			}
			return sel
		},
	},
}

func (d *CSSDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	type stackEntry struct {
		braceDepth int
		label      string
	}
	var stack []stackEntry
	braceDepth := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		open := strings.Count(trimmed, "{")
		close := strings.Count(trimmed, "}")

		closingOnly := close > 0 && open == 0 && isOnlyClosing(trimmed)

		if closingOnly {
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			for range close {
				braceDepth--
				for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
					stack = stack[:len(stack)-1]
				}
			}
			continue
		}

		for range close {
			braceDepth--
			for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
				stack = stack[:len(stack)-1]
			}
		}

		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		netOpen := open - close
		if netOpen > 0 {
			label := d.matchLabel(trimmed)
			if label != "" {
				stack = append(stack, stackEntry{
					braceDepth: braceDepth,
					label:      label,
				})
			}
			braceDepth += netOpen
		}
	}

	return result
}

func (d *CSSDetector) matchLabel(trimmed string) string {
	for _, p := range cssPatterns {
		if m := p.re.FindStringSubmatch(trimmed); m != nil {
			return p.label(m)
		}
	}
	return ""
}
