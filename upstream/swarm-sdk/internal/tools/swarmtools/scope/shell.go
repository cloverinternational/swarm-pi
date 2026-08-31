package scope

import (
	"regexp"
	"strings"
)

// ShellDetector detects scope boundaries in Shell/Bash scripts.
// Tracks: functions, if/fi, for/done, while/done, case/esac, {}.
type ShellDetector struct{}

func init() {
	Register(&ShellDetector{}, ".sh", ".bash", ".zsh", ".fish", ".ksh")
}

func (d *ShellDetector) Name() string { return "shell" }

var shellPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// function name() {  or  name() {
	{
		re:    regexp.MustCompile(`^\s*(?:function\s+)?(\w+)\s*\(\)\s*\{`),
		label: func(m []string) string { return "func " + m[1] },
	},
	// function name {
	{
		re:    regexp.MustCompile(`^\s*function\s+(\w+)\s*\{`),
		label: func(m []string) string { return "func " + m[1] },
	},
	// if
	{
		re:    regexp.MustCompile(`^\s*(?:el)?if\s`),
		label: func(m []string) string { return "if" },
	},
	// else
	{
		re:    regexp.MustCompile(`^\s*else\s*$`),
		label: func(m []string) string { return "else" },
	},
	// for
	{
		re:    regexp.MustCompile(`^\s*for\s`),
		label: func(m []string) string { return "for" },
	},
	// while / until
	{
		re:    regexp.MustCompile(`^\s*while\s`),
		label: func(m []string) string { return "while" },
	},
	{
		re:    regexp.MustCompile(`^\s*until\s`),
		label: func(m []string) string { return "until" },
	},
	// case
	{
		re:    regexp.MustCompile(`^\s*case\s`),
		label: func(m []string) string { return "case" },
	},
	// select
	{
		re:    regexp.MustCompile(`^\s*select\s`),
		label: func(m []string) string { return "select" },
	},
}

// Shell block-closing keywords
var shellClosers = map[string]bool{
	"fi": true, "done": true, "esac": true,
}

func (d *ShellDetector) DetectScopes(lines []string) []ScopeChain {
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

		// Block closers
		firstWord := strings.Fields(trimmed)[0]
		if shellClosers[firstWord] || trimmed == "}" {
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

func (d *ShellDetector) matchLabel(line string) string {
	for _, p := range shellPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
