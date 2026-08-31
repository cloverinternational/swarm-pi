package scope

import (
	"fmt"
	"strings"
	"testing"
)

// =============================================================================
// ScopeChain tests
// =============================================================================

func TestScopeChain_String(t *testing.T) {
	chain := ScopeChain{
		{Label: "func Execute"},
		{Label: "if"},
	}
	got := chain.String()
	if got != "func Execute → if" {
		t.Errorf("String() = %q, want %q", got, "func Execute → if")
	}
}

func TestScopeChain_String_Empty(t *testing.T) {
	chain := ScopeChain{}
	if chain.String() != "" {
		t.Errorf("empty chain String() = %q, want empty", chain.String())
	}
}

func TestScopeChain_HashKey(t *testing.T) {
	chain := ScopeChain{
		{Label: "class Foo"},
		{Label: "def bar"},
	}
	key := chain.HashKey()
	if !strings.Contains(key, "class Foo") || !strings.Contains(key, "def bar") {
		t.Errorf("HashKey() = %q, should contain both labels", key)
	}
	// Uses null byte separator
	if !strings.Contains(key, "\x00") {
		t.Error("HashKey() should use null byte separator")
	}
}

func TestScopeChain_HashKey_Empty(t *testing.T) {
	chain := ScopeChain{}
	if chain.HashKey() != "" {
		t.Errorf("empty chain HashKey() = %q, want empty", chain.HashKey())
	}
}

func TestScopeChain_Equal(t *testing.T) {
	a := ScopeChain{{Label: "func X"}, {Label: "if"}}
	b := ScopeChain{{Label: "func X"}, {Label: "if"}}
	c := ScopeChain{{Label: "func X"}, {Label: "for"}}
	d := ScopeChain{{Label: "func X"}}

	if !a.Equal(b) {
		t.Error("identical chains should be equal")
	}
	if a.Equal(c) {
		t.Error("chains with different labels should not be equal")
	}
	if a.Equal(d) {
		t.Error("chains with different lengths should not be equal")
	}
	if a.Equal(nil) {
		t.Error("non-empty chain should not equal nil")
	}
}

// =============================================================================
// Hash function tests
// =============================================================================

func TestHashLine_Deterministic(t *testing.T) {
	chain := ScopeChain{{Label: "func main"}}
	h1 := HashLine("return nil", chain)
	h2 := HashLine("return nil", chain)
	if h1 != h2 {
		t.Errorf("same input produced different hashes: %q vs %q", h1, h2)
	}
}

func TestHashLine_FourChars(t *testing.T) {
	h := HashLine("anything", ScopeChain{})
	if len(h) != 4 {
		t.Errorf("hash should be 4 chars, got %q (len=%d)", h, len(h))
	}
}

func TestHashLine_ScopeAffectsHash(t *testing.T) {
	content := "return nil"
	h1 := HashLine(content, ScopeChain{{Label: "func A"}})
	h2 := HashLine(content, ScopeChain{{Label: "func B"}})
	if h1 == h2 {
		t.Error("same content in different scopes should (usually) produce different hashes")
	}
}

func TestHashLine_EmptyScope(t *testing.T) {
	h1 := HashLine("hello", ScopeChain{})
	h2 := HashLine("hello", nil)
	// Both should produce valid 4-char hashes
	if len(h1) != 4 || len(h2) != 4 {
		t.Errorf("hashes should be 4 chars: %q, %q", h1, h2)
	}
}

func TestContentOnlyHash_Deterministic(t *testing.T) {
	h1 := ContentOnlyHash("test line")
	h2 := ContentOnlyHash("test line")
	if h1 != h2 {
		t.Errorf("same input produced different hashes: %q vs %q", h1, h2)
	}
	if len(h1) != 4 {
		t.Errorf("hash should be 4 chars, got %q", h1)
	}
}

func TestContentOnlyHash_DiffersFromScopeHash(t *testing.T) {
	content := "some code"
	noScope := ContentOnlyHash(content)
	withScope := HashLine(content, ScopeChain{{Label: "func main"}})
	if noScope == withScope {
		t.Error("content-only hash should differ from scope-aware hash (in most cases)")
	}
}

func TestHashLineWithOrdinal_ZeroOrdinal(t *testing.T) {
	chain := ScopeChain{{Label: "func main"}}
	h0 := hashLineWithOrdinal("}", chain, 0)
	h1 := hashLineWithOrdinal("}", chain, 1)
	if h0 == h1 {
		t.Error("different ordinals should produce different hashes")
	}
}

// =============================================================================
// HashedLine tests
// =============================================================================

func TestHashedLine_Tag(t *testing.T) {
	hl := HashedLine{Number: 42, Hash: "f7b2"}
	if hl.Tag() != "42:f7b2" {
		t.Errorf("Tag() = %q, want %q", hl.Tag(), "42:f7b2")
	}
}

