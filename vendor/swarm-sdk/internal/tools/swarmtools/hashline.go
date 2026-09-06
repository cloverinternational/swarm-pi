// Package swarmtools contains the SwarmCode primary tool implementations.
// The hashline tools implement the hashline edit format described in
// "I Improved 15 LLMs at Coding in One Afternoon" by Can Bölük.
//
// Core idea: every line read from a file is tagged with a short content hash.
// Edits reference lines by lineNumber:hash pairs instead of reproducing content.
// If the file changed since the last read, hashes won't match and the edit is
// rejected before anything gets corrupted.
package swarmtools

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// HashLine represents a single line with its computed hash tag.
type HashLine struct {
	// Number is the 1-based line number.
	Number int
	// Hash is the 4-character hex hash of the line content.
	Hash string
	// Content is the raw line content (no trailing newline).
	Content string
}

// Tag returns the "lineNum:hash" identifier (e.g. "42:a3f1").
func (h HashLine) Tag() string {
	return fmt.Sprintf("%d:%s", h.Number, h.Hash)
}

// FormatLine returns the full hashline output: "lineNum:hash|content".
func (h HashLine) FormatLine() string {
	return fmt.Sprintf("%d:%s|%s", h.Number, h.Hash, h.Content)
}

// LineHash computes a 4-character hex hash of a line's content using FNV-1a.
// FNV-1a is chosen for speed and good distribution on short strings.
// The 4-char hex (65536 values) provides strong collision resistance even in
// multi-thousand line files. Paired with line numbers, collisions are near-zero.
func LineHash(content string) string {
	h := fnv.New32a()
	h.Write([]byte(content))
	sum := h.Sum32()
	// Take lowest 2 bytes, format as 4-char hex
	return fmt.Sprintf("%04x", uint16(sum))
}

// HashFileContent takes raw file content and returns HashLine entries for every line.
// This is the core function used by HashlineRead.
func HashFileContent(content string) []HashLine {
	lines := strings.Split(content, "\n")
	result := make([]HashLine, len(lines))
	for i, line := range lines {
		result[i] = HashLine{
			Number:  i + 1,
			Hash:    LineHash(line),
			Content: line,
		}
	}
	return result
}

// FormatHashlines formats a slice of HashLines into the hashline output format.
// Each line is formatted as "lineNum:hash|content", joined by newlines.
func FormatHashlines(lines []HashLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = l.FormatLine()
	}
	return strings.Join(parts, "\n")
}

// ValidateLineHash checks that the hash at the given 1-based line number
// matches the current file content. Returns an error describing the mismatch
// if validation fails.
func ValidateLineHash(lines []HashLine, lineNum int, expectedHash string) error {
	if lineNum < 1 || lineNum > len(lines) {
		return fmt.Errorf("line %d out of range (file has %d lines)", lineNum, len(lines))
	}
	actual := lines[lineNum-1]
	if actual.Hash != expectedHash {
		return fmt.Errorf(
			"hash mismatch at line %d: expected %q but file has %q (file may have changed since last read)",
			lineNum, expectedHash, actual.Hash,
		)
	}
	return nil
}

// ParseLineRef parses a "lineNum:hash" string (e.g. "42:a3f1") into its components.
// Returns the 1-based line number and the 4-char hash.
func ParseLineRef(ref string) (lineNum int, hash string, err error) {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid line reference %q: expected format lineNum:hash (e.g. \"42:a3f1\")", ref)
	}
	n := 0
	if _, err := fmt.Sscanf(parts[0], "%d", &n); err != nil {
		return 0, "", fmt.Errorf("invalid line number in reference %q: %w", ref, err)
	}
	if n < 1 {
		return 0, "", fmt.Errorf("line number must be >= 1, got %d in reference %q", n, ref)
	}
	hash = parts[1]
	if len(hash) != 4 {
		return 0, "", fmt.Errorf("hash must be exactly 4 hex characters, got %q in reference %q", hash, ref)
	}
	return n, hash, nil
}
