package scope

import (
	"regexp"
	"strings"
)

// JSDetector detects scope boundaries in JavaScript and TypeScript files.
// Tracks: function, class, method, if, for, while, switch, arrow functions.
// Uses brace counting with closing-brace-inherits-closed-scope semantics.
type JSDetector struct{}

func init() {
	Register(&JSDetector{},
		".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts",
	)
}

func (d *JSDetector) Name() string { return "javascript" }

var jsPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// class Name
	{
		re:    regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?class\s+(\w+)`),
		label: func(m []string) string { return "class " + m[1] },
	},
	// function name(
	{
		re:    regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*(\w+)`),
		label: func(m []string) string { return "function " + m[1] },
	},
	// method name( — inside a class
	{
		re:    regexp.MustCompile(`^\s*(?:async\s+)?(\w+)\s*\(`),
		label: func(m []string) string { return "method " + m[1] },
	},
	// const/let/var name = ... => {  or function expression
	{
		re:    regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+(\w+)\s*=`),
		label: func(m []string) string { return m[1] },
	},
	// if (
	{
		re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s*\(`),
		label: func(m []string) string { return "if" },
	},
	// else {
	{
		re:    regexp.MustCompile(`^\s*else\s*\{`),
		label: func(m []string) string { return "else" },
	},
	// for / for...of / for...in
	{
		re:    regexp.MustCompile(`^\s*for\s*\(`),
		label: func(m []string) string { return "for" },
	},
	// while
	{
		re:    regexp.MustCompile(`^\s*while\s*\(`),
		label: func(m []string) string { return "while" },
	},
	// switch
	{
		re:    regexp.MustCompile(`^\s*switch\s*\(`),
		label: func(m []string) string { return "switch" },
	},
	// try
	{
		re:    regexp.MustCompile(`^\s*try\s*\{`),
		label: func(m []string) string { return "try" },
	},
	// catch
	{
		re:    regexp.MustCompile(`^\s*catch\s*`),
		label: func(m []string) string { return "catch" },
	},
}

func (d *JSDetector) DetectScopes(lines []string) []ScopeChain {
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
		openBraces, closeBraces := countJSBraces(trimmed)

		isClosingOnly := closeBraces > 0 && openBraces == 0 && isOnlyClosing(trimmed)

		if isClosingOnly {
			// Assign current chain (includes scope being closed)
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			for range closeBraces {
				braceDepth--
				for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
					stack = stack[:len(stack)-1]
				}
			}
			continue
		}

		// Pop first for non-closing-only lines
		for range closeBraces {
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

		netOpen := openBraces - closeBraces
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

func (d *JSDetector) matchLabel(trimmed string) string {
	for _, p := range jsPatterns {
		if m := p.re.FindStringSubmatch(trimmed); m != nil {
			return p.label(m)
		}
	}
	return ""
}

// isOnlyClosing checks if a trimmed line is only closing characters.
func isOnlyClosing(trimmed string) bool {
	for _, ch := range trimmed {
		if ch != '}' && ch != ')' && ch != ']' && ch != ',' && ch != ';' && ch != ' ' && ch != '\t' {
			return false
		}
	}
	return true
}

// countJSBraces counts { and } with basic string/comment/template literal awareness.
func countJSBraces(line string) (open, close int) {
	inSingle := false
	inDouble := false
	inTemplate := false
	escaped := false
	inLineComment := false

	for i := 0; i < len(line); i++ {
		ch := line[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && (inSingle || inDouble || inTemplate) {
			escaped = true
			continue
		}

		if inLineComment {
			break
		}

		if ch == '/' && i+1 < len(line) && line[i+1] == '/' && !inSingle && !inDouble && !inTemplate {
			inLineComment = true
			continue
		}

		if ch == '\'' && !inDouble && !inTemplate {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle && !inTemplate {
			inDouble = !inDouble
			continue
		}
		if ch == '`' && !inSingle && !inDouble {
			inTemplate = !inTemplate
			continue
		}

		if !inSingle && !inDouble && !inTemplate {
			switch ch {
			case '{':
				open++
			case '}':
				close++
			}
		}
	}
	return open, close
}
