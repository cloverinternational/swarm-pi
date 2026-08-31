package scope

import (
	"strings"
	"testing"
)

// =============================================================================
// FileHashCache basic operations
// =============================================================================

func TestFileHashCache_StoreAndGet(t *testing.T) {
	c := NewFileHashCache()
	lines := []HashedLine{
		{Number: 1, Hash: "a1b2", Content: "hello"},
		{Number: 2, Hash: "c3d4", Content: "world"},
	}

	c.Store("/tmp/test.go", lines)

	got := c.Get("/tmp/test.go")
	if got == nil {
		t.Fatal("expected cached state, got nil")
	}
	if len(got.Lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(got.Lines))
	}
	if got.Lines[0].Hash != "a1b2" || got.Lines[1].Content != "world" {
		t.Errorf("cached lines don't match: %+v", got.Lines)
	}
}

func TestFileHashCache_StoreOverwrites(t *testing.T) {
	c := NewFileHashCache()
	c.Store("/tmp/test.go", []HashedLine{{Number: 1, Hash: "old1", Content: "old"}})
	c.Store("/tmp/test.go", []HashedLine{{Number: 1, Hash: "new1", Content: "new"}})

	got := c.Get("/tmp/test.go")
	if got.Lines[0].Hash != "new1" {
		t.Errorf("expected overwritten hash 'new1', got %q", got.Lines[0].Hash)
	}
}

func TestFileHashCache_GetNonExistent(t *testing.T) {
	c := NewFileHashCache()
	if c.Get("/tmp/nonexistent") != nil {
		t.Error("expected nil for nonexistent path")
	}
}

func TestFileHashCache_Delete(t *testing.T) {
	c := NewFileHashCache()
	c.Store("/tmp/test.go", []HashedLine{{Number: 1, Hash: "a1b2", Content: "x"}})
	c.Delete("/tmp/test.go")
	if c.Get("/tmp/test.go") != nil {
		t.Error("expected nil after delete")
	}
}

func TestFileHashCache_DefensiveCopy(t *testing.T) {
	c := NewFileHashCache()
	lines := []HashedLine{
		{Number: 1, Hash: "a1b2", Content: "original"},
	}
	c.Store("/tmp/test.go", lines)

	// Mutate the original slice — should NOT affect cached data
	lines[0].Content = "mutated"

	got := c.Get("/tmp/test.go")
	if got.Lines[0].Content != "original" {
		t.Errorf("defensive copy failed: cached content is %q, want %q", got.Lines[0].Content, "original")
	}
}

func TestGlobalFileHashCache_Singleton(t *testing.T) {
	ResetGlobalFileHashCache()
	defer ResetGlobalFileHashCache()

	a := GlobalFileHashCache()
	b := GlobalFileHashCache()
	if a != b {
		t.Error("GlobalFileHashCache should return same instance")
	}
}

// =============================================================================
// lookupContentByHash
// =============================================================================

func TestLookupContentByHash_HintHit(t *testing.T) {
	lines := []HashedLine{
		{Number: 1, Hash: "aaaa", Content: "line1"},
		{Number: 2, Hash: "bbbb", Content: "line2"},
		{Number: 3, Hash: "cccc", Content: "line3"},
	}

	content, found := lookupContentByHash(lines, 2, "bbbb")
	if !found {
		t.Fatal("expected to find hash at hint")
	}
	if content != "line2" {
		t.Errorf("content = %q, want %q", content, "line2")
	}
}

func TestLookupContentByHash_SpiralHit(t *testing.T) {
	lines := []HashedLine{
		{Number: 1, Hash: "aaaa", Content: "line1"},
		{Number: 2, Hash: "bbbb", Content: "line2"},
		{Number: 3, Hash: "cccc", Content: "line3"},
	}

	// Hint at line 1, but hash is at line 3
	content, found := lookupContentByHash(lines, 1, "cccc")
	if !found {
		t.Fatal("expected to find hash via spiral")
	}
	if content != "line3" {
		t.Errorf("content = %q, want %q", content, "line3")
	}
}

func TestLookupContentByHash_NotFound(t *testing.T) {
	lines := []HashedLine{
		{Number: 1, Hash: "aaaa", Content: "line1"},
	}

	_, found := lookupContentByHash(lines, 1, "zzzz")
	if found {
		t.Error("should not find nonexistent hash")
	}
}

func TestLookupContentByHash_Empty(t *testing.T) {
	_, found := lookupContentByHash([]HashedLine{}, 1, "aaaa")
	if found {
		t.Error("should not find in empty slice")
	}
}

// =============================================================================
// findLineByContent
// =============================================================================

func TestFindLineByContent_ExactPosition(t *testing.T) {
	lines := makeTestHashedLines([]string{"alpha", "beta", "gamma"})

	result := findLineByContent(lines, 2, "beta")
	if result == nil {
		t.Fatal("expected match")
	}
	if result.Line != 2 || result.Shift != 0 || result.Method != "content_exact" {
		t.Errorf("got Line=%d Shift=%d Method=%q, want 2/0/content_exact",
			result.Line, result.Shift, result.Method)
	}
}

