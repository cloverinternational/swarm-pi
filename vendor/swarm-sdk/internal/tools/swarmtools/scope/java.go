package scope

import "regexp"

// JavaDetector detects scope boundaries in Java source files.
type JavaDetector struct {
	*BraceDetector
}

func init() {
	det := &JavaDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "java",
			Patterns:    javaPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".java")
}

var javaPatterns = []PatternRule{
	// class / interface / enum / record
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?(?:static\s+)?(?:final\s+)?(?:abstract\s+)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?interface\s+(\w+)`),
		Label: func(m []string) string { return "interface " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?enum\s+(\w+)`),
		Label: func(m []string) string { return "enum " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?record\s+(\w+)`),
		Label: func(m []string) string { return "record " + m[1] },
	},
	// method
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?(?:static\s+)?(?:final\s+)?(?:synchronized\s+)?(?:abstract\s+)?(?:\w+(?:<[^>]*>)?[\[\]]*\s+)(\w+)\s*\(`),
		Label: func(m []string) string { return "method " + m[1] },
	},
	// constructor (same name as containing class — use generic label)
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|protected\s+)?(\w+)\s*\([^)]*\)\s*(?:throws\s+\w+(?:\s*,\s*\w+)*)?\s*\{`),
		Label: func(m []string) string { return "method " + m[1] },
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
	// for / enhanced for
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
		Re:    regexp.MustCompile(`^\s*try\s*(?:\(|{)`),
		Label: func(m []string) string { return "try" },
	},
	// catch
	{
		Re:    regexp.MustCompile(`^\s*catch\s*\(`),
		Label: func(m []string) string { return "catch" },
	},
	// finally
	{
		Re:    regexp.MustCompile(`^\s*finally\s*\{`),
		Label: func(m []string) string { return "finally" },
	},
}
