// Package scope provides language-aware scope detection for structural hashing.
//
// The core idea: a line's identity is determined by its content PLUS the
// structural scope it lives in (e.g. "return nil" inside "func Execute"
// inside "type Handler"). This makes hashes stable even when hundreds of
// lines are inserted or deleted elsewhere in the file — as long as the
// structural containers around a line haven't changed, its hash is unchanged.
//
// To guarantee zero collisions, each hash also incorporates a scope-local
// ordinal: when the same content appears multiple times at the same scope
// level (e.g. multiple "}" or blank lines), each occurrence gets a unique
// ordinal so their hashes differ.
//
// To add a new language:
//  1. Create a new file (e.g. rust.go)
//  2. Implement the ScopeDetector interface
//  3. Call Register() in an init() function with the relevant file extensions
package scope

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync"
)

// ScopeEntry represents one level in the scope chain.
// For example, "func Execute" or "class UserService".
type ScopeEntry struct {
	// Label is the stable identifier for this scope level.
	// Should be a normalized form (e.g. "func Execute", not the full signature).
	Label string
}

// ScopeChain is the ordered list of containing scopes for a line,
// from outermost to innermost.
type ScopeChain []ScopeEntry

// String returns a human-readable representation of the chain.
func (sc ScopeChain) String() string {
	parts := make([]string, len(sc))
	for i, e := range sc {
		parts[i] = e.Label
	}
	return strings.Join(parts, " → ")
}

// HashKey returns the string used as input to the hash function.
// Uses null byte as separator to avoid ambiguity.
func (sc ScopeChain) HashKey() string {
	parts := make([]string, len(sc))
	for i, e := range sc {
		parts[i] = e.Label
	}
	return strings.Join(parts, "\x00")
}

// Equal checks if two scope chains are identical.
func (sc ScopeChain) Equal(other ScopeChain) bool {
	if len(sc) != len(other) {
		return false
	}
	for i := range sc {
		if sc[i].Label != other[i].Label {
			return false
		}
	}
	return true
}

// ScopeDetector analyzes a file and determines the scope chain for every line.
// Each language implements this interface.
type ScopeDetector interface {
	// Name returns the detector name (e.g. "go", "python", "indent").
	Name() string

	// DetectScopes takes all lines of a file and returns the scope chain
	// for each line. The returned slice MUST have the same length as lines.
	DetectScopes(lines []string) []ScopeChain
}

// --- Registry ---

var (
	registryMu  sync.RWMutex
	detectors   = map[string]ScopeDetector{} // extension → detector
	fallbackDet ScopeDetector                // used when no extension match
)

// Register maps one or more file extensions to a detector.
// Extensions should include the dot (e.g. ".go", ".py").
func Register(det ScopeDetector, extensions ...string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	for _, ext := range extensions {
		detectors[ext] = det
	}
}

// SetFallback sets the detector used when no language-specific detector matches.
func SetFallback(det ScopeDetector) {
	registryMu.Lock()
	defer registryMu.Unlock()
	fallbackDet = det
}

// ForFile returns the best ScopeDetector for the given file path.
// Falls back to the indent-based detector if no language match is found.
// Also handles special filenames without extensions (Dockerfile, Makefile, etc.)
func ForFile(filePath string) ScopeDetector {
	ext := strings.ToLower(filepath.Ext(filePath))
	registryMu.RLock()
	defer registryMu.RUnlock()

	if det, ok := detectors[ext]; ok {
		return det
	}

	// Handle special filenames without extensions
	base := strings.ToLower(filepath.Base(filePath))
	switch {
	case base == "dockerfile" || strings.HasPrefix(base, "dockerfile."):
		if det, ok := detectors[".dockerfile"]; ok {
			return det
		}
	case base == "makefile" || base == "gnumakefile":
		if det, ok := detectors[".mk"]; ok {
			return det
		}
	case base == "jenkinsfile":
		// Groovy-like syntax — use indent fallback
	case base == "vagrantfile" || base == "gemfile" || base == "rakefile":
		if det, ok := detectors[".rb"]; ok {
			return det
		}
	}

	if fallbackDet != nil {
		return fallbackDet
	}
	// Ultimate fallback — shouldn't happen if init() ran
	return &IndentDetector{}
}

