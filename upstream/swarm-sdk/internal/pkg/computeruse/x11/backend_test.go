package x11

import (
	"testing"
)

func TestNewBackend(t *testing.T) {
	// This test requires a running X11 session
	backend, err := NewBackend(DefaultOptions())
	if err != nil {
		t.Skipf("X11 backend not available: %v", err)
	}
	defer backend.Close()

	t.Run("ListDisplays", func(t *testing.T) {
		displays, err := backend.ListDisplays()
		if err != nil {
			t.Fatalf("ListDisplays failed: %v", err)
		}
		if len(displays) == 0 {
			t.Fatal("No displays found")
		}
		t.Logf("Found %d displays", len(displays))
		for _, d := range displays {
			t.Logf("  Display %d: %s %dx%d at (%d,%d) primary=%v",
				d.ID, d.Name, d.Width, d.Height, d.X, d.Y, d.Primary)
		}
	})

	t.Run("GetDisplaySize", func(t *testing.T) {
		geom, err := backend.GetDisplaySize(0)
		if err != nil {
			t.Fatalf("GetDisplaySize failed: %v", err)
		}
		t.Logf("Primary display: %dx%d", geom.Width, geom.Height)
	})

	t.Run("GetCursorPosition", func(t *testing.T) {
		pos, err := backend.GetCursorPosition()
		if err != nil {
			t.Fatalf("GetCursorPosition failed: %v", err)
		}
		t.Logf("Cursor position: (%d, %d)", pos.X, pos.Y)
	})

	t.Run("GetFrontmostApp", func(t *testing.T) {
		app, err := backend.GetFrontmostApp()
		if err != nil {
			t.Fatalf("GetFrontmostApp failed: %v", err)
		}
		t.Logf("Frontmost app: %s (%s)", app.DisplayName, app.ID)
	})
}
