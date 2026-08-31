package scope

import (
	"regexp"
	"strings"
)

// PatternRule defines a scope-opening pattern for a language.
// Each language provides its own list of rules.
type PatternRule struct {
	Re    *regexp.Regexp
	Label func(match []string) string
}

// BraceConfig configures a brace-based scope detector for a specific language.
type BraceConfig struct {
	// Name is the detector name (e.g. "c", "java").
	LangName string
	// Patterns are the scope-opening patterns, checked in order.
	Patterns []PatternRule
	// CountBraces is the language-specific brace counter.
	// Must handle strings, comments, etc. for the language.
	// If nil, defaults to CStyleBraceCounter.
	CountBraces func(line string) (open, close int)
}

// BraceDetector is a reusable scope detector for brace-delimited languages.
// All C-family languages (C, C++, Java, C#, Go, JS, Rust, etc.) share the
// same core logic: track { and }, push scope on net-open, pop on close,
// closing-only lines inherit the scope they close.
//
// Each language only needs to provide:
//   - Pattern rules for recognizing scope-opening constructs
//   - A brace counter that handles that language's string/comment syntax
type BraceDetector struct {
	config BraceConfig
}

// NewBraceDetector creates a BraceDetector with the given config.
func NewBraceDetector(config BraceConfig) *BraceDetector {
	if config.CountBraces == nil {
		config.CountBraces = CStyleBraceCounter
	}
	return &BraceDetector{config: config}
}

func (d *BraceDetector) Name() string { return d.config.LangName }

func (d *BraceDetector) DetectScopes(lines []string) []ScopeChain {
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
		openBraces, closeBraces := d.config.CountBraces(trimmed)

		closingOnly := closeBraces > 0 && openBraces == 0 && isOnlyClosing(trimmed)

		if closingOnly {
			// Assign current chain — includes the scope being closed
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

		// Pop for closing braces first
		for range closeBraces {
			braceDepth--
			for len(stack) > 0 && stack[len(stack)-1].braceDepth >= braceDepth {
				stack = stack[:len(stack)-1]
			}
		}

		// Build chain
		chain := make(ScopeChain, len(stack))
		for j, s := range stack {
			chain[j] = ScopeEntry{Label: s.label}
		}
		result[i] = chain

		// Push new scope if net-opening
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

func (d *BraceDetector) matchLabel(trimmed string) string {
	for _, p := range d.config.Patterns {
		if m := p.Re.FindStringSubmatch(trimmed); m != nil {
			return p.Label(m)
		}
	}
	return ""
}

// --- Shared brace counters ---

// CStyleBraceCounter counts { and } with C-style string/comment handling.
// Works for C, C++, Java, C#, and similar languages.
func CStyleBraceCounter(line string) (open, close int) {
	inString := false
	inChar := false
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
		if ch == '/' && i+1 < len(line) && line[i+1] == '/' && !inString && !inChar {
			inLineComment = true
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
