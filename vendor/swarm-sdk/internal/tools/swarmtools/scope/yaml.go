package scope

import (
	"regexp"
	"strings"
)

// YAMLDetector detects scope boundaries in YAML and TOML files.
// Uses indentation for YAML, [section] headers for TOML.
type YAMLDetector struct{}

func init() {
	Register(&YAMLDetector{}, ".yaml", ".yml")
}

func (d *YAMLDetector) Name() string { return "yaml" }

var yamlKeyRe = regexp.MustCompile(`^(\s*)(\w[\w.-]*)\s*:`)

func (d *YAMLDetector) DetectScopes(lines []string) []ScopeChain {
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

		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || trimmed == "..." {
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

		// Build chain
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// Check if this is a key that opens a nested scope
		if m := yamlKeyRe.FindStringSubmatch(line); m != nil {
			key := m[2]
			// Only push scope if the next non-empty line is indented more
			if nextIndent, found := nextNonEmptyIndent(lines, i+1); found && nextIndent > indent {
				stack = append(stack, scopeLevel{indent: indent, label: key})
			}
		}
	}

	return result
}

// TOMLDetector detects scope boundaries in TOML files.
type TOMLDetector struct{}

func init() {
	Register(&TOMLDetector{}, ".toml")
}

func (d *TOMLDetector) Name() string { return "toml" }

var tomlSectionRe = regexp.MustCompile(`^\s*\[+([^\]]+)\]+`)

func (d *TOMLDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	var currentSection string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if currentSection != "" {
				result[i] = ScopeChain{{Label: currentSection}}
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		// Section header
		if m := tomlSectionRe.FindStringSubmatch(trimmed); m != nil {
			currentSection = m[1]
			result[i] = ScopeChain{} // Section header is at top level
			continue
		}

		if currentSection != "" {
			result[i] = ScopeChain{{Label: currentSection}}
		} else {
			result[i] = ScopeChain{}
		}
	}

	return result
}
