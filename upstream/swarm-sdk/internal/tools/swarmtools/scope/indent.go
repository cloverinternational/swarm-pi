package scope

import (
	"strings"
)

// IndentDetector is the language-agnostic fallback scope detector.
// It uses indentation level changes to infer scope boundaries.
// Works surprisingly well for any language with consistent indentation.
//
// The logic: when indentation increases, the previous line opened a new scope.
// When indentation decreases, we pop back to the matching scope level.
type IndentDetector struct{}

func init() {
	SetFallback(&IndentDetector{})
}

func (d *IndentDetector) Name() string { return "indent" }

func (d *IndentDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	// scopeStack tracks (indentLevel, label) pairs
	type scopeLevel struct {
		indent int
		label  string
	}
	var stack []scopeLevel

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip empty lines — inherit scope from above
		if trimmed == "" {
			if i > 0 {
				result[i] = copyChain(result[i-1])
			} else {
				result[i] = ScopeChain{}
			}
			continue
		}

		indent := measureIndent(line)

		// Pop stack entries that are at or deeper than current indent
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}

		// Build chain from current stack
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// If the next non-empty line is indented more, this line opens a scope
		if nextIndent, found := nextNonEmptyIndent(lines, i+1); found && nextIndent > indent {
			stack = append(stack, scopeLevel{
				indent: indent,
				label:  normalizeLabel(trimmed),
			})
		}
	}

	return result
}

// measureIndent returns the number of leading whitespace characters,
// counting tabs as 4 spaces for consistency.
func measureIndent(line string) int {
	count := 0
	for _, ch := range line {
		switch ch {
		case ' ':
			count++
		case '\t':
			count += 4
		default:
			return count
		}
	}
	return count
}

// nextNonEmptyIndent finds the indentation of the next non-empty line.
func nextNonEmptyIndent(lines []string, start int) (int, bool) {
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return measureIndent(lines[i]), true
		}
	}
	return 0, false
}

// normalizeLabel creates a short, stable label from a scope-opening line.
// Strips trailing punctuation ({, :, etc.) and truncates for stability.
func normalizeLabel(line string) string {
	// Remove trailing braces, colons, and whitespace
	line = strings.TrimRight(line, " \t{:(")
	// Truncate very long lines (function signatures, etc.)
	if len(line) > 80 {
		line = line[:80]
	}
	return line
}

func copyChain(c ScopeChain) ScopeChain {
	if c == nil {
		return ScopeChain{}
	}
	cp := make(ScopeChain, len(c))
	copy(cp, c)
	return cp
}