func TestFindLineByContent_SpiralSearch(t *testing.T) {
	lines := makeTestHashedLines([]string{"a", "b", "target", "d", "e"})

	// Hint at line 1, content at line 3
	result := findLineByContent(lines, 1, "target")
	if result == nil {
		t.Fatal("expected match")
	}
	if result.Line != 3 || result.Method != "content_nearby" {
		t.Errorf("got Line=%d Method=%q, want 3/content_nearby", result.Line, result.Method)
	}
}

func TestFindLineByContent_NotFound(t *testing.T) {
	lines := makeTestHashedLines([]string{"a", "b", "c"})
	result := findLineByContent(lines, 1, "nonexistent")
	if result != nil {
		t.Error("expected nil for nonexistent content")
	}
}

func TestFindLineByContent_Empty(t *testing.T) {
	result := findLineByContent([]HashedLine{}, 1, "anything")
	if result != nil {
		t.Error("expected nil for empty lines")
	}
}

func TestFindLineByContentInRange_RespectsAfterLine(t *testing.T) {
	lines := makeTestHashedLines([]string{"target", "b", "c", "target", "e"})

	// Content "target" exists at line 1 and line 4. With afterLine=3, should find line 4.
	result := findLineByContentInRange(lines, 4, "target", 3)
	if result == nil {
		t.Fatal("expected match")
	}
	if result.Line != 4 {
		t.Errorf("got Line=%d, want 4 (respecting afterLine=3)", result.Line)
	}
}

// =============================================================================
// FindLineByHashWithCache
// =============================================================================

func TestFindLineByHashWithCache_PrimaryWins(t *testing.T) {
	// When hash is found directly, cache is not needed
	lines := makeTestHashedLines([]string{"alpha", "beta", "gamma"})

	result, err := FindLineByHashWithCache(lines, 2, lines[1].Hash, nil, "/tmp/test.go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Line != 2 || result.Method != "exact" {
		t.Errorf("got Line=%d Method=%q, want 2/exact", result.Line, result.Method)
	}
}

func TestFindLineByHashWithCache_NilCache(t *testing.T) {
	lines := makeTestHashedLines([]string{"alpha"})

	_, err := FindLineByHashWithCache(lines, 1, "zzzz", nil, "/tmp/test.go")
	if err == nil {
		t.Error("expected error with nil cache and nonexistent hash")
	}
}

func TestFindLineByHashWithCache_NoCachedState(t *testing.T) {
	lines := makeTestHashedLines([]string{"alpha"})
	cache := NewFileHashCache()

	_, err := FindLineByHashWithCache(lines, 1, "zzzz", cache, "/tmp/test.go")
	if err == nil {
		t.Error("expected error with empty cache")
	}
}

func TestFindLineByHashWithCache_FallbackToContent(t *testing.T) {
	// THE KEY TEST: simulates the exact scenario that causes failures.
	//
	// Scenario: agent has a stale hash "xxxx" for "target" content at line 3.
	// The current file has "target" at line 4 with a DIFFERENT hash (ordinal/scope shifted).
	// The cache stores the old state where hash "xxxx" → content "target".
	// FindLineByHashWithCache should:
	// 1. Try hash resolution → fail (xxxx not in current file)
	// 2. Look up xxxx in cache → find content "target"
	// 3. Search current file for "target" near line 3 → find at line 4

	// Build the "current" file state (what's on disk now)
	currentLines := []HashedLine{
		{Number: 1, Hash: "1111", Content: "alpha"},
		{Number: 2, Hash: "2222", Content: "beta"},
		{Number: 3, Hash: "3333", Content: "inserted"},
		{Number: 4, Hash: "4444", Content: "target"},
		{Number: 5, Hash: "5555", Content: "gamma"},
	}

	// Build the "old" cached state (what the agent saw when it Read the file)
	cachedLines := []HashedLine{
		{Number: 1, Hash: "1111", Content: "alpha"},
		{Number: 2, Hash: "2222", Content: "beta"},
		{Number: 3, Hash: "xxxx", Content: "target"}, // stale hash for target
		{Number: 4, Hash: "5555", Content: "gamma"},
	}

	staleHash := "xxxx"
	staleHintLine := 3

	// Verify hash xxxx is NOT in the current file
	_, err := FindLineByHash(currentLines, staleHintLine, staleHash)
	if err == nil {
		t.Fatal("hash xxxx should NOT be found in current file")
	}

	// Set up cache with the old state
	cache := NewFileHashCache()
	cache.Store("/tmp/test.txt", cachedLines)

	// Now try with cache — should succeed via content fallback
	result, err := FindLineByHashWithCache(currentLines, staleHintLine, staleHash, cache, "/tmp/test.txt")
	if err != nil {
		t.Fatalf("FindLineByHashWithCache should have fallen back to content match: %v", err)
	}

	// Verify it found "target" at line 4
	if result.Line != 4 {
		t.Errorf("Line=%d, want 4", result.Line)
	}
	if currentLines[result.Line-1].Content != "target" {
		t.Errorf("resolved to wrong content: %q, want %q",
			currentLines[result.Line-1].Content, "target")
	}
	if result.Method != "content_nearby" {
		t.Errorf("Method=%q, want content_nearby", result.Method)
	}

	t.Logf("target resolved from hint %d to line %d (shift=%+d, method=%s)",
		staleHintLine, result.Line, result.Shift, result.Method)
}

