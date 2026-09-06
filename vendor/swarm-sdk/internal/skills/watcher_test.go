package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcher_NewWatcher(t *testing.T) {
	registry := NewRegistry()
	loader := &Loader{
		Registry:    registry,
		InstallDir:  t.TempDir(),
		SearchPaths: []string{},
	}

	watcher, err := NewWatcher(loader, func() {})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	watcher.Stop()
}

func TestWatcher_StartStop(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Create a minimal SKILL.md
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: test\n---\nTest"), 0644); err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	loader := &Loader{
		Registry:    registry,
		InstallDir:  tmpDir,
		SearchPaths: []string{tmpDir},
	}

	onChangeCalled := false
	watcher, err := NewWatcher(loader, func() {
		onChangeCalled = true
	})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}

	if err := watcher.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Modify the SKILL.md to trigger a reload
	time.Sleep(100 * time.Millisecond) // Let watcher settle
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: test-skill-v2\ndescription: test v2\n---\nTest v2"), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce + reload
	time.Sleep(500 * time.Millisecond)

	watcher.Stop()

	// The onChange callback should have been called
	if !onChangeCalled {
		t.Error("onChange callback was not called after SKILL.md change")
	}
}

func TestWatcher_StopWithoutStart(t *testing.T) {
	registry := NewRegistry()
	loader := &Loader{
		Registry:    registry,
		InstallDir:  t.TempDir(),
		SearchPaths: []string{},
	}

	watcher, err := NewWatcher(loader, func() {})
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	// Stop without Start should not panic
	watcher.Stop()
}
