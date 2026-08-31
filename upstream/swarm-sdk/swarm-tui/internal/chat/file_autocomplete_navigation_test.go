package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMentionAutocomplete_NavigationBehavior(t *testing.T) {
	// Create a test directory structure
	tmpDir, err := os.MkdirTemp("", "nav-behavior-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test structure with multiple files in a directory
	docsDir := filepath.Join(tmpDir, "docs")
	os.Mkdir(docsDir, 0755)
	os.WriteFile(filepath.Join(docsDir, "readme.md"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(docsDir, "api.md"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(docsDir, "guide.md"), []byte("test"), 0644)

	srcDir := filepath.Join(tmpDir, "src")
	os.Mkdir(srcDir, 0755)
	os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("test"), 0644)
	os.WriteFile(filepath.Join(srcDir, "utils.go"), []byte("test"), 0644)

	ma := NewMentionAutocomplete(tmpDir)

	t.Run("Tab completes file with absolute path", func(t *testing.T) {
		ma.SetInput("@docs/r")
		if !ma.IsVisible() {
			t.Error("Expected autocomplete to be visible")
		}

		// Should match readme.md
		if len(ma.matches) != 1 || ma.matches[0].Name != "readme.md" {
			t.Errorf("Expected readme.md match, got %d matches", len(ma.matches))
		}

		// Tab should complete with absolute path + space
		completed := ma.CompleteSelection()
		expectedCompleted := "@file:" + filepath.Join(tmpDir, "docs/readme.md") + " "
		if completed != expectedCompleted {
			t.Errorf("Expected '%s', got '%s'", expectedCompleted, completed)
		}
	})

	t.Run("Right arrow does nothing for files", func(t *testing.T) {
		ma.SetInput("@docs/r")

		// Right arrow on a file should return false
		navigated, newValue := ma.NavigateInto()
		if navigated {
			t.Error("NavigateInto should return false for files")
		}
		if newValue != "@docs/r" {
			t.Errorf("NavigateInto should return unchanged input, got '%s'", newValue)
		}
	})

	t.Run("Right arrow enters directories", func(t *testing.T) {
		ma.SetInput("@")

		// Select docs directory (should be first due to sorting)
		if len(ma.matches) < 2 || !ma.matches[0].IsDir {
			t.Fatal("Expected docs directory as first match")
		}

		// Right arrow should enter directory
		navigated, newValue := ma.NavigateInto()
		if !navigated {
			t.Error("NavigateInto should return true for directories")
		}
		if newValue != "@docs/" {
			t.Errorf("Expected '@docs/', got '%s'", newValue)
		}

		// Update with the new value to see contents
		ma.SetInput(newValue)
		if len(ma.matches) != 3 {
			t.Errorf("Expected 3 files in docs/, got %d", len(ma.matches))
		}
	})

	t.Run("Left arrow navigates up", func(t *testing.T) {
		ma.SetInput("@docs/")

		// Left arrow should go back to root
		navigated, newValue := ma.NavigateOut()
		if !navigated {
			t.Error("NavigateOut should return true when in subdirectory")
		}
		if newValue != "@" {
			t.Errorf("Expected '@', got '%s'", newValue)
		}
	})

	t.Run("Tab on directory adds slash", func(t *testing.T) {
		ma.SetInput("@d")

		// Should match docs directory
		if len(ma.matches) != 1 || !ma.matches[0].IsDir {
			t.Error("Expected docs directory match")
		}

		// Tab should complete with slash
		completed := ma.CompleteSelection()
		expectedCompleted := "@file:" + filepath.Join(tmpDir, "docs") + "/"
		if completed != expectedCompleted {
			t.Errorf("Expected '%s', got '%s'", expectedCompleted, completed)
		}
	})
}
