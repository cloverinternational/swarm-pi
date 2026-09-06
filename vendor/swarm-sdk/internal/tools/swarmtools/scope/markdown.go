package scope

import (
	"regexp"
	"strings"
)

// MarkdownDetector detects scope boundaries in Markdown files.
// Uses heading levels (# through ######) as scope containers.
// Nested headings create nested scopes: ## inside # is a child scope.
// Fenced code blocks (```) are treated as belonging to the current heading scope.
type MarkdownDetector struct{}

func init() {
	Register(&MarkdownDetector{}, ".md", ".markdown", ".mdx", ".rst")
}

func (d *MarkdownDetector) Name() string { return "markdown" }

var (
	mdHeadingRe   = regexp.MustCompile(`^(#{1,6})\s+(.+)`)
	mdFenceOpenRe = regexp.MustCompile("^\\s*(`{3,}|~{3,})")
)

func (d *MarkdownDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	type headingLevel struct {
		level int
		label string
	}
	var stack []headingLevel
	inFence := false
	var fenceMarker string

	buildChain := func() ScopeChain {
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		return chain
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Handle fenced code blocks — they don't change scope
		if inFence {
			// Check for closing fence (must match the opening marker type)
			if strings.HasPrefix(trimmed, fenceMarker) && strings.TrimSpace(strings.TrimLeft(trimmed, fenceMarker[:1])) == "" {
				inFence = false
			}
			result[i] = buildChain()
			continue
		}

		if m := mdFenceOpenRe.FindStringSubmatch(trimmed); m != nil {
			inFence = true
			fenceMarker = m[1]
			result[i] = buildChain()
			continue
		}

		// Check for heading
		if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			title := strings.TrimSpace(m[2])

			// Pop headings at same or deeper level
			for len(stack) > 0 && stack[len(stack)-1].level >= level {
				stack = stack[:len(stack)-1]
			}

			// Heading line is AT the boundary — gets parent scope
			result[i] = buildChain()

			// Push this heading as a new scope container
			label := strings.Repeat("#", level) + " " + title
			if len(label) > 60 {
				label = label[:60]
			}
			stack = append(stack, headingLevel{level: level, label: label})
			continue
		}

		// Regular line — inherits current heading scope
		result[i] = buildChain()
	}

	return result
}
