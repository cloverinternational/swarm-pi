package scope

import "regexp"

// ZigDetector detects scope boundaries in Zig source files.
// Zig uses braces with unique constructs: fn, struct, enum, union,
// test, comptime, inline for, etc.
type ZigDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "zig",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// fn
			{
				Re:    regexp.MustCompile(`^\s*(?:pub\s+)?(?:inline\s+)?fn\s+(\w+)`),
				Label: func(m []string) string { return "fn " + m[1] },
			},
			// struct
			{
				Re:    regexp.MustCompile(`^\s*(?:pub\s+)?const\s+(\w+)\s*=\s*(?:extern\s+|packed\s+)?struct`),
				Label: func(m []string) string { return "struct " + m[1] },
			},
			// enum
			{
				Re:    regexp.MustCompile(`^\s*(?:pub\s+)?const\s+(\w+)\s*=\s*enum`),
				Label: func(m []string) string { return "enum " + m[1] },
			},
			// union
			{
				Re:    regexp.MustCompile(`^\s*(?:pub\s+)?const\s+(\w+)\s*=\s*union`),
				Label: func(m []string) string { return "union " + m[1] },
			},
			// test
			{
				Re:    regexp.MustCompile(`^\s*test\s+"([^"]+)"`),
				Label: func(m []string) string { return "test " + m[1] },
			},
			// if
			{
				Re:    regexp.MustCompile(`^\s*if\s*\(`),
				Label: func(m []string) string { return "if" },
			},
			// else
			{
				Re:    regexp.MustCompile(`\}\s*else\s*\{`),
				Label: func(m []string) string { return "else" },
			},
			// while
			{
				Re:    regexp.MustCompile(`^\s*while\s*\(`),
				Label: func(m []string) string { return "while" },
			},
			// for
			{
				Re:    regexp.MustCompile(`^\s*(?:inline\s+)?for\s*\(`),
				Label: func(m []string) string { return "for" },
			},
			// switch
			{
				Re:    regexp.MustCompile(`^\s*switch\s*\(`),
				Label: func(m []string) string { return "switch" },
			},
			// comptime
			{
				Re:    regexp.MustCompile(`^\s*comptime\s*\{`),
				Label: func(m []string) string { return "comptime" },
			},
		},
	})
	Register(det, ".zig")
}