// ListRegistered returns all registered extensions and their detector names.
func ListRegistered() map[string]string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	result := make(map[string]string, len(detectors))
	for ext, det := range detectors {
		result[ext] = det.Name()
	}
	return result
}

// --- Scope-Aware Hashing ---

// hashLineWithOrdinal computes a 4-character hex hash of a line's content
// combined with its scope chain and ordinal disambiguator.
//
// The ordinal ensures that two lines with identical content at the same
// scope level (e.g. two "}" in the same function) get different hashes.
// ordinal=0 means "first occurrence", ordinal=1 means "second", etc.
func hashLineWithOrdinal(content string, chain ScopeChain, ordinal int) string {
	h := fnv.New32a()
	if len(chain) > 0 {
		h.Write([]byte(chain.HashKey()))
		h.Write([]byte{0})
	}
	h.Write([]byte(content))
	if ordinal > 0 {
		h.Write([]byte{0})
		h.Write(fmt.Appendf(nil, "%d", ordinal))
	}
	return fmt.Sprintf("%04x", uint16(h.Sum32()))
}

// HashLine computes a scope-aware hash without ordinal disambiguation.
// Prefer HashFileLines for full-file hashing which includes ordinals.
// This function is useful for single-line testing or when you know the
// content is unique within its scope.
func HashLine(content string, chain ScopeChain) string {
	return hashLineWithOrdinal(content, chain, 0)
}

// ContentOnlyHash computes a hash using only the line content (no scope).
// This is the legacy behavior, useful for fallback matching.
func ContentOnlyHash(content string) string {
	h := fnv.New32a()
	h.Write([]byte(content))
	return fmt.Sprintf("%04x", uint16(h.Sum32()))
}

// HashedLine represents a single line with its scope-aware hash.
type HashedLine struct {
	// Number is the 1-based line number.
	Number int
	// Hash is the 4-character scope-aware hash (guaranteed unique in file).
	Hash string
	// Content is the raw line content.
	Content string
	// Scope is the scope chain for this line (not sent to agent, used internally).
	Scope ScopeChain
	// Ordinal is the occurrence index of this (scope, content) pair.
	// 0 = first/only, 1 = second, etc. Used for disambiguation.
	Ordinal int
}

// Tag returns the "lineNum:hash" reference (e.g. "42:f7b2").
func (h HashedLine) Tag() string {
	return fmt.Sprintf("%d:%s", h.Number, h.Hash)
}

// FormatLine returns "lineNum:hash|content" for Read output.
func (h HashedLine) FormatLine() string {
	return fmt.Sprintf("%d:%s|%s", h.Number, h.Hash, h.Content)
}

// ordinalKey generates the map key for tracking ordinals.
// Two lines get the same key iff they have the same scope chain AND content.
type ordinalKey struct {
	scopeKey string
	content  string
}

// HashFileLines computes scope-aware hashes for every line in a file.
// Guarantees: every line gets a unique hash (no collisions within a file)
// by incorporating scope chain + ordinal disambiguation.
func HashFileLines(lines []string, det ScopeDetector) []HashedLine {
	scopes := det.DetectScopes(lines)
	result := make([]HashedLine, len(lines))

	// Track ordinals: how many times each (scope, content) pair has appeared.
	// This disambiguates identical lines at the same scope level.
	seen := make(map[ordinalKey]int)

	for i, line := range lines {
		key := ordinalKey{
			scopeKey: scopes[i].HashKey(),
			content:  line,
		}
		ord := seen[key]
		seen[key] = ord + 1

		result[i] = HashedLine{
			Number:  i + 1,
			Hash:    hashLineWithOrdinal(line, scopes[i], ord),
			Content: line,
			Scope:   scopes[i],
			Ordinal: ord,
		}
	}

	return result
}

