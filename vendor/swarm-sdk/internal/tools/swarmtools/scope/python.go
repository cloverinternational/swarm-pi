package scope

import (
	"regexp"
	"strings"
)

// PythonDetector detects scope boundaries in Python source files.
// Uses indentation (Python's natural scope delimiter) combined with
// pattern matching for class, def, if, for, with, try, etc.
type PythonDetector struct{}

func init() {
	Register(&PythonDetector{}, ".py", ".pyw")
}

func (d *PythonDetector) Name() string { return "python" }

var pythonPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	{
		re:    regexp.MustCompile(`^\s*class\s+(\w+)`),
		label: func(m []string) string { return "class " + m[1] },
	},
	{
		re:    regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`),
		label: func(m []string) string { return "def " + m[1] },
	},
	{
		re:    regexp.MustCompile(`^\s*if\s+`),
		label: func(m []string) string { return "if" },
	},
	{
		re:    regexp.MustCompile(`^\s*elif\s+`),
		label: func(m []string) string { return "elif" },
	},
	{
		re:    regexp.MustCompile(`^\s*else\s*:`),
		label: func(m []string) string { return "else" },
	},
	{
		re:    regexp.MustCompile(`^\s*for\s+`),
		label: func(m []string) string { return "for" },
	},
	{
		re:    regexp.MustCompile(`^\s*while\s+`),
		label: func(m []string) string { return "while" },
	},
	{
		re:    regexp.MustCompile(`^\s*with\s+`),
		label: func(m []string) string { return "with" },
	},
	{
		re:    regexp.MustCompile(`^\s*try\s*:`),
		label: func(m []string) string { return "try" },
	},
	{
		re:    regexp.MustCompile(`^\s*except\s*`),
		label: func(m []string) string { return "except" },
	},
	{
		re:    regexp.MustCompile(`^\s*finally\s*:`),
		label: func(m []string) string { return "finally" },
	},
}

func (d *PythonDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	type scopeLevel struct {
		indent int
		label  string
	}
	var stack []scopeLevel

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Empty lines inherit scope from above
		if trimmed == "" {
			if i > 0 {
				result[i] = copyChain(result[i-1])
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		// Comment-only lines inherit scope from above
		if strings.HasPrefix(trimmed, "#") {
			if i > 0 {
				result[i] = copyChain(result[i-1])
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		indent := measureIndent(line)

		// Pop scopes that are at or deeper than current indent
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		// Build chain from current stack
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// Check if this line opens a new scope (ends with : and matches a pattern)
		if strings.HasSuffix(trimmed, ":") || strings.HasSuffix(trimmed, ":\\") {
			label := d.matchLabel(line)
			if label != "" {
				stack = append(stack, scopeLevel{
					indent: indent,
					label:  label,
				})
			}
		}
	}

	return result
}

func (d *PythonDetector) matchLabel(line string) string {
	for _, p := range pythonPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
