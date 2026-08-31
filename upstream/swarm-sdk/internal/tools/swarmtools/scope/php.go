package scope

import "regexp"

// PHPDetector detects scope boundaries in PHP source files.
type PHPDetector struct {
	*BraceDetector
}

func init() {
	det := &PHPDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "php",
			Patterns:    phpPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".php", ".phtml")
}

var phpPatterns = []PatternRule{
	// namespace
	{
		Re:    regexp.MustCompile(`^\s*namespace\s+([\w\\]+)`),
		Label: func(m []string) string { return "namespace " + m[1] },
	},
	// class / interface / trait / enum
	{
		Re:    regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*interface\s+(\w+)`),
		Label: func(m []string) string { return "interface " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*trait\s+(\w+)`),
		Label: func(m []string) string { return "trait " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*enum\s+(\w+)`),
		Label: func(m []string) string { return "enum " + m[1] },
	},
	// function / method
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?(?:static\s+)?function\s+(\w+)`),
		Label: func(m []string) string { return "function " + m[1] },
	},
	// if
	{
		Re:    regexp.MustCompile(`^\s*(?:else\s*)?if\s*\(`),
		Label: func(m []string) string { return "if" },
	},
	// else
	{
		Re:    regexp.MustCompile(`^\s*else\s*\{`),
		Label: func(m []string) string { return "else" },
	},
	// for / foreach
	{
		Re:    regexp.MustCompile(`^\s*for(?:each)?\s*\(`),
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
