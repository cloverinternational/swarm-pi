package scope

import (
	"regexp"
	"strings"
)

// ElixirDetector detects scope boundaries in Elixir source files.
// Elixir uses keyword blocks (def/defmodule/if/case...end).
type ElixirDetector struct{}

func init() {
	Register(&ElixirDetector{}, ".ex", ".exs")
}

func (d *ElixirDetector) Name() string { return "elixir" }

var elixirPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// defmodule
	{
		re:    regexp.MustCompile(`^\s*defmodule\s+([\w.]+)`),
		label: func(m []string) string { return "module " + m[1] },
	},
	// defprotocol
	{
		re:    regexp.MustCompile(`^\s*defprotocol\s+([\w.]+)`),
		label: func(m []string) string { return "protocol " + m[1] },
	},
	// defimpl
	{
		re:    regexp.MustCompile(`^\s*defimpl\s+([\w.]+)`),
		label: func(m []string) string { return "impl " + m[1] },
	},
	// def / defp
	{
		re:    regexp.MustCompile(`^\s*defp?\s+(\w+[!?]?)`),
		label: func(m []string) string { return "def " + m[1] },
	},
	// defmacro / defmacrop
	{
		re:    regexp.MustCompile(`^\s*defmacrop?\s+(\w+[!?]?)`),
		label: func(m []string) string { return "defmacro " + m[1] },
	},
	// if / unless
	{
		re:    regexp.MustCompile(`^\s*if\s+`),
		label: func(m []string) string { return "if" },
	},
	{
		re:    regexp.MustCompile(`^\s*unless\s+`),
		label: func(m []string) string { return "unless" },
	},
	// case
	{
		re:    regexp.MustCompile(`^\s*case\s+`),
		label: func(m []string) string { return "case" },
	},
	// cond
	{
		re:    regexp.MustCompile(`^\s*cond\s+do`),
		label: func(m []string) string { return "cond" },
	},
	// for (comprehension)
	{
		re:    regexp.MustCompile(`^\s*for\s+`),
		label: func(m []string) string { return "for" },
	},
	// with
	{
		re:    regexp.MustCompile(`^\s*with\s+`),
		label: func(m []string) string { return "with" },
	},
	// try / receive
	{
		re:    regexp.MustCompile(`^\s*try\s+do`),
		label: func(m []string) string { return "try" },
	},
	{
		re:    regexp.MustCompile(`^\s*receive\s+do`),
		label: func(m []string) string { return "receive" },
	},
}

func (d *ElixirDetector) DetectScopes(lines []string) []ScopeChain {
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
		if trimmed == "end" || trimmed == "end)" {
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

func (d *ElixirDetector) matchLabel(line string) string {
	for _, p := range elixirPatterns {
		if m := p.re.FindStringSubmatch(line); m != nil {
			return p.label(m)
		}
	}
	return ""
}
