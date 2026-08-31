package scope

import "regexp"

// ScalaDetector detects scope boundaries in Scala source files.
type ScalaDetector struct {
	*BraceDetector
}

func init() {
	det := &ScalaDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "scala",
			Patterns:    scalaPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".scala", ".sc")
}

var scalaPatterns = []PatternRule{
	// object / class / trait / case class
	{
		Re:    regexp.MustCompile(`^\s*(?:case\s+)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*object\s+(\w+)`),
		Label: func(m []string) string { return "object " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*trait\s+(\w+)`),
		Label: func(m []string) string { return "trait " + m[1] },
	},
	// def
	{
		Re:    regexp.MustCompile(`^\s*(?:override\s+)?(?:private\s+|protected\s+)?def\s+(\w+)`),
		Label: func(m []string) string { return "def " + m[1] },
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
	// for
	{
		Re:    regexp.MustCompile(`^\s*for\s*[\({]`),
		Label: func(m []string) string { return "for" },
	},
	// while
	{
		Re:    regexp.MustCompile(`^\s*while\s*\(`),
		Label: func(m []string) string { return "while" },
	},
	// match
	{
		Re:    regexp.MustCompile(`\bmatch\s*\{`),
		Label: func(m []string) string { return "match" },
	},
	// try / catch / finally
	{
		Re:    regexp.MustCompile(`^\s*try\s*\{`),
		Label: func(m []string) string { return "try" },
	},
	{
		Re:    regexp.MustCompile(`^\s*catch\s*\{`),
		Label: func(m []string) string { return "catch" },
	},
	{
		Re:    regexp.MustCompile(`^\s*finally\s*\{`),
		Label: func(m []string) string { return "finally" },
	},
}