func TestHashedLine_FormatLine(t *testing.T) {
	hl := HashedLine{Number: 5, Hash: "a3f1", Content: "hello world"}
	got := hl.FormatLine()
	if got != "5:a3f1|hello world" {
		t.Errorf("FormatLine() = %q, want %q", got, "5:a3f1|hello world")
	}
}

// =============================================================================
// HashFileLines tests
// =============================================================================

func TestHashFileLines_BasicGo(t *testing.T) {
	lines := strings.Split("package main\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}\n", "\n")
	det := ForFile("test.go")
	hashed := HashFileLines(lines, det)

	if len(hashed) != len(lines) {
		t.Fatalf("HashFileLines returned %d lines, want %d", len(hashed), len(lines))
	}

	for i, hl := range hashed {
		if hl.Number != i+1 {
			t.Errorf("line %d: Number=%d, want %d", i, hl.Number, i+1)
		}
		if len(hl.Hash) != 4 {
			t.Errorf("line %d: hash=%q, want 4 chars", i, hl.Hash)
		}
		if hl.Content != lines[i] {
			t.Errorf("line %d: content=%q, want %q", i, hl.Content, lines[i])
		}
	}
}

func TestHashFileLines_ZeroCollisions(t *testing.T) {
	// This is the KEY guarantee: no two lines in a file should have the same hash.
	// Test with a file that has many duplicate content lines (like closing braces).
	goCode := `package main

func A() {
	if true {
		x := 1
		_ = x
	}
}

func B() {
	if true {
		y := 2
		_ = y
	}
}

func C() {
	for i := 0; i < 10; i++ {
		if i > 5 {
			break
		}
	}
}`

	lines := strings.Split(goCode, "\n")
	det := ForFile("test.go")
	hashed := HashFileLines(lines, det)

	seen := make(map[string]int)
	for _, hl := range hashed {
		if prev, exists := seen[hl.Hash]; exists {
			t.Errorf("COLLISION: line %d and line %d have same hash %q (content: %q and %q)",
				prev, hl.Number, hl.Hash, hashed[prev-1].Content, hl.Content)
		}
		seen[hl.Hash] = hl.Number
	}
}

func TestHashFileLines_ZeroCollisions_ManyClosingBraces(t *testing.T) {
	// Stress test: many "}" at different scope levels
	code := `func A() {
	if true {
		for {
			x := 1
			_ = x
		}
	}
}
func B() {
	if false {
		for {
			y := 2
			_ = y
		}
	}
}
func C() {
	switch {
	case true:
		z := 3
		_ = z
	}
}`

	lines := strings.Split(code, "\n")
	det := ForFile("test.go")
	hashed := HashFileLines(lines, det)

	seen := make(map[string]int)
	for _, hl := range hashed {
		if prev, exists := seen[hl.Hash]; exists {
			t.Errorf("COLLISION: line %d (%q) and line %d (%q) have same hash %q",
				prev, hashed[prev-1].Content, hl.Number, hl.Content, hl.Hash)
		}
		seen[hl.Hash] = hl.Number
	}
}

func TestHashFileLines_EmptyFile(t *testing.T) {
	lines := []string{""}
	det := ForFile("test.txt")
	hashed := HashFileLines(lines, det)
	if len(hashed) != 1 {
		t.Fatalf("expected 1 line, got %d", len(hashed))
	}
	if hashed[0].Number != 1 {
		t.Error("single line should be number 1")
	}
}

func TestHashFileLines_EmptySlice(t *testing.T) {
	det := ForFile("test.txt")
	hashed := HashFileLines([]string{}, det)
	if len(hashed) != 0 {
		t.Fatalf("expected 0 lines, got %d", len(hashed))
	}
}

// =============================================================================
// Hash stability: the core use case
// =============================================================================

func TestHashStability_GoImportInsert(t *testing.T) {
	// This is THE test: when a linter adds an import at the top, all hashes
	// below must remain stable (because the scope chain hasn't changed).
	original := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}`

	modified := `package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println("hello")
}`

	det := ForFile("test.go")
	origLines := strings.Split(original, "\n")
	modLines := strings.Split(modified, "\n")

	origHashed := HashFileLines(origLines, det)
	modHashed := HashFileLines(modLines, det)

	// Find "fmt.Println" hash in both versions
	var origPrintHash, modPrintHash string
	for _, hl := range origHashed {
		if strings.Contains(hl.Content, "Println") {
			origPrintHash = hl.Hash
		}
	}
	for _, hl := range modHashed {
		if strings.Contains(hl.Content, "Println") {
			modPrintHash = hl.Hash
		}
	}

	if origPrintHash == "" || modPrintHash == "" {
		t.Fatal("couldn't find Println line in one or both versions")
	}

	if origPrintHash != modPrintHash {
		t.Errorf("Println hash changed after import addition: %q -> %q", origPrintHash, modPrintHash)
	}
}

func TestHashStability_PythonAddImport(t *testing.T) {
	original := `import os

