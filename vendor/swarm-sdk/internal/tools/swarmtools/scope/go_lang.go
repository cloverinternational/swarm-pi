package scope

import (
	"regexp"
	"strings"
)

// GoDetector detects scope boundaries in Go source files.
// Tracks: package, func, type, if, for, switch, select, case.
// Uses brace counting for scope depth.
//
// Key design: closing braces (}) are assigned the scope they CLOSE,
// not the scope they return to. This means "}" closing an "if" inside
// "func Start" gets scope [func Start, if], not just [func Start].
// Combined with ordinal disambiguation, this guarantees every "}" in
// the file has a unique hash.
type GoDetector struct{}

func init() {
	Register(&GoDetector{}, ".go")
}

func (d *GoDetector) Name() string { return "go" }

// Go scope-opening patterns. We extract a stable label from each.
var goPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// func (receiver) Name(...)
	{
		re: regexp.MustCompile(`^func\s+\((\w)\s+\*?(\w+)\)\s+(\w+)`),
		label: func(m []string) string {
			return "func " + m[2] + "." + m[3]
		},
	},
	// func Name(...)
	{
		re:    regexp.MustCompile(`^func\s+(\w+)`),
		label: func(m []string) string { return "func " + m[1] },
	},
	// type Name struct/interface
	{
		re:    regexp.MustCompile(`^type\s+(\w+)\s+(struct|interface)`),
		label: func(m []string) string { return "type " + m[1] },
	},
	// if / else if
	{
		re:    regexp.MustCompile(`^\s*(else\s+)?if\s`),
		label: func(m []string) string { return "if" },
	},
	// else {
	{
		re:    regexp.MustCompile(`^\s*else\s*\{`),
		label: func(m []string) string { return "else" },
	},
	// for
	{
		re:    regexp.MustCompile(`^\s*for\s`),
		label: func(m []string) string { return "for" },
	},
	// switch
	{
		re:    regexp.MustCompile(`^\s*switch\s`),
		label: func(m []string) string { return "switch" },
	},
	// select
	{
		re:    regexp.MustCompile(`^\s*select\s*\{`),
		label: func(m []string) string { return "select" },
	},
	// case / default (inside switch/select)
	{
		re: regexp.MustCompile(`^\s*case\s+(.+):`),
		label: func(m []string) string {
			c := strings.TrimSpace(m[1])
			if len(c) > 40 {
				c = c[:40]
			}
			return "case " + c
		},
	},
	{
		re:    regexp.MustCompile(`^\s*default\s*:`),
		label: func(m []string) string { return "default" },
	},
}

func (d *GoDetector) DetectScopes(lines []string) []ScopeChain {
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

		openBraces, closeBraces := countBraces(trimmed)

		// For lines that ONLY close braces (like "}"), assign the scope
		// that's being closed — BEFORE popping. This means "}" gets the
		// full chain including the scope it terminates.
		isClosingOnly := closeBraces > 0 && openBraces == 0 && isOnlyBraces(trimmed)

		if isClosingOnly {
			// Assign current chain (includes the scope being closed)
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			// Now pop
			for range closeBraces {
				braceDepth--
				for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
					stack = stack[:len(stack)-1]
				}
			}
			continue
		}

		// For all other lines: pop first, then assign
		for range closeBraces {
			braceDepth--
			for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
				stack = stack[:len(stack)-1]
			}
		}

		// Build the scope chain
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// Check if this line opens a new scope
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

// matchLabel checks if a line matches any Go scope pattern and returns the label.
func (d *GoDetector) matchLabel(trimmed string) string {
	for _, p := range goPatterns {
		if m := p.re.FindStringSubmatch(trimmed); m != nil {
			return p.label(m)
		}
	}
	return ""
}

// isOnlyBraces checks if a trimmed line contains only closing braces,
// optional commas, and whitespace (e.g. "}", "},", "})")
func isOnlyBraces(trimmed string) bool {
	for _, ch := range trimmed {
		if ch != '}' && ch != ')' && ch != ',' && ch != ' ' && ch != '\t' && ch != ';' {
			return false
		}
	}
	return true
}

// countBraces counts { and } in a line, with basic string/comment awareness.
func countBraces(line string) (open, close int) {
	inString := false
	inRune := false
	inRawString := false
	escaped := false
	inLineComment := false

	for i := 0; i < len(line); i++ {
		ch := line[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && (inString || inRune) {
			escaped = true
			continue
		}

		if inLineComment {
			break
		}

		if inRawString {
			if ch == '`' {
				inRawString = false
			}
			continue
		}

		// Check for line comment
		if ch == '/' && i+1 < len(line) && line[i+1] == '/' && !inString && !inRune {
			inLineComment = true
			continue
		}

		// Raw string
		if ch == '`' && !inString && !inRune {
			inRawString = true
			continue
		}

		if ch == '"' && !inRune {
			inString = !inString
			continue
		}

		if ch == '\'' && !inString {
			inRune = !inRune
			continue
		}

		if !inString && !inRune {
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