func TestFindLineByHashWithCache_ContentAlsoGone(t *testing.T) {
	// Hash in cache, but the content was actually deleted from the current file
	originalLines := []string{"a", "target", "b"}
	modifiedLines := []string{"a", "replaced", "b"} // "target" is gone

	det := &IndentDetector{}
	originalHashed := HashFileLines(originalLines, det)
	modifiedHashed := HashFileLines(modifiedLines, det)

	var targetHash string
	for _, hl := range originalHashed {
		if hl.Content == "target" {
			targetHash = hl.Hash
		}
	}

	cache := NewFileHashCache()
	cache.Store("/tmp/test.txt", originalHashed)

	_, err := FindLineByHashWithCache(modifiedHashed, 2, targetHash, cache, "/tmp/test.txt")
	if err == nil {
		t.Error("expected error when content is truly gone")
	}
	if !strings.Contains(err.Error(), "no longer exists") {
		t.Errorf("error should mention content no longer exists: %v", err)
	}
}

// =============================================================================
// FindLineByHashInRangeWithCache
// =============================================================================

func TestFindLineByHashInRangeWithCache_FallbackWithAfterLine(t *testing.T) {
	originalLines := []string{"a", "", "target", "", "target"}
	modifiedLines := []string{"a", "", "", "target", "", "target"}

	det := &IndentDetector{}
	originalHashed := HashFileLines(originalLines, det)
	modifiedHashed := HashFileLines(modifiedLines, det)

	// Get hash for the SECOND "target" (line 5 in original)
	var targetHash string
	for _, hl := range originalHashed {
		if hl.Content == "target" && hl.Number == 5 {
			targetHash = hl.Hash
		}
	}
	if targetHash == "" {
		t.Skip("couldn't isolate second target hash")
	}

	cache := NewFileHashCache()
	cache.Store("/tmp/test.txt", originalHashed)

	// With afterLine=4, should find the target at or after line 4
	result, err := FindLineByHashInRangeWithCache(modifiedHashed, 5, targetHash, 4, cache, "/tmp/test.txt")
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}

	if modifiedHashed[result.Line-1].Content != "target" {
		t.Errorf("resolved to wrong line: content=%q", modifiedHashed[result.Line-1].Content)
	}
	if result.Line < 4 {
		t.Errorf("resolved line %d is before afterLine 4", result.Line)
	}
}

// =============================================================================
// End-to-end: Markdown ordinal shift scenario
// =============================================================================

func TestCacheFallback_MarkdownOrdinalShift(t *testing.T) {
	// Simulates the AGENTS.md scenario:
	// A markdown file has many empty lines under headings.
	// An edit changes one line, shifting ordinals for empty lines.
	// Subsequent edits with stale hashes should succeed via cache fallback.

	original := `# Guide

Some intro text.

## Section A

Content under A.

## Section B

Content under B.

Important line here.

More content.`

	modified := `# Guide

Some intro text.

## Section A

Content under A.

Extra line added here.

## Section B

Content under B.

Important line here.

More content.`

	det := ForFile("test.md")
	origLines := strings.Split(original, "\n")
	modLines := strings.Split(modified, "\n")

	origHashed := HashFileLines(origLines, det)
	modHashed := HashFileLines(modLines, det)

	// Find "Important line here." in original
	var importantHash string
	var importantHint int
	for _, hl := range origHashed {
		if hl.Content == "Important line here." {
			importantHash = hl.Hash
			importantHint = hl.Number
		}
	}
	if importantHash == "" {
		t.Fatal("couldn't find important line in original")
	}

	// Set up cache
	cache := NewFileHashCache()
	cache.Store("/tmp/test.md", origHashed)

	// Try to resolve using stale hash
	result, err := FindLineByHashWithCache(modHashed, importantHint, importantHash, cache, "/tmp/test.md")
	if err != nil {
		t.Fatalf("expected cache fallback to find the line: %v", err)
	}

	if modHashed[result.Line-1].Content != "Important line here." {
		t.Errorf("resolved to wrong line: content=%q", modHashed[result.Line-1].Content)
	}

	t.Logf("Markdown ordinal shift: line resolved from hint %d to %d (shift=%+d, method=%s)",
		importantHint, result.Line, result.Shift, result.Method)
}

// =============================================================================
// Helpers
// =============================================================================

func makeTestHashedLines(content []string) []HashedLine {
	det := &IndentDetector{}
	return HashFileLines(content, det)
}