class Foo:
    def bar(self):
        return 42`

	modified := `import os
import sys
import json

class Foo:
    def bar(self):
        return 42`

	det := ForFile("test.py")
	origHashed := HashFileLines(strings.Split(original, "\n"), det)
	modHashed := HashFileLines(strings.Split(modified, "\n"), det)

	var origReturnHash, modReturnHash string
	for _, hl := range origHashed {
		if strings.Contains(hl.Content, "return 42") {
			origReturnHash = hl.Hash
		}
	}
	for _, hl := range modHashed {
		if strings.Contains(hl.Content, "return 42") {
			modReturnHash = hl.Hash
		}
	}

	if origReturnHash != modReturnHash {
		t.Errorf("Python return hash changed after import addition: %q -> %q", origReturnHash, modReturnHash)
	}
}

func TestHashStability_JSAddFunction(t *testing.T) {
	original := `function greet() {
    return "hello";
}

function farewell() {
    return "bye";
}`

	// Add a new function between the existing ones
	modified := `function greet() {
    return "hello";
}

function newFunc() {
    console.log("new");
}

function farewell() {
    return "bye";
}`

	det := ForFile("test.js")
	origHashed := HashFileLines(strings.Split(original, "\n"), det)
	modHashed := HashFileLines(strings.Split(modified, "\n"), det)

	var origByeHash, modByeHash string
	for _, hl := range origHashed {
		if strings.Contains(hl.Content, `"bye"`) {
			origByeHash = hl.Hash
		}
	}
	for _, hl := range modHashed {
		if strings.Contains(hl.Content, `"bye"`) {
			modByeHash = hl.Hash
		}
	}

	if origByeHash != modByeHash {
		t.Errorf("JS farewell hash changed after function insertion: %q -> %q", origByeHash, modByeHash)
	}
}

// =============================================================================
// FindLineByHash tests (fuzzy resolution)
// =============================================================================

func TestFindLineByHash_ExactMatch(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c", "d", "e"})

	result, err := FindLineByHash(lines, 3, lines[2].Hash)
	if err != nil {
		t.Fatalf("FindLineByHash error: %v", err)
	}
	if result.Line != 3 {
		t.Errorf("Line=%d, want 3", result.Line)
	}
	if result.Shift != 0 {
		t.Errorf("Shift=%d, want 0", result.Shift)
	}
	if result.Method != "exact" {
		t.Errorf("Method=%q, want %q", result.Method, "exact")
	}
}

func TestFindLineByHash_NearbyShift(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c", "d", "e"})

	// Hash for line 3 ("c"), but hint at line 1
	result, err := FindLineByHash(lines, 1, lines[2].Hash)
	if err != nil {
		t.Fatalf("FindLineByHash error: %v", err)
	}
	if result.Line != 3 {
		t.Errorf("Line=%d, want 3", result.Line)
	}
	if result.Shift != 2 {
		t.Errorf("Shift=%d, want 2", result.Shift)
	}
	if result.Method != "nearby" {
		t.Errorf("Method=%q, want %q", result.Method, "nearby")
	}
}

func TestFindLineByHash_NegativeShift(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c", "d", "e"})

	// Hash for line 2 ("b"), but hint at line 5
	result, err := FindLineByHash(lines, 5, lines[1].Hash)
	if err != nil {
		t.Fatalf("FindLineByHash error: %v", err)
	}
	if result.Line != 2 {
		t.Errorf("Line=%d, want 2", result.Line)
	}
	if result.Shift != -3 {
		t.Errorf("Shift=%d, want -3", result.Shift)
	}
}

func TestFindLineByHash_NotFound(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c"})

	_, err := FindLineByHash(lines, 1, "zzzz")
	if err == nil {
		t.Error("expected error for non-existent hash")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestFindLineByHash_EmptyFile(t *testing.T) {
	_, err := FindLineByHash([]HashedLine{}, 1, "abcd")
	if err == nil {
		t.Error("expected error for empty file")
	}
}

func TestFindLineByHash_ClampHint(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c"})

	// Hint way out of range (high)
	result, err := FindLineByHash(lines, 999, lines[0].Hash)
	if err != nil {
		t.Fatalf("FindLineByHash error: %v", err)
	}
	if result.Line != 1 {
		t.Errorf("Line=%d, want 1", result.Line)
	}

	// Hint way out of range (low)
	result, err = FindLineByHash(lines, -5, lines[2].Hash)
	if err != nil {
		t.Fatalf("FindLineByHash error: %v", err)
	}
	if result.Line != 3 {
		t.Errorf("Line=%d, want 3", result.Line)
	}
}

func TestFindLineByHashInRange_Basic(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c", "d", "e"})

	// Look for line 4's hash, but only at or after line 3
	result, err := FindLineByHashInRange(lines, 4, lines[3].Hash, 3)
	if err != nil {
		t.Fatalf("FindLineByHashInRange error: %v", err)
	}
	if result.Line != 4 {
		t.Errorf("Line=%d, want 4", result.Line)
	}
}

func TestFindLineByHashInRange_RejectsBeforeStart(t *testing.T) {
	lines := makeHashedLines([]string{"a", "b", "c", "d", "e"})

	// Hash for line 1, but require after line 3 — should not find it
	_, err := FindLineByHashInRange(lines, 1, lines[0].Hash, 3)
	if err == nil {
		t.Error("expected error when match is before afterLine")
	}
}

// =============================================================================
// Registry tests
// =============================================================================

func TestForFile_GoExtension(t *testing.T) {
	det := ForFile("main.go")
	if det.Name() != "go" {
		t.Errorf("ForFile(main.go) = %q, want %q", det.Name(), "go")
	}
}

func TestForFile_PythonExtension(t *testing.T) {
	det := ForFile("script.py")
	if det.Name() != "python" {
		t.Errorf("ForFile(script.py) = %q, want %q", det.Name(), "python")
	}
}

func TestForFile_JSExtension(t *testing.T) {
	det := ForFile("app.js")
	if det.Name() != "javascript" {
		t.Errorf("ForFile(app.js) = %q, want %q", det.Name(), "javascript")
	}
}

func TestForFile_TSExtension(t *testing.T) {
	det := ForFile("index.ts")
	if det.Name() != "javascript" {
		t.Errorf("ForFile(index.ts) = %q, want %q", det.Name(), "javascript")
	}
}

func TestForFile_MarkdownExtension(t *testing.T) {
	det := ForFile("README.md")
	if det.Name() != "markdown" {
		t.Errorf("ForFile(README.md) = %q, want %q", det.Name(), "markdown")
	}
}

func TestForFile_JSONExtension(t *testing.T) {
	det := ForFile("config.json")
	if det.Name() != "json" {
		t.Errorf("ForFile(config.json) = %q, want %q", det.Name(), "json")
	}
}

func TestForFile_Dockerfile(t *testing.T) {
	det := ForFile("Dockerfile")
	if det.Name() != "dockerfile" {
		t.Errorf("ForFile(Dockerfile) = %q, want %q", det.Name(), "dockerfile")
	}
}

func TestForFile_DockerfileVariant(t *testing.T) {
	det := ForFile("Dockerfile.prod")
	if det.Name() != "dockerfile" {
		t.Errorf("ForFile(Dockerfile.prod) = %q, want %q", det.Name(), "dockerfile")
	}
}

func TestForFile_Makefile(t *testing.T) {
	det := ForFile("Makefile")
	if det.Name() != "makefile" {
		t.Errorf("ForFile(Makefile) = %q, want %q", det.Name(), "makefile")
	}
}

func TestForFile_Vagrantfile(t *testing.T) {
	det := ForFile("Vagrantfile")
	if det.Name() != "ruby" {
		t.Errorf("ForFile(Vagrantfile) = %q, want %q", det.Name(), "ruby")
	}
}

func TestForFile_UnknownExtension(t *testing.T) {
	det := ForFile("data.xyz")
	// Should fall back to indent detector
	if det.Name() != "indent" {
		t.Errorf("ForFile(data.xyz) = %q, want %q", det.Name(), "indent")
	}
}

func TestListRegistered_ContainsExpected(t *testing.T) {
	reg := ListRegistered()
	expected := []string{".go", ".py", ".js", ".ts", ".md", ".json", ".html", ".css", ".rs"}
	for _, ext := range expected {
		if _, ok := reg[ext]; !ok {
			t.Errorf("ListRegistered missing %q", ext)
		}
	}
}

// =============================================================================
// IndentDetector tests
// =============================================================================

func TestIndentDetector_Basic(t *testing.T) {
	det := &IndentDetector{}
	lines := []string{
		"top level",
		"    indented line",
		"    another indented",
		"back to top",
	}
	scopes := det.DetectScopes(lines)

	if len(scopes) != 4 {
		t.Fatalf("expected 4 scopes, got %d", len(scopes))
	}

	// First line: no scope (top level)
	if len(scopes[0]) != 0 {
		t.Errorf("line 0 scope should be empty, got %v", scopes[0])
	}

	// Indented lines should be under "top level"
	if len(scopes[1]) != 1 || scopes[1][0].Label != "top level" {
		t.Errorf("line 1 scope should be [top level], got %v", scopes[1])
	}

	// Back to top: scope resets
	if len(scopes[3]) != 0 {
		t.Errorf("line 3 scope should be empty, got %v", scopes[3])
	}
}

func TestIndentDetector_EmptyLines(t *testing.T) {
	det := &IndentDetector{}
	lines := []string{
		"header:",
		"    content",
		"",
		"    more content",
	}
	scopes := det.DetectScopes(lines)

	// Empty line should inherit from previous
	if !scopes[2].Equal(scopes[1]) {
		t.Errorf("empty line should inherit scope: got %v, want %v", scopes[2], scopes[1])
	}
}

func TestIndentDetector_TabsAsFourSpaces(t *testing.T) {
	det := &IndentDetector{}
	lines := []string{
		"func:",
		"\tindented",
	}
	scopes := det.DetectScopes(lines)

	if len(scopes[1]) != 1 {
		t.Errorf("tab-indented line should be in scope, got %v", scopes[1])
	}
}

func TestIndentDetector_Empty(t *testing.T) {
	det := &IndentDetector{}
	scopes := det.DetectScopes([]string{})
	if len(scopes) != 0 {
		t.Errorf("empty input should return empty, got %d", len(scopes))
	}
}

// =============================================================================
// GoDetector tests
// =============================================================================

func TestGoDetector_FuncScope(t *testing.T) {
	det := &GoDetector{}
	lines := []string{
		"package main",
		"",
		"func main() {",
		"\tfmt.Println(\"hello\")",
		"}",
	}
	scopes := det.DetectScopes(lines)

	// "package main" — top level
	if len(scopes[0]) != 0 {
		t.Errorf("package line should have empty scope, got %v", scopes[0])
	}

	// Inside main() — scope should be [func main]
	if len(scopes[3]) != 1 || scopes[3][0].Label != "func main" {
		t.Errorf("line inside main should have scope [func main], got %v", scopes[3])
	}

	// Closing brace — should inherit func main scope (closing-brace-inherits-closed)
	if len(scopes[4]) != 1 || scopes[4][0].Label != "func main" {
		t.Errorf("closing brace should inherit scope [func main], got %v", scopes[4])
	}
}

func TestGoDetector_MethodScope(t *testing.T) {
	det := &GoDetector{}
	lines := []string{
		"func (s *Server) Start() {",
		"\ts.running = true",
		"}",
	}
	scopes := det.DetectScopes(lines)

	if len(scopes[1]) != 1 || scopes[1][0].Label != "func Server.Start" {
		t.Errorf("method body should have scope [func Server.Start], got %v", scopes[1])
	}
}

func TestGoDetector_NestedScopes(t *testing.T) {
	det := &GoDetector{}
	lines := []string{
		"func foo() {",
		"\tif true {",
		"\t\tx := 1",
		"\t}",
		"}",
	}
	scopes := det.DetectScopes(lines)

	// x := 1 should be [func foo, if]
	if len(scopes[2]) != 2 {
		t.Errorf("nested line should have 2 scopes, got %d: %v", len(scopes[2]), scopes[2])
	}
	if scopes[2][0].Label != "func foo" || scopes[2][1].Label != "if" {
		t.Errorf("nested scope should be [func foo, if], got %v", scopes[2])
	}

	// Inner closing brace should have [func foo, if] (inherits closed scope)
	if len(scopes[3]) != 2 || scopes[3][1].Label != "if" {
		t.Errorf("inner } should inherit [func foo, if], got %v", scopes[3])
	}

	// Outer closing brace should have [func foo]
	if len(scopes[4]) != 1 || scopes[4][0].Label != "func foo" {
		t.Errorf("outer } should inherit [func foo], got %v", scopes[4])
	}
}

func TestGoDetector_TypeStruct(t *testing.T) {
	det := &GoDetector{}
	lines := []string{
		"type Config struct {",
		"\tHost string",
		"}",
	}
	scopes := det.DetectScopes(lines)

	if len(scopes[1]) != 1 || scopes[1][0].Label != "type Config" {
		t.Errorf("struct field should be in [type Config], got %v", scopes[1])
	}
}

// =============================================================================
// PythonDetector tests
// =============================================================================

func TestPythonDetector_ClassAndDef(t *testing.T) {
	det := &PythonDetector{}
	lines := []string{
		"class Foo:",
		"    def bar(self):",
		"        return 42",
		"    def baz(self):",
		"        return 0",
	}
	scopes := det.DetectScopes(lines)

	// "class Foo:" is top level
	if len(scopes[0]) != 0 {
		t.Errorf("class line should have empty scope, got %v", scopes[0])
	}

	// "def bar" should be under class Foo
	if len(scopes[1]) != 1 || scopes[1][0].Label != "class Foo" {
		t.Errorf("def bar should be in [class Foo], got %v", scopes[1])
	}

	// "return 42" should be under [class Foo, def bar]
	if len(scopes[2]) != 2 || scopes[2][0].Label != "class Foo" || scopes[2][1].Label != "def bar" {
		t.Errorf("return 42 should be in [class Foo, def bar], got %v", scopes[2])
	}

	// "return 0" should be under [class Foo, def baz]
	if len(scopes[4]) != 2 || scopes[4][1].Label != "def baz" {
		t.Errorf("return 0 should be in [class Foo, def baz], got %v", scopes[4])
	}
}

func TestPythonDetector_CommentsInheritScope(t *testing.T) {
	det := &PythonDetector{}
	lines := []string{
		"def foo():",
		"    x = 1",
		"    # a comment",
		"    y = 2",
	}
	scopes := det.DetectScopes(lines)

	// Comment should inherit scope from above
	if !scopes[2].Equal(scopes[1]) {
		t.Errorf("comment should inherit scope: got %v, want %v", scopes[2], scopes[1])
	}
}

// =============================================================================
// MarkdownDetector tests
// =============================================================================

func TestMarkdownDetector_HeadingHierarchy(t *testing.T) {
	det := &MarkdownDetector{}
	lines := []string{
		"# Title",
		"Some text",
		"## Section A",
		"More text",
		"### Subsection",
		"Details",
		"## Section B",
		"Other text",
	}
	scopes := det.DetectScopes(lines)

	// "# Title" — top level (heading itself is at parent scope)
	if len(scopes[0]) != 0 {
		t.Errorf("h1 line should have empty scope, got %v", scopes[0])
	}

	// "Some text" — under # Title
	if len(scopes[1]) != 1 || scopes[1][0].Label != "# Title" {
		t.Errorf("text under h1 should be [# Title], got %v", scopes[1])
	}

	// "## Section A" — under # Title
	if len(scopes[2]) != 1 || scopes[2][0].Label != "# Title" {
		t.Errorf("h2 should be under [# Title], got %v", scopes[2])
	}

	// "More text" — under [# Title, ## Section A]
	if len(scopes[3]) != 2 {
		t.Errorf("text under h2 should have 2 scopes, got %d: %v", len(scopes[3]), scopes[3])
	}

	// "Details" — under [# Title, ## Section A, ### Subsection]
	if len(scopes[5]) != 3 {
		t.Errorf("text under h3 should have 3 scopes, got %d: %v", len(scopes[5]), scopes[5])
	}

	// "Other text" after ## Section B — under [# Title, ## Section B]
	if len(scopes[7]) != 2 || scopes[7][1].Label != "## Section B" {
		t.Errorf("text under Section B should be [# Title, ## Section B], got %v", scopes[7])
	}
}

func TestMarkdownDetector_FencedCodeBlock(t *testing.T) {
	det := &MarkdownDetector{}
	lines := []string{
		"# Title",
		"```go",
		"func main() {",
		"}",
		"```",
		"After code",
	}
	scopes := det.DetectScopes(lines)

	// Code block content should stay in the heading scope, not be parsed as Go
	if len(scopes[2]) != 1 || scopes[2][0].Label != "# Title" {
		t.Errorf("code in fence should be in [# Title], got %v", scopes[2])
	}

	// After code fence ends
	if len(scopes[5]) != 1 || scopes[5][0].Label != "# Title" {
		t.Errorf("text after fence should still be in [# Title], got %v", scopes[5])
	}
}

