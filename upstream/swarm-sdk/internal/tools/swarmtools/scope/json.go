package scope

import (
	"regexp"
	"strings"
)

// JSONDetector detects scope boundaries in JSON and JSONC files.
// Uses brace/bracket nesting with key names as scope labels.
// Since JSON is purely structural (no functions/classes), the scope
// chain tracks nested object keys.
type JSONDetector struct{}

func init() {
	Register(&JSONDetector{}, ".json", ".jsonc", ".json5", ".jsonl")
}

func (d *JSONDetector) Name() string { return "json" }

var jsonKeyRe = regexp.MustCompile(`^\s*"([^"]+)"\s*:\s*[\[{]`)

func (d *JSONDetector) DetectScopes(lines []string) []ScopeChain {
	result := make([]ScopeChain, len(lines))
	if len(lines) == 0 {
		return result
	}

	type stackEntry struct {
		braceDepth int
		label      string
	}
	var stack []stackEntry
	depth := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		openBraces := strings.Count(trimmed, "{") + strings.Count(trimmed, "[")
		closeBraces := strings.Count(trimmed, "}") + strings.Count(trimmed, "]")

		closingOnly := closeBraces > 0 && openBraces == 0 && isJSONClosing(trimmed)

		if closingOnly {
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			for range closeBraces {
				depth--
				for len(stack) > 0 && stack[len(stack)-1].braceDepth >= depth {
					stack = stack[:len(stack)-1]
				}
			}
			continue
		}

		// Pop for closing
		for range closeBraces {
			depth--
			for len(stack) > 0 && stack[len(stack)-1].braceDepth >= depth {
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
			if m := jsonKeyRe.FindStringSubmatch(trimmed); m != nil {
				stack = append(stack, stackEntry{
					braceDepth: depth,
					label:      m[1],
				})
			}
			depth += netOpen
		}
	}

	return result
}

func isJSONClosing(trimmed string) bool {
	for _, ch := range trimmed {
		switch ch {
		case '}', ']', ',', ' ', '\t':
			continue
		default:
			return false
		}
	}
	return true
}
