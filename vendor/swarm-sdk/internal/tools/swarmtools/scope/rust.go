package scope

import (
	"regexp"
	"strings"
)

// RustDetector detects scope boundaries in Rust source files.
// Tracks: fn, impl, struct, enum, trait, if, for, match, loop, mod.
// Uses closing-brace-inherits-closed-scope semantics.
type RustDetector struct{}

func init() {
	Register(&RustDetector{}, ".rs")
}

func (d *RustDetector) Name() string { return "rust" }

var rustPatterns = []struct {
	re    *regexp.Regexp
	label func(match []string) string
}{
	// impl Type or impl Trait for Type
	{
		re:    regexp.MustCompile(`^\s*impl(?:<[^>]*>)?\s+(\w+(?:<[^>]*>)?)\s+for\s+(\w+)`),
		label: func(m []string) string { return "impl " + m[1] + " for " + m[2] },
	},
	{
		re:    regexp.MustCompile(`^\s*impl(?:<[^>]*>)?\s+(\w+)`),
		label: func(m []string) string { return "impl " + m[1] },
	},
	// fn name
	{
		re:    regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+(\w+)`),
		label: func(m []string) string { return "fn " + m[1] },
	},
	// struct / enum / trait
	{
		re:    regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+(\w+)`),
		label: func(m []string) string { return "struct " + m[1] },
	},
	{
		re:    regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+(\w+)`),
		label: func(m []string) string { return "enum " + m[1] },
	},
	{
		re:    regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+(\w+)`),
		label: func(m []string) string { return "trait " + m[1] },
	},
	// mod
	{
		re:    regexp.MustCompile(`^\s*(?:pub\s+)?mod\s+(\w+)`),
		label: func(m []string) string { return "mod " + m[1] },
	},
	// match
	{
		re:    regexp.MustCompile(`^\s*match\s+`),
		label: func(m []string) string { return "match" },
	},
	// if / else if
	{
		re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s+`),
		label: func(m []string) string { return "if" },
	},
	// for
	{
		re:    regexp.MustCompile(`^\s*for\s+`),
		label: func(m []string) string { return "for" },
	},
	// loop
	{
		re:    regexp.MustCompile(`^\s*loop\s*\{`),
		label: func(m []string) string { return "loop" },
	},
	// while
	{
		re:    regexp.MustCompile(`^\s*while\s+`),
		label: func(m []string) string { return "while" },
	},
}

func (d *RustDetector) DetectScopes(lines []string) []ScopeChain {
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
		openBraces, closeBraces := countRustBraces(trimmed)

		isClosingOnly := closeBraces > 0 && openBraces == 0 && isOnlyClosing(trimmed)

		if isClosingOnly {
			chain := make(ScopeChain, len(stack))
			for j, s := range stack {
				chain[j] = ScopeEntry{Label: s.label}
			}
			result[i] = chain

			for range closeBraces {
				braceDepth--
				for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
					stack = stack[:len(stack)-1]
				}
			}
			continue
		}

		for range closeBraces {
			braceDepth--
			for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
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

func (d *RustDetector) matchLabel(trimmed string) string {
	for _, p := range rustPatterns {
		if m := p.re.FindStringSubmatch(trimmed); m != nil {
			return p.label(m)
		}
	}
	return ""
}

func countRustBraces(line string) (open, close int) {
	inString := false
	inChar := false
	inRawString := false
	escaped := false
	inLineComment := false

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && (inString || inChar) {
			escaped = true
			continue
		}
		if inLineComment {
			break
		}
		if inRawString {
			if ch == '"' {
				inRawString = false
			}
			continue
		}
		if ch == '/' && i+1 < len(line) && line[i+1] == '/' && !inString && !inChar {
			inLineComment = true
			continue
		}
		// Rust raw strings: r#"..."#  (simplified — just track "r followed by ")
		if ch == 'r' && i+1 < len(line) && line[i+1] == '"' && !inString && !inChar {
			inRawString = true
			i++ // skip the "
			continue
		}
		if ch == '"' && !inChar {
			inString = !inString
			continue
		}
		if ch == '\'' && !inString {
			inChar = !inChar
			continue
		}
		if !inString && !inChar {
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
