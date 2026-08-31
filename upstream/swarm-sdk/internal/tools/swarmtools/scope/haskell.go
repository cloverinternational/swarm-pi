package scope

import (
	"regexp"
	"strings"
)

// HaskellDetector detects scope boundaries in Haskell source files.
// Uses indentation-based scoping with Haskell-specific constructs:
// module, data, class, instance, where blocks, do blocks, let/in, case/of.
type HaskellDetector struct{}

func init() {
	Register(&HaskellDetector{}, ".hs", ".lhs")
}

func (d *HaskellDetector) Name() string { return "haskell" }

var haskellPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// module
	{
		re:    regexp.MustCompile(`^\s*module\s+([\w.]+)`),
		label: func(m []string) string { return "module " + m[1] },
	},
	// data / newtype / type
	{
		re:    regexp.MustCompile(`^\s*(?:data|newtype)\s+(\w+)`),
		label: func(m []string) string { return "data " + m[1] },
	},
	{
		re:    regexp.MustCompile(`^\s*type\s+(\w+)`),
		label: func(m []string) string { return "type " + m[1] },
	},
	// class
	{
		re:    regexp.MustCompile(`^\s*class\s+.*?(\w+)\s+where`),
		label: func(m []string) string { return "class " + m[1] },
	},
	// instance
	{
		re: regexp.MustCompile(`^\s*instance\s+(.+?)\s+where`),
		label: func(m []string) string {
			inst := m[1]
			if len(inst) > 40 {
				inst = inst[:40]
			}
			return "instance " + inst
		},
	},
	// top-level function (name at column 0 followed by arguments or =)
	{
		re:    regexp.MustCompile(`^(\w+)\s+.*=`),
		label: func(m []string) string { return "func " + m[1] },
	},
	// where block
	{
		re:    regexp.MustCompile(`\bwhere\s*$`),
		label: func(m []string) string { return "where" },
	},
	// do block
	{
		re:    regexp.MustCompile(`\bdo\s*$`),
		label: func(m []string) string { return "do" },
	},
	// case ... of
	{
		re:    regexp.MustCompile(`^\s*case\s+`),
		label: func(m []string) string { return "case" },
	},
}

func (d *HaskellDetector) DetectScopes(lines []string) []ScopeChain {
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

		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			if i > 0 {
				result[i] = copyChain(result[i-1])
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		indent := measureIndent(line)

		// Pop scopes at deeper or equal indent
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// Check if this line opens a scope (next line is indented more)
		label := d.matchLabel(line)
		if label != "" {
			if nextIndent, found := nextNonEmptyIndent(lines, i+1); found && nextIndent > indent {
				stack = append(stack, scopeLevel{indent: indent, label: label})
			}
		}
	}

	return result
}

func (d *HaskellDetector) matchLabel(line string) string {
	for _, p := range haskellPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
