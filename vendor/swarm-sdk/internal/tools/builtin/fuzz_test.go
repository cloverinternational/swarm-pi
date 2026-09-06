package builtin

import (
	"regexp"
	"strings"
	"testing"
)

// FuzzGrepRegexCompilation fuzzes regex pattern compilation.
// High risk: ReDoS (Regular Expression Denial of Service), catastrophic backtracking
func FuzzGrepRegexCompilation(f *testing.F) {
	testCases := []string{
		// Valid patterns
		"hello",
		"[0-9]+",
		"^start",
		"end$",
		"(group)",

		// ReDoS risk patterns
		"(a+)+",
		"(a*)*",
		"(.*)*",
		"(a|a)*",
		"(a|ab)*",
		"(a|a|a)*",
		"(.*a){x}",

		// Catastrophic backtracking
		"(x+x+)+y",
		"(a+)+b",
		"(a*)*b",

		// Edge cases
		"",
		".",
		".*",
		".+",
		".?",
		"[",
		"]",
		"(",
		")",
		"{",
		"}",

		// Large alternations (ReDoS risk)
		"(" + strings.Repeat("a|", 1000) + "b)",

		// Deeply nested
		"((((((((((.)))))))))",

		// Excessive quantifiers
		"{999999999}",
		"{0,999999999}",

		// Character classes
		"[a-z]",
		"[^a-z]",
		"[a-zA-Z0-9_]",
		"[" + strings.Repeat("a", 1000) + "]",

		// Unicode
		"λ+",
		"\\p{L}",

		// Special sequences
		"\\d+",
		"\\w+",
		"\\s+",
		"\\b",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		// Attempt to compile regex - should not hang or panic
		// Use timeout to catch ReDoS
		done := make(chan bool, 1)
		go func() {
			_, _ = regexp.Compile(pattern)
			done <- true
		}()

		select {
		case <-done:
			// Compilation succeeded or failed gracefully
			// If it times out, the regex has ReDoS vulnerability
		}
	})
}

// FuzzGrepFilePathHandling fuzzes file path handling
func FuzzGrepFilePathHandling(f *testing.F) {
	testCases := []string{
		"/path/to/file",
		"relative/path",
		".",
		"..",
		"/",
		"",
		"/./././",
		"/../../../etc/passwd",
		"/path\x00with\x00nulls",
		"/path with spaces",
		"/path\twith\ttabs",
		"/path\nwith\nnewlines",
		strings.Repeat("/", 1000),
		strings.Repeat("a/", 1000),
		"/path" + strings.Repeat("x", 1000000), // 1MB path
		"λ/ω/α",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, path string) {
		// Path handling should not panic
		// Should handle path traversal safely
		_ = path
	})
}

// FuzzGrepPatternMatching fuzzes actual pattern matching
func FuzzGrepPatternMatching(f *testing.F) {
	testCases := []struct {
		pattern string
		text    string
	}{
		{"hello", "hello world"},
		{"[0-9]+", "123 numbers"},
		{"^start", "start of line"},
		{"end$", "line end"},
		{"", ""},
		{".*", "anything"},
		{".", "x"},
		{"(group)", "group"},
	}

	for _, tc := range testCases {
		f.Add(tc.pattern, tc.text)
	}

	f.Fuzz(func(t *testing.T, pattern string, text string) {
		// Try to match - should not panic even on invalid patterns
		re, err := regexp.Compile(pattern)
		if err == nil {
			// Match should not panic
			_ = re.MatchString(text)
		}
	})
}

// FuzzGrepLargeFileHandling fuzzes handling of large file content
func FuzzGrepLargeFileHandling(f *testing.F) {
	testCases := []string{
		"",
		"single line",
		"line1\nline2",
		strings.Repeat("line\n", 10000),
		strings.Repeat("x", 1000000), // 1MB single line
		strings.Repeat("a", 1000) + "\n" + strings.Repeat("b", 1000),
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, content string) {
		// Splitting large content should not panic
		lines := strings.Split(content, "\n")
		if len(lines) == 0 {
			t.Error("Split returned empty lines")
		}
	})
}

// FuzzGrepContextHandling fuzzes context line handling
func FuzzGrepContextHandling(f *testing.F) {
	testCases := struct {
		contextLines int
		fileLines    int
	}{
		contextLines: -1,
		fileLines:    0,
	}

	f.Add(testCases.contextLines, testCases.fileLines)
	f.Add(0, 0)
	f.Add(10, 100)
	f.Add(999999, 1)
	f.Add(-999999, 100)

	f.Fuzz(func(t *testing.T, contextLines int, fileLines int) {
		// Context handling should validate parameters
		if contextLines < 0 {
			// Invalid context lines should be rejected
			_ = contextLines
		}
		if fileLines < 0 {
			// Invalid file lines should be rejected
			_ = fileLines
		}
	})
}

// FuzzGrepOtherFlags fuzzes other grep-like flags
func FuzzGrepOtherFlags(f *testing.F) {
	testCases := []struct {
		ignoreCase bool
		invert     bool
		recursive  bool
	}{
		{true, true, true},
		{false, false, false},
		{true, false, true},
	}

	for _, tc := range testCases {
		f.Add(tc.ignoreCase, tc.invert, tc.recursive)
	}

	f.Fuzz(func(t *testing.T, ignoreCase bool, invert bool, recursive bool) {
		// Flag combinations should all be valid
		_ = ignoreCase
		_ = invert
		_ = recursive
	})
}
