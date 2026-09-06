package scope

import "regexp"

// RDetector detects scope boundaries in R source files.
// R uses braces for blocks, with function definitions and control flow.
type RDetector struct{ *BraceDetector }

func init() {
	det := NewBraceDetector(BraceConfig{
		LangName:    "r",
		CountBraces: CStyleBraceCounter,
		Patterns: []PatternRule{
			// function assignment: name <- function(...)
			{
				Re:    regexp.MustCompile(`^\s*(\w+)\s*<-\s*function\s*\(`),
				Label: func(m []string) string { return "func " + m[1] },
			},
			// function assignment with =
			{
				Re:    regexp.MustCompile(`^\s*(\w+)\s*=\s*function\s*\(`),
				Label: func(m []string) string { return "func " + m[1] },
			},
			// if
			{
				Re:    regexp.MustCompile(`^\s*(?:else\s+)?if\s*\(`),
				Label: func(m []string) string { return "if" },
			},
			// else
			{
				Re:    regexp.MustCompile(`^\s*(?:}\s*)?else\s*\{`),
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
			// repeat
			{
				Re:    regexp.MustCompile(`^\s*repeat\s*\{`),
				Label: func(m []string) string { return "repeat" },
			},
			// tryCatch
			{
				Re:    regexp.MustCompile(`^\s*tryCatch\s*\(`),
				Label: func(m []string) string { return "tryCatch" },
			},
		},
	})
	Register(det, ".r", ".R", ".Rmd")
}
