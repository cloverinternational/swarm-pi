package scope

import "regexp"

// KotlinDetector detects scope boundaries in Kotlin source files.
type KotlinDetector struct {
	*BraceDetector
}

func init() {
	det := &KotlinDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "kotlin",
			Patterns:    kotlinPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".kt", ".kts")
}

var kotlinPatterns = []PatternRule{
	// class / object / interface / enum
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:open\s+|abstract\s+|sealed\s+|data\s+|inner\s+)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:companion\s+)?object\s+(\w+)`),
		Label: func(m []string) string { return "object " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*interface\s+(\w+)`),
		Label: func(m []string) string { return "interface " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*enum\s+class\s+(\w+)`),
		Label: func(m []string) string { return "enum " + m[1] },
	},
	// fun
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:open\s+|override\s+|abstract\s+)?(?:suspend\s+)?fun\s+(?:<[^>]*>\s*)?(?:\w+\.)?(\w+)`),
		Label: func(m []string) string { return "fun " + m[1] },
	},
	// init block
	{
		Re:    regexp.MustCompile(`^\s*init\s*\{`),
		Label: func(m []string) string { return "init" },
	},
	// if / when
	{
		Re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s*\(`),
		Label: func(m []string) string { return "if" },
	},
	{
		Re:    regexp.MustCompile(`^\s*when\s*[\({]`),
		Label: func(m []string) string { return "when" },
	},
	// else
	{
		Re:    regexp.MustCompile(`^\s*else\s*\{`),
		Label: func(m []string) string { return "else" },
	},
	// for / while
	{
		Re:    regexp.MustCompile(`^\s*for\s*\(`),
		Label: func(m []string) string { return "for" },
	},
	{
		Re:    regexp.MustCompile(`^\s*while\s*\(`),
		Label: func(m []string) string { return "while" },
	},
	// try / catch / finally
	{
		Re:    regexp.MustCompile(`^\s*try\s*\{`),
		Label: func(m []string) string { return "try" },
	},
	{
		Re:    regexp.MustCompile(`^\s*catch\s*\(`),
		Label: func(m []string) string { return "catch" },
	},
	{
		Re:    regexp.MustCompile(`^\s*finally\s*\{`),
		Label: func(m []string) string { return "finally" },
	},
}
