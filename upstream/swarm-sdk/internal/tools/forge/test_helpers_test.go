package forge

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a file with the given name and content in dir.
// Shared test helper used across multiple test files.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
