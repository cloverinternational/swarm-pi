package context

import (
	stdctx "context"
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoriesBetween(t *testing.T) {
	tests := []struct {
		name     string
		root     string
		target   string
		expected []string
	}{
		{
			name:     "same directory",
			root:     "/home/user/project",
			target:   "/home/user/project",
			expected: []string{"/home/user/project"},
		},
		{
			name:   "one level deep",
			root:   "/home/user/project",
			target: "/home/user/project/swarm-sdk",
			expected: []string{
				"/home/user/project",
				"/home/user/project/swarm-sdk",
			},
		},
		{
			name:   "two levels deep",
			root:   "/home/user/project",
			target: "/home/user/project/swarm-sdk/client",
			expected: []string{
				"/home/user/project",
				"/home/user/project/swarm-sdk",
				"/home/user/project/swarm-sdk/client",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := directoriesBetween(tt.root, tt.target)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d dirs, got %d: %v", len(tt.expected), len(result), result)
			}
			for i, dir := range result {
				if dir != tt.expected[i] {
					t.Errorf("dir[%d]: expected %q, got %q", i, tt.expected[i], dir)
				}
			}
		})
	}
}

func TestRegisterFileRead(t *testing.T) {
	tmpDir := t.TempDir()
	orch := NewContextOrchestrator(ContextConfig{}, tmpDir, nil, nil, nil)

	// Register a file read in a subdirectory
	orch.RegisterFileRead(filepath.Join(tmpDir, "swarm-sdk", "client", "client.go"))

	if len(orch.nestedIndexDirs) != 1 {
		t.Fatalf("expected 1 registered dir, got %d", len(orch.nestedIndexDirs))
	}

	expectedDir := filepath.Join(tmpDir, "swarm-sdk", "client")
	if !orch.nestedIndexDirs[expectedDir] {
		t.Errorf("expected dir %q to be registered", expectedDir)
	}
}

func TestRegisterFileReadOutsideWorkspace(t *testing.T) {
	tmpDir := t.TempDir()
	orch := NewContextOrchestrator(ContextConfig{}, tmpDir, nil, nil, nil)

	// Register a file read outside the workspace - should be ignored
	orch.RegisterFileRead("/etc/passwd")

	if len(orch.nestedIndexDirs) != 0 {
		t.Fatalf("expected 0 registered dirs (outside workspace), got %d", len(orch.nestedIndexDirs))
	}
}

func TestGetNestedIndexMdContent(t *testing.T) {
	tmpDir := t.TempDir()

	// Create INDEX.md in a subdirectory
	subDir := filepath.Join(tmpDir, "swarm-sdk")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(subDir, "INDEX.md")
	indexContent := "# swarm-sdk\n\nClient library for Swarm."
	if err := os.WriteFile(indexPath, []byte(indexContent), 0644); err != nil {
		t.Fatal(err)
	}

	orch := NewContextOrchestrator(ContextConfig{}, tmpDir, nil, nil, nil)

	// Register a file read in the subdirectory
	orch.RegisterFileRead(filepath.Join(subDir, "client", "client.go"))

	// Get nested INDEX.md content
	content := orch.getNestedIndexMdContent()
	if content == "" {
		t.Fatal("expected non-empty content from getNestedIndexMdContent")
	}

	// Should contain the index content
	if !contains(content, indexContent) {
		t.Errorf("expected content to contain %q, got: %s", indexContent, content)
	}

	// Should contain the path
	if !contains(content, indexPath) {
		t.Errorf("expected content to contain path %q", indexPath)
	}

	// Triggers should be cleared after processing
	if len(orch.nestedIndexDirs) != 0 {
		t.Errorf("expected triggers to be cleared, got %d", len(orch.nestedIndexDirs))
	}

	// Directory should be marked as injected
	if !orch.injectedIndexDirs[subDir] {
		t.Error("expected subDir to be marked as injected")
	}
}

func TestGetNestedIndexMdContentDedup(t *testing.T) {
	tmpDir := t.TempDir()

	// Create INDEX.md in a subdirectory
	subDir := filepath.Join(tmpDir, "swarm-sdk")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(subDir, "INDEX.md")
	if err := os.WriteFile(indexPath, []byte("# sdk\n"), 0644); err != nil {
		t.Fatal(err)
	}

	orch := NewContextOrchestrator(ContextConfig{}, tmpDir, nil, nil, nil)

	// First read
	orch.RegisterFileRead(filepath.Join(subDir, "client.go"))
	content1 := orch.getNestedIndexMdContent()
	if content1 == "" {
		t.Fatal("expected content on first read")
	}

	// Second read - same directory, should be deduped
	orch.RegisterFileRead(filepath.Join(subDir, "other.go"))
	content2 := orch.getNestedIndexMdContent()
	if content2 != "" {
		t.Error("expected empty content on second read (dedup)")
	}
}

func TestDisabledIndexSourceSuppressesNestedIndexInjection(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "pkg")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	const secret = "ambient nested project instructions"
	if err := os.WriteFile(filepath.Join(subDir, "INDEX.md"), []byte(secret), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	if source, idx, ok := FindSourceByID(cfg.Sources, SourceIDIndexMd); ok {
		source.Enabled = false
		cfg.Sources[idx] = source
	}
	loader := &stubConfigLoader{config: cfg}
	orch := NewContextOrchestrator(cfg, tmpDir, loader, nil, nil)
	orch.RegisterFileRead(filepath.Join(subDir, "file.go"))

	block := orch.GetContextBlock(stdctx.Background(), ContextBlockOptions{})
	if contains(block, secret) {
		t.Fatalf("disabled INDEX.md source leaked nested memory:\n%s", block)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
