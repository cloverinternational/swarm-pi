package scope

import "regexp"

// PerlDetector detects scope boundaries in Perl source files.
// Uses braces with Perl-specific patterns: sub, if/elsif/else,
// for/foreach/while, eval, package.
type PerlDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "perl",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// package
			{
				Re:    regexp.MustCompile(`^\s*package\s+([\w:]+)`),
				Label: func(m []string) string { return "package " + m[1] },
			},
			// sub
			{
				Re:    regexp.MustCompile(`^\s*sub\s+(\w+)`),
				Label: func(m []string) string { return "sub " + m[1] },
			},
			// if / elsif / unless
			{
				Re:    regexp.MustCompile(`^\s*(?:els)?if\s*\(`),
				Label: func(m []string) string { return "if" },
			},
			{
				Re:    regexp.MustCompile(`^\s*unless\s*\(`),
				Label: func(m []string) string { return "unless" },
			},
			// else
			{
				Re:    regexp.MustCompile(`^\s*(?:}\s*)?else\s*\{`),
				Label: func(m []string) string { return "else" },
			},
			// for / foreach
			{
				Re:    regexp.MustCompile(`^\s*(?:for|foreach)\s`),
				Label: func(m []string) string { return "for" },
			},
			// while / until
			{
				Re:    regexp.MustCompile(`^\s*while\s*\(`),
				Label: func(m []string) string { return "while" },
			},
			{
				Re:    regexp.MustCompile(`^\s*until\s*\(`),
				Label: func(m []string) string { return "until" },
			},
			// eval
			{
				Re:    regexp.MustCompile(`^\s*eval\s*\{`),
				Label: func(m []string) string { return "eval" },
			},
		},
	})
	Register(det, ".pl", ".pm", ".t")
}
