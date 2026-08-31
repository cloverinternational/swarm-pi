package scope

import "regexp"

// CSharpDetector detects scope boundaries in C# source files.
type CSharpDetector struct {
	*BraceDetector
}

func init() {
	det := &CSharpDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "csharp",
			Patterns:    csharpPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".cs")
}

var csharpPatterns = []PatternRule{
	// namespace
	{
		Re:    regexp.MustCompile(`^\s*namespace\s+([\w.]+)`),
		Label: func(m []string) string { return "namespace " + m[1] },
	},
	// class / struct / interface / record
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:static\s+)?(?:partial\s+)?(?:abstract\s+)?(?:sealed\s+)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:readonly\s+)?struct\s+(\w+)`),
		Label: func(m []string) string { return "struct " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?interface\s+(\w+)`),
		Label: func(m []string) string { return "interface " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?record\s+(\w+)`),
		Label: func(m []string) string { return "record " + m[1] },
	},
	// enum
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?enum\s+(\w+)`),
		Label: func(m []string) string { return "enum " + m[1] },
	},
	// method / property
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:static\s+)?(?:virtual\s+)?(?:override\s+)?(?:async\s+)?(?:\w+(?:<[^>]*>)?[\[\]?]*\s+)(\w+)\s*\(`),
		Label: func(m []string) string { return "method " + m[1] },
	},
	// property with get/set body
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+|internal\s+)?(?:static\s+)?(?:\w+[\[\]?]*\s+)(\w+)\s*\{`),
		Label: func(m []string) string { return "prop " + m[1] },
	},
	// if
	{
		Re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s*\(`),
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
		Re:    regexp.MustCompile(`^\s*catch\s*`),
		Label: func(m []string) string { return "catch" },
	},
	{
		Re:    regexp.MustCompile(`^\s*finally\s*\{`),
		Label: func(m []string) string { return "finally" },
	},
	// using
	{
		Re:    regexp.MustCompile(`^\s*using\s*\(`),
		Label: func(m []string) string { return "using" },
	},
}
