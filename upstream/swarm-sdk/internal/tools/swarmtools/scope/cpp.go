package scope

import "regexp"

// CppDetector detects scope boundaries in C++ source files.
type CppDetector struct {
	*BraceDetector
}

func init() {
	det := &CppDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "cpp",
			Patterns:    cppPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".cpp", ".cc", ".cxx", ".hpp", ".hh", ".hxx", ".h++", ".c++")
}

var cppPatterns = []PatternRule{
	// namespace
	{
		Re:    regexp.MustCompile(`^\s*namespace\s+(\w+)`),
		Label: func(m []string) string { return "namespace " + m[1] },
	},
	// class / struct
	{
		Re:    regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:template\s*<[^>]*>\s*)?struct\s+(\w+)`),
		Label: func(m []string) string { return "struct " + m[1] },
	},
	// method: ReturnType ClassName::MethodName(
	{
		Re:    regexp.MustCompile(`^\s*(?:\w+[\s*&]+)?(\w+)::(\w+)\s*\(`),
		Label: func(m []string) string { return m[1] + "::" + m[2] },
	},
	// function/method (general)
	{
		Re:    regexp.MustCompile(`^(?:static\s+)?(?:virtual\s+)?(?:inline\s+)?(?:const\s+)?(?:unsigned\s+)?\w+[\s*&]+(\w+)\s*\(`),
		Label: func(m []string) string { return "func " + m[1] },
	},
	// enum class
	{
		Re:    regexp.MustCompile(`^\s*enum\s+(?:class\s+)?(\w+)`),
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
	// for / range-based for
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
		Re:    regexp.MustCompile(`^\s*catch\s*\(`),
		Label: func(m []string) string { return "catch" },
	},
	// lambda: [captures](params) {
	{
		Re:    regexp.MustCompile(`^\s*(?:auto\s+\w+\s*=\s*)?\[`),
		Label: func(m []string) string { return "lambda" },
	},
}
