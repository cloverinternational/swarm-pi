package scope

import "regexp"

// DartDetector detects scope boundaries in Dart source files.
// Uses BraceDetector base with Dart-specific patterns.
type DartDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "dart",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// class / abstract class / mixin
			{
				Re:    regexp.MustCompile(`^\s*(?:abstract\s+)?class\s+(\w+)`),
				Label: func(m []string) string { return "class " + m[1] },
			},
			// mixin
			{
				Re:    regexp.MustCompile(`^\s*mixin\s+(\w+)`),
				Label: func(m []string) string { return "mixin " + m[1] },
			},
			// extension
			{
				Re:    regexp.MustCompile(`^\s*extension\s+(\w+)`),
				Label: func(m []string) string { return "extension " + m[1] },
			},
			// enum
			{
				Re:    regexp.MustCompile(`^\s*enum\s+(\w+)`),
				Label: func(m []string) string { return "enum " + m[1] },
			},
			// method / function
			{
				Re:    regexp.MustCompile(`^\s*(?:static\s+)?(?:Future|Stream|void|int|double|String|bool|List|Map|Set|dynamic|\w+(?:<[^>]+>)?)\s+(\w+)\s*\(`),
				Label: func(m []string) string { return "func " + m[1] },
			},
			// if
			{
				Re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s*\(`),
				Label: func(m []string) string { return "if" },
			},
			// else
			{
				Re:    regexp.MustCompile(`^\s*(?:}\s*)?else\s*\{`),
				Label: func(m []string) string { return "else" },
			},
			// for
			{
				Re:    regexp.MustCompile(`^\s*for\s*\(`),
				Label: func(m []string) string { return "for" },
			},
			// while
			{
				Re:    regexp.MustCompile(`^\s*while\s*\(`),
				Label: func(m []string) string { return "while" },
			},
			// switch
			{
				Re:    regexp.MustCompile(`^\s*switch\s*\(`),
				Label: func(m []string) string { return "switch" },
			},
			// try
			{
				Re:    regexp.MustCompile(`^\s*try\s*\{`),
				Label: func(m []string) string { return "try" },
			},
			// catch
			{
				Re:    regexp.MustCompile(`^\s*(?:on\s+\w+\s+)?catch\s*\(`),
				Label: func(m []string) string { return "catch" },
			},
		},
	})
	Register(det, ".dart")
}
