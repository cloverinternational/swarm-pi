package swarmtools

import (
	"strings"
	"testing"
)

// FuzzParseLineRef fuzzes the ParseLineRef function to detect parsing panics.
// This is critical because ParseLineRef is called on user-supplied input.
func FuzzParseLineRef(f *testing.F) {
	// Seed with known valid and invalid inputs
	testCases := []string{
		"1:a1b2",
		"42:f00d",
		"999999:abcd",
		"invalid",
		":",
		"1:",
		":hash",
		"",
		"not-a-number:hash",
		"-1:hash",
		"0:hash",
		"1:toolonghash",
		"1:x",
		"1:x:y:z",
		"1::::::::::",
		"\x00:hash",
		"\n:hash",
		"1\n:hash",
		"\t:hash",
		" 1:hash",
		"1 :hash",
		"1: hash",
		"1:hash ",
		"999999999999999999999:hash",
		"-999999999999999999999:hash",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, ref string) {
		// ParseLineRef should never panic, even on invalid input
		lineNum, hash, err := ParseLineRef(ref)

		// If it succeeds, validate the result
		if err == nil {
			if lineNum < 1 {
				t.Errorf("ParseLineRef returned invalid line number: %d", lineNum)
			}
			if len(hash) != 4 {
				t.Errorf("ParseLineRef returned invalid hash length: %d (expected 4)", len(hash))
			}
		}
	})
}

// FuzzHashFileContent fuzzes the core HashFileContent function.
// Risk: very large files, unusual line endings, memory exhaustion
func FuzzHashFileContent(f *testing.F) {
	testCases := []string{
		"",
		"\n",
		"single line",
		"line1\nline2\nline3",
		"\n\n\n",
		"line1\n\nline3",
		"\x00binary\x00",
		strings.Repeat("line\n", 10000),
		"\r\n",
		"mixed\r\nendings",
		"\xfe\xff",
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, content string) {
		// HashFileContent should never panic
		lines := HashFileContent(content)

		// Validate basic properties
		if len(lines) == 0 && content != "" {
			t.Errorf("HashFileContent returned empty lines for non-empty content")
		}

		// Check line numbers are sequential starting at 1
		for i, line := range lines {
			if line.Number != i+1 {
				t.Errorf("Line number mismatch at index %d: got %d, expected %d", i, line.Number, i+1)
			}

			// Hash should always be 4 hex chars
			if len(line.Hash) != 4 {
				t.Errorf("Hash length invalid for line %d: got %d", line.Number, len(line.Hash))
			}

			// Verify hash is valid hex
			for _, c := range line.Hash {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("Invalid hex character in hash: %c", c)
				}
			}
		}

		// Verify FormatHashlines doesn't panic
		formatted := FormatHashlines(lines)
		if formatted == "" && len(lines) > 0 {
			t.Error("FormatHashlines returned empty string for non-empty lines")
		}
	})
}

// FuzzLineHash fuzzes hash computation with various inputs
func FuzzLineHash(f *testing.F) {
	testCases := []string{
		"",
		"a",
		"short",
		"This is a longer string",
		"\x00\x01\x02\x03",
		"\r\n\t ",
		"λ ω α β",
		strings.Repeat("\n", 1000),
	}

	for _, tc := range testCases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, content string) {
		hash := LineHash(content)

		// Hash should always be exactly 4 hex characters
		if len(hash) != 4 {
			t.Errorf("Hash length invalid: got %d, expected 4", len(hash))
		}

		// All characters should be valid hex
		for _, c := range hash {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("Invalid hex character: %c", c)
			}
		}

		// Same input should always produce same hash
		hash2 := LineHash(content)
		if hash != hash2 {
			t.Errorf("Hash not deterministic: %s vs %s", hash, hash2)
		}
	})
}

// FuzzValidateLineHash fuzzes the validation function
func FuzzValidateLineHash(f *testing.F) {
	// Create test data
	testContent := "line1\nline2\nline3"
	lines := HashFileContent(testContent)

	testCases := []struct {
		lineNum int
		hash    string
	}{
		{1, lines[0].Hash},
		{2, lines[1].Hash},
		{1, "0000"},
		{0, "0000"},
		{999, "0000"},
		{-1, "0000"},
		{1, ""},
		{1, "toolong"},
	}

	for _, tc := range testCases {
		f.Add(tc.lineNum, tc.hash)
	}

	f.Fuzz(func(t *testing.T, lineNum int, hash string) {
		// ValidateLineHash should never panic
		err := ValidateLineHash(lines, lineNum, hash)

		// If line number is invalid, must return error
		if lineNum < 1 || lineNum > len(lines) {
			if err == nil {
				t.Error("Expected error for invalid line number")
			}
		}
	})
}