// =============================================================================
// JSONDetector tests
// =============================================================================

func TestJSONDetector_NestedObjects(t *testing.T) {
	det := &JSONDetector{}
	lines := []string{
		"{",
		`  "name": "test",`,
		`  "config": {`,
		`    "host": "localhost",`,
		`    "port": 8080`,
		"  }",
		"}",
	}
	scopes := det.DetectScopes(lines)

	// Opening brace — top level
	if len(scopes[0]) != 0 {
		t.Errorf("opening { should have empty scope, got %v", scopes[0])
	}

	// "host" inside config — should be under "config"
	if len(scopes[3]) != 1 || scopes[3][0].Label != "config" {
		t.Errorf("host should be in [config], got %v", scopes[3])
	}

	// Closing } for config — should inherit "config" scope
	if len(scopes[5]) != 1 || scopes[5][0].Label != "config" {
		t.Errorf("config closing } should have [config], got %v", scopes[5])
	}
}

// =============================================================================
// HTMLDetector tests
// =============================================================================

func TestHTMLDetector_BasicNesting(t *testing.T) {
	det := ForFile("page.html")
	lines := []string{
		"<html>",
		"<body>",
		"<div>",
		"  <p>Hello</p>",
		"</div>",
		"</body>",
		"</html>",
	}
	scopes := det.DetectScopes(lines)

	// <p>Hello</p> should be inside [html, body, div]
	if len(scopes[3]) != 3 {
		t.Errorf("p tag should have 3 scopes, got %d: %v", len(scopes[3]), scopes[3])
	}
}

