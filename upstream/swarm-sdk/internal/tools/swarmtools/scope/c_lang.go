package scope

import "regexp"

// CDetector detects scope boundaries in C source files.
type CDetector struct {
	*BraceDetector
}

func init() {
	det := &CDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "c",
			Patterns:    cPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".c", ".h")
}

var cPatterns = []PatternRule{
	// function definition: return_type name(
	{
		Re:    regexp.MustCompile(`^(?:static\s+)?(?:inline\s+)?(?:const\s+)?(?:unsigned\s+)?(?:struct\s+)?\w+[\s*]+(\w+)\s*\(`),
		Label: func(m []string) string { return "func " + m[1] },
	},
	// struct name {
	{
		Re:    regexp.MustCompile(`^\s*(?:typedef\s+)?struct\s+(\w+)`),
		Label: func(m []string) string { return "struct " + m[1] },
	},
	// union name {
	{
		Re:    regexp.MustCompile(`^\s*(?:typedef\s+)?union\s+(\w+)`),
		Label: func(m []string) string { return "union " + m[1] },
	},
	// enum name {
	{
		Re:    regexp.MustCompile(`^\s*(?:typedef\s+)?enum\s+(\w+)`),
		Label: func(m []string) string { return "enum " + m[1] },
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
	// do
	{
		Re:    regexp.MustCompile(`^\s*do\s*\{`),
		Label: func(m []string) string { return "do" },
	},
}
