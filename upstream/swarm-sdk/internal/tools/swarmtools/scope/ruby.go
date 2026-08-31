package scope

import (
	"regexp"
	"strings"
)

// RubyDetector detects scope boundaries in Ruby source files.
// Ruby uses keyword-based blocks (def/class/module/if/do...end), not braces.
type RubyDetector struct{}

func init() {
	Register(&RubyDetector{}, ".rb", ".rake", ".gemspec")
}

func (d *RubyDetector) Name() string { return "ruby" }

var rubyPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// module
	{
		re:    regexp.MustCompile(`^\s*module\s+(\w+)`),
		label: func(m []string) string { return "module " + m[1] },
	},
	// class
	{
		re:    regexp.MustCompile(`^\s*class\s+(\w+)`),
		label: func(m []string) string { return "class " + m[1] },
	},
	// def
	{
		re:    regexp.MustCompile(`^\s*def\s+(?:self\.)?(\w+[?!=]?)`),
		label: func(m []string) string { return "def " + m[1] },
	},
	// if / unless
	{
		re:    regexp.MustCompile(`^\s*(?:els)?if\s+`),
		label: func(m []string) string { return "if" },
	},
	{
		re:    regexp.MustCompile(`^\s*unless\s+`),
		label: func(m []string) string { return "unless" },
	},
	// else
	{
		re:    regexp.MustCompile(`^\s*else\s*$`),
		label: func(m []string) string { return "else" },
	},
	// case / when
	{
		re:    regexp.MustCompile(`^\s*case\s`),
		label: func(m []string) string { return "case" },
	},
	// do block
	{
		re:    regexp.MustCompile(`\bdo\s*(?:\|[^|]*\|)?\s*$`),
		label: func(m []string) string { return "do" },
	},
	// while / until / for
	{
		re:    regexp.MustCompile(`^\s*while\s+`),
		label: func(m []string) string { return "while" },
	},
	{
		re:    regexp.MustCompile(`^\s*until\s+`),
		label: func(m []string) string { return "until" },
	},
	{
		re:    regexp.MustCompile(`^\s*for\s+`),
		label: func(m []string) string { return "for" },
	},
	// begin/rescue/ensure
	{
		re:    regexp.MustCompile(`^\s*begin\s*$`),
		label: func(m []string) string { return "begin" },
	},
	{
		re:    regexp.MustCompile(`^\s*rescue\s*`),
		label: func(m []string) string { return "rescue" },
	},
	{
		re:    regexp.MustCompile(`^\s*ensure\s*$`),
		label: func(m []string) string { return "ensure" },
	},
}

func (d *RubyDetector) DetectScopes(lines []string) []ScopeChain {
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

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if i > 0 {
				result[i] = copyChain(result[i-1])
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		indent := measureIndent(line)

		// "end" closes a scope
		if trimmed == "end" {
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		// Pop scopes at deeper or equal indent
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		label := d.matchLabel(line)
		if label != "" {
			stack = append(stack, scopeLevel{indent: indent, label: label})
		}
	}

	return result
}

func (d *RubyDetector) matchLabel(line string) string {
	for _, p := range rubyPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