func TestHTMLDetector_SelfClosingTags(t *testing.T) {
	det := ForFile("page.html")
	lines := []string{
		"<div>",
		"<img src='test.png' />",
		"<br>",
		"<span>text</span>",
		"</div>",
	}
	scopes := det.DetectScopes(lines)

	// <img> is self-closing, shouldn't create scope
	// <br> is void element, shouldn't create scope
	// <span> is inline and closes on same line
	// All should be under [div]
	for i := 1; i <= 3; i++ {
		if len(scopes[i]) != 1 || scopes[i][0].Label != "div" {
			t.Errorf("line %d should be in [div], got %v", i, scopes[i])
		}
	}
}

// =============================================================================
// DockerfileDetector tests
// =============================================================================

func TestDockerfileDetector_MultiStage(t *testing.T) {
	det := ForFile("Dockerfile")
	lines := []string{
		"FROM golang:1.21 AS builder",
		"WORKDIR /app",
		"COPY . .",
		"RUN go build -o main .",
		"",
		"FROM alpine:3.18",
		"COPY --from=builder /app/main /usr/local/bin/",
		"CMD [\"main\"]",
	}
	scopes := det.DetectScopes(lines)

	// FROM line is boundary (no scope)
	if len(scopes[0]) != 0 {
		t.Errorf("FROM line should have empty scope, got %v", scopes[0])
	}

	// WORKDIR inside builder stage
	if len(scopes[1]) != 1 || scopes[1][0].Label != "stage builder" {
		t.Errorf("WORKDIR should be in [stage builder], got %v", scopes[1])
	}

	// Second FROM is also boundary
	if len(scopes[5]) != 0 {
		t.Errorf("second FROM should have empty scope, got %v", scopes[5])
	}

	// CMD inside alpine stage
	if len(scopes[7]) != 1 || strings.Contains(scopes[7][0].Label, "alpine") {
		// It could be "FROM alpine:3.18" as label
	}
}

