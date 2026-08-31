package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMentionAutocomplete_TriggerDetection(t *testing.T) {
	// Create a temp directory with some test files so visibility can be true
	tmpDir, err := os.MkdirTemp("", "trigger-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files that will match various patterns
	os.WriteFile(filepath.Join(tmpDir, "src"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "test"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "one"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "two"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "example"), []byte("test"), 0644)

	ma := NewMentionAutocomplete(tmpDir)

	tests := []struct {
		name        string
		input       string
		wantVisible bool
		wantTrigger int
	}{
		{"empty input", "", false, -1},
		{"no @", "hello world", false, -1},
		{"@ at start", "@", true, 0},
		{"@ with text", "@src", true, 0},
		{"@ in middle", "look at @src", true, 8},
		{"@ after space", "file @test", true, 5},
		{"email-like (no space before @)", "test@example", false, -1},
		{"multiple @ uses last", "first @one then @two", true, 16},
		{"@ with space after (completed)", "@src ", false, 0}, // trigger still detected but not visible
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ma.SetInput(tt.input)
			if ma.IsVisible() != tt.wantVisible {
				t.Errorf("IsVisible() = %v, want %v", ma.IsVisible(), tt.wantVisible)
			}
			// Only check trigger position if we expect a trigger
			if tt.wantTrigger >= 0 {
				if ma.TriggerPosition() != tt.wantTrigger {
					t.Errorf("TriggerPosition() = %v, want %v", ma.TriggerPosition(), tt.wantTrigger)
				}
			}
		})
	}
}

func TestMentionAutocomplete_DirectoryListing(t *testing.T) {
	// Create a temp directory with some test files
	tmpDir, err := os.MkdirTemp("", "file-autocomplete-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test structure
	os.Mkdir(filepath.Join(tmpDir, "src"), 0755)
	os.Mkdir(filepath.Join(tmpDir, "docs"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "src", "app.go"), []byte("test"), 0644)

	ma := NewMentionAutocomplete(tmpDir)

	// Test: @ shows root directory contents (in unified mode, files are limited to 5)
	ma.SetInput("@")
	if !ma.IsVisible() {
		t.Error("Expected autocomplete to be visible for @")
	}
	// In unified mode with no providers, only file matches are shown
	matches := ma.matches
	if len(matches) != 4 { // 2 dirs + 2 files (no hidden files)
		t.Errorf("Expected 4 matches, got %d", len(matches))
	}

	// Verify directories come first
	if len(matches) >= 2 {
		if !matches[0].IsDir || !matches[1].IsDir {
			t.Error("Expected directories to be listed first")
		}
	}

	// Test: @s ranks 'src' first. Matching is fuzzy (exact > prefix > subsequence),
	// so 's' best-matches the 'src' prefix but may also weakly match other names
	// containing 's' (e.g. 'docs'); the invariant is that 'src' is the top result.
	ma.SetInput("@s")
	if len(ma.matches) == 0 || ma.matches[0].Name != "src" {
		got := "<none>"
		if len(ma.matches) > 0 {
			got = ma.matches[0].Name
		}
		t.Errorf("Expected 'src' as top match for '@s', got first=%q (%d matches)", got, len(ma.matches))
	}

	// Test: @src/ lists contents of src directory (file-only mode)
	ma.SetInput("@src/")
	if len(ma.matches) != 1 || ma.matches[0].Name != "app.go" {
		t.Errorf("Expected 1 match for app.go, got %d matches", len(ma.matches))
	}
}

func TestMentionAutocomplete_BoundsCheck(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "bounds-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create subdirectory
	os.Mkdir(filepath.Join(tmpDir, "subdir"), 0755)

	ma := NewMentionAutocomplete(tmpDir)

	// Test: Cannot navigate outside workspace with ..
	ma.SetInput("@../")
	if ma.IsVisible() {
		t.Error("Should not show files outside workspace root")
	}

	// Test: Cannot access parent of parent
	ma.SetInput("@subdir/../../")
	if ma.IsVisible() {
		t.Error("Should not allow escaping workspace via nested ..")
	}
}

func TestMentionAutocomplete_SelectedPath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "selected-path-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	os.Mkdir(filepath.Join(tmpDir, "src"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "file.txt"), []byte("test"), 0644)

	ma := NewMentionAutocomplete(tmpDir)

	// Test: Directory selection adds trailing slash (use file-only mode with /)
	ma.SetInput("@s")
	path := ma.SelectedPath()
	expectedDir := "@file:" + filepath.Join(tmpDir, "src") + "/"
	if path != expectedDir {
		t.Errorf("Expected %s for directory, got %s", expectedDir, path)
	}

	// Test: File selection adds trailing space with absolute path
	ma.SetInput("@f")
	path = ma.SelectedPath()
	expectedPath := "@file:" + filepath.Join(tmpDir, "file.txt") + " "
	if path != expectedPath {
		t.Errorf("Expected '%s' for file, got %s", expectedPath, path)
	}
}

func TestMentionAutocomplete_Navigation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "navigation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create multiple files
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "c.txt"), []byte("test"), 0644)

	ma := NewMentionAutocomplete(tmpDir)
	ma.SetInput("@")

	// Initial selection should be 0
	entry, ok := ma.GetSelectedEntry()
	if !ok {
		t.Fatal("Expected to get selected entry")
	}
	if entry.Name != "a.txt" {
		t.Errorf("Expected first item to be a.txt, got %s", entry.Name)
	}

	// Navigate down
	ma.selectedIdx = 1
	entry, _ = ma.GetSelectedEntry()
	if entry.Name != "b.txt" {
		t.Errorf("Expected second item to be b.txt, got %s", entry.Name)
	}
}

func TestMentionAutocomplete_SubdirectoryNavigation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "subdir-nav-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create nested structure
	os.MkdirAll(filepath.Join(tmpDir, "internal", "chat"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "internal", "chat", "app.go"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "internal", "chat", "modal.go"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "internal", "utils.go"), []byte("test"), 0644)

	ma := NewMentionAutocomplete(tmpDir)

	// Navigate to internal/ (file-only mode since contains /)
	ma.SetInput("@internal/")
	if !ma.IsVisible() {
		t.Error("Expected autocomplete to be visible")
	}
	if len(ma.matches) != 2 { // chat dir + utils.go
		t.Errorf("Expected 2 matches in internal/, got %d", len(ma.matches))
	}

	// Navigate deeper to internal/chat/
	ma.SetInput("@internal/chat/")
	if len(ma.matches) != 2 { // app.go + modal.go
		t.Errorf("Expected 2 matches in internal/chat/, got %d", len(ma.matches))
	}

	// Filter within subdirectory. Fuzzy matching ranks the prefix match first
	// ('a' → app.go) but may also weakly match 'modal.go' (subsequence); assert
	// app.go is the top result.
	ma.SetInput("@internal/chat/a")
	if len(ma.matches) == 0 || ma.matches[0].Name != "app.go" {
		got := "<none>"
		if len(ma.matches) > 0 {
			got = ma.matches[0].Name
		}
		t.Errorf("Expected app.go as top match, got first=%q (%d matches)", got, len(ma.matches))
	}
}