// --- Fuzzy Line Resolution ---

// ResolveResult is the outcome of searching for a hash in the file.
type ResolveResult struct {
	// Line is the resolved 1-based line number in the current file.
	Line int
	// Shift is how far the line moved from the hinted position (0 = exact).
	Shift int
	// Method describes how the match was found.
	Method string // "exact", "nearby", "full_scan"
}

// FindLineByHash locates a line by its hash, using the hinted line number
// for performance. The line number is just a starting point — if the hash
// doesn't match there, we search outward.
//
// Resolution strategy (ordered by speed):
//  1. Exact: hash matches at the hinted line number
//  2. Outward spiral: expand ±1, ±2, ±3... from hint, return first match
//
// The outward spiral naturally returns the closest match to the hint,
// which handles both small shifts (linter added an import) and large
// shifts (300 lines added above).
//
// Returns error only if the hash cannot be found anywhere in the file.
func FindLineByHash(lines []HashedLine, hintLine int, hash string) (*ResolveResult, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty file")
	}

	// Clamp hint to valid range
	if hintLine < 1 {
		hintLine = 1
	}
	if hintLine > len(lines) {
		hintLine = len(lines)
	}

	// Strategy 1: Exact match at hinted position
	idx := hintLine - 1
	if lines[idx].Hash == hash {
		return &ResolveResult{Line: hintLine, Shift: 0, Method: "exact"}, nil
	}

	// Strategy 2: Expand outward — finds closest match
	for delta := 1; delta <= len(lines); delta++ {
		// Check above
		above := idx - delta
		if above >= 0 && lines[above].Hash == hash {
			return &ResolveResult{
				Line:   above + 1,
				Shift:  -delta,
				Method: methodForShift(delta),
			}, nil
		}
		// Check below
		below := idx + delta
		if below < len(lines) && lines[below].Hash == hash {
			return &ResolveResult{
				Line:   below + 1,
				Shift:  delta,
				Method: methodForShift(delta),
			}, nil
		}
		// If both are out of range, we've scanned everything
		if above < 0 && below >= len(lines) {
			break
		}
	}

	return nil, fmt.Errorf(
		"hash %q not found in file (%d lines) — the line may have been modified or deleted since last read",
		hash, len(lines),
	)
}

// FindLineByHashInRange is like FindLineByHash but for resolving the end
// of a range. It only accepts matches at or after afterLine to ensure
// the range is valid (end >= start).
func FindLineByHashInRange(lines []HashedLine, hintLine int, hash string, afterLine int) (*ResolveResult, error) {
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty file")
	}

	if hintLine < 1 {
		hintLine = 1
	}
	if hintLine > len(lines) {
		hintLine = len(lines)
	}

	idx := hintLine - 1
	minIdx := afterLine - 1

	// Check exact position first
	if idx >= minIdx && lines[idx].Hash == hash {
		return &ResolveResult{Line: hintLine, Shift: 0, Method: "exact"}, nil
	}

	// Expand outward, only accepting results >= afterLine
	for delta := 1; delta <= len(lines); delta++ {
		above := idx - delta
		if above >= minIdx && above >= 0 && lines[above].Hash == hash {
			return &ResolveResult{
				Line:   above + 1,
				Shift:  -delta,
				Method: methodForShift(delta),
			}, nil
		}
		below := idx + delta
		if below < len(lines) && lines[below].Hash == hash {
			return &ResolveResult{
				Line:   below + 1,
				Shift:  delta,
				Method: methodForShift(delta),
			}, nil
		}
		if above < 0 && below >= len(lines) {
			break
		}
	}

	return nil, fmt.Errorf(
		"hash %q not found at or after line %d — the line may have been modified or deleted",
		hash, afterLine,
	)
}

func methodForShift(delta int) string {
	if delta <= 50 {
		return "nearby"
	}
	return "full_scan"
}