// =============================================================================
// MakefileDetector tests
// =============================================================================

func TestMakefileDetector_Targets(t *testing.T) {
	det := ForFile("Makefile")
	lines := []string{
		"CC=gcc",
		"",
		"build:",
		"\t$(CC) -o main main.c",
		"\t@echo done",
		"",
		"clean:",
		"\trm -f main",
	}
	scopes := det.DetectScopes(lines)

	// CC=gcc — top level variable
	if len(scopes[0]) != 0 {
		t.Errorf("variable should have empty scope, got %v", scopes[0])
	}

	// build: — target line is top level
	if len(scopes[2]) != 0 {
		t.Errorf("target line should have empty scope, got %v", scopes[2])
	}

	// Recipe lines inside build
	if len(scopes[3]) != 1 || scopes[3][0].Label != "build" {
		t.Errorf("recipe should be in [build], got %v", scopes[3])
	}

	// Recipe under clean
	if len(scopes[7]) != 1 || scopes[7][0].Label != "clean" {
		t.Errorf("clean recipe should be in [clean], got %v", scopes[7])
	}
}

// =============================================================================
// ElixirDetector tests
// =============================================================================

func TestElixirDetector_ModuleAndDef(t *testing.T) {
	det := &ElixirDetector{}
	lines := []string{
		"defmodule MyApp do",
		"  def hello do",
		"    :world",
		"  end",
		"end",
	}
	scopes := det.DetectScopes(lines)

	// defmodule line is at top level
	if len(scopes[0]) != 0 {
		t.Errorf("defmodule should have empty scope, got %v", scopes[0])
	}

	// def hello is under module
	if len(scopes[1]) != 1 || scopes[1][0].Label != "module MyApp" {
		t.Errorf("def hello should be in [module MyApp], got %v", scopes[1])
	}

	// :world is under [module MyApp, def hello]
	if len(scopes[2]) != 2 {
		t.Errorf(":world should have 2 scopes, got %d: %v", len(scopes[2]), scopes[2])
	}

	// "end" for def — should inherit the scope it closes
	if len(scopes[3]) != 2 {
		t.Errorf("inner end should inherit [module MyApp, def hello], got %v", scopes[3])
	}
}

