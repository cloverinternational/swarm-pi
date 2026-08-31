package scope

import "regexp"

// SwiftDetector detects scope boundaries in Swift source files.
type SwiftDetector struct {
	*BraceDetector
}

func init() {
	det := &SwiftDetector{
		BraceDetector: NewBraceDetector(BraceConfig{
			LangName:    "swift",
			Patterns:    swiftPatterns,
			CountBraces: CStyleBraceCounter,
		}),
	}
	Register(det, ".swift")
}

var swiftPatterns = []PatternRule{
	// class / struct / enum / protocol / extension
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+|open\s+|fileprivate\s+)?(?:final\s+)?class\s+(\w+)`),
		Label: func(m []string) string { return "class " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?struct\s+(\w+)`),
		Label: func(m []string) string { return "struct " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?enum\s+(\w+)`),
		Label: func(m []string) string { return "enum " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?protocol\s+(\w+)`),
		Label: func(m []string) string { return "protocol " + m[1] },
	},
	{
		Re:    regexp.MustCompile(`^\s*extension\s+(\w+)`),
		Label: func(m []string) string { return "extension " + m[1] },
	},
	// func
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+|open\s+)?(?:static\s+)?(?:class\s+)?(?:override\s+)?(?:@\w+\s+)*func\s+(\w+)`),
		Label: func(m []string) string { return "func " + m[1] },
	},
	// init
	{
		Re:    regexp.MustCompile(`^\s*(?:public\s+|private\s+|internal\s+)?(?:convenience\s+)?(?:required\s+)?init\s*[?(]`),
		Label: func(m []string) string { return "init" },
	},
	// if / guard
	{
		Re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s+`),
		Label: func(m []string) string { return "if" },
	},
	{
		Re:    regexp.MustCompile(`^\s*guard\s+`),
		Label: func(m []string) string { return "guard" },
	},
	// else
	{
		Re:    regexp.MustCompile(`^\s*else\s*\{`),
		Label: func(m []string) string { return "else" },
	},
	// for
	{
		Re:    regexp.MustCompile(`^\s*for\s+`),
		Label: func(m []string) string { return "for" },
	},
	// while
	{
		Re:    regexp.MustCompile(`^\s*while\s+`),
		Label: func(m []string) string { return "while" },
	},
	// switch
	{
		Re:    regexp.MustCompile(`^\s*switch\s+`),
		Label: func(m []string) string { return "switch" },
	},
	// do / catch
	{
		Re:    regexp.MustCompile(`^\s*do\s*\{`),
		Label: func(m []string) string { return "do" },
	},
	{
		Re:    regexp.MustCompile(`^\s*catch\s*`),
		Label: func(m []string) string { return "catch" },
	},
}
