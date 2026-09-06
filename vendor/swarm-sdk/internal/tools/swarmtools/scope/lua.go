package scope

import (
	"regexp"
	"strings"
)

// LuaDetector detects scope boundaries in Lua source files.
// Lua uses keyword-based blocks (function/if/for/while...end) not braces,
// so this uses indentation + keyword matching.
type LuaDetector struct{}

func init() {
	Register(&LuaDetector{}, ".lua")
}

func (d *LuaDetector) Name() string { return "lua" }

var luaPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// function name(
	{
		re:    regexp.MustCompile(`^\s*(?:local\s+)?function\s+([.\w:]+)`),
		label: func(m []string) string { return "function " + m[1] },
	},
	// variable = function(
	{
		re:    regexp.MustCompile(`^\s*(?:local\s+)?(\w+)\s*=\s*function\s*\(`),
		label: func(m []string) string { return "function " + m[1] },
	},
	// if
	{
		re:    regexp.MustCompile(`^\s*(?:else)?if\s+`),
		label: func(m []string) string { return "if" },
	},
	// else
	{
		re:    regexp.MustCompile(`^\s*else\s*$`),
		label: func(m []string) string { return "else" },
	},
	// for
	{
		re:    regexp.MustCompile(`^\s*for\s+`),
		label: func(m []string) string { return "for" },
	},
	// while
	{
		re:    regexp.MustCompile(`^\s*while\s+`),
		label: func(m []string) string { return "while" },
	},
	// repeat (Lua's do-while)
	{
		re:    regexp.MustCompile(`^\s*repeat\s*$`),
		label: func(m []string) string { return "repeat" },
	},
	// do
	{
		re:    regexp.MustCompile(`^\s*do\s*$`),
		label: func(m []string) string { return "do" },
	},
}

func (d *LuaDetector) DetectScopes(lines []string) []ScopeChain {
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

		// "end" pops the scope
		if trimmed == "end" || strings.HasPrefix(trimmed, "end)") || strings.HasPrefix(trimmed, "end,") {
			// Assign the scope being closed (before popping)
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

		// Build chain
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// Check if this opens a scope
		label := d.matchLabel(line)
		if label != "" {
			stack = append(stack, scopeLevel{indent: indent, label: label})
		}
	}

	return result
}

func (d *LuaDetector) matchLabel(line string) string {
	for _, p := range luaPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