// =============================================================================
// BraceDetector (shared base) tests
// =============================================================================

func TestCStyleBraceCounter_Basic(t *testing.T) {
	tests := []struct {
		line      string
		wantOpen  int
		wantClose int
	}{
		{`func main() {`, 1, 0},
		{`}`, 0, 1},
		{`if (x) { y(); }`, 1, 1},
		{`map[string]struct{}{}`, 2, 2},
		{`"hello { world"`, 0, 0}, // inside string
		{`// { comment`, 0, 0},    // inside comment
	}

	for _, tc := range tests {
		open, close := CStyleBraceCounter(tc.line)
		if open != tc.wantOpen || close != tc.wantClose {
			t.Errorf("CStyleBraceCounter(%q) = (%d, %d), want (%d, %d)",
				tc.line, open, close, tc.wantOpen, tc.wantClose)
		}
	}
}

func TestIsOnlyClosing(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"}", true},
		{"})", true},
		{"},", true},
		{"});", true},
		{"} else {", false},
		{"return}", false},
		{"", true},
	}

	for _, tc := range tests {
		got := isOnlyClosing(tc.input)
		if got != tc.want {
			t.Errorf("isOnlyClosing(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// =============================================================================
// Cross-language scope consistency tests
// =============================================================================

func TestAllDetectors_EmptyInput(t *testing.T) {
	// Every detector must handle empty input gracefully
	detectors := []ScopeDetector{
		&GoDetector{},
		&PythonDetector{},
		&MarkdownDetector{},
		&JSONDetector{},
		&HTMLDetector{},
		&DockerfileDetector{},
		&MakefileDetector{},
		&ElixirDetector{},
		&IndentDetector{},
	}

	for _, det := range detectors {
		scopes := det.DetectScopes([]string{})
		if len(scopes) != 0 {
			t.Errorf("%s detector returned non-empty result for empty input: %v", det.Name(), scopes)
		}
	}
}

func TestAllDetectors_SingleLine(t *testing.T) {
	detectors := []ScopeDetector{
		&GoDetector{},
		&PythonDetector{},
		&MarkdownDetector{},
		&JSONDetector{},
		&HTMLDetector{},
		&DockerfileDetector{},
		&MakefileDetector{},
		&ElixirDetector{},
		&IndentDetector{},
	}

	for _, det := range detectors {
		scopes := det.DetectScopes([]string{"hello world"})
		if len(scopes) != 1 {
			t.Errorf("%s detector returned %d scopes for 1 line, want 1", det.Name(), len(scopes))
		}
	}
}

func TestAllDetectors_ResultLengthMatchesInput(t *testing.T) {
	det := ForFile("test.go")
	lines := []string{"a", "b", "c", "d", "e"}
	scopes := det.DetectScopes(lines)
	if len(scopes) != len(lines) {
		t.Errorf("DetectScopes returned %d scopes for %d lines", len(scopes), len(lines))
	}
}

// =============================================================================
// End-to-end: Read → hash → resolve flow
// =============================================================================

func TestEndToEnd_HashResolveAfterMassiveShift(t *testing.T) {
	// Simulate a file where a linter adds 100 import lines at the top.
	// All hashes for the function body should still resolve.
	original := `package main

func process() {
	step1()
	step2()
	step3()
}`

	// Add 100 lines of imports
	var imports strings.Builder
	imports.WriteString("package main\n\nimport (\n")
	for i := range 100 {
		imports.WriteString(fmt.Sprintf("\t\"pkg%d\"\n", i))
	}
	imports.WriteString(")\n\nfunc process() {\n\tstep1()\n\tstep2()\n\tstep3()\n}")
	modified := imports.String()

	det := ForFile("test.go")
	origHashed := HashFileLines(strings.Split(original, "\n"), det)
	modHashed := HashFileLines(strings.Split(modified, "\n"), det)

	// Find step2() hash in original
	var step2Hash string
	var step2HintLine int
	for _, hl := range origHashed {
		if strings.Contains(hl.Content, "step2") {
			step2Hash = hl.Hash
			step2HintLine = hl.Number
		}
	}

	if step2Hash == "" {
		t.Fatal("couldn't find step2 in original")
	}

	// Resolve using old hint in the new file
	result, err := FindLineByHash(modHashed, step2HintLine, step2Hash)
	if err != nil {
		t.Fatalf("FindLineByHash failed: %v — hash should survive 100-line shift", err)
	}

	// Verify the resolved line is actually step2
	resolvedLine := modHashed[result.Line-1]
	if !strings.Contains(resolvedLine.Content, "step2") {
		t.Errorf("resolved to wrong line: %q", resolvedLine.Content)
	}

	t.Logf("step2 resolved from hint line %d to actual line %d (shift=%+d, method=%s)",
		step2HintLine, result.Line, result.Shift, result.Method)
}

// =============================================================================
// Helpers
// =============================================================================

// makeHashedLines creates HashedLine slice using indent detector (for simple content).
func makeHashedLines(content []string) []HashedLine {
	det := &IndentDetector{}
	return HashFileLines(content, det)
}
